package op

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// 请求级明细导出 (U-key-001 余项): relay_logs 里的数据早就是齐的(输入/输出 token、缓存命中、费用、耗时、命中渠道与模型),
// 缺的只是"能拿出去"的出口。这里按面板同一套筛选条件把它导成 CSV。
//
// 三条口径:
//  1. **流式写**: 逐行 ScanRows 直接写进 ResponseWriter / 任意 io.Writer, 内存不随导出条数增长(把 relay_logs 全量拉进切片再导会先 OOM)。
//  2. **给 Excel 的 BOM**: 首行前写 UTF-8 BOM, 否则 Excel 打开中文表头是乱码。
//  3. **不给下游留假的 -1**: 未提交首字节时库内是 -1, 导出成空串, 让表格工具按"无数据"处理, 不参与均值。
//
// 另外给整次导出设一个上限(RelayLogExportMaxRows), 超过就截断, 避免一次请求把库与响应都拖垮。

// RelayLogExportMaxRows 是单次导出的最大行数。
const RelayLogExportMaxRows = 200000

// relayLogExportHeader 是导出的列名, 顺序即表格列顺序; 与面板展示字段一一对应。
// 第一列给**日志行主键**而不是请求 ID: 一个客户端请求可能留多行(重试/多轮各一行), 请求 ID 会重复,
// 只有行主键能唯一定位一行, 下游做去重/回查时不必再猜。
var relayLogExportHeader = []string{
	"日志ID", "请求ID", "时间", "状态", "分组(请求模型)", "上游模型", "上游自称模型", "模型一致", "目标渠道", "入站协议", "上游协议", "协议转换", "首字节(ms)", "耗时(ms)", "上游轮次", "判定理由",
	"输入tokens", "缓存命中tokens", "输出tokens", "思考强度", "思考tokens", "思考字数", "费用", "API Key", "错误", "失败归因", "终止原因", "尝试明细", "测试请求",
}

// RelayLogExportCSV 按筛选条件把请求级明细写成 CSV, 返回写出的行数(不含表头)。
// 行数达到 RelayLogExportMaxRows 时截断返回, 由调用方决定怎么提示。
func RelayLogExportCSV(writer io.Writer, filter model.RelayLogFilter) (int64, error) {
	return relayLogExportCSVOn(nil, writer, filter)
}

func relayLogExportCSVOn(conn dbConn, writer io.Writer, filter model.RelayLogFilter) (int64, error) {
	if _, err := writer.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return 0, fmt.Errorf("write utf-8 bom: %w", err)
	}
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write(relayLogExportHeader); err != nil {
		return 0, fmt.Errorf("write csv header: %w", err)
	}

	// 导出按时间正序: 拿到表格里是"故事线"顺序, 与面板的倒序分页刻意不同, 便于二次分析。
	query := relayLogQuery(conn, filter).Order("id ASC")
	rows, err := query.Rows()
	if err != nil {
		return 0, fmt.Errorf("query relay logs: %w", err)
	}
	defer rows.Close()

	db := connOrDefault(conn)
	var exported int64
	for rows.Next() {
		if exported >= RelayLogExportMaxRows {
			break
		}
		var entry model.RelayLog
		if err := db.ScanRows(rows, &entry); err != nil {
			return exported, fmt.Errorf("scan relay log row: %w", err)
		}
		if err := csvWriter.Write(relayLogExportRow(entry)); err != nil {
			return exported, fmt.Errorf("write csv row: %w", err)
		}
		exported++
	}
	if err := rows.Err(); err != nil && err != gorm.ErrRecordNotFound {
		return exported, fmt.Errorf("iterate relay logs: %w", err)
	}
	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		return exported, fmt.Errorf("flush csv: %w", err)
	}
	return exported, nil
}

// relayLogExportRow 把一个日志行铺成 CSV 记录。
func relayLogExportRow(entry model.RelayLog) []string {
	firstByte := ""
	if entry.FirstByteMs >= 0 {
		firstByte = strconv.FormatInt(entry.FirstByteMs, 10)
	}
	created := entry.CreatedAt
	if created.IsZero() {
		created = entry.StartedAt
	}
	return []string{
		strconv.FormatUint(entry.ID, 10),
		strconv.FormatUint(entry.RequestID, 10),
		created.Format(time.RFC3339),
		entry.Status,
		entry.Model,
		entry.TargetModel,
		entry.ReportedModel,
		modelMatchLabel(entry.ReportedModel, entry.ModelMismatch),
		entry.TargetChannel,
		protocolLabel(entry.RequestProtocol),
		protocolLabel(entry.TargetProtocol),
		protocolConvertLabel(entry.RequestProtocol, entry.TargetProtocol),
		firstByte,
		strconv.FormatInt(entry.DurationMs, 10),
		strconv.Itoa(entry.Attempts),
		entry.Decision,
		strconv.FormatInt(entry.PromptTokens, 10),
		strconv.FormatInt(entry.CachedTokens, 10),
		strconv.FormatInt(entry.CompletionToks, 10),
		// 思考三列（T-insight-001/005）：强度是请求侧、token 与字数是输出侧。
		// 三者**各记各的、不互斥**。0 照写而不留空：它表示"上游没报 token"或
		// "响应里没有思考文本"，是个确定的事实，抹成空串就看不出这条信息了。
		entry.ReasoningEffort,
		strconv.FormatInt(entry.ReasoningTokens, 10),
		strconv.Itoa(entry.ReasoningChars),
		strconv.FormatFloat(entry.Cost, 'f', -1, 64),
		entry.APIKeyName,
		entry.Error,
		// 失败诊断两列（T-usability-007 / T-trace-003）：都用**后端原文枚举**，
		// 与「状态」「判定理由」两列的既有口径一致 —— 导出是表格工具吃的结构化输出，
		// 换成中文反而让下游脚本无法处理，且要维护一张随时会漂移的映射表。
		//
		// 空值就是空值：存量行没有归因，导出必须留空而不是填 "unknown" ——
		// 编一个默认值会让读表的人以为那是真实的归因结论。
		entry.FaultKind,
		entry.StopReason,
		attemptDetailLabel(entry),
		// 测试请求（T-trace-006）：附在**最后一列**而不是插在中间 ——
		// 下游可能已有按列号解析这份 CSV 的脚本，中间插列会让那些解析静默错位。
		testRequestLabel(entry.IsTest),
	}
}

// testRequestLabel 把测试请求标记铺成导出的可读文本（T-trace-006）。
//
// 两态而非三态：这一列回答"这条日志算不算验证流量"，没声明过的行（含升级前的存量行）
// 就是非测试流量，答案确定 —— 与「模型一致」那种"上游没回报所以未知"的三态不是一类问题。
func testRequestLabel(isTest bool) string {
	if isTest {
		return "是"
	}
	return "否"
}

// attemptDetailLabel 把尝试链压成一行供表格阅读（T-trace-001）。
//
// 为什么导出成文本而不是 JSON: 这一列是给人扫的——"第2轮 channelB 401(member)"
// 一眼就能看出是谁在拖后腿; 原样吐 JSON 在表格工具里只会是一坨需要再解析的东西。
// 需要机器处理时用接口的 attempt_detail 字段（结构化, 带完整原文）。
func attemptDetailLabel(entry model.RelayLog) string {
	if len(entry.AttemptDetail) == 0 {
		return ""
	}
	parts := make([]string, 0, len(entry.AttemptDetail))
	for _, attempt := range entry.AttemptDetail {
		label := fmt.Sprintf("#%d %s %dms", attempt.Round, attempt.Channel, attempt.WaitMs)
		if attempt.FaultKind != "" {
			label += " " + attempt.FaultKind
		} else {
			label += " ok"
		}
		parts = append(parts, label)
	}
	joined := strings.Join(parts, " | ")
	if entry.AttemptsTruncated {
		// 截断必须写在明面上: 只给后半段而不说明, 读的人会以为这就是全部。
		joined = "…(前段已截断) " + joined
	}
	return joined
}

// protocolConvertLabel 回答导出表格里最常被追问的一句话：这次请求中间做过跨协议转换吗？（T-trace-004）
//
// 三种取值各自对应一种事实，不能用同一个字表示：
//
//	"是"     两端协议都已知且不同 —— 中间确实做了一次跨协议转换
//	"否"     两端协议都已知且相同 —— 原样转发
//	"未知"   任一侧为 0（升级前的存量行、systemone 这类自定义形态）—— 没有信息，不做判断
//
// 把"未知"单独写出来而不是留空，是因为空单元格看起来总是良性的：读表的人会把
// 「不知道」当成「没转换」，而这恰恰是升级前那批数据唯一诚实的表述。
func protocolConvertLabel(requestProtocol, targetProtocol int) string {
	if requestProtocol == 0 || targetProtocol == 0 {
		return "未知"
	}
	if requestProtocol == targetProtocol {
		return "否"
	}
	return "是"
}

// protocolLabel 把协议位掩码写成可读名: 导出给人和表格工具看, 数字位掩码没有意义。
func protocolLabel(protocol int) string {
	switch model.Protocol(protocol) {
	case model.ProtocolOpenAIChatCompletion:
		return "openai-chat"
	case model.ProtocolOpenAIResponse:
		return "openai-responses"
	case model.ProtocolAnthropicMessage:
		return "anthropic-messages"
	case 0:
		return ""
	}
	return strconv.Itoa(protocol)
}

// modelMatchLabel 把模型一致性判定铺成导出的可读文本（T-verify-001）。
//
// 三种取值各有含义，不能用同一个字表示：
//
//	"一致"   双方都有值且相同
//	"不一致" 双方都有值但不同 —— 这才是"上游可能偷换模型"的证据
//	"未回报" 上游没给模型名，无从判定
//
// 把"未回报"单独标出来是为了避免读表的人把空单元格当成"没问题"：
// 空格看起来总是良性的，而它其实意味着这条记录没有校验能力。
func modelMatchLabel(reported string, mismatch bool) string {
	if strings.TrimSpace(reported) == "" {
		return "未回报"
	}
	if mismatch {
		return "不一致"
	}
	return "一致"
}
