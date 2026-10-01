package collector

import (
	"encoding/json"
	"strings"
	"testing"
)

// 内置站点模板的第一条纪律：**生成即校验**——发给前端的每一份模板都必须能过装载门禁。
// 这条用例的价值不在"覆盖率"，而在于：模板是手写的 JSON 片段，路径/字段写错时
// 用户会白填一次表单并在装载处被拒，而且看不出是模板的错还是自己的错。
func TestBuiltinTemplatesPassGate(t *testing.T) {
	built := 0
	for _, info := range Templates() {
		pack, err := BuildTemplate(info.Kind, "https://relay.example.com/", info.Mode)
		if err != nil {
			t.Fatalf("模板 %s/%s 生成失败：%v", info.Kind, info.Mode, err)
		}
		if err := pack.Validate(); err != nil {
			t.Fatalf("模板 %s/%s 过不了装载门禁：%v", info.Kind, info.Mode, err)
		}
		// 主机必须来自用户填的网址，且只此一台（预置域名等于绕过 fail closed）。
		if len(pack.Hosts) != 1 || pack.Hosts[0] != "relay.example.com" {
			t.Fatalf("模板 %s 的 hosts 应只含用户填的主机，实际 %v", info.Kind, pack.Hosts)
		}
		// 所有请求地址都必须落在该主机上（防"白名单写 A、请求打 B"）。
		urls := []string{}
		if pack.Login != nil && pack.Login.URL != "" {
			urls = append(urls, pack.Login.URL)
		}
		for _, step := range pack.Read {
			urls = append(urls, step.URL)
		}
		for _, raw := range urls {
			if !strings.HasPrefix(raw, "https://relay.example.com/") {
				t.Fatalf("模板 %s 的请求地址越界：%s", info.Kind, raw)
			}
		}
		// interactive 版本必须真的不带 form 登录步骤（否则验证码场景还是会走 API 登录）。
		if info.Mode == LoginModeInteractive {
			if pack.Login == nil || pack.Login.Mode != LoginModeInteractive {
				t.Fatalf("模板 %s 的 interactive 版本 login.mode 不对：%+v", info.Kind, pack.Login)
			}
			if pack.Login.URL != "" {
				t.Fatalf("模板 %s 的 interactive 版本不该带登录接口地址：%s", info.Kind, pack.Login.URL)
			}
		}
		if _, err := json.Marshal(pack); err != nil { // 前端拿到的是 JSON，必须能序列化
			t.Fatalf("模板 %s 无法序列化：%v", info.Kind, err)
		}
		built++
	}
	if built != len(TemplateKinds)*2 {
		t.Fatalf("模板数量不对：期望 %d，实际 %d", len(TemplateKinds)*2, built)
	}
}

// 单位口径必须写死在模板里：new-api/one-api 是点，sub2api/litellm 是美元。
// 这条实测踩过：32.53 美元会被再除 500000 显示成 6.5e-05。
func TestBuiltinTemplateUnits(t *testing.T) {
	want := map[string]string{
		"new-api": UnitsPoints,
		"one-api": UnitsPoints,
		"sub2api": UnitsUSD,
		"litellm": UnitsUSD,
	}
	for kind, units := range want {
		pack, err := BuildTemplate(kind, "https://relay.example.com", LoginModeForm)
		if err != nil {
			t.Fatalf("模板 %s 生成失败：%v", kind, err)
		}
		if pack.Units != units {
			t.Fatalf("模板 %s 的单位应为 %s，实际 %s", kind, units, pack.Units)
		}
	}
}

func TestBuildTemplateRejectsBadInput(t *testing.T) {
	cases := []struct{ kind, base, mode, want string }{
		{"nope", "https://relay.example.com", LoginModeForm, "未知的站点类型"},
		{"new-api", "", LoginModeForm, "站点地址不能为空"},
		{"new-api", "ftp://relay.example.com", LoginModeForm, "只允许 http/https"},
		{"new-api", "https://relay.example.com", "oauth", "登录形态只允许"},
	}
	for _, item := range cases {
		_, err := BuildTemplate(item.kind, item.base, item.mode)
		if err == nil {
			t.Fatalf("%s/%s/%s 应当被拒绝", item.kind, item.base, item.mode)
		}
		if !strings.Contains(err.Error(), item.want) {
			t.Fatalf("错误文案不含 %q：%v", item.want, err)
		}
	}
	// 不带 scheme 的网址按 https 补全（面板上人常常只填域名）。
	pack, err := BuildTemplate("new-api", "relay.example.com/panel", LoginModeForm)
	if err != nil {
		t.Fatalf("补全 scheme 失败：%v", err)
	}
	if pack.Hosts[0] != "relay.example.com" || !strings.HasPrefix(pack.Read[0].URL, "https://relay.example.com/") {
		t.Fatalf("网址归一化结果不对：hosts=%v url=%s", pack.Hosts, pack.Read[0].URL)
	}
}
