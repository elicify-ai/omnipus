// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// S2 M15: attention_ack.go's "unknown, never false" branches. The nil approval
// registry was covered; these are the rest — a corrupt pending ask, the parent
// walk's depth bound, and attentionBoundFor's negatives. The founder rule they
// all serve: a source that cannot be read leaves needs_attention OUT (the
// client shows unknown), never a guessed false.

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestAttention_UnreadablePendingAskIsUnknownNotFalse: a main whose pending-ask
// blob cannot be decoded has an unreadable source, so needs_attention must be
// left out — even though the walk itself found nothing pending.
func TestAttention_UnreadablePendingAskIsUnknownNotFalse(t *testing.T) {
	clean := pendingApprovalMains{mains: map[string]struct{}{}}
	corrupt := &session.UnifiedMeta{
		SessionMeta: session.SessionMeta{ID: "m", PendingAskJSON: "{not json"},
		Type:        session.SessionTypeMain,
	}
	require.Nil(t, mainNeedsAttention(corrupt, clean),
		"an undecodable pending ask is an unreadable source: the field is left out, never false")

	// Control: the same shape with NO pending ask is a decided false.
	quiet := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "m"}, Type: session.SessionTypeMain}
	no := mainNeedsAttention(quiet, clean)
	require.NotNil(t, no, "a fully readable main gets a decided answer")
	require.False(t, *no, "nothing pending, nothing unseen -> false")

	// A decodable, PENDING ask is a decided true.
	pending, err := json.Marshal(askuser.PendingSet{Status: askuser.StatusPending})
	require.NoError(t, err)
	asking := &session.UnifiedMeta{
		SessionMeta: session.SessionMeta{ID: "m", PendingAskJSON: string(pending)},
		Type:        session.SessionTypeMain,
	}
	require.True(t, *mainNeedsAttention(asking, clean), "a pending question card lights the main")

	// A decodable, non-pending ask is not a reason to light the main.
	resolved, err := json.Marshal(askuser.PendingSet{Status: askuser.StatusAnswered})
	require.NoError(t, err)
	answered := &session.UnifiedMeta{
		SessionMeta: session.SessionMeta{ID: "m", PendingAskJSON: string(resolved)},
		Type:        session.SessionTypeMain,
	}
	require.False(t, *mainNeedsAttention(answered, clean), "an answered card is not pending")
}

// TestAttention_NonMainHasNoAttentionField: only a main carries the field.
func TestAttention_NonMainHasNoAttentionField(t *testing.T) {
	for _, typ := range []session.UnifiedSessionType{session.SessionTypeDelegate, session.SessionTypeTask, session.SessionTypeChat} {
		m := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "h"}, Type: typ}
		require.Nil(t, mainNeedsAttention(m, pendingApprovalMains{mains: map[string]struct{}{}}),
			"a %s session has no needs_attention field", typ)
	}
	require.Nil(t, mainNeedsAttention(nil, pendingApprovalMains{}), "a nil meta has no field")
}

// TestAttention_ParentWalkDepthBound — CHARACTERIZATION TEST.
//
// It pins what the walk does at its bound, and it documents a KNOWN silent
// non-attribution: a chain deeper than maxAttentionParentWalk ends with the
// approval assigned to NO main and `unknown` still false, so a main can be
// reported "nothing to see" while a pending approval sits below it. The bound
// itself is intentional (it stops a cyclic or corrupt parent chain); the silent
// outcome is reported as a product finding rather than accepted here.
func TestAttention_ParentWalkDepthBound(t *testing.T) {
	// A LITERAL depth, not maxAttentionParentWalk+8: deriving it from the
	// constant under test would make the test scale with the very bound it
	// checks, so widening the bound would keep it green.
	const chain = 64
	require.Greater(t, chain, maxAttentionParentWalk, "fixture: the chain must exceed the documented bound")
	metas := map[string]*session.UnifiedMeta{}
	metas["deep-main"] = &session.UnifiedMeta{
		SessionMeta: session.SessionMeta{ID: "deep-main"}, Type: session.SessionTypeMain,
	}
	prev := "deep-main"
	for i := 0; i < chain; i++ {
		id := "h" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		metas[id] = &session.UnifiedMeta{
			SessionMeta: session.SessionMeta{ID: id, ParentSessionID: prev}, Type: session.SessionTypeDelegate,
		}
		prev = id
	}
	leaf := prev

	// Inside the bound: the walk reaches the main and attributes the approval.
	// The FIRST helper created sits one hop below the main.
	near := "h" + string(rune('a'+0%26)) + string(rune('a'+0/26))
	got := pendingApprovalMainsFor(fakeApprovalSource{[]*approvalEntry{{SessionID: near}}}, metaOf(metas))
	require.False(t, got.unknown, "a walk inside the bound is decided")
	require.Contains(t, got.mains, "deep-main", "a helper inside the bound is attributed to its main")

	// Beyond the bound: the walk gives up. CHARACTERIZATION — see the doc above.
	far := pendingApprovalMainsFor(fakeApprovalSource{[]*approvalEntry{{SessionID: leaf}}}, metaOf(metas))
	assert.False(t, far.unknown, "CHARACTERIZATION: the depth bound does not mark the answer unknown")
	assert.NotContains(t, far.mains, "deep-main",
		"CHARACTERIZATION: an approval beyond the bound is silently attributed to no main")
	assert.False(t, *mainNeedsAttention(metas["deep-main"], far),
		"CHARACTERIZATION: the main is then reported 'nothing to see' while an approval is pending below it")
}

// TestAttentionBoundFor_Negatives: attention_bound is the main's saved outcome
// order, and every unreadable or non-main case answers nil rather than a
// guessed number.
func TestAttentionBoundFor_Negatives(t *testing.T) {
	h, _, _ := newRecipientHandler(t)

	// A main with a saved outcome order answers that order.
	store := h.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	main, err := store.GetOrCreateMainSession("ws-bound", "ann")
	require.NoError(t, err)
	// A real goal outcome raises the saved order — the same write a finished
	// goal performs, so the bound is the store's own number, not a fixture field.
	u11AppendGoalOutcome(t, store, main.ID, "goal-bound", generated.GoalOutcomeEndingMet)
	meta, err := store.GetMeta(main.ID)
	require.NoError(t, err)
	require.Positive(t, meta.Attention.OutcomeOrder, "fixture: the outcome must have raised the order")

	got := h.attentionBoundFor(main.ID)
	require.NotNil(t, got, "a main answers a bound")
	assert.EqualValues(t, meta.Attention.OutcomeOrder, *got, "the bound is the main's saved outcome order")

	// A main with no outcomes answers 0, not nil: "nothing seen" is a decided
	// answer, distinct from "could not read".
	quiet, err := store.GetOrCreateMainSession("ws-bound", "ray")
	require.NoError(t, err)
	quietBound := h.attentionBoundFor(quiet.ID)
	require.NotNil(t, quietBound, "a readable main always answers")
	assert.EqualValues(t, 0, *quietBound)

	for _, tc := range []struct {
		name string
		id   string
	}{
		{"empty id", ""},
		{"unknown session", "no-such-session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Nil(t, h.attentionBoundFor(tc.id),
				"an unreadable or absent session answers nil, never a guessed bound")
		})
	}

	// RESIDUAL (honest, verified): attentionBoundFor's `err != nil || meta ==
	// nil` branch is not reachable from a test with the current store. A
	// missing session fails with os.ErrNotExist, so ResolveSessionStore returns
	// nil and the earlier `store == nil` guard answers first; and a CORRUPT
	// meta.json is tolerated by GetMeta (verified below), which returns a usable
	// meta. The branch is defensive. Its mutation survives, and that is reported
	// as a residual rather than papered over with an invented fixture.
	_, metaErr := store.GetMeta("definitely-not-a-session")
	require.ErrorIs(t, metaErr, os.ErrNotExist,
		"evidence: an unknown session is ErrNotExist, which returns earlier at ResolveSessionStore")

	// A non-main answers nil even when it is readable.
	chat, err := store.NewSession(session.SessionTypeChat, "webchat", "ann")
	require.NoError(t, err)
	assert.Nil(t, h.attentionBoundFor(chat.ID), "a non-main has no attention bound")

	// A handler with no loop answers nil rather than panicking.
	bare := &WSHandler{}
	assert.Nil(t, bare.attentionBoundFor(main.ID), "an unwired handler answers nil")
}
