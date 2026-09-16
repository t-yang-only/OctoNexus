package pool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fixedEntries 是查询面单测的公共夹具：覆盖启用/停用、健康/不健康、带错、临期、过期、无到期时间。
func fixedEntries(now time.Time) []Entry {
	expiring := now.Add(2 * time.Hour)
	expired := now.Add(-time.Hour)
	return []Entry{
		{ID: "1", Name: "alpha", Provider: "gemini", Status: "active", Enabled: true, Healthy: true, ExpiresAt: &expiring},
		{ID: "2", Name: "Beta", Provider: "openai", Status: "revoked", Enabled: false, Healthy: false, LastError: "401", ExpiresAt: &expired},
		{ID: "3", Name: "gamma", Provider: "claude", Status: "active", Enabled: false, Healthy: true},
	}
}

func TestParseFilter(t *testing.T) {
	filter, err := ParseFilter("official", "gemini", "active", "alp", "true", "1", "3600", "yes")
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	if filter.Kind != "official" || filter.Provider != "gemini" || filter.Status != "active" || filter.Query != "alp" {
		t.Fatalf("基础字段解析错: %+v", filter)
	}
	if filter.Enabled == nil || !*filter.Enabled || filter.Healthy == nil || !*filter.Healthy {
		t.Fatalf("布尔参数解析错: %+v", filter)
	}
	if filter.Expiring != time.Hour {
		t.Fatalf("expiring_within 应为 1 小时: %v", filter.Expiring)
	}
	if filter.HasExpiry == nil || !*filter.HasExpiry {
		t.Fatalf("has_expiry 解析错: %+v", filter)
	}

	// 非法取值必须报错（而不是静默当成 false——那会让"过滤没生效"看起来像"没有数据"）。
	for _, bad := range []struct{ enabled, expiring, hasExpiry string }{
		{"maybe", "", ""}, {"", "-5", ""}, {"", "abc", ""}, {"", "", "perhaps"},
	} {
		if _, err := ParseFilter("", "", "", "", bad.enabled, "", bad.expiring, bad.hasExpiry); err == nil {
			t.Errorf("非法取值应报错: %+v", bad)
		}
	}

	// 空字符串 = 不过滤。
	empty, err := ParseFilter("", "", "", "", "", "", "", "")
	if err != nil || empty.Enabled != nil || empty.Healthy != nil || empty.Expiring != 0 {
		t.Fatalf("空查询参数应全部不过滤: %+v / %v", empty, err)
	}
}

func TestListFilters(t *testing.T) {
	now := time.Now()
	withFake(t, fakeAdapter{kind: "f", entries: fixedEntries(now)})
	ctx := context.Background()

	all, err := List(ctx, Filter{Kind: "f"}, now)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if all.Total != 3 || all.Scanned != 3 {
		t.Fatalf("全量 = %+v", all)
	}

	cases := []struct {
		name   string
		filter Filter
		want   []string
	}{
		{"按 provider", Filter{Kind: "f", Provider: "GEMINI"}, []string{"1"}}, // 大小写不敏感
		{"按状态", Filter{Kind: "f", Status: "revoked"}, []string{"2"}},
		{"按启用", Filter{Kind: "f", Enabled: boolPtr(true)}, []string{"1"}},
		{"按健康", Filter{Kind: "f", Healthy: boolPtr(false)}, []string{"2"}},
		{"按名称子串（大小写不敏感）", Filter{Kind: "f", Query: "BET"}, []string{"2"}},
		{"按 ID 子串", Filter{Kind: "f", Query: "3"}, []string{"3"}},
		{"只看临期（2 小时内）", Filter{Kind: "f", Expiring: 3 * time.Hour}, []string{"1"}},
		{"只看有到期时间", Filter{Kind: "f", HasExpiry: boolPtr(true)}, []string{"1", "2"}},
		{"只看无到期时间", Filter{Kind: "f", HasExpiry: boolPtr(false)}, []string{"3"}},
		{"组合：启用且健康", Filter{Kind: "f", Enabled: boolPtr(true), Healthy: boolPtr(true)}, []string{"1"}},
	}
	for _, tc := range cases {
		result, err := List(ctx, tc.filter, now)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(result.Items) != len(tc.want) || result.Scanned != 3 {
			t.Fatalf("%s: got %d 条（scanned=%d）, want %v", tc.name, len(result.Items), result.Scanned, tc.want)
		}
		for i, wantID := range tc.want {
			if result.Items[i].ID != wantID {
				t.Fatalf("%s: 第 %d 条 = %s, want %s", tc.name, i, result.Items[i].ID, wantID)
			}
		}
	}

	// 过滤后为空也要如实回 scanned, 便于区分"被过滤掉"与"后端本来就空"。
	empty, err := List(ctx, Filter{Kind: "f", Status: "nope"}, now)
	if err != nil || empty.Total != 0 || empty.Scanned != 3 {
		t.Fatalf("空结果 = %+v / %v", empty, err)
	}
}

func TestSummaryOf(t *testing.T) {
	now := time.Now()
	withFake(t, fakeAdapter{kind: "s", entries: fixedEntries(now)})
	withFake(t, fakeAdapter{kind: "broken-s", err: errFake("上游挂了")})

	summary := SummaryOf(context.Background(), now)
	// 夹具 3 条: 启用 1 / 停用 2; 健康 2 / 不健康 1; 带错 1; 临期 1; 过期 1。
	if summary.Total != 3 || summary.Enabled != 1 || summary.Disabled != 2 {
		t.Fatalf("启用/停用计数 = %+v", summary)
	}
	if summary.Healthy != 2 || summary.Unhealthy != 1 || summary.WithError != 1 {
		t.Fatalf("健康/带错计数 = %+v", summary)
	}
	if summary.ExpiringSoon != 1 || summary.Expired != 1 {
		t.Fatalf("到期窗口计数 = %+v", summary)
	}
	if summary.ByProvider["gemini"] != 1 || summary.ByProvider["openai"] != 1 || summary.ByProvider["claude"] != 1 {
		t.Fatalf("分服务商计数 = %+v", summary.ByProvider)
	}
	if summary.ByStatus["active"] != 2 || summary.ByStatus["revoked"] != 1 {
		t.Fatalf("分状态计数 = %+v", summary.ByStatus)
	}
	if summary.ByKind["s"].Entries != 3 {
		t.Fatalf("分后端计数 = %+v", summary.ByKind)
	}
	// 坏后端要出现在 kind_errors 里：让"空"与"坏"可区分。
	found := false
	for _, failure := range summary.KindErrors {
		if failure.Kind == "broken-s" {
			found = true
		}
	}
	if !found {
		t.Fatalf("坏后端应进 kind_errors: %+v", summary.KindErrors)
	}
}

func TestExportRowsNoCredentialFields(t *testing.T) {
	now := time.Now()
	withFake(t, fakeAdapter{kind: "e", entries: fixedEntries(now)})

	rows, _, err := ExportRows(context.Background(), "e")
	if err != nil || len(rows) != 3 {
		t.Fatalf("ExportRows = %d 行 / %v", len(rows), err)
	}
	// 导出结构里不允许出现任何凭据字段名（防止以后有人往导出行里加 token）。
	blob, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	lowered := strings.ToLower(string(blob))
	for _, forbidden := range []string{"token", "cipher", "secret", "password", "api_key", "apikey"} {
		if strings.Contains(lowered, forbidden) {
			t.Fatalf("导出行出现了疑似凭据字段 %q: %s", forbidden, lowered)
		}
	}
	// 排序稳定：kind 相同则按 provider 再按 name。
	if rows[0].Provider != "claude" || rows[1].Provider != "gemini" || rows[2].Provider != "openai" {
		t.Fatalf("导出排序不稳定: %+v", rows)
	}
}

func TestOperationsManifestFollowsCapabilities(t *testing.T) {
	// 只声明 list：清单里只能有 list 类路由。
	listOnly := operationsOf([]Capability{CapList})
	for _, operation := range listOnly {
		if operation.Capability != CapList {
			t.Fatalf("只声明 list 却出现 %+v", operation)
		}
	}

	// 内置官方账号池：应有 get/probe/refresh/sync 的路由，且**不应有** enable/disable（没声明 toggle）。
	var official AdapterInfo
	for _, info := range Kinds() {
		if info.Kind == officialKind {
			official = info
		}
	}
	if len(official.Operations) == 0 {
		t.Fatal("Kinds() 应回填 Operations")
	}
	paths := map[string]string{}
	for _, operation := range official.Operations {
		key := string(operation.Capability) + ":" + operation.Method
		paths[key] = operation.Path
	}
	for _, want := range []struct {
		capability Capability
		method     string
	}{
		{CapGet, "GET"}, {CapProbe, "POST"}, {CapRefresh, "POST"}, {CapSync, "POST"},
	} {
		key := string(want.capability) + ":" + want.method
		if _, ok := paths[key]; !ok {
			t.Errorf("官方账号池清单缺 %s: %+v", key, official.Operations)
		}
	}
	for _, operation := range official.Operations {
		if operation.Capability == CapToggle {
			t.Errorf("官方账号池没声明 toggle, 清单里不该有启停路由: %+v", operation)
		}
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

func boolPtr(value bool) *bool { return &value }
