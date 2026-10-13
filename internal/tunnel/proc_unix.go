//go:build !windows

package tunnel

import (
	"os/exec"
	"syscall"
)

func setProcAttrs(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// terminate signals the whole process group (ssh/cloudflared may fork).
func terminate(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
