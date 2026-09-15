package relay

import (
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// RouteState 是一个分组的进程内路由状态; 跨该分组的全部请求共享。
// 同时作为路由流的消息形状与分组读取响应中的 runtime 字段: 冷却, 探测与亲和都是本包路由算法的概念,
// 故状态形状由本包定义, 分组的持久化配置不含它; 内部标志未导出, 不会随消息出到 JSON。
// 两种模式共用 CurrentItemID: 手动模式下即人工指定的成员, 故障转移模式下由路由决定,
// 前端由此只读这一个字段即可知道当前承载请求的成员, 无需再按模式分支。
type RouteState struct {
	GroupID       int           `json:"group_id"`        // 状态所属的分组 ID, 供状态流按分组定位。
	CurrentItemID int           `json:"current_item_id"` // 当前承载请求的成员 ID, 0 表示尚未建立路由或未人工指定。
	ProbeItemID   int           `json:"probe_item_id"`   // 当前占用恢复探测的成员 ID, 同一分组同时只允许一个成员被探测; 手动模式恒为 0。
	AffinityUntil int64         `json:"affinity_until"`  // 当前路由的亲和截止 Unix 毫秒时间, 0 表示无亲和; 手动模式恒为 0。
	Cooldowns     map[int]int64 `json:"cooldowns"`       // 失败成员 ID 对应的冷却截止 Unix 毫秒时间, 已到期的条目由前端按当前时间忽略。

	affinityArmed bool   // 当前路由下一次成功后是否开始亲和, 仅故障切换后为真。
	balanceRound  uint64 // 加权轮询 (T-route-002 L5) 的当选者轮转计数, 仅在 flag 开启时推进, 随状态重置归零。
}

const routeStreamBuffer = 16 // 单个路由流连接的非阻塞消息缓冲容量。

// routeBalanceEnabled 是加权轮询热路径 (T-route-002 L5) 的进程内功能开关, 默认关闭。
// 关时 pickGroupItemHot 行为与原 pickGroupItem 完全一致; 开时仅改 failover 候选定序。
// 锁在 relay 包内 (201 锁表 L5 只拥有 relay/handler+route+balance), 故用本包原子量承载,
// 不读 op 设置缓存: 避免越界改 model/op 的 setting.go (221 划归 L4 占用)。
// 由装配层 (server 启动 / 设置变更) 经 SetRouteBalanceEnabled 注入, relay 自身不耦合配置源。
var routeBalanceEnabled atomic.Bool

var (
	routeMu      sync.Mutex                           // routeMu 保护全部分组路由状态。
	routes       = make(map[int]*RouteState)          // routes 按分组 ID 保存路由状态。
	routeStreams = make(map[chan RouteState]struct{}) // 全部路由 SSE 连接。
)

// RouteStateOf 返回分组当前的实时路由状态, 供读取接口随分组一并返回。
// 手动模式没有进程内路由: 当前成员即人工指定的成员, 冷却与亲和均不适用, 故直接由分组配置得出。
func RouteStateOf(group model.Group) RouteState {
	if group.Mode == model.GroupModeManual {
		return RouteState{
			GroupID:       group.ID,
			CurrentItemID: group.ActiveItemID,
			Cooldowns:     map[int]int64{},
		}
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return RouteState{GroupID: group.ID, Cooldowns: map[int]int64{}}
	}
	state := *route
	state.Cooldowns = maps.Clone(route.Cooldowns)
	return state
}

// ResetRouteState 丢弃分组的进程内路由状态, 用于分组切换选择模式或被删除。
// 不丢弃的话冷却与亲和会在 failover 切到 manual 再切回来之后复活并继续影响选路, 分组删除后其状态也会永久残留。
func ResetRouteState(groupID int) {
	routeMu.Lock()
	defer routeMu.Unlock()

	delete(routes, groupID)
}

// pickGroupItem 按分组模式选择本轮目标成员, 没有可用成员时返回零值; group.Items 已按 Priority 升序排列。
// 渠道是否可用不在此判断: 渠道禁用或缺少密钥由调用方发现并作为一轮失败上报, 该成员随即进入冷却而在后续轮次被跳过。
func pickGroupItem(group model.Group) model.GroupItem {
	if group.Mode == model.GroupModeManual {
		for _, item := range group.Items {
			if item.ID == group.ActiveItemID {
				return item
			}
		}
		return model.GroupItem{}
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := groupRouteLocked(group)
	now := time.Now().UnixMilli()
	if route.AffinityUntil <= now {
		route.AffinityUntil = 0
	}

	// 亲和期内沿用当前成员, 不提前探测已恢复的高优先级成员。
	if route.CurrentItemID != 0 && route.AffinityUntil > now {
		return itemOf(group, route.CurrentItemID)
	}

	for _, item := range group.Items {
		// 遍历到当前成员说明比它优先级更高的成员都不可选, 沿用当前成员。
		if item.ID == route.CurrentItemID {
			break
		}
		deadline, cooling := route.Cooldowns[item.ID]
		if cooling && deadline > now {
			continue
		}
		// 冷却已到期的成员只放行一个探测请求, 避免全部请求同时涌向尚未恢复的成员。
		if cooling {
			if route.ProbeItemID != 0 {
				continue
			}
			route.ProbeItemID = item.ID
			publishRouteLocked(route)
			return item
		}
		route.CurrentItemID = item.ID
		publishRouteLocked(route)
		return item
	}
	if route.CurrentItemID != 0 {
		return itemOf(group, route.CurrentItemID)
	}
	return model.GroupItem{}
}

// recordRouteSuccess 上报一轮成功: 结束该成员的冷却与探测占用, 并在故障切换后按配置开始亲和。
func recordRouteSuccess(group model.Group, itemID int, latencyMs int64) {
	// 质量/延迟样本与模式无关: 手动模式也记, 便于切模式时立刻有历史可依。
	recordMemberOutcome(itemID, true, latencyMs)

	if group.Mode == model.GroupModeManual {
		return
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return
	}
	now := time.Now().UnixMilli()
	changed := false

	// 探测成功说明该成员已恢复, 解除冷却; 若当前路由不在亲和期内则立即切回该成员。
	if route.ProbeItemID == itemID {
		route.ProbeItemID = 0
		delete(route.Cooldowns, itemID)
		if route.CurrentItemID == 0 || route.AffinityUntil <= now {
			route.CurrentItemID = itemID
			route.AffinityUntil = 0
		}
		changed = true
	}
	// 亲和只在故障切换后的首次成功时开始, 使请求在一段时间内稳定留在备用成员上。
	if route.CurrentItemID == itemID && route.affinityArmed {
		route.affinityArmed = false
		if group.RelayConfig.MemberAffinitySeconds > 0 {
			route.AffinityUntil = now + int64(group.RelayConfig.MemberAffinitySeconds)*1000
			changed = true
		}
	}
	if changed {
		publishRouteLocked(route)
	}
}

// recordRouteFailure 上报一轮失败: 达到配置的总尝试次数后将该成员打入冷却并让出当前路由, 返回是否已冷却。
// failures 为该成员在本请求内包含首次请求的连续失败次数, 由调用方累计。
func recordRouteFailure(group model.Group, itemID, failures int, latencyMs int64) bool {
	// 一次失败一轮即记一次: 质量口径就是"成员尝试成功率", 与成员尝试上限无关。
	recordMemberOutcome(itemID, false, latencyMs)

	if group.Mode == model.GroupModeManual {
		return false
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return false
	}
	// 探测请求只有一次机会, 常规成员达到配置的总尝试次数后进入冷却。
	if route.ProbeItemID != itemID && failures < group.RelayConfig.MemberMaxAttempts {
		return false
	}

	now := time.Now().UnixMilli()
	route.Cooldowns[itemID] = now + int64(group.RelayConfig.MemberCooldownSeconds)*1000
	if route.ProbeItemID == itemID {
		route.ProbeItemID = 0
	}
	// 当前路由失败才需要下一个成员开始亲和; 独立探测失败不影响当前路由。
	if route.CurrentItemID == itemID {
		route.CurrentItemID = 0
		route.AffinityUntil = 0
		route.affinityArmed = true
	}
	publishRouteLocked(route)
	return true
}

// clearMemberCooldown 解除单个成员的冷却, 供主动探活确认恢复时调用 (R-probe-001)。
// 只删冷却条目, 不动当前路由与亲和: 正在服务的成员不因一次后台探测被切走,
// 该成员在亲和窗口结束后按既有优先级自然回归。返回是否确有冷却被解除。
func clearMemberCooldown(group model.Group, itemID int) bool {
	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return false
	}
	if _, cooling := route.Cooldowns[itemID]; !cooling {
		return false
	}
	delete(route.Cooldowns, itemID)
	publishRouteLocked(route)
	return true
}

// releaseRouteProbe 归还未产生成败结论的探测占用, 用于请求被人工中止或客户端断开。
func releaseRouteProbe(group model.Group, itemID int) {
	routeMu.Lock()
	defer routeMu.Unlock()

	if route := routes[group.ID]; route != nil && route.ProbeItemID == itemID {
		route.ProbeItemID = 0
		publishRouteLocked(route)
	}
}

// groupRouteLocked 取出分组路由状态并清理已删除成员的残留; 调用方必须持有锁。
func groupRouteLocked(group model.Group) *RouteState {
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
		routes[group.ID] = route
	}
	items := make(map[int]bool, len(group.Items))
	for _, item := range group.Items {
		items[item.ID] = true
	}
	for itemID := range route.Cooldowns {
		if !items[itemID] {
			delete(route.Cooldowns, itemID)
		}
	}
	if route.ProbeItemID != 0 && !items[route.ProbeItemID] {
		route.ProbeItemID = 0
	}
	if route.CurrentItemID != 0 && !items[route.CurrentItemID] {
		route.CurrentItemID = 0
		route.AffinityUntil = 0
		route.affinityArmed = false
	}
	return route
}

// itemOf 返回分组内指定 ID 的成员, 不存在时返回零值。
func itemOf(group model.Group, itemID int) model.GroupItem {
	for _, item := range group.Items {
		if item.ID == itemID {
			return item
		}
	}
	return model.GroupItem{}
}

// PickGroupItem 是 pickGroupItem 的可测外壳: 传入顶层分组配置与已展平的授权
// 成员平面表 (op.FlattenGroupItems), 手动与故障转移双分支统一走展平语义。
// 路由状态仍按顶层 group.ID 清理与持有, 冷却/亲和/上限键均为展平后具体成员行 ID。
// 平面表为空时返回零值, 调用方按无目标等待。
func PickGroupItem(group model.Group, flat []model.GroupItem) model.GroupItem {
	return pickGroupItem(group.WithItems(flat))
}

// pickGroupItemHot 是转发热循环的唯一选路入口: lowest_cost / quality_first / lowest_latency / least_busy 由分组模式自身显式开启,
// 不受全局加权轮询开关约束; 其余模式 flag 关走原路径, flag 开走加权轮询定序。
func pickGroupItemHot(group model.Group) model.GroupItem {
	return pickGroupItemByMode(group, routeDeps{
		cost:    memberUnitPrice,
		quality: memberSuccessRate,
		latency: memberLatencyMs,
		busy:    memberBusyCount,
	}, RouteBalanceEnabled())
}

// pickGroupItemByMode 按分组模式与加权轮询开关分发选路（热路径与单测共用同一入口）:
// manual/未识别模式走原语义, failover 依开关决定是否加权轮询定序,
// lowest_cost / quality_first / lowest_latency / least_busy 走各自的定序（模式的显式选择本身即开关）。
func pickGroupItemByMode(group model.Group, deps routeDeps, balanceEnabled bool) model.GroupItem {
	switch {
	case group.Mode == model.GroupModeLowestCost:
		return pickGroupItemLowestCost(group, deps.cost)
	case group.Mode == model.GroupModeQualityFirst:
		return pickGroupItemQualityFirst(group, deps.quality)
	case group.Mode == model.GroupModeLowestLatency:
		return pickGroupItemLowestLatency(group, deps.latency)
	case group.Mode == model.GroupModeLeastBusy:
		return pickGroupItemLeastBusy(group, deps.busy)
	case balanceEnabled:
		return pickGroupItemBalanced(group)
	default:
		return pickGroupItem(group)
	}
}

// RouteBalanceEnabled 报告加权轮询热路径 (T-route-002 L5) 是否开启; 默认关闭,
// 关时转发行为与既有 pickGroupItem 路径完全一致。开关由装配层经 SetRouteBalanceEnabled 注入。
func RouteBalanceEnabled() bool { return routeBalanceEnabled.Load() }

// SetRouteBalanceEnabled 设置加权轮询热路径开关, 供 server 启动或设置变更时调用;
// 变更后清掉各分组轮次, 避免开关翻转沿用旧轮转计数 (flag 关时本不推进轮次, 这里防御性复位)。
func SetRouteBalanceEnabled(enabled bool) {
	routeBalanceEnabled.Store(enabled)
	if !enabled {
		routeMu.Lock()
		for _, route := range routes {
			route.balanceRound = 0
		}
		routeMu.Unlock()
	}
}

// pickGroupItemBalanced 在加权轮询定序下选出本轮目标: 只改"候选谁先被尝试"的顺序,
// 亲和/冷却/探测/上限语义仍归 pickGroupItem 既有链路——先按 rankCandidates 定序,
// 再用重排后的成员表走原选路函数, 首选即加权轮询当选者。
// 定序失败 (候选全部被剔除) 时回退零值, 调用方按无目标等待, 与空表语义一致。
// 轮转计数挂在顶层 RouteState 上随状态重置归零; 仅 failover 模式参与, manual 恒走原路径。
func pickGroupItemBalanced(group model.Group) model.GroupItem {
	if group.Mode != model.GroupModeFailover {
		return pickGroupItem(group)
	}

	routeMu.Lock()
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
		routes[group.ID] = route
	}
	// 先取本轮 round 再自增: 首轮恒为 round0 (复位后同理), 使 SWRR 周期从当选序列首位起算。
	round := route.balanceRound
	route.balanceRound++
	cooldowns := maps.Clone(route.Cooldowns)
	routeMu.Unlock()

	ranked, err := rankCandidates(group.Items, cooldowns, time.Now().UnixMilli(), nil, nil, round)
	if err != nil || len(ranked) == 0 {
		return model.GroupItem{}
	}
	return pickGroupItem(group.WithItems(ranked))
}

// PickGroupItemBalanced 是 pickGroupItemBalanced 的可测外壳, 形状与 PickGroupItem 一致。
func PickGroupItemBalanced(group model.Group, flat []model.GroupItem) model.GroupItem {
	return pickGroupItemBalanced(group.WithItems(flat))
}

// publishRouteLocked 非阻塞发布路由状态, 连接拥塞时关闭它并交给客户端重连获取全量快照; 冷却表按值复制以免前端读到后续变更; 调用方必须持有锁。
func publishRouteLocked(route *RouteState) {
	message := *route
	message.Cooldowns = maps.Clone(route.Cooldowns)
	for stream := range routeStreams {
		select {
		case stream <- message:
		default:
			delete(routeStreams, stream)
			close(stream)
		}
	}
}

// OpenRouteStream 注册路由流连接, 返回后续增量通道。
// 不再返回快照: 分组读取接口已随分组带回当前路由状态, 前端由此拿到的初始值即全量, 连接只负责增量。
func OpenRouteStream() chan RouteState {
	routeMu.Lock()
	defer routeMu.Unlock()

	stream := make(chan RouteState, routeStreamBuffer)
	routeStreams[stream] = struct{}{}
	return stream
}

// CloseRouteStream 注销并关闭指定路由流连接。
func CloseRouteStream(stream chan RouteState) {
	routeMu.Lock()
	defer routeMu.Unlock()

	if _, exists := routeStreams[stream]; exists {
		delete(routeStreams, stream)
		close(stream)
	}
}
