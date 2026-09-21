package proxycore

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// 平台相关与设置读取的小工具都集中在这里，让 manager/config 两个文件保持平台无关。

type dialContextFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

func parseProxyURL(endpoint string) (*url.URL, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("出口地址不合法：%s", endpoint)
	}
	return parsed, nil
}

// dialer 给经代理的请求配一个带超时的底层拨号（代理那一跳也要有超时，否则内核僵住会挂死整轮）。
func (t *probeTransport) dialer() dialContextFunc {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return dialer.DialContext
}

type probeTransport struct{}

// httpTransport 只是给 manager 一个稳定的构造点（保持占位，便于后续加 TLS 参数）。
type httpTransport = probeTransport

// hideWindow：Windows 上后台拉起内核时不要弹黑框。
func hideWindow(cmd *exec.Cmd) { setHideWindow(cmd) }

// killProcessTree：杀掉内核及其子进程（mihomo 自己不会再拉起子进程，但统一口径更稳）。
func killProcessTree(pid int) { killTree(pid) }

// settingString / settingInt 读设置项（op 层已有缓存，读不到就回落默认值）。
func settingString(key model.SettingKey) string {
	value, err := op.SettingGetString(key)
	if err != nil {
		return ""
	}
	return value
}

func settingInt(key model.SettingKey, fallback int) int {
	raw := settingString(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

// AutostartEnabled 读"启动时自动拉起内核"开关（默认开：用户要的就是"软件自己管内核"）。
func AutostartEnabled() bool {
	raw := settingString(model.SettingKeyProxyCoreAutostart)
	if raw == "" {
		return true
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return true
	}
	return value
}

// ProbeURL 返回出口 IP 探测地址（设置项可改，默认 api.ipify.org）。
func ProbeURL() string {
	if custom := settingString(model.SettingKeyProxyExitProbeURL); custom != "" {
		return custom
	}
	return "https://api.ipify.org"
}

// ProbeNode 经节点出口探测出口 IP（供 API 调用）。
func ProbeNode(ctx context.Context, nodeID int) (string, error) {
	endpoint, err := op.ProxyNodeEndpoint(nodeID)
	if err != nil {
		return "", err
	}
	transport := &probeTransport{}
	return probeThrough(ctx, endpoint, ProbeURL(), transport.dialer())
}
