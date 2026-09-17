package migrate

import (
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 14,
		Up:      migrateGroupStreamIdleTimeout,
	})
}

// migrateGroupStreamIdleTimeout 给存量分组补上「流式无进展上限」(T-timeout-001 / T-group-002)。
//
// 背景: member_stream_idle_timeout_seconds 是后来加的字段, 存量分组的 relay_config JSON 里
// 没有这个键, 反序列化后为 0 —— 而 0 的语义正是「关闭该保护」。于是升级后老分组仍然会
// 挂在"上游吐了首帧就再也不出字"的请求上（线上实证: 300–760 秒才被客户端放弃）, 用户必须
// 逐个分组手动去改。用户明确要求把存量分组统一打开, 因此这里做一次性回填。
//
// 口径（回填 300 = 新分组的默认值, 见 model.DefaultGroupRelayConfig）:
//   - 只补这一个键: 其它字段（含用户改过的超时/尝试次数/权重）逐字保留, 不做"整体重置成默认"。
//   - 键缺失或值 <= 0 才补; 已经是正数的分组不动（尊重用户显式配过的值）。
//   - 显式配成 0 的分组会被一起补上 —— 这是刻意的: "0 = 关闭"是给运行期用的开关,
//     升级回填的目标就是"升级后没有分组还关着"; 想关掉的分组在面板上改回 0 即可（迁移只跑一次）。
//   - relay_config 解析不了的行原样跳过（那是另一个问题, 不该由本迁移顺手覆盖）。
//
// 只 UPDATE 行、不碰表结构: 与 009 的教训一致（After 阶段删表会误删 AutoMigrate 刚建的表）。
func migrateGroupStreamIdleTimeout(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("groups") {
		return nil
	}

	// RelayConfig 原样读成字符串: 迁移不依赖当前模型形状, 换版本/换字段都不影响它。
	type groupRelayJSON struct {
		ID          int
		RelayConfig string
	}
	rows := make([]groupRelayJSON, 0)
	if err := db.Table("groups").Select("id, relay_config").Order("id ASC").Find(&rows).Error; err != nil {
		return fmt.Errorf("failed to read groups: %w", err)
	}

	const backfill = 300
	for _, row := range rows {
		config := map[string]any{}
		text := strings.TrimSpace(row.RelayConfig)
		if text != "" {
			if err := json.Unmarshal([]byte(text), &config); err != nil {
				continue // 解析不了的配置留给人看, 不在这里静默重写。
			}
		}
		if current, ok := config["member_stream_idle_timeout_seconds"]; ok {
			// JSON 数字统一解成 float64; 正数说明用户配过, 保留。
			if value, isNumber := current.(float64); isNumber && value > 0 {
				continue
			}
		}
		config["member_stream_idle_timeout_seconds"] = backfill
		encoded, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to encode relay config for group %d: %w", row.ID, err)
		}
		if err := db.Table("groups").Where("id = ?", row.ID).Update("relay_config", string(encoded)).Error; err != nil {
			return fmt.Errorf("failed to backfill group %d: %w", row.ID, err)
		}
	}
	return nil
}
