//go:build !windows

package tunnel

import (
	"os/exec"
	"syscall"
)

func setProcAttrs(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// terminate asks the whole process group to stop (ssh/cloudflared may fork).
func terminate(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }

// kill stops the whole group for good: SIGTERM may be ignored (it is
// inherited as ignored on some CI runners), and killing only the leader
// would leave its children holding the output pipe.
func kill(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
