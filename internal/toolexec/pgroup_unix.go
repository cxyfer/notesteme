//go:build unix

package toolexec

import (
	"os/exec"
	"syscall"
)

// setProcessGroup 讓子程序自成一個 process group。
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup 結束子程序所在的整個 process group；group 已不存在時視為成功。
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// 負的 pid 代表整個 group（Setpgid 後 pgid 等於子程序的 pid）。
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}
