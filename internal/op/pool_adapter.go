package op

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// 声明式适配器规格的密文封装（R-pool-ext-001 第三批）。
//
// 为什么放在 op 而不是 pool 包：`internal/pool` 依赖 `internal/op`（内置的官方账号适配器投影
// op 的数据），所以 op 不能再反向依赖 pool（会成环）。凭据加解密属于"凭据怎么落库"这一层，
// 与官方账号同一套密钥与实现，因此放在 op 里由上层调用。
//
// 与官方账号同一套密钥与同一个解析口径（internal/secret：环境变量 OCTOPUS_OFFICIAL_KEY →
// 数据目录 credential.key，首次使用自动生成）；密钥完全不可得时才报错；
// 区别只在 AAD —— 固定 "pool:declarative"，换用途的密文解不开。
const poolDeclarativeAAD = "pool:declarative"

// ErrPoolSecretKeyMissing 表示没配置凭据加密密钥（与官方账号同一个）。
var ErrPoolSecretKeyMissing = errors.New("pool declarative cipher key not configured")

// PoolSecretSeal 输出 base64(nonce|ciphertext)。
func PoolSecretSeal(plain []byte) (string, error) {
	gcm, err := officialGCM()
	if err != nil {
		// 未配置密钥时官方账号那条错误信息更好懂，这里补一层语义说明。
		if strings.Contains(err.Error(), "not configured") {
			return "", fmt.Errorf("%w: %v", ErrPoolSecretKeyMissing, err)
		}
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("pool declarative cipher nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plain, []byte(poolDeclarativeAAD))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// PoolSecretOpen 解出明文；密文被篡改或被挪用途都会失败。
func PoolSecretOpen(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("pool declarative cipher decode: %w", err)
	}
	gcm, err := officialGCM()
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("pool declarative cipher truncated")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(poolDeclarativeAAD))
	if err != nil {
		return nil, fmt.Errorf("pool declarative cipher open: %w", err)
	}
	return plain, nil
}
