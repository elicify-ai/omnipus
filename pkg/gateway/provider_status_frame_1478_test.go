package gateway

// Issues #1478 and #1280: the agent's provider_status event reaches the chat
// as one provider_status frame built from named fields only, keyed as one
// per-turn projection item (a newer state replaces the older one; the turn
// end clears it), exactly like provider_retry.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestIssue1478_ProviderStatusWaitingFrameCarriesNamedFieldsOnly(t *testing.T) {
	h, ch := pmHub(t)
	silentSince := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	sentAt := silentSince.Add(30 * time.Second)
	stallAt := silentSince.Add(5 * time.Minute)
	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindProviderStatus,
		Meta: agent.EventMeta{TurnID: "turn-ps-1", SessionKey: "chat-pm-1"},
		Payload: agent.ProviderStatusPayload{
			State: "waiting", SessionID: pmSessionID,
			SentAt: sentAt, SilentSince: silentSince, StallAt: stallAt,
		},
	})
	require.Len(t, ch, 1, "one provider_status frame per event")
	raw := <-ch
	var frame generated.ProviderStatusFrame
	require.NoError(t, json.Unmarshal(raw, &frame))
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Equal(t, "provider_status", m["type"])
	require.Equal(t, pmSessionID, m["session_id"])
	require.Equal(t, "turn-ps-1", m["turn_id"])
	require.Equal(t, "waiting", m["state"])
	require.Equal(t, pmJSONTime(t, sentAt), m["sent_at"])
	require.Equal(t, pmJSONTime(t, silentSince), m["silent_since"])
	require.Equal(t, pmJSONTime(t, stallAt), m["stall_at"])
	_, hasRetries := m["retries"]
	require.False(t, hasRetries, "retries belongs to the recovered state only")
	allowed := map[string]bool{"type": true, "session_id": true, "turn_id": true, "state": true, "sent_at": true, "silent_since": true, "stall_at": true, "seq": true}
	for k := range m {
		require.True(t, allowed[k], "unexpected field %q on the frame (contract: additionalProperties false)", k)
	}
}

func TestIssue1478_ProviderStatusOptionalFieldsAreOmittedWhenUnknown(t *testing.T) {
	h, ch := pmHub(t)
	sentAt := time.Date(2026, 10, 29, 12, 1, 0, 0, time.UTC)
	for _, p := range []agent.ProviderStatusPayload{
		{State: "waiting", SessionID: pmSessionID, SentAt: sentAt, SilentSince: sentAt.Add(-31 * time.Second)}, // no stall limit known
		{State: "responding", SessionID: pmSessionID, SentAt: sentAt},
		{State: "recovered", SessionID: pmSessionID, SentAt: sentAt, Retries: 2},
	} {
		h.hubSyncTap(agent.Event{Kind: agent.EventKindProviderStatus, Meta: agent.EventMeta{TurnID: "turn-ps-2"}, Payload: p})
	}
	require.Len(t, ch, 3)
	var waiting, responding, recovered map[string]any
	require.NoError(t, json.Unmarshal(<-ch, &waiting))
	require.NoError(t, json.Unmarshal(<-ch, &responding))
	require.NoError(t, json.Unmarshal(<-ch, &recovered))
	_, hasStall := waiting["stall_at"]
	require.False(t, hasStall, "an unknown silence limit is omitted, never a zero time")
	for _, k := range []string{"silent_since", "stall_at", "retries"} {
		_, has := responding[k]
		require.False(t, has, "responding carries no %s", k)
	}
	require.Equal(t, float64(2), recovered["retries"], "#1280: the recovery note's retry count")
	_, hasSilent := recovered["silent_since"]
	require.False(t, hasSilent)
}

func TestIssue1478_ProviderStatusWithoutSessionIsDropped(t *testing.T) {
	h, ch := pmHub(t)
	h.hubSyncTap(agent.Event{
		Kind:    agent.EventKindProviderStatus,
		Meta:    agent.EventMeta{TurnID: "turn-ps-3", SessionKey: "unbound-chat"},
		Payload: agent.ProviderStatusPayload{State: "responding", SentAt: time.Now()},
	})
	pmAssertNoFrame(t, ch)
}
