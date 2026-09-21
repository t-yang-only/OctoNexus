package relay

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/secret"
	"gorm.io/gorm"
)

// R-proxy-001 出口解析的三条判据。
//
// 最要紧的不是"能走通"，而是**不能静默降级**：绑定了节点却因为内核没起来/节点被停用而解析不出出口时，
// 必须报错。静默退回直连等于把用户的真实出口暴露给上游 —— 那正好是这个功能要防的事。

// prepareProxyNodeDB 把全局库指到临时库并建好 proxy_nodes 表。
// relay 侧的出口解析走 op.ProxyNodeEndpoint（内部用全局句柄），所以测试必须真的初始化全局库。
func prepareProxyNodeDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "relay-proxy.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("初始化测试库失败: %v", err)
	}
	conn := db.GetDB()
	if conn == nil {
		t.Fatal("全局库为空")
	}
	if err := conn.AutoMigrate(&model.ProxyNode{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if sqlDB, err := conn.DB(); err == nil {
		// Windows 下不关连接，t.TempDir 收尾时删不掉库文件（实测报 "being used by another process"）
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	return conn
}

func TestResolveUpstreamClientUsesBoundNode(t *testing.T) {
	conn := prepareProxyNodeDB(t)

	// 一个真实的本地 HTTP 代理桩：记录它替谁转发过（这就是"出口"的可观测证据）。
	var forwarded []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = append(forwarded, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"via":"proxy-node"}`)
	}))
	defer proxy.Close()
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(proxy.URL, "http://"))
	port, _ := strconv.Atoi(portStr)

	node := model.ProxyNode{Name: "桩节点", Type: "http", Server: "127.0.0.1", Port: port, LocalPort: port, Enabled: true}
	if err := conn.Create(&node).Error; err != nil {
		t.Fatalf("建节点失败: %v", err)
	}

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "站点", Proxy: false}}
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Name: "账号", Key: "sk-x", Enabled: true, ProxyNodeID: node.ID}}

	// 账号级绑定优先于"渠道没开代理"：真实请求必须出现在代理桩上
	client, closeIdle, err := resolveUpstreamClientForKey(channel, &key)
	if err != nil {
		t.Fatalf("解析出口失败: %v", err)
	}
	if closeIdle != nil {
		defer closeIdle()
	}
	resp, err := client.Get("http://upstream.invalid/v1/models")
	if err != nil {
		t.Fatalf("经代理请求失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "proxy-node") {
		t.Fatalf("响应不是代理桩给的: %s", body)
	}
	if len(forwarded) == 0 {
		t.Fatal("代理桩没有收到任何请求：出口没有走节点")
	}
	if !strings.Contains(forwarded[0], "upstream.invalid") {
		t.Fatalf("代理桩收到的目标不对: %v", forwarded)
	}

	// 渠道级绑定：账号没绑、渠道绑了，同样要走节点
	channel.ProxyNodeID = node.ID
	client2, closeIdle2, err := resolveUpstreamClientForKey(channel, nil)
	if err != nil {
		t.Fatalf("渠道级出口解析失败: %v", err)
	}
	if closeIdle2 != nil {
		defer closeIdle2()
	}
	if _, err := client2.Get("http://upstream.invalid/v1/models"); err != nil {
		t.Fatalf("经渠道级节点请求失败: %v", err)
	}
	if len(forwarded) < 2 {
		t.Fatalf("渠道级绑定没有走节点，代理桩只收到 %d 次", len(forwarded))
	}
}

func TestResolveUpstreamClientRefusesUnreadyNode(t *testing.T) {
	conn := prepareProxyNodeDB(t)

	// ① 有绑定但内核还没分配端口 → 必须报错，绝不能静默直连
	pending := model.ProxyNode{Name: "未就绪", Type: "ss", Server: "n.example.com", Port: 443, LocalPort: 0, Enabled: true}
	if err := conn.Create(&pending).Error; err != nil {
		t.Fatalf("建节点失败: %v", err)
	}
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "站点"}}
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Name: "账号", Key: "sk-x", Enabled: true, ProxyNodeID: pending.ID}}
	if _, _, err := resolveUpstreamClientForKey(channel, &key); err == nil {
		t.Fatal("节点未就绪时必须报错（静默直连会泄露真实出口）")
	} else if !strings.Contains(err.Error(), "尚未就绪") {
		t.Fatalf("错误信息说明不了原因: %v", err)
	}

	// ② 节点被停用 → 同样拒绝
	disabled := model.ProxyNode{Name: "已停用", Type: "ss", Server: "n.example.com", Port: 443, LocalPort: 41000, Enabled: false}
	if err := conn.Create(&disabled).Error; err != nil {
		t.Fatalf("建节点失败: %v", err)
	}
	key.ProxyNodeID = disabled.ID
	if _, _, err := resolveUpstreamClientForKey(channel, &key); err == nil {
		t.Fatal("停用节点必须拒绝")
	} else if !strings.Contains(err.Error(), "已停用") {
		t.Fatalf("错误信息说明不了原因: %v", err)
	}

	// ③ 没有绑定 → 行为必须与改动前逐字一致（直连）
	key.ProxyNodeID = 0
	channel.Proxy = false
	if _, closeIdle, err := resolveUpstreamClientForKey(channel, &key); err != nil {
		t.Fatalf("未绑定时应走直连: %v", err)
	} else if closeIdle != nil {
		t.Fatal("直连用的是共享客户端，不该要求调用方归还连接")
	}
}

// TestProxyNodeEndpointIsEncryptedAtRestGuard：节点参数的密文形态在 relay 侧也要成立 ——
// 出口解析必须只认 LocalPort，不需要（也不该要求）解密节点参数。
func TestProxyNodeEndpointIgnoresParams(t *testing.T) {
	conn := prepareProxyNodeDB(t)
	t.Setenv("OCTOPUS_OFFICIAL_KEY", "relay-proxy-test-key")
	secret.ResetKey()
	t.Cleanup(secret.ResetKey)

	sealed, err := secret.SealWith("pool:proxy-node", `{"password":"secret-value"}`)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	node := model.ProxyNode{Name: "带密文", Type: "ss", Server: "n.example.com", Port: 443, LocalPort: 41999, Params: sealed, Enabled: true}
	if err := conn.Create(&node).Error; err != nil {
		t.Fatalf("建节点失败: %v", err)
	}
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "站点"}}
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Name: "账号", Key: "sk-x", Enabled: true, ProxyNodeID: node.ID}}
	client, closeIdle, err := resolveUpstreamClientForKey(channel, &key)
	if err != nil {
		t.Fatalf("出口解析不该依赖节点参数能否解密: %v", err)
	}
	if client == nil {
		t.Fatal("客户端为空")
	}
	if closeIdle != nil {
		closeIdle()
	}
}
