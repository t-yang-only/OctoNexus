package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// T-pool-002 官方账号号池转发（凭据侧物化）。
//
// 口径与 docs/requirements/2026-09-14-pool-002预研.md §1 一致：
//   - 每服务商一个 Channel 作为号池入口；
//   - 一个官方 OAuth 授权 = 该渠道下一条 ChannelKey，Key 即解密后的 access token；
//   - 账号可用性 = 该 ChannelKey 的 Enabled（失活只停用凭据，不删账号行，便于人工重授权）。
//
// 不新增模型表：模型与协议位由既有「拉取模型 + 三协议实测」流程产出——那是唯一能给出真实协议位的路径，
// 本文件只负责凭据侧的物化与刷新，因此不改选路、不动协议转换。
// 官方账号的登录态会过期，而号池要靠凭据长跑：临期即用刷新凭据换新 token 落库，
// 否则账号会在号池里静默失效，只能等上游 401 才发现。

const (
	// officialPoolRefreshSkew 是提前刷新的余量：临到期前先换，避免转发正好撞在过期点上。
	officialPoolRefreshSkew = 5 * time.Minute
	// officialPoolNamePrefix 号池渠道名前缀，人工在渠道页一眼可辨。
	officialPoolNamePrefix = "官方账号池-"
)

// OfficialPoolChannelName 返回服务商对应的号池渠道名；渠道名在库内唯一，故它就是映射的定位键。
func OfficialPoolChannelName(provider model.OfficialAccountProvider) string {
	return officialPoolNamePrefix + string(provider)
}

// IsOfficialPoolChannelName 判断渠道名是否属于官方账号池自动物化的渠道。
//
// 用途：号池扩展层要列出"渠道凭据"时得把这类渠道排除掉——它们已经由 official 适配器
// 以「账号」的口径列过一遍了，再按渠道凭据列一次就是同一条凭据两个身份。
// 前缀由这里单一维护，避免别处复制字符串后跟着改名漂移。
func IsOfficialPoolChannelName(name string) bool {
	return strings.HasPrefix(name, officialPoolNamePrefix)
}

// OfficialPoolSync 把某服务商的官方账号物化成号池渠道凭据，并刷新临期 token。
// 幂等：重复同步不产生重复凭据；账号侧的新增、失活、重新授权都在下一次同步里收敛。
// 单条账号的凭据问题（解密失败、无刷新凭据）只记入 Notes 并跳过，不中断其余账号，
// 更不覆盖既有凭据——宁可用旧凭据继续跑，也不把可用凭据擦掉。
func OfficialPoolSync(conn *gorm.DB, provider model.OfficialAccountProvider) (model.OfficialPoolSyncResult, error) {
	result := model.OfficialPoolSyncResult{Provider: provider, ChannelName: OfficialPoolChannelName(provider)}
	if err := model.ValidateOfficialAccountProvider(provider); err != nil {
		return result, err
	}
	target := officialAccountDB(conn)

	accounts, err := officialPoolAccounts(target, provider)
	if err != nil {
		return result, err
	}
	if len(accounts) == 0 {
		// 无账号就没有号池可言：不建空渠道，免得渠道页多出一个永远转不出去的池子。
		result.Notes = append(result.Notes, "该服务商尚无官方账号，未建立号池渠道")
		return result, nil
	}

	channel, err := officialPoolEnsureChannel(target, provider)
	if err != nil {
		return result, err
	}
	result.ChannelID = channel.ID

	// 这里刻意用「先取行、再解密」两步，而不是 officialPoolKeys：
	// 密钥轮换/credential.key 被替换后既有凭据解不开时，既比较不了"内容是否变化"，也无法判断
	// 哪一行才是既有凭据 —— 继续硬写只会在同名列旁边再造一份。此时按池同步的契约降级为
	// 「如实记原因 + 一行都不改」，而不是让整轮同步失败。
	keys, err := officialPoolRawKeys(target, channel.ID)
	if err != nil {
		return result, err
	}
	if err := DecryptChannelKeyRows(keys); err != nil {
		result.Notes = append(result.Notes, fmt.Sprintf(
			"号池既有凭据解不开（密钥与加密时不一致，或 credential.key 被替换）：%v；本次未修改任何凭据，请先恢复密钥或重新授权", err))
		return result, nil
	}
	byName := make(map[string]model.ChannelKey, len(keys))
	for _, key := range keys {
		byName[key.Name] = key
	}

	for _, account := range accounts {
		if account.Status != model.OfficialAccountStatusActive {
			// 非 active（pending/expired/revoked/error）的账号不参与转发，凭据停用而非删除：
			// 保留下行内统计与人工识别用的名称，重新授权后同一条凭据会被重新启用。
			if key, ok := byName[account.ExternalName]; ok && key.Enabled {
				if err := target.Model(&model.ChannelKey{}).Where("id = ?", key.ID).
					Update("enabled", false).Error; err != nil {
					return result, fmt.Errorf("disable key %q: %w", key.Name, err)
				}
				result.Disabled++
			}
			continue
		}

		refreshed := false
		if account, refreshed, err = officialPoolRefreshIfNeeded(target, account); err != nil {
			result.Notes = append(result.Notes, fmt.Sprintf("账号 %s token 刷新失败：%v", account.ExternalName, err))
		}
		if refreshed {
			result.Refreshed++
		}

		access, err := officialDecrypt(account.Provider, account.AccessCipher)
		if err != nil {
			result.Notes = append(result.Notes, fmt.Sprintf(
				"账号 %s 凭据解密失败，检查凭据加密密钥是否与录入时一致（OCTOPUS_OFFICIAL_KEY 或数据目录 credential.key）：%v", account.ExternalName, err))
			continue
		}
		if strings.TrimSpace(access) == "" {
			result.Notes = append(result.Notes, fmt.Sprintf("账号 %s 无访问凭据，需重新授权", account.ExternalName))
			continue
		}

		if key, ok := byName[account.ExternalName]; ok {
			// 逐列点名更新：只写会变的两列，凭据行的统计列不在同步的职责范围内。
			updates := map[string]any{}
			if key.Key != access {
				updates["key"] = sealChannelKeyForStore(access)
			}
			if key.OperatorDisabled {
				// 人工停用是操作者的决定，不是账号的当前状态：同步只补凭据内容，不把 enabled 改回来，
				// 否则"停掉一条坏凭据"会在下一次同步时被静默撤销。
				result.Held++
			} else if !key.Enabled {
				updates["enabled"] = true
			}
			if len(updates) == 0 {
				continue
			}
			if err := target.Model(&model.ChannelKey{}).Where("id = ?", key.ID).Updates(updates).Error; err != nil {
				return result, fmt.Errorf("update key %q: %w", key.Name, err)
			}
			continue
		}

		key := model.ChannelKey{
			ChannelID:        channel.ID,
			ChannelKeyConfig: model.ChannelKeyConfig{Name: account.ExternalName, Key: sealChannelKeyForStore(access), Enabled: true},
		}
		if err := target.Create(&key).Error; err != nil {
			return result, fmt.Errorf("create key %q: %w", account.ExternalName, err)
		}
	}

	keys, err = officialPoolKeys(target, channel.ID)
	if err != nil {
		return result, err
	}
	for _, key := range keys {
		if key.Enabled {
			result.Keys++
		}
	}
	if result.Models, err = officialPoolModelCount(target, channel.ID); err != nil {
		return result, err
	}
	if result.Grants, err = officialPoolGrantCount(target, channel.ID); err != nil {
		return result, err
	}

	// 凭据改了就要让转发侧看到：渠道, 凭据, 模型, 授权四类缓存整体重载。
	// 授权没动，故不刷分组缓存。
	if err := channelRefreshCache(context.Background()); err != nil {
		return result, fmt.Errorf("refresh channel cache: %w", err)
	}
	return result, nil
}

// OfficialPoolStatusList 返回三类服务商号池的只读映射快照，供界面核对账号与凭据的对应关系。
func OfficialPoolStatusList(conn *gorm.DB) ([]model.OfficialPoolStatus, error) {
	target := officialAccountDB(conn)
	providers := []model.OfficialAccountProvider{
		model.OfficialAccountProviderOpenAI,
		model.OfficialAccountProviderGemini,
		model.OfficialAccountProviderClaude,
	}
	statuses := make([]model.OfficialPoolStatus, 0, len(providers))
	for _, provider := range providers {
		status := model.OfficialPoolStatus{Provider: provider, ChannelName: OfficialPoolChannelName(provider)}
		accounts, err := officialPoolAccounts(target, provider)
		if err != nil {
			return nil, err
		}
		status.Accounts = len(accounts)

		var channel model.Channel
		err = target.Where("name = ?", status.ChannelName).First(&channel).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 尚无号池渠道也照常返回一行：界面据此显示"未建立"，无需另行判空。
			// 明细照旧给出（凭据不存在），这样"有账号但还没同步"与"根本没账号"在界面上能区分。
			status.Members = officialPoolMembers(accounts, nil)
			statuses = append(statuses, status)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("lookup official pool channel: %w", err)
		}
		status.ChannelID = channel.ID

		keys, err := officialPoolKeys(target, channel.ID)
		if err != nil {
			return nil, err
		}
		// 号池凭据以账号的 ExternalName 命名（见 OfficialPoolSync），故按名称建索引即可对齐。
		keyByName := make(map[string]model.ChannelKey, len(keys))
		for _, key := range keys {
			keyByName[key.Name] = key
			if key.Enabled {
				status.ActiveKeys++
			}
		}
		status.Members = officialPoolMembers(accounts, keyByName)
		if status.Models, err = officialPoolModelCount(target, channel.ID); err != nil {
			return nil, err
		}
		if status.Grants, err = officialPoolGrantCount(target, channel.ID); err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

// officialPoolMembers 把账号与号池凭据对齐成统一视图的行; keyByName 为空表示该服务商尚未建号池渠道。
func officialPoolMembers(accounts []model.OfficialAccount, keyByName map[string]model.ChannelKey) []model.OfficialPoolMember {
	members := make([]model.OfficialPoolMember, 0, len(accounts))
	for _, account := range accounts {
		member := model.OfficialPoolMember{
			AccountID:    account.ID,
			ExternalName: account.ExternalName,
			Status:       account.Status,
			ExpiresAt:    account.ExpiresAt,
			PlanTier:     account.PlanTier,
			Window5H:     account.Window5H,
			Window7D:     account.Window7D,
			Healthy:      account.Healthy,
			LastError:    account.LastError,
		}
		if key, ok := keyByName[account.ExternalName]; ok {
			member.KeyName = key.Name
			member.KeyEnabled = key.Enabled
			member.KeyOperatorDisabled = key.OperatorDisabled
			member.KeyExists = true
		}
		members = append(members, member)
	}
	return members
}

// officialPoolEnsureChannel 取或建号池渠道：已存在则原样返回，绝不覆盖操作者在渠道页改过的地址与路径。
func officialPoolEnsureChannel(conn *gorm.DB, provider model.OfficialAccountProvider) (model.Channel, error) {
	name := OfficialPoolChannelName(provider)
	var channel model.Channel
	err := conn.Where("name = ?", name).First(&channel).Error
	if err == nil {
		return channel, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Channel{}, fmt.Errorf("lookup official pool channel: %w", err)
	}

	base := officialPoolBaseURL(provider)
	if base == "" {
		return model.Channel{}, fmt.Errorf("no metadata endpoint for provider %q", provider)
	}
	// 地址取该服务商的官方元数据端点，路径取与手工建渠道一致的默认值；
	// 官方端点是否需额外 Header 由操作者在渠道页按实测补 CustomHeader，不在同步里臆造。
	channel = model.Channel{ChannelConfig: model.ChannelConfig{
		Name:                     name,
		Dialect:                  model.DialectGeneric,
		Enabled:                  true,
		BaseURL:                  base,
		OpenAIChatCompletionPath: "/v1/chat/completions",
		OpenAIResponsePath:       "/v1/responses",
		AnthropicMessagePath:     "/v1/messages",
		CustomHeader:             []model.CustomHeader{},
	}}
	if err := conn.Create(&channel).Error; err != nil {
		return model.Channel{}, fmt.Errorf("create official pool channel: %w", err)
	}
	return channel, nil
}

// officialPoolBaseURL 取服务商元数据端点作号池初始地址，并去掉尾部斜杠：
// 地址与协议路径是直接拼接的，留着尾斜杠会拼出双斜杠。
func officialPoolBaseURL(provider model.OfficialAccountProvider) string {
	endpoints, ok := OfficialEndpoints[provider]
	if !ok {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(endpoints.Meta), "/")
}

// officialPoolRefreshIfNeeded 在访问凭据临近过期时用刷新凭据换新 token 并落库，返回是否真的刷新过。
// 无过期机制（ExpiresAt 为空）与余量充足时都不刷新：官方的刷新调用本身会轮换凭据，能不换就不换。
func officialPoolRefreshIfNeeded(conn *gorm.DB, account model.OfficialAccount) (model.OfficialAccount, bool, error) {
	if account.ExpiresAt == nil || account.ExpiresAt.After(time.Now().Add(officialPoolRefreshSkew)) {
		return account, false, nil
	}
	refresh, err := officialDecrypt(account.Provider, account.RefreshCipher)
	if err != nil {
		return account, false, fmt.Errorf("解密刷新凭据: %w", err)
	}
	if strings.TrimSpace(refresh) == "" {
		return account, false, errors.New("该服务商无刷新凭据，需重新授权")
	}

	bundle, err := officialRefresher.Refresh(account.Provider, refresh)
	if err != nil {
		return account, false, err
	}
	if strings.TrimSpace(bundle.AccessToken) == "" {
		return account, false, errors.New("刷新接口返回空访问凭据")
	}
	accessCipher, err := officialEncrypt(account.Provider, bundle.AccessToken)
	if err != nil {
		return account, false, err
	}
	updates := map[string]any{
		"access_cipher": accessCipher,
		"status":        model.OfficialAccountStatusActive,
		"last_error":    "",
	}
	if strings.TrimSpace(bundle.RefreshToken) != "" {
		// 官方换新刷新凭据时以新的为准；不回传则沿用旧的。
		refreshCipher, err := officialEncrypt(account.Provider, bundle.RefreshToken)
		if err != nil {
			return account, false, err
		}
		updates["refresh_cipher"] = refreshCipher
	}
	if bundle.ExpiresIn > 0 {
		expires := time.Now().Add(time.Duration(bundle.ExpiresIn) * time.Second)
		updates["expires_at"] = &expires
	}
	if err := conn.Model(&model.OfficialAccount{}).Where("id = ?", account.ID).Updates(updates).Error; err != nil {
		return account, false, fmt.Errorf("persist refreshed token: %w", err)
	}
	updated, err := officialAccountGetOn(conn, account.ID)
	if err != nil {
		return account, false, err
	}
	return updated, true, nil
}

func officialPoolAccounts(conn *gorm.DB, provider model.OfficialAccountProvider) ([]model.OfficialAccount, error) {
	var accounts []model.OfficialAccount
	if err := conn.Where("provider = ?", provider).Order("id ASC").Find(&accounts).Error; err != nil {
		return nil, fmt.Errorf("list official accounts: %w", err)
	}
	return accounts, nil
}

func officialPoolKeys(conn *gorm.DB, channelID int) ([]model.ChannelKey, error) {
	keys, err := officialPoolRawKeys(conn, channelID)
	if err != nil {
		return nil, err
	}
	// 库内密文、进程内明文：物化同步要拿明文做比较（是否变化）。
	if err := DecryptChannelKeyRows(keys); err != nil {
		return nil, err
	}
	return keys, nil
}

// officialPoolRawKeys 只取行、不解密：解密失败要能被调用方单独识别并降级处理
// （见 OfficialPoolSync 里"解不开就记原因、一行不改"的那段），整轮失败只留给真正的库错误。
func officialPoolRawKeys(conn *gorm.DB, channelID int) ([]model.ChannelKey, error) {
	var keys []model.ChannelKey
	if err := conn.Where("channel_id = ?", channelID).Find(&keys).Error; err != nil {
		return nil, fmt.Errorf("list official pool keys: %w", err)
	}
	return keys, nil
}

func officialPoolModelCount(conn *gorm.DB, channelID int) (int, error) {
	var count int64
	if err := conn.Model(&model.ChannelModel{}).Where("channel_id = ?", channelID).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count official pool models: %w", err)
	}
	return int(count), nil
}

func officialPoolGrantCount(conn *gorm.DB, channelID int) (int, error) {
	var count int64
	err := conn.Model(&model.ChannelGrant{}).
		Where("channel_model_id IN (?)",
			conn.Model(&model.ChannelModel{}).Select("id").Where("channel_id = ?", channelID)).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count official pool grants: %w", err)
	}
	return int(count), nil
}

// ---------- token 刷新（可注入桩供单测） ----------

// OfficialTokenRefresher 用刷新凭据换新的访问凭据；生产走官方 token 端点，测试注入桩。
type OfficialTokenRefresher interface {
	Refresh(provider model.OfficialAccountProvider, refreshToken string) (OfficialTokenBundle, error)
}

var officialRefresher OfficialTokenRefresher = &httpTokenRefresher{}

// SetOfficialTokenRefresherForTest 注入刷新桩，仅测试接线用。
func SetOfficialTokenRefresherForTest(refresher OfficialTokenRefresher) {
	if refresher != nil {
		officialRefresher = refresher
	}
}

type httpTokenRefresher struct{}

func (h *httpTokenRefresher) Refresh(provider model.OfficialAccountProvider, refreshToken string) (OfficialTokenBundle, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return OfficialTokenBundle{}, errors.New("refresh token is empty")
	}
	endpoints, ok := OfficialEndpoints[provider]
	if !ok {
		return OfficialTokenBundle{}, fmt.Errorf("no token endpoint for %q", provider)
	}
	clientID := officialOAuthClientID(provider)
	if clientID == "" {
		return OfficialTokenBundle{}, fmt.Errorf("official OAuth client id not configured for %s (set %s)", provider, officialOAuthClientIDEnv(provider))
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	// 机密客户端才需要 secret；公开客户端（PKCE）留空即不发该字段。
	if secret := strings.TrimSpace(os.Getenv("OCTOPUS_OFFICIAL_CLIENT_SECRET_" + strings.ToUpper(string(provider)))); secret != "" {
		form.Set("client_secret", secret)
	}

	req, err := http.NewRequest(http.MethodPost, endpoints.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return OfficialTokenBundle{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := officialHTTPClient().Do(req)
	if err != nil {
		return OfficialTokenBundle{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, officialResponseMB))
	if err != nil {
		return OfficialTokenBundle{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return OfficialTokenBundle{}, fmt.Errorf("refresh endpoint %s: status %d", provider, resp.StatusCode)
	}
	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return OfficialTokenBundle{}, fmt.Errorf("refresh endpoint %s: %w", provider, err)
	}
	return OfficialTokenBundle{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		ExpiresIn:    parsed.ExpiresIn,
	}, nil
}
