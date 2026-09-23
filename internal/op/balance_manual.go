package op

import (
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 余额的「读不到时怎么办」（R-balance-002）。
//
// 本轮实测：16 个渠道里只有 2 个（pipixia / 转转AI）暴露 OpenAI 计费口径，
// 其余是 new-api 的兄弟站或自建网关，余额接口路径各不相同（404/401/403 全有）。
// 于是把三件事补上，否则面板永远只说「未读到」而用户无从下手：
//   ① 记住**为什么**读不到（原因码 + 可读文案），面板按原因归类；
//   ② 允许人工录入余额（无接口站点也能进总余额，来源标 manual，与自动读数区分）；
//   ③ 余额接口路径可配置（自建额度接口的站点填一次即可自动读）。
//
// 自动读数永远优先于人工录入：人工只是"接口读不到时的兜底"，接口一旦读出数就不再用手工值。

// BalanceReasonNoRecord 是"没读到余额、但也没有任何读数记录"的原因码。
//
// 为什么必须有它：面板上摆的是「未读到 N 个」和一个按原因归类的清单。
// 若读不到原因的渠道既不给码、也不进归类，两个数字就会对不上 ——
// 实测生产就是「未读到 26」配「网络不可达 25」，而少掉的那个渠道
// 在明细里既没有原因、也无从找出是哪一个。
// 归类必须覆盖全部未读到的渠道：宁可用一个诚实的兜底码，也不要静默漏掉。
const BalanceReasonNoRecord = "no_record"

// BalanceReasonRow 是一条"为什么没读到余额"的记录。
type BalanceReasonRow struct {
	Key  string    `json:"reason"` // 机器可读原因码（unreachable/no_endpoint/unauthorized/unparsable/proxy_node/no_record）
	Text string    `json:"text"`   // 给人看的解释（面板直接显示）
	At   time.Time `json:"at"`
}

var balanceReasonStore = struct {
	mu      sync.RWMutex
	entries map[int]BalanceReasonRow
}{entries: map[int]BalanceReasonRow{}}

// ChannelBalanceReason 返回该渠道上一次余额读取失败的原因；空表示没扫过或上次成功。
func ChannelBalanceReason(channelID int) (key, text string) {
	balanceReasonStore.mu.RLock()
	defer balanceReasonStore.mu.RUnlock()
	entry, ok := balanceReasonStore.entries[channelID]
	if !ok {
		return "", ""
	}
	return entry.Key, entry.Text
}

// RecordChannelBalanceReason 记下"这次为什么没读到"；成功（key 为空）时清除，免得显示陈旧解释。
func RecordChannelBalanceReason(channelID int, key, text string) {
	balanceReasonStore.mu.Lock()
	defer balanceReasonStore.mu.Unlock()
	if key == "" {
		delete(balanceReasonStore.entries, channelID)
		return
	}
	if balanceReasonStore.entries == nil {
		balanceReasonStore.entries = map[int]BalanceReasonRow{}
	}
	balanceReasonStore.entries[channelID] = BalanceReasonRow{Key: key, Text: text, At: time.Now()}
}

// ChannelBalanceReasons 返回全部渠道的未读到原因，供汇总接口一次带走。
func ChannelBalanceReasons() map[int]BalanceReasonRow {
	balanceReasonStore.mu.RLock()
	defer balanceReasonStore.mu.RUnlock()
	out := make(map[int]BalanceReasonRow, len(balanceReasonStore.entries))
	for id, entry := range balanceReasonStore.entries {
		out[id] = entry
	}
	return out
}

// BalanceEndpointPath 读取"余额接口路径"设置；空/非法回落 new-api 系默认路径。
func BalanceEndpointPath() string {
	value, err := SettingGetString(model.SettingKeyBalanceUserSelfPath)
	if err != nil {
		return model.BalanceUserSelfPathDefault
	}
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") {
		return model.BalanceUserSelfPathDefault
	}
	return value
}

// ChannelManualBalanceSet 人工录入某渠道的剩余额度（点数口径，与自动读数同一口径）。
// points<=0 表示清除录入；note 是给用户看的备注（例如"官网后台显示"，或因何读不到）。
//
// 只写 channels 表上这三个字段，不动渠道配置——面板保存渠道的列名清单里没有这三列，
// 所以人工录入不会被一次普通保存抹掉。
func ChannelManualBalanceSet(channelID int, points float64, note string) error {
	if points < 0 {
		points = 0
	}
	updates := map[string]any{
		"balance_points": points,
		"balance_note":   strings.TrimSpace(note),
	}
	if points > 0 {
		updates["balance_at"] = time.Now().Format(time.RFC3339)
	} else {
		updates["balance_at"] = ""
	}
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", channelID).Updates(updates).Error; err != nil {
		return err
	}
	return ReloadChannel(channelID)
}

// ReloadChannel 重新读一条渠道进缓存（人工余额这类只改渠道行的操作收尾用）。
func ReloadChannel(channelID int) error {
	var channel model.Channel
	if err := db.GetDB().First(&channel, channelID).Error; err != nil {
		return err
	}
	channelCache.Set(channel.ID, channel)
	return nil
}
