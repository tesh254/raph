//go:build !windows

package syncer

import (
	"os/exec"
	"syscall"
)

func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func stopProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}
