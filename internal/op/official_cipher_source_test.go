package op

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/secret"
)

// 本文件守的是「同一台机器只有一套密钥口径」这条不变量。
//
// 曾经的缺陷：渠道凭据经 internal/secret 解析密钥（环境变量 → 数据目录 credential.key），
// 而官方账号与号池声明式规格自己直接读环境变量。于是没设环境变量的正常安装上，
// 渠道凭据加密是好的、号池却一用就报
// "official credential cipher key not configured (set OCTOPUS_OFFICIAL_KEY)"。
//
// 判据必须是「不设环境变量时能加解密」，而不是「设了环境变量时能加解密」——后者在缺陷版本上也是绿的。

// useOnlyKeyFile 把密钥来源切到数据目录下的 credential.key，并确保环境变量不参与。
func useOnlyKeyFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := conf.AppConfig.Database.Path
	conf.AppConfig.Database.Path = filepath.Join(dir, "data.db")
	if err := os.Unsetenv("OCTOPUS_OFFICIAL_KEY"); err != nil {
		t.Fatalf("unset env: %v", err)
	}
	secret.ResetKey()
	t.Cleanup(func() {
		conf.AppConfig.Database.Path = previous
		secret.ResetKey()
	})
	return dir
}

func TestOfficialCipherWorksWithoutEnvKey(t *testing.T) {
	dir := useOnlyKeyFile(t)

	enc, err := officialEncrypt("openai", "sk-official-access-token")
	if err != nil {
		t.Fatalf("加密失败（未设环境变量时应回退到 credential.key）: %v", err)
	}
	if enc == "" || strings.Contains(enc, "sk-official-access-token") {
		t.Fatalf("密文形状不对: %q", enc)
	}
	if _, err := base64.StdEncoding.DecodeString(enc); err != nil {
		t.Fatalf("密文不是 base64: %v", err)
	}
	plain, err := officialDecrypt("openai", enc)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if plain != "sk-official-access-token" {
		t.Fatalf("解出 = %q", plain)
	}
	// 密钥文件必须真的落在数据目录里（这是"密钥来源是文件"的可观测证据）。
	if _, err := os.Stat(filepath.Join(dir, "credential.key")); err != nil {
		t.Fatalf("credential.key 未生成: %v", err)
	}
}

func TestPoolDeclarativeSealWorksWithoutEnvKey(t *testing.T) {
	useOnlyKeyFile(t)

	spec := []byte(`{"kind":"custom-demo","hosts":["example.invalid"]}`)
	sealed, err := PoolSecretSeal(spec)
	if err != nil {
		t.Fatalf("号池规格加密失败（未设环境变量时应回退到 credential.key）: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "not configured") {
		t.Fatalf("仍报密钥未配置: %v", err)
	}
	opened, err := PoolSecretOpen(sealed)
	if err != nil {
		t.Fatalf("号池规格解密失败: %v", err)
	}
	if string(opened) != string(spec) {
		t.Fatalf("解出 = %s", opened)
	}
	// 反向自检：用途隔离仍在 —— 官方账号的密文不能当号池规格解开（AAD 不同）。
	official, err := officialEncrypt("openai", "sk-should-not-open-as-pool")
	if err != nil {
		t.Fatalf("加密官方账号凭据失败: %v", err)
	}
	if _, err := PoolSecretOpen(official); err == nil {
		t.Fatal("官方账号密文竟能当号池规格解开（AAD 隔离失效）")
	}
}

// TestOfficialCipherKeepsEnvKeyPriority 有环境变量时必须仍然优先用它，
// 否则存量安装（靠环境变量加密）升级后会解不开自己的密文。
func TestOfficialCipherKeepsEnvKeyPriority(t *testing.T) {
	useOnlyKeyFile(t)
	t.Setenv("OCTOPUS_OFFICIAL_KEY", "env-key-priority")
	secret.ResetKey()

	enc, err := officialEncrypt("claude", "sk-env-sealed")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}

	// 换回文件来源：同一份密文必须解不开（证明加密时用的是环境变量那把，不是文件那把）。
	useOnlyKeyFile(t)
	if _, err := officialDecrypt("claude", enc); err == nil {
		t.Fatal("环境变量密钥的密文竟能被文件密钥解开（优先级或缓存有问题）")
	}
	// 再切回同一把环境变量：必须能解开。
	t.Setenv("OCTOPUS_OFFICIAL_KEY", "env-key-priority")
	secret.ResetKey()
	plain, err := officialDecrypt("claude", enc)
	if err != nil {
		t.Fatalf("同一把环境变量密钥解不开: %v", err)
	}
	if plain != "sk-env-sealed" {
		t.Fatalf("解出 = %q", plain)
	}
}
