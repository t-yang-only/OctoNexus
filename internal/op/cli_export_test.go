package op

import (
	"strings"
	"testing"
)

// CLI 配置导出（吸收上游 lingyuins/octopus 的 CLI Config Export）。
//
// 这套用例守的是"用户粘上去就能用"：每个客户端的易错点都在正文里被明确覆盖——
// 少了 /v1、用错的环境变量名、把网关分组名当成上游模型名。这些错配客户端只会报
// 含糊的连接失败，靠用户自己排查代价很高。

func buildFor(t *testing.T, target CLITarget, base, key, model string) CLIExport {
	t.Helper()
	export, err := CLIExportBuild(target, base, key, model)
	if err != nil {
		t.Fatalf("build %s: %v", target, err)
	}
	return export
}

// Claude Code：必须用 ANTHROPIC_AUTH_TOKEN（用 ANTHROPIC_API_KEY 不会发 Authorization 头）。
func TestCLIExportClaudeCodeUsesAuthToken(t *testing.T) {
	export := buildFor(t, CLITargetClaudeCode, "https://gw.example.com", "sk-abc", "claude-sonnet")
	for _, want := range []string{"ANTHROPIC_BASE_URL=https://gw.example.com", "ANTHROPIC_AUTH_TOKEN=sk-abc", "ANTHROPIC_MODEL=claude-sonnet"} {
		if !strings.Contains(export.Content, want) {
			t.Fatalf("缺少 %q:\n%s", want, export.Content)
		}
	}
	if strings.Contains(export.Content, "ANTHROPIC_API_KEY=") {
		t.Fatalf("不该出现 ANTHROPIC_API_KEY（Claude Code 对 AUTH_TOKEN 才发 Authorization 头）:\n%s", export.Content)
	}
}

// Codex：base_url 必须以 /v1 结尾，且给的是 config.toml 形态而不是环境变量。
func TestCLIExportCodexUsesTomlWithV1Suffix(t *testing.T) {
	export := buildFor(t, CLITargetCodex, "https://gw.example.com", "sk-abc", "gpt-5")
	if export.Format != "toml" || export.Filename != "config.toml" {
		t.Fatalf("Codex 应给 config.toml 形态: %+v", export)
	}
	for _, want := range []string{`base_url = "https://gw.example.com/v1"`, `model = "gpt-5"`, "env_key", "wire_api"} {
		if !strings.Contains(export.Content, want) {
			t.Fatalf("缺少 %q:\n%s", want, export.Content)
		}
	}
	// key 不该写进配置文件（会被一起提交进版本库）。
	if strings.Contains(export.Content, "sk-abc") {
		t.Fatalf("Codex 的 key 应走环境变量而不是写进 toml:\n%s", export.Content)
	}
	// 但步骤里要告诉用户怎么设。
	joined := strings.Join(export.Steps, "\n")
	if !strings.Contains(joined, "sk-abc") {
		t.Fatalf("步骤里应给出 key 的设置方式:\n%s", joined)
	}
}

// 所有目标都必须把 model 带出来：只给 base URL 与 key，用户还得回去翻面板才知道请求哪个模型。
func TestCLIExportAlwaysCarriesModel(t *testing.T) {
	targets := []CLITarget{
		CLITargetClaudeCode, CLITargetCodex, CLITargetGeminiCLI,
		CLITargetCherryStudio, CLITargetOpenAICompatible,
	}
	for _, target := range targets {
		export := buildFor(t, target, "https://gw.example.com", "sk-abc", "my-group")
		if !strings.Contains(export.Content, "my-group") {
			t.Fatalf("%s 的导出内容应包含模型名:\n%s", target, export.Content)
		}
		if export.Title == "" || export.Description == "" {
			t.Fatalf("%s 应有标题与说明: %+v", target, export)
		}
		if len(export.Steps) == 0 {
			t.Fatalf("%s 应给出落地步骤", target)
		}
	}
}

// base URL 末尾的斜杠要归一化，否则会拼出 //v1 这种双斜杠。
func TestCLIExportTrimsTrailingSlash(t *testing.T) {
	export := buildFor(t, CLITargetCodex, "https://gw.example.com/", "sk-abc", "m")
	if strings.Contains(export.Content, "//v1") {
		t.Fatalf("末尾斜杠未归一化:\n%s", export.Content)
	}
	if !strings.Contains(export.Content, `base_url = "https://gw.example.com/v1"`) {
		t.Fatalf("应拼出单斜杠的 /v1:\n%s", export.Content)
	}
}

// 缺参数与非法输入必须被挡下来，且理由要可执行。
func TestCLIExportValidation(t *testing.T) {
	if _, err := CLIExportBuild(CLITargetCodex, "", "k", "m"); err == nil {
		t.Fatalf("空 base_url 应被拒绝")
	}
	if _, err := CLIExportBuild(CLITargetCodex, "https://x", "", "m"); err == nil {
		t.Fatalf("空 key 应被拒绝")
	}
	if _, err := CLIExportBuild(CLITargetCodex, "https://x", "k", ""); err == nil {
		t.Fatalf("空 model 应被拒绝")
	}
	// 少了协议头是最常见的粘贴错误，必须在入口就报出来（客户端只会说连接失败）。
	_, err := CLIExportBuild(CLITargetCodex, "gw.example.com", "k", "m")
	if err == nil {
		t.Fatalf("缺协议头应被拒绝")
	}
	if !strings.Contains(err.Error(), "http") {
		t.Fatalf("缺协议头的错误信息应指出该写 http(s)://，实得: %v", err)
	}
	if _, err := CLIExportBuild("vibes", "https://x", "k", "m"); err == nil {
		t.Fatalf("非法目标应被拒绝")
	}
}

// 目标合法性校验与导出分支必须一致：IsValidCLITarget 说合法的，导出就必须能成功。
func TestCLIExportTargetValidityMatchesImplementation(t *testing.T) {
	targets := []string{"claude_code", "codex", "gemini_cli", "cherry_studio", "openai_compatible"}
	for _, target := range targets {
		if !IsValidCLITarget(target) {
			t.Fatalf("%s 应被认作合法目标", target)
		}
		if _, err := CLIExportBuild(CLITarget(target), "https://x", "k", "m"); err != nil {
			t.Fatalf("%s 合法但导出失败: %v", target, err)
		}
	}
	for _, bad := range []string{"", "vscode", "CLAUDE_CODE", "claude"} {
		if IsValidCLITarget(bad) {
			t.Fatalf("%q 不该被认作合法目标", bad)
		}
	}
}

// Cherry Studio 是图形界面，给的是字段清单而不是可粘贴脚本。
func TestCLIExportCherryStudioGivesFields(t *testing.T) {
	export := buildFor(t, CLITargetCherryStudio, "https://gw.example.com", "sk-abc", "m1")
	if export.Format != "fields" {
		t.Fatalf("Cherry Studio 应给字段清单形态: %+v", export)
	}
	for _, want := range []string{"https://gw.example.com/v1", "sk-abc", "m1"} {
		if !strings.Contains(export.Content, want) {
			t.Fatalf("缺少 %q:\n%s", want, export.Content)
		}
	}
}
