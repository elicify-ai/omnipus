package gateway

// #1537 orders 1, 1a, 1b, 2, 3. Oracles: helper-model-notices-spec.md
// FR-001/002/003/007/012 and Machine-Verifiable Constraints (Wire).
// JSON adds the not-yet-generated fields without making Go compilation the RED.
import (
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/stretchr/testify/require"
)

const hmRoot = "hm-top"
const hmChild = "hm-helper"
const hmAgent = "researcher"

var hmSent = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func hmHandler(t *testing.T) (*WSHandler, map[string]*fakeHubConn) {
	t.Helper()
	h := makeMinimalHandler()
	conns := map[string]*fakeHubConn{}
	for _, sid := range []string{hmRoot, hmChild, "hm-middle", "hm-other"} {
		c := newFakeHubConn(sid)
		_, ok := h.hubs.getOrCreate(sid).bindLive(c)
		require.True(t, ok, "SETUP: bind %s", sid)
		conns[sid] = c
	}
	return h, conns
}

// In-process payloads are not wire DTOs and intentionally have no JSON tags.
// Decode dynamically so the spec's future optional field needs no production
// edit merely to compile a RED fixture. Output assertions detect its absence.
func hmChildPayload(t *testing.T, payload any) {
	t.Helper()
	require.NoError(t, json.Unmarshal([]byte(`{"ChildSessionID":"hm-helper","child_session_id":"hm-helper"}`), payload))
}

func hmRetry(t *testing.T, kind agent.EventKind, reason, turn string, child bool) agent.Event {
	t.Helper()
	p := agent.LLMRetryPayload{SessionID: hmRoot, Provider: "openrouter", Model: "m", Reason: reason,
		Attempt: 2, MaxAttempts: 6, MaxRetries: 5, SentAt: hmSent, RetryAt: hmSent.Add(10 * time.Second)}
	if child {
		// Accept either JSON spelling for the new in-process field (not a wire type).
		hmChildPayload(t, &p)
	}
	return agent.Event{Kind: kind, Meta: agent.EventMeta{TurnID: turn, AgentID: hmAgent}, Payload: p}
}

func hmStatus(t *testing.T, state, turn string, child bool) agent.Event {
	t.Helper()
	p := agent.ProviderStatusPayload{SessionID: hmRoot, State: state, SentAt: hmSent}
	if state == "waiting" {
		p.SilentSince = hmSent.Add(-30 * time.Second)
	}
	if state == "recovered" {
		p.Retries = 1
	}
	if child {
		hmChildPayload(t, &p)
	}
	return agent.Event{Kind: agent.EventKindProviderStatus, Meta: agent.EventMeta{TurnID: turn, AgentID: hmAgent}, Payload: p}
}

func hmFrames(t *testing.T, c *fakeHubConn) []map[string]any {
	t.Helper()
	raws := c.snapshot()
	out := make([]map[string]any, 0, len(raws))
	for _, raw := range raws {
		var f map[string]any
		require.NoError(t, json.Unmarshal(raw, &f))
		out = append(out, f)
	}
	return out
}

func hmAssertCopies(t *testing.T, c map[string]*fakeHubConn, frameType string) {
	t.Helper()
	own, top := hmFrames(t, c[hmChild]), hmFrames(t, c[hmRoot])
	require.Len(t, own, 1, "FR-001: helper's own chat receives one %s, not zero", frameType)
	require.Len(t, top, 1, "FR-002: top chat receives one named copy")
	require.Equal(t, hmChild, own[0]["session_id"])
	require.NotContains(t, own[0], "child_session_id")
	require.NotContains(t, own[0], "agent_id")
	require.Equal(t, hmRoot, top[0]["session_id"])
	require.Equal(t, hmChild, top[0]["child_session_id"])
	require.Equal(t, hmAgent, top[0]["agent_id"])
	for _, f := range []map[string]any{own[0], top[0]} {
		require.Equal(t, frameType, f["type"])
		seq, ok := f["seq"].(float64)
		require.True(t, ok, "each copy has a numeric sequence")
		require.Greater(t, seq, float64(0), "each session sequences its own copy")
		for _, k := range []string{"seq", "session_id", "child_session_id", "agent_id"} {
			delete(f, k)
		}
	}
	require.Equal(t, own[0], top[0], "FR-002: no fact except address/identity/seq may differ")
	require.Empty(t, hmFrames(t, c["hm-middle"]), "grandchild notices never go to a middle helper")
	require.Empty(t, hmFrames(t, c["hm-other"]), "no unrelated chat publication")
}

func TestHelperProviderRetryPublishesToHelperAndNamedRootCopy(t *testing.T) {
	for _, reason := range []string{"rate_limit", "network"} {
		t.Run(reason, func(t *testing.T) {
			h, c := hmHandler(t)
			h.hubSyncTap(hmRetry(t, agent.EventKindProviderRetry, reason, "helper-turn", true))
			hmAssertCopies(t, c, "provider_retry")
		})
	}
}

func TestHelperTimeoutAndEmptyRetryPublishBothCopies(t *testing.T) {
	for _, reason := range []string{"timeout", "empty_reply"} {
		t.Run(reason, func(t *testing.T) {
			h, c := hmHandler(t)
			h.hubSyncTap(hmRetry(t, agent.EventKindLLMRetry, reason, "helper-turn", true))
			hmAssertCopies(t, c, "provider_retry")
		})
	}
}

func TestHelperProviderStatusPublishesToHelperAndNamedRootCopy(t *testing.T) {
	for _, state := range []string{"waiting", "responding", "recovered"} {
		t.Run(state, func(t *testing.T) {
			h, c := hmHandler(t)
			h.hubSyncTap(hmStatus(t, state, "helper-turn", true))
			hmAssertCopies(t, c, "provider_status")
		})
	}
}

func TestOwnTurnProviderFramesCarryNoChildFields(t *testing.T) {
	for _, state := range []string{"retry", "waiting", "responding", "recovered"} {
		t.Run(state, func(t *testing.T) {
			h, c := hmHandler(t)
			e := hmStatus(t, state, "root-turn", false)
			if state == "retry" {
				e = hmRetry(t, agent.EventKindProviderRetry, "rate_limit", "root-turn", false)
			}
			h.hubSyncTap(e)
			frames := hmFrames(t, c[hmRoot])
			require.Len(t, frames, 1, "FR-003 own-frame positive control")
			require.Equal(t, hmRoot, frames[0]["session_id"])
			require.Equal(t, "root-turn", frames[0]["turn_id"])
			require.NotContains(t, frames[0], "child_session_id")
			require.NotContains(t, frames[0], "agent_id")
			require.Empty(t, hmFrames(t, c[hmChild]))
		})
	}
}

func TestHelperTurnEndPublishesEndedToTopChat(t *testing.T) {
	for _, status := range []agent.TurnEndStatus{agent.TurnEndStatusCompleted, agent.TurnEndStatusError, agent.TurnEndStatusAborted} {
		t.Run(string(status), func(t *testing.T) {
			h, c := hmHandler(t)
			h.hubSyncTap(hmRetry(t, agent.EventKindProviderRetry, "rate_limit", "old-turn", true))
			before := len(c[hmRoot].snapshot())
			end := agent.Event{Kind: agent.EventKindTurnEnd, Meta: agent.EventMeta{TurnID: "old-turn", AgentID: hmAgent}, Payload: agent.TurnEndPayload{Status: status, SessionID: hmRoot, ChatID: hmChild, IsRoot: false}}
			h.hubSyncTap(end)
			h.hubSyncTap(end) // duplicate delivery cannot manufacture a second tombstone
			after := hmFrames(t, c[hmRoot])[before:]
			require.Len(t, after, 1, "FR-012: one sequenced ended on the top chat, including stop/failure")
			require.Equal(t, "provider_status", after[0]["type"])
			require.Equal(t, "ended", after[0]["state"])
			require.Equal(t, "old-turn", after[0]["turn_id"])
			require.Equal(t, hmChild, after[0]["child_session_id"])
			require.Equal(t, hmAgent, after[0]["agent_id"])
			seq, ok := after[0]["seq"].(float64)
			require.True(t, ok, "ended has a numeric top-chat sequence")
			require.Greater(t, seq, float64(before))
			require.Len(t, hmFrames(t, c[hmChild]), 1, "ended is not sent to the helper's own chat")
			h.hubSyncTap(agent.Event{Kind: agent.EventKindTurnEnd, Meta: agent.EventMeta{TurnID: "unannounced", AgentID: hmAgent}, Payload: agent.TurnEndPayload{Status: status, SessionID: hmRoot, ChatID: hmChild, IsRoot: false}})
			require.Len(t, hmFrames(t, c[hmRoot]), before+1, "unannounced turn has no ended")
		})
	}
}
