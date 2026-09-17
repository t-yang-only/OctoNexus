package secret

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// useEnvKey 把密钥固定成环境变量来源（测试之间要互相隔离，故每次都重置进程内缓存）。
func useEnvKey(t *testing.T, value string) {
	t.Helper()
	t.Setenv("OCTOPUS_OFFICIAL_KEY", value)
	ResetKey()
	t.Cleanup(ResetKey)
}

// useKeyFile 把密钥来源切成数据目录下的密钥文件，返回数据目录路径。
func useKeyFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := conf.AppConfig.Database.Path
	conf.AppConfig.Database.Path = filepath.Join(dir, "data.db")
	os.Unsetenv("OCTOPUS_OFFICIAL_KEY")
	ResetKey()
	t.Cleanup(func() {
		conf.AppConfig.Database.Path = previous
		ResetKey()
	})
	return dir
}

func openSecretTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	// 只建 channel_keys 的迁移需要的三列（迁移只碰这三列）。
	if err := conn.Exec(`CREATE TABLE channel_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		key TEXT NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create channel_keys: %v", err)
	}
	return conn
}

func TestSealOpenRoundTrip(t *testing.T) {
	useEnvKey(t, "test-credential-key")
	plain := "sk-live-abcdef0123456789"
	sealed, err := Seal(plain)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if !IsSealed(sealed) || !strings.HasPrefix(sealed, SealedPrefix) {
		t.Fatalf("密文没有前缀: %q", sealed)
	}
	if strings.Contains(sealed, plain) {
		t.Fatalf("密文里出现了明文")
	}
	got, err := Open(sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got != plain {
		t.Fatalf("还原 = %q, 期望 %q", got, plain)
	}
	// 同一个明文两次加密的密文必须不同（随机 nonce），否则等价于确定性加密。
	again, err := Seal(plain)
	if err != nil {
		t.Fatalf("seal again: %v", err)
	}
	if again == sealed {
		t.Fatalf("两次加密结果相同，nonce 没有随机化")
	}
}

func TestSealAndOpenEdgeValues(t *testing.T) {
	useEnvKey(t, "test-credential-key")
	// 空串原样返回：空凭据的模型层语义不能被加密改掉。
	if sealed, err := Seal(""); err != nil || sealed != "" {
		t.Fatalf("空串 Seal = %q, %v; 期望空串无错", sealed, err)
	}
	if plain, err := Open(""); err != nil || plain != "" {
		t.Fatalf("空串 Open = %q, %v; 期望空串无错", plain, err)
	}
	// 已是密文时 Seal 幂等：否则每次保存都会二次加密，密文越滚越长。
	first, err := Seal("sk-idempotent")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	second, err := Seal(first)
	if err != nil {
		t.Fatalf("seal sealed: %v", err)
	}
	if second != first {
		t.Fatalf("二次加密改变了密文")
	}
}

func TestOpenTreatsUnprefixedValueAsPlaintext(t *testing.T) {
	useEnvKey(t, "test-credential-key")
	// 存量明文行必须继续可用（迁移不是让旧数据失效）。
	got, err := Open("sk-legacy-plaintext")
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if got != "sk-legacy-plaintext" {
		t.Fatalf("存量明文被改写 = %q", got)
	}
}

func TestOpenFailsOnTamperAndWrongKey(t *testing.T) {
	useEnvKey(t, "test-credential-key")
	sealed, err := Seal("sk-tamper-me")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// 篡改密文主体：必须报错，绝不能返回可疑的"明文"。
	broken := sealed[:len(sealed)-4] + "AAAA"
	if _, err := Open(broken); err == nil {
		t.Fatalf("篡改后的密文竟然解开了")
	}
	// 换一把密钥：同样必须报错（这正是"密钥文件丢了"的情形，报错才能让人定位）。
	useEnvKey(t, "another-key")
	if _, err := Open(sealed); err == nil {
		t.Fatalf("用另一把密钥竟然解开了")
	}
}

func TestKeyFileFallbackCreatesKeyAndReloads(t *testing.T) {
	dir := useKeyFile(t)
	path := filepath.Join(dir, KeyFileName)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("密钥文件在首次使用前就存在")
	}
	if !EncryptionEnabled() {
		t.Fatalf("环境变量未设置时应回落到密钥文件，但加密不可用")
	}
	if got := KeySource(); got != path {
		t.Fatalf("密钥来源 = %q, 期望密钥文件路径 %q", got, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("密钥文件未生成: %v", err)
	}
	// 权限位只在 Unix 上由 Go 保证：Windows 的 os.WriteFile 不落地 0600（ACL 决定），
	// 断言它会让测试在开发机上假红，因此只在非 Windows 上断言。
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("密钥文件权限 = %o, 期望 600", perm)
		}
	}
	// 与平台无关的实质断言：文件里确实是一段非空密钥。
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读密钥文件: %v", err)
	}
	if len(strings.TrimSpace(string(content))) < 16 {
		t.Fatalf("密钥文件内容过短: %q", string(content))
	}
	sealed, err := Seal("sk-file-key")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// 模拟重启：清掉进程内缓存，密钥必须能从文件重新读出来。
	ResetKey()
	got, err := Open(sealed)
	if err != nil {
		t.Fatalf("重启后 open: %v", err)
	}
	if got != "sk-file-key" {
		t.Fatalf("重启后还原 = %q", got)
	}
}

func TestSealLegacyChannelKeysIsIdempotent(t *testing.T) {
	useEnvKey(t, "test-credential-key")
	conn := openSecretTestDB(t)
	if err := conn.Exec(`INSERT INTO channel_keys (id, name, key) VALUES
		(1, 'plain', 'sk-plain-one'),
		(2, 'empty', ''),
		(3, 'sealed', ?)`, mustSeal(t, "sk-already-sealed")).Error; err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	sealed, err := SealLegacyChannelKeys(conn)
	if err != nil {
		t.Fatalf("seal legacy: %v", err)
	}
	if sealed != 1 {
		t.Fatalf("加密条数 = %d, 期望 1（空串与已加密的行都不动）", sealed)
	}
	rows := []model.ChannelKey{}
	if err := conn.Select("id", "name", "key").Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !IsSealed(rows[0].Key) {
		t.Fatalf("第 1 行还是明文: %q", rows[0].Key)
	}
	if rows[1].Key != "" {
		t.Fatalf("空串行被改成了 %q", rows[1].Key)
	}
	if plain, err := Open(rows[2].Key); err != nil || plain != "sk-already-sealed" {
		t.Fatalf("已加密行被破坏: %q, %v", rows[2].Key, err)
	}
	// 再跑一次：幂等，不该有任何新的加密。
	again, err := SealLegacyChannelKeys(conn)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if again != 0 {
		t.Fatalf("第二次加密条数 = %d, 期望 0（幂等）", again)
	}
	// 解出来的明文要与原始一致。
	plain, err := Open(rows[0].Key)
	if err != nil || plain != "sk-plain-one" {
		t.Fatalf("还原 = %q, %v; 期望 sk-plain-one", plain, err)
	}
}

func mustSeal(t *testing.T, plain string) string {
	t.Helper()
	sealed, err := Seal(plain)
	if err != nil {
		t.Fatalf("seal helper: %v", err)
	}
	return sealed
}
