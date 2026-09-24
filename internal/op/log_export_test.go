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
	if records[0][0] != "日志ID" || records[0][1] != "请求ID" {
		t.Fatalf("header=%v", records[0])
	}
	// 按列名定位而不是写死下标：导出加一列就会让所有硬下标记错位，
	// 那样的失败信息（"protocol labels=..."）指向的是列号而不是真正的缺陷。
	col := func(name string) int {
		for i, h := range records[0] {
			if h == name {
				return i
			}
		}
		t.Fatalf("导出表头缺少 %q：%v", name, records[0])
		return -1
	}
	idxProtocol, idxFirstByte := col("上游协议"), col("首字节(ms)")
	idxAttempts, idxCost := col("上游轮次"), col("费用")
	idxError, idxReported := col("错误"), col("上游自称模型")
	idxRequestProtocol, idxConvert := col("入站协议"), col("协议转换")
	if records[0][idxReported] != "上游自称模型" {
		t.Fatalf("新列未出现在表头：%v", records[0])
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
	if first[idxProtocol] != "anthropic-messages" || second[idxProtocol] != "openai-responses" {
		t.Fatalf("protocol labels=%q,%q", first[idxProtocol], second[idxProtocol])
	}
	// 入站协议与上游协议必须各自成列（T-trace-004）。
	//
	// 这两行的 RequestProtocol 刻意留 0（fixture 没设，等同升级前存量行），用来钉住
	// 「不知道就写未知、不许拿上游协议顶上」——把入站协议实现成 target_protocol 的克隆，
	// 会在这里得到 chat/responses 而不是空。
	if first[idxRequestProtocol] != "" || second[idxRequestProtocol] != "" {
		t.Fatalf("存量行入站协议应为空（未记录），实得 %q,%q",
			first[idxRequestProtocol], second[idxRequestProtocol])
	}
	if first[idxConvert] != "未知" || second[idxConvert] != "未知" {
		t.Fatalf("任一协议位为 0 时必须写「未知」而不是「否」，实得 %q,%q（拿不知道当没转换会误导读表的人）",
			first[idxConvert], second[idxConvert])
	}
	if first[idxFirstByte] != "12" || second[idxFirstByte] != "" {
		t.Fatalf("first byte column=%q,%q, want 12 and empty (never committed)",
			first[idxFirstByte], second[idxFirstByte])
	}
	if first[idxAttempts] != "3" || second[idxAttempts] != "0" {
		t.Fatalf("attempts column=%q,%q, want 3 and 0 (never reached upstream)",
			first[idxAttempts], second[idxAttempts])
	}
	if first[idxCost] != "0.000123" {
		t.Fatalf("cost column=%q, want the stored float", first[idxCost])
	}
	if second[idxError] != "context canceled" {
		t.Fatalf("error column=%q", second[idxError])
	}
}

// TestRelayLogExportProtocolConversion 两种协议位都记录在案时，导出必须给出确定的「是/否」判断。
//
// 上一个用例只覆盖了「未知」这一种取值，而「未知」在实现上有一个致命退化：
// 把判定写成恒返回「未知」也能让它通过。所以这里必须另取两端协议都已知的样本，
// 一份不同（应为「是」）、一份相同（应为「否」）——两个方向合起来才排除掉常量实现。
func TestRelayLogExportProtocolConversion(t *testing.T) {
	conn := openRelayLogTestDB(t)
	// 跨协议：客户端发 Anthropic Messages，上游吃 OpenAI Chat。
	relayLogSaveOn(conn, model.RelayLog{
		RequestID: 31, Status: "success", Model: "m-conv", TargetChannel: "c1",
		RequestProtocol: int(model.ProtocolAnthropicMessage),
		TargetProtocol:  int(model.ProtocolOpenAIChatCompletion),
	})
	// 原样转发：两端同协议。
	relayLogSaveOn(conn, model.RelayLog{
		RequestID: 32, Status: "success", Model: "m-same", TargetChannel: "c2",
		RequestProtocol: int(model.ProtocolOpenAIResponse),
		TargetProtocol:  int(model.ProtocolOpenAIResponse),
	})

	var buffer bytes.Buffer
	if _, err := relayLogExportCSVOn(conn, &buffer, model.RelayLogFilter{}); err != nil {
		t.Fatalf("export: %v", err)
	}
	records, err := csv.NewReader(bytes.NewReader(buffer.Bytes()[3:])).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("records=%d, want header + 2 rows", len(records))
	}
	col := func(name string) int {
		for i, h := range records[0] {
			if h == name {
				return i
			}
		}
		t.Fatalf("导出表头缺少 %q：%v", name, records[0])
		return -1
	}
	idxReq, idxTarget, idxConvert := col("入站协议"), col("上游协议"), col("协议转换")
	converted, same := records[1], records[2]
	if converted[idxReq] != "anthropic-messages" || converted[idxTarget] != "openai-chat" {
		t.Fatalf("跨协议行两侧协议 = %q / %q, want anthropic-messages / openai-chat",
			converted[idxReq], converted[idxTarget])
	}
	if converted[idxConvert] != "是" {
		t.Errorf("两端协议不同时「协议转换」= %q, want 是", converted[idxConvert])
	}
	if same[idxReq] != "openai-responses" || same[idxTarget] != "openai-responses" {
		t.Fatalf("原样转发行两侧协议 = %q / %q, want 都是 openai-responses",
			same[idxReq], same[idxTarget])
	}
	if same[idxConvert] != "否" {
		t.Errorf("两端协议相同时「协议转换」= %q, want 否（把它标成转换过会让原样转发的请求背黑锅）",
			same[idxConvert])
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
	// 同样按列名定位（见上一个用例的说明）。
	errIdx := -1
	for i, h := range records[0] {
		if h == "错误" {
			errIdx = i
			break
		}
	}
	if errIdx < 0 {
		t.Fatalf("导出表头缺少「错误」列：%v", records[0])
	}
	if records[1][errIdx] != errorText {
		t.Fatalf("error text round trip = %q, want the original with comma/quote/newline", records[1][errIdx])
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
