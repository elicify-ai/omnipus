// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build darwin

package preview

import (
	"os/exec"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// hooksFor wraps the launcher under Seatbelt with the production per-turn
// profile shape (system preamble, Mach lookups = 2) plus the program's own
// folders. Seatbelt is inherited across execve, so the target runs under the
// same profile. Without an available Seatbelt backend the program still runs
// under the AD-6 limits and the result discloses it (FQ2 r1).
func hooksFor(c confinement) spawnHooks {
	backend := sandbox.CurrentSeatbeltBackend()
	if backend == nil || !backend.Available() {
		return spawnHooks{unconfined: "the macOS sandbox is not active"}
	}
	policy := c.policy()
	return spawnHooks{
		prepare: func(cmd *exec.Cmd) error { return backend.ApplyToCmd(cmd, policy) },
	}
}
