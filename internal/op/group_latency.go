package op

import (
	"context"
	"sort"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// T-perf-003 分组维度的延迟画像。
//
// ## 为什么渠道维度还不够
//
// T-perf-001 给了渠道维度（"53HK 首字节 4515ms"），但**用户调用的不是渠道，
// 是分组** —— 客户端填的是 `Max-flash`、`High-flash` 这些名字。
//
// 生产实测：`Max-flash` 有 40 个成员、`High-flash` 34 个，成员来自多个渠道。
// 用户看到分组慢，无法从渠道画像推出是哪个成员拖的 ——
// 那是分组内部选路的结果，只有按分组统计才看得见。
//
// ## 与渠道画像的关系
//
// 两者数据源相同（relay_logs），维度不同（group_id vs target_channel），
// 所以共用阈值常量与分位数算法，不重复实现。
// **两类记录数不同是正常的**：分组画像只含走到了分组的请求
// （分组不存在的请求没有 group_id），渠道画像只含走到了渠道的请求。
type GroupLatency struct {
	GroupID int    `json:"group_id"`
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	// Deleted 标记「分组已被删除，但日志还在保留期内」。
	//
	// 这类条目**保留在后端是对的**（日志是事实，7 天保留期内它确实发生过），
	// 但**不该出现在"我常用的分组多快"这个视图里** —— 用户已经删了它。
	//
	// 单独给一个布尔而不是让前端匹配占位名字符串：
	// 靠 `name == "(已删除的分组)"` 判断是脆弱的，改一次文案就失效。
	Deleted bool `json:"deleted"`
	// MemberCount 是分组当前的成员数。**必须与延迟一起看**：
	// 单成员分组没有选择空间，慢不慢都只能用它。
	MemberCount      int   `json:"member_count"`
	Samples          int64 `json:"samples"`
	FirstByteSamples int64 `json:"first_byte_samples"`
	FirstByteP50Ms   int64 `json:"first_byte_p50_ms"`
	FirstByteP90Ms   int64 `json:"first_byte_p90_ms"`
	DurationP50Ms    int64 `json:"duration_p50_ms"`
	SlowCount        int64 `json:"slow_count"`
}

// GroupLatencySummary 是分组延迟画像的整体结果。
type GroupLatencySummary struct {
	SlowThresholdMs int64 `json:"slow_threshold_ms"`
	Window          int64 `json:"window"`
	// Sample 说明这批样本的来源（窗口原始条数、剔除的测试请求数、是否触限）。
	Sample RelayLogSampleInfo `json:"sample"`
	Groups []GroupLatency     `json:"groups"`
}

// GroupLatencyStats 统计各分组的首字节与总耗时分布。
//
// 排序：**先按有无数据，再按样本数**倒序 —— 不是按延迟升序。
// 理由与渠道画像不同：渠道画像的用途是"挑快的用"（所以快的在前），
// 而分组画像的用途是"看我常用的那几个到底多快"，
// 因此**用得多的排前面**更有用（没数据的自然沉底）。
func GroupLatencyStats(ctx context.Context, window int) (GroupLatencySummary, error) {
	rows, sample, err := relayLogWindow(ctx, window)
	if err != nil {
		return GroupLatencySummary{}, err
	}
	// 收口函数只负责取样本；下面还要查分组元数据（名称、成员数），仍需要连接。
	conn := db.GetDB()

	type bucket struct {
		firstBytes []int64
		durations  []int64
		slow       int64
	}
	byGroup := make(map[int]*bucket)
	for _, row := range rows {
		// 没有 group_id 的请求（分组不存在等）不进任何分组画像 ——
		// 它们没有"这个分组有多快"可言。
		if row.GroupID <= 0 {
			continue
		}
		b, ok := byGroup[row.GroupID]
		if !ok {
			b = &bucket{}
			byGroup[row.GroupID] = b
		}
		if row.FirstByteMs >= 0 {
			b.firstBytes = append(b.firstBytes, row.FirstByteMs)
			if row.FirstByteMs >= slowFirstByteMs {
				b.slow++
			}
		}
		if row.DurationMs > 0 {
			b.durations = append(b.durations, row.DurationMs)
		}
	}

	// 分组名与成员数一次取回：逐个查会变成 N+1。
	var groups []model.Group
	if err := conn.WithContext(ctx).Order("id ASC").Find(&groups).Error; err != nil {
		return GroupLatencySummary{}, err
	}
	var items []model.GroupItem
	if err := conn.WithContext(ctx).Find(&items).Error; err != nil {
		return GroupLatencySummary{}, err
	}
	itemCount := make(map[int]int, len(groups))
	for _, item := range items {
		itemCount[item.GroupID]++
	}
	meta := make(map[int]model.Group, len(groups))
	for _, g := range groups {
		meta[g.ID] = g
	}

	summary := GroupLatencySummary{
		SlowThresholdMs: slowFirstByteMs,
		Window:          sample.Samples,
		Sample:          sample,
		Groups:          make([]GroupLatency, 0, len(byGroup)),
	}
	for gid, b := range byGroup {
		g := meta[gid]
		row := GroupLatency{
			GroupID:          gid,
			Name:             g.Name,
			Mode:             string(g.Mode),
			MemberCount:      itemCount[gid],
			Samples:          int64(len(b.durations)),
			FirstByteSamples: int64(len(b.firstBytes)),
			SlowCount:        b.slow,
		}
		// 分组已被删除但日志还在：给可辨认的占位名 + 显式标记，
		// 前端据 Deleted 把它从"常用分组"视图里滤掉。
		if _, exists := meta[gid]; !exists {
			row.Name = "(已删除的分组)"
			row.Deleted = true
		}
		row.FirstByteP50Ms = percentile(b.firstBytes, 0.50)
		row.FirstByteP90Ms = percentile(b.firstBytes, 0.90)
		row.DurationP50Ms = percentile(b.durations, 0.50)
		summary.Groups = append(summary.Groups, row)
	}
	sort.Slice(summary.Groups, func(i, j int) bool {
		// 样本多的在前（"我常用的"优先）；相同则按 id 稳定排序。
		if summary.Groups[i].Samples != summary.Groups[j].Samples {
			return summary.Groups[i].Samples > summary.Groups[j].Samples
		}
		return summary.Groups[i].GroupID < summary.Groups[j].GroupID
	})
	return summary, nil
}
