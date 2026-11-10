// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 RED pack — test 28 of read-boundary-consistency-spec.md: DS-4 rows
// W1-W8, run on a real windows-latest runner by the windows-tools-tests job
// (FR-031). The `_windows_test.go` suffix is the build constraint: this
// file never compiles on Linux or macOS, so a Linux run of
// `-run '^TestReadBoundary_Windows$'` finds no tests — which is exactly why
// FR-031 forbids running it from the ubuntu-latest windows-compile job.
//
// Oracles: DS-4, FR-002 (on Windows `\` is a separator for the shorthand
// test too — Ambiguity Warning 4), FR-004, FR-009.
package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestReadBoundary_Windows is test 28, one named subtest per DS-4 row (the
// FR-031 step requires all eight `--- PASS: TestReadBoundary_Windows/W<n>`).
//
// Traces: S-7.1; FR-002, FR-009, FR-031.
func TestReadBoundary_Windows(t *testing.T) {
	extHit := func(f *rbFixture) []string { return []string{rbSlash(filepath.Join(f.ext, "ext", "notes.txt"))} }

	admittedWith := func(t *testing.T, f *rbFixture, path string, want []string, readPath string, readIsDir bool) {
		t.Helper()
		res := f.grepCall("needle", rbStr(path))
		if res.IsError {
			t.Fatalf("grep path=%q: DS-4 says admitted, got error: %s", path, res.ForLLM)
		}
		if got := rbHitPaths(res.ForLLM); !rbSameSet(got, want) {
			t.Fatalf("grep path=%q: match paths = %q, want exactly %q", path, got, want)
		}
		if readPath == "" {
			return
		}
		var rr *ToolResult
		if readIsDir {
			rr = f.list.Execute(f.ctx, map[string]any{"path": readPath})
		} else {
			rr = f.read.Execute(f.ctx, map[string]any{"path": readPath})
		}
		if rr.IsError {
			t.Fatalf("DS-4 parity: read-side tool must admit %q, got: %s", readPath, rr.ForLLM)
		}
	}

	t.Run("W1", func(t *testing.T) {
		f := newRBFixture(t)
		admittedWith(t, f, f.ext, extHit(f), f.ext, true)
	})
	t.Run("W2", func(t *testing.T) {
		f := newRBFixture(t)
		fwd := filepath.ToSlash(f.ext)
		admittedWith(t, f, fwd, extHit(f), fwd, true)
	})
	t.Run("W3", func(t *testing.T) {
		f := newRBFixture(t)
		unc := `\\nonexistent-host.invalid\share\x`
		res := f.grepCall("needle", rbStr(unc))
		if !res.IsError {
			t.Fatalf("W3: an unreachable UNC path must be an error, got success:\n%s", res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "must be relative") {
			t.Fatalf("W3: a UNC path is absolute; it must never be refused as \"must be relative\": %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, unc) && !strings.Contains(res.ForLLM, filepath.ToSlash(unc)) {
			t.Errorf("W3: the error must name the path %q: %s", unc, res.ForLLM)
		}
		if rr := f.list.Execute(f.ctx, map[string]any{"path": unc}); !rr.IsError {
			t.Errorf("W3 parity: list_directory of an unreachable UNC path must error too, got: %s", rr.ForLLM)
		}
	})
	t.Run("W4", func(t *testing.T) {
		f := newRBFixture(t)
		rbWrite(t, filepath.Join(f.ws, "notes", "sub", "c.md"), "needle\n")
		admittedWith(t, f, `notes\sub`, []string{"notes/sub/c.md"}, `notes\sub`, true)
	})
	t.Run("W5", func(t *testing.T) {
		f := newRBFixture(t)
		admittedWith(t, f, rbMountName+`\src`, []string{rbMountName + "/src/b.md"}, "", false)
	})
	t.Run("W6", func(t *testing.T) {
		f := newRBFixture(t)
		rel, err := filepath.Rel(f.ws, f.ext)
		if err != nil {
			t.Fatalf("filepath.Rel(ws, ext): %v", err)
		}
		if !strings.HasPrefix(rel, `..\`) {
			t.Fatalf("precondition: <EXT> must be reached from <WS> by a backslash `..` path, got %q", rel)
		}
		admittedWith(t, f, rel, extHit(f), rel, true)
	})
	t.Run("W7", func(t *testing.T) {
		f := newRBFixture(t)
		vol := filepath.VolumeName(f.ext)
		if vol == "" {
			t.Fatalf("precondition: <EXT> %q has no volume name on Windows", f.ext)
		}
		driveRel := vol + "ext"
		g := f.grepCall("needle", rbStr(driveRel))
		l := f.list.Execute(f.ctx, map[string]any{"path": driveRel})
		if g.IsError != l.IsError {
			t.Fatalf("W7 parity: grep and list_directory must give the same admitted/refused outcome for %q; grep IsError=%v (%s), list_directory IsError=%v (%s)",
				driveRel, g.IsError, g.ForLLM, l.IsError, l.ForLLM)
		}
		if strings.Contains(g.ForLLM, "must be relative") {
			t.Fatalf("W7: a drive-relative path must never be refused as \"must be relative\": %s", g.ForLLM)
		}
	})
	t.Run("W8", func(t *testing.T) {
		f := newRBFixture(t)
		root := filepath.VolumeName(f.ext) + `\`
		res := f.grepCall("needle", rbStr(root))
		if res.IsError {
			t.Fatalf("W8: grep path=%q must be admitted, got error: %s", root, res.ForLLM)
		}
		// Same stated deviation as TestReadBoundary_VolumeRootBounded: no
		// limit-injection seam exists, so any of the DS-5 limits a root walk
		// can hit first is accepted. D7 (DS-5 limits 1-7) declares the limits
		// are unchanged; max_bytes (filegrep's TotalBytes budget,
		// DefaultMaxBytes = 256 MiB) is a valid bounded outcome per FR-011
		// alongside max_files and deadline.
		if reason := rbTruncation(res.ForLLM); reason != "max_files" && reason != "max_bytes" && reason != "deadline" {
			t.Fatalf("W8: truncation reason = %q, want max_files, max_bytes, or deadline (D7/FR-011)", reason)
		}
		if rr := f.list.Execute(f.ctx, map[string]any{"path": root}); rr.IsError {
			t.Fatalf("W8 parity: list_directory must admit %q, got: %s", root, rr.ForLLM)
		}
	})
}
