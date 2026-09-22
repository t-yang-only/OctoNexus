package op

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 冷却持久化的测试基建与 model_mapping_test.go 同款：
// db.InitDB 是全局单例，用 t.TempDir() 每用例建库会替换全局 db 并泄漏连接，
// Windows 上表现为 TempDir RemoveAll cleanup 失败。故用包级 once + 固定路径。
var (
	routeCooldownOnce sync.Once
	routeCooldownErr  error
)

func ensureRouteCooldownDB(t *testing.T) {
	t.Helper()
	routeCooldownOnce.Do(func() {
		path := filepath.Join(os.TempDir(), "octopus-route-cooldown-test.db")
		_ = os.Remove(path)
		routeCooldownErr = db.InitDB("sqlite", path, false)
	})
	if routeCooldownErr != nil {
		t.Fatalf("初始化测试库失败: %v", routeCooldownErr)
	}
	if err := db.GetDB().AutoMigrate(&model.RouteCooldown{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	// 用例间隔离：每例从空表开始。
	if err := db.GetDB().Where("1 = 1").Delete(&model.RouteCooldown{}).Error; err != nil {
		t.Fatalf("清理测试数据失败: %v", err)
	}
}

// 保存后能读回同一条，未到期时必须被返回。
func TestRouteCooldownSaveAndLoad(t *testing.T) {
	ensureRouteCooldownDB(t)
	deadline := time.Now().Add(5 * time.Minute).UnixMilli()
	RouteCooldownSave(101, 11, deadline)

	got, err := RouteCooldownLoad(context.Background())
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if got[101][11] != deadline {
		t.Fatalf("读回 deadline = %v, want %v", got[101][11], deadline)
	}
}

// **核心不变量**：已到期的冷却不该被恢复。
//
// 反例：把到期项也返回，重启会把一个早就该解封的成员继续封着 ——
// 用户看到的是"某个渠道明明恢复了却一直不被使用"，且没有任何日志线索。
func TestRouteCooldownLoadDropsExpired(t *testing.T) {
	ensureRouteCooldownDB(t)
	expired := time.Now().Add(-time.Minute).UnixMilli()
	alive := time.Now().Add(time.Minute).UnixMilli()
	RouteCooldownSave(102, 21, expired)
	RouteCooldownSave(102, 22, alive)

	got, err := RouteCooldownLoad(context.Background())
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if _, ok := got[102][21]; ok {
		t.Fatalf("已到期的冷却不该被恢复: %+v", got[102])
	}
	if got[102][22] != alive {
		t.Fatalf("未到期的冷却必须被恢复: %+v", got[102])
	}
}

// 到期项要被顺手清掉，否则表只增不减。
func TestRouteCooldownLoadPrunesExpired(t *testing.T) {
	ensureRouteCooldownDB(t)
	RouteCooldownSave(103, 31, time.Now().Add(-time.Minute).UnixMilli())

	if _, err := RouteCooldownLoad(context.Background()); err != nil {
		t.Fatalf("加载失败: %v", err)
	}

	var count int64
	if err := db.GetDB().Model(&model.RouteCooldown{}).
		Where("group_id = ? AND item_id = ?", 103, 31).Count(&count).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("到期项应被清理，实际还剩 %d 行", count)
	}
}

// 同一成员重复冷却（例如再次被限流）应更新同一行，不新增行。
func TestRouteCooldownSaveUpserts(t *testing.T) {
	ensureRouteCooldownDB(t)
	first := time.Now().Add(time.Minute).UnixMilli()
	second := time.Now().Add(10 * time.Minute).UnixMilli()
	RouteCooldownSave(104, 41, first)
	RouteCooldownSave(104, 41, second)

	var count int64
	if err := db.GetDB().Model(&model.RouteCooldown{}).
		Where("group_id = ? AND item_id = ?", 104, 41).Count(&count).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("重复保存应更新同一行，实际 %d 行", count)
	}

	got, _ := RouteCooldownLoad(context.Background())
	if got[104][41] != second {
		t.Fatalf("应保留最后一次的 deadline，实得 %v", got[104][41])
	}
}

// **解除必须落库**：否则重启后这条已解除的冷却会从库里复活，
// 把一个已恢复的成员重新封住 —— 探活刚刚证明它可用。
func TestRouteCooldownClearRemovesRow(t *testing.T) {
	ensureRouteCooldownDB(t)
	RouteCooldownSave(105, 51, time.Now().Add(time.Minute).UnixMilli())
	RouteCooldownClear(105, 51)

	got, err := RouteCooldownLoad(context.Background())
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if _, ok := got[105][51]; ok {
		t.Fatalf("已解除的冷却不该再出现: %+v", got)
	}
}

// 分组级清理：分组被删除时它的冷却行必须一并消失，
// 否则分组 ID 复用时会把别人的冷却带进来。
func TestRouteCooldownClearGroupRemovesAllMembers(t *testing.T) {
	ensureRouteCooldownDB(t)
	RouteCooldownSave(106, 61, time.Now().Add(time.Minute).UnixMilli())
	RouteCooldownSave(106, 62, time.Now().Add(time.Minute).UnixMilli())
	RouteCooldownSave(107, 71, time.Now().Add(time.Minute).UnixMilli())

	RouteCooldownClearGroup(106)

	got, err := RouteCooldownLoad(context.Background())
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if _, ok := got[106]; ok {
		t.Fatalf("分组 106 的冷却应被全部清除: %+v", got[106])
	}
	if got[107][71] == 0 {
		t.Fatalf("不该误删其它分组: %+v", got)
	}
}

// 空表返回空 map 而不是 nil，让调用方可以直接 range。
func TestRouteCooldownLoadEmptyReturnsEmptyMap(t *testing.T) {
	ensureRouteCooldownDB(t)
	got, err := RouteCooldownLoad(context.Background())
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if got == nil {
		t.Fatalf("应返回空 map 而非 nil")
	}
	if len(got) != 0 {
		t.Fatalf("空表应返回 0 条，实得 %d", len(got))
	}
}

// 表不存在时按"没有冷却"处理并告警，不返回错误。
// 这条容忍纪律与 ModelMappingRefresh 一致：缺一张表不该让实例起不来。
func TestRouteCooldownLoadToleratesMissingTable(t *testing.T) {
	ensureRouteCooldownDB(t)
	if err := db.GetDB().Migrator().DropTable(&model.RouteCooldown{}); err != nil {
		t.Fatalf("删表失败: %v", err)
	}
	got, err := RouteCooldownLoad(context.Background())
	if err != nil {
		t.Fatalf("缺表不该报错: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("缺表应按空处理，实得 %d 条", len(got))
	}
	// 复原，避免影响后续用例（once 只会建一次表）。
	if err := db.GetDB().AutoMigrate(&model.RouteCooldown{}); err != nil {
		t.Fatalf("重建表失败: %v", err)
	}
}
