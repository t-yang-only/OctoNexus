package op

import (
	"sort"
	"strconv"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// T-quota-001 接线·映射层（163 清单 ②，NM-CUR-217/L3）。
// op 不能 import health（health→rhttp→op 成环），故本层只产出"扫谁、用什么凭证"的映射，
// 消费循环在 task 包（quota_scan.go），采集原语在 health 包（FetchBalance/ScanOneChannel），三层各自可测。
//
// 监控凭证口径（P2）：监控凭证与转发 key 分离是目标态；当前渠道模型无专用监控凭证字段，
// 过渡口径取该渠道 ID 最小的一枚启用凭据 Key。渠道一旦具备独立监控凭证字段，只改 firstEnabledKeyToken 一处。
// 阈值口径：quota_alert_threshold 只管"要不要记告警"；归零停用恒按 remaining<=0 判定（model.QuotaZeroThreshold），
// 与告警阈值解耦——未配置阈值也必须能停归零渠道（R-quota-002 主诉求）。

// QuotaScanTarget 是一次余额扫描的目标映射结果。
type QuotaScanTarget struct {
	ChannelID    int
	ChannelName  string
	BaseURL      string
	MonitorToken string
	UseProxy     bool
}

// QuotaScanTargets 把全部启用且可采集的渠道映射为扫描目标（163 清单 ②）。
// 跳过条件：渠道停用、无 BaseURL（new-api 系端点无从拼接）、无任何启用凭据（无凭证可用）。
// 返回按渠道 ID 升序，保证多轮扫描顺序稳定、日志可比对。
func QuotaScanTargets() []QuotaScanTarget {
	targets := make([]QuotaScanTarget, 0)
	for _, channel := range channelCache.GetAll() {
		if !channel.Enabled || channel.BaseURL == "" {
			continue
		}
		token, found := firstEnabledKeyToken(channel.ID)
		if !found {
			continue
		}
		targets = append(targets, QuotaScanTarget{
			ChannelID:    channel.ID,
			ChannelName:  channel.Name,
			BaseURL:      channel.BaseURL,
			MonitorToken: token,
			UseProxy:     channel.Proxy,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ChannelID < targets[j].ChannelID })
	return targets
}

// firstEnabledKeyToken 返回渠道 ID 最小的一枚启用凭据的 Key（监控凭证过渡口径）。
// Key 属敏感信息：调用方只可放进请求头，禁止写日志。
func firstEnabledKeyToken(channelID int) (string, bool) {
	var best *model.ChannelKey
	for _, key := range channelKeyCache.GetAll() {
		if key.ChannelID != channelID || !key.Enabled {
			continue
		}
		if best == nil || key.ID < best.ID {
			selected := key
			best = &selected
		}
	}
	if best == nil {
		return "", false
	}
	return best.Key, true
}

// QuotaScanInterval 读取扫描周期；设置缺失/非法回退 5 分钟（P5 低频），0 表示停用任务。
func QuotaScanInterval() time.Duration {
	minutes, err := SettingGetInt(model.SettingKeyQuotaScanInterval)
	if err != nil {
		minutes = 5
	}
	if minutes < 0 {
		minutes = 0
	}
	return time.Duration(minutes) * time.Minute
}

// QuotaAlertThreshold 读取告警阈值；缺失/空/非法回退 0（不告警）。
func QuotaAlertThreshold() float64 {
	value, err := SettingGetString(model.SettingKeyQuotaAlertThreshold)
	if err != nil || value == "" {
		return 0
	}
	threshold, err := strconv.ParseFloat(value, 64)
	if err != nil || threshold < 0 {
		return 0
	}
	return threshold
}
