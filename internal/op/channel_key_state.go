package op

import (
	"context"
	"errors"
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// 人工启停一条渠道凭据。
//
// 为什么需要这一层: 渠道保存走的是"整体替换", 号池同步走的是"按账号状态收敛凭据",
// 两条路径都在写 enabled, 于是谁都能把对方的决定覆盖掉 —— 实测号池同步对 active 账号
// 是无条件把 enabled 改回 true 的, 所以"人工停掉一条坏凭据"会在下一次同步时被静默撤销。
// 因此人工停用单独记一位 OperatorDisabled: 它表达的是操作者的决定, 不是账号的当前状态,
// 同步逻辑读到这一位就不再自动启用, 只有人工再启一次才解开。
//
// 返回值第一个是"是否真的命中这条凭据": 渠道存在但没有这条凭据时返回 (false, nil),
// 由调用方决定映射成 404 还是别的语义。
func SetChannelKeyEnabled(conn *gorm.DB, channelID int, keyName string, enabled bool) (bool, error) {
	if channelID <= 0 || keyName == "" {
		return false, fmt.Errorf("channel id and key name are required")
	}
	target := officialAccountDB(conn)

	var key model.ChannelKey
	err := target.Where("channel_id = ? AND name = ?", channelID, keyName).First(&key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lookup channel key %q: %w", keyName, err)
	}

	// 目标状态与现状一致就什么都不写: 反复调用不该产生写事务, 也不该刷新转发侧缓存。
	holds := !enabled
	if key.Enabled == enabled && key.OperatorDisabled == holds {
		return true, nil
	}
	if err := target.Model(&model.ChannelKey{}).Where("id = ?", key.ID).
		Updates(map[string]any{"enabled": enabled, "operator_disabled": holds}).Error; err != nil {
		return true, fmt.Errorf("update channel key %q: %w", keyName, err)
	}

	// 凭据改了就要让转发侧立刻看到(与渠道保存、号池同步同一处理): 授权没动, 故不刷分组缓存。
	if err := channelRefreshCache(context.Background()); err != nil {
		return true, err
	}
	return true, nil
}
