// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

// #920 RED pack — test 29 of read-boundary-consistency-spec.md (S-7.2,
// US-7 AS-4): on Linux and macOS a backslash is part of a file name,
// exactly as read_file treats it. Passes on today's code BY DESIGN — it is
// FR-009's regression pin that the Windows-aware path parsing GREEN adds
// does not turn `\` into a separator on POSIX.
package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestReadBoundary_BackslashLiteralUnix is test 29.
//
// Traces: S-7.2; FR-009.
func TestReadBoundary_BackslashLiteralUnix(t *testing.T) {
	f := newRBFixture(t)
	dir := `notes\sub`
	rbWrite(t, filepath.Join(f.ws, dir, "c.md"), "needle\n")

	res := f.grepCall("needle", rbStr(dir))
	if res.IsError {
		t.Fatalf("S-7.2: grep path=%q must search the folder literally named %q, got: %s", dir, dir, res.ForLLM)
	}
	want := []string{dir + "/c.md"}
	if got := rbHitPaths(res.ForLLM); !rbSameSet(got, want) {
		t.Fatalf("S-7.2: match paths = %q, want exactly %q (begin %q)", got, want, dir+"/")
	}
	lr := f.list.Execute(f.ctx, map[string]any{"path": dir})
	if lr.IsError || !strings.Contains(lr.ForLLM, "c.md") {
		t.Fatalf("S-7.2 parity: list_directory(%q) must list c.md, got IsError=%v: %s", dir, lr.IsError, lr.ForLLM)
	}
}
