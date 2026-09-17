package poolstore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/pool"
)

// 脱敏是**唯一**的出口：任何接口拿到的 spec 都不能带凭据。
// 这条不变量很脆——只要哪天有人在新出口上直接回结构体就会破防，所以给它一条判据。
func TestRedactStripsEverySecretField(t *testing.T) {
	spec := pool.DeclarativeSpec{
		Kind:    "custom-x",
		Title:   "x",
		BaseURL: "https://api.example.com",
		Auth:    pool.DeclarativeAuth{Type: "login", LoginURL: "/login", TokenPath: "data.token"},
		Secret: pool.DeclarativeSecret{
			Token:    "tok-should-not-survive",
			Username: "user-should-not-survive",
			Password: "pass-should-not-survive",
		},
	}
	redacted := Redact(spec)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"tok-should-not-survive", "user-should-not-survive", "pass-should-not-survive"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("脱敏后仍出现凭据 %q: %s", secret, encoded)
		}
	}
	if redacted.Secret != (pool.DeclarativeSecret{}) {
		t.Fatalf("Secret 应为零值, 实际 %+v", redacted.Secret)
	}
	// 非凭据字段必须原样保留：脱敏不能顺手把配置也擦了。
	if redacted.Kind != spec.Kind || redacted.Auth.TokenPath != spec.Auth.TokenPath {
		t.Fatalf("脱敏不该改动非凭据字段: %+v", redacted)
	}
}

// drop 只去掉指定 kind，其余保持顺序不变（移除某条时不能连带影响别人的适配器）。
func TestDropKeepsOtherSpecsInOrder(t *testing.T) {
	list := []pool.DeclarativeSpec{{Kind: "custom-a"}, {Kind: "custom-b"}, {Kind: "custom-c"}}
	kept := drop(list, "custom-b")
	if len(kept) != 2 || kept[0].Kind != "custom-a" || kept[1].Kind != "custom-c" {
		t.Fatalf("移除结果不对: %+v", kept)
	}
	if len(list) != 3 {
		t.Fatal("drop 不该修改入参")
	}
	if len(drop(list, "custom-z")) != 3 {
		t.Fatal("移除不存在的 kind 应原样返回")
	}
}
