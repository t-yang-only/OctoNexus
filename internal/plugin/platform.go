package plugin

import (
	"fmt"
	"net/http"
	"os/exec"
	"time"
)

// 平台相关能力集中在这里（与 internal/proxycore 同一结构：平台无关逻辑不出现 syscall）。

// hideWindow 让后台拉起的插件进程不弹控制台窗口（Windows）。
func hideWindow(cmd *exec.Cmd) { setHideWindow(cmd) }

// killProcessTree 结束插件及其子进程。
func killProcessTree(pid int) { killTree(pid) }

// httpProbe 对 runtime=http 的外部服务做一次轻量探测：任何 HTTP 响应都算"服务在"，
// 只有连不上/超时才判失败（401 也说明服务活着 —— 那是它的鉴权，不是不可达）。
func httpProbe(endpoint string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("连不上插件服务 %s：%w", endpoint, err)
	}
	_ = resp.Body.Close()
	return nil
}
