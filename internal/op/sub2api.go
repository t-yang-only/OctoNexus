package op

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"github.com/t-yang-only/OctoNexus/internal/secret"
	"gorm.io/gorm"
)

var (
	ErrSub2APIAccountNotFound = gorm.ErrRecordNotFound
)

const sub2apiSecretAAD = "sub2api:account"

// isSensitiveHeader 判定是否为鉴权与敏感请求头
func isSensitiveHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "authorization" ||
		lower == "cookie" ||
		lower == "x-api-key" ||
		lower == "api-key" ||
		lower == "x-auth-token" ||
		lower == "token" ||
		lower == "proxy-authorization"
}

// Sub2APIAccountCreateRequest 创建请求
type Sub2APIAccountCreateRequest struct {
	Name        string                   `json:"name" binding:"required"`
	Platform    model.Sub2APIPlatform    `json:"platform" binding:"required"`
	AccountType model.Sub2APIAccountType `json:"account_type"`
	BaseURL     string                   `json:"base_url" binding:"required"`
	AccountMode string                   `json:"account_mode"`
	APIProtocol string                   `json:"api_protocol"`
	Enabled     *bool                    `json:"enabled"`
	ProxyNodeID int                      `json:"proxy_node_id"`
	RateLimit   int                      `json:"rate_limit"`
	ApiKey      string                   `json:"api_key"`
	Headers     map[string]string        `json:"headers"`
	Notes       string                   `json:"notes"`
	SyncChannel bool                     `json:"sync_channel"` // 是否同步物化为渠道
}

// Sub2APIAccountUpdateRequest 更新请求
type Sub2APIAccountUpdateRequest struct {
	Name        *string           `json:"name"`
	BaseURL     *string           `json:"base_url"`
	AccountMode *string           `json:"account_mode"`
	APIProtocol *string           `json:"api_protocol"`
	Enabled     *bool             `json:"enabled"`
	ProxyNodeID *int              `json:"proxy_node_id"`
	RateLimit   *int              `json:"rate_limit"`
	ApiKey      *string           `json:"api_key"`
	Headers     map[string]string `json:"headers"`
	Notes       *string           `json:"notes"`
	SyncChannel *bool             `json:"sync_channel"`
}

// Sub2APIAccountList 获取列表
func Sub2APIAccountList(ctx context.Context) ([]model.Sub2APIAccount, error) {
	conn := db.GetDB()
	if conn == nil {
		return nil, fmt.Errorf("数据库连接不可用")
	}
	var list []model.Sub2APIAccount
	err := conn.Order("id desc").Find(&list).Error
	return list, err
}

// Sub2APIAccountCreate 创建新账号反代配置并可选自动物化为渠道
func Sub2APIAccountCreate(ctx context.Context, req Sub2APIAccountCreateRequest) (*model.Sub2APIAccount, error) {
	trimmedName := strings.TrimSpace(req.Name)
	if err := model.ValidateSub2APIName(trimmedName); err != nil {
		return nil, err
	}
	if err := model.ValidateSub2APIPlatform(req.Platform); err != nil {
		return nil, err
	}
	if err := model.ValidateSub2APIBaseURL(req.BaseURL); err != nil {
		return nil, err
	}

	acctType := req.AccountType
	if acctType == "" {
		acctType = model.Sub2APIAccountTypeOAuth
	}
	if err := model.ValidateSub2APIAccountType(acctType); err != nil {
		return nil, err
	}

	accountMode := req.AccountMode
	if accountMode == "" {
		accountMode = "payg"
	}
	if err := model.ValidateSub2APIAccountMode(accountMode); err != nil {
		return nil, err
	}

	apiProto := req.APIProtocol
	if apiProto == "" {
		apiProto = "chat_completions"
	}
	if err := model.ValidateSub2APIProtocol(apiProto); err != nil {
		return nil, err
	}

	if err := model.ValidateSub2APIRateLimit(req.RateLimit); err != nil {
		return nil, err
	}

	conn := db.GetDB()
	if conn == nil {
		return nil, fmt.Errorf("数据库连接不可用")
	}

	if req.ProxyNodeID > 0 {
		var count int64
		if err := conn.Model(&model.ProxyNode{}).Where("id = ?", req.ProxyNodeID).Count(&count).Error; err != nil || count == 0 {
			return nil, fmt.Errorf("绑定出口节点(%d)不存在", req.ProxyNodeID)
		}
	}

	var nameCount int64
	if err := conn.Model(&model.Sub2APIAccount{}).Where("name = ?", trimmedName).Count(&nameCount).Error; err == nil && nameCount > 0 {
		return nil, fmt.Errorf("账号别名 %q 已存在，请更换名称", trimmedName)
	}

	sensitiveHeaders := make(map[string]string)
	nonSensitiveHeaders := make(map[string]string)
	for k, v := range req.Headers {
		tk := strings.TrimSpace(k)
		tv := strings.TrimSpace(v)
		if tk == "" || tv == "" {
			continue
		}
		if isSensitiveHeader(tk) {
			sensitiveHeaders[tk] = tv
		} else {
			nonSensitiveHeaders[tk] = tv
		}
	}

	credsData := map[string]any{
		"api_key": strings.TrimSpace(req.ApiKey),
		"headers": sensitiveHeaders,
	}
	credsBytes, _ := json.Marshal(credsData)
	encCreds, err := secret.SealWith(sub2apiSecretAAD, string(credsBytes))
	if err != nil {
		return nil, fmt.Errorf("凭据加密失败: %w", err)
	}

	extraData := map[string]any{
		"headers":               nonSensitiveHeaders,
		"has_sensitive_headers": len(sensitiveHeaders) > 0,
	}
	extraBytes, _ := json.Marshal(extraData)

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	account := &model.Sub2APIAccount{
		Name:        trimmedName,
		Platform:    req.Platform,
		AccountType: acctType,
		BaseURL:     strings.TrimRight(strings.TrimSpace(req.BaseURL), "/"),
		AccountMode: accountMode,
		APIProtocol: apiProto,
		Enabled:     enabled,
		ProxyNodeID: req.ProxyNodeID,
		RateLimit:   req.RateLimit,
		Credentials: encCreds,
		Extra:       string(extraBytes),
		Notes:       strings.TrimSpace(req.Notes),
	}

	if err := conn.Create(account).Error; err != nil {
		return nil, fmt.Errorf("创建 Sub2API 账号失败: %w", err)
	}

	if req.SyncChannel {
		createdChan, err := syncSub2APIAccountToChannel(ctx, conn, account, strings.TrimSpace(req.ApiKey))
		if err != nil {
			_ = conn.Delete(account)
			return nil, fmt.Errorf("创建关联渠道失败: %w", err)
		}
		account.ChannelID = createdChan.ID
		if err := conn.Model(account).Update("channel_id", createdChan.ID).Error; err != nil {
			_ = ChannelDel(createdChan.ID, ctx)
			_ = conn.Delete(account)
			return nil, fmt.Errorf("写入账号关联渠道ID失败: %w", err)
		}
	}

	return account, nil
}

// buildSub2APIChannelDetail 构造渠道物化规格，保证渠道模型、协议授权及安全头配置完整
func buildSub2APIChannelDetail(acct *model.Sub2APIAccount, plainKey string) *model.ChannelDetail {
	channelName := fmt.Sprintf("Sub2API-%s-%s-%d", acct.Platform, acct.Name, acct.ID)
	dialect := model.DialectGeneric
	var defaultModels []string
	var protoMask model.Protocol

	switch acct.APIProtocol {
	case "chat_completions":
		protoMask = model.ProtocolOpenAIChatCompletion
	case "responses":
		protoMask = model.ProtocolOpenAIResponse
	case "anthropic":
		protoMask = model.ProtocolAnthropicMessage
	case "adaptive":
		protoMask = model.ProtocolOpenAIChatCompletion | model.ProtocolOpenAIResponse | model.ProtocolAnthropicMessage
	default:
		protoMask = model.ProtocolOpenAIChatCompletion
	}

	switch acct.Platform {
	case model.Sub2APIPlatformAnthropic:
		dialect = model.DialectGeneric
		defaultModels = []string{"claude-3-7-sonnet-20250219", "claude-3-5-sonnet-20241022", "claude-3-5-haiku-20241022", "claude-3-opus-20240229"}
		if acct.APIProtocol == "anthropic" {
			protoMask = model.ProtocolAnthropicMessage
		}
	case model.Sub2APIPlatformOpenAI:
		dialect = model.DialectGeneric
		defaultModels = []string{"gpt-4o", "gpt-4o-mini", "o1", "o3-mini", "gpt-4-turbo"}
	case model.Sub2APIPlatformGemini:
		dialect = model.DialectGeneric
		defaultModels = []string{"gemini-2.5-pro", "gemini-2.0-flash", "gemini-1.5-pro"}
		protoMask |= model.ProtocolGeminiContents
	case model.Sub2APIPlatformAntigravity:
		dialect = model.DialectAntigravity
		defaultModels = []string{"gemini-pro-agent", "gemini-1.5-pro"}
	case model.Sub2APIPlatformGrok:
		dialect = model.DialectXAI
		defaultModels = []string{"grok-2", "grok-3"}
	case model.Sub2APIPlatformDeepSeek:
		dialect = model.DialectDeepSeek
		defaultModels = []string{"deepseek-chat", "deepseek-reasoner"}
	case model.Sub2APIPlatformKimi:
		dialect = model.DialectMoonshot
		defaultModels = []string{"moonshot-v1-8k", "moonshot-v1-32k", "moonshot-v1-128k"}
	case model.Sub2APIPlatformOpenCode:
		dialect = model.DialectOpenCode
		defaultModels = []string{"opencode-zen", "opencode-go"}
	case model.Sub2APIPlatformCline:
		dialect = model.DialectCline
		defaultModels = []string{"cline-default"}
	default:
		defaultModels = []string{"default"}
	}

	var customHeaders []model.CustomHeader
	var extraMap map[string]any
	if json.Unmarshal([]byte(acct.Extra), &extraMap) == nil {
		if hdrs, ok := extraMap["headers"].(map[string]any); ok {
			for k, v := range hdrs {
				if vs, ok := v.(string); ok && k != "" && vs != "" && !isSensitiveHeader(k) {
					customHeaders = append(customHeaders, model.CustomHeader{
						HeaderKey:   k,
						HeaderValue: vs,
					})
				}
			}
		}
	}
	// 敏感头（Cookie、X-Auth-Token等）解密后以 AES-256 密文写入渠道（库内无明文，但在 relay 转发时安全解密注入）
	if acct.Credentials != "" {
		if plain, err := secret.OpenWith(sub2apiSecretAAD, acct.Credentials); err == nil {
			var credsMap map[string]any
			if json.Unmarshal([]byte(plain), &credsMap) == nil {
				if hdrs, ok := credsMap["headers"].(map[string]any); ok {
					for hk, hv := range hdrs {
						if strings.EqualFold(hk, "authorization") {
							continue // Authorization 由 ChannelKey 单一收口承载，不入 CustomHeader
						}
						if hvs, ok := hv.(string); ok && hk != "" && hvs != "" {
							sealedVal, err := secret.SealWith("channel:header", hvs)
							if err == nil {
								customHeaders = append(customHeaders, model.CustomHeader{
									HeaderKey:   hk,
									HeaderValue: sealedVal,
								})
							}
						}
					}
				}
			}
		}
	}

	grants := make([]model.ChannelGrantConfig, 0, len(defaultModels))
	for _, m := range defaultModels {
		grants = append(grants, model.ChannelGrantConfig{
			ModelName: m,
			KeyName:   "default",
			Protocols: protoMask,
		})
	}

	enabled := acct.Enabled
	detail := &model.ChannelDetail{
		ID: acct.ChannelID,
		ChannelConfig: model.ChannelConfig{
			Name:         channelName,
			Dialect:      dialect,
			Enabled:      enabled,
			BaseURL:      acct.BaseURL,
			ProxyNodeID:  acct.ProxyNodeID,
			CustomHeader: customHeaders,
		},
		Keys: []model.ChannelKeyInput{
			{Name: "default", Key: plainKey, Enabled: &enabled, ProxyNodeID: &acct.ProxyNodeID},
		},
		Models: defaultModels,
		Grants: grants,
	}

	return detail
}

// syncSub2APIAccountToChannel 将反代账号物化注册为系统渠道并补齐模型与授权
func syncSub2APIAccountToChannel(ctx context.Context, conn *gorm.DB, acct *model.Sub2APIAccount, plainKey string) (*model.ChannelDetail, error) {
	detail := buildSub2APIChannelDetail(acct, plainKey)
	created, err := ChannelCreate(detail, ctx)
	if err != nil {
		return nil, err
	}
	// 守住 R3-F7: 消除 Channel.Enabled 的 GORM default:true 覆盖
	if !acct.Enabled {
		_ = ChannelEnabled(created.ID, false, ctx)
		created.Enabled = false
	}
	return created, nil
}

// Sub2APIAccountUpdate 更新账号配置并深度联动物化渠道
func Sub2APIAccountUpdate(ctx context.Context, id int, req Sub2APIAccountUpdateRequest) (*model.Sub2APIAccount, error) {
	conn := db.GetDB()
	if conn == nil {
		return nil, fmt.Errorf("数据库连接不可用")
	}

	var acct model.Sub2APIAccount
	if err := conn.First(&acct, id).Error; err != nil {
		return nil, ErrSub2APIAccountNotFound
	}

	// 1. 预校验全部入参
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if err := model.ValidateSub2APIName(trimmed); err != nil {
			return nil, err
		}
		var nameCount int64
		if err := conn.Model(&model.Sub2APIAccount{}).Where("name = ? AND id != ?", trimmed, id).Count(&nameCount).Error; err == nil && nameCount > 0 {
			return nil, fmt.Errorf("账号别名 %q 已存在，请更换名称", trimmed)
		}
		acct.Name = trimmed
	}
	if req.BaseURL != nil {
		if err := model.ValidateSub2APIBaseURL(*req.BaseURL); err != nil {
			return nil, err
		}
		acct.BaseURL = strings.TrimRight(strings.TrimSpace(*req.BaseURL), "/")
	}
	if req.AccountMode != nil {
		if err := model.ValidateSub2APIAccountMode(*req.AccountMode); err != nil {
			return nil, err
		}
		acct.AccountMode = *req.AccountMode
	}
	if req.APIProtocol != nil {
		if err := model.ValidateSub2APIProtocol(*req.APIProtocol); err != nil {
			return nil, err
		}
		acct.APIProtocol = *req.APIProtocol
	}
	if req.Enabled != nil {
		acct.Enabled = *req.Enabled
	}
	if req.ProxyNodeID != nil {
		if *req.ProxyNodeID > 0 {
			var count int64
			if err := conn.Model(&model.ProxyNode{}).Where("id = ?", *req.ProxyNodeID).Count(&count).Error; err != nil || count == 0 {
				return nil, fmt.Errorf("绑定出口节点(%d)不存在", *req.ProxyNodeID)
			}
		}
		acct.ProxyNodeID = *req.ProxyNodeID
	}
	if req.RateLimit != nil {
		if err := model.ValidateSub2APIRateLimit(*req.RateLimit); err != nil {
			return nil, err
		}
		acct.RateLimit = *req.RateLimit
	}
	if req.Notes != nil {
		acct.Notes = strings.TrimSpace(*req.Notes)
	}

	// 2. 仅在显式传入密钥或头时重写 Credentials（解决 R3-F5：防坏密文在改 Notes 时被静默抹除）
	plainKey := ""
	if req.ApiKey != nil || req.Headers != nil {
		sensitiveHeaders := make(map[string]string)
		if acct.Credentials != "" {
			plain, err := secret.OpenWith(sub2apiSecretAAD, acct.Credentials)
			if err != nil {
				return nil, fmt.Errorf("原有凭据解密失败，禁止覆盖: %w", err)
			}
			var credsMap map[string]any
			if json.Unmarshal([]byte(plain), &credsMap) == nil {
				if k, ok := credsMap["api_key"].(string); ok {
					plainKey = k
				}
				if hdrs, ok := credsMap["headers"].(map[string]any); ok {
					for hk, hv := range hdrs {
						if hvs, ok := hv.(string); ok {
							sensitiveHeaders[hk] = hvs
						}
					}
				}
			}
		}

		if req.ApiKey != nil {
			plainKey = strings.TrimSpace(*req.ApiKey)
		}

		nonSensitiveHeaders := make(map[string]string)
		if acct.Extra != "" {
			var extraMap map[string]any
			if json.Unmarshal([]byte(acct.Extra), &extraMap) == nil {
				if hdrs, ok := extraMap["headers"].(map[string]any); ok {
					for hk, hv := range hdrs {
						if hvs, ok := hv.(string); ok && !isSensitiveHeader(hk) {
							nonSensitiveHeaders[hk] = hvs
						}
					}
				}
			}
		}

		if req.Headers != nil {
			sensitiveHeaders = make(map[string]string)
			nonSensitiveHeaders = make(map[string]string)
			for k, v := range req.Headers {
				tk := strings.TrimSpace(k)
				tv := strings.TrimSpace(v)
				if tk == "" || tv == "" {
					continue
				}
				if isSensitiveHeader(tk) {
					sensitiveHeaders[tk] = tv
				} else {
					nonSensitiveHeaders[tk] = tv
				}
			}
		}

		credsData := map[string]any{
			"api_key": plainKey,
			"headers": sensitiveHeaders,
		}
		credsBytes, _ := json.Marshal(credsData)
		encCreds, err := secret.SealWith(sub2apiSecretAAD, string(credsBytes))
		if err != nil {
			return nil, fmt.Errorf("加密更新凭据失败: %w", err)
		}
		acct.Credentials = encCreds

		extraData := map[string]any{
			"headers":               nonSensitiveHeaders,
			"has_sensitive_headers": len(sensitiveHeaders) > 0,
		}
		extraBytes, _ := json.Marshal(extraData)
		acct.Extra = string(extraBytes)
	}

	// 3. 联动解绑或新建物化渠道（解决 R3-F4：删除出错明确阻断）
	if req.SyncChannel != nil {
		if *req.SyncChannel && acct.ChannelID == 0 {
			createdChan, err := syncSub2APIAccountToChannel(ctx, conn, &acct, plainKey)
			if err != nil {
				return nil, fmt.Errorf("物化新建渠道失败: %w", err)
			}
			acct.ChannelID = createdChan.ID
		} else if !*req.SyncChannel && acct.ChannelID > 0 {
			if err := ChannelDel(acct.ChannelID, ctx); err != nil {
				if !strings.Contains(err.Error(), "not found") {
					return nil, fmt.Errorf("解绑删除关联渠道(%d)失败: %w", acct.ChannelID, err)
				}
			}
			acct.ChannelID = 0
		}
	}

	// 4. 先落库保存账号行（解决 R3-F3：账号落库成功才同步渠道，避免半更新）
	if err := conn.Save(&acct).Error; err != nil {
		return nil, fmt.Errorf("保存账号失败: %w", err)
	}

	// 5. 增量更新已物化渠道（解决 R3-F1：保留手工模型、计费与自定义端点，不全量抹平）
	if acct.ChannelID > 0 {
		existing, err := ChannelDetailGet(acct.ChannelID)
		if err == nil {
			existing.BaseURL = acct.BaseURL
			existing.Enabled = acct.Enabled
			existing.ProxyNodeID = acct.ProxyNodeID
			existing.Name = fmt.Sprintf("Sub2API-%s-%s-%d", acct.Platform, acct.Name, acct.ID)

			if plainKey != "" {
				foundKey := false
				for i := range existing.Keys {
					if existing.Keys[i].Name == "default" {
						existing.Keys[i].Key = plainKey
						existing.Keys[i].Enabled = &acct.Enabled
						existing.Keys[i].ProxyNodeID = &acct.ProxyNodeID
						foundKey = true
						break
					}
				}
				if !foundKey {
					enabled := acct.Enabled
					existing.Keys = append(existing.Keys, model.ChannelKeyInput{
						Name:        "default",
						Key:         plainKey,
						Enabled:     &enabled,
						ProxyNodeID: &acct.ProxyNodeID,
					})
				}
			}

			// 仅在明确修改头时同步非敏感头
			if req.Headers != nil {
				var customHeaders []model.CustomHeader
				for k, v := range req.Headers {
					if !isSensitiveHeader(k) && strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
						customHeaders = append(customHeaders, model.CustomHeader{
							HeaderKey:   strings.TrimSpace(k),
							HeaderValue: strings.TrimSpace(v),
						})
					}
				}
				existing.CustomHeader = customHeaders
			}

			if _, err := ChannelUpdate(&existing, ctx); err != nil {
				return nil, fmt.Errorf("联动物化渠道更新失败: %w", err)
			}
		}
	}

	return &acct, nil
}

// Sub2APIAccountDelete 删除账号配置及关联渠道
func Sub2APIAccountDelete(ctx context.Context, id int) error {
	conn := db.GetDB()
	if conn == nil {
		return fmt.Errorf("数据库连接不可用")
	}

	var acct model.Sub2APIAccount
	if err := conn.First(&acct, id).Error; err != nil {
		return ErrSub2APIAccountNotFound
	}

	if acct.ChannelID > 0 {
		if err := ChannelDel(acct.ChannelID, ctx); err != nil {
			if !strings.Contains(err.Error(), "not found") {
				return fmt.Errorf("删除关联渠道(%d)失败: %w", acct.ChannelID, err)
			}
		}
	}

	res := conn.Delete(&acct)
	if res.Error != nil {
		return res.Error
	}
	return nil
}

// Sub2APIAccountProbe 探测反代账号连通性
func Sub2APIAccountProbe(ctx context.Context, id int) (string, error) {
	conn := db.GetDB()
	if conn == nil {
		return "", fmt.Errorf("数据库连接不可用")
	}
	var account model.Sub2APIAccount
	if err := conn.First(&account, id).Error; err != nil {
		return "", err
	}

	// R10-F1: 与 Sub2APIAccountSyncModels 的无凭据守卫对齐 (R5-F3 的同款交叉点):
	// 无凭据时若照样发出请求, 上游 401 只会回一句 "上游返回错误状态: HTTP 401",
	// 运维看不出是该补凭据。probe 与 sync 走同一套凭据语义, 此处必须在入口拦截。
	if account.Credentials == "" {
		return "", fmt.Errorf("该账号未配置凭据，请先编辑账号补全 API Key 后再探测")
	}

	trimmedBase := strings.TrimRight(strings.TrimSpace(account.BaseURL), "/")
	probeURL := strings.TrimSuffix(trimmedBase, "/v1") + "/v1/models"

	var authBearer string
	probeHeaders := make(map[string]string)

	if account.Credentials != "" {
		plainCreds, err := secret.OpenWith(sub2apiSecretAAD, account.Credentials)
		if err != nil {
			return "", fmt.Errorf("凭据解密失败: %w", err)
		}
		var credsMap map[string]any
		if err := json.Unmarshal([]byte(plainCreds), &credsMap); err != nil {
			return "", fmt.Errorf("凭据反序列化失败: %w", err)
		}
		if k, ok := credsMap["api_key"].(string); ok && k != "" {
			authBearer = k
		}
		if hdrs, ok := credsMap["headers"].(map[string]any); ok {
			for hk, hv := range hdrs {
				if hvs, ok := hv.(string); ok && hk != "" && hvs != "" {
					probeHeaders[hk] = hvs
				}
			}
		}
	}

	if account.Extra != "" {
		var extraMap map[string]any
		if err := json.Unmarshal([]byte(account.Extra), &extraMap); err == nil {
			if hdrs, ok := extraMap["headers"].(map[string]any); ok {
				for hk, hv := range hdrs {
					if hvs, ok := hv.(string); ok && hk != "" && hvs != "" {
						probeHeaders[hk] = hvs
					}
				}
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return "", fmt.Errorf("构建探测请求失败: %w", err)
	}

	for k, v := range probeHeaders {
		req.Header.Set(k, v)
	}
	if authBearer != "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+authBearer)
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}
	if account.ProxyNodeID > 0 {
		endpoint, err := ProxyNodeEndpoint(account.ProxyNodeID)
		if err != nil {
			return "", fmt.Errorf("绑定出口节点(%d)不可用: %w", account.ProxyNodeID, err)
		}
		parsedProxy, err := url.Parse(endpoint)
		if err != nil {
			return "", fmt.Errorf("解析出口节点地址失败: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsedProxy)
	}

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("探测目标连通异常: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("上游返回错误状态: HTTP %d (%s)", resp.StatusCode, resp.Status)
	}

	return fmt.Sprintf("HTTP %d (%s)", resp.StatusCode, resp.Status), nil
}

// syncModelsLocks 按渠道 ID 分锁, 保护动态模型同步的读-改-写窗口 (R5-F1)。
// 单二进制部署下进程内互斥即覆盖全部并发源; LoadOrStore 保证同一渠道拿到同一把锁。
var syncModelsLocks sync.Map

func syncModelsLockFor(channelID int) *sync.Mutex {
	v, _ := syncModelsLocks.LoadOrStore(channelID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Sub2APISyncModelsResult 动态模型同步结果。
// Stale 是「上游已不再返回、但渠道里仍有」的模型: 只报告不删除 —— 渠道模型可能含
// 人工手填与历史授权(R3-F1 的保护教训), 自动清理会误伤, 必须由运维在面板确认后处理。
type Sub2APISyncModelsResult struct {
	Models []string `json:"models"` // 同步后渠道全量模型
	Added  []string `json:"added"`  // 本次新并入的模型
	Stale  []string `json:"stale"`  // 上游已下架但渠道仍有的模型(待人工确认)
}

// Sub2APIAccountSyncModels 动态从上游 /v1/models 拉取真实可用模型并增量同步至物化渠道和自动分组。
// 返回本次新增(Added)与上游已下架(Stale)的可观测结果; Stale 只报告不自动删除。
func Sub2APIAccountSyncModels(ctx context.Context, id int) (*Sub2APISyncModelsResult, error) {
	conn := db.GetDB()
	if conn == nil {
		return nil, fmt.Errorf("数据库连接不可用")
	}
	var account model.Sub2APIAccount
	if err := conn.First(&account, id).Error; err != nil {
		return nil, ErrSub2APIAccountNotFound
	}
	if account.ChannelID == 0 {
		return nil, fmt.Errorf("该账号尚未物化为渠道，请先开启物化渠道")
	}

	// R5-F3: 无凭据时直接给出可执行错误, 不再静默发出无认证请求后让上游 401 替我们报错
	if account.Credentials == "" {
		return nil, fmt.Errorf("该账号未配置凭据，请先编辑账号补全 API Key 后再同步模型")
	}

	// R8-F1: 账号级最小间隔保护。误连打(双击/多标签页)会把同样的请求反复打到上游,
	// 间隔内直接拒绝并告知剩余等待, 不消耗上游配额。零值 LastSyncAt(从未同步)不拦截。
	const minSyncInterval = 10 * time.Second
	if !account.LastSyncAt.IsZero() {
		if wait := minSyncInterval - time.Since(account.LastSyncAt); wait > 0 {
			return nil, fmt.Errorf("距上次成功同步不足 %d 秒，请 %.0f 秒后重试（防止误连打反复请求上游）",
				int(minSyncInterval.Seconds()), wait.Seconds())
		}
	}

	// R5-F1: 渠道级互斥保护读-改-写窗口: 并发 sync 或 sync 与面板编辑同时进行时,
	// 后写者会静默覆盖先写者的合并结果。按渠道 ID 分锁, 单二进制部署即覆盖全部并发源。
	syncLock := syncModelsLockFor(account.ChannelID)
	syncLock.Lock()
	defer syncLock.Unlock()

	trimmedBase := strings.TrimRight(strings.TrimSpace(account.BaseURL), "/")
	modelsURL := strings.TrimSuffix(trimmedBase, "/v1") + "/v1/models"

	var authBearer string
	requestHeaders := make(map[string]string)

	{
		plainCreds, err := secret.OpenWith(sub2apiSecretAAD, account.Credentials)
		if err != nil {
			return nil, fmt.Errorf("凭据解密失败: %w", err)
		}
		var credsMap map[string]any
		if err := json.Unmarshal([]byte(plainCreds), &credsMap); err != nil {
			return nil, fmt.Errorf("凭据解析失败: %w", err)
		}
		if k, ok := credsMap["api_key"].(string); ok && k != "" {
			authBearer = k
		}
		if hdrs, ok := credsMap["headers"].(map[string]any); ok {
			for hk, hv := range hdrs {
				if hvs, ok := hv.(string); ok && hk != "" && hvs != "" {
					requestHeaders[hk] = hvs
				}
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建模型拉取请求失败: %w", err)
	}

	for k, v := range requestHeaders {
		req.Header.Set(k, v)
	}
	if authBearer != "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+authBearer)
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}
	if account.ProxyNodeID > 0 {
		endpoint, err := ProxyNodeEndpoint(account.ProxyNodeID)
		if err != nil {
			return nil, fmt.Errorf("绑定出口节点不可用: %w", err)
		}
		parsedProxy, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("解析出口节点地址失败: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsedProxy)
	}

	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求上游模型列表失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("上游模型端点返回错误 HTTP %d (%s)", resp.StatusCode, resp.Status)
	}

	type modelEntry struct {
		ID string `json:"id"`
	}
	type modelListResp struct {
		Data []modelEntry `json:"data"`
	}

	var listData modelListResp
	if err := json.NewDecoder(resp.Body).Decode(&listData); err != nil {
		return nil, fmt.Errorf("解析上游模型 JSON 失败: %w", err)
	}

	if len(listData.Data) == 0 {
		return nil, fmt.Errorf("上游模型列表为空")
	}

	var upstreamModels []string
	seen := make(map[string]struct{})
	for _, m := range listData.Data {
		tid := strings.TrimSpace(m.ID)
		if tid != "" {
			if _, ok := seen[tid]; !ok {
				seen[tid] = struct{}{}
				upstreamModels = append(upstreamModels, tid)
			}
		}
	}

	// 增量同步至关联物化渠道
	existingChan, err := ChannelDetailGet(account.ChannelID)
	if err != nil {
		return nil, fmt.Errorf("读取关联渠道(%d)失败: %w", account.ChannelID, err)
	}

	protoMask := model.ProtocolOpenAIChatCompletion
	switch account.APIProtocol {
	case "responses":
		protoMask = model.ProtocolOpenAIResponse
	case "anthropic":
		protoMask = model.ProtocolAnthropicMessage
	case "adaptive":
		protoMask = model.ProtocolOpenAIChatCompletion | model.ProtocolOpenAIResponse | model.ProtocolAnthropicMessage
	}

	// 合并并去重现有模型与上游拉取的动态模型
	modelMap := make(map[string]struct{})
	for _, m := range existingChan.Models {
		modelMap[m] = struct{}{}
	}
	var addedModels []string
	for _, m := range upstreamModels {
		if _, exists := modelMap[m]; !exists {
			existingChan.Models = append(existingChan.Models, m)
			modelMap[m] = struct{}{}
			addedModels = append(addedModels, m)
		}
	}

	// 下架判据的基准是「上一次成功同步时上游返回的集合」而非「渠道当前模型」:
	// 物化预置与运维手填的模型从未进入过该集合, 用渠道存量当基准会把它们误报成下架。
	// 首次同步(无基准)不报 Stale —— 没有"上次"就无从判断"下架"。
	var lastSyncModels []string
	if account.LastSyncModels != "" {
		if err := json.Unmarshal([]byte(account.LastSyncModels), &lastSyncModels); err != nil {
			lastSyncModels = nil // 坏基准按无基准处理, 不阻断同步主流程
		}
	}
	lastSyncSet := make(map[string]struct{}, len(lastSyncModels))
	for _, m := range lastSyncModels {
		lastSyncSet[m] = struct{}{}
	}
	upstreamSet := make(map[string]struct{}, len(upstreamModels))
	for _, m := range upstreamModels {
		upstreamSet[m] = struct{}{}
	}
	var staleModels []string
	for _, m := range lastSyncModels {
		if _, gone := upstreamSet[m]; !gone {
			staleModels = append(staleModels, m)
		}
	}

	// 确保所有合并后的模型均包含授权
	// R5-F2: grant 必须引用真实存在的凭据名。取渠道现有凭据(ChannelDetailGet 按名称排序, 取值确定),
	// 不再硬编码 default —— 用户若在面板删掉/改名了物化时创建的 default 凭据,
	// ChannelUpdate 会以 "channel key %q does not belong to channel" 拒绝且看不出与该从哪补。
	grantKeyName := ""
	for _, k := range existingChan.Keys {
		if k.Name != "" {
			grantKeyName = k.Name
			break
		}
	}
	if grantKeyName == "" {
		return nil, fmt.Errorf("关联渠道(%d)没有可用凭据，请先在面板为该渠道添加渠道 Key 后重试", account.ChannelID)
	}
	grantMap := make(map[string]struct{})
	for _, g := range existingChan.Grants {
		grantMap[g.ModelName] = struct{}{}
	}
	for _, m := range existingChan.Models {
		if _, ok := grantMap[m]; !ok {
			existingChan.Grants = append(existingChan.Grants, model.ChannelGrantConfig{
				ModelName: m,
				KeyName:   grantKeyName,
				Protocols: protoMask,
			})
			grantMap[m] = struct{}{}
		}
	}

	if _, err := ChannelUpdate(&existingChan, ctx); err != nil {
		return nil, fmt.Errorf("更新渠道模型授权失败: %w", err)
	}

	// 成功后回写本次上游集合, 作为下一次同步的下架判据基准
	if payload, err := json.Marshal(upstreamModels); err == nil {
		if err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", account.ID).
			Update("last_sync_models", string(payload)).Error; err != nil {
			return nil, fmt.Errorf("回写同步基准失败: %w", err)
		}
	}
	// 回写成功时间戳, 作为最小间隔保护的基准; 同一次写回可观测摘要(R9-F1)
	summaryPayload, err := json.Marshal(map[string]any{
		"added_count": len(addedModels),
		"stale_count": len(staleModels),
		"stale":       staleModels,
	})
	if err != nil {
		return nil, fmt.Errorf("生成同步摘要失败: %w", err)
	}
	if err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", account.ID).
		Updates(map[string]any{
			"last_sync_at":      time.Now(),
			"last_sync_summary": string(summaryPayload),
		}).Error; err != nil {
		return nil, fmt.Errorf("回写同步时间与摘要失败: %w", err)
	}

	return &Sub2APISyncModelsResult{
		Models: existingChan.Models,
		Added:  addedModels,
		Stale:  staleModels,
	}, nil
}
