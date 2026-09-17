// Package secret 收敛「凭据怎么加密落库」这一件事：密钥从哪里来、密文长什么样。
//
// 为什么单独成包：migrate 需要把存量明文凭据加密，而 migrate 不能依赖 op（op 依赖 db，会成环）。
// 密钥解析又必须与 op 的读写共用同一份实现（否则"谁加密、谁解密"会各一套），
// 因此把与业务无关的加解密原语放在这里，由 op 与 migrate 共同依赖。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

// 渠道凭据静态加密（R-sec-001 余项 / T-sec-001）：`channel_keys.key` 在库里是密文，进程内与接口上是明文。
//
// 为什么收口在缓存这一层（op 侧）：选路、探测、配额扫描、号池刷新都从 channelKeyCache 取凭据，
// 把「库内密文 → 缓存明文」收口在装载处、「缓存明文 → 库内密文」收口在写入处，
// 既有的转发链路一行都不用改，也就不可能在某个分支漏掉解密而把密文当凭据发出去。
//
// 三条口径：
//  1. 带前缀的值是密文，没有前缀的值一律按明文处理 —— 存量行不需要一次性迁移就能继续用。
//  2. 解密失败一律报错，绝不把密文当明文返回：把密文当 API key 发出去，得到的是上游 401，
//     而不是一条能定位问题的日志。
//  3. 密钥优先级：环境变量 OCTOPUS_OFFICIAL_KEY（与官方账号同一把，避免两套密钥体系）
//     → 数据目录下的 credential.key（0600，首次使用时自动生成并打印路径）。
const (
	// SealedPrefix 标出「这串是密文」。
	SealedPrefix = "enc:v1:"
	// credentialAAD 绑定用途：官方账号与号池规格的密文拿到这里解不开，反之亦然。
	credentialAAD = "credential:channel-key"
	// KeyFileName 是自动生成的密钥文件名，位于 database.path 所在目录。
	KeyFileName = "credential.key"
)

// ErrKeyMissing 表示没有可用的凭据加密密钥（环境变量与密钥文件都没有且无法生成）。
var ErrKeyMissing = errors.New("credential cipher key not configured")

var (
	keyOnce  sync.Once
	keyBytes []byte
	keyErr   error
	keyFrom  string // 密钥来源说明：环境变量名或密钥文件路径。
)

// KeySource 返回密钥来源（环境变量名或密钥文件路径），未配置时为空串。
func KeySource() string {
	if _, err := cipherKey(); err != nil {
		return ""
	}
	return keyFrom
}

// EncryptionEnabled 报告凭据加密是否可用（启动日志与迁移判断用）。
func EncryptionEnabled() bool {
	_, err := cipherKey()
	return err == nil
}

// KeyPath 返回密钥文件路径：与数据库同目录（数据库备份与密钥文件应当一起保存）。
func KeyPath() string {
	dbPath := strings.TrimSpace(conf.AppConfig.Database.Path)
	if dbPath == "" {
		dbPath = "data/data.db"
	}
	dir := filepath.Dir(dbPath)
	if dir == "" || dir == "." {
		dir = "data"
	}
	return filepath.Join(dir, KeyFileName)
}

// cipherKey 解析并缓存 32 字节密钥（SHA-256 派生，与官方账号同一口径）。
func cipherKey() ([]byte, error) {
	keyOnce.Do(func() {
		keyBytes, keyFrom, keyErr = loadCipherKey()
	})
	if keyErr != nil {
		return nil, keyErr
	}
	return keyBytes, nil
}

func loadCipherKey() ([]byte, string, error) {
	if secret := strings.TrimSpace(os.Getenv("OCTOPUS_OFFICIAL_KEY")); secret != "" {
		sum := sha256.Sum256([]byte(secret))
		return sum[:], "OCTOPUS_OFFICIAL_KEY", nil
	}
	path := KeyPath()
	if raw, err := os.ReadFile(path); err == nil {
		if value := strings.TrimSpace(string(raw)); value != "" {
			sum := sha256.Sum256([]byte(value))
			return sum[:], path, nil
		}
		return nil, "", fmt.Errorf("%w: 密钥文件 %s 是空的", ErrKeyMissing, path)
	} else if !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("read credential key file %s: %w", path, err)
	}

	// 首次使用：生成 32 字节随机密钥并落盘 0600。打印路径是刻意的 —— 这个文件丢了，
	// 已加密的凭据就解不开（可以在界面重新填写，但没有别的办法找回）。
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, "", fmt.Errorf("generate credential key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, "", fmt.Errorf("create credential key dir: %w", err)
	}
	value := base64.StdEncoding.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		return nil, "", fmt.Errorf("write credential key file %s: %w", path, err)
	}
	log.Warnf("已生成渠道凭据加密密钥 %s（Linux/macOS 权限 0600；Windows 由文件系统 ACL 决定）：请与数据备份一起保存 —— 该文件丢失后已加密的上游凭据无法解密（只能在界面重新填写）", path)
	sum := sha256.Sum256([]byte(value))
	return sum[:], path, nil
}

// ResetKey 供测试使用：清掉进程内缓存的密钥（测试会改环境变量或换数据目录）。
func ResetKey() {
	keyOnce = sync.Once{}
	keyBytes = nil
	keyErr = nil
	keyFrom = ""
}

func credentialGCM() (cipher.AEAD, error) {
	key, err := cipherKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("credential cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// IsSealed 报告落库值是不是密文（没有前缀的值按明文处理）。
func IsSealed(stored string) bool {
	return strings.HasPrefix(stored, SealedPrefix)
}

// Seal 把明文凭据加密成落库形状 base64(nonce|ciphertext) 并加前缀。
// 空串原样返回（空凭据的模型层语义不变）；已是密文时原样返回（幂等，避免二次加密）。
func Seal(plain string) (string, error) {
	if plain == "" || IsSealed(plain) {
		return plain, nil
	}
	gcm, err := credentialGCM()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("credential cipher nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), []byte(credentialAAD))
	return SealedPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Open 把落库值还原成明文：没有前缀的值原样返回（存量明文行），
// 带前缀的值必须能解开，否则报错 —— 绝不把密文当明文交出去。
func Open(stored string) (string, error) {
	if stored == "" || !IsSealed(stored) {
		return stored, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, SealedPrefix))
	if err != nil {
		return "", fmt.Errorf("credential cipher decode: %w", err)
	}
	gcm, err := credentialGCM()
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("credential cipher truncated")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(credentialAAD))
	if err != nil {
		return "", fmt.Errorf("credential cipher open: %w", err)
	}
	return string(plain), nil
}

// OpenRow 就地解密一行的凭据；失败时报错并带上凭据名（不含凭据本身）。
func OpenRow(row *model.ChannelKey) error {
	plain, err := Open(row.Key)
	if err != nil {
		hint := "未配置"
		if KeySource() != "" {
			hint = KeySource()
		}
		return fmt.Errorf("channel key %q (id=%d) 无法解密：%w（密钥来源：%s；请检查 OCTOPUS_OFFICIAL_KEY 或 %s 是否与加密时一致）",
			row.Name, row.ID, err, hint, KeyFileName)
	}
	row.Key = plain
	return nil
}

// DecryptRows 解密一批已从库里读出的凭据行（就地修改）。
func DecryptRows(rows []model.ChannelKey) error {
	for i := range rows {
		if err := OpenRow(&rows[i]); err != nil {
			return err
		}
	}
	return nil
}

// SealLegacyChannelKeys 把库里还是明文的凭据行加密（幂等：只处理没有前缀的行），返回处理条数。
//
// 迁移用它，备份导入之后也用它：导入回来的旧转储里凭据是明文，落地即加密比等下一次重启更紧。
func SealLegacyChannelKeys(conn *gorm.DB) (int, error) {
	rows := []model.ChannelKey{}
	if err := conn.Select("id", "name", "key").Find(&rows).Error; err != nil {
		return 0, fmt.Errorf("load channel keys for sealing: %w", err)
	}
	sealed := 0
	for _, row := range rows {
		if row.Key == "" || IsSealed(row.Key) {
			continue
		}
		value, err := Seal(row.Key)
		if err != nil {
			return sealed, fmt.Errorf("seal channel key id=%d: %w", row.ID, err)
		}
		if err := conn.Model(&model.ChannelKey{}).Where("id = ?", row.ID).
			Update("key", value).Error; err != nil {
			return sealed, fmt.Errorf("update channel key id=%d: %w", row.ID, err)
		}
		sealed++
	}
	return sealed, nil
}
