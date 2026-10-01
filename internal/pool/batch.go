package pool

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// 第四批 A/B：外部工具包需要的两件事——批量动作与（query.go 里的）分页排序。
//
// 批量动作的设计取向：**逐条独立**。一条失败不影响其余，也不会因为某个后端没声明能力就整批 400；
// 每条结果都带自己的错误与条目，最后给一份计数汇总。工具包据此渲染"成功 8 条、失败 2 条（原因见明细）"。

// 批量动作的取值。与能力位一一对应：probe→CapProbe、refresh→CapRefresh、enable/disable→CapToggle。
const (
	BatchProbe   = "probe"
	BatchRefresh = "refresh"
	BatchEnable  = "enable"
	BatchDisable = "disable"
)

// 批量条数上限：防止一条请求把整个号池（可能上千条）全打一遍。
const (
	batchDefaultMax = 50
	batchHardMax    = 200
)

// BatchRequest 是批量动作请求。
//
// 二选一定位目标：IDs 显式列出，或 Filter + Max 按条件取（Max 必填且有上限——
// 不允许"不写条件就全量打一遍"，那是运维事故而不是功能）。
type BatchRequest struct {
	Action string
	Kind   string
	IDs    []string
	Filter *Filter
	Max    int
}

// BatchItemResult 是单条的结论。
type BatchItemResult struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Entry *Entry `json:"entry,omitempty"`
	Error string `json:"error,omitempty"`
}

// BatchResult 是批量动作的汇总。
type BatchResult struct {
	Action  string            `json:"action"`
	Total   int               `json:"total"`
	OK      int               `json:"ok"`
	Failed  int               `json:"failed"`
	Skipped int               `json:"skipped"` // 因超出 Max 未处理
	Items   []BatchItemResult `json:"items"`
}

// Batch 执行批量动作。
func Batch(ctx context.Context, request BatchRequest, now time.Time) (BatchResult, error) {
	action := strings.ToLower(strings.TrimSpace(request.Action))
	switch action {
	case BatchProbe, BatchRefresh, BatchEnable, BatchDisable:
	default:
		return BatchResult{}, fmt.Errorf("unknown batch action %q, want probe/refresh/enable/disable", request.Action)
	}
	if len(request.IDs) == 0 && request.Filter == nil {
		return BatchResult{}, fmt.Errorf("batch needs either ids or a filter (no implicit whole-pool action)")
	}

	targets, err := batchTargets(ctx, request, now)
	if err != nil {
		return BatchResult{}, err
	}

	maxItems := request.Max
	if maxItems <= 0 {
		maxItems = batchDefaultMax
	}
	if maxItems > batchHardMax {
		maxItems = batchHardMax
	}

	result := BatchResult{Action: action, Total: len(targets)}
	for index, target := range targets {
		if index >= maxItems {
			result.Skipped = len(targets) - index
			break
		}
		item := BatchItemResult{Kind: target.Kind, ID: target.ID}
		var entry Entry
		var err error
		switch action {
		case BatchProbe:
			entry, err = Probe(ctx, target.Kind, target.ID)
		case BatchRefresh:
			entry, err = Refresh(ctx, target.Kind, target.ID)
		case BatchEnable:
			entry, err = SetEnabled(ctx, target.Kind, target.ID, true)
		case BatchDisable:
			entry, err = SetEnabled(ctx, target.Kind, target.ID, false)
		}
		if err != nil {
			item.Error = err.Error()
			// 探活失败也带条目：结论里那句"现在是什么状态"对工具包有用。
			if action == BatchProbe && entry.ID != "" {
				copied := entry
				item.Entry = &copied
			}
			result.Failed++
		} else {
			item.OK = true
			copied := entry
			item.Entry = &copied
			result.OK++
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// batchTargets 解析批量目标：IDs 优先（按给定顺序、去重），否则按 Filter 取（复用 List 的过滤与排序）。
func batchTargets(ctx context.Context, request BatchRequest, now time.Time) ([]Entry, error) {
	if len(request.IDs) > 0 {
		kind := request.Kind
		seen := map[string]bool{}
		targets := make([]Entry, 0, len(request.IDs))
		for _, id := range request.IDs {
			key := kind + "\x00" + id
			if seen[key] {
				continue
			}
			seen[key] = true
			targets = append(targets, Entry{Kind: kind, ID: id})
		}
		return targets, nil
	}

	filter := *request.Filter
	if filter.Limit <= 0 {
		filter.Limit = batchHardMax
	}
	listed, err := List(ctx, filter, now)
	if err != nil {
		return nil, err
	}
	targets := make([]Entry, 0, len(listed.Items))
	for _, entry := range listed.Items {
		targets = append(targets, Entry{Kind: entry.Kind, ID: entry.ID})
	}
	return targets, nil
}
