// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !linux && !darwin && !windows

package config

// This file now covers only the platforms with no reader of their own: the
// BSDs, Solaris, Plan 9, WASM. Linux reads /proc/meminfo and cgroups
// (meminfo_linux.go), Darwin reads sysctls (meminfo_darwin.go), and Windows
// declares its own gap in its own file (meminfo_windows.go) rather than
// hiding inside this shared stub — a declared gap is a task, a shared stub is
// a place nobody looks.
//
// HISTORY, kept because it is the reason this file returns what it returns.
// MAJOR-2 (code review 2026-08-04): the non-Linux reader used to return half
// of a hardcoded 4 GB constant, i.e. a fabricated ~2 GiB with no relationship
// to the real machine. Fed through the old availableRAM/3.5 MB agent sizing,
// that fictitious value produced a default of 585 concurrent agents on ANY
// macOS/Windows/BSD box regardless of its actual hardware — a default that
// FAILED OPEN on a number nothing had measured.
//
// The reader is now two-valued, so "no signal" is a first-class answer rather
// than a value. It reports ok=false and every consumer takes its own
// unmeasurable-host branch — refuse to GROW past a conservative floor, never
// refuse to RUN. An operator on capable BSD hardware who wants more
// concurrency sets performance.max_parallel_agents explicitly, which no
// memory reading ever overrides.
//
// NEITHER this function nor meminfo_other_test.go's coverage of it is ever
// compiled or executed by any test run in this project's Linux-only CI. It is
// verified only by cross-compiling (GOOS=freebsd go build), which confirms
// the code type-checks and nothing more.

// readMemTotalBytes reports undeterminable: there is no /proc/meminfo and
// this file's platforms have no reader.
func readMemTotalBytes() (uint64, bool) {
	return 0, false
}

// readMemAvailableBytes reports undeterminable: there is no /proc/meminfo and
// this file's platforms have no reader.
func readMemAvailableBytes() (uint64, bool) {
	return 0, false
}

// readCgroupMemoryAvailableBytes: cgroups are a Linux kernel feature and are
// never present on other platforms.
func readCgroupMemoryAvailableBytes() (uint64, bool) {
	return 0, false
}

// readCgroupMemoryBudgetBytes: cgroups are a Linux kernel feature and are
// never present on other platforms.
func readCgroupMemoryBudgetBytes() (available, limit uint64, ok bool) {
	return 0, 0, false
}
