// provider_messages_projection_test.go — gate finding F3: the provider_retry
// and provider_fallback frames must register with the WS hub's active-turn
// projection (provider-messages spec section 2, MAJ-108) under stable
// per-turn keys — a newer retry replaces the item (the rebuild shows only
// the pending attempt), and the item leaves with the turn (forgetTurn at
// TurnEnd). Before the fix both frames published with empty hubFrameMeta, so
// a mid-retry snapshot or rebuild showed a turn with no visible retry.
package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

func TestHub_ProviderRetryFallbackRegistersProjectionItems_F3(t *testing.T) {
	h, ch := pmHub(t)

	turn := "turn-pm-f3"
	retryEvent := func(attempt int) agent.Event {
		sentAt := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
		return agent.Event{
			Kind: agent.EventKindProviderRetry,
			Meta: agent.EventMeta{TurnID: turn, SessionKey: "chat-pm-1"},
			Payload: agent.LLMRetryPayload{
				Attempt:     attempt,
				MaxAttempts: 3,
				Reason:      "rate_limit",
				Provider:    "openrouter",
				Model:       "model-b",
				SentAt:      sentAt,
				RetryAt:     sentAt.Add(2 * time.Second),
				SessionID:   pmSessionID,
			},
		}
	}

	findItem := func(turnID, prefix string) *projSnapshotItem {
		hub := h.hubs.getOrCreate(pmSessionID)
		hub.mu.Lock()
		items := hub.proj.snapshot()
		hub.mu.Unlock()
		var found *projSnapshotItem
		for i := range items {
			if items[i].turnID == turnID && strings.HasPrefix(items[i].id, prefix) {
				found = &items[i]
			}
		}
		return found
	}
	countItems := func(turnID, prefix string) int {
		hub := h.hubs.getOrCreate(pmSessionID)
		hub.mu.Lock()
		items := hub.proj.snapshot()
		hub.mu.Unlock()
		n := 0
		for _, it := range items {
			if it.turnID == turnID && strings.HasPrefix(it.id, prefix) {
				n++
			}
		}
		return n
	}

	// The first retry (attempt 2) registers exactly one provider_retry item.
	h.hubSyncTap(retryEvent(2))
	select {
	case <-ch:
	default:
		t.Fatal("retry frame must still be delivered to the live connection")
	}
	it := findItem(turn, "provider_retry:")
	require.NotNil(t, it, "the retry frame must register a projection item (F3/MAJ-108)")
	require.Equal(t, "provider_retry:"+turn, it.id)
	var f1 generated.ProviderRetryFrame
	require.NoError(t, json.Unmarshal(it.start, &f1))
	require.Equal(t, 2, f1.Attempt)

	// A newer retry for the same turn REPLACES the item (same stable key) —
	// it must not accumulate a second item.
	h.hubSyncTap(retryEvent(3))
	select {
	case <-ch:
	default:
		t.Fatal("second retry frame must still be delivered")
	}
	it = findItem(turn, "provider_retry:")
	require.NotNil(t, it)
	var f2 generated.ProviderRetryFrame
	require.NoError(t, json.Unmarshal(it.start, &f2))
	require.Equal(t, 3, f2.Attempt, "the newer attempt's frame replaces the item's start")
	require.Equal(t, 1, countItems(turn, "provider_retry:"), "retries replace; they must not accumulate")

	// The fallback frame registers its own per-turn item.
	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindProviderFallback,
		Meta: agent.EventMeta{TurnID: turn, SessionKey: "chat-pm-1"},
		Payload: agent.ProviderFallbackPayload{
			SessionID:        pmSessionID,
			TurnID:           turn,
			AnsweredModel:    "model-b",
			UnavailableModel: "model-a",
			UnavailableCode:  "rate_limited",
		},
	})
	select {
	case <-ch:
	default:
		t.Fatal("fallback frame must still be delivered")
	}
	it = findItem(turn, "provider_fallback:")
	require.NotNil(t, it, "the fallback frame must register a projection item (F3/MAJ-108)")
	require.Equal(t, "provider_fallback:"+turn, it.id)

	// Terminal: forgetTurn (the TurnEnd path) drops both items.
	hub := h.hubs.getOrCreate(pmSessionID)
	hub.forgetTurn(turn)
	for _, item := range func() []projSnapshotItem {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return hub.proj.snapshot()
	}() {
		require.NotEqual(t, turn, item.turnID, "no item of the ended turn may survive forgetTurn")
	}
}
