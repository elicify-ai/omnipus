package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This checks the real builtin's admission, not a synthetic successful tool
// result: a genuine pending card must exist and have the server-derived owner.
func (h *wu4TranscriptHarness) assertSecretCardAdmitted(t *testing.T) {
	t.Helper()
	assert.Equal(t, 1, len(h.cards), "permitted request_secret must create exactly one actual pending card in this conversation")
	if len(h.cards) == 1 {
		rec := h.cards[0]
		assert.Equal(t, h.f.root, rec.Card.SessionId, "card must be bound to the actual transcript session, never an empty id or routing key")
		assert.Equal(t, "qa-secret-wake-owner", rec.Owner, "card owner comes from the real session")
		assert.Equal(t, "omnipus", rec.Card.ActingAgentId)
		assert.Equal(t, "pending", rec.Card.State)
		stored, err := h.f.al.SecretRequests().Get(rec.Card.RequestId, rec.Owner)
		require.NoError(t, err)
		assert.Equal(t, rec.Card, stored.Card, "the pending card must really be readable from the registry")
	}
	snapshot := h.f.al.SecretRequests().Snapshot("qa-secret-wake-owner", h.f.root)
	assert.Equal(t, 1, len(snapshot), "the real conversation snapshot must contain the admitted card")
	requests := h.provider.snapshot()
	if len(requests) > 0 {
		for _, message := range requests[len(requests)-1] {
			if message.Role == "tool" && message.ToolCallID == wu4TranscriptSecretCallID {
				t.Logf("actual request_secret tool result seen by the model: %q", message.Content)
			}
		}
	}
}

// Observable proof of TranscriptStore wiring: both actual calls must be in
// THIS session's persisted transcript, not only in an in-memory tool context.
func (h *wu4TranscriptHarness) assertToolsArchived(t *testing.T, probeID string) {
	t.Helper()
	entries, err := h.f.al.GetSessionStore().ReadTranscript(h.f.root)
	require.NoError(t, err)
	wanted := map[string]string{probeID: wu4OriginProbeName, wu4TranscriptSecretCallID: "request_secret"}
	counts := make(map[string]int)
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if toolName, selected := wanted[string(call.ID)]; selected {
				counts[string(call.ID)]++
				assert.Equal(t, toolName, call.Tool)
			}
		}
	}
	for id := range wanted {
		assert.Equal(t, 1, counts[id], "executed tool %s must be durably recorded exactly once in its owning conversation", id)
	}
}

func wu4AssertContinuationTranscript(t *testing.T, kind string) {
	t.Helper()
	h := newWU4TranscriptHarness(t, false)
	input := h.runWorkerContinuation(t, kind)
	h.requireInputConsumedOnce(t, input)
	observations, err := h.probe.snapshot()
	require.NoError(t, err)
	require.Equal(t, 2, len(observations), "instrument: original and continuation workload tools both executed")
	require.NotEmpty(t, observations[0].turnID)
	require.NotEmpty(t, observations[1].turnID)
	require.NotEqual(t, observations[0].turnID, observations[1].turnID, "instrument: this is post-turn Continue, not mid-turn polling")
	require.Equal(t, h.f.root, observations[0].sessionID, "instrument: original direct turn has its actual transcript identity")
	wantLabel := ""
	if kind == "peer" {
		wantLabel = "asked by noah"
	}
	require.Equal(t, wantLabel, observations[1].label, "instrument: the repaired P0-d origin handoff is really exercised")
	require.False(t, observations[1].channelTriggered, "web and peer inputs permit secret-card admission; no channel bypass is expected")
	t.Logf("actual continuation tool transcript session=%q; required owning session=%q", observations[1].sessionID, h.f.root)
	assert.Equal(t, h.f.root, observations[1].sessionID, "post-turn continuation must carry the session's actual ToolTranscriptSessionID")
	h.assertSecretCardAdmitted(t)
	h.assertToolsArchived(t, wu4TranscriptContinuationProbeID)
	assert.Equal(t, 4, len(h.provider.snapshot()), "request_secret must park the continuation without a further model call")
}

func TestWU4ContinuationRetainsTranscriptContextForWebInput(t *testing.T) {
	wu4AssertContinuationTranscript(t, "web")
}

func TestWU4ContinuationRetainsTranscriptContextForPeerInput(t *testing.T) {
	wu4AssertContinuationTranscript(t, "peer")
}

func TestWU4DirectTurnCarriesTranscriptContextAndAdmitsSecretCard(t *testing.T) {
	h := newWU4TranscriptHarness(t, true)
	_, _, err := h.f.al.processMessage(context.Background(), h.initialMessage())
	require.NoError(t, err)
	observations, err := h.probe.snapshot()
	require.NoError(t, err)
	require.Equal(t, 1, len(observations), "one real direct-turn workload tool executes")
	assert.Equal(t, h.f.root, observations[0].sessionID, "direct turn supplies the actual transcript session id")
	require.NotEmpty(t, observations[0].turnID)
	require.Empty(t, observations[0].label)
	require.False(t, observations[0].channelTriggered)
	t.Logf("actual direct tool transcript session=%q; required owning session=%q", observations[0].sessionID, h.f.root)
	h.assertSecretCardAdmitted(t)
	h.assertToolsArchived(t, wu4TranscriptInitialProbeID)
	assert.Equal(t, 2, len(h.provider.snapshot()), "real request_secret admission parks without another model call")
}
