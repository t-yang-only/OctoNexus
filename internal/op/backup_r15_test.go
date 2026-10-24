package op

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"github.com/t-yang-only/OctoNexus/internal/secret"
)

// R15-F1: 异机导入带密文的 Sub2API 账号时必须发出警告，避免用户遭遇静默黑盒解密失败。
func TestDBImportWarnsOnForeignSub2APICredentials(t *testing.T) {
	foreignKey := "foreign-sub2api-key"
	useImportCipherKey(t, foreignKey)
	foreignCreds, err := secret.SealWith(sub2apiSecretAAD, `{"api_key":"foreign-sub2-token"}`)
	if err != nil {
		t.Fatalf("seal foreign creds: %v", err)
	}

	useImportCipherKey(t, "current-instance-key")
	conn := openImportTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	dump := &model.DBDump{
		Version: dbDumpVersion,
		Sub2APIAccounts: []model.Sub2APIAccountExport{
			{
				Sub2APIAccount: model.Sub2APIAccount{
					ID:       101,
					Name:     "foreign-sub2",
					Platform: model.Sub2APIPlatformOpenAI,
					BaseURL:  "https://foreign.example.com",
					Enabled:  true,
				},
				CredentialsOut: foreignCreds,
			},
		},
	}
	res, err := DBImportIncremental(context.Background(), dump)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.RowsAffected["sub2api_accounts"] != 1 {
		t.Fatalf("sub2api_accounts rows = %d, want 1", res.RowsAffected["sub2api_accounts"])
	}
	joined := strings.Join(res.Warnings, " | ")
	if !strings.Contains(joined, "订阅反代账号") || !strings.Contains(joined, "解不开") {
		t.Fatalf("警告中应包含订阅反代账号解不开提示, 实际: %v", res.Warnings)
	}
}

// R15-F2: 导入明文 Sub2API 凭据时必须自动落库加密 (静态加密契约在导入路径生效)。
func TestDBImportSealsPlaintextSub2APICredentials(t *testing.T) {
	useImportCipherKey(t, "current-instance-key")
	conn := openImportTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	plainCreds := `{"api_key":"sk-plain-import-test"}`
	dump := &model.DBDump{
		Version: dbDumpVersion,
		Sub2APIAccounts: []model.Sub2APIAccountExport{
			{
				Sub2APIAccount: model.Sub2APIAccount{
					ID:       102,
					Name:     "plain-sub2",
					Platform: model.Sub2APIPlatformOpenAI,
					BaseURL:  "https://plain.example.com",
					Enabled:  true,
				},
				CredentialsOut: plainCreds,
			},
		},
	}
	res, err := DBImportIncremental(context.Background(), dump)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.RowsAffected["sub2api_accounts"] != 1 {
		t.Fatalf("sub2api_accounts rows = %d, want 1", res.RowsAffected["sub2api_accounts"])
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("明文导入不应产生异机解密警告, 实际: %v", res.Warnings)
	}

	var stored model.Sub2APIAccount
	if err := conn.First(&stored, 102).Error; err != nil {
		t.Fatalf("read stored account: %v", err)
	}
	if !secret.IsSealed(stored.Credentials) {
		t.Fatalf("导入后凭据未执行静态加密: %q", stored.Credentials)
	}
	opened, err := secret.OpenWith(sub2apiSecretAAD, stored.Credentials)
	if err != nil {
		t.Fatalf("解密落库密文失败: %v", err)
	}
	if opened != plainCreds {
		t.Fatalf("解密结果不一致: got %q, want %q", opened, plainCreds)
	}
}

// R15-F3: 导出与导入完整往返 Sub2API 的 LastSyncModels 下架基准模型。
func TestDBDumpRoundTripsSub2APILastSyncModels(t *testing.T) {
	useImportCipherKey(t, "current-instance-key")
	source := openImportTestDB(t)
	restoreSource := db.SetDBForTest(source)
	defer restoreSource()

	baselineModels := `["gpt-4o","claude-3-5-sonnet"]`
	s2Account := model.Sub2APIAccount{
		ID:             103,
		Name:           "sync-baseline-sub2",
		Platform:       model.Sub2APIPlatformOpenAI,
		BaseURL:        "https://sync.example.com",
		LastSyncModels: baselineModels,
		Enabled:        true,
	}
	if err := source.Create(&s2Account).Error; err != nil {
		t.Fatalf("seed sub2api account: %v", err)
	}

	dump, err := DBExportAll(context.Background())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(dump.Sub2APIAccounts) != 1 {
		t.Fatalf("exported accounts = %d, want 1", len(dump.Sub2APIAccounts))
	}
	if dump.Sub2APIAccounts[0].LastSyncModelsOut != baselineModels {
		t.Fatalf("exported LastSyncModelsOut = %q, want %q", dump.Sub2APIAccounts[0].LastSyncModelsOut, baselineModels)
	}

	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatalf("marshal dump: %v", err)
	}
	if !strings.Contains(string(raw), "last_sync_models") {
		t.Fatalf("marshal JSON 缺少 last_sync_models: %s", string(raw))
	}

	target := openImportTestDB(t)
	db.SetDBForTest(target)
	restored := &model.DBDump{}
	if err := json.Unmarshal(raw, restored); err != nil {
		t.Fatalf("unmarshal dump: %v", err)
	}

	res, err := DBImportIncremental(context.Background(), restored)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.RowsAffected["sub2api_accounts"] != 1 {
		t.Fatalf("rows = %d, want 1", res.RowsAffected["sub2api_accounts"])
	}

	var got model.Sub2APIAccount
	if err := target.First(&got, 103).Error; err != nil {
		t.Fatalf("load target account: %v", err)
	}
	if got.LastSyncModels != baselineModels {
		t.Fatalf("restored LastSyncModels = %q, want %q", got.LastSyncModels, baselineModels)
	}
}
