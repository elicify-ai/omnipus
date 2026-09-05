// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package config

// Windows has NO memory reader. This file exists so that fact is DECLARED in
// one obvious place rather than defaulted through a shared non-Linux stub
// where nobody would look for it (FR-066).
//
// What this means in practice, stated plainly because an operator deploying
// on Windows is entitled to know it: every memory-derived control in this
// process — the browser pool's admission gate, agent admission's live
// headroom check — sees "availability cannot be determined" on Windows and
// takes its unmeasurable-host branch. Neither refuses to run; both refuse to
// GROW past a conservative floor. Windows browser support is therefore
// DEGRADED-UNSUPPORTED: it works, at a fixed floor, and no amount of physical
// RAM on the machine will raise it. An operator who wants more concurrency on
// Windows sets performance.max_parallel_agents explicitly, which is never
// overridden by any memory reading.
//
// This is a DIFFERENT situation from a Linux host with no readable
// /proc/meminfo (gVisor, distroless, a hardened seccomp profile). That host
// is a SUPPORTED deployment that happens to be unmeasurable, and the same
// ok=false branch serves it. Windows is unmeasurable because nobody wrote the
// reader. The two must not be presented to operators as one platform class:
// the Linux case is a deployment choice with a documented consequence, the
// Windows case is a gap in this codebase.
//
// THE FIX ROUTE, so this is a task and not a mystery: call
// GlobalMemoryStatusEx (kernel32.dll) via
// golang.org/x/sys/windows.NewLazySystemDLL — which keeps the pure-Go,
// no-CGo constraint — and fill a MEMORYSTATUSEX struct. Its
// ullTotalPhys/ullAvailPhys fields map directly onto the (total, available)
// pair the rest of this package wants, and ullAvailPhys is much closer to
// Linux's MemAvailable than Darwin's assembled approximation is. As of
// golang.org/x/sys v0.47.0 (the version in go.mod) that package wraps
// NEITHER GlobalMemoryStatusEx NOR MEMORYSTATUSEX, so the struct and the
// syscall wrapper have to be declared here by hand. That is the entire
// reason this is a placeholder rather than an implementation: it is a
// half-day of hand-written syscall plumbing that nobody has been able to
// test on a Windows host, and shipping an untested memory reader that a
// hard admission gate depends on is worse than shipping an honest ok=false.

// readMemTotalBytes reports undeterminable on Windows. See the file comment.
func readMemTotalBytes() (uint64, bool) {
	return 0, false
}

// readMemAvailableBytes reports undeterminable on Windows. See the file
// comment for what that costs an operator and how to fix it.
func readMemAvailableBytes() (uint64, bool) {
	return 0, false
}

// readCgroupMemoryAvailableBytes: cgroups are a Linux kernel feature and are
// never present on Windows.
func readCgroupMemoryAvailableBytes() (uint64, bool) {
	return 0, false
}

// readCgroupMemoryBudgetBytes: cgroups are a Linux kernel feature and are
// never present on Windows.
func readCgroupMemoryBudgetBytes() (available, limit uint64, ok bool) {
	return 0, 0, false
}
