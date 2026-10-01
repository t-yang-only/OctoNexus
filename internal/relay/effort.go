package relay

// 思考等级（reasoning effort）的取值与解析。
//
// # 这个文件为什么存在（上游 issue #409）
//
// 上游报告的缺陷：渠道参数覆盖把思考等级改成 low、实测已生效，但日志页的徽章
// 仍显示客户端请求里的 high —— 界面上没有任何地方能确认覆盖是否生效，
// 用户第一反应都是"覆盖没生效"。
//
// 两件事共同造成它：
//
//  1. **只解析了一种形态**。入站只取 `reasoning_effort`（OpenAI Chat 口径）。
//     Anthropic Messages 客户端带的思考等级在 `thinking` 或 `output_config.effort`，
//     于是这类请求的档位根本进不了日志。
//  2. **记的是入站值，不是生效值**。参数覆盖发生在出站阶段
//     （applyChannelConfig），而存进请求状态的是读入客户端请求时解析的值。
//     覆盖之后没有任何地方回填生效值。
//
// # 修法
//
// 用一个函数统一解析两种形态，并在**出站请求定型后**再解析一次、
// 把生效值回填进请求状态。日志/画像读到的 thenceforth 是"真正发出去的那个档"。
//
// # 为什么值得单列一个文件
//
// 这个解析会被入站和出站两处调用。两处各写一遍必然分叉
// （上游的 bug 本质上就是"入站记一份、出站改另一份"），共用一个函数才一致。

import (
	"encoding/json"
	"strings"
)

// 已知的思考等级取值。**不做白名单校验**，只用于归一化大小写：
// 各家取值并不统一（low/medium/high、xhigh、minimal、none……），
// 上游将来新增档位时白名单会把它挡掉，那会让"已生效"显示成"未知"。
//
// 这里只做 TrimSpace + 小写化：同一档位的大小写差异（"High" vs "high"）
// 不该在界面上显示成两个值。

// parseThinkingEffort 从请求体里解析思考等级，返回归一化后的档位；没有则为空串。
//
// 覆盖两种客户端形态：
//
//	OpenAI Chat Completions : "reasoning_effort": "high"
//	Anthropic Messages      : "thinking": {"type": "enabled", "budget_tokens": N}
//	                          或 "output_config": {"effort": "low"}
//
// Anthropic 的 `thinking` 是布尔语义（开/关 + 预算），不是档位；
// 只有它显式关闭（type=disabled）时才有明确含义 —— 归为 "none"。
// 其余情况优先取 output_config.effort 的具体档位。
func parseThinkingEffort(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var envelope struct {
		ReasoningEffort string `json:"reasoning_effort"`
		Thinking        struct {
			Type string `json:"type"`
		} `json:"thinking"`
		OutputConfig struct {
			Effort string `json:"effort"`
		} `json:"output_config"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		// 解析失败不当成错误：正文可能不是 JSON（透传的其它协议），
		// 此时没有档位可显示，返回空串即可。
		return ""
	}

	effort := strings.TrimSpace(envelope.ReasoningEffort)
	if effort == "" {
		effort = strings.TrimSpace(envelope.OutputConfig.Effort)
	}
	if effort == "" && envelope.Thinking.Type != "" {
		// thinking 只在显式禁用时有确定含义。
		switch strings.ToLower(strings.TrimSpace(envelope.Thinking.Type)) {
		case "disabled":
			effort = "none"
		case "enabled":
			// 开了但没给档位：Anthropic 的 budget_tokens 不映射到 low/high，
			// 如实报"enabled"而不是猜一个档。
			effort = "enabled"
		}
	}
	return strings.ToLower(effort)
}

// normalizeEffort 归一化档位显示值：去空白 + 小写。
// 单独抽出来是为了让测试能直接钉住归一化规则（大小写/空白不该产生两个显示值）。
func normalizeEffort(effort string) string {
	return strings.ToLower(strings.TrimSpace(effort))
}
