// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build !windows && !linux && !darwin

package daemon

// isZombie reports whether the process is in zombie state.
//
// NOT IMPLEMENTED on this platform (FreeBSD, NetBSD, OpenBSD, DragonFly,
// Solaris/Illumos, AIX): the pinned golang.org/x/sys (v0.48.0) exposes no
// kinfo_proc / kinfo_proc2 sysctl wrapper outside darwin, and the raw
// SysctlRaw bytes cannot be decoded without hand-copied struct layouts whose
// status-field offset differs per BSD and per architecture — a wrong offset
// reads a garbage byte and silently misreports liveness, which is guessing
// rather than detection.
//
// Per the graceful-degradation constraint (CLAUDE.md Hard Constraint #4) we
// keep the pre-existing behaviour — assume not zombie — rather than guess.
// Consequence: a dead-but-unreaped gateway is still reported alive here, and
// its PID file is not cleared, exactly as before the per-platform zombie
// files were introduced. A real fix needs either a newer x/sys with BSD
// kinfo_proc wrappers or verified per-BSD struct definitions.
func isZombie(_ int) bool {
	return false
}
