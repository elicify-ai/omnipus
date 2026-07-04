//go:build windows

package tools

import (
	"log/slog"
	"os/exec"
	"strconv"
)

func killProcessGroup(pid int) error {
	err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
	if err != nil {
		slog.Warn("killProcessGroup: taskkill failed", "pid", pid, "error", err)
	}
	return err
}
