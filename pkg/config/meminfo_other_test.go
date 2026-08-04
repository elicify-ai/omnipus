//go:build !linux

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

// NOTE ON EXECUTABILITY: this file, like meminfo_other.go itself, is gated
// `//go:build !linux`. Every Go test run in this repo (local dev pod and CI,
// per CLAUDE.md's "Testing & building" section) runs on Linux, so this file
// is NEVER compiled or executed by any test invocation used to validate this
// project. It is verified here only by cross-compilation from Linux
// (`GOOS=darwin`/`GOOS=windows go vet`/`go test -c`), which confirms the code
// type-checks and the assertions are internally consistent, but NOT that the
// runtime behavior is correct on a real non-Linux machine — there is no way
// to get that signal from this environment. This gap is the same one MAJOR-2
// (2026-08-04 code review) flagged: nothing catches a bug on this path
// because nothing ever runs it.

// TestReadMemAvailableBytes_NonLinux_FailsConservativeNotFictitious
// reproduces MAJOR-2: before this fix, readMemAvailableBytes() on non-Linux
// returned readMemTotalBytes()/2 — half of a hardcoded 4 GB constant that has
// no relationship to the real machine. Fed through autoDetectMaxParallel's
// availableRAM/3.5MB sizing, that fictitious ~2 GiB produced a default of
// 585 concurrent agents on any macOS/Windows/BSD box (or Linux box with an
// unreadable /proc/meminfo, e.g. gVisor) regardless of its actual hardware —
// a "fails open" default with no basis in reality, replacing the old (and
// correct-by-luck) conservative default of 2.
//
// The fix returns 0 (no real signal available without per-OS memory-query
// code this project has not built) so autoDetectMaxParallel's clampParallel
// floor takes over deliberately, restoring the old conservative-by-design
// behavior instead of a coincidentally-conservative one.
func TestReadMemAvailableBytes_NonLinux_FailsConservativeNotFictitious(t *testing.T) {
	got := readMemAvailableBytes()
	if got != 0 {
		t.Fatalf("readMemAvailableBytes() on non-Linux = %d, want 0 (no real memory signal available; must fail conservative rather than deriving a fictitious value from readMemTotalBytes())", got)
	}
}

// TestAutoDetectMaxParallel_NonLinux_DefensiveFloorNotFictitious verifies the
// end-to-end effect: with no real memory signal, autoDetectMaxParallel()
// must land on the documented floor (2) rather than the fictitious 585 the
// pre-fix half-of-4GB constant produced.
func TestAutoDetectMaxParallel_NonLinux_DefensiveFloorNotFictitious(t *testing.T) {
	got := autoDetectMaxParallel()
	const wantFloor = 2
	if got != wantFloor {
		t.Fatalf("autoDetectMaxParallel() on non-Linux = %d, want %d (the auto-detect floor — no fabricated 585-agent default from a fictitious memory reading)", got, wantFloor)
	}
}

// TestReadCgroupMemoryAvailableBytes_NonLinux_AlwaysAbsent verifies cgroups
// (a Linux kernel feature) are correctly reported as never present on other
// platforms, so availableRAMBytes() always falls through to the meminfo-
// derived (now conservative-zero) reading.
func TestReadCgroupMemoryAvailableBytes_NonLinux_AlwaysAbsent(t *testing.T) {
	_, ok := readCgroupMemoryAvailableBytes()
	if ok {
		t.Fatal("readCgroupMemoryAvailableBytes() ok = true on non-Linux, want false (cgroups are Linux-only)")
	}
}
