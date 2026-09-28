// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 19 and 20.
//
// Oracle: D-WRITE-IDENTITY / CRIT-001 / FR-VA-005 / FR-VA-005a. An upsert of
// an existing `name` must target that view's discovered SourcePath, never a
// name-reconstructed path (US-1 AS-5, Dataset F-1); a create whose default
// path already exists — whatever it contains, view or not — must be
// refused, naming the conflict, never silently overwritten (US-1 AS-6,
// Dataset F-2/D2).
//
// Today write_view/create_view (pkg/knowledge/knowledge_configure.go,
// knowledge_configure_create_view.go) hard-code their destination as
// filepath.Join(records.ViewsDir(root), viewName+controlPlaneFileExt) — a
// deterministic path built from the NAME alone, never from a discovered
// SourcePath. create_view's only pre-write check
// (viewNameCollisionRefusal via records.LoadViews) only ever catches a name
// that ALREADY PARSES as a loaded view; it says nothing about an unrelated
// or unparseable filesystem entry already sitting at the exact destination
// path, and overwriteControlPlaneFile has no existence check of its own —
// verified by reading both functions in full.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestWriteView_UpsertTargetsExistingSourcePath$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestCreateView_RefusesOccupiedDefaultPath$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestWriteView_UpsertTargetsExistingSourcePath is TDD Plan test 19
// (US-1 AS-5, CRIT-001, Dataset F-1): write_view on an existing name must
// write to that view's CURRENT discovered SourcePath, never create a SECOND
// file for the same name at the deterministic ViewsDir path.
//
// A view named "weekly" is hand-placed OUTSIDE the legacy ViewsDir (a
// location only reachable once discovery-anywhere lands, exercised here by
// planting the file directly) — the exact "already exists elsewhere" shape
// D-WRITE-IDENTITY's upsert rule must handle. write_view is then called with
// that same name; today it can only ever see records.ViewsDir(root), so it
// creates a SECOND "weekly" file there instead of updating the existing one
// — precisely CRIT-001's failure case.
func TestWriteView_UpsertTargetsExistingSourcePath(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	require.False(t, tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "widget",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties": map[string]any{
				"status": map[string]any{"type": "enum", "values": []any{"draft", "shipped"}},
			},
		},
	}).IsError)

	// The pre-existing view, hand-placed anywhere other than ViewsDir.
	existingPath := filepath.Join(root, "notes", "weekly.view")
	require.NoError(t, os.MkdirAll(filepath.Dir(existingPath), 0o755))
	require.NoError(t, os.WriteFile(existingPath, []byte("name: weekly\nlabel: Original\ntype: widget\n"), 0o644))

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "weekly",
		"definition": map[string]any{
			"type":   "widget",
			"filter": map[string]any{"property": "status", "op": "=", "value": "draft"},
		},
	})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)

	legacyPath := filepath.Join(records.ViewsDir(root), "weekly.yaml")
	if _, statErr := os.Stat(legacyPath); statErr == nil {
		t.Errorf(
			"US-1 AS-5/CRIT-001/D-WRITE-IDENTITY: write_view created a SECOND file for the existing name "+
				"\"weekly\" at %s, instead of writing to the view's already-discovered location %s — an "+
				"upsert of an existing name must target that view's OWN SourcePath, never a "+
				"name-reconstructed path",
			legacyPath, existingPath,
		)
	}
}

// TestCreateView_RefusesOccupiedDefaultPath is TDD Plan test 20
// (US-1 AS-6, CRIT-001, Dataset F-2/D2): a create whose default path already
// exists — whatever it contains, view or not — must be refused, naming the
// conflict, never silently overwritten.
//
// The occupant here is a file that does NOT parse as a view (so
// viewNameCollisionRefusal's records.LoadViews-based check cannot see it at
// all) sitting exactly at create_view's deterministic destination
// (records.ViewsDir(root)/report.yaml). Today's create_view has no
// existence check at the write step (overwriteControlPlaneFile), so it
// silently clobbers it.
func TestCreateView_RefusesOccupiedDefaultPath(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	require.NoError(t, os.MkdirAll(records.ViewsDir(root), 0o755))
	conflictPath := filepath.Join(records.ViewsDir(root), "report.yaml")
	unrelatedContent := "this is not a view at all, just an occupant of the default path\n"
	require.NoError(t, os.WriteFile(conflictPath, []byte(unrelatedContent), 0o644))

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "report", "kind": "table",
	})

	if !res.IsError {
		t.Errorf(
			"US-1 AS-6/CRIT-001/D-WRITE-IDENTITY: create_view name=report succeeded even though %s "+
				"already existed and was not a view — a create whose default path is already occupied "+
				"MUST be refused, naming the conflict, never silently overwritten. Result: %s",
			conflictPath, res.ForLLM,
		)
	}

	got, rerr := os.ReadFile(conflictPath)
	require.NoError(t, rerr, "the occupied path must still exist")
	if string(got) != unrelatedContent {
		t.Fatalf(
			"the pre-existing occupant of %s was overwritten by create_view — nothing may be written "+
				"until the caller resolves the conflict.\nwant (unchanged): %q\ngot: %q",
			conflictPath, unrelatedContent, string(got),
		)
	}
}
