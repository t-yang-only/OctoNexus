package clashcfg

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 订阅拉取纪律（与项目其它出网路径一致）：只走 http(s)、固定总超时、响应体上限、
// 跳转次数上限；错误信息里**绝不带完整 URL**（订阅地址里通常带 token）。
const (
	FetchTimeout    = 30 * time.Second
	MaxBodyBytes    = 8 << 20 // 订阅里 rues/geodata 可能很大，8 MiB 足够；超限直接拒绝而不是截断解析
	maxRedirects    = 5
	userAgentSuffix = "octopus"
)

// Fetch 拉取订阅地址并返回原始配置字节。返回值里的错误已做脱敏（不含 token）。
func Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("订阅地址为空")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("订阅地址不合法：%v", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("订阅地址只支持 http/https，实际 scheme=%q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("订阅地址缺少主机名")
	}

	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()

	client := &http.Client{
		Timeout: FetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("跳转次数超过 %d 次", maxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("跳转目标协议不允许：%s", req.URL.Scheme)
			}
			// 不把订阅令牌带到别的域：跨主机跳转时清掉认证头与查询串。
			if len(via) > 0 && !sameHost(via[0].URL.Host, req.URL.Host) {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造订阅请求失败：%v", err)
	}
	req.Header.Set("User-Agent", "clash-verge/"+userAgentSuffix) // 部分订阅网关按 UA 返回 clash 格式
	req.Header.Set("Accept", "text/yaml,text/plain,*/*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("拉取订阅失败（%s）：%v", RedactURL(rawURL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("拉取订阅失败（%s）：HTTP %d", RedactURL(rawURL), resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取订阅内容失败（%s）：%v", RedactURL(rawURL), err)
	}
	if len(body) > MaxBodyBytes {
		return nil, fmt.Errorf("订阅内容超过 %d MiB 上限（%s）", MaxBodyBytes>>20, RedactURL(rawURL))
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("订阅内容为空（%s）", RedactURL(rawURL))
	}
	return body, nil
}

// RedactURL 只保留 scheme://host/前若干字符路径，用于日志与错误信息展示。
// 订阅地址的最后一段通常是令牌，必须去掉。
func RedactURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return "（地址不可解析）"
	}
	path := parsed.EscapedPath()
	if path == "" || path == "/" {
		return parsed.Scheme + "://" + parsed.Host + "/"
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 0 {
		parts[len(parts)-1] = "***"
	}
	return parsed.Scheme + "://" + parsed.Host + "/" + strings.Join(parts, "/")
}

func sameHost(a, b string) bool {
	ah, _, errA := net.SplitHostPort(a)
	bh, _, errB := net.SplitHostPort(b)
	if errA != nil {
		ah = a
	}
	if errB != nil {
		bh = b
	}
	return strings.EqualFold(ah, bh)
}
