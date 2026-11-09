//go:build windows

package session

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func configureCommandCancellation(command *exec.Cmd) {
	command.Cancel = func() error {
		system, err := windows.GetSystemDirectory()
		if err != nil {
			return command.Process.Kill()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		kill := exec.CommandContext(ctx, filepath.Join(system, "taskkill.exe"), "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid))
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := kill.Run(); err != nil {
			return command.Process.Kill()
		}
		return nil
	}
}
