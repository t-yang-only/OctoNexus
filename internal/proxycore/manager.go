// Package proxycore 托管一个**独立的** mihomo 内核实例，把节点池里的每个节点变成一个本地出口。
//
// 为什么要有它（R-proxy-001）：Clash 节点说的是 ss/vmess/trojan/hysteria2 这些协议，Go 标准库只会
// HTTP/SOCKS5 代理。要让"某个账号从某个节点出去"，中间必须有个内核把协议翻译成 `http://127.0.0.1:<port>`。
//
// 设计口径：
//   - **一节点一入站**：每个启用节点在配置里生成一个 listeners（type: http），端口从冷门端口池里分配。
//     不用"单端口 + 内核 API 切节点"：并发请求下切来切去必然串号，防关联就白做了。
//   - 端口池避开常用端口：默认 41000-41999（浏览器/系统/常见工具都不在这段），可配。
//   - 进程与文件都关在 `<数据目录>/core/` 内：配置、日志、二进制同放一处，整体备份就能带走。
//   - 内核二进制**不由我们随包分发**：优先设置项 proxy_core_path，其次 `<数据目录>/core/mihomo[.exe]`；
//     都不在就报错并给出下载地址（把"装什么、从哪装"交给用户决定）。
package proxycore

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// 端口池默认区间：刻意选在高位冷门段，避开 7890/7891/1080/10809 这些"人尽皆知"的代理端口，
// 也避开 Windows 动态端口起始段常见的冲突区。
const (
	DefaultPortStart = 41000
	DefaultPortEnd   = 41999
	configFileName   = "config.yaml"
	logFileName      = "core.log"
	binaryNameWin    = "mihomo.exe"
	binaryNameUnix   = "mihomo"
	startTimeout     = 15 * time.Second
	MgmtPortOffset   = 1 // 管理端口占用池内第一个端口，便于同段管理
)

// Status 是内核的面板可见状态。
type Status struct {
	Running    bool   `json:"running"`
	PID        int    `json:"pid"`
	Binary     string `json:"binary"`
	BinaryOK   bool   `json:"binary_ok"`
	ConfigPath string `json:"config_path"`
	LogPath    string `json:"log_path"`
	Listeners  int    `json:"listeners"`  // 配置里生成的入站数（=已分配端口的启用节点数）
	MgmtPort   int    `json:"mgmt_port"`  // 管理/占位端口
	LastError  string `json:"last_error"` // 最近一次启动/同步的失败原因
	Version    string `json:"version"`    // 内核自报版本（`-v`）
	StartedAt  string `json:"started_at"` // 本次启动时间（RFC3339，空=未运行）
	Ports      []int  `json:"ports"`      // 当前分配出去的出口入站端口（排障时可逐个 curl）
}

type manager struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	startedAt time.Time
	lastErr   string
	version   string
}

var defaultManager = &manager{}

// Default 返回进程内单例（内核是全局唯一资源：一套端口、一个进程）。
func Default() *manager { return defaultManager }

// CoreDir 返回内核工作目录（与业务库同目录下的 core/）。
//
// 一定返回**绝对路径**：数据库路径在配置里通常是相对的（data/data.db），
// 若把相对路径继续往下传，内核进程的 cwd 一变就变成"找不到路径"（实测踩过：fork/exec data\core\mihomo.exe 失败）。
func CoreDir() string {
	base := filepath.Dir(conf.AppConfig.Database.Path)
	if base == "" || base == "." {
		base = "data"
	}
	if abs, err := filepath.Abs(base); err == nil {
		base = abs
	}
	return filepath.Join(base, "core")
}

func configPath() string { return filepath.Join(CoreDir(), configFileName) }
func logPath() string    { return filepath.Join(CoreDir(), logFileName) }

// BinaryPath 解析内核二进制位置：设置项 → 数据目录默认位置 → 报错（附下载指引）。
func BinaryPath() (string, error) {
	if configured := strings.TrimSpace(settingString(model.SettingKeyProxyCorePath)); configured != "" {
		if abs, err := filepath.Abs(configured); err == nil {
			configured = abs
		}
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured, nil
		}
		return "", fmt.Errorf("设置里的内核路径不存在：%s", configured)
	}
	candidates := []string{
		filepath.Join(CoreDir(), binaryNameWin),
		filepath.Join(CoreDir(), binaryNameUnix),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("没有可用的 mihomo 内核：把内核放到 %s，或在设置里指定内核路径（下载：https://github.com/MetaCubeX/mihomo/releases）",
		filepath.Join(CoreDir(), binaryNameWin))
}

// PortRange 返回端口池区间（设置项越界即夹回默认值；start >= end 时整体回落默认区间）。
func PortRange() (int, int) {
	start := settingInt(model.SettingKeyProxyCorePortStart, DefaultPortStart)
	end := settingInt(model.SettingKeyProxyCorePortEnd, DefaultPortEnd)
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

// pickFreePort 从区间里挑一个当前没被占用、也没被节点池占用的端口。
func pickFreePort(start, end int, used map[int]bool) int {
	for port := start; port <= end; port++ {
		if used[port] || !portFree(port) {
			continue
		}
		return port
	}
	return 0
}

func portFree(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// AllocatePorts 给"启用但还没分配端口"的节点分配本地入站端口（面板"同步端口"按钮与内核启动都走它）。
func AllocatePorts() (int, error) { return allocatePorts() }

// allocatePorts 给"启用但还没分配端口"的节点分配本地入站端口，并写下 LocalPort。
//
// 口径：已分配且仍可用的端口**保持不变**（改端口会让已生效的绑定瞬间失效）；
// 端口被别人占了（例如用户手动跑了别的东西）才重新分配。
func allocatePorts() (int, error) {
	conn := db.GetDB()
	if conn == nil {
		return 0, fmt.Errorf("数据库不可用")
	}
	var nodes []model.ProxyNode
	if err := conn.Model(&model.ProxyNode{}).Order("id asc").Find(&nodes).Error; err != nil {
		return 0, fmt.Errorf("load proxy nodes: %w", err)
	}
	start, end := PortRange()
	used := map[int]bool{}
	for _, node := range nodes {
		if node.LocalPort > 0 {
			used[node.LocalPort] = true
		}
	}
	assigned := 0
	for _, node := range nodes {
		if !node.Enabled {
			continue
		}
		if node.LocalPort >= start && node.LocalPort <= end && portFree(node.LocalPort) {
			continue
		}
		// 旧端口不可用或不在池内：释放后重挑
		if node.LocalPort > 0 {
			delete(used, node.LocalPort)
		}
		port := pickFreePort(start, end, used)
		if port == 0 {
			return assigned, fmt.Errorf("端口池 %d-%d 已用尽，无法为节点 %q 分配出口端口", start, end, node.Name)
		}
		if err := conn.Model(&model.ProxyNode{}).Where("id = ?", node.ID).
			Update("local_port", port).Error; err != nil {
			return assigned, fmt.Errorf("save local port for %q: %w", node.Name, err)
		}
		used[port] = true
		assigned++
	}
	return assigned, nil
}

// ReleaseDisabledPorts 把停用节点的端口收回（配置里也不会再出现它们的入站）。
func ReleaseDisabledPorts() (int, error) {
	conn := db.GetDB()
	if conn == nil {
		return 0, fmt.Errorf("数据库不可用")
	}
	result := conn.Model(&model.ProxyNode{}).Where("enabled = ? AND local_port > 0", false).Update("local_port", 0)
	return int(result.RowsAffected), result.Error
}

// ---------- 进程管理 ----------

// Status 汇报内核现状（不启动任何东西）。
func (m *manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *manager) statusLocked() Status {
	binary, binErr := BinaryPath()
	status := Status{
		Binary:     binary,
		BinaryOK:   binErr == nil,
		ConfigPath: configPath(),
		LogPath:    logPath(),
		LastError:  m.lastErr,
		Version:    m.version,
	}
	if status.LastError == "" && binErr != nil {
		status.LastError = binErr.Error()
	}
	if m.cmd != nil && m.cmd.Process != nil && m.cmd.ProcessState == nil {
		status.Running = true
		status.PID = m.cmd.Process.Pid
		if !m.startedAt.IsZero() {
			status.StartedAt = m.startedAt.Format(time.RFC3339)
		}
	}
	if conn := db.GetDB(); conn != nil {
		var nodes []model.ProxyNode
		if err := conn.Model(&model.ProxyNode{}).Where("enabled = ? AND local_port > 0", true).
			Order("id asc").Find(&nodes).Error; err == nil {
			status.Listeners = len(nodes)
			for _, node := range nodes {
				status.Ports = append(status.Ports, node.LocalPort)
			}
		}
	}
	return status
}

// Start 生成配置并（重新）拉起内核。已在跑时先停后起（配置改动一律靠重启生效，行为可预测）。
func (m *manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked(ctx)
}

func (m *manager) startLocked(ctx context.Context) error {
	binary, err := BinaryPath()
	if err != nil {
		m.lastErr = err.Error()
		return err
	}
	if err := os.MkdirAll(CoreDir(), 0o755); err != nil {
		m.lastErr = fmt.Sprintf("创建内核目录失败：%v", err)
		return fmt.Errorf("%s", m.lastErr)
	}
	m.stopLocked()

	// 先分配/回收端口，再按分配结果生成配置：配置里的入站端口必须与库里的 LocalPort 一致，
	// 否则会出现"绑定指向 A 端口、配置里是 B 端口"的错位。
	if _, err := allocatePorts(); err != nil {
		m.lastErr = err.Error()
		return err
	}
	if _, err := ReleaseDisabledPorts(); err != nil {
		m.lastErr = err.Error()
		return err
	}

	yamlBody, listenerPorts, err := BuildConfig(db.GetDB())
	if err != nil {
		m.lastErr = err.Error()
		return err
	}
	if len(listenerPorts) == 0 {
		err := fmt.Errorf("没有启用中的节点，未启动内核（先在代理页启用节点并导入）")
		m.lastErr = err.Error()
		return err
	}
	if err := os.WriteFile(configPath(), yamlBody, 0o600); err != nil {
		m.lastErr = fmt.Sprintf("写内核配置失败：%v", err)
		return fmt.Errorf("%s", m.lastErr)
	}

	logFile, err := os.OpenFile(logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		m.lastErr = fmt.Sprintf("打开内核日志失败：%v", err)
		return fmt.Errorf("%s", m.lastErr)
	}
	cmd := exec.Command(binary, "-d", CoreDir(), "-f", configPath())
	cmd.Dir = CoreDir()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		m.lastErr = fmt.Sprintf("启动内核失败：%v", err)
		return fmt.Errorf("%s", m.lastErr)
	}
	_ = logFile.Close()
	m.cmd = cmd
	m.startedAt = time.Now()
	m.lastErr = ""
	if m.version == "" {
		// 版本只在首次启动时问一次（`-v` 会退出进程，不能与运行中的实例抢目录），
		// 面板上"装的是哪个内核"必须是查得到的实据。
		if out, err := exec.Command(binary, "-v").CombinedOutput(); err == nil {
			if line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]); line != "" {
				m.version = line
			}
		}
	}

	// 进程守护：命令退出时清掉句柄，避免"进程早就死了但状态还显示在跑"
	go func(started *exec.Cmd) {
		_ = started.Wait()
		m.mu.Lock()
		if m.cmd == started {
			m.cmd = nil
		}
		m.mu.Unlock()
	}(cmd)

	// 就绪判定：每一个入站端口都必须真的能连上，否则算启动失败（不能"进程活着"就当成功）
	if err := waitListeners(ctx, listenerPorts); err != nil {
		m.lastErr = err.Error()
		m.stopLocked()
		return err
	}
	return nil
}

// Stop 停掉内核进程（含子进程）。
func (m *manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

func (m *manager) stopLocked() {
	if m.cmd == nil || m.cmd.Process == nil {
		m.cmd = nil
		return
	}
	killProcessTree(m.cmd.Process.Pid)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m.cmd.ProcessState != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	m.cmd = nil
	m.startedAt = time.Time{}
}

// Restart 重新生成配置并重启（节点增删改/绑定变化后调用）。
func (m *manager) Restart(ctx context.Context) error {
	return m.Start(ctx)
}

// Sync 按数据库现状重建配置并重启内核；内核未启用（无节点）时静默停掉。
func (m *manager) Sync(ctx context.Context) error {
	conn := db.GetDB()
	if conn == nil {
		return fmt.Errorf("数据库不可用")
	}
	var enabled int64
	if err := conn.Model(&model.ProxyNode{}).Where("enabled = ?", true).Count(&enabled).Error; err != nil {
		return err
	}
	if enabled == 0 {
		m.Stop()
		return nil
	}
	return m.Start(ctx)
}

// waitListeners 轮询每个入站端口直到可连接；任一端口超时即失败并给出内核日志尾。
func waitListeners(ctx context.Context, ports []int) error {
	deadline := time.Now().Add(startTimeout)
	pending := map[int]bool{}
	for _, port := range ports {
		pending[port] = true
	}
	for time.Now().Before(deadline) {
		for port := range pending {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				delete(pending, port)
			}
		}
		if len(pending) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待内核入站端口被取消：%v", ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("内核启动超时：仍有 %d 个入站端口未就绪；内核日志尾部：%s", len(pending), logTail(6))
}

// logTail 读内核日志尾部若干行（用于把失败原因直接摊给用户）。
func logTail(lines int) string {
	raw, err := os.ReadFile(logPath())
	if err != nil {
		return "（读不到内核日志）"
	}
	all := strings.Split(strings.TrimRight(string(raw), "\r\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, " | ")
}

// ProbeExitIP 经"某个节点对应的本地入站"访问出口 IP 探测地址，返回看到的出口 IP。
// 这是"两个账号到底是不是不同出口"的可见证据（面板上显示的就是它）。
func (m *manager) ProbeExitIP(ctx context.Context, nodeID int, probeURL string) (string, error) {
	endpoint, err := op.ProxyNodeEndpoint(nodeID)
	if err != nil {
		return "", err
	}
	transport := &probeTransport{}
	return probeThrough(ctx, endpoint, probeURL, transport.dialer())
}
