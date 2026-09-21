//go:build windows

package plugin

import (
	"os/exec"
	"strconv"
	"syscall"
)

// CREATE_NO_WINDOW / CREATE_NEW_PROCESS_GROUP：插件是后台服务，不该弹控制台窗口。
const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

func setHideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | createNoWindow,
	}
}

// killTree 用 taskkill /T 结束整个进程树：脚本型插件常常再拉起一个子进程（如 node/python 包装器），
// 只杀父进程会留下孤儿占着端口，下次启动就变成"端口被占"的假故障。
func killTree(pid int) {
	if pid <= 0 {
		return
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	_ = kill.Run()
}
