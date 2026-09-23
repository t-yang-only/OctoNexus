package op

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// attempt_export_test.go 钉住尝试链在**落库与导出**两个出口上的完整性（T-trace-001）。
//
// 为什么单测要跑到真实数据库而不是只测内存结构: 尝试链是 []struct 字段, 能否真正落盘
// 取决于 GORM 的 serializer:json 是否生效、AutoMigrate 有没有把列建出来。
// 只测内存结构的话, "字段加对了但存不进去" 这类问题要到部署后才会暴露。

// 尝试链必须能原样写进库、原样读回来（序列化器生效 + 列已建）。
func TestRelayLogAttemptDetailRoundTrip(t *testing.T) {
	conn := openRelayLogTestDB(t)

	entry := model.RelayLog{
		RequestID:     70001,
		Status:        "success",
		Model:         "group-x",
		TargetChannel: "channelC",
		TargetModel:   "model-c",
		StartedAt:     time.Now(),
		Attempts:      3,
		AttemptDetail: []model.RelayAttemptDetail{
			{Round: 1, Channel: "channelA", Model: "model-a", WaitMs: 120, FaultKind: "transient", Error: "upstream 500"},
			{Round: 2, Channel: "channelB", Model: "model-b", WaitMs: 60, FaultKind: "member", Error: "invalid key"},
			{Round: 3, Channel: "channelC", Model: "model-c", WaitMs: 10},
		},
	}
	relayLogSaveOn(conn, entry)

	var got model.RelayLog
	if err := conn.Where("request_id = ?", 70001).First(&got).Error; err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if len(got.AttemptDetail) != 3 {
		t.Fatalf("落库后尝试链长度 = %d, want 3（serializer:json 可能没生效）", len(got.AttemptDetail))
	}
	if got.AttemptDetail[0].Channel != "channelA" || got.AttemptDetail[0].FaultKind != "transient" || got.AttemptDetail[0].Error != "upstream 500" {
		t.Errorf("第 1 轮往返后不一致: %+v", got.AttemptDetail[0])
	}
	if got.AttemptDetail[1].FaultKind != "member" || got.AttemptDetail[1].WaitMs != 60 {
		t.Errorf("第 2 轮往返后不一致: %+v", got.AttemptDetail[1])
	}
	if got.AttemptDetail[2].FaultKind != "" || got.AttemptDetail[2].Error != "" {
		t.Errorf("成功轮不该带回错误信息: %+v", got.AttemptDetail[2])
	}
	if got.AttemptsTruncated {
		t.Error("未截断却读回 AttemptsTruncated=true")
	}
}

// 旧数据（升级前落库、没有该字段）读回来必须是空链而不是报错 —— 迁移兼容性。
func TestRelayLogAttemptDetailEmptyForLegacyRow(t *testing.T) {
	conn := openRelayLogTestDB(t)

	relayLogSaveOn(conn, model.RelayLog{
		RequestID: 70002, Status: "success", Model: "legacy",
		StartedAt: time.Now(), Attempts: 1,
	})

	var got model.RelayLog
	if err := conn.Where("request_id = ?", 70002).First(&got).Error; err != nil {
		t.Fatalf("旧行回读失败: %v", err)
	}
	if len(got.AttemptDetail) != 0 {
		t.Fatalf("旧行应读出空链, got %+v", got.AttemptDetail)
	}
}

// CSV 导出的「尝试明细」列必须真的写出人可读摘要, 且表头与行**列数一致**。
//
// 少了这条, 加列时漏改 relayLogExportRow 不会被任何断言发现 ——
// 表现是导出的 CSV 整体错位一列, 而字段名还在, 极难察觉。
func TestAttemptDetailExportColumn(t *testing.T) {
	conn := openRelayLogTestDB(t)

	relayLogSaveOn(conn, model.RelayLog{
		RequestID: 70003, Status: "failed", Model: "group-y",
		TargetChannel: "channelZ", StartedAt: time.Now(), Attempts: 2,
		AttemptDetail: []model.RelayAttemptDetail{
			{Round: 1, Channel: "channelA", WaitMs: 120, FaultKind: "transient"},
			{Round: 2, Channel: "channelZ", WaitMs: 30, FaultKind: "member"},
		},
		AttemptsTruncated: true,
	})

	var buf bytes.Buffer
	if _, err := relayLogExportCSVOn(conn, &buf, model.RelayLogFilter{}); err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	// 导出带 UTF-8 BOM, 解析前先剥掉。
	raw := buf.Bytes()
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		raw = raw[3:]
	}
	records, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		t.Fatalf("解析 CSV 失败: %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("导出记录数 = %d, want >= 2", len(records))
	}

	header := records[0]
	if len(header) != len(relayLogExportHeader) {
		t.Fatalf("表头列数 = %d, want %d", len(header), len(relayLogExportHeader))
	}
	detailIdx := -1
	for i, name := range header {
		if name == "尝试明细" {
			detailIdx = i
		}
	}
	if detailIdx < 0 {
		t.Fatalf("表头里找不到「尝试明细」: %v", header)
	}
	// 每一行的列数都必须等于表头 —— 这是"加列时行没跟上"的直接判据。
	for i, row := range records[1:] {
		if len(row) != len(header) {
			t.Fatalf("第 %d 行列数 = %d, want %d（行与表头错位）", i+1, len(row), len(header))
		}
	}

	cell := records[1][detailIdx]
	// 必须同时含两轮的渠道名与分类, 并显式标出被截断。
	for _, want := range []string{"#1 channelA", "#2 channelZ", "transient", "member", "已截断"} {
		if !strings.Contains(cell, want) {
			t.Errorf("尝试明细列 = %q, 缺少 %q", cell, want)
		}
	}
}
