package op

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// T-usability-009 分组使用情况。
//
// ## 解决什么问题
//
// 生产实测（2026-09-23）：418 个分组里只有 41 个被调用过 —— **94% 从未使用**。
// 而这 418 个分组**全部**是客户端可直接调用的模型名（没有任何分组嵌套），
// 也就是说用户在 AI 客户端的模型列表里会看到 418 个条目。
//
// 这里不替用户判断"哪些该删"（那取决于他打算怎么用），
// 只把事实摆出来：哪些分组在用、用得多不多、最后一次是什么时候。
// 用户看到"我建了 418 个但只用了 41 个"之后，自己就知道该怎么处理。
//
// ## 为什么不给"建议删除"的结论
//
// 未被调用**不等于**该删：备用分组、给子分组复用的分组、尚未启用的新分组
// 都可能合法地没有流量。本项目在 T-usability-006 上刚踩过这个坑
// （把「上游清单未列出」当成「不可用」，差点让用户删掉有效配置）——
// 结论性判断必须留给掌握上下文的人。
type GroupUsage struct {
	GroupID int    `json:"group_id"`
	Name    string `json:"name"`
	// Mode 是分组的选路模式（manual/allocate/smart）——
	// 比「是否启用」更能说明这个分组是干什么的。
	Mode      string `json:"mode"`
	ItemCount int    `json:"item_count"`
	// IsAuto 标记名字形态是「渠道名/模型名」—— 自动生成的分组。
	// 只作展示用：用户往往不记得哪些是自己建的、哪些是保存渠道时自动生成的。
	IsAuto bool `json:"is_auto"`
	// CallCount 是窗口内的调用次数（0 表示从未被调用过）。
	CallCount int64 `json:"call_count"`
	// LastCallAt 是最后一次调用的时间，零值表示从未调用。
	LastCallAt time.Time `json:"last_call_at"`
}

// GroupUsageSummary 是分组使用的整体情况。
type GroupUsageSummary struct {
	Total int `json:"total"`
	// Used / Unused 是窗口内有/无调用的分组数。
	Used   int `json:"used"`
	Unused int `json:"unused"`
	// Auto / Manual 是按名字形态分的数量。
	Auto   int `json:"auto"`
	Manual int `json:"manual"`
	// Window 是统计调用次数时扫过的日志条数。
	Window int64 `json:"window"`
	// Groups 按「调用次数倒序、名字升序」排列 —— 常用的在前，一眼看到主力。
	Groups []GroupUsage `json:"groups"`
}

// GroupUsageStats 统计分组的使用情况。
//
// 调用次数从 relay_logs 现算（按 group_id 聚合），不新增计数列：
// 分组调用统计是纯展示需求，为它加一列常驻计数会引入"计数与日志不一致"的新问题，
// 而日志本来就有保留期、统计窗口天然与之一致。
func GroupUsageStats(ctx context.Context, window int) (GroupUsageSummary, error) {
	if window <= 0 {
		window = 5000
	}
	if window > 50000 {
		window = 50000
	}
	conn := db.GetDB()

	var groups []model.Group
	if err := conn.WithContext(ctx).Order("id ASC").Find(&groups).Error; err != nil {
		return GroupUsageSummary{}, err
	}

	// 成员数一次性取回后按分组归类：逐个分组查会变成 N+1 次查询。
	var items []model.GroupItem
	if err := conn.WithContext(ctx).Find(&items).Error; err != nil {
		return GroupUsageSummary{}, err
	}
	itemCount := make(map[int]int, len(groups))
	for _, item := range items {
		itemCount[item.GroupID]++
	}

	// 调用次数与最后调用时间：只扫窗口内的日志。
	//
	// Last 用 string 接收而不是 time.Time：聚合函数 max() 在 sqlite 上返回的是
	// 原始文本（存储形态），直接扫进 time.Time 会报 "unsupported Scan"。
	// 文本形态本身就是 RFC3339，下面解析回时间。
	type agg struct {
		GroupID int
		Calls   int64
		Last    string
	}
	var rows []agg
	if err := conn.WithContext(ctx).
		Table("relay_logs").
		Select("group_id, count(*) AS calls, max(started_at) AS last").
		Where("group_id > 0").
		Group("group_id").
		Scan(&rows).Error; err != nil {
		return GroupUsageSummary{}, err
	}
	usage := make(map[int]agg, len(rows))
	for _, row := range rows {
		usage[row.GroupID] = row
	}

	var windowCount int64
	if err := conn.WithContext(ctx).Model(&model.RelayLog{}).Count(&windowCount).Error; err != nil {
		return GroupUsageSummary{}, err
	}

	summary := GroupUsageSummary{
		Total:  len(groups),
		Window: windowCount,
		Groups: make([]GroupUsage, 0, len(groups)),
	}
	for _, g := range groups {
		u := GroupUsage{
			GroupID:   g.ID,
			Name:      g.Name,
			Mode:      string(g.Mode),
			ItemCount: itemCount[g.ID],
			IsAuto:    isAutoGroupName(g.Name),
		}
		if a, ok := usage[g.ID]; ok {
			u.CallCount = a.Calls
			// 解析失败不算错：这只是展示用的时间戳，
			// 解析不出来时留零值（前端显示"从未"），不该让整个统计失败。
			if parsed, err := parseLogTime(a.Last); err == nil {
				u.LastCallAt = parsed
			}
		}
		if u.CallCount > 0 {
			summary.Used++
		} else {
			summary.Unused++
		}
		if u.IsAuto {
			summary.Auto++
		} else {
			summary.Manual++
		}
		summary.Groups = append(summary.Groups, u)
	}

	sort.SliceStable(summary.Groups, func(i, j int) bool {
		if summary.Groups[i].CallCount != summary.Groups[j].CallCount {
			return summary.Groups[i].CallCount > summary.Groups[j].CallCount
		}
		return summary.Groups[i].Name < summary.Groups[j].Name
	})
	return summary, nil
}

// parseLogTime 解析日志时间戳。
//
// 容错多种形态：sqlite 里存的是带时区的 RFC3339Nano，但聚合函数取出来的文本
// 可能被驱动规整过。逐个尝试常见布局，都失败就让调用方用零值 ——
// 这只是展示用的时间戳，不值得为它让整个统计报错。
func parseLogTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("empty")
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("unrecognized time format: " + raw)
}

// isAutoGroupName 判断分组名是不是「渠道名/模型名」这个自动生成形态。
//
// 只按形态判断（含斜杠且斜杠两侧都非空）—— 不去库里核对渠道是否真存在：
// 那个渠道可能已经被删了，而分组还在，此时它仍然是自动生成的。
// 这个字段只用于展示分组来源，不参与任何清理决策。
func isAutoGroupName(name string) bool {
	idx := -1
	for i := 0; i < len(name); i++ {
		if name[i] == '/' {
			idx = i
			break
		}
	}
	if idx <= 0 || idx == len(name)-1 {
		return false
	}
	return true
}
