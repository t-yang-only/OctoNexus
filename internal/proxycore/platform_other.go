//go:build !windows

package proxycore

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func setHideWindow(_ *exec.Cmd) {}

// killTree 先杀进程组，再兜底杀自身（内核不会拉子进程，但统一口径更稳）。
func killTree(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
	}
	_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
}
