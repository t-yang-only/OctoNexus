package op

// 导入的主键冲突必须如实回报。
//
// ## 现场（2026-10-01 实测）
//
// 把 CN2H2G 的备份导进 apidate 时，备份里有 `阿里云`(id=23) 与 `白嫖`(id=25)，
// 而 apidate 的 23/25 号已被别的渠道占用。`createRowsRaw(..., doNothing=true)`
// 按主键冲突跳过 —— 两个渠道静默消失，接口只回 `channels: +1`。
// 用户看到的是"导入成功"，然后在调用时才发现某个渠道不存在。
//
// ## 为什么这类丢失特别值得防
//
// 跨实例恢复时两个实例的 id 空间各自独立，撞 id 是**常态**而不是异常。
// 孤儿行至少是备份自己的问题；主键冲突却是"备份完全正常、只是撞了"，
// 因此更不能静默。

import (
	"context"
	"testing"

	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
)

// 同 id 不同名 → 必须报成冲突，并点名双方。
//
// 这是本轮现场的最小复现：库里已有 id=23 的"阿里百炼"，备份要插 id=23 的"阿里云"。
func TestImportReportsPrimaryKeyConflict(t *testing.T) {
	conn := openImportTestDB(t)
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)

	// 库内已占用 id=23。
	if err := conn.Create(&model.Channel{
		ChannelConfig: model.ChannelConfig{Name: "阿里百炼", BaseURL: "https://a.example.com", Enabled: true},
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	var seeded model.Channel
	if err := conn.Where("name = ?", "阿里百炼").First(&seeded).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	occupied := seeded.ID

	// 备份要插同一个 id，但名字不同 —— 这一行会被跳过。
	dump := &model.DBDump{
		Version: dbDumpVersion,
		Channels: []model.Channel{{
			ID:            occupied,
			ChannelConfig: model.ChannelConfig{Name: "阿里云", BaseURL: "https://b.example.com", Enabled: true},
		}},
	}

	res, err := DBImportIncremental(context.Background(), dump)
	if err != nil {
		t.Fatalf("DBImportIncremental: %v", err)
	}

	if res.Skipped["channels"] != 1 {
		t.Errorf("应报 1 行主键冲突被跳过，实得 %d（这份备份里的渠道丢了却没人说）",
			res.Skipped["channels"])
	}
	joined := ""
	for _, w := range res.Warnings {
		joined += w + "\n"
	}
	if !hasSubstrOp(joined, "阿里云") || !hasSubstrOp(joined, "阿里百炼") {
		t.Errorf("警告应点名冲突双方（备份里的名字与被占用的名字），实得：%s", joined)
	}
	// 库内那行不该被改名。
	if err := conn.Where("name = ?", "阿里百炼").First(&model.Channel{}).Error; err != nil {
		t.Errorf("库内原有渠道被改动了：%v", err)
	}
}

// 同 id 同名 → 属重复导入，**不算**冲突（否则重复导同一份备份会刷一堆假警告）。
func TestImportSameIDAndNameIsNotConflict(t *testing.T) {
	conn := openImportTestDB(t)
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)

	if err := conn.Create(&model.Channel{
		ChannelConfig: model.ChannelConfig{Name: "53HK", BaseURL: "https://c.example.com", Enabled: true},
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	var seeded model.Channel
	if err := conn.Where("name = ?", "53HK").First(&seeded).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}

	dump := &model.DBDump{
		Version: dbDumpVersion,
		Channels: []model.Channel{{
			ID:            seeded.ID,
			ChannelConfig: model.ChannelConfig{Name: "53HK", BaseURL: "https://c.example.com", Enabled: true},
		}},
	}
	res, err := DBImportIncremental(context.Background(), dump)
	if err != nil {
		t.Fatalf("DBImportIncremental: %v", err)
	}
	if res.Skipped["channels"] != 0 {
		t.Errorf("同 id 同名是重复导入，不该报冲突，实得 %d", res.Skipped["channels"])
	}
}

// 全新备份（id 都不冲突）不该产生任何冲突警告 —— 防止"永远报一堆"的假信号。
func TestImportFreshDumpHasNoConflict(t *testing.T) {
	conn := openImportTestDB(t)
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)

	dump := &model.DBDump{
		Version: dbDumpVersion,
		Channels: []model.Channel{
			{ID: 1, ChannelConfig: model.ChannelConfig{Name: "A", BaseURL: "https://a.example.com", Enabled: true}},
			{ID: 2, ChannelConfig: model.ChannelConfig{Name: "B", BaseURL: "https://b.example.com", Enabled: true}},
		},
		Groups: []model.Group{{ID: 10, Name: "g1"}},
	}
	res, err := DBImportIncremental(context.Background(), dump)
	if err != nil {
		t.Fatalf("DBImportIncremental: %v", err)
	}
	if res.Skipped["channels"] != 0 || res.Skipped["groups"] != 0 {
		t.Errorf("全新导入不该有冲突，实得 channels=%d groups=%d",
			res.Skipped["channels"], res.Skipped["groups"])
	}
	var n int64
	conn.Model(&model.Channel{}).Count(&n)
	if n != 2 {
		t.Errorf("应导入 2 个渠道，实得 %d", n)
	}
}

func hasSubstrOp(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
