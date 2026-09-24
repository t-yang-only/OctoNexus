package op

import (
	"context"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// relayLogWindowDefault / relayLogWindowMax 是画像取数的默认与上限条数。
//
// 与历史日志页的 RelayLogPageMaxLimit 不是一回事：那个是"给人一页看多少"，
// 这个是"统计吃多少样本"，两者各自演进（页大小会跟着界面调整，样本量跟着统计口径调整）。
const (
	relayLogWindowDefault = 500
	relayLogWindowMax     = 20000
)

// RelayLogSampleInfo 说明一批画像样本是怎么来的（T-trace-006）。
//
// 存在的理由：画像默认**排除**测试请求，而排除一旦不可见就会让人怀疑数字 ——
// 界面上写着"窗口 500"、日志页明明有 503 行，看的人只会觉得哪一边错了，
// 而不会想到"那三条是我自己发的验证请求"。把扣除的条数摆出来，两边立刻对得上。
type RelayLogSampleInfo struct {
	// Window 是窗口内的原始日志条数（**含**被排除的测试请求）。
	//
	// 保持"原始条数"而不是"样本条数"，是为了让窗口边界与日志页完全一致：
	// 若改成剔除后再算，那么排除一条测试请求就会让窗口多吞一条更早的日志，
	// 两个面板的样本范围悄悄错开，而数字看起来都是合理的。
	Window int64 `json:"window"`
	// Samples 是实际进入统计的条数（= Window - TestSkipped）。
	Samples int64 `json:"samples"`
	// TestSkipped 是窗口内声明为测试请求而被排除的条数。
	TestSkipped int64 `json:"test_skipped"`
	// Truncated 表示窗口取满仍有更早的行未取到。
	Truncated bool `json:"truncated"`
}

// relayLogWindow 取最近 window 条请求日志作为画像样本，并剔除测试请求。
//
// 所有基于 relay_logs 的画像都必须经它取数。收口的理由不是省几行代码，而是**口径只有一个**：
// 从前每个画像各写一遍 Order("id DESC").Limit(window)，于是"哪些日志算样本"这件事散在七处，
// 任何一次口径调整（例如本轮新增的测试请求剔除）都得靠人记得改全七处 —— 而漏掉的那一处
// 不会报错，只会静默给出与其它面板不一样的答案。
//
// 剔除策略：**在 Go 侧剔除而不是用 SQL WHERE**。看着多此一举，但 WHERE 会改变窗口边界 ——
// 请求 window=500 时会一路往下取到 500 条非测试日志，等于"排除测试请求"顺带把窗口拉长了，
// 于是排除前后两次统计的样本范围根本不可比（而那正是这个功能想解决的问题）。
// 取满再剔除，窗口就还是那 500 条，只是其中几条不进统计。
//
// 测试请求**只进日志页**，画像一律看不到它：想看测试请求本身应该去日志页（那里有完整记录
// 与三态过滤），而不是让它混进"我的网关健康吗"这个问题的答案里。
func relayLogWindow(ctx context.Context, window int) ([]model.RelayLog, RelayLogSampleInfo, error) {
	if window <= 0 {
		window = relayLogWindowDefault
	}
	if window > relayLogWindowMax {
		window = relayLogWindowMax
	}

	var all []model.RelayLog
	if err := db.GetDB().WithContext(ctx).
		Order("id DESC").Limit(window).
		Find(&all).Error; err != nil {
		return nil, RelayLogSampleInfo{}, err
	}

	rows := make([]model.RelayLog, 0, len(all))
	var skipped int64
	for _, row := range all {
		if row.IsTest {
			skipped++
			continue
		}
		rows = append(rows, row)
	}

	info := RelayLogSampleInfo{
		Window:      int64(len(all)),
		Samples:     int64(len(rows)),
		TestSkipped: skipped,
		Truncated:   len(all) >= window,
	}
	return rows, info, nil
}
