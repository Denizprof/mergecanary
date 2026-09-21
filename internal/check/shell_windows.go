//go:build windows

package check

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
)

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	// CmdLine bypasses Go's argument quoting, which cmd.exe does not understand.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /S /C "` + command + `"`}
	// Kill the whole process tree, not just cmd.exe.
	cmd.Cancel = func() error {
		return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
	return cmd
}
