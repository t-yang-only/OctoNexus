package op

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/secret"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// R-proxy-001 代理节点池的落库与导入判据。
//
// 最要紧的一条：节点参数（ss password / vmess uuid / hysteria2 password）与订阅地址都是凭据，
// 必须**密文落库**，且加密不可用时拒绝写入而不是退回明文。这条不靠"接口返回 200"判，
// 直接查库内那一行的字节。

var proxyNodeDBSeq int64

func openProxyNodeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&proxyNodeDBSeq, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	if err := conn.AutoMigrate(&model.ProxyNode{}, &model.ProxySubscription{}, &model.Channel{}, &model.ChannelKey{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return conn
}

func useProxyCipherKey(t *testing.T, value string) {
	t.Helper()
	t.Setenv("OCTOPUS_OFFICIAL_KEY", value)
	secret.ResetKey()
	t.Cleanup(secret.ResetKey)
}

const proxyFixtureYAML = `
proxies:
  - {name: "N1", type: ss, server: n1.example.com, port: 443, cipher: aes-128-gcm, password: node-password-1}
  - {name: "N2", type: vmess, server: n2.example.com, port: 8443, uuid: node-uuid-2, alterId: 0}
`

func TestProxyNodeImportSealsParams(t *testing.T) {
	useProxyCipherKey(t, "proxy-node-test-key")
	conn := openProxyNodeTestDB(t)

	result, err := ProxyNodeImportYAML(conn, []byte(proxyFixtureYAML), "unit-sub", 7)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if result.Imported != 2 || result.Updated != 0 || result.Skipped != 0 {
		t.Fatalf("导入计数 = %+v，期望 imported=2", result)
	}

	// 库内那一行必须是密文：明文口令不能出现在任何列里
	var nodes []model.ProxyNode
	if err := conn.Order("id asc").Find(&nodes).Error; err != nil {
		t.Fatalf("读节点失败: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("节点数 = %d", len(nodes))
	}
	for _, node := range nodes {
		if !secret.IsSealed(node.Params) {
			t.Fatalf("节点 %q 的参数未加密落库: %q", node.Name, node.Params)
		}
		if strings.Contains(node.Params, "node-password-1") || strings.Contains(node.Params, "node-uuid-2") {
			t.Fatalf("节点 %q 的密文里出现明文凭据", node.Name)
		}
	}
	// 且必须能解回原值（加密不是把数据弄丢）
	plain, err := OpenProxyNodeParams(nodes[0])
	if err != nil {
		t.Fatalf("解不开自己刚写的参数: %v", err)
	}
	if !strings.Contains(plain, "node-password-1") {
		t.Fatalf("解出的参数缺少原字段: %s", plain)
	}
	if nodes[0].SubID != 7 || nodes[0].Source != "unit-sub" {
		t.Fatalf("来源标记丢失: %+v", nodes[0])
	}

	// 同一份内容再导一次：必须全部跳过（否则每次刷新都会全表重写）
	again, err := ProxyNodeImportYAML(conn, []byte(proxyFixtureYAML), "unit-sub", 7)
	if err != nil {
		t.Fatalf("二次导入失败: %v", err)
	}
	if again.Skipped != 2 || again.Imported != 0 || again.Updated != 0 {
		t.Fatalf("二次导入计数 = %+v，期望 skipped=2", again)
	}

	// 内容变了：必须更新而不是新增副本
	changed := strings.ReplaceAll(proxyFixtureYAML, "n1.example.com", "n1-new.example.com")
	third, err := ProxyNodeImportYAML(conn, []byte(changed), "unit-sub", 7)
	if err != nil {
		t.Fatalf("三次导入失败: %v", err)
	}
	if third.Updated != 1 || third.Imported != 0 {
		t.Fatalf("内容变化后计数 = %+v，期望 updated=1", third)
	}
	var total int64
	conn.Model(&model.ProxyNode{}).Count(&total)
	if total != 2 {
		t.Fatalf("节点总数 = %d，期望 2（更新不能产生副本）", total)
	}
}

// TestProxyNodeImportRefusesWithoutCipherKey：密钥不可用时必须拒绝写入，绝不退回明文。
func TestProxyNodeImportRefusesWithoutCipherKey(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("写占位文件失败: %v", err)
	}
	previous := conf.AppConfig.Database.Path
	conf.AppConfig.Database.Path = filepath.Join(blocker, "data.db")
	if err := os.Unsetenv("OCTOPUS_OFFICIAL_KEY"); err != nil {
		t.Fatalf("清环境变量失败: %v", err)
	}
	secret.ResetKey()
	t.Cleanup(func() {
		conf.AppConfig.Database.Path = previous
		secret.ResetKey()
	})

	conn := openProxyNodeTestDB(t)
	if _, err := ProxyNodeImportYAML(conn, []byte(proxyFixtureYAML), "unit-sub", 0); err == nil {
		t.Fatal("加密不可用时导入必须失败，而不是明文落库")
	}
	var count int64
	conn.Model(&model.ProxyNode{}).Count(&count)
	if count != 0 {
		t.Fatalf("拒绝写入后不该有任何行，实际 %d", count)
	}
}

func TestProxyNodeSubscriptionSealsURL(t *testing.T) {
	useProxyCipherKey(t, "proxy-node-test-key")
	conn := openProxyNodeTestDB(t)

	const token = "ca4ce730f09c4b2f72554343120e074f"
	sub, err := ProxySubscriptionCreate(conn, model.ProxySubscriptionInput{
		Name: "主订阅",
		URL:  "https://f.vvud.us/s/" + token,
	})
	if err != nil {
		t.Fatalf("建订阅失败: %v", err)
	}
	if !secret.IsSealed(sub.URLCipher) {
		t.Fatalf("订阅地址未加密: %q", sub.URLCipher)
	}
	if strings.Contains(sub.URLCipher, token) {
		t.Fatal("密文里出现订阅令牌")
	}
	if strings.Contains(sub.URLHint, token) {
		t.Fatalf("明文提示里泄露令牌: %q", sub.URLHint)
	}
	plain, err := secret.OpenWith(proxySubscriptionAAD, sub.URLCipher)
	if err != nil {
		t.Fatalf("解不开订阅地址: %v", err)
	}
	if !strings.HasSuffix(plain, token) {
		t.Fatalf("解出的地址不对: %q", plain)
	}
	// 出参形状：列表接口读出来的对象里不能有 Cipher 字段进 JSON（靠 json:"-" 兜住）
	if strings.Contains(mustJSONBytes(t, sub), token) || strings.Contains(mustJSONBytes(t, sub), "url_cipher") {
		t.Fatalf("订阅出参泄露了地址字段: %s", mustJSONBytes(t, sub))
	}
}

// TestProxyNodeDeleteRefusesWhenBound：被渠道/账号绑定时必须拒绝删除（否则代理静默失效）。
func TestProxyNodeDeleteRefusesWhenBound(t *testing.T) {
	useProxyCipherKey(t, "proxy-node-test-key")
	conn := openProxyNodeTestDB(t)
	result, err := ProxyNodeImportYAML(conn, []byte(proxyFixtureYAML), "unit-sub", 0)
	if err != nil || result.Imported != 2 {
		t.Fatalf("准备数据失败: %v %+v", err, result)
	}
	var nodes []model.ProxyNode
	conn.Order("id asc").Find(&nodes)

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "站点A", ProxyNodeID: nodes[0].ID}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "账号A", Key: "sk-a", Enabled: true, ProxyNodeID: nodes[1].ID}}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("建账号失败: %v", err)
	}

	if err := ProxyNodeDelete(conn, nodes[0].ID); err == nil {
		t.Fatal("被渠道绑定的节点必须拒绝删除")
	}
	if err := ProxyNodeDelete(conn, nodes[1].ID); err == nil {
		t.Fatal("被账号绑定的节点必须拒绝删除")
	}
	usage, err := ProxyNodeUsageMap(conn)
	if err != nil {
		t.Fatalf("usage 查询失败: %v", err)
	}
	if len(usage) != 2 {
		t.Fatalf("usage 应覆盖两个节点，实际 %+v", usage)
	}
	// 解绑后必须可删（拒绝不是永久的）
	if err := conn.Model(&model.Channel{}).Where("id = ?", channel.ID).Update("proxy_node_id", 0).Error; err != nil {
		t.Fatalf("解绑失败: %v", err)
	}
	if err := conn.Model(&model.ChannelKey{}).Where("id = ?", key.ID).Update("proxy_node_id", 0).Error; err != nil {
		t.Fatalf("解绑失败: %v", err)
	}
	if err := ProxyNodeDelete(conn, nodes[0].ID); err != nil {
		t.Fatalf("解绑后应可删除: %v", err)
	}
}

// mustJSONBytes 把出参序列化成 JSON 字符串（断言"接口出参里没有凭据字段"用）。
func mustJSONBytes(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	return string(raw)
}
