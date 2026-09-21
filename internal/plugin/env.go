package plugin

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// BuildEnv 组装注入给插件进程的环境变量。
//
// 这是"项目为社区反代提供环境 + 全局隐秘代理"的落点：插件作者**不需要**实现代理支持，
// 只要用标准库发 HTTP 请求，出网就会经 OCTOPUS_EGRESS_PROXY 指向的节点出口。
//
// 三条硬口径：
//  1. 出口未就绪时由调用方拒启动（本函数只负责注入），绝不"注入不了就直连"；
//  2. NO_PROXY 必须含回环 —— 否则插件回调 octopus（或访问自己）也会绕节点出去，
//     轻则自环超时，重则把内网地址送到出口节点上解析；
//  3. 我们自己注入的 OCTOPUS_* 代理变量在重新组装前先剔除，避免上一个出口的残留被继承。
func BuildEnv(row model.Plugin, port int, egressProxy, token string) []string {
	dir := PluginDir(row.Slug)

	env := make([]string, 0, len(os.Environ())+16)
	for _, kv := range os.Environ() {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		switch strings.ToUpper(key) {
		case "OCTOPUS_EGRESS_PROXY", "OCTOPUS_PLUGIN_PORT", "OCTOPUS_PLUGIN_TOKEN", "OCTOPUS_PLUGIN_SLUG",
			"OCTOPUS_PLUGIN_DIR", "OCTOPUS_DATA_DIR", "OCTOPUS_ADMIN_BASE", "OCTOPUS_RELAY_BASE":
			continue
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY":
			// 这两个大小写形态会在大小写不敏感的环境里互相打架，统一由本函数按口径重写。
			continue
		}
		env = append(env, kv)
	}

	portKey := row.PortEnv
	if strings.TrimSpace(portKey) == "" {
		portKey = "PORT"
	}
	env = append(env,
		portKey+"="+strconv.Itoa(port),
		"OCTOPUS_PLUGIN_SLUG="+row.Slug,
		"OCTOPUS_PLUGIN_PORT="+strconv.Itoa(port),
		"OCTOPUS_PLUGIN_DIR="+dir,
		"OCTOPUS_DATA_DIR="+filepath.Dir(Dir()),
		"OCTOPUS_ADMIN_BASE=http://127.0.0.1:"+strconv.Itoa(conf.AppConfig.Server.AdminPort),
		"OCTOPUS_RELAY_BASE=http://127.0.0.1:"+strconv.Itoa(conf.AppConfig.Server.RelayPort),
		"OCTOPUS_PLUGIN_PROTOCOL="+row.Protocol,
		"OCTOPUS_PLUGIN_BASE_PATH="+row.BasePath,
	)
	if token != "" {
		env = append(env, "OCTOPUS_PLUGIN_TOKEN="+token)
	}

	if egressProxy != "" {
		env = append(env,
			"OCTOPUS_EGRESS_PROXY="+egressProxy,
			// 常见运行时（Go/Python/Node/curl）默认认这几个；社区工具不必为此写代码。
			"HTTP_PROXY="+egressProxy,
			"HTTPS_PROXY="+egressProxy,
			"ALL_PROXY="+egressProxy,
			"http_proxy="+egressProxy,
			"https_proxy="+egressProxy,
			"all_proxy="+egressProxy,
			"NO_PROXY=127.0.0.1,localhost,::1",
			"no_proxy=127.0.0.1,localhost,::1",
		)
	}
	return env
}

// ResolveEgress 解析这个插件的出口。
//
// 返回值语义：
//   - proxy 非空：注入该出口，插件出网走节点；
//   - mode=direct：不注入（清单显式声明允许直连）；
//   - 其余情况（pool 但没绑定节点 / 绑定的节点未就绪 / 绑定的节点不存在）一律**返回错误**：
//     宁可插件起不来，也不让它在用户以为"已经隐藏出口"的前提下用真实 IP 出去。
func ResolveEgress(row model.Plugin) (string, string, error) {
	switch row.EgressMode {
	case EgressDirect, EgressExternal:
		return "", "", nil
	}
	nodeID := row.EgressNodeID
	if nodeID == 0 {
		nodeID = defaultEgressNodeID()
	}
	if nodeID == 0 {
		return "", "", fmt.Errorf(
			"插件 %s 声明 egress=pool 但没有出口节点：在插件页给它选一个出口，或在设置里配 plugin_default_egress_id（改清单为 egress=direct 才允许直连）",
			row.Slug)
	}
	endpoint, err := op.ProxyNodeEndpoint(nodeID)
	if err != nil {
		return "", "", fmt.Errorf("插件 %s 的出口节点不可用：%w", row.Slug, err)
	}
	if _, err := url.Parse(endpoint); err != nil {
		return "", "", fmt.Errorf("插件 %s 的出口地址不合法：%s", row.Slug, endpoint)
	}
	return endpoint, "", nil
}

func defaultEgressNodeID() int {
	raw, err := op.SettingGetString(model.SettingKeyPluginDefaultEgressID)
	if err != nil {
		return 0
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0
	}
	return value
}

// CheckHTTPEntry 校验 runtime=http 的远端地址是否在白名单内（回环恒允许）。
//
// 白名单为空 = 只允许回环：与号池声明式适配器的 pool_declarative_hosts 同一 fail-closed 口径 ——
// 一个能被远程地址驱动的"上游"是 SSRF 的天然入口，默认必须什么都不允许。
func CheckHTTPEntry(ctx context.Context, entry string) error {
	parsed, err := url.Parse(entry)
	if err != nil {
		return fmt.Errorf("服务地址不合法：%s", entry)
	}
	host := parsed.Hostname()
	if isLoopbackHost(host) {
		return nil
	}
	allowed, err := op.SettingGetString(model.SettingKeyPluginHTTPHosts)
	if err != nil {
		allowed = ""
	}
	for _, candidate := range strings.Split(allowed, ",") {
		if strings.EqualFold(strings.TrimSpace(candidate), host) {
			return nil
		}
	}
	return fmt.Errorf("runtime=http 的主机 %s 不在白名单里：默认只允许本机回环。要接远端服务，先把主机名加进设置项 plugin_http_hosts", host)
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return false
}

// probeReady 用一次 TCP 连接判断插件是否已经在监听。
// 就绪判据取"真的能连上"，而不是"进程还在"——插件的常见失败形态是进程活着但端口没起来
// （清单给错了端口变量名、或它自己绑到了别的地址），只看进程会把它当成启动成功。
func probeReady(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
