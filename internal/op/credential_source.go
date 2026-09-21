package op

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/collector"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/secret"
)

// 三类密文用不同 AAD 做用途隔离：采集包（可能含站点内网路径）、登录凭据、登录会话，互不通用。
const (
	collectorAADPack = "collector:pack"
	collectorAADCred = "collector:credential"
	collectorAADSess = "collector:session"
)

const collectorStepKeep = 12

// collectorState 保存"正在进行的登录会话"：交互登录时浏览器与采集器必须用同一个 Cookie 罐，
// 否则用户在页面里登进去了、采集器却拿着一份空会话（这正是这类功能最经典的失败方式）。
var collectorState = struct {
	sync.Mutex
	sessions map[int]*collector.Session
	steps    map[int][]string
	// tokens 是"登录临时地址"用的一次性令牌（源 id → 令牌）。
	//
	// 为什么需要它：这个地址本来就是**发给用户、在任意浏览器/设备上打开**的，
	// 而管理面路由挂了 middleware.Auth —— 没有面板 cookie 的浏览器只会拿到
	// `401 {"code":401,"message":"Authentication failed"}`（用户看到的是一张空白页）。
	// 令牌把"能打开这个反代"这件事从"必须先在面板登录"解耦成"拿到这条链接"。
	tokens map[int]string
}{sessions: map[int]*collector.Session{}, steps: map[int][]string{}, tokens: map[int]string{}}

type collectorCredentials struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Captcha  string `json:"captcha,omitempty"`
	OTP      string `json:"otp,omitempty"`
}

// CredentialSourceList 列出全部采集凭据（含采集包原文与会话状态，供面板展示与编辑）。
func CredentialSourceList(ctx context.Context) ([]model.CredentialSourceDetail, error) {
	var rows []model.CredentialSource
	if err := db.GetDB().WithContext(ctx).Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.CredentialSourceDetail, 0, len(rows))
	for _, row := range rows {
		out = append(out, collectorDetail(row))
	}
	return out, nil
}

// CredentialSourceGet 读取单个采集凭据。
func CredentialSourceGet(ctx context.Context, id int) (model.CredentialSourceDetail, error) {
	row, err := credentialSourceRow(ctx, id)
	if err != nil {
		return model.CredentialSourceDetail{}, err
	}
	return collectorDetail(row), nil
}

// CredentialSourceSave 新建或更新一个采集凭据。
//
// 两条"留空即不改"的规则很重要：面板改一个开关不必把整份采集包与密码重发一遍，
// 否则任何一次部分更新都会把没带上的字段清空（这是同类功能最常见的静默数据丢失）。
func CredentialSourceSave(ctx context.Context, id int, in model.CredentialSourceInput) (model.CredentialSourceDetail, error) {
	var row model.CredentialSource
	creating := id <= 0
	if !creating {
		existing, err := credentialSourceRow(ctx, id)
		if err != nil {
			return model.CredentialSourceDetail{}, err
		}
		row = existing
	}

	if strings.TrimSpace(in.Name) != "" {
		row.Name = strings.TrimSpace(in.Name)
	}
	if creating && row.Name == "" {
		return model.CredentialSourceDetail{}, fmt.Errorf("采集凭据需要 name")
	}
	if strings.TrimSpace(in.Kind) != "" {
		row.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	}
	if row.Kind == "" {
		row.Kind = model.CredentialKindPack
	}
	if row.Kind != model.CredentialKindLogin && row.Kind != model.CredentialKindPack {
		return model.CredentialSourceDetail{}, fmt.Errorf("kind 必须是 %s 或 %s", model.CredentialKindLogin, model.CredentialKindPack)
	}
	if strings.TrimSpace(in.Site) != "" {
		row.Site = strings.TrimRight(strings.TrimSpace(in.Site), "/")
	}
	// 出口节点单独判定：面板改一个下拉不必把 site 一起重发（绑定在 site 分支里会被静默丢弃）。
	if in.ProxyNodeID != nil {
		row.ProxyNodeID = *in.ProxyNodeID
	}
	if in.ChannelID > 0 || creating {
		row.ChannelID = in.ChannelID
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	} else if creating {
		row.Enabled = true
	}
	if in.AutoRefresh != nil {
		row.AutoRefresh = *in.AutoRefresh
	} else if creating {
		row.AutoRefresh = true
	}

	// 采集包：给了才替换（解析失败连保存一起拒绝，不留"半份包"）。
	if strings.TrimSpace(in.Pack) != "" {
		pack, err := collector.ParsePack([]byte(in.Pack))
		if err != nil {
			return model.CredentialSourceDetail{}, fmt.Errorf("采集包被拒绝：%v", err)
		}
		sealed, err := secret.SealWith(collectorAADPack, compactJSON(pack))
		if err != nil {
			return model.CredentialSourceDetail{}, err
		}
		row.PackCipher = sealed
		row.Hosts = strings.Join(pack.Hosts, ",")
		if pack.Units != "" {
			row.Units = pack.Units
		}
	}
	if row.PackCipher == "" {
		return model.CredentialSourceDetail{}, fmt.Errorf("缺少采集包：必须提供一份能读到余额的包（或先导入一份）")
	}
	if row.Kind == model.CredentialKindLogin && row.Site == "" {
		return model.CredentialSourceDetail{}, fmt.Errorf("交互登录必须填站点地址（登录页的反代目标）")
	}

	// 凭据：给了才合并（密码留空表示"不改密码"，不是"把密码清空"）。
	if in.Username != "" || in.Password != "" || in.Captcha != "" || in.OTP != "" {
		existing := collectorCredentials{}
		if row.CredCipher != "" {
			if plain, err := secret.OpenWith(collectorAADCred, row.CredCipher); err == nil {
				_ = json.Unmarshal([]byte(plain), &existing)
			}
		}
		if in.Username != "" {
			existing.Username = in.Username
			row.Username = in.Username
		}
		if in.Password != "" {
			existing.Password = in.Password
		}
		if in.Captcha != "" {
			existing.Captcha = in.Captcha
		}
		if in.OTP != "" {
			existing.OTP = in.OTP
		}
		sealed, err := secret.SealWith(collectorAADCred, compactJSON(existing))
		if err != nil {
			return model.CredentialSourceDetail{}, err
		}
		row.CredCipher = sealed
	}

	if creating {
		if row.Status == "" {
			row.Status = model.CredentialStatusPending
		}
		if err := db.GetDB().WithContext(ctx).Create(&row).Error; err != nil {
			return model.CredentialSourceDetail{}, err
		}
	} else {
		if err := db.GetDB().WithContext(ctx).Model(&model.CredentialSource{}).
			Where("id = ?", row.ID).
			Select("name", "kind", "site", "proxy_node_id", "channel_id", "enabled", "auto_refresh", "units", "status",
				"username", "hosts", "pack_cipher", "cred_cipher", "session_cipher", "last_read_at",
				"last_error", "last_balance", "last_used", "last_quota", "last_currency", "note").
			Updates(&row).Error; err != nil {
			return model.CredentialSourceDetail{}, err
		}
	}
	return collectorDetail(row), nil
}

// CredentialSourceDelete 删除采集凭据并清掉内存会话（密文随之作废）。
func CredentialSourceDelete(ctx context.Context, id int) error {
	if err := db.GetDB().WithContext(ctx).Where("id = ?", id).Delete(&model.CredentialSource{}).Error; err != nil {
		return err
	}
	collectorState.Lock()
	delete(collectorState.sessions, id)
	delete(collectorState.steps, id)
	delete(collectorState.tokens, id)
	collectorState.Unlock()
	return nil
}

// CredentialSourceStartLogin 开始一次交互登录：分配会话并给出"登录临时地址"。
func CredentialSourceStartLogin(ctx context.Context, id int) (model.CredentialSourceDetail, error) {
	row, err := credentialSourceRow(ctx, id)
	if err != nil {
		return model.CredentialSourceDetail{}, err
	}
	if row.Kind != model.CredentialKindLogin {
		return model.CredentialSourceDetail{}, fmt.Errorf("只有 kind=%s 的采集凭据需要交互登录（包模式直接跑就行）", model.CredentialKindLogin)
	}
	if row.Site == "" {
		return model.CredentialSourceDetail{}, fmt.Errorf("缺少站点地址，无法反代登录页")
	}
	ensureReachableNode(ctx, &row)
	session, err := credentialSession(id, row)
	if err != nil {
		return model.CredentialSourceDetail{}, err
	}
	// 反代登录也要走出口：页面加载与表单提交都不使用本机真实 IP。
	proxyURL, proxyErr := collectorProxyURL(row)
	if proxyErr != nil {
		return model.CredentialSourceDetail{}, proxyErr
	}
	// 一次性令牌：地址可以发给任意浏览器/设备打开，不要求先登录面板。
	token, tokenErr := collectorLoginToken()
	if tokenErr != nil {
		return model.CredentialSourceDetail{}, tokenErr
	}
	collectorState.Lock()
	collectorState.tokens[id] = token
	collectorState.Unlock()

	prefix := CollectorPortalPrefix(id, token)
	if _, err := collector.NewLoginProxyVia(row.Site, session, prefix, proxyURL); err != nil {
		return model.CredentialSourceDetail{}, err
	}
	detail := collectorDetail(row)
	detail.LoginURL = prefix + "/"
	return detail, nil
}

// CredentialSourceFinishLogin 结束交互登录：把捕获到的会话加密落库，并立刻试读一次账。
//
// "立刻试读"是刻意的：只存会话不验证，用户会以为成功了，直到下一轮扫描才发现其实没登进去。
func CredentialSourceFinishLogin(ctx context.Context, id int) (model.CredentialSourceDetail, error) {
	row, err := credentialSourceRow(ctx, id)
	if err != nil {
		return model.CredentialSourceDetail{}, err
	}
	if row.Kind != model.CredentialKindLogin {
		return model.CredentialSourceDetail{}, fmt.Errorf("只有交互登录类型的凭据才有\"完成登录\"这一步")
	}
	session, ok := collectorSessionIfAny(id)
	if !ok {
		return model.CredentialSourceDetail{}, fmt.Errorf("没有进行中的登录会话：先打开登录地址完成登录，再点完成")
	}
	state := session.Export()
	if len(state.Cookies) == 0 && len(state.Vars) == 0 {
		return model.CredentialSourceDetail{}, fmt.Errorf("还没捕获到任何会话：请在登录页里完成登录（含验证码）后再点完成")
	}
	if err := collectorStoreSession(ctx, id, state); err != nil {
		return model.CredentialSourceDetail{}, err
	}
	// 记下"抓到了什么形状"（只记名字与条数，**不记值**）：令牌抓不到时，这一行就是判据——
	// 用户看到"Cookie 3 条 / 变量 0 个"就知道是令牌没进会话，而不是登录没成功。
	collectorRememberSteps(id, []string{captureShapeLine(state)})
	reading, result, err := CredentialSourceRunNow(ctx, id)
	detail, _ := CredentialSourceGet(ctx, id)
	if err != nil {
		return detail, err
	}
	if !result.OK() {
		return detail, fmt.Errorf("会话已保存，但试读余额失败：%s", result.ReasonText)
	}
	_ = reading
	return detail, nil
}

// CredentialSourceClearSession 清掉会话（重新登录时用）。
func CredentialSourceClearSession(ctx context.Context, id int) (model.CredentialSourceDetail, error) {
	row, err := credentialSourceRow(ctx, id)
	if err != nil {
		return model.CredentialSourceDetail{}, err
	}
	collectorState.Lock()
	delete(collectorState.sessions, id)
	delete(collectorState.steps, id)
	delete(collectorState.tokens, id)
	collectorState.Unlock()
	row.SessionCipher = ""
	row.Status = model.CredentialStatusPending
	row.LastError = ""
	if err := db.GetDB().WithContext(ctx).Model(&model.CredentialSource{}).Where("id = ?", id).
		Select("session_cipher", "status", "last_error").Updates(&row).Error; err != nil {
		return model.CredentialSourceDetail{}, err
	}
	return collectorDetail(row), nil
}

// CredentialSourceRunNow 立即采集一次：跑采集包 → 落库读数 → 绑定渠道时直接进余额管线。
func CredentialSourceRunNow(ctx context.Context, id int) (model.CollectorReading, collector.Result, error) {
	row, err := credentialSourceRow(ctx, id)
	if err != nil {
		return model.CollectorReading{}, collector.Result{}, err
	}
	pack, err := collectorPack(row)
	if err != nil {
		return model.CollectorReading{}, collector.Result{}, err
	}
	creds, err := collectorCredentialBundle(row)
	if err != nil {
		return model.CollectorReading{}, collector.Result{}, err
	}
	ensureReachableNode(ctx, &row)
	session, err := credentialSession(id, row)
	if err != nil {
		return model.CollectorReading{}, collector.Result{}, err
	}
	proxyURL, proxyErr := collectorProxyURL(row)
	if proxyErr != nil {
		return model.CollectorReading{}, collector.Result{}, proxyErr
	}
	result := collector.RunVia(ctx, pack, session, creds, row.Site, proxyURL)

	row.LastReadAt = time.Now().Format(time.RFC3339)
	row.LastError = ""
	if result.OK() {
		row.Status = model.CredentialStatusAuthorized
		row.LastBalance = result.Balance
		row.LastUsed = result.Used
		row.LastQuota = result.Quota
		row.LastCurrency = result.Currency
		if result.Note != "" {
			row.Note = result.Note
		}
		if result.Username != "" {
			row.Username = result.Username
		}
	} else {
		row.Status = model.CredentialStatusFailed
		row.LastError = result.ReasonText
	}
	if sealed, err := secret.SealWith(collectorAADSess, compactJSON(result.Session)); err == nil {
		row.SessionCipher = sealed
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.CredentialSource{}).Where("id = ?", id).
		Select("status", "session_cipher", "last_read_at", "last_error", "last_balance", "last_used",
			"last_quota", "last_currency", "note", "username").Updates(&row).Error; err != nil {
		return model.CollectorReading{}, result, err
	}
	collectorRememberSteps(id, result.Steps)

	reading := model.CollectorReading{
		SourceID: id, ChannelID: row.ChannelID, Units: result.Units, Balance: result.Balance,
		Used: result.Used, Quota: result.Quota, Currency: result.Currency,
		Username: row.Username, Note: result.Note, At: row.LastReadAt,
	}
	if row.ChannelID > 0 {
		if result.OK() {
			// 单位口径由包声明：报美元的按美元记，报点数的按点数记（否则会再被折算 50 万倍）。
			RecordChannelBalanceWithUnit(row.ChannelID, result.Balance, result.Units == collector.UnitsUSD)
			RecordChannelBalanceReason(row.ChannelID, "", "")
		} else {
			RecordChannelBalanceReason(row.ChannelID, result.ReasonKey, "采集包读取失败："+result.ReasonText)
		}
	}
	return reading, result, nil
}

// CollectorReadingForChannel 给余额扫描用：该渠道是否有"启用且自动刷新"的采集凭据，有就现跑一次。
//
// 语义上采集凭据比通用探测更权威 —— 它是用户明确为这个渠道配的读法（人在面板里登录过 / 装了自己的包），
// 所以扫描优先用它；失败才回落到内置的四种协议探测。
func CollectorReadingForChannel(ctx context.Context, channelID int) (model.CollectorReading, bool) {
	row, err := credentialSourceForChannel(ctx, channelID)
	if err != nil || row.ID == 0 {
		return model.CollectorReading{}, false
	}
	reading, result, err := CredentialSourceRunNow(ctx, row.ID)
	if err != nil {
		RecordChannelBalanceReason(channelID, collector.ReasonUnreachable, "采集凭据执行失败："+trimCollectorError(err.Error()))
		return model.CollectorReading{}, false
	}
	if !result.OK() {
		return model.CollectorReading{}, false
	}
	return reading, true
}

// collectorLoginToken 生成一条登录地址的令牌（24 字节随机，URL 安全编码）。
func collectorLoginToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成登录令牌失败：%w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// CollectorPortalPrefix 是"发给用户的登录临时地址"前缀：带一次性令牌，因此不需要面板会话。
// 令牌放在路径里（而不是 query）：反代会把前缀写进页面与脚本里的每个子请求，
// 路径形态能让这些子请求自动带上令牌。
func CollectorPortalPrefix(id int, token string) string {
	return fmt.Sprintf("/api/v1/collector/portal/%d/%s", id, token)
}

// CollectorLoginTokenMatches 校验一次性令牌（常量时间比较，避免按前缀猜令牌）。
func CollectorLoginTokenMatches(id int, token string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	collectorState.Lock()
	expected, ok := collectorState.tokens[id]
	collectorState.Unlock()
	if !ok || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(token)) == 1
}

// CollectorLoginProxyWithPrefix 用指定前缀构造登录代理（公开路径与面板路径共用同一套实现）。
func CollectorLoginProxyWithPrefix(ctx context.Context, id int, prefix string) (*collector.LoginProxy, error) {
	return collectorLoginProxyWithPrefix(ctx, id, prefix)
}

// CredentialSourceExportPack 导出这条采集凭据的"扩展包"（= 采集包结构）。
//
// 这是"可分享"的关键：包里只有 hosts / login 步骤 / read 步骤 / units / 字段映射，
// **账号密码不在包里**（它们单独存在 CredCipher，导出时结构上拿不到）——
// 所以别人拿到包之后只需另填自己的账号密码即可用，不会泄露任何人的凭据。
func CredentialSourceExportPack(ctx context.Context, id int) (model.CredentialSourceDetail, string, error) {
	row, err := credentialSourceRow(ctx, id)
	if err != nil {
		return model.CredentialSourceDetail{}, "", err
	}
	if row.PackCipher == "" {
		return model.CredentialSourceDetail{}, "", fmt.Errorf("这条凭据没有采集包")
	}
	plain, err := secret.OpenWith(collectorAADPack, row.PackCipher)
	if err != nil {
		return model.CredentialSourceDetail{}, "", fmt.Errorf("采集包解不开：%w", err)
	}
	return collectorDetail(row), plain, nil
}

// CollectorLoginProxy 构造一次交互登录代理（每个请求现场构造，避免缓存出陈旧前缀）。
func CollectorLoginProxy(ctx context.Context, id int) (*collector.LoginProxy, error) {
	return collectorLoginProxyWithPrefix(ctx, id, CollectorLoginPrefix(id))
}

func collectorLoginProxyWithPrefix(ctx context.Context, id int, prefix string) (*collector.LoginProxy, error) {
	row, err := credentialSourceRow(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.Kind != model.CredentialKindLogin {
		return nil, fmt.Errorf("该采集凭据不是交互登录类型")
	}
	if row.Site == "" {
		return nil, fmt.Errorf("缺少站点地址")
	}
	session, err := credentialSession(id, row)
	if err != nil {
		return nil, err
	}
	proxyURL, proxyErr := collectorProxyURL(row)
	if proxyErr != nil {
		return nil, proxyErr
	}
	// 必须用调用方给的 prefix：portal 是公开路径，旧前缀 /api/v1/collector/login/<id> 需要面板会话，
	// 一旦写错，页面里的资源与接口全会指向鉴权路径 → 浏览器每个资源都 401 → 整页白屏（实测就是这个）。
	proxy, err := collector.NewLoginProxyVia(row.Site, session, prefix, proxyURL)
	if err != nil {
		return nil, err
	}
	// 备用出口：节点池会抖，主出口失败时按顺序换人重试（最多取 5 个其它启用节点）。
	var fallbacks []*http.Client
	for _, nodeID := range preferredProxyNodeIDs(5) {
		if nodeID == row.ProxyNodeID {
			continue
		}
		endpoint, endpointErr := ProxyNodeEndpoint(nodeID)
		if endpointErr != nil || endpoint == "" {
			continue
		}
		if client, clientErr := collector.ProxyClient(endpoint); clientErr == nil {
			fallbacks = append(fallbacks, client)
		}
	}
	proxy.SetFallbackProxies(fallbacks)
	return proxy, nil
}

// CollectorLoginPrefix 是登录代理挂载的路径前缀。
func CollectorLoginPrefix(id int) string {
	return fmt.Sprintf("/api/v1/collector/login/%d", id)
}

// CollectorSteps 返回某采集凭据最近一次的步骤轨迹（脱敏后的排障信息）。
func CollectorSteps(id int) []string {
	collectorState.Lock()
	defer collectorState.Unlock()
	return append([]string{}, collectorState.steps[id]...)
}

func credentialSourceRow(ctx context.Context, id int) (model.CredentialSource, error) {
	var row model.CredentialSource
	if id <= 0 {
		return row, fmt.Errorf("采集凭据 id 非法")
	}
	if err := db.GetDB().WithContext(ctx).First(&row, id).Error; err != nil {
		return row, fmt.Errorf("采集凭据 #%d 不存在：%v", id, err)
	}
	return row, nil
}

func credentialSourceForChannel(ctx context.Context, channelID int) (model.CredentialSource, error) {
	var row model.CredentialSource
	if channelID <= 0 {
		return row, fmt.Errorf("渠道 id 非法")
	}
	err := db.GetDB().WithContext(ctx).
		Where("channel_id = ? AND enabled = ? AND auto_refresh = ?", channelID, true, true).
		Order("id asc").First(&row).Error
	return row, err
}

// credentialSession 取内存会话；没有就从库里恢复（换进程/重启后仍然可用）。
func credentialSession(id int, row model.CredentialSource) (*collector.Session, error) {
	if session, ok := collectorSessionIfAny(id); ok {
		return session, nil
	}
	session := collector.NewSession()
	if row.SessionCipher != "" {
		plain, err := secret.OpenWith(collectorAADSess, row.SessionCipher)
		if err != nil {
			return nil, fmt.Errorf("登录会话解不开（凭据密钥变了？）：%v", err)
		}
		var state collector.SessionState
		if err := json.Unmarshal([]byte(plain), &state); err != nil {
			return nil, fmt.Errorf("登录会话内容损坏：%v", err)
		}
		restored, err := collector.SessionFromState(state)
		if err != nil {
			return nil, err
		}
		session = restored
	}
	collectorState.Lock()
	collectorState.sessions[id] = session
	collectorState.Unlock()
	return session, nil
}

func collectorSessionIfAny(id int) (*collector.Session, bool) {
	collectorState.Lock()
	defer collectorState.Unlock()
	session, ok := collectorState.sessions[id]
	return session, ok
}

func collectorStoreSession(ctx context.Context, id int, state collector.SessionState) error {
	sealed, err := secret.SealWith(collectorAADSess, compactJSON(state))
	if err != nil {
		return err
	}
	return db.GetDB().WithContext(ctx).Model(&model.CredentialSource{}).Where("id = ?", id).
		Update("session_cipher", sealed).Error
}

func collectorPack(row model.CredentialSource) (collector.Pack, error) {
	if row.PackCipher == "" {
		return collector.Pack{}, fmt.Errorf("采集凭据 #%d 没有采集包", row.ID)
	}
	plain, err := secret.OpenWith(collectorAADPack, row.PackCipher)
	if err != nil {
		return collector.Pack{}, fmt.Errorf("采集包解不开（凭据密钥变了？）：%v", err)
	}
	pack, err := collector.ParsePack([]byte(plain))
	if err != nil {
		return collector.Pack{}, fmt.Errorf("采集包内容非法：%v", err)
	}
	return pack, nil
}

func collectorCredentialBundle(row model.CredentialSource) (collector.Credentials, error) {
	if row.CredCipher == "" {
		return collector.Credentials{}, nil
	}
	plain, err := secret.OpenWith(collectorAADCred, row.CredCipher)
	if err != nil {
		return collector.Credentials{}, fmt.Errorf("登录凭据解不开（凭据密钥变了？）：%v", err)
	}
	var bundle collectorCredentials
	if err := json.Unmarshal([]byte(plain), &bundle); err != nil {
		return collector.Credentials{}, fmt.Errorf("登录凭据内容损坏：%v", err)
	}
	return collector.Credentials{Username: bundle.Username, Password: bundle.Password, Captcha: bundle.Captcha, OTP: bundle.OTP}, nil
}

// collectorDetail 组装面板形态：密文不外传，只给"有没有"的布尔与采集包原文。
func collectorDetail(row model.CredentialSource) model.CredentialSourceDetail {
	detail := model.CredentialSourceDetail{
		CredentialSource: row,
		HasCredentials:   row.CredCipher != "",
		HasSession:       row.SessionCipher != "",
		Steps:            CollectorSteps(row.ID),
	}
	if row.PackCipher != "" {
		if plain, err := secret.OpenWith(collectorAADPack, row.PackCipher); err == nil {
			detail.PackText = plain
		}
	}
	if row.Kind == model.CredentialKindLogin && row.Site != "" {
		detail.LoginURL = CollectorLoginPrefix(row.ID) + "/"
	}
	return detail
}

func collectorRememberSteps(id int, steps []string) {
	if len(steps) == 0 {
		return
	}
	collectorState.Lock()
	defer collectorState.Unlock()
	merged := append(collectorState.steps[id], steps...)
	if len(merged) > collectorStepKeep {
		merged = merged[len(merged)-collectorStepKeep:]
	}
	collectorState.steps[id] = merged
}

func compactJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func trimCollectorError(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 160 {
		return text[:160] + "…"
	}
	return text
}

// collectorProxyURL 解析一条采集凭据的出口：
//   - 0 = 直连（保持既有行为，没有节点的老数据照旧能用）；
//   - 非 0 且节点已就绪 → 返回内核给它的本地入站地址；
//   - 非 0 但节点未就绪/已停用 → **报错拒绝采集**，绝不静默改走直连。
//
// 最后一条是这个功能的立身之本：静默直连会把使用者的真实 IP 暴露给站点，
// 而这正是用户要求"用代理，不要让我的 IP 被拉黑"要防的事。
func collectorProxyURL(row model.CredentialSource) (string, error) {
	if row.ProxyNodeID == 0 {
		return "", nil
	}
	endpoint, err := ProxyNodeEndpoint(row.ProxyNodeID)
	if err != nil {
		return "", fmt.Errorf("采集出口不可用：%v", err)
	}
	return endpoint, nil
}

// captureShapeLine 把会话形状写成一行排障信息（只含键名与条数，绝不含令牌值）。
func captureShapeLine(state collector.SessionState) string {
	names := make([]string, 0, len(state.Vars))
	for key := range state.Vars {
		names = append(names, key)
	}
	sort.Strings(names)
	hint := "无令牌"
	if _, ok := state.Vars[collector.VarToken]; ok {
		hint = "含令牌"
	}
	return fmt.Sprintf("会话已捕获：Cookie %d 条 / 变量 %d 个（%s）%s",
		len(state.Cookies), len(state.Vars), strings.Join(names, ","), hint)
}

// ensureReachableNode 保证该凭据绑的出口**真的能到达站点**；到不了就自动换一个能到的并落库。
//
// 为什么需要：节点池会抖（实测同一节点几分钟前 200、几分钟后就 EOF），而出口不可达在反代登录页上
// 表现为「登录代理错误：上游请求失败：Get "https://…": EOF」——用户完全看不出是节点问题，
// 只会以为"登录又坏了"。这里在开始登录/采集之前先探一次活，把节点问题就地解决掉。
func ensureReachableNode(ctx context.Context, row *model.CredentialSource) {
	if row.Site == "" {
		return
	}
	if row.ProxyNodeID > 0 && probeNodeReachesSite(row.ProxyNodeID, row.Site) {
		return
	}
	previous := row.ProxyNodeID
	for _, nodeID := range preferredProxyNodeIDs(16) {
		if nodeID == previous || !probeNodeReachesSite(nodeID, row.Site) {
			continue
		}
		row.ProxyNodeID = nodeID
		_ = db.GetDB().WithContext(ctx).Model(&model.CredentialSource{}).
			Where("id = ?", row.ID).Update("proxy_node_id", nodeID).Error
		collectorRememberSteps(row.ID, []string{
			fmt.Sprintf("出口节点 %d 到不了站点，已自动切到 %d", previous, nodeID)})
		return
	}
}

// probeNodeReachesSite 用指定出口探站点根：拿到任何 HTTP 响应都算可达（连接失败/EOF 算不可达）。
func probeNodeReachesSite(nodeID int, site string) bool {
	endpoint, err := ProxyNodeEndpoint(nodeID)
	if err != nil || endpoint == "" {
		return false
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(parsed), TLSHandshakeTimeout: 6 * time.Second},
		Timeout:   10 * time.Second,
	}
	request, err := http.NewRequest(http.MethodGet, site, nil)
	if err != nil {
		return false
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/124.0")
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return true
}

// enabledProxyNodeIDs 取启用的出口节点 id（按 id 升序，最多 limit 个）。
func enabledProxyNodeIDs(limit int) []int {
	var ids []int
	_ = db.GetDB().Model(&model.ProxyNode{}).Where("enabled = ? AND local_port > 0", true).
		Order("id asc").Limit(limit).Pluck("id", &ids).Error
	return ids
}

// asianNodeHints 是"亚洲节点"的名称关键字（节点名里带地区信息，实测形如
// "香港W09 | IEPL" / "D-日本1-YouTube…" / "专线A1-美国3-…"）。亚洲节点到目标站点的
// 延迟通常低得多，所以选出口时优先挑它们。
var asianNodeHints = []string{
	"香港", "hk", "hongkong", "hong kong",
	"日本", "jp", "japan", "东京", "大阪",
	"新加坡", "sg", "singapore",
	"台湾", "tw", "taiwan", "台北",
	"韩国", "kr", "korea", "首尔",
	"马来", "my", "malaysia", "泰国", "th", "thailand", "越南", "vn", "vietnam",
	"印度", "in", "india", "菲律宾", "ph", "印尼", "id", "indonesia",
}

// isAsianNode 判断节点名是否指向亚洲地区。
func isAsianNode(name string) bool {
	lowered := strings.ToLower(name)
	for _, hint := range asianNodeHints {
		if strings.Contains(lowered, hint) {
			return true
		}
	}
	return false
}

// preferredProxyNodeIDs 按「健康优先 + 亚洲优先」给出候选出口节点 id。
//
// 排序理由（两条都是实测教训）：① 不通的节点排最后——它会把"登录页打不开 / 采集 EOF"直接带给用户；
// ② 亚洲节点排前面——用户明确要求"尽量用亚洲的，快一点"。从未探过的节点算"未知"，排在健康节点之后、
// 不健康节点之前（不拿"没探过"当"不可用"，否则刚导入的节点会被自己的结论锁死）。
func preferredProxyNodeIDs(limit int) []int {
	var nodes []model.ProxyNode
	_ = db.GetDB().Model(&model.ProxyNode{}).
		Where("enabled = ? AND local_port > 0", true).Order("id asc").Find(&nodes).Error
	var healthyAsian, healthyOther, unknown, unhealthy []int
	for _, node := range nodes {
		switch {
		case !node.LastProbeOK && node.LastProbeAt != nil:
			unhealthy = append(unhealthy, node.ID)
		case node.LastProbeAt == nil:
			unknown = append(unknown, node.ID)
		case isAsianNode(node.Name):
			healthyAsian = append(healthyAsian, node.ID)
		default:
			healthyOther = append(healthyOther, node.ID)
		}
	}
	ordered := append(append(append(healthyAsian, healthyOther...), unknown...), unhealthy...)
	if limit > 0 && len(ordered) > limit {
		ordered = ordered[:limit]
	}
	return ordered
}
