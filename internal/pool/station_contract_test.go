package pool_test

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/pool"
	"github.com/bestruirui/octopus/internal/pool/pooltest"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 用契约自检工具包验一遍新加的渠道凭据适配器：七类检查（能力位、字段名、条目 ID、
// 凭据不外泄、列表与详情一致、panic 兜底……）跑在自己刚写的适配器上，
// 既是给自己上锁，也是 `internal/pool/pooltest` 的第一份真实用户。
//
// 必须放在 pool_test 这个外部测试包里：pooltest 依赖 pool，若放在 package pool 里就成了循环引用。

var stationContractDBCounter int64

func TestChannelAdapterPassesContractSelfCheck(t *testing.T) {
	dsn := fmt.Sprintf("file:station-contract-%s-%d?mode=memory&cache=shared", t.Name(), atomic.AddInt64(&stationContractDBCounter, 1))
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open contract test db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{}); err != nil {
		t.Fatalf("migrate contract test db: %v", err)
	}
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "契约站", BaseURL: "https://contract.example.com", Enabled: true}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	for _, name := range []string{"k1", "k2"} {
		key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: name, Key: "sk-" + name, Enabled: true}}
		if err := conn.Create(&key).Error; err != nil {
			t.Fatalf("create key %s: %v", name, err)
		}
	}

	adapter, ok := pool.Lookup("channel")
	if !ok {
		t.Fatalf("内置的 channel 适配器没注册上：%v", pool.Kinds())
	}
	pooltest.Run(t, adapter)
}
