// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 fix-round-3 RED pack — T7, found live in the UAT run (goroutine dump
// of the UAT gateway showed two read_file calls blocked in os.Open inside
// pkg/tools/resolvepath.go::(*PathHandle).Open, called from
// pkg/tools/filesystem.go::(*ReadFileTool).Execute).
//
// ORACLE (spec-derived, not implementation-derived): the read-boundary spec
// (docs/internal/specs/read-boundary-consistency-spec.md) MV-8 states
// plainly, for grep, that "on every platform, grep never opens a
// non-regular, non-directory entry (a FIFO, device file or socket) met
// during a walk or named directly by `path`" — and the spec's own edge
// cases and User Story 3 establish that #920 makes read_file AUTO-RUN
// outside the workspace exactly as grep does (D2/D3), with NO approval
// card in between. A tool that now runs unattended, on a path an agent
// picked, must not be able to freeze the turn forever just because that
// path names a FIFO — a turn is a shared resource (JUDGE-FR-060 and the
// goal-ending-on-lost-UI retirement both treat "the turn keeps running
// with nobody able to stop it" as the failure shape to avoid). Read.go's
// OWN sibling method, OpenRegularNonBlocking (resolvepath.go), states the
// contract for the image-only code path today: "a concurrent replacement
// with a FIFO cannot block the turn" — this test asserts that SAME
// contract must hold for the ordinary (non-image) read path Execute
// otherwise takes (filesystem.go::ReadFileTool.Execute: `file, err =
// handle.Open()` for anything imageFormat does not recognise), never
// merely a hang followed by an eventual read.
//
// Oracle: read_file of a FIFO — one path outside the workspace (the
// unrestricted os.Open(h.abs) branch, PathHandle.Open, h.root == nil) and
// one path inside the workspace (the confined h.root.Open(h.rel) branch,
// h.root != nil) — must return a clear refusal naming what the entry is
// (never a hang, never a successful read of FIFO "content") within a 2 s
// watchdog.
//
//go:build unix

package tools

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// readFileWithWatchdog runs tool.Execute in a goroutine and fails the test
// if no result arrives within 2 seconds — the same shape
// TestGrepGateFS_Open_FIFOSwapNeverHangs and this fix round's other hang
// tests use, so a genuine hang (not merely a slow call) is what's asserted.
func readFileWithWatchdog(t *testing.T, run func() *ToolResult) *ToolResult {
	t.Helper()
	done := make(chan *ToolResult, 1)
	go func() { done <- run() }()
	select {
	case res := <-done:
		return res
	case <-time.After(2 * time.Second):
		t.Fatal("T7: read_file hung reading a FIFO — PathHandle.Open has no O_NONBLOCK protection (only OpenRegularNonBlocking, the image-only path, has it)")
		return nil // unreachable; t.Fatal stops the goroutine that called it
	}
}

// TestReadBoundary_ReadFileFIFOOutsideWorkspaceRefusesWithoutHanging is T7's
// first scenario: an ordinary (non-image) read_file of a FIFO OUTSIDE the
// workspace — the unrestricted PathHandle.Open branch (h.root == nil),
// which #920 now lets run with no approval card at all under Auto-approve.
func TestReadBoundary_ReadFileFIFOOutsideWorkspaceRefusesWithoutHanging(t *testing.T) {
	f := newRBFixture(t)
	fifoPath := filepath.Join(f.ext, "outside.fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("mkfifo %q: %v", fifoPath, err)
	}

	res := readFileWithWatchdog(t, func() *ToolResult {
		return f.read.Execute(f.ctx, map[string]any{"path": fifoPath})
	})
	if !res.IsError {
		t.Fatalf("T7: read_file of a FIFO must be refused, got a successful result: %s", res.ForLLM)
	}
}

// TestReadBoundary_ReadFileFIFOInsideWorkspaceRefusesWithoutHanging is T7's
// second scenario: the SAME FIFO hazard through the confined branch
// (h.root != nil, h.root.Open(h.rel)) — a FIFO living inside the agent's
// own workspace.
func TestReadBoundary_ReadFileFIFOInsideWorkspaceRefusesWithoutHanging(t *testing.T) {
	f := newRBFixture(t)
	fifoPath := filepath.Join(f.ws, "inside.fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("mkfifo %q: %v", fifoPath, err)
	}

	res := readFileWithWatchdog(t, func() *ToolResult {
		return f.read.Execute(f.ctx, map[string]any{"path": "inside.fifo"})
	})
	if !res.IsError {
		t.Fatalf("T7: read_file of a workspace-local FIFO must be refused, got a successful result: %s", res.ForLLM)
	}
}
