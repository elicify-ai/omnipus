// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !linux

package config

// readMemTotalBytes returns a conservative 4 GB constant on non-Linux platforms.
// The auto-detect heuristic in autoDetectMaxParallel is also floored/capped by
// clampParallel, so a bad estimate here still yields a bounded, sane default.
func readMemTotalBytes() uint64 {
	return 4 * 1024 * 1024 * 1024 // 4 GB
}

// readMemAvailableBytes has no real memory signal to read on non-Linux
// platforms — there is no /proc/meminfo, and this project takes on no
// per-OS memory-query code (sysctl on Darwin/BSD, GlobalMemoryStatusEx on
// Windows) to get one (CLAUDE.md's pure-Go/single-binary/minimal-footprint
// constraints; that real implementation is future work, not done here).
//
// MAJOR-2 (code review 2026-08-04): this function previously returned
// readMemTotalBytes()/2 — half of the hardcoded 4 GB fallback constant,
// i.e. a fabricated ~2 GiB with no relationship to the real machine. Fed
// through autoDetectMaxParallel's availableRAM/~3.5MB sizing, that
// fictitious value produced a default of 585 concurrent agents on ANY
// macOS/Windows/BSD box (or a Linux box whose /proc/meminfo is unreadable,
// e.g. gVisor) regardless of its actual hardware — a "fails open" default,
// replacing the old (conservative-by-construction) default of 2.
//
// This now deliberately returns 0: with no real signal, autoDetectMaxParallel
// (via clampParallel's autoDetectFloorParallel) lands on the same
// conservative floor of 2 non-Linux platforms shipped before this
// regression — failing CONSERVATIVE and saying so, rather than failing open
// on a number invented from a constant that was never meant to model
// availability in the first place. An operator on capable non-Linux
// hardware who wants more concurrency sets performance.max_parallel_agents
// explicitly (clampParallelExplicit honors it outright, never overridden by
// this auto-detect path — see EffectiveMaxParallelAgents).
//
// NEITHER this function nor meminfo_other_test.go's coverage of it is ever
// compiled or executed by any test run in this Linux-only dev pod or CI (see
// CLAUDE.md's "Testing & building" section) — this file's `//go:build !linux`
// constraint means the bug above shipped with nothing able to catch it. The
// accompanying test file is verified only by cross-compiling for
// GOOS=windows/darwin from Linux (confirms it type-checks), never by
// actually running it on a non-Linux machine.
func readMemAvailableBytes() uint64 {
	return 0
}

// readCgroupMemoryAvailableBytes: cgroups are a Linux kernel feature and are
// never present on other platforms.
func readCgroupMemoryAvailableBytes() (uint64, bool) {
	return 0, false
}
