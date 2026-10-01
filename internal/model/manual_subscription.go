package model

import (
	"fmt"
	"strings"
	"time"
)

// ManualSubscription 是"上游没有余额接口时手工录一条"的套餐记录（R-acct-004 / T-acct-005）。
//
// 为什么单独一张表、不塞进渠道凭据：
//   - 它没有上游密钥，塞进 channel_keys 会让"凭据"这个概念同时承载"能连上游的秘密"和"我在哪买了套餐"，
//     备份/加密/轮换三件事的边界都会糊掉；
//   - 生命周期不同：凭据会轮换，套餐会到期续费、会临时调整余额，二者的变更节奏与审计诉求都不一样；
//   - 目标是把"无接口的站点"也纳入总余额（T-balance-001），那只需要一个能吃进同一口径的余额来源。
type ManualSubscription struct {
	ID        int `json:"id" gorm:"primaryKey"`
	ChannelID int `json:"channel_id"` // 绑定的渠道；0 = 不绑定具体渠道（通用套餐/年付包）
	// Name 是套餐名，面板里用来认账（如 "Claude Pro 月付"），故不做唯一约束、允许同名多期。
	Name          string  `json:"name" gorm:"not null"`
	Note          string  `json:"note,omitempty"`
	BalancePoints float64 `json:"balance_points,omitempty"`
	// PointsPerUnit 是这条记录自己的换算口径；0 = 用全局设置（不同站点基数不同，允许覆盖）。
	PointsPerUnit float64 `json:"points_per_unit,omitempty"`
	// ExpireAt 是有效期（Unix 秒）；0 = 无期限。到期后这条记录不再计入总额（见 op.BalanceSummaryGet 的口径）。
	ExpireAt  int64 `json:"expire_at,omitempty"`
	Enabled   bool  `json:"enabled"`
	CreatedAt int64 `json:"created_at,omitempty"`
	UpdatedAt int64 `json:"updated_at,omitempty"`
}

// ManualSubscriptionRow 是余额快照里的一条手动订阅明细（含折算结果与到期状态）。
type ManualSubscriptionRow struct {
	ID            int     `json:"id"`
	ChannelID     int     `json:"channel_id"`
	ChannelName   string  `json:"channel_name,omitempty"`
	Name          string  `json:"name"`
	Note          string  `json:"note,omitempty"`
	Enabled       bool    `json:"enabled"`
	BalancePoints float64 `json:"balance_points"`
	PointsPerUnit float64 `json:"points_per_unit"` // 生效口径（行级覆盖或全局）
	Balance       float64 `json:"balance"`         // 折算后的货币值
	ExpireAt      int64   `json:"expire_at,omitempty"`
	DaysLeft      int     `json:"days_left"` // 剩余天数；无期限为 -1，已过期为 0
	Expired       bool    `json:"expired"`
	// Counted 表示这条记录是否真的进了 Total：停用、已过期、以及「该渠道已有自动读数」的都不计。
	// 面板据此显示「未计入」，而不是让用户以为总额漏算了。
	Counted bool `json:"counted"`
}

// NormalizeManualSubscription 归一化与校验：返回可落库的副本（名称去空白、时间戳补齐）。
func NormalizeManualSubscription(input ManualSubscription) (ManualSubscription, error) {
	item := input
	item.Name = strings.TrimSpace(item.Name)
	if item.Name == "" {
		return item, fmt.Errorf("套餐名称不能为空")
	}
	if len([]rune(item.Name)) > 64 {
		return item, fmt.Errorf("套餐名称过长（最多 64 字）")
	}
	item.Note = strings.TrimSpace(item.Note)
	if item.BalancePoints < 0 {
		return item, fmt.Errorf("剩余额度不能为负")
	}
	if item.PointsPerUnit < 0 {
		return item, fmt.Errorf("换算口径不能为负（0 表示使用全局口径）")
	}
	if item.ChannelID < 0 {
		return item, fmt.Errorf("渠道不能为负")
	}
	if item.ExpireAt < 0 {
		return item, fmt.Errorf("有效期不能为负")
	}
	if item.ID < 0 {
		return item, fmt.Errorf("id 非法")
	}
	return item, nil
}

// ManualSubscriptionDaysLeft 算剩余天数：无期限 -1，已过期 0，不足一天按 1 天。
func ManualSubscriptionDaysLeft(expireAt int64, now time.Time) (int, bool) {
	if expireAt <= 0 {
		return -1, false
	}
	delta := time.Unix(expireAt, 0).Sub(now)
	if delta <= 0 {
		return 0, true
	}
	days := int(delta / (24 * time.Hour))
	if delta%(24*time.Hour) != 0 {
		days++
	}
	return days, false
}
