//go:build windows

package proxycore

import (
	"os/exec"
	"strconv"
	"syscall"
)

// Windows：内核用 CREATE_NO_WINDOW 启动，避免后台拉起时弹控制台窗口。
const createNoWindow = 0x08000000

func setHideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// killTree 用 taskkill /T 连子进程一起收掉（内核被孤立时端口会一直被占）。
func killTree(pid int) {
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}
