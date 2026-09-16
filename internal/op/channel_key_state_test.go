package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// 人工停用必须扛得住号池同步。
//
// 这一条是被实际代码逼出来的：OfficialPoolSync 对 active 账号是无条件
// `updates["enabled"] = true`，也就是说"人工停掉一条坏凭据"会在下一次同步时被静默撤销。
// 本测试盯的就是这个交互——它不是"启停能不能写库"（那是次要的），而是"操作者的决定会不会被推翻"。
func TestOfficialPoolSyncRespectsOperatorDisabled(t *testing.T) {
	withOfficialKey(t, "pool-cipher-key-hold")
	conn := openOfficialPoolTestDB(t)
	withPoolRefresher(t, &stubRefresher{})
	account := seedPoolAccount(t, conn, model.OfficialAccountProviderOpenAI, poolAccountSeed{
		name: "op-held", status: model.OfficialAccountStatusActive, access: "access-op-held",
	})

	first, err := OfficialPoolSync(conn, model.OfficialAccountProviderOpenAI)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if first.Keys != 1 {
		t.Fatalf("first sync keys = %d, want 1", first.Keys)
	}
	keyName := account.ExternalName

	// 人工停用：enabled 落下，同时带上"人工停用"这一位。
	found, err := SetChannelKeyEnabled(conn, first.ChannelID, keyName, false)
	if err != nil || !found {
		t.Fatalf("SetChannelKeyEnabled(false) = (%v, %v), want (true, nil)", found, err)
	}
	held := poolKeysOf(t, conn, first.ChannelID)[keyName]
	if held.Enabled || !held.OperatorDisabled {
		t.Fatalf("after disable: enabled=%v operator_disabled=%v, want false/true", held.Enabled, held.OperatorDisabled)
	}

	// 关键断言：账号仍是 active（同步的本职工作是"账号能用就启用"），但同步不能推翻人工停用。
	second, err := OfficialPoolSync(conn, model.OfficialAccountProviderOpenAI)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	afterSync := poolKeysOf(t, conn, first.ChannelID)[keyName]
	if afterSync.Enabled {
		t.Fatalf("sync re-enabled an operator-disabled key: %+v", afterSync)
	}
	if !afterSync.OperatorDisabled {
		t.Fatalf("sync cleared the operator-disabled flag: %+v", afterSync)
	}
	if second.Held != 1 {
		t.Fatalf("sync held = %d, want 1（结论要如实说明有一条被人工按住）", second.Held)
	}
	if second.Keys != 0 {
		t.Fatalf("sync keys = %d, want 0（被按住的不算启用凭据）", second.Keys)
	}

	// 只读视图也要把这两种"停用"区分开，否则界面上看不出该不该人工介入。
	statuses, err := OfficialPoolStatusList(conn)
	if err != nil {
		t.Fatalf("status list: %v", err)
	}
	var member *model.OfficialPoolMember
	for _, status := range statuses {
		if status.Provider != model.OfficialAccountProviderOpenAI {
			continue
		}
		for i := range status.Members {
			if status.Members[i].AccountID == account.ID {
				member = &status.Members[i]
			}
		}
	}
	if member == nil {
		t.Fatal("status list 里找不到刚种的账号")
	}
	if member.KeyEnabled || !member.KeyOperatorDisabled {
		t.Fatalf("member 视图没区分人工停用: enabled=%v operator_disabled=%v",
			member.KeyEnabled, member.KeyOperatorDisabled)
	}

	// 人工再启：这一位要一起清掉，之后同步照常维护它。
	if found, err := SetChannelKeyEnabled(conn, first.ChannelID, keyName, true); err != nil || !found {
		t.Fatalf("SetChannelKeyEnabled(true) = (%v, %v), want (true, nil)", found, err)
	}
	back := poolKeysOf(t, conn, first.ChannelID)[keyName]
	if !back.Enabled || back.OperatorDisabled {
		t.Fatalf("after enable: enabled=%v operator_disabled=%v, want true/false", back.Enabled, back.OperatorDisabled)
	}
	third, err := OfficialPoolSync(conn, model.OfficialAccountProviderOpenAI)
	if err != nil {
		t.Fatalf("third sync: %v", err)
	}
	if third.Keys != 1 || third.Held != 0 {
		t.Fatalf("third sync keys/held = %d/%d, want 1/0", third.Keys, third.Held)
	}
}

// 渠道页与号池 API 是两个写同一件事的入口，规则必须一致：
//   - 页面提交 enabled=false（显式关）= 人工停用，号池同步不许把它打开；
//   - 页面提交 enabled=true（显式开）= 解开人工停用，之后交回同步维护；
//   - 页面没动这一条（值没变）就什么都不写，人工停用自然保留。
func TestChannelUpdateKeepsOperatorDecisionCoherent(t *testing.T) {
	withOfficialKey(t, "pool-cipher-key-edit")
	conn := openOfficialPoolTestDB(t)
	withPoolRefresher(t, &stubRefresher{})
	seedPoolAccount(t, conn, model.OfficialAccountProviderOpenAI, poolAccountSeed{
		name: "op-edit", status: model.OfficialAccountStatusActive, access: "access-op-edit",
	})
	synced, err := OfficialPoolSync(conn, model.OfficialAccountProviderOpenAI)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	submitted := func(enabled bool) {
		t.Helper()
		if err := syncChannelKeys(conn, synced.ChannelID, []model.ChannelKeyInput{
			{Name: "op-edit", Key: "access-op-edit", Enabled: boolPtr(enabled)},
		}); err != nil {
			t.Fatalf("syncChannelKeys(%v): %v", enabled, err)
		}
	}

	// 页面上关掉它 = 人工停用：同步不许打开。
	submitted(false)
	off := poolKeysOf(t, conn, synced.ChannelID)["op-edit"]
	if off.Enabled || !off.OperatorDisabled {
		t.Fatalf("页面停用后 enabled=%v operator_disabled=%v, want false/true", off.Enabled, off.OperatorDisabled)
	}
	res, err := OfficialPoolSync(conn, model.OfficialAccountProviderOpenAI)
	if err != nil || res.Held != 1 {
		t.Fatalf("同步结论没如实说有一条被按住: held=%d err=%v", res.Held, err)
	}
	if again := poolKeysOf(t, conn, synced.ChannelID)["op-edit"]; again.Enabled {
		t.Fatalf("同步把页面停用又打开了: %+v", again)
	}

	// 再提交一次同样的"关"：值没变就不该产生写事务，人工停用原样保留。
	submitted(false)
	if keep := poolKeysOf(t, conn, synced.ChannelID)["op-edit"]; !keep.OperatorDisabled || keep.Enabled {
		t.Fatalf("重复提交把状态弄乱了: %+v", keep)
	}

	// 页面上重新打开它 = 解开人工停用：之后同步照常维护。
	submitted(true)
	on := poolKeysOf(t, conn, synced.ChannelID)["op-edit"]
	if !on.Enabled || on.OperatorDisabled {
		t.Fatalf("页面启用后 enabled=%v operator_disabled=%v, want true/false", on.Enabled, on.OperatorDisabled)
	}
	if res, err := OfficialPoolSync(conn, model.OfficialAccountProviderOpenAI); err != nil || res.Keys != 1 || res.Held != 0 {
		t.Fatalf("解开后同步 keys/held = %d/%d err=%v, want 1/0", res.Keys, res.Held, err)
	}
}

func TestSetChannelKeyEnabledUnknownAndInvalid(t *testing.T) {
	conn := openOfficialPoolTestDB(t)

	// 渠道存在但没有这条凭据：明确回"没命中"，由接口层决定语义（这里不该是 error）。
	found, err := SetChannelKeyEnabled(conn, 4321, "no-such-key", false)
	if err != nil || found {
		t.Fatalf("unknown key = (%v, %v), want (false, nil)", found, err)
	}

	// 参数不合法要当场拒绝，而不是去库里胡乱匹配。
	if _, err := SetChannelKeyEnabled(conn, 0, "k", false); err == nil {
		t.Fatal("channel id 0 应当报错")
	}
	if _, err := SetChannelKeyEnabled(conn, 1, "", false); err == nil {
		t.Fatal("空凭据名应当报错")
	}
}

func boolPtr(value bool) *bool {
	return &value
}
