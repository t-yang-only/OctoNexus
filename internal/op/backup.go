package op

import (
	"context"
	"fmt"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 渠道拆分为渠道, 凭据, 模型与渠道授权后导出结构变化, 版本随之递增;
// 版本 5 起 api_keys.supported_models 由逗号分隔字符串改为 JSON 数组。
const dbDumpVersion = 5

// DBExportAll 导出完整数据库内容，包括所有统计数据。
func DBExportAll(ctx context.Context) (*model.DBDump, error) {
	conn := db.GetDB().WithContext(ctx)

	d := &model.DBDump{
		Version:    dbDumpVersion,
		ExportedAt: time.Now().UTC(),
	}

	if err := conn.Find(&d.Channels).Error; err != nil {
		return nil, fmt.Errorf("export channels: %w", err)
	}
	if err := conn.Find(&d.Groups).Error; err != nil {
		return nil, fmt.Errorf("export groups: %w", err)
	}
	if err := conn.Find(&d.ChannelKeys).Error; err != nil {
		return nil, fmt.Errorf("export channel_keys: %w", err)
	}
	if err := conn.Find(&d.ChannelModels).Error; err != nil {
		return nil, fmt.Errorf("export channel_models: %w", err)
	}
	if err := conn.Find(&d.ChannelGrants).Error; err != nil {
		return nil, fmt.Errorf("export channel_grants: %w", err)
	}
	if err := conn.Find(&d.GroupItems).Error; err != nil {
		return nil, fmt.Errorf("export group_items: %w", err)
	}
	if err := conn.Find(&d.LLMInfos).Error; err != nil {
		return nil, fmt.Errorf("export llm_infos: %w", err)
	}
	if err := conn.Find(&d.APIKeys).Error; err != nil {
		return nil, fmt.Errorf("export api_keys: %w", err)
	}
	if err := conn.Find(&d.Settings).Error; err != nil {
		return nil, fmt.Errorf("export settings: %w", err)
	}

	if err := conn.Find(&d.StatsTotal).Error; err != nil {
		return nil, fmt.Errorf("export stats_total: %w", err)
	}
	if err := conn.Find(&d.StatsDaily).Error; err != nil {
		return nil, fmt.Errorf("export stats_daily: %w", err)
	}
	if err := conn.Find(&d.StatsHourly).Error; err != nil {
		return nil, fmt.Errorf("export stats_hourly: %w", err)
	}
	if err := conn.Find(&d.StatsAPIKey).Error; err != nil {
		return nil, fmt.Errorf("export stats_api_key: %w", err)
	}

	return d, nil
}

func DBImportIncremental(ctx context.Context, dump *model.DBDump) (*model.DBImportResult, error) {
	if dump == nil {
		return nil, fmt.Errorf("empty dump")
	}

	if dump.Version != 0 && dump.Version != dbDumpVersion {
		return nil, fmt.Errorf("unsupported dump version: %d", dump.Version)
	}

	// 导入也是写入边界, 渠道配置须与接口提交走同一套规范化: 手改过的备份文件同样可能带空白,
	// 空串或缺省字段, 不在此收敛则读侧要为每个字段各自兜底。
	for i := range dump.Channels {
		config, err := normalizeChannelConfig(dump.Channels[i].ChannelConfig)
		if err != nil {
			return nil, fmt.Errorf("import channel %d: %w", dump.Channels[i].ID, err)
		}
		dump.Channels[i].ChannelConfig = config
	}

	conn := db.GetDB().WithContext(ctx)
	res := &model.DBImportResult{RowsAffected: map[string]int64{}}
	err := conn.Transaction(func(tx *gorm.DB) error {
		// base tables
		if n, err := createRowsRaw(tx, dump.Channels, nil, true); err != nil {
			return fmt.Errorf("import channels: %w", err)
		} else {
			res.RowsAffected["channels"] = n
		}
		// 渠道按主键冲突跳过, 统计需单独覆盖; 凭据与模型走整行覆盖, 统计随行一并导入。
		for _, channel := range dump.Channels {
			if err := tx.Model(&model.Channel{}).
				Where("id = ?", channel.ID).
				Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
				Updates(&channel).Error; err != nil {
				return fmt.Errorf("import channel stats: %w", err)
			}
		}
		if n, err := createRowsRaw(tx, dump.Groups, nil, true); err != nil {
			return fmt.Errorf("import groups: %w", err)
		} else {
			res.RowsAffected["groups"] = n
		}
		if n, err := createRowsRaw(tx, dump.ChannelKeys, []clause.Column{{Name: "id"}}, false); err != nil {
			return fmt.Errorf("import channel_keys: %w", err)
		} else {
			res.RowsAffected["channel_keys"] = n
		}
		if n, err := createRowsRaw(tx, dump.ChannelModels, []clause.Column{{Name: "id"}}, false); err != nil {
			return fmt.Errorf("import channel_models: %w", err)
		} else {
			res.RowsAffected["channel_models"] = n
		}
		if n, err := createRowsRaw(tx, dump.ChannelGrants, []clause.Column{{Name: "id"}}, false); err != nil {
			return fmt.Errorf("import channel_grants: %w", err)
		} else {
			res.RowsAffected["channel_grants"] = n
		}
		if n, err := createRowsRaw(tx, dump.GroupItems, nil, true); err != nil {
			return fmt.Errorf("import group_items: %w", err)
		} else {
			res.RowsAffected["group_items"] = n
		}
		if n, err := createRowsRaw(tx, dump.LLMInfos, []clause.Column{{Name: "name"}}, false); err != nil {
			return fmt.Errorf("import llm_infos: %w", err)
		} else {
			res.RowsAffected["llm_infos"] = n
		}
		if n, err := createRowsRaw(tx, dump.APIKeys, nil, true); err != nil {
			return fmt.Errorf("import api_keys: %w", err)
		} else {
			res.RowsAffected["api_keys"] = n
		}
		if n, err := createUpsertSettings(tx, dump.Settings); err != nil {
			return fmt.Errorf("import settings: %w", err)
		} else {
			res.RowsAffected["settings"] = n
		}

		if n, err := createUpsertAll(tx, dump.StatsTotal, []clause.Column{{Name: "id"}}); err != nil {
			return fmt.Errorf("import stats_total: %w", err)
		} else {
			res.RowsAffected["stats_total"] = n
		}
		if n, err := createUpsertAll(tx, dump.StatsDaily, []clause.Column{{Name: "date"}}); err != nil {
			return fmt.Errorf("import stats_daily: %w", err)
		} else {
			res.RowsAffected["stats_daily"] = n
		}
		if n, err := createUpsertAll(tx, dump.StatsHourly, []clause.Column{{Name: "hour"}}); err != nil {
			return fmt.Errorf("import stats_hourly: %w", err)
		} else {
			res.RowsAffected["stats_hourly"] = n
		}
		if n, err := createUpsertAll(tx, dump.StatsAPIKey, []clause.Column{{Name: "api_key_id"}}); err != nil {
			return fmt.Errorf("import stats_api_key: %w", err)
		} else {
			res.RowsAffected["stats_api_key"] = n
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// batchSize 控制每次 INSERT 的最大行数。
// 单行字段数较多（如 stats_hourly 含 9 个字段），若一次插入过多行会超过数据库绑定参数上限（SQLite/PostgreSQL 为 65535），按行数分批写入可规避该限制。
const batchSize = 2000

// createRowsRaw 以"原始行"语义整批写入转储数据: 绕开 GORM Create 的关联自动展开。
// 备份里的 ChannelGrant / GroupItem 等模型都声明了关联字段 (如 GroupItem.ChannelGrant 的级联外键声明),
// Create 会尝试按关联回填而非整行插入: 关联为空的对象被静默跳过 (RowsAffected 归零、表却是空表),
// 于是后续外键引用整链崩塌 (导入 group_items 时 FOREIGN KEY constraint failed)。
// 转储本就是带主键的平表快照, 按表名直插即可, 无需任何关联语义。
func createRowsRaw(tx *gorm.DB, rows any, columns []clause.Column, doNothing bool) (int64, error) {
	switch v := rows.(type) {
	case []model.Channel:
		return rawCreate(tx, v, "channels", columns, doNothing)
	case []model.Group:
		return rawCreate(tx, v, "groups", columns, doNothing)
	case []model.ChannelKey:
		return rawCreate(tx, v, "channel_keys", columns, doNothing)
	case []model.ChannelModel:
		return rawCreate(tx, v, "channel_models", columns, doNothing)
	case []model.ChannelGrant:
		return rawCreate(tx, v, "channel_grants", columns, doNothing)
	case []model.GroupItem:
		return rawCreate(tx, v, "group_items", columns, doNothing)
	case []model.LLMInfo:
		return rawCreate(tx, v, "llm_infos", columns, doNothing)
	case []model.APIKey:
		return rawCreate(tx, v, "api_keys", columns, doNothing)
	case []model.StatsTotal:
		return rawCreate(tx, v, "stats_total", columns, doNothing)
	case []model.StatsDaily:
		return rawCreate(tx, v, "stats_daily", columns, doNothing)
	case []model.StatsHourly:
		return rawCreate(tx, v, "stats_hourly", columns, doNothing)
	case []model.StatsAPIKey:
		return rawCreate(tx, v, "stats_api_key", columns, doNothing)
	default:
		return 0, fmt.Errorf("unsupported dump row type %T", rows)
	}
}

// rawCreate 把一批转储行按主键逐条直接插入物理表: 语义与 createDoNothing (冲突跳过) /
// createUpsertAll (整行覆盖) 一致, 但不触发关联自动保存。返回实际写入/更新行数。
func rawCreate[T any](tx *gorm.DB, rows []T, table string, columns []clause.Column, doNothing bool) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	var before, after int64
	if doNothing {
		// 逐条 Create 的 RowsAffected 在冲突跳过时为 0, 但与事务前计数差值口径一致即可。
		if err := tx.Table(table).Count(&before).Error; err != nil {
			return 0, err
		}
	}
	for _, row := range rows {
		q := tx.Omit(clause.Associations).Table(table)
		if doNothing {
			q = q.Clauses(clause.OnConflict{DoNothing: true})
		} else {
			q = q.Clauses(clause.OnConflict{Columns: columns, UpdateAll: true})
		}
		rowCopy := row
		if err := q.Create(&rowCopy).Error; err != nil {
			return 0, err
		}
	}
	if doNothing {
		if err := tx.Table(table).Count(&after).Error; err != nil {
			return 0, err
		}
		return after - before, nil
	}
	return int64(len(rows)), nil
}

func createDoNothing[T any](tx *gorm.DB, rows []T) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&rows, batchSize)
	return result.RowsAffected, result.Error
}

func createUpsertAll[T any](tx *gorm.DB, rows []T, columns []clause.Column) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   columns,
		UpdateAll: true,
	}).CreateInBatches(&rows, batchSize)
	return result.RowsAffected, result.Error
}

func createUpsertSettings(tx *gorm.DB, rows []model.Setting) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&rows)
	return result.RowsAffected, result.Error
}
