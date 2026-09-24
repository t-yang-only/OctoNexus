package relay

import (
	"encoding/json"
	"unicode/utf8"
)

// reasoning_chars 的取值口径（T-insight-005，吸收参考项目 lingyuins/octopus 的 ReasoningChars）。
//
// ## 为什么需要它
//
// T-insight-001 已经落了 reasoning_tokens，但**上游绝大多数并不回报**这个字段：
// 生产实测 122 行日志里 reasoning_tokens > 0 的**一条都没有**（NULL 119 条）。
// 也就是说"这条请求的思考有多长"在界面上长期是空白。客户端能看到的只有
// 响应正文里那段思考文本本身，于是需要一个不依赖上游自觉的度量。
//
// ## 为什么不照搬参考项目的"互斥"口径
//
// 参考项目的做法是：有官方 reasoning_tokens 时**不记** 字符数（两者互斥）。
// 照搬会让这个字段的**分母随上游心情变化**——同一个渠道换了模型、或上游升级后
// 开始回报 token，历史序列就会从"有值"断成"全 0"，看起来像思考突然消失了。
//
// 这里改成：**两个字段各记各的，都落库**。字符数是"文本有多长"（进程内可确定），
// token 是"上游说花了多少"（外部事实），两者本就不是同一个量，不需要互相顶替。
// 界面按各自有无分别展示，聚合时也各有分母。
//
// ## 为什么不是 token 的估算
//
// 不拿字符数 ÷ 系数去冒充 token：那个系数依赖语言与分词器（中文约 1 字 1 token、
// 英文约 4 字符 1 token），估出来的值会与真实 token 混在同一个字段里，
// 无法区分"上游报的"与"我们猜的"。字段分开，谁都不会被污染。

// reasoningCharsLimit 是单条响应里提取思考文本的**字节**上限。
//
// 提取要遍历响应正文，而正文在流式聚合后可能有数 MB（长回答 + 长思考）。超过上限
// 就不再继续找——字符数本身就不是一个需要精确到字节的指标（它衡量"思考有多长"的
// 量级），而遍历开销发生在请求终态的落库路径上，必须设一个明确的上界。
//
// 8 MiB 的取值依据：正常单次回答（含思考）远小于此；超过这个量的响应本身就是
// 异常，此时返回"已达上限的近似值"比拖慢落库更合理。
const reasoningCharsLimit = 8 << 20

// reasoningCharsFromBody 从聚合后的响应正文里取出思考文本的 rune 数（T-insight-005）。
//
// 返回 0 的三种情形要分清（界面据此显示「—」还是「0 字」）：
//   - 正文为空或不是合法 JSON（异常路径）；
//   - 正文里确实没有思考文本（该模型不思考，或思考被上游折叠掉了）；
//   - 模型走了 systemone 这类自定义形态（不产生标准响应体）。
//
// 前两种在语义上都答"没有思考文本"，因此统一返回 0。
func reasoningCharsFromBody(body string) int {
	if body == "" {
		return 0
	}
	if len(body) > reasoningCharsLimit {
		body = body[:reasoningCharsLimit]
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return 0
	}
	return reasoningCharsFromParsed(parsed)
}

// reasoningCharsFromParsed 是提取的实际逻辑，抽出来便于单测直接喂结构体。
//
// 三处形态都要认，缺一处就会有整类请求静默记 0：
//
//	openai:    choices[].message.reasoning_content / .reasoning
//	           （responses 协议也归到 choices 形态，见 transform 层）
//	anthropic: content[].type == "thinking" 的 .thinking，
//	           以及思考摘要形态的 .summary[].text
//	responses: output[].type == "reasoning" 的 .summary[].text 与 .content[].text
//
// 不认识的形态一律跳过而不是报错：这里跑在请求收尾路径上，任何 panic 或错误
// 都会影响落库，而"少算几段思考"是可接受的降级。
func reasoningCharsFromParsed(parsed map[string]any) int {
	total := 0

	// ---------- OpenAI Chat Completions 形态 ----------
	// message 与 delta 都看：非流式的聚合结果在 message，少数实现放 delta。
	if choices, ok := parsed["choices"].([]any); ok {
		for _, raw := range choices {
			choice, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			for _, field := range []string{"message", "delta"} {
				container, ok := choice[field].(map[string]any)
				if !ok {
					continue
				}
				total += runeCountOf(container["reasoning_content"])
				// 部分服务商（如 Synthetic）用 reasoning 而非 reasoning_content。
				total += runeCountOf(container["reasoning"])
			}
		}
	}

	// ---------- Anthropic Messages 形态 ----------
	if content, ok := parsed["content"].([]any); ok {
		for _, raw := range content {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			// thinking 块的正文就是思考文本；redacted_thinking 是被加密的，
			// 里面没有可读文本，只记它自己的字段会得到一段 base64，故不看。
			total += runeCountOf(block["thinking"])
			total += reasoningSummaryChars(block["summaries"])
			total += reasoningSummaryChars(block["summary"])
		}
	}

	// ---------- OpenAI Responses 形态 ----------
	if output, ok := parsed["output"].([]any); ok {
		for _, raw := range output {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if itemType, _ := item["type"].(string); itemType != "reasoning" {
				continue
			}
			total += reasoningSummaryChars(item["summary"])
			total += reasoningSummaryChars(item["summaries"])
			// reasoning 的详细内容有时放在 content[] 里（type=reasoning_text）。
			if content, ok := item["content"].([]any); ok {
				for _, rawContent := range content {
					part, ok := rawContent.(map[string]any)
					if !ok {
						continue
					}
					total += runeCountOf(part["text"])
				}
			}
		}
	}

	return total
}

// reasoningSummaryChars 累计 summary 数组/单对象里的 text 字符数。
//
// Anthropic 的 summary 是数组（summaries），Responses 的 summary 也是数组，
// 但也见过单对象的写法，因此两种都接。
func reasoningSummaryChars(value any) int {
	switch typed := value.(type) {
	case []any:
		total := 0
		for _, raw := range typed {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			total += runeCountOf(entry["text"])
		}
		return total
	case map[string]any:
		return runeCountOf(typed["text"])
	default:
		return 0
	}
}

// runeCountOf 把 JSON 里的字符串值按 rune 计数（一个中文/emoji 都算 1）。
//
// 用 rune 而不是字节：参考项目 anthropic 的注释写明按 UTF-8 rune 计，
// 而字节数会让中文思考的长度看起来是英文的四倍（同一个"字数"在不同语言下不可比）。
func runeCountOf(value any) int {
	text, ok := value.(string)
	if !ok || text == "" {
		return 0
	}
	return utf8.RuneCountInString(text)
}
