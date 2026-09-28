// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 RED pack — test 4a of read-boundary-consistency-spec.md.
//
// Oracles: FR-010 (the error-vs-lost boundary is one existence check right
// after the single read decision; a root that existed at that check and is
// gone when opened is carried as an unreachable root -> truncated root_lost,
// never an error, never a silent zero), MV-3 (a lost root is still listed in
// the roots row), DS-1 row 25.
package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestReadBoundary_AbsoluteRootLost is test 4a (S-1.14). The seam
// grepScopeStatOpenHook removes <EXT>/gone once, synchronously, after the
// admission and the existence check and before the root open.
//
// Traces: S-1.14; FR-010, FR-021.
func TestReadBoundary_AbsoluteRootLost(t *testing.T) {
	f := newRBFixture(t)
	gone := filepath.Join(f.ext, "gone")
	rbWrite(t, filepath.Join(gone, "n.txt"), "needle\n")
	installGrepScopeLostHook(t, gone)

	res := f.grepCall("needle", rbStr(gone))
	if res.IsError {
		t.Fatalf("S-1.14: a root lost after admission is not an error, got: %s", res.ForLLM)
	}
	if reason := rbTruncation(res.ForLLM); reason != "root_lost" {
		t.Fatalf("S-1.14: truncation reason = %q, want root_lost:\n%s", reason, res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, rbSlash(gone)) && !strings.Contains(res.ForLLM, gone) {
		t.Errorf("S-1.14: the result must name the lost root %q:\n%s", gone, res.ForLLM)
	}
	rows := f.rows(t)
	rbAssertOneRootsRow(t, rows, []string{gone}, gone)
	if denials := rbRowsFor(rows, rbAccessDeniedEvent, ""); len(denials) != 0 {
		t.Errorf("S-1.14: no path.access_denied row may be written, got %+v", denials)
	}
}
