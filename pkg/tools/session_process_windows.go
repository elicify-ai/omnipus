//go:build windows

package tools

import (
	"log/slog"
	"os/exec"
	"strconv"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

func killProcessGroup(pid int) error {
	// FR-239: taskkill is a Windows system binary; it starts from the closed
	// child allowlist, not the raw gateway environment.
	taskkillCmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	taskkillCmd.Env = sandbox.ScrubGatewayEnv()
	err := taskkillCmd.Run()
	if err != nil {
		slog.Warn("killProcessGroup: taskkill failed", "pid", pid, "error", err)
	}
	return err
}
