//go:build windows

package tools

import "os/exec"

func prepareCommandForTermination(cmd *exec.Cmd) {
	// no-op on Windows
}
