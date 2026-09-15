// Omnipus — regression coverage for F2: the MV-9 attachment carve-out (see
// pkg/records/knowledgefind/find.go's textOnlyServable doc comment, and
// docs/internal/specs/unified-search-and-grep-spec.md) is defeated by the
// same root cause as F1 (this package's f9b_typed_only_reproduction_test.go)
// when the query's kind is `attachment` and its `words` matches nothing.
//
// THE DEFECT: on a build where the properties index cannot exist at all
// (records_no_sqlite / mipsle / netbsd / freebsd-arm — records.
// PropertyIndexAvailable is false), textOnlyServable() deliberately refuses
// `kind=attachment` too, so knowledge_find MUST answer with the honest
// "the properties index is not open" refusal rather than silently limiting
// itself to the ONE index this build has. Before F1's fix, findRecords's
// word-search zero-hit early return (find.go:578-592) ran BEFORE
// textOnlyServable() was ever consulted (it is checked only inside the
// d.Store == nil block starting at find.go:620), so a `kind=attachment`
// query whose `words` happened to match nothing returned
// "COMPLETE: yes — 0 records matched" instead — the exact silent
// broadening MV-9's carve-out exists to prevent, reachable purely because
// the word half missed. Change the word to one that matches and the SAME
// query already reached the refusal.
//
// This file runs ONLY on a build where records.PropertyIndexAvailable is
// false — the forcing tag (`records_no_sqlite`) is what lets it run on any
// host, matching the convention pkg/records/propindex_contract.go
// documents.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson,records_no_sqlite -count=1 -p 1 -run '^TestF2_AttachmentCarveOut' ./pkg/vaultprops/
package vaultprops

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestF2_AttachmentCarveOut_WordMissMustRefuseNotAnswerZero reproduces F2
// through the real knowledge_find tool: a note and an attachment sharing a
// matchable term, indexed into the TEXT index only (the same
// knowledge.SyncTracked call TestFindTool_AttachmentKindHonouredBefore
// PropertiesIndexSynced uses on the SQLite-capable build), on a build with
// no properties index at all. A `kind=attachment` query for a word that
// matches nothing must refuse, honestly naming the platform limitation —
// never answer a confident zero.
func TestF2_AttachmentCarveOut_WordMissMustRefuseNotAnswerZero(t *testing.T) {
	if records.PropertyIndexAvailable {
		t.Skip("this reproduction is specific to a build with no properties index at all; " +
			"see TestFindTool_AttachmentKindHonouredBeforePropertiesIndexSynced for the SQLite-capable case")
	}

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
	// No properties index exists on this build at all — openFindStore's
	// knowledge.PropertiesIndexPath os.Stat miss (or, on this build,
	// propindex.Open itself failing) hands Find() Store=nil unconditionally.

	findTool := NewFindTool(home)

	t.Run("kind=attachment, words matching something still works (sanity)", func(t *testing.T) {
		res := findTool.Execute(f9Ctx("mia", ws), map[string]any{
			"words": "quarterly", "kind": "attachment",
		})
		require.NotNil(t, res)
		require.True(t, res.IsError,
			"on a build with no properties index at all, kind=attachment must refuse honestly "+
				"(MV-9's carve-out) rather than answer from the text index alone — got: %s", res.ForLLM)
		require.Contains(t, res.ForLLM, "the properties index is not open", res.ForLLM)
	})

	t.Run("kind=attachment, words matching nothing must ALSO refuse, not answer a confident zero", func(t *testing.T) {
		res := findTool.Execute(f9Ctx("mia", ws), map[string]any{
			"words": "zzz-nomatch-nqxv-f2-regression", "kind": "attachment",
		})
		require.NotNil(t, res)
		if !res.IsError {
			t.Fatalf("F2: a kind=attachment query whose `words` matched nothing answered SUCCESS "+
				"(a confident zero) instead of refusing, on a build with no properties index at all — "+
				"the SAME query with a matching word (the sanity check above) correctly refuses. The "+
				"verdict must not depend on whether the word half happened to match.\ngot: %s", res.ForLLM)
		}
		require.Contains(t, res.ForLLM, "the properties index is not open",
			"expected the documented 'properties index is not open' refusal, not a different error\ngot: %s", res.ForLLM)
	})
}
