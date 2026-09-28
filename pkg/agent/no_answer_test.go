// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C RED tests for the no-answer outcome marker + notice (spec sec 1 C4,
// D20/D26, T4/FR-018; sec 16 items 3 and 27 backend half).

func TestNoAnswer_NoticeTextFormat(t *testing.T) {
	got := noAnswerNoticeText("openai", "gpt-5.2")
	assert.Equal(t, "The model (openai \u00b7 gpt-5.2) did not respond", got)
}

func TestNoAnswer_NoticeTextFormat_DistinctParts(t *testing.T) {
	got := noAnswerNoticeText("openrouter", "glm-5.3-flash")
	assert.Equal(t, "The model (openrouter \u00b7 glm-5.3-flash) did not respond", got)
}

// The notice-bearing entry: content IS the notice, marker rides the entry.

func TestNoAnswer_RoundWithThinkingNoAnswer_PersistsNoticeEntryWithMarker(t *testing.T) {
	provider := &thinkingStreamProvider{rounds: []thinkingRound{
		{reasoning: "I can complete this from memory; no tool needed.", answer: ""},
	}}
	al := newThinkingTestLoop(t, provider)
	w := newSessionWorker("wpc-noanswer", al, func() {})
	w.processTurn(context.Background(), bus.InboundMessage{
		Channel: "test",
		Sender:  bus.SenderInfo{CanonicalID: "user-a"},
		ChatID:  "chat-a",
		Content: "say something",
		Peer:    bus.Peer{Kind: bus.PeerDirect, ID: "user-a"},
	})
	entries := transcriptEntries(t, al)

	// The notice-bearing assistant entry.
	var noticeEntry *session.TranscriptEntry
	for i := range entries {
		e := entries[i]
		if e.Role == "assistant" && strings.Contains(e.Content, "did not respond") {
			noticeEntry = &entries[i]
		}
	}
	require.NotNil(t, noticeEntry, "a thinking round with empty answer must persist the notice at the answer position; entries=%+v", entries)
	assert.Equal(t, "The model (test \u00b7 test-model) did not respond", noticeEntry.Content,
		"the assistant entry content IS the D26 notice (SC-012: byte-identical on every surface)")
	assert.Equal(t, session.OutcomeNoAnswer, noticeEntry.Outcome,
		"the typed outcome marker rides the assistant entry (C4)")
	assert.Len(t, filterThinkingEntries(entries), 1,
		"the no-answer round still stores its thinking row (the notice never replaces it)")

	// T4 (FR-018): the notice is display- and delivery-only - never model
	// context. The context file records the round as the provider returned it
	// (empty answer content), so the notice string appears NOWHERE in any
	// provider-bound request.
	for i, req := range provider.requests {
		for _, m := range req {
			assert.NotContains(t, m.Content, "did not respond",
				"request %d: the D26 notice string must never enter model context (T4/FR-018)", i)
		}
	}
}
