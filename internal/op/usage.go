package op

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 分模型×小时用量明细（NM-CUR-025 裁决实现）:
// 转发终态一行累加进内存桶, 随统计保存周期批量落库（无独立调度器）,
// 落库时顺带删除 180 天前的旧桶; 查询按时间窗从库内聚合返回。

type usageKey struct {
	hour   string
	model  string
	number string // channel name
}

var usageBuckets = make(map[usageKey]*model.UsageHourly)
var usageBucketsLock sync.Mutex

// LogUsageHourly 把一次已结束请求的指标累加进当前小时桶。
// model 或渠道名为空（未选出目标即结束的请求）时跳过: 无归属维度的失败不进模型明细。
func LogUsageHourly(modelName, channelName string, metrics model.StatsMetrics) {
	if modelName == "" || channelName == "" {
		return
	}
	key := usageKey{hour: model.UsageHourKey(time.Now()), model: modelName, number: channelName}
	usageBucketsLock.Lock()
	defer usageBucketsLock.Unlock()
	bucket, ok := usageBuckets[key]
	if !ok {
		bucket = &model.UsageHourly{Hour: key.hour, ModelName: key.model, ChannelName: key.number}
		usageBuckets[key] = bucket
	}
	bucket.StatsMetrics.Add(metrics)
}

// UsageSaveDB 把内存桶批量落库并清理保留期外的旧数据。
// 与统计保存任务同周期调用, 失败只记日志不回滚内存（下轮全量 upsert 自愈）。
//
// 内存桶在落库后即被取走, 因此它只装"上次落库之后"的增量, 而库内同键行是更早窗口的累计值:
// 两者必须先相加再写回, 直接用 UpdateAll 覆盖会把同一小时里更早窗口的用量抹掉。
func UsageSaveDB(ctx context.Context) {
	usageBucketsLock.Lock()
	rows := make([]model.UsageHourly, 0, len(usageBuckets))
	for key, bucket := range usageBuckets {
		row := *bucket
		rows = append(rows, row)
		delete(usageBuckets, key)
	}
	usageBucketsLock.Unlock()

	if len(rows) > 0 {
		merged, err := mergeUsageRows(ctx, rows)
		if err != nil {
			restoreUsageBuckets(rows)
			log.Errorf("usage merge db error: %v", err)
			return
		}
		if err := db.GetDB().WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "hour"}, {Name: "model_name"}, {Name: "channel_name"}},
			UpdateAll: true,
		}).Create(&merged).Error; err != nil {
			// 落库失败把桶放回内存, 下轮重试。
			restoreUsageBuckets(rows)
			log.Errorf("usage save db error: %v", err)
			return
		}
	}

	cutoff := time.Now().AddDate(0, 0, -model.UsageHourlyRetentionDays).Format("2006010215")
	if err := db.GetDB().WithContext(ctx).Where("hour < ?", cutoff).Delete(&model.UsageHourly{}).Error; err != nil {
		log.Errorf("usage retention cleanup error: %v", err)
	}
}

// mergeUsageRows 把待落库的增量并进库内同键行, 返回可直接 upsert 的完整值; 库内没有同键行时原样保留。
func mergeUsageRows(ctx context.Context, rows []model.UsageHourly) ([]model.UsageHourly, error) {
	for i := range rows {
		var stored model.UsageHourly
		err := db.GetDB().WithContext(ctx).
			Where("hour = ? AND model_name = ? AND channel_name = ?", rows[i].Hour, rows[i].ModelName, rows[i].ChannelName).
			First(&stored).Error
		if err == nil {
			stored.StatsMetrics.Add(rows[i].StatsMetrics)
			rows[i].StatsMetrics = stored.StatsMetrics
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	return rows, nil
}

// restoreUsageBuckets 把已从内存取走的行放回桶, 供落库失败后下一轮重试。
func restoreUsageBuckets(rows []model.UsageHourly) {
	usageBucketsLock.Lock()
	defer usageBucketsLock.Unlock()
	for _, row := range rows {
		key := usageKey{hour: row.Hour, model: row.ModelName, number: row.ChannelName}
		bucket, ok := usageBuckets[key]
		if !ok {
			bucket = &model.UsageHourly{Hour: row.Hour, ModelName: row.ModelName, ChannelName: row.ChannelName}
			usageBuckets[key] = bucket
		}
		bucket.StatsMetrics.Add(row.StatsMetrics)
	}
}

// UsageRow 是时间窗内按整点×模型聚合的一行。
type UsageRow struct {
	Hour        string `json:"hour"`
	ModelName   string `json:"model_name"`
	ChannelName string `json:"channel_name"`
	model.StatsMetrics
}

// UsageQuery 聚合时间窗内的库内桶与未落库内存桶, 按 hour 升序、model、channel 排序返回。
func UsageQuery(ctx context.Context, r model.UsageRange) []UsageRow {
	cutoff := model.UsageRangeCutoff(r, time.Now()).Format("2006010215")
	rows := make([]UsageRow, 0)
	// 表名必须由实体类型给出: UsageRow 只作扫描目标, 单独 Find 会被 GORM 推出不存在的
	// usage_rows 表, 查询报错又被吞掉, 接口于是恒返回空明细。
	if err := db.GetDB().WithContext(ctx).
		Model(&model.UsageHourly{}).
		Where("hour >= ?", cutoff).
		Order("hour, model_name, channel_name").
		Find(&rows).Error; err != nil {
		log.Errorf("usage query error: %v", err)
		return rows
	}

	// 内存桶与库内行同键时并入, 不同键追加。
	// 只用下标定位库内行并在其上累加: 待追加行先收在 pending, 既不受 append 扩容影响,
	// 也避免内存桶被先追加一次、再被并入一次 (那样数值会翻倍并出现重复行)。
	index := make(map[usageKey]int, len(rows))
	for i := range rows {
		index[usageKey{hour: rows[i].Hour, model: rows[i].ModelName, number: rows[i].ChannelName}] = i
	}
	pending := make([]UsageRow, 0, len(usageBuckets))
	usageBucketsLock.Lock()
	for key, bucket := range usageBuckets {
		if key.hour < cutoff {
			continue
		}
		if i, ok := index[key]; ok {
			rows[i].StatsMetrics.Add(bucket.StatsMetrics)
			continue
		}
		pending = append(pending, UsageRow{
			Hour:         key.hour,
			ModelName:    key.model,
			ChannelName:  key.number,
			StatsMetrics: bucket.StatsMetrics,
		})
	}
	usageBucketsLock.Unlock()
	rows = append(rows, pending...)

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Hour != rows[j].Hour {
			return rows[i].Hour < rows[j].Hour
		}
		if rows[i].ModelName != rows[j].ModelName {
			return rows[i].ModelName < rows[j].ModelName
		}
		return rows[i].ChannelName < rows[j].ChannelName
	})
	return rows
}
