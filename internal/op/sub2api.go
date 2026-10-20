package op

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// Sub2APIAccountCreateRequest 创建请求
type Sub2APIAccountCreateRequest struct {
	Name        string                   `json:"name" binding:"required"`
	Platform    model.Sub2APIPlatform    `json:"platform" binding:"required"`
	AccountType model.Sub2APIAccountType `json:"account_type"`
	BaseURL     string                   `json:"base_url" binding:"required"`
	AccountMode string                   `json:"account_mode"`
	APIProtocol string                   `json:"api_protocol"`
	Enabled     bool                     `json:"enabled"`
	ProxyNodeID int                      `json:"proxy_node_id"`
	RateLimit   int                      `json:"rate_limit"`
	ApiKey      string                   `json:"api_key"`
	Headers     map[string]string        `json:"headers"`
	Notes       string                   `json:"notes"`
	SyncChannel bool                     `json:"sync_channel"` // 是否同步物化为渠道
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
	if err := model.ValidateSub2APIPlatform(req.Platform); err != nil {
		return nil, err
	}
	if err := model.ValidateSub2APIBaseURL(req.BaseURL); err != nil {
		return nil, err
	}
	conn := db.GetDB()
	if conn == nil {
		return nil, fmt.Errorf("数据库连接不可用")
	}

	credsData := map[string]any{
		"api_key": strings.TrimSpace(req.ApiKey),
	}
	credsBytes, _ := json.Marshal(credsData)
	encCreds, err := secret.SealWith(sub2apiSecretAAD, string(credsBytes))
	if err != nil {
		return nil, fmt.Errorf("凭据加密失败: %w", err)
	}

	extraData := map[string]any{
		"headers": req.Headers,
	}
	extraBytes, _ := json.Marshal(extraData)

	accountMode := req.AccountMode
	if accountMode == "" {
		accountMode = "payg"
	}
	apiProto := req.APIProtocol
	if apiProto == "" {
		apiProto = "chat_completions"
	}
	acctType := req.AccountType
	if acctType == "" {
		acctType = model.Sub2APIAccountTypeOAuth
	}

	account := &model.Sub2APIAccount{
		Name:        strings.TrimSpace(req.Name),
		Platform:    req.Platform,
		AccountType: acctType,
		BaseURL:     strings.TrimRight(strings.TrimSpace(req.BaseURL), "/"),
		AccountMode: accountMode,
		APIProtocol: apiProto,
		Enabled:     req.Enabled,
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
		_ = syncSub2APIAccountToChannel(ctx, conn, account, strings.TrimSpace(req.ApiKey))
	}

	return account, nil
}

// syncSub2APIAccountToChannel 将反代账号物化注册为系统渠道，无缝接入选路与统计体系
func syncSub2APIAccountToChannel(ctx context.Context, conn *gorm.DB, acct *model.Sub2APIAccount, plainKey string) error {
	channelName := fmt.Sprintf("Sub2API-%s-%s", acct.Platform, acct.Name)
	dialect := model.DialectGeneric
	if acct.Platform == model.Sub2APIPlatformAntigravity {
		dialect = model.DialectAntigravity
	}

	var customHeaders []model.CustomHeader
	var extraMap map[string]any
	if json.Unmarshal([]byte(acct.Extra), &extraMap) == nil {
		if hdrs, ok := extraMap["headers"].(map[string]any); ok {
			for k, v := range hdrs {
				if vs, ok := v.(string); ok && k != "" && vs != "" {
					customHeaders = append(customHeaders, model.CustomHeader{
						HeaderKey:   k,
						HeaderValue: vs,
					})
				}
			}
		}
	}

	enabled := true
	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:         channelName,
			Dialect:      dialect,
			Enabled:      acct.Enabled,
			BaseURL:      acct.BaseURL,
			ProxyNodeID:  acct.ProxyNodeID,
			CustomHeader: customHeaders,
		},
		Keys: []model.ChannelKeyInput{
			{Name: "default", Key: plainKey, Enabled: &enabled},
		},
	}

	created, err := ChannelCreate(detail, ctx)
	if err == nil && created != nil {
		acct.ChannelID = created.ID
		conn.Model(acct).Update("channel_id", created.ID)
	}
	return err
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
		_ = ChannelDel(acct.ChannelID, ctx)
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

	client := &http.Client{Timeout: 10 * time.Second}
	probeURL := account.BaseURL + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return "", fmt.Errorf("构建探测请求失败: %w", err)
	}

	if account.Credentials != "" {
		plainCreds, err := secret.OpenWith(sub2apiSecretAAD, account.Credentials)
		if err == nil {
			var credsMap map[string]any
			if json.Unmarshal([]byte(plainCreds), &credsMap) == nil {
				if k, ok := credsMap["api_key"].(string); ok && k != "" {
					req.Header.Set("Authorization", "Bearer "+k)
				}
			}
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("探测目标连通异常: %w", err)
	}
	defer resp.Body.Close()

	return fmt.Sprintf("HTTP %d (%s)", resp.StatusCode, resp.Status), nil
}
