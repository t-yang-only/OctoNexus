//go:build !windows

package plugin

import (
	"os"
	"os/exec"
	"syscall"
)

func setHideWindow(cmd *exec.Cmd) {
	// 非 Windows 平台没有窗口概念；开独立进程组，便于整组结束。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree 结束整个进程组（脚本型插件常会再拉起子进程，只杀父进程会留下孤儿占着端口）。
func killTree(pid int) {
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if process, findErr := os.FindProcess(pid); findErr == nil {
			_ = process.Kill()
		}
	}
}
