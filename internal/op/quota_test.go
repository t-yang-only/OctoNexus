package op

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var quotaZeroTestDBSeq int64

// openQuotaZeroTestDB 打开一块直连内存 SQLite，建出停用/审计依赖的全部表。
// DSN 按测试名+计数器隔离，避免 -count=N 重跑时撞 channels.name 唯一键（NM-CUR-042 复现）。
func openQuotaZeroTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&quotaZeroTestDBSeq, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.Channel{},
		&model.ChannelKey{},
		&model.ChannelModel{},
		&model.ChannelGrant{},
		&model.QuotaAction{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return conn
}

// seedQuotaZeroChannel 造一个最小可用渠道：2 凭据 × 1 模型，全组合授权。
// 返回渠道主键与凭据主键，供停用/恢复断言。
func seedQuotaZeroChannel(t *testing.T, conn *gorm.DB, channelName string) (int, []int) {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: channelName}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	keyIDs := make([]int, 0, 2)
	for _, name := range []string{"k1", "k2"} {
		key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: name, Key: "sk-test-" + name, Enabled: true}}
		if err := conn.Create(&key).Error; err != nil {
			t.Fatalf("create key %q: %v", name, err)
		}
		keyIDs = append(keyIDs, key.ID)
	}
	m := model.ChannelModel{ChannelID: channel.ID, Name: "m1"}
	if err := conn.Create(&m).Error; err != nil {
		t.Fatalf("create model: %v", err)
	}
	for _, kid := range keyIDs {
		grant := model.ChannelGrant{ChannelModelID: m.ID, ChannelKeyID: kid, Protocols: model.ProtocolOpenAIChatCompletion}
		if err := conn.Create(&grant).Error; err != nil {
			t.Fatalf("create grant: %v", err)
		}
	}
	return channel.ID, keyIDs
}

// quotaKeysEnabled 返回指定凭据的启用状态映射，供停用/恢复断言。
func quotaKeysEnabled(t *testing.T, conn *gorm.DB, keyIDs []int) map[int]bool {
	t.Helper()
	got := make(map[int]bool, len(keyIDs))
	for _, kid := range keyIDs {
		var key model.ChannelKey
		if err := conn.First(&key, kid).Error; err != nil {
			t.Fatalf("load key %d: %v", kid, err)
		}
		got[kid] = key.Enabled
	}
	return got
}

// TestQuotaZeroStopDisablesKeys 覆盖停用主路径：归零停用全部启用凭据，重复触发幂等。
func TestQuotaZeroStopDisablesKeys(t *testing.T) {
	conn := openQuotaZeroTestDB(t)
	channelID, keyIDs := seedQuotaZeroChannel(t, conn, "quota-chan")

	stopped, err := quotaZeroStopOn(conn, channelID, 0)
	if err != nil {
		t.Fatalf("stop at zero: %v", err)
	}
	if len(stopped) != 2 {
		t.Fatalf("stopped = %v, want both keys", stopped)
	}
	for kid, enabled := range quotaKeysEnabled(t, conn, keyIDs) {
		if enabled {
			t.Fatalf("key %d still enabled after zero stop", kid)
		}
	}

	// 重复触发幂等：无启用凭据时返回空且不报错。
	stopped, err = quotaZeroStopOn(conn, channelID, -5)
	if err != nil {
		t.Fatalf("repeat stop: %v", err)
	}
	if len(stopped) != 0 {
		t.Fatalf("repeat stopped = %v, want empty", stopped)
	}

	// 未归零拒绝：剩余额度为正时不执行停用。
	if _, err := quotaZeroStopOn(conn, channelID, 10); err == nil {
		t.Fatalf("positive remaining: want error")
	}
	for kid, enabled := range quotaKeysEnabled(t, conn, keyIDs) {
		if enabled {
			t.Fatalf("key %d unexpectedly re-enabled", kid)
		}
	}

	// 渠道不存在拒绝。
	if _, err := quotaZeroStopOn(conn, 999999, 0); err == nil {
		t.Fatalf("missing channel: want error")
	}
}

// TestQuotaManualRestoreAndAudit 覆盖手动恢复与审计落库：恢复单凭据并记两类审计。
func TestQuotaManualRestoreAndAudit(t *testing.T) {
	conn := openQuotaZeroTestDB(t)
	channelID, keyIDs := seedQuotaZeroChannel(t, conn, "restore-chan")

	if _, err := quotaZeroStopOn(conn, channelID, 0); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := quotaManualRestoreOn(conn, keyIDs[0], "tester"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	states := quotaKeysEnabled(t, conn, keyIDs)
	if !states[keyIDs[0]] {
		t.Fatalf("key %d not restored", keyIDs[0])
	}
	if states[keyIDs[1]] {
		t.Fatalf("key %d should stay disabled", keyIDs[1])
	}

	// 恢复已启用凭据：no-op 但仍记审计。
	if err := quotaManualRestoreOn(conn, keyIDs[0], "tester"); err != nil {
		t.Fatalf("restore enabled: %v", err)
	}

	// 恢复不存在的凭据拒绝。
	if err := quotaManualRestoreOn(conn, 999999, "tester"); err == nil {
		t.Fatalf("missing key: want error")
	}

	// 审计：1 停用 + 2 恢复，按渠道可查，倒序排列。
	actions, total := quotaActionsOn(conn, channelID, 50, 0)
	if total != 3 {
		t.Fatalf("audit total = %d, want 3", total)
	}
	if actions[0].Action != model.QuotaActionManualRestore {
		t.Fatalf("latest action = %q, want manual_restore", actions[0].Action)
	}
	if actions[0].Actor != "tester" {
		t.Fatalf("actor = %q, want tester", actions[0].Actor)
	}
	foundStop := false
	for _, a := range actions {
		if a.Action == model.QuotaActionAutoStop && a.Actor == "system:auto" {
			foundStop = true
		}
	}
	if !foundStop {
		t.Fatalf("auto_stop audit missing in %+v", actions)
	}

	// 分页：limit 截断但 total 仍是全量；超限收敛到页上限。
	got, total := quotaActionsOn(conn, channelID, 2, 0)
	if len(got) != 2 || total != 3 {
		t.Fatalf("page = %d/%d, want 2/3", len(got), total)
	}
	got, _ = quotaActionsOn(conn, channelID, 100000, 0)
	if len(got) != 3 {
		t.Fatalf("over-limit len = %d, want 3", len(got))
	}
	_ = context.Background
}
