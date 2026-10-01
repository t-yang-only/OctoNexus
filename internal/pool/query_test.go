package pool

import (
	"context"
	"encoding/json"
	"fmt"
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
	filter, err := ParseFilter(map[string]string{
		"kind": "official", "provider": "gemini", "status": "active", "q": "alp",
		"enabled": "true", "healthy": "1", "expiring_within": "3600", "has_expiry": "yes",
	})
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
		if _, err := ParseFilter(map[string]string{
			"enabled": bad.enabled, "expiring_within": bad.expiring, "has_expiry": bad.hasExpiry,
		}); err == nil {
			t.Errorf("非法取值应报错: %+v", bad)
		}
	}

	// 空字符串 = 不过滤。
	empty, err := ParseFilter(nil)
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

	rows, _, err := ExportRows(context.Background(), Filter{Kind: "e"}, now)
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

func TestExportRowsHonoursTheSameFilterAsList(t *testing.T) {
	now := time.Now()
	withFake(t, fakeAdapter{kind: "e", entries: fixedEntries(now)})

	enabled := false
	hasExpiry := true
	cases := []struct {
		name   string
		filter Filter
	}{
		{"按名称子串", Filter{Kind: "e", Query: "beta"}},
		{"按状态", Filter{Kind: "e", Status: "active"}},
		{"按启用位", Filter{Kind: "e", Enabled: &enabled}},
		{"按有无到期时间", Filter{Kind: "e", HasExpiry: &hasExpiry}},
		{"按临期窗口", Filter{Kind: "e", Expiring: time.Hour, HasExpiry: &hasExpiry}},
		{"按服务商", Filter{Kind: "e", Provider: "gemini"}},
		{"排序与列表一致", Filter{Kind: "e", Sort: "name", Desc: true}},
	}
	for _, testCase := range cases {
		result, err := List(context.Background(), testCase.filter, now)
		if err != nil {
			t.Fatalf("%s: List: %v", testCase.name, err)
		}
		rows, _, err := ExportRows(context.Background(), testCase.filter, now)
		if err != nil {
			t.Fatalf("%s: ExportRows: %v", testCase.name, err)
		}
		// 导出与列表必须逐条同序同内容 —— 面板"按当前筛选导出"才拿得到与屏幕一致的结果。
		if len(rows) != len(result.Items) {
			t.Fatalf("%s: 导出 %d 行 / 列表 %d 条", testCase.name, len(rows), len(result.Items))
		}
		for i, entry := range result.Items {
			if rows[i].ID != entry.ID {
				t.Fatalf("%s: 第 %d 行导出 %q / 列表 %q", testCase.name, i, rows[i].ID, entry.ID)
			}
		}
	}

	// 导出不分页：limit/offset 只影响列表页，不影响导出（导出=把当前筛选结果整个拿走）。
	paged := Filter{Kind: "e", Limit: 1, Offset: 1}
	result, err := List(context.Background(), paged, now)
	if err != nil {
		t.Fatalf("List(分页): %v", err)
	}
	rows, _, err := ExportRows(context.Background(), paged, now)
	if err != nil {
		t.Fatalf("ExportRows(分页): %v", err)
	}
	if result.Returned != 1 || len(rows) != 3 {
		t.Fatalf("分页语义错：列表 returned=%d（应 1），导出 %d 行（应 3）", result.Returned, len(rows))
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
		{CapToggle, "POST"},
	} {
		key := string(want.capability) + ":" + want.method
		if _, ok := paths[key]; !ok {
			t.Errorf("官方账号池清单缺 %s: %+v", key, official.Operations)
		}
	}
	// 清单由能力位推导，因此"有启停能力就必须有启停路由"之外还要有反面：
	// 没声明 revoke 时清单里就不能出现它（避免清单与实际路由漂移）。
	for _, operation := range official.Operations {
		if operation.Capability == CapRevoke {
			t.Errorf("官方账号池没声明 %s, 清单里不该有这条路: %+v", operation.Capability, operation)
		}
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

func boolPtr(value bool) *bool { return &value }

// 第四批 A：分页与排序。重点是**过滤后条数与本次返回条数分开回**——
// 工具包据此翻页，也不会把"这一页"误当"全部"。
func TestListPagingAndSorting(t *testing.T) {
	withoutBuiltin(t)
	withFake(t, &fakeAdapter{kind: "page", entries: []Entry{
		{ID: "3", Name: "c"}, {ID: "1", Name: "a"}, {ID: "2", Name: "b"},
	}})
	now := time.Now()

	page, err := List(context.Background(), Filter{Kind: "page", Sort: "name", Limit: 2}, now)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.Total != 3 || page.Returned != 2 || len(page.Items) != 2 {
		t.Fatalf("分页口径不对: total=%d returned=%d items=%d", page.Total, page.Returned, len(page.Items))
	}
	if page.Items[0].Name != "a" || page.Items[1].Name != "b" {
		t.Fatalf("排序不对: %v %v", page.Items[0].Name, page.Items[1].Name)
	}

	second, err := List(context.Background(), Filter{Kind: "page", Sort: "name", Limit: 2, Offset: 2}, now)
	if err != nil {
		t.Fatalf("List offset: %v", err)
	}
	if second.Returned != 1 || second.Items[0].Name != "c" {
		t.Fatalf("第二页不对: returned=%d", second.Returned)
	}

	desc, err := List(context.Background(), Filter{Kind: "page", Sort: "name", Desc: true}, now)
	if err != nil {
		t.Fatalf("List desc: %v", err)
	}
	if desc.Items[0].Name != "c" {
		t.Fatalf("倒序不对: %v", desc.Items[0].Name)
	}

	// offset 越过总数：回空列表而不是报错（翻页越界是正常操作），total 仍是过滤后条数。
	beyond, err := List(context.Background(), Filter{Kind: "page", Offset: 99}, now)
	if err != nil {
		t.Fatalf("List beyond: %v", err)
	}
	if beyond.Returned != 0 || len(beyond.Items) != 0 || beyond.Total != 3 {
		t.Fatalf("越界页口径不对: returned=%d total=%d", beyond.Returned, beyond.Total)
	}
}

// 排序字段写错不该让整请求失败——外部工具传错字段是常事，回退 kind 排序即可。
func TestListUnknownSortFallsBack(t *testing.T) {
	withoutBuiltin(t)
	withFake(t, &fakeAdapter{kind: "sortsafe", entries: []Entry{{ID: "1", Name: "b"}, {ID: "2", Name: "a"}}})
	page, err := List(context.Background(), Filter{Kind: "sortsafe", Sort: "no_such_field"}, time.Now())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("条数不对: %d", len(page.Items))
	}
}

// 第四批 B：批量动作逐条独立——一条失败不影响其余，失败的带原因，成功的带条目。
func TestBatchIsolatesPerItemFailures(t *testing.T) {
	withoutBuiltin(t)
	withFake(t, &opsAdapter{
		kind: "batch-iso",
		caps: []Capability{CapList, CapRefresh},
		refreshFn: func(id string) (Entry, error) {
			if id == "bad" {
				return Entry{}, fmt.Errorf("上游 500")
			}
			return Entry{ID: id, Name: id, Status: "refreshed"}, nil
		},
	})
	result, err := Batch(context.Background(), BatchRequest{
		Action: BatchRefresh, Kind: "batch-iso", IDs: []string{"ok", "bad", "ok"},
	}, time.Now())
	if err != nil {
		t.Fatalf("Batch 不该整体失败: %v", err)
	}
	if result.Total != 2 {
		t.Fatalf("去重后应 2 条: %d", result.Total)
	}
	if result.OK != 1 || result.Failed != 1 {
		t.Fatalf("计数不对: ok=%d failed=%d", result.OK, result.Failed)
	}
	if !result.Items[0].OK || result.Items[0].ID != "ok" || result.Items[0].Entry == nil {
		t.Fatalf("成功条目应带 entry: %+v", result.Items[0])
	}
	if result.Items[1].Error == "" {
		t.Fatal("失败条目应带原因")
	}
}

// 未声明能力的后端：逐条回 capability not supported，而不是整批 400
// （否则一个没声明 toggle 的后端会让整批 enable 全废）。
func TestBatchUnsupportedCapabilityIsPerItem(t *testing.T) {
	withoutBuiltin(t)
	withFake(t, &fakeAdapter{kind: "batch-notoggle", entries: []Entry{{ID: "1"}}})
	result, err := Batch(context.Background(), BatchRequest{
		Action: BatchDisable, Kind: "batch-notoggle", IDs: []string{"1"},
	}, time.Now())
	if err != nil {
		t.Fatalf("Batch 不该整体失败: %v", err)
	}
	if result.Failed != 1 || result.Items[0].Error == "" {
		t.Fatalf("应逐条回不支持: %+v", result.Items)
	}
}

// 批量护栏：动作名非法、以及"不写条件就全量"都要被拒。
func TestBatchGuards(t *testing.T) {
	withoutBuiltin(t)
	if _, err := Batch(context.Background(), BatchRequest{Action: "drop_everything", IDs: []string{"1"}}, time.Now()); err == nil {
		t.Fatal("非法动作应报错")
	}
	if _, err := Batch(context.Background(), BatchRequest{Action: BatchProbe}, time.Now()); err == nil {
		t.Fatal("既不给 ids 也不给 filter 应报错（不允许隐式全量）")
	}
}

// 批量上限：超出 Max 的条目计 skipped（显式告诉调用方还有多少没做），不静默丢。
func TestBatchRespectsMax(t *testing.T) {
	withoutBuiltin(t)
	withFake(t, &opsAdapter{kind: "batch-max", caps: []Capability{CapList, CapRefresh}})
	result, err := Batch(context.Background(), BatchRequest{
		Action: BatchRefresh, Kind: "batch-max", IDs: []string{"a", "b", "c", "d"}, Max: 2,
	}, time.Now())
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if result.OK != 2 || result.Skipped != 2 || result.Total != 4 {
		t.Fatalf("Max 语义不对: ok=%d skipped=%d total=%d", result.OK, result.Skipped, result.Total)
	}
}

// 第四批 D：OpenAPI 由注册表推导——声明了 probe 就有 probe 路由，没有 toggle 就没有 enable/disable。
// 这条守卫的意义：新适配器注册进来，文档自动长出来，不需要有人记得改文档。
func TestOpenAPIFollowsRegistry(t *testing.T) {
	withoutBuiltin(t)
	withFake(t, &fakeAdapter{kind: "docs", caps: []Capability{CapList, CapProbe}})
	doc := OpenAPIDocument("test")
	if doc["openapi"] != "3.0.3" {
		t.Fatalf("openapi 版本不对: %v", doc["openapi"])
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("缺 paths")
	}
	for _, want := range []string{
		"/api/v1/pool/entries",
		"/api/v1/pool/kinds",
		"/api/v1/pool/entries/{kind}/{id}/probe",
	} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("缺路径 %s", want)
		}
	}
	for _, unwanted := range []string{
		"/api/v1/pool/entries/{kind}/{id}/enable",
		"/api/v1/pool/entries/{kind}/{id}/disable",
	} {
		if _, ok := paths[unwanted]; ok {
			t.Fatalf("未声明 toggle 不该出现 %s", unwanted)
		}
	}
	schemas, ok := doc["components"].(map[string]any)["schemas"].(map[string]any)
	if !ok || len(schemas) < 10 {
		t.Fatalf("组件 schema 太少")
	}
	for _, want := range []string{"Entry", "AdapterInfo", "Summary", "BatchRequest", "BatchResult"} {
		if _, ok := schemas[want]; !ok {
			t.Fatalf("缺 schema %s", want)
		}
	}
}

// 文档里的 kind 枚举跟着注册表走。
func TestOpenAPIKindEnumFollowsRegistry(t *testing.T) {
	withoutBuiltin(t)
	withFake(t, &fakeAdapter{kind: "enum-kind"})
	paths := OpenAPIDocument("test")["paths"].(map[string]any)
	parameters := paths["/api/v1/pool/kinds/{kind}"].(map[string]any)["get"].(map[string]any)["parameters"].([]any)
	enum, ok := parameters[0].(map[string]any)["schema"].(map[string]any)["enum"].([]string)
	if !ok {
		t.Fatal("kind 参数应带枚举")
	}
	found := false
	for _, value := range enum {
		if value == "enum-kind" {
			found = true
		}
	}
	if !found {
		t.Fatalf("枚举未包含新注册的 kind: %v", enum)
	}
}
