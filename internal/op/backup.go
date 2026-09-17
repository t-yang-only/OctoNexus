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
	res := &model.DBImportResult{RowsAffected: map[string]int64{}, Skipped: map[string]int64{}}
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
		// 基础表就位后再过滤孤儿子行: 备份可能带着"父行已被删除"的残留（渠道删了、凭据/模型/授权/分组成员的
		// 行还在），直接插入会撞外键约束而让整份备份导入失败（上游 PR #344 的同类问题）。
		// 过滤只丢确定无法插入的行, 数量如实回报, 不静默。
		if skipped, err := sanitizeOrphanRows(tx, dump); err != nil {
			return err
		} else {
			for table, count := range skipped {
				res.Skipped[table] = count
			}
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

// sanitizeOrphanRows 过滤转储里引用已不存在父行的孤儿子行, 返回按表统计的丢弃数量。
//
// 为什么要做（对齐上游 PR #344）: 备份是"某时刻的快照", 而快照里的子行可能指向早已删除的父行 ——
// 渠道删除后残留的凭据/模型/授权行、授权删除后残留的分组成员行等。这些行本身插不进去
// （外键约束), 却会让**整份备份导入失败**, 于是一个孤儿行就能挡住用户恢复全部数据。
// 有效父键集合 = 本次转储里的父行 ∪ 库里已存在的父行（导入是增量语义, 父行可能早就在库里）。
//
// 只丢确定无法插入的行, 且数量如实回给调用方, 不做静默"修复"。
func sanitizeOrphanRows(tx *gorm.DB, dump *model.DBDump) (map[string]int64, error) {
	skipped := map[string]int64{}

	// 按依赖顺序逐层过滤: 子行的有效父键必须取自**过滤后**的上层集合,
	// 否则"父行自己就是孤儿"的子行会被误判为合法（先算出来的集合里还带着即将被丢掉的行）。
	channelIDs, err := validParentIDs(tx, &model.Channel{}, idsOf(dump.Channels, func(row model.Channel) int { return row.ID }))
	if err != nil {
		return nil, err
	}

	keys := dump.ChannelKeys[:0]
	for _, row := range dump.ChannelKeys {
		if channelIDs[row.ChannelID] {
			keys = append(keys, row)
			continue
		}
		skipped["channel_keys"]++
	}
	dump.ChannelKeys = keys

	models := dump.ChannelModels[:0]
	for _, row := range dump.ChannelModels {
		if channelIDs[row.ChannelID] {
			models = append(models, row)
			continue
		}
		skipped["channel_models"]++
	}
	dump.ChannelModels = models

	keyIDs, err := validParentIDs(tx, &model.ChannelKey{}, idsOf(dump.ChannelKeys, func(row model.ChannelKey) int { return row.ID }))
	if err != nil {
		return nil, err
	}
	modelIDs, err := validParentIDs(tx, &model.ChannelModel{}, idsOf(dump.ChannelModels, func(row model.ChannelModel) int { return row.ID }))
	if err != nil {
		return nil, err
	}

	grants := dump.ChannelGrants[:0]
	for _, row := range dump.ChannelGrants {
		if modelIDs[row.ChannelModelID] && keyIDs[row.ChannelKeyID] {
			grants = append(grants, row)
			continue
		}
		skipped["channel_grants"]++
	}
	dump.ChannelGrants = grants

	groupIDs, err := validParentIDs(tx, &model.Group{}, idsOf(dump.Groups, func(row model.Group) int { return row.ID }))
	if err != nil {
		return nil, err
	}
	grantIDs, err := validParentIDs(tx, &model.ChannelGrant{}, idsOf(dump.ChannelGrants, func(row model.ChannelGrant) int { return row.ID }))
	if err != nil {
		return nil, err
	}

	items := dump.GroupItems[:0]
	for _, row := range dump.GroupItems {
		if !groupIDs[row.GroupID] {
			skipped["group_items"]++
			continue
		}
		// 授权成员 (ChannelGrantID) 与子分组成员 (ChildGroupID) 互为互斥的两侧, 只校验非空那一侧。
		if row.ChannelGrantID != nil && !grantIDs[*row.ChannelGrantID] {
			skipped["group_items"]++
			continue
		}
		if row.ChildGroupID != nil && !groupIDs[*row.ChildGroupID] {
			skipped["group_items"]++
			continue
		}
		items = append(items, row)
	}
	dump.GroupItems = items

	apiKeyIDs, err := validParentIDs(tx, &model.APIKey{}, idsOf(dump.APIKeys, func(row model.APIKey) int { return row.ID }))
	if err != nil {
		return nil, err
	}
	apiKeyStats := dump.StatsAPIKey[:0]
	for _, row := range dump.StatsAPIKey {
		if apiKeyIDs[row.APIKeyID] {
			apiKeyStats = append(apiKeyStats, row)
			continue
		}
		skipped["stats_api_key"]++
	}
	dump.StatsAPIKey = apiKeyStats

	return skipped, nil
}

// idsOf 取出转储行里的主键集合（跳过 0: 未落库的行没有主键）。
func idsOf[T any](rows []T, id func(T) int) map[int]bool {
	out := make(map[int]bool, len(rows))
	for _, row := range rows {
		if value := id(row); value != 0 {
			out[value] = true
		}
	}
	return out
}

// validParentIDs 把"过滤后仍在转储里的主键"与"库里已有的主键"合并成有效父键集合。
// 调用前必须先完成上层的过滤, 否则集合里会带着即将被丢掉的行。
func validParentIDs(tx *gorm.DB, dest any, fromDump map[int]bool) (map[int]bool, error) {
	valid := make(map[int]bool, len(fromDump))
	for id := range fromDump {
		valid[id] = true
	}
	var existing []int
	if err := tx.Model(dest).Pluck("id", &existing).Error; err != nil {
		return nil, fmt.Errorf("query %T ids for orphan filtering: %w", dest, err)
	}
	for _, id := range existing {
		valid[id] = true
	}
	return valid, nil
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
