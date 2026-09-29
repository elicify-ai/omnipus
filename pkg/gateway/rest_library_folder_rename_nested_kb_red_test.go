// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-034, TDD Plan
// row 83 (va-qa3 dispatch, P4, REST half).
//
// Oracle: FR-VA-034 (team-lead ruling 2026-09-29): "A same-collection FOLDER
// rename/move whose subtree contains a nested knowledge base with tracked
// views MUST be REFUSED (same LibraryMoveConflictError
// view_tracked_transfer_refused, naming the tracked paths)."
//
// The fixture enrolls a view in the nested KB's outside-vault membership
// record, not just a derived_from marker. The response oracle is FR-VA-034;
// execution remains unverified until the discovery branch compiles.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryRename_RefusesSameCollectionFolderMoveContainingNestedKBWithTrackedViews$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
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
	plantTrackedView(t, api.homePath, nested, "Projects.base", "Open.view")
	before := loadRecordedViews(t, api.homePath, nested)

	w := libPostJSON(t, api, "/api/v1/library/"+ws+"/rename",
		`{"from":"vault-a/Outer","to":"vault-a/Outer2"}`)

	requireTrackedConflict(t, w, "vault-a/Outer/NestedKB/Open.view")
	require.Equal(t, before, loadRecordedViews(t, api.homePath, nested), "the nested KB remains enrolled after refusal")
	require.DirExists(t, nested)
	require.FileExists(t, filepath.Join(nested, "Open.view"))
	require.NoDirExists(t, filepath.Join(vaultA, "Outer2"))
	require.Equal(t, "Open.view", loadRecordedViews(t, api.homePath, nested).Bases["Projects.base"]["open"])
}
