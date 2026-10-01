package pluginmarket

// 安装通路：上传文件 / GitHub 链接 → 解析 → 分派到既有能力（需求3）。
//
// 这一层刻意保持薄：下载只负责"把字节取回来"，安装只负责"按 kind 转交"。
// 真正的装载门禁、加密落库、白名单校验都在既有通路里，这里不重复实现 ——
// 两处各写一遍必然分叉，而分叉的后果是"市场说装好了、实际没生效"。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/t-yang-only/OctoNexus/internal/rhttp"
)

// MaxGitHubPluginBytes 是从 GitHub 下载插件的大小上限。
//
// 与 MaxPluginBytes 同一量级：插件描述文件就应该是 KB 级。
// 单独一个上限（而不是复用同一个常量）是因为这里多一层"别把仓库整个拉下来"的
// 考虑 —— 指错链接时 GitHub 会返回 HTML 页面，那个可能比 1MiB 大得多。
const MaxGitHubPluginBytes = 1 << 20

// gitHubRawPrefixes 是允许直接当 raw 内容取回的域名前缀。
//
// 只认这三个是刻意收紧：用户粘贴的链接形态各异（blob 页面、raw、API），
// 其余形态要么是 HTML 页面（解析必失败），要么需要额外一次 API 调用。
// 收到不认识的形态时**明确报错并说明要哪种**，而不是猜一个试着下载 ——
// 猜错时用户看到的是"不是合法 JSON"，根本想不到是链接形态不对。
var gitHubRawPrefixes = []string{
	"https://raw.githubusercontent.com/",
	"https://github.com/",
}

// normalizeGitHubURL 把用户粘贴的 GitHub 链接转成 raw 内容地址。
//
// 支持三种常见形态：
//
//	https://raw.githubusercontent.com/<owner>/<repo>/<ref>/<path>   原样返回
//	https://github.com/<owner>/<repo>/blob/<ref>/<path>             换成 raw
//	https://github.com/<owner>/<repo>/raw/<ref>/<path>              换成 raw
//	https://api.github.com/repos/<owner>/<repo>/contents/<path>     换成 raw（ref 缺省用 main）
//
// 其余一律报错。这里的判断全部基于路径形状，不看响应内容 ——
// "下载下来发现是 HTML 再报错"会让错误信息离真正的问题（链接形态）太远。
func normalizeGitHubURL(raw string) (string, error) {
	url := strings.TrimSpace(raw)
	if url == "" {
		return "", fmt.Errorf("链接为空")
	}
	if !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("只接受 https 链接（当前：%s）", url)
	}

	switch {
	case strings.HasPrefix(url, "https://raw.githubusercontent.com/"):
		return url, nil

	case strings.HasPrefix(url, "https://github.com/"):
		rest := strings.TrimPrefix(url, "https://github.com/")
		parts := strings.SplitN(rest, "/", 5)
		// <owner>/<repo>/<blob|raw>/<ref>/<path>
		if len(parts) == 5 && (parts[2] == "blob" || parts[2] == "raw") {
			return "https://raw.githubusercontent.com/" + parts[0] + "/" + parts[1] + "/" + parts[3] + "/" + parts[4], nil
		}
		return "", fmt.Errorf("无法识别的 GitHub 链接形态：%s（需要 …/blob/<分支>/<文件> 或 raw.githubusercontent.com 直链）", url)

	case strings.HasPrefix(url, "https://api.github.com/repos/"):
		rest := strings.TrimPrefix(url, "https://api.github.com/repos/")
		parts := strings.SplitN(rest, "/", 4)
		// <owner>/<repo>/contents/<path...>
		if len(parts) == 4 && parts[2] == "contents" {
			return "https://raw.githubusercontent.com/" + parts[0] + "/" + parts[1] + "/HEAD/" + parts[3], nil
		}
		return "", fmt.Errorf("无法识别的 GitHub API 链接形态：%s", url)
	}

	for _, prefix := range gitHubRawPrefixes {
		if strings.HasPrefix(url, prefix) {
			continue
		}
	}
	return "", fmt.Errorf("只接受 GitHub 链接（raw.githubusercontent.com / github.com / api.github.com），当前：%s", url)
}

// FetchFromGitHub 下载并解析一个 GitHub 上的插件。
//
// 走 rhttp 的 Direct→Proxy 回退与项目其它外呼一致；失败时把两种尝试都试过，
// 错误信息里带上 HTTP 状态，便于区分"链接不存在(404)"与"网络不通"。
func FetchFromGitHub(url string) (Plugin, error) {
	rawURL, err := normalizeGitHubURL(url)
	if err != nil {
		return Plugin{}, err
	}
	body, err := fetchLimited(rawURL, MaxGitHubPluginBytes)
	if err != nil {
		return Plugin{}, err
	}
	return Parse(body)
}

// fetchLimited 取回 URL 内容并限制大小。
//
// 限制用 io.LimitReader 而不是先全读再判：后者会把超大响应整个读进内存，
// 而这个入口的内容来自用户粘贴的链接，本就不该被信任。
//
// 直连失败时试一次代理，与项目其它外呼（price/update）同款。**每次尝试都新建请求**：
// http.Request 不是可重放的对象 —— Body 是 io.Reader，读一次就耗尽；复用同一个 req
// 做第二次 Do 时，连接状态与上下文都带着第一次的痕迹，失败模式难以预测。
func fetchLimited(url string, limit int64) ([]byte, error) {
	do := func(useProxy bool) (*http.Response, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "octopus-plugin-market/1")
		client, err := rhttp.Direct()
		if useProxy {
			client, err = rhttp.Proxy()
		}
		if err != nil {
			// 客户端构造失败时退回到一个不带代理的裸客户端：取不到内容比整个功能不可用要好。
			client = &http.Client{Timeout: 30 * time.Second}
		}
		return client.Do(req)
	}

	resp, err := do(false)
	if err != nil {
		// 直连失败时试一次代理：本机访问 GitHub 可能因网络环境需要出口。
		if proxyResp, proxyErr := do(true); proxyErr == nil {
			resp = proxyResp
		}
	}
	if resp == nil {
		return nil, fmt.Errorf("下载失败：%v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载失败：HTTP %d（链接不存在或仓库私有）", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败：%w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("内容超过 %d 字节：这看起来不是插件描述文件", limit)
	}
	return body, nil
}

// InstallResult 是一次安装的结论，回给界面说明"装成了什么"。
type InstallResult struct {
	Kind PluginKind `json:"kind"`
	Name string     `json:"name"`
	// Target 描述落到了哪个既有实体上（如 "collector source 12" / "pool adapter custom-x"），
	// 让用户在界面上能顺着一句话找到刚装的东西。
	Target string `json:"target"`
	// Pack / Spec 是转交出去的载荷原文（不含凭据），供界面预览与排查。
	Pack json.RawMessage `json:"pack,omitempty"`
	Spec json.RawMessage `json:"spec,omitempty"`
}

// Prepare 把解析后的插件转成"可以交给既有通路"的载荷，但不落库。
//
// 分开 Prepare 与落库两步是有意的：调用方（handler）要先做装载门禁与用户确认，
// 再决定是否真的写入。合一步会逼着"下载即落库"，那种接口没法给用户反悔的机会。
func (p Plugin) Prepare() (InstallResult, error) {
	switch p.Kind {
	case KindBalance:
		var spec BalanceSpec
		if err := json.Unmarshal(p.Spec, &spec); err != nil {
			return InstallResult{}, fmt.Errorf("balance 插件 spec 解析失败：%w", err)
		}
		if len(spec.Hosts) == 0 {
			return InstallResult{}, fmt.Errorf("balance 插件必须声明 hosts（要连哪些站点）")
		}
		if len(spec.Read) == 0 {
			return InstallResult{}, fmt.Errorf("balance 插件必须声明 read（怎么读余额）")
		}
		return InstallResult{Kind: KindBalance, Name: p.DisplayName(), Pack: p.Spec}, nil

	case KindPool:
		var spec PoolSpec
		if err := json.Unmarshal(p.Spec, &spec); err != nil {
			return InstallResult{}, fmt.Errorf("pool 插件 spec 解析失败：%w", err)
		}
		if strings.TrimSpace(spec.BaseURL) == "" {
			return InstallResult{}, fmt.Errorf("pool 插件必须声明 base_url")
		}
		if strings.TrimSpace(spec.Kind) == "" {
			return InstallResult{}, fmt.Errorf("pool 插件必须声明 kind（适配器标识）")
		}
		return InstallResult{Kind: KindPool, Name: p.DisplayName(), Spec: p.Spec}, nil
	}
	return InstallResult{}, fmt.Errorf("不支持的 kind：%s", string(p.Kind))
}
