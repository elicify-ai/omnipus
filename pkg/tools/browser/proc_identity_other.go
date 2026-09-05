//go:build !linux

// Omnipus — process identity confirmation for FR-042a orphan reconciliation
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

// confirmProcessIsOurChrome always reports FALSE off Linux, and that is a
// DECLARED GAP rather than a defensive default (ADR-075 FR-042a).
//
// What it costs: boot reconciliation will not terminate an orphaned Chrome on
// macOS or Windows. It clears the stale marker and logs a WARN naming the pid,
// so an operator can see and end it; the process itself is left alone.
//
// Why that is the right trade, and not laziness. The only way to answer "is
// pid N running OUR Chrome?" on Linux without shelling out is /proc/<pid>/exe,
// which macOS and Windows do not have. The alternatives are all worse:
//
//   - proc_pidpath / KERN_PROCARGS2 on Darwin need cgo or an unstable sysctl
//     layout; this project is pure Go, no cgo (hard constraint 2).
//   - Shelling out to ps/lsof is forbidden on security-relevant paths
//     (hard constraint 2 again), and this path decides whether to kill a
//     process.
//   - Trusting the marker's pid alone is the actual danger. Pids are reused.
//     A marker written before a crash can name an unrelated program by the
//     time the gateway restarts, and "terminate whatever pid the file says"
//     is how a gateway kills a user's editor.
//
// So the honest answer off Linux is "cannot confirm", and the honest action on
// "cannot confirm" is to touch nothing.
func confirmProcessIsOurChrome(int) bool { return false }

// terminatePID is a no-op off Linux. Nothing calls it there without a positive
// identity confirmation, and confirmProcessIsOurChrome never gives one — this
// exists so the reconciliation code reads the same on every platform rather
// than growing a build-tagged branch at the call site.
func terminatePID(int) {}
