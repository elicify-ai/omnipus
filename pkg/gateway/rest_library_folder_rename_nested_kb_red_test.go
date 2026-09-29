// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-034, TDD Plan
// row 83 (va-qa3 dispatch, P4, REST half).
//
// Oracle: FR-VA-034 (team-lead ruling 2026-09-29): "A same-collection FOLDER
// rename/move whose subtree contains a nested knowledge base with tracked
// views MUST be REFUSED (same LibraryMoveConflictError
// view_tracked_transfer_refused, naming the tracked paths)."
//
// Verified by reading (2026-09-29): libraryFolderInCollection
// (pkg/gateway/rest_library_knowledge_cascade.go) only checks whether the
// MOVED FOLDER ITSELF is a knowledge base (detectKnowledgeBaseInRoot(root,
// rel)) — if that folder is a plain subfolder of the enclosing collection
// but contains a NESTED knowledge base one or more levels DEEPER in its own
// subtree, that nested-KB check never fires, and the folder is governed as
// an ordinary managed folder of the OUTER collection, or falls to plain
// filesystem semantics — either way with no subtree walk for a nested KB or
// its tracked views. There is no RenameWithViewMembership guard anywhere
// (confirmed: zero hits for that name in pkg/).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryRename_RefusesSameCollectionFolderMoveContainingNestedKBWithTrackedViews$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLibraryRename_RefusesSameCollectionFolderMoveContainingNestedKBWithTrackedViews
// is TDD Plan row 83 (FR-VA-034): renaming/moving "Outer" — a plain
// subfolder of the enclosing knowledge base "vault-a" — must be refused
// because its own subtree contains "Outer/NestedKB", itself a knowledge
// base holding a tracked view.
func TestLibraryRename_RefusesSameCollectionFolderMoveContainingNestedKBWithTrackedViews(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	root := workDir(api, ws)
	vaultA := filepath.Join(root, "vault-a")
	require.NoError(t, os.MkdirAll(vaultA, 0o755))
	makeKnowledgeBase(t, vaultA, "Vault A")

	outer := filepath.Join(vaultA, "Outer")
	require.NoError(t, os.MkdirAll(outer, 0o755))
	nested := filepath.Join(outer, "NestedKB")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	makeKnowledgeBase(t, nested, "Nested KB")
	plantTrackedView(t, nested, "Projects.base", "Open.view")

	w := libPostJSON(t, api, "/api/v1/library/"+ws+"/rename",
		`{"from":"vault-a/Outer","to":"vault-a/Outer2"}`)

	if w.Code != http.StatusConflict {
		t.Fatalf("FR-VA-034/row 83: renaming/moving 'Outer' — a same-collection folder whose subtree "+
			"contains a NESTED knowledge base (Outer/NestedKB) with a tracked view "+
			"(NestedKB/Open.view, derived_from: Projects.base) — must be refused with 409 "+
			"view_tracked_transfer_refused, naming the tracked path. Got HTTP %d: %s\n"+
			"libraryFolderInCollection only checks whether the MOVED folder itself "+
			"(detectKnowledgeBaseInRoot(root, \"Outer\")) is a knowledge base — it never walks the "+
			"subtree for a NESTED one, so the nested KB and its tracked view were silently carried "+
			"along by the rename. There is no RenameWithViewMembership guard anywhere in the codebase.",
			w.Code, w.Body.String())
	}
	require.Contains(t, w.Body.String(), "view_tracked_transfer_refused")
	require.Contains(t, w.Body.String(), "vault-a/Outer/NestedKB/Open.view")
}
