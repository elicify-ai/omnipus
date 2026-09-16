// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package daemon

import (
	"fmt"
	"os"
	"strings"
)

// isZombie reports whether the process is in zombie state by reading
// /proc/<pid>/status. Returns false on any read error (conservative: assume
// not zombie).
func isZombie(pid int) bool {
	statusPath := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "State:") {
			return strings.Contains(line, "Z")
		}
	}
	return false
}
