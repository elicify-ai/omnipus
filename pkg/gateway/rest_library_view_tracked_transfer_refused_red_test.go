// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-031, TDD Plan
// rows 74, 75, 76, 79, 80 (va-qa3 dispatch, P2).
//
// Oracle: FR-VA-031 (founder-direction amendment 2026-09-29, architect ruling
// Q-A = C, team-lead clarification 2026-09-29). A Library transfer OUT of a
// tracked view's or a tracked-view-holding `.base`'s source collection — to
// another knowledge base OR to ordinary (non-knowledge-base) workspace
// storage — MUST be refused with the ONE typed 409 body
// `LibraryMoveConflictError`, code `view_tracked_transfer_refused`, naming
// the tracked paths on `tracked_paths`. A folder transfer containing either
// is refused the same way. COPIES OUT stay allowed: the copy has
// `derived_from` stripped (D-DUPLICATE) and carries no authority; the
// source is untouched.
//
// Verified by reading (2026-09-29): handleLibraryTransfer
// (pkg/gateway/rest_library_write.go) has NO knowledge-base-boundary check
// and NO view/`.base` provenance check at all — it only calls checkCreateName
// (destination-name collision) and, for a same-workspace MOVE, the
// same-collection note-rename branch (libraryManagedEntryInCollection +
// sameCollectionDestination); a cross-collection destination always falls
// through to the plain library.MoveInto/CopyInto path. mapLibraryErr
// (pkg/gateway/rest_library.go) never emits a `LibraryMoveConflictError`
// body for any case — its switch only knows library.Err* sentinels, none of
// which represents "tracked view leaving its collection". There is also no
// pipeline-owned membership record anywhere (confirmed: zero non-test,
// non-generated hits for "ViewMembership"/"view_membership" outside comments
// — same finding va-qa2 recorded for tests 50/51). Each test below plants a
// view carrying `derived_from` (the only provenance marker that exists
// today) as the closest real proxy for "tracked", calls the REAL HTTP
// handler, and observes the real (wrong) outcome.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryTransfer_RefusesCrossKBMoveOfTrackedView$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryTransfer_RefusesCrossKBMoveOfBaseWithTrackedViews$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryTransfer_RefusesCrossKBFolderMoveContainingTrackedView$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryTransfer_RefusesMoveOfTrackedViewToPlainWorkspaceStorage$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryTransfer_CopyOutOfTrackedViewStripsMarkerAndLeavesSourceUntouched$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeRestructureMove_HasNoCrossCollectionDestinationCapability$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// twoKBWorkspace seeds one workspace holding two independent knowledge
// bases (vault-a, vault-b) so a same-workspace transfer between them is a
// genuine cross-collection transfer — the founder's "another knowledge base"
// case — reachable without the cross-workspace path FR-VA-031's own
// LibraryTransferRequest schema documents as user-facing only (never an
// agent tool surface).
func twoKBWorkspace(t *testing.T) (api *restAPI, ws, vaultA, vaultB string) {
	t.Helper()
	api, ws = buildLibraryTestAPI(t)
	root := workDir(api, ws)
	vaultA = filepath.Join(root, "vault-a")
	vaultB = filepath.Join(root, "vault-b")
	require.NoError(t, os.MkdirAll(vaultA, 0o755))
	require.NoError(t, os.MkdirAll(vaultB, 0o755))
	makeKnowledgeBase(t, vaultA, "Vault A")
	makeKnowledgeBase(t, vaultB, "Vault B")
	return api, ws, vaultA, vaultB
}

// plantTrackedView writes a `.base` file declaring one view and the `.view`
// file itself, `derived_from`-marked to that `.base` — the provenance marker
// FR-VA-031's "tracked" concept is defined over (a view in its `.base`'s
// membership record; `derived_from` is the closest real proxy, per this
// file's header).
func plantTrackedView(t *testing.T, vault, baseRel, viewRel string) {
	t.Helper()
	basePath := filepath.Join(vault, filepath.FromSlash(baseRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o755))
	require.NoError(t, os.WriteFile(basePath,
		[]byte("filters:\n  and:\n    - type == \"widget\"\nviews:\n  - type: table\n    name: Open\n"), 0o644))
	viewPath := filepath.Join(vault, filepath.FromSlash(viewRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(viewPath), 0o755))
	require.NoError(t, os.WriteFile(viewPath,
		[]byte("name: open\nlabel: Open\nkind: table\nsource: "+baseRel+"\nderived_from: "+baseRel+"\n"), 0o644))
}

// transferBody builds the JSON body handleLibraryTransfer's
// gen.LibraryTransferRequest decodes, for a same-workspace transfer.
func transferBody(ws, fromPath, toPath string) string {
	return `{"from_workspace_id":"` + ws + `","from_path":"` + fromPath +
		`","to_workspace_id":"` + ws + `","to_path":"` + toPath + `"}`
}

// TestLibraryTransfer_RefusesCrossKBMoveOfTrackedView is TDD Plan row 74
// (FR-VA-031): moving a tracked `.view` file to a DIFFERENT knowledge base
// must be refused — 409 LibraryMoveConflictError, code
// view_tracked_transfer_refused, tracked_paths naming the view.
func TestLibraryTransfer_RefusesCrossKBMoveOfTrackedView(t *testing.T) {
	api, ws, vaultA, _ := twoKBWorkspace(t)
	plantTrackedView(t, vaultA, "Projects.base", "Open.view")

	w := libPostJSON(t, api, "/api/v1/library/move", transferBody(ws, "vault-a/Open.view", "vault-b/Open.view"))

	if w.Code != http.StatusConflict {
		t.Fatalf("FR-VA-031/row 74: moving a tracked .view (derived_from: Projects.base) out of its "+
			"collection into a different knowledge base must be refused with 409 "+
			"view_tracked_transfer_refused. Got HTTP %d: %s\n"+
			"handleLibraryTransfer has no knowledge-base-boundary or provenance check at all — the move "+
			"silently succeeded — and there is no pipeline-owned membership record to consult for "+
			"'tracked' in the first place (per this file's header). Those are the missing seams.",
			w.Code, w.Body.String())
	}
	require.Contains(t, w.Body.String(), "view_tracked_transfer_refused")
	require.Contains(t, w.Body.String(), "vault-a/Open.view")
}

// TestLibraryTransfer_RefusesCrossKBMoveOfBaseWithTrackedViews is TDD Plan
// row 75 (FR-VA-031): moving a `.base` file that HAS tracked views to a
// different knowledge base must be refused the same way, naming the tracked
// view paths.
func TestLibraryTransfer_RefusesCrossKBMoveOfBaseWithTrackedViews(t *testing.T) {
	api, ws, vaultA, _ := twoKBWorkspace(t)
	plantTrackedView(t, vaultA, "Projects.base", "Open.view")

	w := libPostJSON(t, api, "/api/v1/library/move", transferBody(ws, "vault-a/Projects.base", "vault-b/Projects.base"))

	if w.Code != http.StatusConflict {
		t.Fatalf("FR-VA-031/row 75: moving a .base file that has tracked views (Open.view, "+
			"derived_from: Projects.base) out of its collection must be refused with 409 "+
			"view_tracked_transfer_refused, naming the tracked view path(s). Got HTTP %d: %s\n"+
			"handleLibraryTransfer has no check for '.base has tracked views' at all — the move "+
			"silently succeeded, leaving Open.view behind with a derived_from that now names nothing "+
			"in Vault A. Missing seam: the pipeline-owned membership record FR-VA-031 keys 'tracked' "+
			"off does not exist anywhere in the codebase.",
			w.Code, w.Body.String())
	}
	require.Contains(t, w.Body.String(), "view_tracked_transfer_refused")
	require.Contains(t, w.Body.String(), "vault-a/Open.view")
}

// TestLibraryTransfer_RefusesCrossKBFolderMoveContainingTrackedView is TDD
// Plan row 76 (FR-VA-031): a FOLDER transfer to a different knowledge base,
// whose subtree contains a tracked `.view`, must be refused the same way.
func TestLibraryTransfer_RefusesCrossKBFolderMoveContainingTrackedView(t *testing.T) {
	api, ws, vaultA, _ := twoKBWorkspace(t)
	plantTrackedView(t, vaultA, "Projects.base", "Sub/Open.view")

	w := libPostJSON(t, api, "/api/v1/library/move", transferBody(ws, "vault-a/Sub", "vault-b/Sub"))

	if w.Code != http.StatusConflict {
		t.Fatalf("FR-VA-031/row 76: moving a FOLDER whose subtree contains a tracked .view "+
			"(Sub/Open.view, derived_from: Projects.base) to a different knowledge base must be "+
			"refused with 409 view_tracked_transfer_refused, naming the tracked path. Got HTTP %d: "+
			"%s\nhandleLibraryTransfer treats a folder move identically to a file move (plain "+
			"library.MoveInto) — it never walks the subtree for tracked views, because there is no "+
			"membership record to check against for any path, folder or file.",
			w.Code, w.Body.String())
	}
	require.Contains(t, w.Body.String(), "view_tracked_transfer_refused")
	require.Contains(t, w.Body.String(), "vault-a/Sub/Open.view")
}

// TestLibraryTransfer_RefusesMoveOfTrackedViewToPlainWorkspaceStorage is TDD
// Plan row 79 (FR-VA-031, team-lead clarification 2026-09-29): the refusal
// also applies to a transfer into ORDINARY (non-knowledge-base) workspace
// storage, not only into another knowledge base.
func TestLibraryTransfer_RefusesMoveOfTrackedViewToPlainWorkspaceStorage(t *testing.T) {
	api, ws, vaultA, _ := twoKBWorkspace(t)
	plantTrackedView(t, vaultA, "Projects.base", "Open.view")
	require.NoError(t, os.MkdirAll(filepath.Join(workDir(api, ws), "scratch"), 0o755))

	w := libPostJSON(t, api, "/api/v1/library/move", transferBody(ws, "vault-a/Open.view", "scratch/Open.view"))

	if w.Code != http.StatusConflict {
		t.Fatalf("FR-VA-031/row 79 (team-lead clarification): moving a tracked .view into plain, "+
			"non-knowledge-base workspace storage ('scratch/') must be refused with 409 "+
			"view_tracked_transfer_refused, exactly as a transfer into another knowledge base is. "+
			"Got HTTP %d: %s\nhandleLibraryTransfer has no concept of 'destination is not a knowledge "+
			"base' either — the same missing membership-record check this whole FR needs.",
			w.Code, w.Body.String())
	}
	require.Contains(t, w.Body.String(), "view_tracked_transfer_refused")
}

// TestLibraryTransfer_CopyOutOfTrackedViewStripsMarkerAndLeavesSourceUntouched
// is TDD Plan row 80 (FR-VA-031 clarification): a COPY out of the collection
// stays allowed, but the copy must have `derived_from` stripped (D-DUPLICATE)
// — it carries no authority — and the source must be untouched.
func TestLibraryTransfer_CopyOutOfTrackedViewStripsMarkerAndLeavesSourceUntouched(t *testing.T) {
	api, ws, vaultA, _ := twoKBWorkspace(t)
	plantTrackedView(t, vaultA, "Projects.base", "Open.view")

	w := libPostJSON(t, api, "/api/v1/library/copy", transferBody(ws, "vault-a/Open.view", "vault-b/Open.view"))
	require.Equal(t, http.StatusCreated, w.Code, "a copy out of the collection must stay ALLOWED per "+
		"FR-VA-031's clarification — refusing it would be a regression, not a fix: %s", w.Body.String())

	srcBody, srcErr := os.ReadFile(filepath.Join(vaultA, "Open.view"))
	require.NoError(t, srcErr, "the source view must be untouched by a copy-out")
	require.Contains(t, string(srcBody), "derived_from: Projects.base",
		"the SOURCE view's derived_from marker must survive a copy-out unchanged")

	dstBody, dstErr := os.ReadFile(filepath.Join(workDir(api, ws), "vault-b", "Open.view"))
	require.NoError(t, dstErr, "the copy must land at the destination path")
	if strings.Contains(string(dstBody), "derived_from:") {
		t.Fatalf("FR-VA-031 clarification/row 80: a copy of a tracked view taken OUT of its collection "+
			"must have derived_from stripped — it carries no authority. The copy at vault-b/Open.view "+
			"still reads:\n%s\nhandleLibraryTransfer's copy mode (library.CopyInto) has no per-file "+
			"provenance-stripping step at all (confirmed: zero hits for "+
			"'RewriteCopiedViewIdentity'/'StripDerivedFrom' anywhere in pkg/) — the shared pkg/records "+
			"rewrite function D-DUPLICATE calls for is the missing seam.",
			dstBody)
	}
}
