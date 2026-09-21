package op

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/collector"
	"gorm.io/gorm"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// collectorTestDB 装好测试库并把它接到全局 DB（op 的写路径都走 db.GetDB()），
// 同时清掉包级的"进行中登录会话"：那是跨用例的共享状态，不清会让上一个用例的会话被下一个认领。
func collectorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn := openImportTestDB(t)
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)
	collectorState.Lock()
	collectorState.sessions = map[int]*collector.Session{}
	collectorState.steps = map[int][]string{}
	collectorState.Unlock()
	return conn
}

// collectorPackFor 造一份指向本地夹具站的采集包（登录拿令牌 → 用令牌查账）。
func collectorPackFor(t *testing.T, base, host, units string) string {
	t.Helper()
	pack := `{
		"name": "夹具站",
		"units": "` + units + `",
		"hosts": ["` + host + `"],
		"login": {
			"mode": "form",
			"url": "` + base + `/api/login",
			"body_type": "json",
			"body": "{\"u\":\"{username}\",\"p\":\"{password}\"}",
			"extract": [{"field": "token", "from": "json", "path": "data.token"}]
		},
		"read": [{
			"name": "读余额",
			"url": "` + base + `/api/self?units=` + units + `",
			"headers": {"Authorization": "Bearer {token}"},
			"extract": [{"field": "balance", "from": "json", "path": "data.quota"}]
		}]
	}`
	if _, err := collector.ParsePack([]byte(pack)); err != nil {
		t.Fatalf("夹具采集包非法：%v", err)
	}
	return pack
}

// TestCredentialSourceStoresSecretsSealed：采集包与登录凭据必须密文落库，且出参不回显密码。
func TestCredentialSourceStoresSecretsSealed(t *testing.T) {
	useImportCipherKey(t, "collector-cipher-key")
	collectorTestDB(t)
	ctx := t.Context()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"token":"T","quota":1}}`))
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")

	detail, err := CredentialSourceSave(ctx, 0, model.CredentialSourceInput{
		Name: "夹具采集", Kind: model.CredentialKindPack, ChannelID: 5, Pack: collectorPackFor(t, server.URL, host, collector.UnitsUSD), Username: "yang", Password: "s3cr3t-pw",
	})
	if err != nil {
		t.Fatalf("保存采集凭据失败：%v", err)
	}

	var row model.CredentialSource
	if err := db.GetDB().First(&row, detail.ID).Error; err != nil {
		t.Fatalf("读回落库行失败：%v", err)
	}
	if !strings.HasPrefix(row.PackCipher, "enc:v1:") || !strings.HasPrefix(row.CredCipher, "enc:v1:") {
		t.Fatalf("采集包/凭据没有密文落库：pack=%q cred=%q", row.PackCipher[:8], row.CredCipher[:8])
	}
	if strings.Contains(row.CredCipher, "s3cr3t-pw") {
		t.Fatal("密码明文出现在库里")
	}
	if detail.PackText == "" {
		t.Error("采集包原文应回显给面板编辑（它是配置不是凭据）")
	}
	if !detail.HasCredentials || detail.HasSession {
		t.Errorf("凭据/会话状态标记不对：creds=%v session=%v", detail.HasCredentials, detail.HasSession)
	}
}

// TestCredentialSourceReadingFeedsBalanceWithUnit：采集到的读数必须按包声明的单位进余额管线。
// 判据刻意分成"美元"与"点数"两份：单位搞错会让 32.53 美元显示成 6.5e-05（本轮实测踩过）。
func TestCredentialSourceReadingFeedsBalanceWithUnit(t *testing.T) {
	useImportCipherKey(t, "collector-cipher-key")
	collectorTestDB(t)
	ctx := t.Context()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/login":
			_, _ = w.Write([]byte(`{"data":{"token":"T-2"}}`))
		case "/api/self":
			if r.Header.Get("Authorization") != "Bearer T-2" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("units") == "points" {
				_, _ = w.Write([]byte(`{"data":{"quota":16265000}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"quota":32.53}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")

	usd, err := CredentialSourceSave(ctx, 0, model.CredentialSourceInput{
		Name: "美元站", Kind: model.CredentialKindPack, ChannelID: 91,
		Pack: collectorPackFor(t, server.URL, host, collector.UnitsUSD), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	if _, result, err := CredentialSourceRunNow(ctx, usd.ID); err != nil || !result.OK() {
		t.Fatalf("美元站采集失败：%v / %s", err, result.ReasonText)
	}
	if value, ok := ChannelBalance(91); !ok || value != 32.53 {
		t.Errorf("美元读数错误：%v (ok=%v)", value, ok)
	}
	if !ChannelBalanceInCurrency(91) {
		t.Error("美元读数被标成点口径，聚合层会再折算一次")
	}

	points, err := CredentialSourceSave(ctx, 0, model.CredentialSourceInput{
		Name: "点数站", Kind: model.CredentialKindPack, ChannelID: 92,
		Pack: collectorPackFor(t, server.URL, host, collector.UnitsPoints), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	if _, result, err := CredentialSourceRunNow(ctx, points.ID); err != nil || !result.OK() {
		t.Fatalf("点数站采集失败：%v / %s", err, result.ReasonText)
	}
	if value, ok := ChannelBalance(92); !ok || value != 16265000 {
		t.Errorf("点数读数错误：%v (ok=%v)", value, ok)
	}
	if ChannelBalanceInCurrency(92) {
		t.Error("点数读数被标成货币口径，聚合层不会再折算")
	}
}

// TestCollectorReadingForChannelRequiresEnabledAndAutoRefresh：只有"启用且自动刷新"的采集凭据才参与扫描。
func TestCollectorReadingForChannelRequiresEnabledAndAutoRefresh(t *testing.T) {
	useImportCipherKey(t, "collector-cipher-key")
	collectorTestDB(t)
	ctx := t.Context()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/login" {
			_, _ = w.Write([]byte(`{"data":{"token":"T"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"quota":7}}`))
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")

	disabled := false
	detail, err := CredentialSourceSave(ctx, 0, model.CredentialSourceInput{
		Name: "停用的", Kind: model.CredentialKindPack, ChannelID: 93, Enabled: &disabled,
		Pack: collectorPackFor(t, server.URL, host, collector.UnitsUSD), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	if _, ok := CollectorReadingForChannel(ctx, 93); ok {
		t.Error("停用的采集凭据不应参与扫描")
	}
	enabled := true
	if _, err := CredentialSourceSave(ctx, detail.ID, model.CredentialSourceInput{Enabled: &enabled}); err != nil {
		t.Fatalf("启用失败：%v", err)
	}
	if _, ok := CollectorReadingForChannel(ctx, 93); !ok {
		t.Error("启用后应参与扫描")
	}
}

// TestCredentialSourceDeleteDropsRow：删除后行与会话一起消失（密文随之作废）。
func TestCredentialSourceDeleteDropsRow(t *testing.T) {
	useImportCipherKey(t, "collector-cipher-key")
	collectorTestDB(t)
	ctx := t.Context()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/login" {
			_, _ = w.Write([]byte(`{"data":{"token":"T"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"quota":1}}`))
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	detail, err := CredentialSourceSave(ctx, 0, model.CredentialSourceInput{
		Name: "待删", Kind: model.CredentialKindPack, Pack: collectorPackFor(t, server.URL, host, collector.UnitsUSD),
		Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	if _, _, err := CredentialSourceRunNow(ctx, detail.ID); err != nil {
		t.Fatalf("采集失败：%v", err)
	}
	if err := CredentialSourceDelete(ctx, detail.ID); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	var count int64
	if err := db.GetDB().Model(&model.CredentialSource{}).Where("id = ?", detail.ID).Count(&count).Error; err != nil {
		t.Fatalf("计数失败：%v", err)
	}
	if count != 0 {
		t.Errorf("删除后仍有 %d 行", count)
	}
	if _, ok := collectorSessionIfAny(detail.ID); ok {
		t.Error("删除后内存会话还在")
	}
}
