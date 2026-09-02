//go:build !linux && !darwin && !windows

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

// NOTE ON EXECUTABILITY: this file, like meminfo_other.go itself, is gated
// `//go:build !linux && !darwin && !windows` — it now covers only the
// platforms with no reader of their own (the BSDs, Solaris, Plan 9, WASM);
// Darwin and Windows have their own readers and their own tests. Every Go
// test run in this repo (local dev pod and CI, per CLAUDE.md's "Testing &
// building" section) runs on Linux, so this file is NEVER compiled or
// executed by any test invocation used to validate this project. It is verified here only by cross-compilation from Linux
// (`GOOS=darwin`/`GOOS=windows go vet`/`go test -c`), which confirms the code
// type-checks and the assertions are internally consistent, but NOT that the
// runtime behavior is correct on a real non-Linux machine — there is no way
// to get that signal from this environment. This gap is the same one MAJOR-2
// (2026-08-04 code review) flagged: nothing catches a bug on this path
// because nothing ever runs it.

// TestReadMemAvailableBytes_NonLinux_FailsConservativeNotFictitious
// reproduces MAJOR-2: before that fix, readMemAvailableBytes() on non-Linux
// returned readMemTotalBytes()/2 — half of a hardcoded 4 GB constant that has
// no relationship to the real machine. Fed through the old
// availableRAM/3.5 MB agent sizing, that fictitious ~2 GiB produced a default
// of 585 concurrent agents on any BSD box regardless of its actual hardware —
// a "fails open" default with no basis in reality.
//
// The reader is now two-valued, so "no signal" is a first-class ANSWER rather
// than a value, and the assertion is on ok rather than on a magic number. A
// platform with no reader must report undeterminable; every consumer then
// takes its own documented unmeasurable-host branch.
func TestReadMemAvailableBytes_NonLinux_FailsConservativeNotFictitious(t *testing.T) {
	got, ok := readMemAvailableBytes()
	if ok {
		t.Fatalf("readMemAvailableBytes() on a platform with no reader returned ok=true (%d bytes), want ok=false — a fabricated figure derived from a constant is worse than an honest 'cannot determine'", got)
	}
	if got != 0 {
		t.Fatalf("readMemAvailableBytes() on a platform with no reader = %d, want 0 alongside ok=false", got)
	}
}

// TestReadMemTotalBytes_NonLinux_FailsConservativeNotFictitious is the
// companion assertion on the total reader, which used to be where the
// fabricated 4 GB constant actually lived.
func TestReadMemTotalBytes_NonLinux_FailsConservativeNotFictitious(t *testing.T) {
	got, ok := readMemTotalBytes()
	if ok {
		t.Fatalf("readMemTotalBytes() on a platform with no reader returned ok=true (%d bytes), want ok=false", got)
	}
}

// TestAvailableRAMBytes_NonLinux_IsUndeterminable replaces the deleted
// TestAutoDetectMaxParallel_NonLinux_DefensiveFloorNotFictitious. That test
// asserted the end-to-end effect of the memory formula, which no longer
// exists (there is no computed default any more). Its INTENT — a platform
// with no memory signal must not produce a fabricated capacity — survives
// here as an assertion on the accessor itself, and in pkg/agent's
// unmeasurable-host admission tests for the consumer half.
func TestAvailableRAMBytes_NonLinux_IsUndeterminable(t *testing.T) {
	got, ok := availableRAMBytes()
	if ok {
		t.Fatalf("availableRAMBytes() on a platform with no reader = (%d, true), want ok=false", got)
	}
}

// TestReadCgroupMemoryAvailableBytes_NonLinux_AlwaysAbsent verifies cgroups
// (a Linux kernel feature) are correctly reported as never present on other
// platforms, so availableRAMBytes() has no determinable signal at all there.
func TestReadCgroupMemoryAvailableBytes_NonLinux_AlwaysAbsent(t *testing.T) {
	_, ok := readCgroupMemoryAvailableBytes()
	if ok {
		t.Fatal("readCgroupMemoryAvailableBytes() ok = true on non-Linux, want false (cgroups are Linux-only)")
	}
}
