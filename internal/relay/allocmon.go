package relay

import (
	"sort"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// 分压与限流监控（T-monitor-001）。
//
// 用户口径："给前端页面升级更加详细的监控页面"。这里只做**后端可观测事实的汇总**：
// 每个成员此刻的剩余请求数、权重、健康分、冷却/限流状态，以及它在分压里"下一次会不会被优先选"。
//
// 三条口径：
//  1. 明细只读（advance=false 的分配口径 + 只读冷却/限流账），看一眼绝不改变选路状态；
//  2. **不复刻面板的折算逻辑**：剩余请求数、来源、权重都由选路层自己算出来（与真正选路同一函数），
//     面板只展示；否则面板与选路迟早会出现两套口径；
//  3. 未知就是未知：剩余请求数未知的成员照常列出（Known=false），不显示 0（那会被读成"没钱了"）。
type MemberAllocationRow struct {
	ItemID       int     `json:"item_id"`
	GroupID      int     `json:"group_id"`
	GroupName    string  `json:"group_name"`
	ChannelID    int     `json:"channel_id"`
	ChannelName  string  `json:"channel_name"`
	ModelName    string  `json:"model_name"`
	KeyName      string  `json:"key_name"`
	Priority     int     `json:"priority"`
	Available    bool    `json:"available"`
	SmartTier    string  `json:"smart_tier"`
	Known        bool    `json:"known"`         // 剩余请求数是否已知（false 时下方的值无意义）
	Requests     float64 `json:"requests"`      // 估算的剩余请求数
	Source       string  `json:"source"`        // monthly / balance / ""（未知）
	Weight       int     `json:"weight"`        // 分压权重 1..100
	HealthFactor float64 `json:"health_factor"` // 0..1 健康折扣系数
	Counted      bool    `json:"counted"`       // 是否参与本次分配（余量不足/不可用的成员不参与）
	Cooling      bool    `json:"cooling"`       // 是否处于冷却中
	CooldownMs   int64   `json:"cooldown_ms"`   // 冷却剩余毫秒（未冷却为 0）
	Throttled    bool    `json:"throttled"`     // 是否处于上游限流等待中
	ThrottleMs   int64   `json:"throttle_ms"`   // 限流剩余毫秒
	ThrottleHits int     `json:"throttle_hits"` // 进程内累计被限流次数
	SuccessRate  float64 `json:"success_rate"`  // 最近窗口成功率（无样本为 0, HasSamples 区分）
	HasSamples   bool    `json:"has_samples"`
	LatencyMs    int64   `json:"latency_ms"`    // 最近一次尝试耗时
	RecentReqs   int     `json:"recent_reqs"`   // 最近一分钟请求数
	RecentTokens int     `json:"recent_tokens"` // 最近一分钟 token 数
	Limited      bool    `json:"limited"`       // 是否已达自限流上限
	// 速度（T-speed-001）：首帧与吞吐是"速度好不好"的两个可观测事实，
	// 决策（折扣 / 看门狗预算）也由它们推出来，因此面板必须能看到原始读数与结论。
	SpeedSamples int64   `json:"speed_samples"`  // 速度窗口内的成功样本数
	TtfbMs       int64   `json:"ttfb_ms"`        // 最近一次流式首帧耗时（0 = 还没量到）
	TokensPerSec float64 `json:"tokens_per_sec"` // 窗口内的输出吞吐（0 = 还没量到）
	SpeedReady   bool    `json:"speed_ready"`    // 样本是否够下结论（不足时选路按未知处理）
	SpeedFactor  float64 `json:"speed_factor"`   // 速度折扣系数 0..1（1 = 不打折）
	Slow         bool    `json:"slow"`           // 是否被判为慢（首帧或吞吐超过阈值）
}

// AllocationSummary 是分压监控的汇总卡片。
type AllocationSummary struct {
	GroupCount     int     `json:"group_count"`   // 有成员的分组数
	MemberCount    int     `json:"member_count"`  // 全部成员数
	KnownCount     int     `json:"known_count"`   // 剩余请求数已知的成员数
	UnknownCount   int     `json:"unknown_count"` // 未知的成员数（不惩罚, 但要让人看见"还有多少是未知的"）
	CoolingCount   int     `json:"cooling_count"`
	ThrottledCount int     `json:"throttled_count"`
	LimitedCount   int     `json:"limited_count"`
	SlowCount      int     `json:"slow_count"`     // 被判为慢的成员数（首帧或吞吐超阈值, 样本足够时才判）
	TotalRequests  float64 `json:"total_requests"` // 已知成员的剩余请求数合计（只统计已知, 不把未知当 0）
	MinRequests    float64 `json:"min_requests"`   // 已知成员里的最小剩余请求数
	MaxRequests    float64 `json:"max_requests"`   // 已知成员里的最大剩余请求数
	GeneratedAt    int64   `json:"generated_at"`   // 快照时刻（Unix 毫秒）
}

// AllocationSnapshot 是监控接口的完整返回: 汇总 + 明细 + 口径说明。
type AllocationSnapshot struct {
	Summary AllocationSummary     `json:"summary"`
	Rows    []MemberAllocationRow `json:"rows"`
	// ModeCounts 按分组模式统计成员数, 便于一眼看出"有多少成员正走在分压/加权/贪心路径上"。
	ModeCounts map[string]int `json:"mode_counts"`
	// Settings 是分压相关设置的当前生效值（面板只展示, 不重复实现折算）。
	Settings AllocationSettingsView `json:"settings"`
}

// AllocationSettingsView 是分压设置的可展示形状（与 allocateSettingsOf 同一来源）。
type AllocationSettingsView struct {
	EstimateTokens     int     `json:"estimate_tokens"`
	HealthWeight       int     `json:"health_weight"`
	SlowLatencyMs      int64   `json:"slow_latency_ms"`
	MinRequests        float64 `json:"min_requests"`
	MemberRPMLimit     int     `json:"member_rpm_limit"`
	MemberTPMLimit     int     `json:"member_tpm_limit"`
	ThrottleCapSeconds int64   `json:"throttle_cap_seconds"`
	PointsPerUnit      float64 `json:"points_per_unit"`
	// 速度维度（T-speed-001）：面板要能看到"现在是按什么标准判慢、折扣多强、看门狗收多少"。
	SpeedWeight      int     `json:"speed_weight"`
	SlowTtfbMs       int64   `json:"slow_ttfb_ms"`
	SlowTokensPerSec float64 `json:"slow_tokens_per_sec"`
	FeMultiple       int     `json:"first_event_multiple"`
	FeFloorMs        int64   `json:"first_event_floor_ms"`
}

// AllocationMonitor 汇总全部分组的分压与限流事实。
//
// 只读口径：不推进分配累加器（allocateRank advance=false）、不改冷却/限流账；
// 反复调用本函数不改变任何选路行为。
func AllocationMonitor(nowMs int64) AllocationSnapshot {
	groups := op.GroupList()
	settings := allocateSettingsOf()
	view := AllocationSettingsView{
		EstimateTokens:     settings.tokens,
		HealthWeight:       settings.healthWeight,
		SlowLatencyMs:      settings.slowMs,
		MinRequests:        settings.minRequests,
		MemberRPMLimit:     settings.rpmLimit,
		MemberTPMLimit:     settings.tpmLimit,
		ThrottleCapSeconds: settings.throttleCapMs / 1000,
		PointsPerUnit:      balancePointsPerUnit(),
		SpeedWeight:        settings.speed.weightPct,
		SlowTtfbMs:         settings.speed.slowTtfbMs,
		SlowTokensPerSec:   settings.speed.slowTps,
		FeMultiple:         settings.speed.feMultiple,
		FeFloorMs:          settings.speed.feFloorMs,
	}
	snapshot := AllocationSnapshot{
		Rows:       make([]MemberAllocationRow, 0),
		ModeCounts: make(map[string]int),
		Settings:   view,
	}
	snapshot.Summary.GeneratedAt = nowMs

	deps := hotRouteDeps()
	for _, group := range groups {
		flat := op.FlattenGroupItems(group)
		if len(flat) == 0 {
			continue
		}
		snapshot.Summary.GroupCount++
		snapshot.ModeCounts[string(group.Mode)] += len(flat)

		routeMu.Lock()
		route := routes[group.ID]
		cooldowns := map[int]int64{}
		if route != nil {
			cooldowns = cloneCooldowns(route.Cooldowns)
		}
		routeMu.Unlock()

		// 分压口径的样本（与真正选路同一函数）: 只有 allocate 模式需要权重, 其它模式的成员
		// 照常列出（用户要看全部成员的去向）, 但权重留 0 表示"这个模式不做分压"。
		samples := map[int]allocateSample{}
		factors := map[int]float64{}
		if group.Mode == model.GroupModeAllocate {
			in := allocateInput{tokens: settings.tokens, pointsPerUnit: balancePointsPerUnit(), settings: settings}
			planned, plannedFactors, cooling, err := allocatePlan(flat, cooldowns, nowMs, nil, deps, in)
			if err == nil {
				for index, sample := range planned {
					samples[sample.item.ID] = sample
					if index < len(plannedFactors) {
						factors[sample.item.ID] = plannedFactors[index]
					}
				}
			}
			_ = cooling
		}

		for _, item := range flat {
			row := MemberAllocationRow{
				ItemID:      item.ID,
				GroupID:     group.ID,
				GroupName:   group.Name,
				ChannelID:   item.ChannelID,
				ChannelName: item.ChannelName,
				ModelName:   item.ModelName,
				KeyName:     item.KeyName,
				Priority:    item.Priority,
				Available:   item.Available,
				SmartTier:   item.SmartTier,
			}
			snapshot.Summary.MemberCount++

			// 冷却与限流账（只读）。
			if deadline, ok := cooldowns[item.ID]; ok && deadline > nowMs {
				row.Cooling = true
				row.CooldownMs = deadline - nowMs
				snapshot.Summary.CoolingCount++
			}
			if record, ok := MemberThrottleOf(item.ID); ok {
				row.ThrottleHits = record.Hits
				if record.UntilMs > nowMs {
					row.Throttled = true
					row.ThrottleMs = record.UntilMs - nowMs
					snapshot.Summary.ThrottledCount++
				}
			}
			// 质量/延迟/近期负载。
			if rate, ok := deps.quality(item.ID); ok {
				row.SuccessRate, row.HasSamples = rate, true
			}
			if ms, ok := deps.latency(item.ID); ok {
				row.LatencyMs = ms
			}
			if requests, tokens, ok := deps.load(item.ID); ok {
				row.RecentReqs, row.RecentTokens = requests, tokens
			}
			// 速度读数与结论（T-speed-001）：读数来自速度账, 结论来自与设置同一函数,
			// 因此面板上的"慢"一定与选路时的折扣是同一个判断。
			if reading, ok := memberSpeedReading(item.ID); ok {
				row.SpeedSamples = reading.Samples
				row.TtfbMs = reading.TtfbMs
				row.TokensPerSec = reading.TokensPerSec
				row.SpeedReady = reading.Ready
			}
			row.SpeedFactor = memberSpeedFactor(item.ID, settings.speed)
			row.Slow = row.SpeedReady && row.SpeedFactor < 1
			if row.Slow {
				snapshot.Summary.SlowCount++
			}

			if sample, ok := samples[item.ID]; ok {
				row.Known = sample.known
				row.Requests = sample.requests
				row.Source = sample.source
				row.Weight = sample.weight
				row.Limited = sample.limited
				row.Counted = true
				if factor, ok := factors[item.ID]; ok {
					row.HealthFactor = factor
				}
			}
			if row.Limited {
				snapshot.Summary.LimitedCount++
			}
			if row.Known {
				snapshot.Summary.KnownCount++
				snapshot.Summary.TotalRequests += row.Requests
				if snapshot.Summary.MaxRequests == 0 || row.Requests > snapshot.Summary.MaxRequests {
					snapshot.Summary.MaxRequests = row.Requests
				}
				if snapshot.Summary.MinRequests == 0 || row.Requests < snapshot.Summary.MinRequests {
					snapshot.Summary.MinRequests = row.Requests
				}
			} else if group.Mode == model.GroupModeAllocate {
				// 只有分压模式才谈"剩余请求数未知"——其它模式本来就不用这个维度。
				snapshot.Summary.UnknownCount++
			}
			snapshot.Rows = append(snapshot.Rows, row)
		}
	}

	// 明细排序: 先按分组, 再按权重降序（分压模式的分流顺序）, 最后按成员行 ID 保证稳定。
	sort.SliceStable(snapshot.Rows, func(i, j int) bool {
		if snapshot.Rows[i].GroupID != snapshot.Rows[j].GroupID {
			return snapshot.Rows[i].GroupID < snapshot.Rows[j].GroupID
		}
		if snapshot.Rows[i].Weight != snapshot.Rows[j].Weight {
			return snapshot.Rows[i].Weight > snapshot.Rows[j].Weight
		}
		return snapshot.Rows[i].ItemID < snapshot.Rows[j].ItemID
	})
	return snapshot
}

// AllocationNowMs 供调用方（接口层）取统一的时间锚点：全部剩余时长都相对同一个时刻计算。
func AllocationNowMs() int64 { return time.Now().UnixMilli() }
