package relay

import (
	"errors"
	"sort"

	"github.com/bestruirui/octopus/internal/model"
)

// T-route-002 均衡请求第一版：号池内加权轮询候选器。
// 口径（登记台 A 车道 W2#2 登记语义）：
//  1. 加权轮询（平滑加权轮询 smooth weighted round-robin）：
//     成员权重=优先级倒序分（priority 越小权重越大），调用方以"连续请求无状态打分"
//     的方式从候选里挑下一个，避免状态竞争；有状态轮转由调用方按返回顺序消费
//     或复用 pickGroupItem 的 CurrentItemID 亲和即可——本包只给"候选定序"，
//     不另起一套路由状态（亲和/冷却仍归顶层 RouteState）。
//  2. 健康/冷却/归零剔除：
//     - 冷却中成员（deadline 未到）天然排后，不剔除（到期即回）；
//     - channel.Enabled=false、凭据停用（QuotaZeroStop 已把 ChannelKey.Enabled=false）
//       导致 Available=false 的成员剔除；快照 Available 即渠道+凭据启用口径；
//     - 剩余额度<=0 归零的渠道由 T-quota-002 停用凭据后自然 Available=false，此处
//       另收 zeroBalanceGrantIDs 显式剔除（调用方把阈值事件打中的授权 ID 传进来）。
//  3. 最低延迟可插拔接口：LatencyProvider 由调用方实现（本包默认 nil=不参与排序），
//     有值时在同权重分档内按延迟升序（P50 口径由提供方定）；无值时保持 priority 顺序。
//  4. 慢成员分区（T-route-003，见 splitSlow）：最近一次尝试耗时超过阈值的成员排到
//     正常成员之后。这是全部模式共用的收口点，failover 也由此获得慢成员保护。

// ErrNoEligibleMember 表示候选全部被剔除。
var ErrNoEligibleMember = errors.New("no eligible member")

// LatencyProvider 返回成员最近延迟毫秒；ok=false 表示无数据（不参与排序）。
type LatencyProvider func(itemID int) (latencyMs int64, ok bool)

// rankCandidates 对候选做加权轮询定序：
// 平滑加权轮询的可测纯函数版——权重 w=1+n-i（i 为 priority 升序下标），
// round 为轮转序号（调用方自增，0 起），返回第 round 个当选者的定序表（首个即当选）。
// 冷却中成员压到队尾（仍保留，到期回）；被剔除的成员不在表内。
func rankCandidates(items []model.GroupItem, cooldowns map[int]int64, nowMs int64, zeroBalanceGrantIDs map[int]bool, latency LatencyProvider, round uint64) ([]model.GroupItem, error) {
	eligible, cooling, err := partitionCandidates(items, cooldowns, nowMs, zeroBalanceGrantIDs)
	if err != nil {
		return nil, err
	}
	// 慢成员判定用包内统一数据源, 而不是本函数收到的 latency:
	// failover 走这条路时 latency 传的就是 nil（它不按延迟排序），若沿用该参数,
	// 保护在最主要的模式上会静默失效。排序键仍按传入的 latency（见下方 stableLatencySort）。
	fast, slow := splitSlow(eligible, slowLatencySource, slowLatencyThresholdMs())
	// priority 升序定权重：首个权重最高。
	// 全员都慢时 fast 为空：此时没有"正常成员"可轮询, 直接以 slow 打头 ——
	// 不能进 smoothWeightedOrder, 它对空集会在 current[best] 上越界（best 恒为 -1）。
	out := make([]model.GroupItem, 0, len(fast)+len(slow)+len(cooling))
	if len(fast) > 0 {
		sorted := append([]model.GroupItem(nil), fast...)
		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].Priority != sorted[j].Priority {
				return sorted[i].Priority < sorted[j].Priority
			}
			return sorted[i].ID < sorted[j].ID
		})
		weights := make([]int, len(sorted))
		total := 0
		for i := range sorted {
			weights[i] = len(sorted) - i
			total += weights[i]
		}
		order := smoothWeightedOrder(len(sorted), weights, total, round)
		for _, idx := range order {
			out = append(out, sorted[idx])
		}
	}
	if latency != nil {
		stableLatencySort(out, latency)
	}
	out = append(out, slow...)
	return append(out, cooling...), nil
}

// partitionCandidates 是各选路策略共用的候选分区：eligible 为当前可尝试的成员,
// cooling 为冷却中成员（到期即回, 由调用方压到队尾）；渠道或凭据停用（Available=false）与
// 剩余额度归零（zeroBalanceGrantIDs）的成员直接剔除。可尝试成员为空时返回 ErrNoEligibleMember,
// 由调用方按"无目标"等待——冷却成员不算可尝试, 与加权轮询定序的既有语义保持一致。
func partitionCandidates(items []model.GroupItem, cooldowns map[int]int64, nowMs int64, zeroBalanceGrantIDs map[int]bool) (eligible, cooling []model.GroupItem, err error) {
	eligible = make([]model.GroupItem, 0, len(items))
	for _, item := range items {
		if !item.Available {
			continue
		}
		if zeroBalanceGrantIDs != nil && zeroBalanceGrantIDs[item.GrantRef()] {
			continue
		}
		if deadline, ok := cooldowns[item.ID]; ok && deadline > nowMs {
			cooling = append(cooling, item)
			continue
		}
		eligible = append(eligible, item)
	}
	if len(eligible) == 0 {
		return nil, cooling, ErrNoEligibleMember
	}
	return eligible, cooling, nil
}

// splitSlow 把可尝试成员分成"正常"与"慢"两段（T-route-003）。
//
// 为什么需要它：冷却只惩罚**失败**的成员，而"成功但极慢"的成员永远不会被冷却——
// 实测一个首字节 71~140 秒的上游在 failover 分组里按 priority 长期占据队首，
// 每次请求都要先等它把首事件超时耗完才换人（一次 163 秒）。慢不是故障，
// 所以既不能把它塞进冷却（那会让探测/等待链路接管一个其实能用的成员），
// 也不能剔除（成员少的分组还要靠它兜底）。它只是不该排在别人前面。
//
// 三条判据：
//   - 阈值 <=0 或没有延迟数据源时整体关闭，返回原序（failover 的既有语义逐字不变）；
//   - 只认"有样本且超过阈值"，无样本的新成员保持乐观先验，不会被压到队尾。
//     变异检查结论：`ok` 判断在当前数据源下功能冗余 —— memberLatencyMs 对无样本返回
//     的是 0，而阈值必须为正才走到这里，0 永远超不过正阈值，所以删掉 `ok &&` 没有任何
//     用例变红。保留它是把"无样本 != 慢"的意图写在代码里，并在将来出现"以零值表示未知"
//     的数据源时仍然正确；
//   - fast 全空时 slow 仍是返回值的一部分，调用方拼成 fast+slow+cooling 后
//     首个候选依然是可用成员——"全是慢成员"不等于"无可用成员"。
func splitSlow(eligible []model.GroupItem, latency LatencyProvider, thresholdMs int64) (fast, slow []model.GroupItem) {
	if thresholdMs <= 0 || latency == nil || len(eligible) == 0 {
		return eligible, nil
	}
	fast = make([]model.GroupItem, 0, len(eligible))
	for _, item := range eligible {
		if ms, ok := latency(item.ID); ok && ms > thresholdMs {
			slow = append(slow, item)
			continue
		}
		fast = append(fast, item)
	}
	return fast, slow
}

// smoothWeightedOrder 返回平滑加权轮询在给定 round 下的当选者优先的下标序列：
// 经典算法 current[i]+=w[i]，选最大者 current-=total；为纯函数可测。
// 平滑加权轮询以 total 步为一个周期（每步累加器总增 total、总减 total，
// T 步内各成员恰被选中 w_i 次，走完回到全零），故先对 total 取模再重放：
// 调用方按请求计数自增 round，长期运行会涨到上亿，直接重放就是一次 O(round)
// 的挂死。取模后语义与全量重放完全一致。
func smoothWeightedOrder(n int, weights []int, total int, round uint64) []int {
	// 空集是合法输入（全员都慢时调用方根本不进来, 但纯函数不该对空输入 panic）。
	if n <= 0 || len(weights) < n {
		return nil
	}
	if total > 0 {
		round = round % uint64(total)
	}
	current := make([]int, n)
	order := make([]int, 0, n)
	picked := make([]bool, n)
	for step := uint64(0); step <= round; step++ {
		best := -1
		for i := range current {
			current[i] += weights[i]
			if best == -1 || current[i] > current[best] {
				best = i
			}
		}
		if step < round {
			current[best] -= total
			continue
		}
		current[best] -= total
		picked[best] = true
		order = append(order, best)
	}
	for i := range current {
		if !picked[i] {
			order = append(order, i)
		}
	}
	return order
}

// stableLatencySort 在同 priority 档内按延迟升序稳定排序；无延迟数据的成员沉底（仍保留）。
func stableLatencySort(out []model.GroupItem, latency LatencyProvider) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return false
		}
		li, oki := latency(out[i].ID)
		lj, okj := latency(out[j].ID)
		if oki && okj {
			return li < lj
		}
		return oki && !okj
	})
}
