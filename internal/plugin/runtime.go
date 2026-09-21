package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
)

// 端口池默认区间：刻意与内核端口池（41000-41999）分开，两侧各自排障时一眼能分清是谁占了端口。
const (
	DefaultPortStart = 42000
	DefaultPortEnd   = 42999
	logFileName      = "plugin.log"
	startTimeout     = 15 * time.Second
	maxLogBytes      = 4 << 20
)

type proc struct {
	cmd       *exec.Cmd
	startedAt time.Time
}

var (
	mu      sync.Mutex
	running = map[string]*proc{}
)

// PortRange 返回插件端口池区间（设置项越界即夹回默认值，口径与内核一致）。
func PortRange() (int, int) {
	start := settingInt(model.SettingKeyPluginPortStart, DefaultPortStart)
	end := settingInt(model.SettingKeyPluginPortEnd, DefaultPortEnd)
	if start < 1024 || start > 65535 {
		start = DefaultPortStart
	}
	if end < 1024 || end > 65535 {
		end = DefaultPortEnd
	}
	if start >= end {
		start, end = DefaultPortStart, DefaultPortEnd
	}
	return start, end
}

// IsRunning 报告插件进程是否由本进程托管着。
func IsRunning(slug string) (bool, int) {
	mu.Lock()
	defer mu.Unlock()
	if p, ok := running[slug]; ok && p.cmd.Process != nil {
		return true, p.cmd.Process.Pid
	}
	return false, 0
}

// Start 启动插件：解析出口 → 分配端口 → 注入环境 → 拉起进程 → 等端口就绪 → 回写状态。
//
// 任何一步失败都返回可读原因，并且**不留半启动状态**（端口与进程都会回收）。
func Start(ctx context.Context, slug string) (model.PluginStatus, error) {
	conn := db.GetDB()
	if conn == nil {
		return model.PluginStatus{}, fmt.Errorf("数据库未初始化")
	}
	row, err := Get(ctx, slug)
	if err != nil {
		return model.PluginStatus{}, err
	}
	if !row.Enabled {
		return model.PluginStatus{}, fmt.Errorf("插件 %s 已停用：先在插件页启用它", slug)
	}
	if ok, _ := IsRunning(slug); ok {
		return Status(ctx, slug)
	}

	// 每次启动都重新读一遍清单：玩家改了 plugin.json 之后重启插件即生效，
	// 不需要"先扫描再启动"两步（端口/入口变化会在这里体现出来）。
	parsed, err := Load(PluginDir(slug))
	if err != nil {
		recordError(ctx, row.ID, err)
		return model.PluginStatus{}, err
	}
	if err := upsertManifest(ctx, conn, parsed); err != nil {
		recordError(ctx, row.ID, err)
		return model.PluginStatus{}, err
	}
	row, err = Get(ctx, slug)
	if err != nil {
		return model.PluginStatus{}, err
	}

	token, err := tokenFor(ctx, conn, row)
	if err != nil {
		recordError(ctx, row.ID, err)
		return model.PluginStatus{}, err
	}

	egress, _, err := ResolveEgress(row)
	if err != nil {
		recordError(ctx, row.ID, err)
		return model.PluginStatus{}, err
	}

	if row.Runtime == RuntimeHTTP {
		if err := CheckHTTPEntry(ctx, row.Entry); err != nil {
			recordError(ctx, row.ID, err)
			return model.PluginStatus{}, err
		}
		// 外部服务不由我们托管：确认能连上即视为"运行中"，并明确告知出口不由 octopus 负责。
		parsedURL := row.Entry
		if err := checkReachable(parsedURL); err != nil {
			recordError(ctx, row.ID, err)
			return model.PluginStatus{}, err
		}
		if err := conn.WithContext(ctx).Model(&model.Plugin{}).Where("id = ?", row.ID).
			Updates(map[string]any{"status": StatusRunning, "last_error": "", "pid": 0}).Error; err != nil {
			return model.PluginStatus{}, err
		}
		if row.AutoChannel {
			if err := ensureChannel(ctx, &row, row.Entry, token); err != nil {
				return model.PluginStatus{}, err
			}
		}
		return Status(ctx, slug)
	}

	port, err := allocatePort(ctx, row)
	if err != nil {
		recordError(ctx, row.ID, err)
		return model.PluginStatus{}, err
	}
	args, err := RenderArgs(parsed.Args, port, parsed.Dir, filepath.Dir(Dir()), slug)
	if err != nil {
		recordError(ctx, row.ID, err)
		return model.PluginStatus{}, err
	}

	entryPath := filepath.Join(parsed.Dir, filepath.FromSlash(parsed.Entry))
	logPath := filepath.Join(parsed.Dir, logFileName)
	if err := rotateLog(logPath); err != nil {
		recordError(ctx, row.ID, err)
		return model.PluginStatus{}, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		recordError(ctx, row.ID, err)
		return model.PluginStatus{}, err
	}

	cmd := exec.Command(entryPath, args...)
	cmd.Dir = parsed.Dir
	cmd.Env = BuildEnv(row, port, egress, token)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		recordError(ctx, row.ID, fmt.Errorf("拉起插件失败：%w", err))
		return model.PluginStatus{}, fmt.Errorf("拉起插件失败：%w", err)
	}
	_ = logFile.Close()

	mu.Lock()
	running[slug] = &proc{cmd: cmd, startedAt: time.Now()}
	mu.Unlock()
	go supervise(slug, row.ID, cmd)

	if err := waitReady(ctx, cmd, port); err != nil {
		_ = Stop(ctx, slug)
		recordError(ctx, row.ID, err)
		return model.PluginStatus{}, err
	}

	now := time.Now()
	if err := conn.WithContext(ctx).Model(&model.Plugin{}).Where("id = ?", row.ID).Updates(map[string]any{
		"status": StatusRunning, "pid": cmd.Process.Pid, "port": port,
		"last_start_at": now, "last_error": "",
	}).Error; err != nil {
		// 进程已经起来了，但状态写不进去 —— 这种"半托管"状态必须就地收拾掉：
		// 留着它，面板说没启动、实际端口占着且在跑，下一次启动又撞端口，问题会更难查。
		_ = Stop(ctx, slug)
		return model.PluginStatus{}, fmt.Errorf("回写插件状态失败（已停止该插件）：%w", err)
	}

	if row.AutoChannel {
		endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
		if err := ensureChannel(ctx, &row, endpoint, token); err != nil {
			logf("插件 %s：自动注册渠道失败：%v", slug, err)
		}
	}
	return Status(ctx, slug)
}

// supervise 等进程结束并回写"它自己退了"这个事实。
//
// 插件退出必须落库：否则面板会一直显示"运行中"，用户以为在用、实际每个请求都连不上
// （这与内核托管踩过的坑一致：状态来自真实进程，不来自我们的愿望）。
func supervise(slug string, id int, cmd *exec.Cmd) {
	err := cmd.Wait()
	mu.Lock()
	delete(running, slug)
	mu.Unlock()

	status := StatusExited
	message := ""
	if err != nil {
		message = err.Error()
	}
	conn := db.GetDB()
	if conn == nil {
		return
	}
	now := time.Now()
	_ = conn.Model(&model.Plugin{}).Where("id = ?", id).Updates(map[string]any{
		"status": status, "pid": 0, "last_exit_at": now, "last_error": message,
	}).Error
}

// Stop 结束插件进程（含子进程树）并把状态落成 stopped。
func Stop(ctx context.Context, slug string) error {
	mu.Lock()
	p, ok := running[slug]
	if ok {
		delete(running, slug)
	}
	mu.Unlock()

	if ok && p.cmd.Process != nil {
		killProcessTree(p.cmd.Process.Pid)
	}
	conn := db.GetDB()
	if conn == nil {
		return nil
	}
	row, err := Get(ctx, slug)
	if err != nil {
		return err
	}
	return conn.WithContext(ctx).Model(&model.Plugin{}).Where("id = ?", row.ID).
		Updates(map[string]any{"status": StatusStopped, "pid": 0}).Error
}

// Autostart 拉起所有"随实例启动"的插件（缺出口/清单坏掉只告警，不阻断实例启动）。
func Autostart(ctx context.Context) {
	rows, err := List(ctx)
	if err != nil {
		return
	}
	for _, row := range rows {
		if !row.AutoStart || !row.Enabled {
			continue
		}
		if _, err := Start(ctx, row.Slug); err != nil {
			logf("插件 %s 自启失败：%v", row.Slug, err)
		}
	}
}

// Status 返回单个插件的运行时视图（含出口解析结论与日志尾部）。
func Status(ctx context.Context, slug string) (model.PluginStatus, error) {
	row, err := Get(ctx, slug)
	if err != nil {
		return model.PluginStatus{}, err
	}
	return statusOf(ctx, row, 12), nil
}

// Statuses 返回全部插件的运行时视图。
func Statuses(ctx context.Context) ([]model.PluginStatus, error) {
	rows, err := List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.PluginStatus, 0, len(rows))
	for _, row := range rows {
		out = append(out, statusOf(ctx, row, 0))
	}
	return out, nil
}

func statusOf(ctx context.Context, row model.Plugin, tail int) model.PluginStatus {
	isRunning, pid := IsRunning(row.Slug)
	status := row.Status
	if isRunning {
		status = StatusRunning
	} else if status == StatusRunning {
		// 实例重启过：库里还写着 running，但本进程没有托管它 —— 以事实为准，并顺手纠正。
		status = StatusExited
		if conn := db.GetDB(); conn != nil {
			_ = conn.WithContext(ctx).Model(&model.Plugin{}).Where("id = ?", row.ID).
				Updates(map[string]any{"status": StatusExited, "pid": 0}).Error
		}
	}
	args, _ := unmarshalArgs(row.Args)
	endpoint := row.Entry
	egressProxy := ""
	egressNote := ""
	switch row.Runtime {
	case RuntimeHTTP:
		endpoint = row.Entry
		if row.EgressMode == EgressExternal {
			egressNote = "外部服务：出口由插件自己负责，octopus 不注入代理"
		}
	default:
		if row.Port > 0 {
			endpoint = fmt.Sprintf("http://127.0.0.1:%d", row.Port)
		} else {
			endpoint = ""
		}
		if proxy, _, err := ResolveEgress(row); err == nil {
			egressProxy = proxy
		} else if row.EgressMode == EgressPool {
			egressNote = err.Error()
		}
	}
	statusRow := model.PluginStatus{
		Slug: row.Slug, Name: row.Name, Version: row.Version, Runtime: row.Runtime,
		Enabled: row.Enabled, AutoStart: row.AutoStart, AutoChannel: row.AutoChannel,
		Protocol: row.Protocol, Entry: row.Entry, Args: args,
		Running: isRunning, Status: status, PID: pid, Port: row.Port,
		BasePath: row.BasePath, HealthPath: row.HealthPath,
		Endpoint: endpoint, EgressProxy: egressProxy, EgressMode: row.EgressMode,
		EgressNodeID: row.EgressNodeID, EgressNote: egressNote,
		ChannelID: row.ChannelID, LastError: row.LastError, Dir: PluginDir(row.Slug),
	}
	if row.LastStartAt != nil {
		statusRow.LastStartAt = row.LastStartAt.Format(time.RFC3339)
	}
	if row.LastExitAt != nil {
		statusRow.LastExitAt = row.LastExitAt.Format(time.RFC3339)
	}
	if tail > 0 {
		statusRow.LogTail = logTail(PluginDir(row.Slug), tail)
	}
	return statusRow
}

// allocatePort 给插件挑一个本地入站端口。
//
// 挑法（口径与内核端口池一致）：已分配且现在确实空闲的端口保持不变（重启后端口稳定，
// 渠道地址与用户书签都不会漂）；否则在池内找第一个既没被节点池占用、也没被别的插件占用、
// 且真能绑上的端口。
func allocatePort(ctx context.Context, row model.Plugin) (int, error) {
	conn := db.GetDB()
	if conn == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	used := map[int]bool{}
	var nodes []model.ProxyNode
	if err := conn.WithContext(ctx).Model(&model.ProxyNode{}).Where("local_port > 0").Find(&nodes).Error; err != nil {
		return 0, fmt.Errorf("读取节点端口失败：%w", err)
	}
	for _, node := range nodes {
		used[node.LocalPort] = true
	}
	var others []model.Plugin
	if err := conn.WithContext(ctx).Model(&model.Plugin{}).Where("port > 0").Find(&others).Error; err != nil {
		return 0, fmt.Errorf("读取插件端口失败：%w", err)
	}
	for _, other := range others {
		used[other.Port] = true
	}

	if row.Port > 0 && !used[row.Port] && portFree(row.Port) {
		return row.Port, nil
	}
	start, end := PortRange()
	for port := start; port <= end; port++ {
		if used[port] || !portFree(port) {
			continue
		}
		return port, nil
	}
	return 0, fmt.Errorf("插件端口池 %d-%d 没有可用端口：先停掉不用的插件，或在设置里扩大 plugin_port_start/end", start, end)
}

// waitReady 等插件端口就绪；进程提前退出时立刻失败并把日志尾部带上。
func waitReady(ctx context.Context, cmd *exec.Cmd, port int) error {
	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		if probeReady(port) {
			return nil
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return fmt.Errorf("插件进程启动后立即退出（exit=%v）：%s", cmd.ProcessState.ExitCode(), lastLogLine(cmd.Dir))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待插件就绪被取消：%w", ctx.Err())
		case <-time.After(120 * time.Millisecond):
		}
	}
	return fmt.Errorf("等待插件端口 %d 就绪超时（%s）：%s", port, startTimeout, lastLogLine(cmd.Dir))
}

// ensureChannel 让插件在 octopus 里表现为一个**普通渠道**（于是分压/监控/选路/余额一律生效）。
//
// 只做两件事：地址指向插件入站端口、凭据用插件令牌。模型与授权仍由既有流程管理：
// 插件支持的模型由"拉取模型"或人工声明决定 —— 这里不替用户猜。
func ensureChannel(ctx context.Context, row *model.Plugin, endpoint, token string) error {
	detail := model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:    channelName(row),
			BaseURL: endpoint,
			Enabled: true,
		},
		Keys: []model.ChannelKeyInput{{Name: "plugin", Key: token}},
	}
	if row.ChannelID == 0 {
		created, err := op.ChannelCreate(&detail, ctx)
		if err != nil {
			return fmt.Errorf("注册渠道失败：%w", err)
		}
		row.ChannelID = created.ID
		conn := db.GetDB()
		if conn == nil {
			return nil
		}
		return conn.WithContext(ctx).Model(&model.Plugin{}).Where("id = ?", row.ID).
			Update("channel_id", created.ID).Error
	}
	detail.ID = row.ChannelID
	if _, err := op.ChannelUpdate(&detail, ctx); err != nil {
		return fmt.Errorf("更新渠道失败：%w", err)
	}
	return nil
}

func channelName(row *model.Plugin) string {
	if strings.TrimSpace(row.Name) != "" {
		return "plugin:" + row.Slug + " (" + row.Name + ")"
	}
	return "plugin:" + row.Slug
}

// checkReachable 用一次 HTTP 探测确认外部服务确实在（runtime=http 的"启动"判据）。
func checkReachable(endpoint string) error {
	return httpProbe(endpoint)
}

// ---- 小工具 ----

func settingInt(key model.SettingKey, fallback int) int {
	raw, err := op.SettingGetString(key)
	if err != nil || strings.TrimSpace(raw) == "" {
		return fallback
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}

func portFree(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func rotateLog(path string) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxLogBytes {
		return nil
	}
	return os.Rename(path, path+".1")
}

func logTail(dir string, lines int) []string {
	raw, err := os.ReadFile(filepath.Join(dir, logFileName))
	if err != nil {
		return nil
	}
	all := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	out := make([]string, 0, len(all))
	for _, line := range all {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, truncate(line, 400))
	}
	return out
}

func lastLogLine(dir string) string {
	tail := logTail(dir, 3)
	if len(tail) == 0 {
		return "（插件没有输出任何日志）"
	}
	return strings.Join(tail, " / ")
}

func truncate(text string, max int) string {
	if len(text) <= max {
		return text
	}
	return text[:max] + "…"
}

func marshalArgs(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("序列化启动参数失败：%w", err)
	}
	return string(raw), nil
}

func unmarshalArgs(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, err
	}
	return args, nil
}

func randomToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成插件令牌失败：%w", err)
	}
	return "sk-plugin-" + hex.EncodeToString(buf), nil
}

func recordError(ctx context.Context, id int, err error) {
	conn := db.GetDB()
	if conn == nil {
		return
	}
	_ = conn.WithContext(ctx).Model(&model.Plugin{}).Where("id = ?", id).
		Updates(map[string]any{"status": StatusError, "last_error": err.Error()}).Error
}

func logf(format string, args ...any) {
	log.Warnf("plugin: "+format, args...)
}
