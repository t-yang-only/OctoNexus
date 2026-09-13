package op

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var relayLogTestDBSeq int64

// openRelayLogTestDB 打开一块直连内存 SQLite 并建出 relay_logs 表。
// DSN 按测试名+计数器隔离: op 层经全局库取连接, 本测试用 *On(conn, …) 变体直连,
// 不碰生产库, 也不与 auto_group 测试的内存库互相干扰。
func openRelayLogTestDB(t *testing.T) dbConn {
	t.Helper()
	seq := atomic.AddInt64(&relayLogTestDBSeq, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(&model.RelayLog{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return conn
}

// TestRelayLogSaveListClean 覆盖历史日志落库→筛选查询→保留期清理的主路径。
func TestRelayLogSaveListClean(t *testing.T) {
	conn := openRelayLogTestDB(t)

	relayLogSaveOn(conn, model.RelayLog{RequestID: 1, Status: "success", Model: "gpt-x", TargetChannel: "c1", APIKeyName: "k1", Error: ""})
	relayLogSaveOn(conn, model.RelayLog{RequestID: 2, Status: "failed", Model: "claude-y", TargetChannel: "c2", APIKeyName: "k2", Error: "boom"})
	relayLogSaveOn(conn, model.RelayLog{RequestID: 3, Status: "success", Model: "gpt-x", TargetChannel: "c1", APIKeyName: "k1", Error: ""})

	// 未提交的首字节记 -1, 查询侧原样返回。
	relayLogSaveOn(conn, model.RelayLog{RequestID: 4, Status: "canceled", Model: "m", FirstByteMs: -1})

	got, total := relayLogListOn(conn, model.RelayLogFilter{})
	if total != 4 || len(got) != 4 {
		t.Fatalf("list all = %d/%d, want 4/4", len(got), total)
	}
	// 倒序: 最新落库的在前。
	if got[0].RequestID != 4 {
		t.Fatalf("list order first = %d, want 4 (id DESC)", got[0].RequestID)
	}

	got, total = relayLogListOn(conn, model.RelayLogFilter{Status: "success"})
	if total != 2 {
		t.Fatalf("status filter total = %d, want 2", total)
	}

	got, total = relayLogListOn(conn, model.RelayLogFilter{Channel: "c2"})
	if total != 1 || got[0].RequestID != 2 {
		t.Fatalf("channel filter = %+v, want request 2", got)
	}

	got, total = relayLogListOn(conn, model.RelayLogFilter{Q: "boom"})
	if total != 1 || got[0].RequestID != 2 {
		t.Fatalf("keyword filter = %+v, want request 2", got)
	}

	// 分页: limit 截断, total 仍是全量。
	got, total = relayLogListOn(conn, model.RelayLogFilter{Limit: 2})
	if len(got) != 2 || total != 4 {
		t.Fatalf("page limit = %d/%d, want 2/4", len(got), total)
	}

	// 超限 limit 收敛到页上限而非无界查询。
	got, _ = relayLogListOn(conn, model.RelayLogFilter{Limit: 100000})
	if len(got) != 4 {
		t.Fatalf("over-limit query len = %d, want 4", len(got))
	}

	// 保留期 0 天按默认 7 天清理: 刚落库的记录应全部保留, 删除 0 条。
	if n := relayLogCleanOn(conn, 0); n != 0 {
		t.Fatalf("clean fresh = %d, want 0", n)
	}
	// 负保留期同样收敛到默认行为, 不删库。
	if n := relayLogCleanOn(conn, -1); n != 0 {
		t.Fatalf("clean negative = %d, want 0", n)
	}
}
