package op

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

// 手动订阅（R-acct-004 / T-acct-005）：上游没有余额接口时，由人录一条套餐余额与有效期，
// 它按与自动读到的余额**同一口径**折算后并入总余额（T-balance-001）。
//
// 为什么用内存缓存而不是每次查库：余额快照会被转发口的 /v1/dashboard/billing/* 端点按请求触发，
// 走库会让每条客户端请求多一次查询；这张表的规模是"人手工录的几条"，缓存代价可以忽略。
var manualSubscriptionCache = struct {
	sync.RWMutex
	items []model.ManualSubscription
}{}

// ManualSubscriptionList 返回全部手动订阅（按通道与主键定序，便于面板与快照稳定输出）。
func ManualSubscriptionList() []model.ManualSubscription {
	manualSubscriptionCache.RLock()
	defer manualSubscriptionCache.RUnlock()
	out := make([]model.ManualSubscription, len(manualSubscriptionCache.items))
	copy(out, manualSubscriptionCache.items)
	return out
}

// ManualSubscriptionRefresh 从库刷新手动订阅缓存（启动与写操作后调用）。
//
// 表不存在时按"没有手动订阅"处理并告警，而不是让整次缓存初始化失败：
// 手动订阅是**可选的余额来源**（上游没有余额接口时才用得上），它的表缺失
// （只建了部分表的测试环境、极端情况下的老库）不该把渠道/分组/凭据的缓存一起拖垮、让实例起不来。
// 其它错误照旧上报 —— 只有"这张表不存在"这一种情况被容忍。
func ManualSubscriptionRefresh(ctx context.Context) error {
	conn := db.GetDB().WithContext(ctx)
	if !conn.Migrator().HasTable(&model.ManualSubscription{}) {
		log.Warnf("manual subscriptions: table missing, treating as empty (optional balance source)")
		manualSubscriptionCache.Lock()
		manualSubscriptionCache.items = nil
		manualSubscriptionCache.Unlock()
		return nil
	}
	items := []model.ManualSubscription{}
	if err := conn.Order("channel_id ASC, id ASC").Find(&items).Error; err != nil {
		return fmt.Errorf("failed to load manual subscriptions: %w", err)
	}
	manualSubscriptionCache.Lock()
	manualSubscriptionCache.items = items
	manualSubscriptionCache.Unlock()
	return nil
}

// ManualSubscriptionGet 按主键取一条。
func ManualSubscriptionGet(id int) (model.ManualSubscription, error) {
	for _, item := range ManualSubscriptionList() {
		if item.ID == id {
			return item, nil
		}
	}
	return model.ManualSubscription{}, fmt.Errorf("manual subscription %d not found", id)
}

// ManualSubscriptionCreate 新建一条：先归一化校验，再落库，最后刷新缓存。
func ManualSubscriptionCreate(ctx context.Context, input model.ManualSubscription) (model.ManualSubscription, error) {
	item, err := model.NormalizeManualSubscription(input)
	if err != nil {
		return model.ManualSubscription{}, err
	}
	if item.ChannelID != 0 {
		if _, err := ChannelGet(item.ChannelID); err != nil {
			return model.ManualSubscription{}, fmt.Errorf("渠道 %d 不存在", item.ChannelID)
		}
	}
	item.ID = 0
	now := time.Now().Unix()
	item.CreatedAt = now
	item.UpdatedAt = now
	if err := db.GetDB().WithContext(ctx).Create(&item).Error; err != nil {
		return model.ManualSubscription{}, fmt.Errorf("failed to create manual subscription: %w", err)
	}
	if err := ManualSubscriptionRefresh(ctx); err != nil {
		return item, err
	}
	return item, nil
}

// ManualSubscriptionUpdate 覆盖式更新：只允许改这些列，主键与创建时间不动。
func ManualSubscriptionUpdate(ctx context.Context, input model.ManualSubscription) (model.ManualSubscription, error) {
	item, err := model.NormalizeManualSubscription(input)
	if err != nil {
		return model.ManualSubscription{}, err
	}
	if item.ID <= 0 {
		return model.ManualSubscription{}, fmt.Errorf("id 非法")
	}
	current, err := ManualSubscriptionGet(item.ID)
	if err != nil {
		return model.ManualSubscription{}, err
	}
	if item.ChannelID != 0 {
		if _, err := ChannelGet(item.ChannelID); err != nil {
			return model.ManualSubscription{}, fmt.Errorf("渠道 %d 不存在", item.ChannelID)
		}
	}
	item.CreatedAt = current.CreatedAt
	item.UpdatedAt = time.Now().Unix()
	updates := map[string]any{
		"channel_id":      item.ChannelID,
		"name":            item.Name,
		"note":            item.Note,
		"balance_points":  item.BalancePoints,
		"points_per_unit": item.PointsPerUnit,
		"expire_at":       item.ExpireAt,
		"enabled":         item.Enabled,
		"updated_at":      item.UpdatedAt,
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.ManualSubscription{}).
		Where("id = ?", item.ID).Updates(updates).Error; err != nil {
		return model.ManualSubscription{}, fmt.Errorf("failed to update manual subscription: %w", err)
	}
	if err := ManualSubscriptionRefresh(ctx); err != nil {
		return item, err
	}
	return item, nil
}

// ManualSubscriptionDelete 删除一条。
func ManualSubscriptionDelete(ctx context.Context, id int) error {
	if _, err := ManualSubscriptionGet(id); err != nil {
		return err
	}
	if err := db.GetDB().WithContext(ctx).Delete(&model.ManualSubscription{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete manual subscription: %w", err)
	}
	return ManualSubscriptionRefresh(ctx)
}

// manualSubscriptionRows 把缓存里的记录折算成快照明细（口径见 model.ManualSubscriptionRow）。
func manualSubscriptionRows(globalPointsPerUnit float64, now time.Time) []model.ManualSubscriptionRow {
	items := ManualSubscriptionList()
	rows := make([]model.ManualSubscriptionRow, 0, len(items))
	for _, item := range items {
		unit := item.PointsPerUnit
		if unit <= 0 {
			unit = globalPointsPerUnit
		}
		days, expired := model.ManualSubscriptionDaysLeft(item.ExpireAt, now)
		row := model.ManualSubscriptionRow{
			ID:            item.ID,
			ChannelID:     item.ChannelID,
			Name:          item.Name,
			Note:          item.Note,
			Enabled:       item.Enabled,
			BalancePoints: item.BalancePoints,
			PointsPerUnit: unit,
			Balance:       model.ConvertBalancePoints(item.BalancePoints, unit),
			ExpireAt:      item.ExpireAt,
			DaysLeft:      days,
			Expired:       expired,
		}
		if item.ChannelID != 0 {
			if channel, err := ChannelGet(item.ChannelID); err == nil {
				row.ChannelName = channel.Name
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ChannelID != rows[j].ChannelID {
			return rows[i].ChannelID < rows[j].ChannelID
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}

// manualSubscriptionByChannel 返回"按渠道绑定且可用"的手动余额（启用、未过期、绑定了渠道）。
//
// 只收启用且未过期的：过期的套餐再显示成余额会让总额虚高，而"我明明续过费"这件事
// 由面板显示到期状态来提醒，不靠把它算进钱里。
func manualSubscriptionByChannel(rows []model.ManualSubscriptionRow) map[int]model.ManualSubscriptionRow {
	out := map[int]model.ManualSubscriptionRow{}
	for _, row := range rows {
		if row.ChannelID == 0 || !row.Enabled || row.Expired {
			continue
		}
		if _, exists := out[row.ChannelID]; !exists {
			out[row.ChannelID] = row
		}
	}
	return out
}

// ManualSubscriptionListForTest 仅供测试：直接读库（不让测试依赖缓存刷新时机）。
func ManualSubscriptionListForTest(ctx context.Context, conn *gorm.DB) ([]model.ManualSubscription, error) {
	items := []model.ManualSubscription{}
	if err := conn.WithContext(ctx).Order("id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}
