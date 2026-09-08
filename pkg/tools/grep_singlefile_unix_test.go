//go:build !windows

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Unix-only coverage for DEFECT-G1(b)'s remaining honest-error cases:
// permission-denied (Unix file mode bits) and a non-regular, non-directory
// `path` target (a FIFO via syscall.Mkfifo, unix-only per
// rest_clivalidate_fifo_test.go's own precedent). Neither is meaningfully
// testable on Windows the same way.
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestGrepTool_ScopeSingleFile_PermissionDeniedIsHonest proves a Stat
// failure classified as fs.ErrPermission renders a message that says so,
// never the old blanket "not found" (which would be doubly wrong here: the
// path both exists AND is not a naming problem).
func TestGrepTool_ScopeSingleFile_PermissionDeniedIsHonest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: Unix permission bits do not block access")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.MkdirAll(locked, 0o700); err != nil {
		t.Fatalf("seed locked dir: %v", err)
	}
	target := filepath.Join(locked, "secret.txt")
	mustWriteFile(t, target, "needle\n")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod locked dir: %v", err)
	}
	defer func() {
		if err := os.Chmod(locked, 0o700); err != nil {
			t.Fatalf("restore locked dir perms: %v", err)
		}
	}()

	tool := newTestGrepTool(t, dir)
	res := tool.Execute(context.Background(), map[string]any{
		"pattern": "needle",
		"path":    "locked/secret.txt",
	})
	if !res.IsError {
		t.Fatalf("a permission-denied `path` must be refused, got success:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "permission denied") {
		t.Fatalf("expected an honest permission-denied message, got: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "not found") || strings.Contains(res.ForLLM, "does not exist") {
		t.Fatalf("must not claim the path is missing when the real problem is permissions, got: %s", res.ForLLM)
	}
}

// TestGrepTool_ScopeSingleFile_NonRegularKindIsHonest proves `path` naming
// something that is neither a directory nor a regular file (a FIFO) is
// refused with a message naming what it actually is, never a claim that it
// does not exist. Stat (not Open) on a FIFO never blocks, so this cannot
// hang even though nothing writes to the pipe.
func TestGrepTool_ScopeSingleFile_NonRegularKindIsHonest(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create a FIFO on this filesystem: %v", err)
	}

	tool := newTestGrepTool(t, dir)
	res := tool.Execute(context.Background(), map[string]any{
		"pattern": "needle",
		"path":    "pipe",
	})
	if !res.IsError {
		t.Fatalf("a `path` naming a FIFO must be refused, got success:\n%s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "does not exist") || strings.Contains(res.ForLLM, "not found") {
		t.Fatalf("a FIFO exists — the message must not claim it doesn't, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "not a directory or a regular file") {
		t.Fatalf("expected a message naming what the path actually is, got: %s", res.ForLLM)
	}
}
