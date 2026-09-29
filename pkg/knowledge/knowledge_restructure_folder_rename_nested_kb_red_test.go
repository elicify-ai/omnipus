// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-034, TDD Plan
// row 83 (va-qa3 dispatch, P4, agent-path half — the REST half is
// rest_library_folder_rename_nested_kb_red_test.go, pkg/gateway).
//
// Oracle: FR-VA-034 (team-lead ruling 2026-09-29): "A same-collection FOLDER
// rename/move whose subtree contains a nested knowledge base with tracked
// views MUST be REFUSED ... via BOTH the Library rename REST path AND the
// agent knowledge_restructure path — both through the shared
// RenameWithViewMembership guard."
//
// Verified by reading (2026-09-29): knowledge_restructure's rename op
// (execRenameMove, pkg/knowledge/knowledge_restructure.go) has no nested-KB
// or view-membership check anywhere — it resolves the source/destination
// paths within the SAME collection and calls the renamer with no subtree
// walk for a nested knowledge base at all. There is no
// RenameWithViewMembership guard anywhere in the codebase (confirmed by
// grep: zero hits for that name in pkg/) — the SAME missing seam the REST
// half's test documents, which this test's assertions independently
// confirm by driving the real Execute() call.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRestructureRename_RefusesSameCollectionFolderMoveContainingNestedKBWithTrackedViews$' ./pkg/knowledge/
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

// TestRestructureRename_RefusesSameCollectionFolderMoveContainingNestedKBWithTrackedViews
// is TDD Plan row 83's agent-path half (FR-VA-034): renaming "Outer" — a
// plain subfolder of the enclosing knowledge base "KB" — must be refused
// because its own subtree contains "Outer/NestedKB", itself a knowledge
// base holding a tracked view.
func TestRestructureRename_RefusesSameCollectionFolderMoveContainingNestedKBWithTrackedViews(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)

	outer := filepath.Join(root, "Outer")
	require.NoError(t, os.MkdirAll(outer, 0o755))
	nested := filepath.Join(outer, "NestedKB")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	a4Vault(t, nested, "Nested KB")

	// plantTrackedView's gateway-package twin: derived_from is the closest
	// real proxy for "tracked" that exists anywhere today (per
	// rest_library_view_tracked_transfer_refused_red_test.go's header).
	require.NoError(t, os.WriteFile(filepath.Join(nested, "Projects.base"),
		[]byte("filters:\n  and:\n    - type == \"widget\"\nviews:\n  - type: table\n    name: Open\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "Open.view"),
		[]byte("name: open\nlabel: Open\nkind: table\nsource: Projects.base\nderived_from: Projects.base\n"), 0o644))

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "rename", "collection": "KB", "path": "Outer", "new_name": "Outer2", "folder": true,
	})

	if !res.IsError {
		t.Fatalf("FR-VA-034/row 83 (agent path): renaming/moving 'Outer' — a same-collection folder "+
			"whose subtree contains a NESTED knowledge base (Outer/NestedKB) with a tracked view "+
			"(NestedKB/Open.view, derived_from: Projects.base) — must be refused. Got success: %s\n"+
			"execRenameMove has no nested-KB or view-membership check at all — the rename silently "+
			"succeeded, carrying the nested KB and its tracked view along with it. There is no "+
			"RenameWithViewMembership guard anywhere in the codebase (confirmed by grep).",
			res.ForLLM)
	}

	// Nothing moved: the nested KB and its tracked view must still be at
	// their ORIGINAL path if the refusal is real (never a "refused but
	// partially applied" outcome).
	require.DirExists(t, nested, "a genuine refusal must leave the nested KB at its original path")
	require.FileExists(t, filepath.Join(nested, "Open.view"),
		"a genuine refusal must leave the tracked view untouched at its original path")
	require.NoDirExists(t, filepath.Join(root, "Outer2"),
		"a genuine refusal must not have created the destination folder at all")
}
