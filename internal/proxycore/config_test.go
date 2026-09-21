package proxycore

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/secret"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

// R-proxy-001 内核配置生成的判据。
//
// 这里最要紧的三条：
//  ① 一个启用节点 = 一个 http 入站端口（不是"单端口 + 切节点"，那样并发请求会串号）；
//  ② 不占 mixed-port/port 这些"人尽皆知"的代理端口（不然会和用户自己的 Clash 抢口）；
//  ③ 节点参数必须来自密文解密，解不开就让整次生成失败 —— 不拿密文当参数喂给内核。

func prepareCoreDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "core.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("初始化测试库失败: %v", err)
	}
	conn := db.GetDB()
	if err := conn.AutoMigrate(&model.ProxyNode{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if sqlDB, err := conn.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	return conn
}

func TestBuildConfigOneListenerPerNode(t *testing.T) {
	conn := prepareCoreDB(t)
	t.Setenv("OCTOPUS_OFFICIAL_KEY", "proxy-core-test-key")
	secret.ResetKey()
	t.Cleanup(secret.ResetKey)

	nodes := []model.ProxyNode{
		{Name: "香港-01", Type: "ss", Server: "hk.example.com", Port: 443, LocalPort: 41001, Enabled: true},
		{Name: "日本-01", Type: "vmess", Server: "jp.example.com", Port: 8443, LocalPort: 41002, Enabled: true},
		{Name: "停用的", Type: "ss", Server: "off.example.com", Port: 443, LocalPort: 41003, Enabled: false},
		{Name: "没端口的", Type: "ss", Server: "noport.example.com", Port: 443, LocalPort: 0, Enabled: true},
	}
	for i := range nodes {
		params, err := secret.SealWith("pool:proxy-node", `{"cipher":"aes-128-gcm","password":"sk-node-secret"}`)
		if err != nil {
			t.Fatalf("加密失败: %v", err)
		}
		nodes[i].Params = params
		if err := conn.Create(&nodes[i]).Error; err != nil {
			t.Fatalf("建节点失败: %v", err)
		}
	}

	body, ports, err := BuildConfig(conn)
	if err != nil {
		t.Fatalf("生成配置失败: %v", err)
	}
	if len(ports) != 2 || ports[0] != 41001 || ports[1] != 41002 {
		t.Fatalf("入站端口 = %v，期望 [41001 41002]（停用与未分配端口的节点不该出现）", ports)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("生成的配置不是合法 YAML: %v", err)
	}
	for _, forbidden := range []string{"mixed-port", "port", "socks-port"} {
		if _, exists := doc[forbidden]; exists {
			t.Fatalf("配置里不该出现 %q（会与用户自己的代理工具抢端口）", forbidden)
		}
	}
	listeners, ok := doc["listeners"].([]any)
	if !ok || len(listeners) != 2 {
		t.Fatalf("listeners = %v，期望 2 个", doc["listeners"])
	}
	for _, raw := range listeners {
		entry := raw.(map[string]any)
		if entry["type"] != "http" {
			t.Fatalf("入站类型 = %v，期望 http（octopus 只会用 http 代理）", entry["type"])
		}
		if entry["listen"] != "127.0.0.1" {
			t.Fatalf("入站监听 = %v，必须只绑本机", entry["listen"])
		}
		if entry["proxy"] == nil || entry["proxy"] == "" {
			t.Fatalf("入站没有指定出口节点: %v", entry)
		}
	}
	proxies, ok := doc["proxies"].([]any)
	if !ok || len(proxies) != 2 {
		t.Fatalf("proxies = %v，期望 2 个", doc["proxies"])
	}
	first := proxies[0].(map[string]any)
	// 参数（密文里那部分）必须被摊平进来，否则内核连不上节点
	if first["password"] != "sk-node-secret" || first["cipher"] != "aes-128-gcm" {
		t.Fatalf("节点参数没有摊平进配置: %+v", first)
	}
	// 库里的列是权威：参数里若残留 name/server 不得覆盖
	raw, _ := json.Marshal(first)
	if strings.Contains(string(raw), "enc:v1:") {
		t.Fatalf("配置里出现了密文原文: %s", raw)
	}
}

// TestBuildConfigFailsOnUndecryptableParams：密文解不开必须整体失败（把密文喂给内核只会得到一堆看不懂的错）。
func TestBuildConfigFailsOnUndecryptableParams(t *testing.T) {
	conn := prepareCoreDB(t)
	t.Setenv("OCTOPUS_OFFICIAL_KEY", "key-a")
	secret.ResetKey()
	foreign, err := secret.SealWith("pool:proxy-node", `{"password":"x"}`)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	// 换一把不匹配的密钥再写库 → 读的时候必然解不开
	t.Setenv("OCTOPUS_OFFICIAL_KEY", "key-b")
	secret.ResetKey()
	t.Cleanup(secret.ResetKey)

	node := model.ProxyNode{Name: "异库节点", Type: "ss", Server: "n.example.com", Port: 443, LocalPort: 41010, Params: foreign, Enabled: true}
	if err := conn.Create(&node).Error; err != nil {
		t.Fatalf("建节点失败: %v", err)
	}
	if _, _, err := BuildConfig(conn); err == nil {
		t.Fatal("参数解不开时必须报错，而不是把密文当参数写进配置")
	}
}

// TestBuildConfigNoEnabledNodes：没有可用节点时给出可执行的提示（内核不该被拉起）。
func TestBuildConfigNoEnabledNodes(t *testing.T) {
	conn := prepareCoreDB(t)
	if _, _, err := BuildConfig(conn); err == nil {
		t.Fatal("没有启用节点时应当报错")
	}
}

// TestPortRangeFallsBackOnBadSettings：端口池设置越界时回落默认区间，而不是把内核丢到特权端口上。
func TestPortRangeFallsBackOnBadSettings(t *testing.T) {
	if start, end := PortRange(); start != DefaultPortStart || end != DefaultPortEnd {
		t.Fatalf("默认端口池 = %d-%d，期望 %d-%d（读不到设置时应回落默认）",
			start, end, DefaultPortStart, DefaultPortEnd)
	}
}
