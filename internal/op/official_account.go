package op

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// T-acct-001 官方账号 OAuth 接入（163 清单全五步）。
// 口径：
// ① authorize 签发 PKCE(S256) + 一次性 state，pending 账号先落库；
// ② state 内存表 TTL 120s，条件消费（取走即删），过期/未知/重放三分量拒绝，
//    重启丢在途 state 可接受（重新 authorize 即可）；
// ③ code→token 交换与套餐/窗口读取都走可注入客户端（生产=真实 HTTP，测试=桩），
//    one_time_code 绝不落库；
// ④ AccessCipher/RefreshCipher AES-256-GCM 加密落库，密钥取环境变量
//    OCTOPUS_OFFICIAL_KEY（任意熵 SHA-256 派生 32B），未配置时 authorize 直接拒绝，
//    杜绝明文静默入库；响应 JSON 侧靠 `json:"-"` 双保险不出现密文与明文。

var (
	// ErrOfficialKeyMissing 表示未配置凭据加密密钥，拒绝发起接入。
	ErrOfficialKeyMissing = errors.New("official account cipher key not configured")
	// ErrOfficialStateUnknown 表示 state 不存在（含签发前伪造）。
	ErrOfficialStateUnknown = errors.New("oauth state unknown")
	// ErrOfficialStateExpired 表示 state 已过期。
	ErrOfficialStateExpired = errors.New("oauth state expired")
	// ErrOfficialStateConsumed 表示 state 已被消费（重放）。
	ErrOfficialStateConsumed = errors.New("oauth state already consumed")
	// ErrOfficialAccountNotFound 表示账号不存在。
	ErrOfficialAccountNotFound = gorm.ErrRecordNotFound
)

const (
	officialStateTTL   = 120 * time.Second
	officialReadTO     = 10 * time.Second
	officialResponseMB = 1 << 20
)

// OfficialOAuthEndpoints 是三类官方的授权/换码/元数据端点集合；
// 变量形态允许测试整体替换，生产默认走官方域名。
type OfficialOAuthEndpoints struct {
	Authorize string // 授权页（浏览器跳转）。
	Token     string // code→token 交换。
	Meta      string // 套餐/健康/窗口读取（占位 {account} 由实现方处理）。
}

var OfficialEndpoints = map[model.OfficialAccountProvider]OfficialOAuthEndpoints{
	model.OfficialAccountProviderOpenAI: {Authorize: "https://auth.openai.com/oauth/authorize", Token: "https://auth.openai.com/oauth/token", Meta: "https://chatgpt.com/backend-api/"},
	model.OfficialAccountProviderGemini: {Authorize: "https://accounts.google.com/o/oauth2/v2/auth", Token: "https://oauth2.googleapis.com/token", Meta: "https://generativelanguage.googleapis.com/"},
	model.OfficialAccountProviderClaude: {Authorize: "https://claude.ai/oauth/authorize", Token: "https://console.anthropic.com/v1/oauth/token", Meta: "https://claude.ai/"},
}

// OfficialTokenBundle 是换码结果：明文只在内存中短暂存在，加密后即丢弃。
type OfficialTokenBundle struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64  // 秒；<=0 表示无过期机制。
	ExternalName string // 官方侧账号标识（邮箱/组织名），空则由调用方兜底。
}

// TokenExchanger 把 one_time_code 换成 token；可注入桩实现供单测。
type TokenExchanger interface {
	Exchange(provider model.OfficialAccountProvider, code, verifier string) (OfficialTokenBundle, error)
}

// UsageSnapshot 是套餐/健康/窗口读取结果。
type UsageSnapshot struct {
	PlanTier string
	Window5H string
	Window7D string
	Healthy  bool
}

// UsageReader 读取官方侧元数据快照；可注入桩实现供单测。
type UsageReader interface {
	Read(provider model.OfficialAccountProvider, accessToken string) (UsageSnapshot, error)
}

var (
	officialExchanger TokenExchanger = &httpTokenExchanger{}
	officialReader    UsageReader    = &httpUsageReader{}
)

// SetOfficialOAuthClientsForTest 注入换码/读侧桩，仅测试接线用。
func SetOfficialOAuthClientsForTest(ex TokenExchanger, rd UsageReader) {
	if ex != nil {
		officialExchanger = ex
	}
	if rd != nil {
		officialReader = rd
	}
}

// ---------- PKCE + state ----------

func officialPKCEPair() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate pkce verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

type officialStateEntry struct {
	accountID int
	provider  model.OfficialAccountProvider
	verifier  string
	expiresAt time.Time
}

var officialStates = struct {
	mu   sync.Mutex
	data map[string]officialStateEntry // key = SHA-256(state) hex
}{data: map[string]officialStateEntry{}}

func officialStateHash(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

func putOfficialState(state string, entry officialStateEntry) {
	officialStates.mu.Lock()
	defer officialStates.mu.Unlock()
	officialStates.data[officialStateHash(state)] = entry
}

// consumeOfficialState 条件消费：取走即删，并发下至多一次成功。
// 过期项在命中时删除并回 Expired；未知回 Unknown。
func consumeOfficialState(state string) (officialStateEntry, error) {
	hash := officialStateHash(strings.TrimSpace(state))
	officialStates.mu.Lock()
	defer officialStates.mu.Unlock()
	entry, ok := officialStates.data[hash]
	if !ok {
		return officialStateEntry{}, ErrOfficialStateUnknown
	}
	delete(officialStates.data, hash)
	if time.Now().After(entry.expiresAt) {
		return officialStateEntry{}, ErrOfficialStateExpired
	}
	return entry, nil
}

// ---------- 凭据加密 (AES-256-GCM) ----------

func officialCipherKey() ([]byte, error) {
	secret := strings.TrimSpace(os.Getenv("OCTOPUS_OFFICIAL_KEY"))
	if secret == "" {
		return nil, ErrOfficialKeyMissing
	}
	sum := sha256.Sum256([]byte(secret))
	return sum[:], nil
}

func officialGCM() (cipher.AEAD, error) {
	key, err := officialCipherKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("official cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// officialEncrypt 输出 base64(nonce|ciphertext)；AAD 绑服务商防跨账号挪用语义。
func officialEncrypt(provider model.OfficialAccountProvider, plain string) (string, error) {
	if plain == "" {
		return "", nil // 无刷新机制的服务商落空串，语义与模型 default:'' 对齐。
	}
	gcm, err := officialGCM()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("official cipher nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), []byte(provider))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func officialDecrypt(provider model.OfficialAccountProvider, enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("official cipher decode: %w", err)
	}
	gcm, err := officialGCM()
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("official cipher truncated")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(provider))
	if err != nil {
		return "", fmt.Errorf("official cipher open: %w", err)
	}
	return string(plain), nil
}

// OfficialStateTTL 暴露 state 有效期供接口回显。
func OfficialStateTTL() time.Duration { return officialStateTTL }

// ---------- 接入流程 ----------

// OfficialAccountAuthorize 发起接入：建 pending 账号 + 签发 state/PKCE，返回授权 URL。
// 加密密钥未配置时直接拒绝，不落半成品行。
func OfficialAccountAuthorize(conn *gorm.DB, provider model.OfficialAccountProvider) (account model.OfficialAccount, authorizeURL, state string, err error) {
	if err = model.ValidateOfficialAccountProvider(provider); err != nil {
		return
	}
	if _, keyErr := officialCipherKey(); keyErr != nil {
		return account, "", "", keyErr
	}
	endpoints, ok := OfficialEndpoints[provider]
	if !ok {
		return account, "", "", fmt.Errorf("no oauth endpoints for provider %q", provider)
	}
	verifier, challenge, err := officialPKCEPair()
	if err != nil {
		return
	}
	stateRaw := make([]byte, 24)
	if _, err = rand.Read(stateRaw); err != nil {
		return account, "", "", fmt.Errorf("generate state: %w", err)
	}
	state = base64.RawURLEncoding.EncodeToString(stateRaw)
	now := time.Now()
	account = model.OfficialAccount{
		Provider:     provider,
		ExternalName: string(provider) + "-pending-" + state[:12],
		Status:       model.OfficialAccountStatusPending,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err = officialAccountDB(conn).Create(&account).Error; err != nil {
		return model.OfficialAccount{}, "", "", fmt.Errorf("persist pending account: %w", err)
	}
	putOfficialState(state, officialStateEntry{accountID: account.ID, provider: provider, verifier: verifier, expiresAt: now.Add(officialStateTTL)})

	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {os.Getenv("OCTOPUS_OFFICIAL_CLIENT_ID_" + strings.ToUpper(string(provider)))},
		"redirect_uri":          {officialRedirectURI()},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	authorizeURL = endpoints.Authorize + "?" + q.Encode()
	return account, authorizeURL, state, nil
}

// OfficialAccountCallback 确认回调：state 条件消费 → 换码 → 密文落库 → active。
// 任何一步失败都不回滚 state（一次消费语义），账号停留 pending。
func OfficialAccountCallback(conn *gorm.DB, accountID int, oneTimeCode, state string) (model.OfficialAccount, error) {
	entry, err := consumeOfficialState(state)
	if err != nil {
		if errors.Is(err, ErrOfficialStateUnknown) {
			// 无法区分"从未签发"与"重放"时统一 Unknown，重放必然先被消费删除。
			return model.OfficialAccount{}, ErrOfficialStateUnknown
		}
		return model.OfficialAccount{}, err
	}
	if entry.accountID != accountID || entry.provider == "" {
		return model.OfficialAccount{}, ErrOfficialStateUnknown
	}
	bundle, err := officialExchanger.Exchange(entry.provider, oneTimeCode, entry.verifier)
	if err != nil {
		return model.OfficialAccount{}, fmt.Errorf("exchange code: %w", err)
	}
	if strings.TrimSpace(bundle.AccessToken) == "" {
		return model.OfficialAccount{}, fmt.Errorf("exchange code: empty access token")
	}
	access, err := officialEncrypt(entry.provider, bundle.AccessToken)
	if err != nil {
		return model.OfficialAccount{}, err
	}
	refresh, err := officialEncrypt(entry.provider, bundle.RefreshToken)
	if err != nil {
		return model.OfficialAccount{}, err
	}
	updates := map[string]any{
		"access_cipher":  access,
		"refresh_cipher": refresh,
		"status":         model.OfficialAccountStatusActive,
		"last_error":     "",
	}
	if bundle.ExternalName != "" {
		updates["external_name"] = bundle.ExternalName
	}
	if bundle.ExpiresIn > 0 {
		expires := time.Now().Add(time.Duration(bundle.ExpiresIn) * time.Second)
		updates["expires_at"] = &expires
	}
	target := officialAccountDB(conn)
	result := target.Model(&model.OfficialAccount{}).Where("id = ? AND provider = ?", accountID, entry.provider).Updates(updates)
	if result.Error != nil {
		return model.OfficialAccount{}, fmt.Errorf("update account: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return model.OfficialAccount{}, ErrOfficialAccountNotFound
	}
	return officialAccountGetOn(conn, accountID)
}

// OfficialAccountReadUsage 读取套餐/健康/窗口快照并回填账号行（解密的 access token 只在内存存在）。
func OfficialAccountReadUsage(conn *gorm.DB, accountID int) (model.OfficialAccount, error) {
	account, err := officialAccountGetOn(conn, accountID)
	if err != nil {
		return account, err
	}
	if account.Status != model.OfficialAccountStatusActive {
		return account, fmt.Errorf("account %d not active (status %q)", accountID, account.Status)
	}
	access, err := officialDecrypt(account.Provider, account.AccessCipher)
	if err != nil {
		return account, err
	}
	snap, err := officialReader.Read(account.Provider, access)
	if err != nil {
		fail := model.OfficialAccount{Status: model.OfficialAccountStatusError, LastError: err.Error()}
		officialAccountDB(conn).Model(&model.OfficialAccount{}).Where("id = ?", accountID).Updates(map[string]any{
			"status": fail.Status, "last_error": fail.LastError,
		})
		return account, fmt.Errorf("read usage: %w", err)
	}
	now := time.Now()
	if err := officialAccountDB(conn).Model(&model.OfficialAccount{}).Where("id = ?", accountID).Updates(map[string]any{
		"plan_tier": snap.PlanTier, "window_5h": snap.Window5H, "window_7d": snap.Window7D,
		"healthy": snap.Healthy, "last_checked_at": &now, "last_error": "",
	}).Error; err != nil {
		return account, fmt.Errorf("persist usage snapshot: %w", err)
	}
	return officialAccountGetOn(conn, accountID)
}

// OfficialAccountList 返回账号行（密文字段有 json:"-"，序列化天然脱敏）。
func OfficialAccountList(conn *gorm.DB) ([]model.OfficialAccount, error) {
	var accounts []model.OfficialAccount
	err := officialAccountDB(conn).Order("id ASC").Find(&accounts).Error
	return accounts, err
}

func officialAccountGetOn(conn *gorm.DB, id int) (model.OfficialAccount, error) {
	var account model.OfficialAccount
	if err := officialAccountDB(conn).First(&account, id).Error; err != nil {
		return model.OfficialAccount{}, err
	}
	return account, nil
}

func officialAccountDB(conn *gorm.DB) *gorm.DB {
	if conn != nil {
		return conn
	}
	return db.GetDB()
}

func officialRedirectURI() string {
	v := os.Getenv("OCTOPUS_OFFICIAL_REDIRECT_URI")
	if v == "" {
		return "oob"
	}
	return v
}

// ---------- 生产 HTTP 实现 ----------

type httpTokenExchanger struct{}

func (h *httpTokenExchanger) Exchange(provider model.OfficialAccountProvider, code, verifier string) (OfficialTokenBundle, error) {
	endpoints, ok := OfficialEndpoints[provider]
	if !ok {
		return OfficialTokenBundle{}, fmt.Errorf("no token endpoint for %q", provider)
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {os.Getenv("OCTOPUS_OFFICIAL_CLIENT_ID_" + strings.ToUpper(string(provider)))},
		"redirect_uri":  {officialRedirectURI()},
		"code_verifier": {verifier},
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
		return OfficialTokenBundle{}, fmt.Errorf("token endpoint %s: status %d", provider, resp.StatusCode)
	}
	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return OfficialTokenBundle{}, fmt.Errorf("token endpoint %s: %w", provider, err)
	}
	return OfficialTokenBundle{AccessToken: parsed.AccessToken, RefreshToken: parsed.RefreshToken, ExpiresIn: parsed.ExpiresIn}, nil
}

type httpUsageReader struct{}

func (h *httpUsageReader) Read(provider model.OfficialAccountProvider, accessToken string) (UsageSnapshot, error) {
	endpoints, ok := OfficialEndpoints[provider]
	if !ok {
		return UsageSnapshot{}, fmt.Errorf("no meta endpoint for %q", provider)
	}
	req, err := http.NewRequest(http.MethodGet, endpoints.Meta, nil)
	if err != nil {
		return UsageSnapshot{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := officialHTTPClient().Do(req)
	if err != nil {
		return UsageSnapshot{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, officialResponseMB))
	if err != nil {
		return UsageSnapshot{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return UsageSnapshot{}, fmt.Errorf("meta endpoint %s: status %d", provider, resp.StatusCode)
	}
	// 元数据 JSON 结构各家不一且未承诺：宽松取 plan/tier 与 5h/7d 字段，缺省按"未读取"回空。
	var parsed struct {
		Plan     string `json:"plan"`
		Tier     string `json:"tier"`
		Window5H string `json:"window_5h"`
		Window7D string `json:"window_7d"`
		Healthy  *bool  `json:"healthy"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return UsageSnapshot{}, fmt.Errorf("meta endpoint %s: %w", provider, err)
	}
	plan := parsed.Plan
	if plan == "" {
		plan = parsed.Tier
	}
	healthy := parsed.Healthy != nil && *parsed.Healthy
	return UsageSnapshot{PlanTier: plan, Window5H: parsed.Window5H, Window7D: parsed.Window7D, Healthy: healthy}, nil
}

var officialHTTPClient = func() *http.Client {
	return &http.Client{Timeout: officialReadTO}
}
