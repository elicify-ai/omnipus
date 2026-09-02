//go:build linux

// Omnipus — process identity confirmation for FR-042a orphan reconciliation
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// confirmProcessIsOurChrome reports whether pid is a Chromium/Chrome binary
// this gateway family would have launched.
//
// Boot reconciliation (pool.go's ReconcileMarkers) will TERMINATE a process on
// the strength of this answer, so it must be conservative in one direction
// only: a false NO costs a leftover Chrome that lives until the machine
// reboots; a false YES kills a process that was never ours. Pids are reused,
// and a marker written minutes ago can name a completely unrelated program by
// the time a gateway restarts — which is precisely why the marker's pid alone
// is not sufficient evidence and this check exists.
//
// It reads /proc/<pid>/exe, which is the kernel's own answer about what a
// process is actually executing, not a string the process chose for itself
// (unlike /proc/<pid>/cmdline, which a process can rewrite). That makes this
// Linux-only; see the non-Linux file for what happens elsewhere.
func confirmProcessIsOurChrome(pid int) bool {
	if pid <= 0 {
		return false
	}
	exe, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return false
	}
	base := strings.ToLower(filepath.Base(exe))
	switch {
	case base == "chrome", base == "chromium", base == "chromium-browser":
		return true
	case base == "google-chrome", base == "google-chrome-stable":
		return true
	case base == "chrome-headless-shell":
		return true
	}
	return false
}

// terminatePID asks pid to exit. SIGTERM, not SIGKILL: Chrome flushes its
// profile — cookies, Local Storage, the session file — on a clean shutdown,
// and the whole point of keeping the profile directory is that the workspace
// is still logged in next time. SIGKILL on an orphan would risk leaving that
// profile half-written, turning a tidy-up into a logout.
func terminatePID(pid int) {
	if pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
}
