package op

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// U-key-001 余项（请求级明细导出）的单测：CSV 形状、字段口径、筛选条件与 Excel 友好的 BOM。

// TestRelayLogExportCSVShape 导出内容的列、顺序与字段口径：
// BOM 在最前、表头固定、按 id 正序、未提交首字节导出为空串、协议位掩码写成可读名。
func TestRelayLogExportCSVShape(t *testing.T) {
	conn := openRelayLogTestDB(t)
	created := time.Date(2026, 9, 16, 1, 2, 3, 0, time.Local)
	relayLogSaveOn(conn, model.RelayLog{
		RequestID: 11, Status: "success", Model: "DS-TEST-formats", TargetModel: "mock-good",
		TargetChannel: "DS-TEST-mock", TargetProtocol: int(model.ProtocolAnthropicMessage),
		FirstByteMs: 12, DurationMs: 340, Attempts: 3, PromptTokens: 11, CachedTokens: 3, CompletionToks: 7,
		Cost: 0.000123, APIKeyName: "k1", CreatedAt: created,
	})
	// 未提交首字节的请求在库内是 -1, 导出必须给空串(表格工具按"无数据"处理)。
	relayLogSaveOn(conn, model.RelayLog{
		RequestID: 12, Status: "canceled", Model: "gpt-x", TargetChannel: "c2",
		TargetProtocol: int(model.ProtocolOpenAIResponse), FirstByteMs: -1, DurationMs: 25007,
		APIKeyName: "k2", Error: "context canceled", CreatedAt: created.Add(time.Second),
	})

	var buffer bytes.Buffer
	written, err := relayLogExportCSVOn(conn, &buffer, model.RelayLogFilter{})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if written != 2 {
		t.Fatalf("written=%d, want 2", written)
	}
	raw := buffer.Bytes()
	if !bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("csv does not start with a utf-8 bom (excel would show mojibake headers)")
	}
	records, err := csv.NewReader(bytes.NewReader(raw[3:])).ReadAll()
	if err != nil {
		t.Fatalf("parse exported csv: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("records=%d, want 1 header + 2 rows", len(records))
	}
	if records[0][0] != "日志ID" || records[0][1] != "请求ID" || records[0][7] != "上游协议" ||
		records[0][10] != "上游轮次" || records[0][11] != "判定理由" || records[0][17] != "错误" {
		t.Fatalf("header=%v", records[0])
	}
	first, second := records[1], records[2]
	// 第一列是日志行主键(唯一), 第二列才是请求 ID: 请求 ID 会因重试而重复, 不能当行标识。
	if first[1] != "11" || second[1] != "12" {
		t.Fatalf("export order = %s,%s, want ascending request ids 11,12", first[1], second[1])
	}
	if first[0] == second[0] {
		t.Fatalf("log id column is not unique: %s vs %s", first[0], second[0])
	}
	if first[2] != created.Format(time.RFC3339) {
		t.Fatalf("time column=%q, want RFC3339", first[2])
	}
	if first[7] != "anthropic-messages" || second[7] != "openai-responses" {
		t.Fatalf("protocol labels=%q,%q", first[7], second[7])
	}
	if first[8] != "12" || second[8] != "" {
		t.Fatalf("first byte column=%q,%q, want 12 and empty (never committed)", first[8], second[8])
	}
	if first[10] != "3" || second[10] != "0" {
		t.Fatalf("attempts column=%q,%q, want 3 and 0 (never reached upstream)", first[10], second[10])
	}
	if first[15] != "0.000123" {
		t.Fatalf("cost column=%q, want the stored float (判定理由列插在上游轮次之后, 后续列右移一位)", first[15])
	}
	if second[17] != "context canceled" {
		t.Fatalf("error column=%q", second[17])
	}
}

// TestRelayLogExportCSVEscapesAndFilters 带逗号/引号/换行的错误信息能原样往返, 且筛选条件真的生效。
func TestRelayLogExportCSVEscapesAndFilters(t *testing.T) {
	conn := openRelayLogTestDB(t)
	errorText := "upstream 500: {\"message\":\"boom, again\"}\nsecond line"
	relayLogSaveOn(conn, model.RelayLog{RequestID: 21, Status: "failed", Model: "m1", TargetChannel: "c1", Error: errorText})
	relayLogSaveOn(conn, model.RelayLog{RequestID: 22, Status: "success", Model: "m2", TargetChannel: "c2"})

	var buffer bytes.Buffer
	if _, err := relayLogExportCSVOn(conn, &buffer, model.RelayLogFilter{Status: "failed"}); err != nil {
		t.Fatalf("export filtered: %v", err)
	}
	records, err := csv.NewReader(bytes.NewReader(buffer.Bytes()[3:])).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records=%d, want header + 1 filtered row", len(records))
	}
	if records[1][1] != "21" {
		t.Fatalf("filtered row request id=%s, want 21", records[1][1])
	}
	if records[1][17] != errorText {
		t.Fatalf("error text round trip = %q, want the original with comma/quote/newline", records[1][17])
	}
}

// TestRelayLogExportCSVEmpty 空结果仍然给出表头(下游脚本不必特判空文件), 且不报错。
func TestRelayLogExportCSVEmpty(t *testing.T) {
	conn := openRelayLogTestDB(t)
	var buffer bytes.Buffer
	written, err := relayLogExportCSVOn(conn, &buffer, model.RelayLogFilter{})
	if err != nil {
		t.Fatalf("export empty: %v", err)
	}
	if written != 0 {
		t.Fatalf("written=%d, want 0", written)
	}
	text := buffer.String()
	if !strings.Contains(text, "请求ID") || strings.Count(strings.TrimSpace(strings.TrimPrefix(text, "\ufeff")), "\n") != 0 {
		t.Fatalf("empty export = %q, want only the header line", text)
	}
}
