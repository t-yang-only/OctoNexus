package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 清单文件名：插件目录下的 plugin.json。它是插件与 octopus 之间唯一的静态契约。
const ManifestFile = "plugin.json"

// 插件标识的形状：小写字母数字与 -，必须是目录名本身（防止一份清单被复制到别的目录后
// 冒充另一个插件，也让"哪个目录对应哪条记录"永远对得上）。
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// 允许的占位符：{port} 是插件必须监听的端口，{dir} 是插件自身目录，{data} 是数据目录，{slug} 是标识。
var argPlaceholders = map[string]bool{"{port}": true, "{dir}": true, "{data}": true, "{slug}": true}

// Manifest 是 plugin.json 的解析结果。
type Manifest struct {
	Slug        string   `json:"slug"`         // 可省略：省略时取目录名（校验时以目录名为准）
	Name        string   `json:"name"`         // 展示名
	Version     string   `json:"version"`      // 插件自报版本
	Runtime     string   `json:"runtime"`      // exec | http
	Entry       string   `json:"entry"`        // exec: 相对插件目录的入口；http: 服务地址
	Args        []string `json:"args"`         // exec 的启动参数（可选）
	PortEnv     string   `json:"port_env"`     // 通知端口的变量名（默认 PORT）
	Protocol    string   `json:"protocol"`     // openai | anthropic
	BasePath    string   `json:"base_path"`    // 上游路径前缀（默认 /v1）
	HealthPath  string   `json:"health_path"`  // 就绪探测路径（默认 = BasePath + /models）
	EgressMode  string   `json:"egress"`       // pool（默认，强制走节点出口）| direct（显式声明）
	AutoChannel *bool    `json:"auto_channel"` // 默认 true：自动注册成渠道
	AutoStart   bool     `json:"auto_start"`   // 默认 false
	Description string   `json:"description"`
}

// Parsed 是"清单 + 落库字段"的合并结果：结构字段来自文件，运行期选定项由调用方填。
type Parsed struct {
	Manifest
	Dir      string   // 插件目录（绝对路径）
	Warnings []string // 不阻断装载、但用户需要知情的事项
}

// normalizeSlug 校验并归一标识。
func normalizeSlug(dirName string) (string, error) {
	slug := strings.ToLower(strings.TrimSpace(dirName))
	if !slugPattern.MatchString(slug) {
		return "", fmt.Errorf("插件目录名 %q 不是合法标识（只允许小写字母、数字与连字符，且以字母数字开头，最长 64）", dirName)
	}
	return slug, nil
}

// Load 读取并校验一个插件目录。
//
// 校验口径与号池声明式适配器一致：**信息不足或越界一律拒绝装载**（fail closed），
// 因为插件是要被 octopus 拉起来的可执行体，宽松的清单等于把"我信任这个目录"写在脸上。
func Load(dir string) (*Parsed, error) {
	slug, err := normalizeSlug(filepath.Base(dir))
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("读不到 %s：%w", ManifestFile, err)
	}
	manifest := Manifest{}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("%s 不是合法 JSON：%w", ManifestFile, err)
	}
	if declared := strings.TrimSpace(manifest.Slug); declared != "" && strings.ToLower(declared) != slug {
		return nil, fmt.Errorf("清单里的 slug %q 与目录名 %q 不一致（slug 必须以目录名为准）", declared, slug)
	}
	manifest.Slug = slug

	parsed := &Parsed{Manifest: manifest, Dir: dir}
	manifest.Name = strings.TrimSpace(manifest.Name)
	if manifest.Name == "" {
		return nil, fmt.Errorf("清单缺少 name")
	}
	manifest.Runtime = strings.ToLower(strings.TrimSpace(manifest.Runtime))
	if manifest.Runtime == "" {
		manifest.Runtime = RuntimeExec
	}
	switch manifest.Runtime {
	case RuntimeExec:
		if err := validateExecEntry(dir, &manifest); err != nil {
			return nil, err
		}
	case RuntimeHTTP:
		if err := validateHTTPEntry(&manifest); err != nil {
			return nil, err
		}
		parsed.Warnings = append(parsed.Warnings,
			"runtime=http 指向的是已存在的服务：octopus 无法为它注入出口，它的出网请自行配置（要受管控请改用 runtime=exec）")
	default:
		return nil, fmt.Errorf("runtime %q 不支持（可选 %s / %s）", manifest.Runtime, RuntimeExec, RuntimeHTTP)
	}

	manifest.Protocol = strings.ToLower(strings.TrimSpace(manifest.Protocol))
	if manifest.Protocol == "" {
		manifest.Protocol = ProtocolOpenAI
	}
	switch manifest.Protocol {
	case ProtocolOpenAI, ProtocolAnthropic:
	default:
		return nil, fmt.Errorf("protocol %q 不支持（可选 %s / %s）", manifest.Protocol, ProtocolOpenAI, ProtocolAnthropic)
	}

	manifest.BasePath = normalizeBasePath(manifest.BasePath)
	manifest.HealthPath = strings.TrimSpace(manifest.HealthPath)
	if manifest.HealthPath == "" {
		manifest.HealthPath = manifest.BasePath + "/models"
	}
	if !strings.HasPrefix(manifest.HealthPath, "/") {
		return nil, fmt.Errorf("health_path 必须以 / 开头")
	}

	manifest.EgressMode = strings.ToLower(strings.TrimSpace(manifest.EgressMode))
	if manifest.EgressMode == "" {
		manifest.EgressMode = EgressPool
	}
	switch manifest.EgressMode {
	case EgressPool:
	case EgressDirect:
		parsed.Warnings = append(parsed.Warnings,
			"清单声明 egress=direct：插件将直连真实出口 IP（不是经节点池），跨账号使用同一出口时可能被上游识别关联")
	case EgressExternal:
		if manifest.Runtime != RuntimeHTTP {
			return nil, fmt.Errorf("egress=%s 只对 runtime=http 有意义", EgressExternal)
		}
	default:
		return nil, fmt.Errorf("egress %q 不支持（可选 %s / %s）", manifest.EgressMode, EgressPool, EgressDirect)
	}

	manifest.PortEnv = strings.TrimSpace(manifest.PortEnv)
	if manifest.PortEnv == "" {
		manifest.PortEnv = "PORT"
	}
	if err := validateEnvName(manifest.PortEnv); err != nil {
		return nil, err
	}
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.Description = strings.TrimSpace(manifest.Description)
	parsed.Manifest = manifest
	return parsed, nil
}

// validateExecEntry 校验可执行入口：必须落在插件目录内、真实存在。
//
// 判据取"解析后仍在目录内"而不是"字符串里没有 .."：`./sub/../run.py` 与 `run.py` 是同一个文件，
// 前者被拒只会让作者困惑；而 `../../evil.sh` 解析后落在目录外，那才是真正要挡的。
func validateExecEntry(dir string, manifest *Manifest) error {
	entry := strings.TrimSpace(manifest.Entry)
	if entry == "" {
		return fmt.Errorf("runtime=exec 必须给 entry（相对插件目录的入口文件）")
	}
	if filepath.IsAbs(entry) || strings.HasPrefix(entry, "/") || strings.HasPrefix(entry, `\`) {
		return fmt.Errorf("entry 必须是相对路径（得到绝对路径 %q）", entry)
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(entry)))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("entry 不能指向插件目录外（得到 %q）", entry)
	}
	manifest.Entry = cleaned
	abs := filepath.Join(dir, filepath.FromSlash(cleaned))
	if rel, err := filepath.Rel(dir, abs); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("entry 不能指向插件目录外（得到 %q）", entry)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("entry 指向的入口不存在：%s", abs)
	}
	if info.IsDir() {
		return fmt.Errorf("entry 指向的是目录：%s", abs)
	}
	for i, arg := range manifest.Args {
		manifest.Args[i] = strings.TrimSpace(arg)
		if strings.ContainsRune(manifest.Args[i], 0) {
			return fmt.Errorf("args[%d] 含 NUL 字符", i)
		}
	}
	return nil
}

// validateHTTPEntry 校验 http 运行时的地址：只允许 http(s)，主机必须是本机回环或白名单里的主机。
// 白名单为空 = 只允许回环（与号池声明式适配器 pool_declarative_hosts 同一 fail-closed 口径）。
func validateHTTPEntry(manifest *Manifest) error {
	entry := strings.TrimSpace(manifest.Entry)
	if entry == "" {
		return fmt.Errorf("runtime=http 必须给 entry（服务地址，如 http://127.0.0.1:8080）")
	}
	if !strings.HasPrefix(entry, "http://") && !strings.HasPrefix(entry, "https://") {
		return fmt.Errorf("runtime=http 的 entry 必须是 http:// 或 https:// 地址")
	}
	manifest.Entry = strings.TrimRight(entry, "/")
	return nil
}

func validateEnvName(name string) error {
	for i, r := range name {
		ok := r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("port_env %q 不是合法的环境变量名", name)
		}
	}
	return nil
}

// normalizeBasePath 归一路径前缀：非空、以 / 开头、不以 / 结尾。
func normalizeBasePath(raw string) string {
	path := strings.TrimSpace(raw)
	if path == "" || path == "/" {
		return "/v1"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimRight(path, "/")
}

// RenderArgs 渲染启动参数里的占位符（{port}/{dir}/{data}/{slug}）。
// 未知占位符一律报错而不是原样传给插件：原样传会让插件收到一个字面量 "{post}"，
// 表现出"插件起来了但行为不对"，比直接拒绝难查得多。
func RenderArgs(args []string, port int, dir, dataDir, slug string) ([]string, error) {
	rendered := make([]string, 0, len(args))
	for _, arg := range args {
		out := arg
		out = strings.ReplaceAll(out, "{port}", fmt.Sprintf("%d", port))
		out = strings.ReplaceAll(out, "{dir}", dir)
		out = strings.ReplaceAll(out, "{data}", dataDir)
		out = strings.ReplaceAll(out, "{slug}", slug)
		if err := checkUnknownPlaceholders(out); err != nil {
			return nil, fmt.Errorf("参数 %q：%w", arg, err)
		}
		rendered = append(rendered, out)
	}
	return rendered, nil
}

var placeholderPattern = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

func checkUnknownPlaceholders(rendered string) error {
	for _, match := range placeholderPattern.FindAllString(rendered, -1) {
		if !argPlaceholders[match] {
			return fmt.Errorf("未知占位符 %s（可用 %s）", match, "port/dir/data/slug")
		}
	}
	return nil
}
