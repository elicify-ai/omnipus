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
// After merging GREEN-security, each fixture below enrolls the view in the
// real outside-vault record. The response oracle is the spec, not the prior
// implementation. Execution remains UNVERIFIED until the discovery seam
// compiles; a package build error is not a behavioral RED receipt.
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

	"github.com/elicify-ai/omnipus/pkg/knowledge"
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

// plantTrackedView creates both files AND the private pipeline-owned membership
// entry. A marker alone is not authority (FR-VA-031, FR-VA-033).
func plantTrackedView(t *testing.T, home, vault, baseRel, viewRel string) {
	t.Helper()
	basePath := filepath.Join(vault, filepath.FromSlash(baseRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o755))
	require.NoError(t, os.WriteFile(basePath,
		[]byte("filters:\n  and:\n    - type == \"widget\"\nviews:\n  - type: table\n    name: Open\n"), 0o644))
	viewPath := filepath.Join(vault, filepath.FromSlash(viewRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(viewPath), 0o755))
	require.NoError(t, os.WriteFile(viewPath,
		[]byte("name: open\nlabel: Open\nkind: table\nsource: "+baseRel+"\nderived_from: "+baseRel+"\n"), 0o644))
	require.NoError(t, knowledge.WithViewMembership(home, vault, func(m *knowledge.ViewMembership) error {
		if m.Bases[baseRel] == nil {
			m.Bases[baseRel] = make(map[string]string)
		}
		m.Bases[baseRel]["open"] = viewRel
		return knowledge.SaveViewMembership(m)
	}))
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
	plantTrackedView(t, api.homePath, vaultA, "Projects.base", "Open.view")
	before := loadRecordedViews(t, api.homePath, vaultA)

	w := libPostJSON(t, api, "/api/v1/library/move", transferBody(ws, "vault-a/Open.view", "vault-b/Open.view"))

	requireTrackedConflict(t, w, "vault-a/Open.view")
	require.Equal(t, before, loadRecordedViews(t, api.homePath, vaultA), "refusing a transfer leaves authority unchanged")
	require.FileExists(t, filepath.Join(vaultA, "Open.view"))
	require.NoFileExists(t, filepath.Join(workDir(api, ws), "vault-b", "Open.view"))
}

// TestLibraryTransfer_RefusesCrossKBMoveOfBaseWithTrackedViews is TDD Plan
// row 75 (FR-VA-031): moving a `.base` file that HAS tracked views to a
// different knowledge base must be refused the same way, naming the tracked
// view paths.
func TestLibraryTransfer_RefusesCrossKBMoveOfBaseWithTrackedViews(t *testing.T) {
	api, ws, vaultA, _ := twoKBWorkspace(t)
	plantTrackedView(t, api.homePath, vaultA, "Projects.base", "Open.view")
	before := loadRecordedViews(t, api.homePath, vaultA)

	w := libPostJSON(t, api, "/api/v1/library/move", transferBody(ws, "vault-a/Projects.base", "vault-b/Projects.base"))

	requireTrackedConflict(t, w, "vault-a/Open.view")
	require.Equal(t, before, loadRecordedViews(t, api.homePath, vaultA), "refusing a transfer leaves authority unchanged")
	require.FileExists(t, filepath.Join(vaultA, "Projects.base"))
	require.NoFileExists(t, filepath.Join(workDir(api, ws), "vault-b", "Projects.base"))
}

// TestLibraryTransfer_RefusesCrossKBFolderMoveContainingTrackedView is TDD
// Plan row 76 (FR-VA-031): a FOLDER transfer to a different knowledge base,
// whose subtree contains a tracked `.view`, must be refused the same way.
func TestLibraryTransfer_RefusesCrossKBFolderMoveContainingTrackedView(t *testing.T) {
	api, ws, vaultA, _ := twoKBWorkspace(t)
	plantTrackedView(t, api.homePath, vaultA, "Projects.base", "Sub/Open.view")
	before := loadRecordedViews(t, api.homePath, vaultA)

	w := libPostJSON(t, api, "/api/v1/library/move", transferBody(ws, "vault-a/Sub", "vault-b/Sub"))

	requireTrackedConflict(t, w, "vault-a/Sub/Open.view")
	require.Equal(t, before, loadRecordedViews(t, api.homePath, vaultA), "refusing a transfer leaves authority unchanged")
	require.FileExists(t, filepath.Join(vaultA, "Sub", "Open.view"))
	require.NoDirExists(t, filepath.Join(workDir(api, ws), "vault-b", "Sub"))
}

// TestLibraryTransfer_RefusesMoveOfTrackedViewToPlainWorkspaceStorage is TDD
// Plan row 79 (FR-VA-031, team-lead clarification 2026-09-29): the refusal
// also applies to a transfer into ORDINARY (non-knowledge-base) workspace
// storage, not only into another knowledge base.
func TestLibraryTransfer_RefusesMoveOfTrackedViewToPlainWorkspaceStorage(t *testing.T) {
	api, ws, vaultA, _ := twoKBWorkspace(t)
	plantTrackedView(t, api.homePath, vaultA, "Projects.base", "Open.view")
	require.NoError(t, os.MkdirAll(filepath.Join(workDir(api, ws), "scratch"), 0o755))
	before := loadRecordedViews(t, api.homePath, vaultA)

	w := libPostJSON(t, api, "/api/v1/library/move", transferBody(ws, "vault-a/Open.view", "scratch/Open.view"))

	requireTrackedConflict(t, w, "vault-a/Open.view")
	require.Equal(t, before, loadRecordedViews(t, api.homePath, vaultA), "refusing a transfer leaves authority unchanged")
	require.FileExists(t, filepath.Join(vaultA, "Open.view"))
	require.NoFileExists(t, filepath.Join(workDir(api, ws), "scratch", "Open.view"))
}

// TestLibraryTransfer_CopyOutOfTrackedViewStripsMarkerAndLeavesSourceUntouched
// is TDD Plan row 80 (FR-VA-031 clarification): a COPY out of the collection
// stays allowed, but the copy must have `derived_from` stripped (D-DUPLICATE)
// — it carries no authority — and the source must be untouched.
func TestLibraryTransfer_CopyOutOfTrackedViewStripsMarkerAndLeavesSourceUntouched(t *testing.T) {
	api, ws, vaultA, _ := twoKBWorkspace(t)
	plantTrackedView(t, api.homePath, vaultA, "Projects.base", "Open.view")

	w := libPostJSON(t, api, "/api/v1/library/copy", transferBody(ws, "vault-a/Open.view", "vault-b/Open.view"))
	require.Equal(t, http.StatusCreated, w.Code, "a copy out of the collection must stay ALLOWED per "+
		"FR-VA-031's clarification — refusing it would be a regression, not a fix: %s", w.Body.String())

	srcBody, srcErr := os.ReadFile(filepath.Join(vaultA, "Open.view"))
	require.NoError(t, srcErr, "the source view must be untouched by a copy-out")
	require.Contains(t, string(srcBody), "derived_from: Projects.base",
		"the SOURCE view's derived_from marker must survive a copy-out unchanged")

	dstBody, dstErr := os.ReadFile(filepath.Join(workDir(api, ws), "vault-b", "Open.view"))
	require.NoError(t, dstErr, "the copy must land at the destination path")
	require.False(t, strings.Contains(string(dstBody), "derived_from:"),
		"FR-VA-031/row 80: copy must have its derived_from stripped and carry no authority: %s", dstBody)
	require.Equal(t, "Open.view", loadRecordedViews(t, api.homePath, vaultA).Bases["Projects.base"]["open"])
	require.Empty(t, loadRecordedViews(t, api.homePath, filepath.Join(workDir(api, ws), "vault-b")).Bases,
		"the destination copy must never gain source management authority")
}
