// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// turn_stream_message_id_test.go — #823 catch-up-redesign unit coverage for
// pkg/agent/turn_stream.go's message-id mechanism: nextRoundMessageID,
// getCurrentMessageID, roundMessageIDOrNew, and stampStreamerMessageID.
//
// These are the direct, isolated tests of the [U]nverified assumption
// BE-DESIGN.md §11 item 2 flags: "several provider calls per logical answer
// must share one message_id ... Lane B verifies this in loop_truncation.go
// before implementing." The reuse/no-reuse decision under test here is the
// SAME code path the real streaming call site
// (pkg/agent/loop_run_turn.go::callProviderOnce) drives — see
// turn_stream_message_id_wiring_test.go for the end-to-end proof that the
// call site actually invokes it.
//
// Build: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestNextRoundMessageID|^TestStampStreamerMessageID|^TestRoundMessageIDOrNew' -p 1 ./pkg/agent/

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNextRoundMessageID_FreshEveryRoundByDefault proves the base case: with
// no continuation ever dispatched, every call mints a distinct, non-empty
// id — the ordinary "each round is its own message" behavior (a fresh
// bubble after every tool call, and for the turn's very first round).
func TestNextRoundMessageID_FreshEveryRoundByDefault(t *testing.T) {
	ts := &turnState{}

	id1 := ts.nextRoundMessageID()
	require.NotEmpty(t, id1, "the first round must mint a non-empty id")

	id2 := ts.nextRoundMessageID()
	require.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2,
		"a second round with no continuation dispatched must get its OWN fresh id, "+
			"not reuse the previous round's — this is the ordinary post-tool-call new-message case")

	assert.Equal(t, id2, ts.getCurrentMessageID(),
		"getCurrentMessageID must reflect the MOST RECENT nextRoundMessageID call")
}

// TestNextRoundMessageID_ReusesIDAfterContinuationDispatched proves the core
// ADR-087 D6 auto-continue invariant: the round immediately following a
// markContinuationDispatched call reuses the PREVIOUS round's id instead of
// minting a new one — because the model is finishing the same answer, not
// starting a new one, and the live tokens for both rounds must share one
// bubble identity.
func TestNextRoundMessageID_ReusesIDAfterContinuationDispatched(t *testing.T) {
	ts := &turnState{}

	firstRoundID := ts.nextRoundMessageID()
	require.NotEmpty(t, firstRoundID)

	// The first round's response came back truncated and eligible to
	// continue — this is exactly what
	// loop_truncation.go::evaluateTruncatedSuccess calls when it decides to
	// auto-continue (truncationContinuationEligible returned ok==true).
	ts.markContinuationDispatched()

	secondRoundID := ts.nextRoundMessageID()
	assert.Equal(t, firstRoundID, secondRoundID,
		"the round immediately after markContinuationDispatched must REUSE the previous "+
			"round's message id — this is the SAME logical answer, continued")

	// The reuse is one-shot: a THIRD round with no fresh
	// markContinuationDispatched call in between must NOT keep reusing the
	// stale id.
	thirdRoundID := ts.nextRoundMessageID()
	assert.NotEqual(t, secondRoundID, thirdRoundID,
		"continueSameMessageID must be consumed (cleared) by the reuse it granted — a round "+
			"two-deep past the dispatch, with no NEW dispatch of its own, must get a fresh id")
}

// TestNextRoundMessageID_BoundedContinuationChainAllShareOneID proves the
// multi-round case explicitly: ADR-087 D6.3 bounds auto-continue at 2
// rounds, and every round in that chain (the original truncated round, plus
// both continuations) must share exactly one message id — reusing
// markContinuationDispatched's re-arming behavior once per additional
// dispatched round, exactly as loop_truncation.go's real call sequence
// does.
func TestNextRoundMessageID_BoundedContinuationChainAllShareOneID(t *testing.T) {
	ts := &turnState{}

	round1 := ts.nextRoundMessageID() // truncated, dispatches continuation 1/2
	ts.markContinuationDispatched()

	round2 := ts.nextRoundMessageID() // continuation of round1
	ts.markContinuationDispatched()   // ALSO truncated, dispatches continuation 2/2

	round3 := ts.nextRoundMessageID() // continuation of round1+round2

	assert.Equal(t, round1, round2, "round 2 (first continuation) must share round 1's id")
	assert.Equal(t, round1, round3, "round 3 (second, bound-exhausting continuation) must still share round 1's id")
}

// TestNextRoundMessageID_ToolCallCarveOutDoesNotReuse proves the case the
// design flagged as easy to get wrong: ADR-087 D4's "truncated but has
// complete tool calls" carve-out (markContinuationPending, called from
// recordToolCalls) also leaves continuationPending true — but, unlike
// markContinuationDispatched, it must NOT arm message-id reuse, because
// tool execution runs before the next round: that next round is a genuinely
// NEW message, not a continuation of the truncated narration that preceded
// the tool call.
func TestNextRoundMessageID_ToolCallCarveOutDoesNotReuse(t *testing.T) {
	ts := &turnState{}

	roundWithTruncatedToolCall := ts.nextRoundMessageID()
	require.NotEmpty(t, roundWithTruncatedToolCall)

	// This round's response was truncated AND carried complete tool calls —
	// recordToolCalls' D4 carve-out branch.
	ts.markContinuationPending()

	nextRoundAfterToolExecution := ts.nextRoundMessageID()
	assert.NotEqual(t, roundWithTruncatedToolCall, nextRoundAfterToolExecution,
		"markContinuationPending (the D4 tool-calls carve-out) must NOT arm message-id reuse — "+
			"the round after tool execution is a NEW message, even though continuationPending "+
			"itself stays true until this round's own response resolves it")
}

// TestNextRoundMessageID_ResolveContinuationClearsPendingReuse proves
// resolveContinuation (called when a later round completes WITHOUT needing
// the truncation branch at all, or by D4a/D4b once they annotate the final
// content) disarms continueSameMessageID too — so a chain that resolves
// before ever being consumed by nextRoundMessageID cannot leak a stale
// "reuse" instruction into some unrelated later round.
func TestNextRoundMessageID_ResolveContinuationClearsPendingReuse(t *testing.T) {
	ts := &turnState{}

	firstID := ts.nextRoundMessageID()
	ts.markContinuationDispatched()

	// Something resolves the chain WITHOUT nextRoundMessageID ever consuming
	// the armed reuse flag (e.g. evaluateTruncatedSuccess's D4a "nothing was
	// ever produced" branch, which calls resolveContinuation directly).
	ts.resolveContinuation()

	secondID := ts.nextRoundMessageID()
	assert.NotEqual(t, firstID, secondID,
		"resolveContinuation must clear the pending reuse flag — a round minted after an "+
			"out-of-band resolution must not inherit a stale continuation's id")
}

// TestGetCurrentMessageID_EmptyBeforeAnyRound proves the zero-value state: a
// brand-new turnState (before nextRoundMessageID has ever run — e.g. a
// non-streaming turn that never reaches callProviderOnce's streaming
// branch) reports "" as its current message id.
func TestGetCurrentMessageID_EmptyBeforeAnyRound(t *testing.T) {
	ts := &turnState{}
	assert.Empty(t, ts.getCurrentMessageID())
}

// TestRoundMessageIDOrNew_UsesActiveRoundID proves the transcript-writer
// side of the wiring: once a round is active (nextRoundMessageID has run),
// roundMessageIDOrNew returns that SAME id — this is what lets
// turn_transcript.go::appendIntermediateAssistantTranscript persist an
// entry carrying the exact id the live streamed frames for that round
// already carry.
func TestRoundMessageIDOrNew_UsesActiveRoundID(t *testing.T) {
	ts := &turnState{}
	roundID := ts.nextRoundMessageID()

	assert.Equal(t, roundID, ts.roundMessageIDOrNew(),
		"roundMessageIDOrNew must return the SAME id as the active round, not mint a new one")
	// Calling it again (as a second appendIntermediateAssistantTranscript
	// for a DIFFERENT round would, after its own nextRoundMessageID call)
	// must still agree with the current round's id at the time each call
	// runs — non-destructive, not consumed by reading it.
	assert.Equal(t, roundID, ts.roundMessageIDOrNew())
}

// TestRoundMessageIDOrNew_FallsBackToFreshIDWhenNoRoundActive proves the
// pre-#823 behavior is preserved exactly for callers with no active
// streaming round (external_dispatch.go's child sub-turn narration, which
// never goes through callProviderOnce's streaming branch): each call mints
// its own fresh, distinct id, matching the old unconditional
// uuid.New().String() this method replaced.
func TestRoundMessageIDOrNew_FallsBackToFreshIDWhenNoRoundActive(t *testing.T) {
	ts := &turnState{}

	id1 := ts.roundMessageIDOrNew()
	id2 := ts.roundMessageIDOrNew()
	require.NotEmpty(t, id1)
	require.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2,
		"with no active round, every call must mint its OWN fresh id — this is the exact "+
			"pre-#823 behavior for a turnState that never streams (e.g. a delegation child's "+
			"non-streaming narration writes), and must not regress into accidentally sharing "+
			"one id across genuinely separate entries")
}

// TestStampStreamerMessageID_StampsCurrentRoundID verifies
// stampStreamerMessageID stamps the streamer with the CURRENT round's id
// (from the most recent nextRoundMessageID call), mirroring
// stampStreamerTurnID's own test exactly.
func TestStampStreamerMessageID_StampsCurrentRoundID(t *testing.T) {
	ts := &turnState{}
	roundID := ts.nextRoundMessageID()
	streamer := &producerAgentIDMockStreamer{}

	ts.stampStreamerMessageID(streamer)

	require.Len(t, streamer.setMessageIDCalls, 1, "SetMessageID must be called exactly once")
	assert.Equal(t, roundID, streamer.setMessageIDCalls[0],
		"the streamer must be stamped with the CURRENT round's own message id")
}

// TestStampStreamerMessageID_NoOpWhenStreamerDoesNotImplementInterface
// mirrors the sibling stamp functions' fallback tests: streamers without
// the optional SetMessageID method are left untouched.
func TestStampStreamerMessageID_NoOpWhenStreamerDoesNotImplementInterface(t *testing.T) {
	ts := &turnState{}
	ts.nextRoundMessageID()
	streamer := &mockStreamer{} // does NOT implement SetMessageID

	assert.NotPanics(t, func() {
		ts.stampStreamerMessageID(streamer)
	}, "must be a safe no-op for streamers without SetMessageID")
}
