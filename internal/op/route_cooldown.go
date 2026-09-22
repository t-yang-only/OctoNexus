package op

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm/clause"
)

// 冷却状态持久化（T-route-003）。
//
// 背景：冷却是"上游说别来了"的记账（默认 600 秒），原先只存在进程内存里。
// 每次重启都会把全部正在冷却的成员一次性放行 —— 部署、崩溃自愈、改配置都触发。
// 实测环境下 337 个分组用默认 600 秒冷却，而部署本身就会重启实例。
//
// 这里只做两件事：写（冷却变更时 upsert / 解除时删除）与读（启动时恢复未到期的）。
// 到期条目在读取时过滤并顺手清掉，不引入后台清理任务 —— 行数上界就是成员总数。

// RouteCooldownSave 记录（或更新）一个成员的冷却截止时刻。
//
// 失败只告警不返回错误：落库失败不该让转发路径失败 —— 冷却是"优化下次选择"的信息，
// 不是请求正确性的一部分。宁可丢掉一次持久化，也不能让一次转发因为写库失败而报错。
func RouteCooldownSave(groupID, itemID int, deadline int64) {
	conn := db.GetDB()
	if conn == nil || !conn.Migrator().HasTable(&model.RouteCooldown{}) {
		return
	}
	row := model.RouteCooldown{GroupID: groupID, ItemID: itemID, Deadline: deadline}
	err := conn.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "group_id"}, {Name: "item_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"deadline", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		log.Warnf("persist route cooldown (group=%d item=%d) failed: %v", groupID, itemID, err)
	}
}

// RouteCooldownClear 删除一个成员的冷却记录（冷却被解除或到期）。
func RouteCooldownClear(groupID, itemID int) {
	conn := db.GetDB()
	if conn == nil || !conn.Migrator().HasTable(&model.RouteCooldown{}) {
		return
	}
	if err := conn.Where("group_id = ? AND item_id = ?", groupID, itemID).
		Delete(&model.RouteCooldown{}).Error; err != nil {
		log.Warnf("clear route cooldown (group=%d item=%d) failed: %v", groupID, itemID, err)
	}
}

// RouteCooldownClearGroup 删除一个分组的全部冷却记录（分组被删除或模式切到手动）。
func RouteCooldownClearGroup(groupID int) {
	conn := db.GetDB()
	if conn == nil || !conn.Migrator().HasTable(&model.RouteCooldown{}) {
		return
	}
	if err := conn.Where("group_id = ?", groupID).Delete(&model.RouteCooldown{}).Error; err != nil {
		log.Warnf("clear route cooldowns for group %d failed: %v", groupID, err)
	}
}

// RouteCooldownLoad 读出全部**仍在冷却期内**的记录，按分组聚合。
//
// 两条口径：
//   - 已到期的条目直接丢弃并顺手删除（不返回给调用方）。若不删，这条证据会永远留在库里，
//     下次启动时再被过滤一次 —— 行数只增不减。
//   - 表不存在按"没有冷却"处理并告警（与 ModelMappingRefresh 同一纪律）：
//     缺这张表不该让实例起不来，而"没有冷却"恰好就是安全的默认行为（不误封任何成员）。
func RouteCooldownLoad(ctx context.Context) (map[int]map[int]int64, error) {
	conn := db.GetDB().WithContext(ctx)
	if !conn.Migrator().HasTable(&model.RouteCooldown{}) {
		log.Warnf("route cooldowns: table missing, treating as empty (no restored cooldowns)")
		return map[int]map[int]int64{}, nil
	}

	var rows []model.RouteCooldown
	if err := conn.Find(&rows).Error; err != nil {
		return nil, err
	}

	nowMs := time.Now().UnixMilli()
	out := make(map[int]map[int]int64)
	expired := make([]int, 0)
	for _, row := range rows {
		if row.Deadline <= nowMs {
			expired = append(expired, row.ID)
			continue
		}
		if out[row.GroupID] == nil {
			out[row.GroupID] = make(map[int]int64)
		}
		out[row.GroupID][row.ItemID] = row.Deadline
	}
	if len(expired) > 0 {
		// 顺手清理：这些条目已经没有任何作用，留着只会让表持续增长。
		if err := conn.Where("id IN ?", expired).Delete(&model.RouteCooldown{}).Error; err != nil {
			log.Warnf("prune %d expired route cooldowns failed: %v", len(expired), err)
		}
	}
	return out, nil
}
