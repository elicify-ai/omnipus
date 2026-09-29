// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 48 and 68.
//
// Oracle: FD-6 ("`write_view` on a view whose current `ViewSet` entry carries
// `derived_from` refuses the write, naming the managing `.base` file")
// resolving R2-MAJ-003, and FR-VA-008e; plus US-2 AS-6 / R2-MIN-007 ("every
// one of those surfaces [knowledge_describe, knowledge_find,
// knowledge_configure] states that the view is derived and names its source
// `.base` file — never silently presenting it as an ordinary,
// independently-owned file").
//
// UNBLOCKED for test 48 (2026-09-29, va-qa2 dispatch): generated.ViewDef.
// DerivedFrom now exists on the merged feature branch (contract commit
// 562acdc32) — verified by reading pkg/api/generated/openapi_types.gen.go.
// Test 48 is rewritten below as a real assertion against execWriteView, the
// real entry point: verified by reading it (pkg/knowledge/
// knowledge_configure.go::execWriteView), the function has NO code path that
// reads the EXISTING on-disk file's `derived_from` before overwriting it —
// it only collision-checks OTHER views' names/labels via
// viewNameCollisionRefusal, then unconditionally calls
// overwriteControlPlaneFile at viewPath. So a write_view call against a
// derived view's own name lands silently today, exactly the FD-6/FR-VA-008e
// violation this test proves.
//
// Test 68 stays BLOCKED — it needs the pipeline-owned membership record
// (D-PROVENANCE) across three surfaces, which still does not exist anywhere
// (verified: zero hits for "ViewMembership"/"membership record" outside
// comments).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestWriteView_RefusesDerivedView$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeTools_StateDerivedViewSourceOnAllThreeSurfaces$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteView_RefusesDerivedView is TDD Plan test 48 (FD-6, R2-MAJ-003,
// FR-VA-008e, Dataset F-9): write_view on a view whose CURRENT ViewSet entry
// carries `derived_from` must refuse the write and name the managing .base
// file, never silently drop the marker and land the edit.
//
// REAL, EXECUTABLE: a view file is planted directly on disk (simulating one
// the import/re-derivation pipeline produced) carrying `derived_from:
// Projects.base`. write_view is then called against that SAME view name with
// an ordinary, otherwise-legal edit. Today's execWriteView never reads the
// existing file before overwriting it, so the call succeeds and the
// `derived_from` marker is lost — both facts asserted below.
func TestWriteView_RefusesDerivedView(t *testing.T) {
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

	viewsDir := filepath.Join(root, ".omnipus-vault", "views")
	require.NoError(t, os.MkdirAll(viewsDir, 0o755))
	viewPath := filepath.Join(viewsDir, "derived-dash.yaml")
	original := "name: derived-dash\ntype: widget\nsource: Projects.base\nderived_from: Projects.base\n" +
		"filter:\n  property: status\n  op: \"=\"\n  value: draft\n"
	require.NoError(t, os.WriteFile(viewPath, []byte(original), 0o600))

	write := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "derived-dash",
		"definition": map[string]any{
			"type":   "widget",
			"filter": map[string]any{"property": "status", "op": "=", "value": "shipped"},
		},
	})
	if !write.IsError {
		t.Fatalf(
			"FD-6/R2-MAJ-003/FR-VA-008e/Dataset F-9: write_view on \"derived-dash\", whose on-disk file "+
				"carries derived_from: Projects.base, must be REFUSED naming the managing .base — got "+
				"success instead: %s",
			write.ForLLM,
		)
	}

	after, rerr := os.ReadFile(viewPath)
	require.NoError(t, rerr)
	if string(after) != original {
		t.Fatalf(
			"FD-6/FR-VA-008e: the derived view's on-disk bytes changed even though the write should have "+
				"been refused entirely.\nwant (unchanged): %q\ngot: %q",
			original, string(after),
		)
	}
}

// TestKnowledgeTools_StateDerivedViewSourceOnAllThreeSurfaces is TDD Plan
// test 68 (R2-MIN-007, US-2 AS-6): knowledge_describe, knowledge_find and
// knowledge_configure must each state, in their OWN output, that a view is
// derived and name its source .base file for the same fixture — the
// traceability matrix names this obligation but the original spec draft
// left it as a test nobody wrote (R2-MIN-007's own finding).
//
// BLOCKED: same root cause — with no `derived_from` field or membership
// record, none of renderViews (knowledge_describe), ViewFindLoader
// (knowledge_find) or knowledge_configure's write-result rendering has
// anything to report a "derived" status FROM.
func TestKnowledgeTools_StateDerivedViewSourceOnAllThreeSurfaces(t *testing.T) {
	t.Fatal("BLOCKED: ViewDef.derived_from and the pipeline-owned membership record (D-PROVENANCE) " +
		"are not implemented — required before knowledge_describe, knowledge_find and " +
		"knowledge_configure can each be shown to state a derived view's status and source .base " +
		"in their own output, per US-2 AS-6 / R2-MIN-007 / TDD test 68.")
}
