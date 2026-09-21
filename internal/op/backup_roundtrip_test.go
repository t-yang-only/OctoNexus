package op

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/secret"
)

// TestDBDumpRoundTripsProxyAndAccountTables 备份-恢复必须带上出口节点池、代理订阅、官方账号与手动订阅。
//
// 为什么这条要有: 这四张表此前不在转储里, 而 channels.proxy_node_id / channel_keys.proxy_node_id
// 照旧会被导出 —— 恢复后的表现是"渠道绑着一个不存在的节点", 转发 fail-closed 全部报错,
// 且备份本身看不出缺了字段。判据取"密文原样往返 + 换库后能解出原值", 而不是只看行数:
// 三个模型的凭据字段带 json:"-"（防接口泄露）, 只要转储形状写错, 行数一样对、凭据却没了。
func TestDBDumpRoundTripsProxyAndAccountTables(t *testing.T) {
	useImportCipherKey(t, "roundtrip-cipher-key")
	source := openImportTestDB(t)
	restore := db.SetDBForTest(source)
	defer restore()

	nodeParams, err := secret.SealWith(proxyNodeAAD, `{"password":"node-secret"}`)
	if err != nil {
		t.Fatalf("seal node params: %v", err)
	}
	subURL, err := secret.SealWith(proxySubscriptionAAD, "https://example.invalid/s/token-abc")
	if err != nil {
		t.Fatalf("seal subscription url: %v", err)
	}
	access, err := officialEncrypt(model.OfficialAccountProviderOpenAI, "access-token-value")
	if err != nil {
		t.Fatalf("encrypt official access: %v", err)
	}

	node := model.ProxyNode{ID: 7, Name: "rt-node", Type: "ss", Server: "198.51.100.7", Port: 8388, Params: nodeParams, SubID: 3, LocalPort: 41007, Enabled: true}
	if err := source.Create(&node).Error; err != nil {
		t.Fatalf("seed proxy node: %v", err)
	}
	sub := model.ProxySubscription{ID: 3, Name: "rt-sub", URLCipher: subURL, URLHint: "https://example.invalid/s/***", Enabled: true, NodeCount: 1}
	if err := source.Create(&sub).Error; err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	account := model.OfficialAccount{ID: 5, Provider: model.OfficialAccountProviderOpenAI, ExternalName: "rt@example.com", Status: model.OfficialAccountStatusActive, AccessCipher: access}
	if err := source.Create(&account).Error; err != nil {
		t.Fatalf("seed official account: %v", err)
	}
	manual := model.ManualSubscription{ID: 9, Name: "rt-manual", Enabled: true}
	if err := source.Create(&manual).Error; err != nil {
		t.Fatalf("seed manual subscription: %v", err)
	}

	dump, err := DBExportAll(context.Background())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(dump.ProxyNodes) != 1 || len(dump.ProxySubscriptions) != 1 || len(dump.OfficialAccounts) != 1 || len(dump.ManualSubscriptions) != 1 {
		t.Fatalf("导出条数 = nodes:%d subs:%d accounts:%d manual:%d, want 1/1/1/1",
			len(dump.ProxyNodes), len(dump.ProxySubscriptions), len(dump.OfficialAccounts), len(dump.ManualSubscriptions))
	}

	// 走一次真实的 JSON 往返: 接口导入拿到的就是这一步之后的形状。
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatalf("marshal dump: %v", err)
	}
	for _, needle := range []string{`"params"`, `"url_cipher"`, `"access_cipher"`} {
		if !strings.Contains(string(raw), needle) {
			t.Fatalf("转储 JSON 里没有 %s: 凭据字段被 json:\"-\" 吞掉了", needle)
		}
	}
	restored := &model.DBDump{}
	if err := json.Unmarshal(raw, restored); err != nil {
		t.Fatalf("unmarshal dump: %v", err)
	}

	target := openImportTestDB(t)
	db.SetDBForTest(target)
	res, err := DBImportIncremental(context.Background(), restored)
	if err != nil {
		t.Fatalf("import into fresh db: %v", err)
	}
	for _, table := range []string{"proxy_nodes", "proxy_subscriptions", "official_accounts", "manual_subscriptions"} {
		if res.RowsAffected[table] != 1 {
			t.Fatalf("%s rows = %d, want 1 (rows_affected=%v)", table, res.RowsAffected[table], res.RowsAffected)
		}
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("同实例备份不该有告警: %v", res.Warnings)
	}

	var gotNode model.ProxyNode
	if err := target.First(&gotNode, 7).Error; err != nil {
		t.Fatalf("load restored node: %v", err)
	}
	plain, err := secret.OpenWith(proxyNodeAAD, gotNode.Params)
	if err != nil {
		t.Fatalf("恢复后的节点参数解不开: %v", err)
	}
	if plain != `{"password":"node-secret"}` {
		t.Fatalf("节点参数 = %q", plain)
	}
	if gotNode.LocalPort != 41007 || !gotNode.Enabled || gotNode.SubID != 3 {
		t.Fatalf("节点字段没原样恢复: %+v", gotNode)
	}

	var gotSub model.ProxySubscription
	if err := target.First(&gotSub, 3).Error; err != nil {
		t.Fatalf("load restored subscription: %v", err)
	}
	if url, err := secret.OpenWith(proxySubscriptionAAD, gotSub.URLCipher); err != nil || url != "https://example.invalid/s/token-abc" {
		t.Fatalf("订阅地址恢复失败: url=%q err=%v", url, err)
	}

	var gotAccount model.OfficialAccount
	if err := target.First(&gotAccount, 5).Error; err != nil {
		t.Fatalf("load restored account: %v", err)
	}
	if token, err := officialDecrypt(gotAccount.Provider, gotAccount.AccessCipher); err != nil || token != "access-token-value" {
		t.Fatalf("官方账号凭据恢复失败: token=%q err=%v", token, err)
	}

	var gotManual model.ManualSubscription
	if err := target.First(&gotManual, 9).Error; err != nil {
		t.Fatalf("load restored manual subscription: %v", err)
	}
}

// TestDBImportWarnsOnForeignProxySecrets 跨实例恢复时的如实回报: 密文带回来了、但换过密钥就解不开。
//
// 负向对照是这条用例的核心: 同一份数据在"同密钥"下必须**没有**告警（见上一条用例），
// 只有换了密钥才报 —— 否则告警恒亮等于没告警。
func TestDBImportWarnsOnForeignProxySecrets(t *testing.T) {
	foreignKey := "foreign-cipher-key"
	useImportCipherKey(t, foreignKey)
	foreignParams, err := secret.SealWith(proxyNodeAAD, `{"password":"made-elsewhere"}`)
	if err != nil {
		t.Fatalf("seal foreign params: %v", err)
	}
	foreignURL, err := secret.SealWith(proxySubscriptionAAD, "https://elsewhere.invalid/s/tok")
	if err != nil {
		t.Fatalf("seal foreign url: %v", err)
	}
	foreignAccess, err := officialEncrypt(model.OfficialAccountProviderOpenAI, "elsewhere-token")
	if err != nil {
		t.Fatalf("encrypt foreign access: %v", err)
	}

	// 换一把密钥（模拟"备份来自另一实例 / credential.key 已更换"）。
	useImportCipherKey(t, "this-instance-key")
	conn := openImportTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	dump := &model.DBDump{
		Version:            dbDumpVersion,
		ProxyNodes:         []model.ProxyNodeExport{{ProxyNode: model.ProxyNode{ID: 1, Name: "foreign-node", Type: "ss", Server: "198.51.100.9", Port: 443, Enabled: true}, ParamsOut: foreignParams}},
		ProxySubscriptions: []model.ProxySubscriptionExport{{ProxySubscription: model.ProxySubscription{ID: 2, Name: "foreign-sub", Enabled: true}, URLCipherOut: foreignURL}},
		OfficialAccounts:   []model.OfficialAccountExport{{OfficialAccount: model.OfficialAccount{ID: 3, Provider: model.OfficialAccountProviderOpenAI, ExternalName: "f@example.com", Status: model.OfficialAccountStatusActive}, AccessCipherOut: foreignAccess}},
	}
	res, err := DBImportIncremental(context.Background(), dump)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.RowsAffected["proxy_nodes"] != 1 {
		t.Fatalf("解不开也不该拒收行（要与渠道凭据同一口径）: %v", res.RowsAffected)
	}
	joined := strings.Join(res.Warnings, " | ")
	for _, want := range []string{"出口节点", "代理订阅", "官方账号"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("告警里没有提到%s: %v", want, res.Warnings)
		}
	}
}
