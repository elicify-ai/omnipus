// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

// #920 founder decision D13 (read-boundary-consistency-interview.md,
// 2026-09-27): grep's walk always skips the Linux kernel pseudo-folders
// /proc, /sys and /dev, and never content-reads device files, pipes or
// sockets. read_file of a named file there is unchanged.
//
// Oracle: D13's own wording. The RED pack had no oracle for it (D13 was
// decided after RED), so this test was written by backend-lead at GREEN,
// shown red on the tree that already widened grep but did not yet skip the
// pseudo-folders, then green.
//
// Linux only (build tag): /proc and /sys are Linux kernel file systems.
package tools

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// rbWithWatchdog runs one grep call and fails loudly if it does not return
// within limit — a blocking read of a pipe or a kernel file must surface as
// a failure, never as a hung test binary.
func rbWithWatchdog(t *testing.T, limit time.Duration, call func() *ToolResult) *ToolResult {
	t.Helper()
	done := make(chan *ToolResult, 1)
	go func() { done <- call() }()
	select {
	case res := <-done:
		return res
	case <-time.After(limit):
		t.Fatalf("grep did not return within %s (D13: a pipe or kernel file must never block the walk)", limit)
		return nil
	}
}

func rbAssertNoPseudoHits(t *testing.T, out string) {
	t.Helper()
	for _, h := range rbHitPaths(out) {
		for _, p := range []string{"/proc/", "/sys/", "/dev/"} {
			if strings.HasPrefix(h, p) {
				t.Errorf("D13: match %q lies under the kernel pseudo-folder %s", h, strings.TrimSuffix(p, "/"))
			}
		}
	}
}

// TestGrepTool_SkipsKernelPseudoFolders is D13's test.
func TestGrepTool_SkipsKernelPseudoFolders(t *testing.T) {
	for _, p := range []string{"/proc/self/status", "/dev/null"} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("precondition: %s must exist on a Linux host: %v", p, err)
		}
	}

	t.Run("a path under /proc is refused, never walked", func(t *testing.T) {
		f := newRBFixture(t)
		res := rbWithWatchdog(t, 60*time.Second, func() *ToolResult { return f.grepCall("status", rbStr("/proc/self")) })
		rbAssertNoPseudoHits(t, res.ForLLM)
		if !res.IsError {
			t.Fatalf("D13: grep path=/proc/self must be refused (the walk never enters /proc), got success:\n%.2000s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "kernel pseudo-folder") {
			t.Fatalf("D13: the refusal must say why (kernel pseudo-folder), got: %s", res.ForLLM)
		}
		// read_file of a named file there is unchanged (D13).
		if rr := f.read.Execute(f.ctx, map[string]any{"path": "/proc/self/status"}); rr.IsError {
			t.Fatalf("D13: read_file of a named /proc file is unchanged and must succeed, got: %s", rr.ForLLM)
		}
	})

	t.Run("a volume-root walk never enters /proc, /sys or /dev", func(t *testing.T) {
		f := newRBFixture(t)
		// "null" name-matches /dev/null, which a walk from / reaches early
		// (alphabetical order: bin, boot, dev, ...).
		res := rbWithWatchdog(t, 60*time.Second, func() *ToolResult { return f.grepCall("null", rbStr("/")) })
		if res.IsError {
			t.Fatalf("DS-1 row 23: grep path=/ is admitted, got error: %s", res.ForLLM)
		}
		rbAssertNoPseudoHits(t, res.ForLLM)
	})

	t.Run("a pipe is never opened: a FIFO .gitignore does not block the walk", func(t *testing.T) {
		f := newRBFixture(t)
		dir := filepath.Join(f.ws, "pipes")
		rbWrite(t, filepath.Join(dir, "a.txt"), "needle\n")
		if err := syscall.Mkfifo(filepath.Join(dir, ".gitignore"), 0o600); err != nil {
			t.Fatalf("mkfifo: %v", err)
		}
		if err := syscall.Mkfifo(filepath.Join(dir, "needle.fifo"), 0o600); err != nil {
			t.Fatalf("mkfifo: %v", err)
		}
		res := rbWithWatchdog(t, 30*time.Second, func() *ToolResult { return f.grepCall("needle", rbStr("pipes")) })
		if res.IsError {
			t.Fatalf("grep path=pipes failed: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "pipes/a.txt:1:") {
			t.Errorf("the regular file must still be content-searched, got:\n%s", res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "pipes/needle.fifo:") {
			t.Errorf("D13: a FIFO must never be content-read (a name match is fine), got:\n%s", res.ForLLM)
		}
	})
}
