// Omnipus — regression coverage for the AGENT-facing half of the UAT
// attachment-misclassification finding fixed in
// pkg/records/knowledgefind/find.go's textOnlyServable/textOnlyResponse and
// this package's findTextSearcher.Search.
//
// THE HUMAN-facing symptom (POST .../knowledge/find) and this file's own
// scenario are the SAME defect reached through a DIFFERENT front door: both
// knowledge_find (this file, via FindTool.Execute) and the REST vault-search
// endpoint (pkg/gateway/rest_knowledge_find_test.go) build their
// knowledgefind.Deps through the one shared vaultprops.OpenFindEnv, and both
// therefore ran the identical text-only fallback whenever the properties
// index was nil. Before the fix: a `kind: "attachment"` query answered a
// refusal ("the properties index is not open") even though the text index
// held the attachment; a `kind: "note"` (or default) query for the same
// words returned the attachment mislabelled as a note.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestFindTool_AttachmentKind' ./pkg/vaultprops/
package vaultprops

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

// TestFindTool_AttachmentKindHonouredBeforePropertiesIndexSynced reproduces
// the UAT finding through the agent's own knowledge_find tool: a note and an
// attachment sharing a matchable term, indexed into the TEXT index only
// (knowledge.SyncTracked — the same call buildVaultSearchVaultNoPropsSync
// makes in pkg/gateway/rest_knowledge_find_test.go) with vaultprops.Sync
// never called, so the properties index file never exists for this
// collection and openFindStore legitimately returns nil.
func TestFindTool_AttachmentKindHonouredBeforePropertiesIndexSynced(t *testing.T) {
	skipWithoutSQLite(t)

	home := f9Home(t)
	ws := f9Workspace(t, home)

	col, err := knowledge.CreateInWorkspace(home, ws, "kb", knowledge.Marker{DisplayName: "KB"})
	require.NoError(t, err)
	root := col.Root()

	f9Note(t, root, "quarterly-review.md",
		"# Quarterly Review\n\nOur quarterly numbers were strong this cycle.\n")
	f9Note(t, root, "assets/quarterly-contract.pdf", "binary-ish bytes, never opened\n")

	ix, err := knowledge.OpenIndex(home, root)
	require.NoError(t, err)
	defer func() { _ = ix.Close() }()
	_, err = knowledge.SyncTracked(context.Background(), ix,
		knowledge.SharedProgressTracker(root), knowledge.SyncOptions{})
	require.NoError(t, err)
	// No vaultprops.Sync call — see the file header. openFindStore's own
	// doc (find_tool.go) calls the resulting nil Store "the ordinary state
	// of a collection nobody has run check_integrity/indexing against yet".

	findTool := NewFindTool(home)

	t.Run("kind=attachment finds the attachment by name", func(t *testing.T) {
		res := findTool.Execute(f9Ctx("mia", ws), map[string]any{
			"words": "quarterly", "kind": "attachment",
		})
		require.NotNil(t, res)
		require.False(t, res.IsError,
			"kind=attachment must be answerable from the text index alone (no properties-store "+
				"column is needed for a plain name match) — got refusal: %s", res.ForLLM)
		require.Contains(t, res.ForLLM, "assets/quarterly-contract.pdf",
			"the attachment hit must be present in the tool's rendered response:\n%s", res.ForLLM)
	})

	t.Run("kind=note (default) does not also return the attachment", func(t *testing.T) {
		res := findTool.Execute(f9Ctx("mia", ws), map[string]any{"words": "quarterly"})
		require.NotNil(t, res)
		require.False(t, res.IsError, "a bare words query must keep working with no properties index: %s", res.ForLLM)
		require.Contains(t, res.ForLLM, "quarterly-review.md")
		require.False(t, strings.Contains(res.ForLLM, "quarterly-contract.pdf"),
			"an attachment must never be reported as a note:\n%s", res.ForLLM)
	})
}
