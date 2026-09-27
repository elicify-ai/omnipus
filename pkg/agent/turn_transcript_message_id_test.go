// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// turn_transcript_message_id_test.go — #823 catch-up-redesign coverage for
// the persisted side of the shared message-id mechanism:
// appendIntermediateAssistantTranscript (turn_transcript.go) must write the
// SAME id its round's live streamed frames already carry (via
// roundMessageIDOrNew, turn_stream.go), against a REAL
// *session.UnifiedStore and REAL transcript read-back — mirroring
// turn_adr057_test.go's own "real store, real turnState" discipline rather
// than a mock.
//
// Build: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestAppendIntermediateAssistantTranscript_MessageID' -p 1 ./pkg/agent/

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestAppendIntermediateAssistantTranscript_MessageID_MatchesActiveRoundID
// proves the persisted-side half of #823's live/persisted unification: once
// nextRoundMessageID has minted an id for the active round (exactly what
// the real streaming call site does before appendIntermediateAssistantTranscript
// ever runs — see loop_run_turn_response.go::recordToolCalls), the entry
// AppendTranscriptStrict receives carries that EXACT id, not some other
// freshly-minted uuid.
func TestAppendIntermediateAssistantTranscript_MessageID_MatchesActiveRoundID(t *testing.T) {
	store := u3NewRealStore(t)
	meta, err := store.NewSession(session.SessionTypeChat, "", "msgid-test-agent")
	require.NoError(t, err)

	ts := &turnState{
		sessionKey:          "msgid-transcript-sesskey",
		turnID:              "msgid-transcript-turn",
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
	}

	roundID := ts.nextRoundMessageID() // what the streaming call site does first
	require.NotEmpty(t, roundID)

	ts.appendIntermediateAssistantTranscript("narration before the tool call")

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Len(t, entries, 1, "exactly one entry must have been written")
	assert.Equal(t, roundID, entries[0].ID,
		"the persisted entry's ID must equal the SAME id nextRoundMessageID minted for this "+
			"round — this is what lets a live TokenFrame.message_id and the persisted "+
			"TranscriptEntry.ID agree on the wire (#823 design §4.4/§6.3)")
	assert.Equal(t, "narration before the tool call", entries[0].Content)
}

// TestAppendIntermediateAssistantTranscript_MessageID_TwoRoundsGetDistinctIDs
// proves the multi-round shape end-to-end against a real store: a SECOND
// round (a fresh nextRoundMessageID call, as the real call site issues once
// per LLM call) produces a SECOND, distinct persisted entry id — the
// ordinary post-tool-call new-message case, this time verified against the
// actual on-disk transcript rather than an in-memory assertion only.
func TestAppendIntermediateAssistantTranscript_MessageID_TwoRoundsGetDistinctIDs(t *testing.T) {
	store := u3NewRealStore(t)
	meta, err := store.NewSession(session.SessionTypeChat, "", "msgid-test-agent-2")
	require.NoError(t, err)

	ts := &turnState{
		sessionKey:          "msgid-transcript-sesskey-2",
		turnID:              "msgid-transcript-turn-2",
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
	}

	round1ID := ts.nextRoundMessageID()
	ts.appendIntermediateAssistantTranscript("first segment")

	round2ID := ts.nextRoundMessageID()
	ts.appendIntermediateAssistantTranscript("second segment")

	require.NotEqual(t, round1ID, round2ID, "test setup: two ordinary rounds must mint distinct ids")

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, round1ID, entries[0].ID, "first entry must carry round 1's own id")
	assert.Equal(t, round2ID, entries[1].ID, "second entry must carry round 2's own id, not round 1's")
	assert.NotEqual(t, entries[0].ID, entries[1].ID)
}

// TestAppendIntermediateAssistantTranscript_MessageID_FallsBackWhenNoActiveRound
// proves the pre-#823 behavior survives for a caller that never goes
// through the streaming call site (e.g. external_dispatch.go's child
// sub-turn narration): with no nextRoundMessageID call ever made, the
// persisted entry still gets a real, non-empty, unique id — never an empty
// string and never accidentally shared with a later such write.
func TestAppendIntermediateAssistantTranscript_MessageID_FallsBackWhenNoActiveRound(t *testing.T) {
	store := u3NewRealStore(t)
	meta, err := store.NewSession(session.SessionTypeChat, "", "msgid-test-agent-3")
	require.NoError(t, err)

	ts := &turnState{
		sessionKey:          "msgid-transcript-sesskey-3",
		turnID:              "msgid-transcript-turn-3",
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
	}
	// No nextRoundMessageID call — simulates external_dispatch.go's
	// childTS.appendIntermediateAssistantTranscript call sites, which never
	// stream through callProviderOnce.

	ts.appendIntermediateAssistantTranscript("first (no active round)")
	ts.appendIntermediateAssistantTranscript("second (no active round)")

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.NotEmpty(t, entries[0].ID)
	assert.NotEmpty(t, entries[1].ID)
	assert.NotEqual(t, entries[0].ID, entries[1].ID,
		"with no active streaming round, each write must still get its OWN fresh id — "+
			"this is the exact pre-#823 behavior (uuid.New().String() every call), not a "+
			"regression into sharing one id across genuinely unrelated entries")
}
