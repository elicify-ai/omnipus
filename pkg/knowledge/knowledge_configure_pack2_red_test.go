// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 4, 7, 8, 9, 26 and 31.
//
// VERDICT per test: 4 real-fail, 7 CHARACTERIZATION (passes today — see its
// own doc comment), 8 real-fail, 9 CHARACTERIZATION (passes today), 26
// real-fail (strengthened after an initial weak assertion accidentally
// passed — see its own doc comment), 31 real-fail.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestCreateView_DefaultsToCollectionRoot$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestWriteView_RefusesSymlinkEscape$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestWriteView_RefusesTraversalFilename$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestCreateView_SucceedsAndIsImmediatelyDiscoverable$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestWriteView_RefusesCallerSuppliedDerivedFrom$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestDeleteView_GoesToTrash$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestCreateView_DefaultsToCollectionRoot is TDD Plan test 4 (US-1 AS-4,
// Q5/B): a no-destination create_view call, with no existing occupant, must
// land the file at the COLLECTION ROOT — not the legacy hidden
// .omnipus-vault/views directory.
//
// Today's execCreateView hard-codes filepath.Join(records.ViewsDir(root),
// viewName+controlPlaneFileExt) as the destination, unconditionally.
func TestCreateView_DefaultsToCollectionRoot(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "quarterly", "kind": "table",
	})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)

	wantPath := filepath.Join(root, "quarterly.view")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf(
			"US-1 AS-4/Q5/B: create_view with no destination must land at the collection root (%s), "+
				"stat error: %v — got %s",
			wantPath, err, res.ForLLM,
		)
	}
}

// TestWriteView_RefusesSymlinkEscape is TDD Plan test 7 (US-3 AS-1, EC-1):
// a write destination resolving outside the collection root through a
// symlink must be refused, with nothing written.
//
// CHARACTERIZATION: this already passes today. overwriteControlPlaneFile
// calls resolveControlWritePath, which runs CollectionRoot.
// ResolveContainedNoSymlink before any write — so a symlinked
// .omnipus-vault/views directory pointing outside the real collection root
// is already refused by the EXISTING containment machinery, even though
// F11 characterizes the writer as not currently exercising it (F11 is about
// there being no CALLER-SUPPLIED destination to attack, not about the
// check's absence). Pinned so CHECK's mutation pass can verify the
// assertion watches something real.
func TestWriteView_RefusesSymlinkEscape(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".omnipus-vault"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, ".omnipus-vault", "views")))

	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "escape", "kind": "table",
	})
	assert.True(t, res.IsError, "US-3 AS-1/EC-1: a write through a symlinked views directory pointing "+
		"outside the collection root must be refused; got success: %s", res.ForLLM)
	assert.NoFileExists(t, filepath.Join(outside, "escape.yaml"),
		"nothing may be written through the symlink escape")
}

// TestWriteView_RefusesTraversalFilename is TDD Plan test 8 (US-3 AS-2): a
// proposed view filename containing a path-traversal segment or an
// OS-reserved/illegal component must be refused by pathsafe-equivalent
// validation before any file is touched.
//
// The `..`-bearing half of this is already covered by the EXISTING
// TestKnowledgeConfigure_WriteView_NameEscapingTheViewsDir_Refused. This
// test targets the specific gap that test does NOT cover: an OS-RESERVED
// device name (FR-007 names this explicitly, Dataset D7's "CON" example).
// controlPlaneNameRefusal (read in full) has no such check at all —
// confirmed: it only refuses blank/NUL/separators/absolute/"."/"..",
// control chars, quote-ish characters, and a leading dot. "CON" passes
// every one of those checks.
func TestWriteView_RefusesTraversalFilename(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "CON", "kind": "table",
	})
	if !res.IsError {
		t.Fatalf(
			"US-3 AS-2/Dataset D7/FR-007: create_view name=\"CON\" (an OS-reserved device name) "+
				"succeeded — pathsafe-equivalent validation must refuse a reserved/illegal component "+
				"before any write. Got: %s",
			res.ForLLM,
		)
	}
	assert.NoFileExists(t, filepath.Join(root, "CON.view"))
	assert.NoFileExists(t, filepath.Join(records.ViewsDir(root), "CON.yaml"))
}

// TestCreateView_SucceedsAndIsImmediatelyDiscoverable is TDD Plan test 9
// (US-3 AS-3): a legitimate write must succeed and be found by the very
// next discovery call.
//
// CHARACTERIZATION: this already passes today, for the legacy
// ViewsDir-based discovery — create_view writes to ViewsDir(root) and
// knowledge_describe's renderViews reads via the same records.LoadViews
// over that same directory, so a write is self-consistently discoverable
// today. This does NOT characterize discovery-ANYWHERE (TDD tests 1-3
// cover that gap separately) — only that write-then-discover round-trips
// within today's one supported location.
func TestCreateView_SucceedsAndIsImmediatelyDiscoverable(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "freshly-written", "kind": "table",
	})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)

	set, report, err := records.LoadViews(root, nil)
	require.NoError(t, err)
	require.Empty(t, report.Rejections)
	_, ok := set.Get("freshly-written")
	assert.True(t, ok, "US-3 AS-3: a legitimate write must be found by the very next discovery call; "+
		"loaded names: %v", set.Names())
}

// TestWriteView_RefusesCallerSuppliedDerivedFrom is TDD Plan test 26
// (D-PROVENANCE, Non-Behaviors): write_view/create_view must refuse a call
// that supplies `derived_from` — provenance is written exclusively by the
// import/re-derivation pipeline.
//
// Today's `definition` map is JSON-round-tripped into generated.ViewDef via
// records.ParseView's DisallowUnknownFields (F1, unchanged), so a caller
// passing `derived_from` inside `definition` IS already refused today —
// but for the WRONG reason. Verified directly (a throwaway probe run
// against this exact call): the message is exactly
//
//	`the view file declares a field this release does not know: "derived_from"`
//
// — the SAME generic wording ANY unrecognized key in `definition` would
// produce (a typo, a retired key, anything). FR-VA-009a's own wording
// requires a DEDICATED refusal explaining that derived_from is "settable
// only by the import/re-derivation pipeline" — this assertion checks for
// that specific explanation, which the generic message does not carry, so
// a bare "was it refused at all" check (which passes today, for the wrong
// reason) is not used here.
func TestWriteView_RefusesCallerSuppliedDerivedFrom(t *testing.T) {
	home, ws, _ := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "sneaky",
		"definition": map[string]any{
			"derived_from": "someone/else.base",
		},
	})
	require.True(t, res.IsError, "a caller-supplied derived_from must be refused")
	assert.Contains(t, res.ForLLM, "derived_from")
	if !(containsAny(res.ForLLM, "re-derivation", "import pipeline", "pipeline-owned", "settable only by")) {
		t.Fatalf(
			"D-PROVENANCE/FR-VA-009a: the refusal must explain that derived_from is set only by the "+
				"import/re-derivation pipeline — today's message is the SAME generic "+
				"\"declares a field this release does not know\" wording every unrecognized key gets, "+
				"never mentioning provenance at all. Got: %s",
			res.ForLLM,
		)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// TestDeleteView_GoesToTrash is TDD Plan test 31 (US-7 AS-1, MAJ-004/
// D-DELETE): delete_view must trash the file via the same Trasher every
// other agent-facing Library deletion uses — not os.Remove — so it is
// restorable.
//
// F5a's own finding: execDeleteView calls removeControlPlaneFile, a bare
// os.Remove with no trash/restore path at all.
func TestDeleteView_GoesToTrash(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)

	require.False(t, tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "disposable", "kind": "table",
	}).IsError)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "delete_view", "view": "disposable",
	})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)

	assert.NoFileExists(t, filepath.Join(root, "disposable.view"))

	trashDir := filepath.Join(root, ".trash")
	entries, _ := os.ReadDir(trashDir)
	if len(entries) == 0 {
		t.Fatalf(
			"US-7 AS-1/MAJ-004/D-DELETE: delete_view must trash the file via (*Trasher).Trash so it "+
				"is restorable — found no entry under %s (a hard os.Remove leaves nothing to restore)",
			trashDir,
		)
	}
}
