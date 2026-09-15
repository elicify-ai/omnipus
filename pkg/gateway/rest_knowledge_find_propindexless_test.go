//go:build records_no_sqlite || mipsle || netbsd || (freebsd && arm)

// Omnipus — ADR-081 / spec MV-9's platform carve-out, executed: on a build
// without the properties index, POST .../knowledge/find's Attachments group
// (and any other properties-index-only kind) must refuse HONESTLY — an empty
// array WITH complete:false and the engine's own refusal reason — never a
// silently bare empty group. Spec test 28's build-tag variant.
//
// Run this half on any host with the forcing tag:
//
//	go test -tags goolm,stdjson,records_no_sqlite -run TestVaultSearch_Propindexless -p 1 ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestVaultSearch_PropindexlessAttachmentCarveOut is the MV-9 platform
// carve-out, executed against the real handler.
func TestVaultSearch_PropindexlessAttachmentCarveOut(t *testing.T) {
	require.False(t, records.PropertyIndexAvailable,
		"this file compiles only under the SQLite-less build constraints — if this fails, "+
			"the //go:build line here and propindex_stub_unavailable.go's have drifted apart")

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	// A plain note, so the notes group can still answer honestly (kind=note is
	// textOnlyServable — the platform's own promise, propindex_stub.go:
	// "plain-word search (bleve) and knowledge_read still work"). The
	// attachment is deliberately NOT synced to a properties index: on this
	// build vaultprops.Sync is itself a refusal (records.RequirePropertyIndex),
	// so there is no properties index to sync it to.
	writeNote(t, vault, "security.md", "# Security\n\nLandlock and seccomp.\n")
	writeNote(t, vault, "img/diagram-v3.png", "binary-ish bytes\n")
	indexKnowledgeBase(t, api.homePath, vault)

	colID := collectionIDOf(t, api, ws, "vault")

	w := vaultFindPost(t, api, ws, map[string]any{"query": "diagram-v3", "collection_id": colID})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	require.NotNil(t, resp.Attachments, "the handler always sends the attachments array, even when refused")
	assert.Empty(t, *resp.Attachments,
		"never a silently bare empty group — it must also carry complete:false and a reason")
	assert.False(t, resp.Complete, "an attachment query this platform cannot answer is not a complete answer")
	require.NotNil(t, resp.CompleteReason, "the engine's own refusal reason must be surfaced")
	assert.Contains(t, *resp.CompleteReason, "properties index",
		"the reason names WHY — the properties index this kind needs is not open")
}

// TestVaultSearch_PropindexlessNoteSearchStillWorks proves the carve-out is
// SCOPED to attachments (and any other properties-index-only kind): plain
// note search must keep working exactly as the platform posture promises.
func TestVaultSearch_PropindexlessNoteSearchStillWorks(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "security.md", "# Security\n\nLandlock and seccomp.\n")
	indexKnowledgeBase(t, api.homePath, vault)
	colID := collectionIDOf(t, api, ws, "vault")

	w := vaultFindPost(t, api, ws, map[string]any{"query": "seccomp", "collection_id": colID})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	require.Len(t, resp.Notes, 1, "plain-word note search must keep working with no properties index")
	assert.Equal(t, "security.md", resp.Notes[0].Path)
}
