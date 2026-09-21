package clashcfg

import (
	"strings"
	"testing"
)

// 判据取自用户本机真实订阅的形状（ss / vmess / vless / hysteria2 / anytls 各一），
// 不打印任何凭据值，只断言"解析出来的形状"。

const realShapeYAML = `
mixed-port: 7890
allow-lan: false
proxies:
  - {name: "HK-01", type: ss, server: hk1.example.com, port: 443, cipher: aes-128-gcm, password: secret-a, udp: true}
  - {name: "JP-01", type: vmess, server: jp1.example.com, port: 8443, uuid: uuid-a, alterId: 0, cipher: auto, tls: true, network: ws, ws-opts: {path: /ws}}
  - {name: "US-01", type: vless, server: us1.example.com, port: 443, uuid: uuid-b, network: tcp, tls: true, flow: xtls-rprx-vision, reality-opts: {public-key: pk}}
  - {name: "SG-01", type: hysteria2, server: sg1.example.com, port: 443, password: secret-b, sni: sg.example.com, skip-cert-verify: true}
  - {name: "TW-01", type: anytls, server: tw1.example.com, port: 8443, password: secret-c, sni: tw.example.com}
  - {name: "BAD-1", type: ss, server: bad.example.com}                      # 缺 port：跳过
  - {name: "BAD-2", type: unknown-proto, server: bad2.example.com, port: 1} # 类型不支持：跳过
  - {name: "HK-01", type: ss, server: hk2.example.com, port: 443, cipher: aes-128-gcm, password: secret-d} # 重名：改名保留
proxy-groups:
  - {name: AUTO, type: url-test, proxies: ["HK-01"]}
rules:
  - MATCH,AUTO
`

func TestParseClashConfigExtractsNodes(t *testing.T) {
	result, err := Parse([]byte(realShapeYAML))
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if len(result.Nodes) != 6 {
		names := make([]string, 0, len(result.Nodes))
		for _, n := range result.Nodes {
			names = append(names, n.Name)
		}
		t.Fatalf("节点数 = %d（%v），期望 6：5 个合法 + 1 个重名改名", len(result.Nodes), names)
	}
	byName := map[string]Node{}
	for _, n := range result.Nodes {
		byName[n.Name] = n
	}
	// 协议字段必须先被拆出去，再进 Params —— 否则内核按名选节点时拿不到 server/port
	for _, tc := range []struct {
		name, typ, server string
		port              int
	}{
		{"HK-01", "ss", "hk1.example.com", 443},
		{"JP-01", "vmess", "jp1.example.com", 8443},
		{"US-01", "vless", "us1.example.com", 443},
		{"SG-01", "hysteria2", "sg1.example.com", 443},
		{"TW-01", "anytls", "tw1.example.com", 8443},
	} {
		n, ok := byName[tc.name]
		if !ok {
			t.Fatalf("缺少节点 %q", tc.name)
		}
		if n.Type != tc.typ || n.Server != tc.server || n.Port != tc.port {
			t.Fatalf("%s 解析结果 = %s/%s/%d", tc.name, n.Type, n.Server, n.Port)
		}
		for _, reserved := range []string{"name", "type", "server", "port"} {
			if _, leaked := n.Params[reserved]; leaked {
				t.Fatalf("%s 的 Params 里不该再出现 %q（已拆为独立字段）", tc.name, reserved)
			}
		}
	}
	if n := byName["HK-01"]; n.Params["password"] != "secret-a" || n.Params["cipher"] != "aes-128-gcm" {
		t.Fatalf("ss 透传字段丢失: %+v", n.Params)
	}
	if n := byName["US-01"]; n.Params["reality-opts"] == nil {
		t.Fatal("vless 的 reality-opts 必须原样透传")
	}
	// 重名节点必须改名保留，而不是被吃掉
	if _, ok := byName["HK-01-2"]; !ok {
		t.Fatalf("重名节点未改名保留，实际名单: %v", keys(byName))
	}
	// 跳过的节点要有 Note（可解释，不是静默丢弃）
	joined := strings.Join(result.Notes, " | ")
	if !strings.Contains(joined, "port 非法或缺失") || !strings.Contains(joined, "暂不支持") {
		t.Fatalf("跳过原因未如实记录: %v", result.Notes)
	}
	if !strings.Contains(joined, "重复") {
		t.Fatalf("重名未记录: %v", result.Notes)
	}
}

// TestParseStableParamsJSON：同一份配置两次解析必须得到同一串参数（否则每次导入都判成"变了"而刷新一遍）。
func TestParseStableParamsJSON(t *testing.T) {
	first, err := Parse([]byte(realShapeYAML))
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	second, err := Parse([]byte(realShapeYAML))
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	for i := range first.Nodes {
		if first.Nodes[i].ParamsJSON() != second.Nodes[i].ParamsJSON() {
			t.Fatalf("第 %d 个节点两次序列化不一致:\n%s\n%s",
				i, first.Nodes[i].ParamsJSON(), second.Nodes[i].ParamsJSON())
		}
	}
}

// TestParseRejectsNonClashInput：喂进去不是 Clash 配置时要给出人话错误，而不是解析出 0 个节点当成功。
func TestParseRejectsNonClashInput(t *testing.T) {
	if _, err := Parse([]byte("hello: world")); err == nil {
		t.Fatal("没有 proxies 的 YAML 必须报错")
	}
	if _, err := Parse([]byte("this is not: [valid")); err == nil {
		t.Fatal("非法 YAML 必须报错")
	}
	result, err := Parse([]byte("proxies:\n  - {name: X, type: ss, server: s.example.com, port: 443}\n"))
	if err != nil {
		t.Fatalf("最小合法配置不该报错: %v", err)
	}
	if len(result.Nodes) != 1 {
		t.Fatalf("最小合法配置应解析出 1 个节点，实际 %d", len(result.Nodes))
	}
}

// TestParseSeparatesSubscriptionInfoNodes：订阅方会把"剩余流量/套餐到期/续费网址"塞进 proxies，
// 它们形状上是合法节点、实际连不通 —— 必须不进节点池，但要保留信息（用户想看到还剩多少流量）。
// 用例原文取自用户真实订阅里出现过的三条，外加一条"名字带流量但是真节点"的反向对照防误伤。
func TestParseSeparatesSubscriptionInfoNodes(t *testing.T) {
	const yamlWithInfo = `
proxies:
  - {name: "剩余流量：728.26 GB", type: ss, server: info.example.com, port: 49642, cipher: aes-128-gcm, password: x}
  - {name: "套餐到期：长期有效", type: ss, server: info.example.com, port: 49642, cipher: aes-128-gcm, password: x}
  - {name: "✅续费网址:https://getvv.cloud", type: ss, server: info.example.com, port: 49642, cipher: aes-128-gcm, password: x}
  - {name: "专线A1-美国1-4倍率", type: ss, server: real.example.com, port: 49642, cipher: aes-128-gcm, password: x}
  - {name: "日本-流量专线", type: ss, server: real2.example.com, port: 443, cipher: aes-128-gcm, password: x}
`
	result, err := Parse([]byte(yamlWithInfo))
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if len(result.Nodes) != 2 {
		names := []string{}
		for _, n := range result.Nodes {
			names = append(names, n.Name)
		}
		t.Fatalf("真节点数 = %d（%v），期望 2：三条运营信息不算节点，而\"日本-流量专线\"是真节点不能误伤", len(result.Nodes), names)
	}
	if len(result.Infos) != 3 {
		t.Fatalf("信息条数 = %d（%v），期望 3", len(result.Infos), result.Infos)
	}
	joined := strings.Join(result.Infos, " | ")
	if !strings.Contains(joined, "728.26 GB") || !strings.Contains(joined, "长期有效") {
		t.Fatalf("信息内容丢失: %v", result.Infos)
	}
	if !strings.Contains(strings.Join(result.Notes, " "), "订阅信息") {
		t.Fatalf("未记 Note 说明跳过了什么: %v", result.Notes)
	}
}

func TestRedactURLHidesToken(t *testing.T) {
	cases := map[string]string{
		"https://f.vvud.us/s/ca4ce730f09c4b2f72554343120e074f": "https://f.vvud.us/s/***",
		"https://example.com":  "https://example.com/",
		"https://example.com/": "https://example.com/",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Fatalf("RedactURL(%q) = %q，期望 %q", in, got, want)
		}
	}
	if strings.Contains(RedactURL("https://f.vvud.us/s/ca4ce730f09c4b2f72554343120e074f"), "ca4ce730") {
		t.Fatal("脱敏后仍带令牌")
	}
}

func keys(m map[string]Node) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
