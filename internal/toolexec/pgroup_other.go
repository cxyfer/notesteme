//go:build !unix

package toolexec

import "os/exec"

// TODO(ADR-0009)：Windows 用 Job Object 結束整組子程序；目前只結束直接的子程序。
func setProcessGroup(*exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
