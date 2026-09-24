package op

import (
	"bytes"
	"encoding/csv"
	"reflect"
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

// TestRelayLogExportDiagnosticColumns 思考与失败诊断五列必须都在导出里，且值落在自己的列上。
//
// ## 为什么要有这条判据
//
// 这五列是在界面已经能看到之后才补的（T-insight-005 收尾时对表发现的缺口）：
// 思考强度/思考 token/思考字数在前端日志卡片上有，失败归因与终止原因在前端有
// 专门的面板，但导出的 23 列里一格都没有 —— 导出是拿去做分析的，
// 缺了这些列，"哪些渠道出哪类错""思考强度与成本的关系"就都做不了。
//
// ## 取值刻意每列不同，专门钉住"列错位"
//
// 加列最容易犯的错不是漏值，而是**值写到了相邻列**（尤其 thinking 三列挤在一起时，
// 把 tokens 与 chars 写反，值看起来都像数字，肉眼审不出来）。
// 所以这份 fixture 给每列一个可区分的值，逐个断言。
func TestRelayLogExportDiagnosticColumns(t *testing.T) {
	conn := openRelayLogTestDB(t)
	const (
		effort    = "xhigh"
		tokens    = int64(150)
		chars     = 206
		faultKind = "transient"
		stopText  = "action=stop;reason=attempt_budget_exhausted;source=config"
	)
	relayLogSaveOn(conn, model.RelayLog{
		RequestID: 41, Status: "failed", Model: "m-diag", TargetChannel: "c1",
		ReasoningEffort: effort, ReasoningTokens: tokens, ReasoningChars: chars,
		FaultKind: faultKind, StopReason: stopText, Error: "upstream_error",
	})

	var buffer bytes.Buffer
	if _, err := relayLogExportCSVOn(conn, &buffer, model.RelayLogFilter{}); err != nil {
		t.Fatalf("export: %v", err)
	}
	records, err := csv.NewReader(bytes.NewReader(buffer.Bytes()[3:])).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records=%d, want header + 1 row", len(records))
	}
	header, row := records[0], records[1]

	// 列数与行长度必须一致：少一列会让 CSV 解析整体错位，
	// 而 Excel 打开时只会把最后一列吃掉，看起来像"数据缺失"。
	if len(row) != len(header) {
		t.Fatalf("row has %d fields but header has %d（加列时漏了行构造）", len(row), len(header))
	}

	col := func(name string) int {
		for i, h := range header {
			if h == name {
				return i
			}
		}
		t.Fatalf("导出表头缺少 %q：%v", name, header)
		return -1
	}

	cases := []struct {
		column string
		want   string
		why    string
	}{
		{"思考强度", effort, "请求侧原样记录"},
		{"思考tokens", "150", "上游回报的 token 数"},
		{"思考字数", "206", "响应正文里思考文本的字符数"},
		{"失败归因", faultKind, "失败算谁的账（原文枚举）"},
		{"终止原因", stopText, "哪条规则终止了请求（原始串）"},
	}
	for _, c := range cases {
		got := row[col(c.column)]
		if got != c.want {
			t.Errorf("「%s」列 = %q, want %q（%s）", c.column, got, c.want, c.why)
		}
	}

	// 三列挤在一起时最容易互相串位，这里再显式钉一次互不相等。
	if row[col("思考tokens")] == row[col("思考字数")] {
		t.Errorf("思考 tokens 与思考字数落成了同一个值，两列可能写反或重复：%q",
			row[col("思考tokens")])
	}
}

// TestRelayLogExportReasoningColumnsCoexist 两个思考字段同时有值时必须都导出。
//
// 这是本功能的核心口径：token 是"上游说花了多少"、字数是"文本实际多长"，
// 两者**各记各的、不互斥**（生产已出现同一行 chars=206 / tokens=150 的实例）。
// 参考项目那种"有官方 token 就不记字符数"的互斥写法会让这一行的 206 字凭空消失。
func TestRelayLogExportReasoningColumnsCoexist(t *testing.T) {
	conn := openRelayLogTestDB(t)
	relayLogSaveOn(conn, model.RelayLog{
		RequestID: 51, Status: "success", Model: "m-coexist", TargetChannel: "c1",
		ReasoningEffort: "high", ReasoningTokens: 150, ReasoningChars: 206,
	})

	var buffer bytes.Buffer
	if _, err := relayLogExportCSVOn(conn, &buffer, model.RelayLogFilter{}); err != nil {
		t.Fatalf("export: %v", err)
	}
	records, err := csv.NewReader(bytes.NewReader(buffer.Bytes()[3:])).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	row := records[1]
	col := func(name string) int {
		for i, h := range records[0] {
			if h == name {
				return i
			}
		}
		t.Fatalf("导出表头缺少 %q", name)
		return -1
	}
	if row[col("思考强度")] != "high" {
		t.Errorf("思考强度 = %q, want high", row[col("思考强度")])
	}
	if row[col("思考tokens")] != "150" {
		t.Errorf("思考tokens = %q, want 150", row[col("思考tokens")])
	}
	// 互斥实现（有 token 就把 chars 清零）会在这里得到 "0"。
	if row[col("思考字数")] != "206" {
		t.Errorf("思考字数 = %q, want 206（上游报了 token 也不能把字符数丢掉，两者是独立的量）",
			row[col("思考字数")])
	}
}

// TestRelayLogExportDiagnosticColumnsEmptyForOldRows 升级前落库的行在诊断列上必须是空/零，
// 不能被写成看似有值的占位。
//
// 负向对照的意义：这些列是后加的，库里的存量行（122 行）本来就没有这些信息。
// 若实现用"unknown"之类的默认串填充，读表的人会以为那是真实的归因结论。
func TestRelayLogExportDiagnosticColumnsEmptyForOldRows(t *testing.T) {
	conn := openRelayLogTestDB(t)
	// 只设存量行必有的字段，诊断列全部留零值（等同升级前的行）。
	relayLogSaveOn(conn, model.RelayLog{
		RequestID: 61, Status: "success", Model: "m-old", TargetChannel: "c1",
	})

	var buffer bytes.Buffer
	if _, err := relayLogExportCSVOn(conn, &buffer, model.RelayLogFilter{}); err != nil {
		t.Fatalf("export: %v", err)
	}
	records, err := csv.NewReader(bytes.NewReader(buffer.Bytes()[3:])).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	row := records[1]
	col := func(name string) int {
		for i, h := range records[0] {
			if h == name {
				return i
			}
		}
		t.Fatalf("导出表头缺少 %q", name)
		return -1
	}
	// 字符串类诊断列：空串（不是 "unknown"、"未知" 之类编出来的结论）。
	for _, name := range []string{"思考强度", "失败归因", "终止原因"} {
		if row[col(name)] != "" {
			t.Errorf("存量行的「%s」应为空串，实得 %q（编一个默认值会让人以为那是真实结论）",
				name, row[col(name)])
		}
	}
	// 数值类诊断列：0（表示上游没报 / 没有思考文本，是确定的事实）。
	if row[col("思考tokens")] != "0" || row[col("思考字数")] != "0" {
		t.Errorf("存量行的思考计数应为 0，实得 tokens=%q chars=%q",
			row[col("思考tokens")], row[col("思考字数")])
	}
}

// relayLogExportColumns 记录 RelayLog 的每个字段在导出里的去处。
//
// ## 为什么要有这张表 + 下面那条测试
//
// v0.61（终止原因）、v0.63（思考强度/思考 token）、v0.65（入站协议）、v0.67（思考字数）
// **连续四次**在 RelayLog 上加了字段、四次都没同步导出 —— 这不是偶然，而是流程缺口：
// 加字段时改的是 model/relay 两处，导出在另一个包里，很容易整块忘掉。
// 后果是界面上看得见、导出拿不到，而导出恰恰是拿去做分析的。
//
// 所以把「每个字段必须有个去处」变成一条会红的判据：新增字段时要么给它一列，
// 要么在这里写明为什么不导出。Skip 理由不能为空 —— 空理由等于没做决定。
var relayLogExportColumns = map[string]string{
	"ID":              "日志ID",
	"RequestID":       "请求ID",
	"CreatedAt":       "时间",
	"Status":          "状态",
	"Model":           "分组(请求模型)",
	"TargetModel":     "上游模型",
	"ReportedModel":   "上游自称模型",
	"ModelMismatch":   "模型一致",
	"TargetChannel":   "目标渠道",
	"RequestProtocol": "入站协议",
	"TargetProtocol":  "上游协议",
	"FirstByteMs":     "首字节(ms)",
	"DurationMs":      "耗时(ms)",
	"Attempts":        "上游轮次",
	"Decision":        "判定理由",
	"PromptTokens":    "输入tokens",
	"CachedTokens":    "缓存命中tokens",
	"CompletionToks":  "输出tokens",
	"ReasoningEffort": "思考强度",
	"ReasoningTokens": "思考tokens",
	"ReasoningChars":  "思考字数",
	"Cost":            "费用",
	"APIKeyName":      "API Key",
	"Error":           "错误",
	"FaultKind":       "失败归因",
	"StopReason":      "终止原因",
	"IsTest":          "测试请求",
	"AttemptDetail":   "尝试明细",
	// 截断标记没有独立列，它作为前缀写在同一列里（"...(前段已截断) #1 ..."）：
	// 单独占一列会让表更宽，而它只在极少数行上有值。
	"AttemptsTruncated": "尝试明细",
}

// relayLogExportSkips 是**有意不进导出**的字段，理由必须写清楚。
var relayLogExportSkips = map[string]string{
	"GroupID":   "内部分组主键；导出已有「分组(请求模型)」这一列给人看，主键对读表的人没有意义",
	"StartedAt": "与 CreatedAt 几乎同源（落库时刻）；导出只给一列，避免表里出现两个会打架的时间",
}

// TestRelayLogExportFieldCoverage 双向守卫：字段必须有去处，去处必须真在表头里。
//
// 方向一（字段 → 列）：新增 RelayLog 字段却忘了决定是否导出时变红。
// 方向二（列 → 表头）：清单里写了列名但表头没实现（打错字、改了名）时变红 ——
// 只做方向一会让「清单里写了个不存在的列」悄悄通过。
func TestRelayLogExportFieldCoverage(t *testing.T) {
	headerSet := make(map[string]bool, len(relayLogExportHeader))
	for _, column := range relayLogExportHeader {
		headerSet[column] = true
	}

	// 方向二：清单指向的列必须真实存在。
	for field, column := range relayLogExportColumns {
		if !headerSet[column] {
			t.Errorf("RelayLog.%s 声称导出到「%s」，但表头里没有这一列", field, column)
		}
	}

	// 方向一：每个字段都要有决定。
	typ := reflect.TypeOf(model.RelayLog{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if name == "" || name[0] < 'A' || name[0] > 'Z' {
			continue // 非导出字段（内部状态）不参与
		}
		if _, ok := relayLogExportColumns[name]; ok {
			continue
		}
		reason, ok := relayLogExportSkips[name]
		if !ok {
			t.Errorf("RelayLog.%s 既不在 relayLogExportColumns、也不在 relayLogExportSkips —— "+
				"新增字段时必须二选一（v0.61~v0.67 连续四次漏导出的根因就是没有这道闸）", name)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("RelayLog.%s 标记为跳过导出但没写理由（空理由等于没做决定）", name)
		}
	}

	// 反向冗余检查：清单里的字段名必须真的存在于结构体上（防改名后清单残留）。
	for field := range relayLogExportColumns {
		if _, ok := typ.FieldByName(field); !ok {
			t.Errorf("relayLogExportColumns 里的 %s 在 RelayLog 上不存在（字段改名后清单没同步）", field)
		}
	}
	for field := range relayLogExportSkips {
		if _, ok := typ.FieldByName(field); !ok {
			t.Errorf("relayLogExportSkips 里的 %s 在 RelayLog 上不存在（字段改名后清单没同步）", field)
		}
	}
}
