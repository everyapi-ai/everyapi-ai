//go:build windows

package workspace

import "os/exec"

func detachCommand(command *exec.Cmd) {}
