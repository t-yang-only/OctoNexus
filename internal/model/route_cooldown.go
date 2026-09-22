package model

import "time"

// RouteCooldown 把「某个分组成员正处于冷却」这一事实落库。
//
// 为什么要持久化：冷却默认 600 秒（337 个分组都用默认值），而它原先只存在进程内存里。
// 每次重启（部署、崩溃自愈、改配置）都会把**全部**正在冷却的成员一次性放行，
// 刚被上游 429 的渠道会被立刻重新打满 —— 冷却是"上游说别来了"的证据，
// 它不该因为重启而失效。
//
// 只存冷却，不存亲和/当前成员/轮询累加器：
//   - 亲和（默认 30 秒）与加权轮询的轮转计数是短周期、与"这一刻的流量分布"绑定的调优状态，
//     重启后重新收敛是合理的，落了库反而会把旧分布带进新进程；
//   - 冷却是**外部事实**的记账（上游限流、成员故障），生命周期与进程无关。
//
// 到期条目由读取方按 now 过滤并顺手清理，不做后台清理任务：
// 一张只在重启时读、在每次冷却变更时写的表，行数上界就是成员总数，无需额外机制。
type RouteCooldown struct {
	ID        int       `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupID   int       `json:"group_id" gorm:"uniqueIndex:idx_route_cooldown_member,priority:1;not null"`
	ItemID    int       `json:"item_id" gorm:"uniqueIndex:idx_route_cooldown_member,priority:2;not null"`
	Deadline  int64     `json:"deadline" gorm:"index;not null"` // 冷却截止 Unix 毫秒
	UpdatedAt time.Time `json:"updated_at"`
}
