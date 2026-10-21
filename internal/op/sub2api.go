package op

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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