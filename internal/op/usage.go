package op

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/charmbracelet/log"
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
		if err := db.GetDB().WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "hour"}, {Name: "model_name"}, {Name: "channel_name"}},
			UpdateAll: true,
		}).Create(&rows).Error; err != nil {
			// 落库失败把桶放回内存, 下轮重试。
			usageBucketsLock.Lock()
			for _, row := range rows {
				key := usageKey{hour: row.Hour, model: row.ModelName, number: row.ChannelName}
				bucket, ok := usageBuckets[key]
				if !ok {
					bucket = &model.UsageHourly{Hour: row.Hour, ModelName: row.ModelName, ChannelName: row.ChannelName}
					usageBuckets[key] = bucket
				}
				bucket.StatsMetrics.Add(row.StatsMetrics)
			}
			usageBucketsLock.Unlock()
			log.Errorf("usage save db error: %v", err)
			return
		}
	}

	cutoff := time.Now().AddDate(0, 0, -model.UsageHourlyRetentionDays).Format("2006010215")
	if err := db.GetDB().WithContext(ctx).Where("hour < ?", cutoff).Delete(&model.UsageHourly{}).Error; err != nil {
		log.Errorf("usage retention cleanup error: %v", err)
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
	if err := db.GetDB().WithContext(ctx).
		Where("hour >= ?", cutoff).
		Order("hour, model_name, channel_name").
		Find(&rows).Error; err != nil {
		log.Errorf("usage query error: %v", err)
		return rows
	}

	usageBucketsLock.Lock()
	for key, bucket := range usageBuckets {
		if key.hour < cutoff {
			continue
		}
		rows = append(rows, UsageRow{
			Hour:         key.hour,
			ModelName:    key.model,
			ChannelName:  key.number,
			StatsMetrics: bucket.StatsMetrics,
		})
	}
	usageBucketsLock.Unlock()

	// 内存桶与库内同键时合并。
	merged := make(map[usageKey]*UsageRow, len(rows))
	for i := range rows {
		key := usageKey{hour: rows[i].Hour, model: rows[i].ModelName, number: rows[i].ChannelName}
		merged[key] = &rows[i]
	}
	usageBucketsLock.Lock()
	for key, bucket := range usageBuckets {
		if key.hour < cutoff {
			continue
		}
		if row, ok := merged[key]; ok {
			row.StatsMetrics.Add(bucket.StatsMetrics)
			continue
		}
		rows = append(rows, UsageRow{
			Hour:         key.hour,
			ModelName:    key.model,
			ChannelName:  key.number,
			StatsMetrics: bucket.StatsMetrics,
		})
	}
	usageBucketsLock.Unlock()

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
