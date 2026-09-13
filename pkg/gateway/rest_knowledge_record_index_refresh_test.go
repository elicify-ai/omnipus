// Regression tests for UAT D-67 (2026-09-13): a record written through the
// REST door (POST .../knowledge/records) must be visible in the properties
// index straight away — the same read-your-own-write guarantee the agent door
// (knowledge_edit) already gives — not only after a gateway restart.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// requirePropertiesIndexSynced is the ONE verdict on a vaultprops.Sync
// error in this file (Codex review 2026-09-14, finding 14). It skips ONLY
// for the explicit unsupported-platform sentinel — a build where the
// properties index is not compiled in (mipsle, netbsd, freebsd/arm) — and
// fails on anything else. It used to be an inline `t.Skipf` on ANY error,
// which turned a SQL regression, a corrupt index or a full disk into a green
// "skipped" that never reached the refresh assertion.
func requirePropertiesIndexSynced(t testing.TB, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if errors.Is(err, records.ErrPropertyIndexUnavailable) {
		t.Skipf("properties index unavailable on this platform: %v", err)
		return
	}
	t.Fatalf("vaultprops.Sync failed for a reason that is NOT the platform carve-out; this is a real defect, not a skip: %v", err)
}

// storedPropText reads one property's first stored text element for one path
// straight out of the collection's properties index — the same store every
// view answer is served from, so this is the oracle the UAT lane used
// (view answer vs disk), not a re-read of the note.
func storedPropText(t *testing.T, home, vault, relPath, prop string) (string, bool) {
	t.Helper()
	if err := records.RequirePropertyIndex(records.CapabilityOpenIndex); err != nil {
		t.Skipf("properties index unavailable on this platform: %v", err)
	}
	idxPath, err := knowledge.PropertiesIndexPath(home, vault)
	require.NoError(t, err)
	store, err := propindex.Open(context.Background(), idxPath, propindex.Options{})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	var (
		found bool
		text  string
	)
	err = store.Candidates(context.Background(), propindex.Selector{}, func(c propindex.Candidate) (propindex.Verdict, error) {
		if c.Path != relPath {
			return propindex.Rejected, nil
		}
		found = true
		if p, ok := c.Prop(prop); ok && len(p.Elems) > 0 {
			text = p.Elems[0].Text
		}
		return propindex.Accepted, nil
	})
	require.NoError(t, err)
	return text, found
}

// TestKnowledgeRecordWrite_UpdateRefreshesPropertiesIndex — D-67 exactly as
// observed: the index was built (boot-time sync) with the old value, the cell
// editor writes a new one through REST, and every view keeps rendering the
// old value until restart. After the fix the index row carries the new value
// as soon as the write returns.
func TestKnowledgeRecordWrite_UpdateRefreshesPropertiesIndex(t *testing.T) {
	api, ws, vault := buildRecordTestVault(t)
	_, syncErr := vaultprops.Sync(context.Background(), api.homePath, vault, vaultprops.SyncOptions{})
	requirePropertiesIndexSynced(t, syncErr)
	before, ok := storedPropText(t, api.homePath, vault, "w1.md", "name")
	require.True(t, ok, "fixture must be indexed before the write")
	require.Equal(t, "Sprocket", before)

	body := map[string]any{
		"mode":          "update",
		"type":          "widget",
		"id":            "WD-0001",
		"version_token": widgetVersionToken(t, api, ws),
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "Sprocket Mk II"}}},
		},
	}
	w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	after, ok := storedPropText(t, api.homePath, vault, "w1.md", "name")
	require.True(t, ok)
	require.Equal(t, "Sprocket Mk II", after,
		"D-67: the properties index must carry the value the REST write just landed on disk")
}

// TestKnowledgeRecordWrite_CreateRefreshesPropertiesIndex — the create half:
// a record created through REST must be a row in the index immediately, not
// only after the next full sync.
func TestKnowledgeRecordWrite_CreateRefreshesPropertiesIndex(t *testing.T) {
	api, ws, vault := buildRecordTestVault(t)
	_, syncErr := vaultprops.Sync(context.Background(), api.homePath, vault, vaultprops.SyncOptions{})
	requirePropertiesIndexSynced(t, syncErr)

	body := map[string]any{
		"mode": "create",
		"type": "widget",
		"path": "w2.md",
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "Gear"}}},
		},
	}
	w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	out := decodeJSON[gen.VaultRecord](t, w)

	text, ok := storedPropText(t, api.homePath, vault, out.Path, "name")
	require.True(t, ok, "D-67: a record created through REST must be indexed immediately")
	require.Equal(t, "Gear", text)
}
