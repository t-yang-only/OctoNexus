package op

import (
	"context"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestChannelUpdatePersistsProxyNodeBinding 渠道级出口节点的落库判据（R-proxy-001 演练抓出的真缺陷）。
//
// 缺陷现场: ChannelUpdate 用「逐列点名」的 Select 更新, 那份名单里漏了 proxy_node_id。
// 结果是: 面板上选好出口节点、接口回 200、当前进程里也看不出异常, 但**库里那一行始终是 0**;
// 重启之后渠道重新直连真实出口 —— "给渠道选出口"这个功能等于没生效, 而且完全没有报错。
// 判据取库内行（重启后还会存在的事实）, 并把"改回直连(0)"也纳入: 清零是最容易被按零值跳过的一类写入。
func TestChannelUpdatePersistsProxyNodeBinding(t *testing.T) {
	openChannelKeyTestDB(t)

	created, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "DS-UNIT-proxynode", BaseURL: "http://127.0.0.1:1", Enabled: true, ProxyNodeID: 7},
		Keys:          []model.ChannelKeyInput{{Name: "k1", Key: "sk-proxynode"}},
		Models:        []string{"mock-good"},
	}, context.Background())
	if err != nil {
		t.Fatalf("ChannelCreate: %v", err)
	}
	var row model.Channel
	if err := dbGetChannel(t, created.ID, &row); err != nil {
		t.Fatalf("query channel: %v", err)
	}
	if row.ProxyNodeID != 7 {
		t.Fatalf("新建后 proxy_node_id = %d, want 7", row.ProxyNodeID)
	}

	// 面板保存走的就是这条路径: 逐列点名更新。
	if _, err := ChannelUpdate(&model.ChannelDetail{
		ID:            created.ID,
		ChannelConfig: model.ChannelConfig{Name: "DS-UNIT-proxynode", BaseURL: "http://127.0.0.1:1", Enabled: true, ProxyNodeID: 9},
		Keys:          []model.ChannelKeyInput{{Name: "k1", Key: "sk-proxynode"}},
		Models:        []string{"mock-good"},
	}, context.Background()); err != nil {
		t.Fatalf("ChannelUpdate: %v", err)
	}
	if err := dbGetChannel(t, created.ID, &row); err != nil {
		t.Fatalf("query channel: %v", err)
	}
	if row.ProxyNodeID != 9 {
		t.Fatalf("保存后 proxy_node_id = %d, want 9（Select 名单漏列会让它只在缓存里, 重启即丢）", row.ProxyNodeID)
	}

	// 逆向对照: 改回直连也必须真的写回去。
	if _, err := ChannelUpdate(&model.ChannelDetail{
		ID:            created.ID,
		ChannelConfig: model.ChannelConfig{Name: "DS-UNIT-proxynode", BaseURL: "http://127.0.0.1:1", Enabled: true},
		Keys:          []model.ChannelKeyInput{{Name: "k1", Key: "sk-proxynode"}},
		Models:        []string{"mock-good"},
	}, context.Background()); err != nil {
		t.Fatalf("ChannelUpdate(clear): %v", err)
	}
	if err := dbGetChannel(t, created.ID, &row); err != nil {
		t.Fatalf("query channel: %v", err)
	}
	if row.ProxyNodeID != 0 {
		t.Fatalf("清空后 proxy_node_id = %d, want 0", row.ProxyNodeID)
	}
}

// TestChannelUpdateKeepsManualBalance 渠道保存不得把人工录入的余额在**缓存里**抹成零。
//
// 缺陷现场: ChannelUpdate 末尾用 `model.Channel{ID, ChannelConfig: detail.ChannelConfig}` 重建缓存条目,
// 而人工余额三列（balance_points/balance_at/balance_note）不在表单载荷里 —— 库里的值还在,
// 面板却显示"未录入", 一直持续到重启。这条判据同时看缓存与库: 只看库会漏（库内本来就是对的）。
func TestChannelUpdateKeepsManualBalance(t *testing.T) {
	openChannelKeyTestDB(t)

	created, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "DS-UNIT-manual", BaseURL: "http://127.0.0.1:1", Enabled: true},
		Keys:          []model.ChannelKeyInput{{Name: "k1", Key: "sk-manual"}},
		Models:        []string{"mock-good"},
	}, context.Background())
	if err != nil {
		t.Fatalf("ChannelCreate: %v", err)
	}
	if err := ChannelManualBalanceSet(created.ID, 2500000, "预付 5 美元"); err != nil {
		t.Fatalf("ChannelManualBalanceSet: %v", err)
	}

	// 面板再保存一次这个渠道（表单里没有余额三列）。
	if _, err := ChannelUpdate(&model.ChannelDetail{
		ID:            created.ID,
		ChannelConfig: model.ChannelConfig{Name: "DS-UNIT-manual", BaseURL: "http://127.0.0.1:1", Enabled: true},
		Keys:          []model.ChannelKeyInput{{Name: "k1", Key: "sk-manual"}},
		Models:        []string{"mock-good"},
	}, context.Background()); err != nil {
		t.Fatalf("ChannelUpdate: %v", err)
	}

	var row model.Channel
	if err := dbGetChannel(t, created.ID, &row); err != nil {
		t.Fatalf("query channel: %v", err)
	}
	if row.BalancePoints != 2500000 || row.BalanceNote != "预付 5 美元" {
		t.Fatalf("库内人工余额 = %v/%q, want 2500000/预付 5 美元", row.BalancePoints, row.BalanceNote)
	}
	cached, err := ChannelGet(created.ID)
	if err != nil {
		t.Fatalf("ChannelGet: %v", err)
	}
	if cached.BalancePoints != 2500000 || cached.BalanceNote != "预付 5 美元" {
		t.Fatalf("缓存里的人工余额被保存抹掉了: %v/%q（面板会显示成未录入, 直到重启）", cached.BalancePoints, cached.BalanceNote)
	}
}
