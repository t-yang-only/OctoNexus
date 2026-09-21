package op

import (
	"context"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/secret"
	"gorm.io/gorm"
)

// useImportCipherKey 把凭据密钥固定成环境变量来源并重置进程内缓存（与 secret 包测试同一约定）。
func useImportCipherKey(t *testing.T, value string) {
	t.Helper()
	t.Setenv("OCTOPUS_OFFICIAL_KEY", value)
	secret.ResetKey()
	t.Cleanup(secret.ResetKey)
}

// openImportTestDB 在共享测试装置之上补齐导入路径真正会碰到的表
// （孤儿行过滤要读 api_keys，统计表也随备份一起导入）。不改动共享装置本身。
func openImportTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn := openBackupTestDB(t)
	if err := conn.AutoMigrate(
		&model.APIKey{},
		&model.LLMInfo{},
		&model.Setting{},
		&model.StatsTotal{},
		&model.StatsDaily{},
		&model.StatsHourly{},
		&model.StatsAPIKey{},
		&model.OfficialAccount{},
		&model.ProxyNode{},
		&model.ProxySubscription{},
		&model.ManualSubscription{},
		&model.CredentialSource{},
	); err != nil {
		t.Fatalf("migrate import test db: %v", err)
	}
	return conn
}

// importFixture 造一份最小备份：一个渠道 + 一条待导入凭据。
// 字段用赋值而不是结构体字面量：model.Channel.Name 与 model.ChannelKey.Key 都是内嵌配置里的
// 提升字段，提升字段出现在字面量里需要较新的语言版本（go.mod 的语言版本比工具链低）。
func importFixture(key string) *model.DBDump {
	channel := model.Channel{ID: 1}
	channel.Name = "导入渠道"
	channel.BaseURL = "https://upstream.invalid"
	keyRow := model.ChannelKey{ID: 11, ChannelID: 1}
	keyRow.Name = "k1"
	keyRow.Key = key
	return &model.DBDump{
		Version:     dbDumpVersion,
		Channels:    []model.Channel{channel},
		ChannelKeys: []model.ChannelKey{keyRow},
	}
}

// TestDBImportSealsChannelKeys 导入是写 channel_keys 的路径之一，必须与面板保存走同一套加密收口。
//
// 判据取「库内那一行」而不是导入返回值：明文落库时返回值一样好看（实测线上就是这么漏的——
// 导入 46 条，sealed=0/46，要等下一次重启才被启动补加密补上，中间窗口库内与快照都是明文）。
func TestDBImportSealsChannelKeys(t *testing.T) {
	useImportCipherKey(t, "import-cipher-key")
	conn := openImportTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	res, err := DBImportIncremental(context.Background(), importFixture("sk-import-plain"))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.RowsAffected["channel_keys"] != 1 {
		t.Fatalf("channel_keys rows = %d, want 1", res.RowsAffected["channel_keys"])
	}

	var stored model.ChannelKey
	if err := conn.First(&stored, 11).Error; err != nil {
		t.Fatalf("load stored key: %v", err)
	}
	if !secret.IsSealed(stored.Key) {
		t.Fatalf("导入的凭据不是密文: %q", stored.Key)
	}
	if strings.Contains(stored.Key, "sk-import-plain") {
		t.Fatalf("库内密文里出现了明文: %q", stored.Key)
	}
	plain, err := secret.Open(stored.Key)
	if err != nil {
		t.Fatalf("导入后的凭据解不开: %v", err)
	}
	if plain != "sk-import-plain" {
		t.Fatalf("解出 = %q, want sk-import-plain", plain)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("同实例明文导入不该有告警: %v", res.Warnings)
	}
}

// TestDBImportKeepsAlreadySealedKeyReadable 已加密的备份导回同一实例：不能二次加密（否则原值永久丢失）。
func TestDBImportKeepsAlreadySealedKeyReadable(t *testing.T) {
	useImportCipherKey(t, "import-cipher-key")
	conn := openImportTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	sealed, err := secret.Seal("sk-already-sealed")
	if err != nil {
		t.Fatalf("seal fixture: %v", err)
	}
	if _, err := DBImportIncremental(context.Background(), importFixture(sealed)); err != nil {
		t.Fatalf("import: %v", err)
	}

	var stored model.ChannelKey
	if err := conn.First(&stored, 11).Error; err != nil {
		t.Fatalf("load stored key: %v", err)
	}
	plain, err := secret.Open(stored.Key)
	if err != nil {
		t.Fatalf("二次加密导致解不开: %v", err)
	}
	if plain != "sk-already-sealed" {
		t.Fatalf("解出 = %q, want sk-already-sealed", plain)
	}
}

// TestDBImportWarnsOnForeignSealedKey 跨实例恢复：备份里的密文本实例解不开时必须如实回报条数，
// 否则表现出来只是「导入成功、随后每个渠道都报错」。
func TestDBImportWarnsOnForeignSealedKey(t *testing.T) {
	useImportCipherKey(t, "key-A")
	foreign, err := secret.Seal("sk-from-other-instance")
	if err != nil {
		t.Fatalf("seal fixture: %v", err)
	}
	useImportCipherKey(t, "key-B")

	conn := openImportTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	res, err := DBImportIncremental(context.Background(), importFixture(foreign))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "1 条") {
		t.Fatalf("warnings = %v, want 一条「1 条…」告警", res.Warnings)
	}

	var stored model.ChannelKey
	if err := conn.First(&stored, 11).Error; err != nil {
		t.Fatalf("load stored key: %v", err)
	}
	if stored.Key != foreign {
		t.Fatalf("外来密文被改写: %q -> %q", foreign, stored.Key)
	}
	if _, err := secret.Open(stored.Key); err == nil {
		t.Fatal("用本实例密钥竟能解开外来密文（密钥隔离失效）")
	}
	// 反向自检：同一份外来密文在 key-A 下必须能解开，证明告警针对的是「密钥不匹配」而不是数据损坏。
	useImportCipherKey(t, "key-A")
	if plain, err := secret.Open(foreign); err != nil || plain != "sk-from-other-instance" {
		t.Fatalf("原实例解不开自己产的密文: %q/%v", plain, err)
	}
}
