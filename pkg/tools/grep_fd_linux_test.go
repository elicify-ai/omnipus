// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

// #920 RED pack — test 32 of read-boundary-consistency-spec.md (FR-032):
// every handle the widened grep opens is closed on every exit path. Linux
// only (own file, build tag) because it counts /proc/self/fd; the portable
// outcome assertions are test 32a (grep_test.go::TestGrepTool_NewRootExitPaths).
// Precedent for the guard: pkg/sandbox/spawn_bg_fd_test.go.
package tools

import (
	"os"
	"testing"
)

func rbOpenFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("/proc/self/fd is not readable here (%v) — FR-032 requires --- PASS, not --- SKIP, on the Linux CI legs", err)
	}
	return len(entries)
}

// TestGrepTool_NewRootClosedOnEveryExit is test 32. Each exit path first
// asserts its specified outcome (so a call that never reached the new root
// type cannot pass vacuously), then that the process's open-descriptor
// count is back to its pre-call baseline.
//
// Traces: S-5.6; FR-032.
func TestGrepTool_NewRootClosedOnEveryExit(t *testing.T) {
	rbOpenFDs(t) // skip early (loudly) where /proc/self/fd cannot be read
	for _, c := range rbExitPathCases() {
		t.Run(c.name, func(t *testing.T) {
			f := newRBFixture(t)
			// Warm-up on the same fixture: lets the audit logger and any
			// lazily-opened process-wide descriptor settle before the
			// baseline, so only the measured call's handles are counted.
			if warm := f.grepCall("needle", nil); warm.IsError {
				t.Fatalf("warm-up grep failed: %s", warm.ForLLM)
			}
			before := rbOpenFDs(t)
			res := c.run(t, f)
			c.check(t, f, res)
			if after := rbOpenFDs(t); after != before {
				t.Fatalf("FR-032 %s exit path: open descriptors %d -> %d; every os.Root the call opened must be closed", c.name, before, after)
			}
		})
	}
}
