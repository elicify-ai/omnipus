// Omnipus — the store coverage caveat on the response (Codex review
// 2026-09-14, finding 6): a Deps whose store is usable but whose collection
// was not fully evaluated must yield complete:false with the caveat as the
// reason, while the rows the store does hold are still returned.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestFind_StoreCoverageCaveatMarksTheAnswerIncomplete — the verdict side of
// finding 6. The rows come back (partial results stay available); the verdict
// does not claim the collection was fully evaluated; the caveat sentence is
// the reason and rides the problems list.
func TestFind_StoreCoverageCaveatMarksTheAnswerIncomplete(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "12.5")

	d := f.deps()
	d.StoreCoverageCaveat = "this knowledge base was not fully evaluated: every readable file is " +
		"indexed, but 1 of the 2 files Sync saw could not be read, so records in them cannot " +
		"appear in any answer: garden/locked.md"

	req := generated.VaultFindRequest{Type: strPtrTo("plant")}
	resp, err := Find(context.Background(), d, req)
	require.NoError(t, err)

	// The readable record is still returned — partial results stay available.
	require.Len(t, resp.Rows, 1, "the rows the store holds must still be returned")

	// The verdict is complete:false, with the caveat as the stated reason.
	assert.False(t, resp.Complete, "an answer over a not-fully-evaluated collection must not be complete")
	require.NotNil(t, resp.CompleteReason)
	assert.Contains(t, *resp.CompleteReason, "problem(s) reported")

	// The caveat itself is a recorded problem the caller can read verbatim.
	found := false
	for _, p := range resp.Problems {
		if strings.Contains(p.Reason, "not fully evaluated") && strings.Contains(p.Reason, "garden/locked.md") {
			found = true
		}
	}
	assert.True(t, found, "the caveat must ride the problems list naming the unreadable file; got %+v", resp.Problems)
}

// TestFind_NoCaveatKeepsTheVerdictComplete — the negative control: without a
// caveat the same query stays complete, so the test above cannot be passing
// vacuously.
func TestFind_NoCaveatKeepsTheVerdictComplete(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "12.5")

	req := generated.VaultFindRequest{Type: strPtrTo("plant")}
	resp, err := Find(context.Background(), f.deps(), req)
	require.NoError(t, err)
	require.Len(t, resp.Rows, 1)
	assert.True(t, resp.Complete, "a fully evaluated collection keeps its complete verdict")
}

// strPtrTo is the local pointer helper for the generated request field.
func strPtrTo(s string) *string { return &s }
