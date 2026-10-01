package op

import (
	"fmt"
	"strings"
)

// CLI 配置导出（吸收上游 lingyuins/octopus 的 CLI Config Export）。
//
// 解决的问题：把网关接好之后，还得知道"客户端该怎么配"——base URL 填什么、key 填哪把、
// model 填什么。这三样在面板上分散在三个地方，而客户端各自要的格式又不同。
// 这里把它们组装成可以直接粘贴的片段。
//
// 比上游多两样东西，都是实际配过之后才知道缺的：
//   - **带上 model（分组名）**：只给 base URL 与 key 是不够的——客户端还得知道请求哪个模型名，
//     而本项目的模型名就是分组名，不写清楚用户只能回去翻面板。
//   - **给出配置文件形态**：只给环境变量在 Codex 这类用 config.toml 的工具上不够用。

// CLITarget 是导出目标。
type CLITarget string

const (
	CLITargetClaudeCode       CLITarget = "claude_code"
	CLITargetCodex            CLITarget = "codex"
	CLITargetGeminiCLI        CLITarget = "gemini_cli"
	CLITargetCherryStudio     CLITarget = "cherry_studio"
	CLITargetOpenAICompatible CLITarget = "openai_compatible"
)

// IsValidCLITarget 目标是否受支持。
func IsValidCLITarget(value string) bool {
	switch CLITarget(value) {
	case CLITargetClaudeCode, CLITargetCodex, CLITargetGeminiCLI, CLITargetCherryStudio, CLITargetOpenAICompatible:
		return true
	}
	return false
}

// CLIExport 是一次导出的结果。
type CLIExport struct {
	Tool        string   `json:"tool"`
	Title       string   `json:"title"`
	Format      string   `json:"format"`      // env / toml / json / fields
	Filename    string   `json:"filename"`    // 建议落盘的文件名；fields 形态为空
	Content     string   `json:"content"`     // 可直接粘贴的内容
	Description string   `json:"description"` // 一句话说明
	Steps       []string `json:"steps"`       // 落地步骤（放哪、怎么生效）
	Notes       []string `json:"notes"`       // 口径提醒（易错点）
}

// CLIExportBuild 组装一个导出片段。
//
// baseURL 是**网关的转发口地址**（不是管理面），apiKey 是网关签发的 Key，
// model 是客户端要请求的分组名。三者都由调用方给出——网关无法可靠地推断自己的公网地址
// （反代、端口映射都可能改变它），猜错会让用户配出一个连不上的客户端。
func CLIExportBuild(target CLITarget, baseURL, apiKey, model string) (CLIExport, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	key := strings.TrimSpace(apiKey)
	name := strings.TrimSpace(model)
	if base == "" {
		return CLIExport{}, fmt.Errorf("base_url is required")
	}
	if key == "" {
		return CLIExport{}, fmt.Errorf("api_key is required")
	}
	if name == "" {
		return CLIExport{}, fmt.Errorf("model is required")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		// 少了协议头是最常见的粘贴错误，且客户端报错通常很含糊（只说连接失败）。
		return CLIExport{}, fmt.Errorf("base_url must start with http:// or https://")
	}

	switch target {
	case CLITargetClaudeCode:
		return claudeCodeExport(base, key, name), nil
	case CLITargetCodex:
		return codexExport(base, key, name), nil
	case CLITargetGeminiCLI:
		return geminiCLIExport(base, key, name), nil
	case CLITargetCherryStudio:
		return cherryStudioExport(base, key, name), nil
	case CLITargetOpenAICompatible:
		return openAICompatibleExport(base, key, name), nil
	default:
		return CLIExport{}, fmt.Errorf("unsupported tool: %s", target)
	}
}

func claudeCodeExport(base, key, model string) CLIExport {
	content := fmt.Sprintf(`export ANTHROPIC_BASE_URL=%s
export ANTHROPIC_AUTH_TOKEN=%s
export ANTHROPIC_MODEL=%s
export ANTHROPIC_SMALL_FAST_MODEL=%s`, base, key, model, model)
	return CLIExport{
		Tool: "claude_code", Title: "Claude Code", Format: "env", Filename: "octonexus-claude.sh",
		Content:     content,
		Description: "把这三行放进 shell 启动文件（或当前会话直接 source），Claude Code 就会走网关。",
		Steps: []string{
			"保存为 octonexus-claude.sh，执行 source octonexus-claude.sh",
			"想长期生效就把这几行追加到 ~/.bashrc 或 ~/.zshrc",
			"运行 claude，用 /status 确认 base URL 已经指向网关",
		},
		Notes: []string{
			"用 ANTHROPIC_AUTH_TOKEN（不是 ANTHROPIC_API_KEY）：Claude Code 对前者才会发 Authorization 头。",
			"ANTHROPIC_MODEL 填的是网关的分组名，不是上游真实模型名。",
			"这段脚本里有明文密钥：别提交进版本库，也别贴进工单/聊天记录。",
		},
	}
}

func codexExport(base, key, model string) CLIExport {
	content := fmt.Sprintf(`model_provider = "octonexus"
model = "%s"

[model_providers.octonexus]
name = "OctoNexus"
base_url = "%s/v1"
env_key = "OCTONEXUS_API_KEY"
wire_api = "chat"`, model, base)
	return CLIExport{
		Tool: "codex", Title: "Codex CLI", Format: "toml", Filename: "config.toml",
		Content:     content,
		Description: "把内容合并进 ~/.codex/config.toml，并设置环境变量 OCTONEXUS_API_KEY。",
		Steps: []string{
			"把上面内容合并进 ~/.codex/config.toml（已有 [model_providers] 时追加块，不要重复写顶层键）",
			fmt.Sprintf("设置环境变量：export OCTONEXUS_API_KEY=%s", key),
			"运行 codex，模型名应显示为网关的分组名",
		},
		Notes: []string{
			"base_url 必须以 /v1 结尾：Codex 会在后面直接拼 /chat/completions。",
			"wire_api 用 chat（本网关的 OpenAI 兼容口是 Chat Completions）。",
			"key 写在环境变量里而不是配置文件里，避免它被一起提交进版本库。",
		},
	}
}

func geminiCLIExport(base, key, model string) CLIExport {
	content := fmt.Sprintf(`export GEMINI_API_KEY=%s
export GOOGLE_GEMINI_BASE_URL=%s
export GEMINI_MODEL=%s`, key, base, model)
	return CLIExport{
		Tool: "gemini_cli", Title: "Gemini CLI", Format: "env", Filename: "octonexus-gemini.sh",
		Content:     content,
		Description: "设置环境变量后 Gemini CLI 会走网关。",
		Steps: []string{
			"保存为 octonexus-gemini.sh，执行 source octonexus-gemini.sh",
			"运行 gemini 验证连通性",
		},
		Notes: []string{
			"GEMINI_API_KEY 填的是网关签发的 Key，不是 Google 的密钥。",
			"不同版本对 base URL 变量名可能不同（GOOGLE_GEMINI_BASE_URL / GEMINI_BASE_URL），配不上时两个都设一遍。",
			"这段脚本里有明文密钥：别提交进版本库，也别贴进工单/聊天记录。",
		},
	}
}

func cherryStudioExport(base, key, model string) CLIExport {
	content := fmt.Sprintf(`API 类型：OpenAI
API 地址：%s/v1
API 密钥：%s
模型名称：%s`, base, key, model)
	return CLIExport{
		Tool: "cherry_studio", Title: "Cherry Studio", Format: "fields", Filename: "",
		Content:     content,
		Description: "Cherry Studio 是图形界面，按下面的字段逐项填即可。",
		Steps: []string{
			"设置 → 模型服务 → 添加提供商，API 类型选 OpenAI",
			"API 地址填 %s/v1，API 密钥填网关签发的 Key",
			"在模型列表里手工添加「%s」这个模型名（网关的分组名不会自动同步）",
		},
		Notes: []string{
			"地址要带 /v1：Cherry Studio 不会自动补。",
			"模型名必须与网关的分组名逐字一致；不确定时用面板的「模型映射」配一条通配规则。",
			"这里的密钥是明文展示的：填完就别把截图发出去。",
		},
	}
}

func openAICompatibleExport(base, key, model string) CLIExport {
	content := fmt.Sprintf(`export OPENAI_BASE_URL=%s/v1
export OPENAI_API_KEY=%s
# 请求时 model 参数填：%s`, base, key, model)
	return CLIExport{
		Tool: "openai_compatible", Title: "OpenAI 兼容客户端", Format: "env", Filename: "octonexus-openai.sh",
		Content:     content,
		Description: "任何 OpenAI 兼容的 SDK / 工具都可以这样配。",
		Steps: []string{
			"source 这段脚本，或用等价的配置项填进你的客户端",
			"请求路径用 /v1/chat/completions，model 参数填分组名",
		},
		Notes: []string{
			"本网关同时提供 /v1/responses 与 /v1/messages，按客户端支持的协议选。",
			"base URL 带 /v1，路径不要再重复写 /v1。",
			"这段脚本里有明文密钥：别提交进版本库，也别贴进工单/聊天记录。",
		},
	}
}
