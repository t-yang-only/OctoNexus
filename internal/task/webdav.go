package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// T-backup-001 WebDAV 云备份。
//
// 为什么放在 task 包：WebDAV 上传要读设置（op）又要发通知（notify），
// 而 op 不能依赖 notify（notify 经 rhttp 反向依赖 op），所以投递类逻辑一律归 task。
//
// 只做三件事：导出转储 → PUT 到 WebDAV → 按保留份数清理旧文件。
// 内容与面板的「导出」**完全同构**（同一个 op.DBExportAll 的 JSON），
// 于是恢复时可以直接把文件喂给现有的导入接口，不需要另一套解析。
//
// 口令只从环境变量读，不进设置表：设置表会随导出转储与备份一起流转，
// 把口令写进去等于把它复制到每一个备份里。

// WebDAVConfig 是一次上传所需的全部配置。
type WebDAVConfig struct {
	URL      string
	Username string
	Password string
	Keep     int
}

// WebDAVConfigFromSettings 从设置表 + 环境变量组装配置。
//
// 三处「没配好」的情形都返回明确的错误而不是空配置：
// 定时任务静默什么都不做是最难排查的一类故障（用户以为在备份，实际一份都没有）。
func WebDAVConfigFromSettings() (WebDAVConfig, error) {
	base, err := op.SettingGetString(model.SettingKeyWebDAVURL)
	if err != nil {
		return WebDAVConfig{}, fmt.Errorf("read webdav url: %w", err)
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return WebDAVConfig{}, fmt.Errorf("webdav url is not configured")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return WebDAVConfig{}, fmt.Errorf("webdav url must start with http:// or https://")
	}

	username, _ := op.SettingGetString(model.SettingKeyWebDAVUsername)
	keep := 0
	if raw, err := op.SettingGetString(model.SettingKeyWebDAVKeep); err == nil {
		if parsed, convErr := parseIntSetting(raw); convErr == nil {
			keep = parsed
		}
	}
	return WebDAVConfig{
		URL:      base,
		Username: strings.TrimSpace(username),
		// 只从环境变量读：故意不提供设置项，避免口令进库。
		Password: strings.TrimSpace(os.Getenv("OCTOPUS_WEBDAV_PASSWORD")),
		Keep:     keep,
	}, nil
}

// buildWebDAVRequest 组装一个带 Basic 认证的请求。
// 用户名与口令都为空时不带 Authorization 头（部分服务允许匿名或把令牌写在 URL 里）。
func buildWebDAVRequest(ctx context.Context, method, target string, body []byte, cfg WebDAVConfig) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	if cfg.Username != "" || cfg.Password != "" {
		req.SetBasicAuth(cfg.Username, cfg.Password)
	}
	return req, nil
}

// webDAVClient 返回上传用客户端。
//
// 刻意**不走代理设置**：备份目标是用户自己的网盘，直连比经由上游代理更可靠
// （代理节点失效会让备份静默中断，而备份恰恰是最后一道防线）。
func webDAVClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute}
}

// webDAVRemoteName 生成远端文件名。
//
// 用固定前缀 + UTC 时间戳：固定前缀让清理规则只需匹配这一种形态，
// 不会误删用户放在同一目录里的其它文件。
func webDAVRemoteName(at time.Time) string {
	return fmt.Sprintf("octopus-backup-%s.json", at.UTC().Format("20060102-150405"))
}

// WebDAVUpload 导出当前数据库并上传到 WebDAV，返回远端文件名。
func WebDAVUpload(ctx context.Context, cfg WebDAVConfig, at time.Time) (string, error) {
	dump, err := op.DBExportAll(ctx)
	if err != nil {
		return "", fmt.Errorf("export database: %w", err)
	}
	body, err := marshalDBDump(dump)
	if err != nil {
		return "", fmt.Errorf("encode dump: %w", err)
	}

	base := strings.TrimRight(cfg.URL, "/")
	name := webDAVRemoteName(at)
	target := base + "/" + url.PathEscape(name)

	req, err := buildWebDAVRequest(ctx, http.MethodPut, target, body, cfg)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := webDAVClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("upload to webdav: %w", err)
	}
	defer resp.Body.Close()
	// 读掉响应体：否则连接无法复用，且部分服务端会因未读而报错。
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("webdav upload failed: HTTP %d", resp.StatusCode)
	}
	return name, nil
}

// extractHrefs 从 WebDAV 的 PROPFIND 响应里取出所有 href 的值。
//
// 用字符串扫描而不是 XML 解析：各服务端的命名空间前缀差异很大（d: / D: / 无前缀），
// 而我们要的只是路径字符串，按标记边界切分对三种写法都成立，且不引入 XML 依赖。
func extractHrefs(body string) []string {
	out := make([]string, 0)
	for _, openTag := range []string{"<d:href>", "<D:href>", "<href>"} {
		closeTag := "</" + openTag[1:]
		rest := body
		for {
			start := strings.Index(rest, openTag)
			if start < 0 {
				break
			}
			rest = rest[start+len(openTag):]
			end := strings.Index(rest, closeTag)
			if end < 0 {
				break
			}
			if value := strings.TrimSpace(rest[:end]); value != "" {
				out = append(out, value)
			}
			rest = rest[end+len(closeTag):]
		}
	}
	return out
}

// WebDAVList 列出远端目录下由本程序产生的备份文件（只返回文件名，按名字升序）。
func WebDAVList(ctx context.Context, cfg WebDAVConfig) ([]string, error) {
	base := strings.TrimRight(cfg.URL, "/") + "/"
	req, err := buildWebDAVRequest(ctx, "PROPFIND", base, nil, cfg)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")

	resp, err := webDAVClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("list webdav: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("webdav list failed: HTTP %d", resp.StatusCode)
	}

	names := make([]string, 0)
	for _, href := range extractHrefs(string(body)) {
		decoded, err := url.PathUnescape(href)
		if err != nil {
			decoded = href
		}
		name := path.Base(strings.TrimRight(decoded, "/"))
		if strings.HasPrefix(name, "octopus-backup-") && strings.HasSuffix(name, ".json") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// WebDAVPrune 按保留份数删除最旧的备份，返回被删除的文件名。
//
// keep <= 0 表示不清理 —— 这是「不替用户决定删东西」的默认，而不是"删光"。
// 删除失败只记录在返回的错误里，调用方按告警处理：清理失败不该让本次备份算失败
// （备份本身已经成功了）。
func WebDAVPrune(ctx context.Context, cfg WebDAVConfig) ([]string, error) {
	if cfg.Keep <= 0 {
		return nil, nil
	}
	names, err := WebDAVList(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if len(names) <= cfg.Keep {
		return nil, nil
	}

	base := strings.TrimRight(cfg.URL, "/")
	removed := make([]string, 0, len(names)-cfg.Keep)
	var firstErr error
	for _, name := range names[:len(names)-cfg.Keep] {
		req, err := buildWebDAVRequest(ctx, http.MethodDelete, base+"/"+url.PathEscape(name), nil, cfg)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		resp, err := webDAVClient().Do(req)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("delete %s: %w", name, err)
			}
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if firstErr == nil {
				firstErr = fmt.Errorf("delete %s: HTTP %d", name, resp.StatusCode)
			}
			continue
		}
		removed = append(removed, name)
	}
	return removed, firstErr
}

// marshalDBDump 把转储编码成 JSON。
//
// 独立成函数是为了让「上传的内容」与「面板导出接口返回的内容」在测试里可以逐字节比对：
// 两者若漂移，用户在面板上验证过的备份格式就与他实际拿到的那份不一样。
func marshalDBDump(dump *model.DBDump) ([]byte, error) {
	return json.Marshal(dump)
}

// WebDAVDownload 按文件名从远端取回备份内容。
//
// 文件名只允许本程序自己产生的那种形态（固定前缀 + 时间戳 + .json）：
// 这个函数被"恢复"这条危险路径调用，放开任意文件名等于让一个拼错的参数
// 去下载目录里的其它东西。
func WebDAVDownload(ctx context.Context, cfg WebDAVConfig, name string) ([]byte, error) {
	if !isOwnBackupName(name) {
		return nil, fmt.Errorf("不是本程序产生的备份文件名: %s", name)
	}
	base := strings.TrimRight(cfg.URL, "/")
	req, err := buildWebDAVRequest(ctx, http.MethodGet, base+"/"+url.PathEscape(name), nil, cfg)
	if err != nil {
		return nil, err
	}
	resp, err := webDAVClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载备份失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("下载备份失败: HTTP %d", resp.StatusCode)
	}
	// 上限 512MiB：备份件随用户规模增长，给足余量但不当成无上限的读。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("读备份内容失败: %w", err)
	}
	return body, nil
}

// isOwnBackupName 判断文件名是否为本程序产生的备份。
func isOwnBackupName(name string) bool {
	return strings.HasPrefix(name, "octopus-backup-") &&
		strings.HasSuffix(name, ".json") &&
		// 防止用 "../" 之类逃出目录：只允许前缀与后缀之间是时间戳字符。
		!strings.ContainsAny(name, "/\\")
}

// WebDAVRestore 从远端某份备份恢复。
//
// 三条必须说清楚的语义（都会如实回报给调用方，不替用户猜）：
//   - 导入是**增量**的：插入新行、对自然键 upsert。它不会删除"备份之后新增的行"，
//     所以结果不是"回到备份那一刻的快照"，而是"把备份里的内容合并进来"。
//   - 恢复**不碰本机现有配置**，所以它不能用来"清空重来"。
//   - 备份里的渠道凭据若用本实例密钥解不开（跨实例恢复），会如实回报条数，
//     否则表现为"恢复成功但渠道全报错"。
//
// 调用方负责在调用前做一次本地导出兜底 —— 那是恢复路径唯一可靠的回退手段。
func WebDAVRestore(ctx context.Context, cfg WebDAVConfig, name string) (*model.DBImportResult, error) {
	raw, err := WebDAVDownload(ctx, cfg, name)
	if err != nil {
		return nil, err
	}
	var dump model.DBDump
	if err := json.Unmarshal(raw, &dump); err != nil {
		return nil, fmt.Errorf("备份内容不是合法转储 JSON: %w", err)
	}
	if dump.Version == 0 {
		return nil, fmt.Errorf("备份缺少版本号，无法确认格式")
	}
	return op.DBImportIncremental(ctx, &dump)
}

// parseIntSetting 读一个整数字段，缺失或非法时返回错误（调用方决定回落值）。
func parseIntSetting(raw string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(raw))
}

// WebDAVBackupOnce 跑一轮云备份：上传 + 按保留份数清理。
//
// 返回远端文件名与被清理的文件名，供调用方记日志或通知。
// 上传失败直接返回错误（本次备份失败）；清理失败只作为第二个返回值附带，
// 因为备份本身已经成功，把清理失败算成备份失败会让用户以为没有备份。
func WebDAVBackupOnce(ctx context.Context, cfg WebDAVConfig, at time.Time) (string, []string, error) {
	name, err := WebDAVUpload(ctx, cfg, at)
	if err != nil {
		return "", nil, err
	}
	removed, pruneErr := WebDAVPrune(ctx, cfg)
	return name, removed, pruneErr
}
