package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/secret"
	"gorm.io/gorm"
)

// 本文件是 R-sec-001 余项（渠道凭据静态加密）的迁移演练：
// 明文行被加密、空串与已加密行不动、再跑一次幂等、没有密钥时整次跳过而不是把启动卡死。

// createLegacyChannelKeys 造一张只有 id/name/key 三列的旧形状 channel_keys 表（迁移只碰这三列）。
func createLegacyChannelKeys(t *testing.T, conn *gorm.DB, rows map[int]string) {
	t.Helper()
	if err := conn.Exec(`CREATE TABLE channel_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		key TEXT NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create legacy channel_keys: %v", err)
	}
	for id, key := range rows {
		if err := conn.Exec(`INSERT INTO channel_keys (id, name, key) VALUES (?, ?, ?)`,
			id, "k", key).Error; err != nil {
			t.Fatalf("seed channel key %d: %v", id, err)
		}
	}
}

func readChannelKey(t *testing.T, conn *gorm.DB, id int) string {
	t.Helper()
	var value string
	if err := conn.Table("channel_keys").Select("key").Where("id = ?", id).Scan(&value).Error; err != nil {
		t.Fatalf("read channel key %d: %v", id, err)
	}
	return value
}

func TestMigrate15SealsPlaintextChannelKeys(t *testing.T) {
	t.Setenv("OCTOPUS_OFFICIAL_KEY", "migrate-15-test-key")
	secret.ResetKey()
	t.Cleanup(secret.ResetKey)

	conn := openMigrateTestDB(t)
	createLegacyChannelKeys(t, conn, map[int]string{
		1: "sk-plain-one",
		2: "",
		3: "sk-plain-two",
	})

	if err := migrateChannelKeyEncryption(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, id := range []int{1, 3} {
		value := readChannelKey(t, conn, id)
		if !secret.IsSealed(value) {
			t.Fatalf("第 %d 行仍是明文: %q", id, value)
		}
		if strings.Contains(value, "sk-plain") {
			t.Fatalf("第 %d 行的密文里出现了明文", id)
		}
	}
	if got := readChannelKey(t, conn, 2); got != "" {
		t.Fatalf("空串行被改成了 %q", got)
	}

	// 解出来的明文必须与原值一致（迁移只改存储形态，不改内容）。
	plain, err := secret.Open(readChannelKey(t, conn, 1))
	if err != nil {
		t.Fatalf("open sealed: %v", err)
	}
	if plain != "sk-plain-one" {
		t.Fatalf("还原 = %q, 期望 sk-plain-one", plain)
	}

	// 幂等：再跑一次不该改动任何行。
	before := readChannelKey(t, conn, 1)
	if err := migrateChannelKeyEncryption(conn); err != nil {
		t.Fatalf("migrate twice: %v", err)
	}
	if after := readChannelKey(t, conn, 1); after != before {
		t.Fatalf("第二次迁移改动了已加密的行")
	}
}

func TestMigrate15SkipsWhenNoCipherKey(t *testing.T) {
	// 环境变量清空 + 数据目录指向写不出来的位置：加密不可用。
	t.Setenv("OCTOPUS_OFFICIAL_KEY", "")
	previousPath := setUnwritableDatabasePath(t)
	secret.ResetKey()
	t.Cleanup(func() {
		restoreDatabasePath(previousPath)
		secret.ResetKey()
	})

	if secret.EncryptionEnabled() {
		t.Skip("本机在该目录下仍能生成密钥文件，跳过「无密钥」分支")
	}

	conn := openMigrateTestDB(t)
	createLegacyChannelKeys(t, conn, map[int]string{1: "sk-stays-plain"})

	if err := migrateChannelKeyEncryption(conn); err != nil {
		t.Fatalf("无密钥时迁移应跳过而不是报错: %v", err)
	}
	if got := readChannelKey(t, conn, 1); got != "sk-stays-plain" {
		t.Fatalf("无密钥时行被改动: %q", got)
	}
}

func TestMigrate15NoTableIsNoop(t *testing.T) {
	conn := openMigrateTestDB(t)
	// 没有 channel_keys 表（全新库的另一条分支）：不报错。
	if err := migrateChannelKeyEncryption(conn); err != nil {
		t.Fatalf("没有表时应直接返回: %v", err)
	}
}

// setUnwritableDatabasePath 把 database.path 指到一个「文件里面」，让密钥文件既建不了目录也写不进去，
// 从而构造出「没有可用密钥」的安装形态；返回原路径供还原。
func setUnwritableDatabasePath(t *testing.T) string {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	previous := conf.AppConfig.Database.Path
	conf.AppConfig.Database.Path = filepath.Join(blocker, "data.db")
	return previous
}

func restoreDatabasePath(previous string) {
	conf.AppConfig.Database.Path = previous
}
