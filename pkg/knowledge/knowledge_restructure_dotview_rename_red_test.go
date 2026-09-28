// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 32, 62 and 63.
//
// Test 32 was part of qa-lead's original curated scope (US-8 AS-1,
// MAJ-002/D-MOVE — the "from" side) but was omitted from that pack's own
// report by mistake; it is added here alongside 62/63 (the "to" side,
// R2-MAJ-004) because all three exercise the exact same code path
// (execRenameMove/ensureMarkdown) and the same real harness.
//
// Oracle: F18 ("Agents have no Library move/rename door for a non-markdown
// file today: knowledge_restructure's rename/move op runs ensureMarkdown on
// BOTH from and to unless the caller passes folder: true, appending .md to
// anything not already .md/.markdown") and D-MOVE's round-2 rewrite ("to
// does not run its own check at all — it inherits from's decision... An
// extension-less new_name... keeps the SOURCE's extension appended, never
// .md... A new_name/new_folder combination that would CHANGE a .view file's
// extension... is REFUSED").
//
// Verified today by reading pkg/knowledge/authoring_tools.go::ensureMarkdown
// (appends ".md" to any rel not already .md/.markdown, with NO carve-out for
// an existing non-markdown regular file) and
// pkg/knowledge/knowledge_restructure.go::execRenameMove (no exact-path-first
// check on either from or to — that carve-out exists ONLY in
// (*Trasher).trashSourcePath for trash, per F18/D-MOVE). All three tests
// below are real, currently-failing assertions against that code — no new
// contract field is needed for any of them.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeRestructure_MovesDotViewPathUnchanged$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeRestructure_RenameKeepsSourceExtensionOnExtensionlessNewName$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeRestructure_RefusesExtensionChangeOnView$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKnowledgeRestructure_MovesDotViewPathUnchanged is TDD Plan test 32
// (US-8 AS-1, MAJ-002/D-MOVE): renaming a `.view` file through
// knowledge_restructure must target the `.view` path exactly, unchanged by
// ensureMarkdown — never a `.view.md` file.
func TestKnowledgeRestructure_MovesDotViewPathUnchanged(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	require.NoError(t, os.WriteFile(filepath.Join(root, "roadmap.view"), []byte("name: roadmap\n"), 0o644))
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "rename", "collection": "KB", "path": "roadmap.view", "new_name": "roadmap-q3.view",
	})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)

	if _, err := os.Stat(filepath.Join(root, "roadmap-q3.view.md")); err == nil {
		t.Fatalf("US-8 AS-1/MAJ-002/D-MOVE: renaming a .view file produced roadmap-q3.view.md — " +
			"ensureMarkdown must not touch a .view path")
	}
	assert.FileExists(t, filepath.Join(root, "roadmap-q3.view"))
	assert.NoFileExists(t, filepath.Join(root, "roadmap.view"))
}

// TestKnowledgeRestructure_RenameKeepsSourceExtensionOnExtensionlessNewName
// is TDD Plan test 62 (R2-MAJ-004, FR-VA-016a): renaming `roadmap.view` with
// an EXTENSIONLESS `new_name` must keep the SOURCE's extension
// (`roadmap-q3.view`), never `.md` — the destination has no extension of its
// own to consult, so per D-MOVE's round-2 rewrite it takes the source's.
func TestKnowledgeRestructure_RenameKeepsSourceExtensionOnExtensionlessNewName(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	require.NoError(t, os.WriteFile(filepath.Join(root, "roadmap.view"), []byte("name: roadmap\n"), 0o644))
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "rename", "collection": "KB", "path": "roadmap.view", "new_name": "roadmap-q3",
	})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)

	if _, err := os.Stat(filepath.Join(root, "roadmap-q3.md")); err == nil {
		t.Fatalf(
			"R2-MAJ-004/FR-VA-016a: an extensionless new_name (\"roadmap-q3\") for source \"roadmap.view\" "+
				"produced roadmap-q3.md — the destination has no extension of its own, so it must inherit "+
				"the SOURCE's (\"roadmap-q3.view\"), never fall back to .md",
		)
	}
	assert.FileExists(t, filepath.Join(root, "roadmap-q3.view"))
}

// TestKnowledgeRestructure_RefusesExtensionChangeOnView is TDD Plan test 63
// (R2-MAJ-004, FR-VA-016a): a new_name/new_folder combination that would
// CHANGE a `.view` file's extension (e.g. to `.txt`) must be REFUSED, BY A
// DEDICATED CHECK NAMING THE EXTENSION CHANGE — a silent type change on
// rename has no confirmation mechanism, so it is not allowed at all.
//
// The call IS refused today, but for the WRONG reason: ensureMarkdool's
// existing bug (test 32/62's own finding — it mangles a `.view` SOURCE into
// a `.md`-suffixed lookup) makes execRenameMove refuse with "rename source
// not found: roadmap.view.md" BEFORE any extension-change decision is ever
// reached — a source-resolution failure, not the dedicated
// extension-identity refusal FR-VA-016a requires. This assertion is written
// to fail on exactly that accidental-pass shape (a bare `res.IsError`
// check would pass today for the wrong reason and prove nothing).
func TestKnowledgeRestructure_RefusesExtensionChangeOnView(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	require.NoError(t, os.WriteFile(filepath.Join(root, "roadmap.view"), []byte("name: roadmap\n"), 0o644))
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "rename", "collection": "KB", "path": "roadmap.view", "new_name": "roadmap.txt",
	})
	require.True(t, res.IsError, "renaming roadmap.view to roadmap.txt must be refused")

	assert.NotContains(t, res.ForLLM, "not found",
		"R2-MAJ-004/FR-VA-016a: refused for a source-resolution failure (ensureMarkdown's .view bug, "+
			"test 32/62's own finding), not for the dedicated extension-change refusal this row "+
			"requires — full message: %s", res.ForLLM)
	assert.FileExists(t, filepath.Join(root, "roadmap.view"))
	assert.NoFileExists(t, filepath.Join(root, "roadmap.txt"))
}
