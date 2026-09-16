package pool

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// 第三批：查询面与机器可读的操作清单。
//
// 取向：外部反代工具包接进来之后，真正需要的是"能问细分问题"与"能自己渲染操作"——
// 前者是过滤与汇总，后者是操作清单（Operations）。两者都由本文件统一提供，
// 各适配器不需要自己实现，也就不会各写一套口径。

// Operation 是某个号池后端支持的一次 HTTP 调用。
//
// 它由能力位**推导**出来（而不是各适配器手写）：声明了 CapProbe 就自动有 probe 这条路由，
// 于是"自描述"不会与实际路由漂移——外部工具照着这份清单渲染按钮即可，不必读 octopus 源码。
type Operation struct {
	Capability  Capability `json:"capability"`
	Method      string     `json:"method"`
	Path        string     `json:"path"` // 模板路径，{kind}/{id} 为占位
	Description string     `json:"description"`
}

// operationsOf 把能力位翻译成可调用的路由清单。
func operationsOf(capabilities []Capability) []Operation {
	table := map[Capability][]Operation{
		CapList: {
			{CapList, "GET", "/api/v1/pool/entries?kind={kind}", "列出该后端的条目（统一视图）"},
			{CapList, "GET", "/api/v1/pool/kinds", "列出所有后端与能力"},
		},
		CapGet:       {{CapGet, "GET", "/api/v1/pool/entries/{kind}/{id}", "取单条详情"}},
		CapProbe:     {{CapProbe, "POST", "/api/v1/pool/entries/{kind}/{id}/probe", "探活/读配额"}},
		CapRefresh:   {{CapRefresh, "POST", "/api/v1/pool/entries/{kind}/{id}/refresh", "刷新凭据"}},
		CapToggle:    {{CapToggle, "POST", "/api/v1/pool/entries/{kind}/{id}/enable", "启用条目"}, {CapToggle, "POST", "/api/v1/pool/entries/{kind}/{id}/disable", "停用条目"}},
		CapProvision: {{CapProvision, "POST", "/api/v1/pool/entries", "新建条目（授权/登录流程）"}},
		CapRevoke:    {{CapRevoke, "DELETE", "/api/v1/pool/entries/{kind}/{id}", "删除/撤销条目"}},
		CapSync:      {{CapSync, "POST", "/api/v1/pool/kinds/{kind}/sync", "物化到转发层"}},
	}
	operations := make([]Operation, 0, len(capabilities))
	for _, capability := range capabilities {
		operations = append(operations, table[capability]...)
	}
	return operations
}

// Filter 是统一视图的过滤条件。零值表示不过滤。
type Filter struct {
	Kind      string
	Provider  string
	Status    string
	Query     string // 名称/ID 子串，大小写不敏感
	Enabled   *bool
	Healthy   *bool
	Expiring  time.Duration // 只留"距今 X 之内到期"的条目
	HasExpiry *bool         // true=只留有过期时间的

	// 分页与排序（外部工具包按页拉取时用；Limit<=0 表示不翻页）。
	Limit  int    // 返回条数上限
	Offset int    // 跳过条数
	Sort   string // kind / name / provider / status / enabled / expires（默认 kind）
	Desc   bool   // 倒序
}

// ListResult 是过滤后的统一视图结果，带上后端侧的错误与过滤前后的计数，
// 便于调用方回答"是不是被过滤掉了"而不是看着空表猜。
type ListResult struct {
	Items    []Entry     `json:"items"`
	Total    int         `json:"total"`    // 过滤后条数（未分页前）
	Matched  int         `json:"matched"`  // 同 Total（保留字段名可读性）
	Returned int         `json:"returned"` // 本次实际返回条数（分页后）
	Scanned  int         `json:"scanned"`  // 过滤前条数
	Warnings []KindError `json:"warnings,omitempty"`
}

// List 取统一视图并按 Filter 过滤。
func List(ctx context.Context, filter Filter, now time.Time) (ListResult, error) {
	entries, failures, err := Entries(ctx, filter.Kind)
	if err != nil {
		return ListResult{}, err
	}
	result := ListResult{Scanned: len(entries), Warnings: failures}
	items := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if !filter.matches(entry, now) {
			continue
		}
		items = append(items, entry)
	}
	// 先排序再分页：过滤后条数（total）与本次返回条数（returned）分开回，
	// 调用方据此翻页，也不会把"这一页"误当成"全部"。
	sortEntries(items, filter.Sort, filter.Desc)
	result.Total = len(items)
	result.Matched = len(items)
	if filter.Offset > 0 {
		if filter.Offset >= len(items) {
			items = items[:0]
		} else {
			items = items[filter.Offset:]
		}
	}
	if filter.Limit > 0 && filter.Limit < len(items) {
		items = items[:filter.Limit]
	}
	result.Items = items
	result.Returned = len(items)
	return result, nil
}

// sortEntries 稳定排序；未知排序字段按 kind 处理（外部工具传错字段不该让整请求失败）。
// 默认（与 "kind"）用 kind→provider→name 三键：列表与导出共用同一个比较器，
// 这样"面板看到的顺序"与"导出拿走的顺序"永远一致，diff 也稳定。
func sortEntries(items []Entry, sortBy string, desc bool) {
	less := lessByKindProviderName
	switch strings.ToLower(sortBy) {
	case "name":
		less = func(items []Entry, i, j int) bool { return items[i].Name < items[j].Name }
	case "provider":
		less = func(items []Entry, i, j int) bool { return items[i].Provider < items[j].Provider }
	case "status":
		less = func(items []Entry, i, j int) bool { return items[i].Status < items[j].Status }
	case "enabled":
		less = func(items []Entry, i, j int) bool { return !items[i].Enabled && items[j].Enabled }
	case "expires":
		less = func(items []Entry, i, j int) bool { return expiryKey(items[i]) < expiryKey(items[j]) }
	}
	sort.SliceStable(items, func(i, j int) bool {
		if desc {
			return less(items, j, i)
		}
		return less(items, i, j)
	})
}

// lessByKindProviderName 是默认排序：kind → provider → name（导出工具最爱的稳定三键）。
func lessByKindProviderName(items []Entry, i, j int) bool {
	if items[i].Kind != items[j].Kind {
		return items[i].Kind < items[j].Kind
	}
	if items[i].Provider != items[j].Provider {
		return items[i].Provider < items[j].Provider
	}
	return items[i].Name < items[j].Name
}

// expiryKey 把可空时间转成可比较的字符串：没有到期时间的排在最后。
func expiryKey(entry Entry) string {
	if entry.ExpiresAt == nil {
		return "9999-99-99"
	}
	return entry.ExpiresAt.Format(time.RFC3339)
}

func (f Filter) matches(entry Entry, now time.Time) bool {
	if f.Kind != "" && entry.Kind != f.Kind {
		return false
	}
	if f.Provider != "" && !strings.EqualFold(entry.Provider, f.Provider) {
		return false
	}
	if f.Status != "" && !strings.EqualFold(entry.Status, f.Status) {
		return false
	}
	if f.Enabled != nil && entry.Enabled != *f.Enabled {
		return false
	}
	if f.Healthy != nil && entry.Healthy != *f.Healthy {
		return false
	}
	if f.Query != "" {
		needle := strings.ToLower(f.Query)
		if !strings.Contains(strings.ToLower(entry.Name), needle) &&
			!strings.Contains(strings.ToLower(entry.ID), needle) {
			return false
		}
	}
	if f.HasExpiry != nil {
		if (*f.HasExpiry) != (entry.ExpiresAt != nil) {
			return false
		}
	}
	if f.Expiring > 0 {
		if entry.ExpiresAt == nil {
			return false
		}
		if entry.ExpiresAt.Before(now) || entry.ExpiresAt.Sub(now) > f.Expiring {
			return false
		}
	}
	return true
}

// Summary 是号池的汇总视图：面板与外部工具用它一眼回答"池子健不健康"。
//
// 与 Stats 的区别：Stats 只数个数；Summary 按状态/服务商/到期窗口切分，
// 并显式给出"没数据"的后端（KindErrors），让"空"与"坏"可区分。
type Summary struct {
	Total        int                 `json:"total"`
	Enabled      int                 `json:"enabled"`
	Disabled     int                 `json:"disabled"`
	Healthy      int                 `json:"healthy"`
	Unhealthy    int                 `json:"unhealthy"`
	WithError    int                 `json:"with_error"`    // 带 last_error 的条目
	ExpiringSoon int                 `json:"expiring_soon"` // 24 小时内到期
	Expired      int                 `json:"expired"`       // 已过期但仍在池子里
	ByKind       map[string]KindStat `json:"by_kind"`
	ByProvider   map[string]int      `json:"by_provider"`
	ByStatus     map[string]int      `json:"by_status"`
	KindErrors   []KindError         `json:"kind_errors,omitempty"`
}

// SummaryOf 汇总统一视图；24 小时作为"临期"窗口是号池运维的常见口径（提前发现而不是等 401）。
func SummaryOf(ctx context.Context, now time.Time) Summary {
	summary := Summary{
		ByKind:     map[string]KindStat{},
		ByProvider: map[string]int{},
		ByStatus:   map[string]int{},
	}
	entries, failures, err := Entries(ctx, "")
	summary.KindErrors = failures
	if err != nil {
		summary.KindErrors = append(summary.KindErrors, KindError{Kind: "*", Error: err.Error()})
		return summary
	}
	const expiringWindow = 24 * time.Hour
	for _, entry := range entries {
		summary.Total++
		if entry.Enabled {
			summary.Enabled++
		} else {
			summary.Disabled++
		}
		if entry.Healthy {
			summary.Healthy++
		} else {
			summary.Unhealthy++
		}
		if entry.LastError != "" {
			summary.WithError++
		}
		if entry.ExpiresAt != nil {
			switch {
			case entry.ExpiresAt.Before(now):
				summary.Expired++
			case entry.ExpiresAt.Sub(now) <= expiringWindow:
				summary.ExpiringSoon++
			}
		}
		kind := summary.ByKind[entry.Kind]
		kind.Entries++
		if entry.Enabled {
			kind.Enabled++
		}
		if entry.Healthy {
			kind.Healthy++
		}
		summary.ByKind[entry.Kind] = kind
		provider := entry.Provider
		if provider == "" {
			provider = "(未标注)"
		}
		summary.ByProvider[provider]++
		status := entry.Status
		if status == "" {
			status = "(未知)"
		}
		summary.ByStatus[status]++
	}
	return summary
}

// ExportRow 是导出的扁平行（不含任何凭据字段，字段固定，便于外部工具直接吃）。
type ExportRow struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Status    string `json:"status"`
	Enabled   bool   `json:"enabled"`
	Healthy   bool   `json:"healthy"`
	PlanTier  string `json:"plan_tier"`
	ExpiresAt string `json:"expires_at"`
	LastError string `json:"last_error"`
}

// ExportRows 生成导出行：筛选与排序口径与统一视图完全一致（同一份 Filter、同一个 matches/sortEntries），
// 只在最后一层换成"扁平行"的字段形状 —— 否则面板看到的和导出拿走会是两套口径。
//
// 导出**不分页**：导出就是"把当前筛选结果整个拿走"，Limit/Offset 在这里被忽略（列表页要翻页，导出不要）。
func ExportRows(ctx context.Context, filter Filter, now time.Time) ([]ExportRow, []KindError, error) {
	entries, failures, err := Entries(ctx, filter.Kind)
	if err != nil {
		return nil, nil, err
	}
	picked := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if !filter.matches(entry, now) {
			continue
		}
		picked = append(picked, entry)
	}
	if filter.Sort == "" {
		// 默认排序与列表共用同一个比较器（kind → provider → name），保证两边顺序一致。
		sortEntries(picked, "", false)
	} else {
		sortEntries(picked, filter.Sort, filter.Desc)
	}
	rows := make([]ExportRow, 0, len(picked))
	for _, entry := range picked {
		expires := ""
		if entry.ExpiresAt != nil {
			expires = entry.ExpiresAt.Format(time.RFC3339)
		}
		rows = append(rows, ExportRow{
			Kind: entry.Kind, ID: entry.ID, Name: entry.Name, Provider: entry.Provider,
			Status: entry.Status, Enabled: entry.Enabled, Healthy: entry.Healthy,
			PlanTier: entry.PlanTier, ExpiresAt: expires, LastError: entry.LastError,
		})
	}
	return rows, failures, nil
}

// ParseFilter 把查询参数解析成 Filter（HTTP 层只做字符串到类型的转换，判断逻辑留在本包，
// 这样 CLI/外部工具直接调本包也能得到同样的语义）。
func ParseFilter(params map[string]string) (Filter, error) {
	filter := Filter{
		Kind:     params["kind"],
		Provider: params["provider"],
		Status:   params["status"],
		Query:    params["q"],
	}
	if raw := params["limit"]; raw != "" {
		limit, err := parseSeconds(raw)
		if err != nil {
			return filter, fmt.Errorf("limit: %w", err)
		}
		filter.Limit = int(limit)
	}
	if raw := params["offset"]; raw != "" {
		offset, err := parseSeconds(raw)
		if err != nil {
			return filter, fmt.Errorf("offset: %w", err)
		}
		filter.Offset = int(offset)
	}
	filter.Sort = params["sort"]
	if raw := params["desc"]; raw != "" {
		value, err := parseBool(raw)
		if err != nil {
			return filter, fmt.Errorf("desc: %w", err)
		}
		filter.Desc = value
	}
	enabled, healthy, expiring, hasExpiry := params["enabled"], params["healthy"], params["expiring_within"], params["has_expiry"]
	if enabled != "" {
		value, err := parseBool(enabled)
		if err != nil {
			return filter, fmt.Errorf("enabled: %w", err)
		}
		filter.Enabled = &value
	}
	if healthy != "" {
		value, err := parseBool(healthy)
		if err != nil {
			return filter, fmt.Errorf("healthy: %w", err)
		}
		filter.Healthy = &value
	}
	if hasExpiry != "" {
		value, err := parseBool(hasExpiry)
		if err != nil {
			return filter, fmt.Errorf("has_expiry: %w", err)
		}
		filter.HasExpiry = &value
	}
	if expiring != "" {
		seconds, err := parseSeconds(expiring)
		if err != nil {
			return filter, fmt.Errorf("expiring_within: %w", err)
		}
		filter.Expiring = time.Duration(seconds) * time.Second
	}
	return filter, nil
}

func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("want true/false, got %q", raw)
	}
}

func parseSeconds(raw string) (int64, error) {
	var seconds int64
	if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &seconds); err != nil || seconds < 0 {
		return 0, fmt.Errorf("want a non-negative number of seconds, got %q", raw)
	}
	return seconds, nil
}
