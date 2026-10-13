//go:build windows

package tunnel

import "os/exec"

func setProcAttrs(*exec.Cmd) {}

func terminate(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
