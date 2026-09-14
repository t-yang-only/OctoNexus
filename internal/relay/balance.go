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

// ErrNoEligibleMember 表示候选全部被剔除。
var ErrNoEligibleMember = errors.New("no eligible member")

// LatencyProvider 返回成员最近延迟毫秒；ok=false 表示无数据（不参与排序）。
type LatencyProvider func(itemID int) (latencyMs int64, ok bool)

// rankCandidates 对候选做加权轮询定序：
// 平滑加权轮询的可测纯函数版——权重 w=1+n-i（i 为 priority 升序下标），
// round 为轮转序号（调用方自增，0 起），返回第 round 个当选者的定序表（首个即当选）。
// 冷却中成员压到队尾（仍保留，到期回）；被剔除的成员不在表内。
func rankCandidates(items []model.GroupItem, cooldowns map[int]int64, nowMs int64, zeroBalanceGrantIDs map[int]bool, latency LatencyProvider, round uint64) ([]model.GroupItem, error) {
	eligible := make([]model.GroupItem, 0, len(items))
	cooling := make([]model.GroupItem, 0)
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
		return nil, ErrNoEligibleMember
	}
	// priority 升序定权重：首个权重最高。
	sorted := append([]model.GroupItem(nil), eligible...)
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
	out := make([]model.GroupItem, 0, len(sorted)+len(cooling))
	for _, idx := range order {
		out = append(out, sorted[idx])
	}
	if latency != nil {
		stableLatencySort(out, latency)
	}
	return append(out, cooling...), nil
}

// smoothWeightedOrder 返回平滑加权轮询在给定 round 下的当选者优先的下标序列：
// 经典算法 current[i]+=w[i]，选最大者 current-=total；为纯函数可测。
// 平滑加权轮询以 total 步为一个周期（每步累加器总增 total、总减 total，
// T 步内各成员恰被选中 w_i 次，走完回到全零），故先对 total 取模再重放：
// 调用方按请求计数自增 round，长期运行会涨到上亿，直接重放就是一次 O(round)
// 的挂死。取模后语义与全量重放完全一致。
func smoothWeightedOrder(n int, weights []int, total int, round uint64) []int {
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
