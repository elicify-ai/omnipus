// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests 1
// and 2.
//
// Oracle: FD-1 / FR-VA-001 ("The system MUST discover every `.view`-extension
// file reachable inside a collection's real, symlink-resolved boundary...")
// and FR-VA-003 / D-DEDUP ("The system MUST reject two views anywhere in the
// collection that declare the same `name`... evaluated over the full
// discovery result rather than one directory").
//
// Today's records.LoadViews(vaultRoot, schemas) only ever os.ReadDir's the
// ONE fixed directory records.ViewsDir(vaultRoot) == <root>/.omnipus-vault/
// views — confirmed by reading pkg/records/view.go::LoadViews, which builds
// `dir := ViewsDir(vaultRoot)` and never walks anything else. A `.view` file
// placed anywhere else in the collection is invisible to it. These tests pin
// the SPEC's requirement (discover anywhere, dedup over the whole tree) as
// the expected behaviour, using the loader that exists today as the unit
// under test — they must fail until the widened discovery (§4 step 1, the
// planned pkg/knowledge helper) replaces the fixed-directory scan.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLoadViews_FindsViewOutsideFixedViewsDirectory$' ./pkg/records/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLoadViews_DedupAcrossDirectories$' ./pkg/records/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package records

import (
	"os"
	"path/filepath"
	"testing"
)

// vaViewBody is a minimal, well-formed view declaring only the required
// `name` key (F8: only `name` is required under the ViewDef schema).
func vaViewBody(name string) string {
	return "name: " + name + "\n"
}

// TestLoadViews_FindsViewOutsideFixedViewsDirectory is TDD Plan test 1
// (TestWalkContained_FindsViewExtensionAnywhere's spirit, exercised through
// today's actual reader): a `.view`-suffixed file placed by a human directly
// in a collection subfolder — never under .omnipus-vault/views — must still
// appear in the loaded ViewSet, per FD-1/FR-VA-001 and US-1 AS-1 ("Given a
// .view file placed by a human directly in a collection subfolder... the
// view appears... exactly as a view under the old fixed directory would
// have").
//
// This currently fails because records.LoadViews never looks anywhere except
// ViewsDir(root); the file below, at "notes/weekly.view", is never read.
func TestLoadViews_FindsViewOutsideFixedViewsDirectory(t *testing.T) {
	root := t.TempDir()
	notesDir := filepath.Join(root, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("mkdir notes: %v", err)
	}
	// Spec's chosen extension is ".view" (Q1/A) — the fixture uses it
	// directly rather than the legacy ".yaml" suffix ViewsDir writers use
	// today, because FR-VA-001 is about the widened discovery LOCATION, not
	// about the extension choice (a separate, already-settled decision).
	if err := os.WriteFile(filepath.Join(notesDir, "weekly.view"), []byte(vaViewBody("weekly")), 0o644); err != nil {
		t.Fatalf("write view fixture: %v", err)
	}

	set, report, err := LoadViews(root, nil)
	if err != nil {
		t.Fatalf("LoadViews returned an unexpected error: %v", err)
	}
	if report != nil && len(report.Rejections) > 0 {
		t.Fatalf("LoadViews reported rejections for a well-formed view: %+v", report.Rejections)
	}

	v, ok := set.Get("weekly")
	if !ok {
		t.Fatalf(
			"US-1 AS-1 / FR-VA-001: a .view file placed in a collection subfolder (notes/weekly.view) "+
				"must be discovered — LoadViews found %d view(s): %v",
			set.Len(), set.Names(),
		)
	}
	wantSourcePath := filepath.Join(notesDir, "weekly.view")
	if v.SourcePath != wantSourcePath {
		t.Errorf("SourcePath = %q, want %q (the view's real on-disk location, not the legacy views dir)",
			v.SourcePath, wantSourcePath)
	}
}

// TestLoadViews_DedupAcrossDirectories is TDD Plan test 2
// (TestLoadViews_DedupAcrossDirectories): two views anywhere in the
// collection declaring the same `name` must both be rejected via the
// existing RejectViewDuplicateName code (US-1 AS-3, D-DEDUP, unchanged rule
// — only the input SET the rule runs over is widened per FR-VA-003).
//
// This currently fails because neither file lives under ViewsDir(root), so
// LoadViews never reads either one — report.Rejections is empty and
// set.Len() is 0, not "both rejected."
func TestLoadViews_DedupAcrossDirectories(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "projects", "alpha")
	dirB := filepath.Join(root, "archive")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatalf("mkdir dirA: %v", err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatalf("mkdir dirB: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirA, "open-by-owner.view"), []byte(vaViewBody("open-by-owner")), 0o644); err != nil {
		t.Fatalf("write view A: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "open-by-owner-2.view"), []byte(vaViewBody("open-by-owner")), 0o644); err != nil {
		t.Fatalf("write view B: %v", err)
	}

	set, report, err := LoadViews(root, nil)
	if err != nil {
		t.Fatalf("LoadViews returned an unexpected error: %v", err)
	}

	if _, ok := set.Get("open-by-owner"); ok {
		t.Errorf("US-1 AS-3/D-DEDUP: a name declared by two files anywhere in the collection must never " +
			"resolve to either one — the ViewSet must not contain it at all")
	}

	found := false
	for _, rej := range report.Rejections {
		if rej.Code == RejectViewDuplicateName && rej.Name == "open-by-owner" {
			found = true
			if len(rej.Paths) != 2 {
				t.Errorf("duplicate rejection for %q must name BOTH colliding paths, got %d: %v",
					rej.Name, len(rej.Paths), rej.Paths)
			}
		}
	}
	if !found {
		t.Fatalf(
			"US-1 AS-3/FR-VA-003: two views declaring the SAME name in DIFFERENT subfolders "+
				"(projects/alpha/open-by-owner.view, archive/open-by-owner-2.view) must both be rejected "+
				"as %q, evaluated over the full discovery result — got rejections: %+v (scanned: %v)",
			RejectViewDuplicateName, report.Rejections, report.ScannedFiles,
		)
	}
}
