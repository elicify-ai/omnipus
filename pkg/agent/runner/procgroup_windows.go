//go:build windows

// procgroup_windows.go — ADR-057 U22 (W9c, FR-029) Windows half.
//
// FR-029 is explicitly POSIX-only: Setpgid and negative-pid process-group
// signalling have no equivalent in this codebase on Windows. This mirrors
// how pkg/fileutil/flock_windows.go documents its own no-op scoping for a
// POSIX-only guarantee elsewhere in the tree (see CLAUDE.md's storage
// section). Each driver's cmd.Cancel therefore keeps Go's os/exec default
// behavior on Windows — it signals only the direct child PID. A future
// Windows compensation (e.g. Job Objects, matching this repo's sandbox
// backend for Windows children) is out of scope for this unit.
package runner

import "os/exec"

// u22SetupProcessGroup is a documented no-op on Windows — Setpgid has no
// Windows equivalent. See package doc comment.
func u22SetupProcessGroup(_ *exec.Cmd) {}

// u22InstallGroupCancel is a documented no-op on Windows — process-group
// signalling has no equivalent, so cmd.Cancel keeps its Go-default
// direct-child-only behavior. See package doc comment.
func u22InstallGroupCancel(_ *exec.Cmd) {}
