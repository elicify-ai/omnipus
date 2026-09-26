/**
 * provider_messages_frames_test.go — provider-messages spec RED tests:
 * TDD rows 14, 15, 16 (frame half), 29 (§7.2 gateway rows).
 *
 * Oracles come from the SPEC ONLY:
 *   - §7.1 item 3 / §7.2 line "hubSyncTap gains a handler forwarding
 *     EventKindProviderRetry (and only it)": the frame is built FROM NAMED
 *     FIELDS ONLY and never reads LLMRetryPayload.Error (C-2, MAJ-015) — no
 *     `error` field of any kind, no provider-body substring anywhere
 *     (sentinel scan 1, DG-1/DG-2; scan 2's DOM half is row 20's e2e and
 *     rows 17/18's component side).
 *   - Named fields and values (row 14 / §10 A-1): session_id, turn_id,
 *     provider, model, retry_at (RFC 3339), sent_at (RFC 3339, C-11/
 *     MAJ-108), retry_after_seconds, attempt (2..3, the call about to be
 *     made, C-8), max_attempts (MIN-104), error_code.
 *   - Error-frame pass-through pin (row 14, MAJ-001/D1): a curated
 *     ErrorPayload.Code passes message/code through untouched; Detail stays
 *     exactly as it is ("status=… body=…", §7.1 item 1's "detail stays
 *     exactly as it is").
 *   - Row 29 (RG-4): each of EventKindLLMRetry's seven other reasons
 *     (streaming_reset, timeout, context_limit, empty_response,
 *     orphan-tool-markup repair, truncated tool call, truncation continue)
 *     AND the delegated path's rate_limit retries produce NO provider_retry
 *     frame — §13 keeps the delegated attempt semantics (1..2 of 2) visibly
 *     separate from C-8's root semantics (2..3 of 3).
 *
 * COMPILE-RED: agent.EventKindProviderRetry, the additive LLMRetryPayload
 * fields (Provider, Model, RetryAt, SentAt, MaxAttempts, SessionID — §7.2
 * line 224 names the payload additions), and generated.ProviderRetryFrame do
 * not exist yet; the spec names them (§2/§7) so the tests spell them so
 * GREEN has an unambiguous target. Everything else referenced here exists.
 *
 * Session resolution: the hub's own convention (hubError, hubRateLimit,
 * hubJudgeVerdict) is payload SessionID-then-ChatID resolution; the test
 * synthesizes the payload with SessionID AND sets Meta.SessionKey so the
 * assertion — the frame arrives on the bound session — holds under either
 * seam GREEN picks, pinning behaviour, not seam choice.
 */

package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// pmSentinel is the #711 sentinel (MAJ-105): a provider-body substring that
// must appear in no new frame payload of any type and in no error-frame
// field except payload.llm_error.detail.
const pmSentinel = "SENTINEL-BODY-7Q4Z"

// pmSessionID is the session the test connection is bound under.
const pmSessionID = "hub-test-session:chat-pm-1"

// pmHub wires a minimal WSHandler with one connection bound to chat
// "chat-pm-1" → session "hub-test-session:chat-pm-1" and hubSyncTap
// installed on a fresh bus (cleaned up with the test).
func pmHub(t *testing.T) (*WSHandler, chan []byte) {
	t.Helper()
	bus := agent.NewEventBus()
	t.Cleanup(func() { bus.Close() })
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(8)
	attachHubTestBus(h, bus, "chat-pm-1", wc)
	return h, ch
}

// pmAssertNoFrame fails if ANY frame arrives within 150 ms — the negative
// half of row 29 (and the proof the bus is wired at all comes from the
// positive-control tests, so a dead tap cannot fake this green).
func pmAssertNoFrame(t *testing.T, ch chan []byte) {
	t.Helper()
	select {
	case data := <-ch:
		t.Fatalf("unexpected frame arrived: %s", data)
	case <-time.After(150 * time.Millisecond):
	}
}

// pmJSONTime renders a time the way encoding/json renders time.Time — the
// exact wire string the generated frame decodes from.
func pmJSONTime(t *testing.T, ts time.Time) string {
	t.Helper()
	b, err := json.Marshal(ts)
	require.NoError(t, err)
	return strings.Trim(string(b), `"`)
}

// pmJoinPath appends one map key to a dotted JSON path.
func pmJoinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// pmCollectSentinelPaths deep-walks a decoded JSON tree and records the path
// of every string value containing sentinel.
func pmCollectSentinelPaths(prefix string, node any, sentinel string, out *[]string) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			pmCollectSentinelPaths(pmJoinPath(prefix, k), child, sentinel, out)
		}
	case []any:
		for i, child := range v {
			pmCollectSentinelPaths(fmt.Sprintf("%s[%d]", prefix, i), child, sentinel, out)
		}
	case string:
		if strings.Contains(v, sentinel) {
			*out = append(*out, prefix)
		}
	}
}

// ── Row 14, retry half (C-2/C-8/C-11/MAJ-102/MAJ-015/MIN-104) ───────────────

func TestHubSyncTap_ProviderRetryFrame_NamedFieldsOnly(t *testing.T) {
	h, ch := pmHub(t)

	// Facts per §10 A-1 / C-11: sent_at = server wall clock at emission,
	// retry_at = sent_at + retry_after_seconds. Error is set to prove the
	// handler never reads it.
	sentAt := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	retryAt := sentAt.Add(120 * time.Second)

	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindProviderRetry, // COMPILE-RED: new event kind (MAJ-102)
		Meta: agent.EventMeta{TurnID: "turn-pm-retry-1", SessionKey: "chat-pm-1"},
		Payload: agent.LLMRetryPayload{
			Attempt:     2, // the call ABOUT to be made (C-8)
			Reason:      "rate_limit",
			Error:       pmSentinel, // C-2: log-only — must never reach the frame
			Provider:    "openrouter",
			Model:       "model-b",
			RetryAt:     retryAt,
			SentAt:      sentAt,
			MaxAttempts: 3,
			SessionID:   pmSessionID,
		},
	})

	require.Len(t, ch, 1, "EventKindProviderRetry must be forwarded as exactly one provider_retry frame")
	raw := <-ch

	var frame generated.ProviderRetryFrame // COMPILE-RED: new generated type
	require.NoError(t, json.Unmarshal(raw, &frame))

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	require.Equal(t, "provider_retry", m["type"])
	require.Equal(t, pmSessionID, m["session_id"])
	require.Equal(t, "turn-pm-retry-1", m["turn_id"])
	require.Equal(t, "openrouter", m["provider"])
	require.Equal(t, "model-b", m["model"])
	require.Equal(t, pmJSONTime(t, sentAt), m["sent_at"], "sent_at must carry the server wall clock (C-11/MAJ-108)")
	require.Equal(t, pmJSONTime(t, retryAt), m["retry_at"])
	require.Equal(t, float64(120), m["retry_after_seconds"])
	require.Equal(t, float64(2), m["attempt"], "attempt is the call about to be made (C-8)")
	require.Equal(t, float64(3), m["max_attempts"], "max_attempts from the payload field (MIN-104), never hard-coded")

	// error_code: the spec names the field (§7.1 item 3) but pins no value
	// mapping from the payload — an oracle gap, named: presence + non-empty
	// only.
	code, _ := m["error_code"].(string)
	require.NotEmpty(t, code, "error_code must be present on the retry frame (§7.1 item 3)")

	// C-2 named-fields-only: no `error` field of any kind, and the sentinel
	// carried in LLMRetryPayload.Error appears NOWHERE in the frame.
	_, hasErrorField := m["error"]
	require.False(t, hasErrorField, "retry frame carries an `error` field — C-2 forbids any error field (MAJ-015)")
	require.NotContains(t, string(raw), pmSentinel, "LLMRetryPayload.Error leaked into the frame — C-2/MAJ-015/DG-1")
}

// ── Row 14, error-frame pass-through pin (MAJ-001, D1) ──────────────────────

func TestHubError_CuratedErrorPassesThroughUntouched(t *testing.T) {
	h, ch := pmHub(t)

	// The agent translator assembled this sentence from the §6 auth template;
	// the hub is a thin translator and must not touch it.
	assembled := "Anthropic rejected the API key. Check the key in Settings → Providers."
	p := agent.ErrorPayload{
		Stage:   "provider",
		Code:    "provider_auth_failed",
		Message: assembled,
		ProviderError: &agent.ProviderError{
			Status: 401,
			Body:   `{"error":{"message":"Incorrect API key provided: sk-proj-****abcd"}}`,
		},
		SessionID: pmSessionID,
	}
	h.hubSyncTap(agent.Event{
		Kind:    agent.EventKindError,
		Meta:    agent.EventMeta{TurnID: "turn-pm-err-1"},
		Payload: p,
	})

	require.Len(t, ch, 1, "EventKindError must produce exactly one error frame")
	raw := <-ch

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	require.Equal(t, pmSessionID, m["session_id"])
	payload, ok := m["payload"].(map[string]any)
	require.True(t, ok, "error frame must carry a payload object")
	le, ok := payload["llm_error"].(map[string]any)
	require.True(t, ok, "error frame payload must carry llm_error")

	require.Equal(t, "provider_auth_failed", le["code"], "curated code passes through")
	require.Equal(t, assembled, le["message"], "the agent-assembled sentence passes through untouched (MAJ-001)")
	require.Equal(t, false, le["retryable"], "auth is terminal — not retryable (§6 auth copy; pin)")

	// D1: detail stays exactly as it is — the BuildDetail format (status=…
	// body=… preview) is the residual the spec pins, so assert its shape.
	detail, _ := le["detail"].(string)
	require.NotEmpty(t, detail, "detail must stay on the wire (D1: the round-0 removal plan is withdrawn)")
	require.Contains(t, detail, "status=401", "detail keeps the status=… body=… shape (D1: detail unchanged)")
}

// ── Row 14, facts half (C-16/OBS-103) — the real carrier ────────────────────
//
// GREEN's carrier: agent.ErrorPayload.ProviderError carries the failing
// attempt's identity (Provider/Model/RequestID, MAJ-110), and agent.
// WireLLMError threads it into generated.LLMError.Facts; the hub forwards
// untouched. The spec pins the SEMANTICS, not the seam names: identity
// present → facts on the wire; identity absent → facts absent; the facts
// key set is CLOSED — no retry facts (OBS-103); detail untouched (D1, pinned
// by the pass-through test above).

// pmLLMErrorOf pulls payload.llm_error out of a decoded error frame.
func pmLLMErrorOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	payload, ok := m["payload"].(map[string]any)
	require.True(t, ok, "error frame must carry a payload object")
	le, ok := payload["llm_error"].(map[string]any)
	require.True(t, ok, "error frame payload must carry llm_error")
	return le
}

func TestHubError_ErrorFrameFacts_C16(t *testing.T) {
	t.Run("identity present → facts carries the failing attempt's identity and the captured request_id", func(t *testing.T) {
		h, ch := pmHub(t)

		h.hubSyncTap(agent.Event{
			Kind: agent.EventKindError,
			Meta: agent.EventMeta{TurnID: "turn-pm-facts-1"},
			Payload: agent.ErrorPayload{
				Stage:   "provider",
				Code:    "provider_auth_failed",
				Message: "Anthropic rejected the API key. Check the key in Settings → Providers.",
				ProviderError: &agent.ProviderError{
					Status:    401,
					Body:      `{"error":{"message":"Incorrect API key provided: sk-proj-****abcd"}}`,
					Provider:  "anthropic",
					Model:     "claude-x",
					RequestID: "req-7q4z-1",
				},
				SessionID: pmSessionID,
			},
		})

		require.Len(t, ch, 1)
		raw := <-ch

		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		le := pmLLMErrorOf(t, m)

		facts, ok := le["facts"].(map[string]any)
		require.True(t, ok, "identity present → payload.llm_error.facts must be on the wire (C-16)")
		require.Equal(t, map[string]any{
			"provider":   "anthropic",
			"model":      "claude-x",
			"request_id": "req-7q4z-1",
		}, facts, "facts carries exactly the failing attempt's identity + captured request_id — the key set is CLOSED (C-16/OBS-103)")

		// OBS-103, structural: no retry facts under facts — retry timing lives
		// on the provider_retry frame alone (the row-14 retry half pins it).
		for _, banned := range []string{"retry_after_seconds", "retry_at", "attempts", "max_attempts"} {
			require.NotContains(t, facts, banned, "facts carries %q — OBS-103 forbids retry facts on the error frame", banned)
		}
	})

	t.Run("identity absent → facts absent", func(t *testing.T) {
		h, ch := pmHub(t)

		h.hubSyncTap(agent.Event{
			Kind: agent.EventKindError,
			Meta: agent.EventMeta{TurnID: "turn-pm-facts-2"},
			Payload: agent.ErrorPayload{
				Stage:         "provider",
				Code:          "provider_auth_failed",
				Message:       "Anthropic rejected the API key. Check the key in Settings → Providers.",
				ProviderError: nil,
				SessionID:     pmSessionID,
			},
		})

		require.Len(t, ch, 1)
		raw := <-ch

		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		le := pmLLMErrorOf(t, m)

		require.NotContains(t, le, "facts", "identity absent → facts must be absent — the catalogue sentence renders (AU-2)")
	})
}

// ── Row 15, scans 1 + 3 (C-1, C-2, DG-1, DG-2; MAJ-105) ─────────────────────
//
// Scan 1: the sentinel appears in no provider_retry/provider_fallback
// payload and in no error-frame field except payload.llm_error.detail.
// Scan 3 (positive control): the sentinel DOES appear in detail — proving
// the scan could see a leak. Scan 2 (non-Verbose DOM) is row 20's e2e half
// and rows 17/18's component side; the mutation check (forwarding
// LLMRetryPayload.Error turns scan 1 red) is demonstrated by Test A's
// NotContains assertion and is CHECK-phase work to re-demonstrate.

func TestSentinel_ProviderBodyAppearsOnlyInDetail_Scan1And3(t *testing.T) {
	h, ch := pmHub(t)

	// The raw body carries the sentinel; the curated message does not. Today
	// the only path a body can reach the frame is Detail (buildDetail's
	// "status=… body=…" preview) — exactly what D1 preserves.
	p := agent.ErrorPayload{
		Stage:   "provider",
		Code:    "provider_auth_failed",
		Message: "Anthropic rejected the API key. Check the key in Settings → Providers.",
		ProviderError: &agent.ProviderError{
			Status: 401,
			Body:   `{"error":{"message":"upstream said ` + pmSentinel + `"}}`,
		},
		SessionID: pmSessionID,
	}
	h.hubSyncTap(agent.Event{
		Kind:    agent.EventKindError,
		Meta:    agent.EventMeta{TurnID: "turn-pm-scan-1"},
		Payload: p,
	})

	require.Len(t, ch, 1)
	raw := <-ch

	var root any
	require.NoError(t, json.Unmarshal(raw, &root))
	var paths []string
	pmCollectSentinelPaths("", root, pmSentinel, &paths)

	require.NotEmpty(t, paths,
		"positive control (scan 3) FAILED: the sentinel does not appear in detail — the scan could not have seen a leak")
	for _, path := range paths {
		require.Equal(t, "payload.llm_error.detail", path,
			"sentinel appeared outside payload.llm_error.detail at %q (scan 1, C-1/C-2/DG-1)", path)
	}
}

// ── Row 29 (RG-4): the seven other reasons + delegated rate_limit ───────────
//
// Each EventKindLLMRetry reason stays unforwarded; the delegated path's
// rate_limit retries stay unannounced (§13: delegated policy unchanged).
// Green today BY DESIGN — EventKindLLMRetry has no hubSyncTap arm — and the
// positive control is Test A's compile-red provider_retry arm: once GREEN
// adds the forwarding, this test is what keeps the other reasons dark.

func TestLLMRetry_OtherReasonsAndDelegatedRateLimit_NeverForwarded(t *testing.T) {
	h, ch := pmHub(t)

	reasons := []string{
		"streaming_reset",
		"timeout",
		"context_limit",
		"empty_response",
		"orphan_tool_markup",  // orphan-tool-markup repair
		"tool_call_truncated", // truncated tool call
		"truncation_continue", // truncation continue
		"rate_limit",          // delegated path (loop_provider_retry.go) — stays unforwarded
	}
	for _, reason := range reasons {
		h.hubSyncTap(agent.Event{
			Kind: agent.EventKindLLMRetry,
			Meta: agent.EventMeta{TurnID: "turn-pm-29", SessionKey: "chat-pm-1"},
			Payload: agent.LLMRetryPayload{
				Attempt:     1,
				MaxRetries:  2,
				Reason:      reason,
				Error:       pmSentinel, // even with a raw error set: still never forwarded
				MaxAttempts: 3,          // COMPILE-RED field on this payload (additive, §7.2)
			},
		})
	}
	pmAssertNoFrame(t, ch)
}

// ── Row 16, frame half (C-2/C-17/C-18/MIN-103) — the named event kind ───────
//
// GREEN named the emitter: agent.EventKindProviderFallback carrying
// agent.ProviderFallbackPayload; the hub forwards it as the §7.1 item-3
// provider_fallback frame. Oracle per §7.1 item 3 + C-2: exactly one frame
// per event, named fields only, unavailable_code in {rate_limited,
// model_retired}, no raw error text anywhere on the wire (the sentinel scan
// covers it), and the once-per-pair persistence is the pkg/agent half
// (assemble_provider_message_test.go) + the replay carrier (below).

func TestHubSyncTap_ProviderFallbackFrame_NamedFieldsOnly(t *testing.T) {
	h, ch := pmHub(t)

	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindProviderFallback,
		Meta: agent.EventMeta{TurnID: "turn-pm-fb-1", SessionKey: "chat-pm-1"},
		Payload: agent.ProviderFallbackPayload{
			SessionID:        pmSessionID,
			TurnID:           "turn-pm-fb-1",
			AnsweredModel:    "model-fallback",
			UnavailableModel: "model-a",
			UnavailableCode:  "rate_limited",
		},
	})

	require.Len(t, ch, 1, "EventKindProviderFallback must be forwarded as exactly one provider_fallback frame")
	raw := <-ch

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	require.Equal(t, "provider_fallback", m["type"])
	require.Equal(t, pmSessionID, m["session_id"])
	require.Equal(t, "turn-pm-fb-1", m["turn_id"])
	require.Equal(t, "model-fallback", m["answered_model"])
	require.Equal(t, "model-a", m["unavailable_model"])
	require.Equal(t, "rate_limited", m["unavailable_code"])

	// C-2 named-fields-only: the frame's key set is closed — every key is one
	// the contract names (seq optional per #823); no `error`, no `reason`, no
	// raw provider text of any kind.
	for k := range m {
		require.Contains(t, []string{"type", "session_id", "turn_id",
			"answered_model", "unavailable_model", "unavailable_code", "seq"}, k,
			"provider_fallback frame carries unexpected field %q — C-2 named-fields-only", k)
	}
	require.NotContains(t, string(raw), pmSentinel, "raw error text leaked into the provider_fallback frame (C-2)")

	// The enum's second value forwards the same way.
	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindProviderFallback,
		Meta: agent.EventMeta{TurnID: "turn-pm-fb-2", SessionKey: "chat-pm-1"},
		Payload: agent.ProviderFallbackPayload{
			SessionID:        pmSessionID,
			TurnID:           "turn-pm-fb-2",
			AnsweredModel:    "model-fallback",
			UnavailableModel: "model-a",
			UnavailableCode:  "model_retired",
		},
	})
	raw2 := <-ch
	var m2 map[string]any
	require.NoError(t, json.Unmarshal(raw2, &m2))
	require.Equal(t, "model_retired", m2["unavailable_code"],
		"unavailable_code forwards verbatim — enum value model_retired (D12 hint gate input)")
}

// ── Row 16/30 replay carriers (C-13/C-17/MAJ-104) ───────────────────────────
//
// The persisted flag and the persisted note must survive the reload path:
// buildReplayErrorFrame round-trips TranscriptEntry.ProviderMessage into
// LLMErrorReplay.ProviderMessage; buildReplayFallbackNote turns a persisted
// EntryTypeProviderFallback entry into the replay_provider_fallback carrier
// with the pair facts intact (the SPA dedups it against the live
// announcement on entry_id, FB-2).

func TestReplayErrorFrame_ProviderMessageFlagRoundTrips_MAJ104(t *testing.T) {
	flagged := session.TranscriptEntry{
		ID: "entry-flag-1", Type: session.EntryTypeSystem, Status: "error",
		Content:         "OpenRouter rejected the API key. Check the key in Settings → Providers.",
		ErrorCode:       "provider_auth_failed",
		ProviderMessage: true,
	}
	f := buildReplayErrorFrame(pmSessionID, flagged)
	require.NotNil(t, f.Payload, "flagged entry must carry an llm_error replay payload")
	require.NotNil(t, f.Payload.LlmError.ProviderMessage,
		"provider_message=true must round-trip onto the replay carrier (MAJ-104/C-14)")
	require.True(t, *f.Payload.LlmError.ProviderMessage)
	require.Equal(t, "provider_auth_failed", f.Payload.LlmError.Code)
	require.Equal(t, flagged.Content, f.Payload.LlmError.Message)

	unflagged := session.TranscriptEntry{
		ID: "entry-plain-1", Type: session.EntryTypeSystem, Status: "error",
		Content:   "rate_limit: policyRule (retry after 30s)",
		ErrorCode: "rate_limited",
	}
	f2 := buildReplayErrorFrame(pmSessionID, unflagged)
	require.NotNil(t, f2.Payload)
	require.Nil(t, f2.Payload.LlmError.ProviderMessage,
		"own-limiter row persists provider_message=false → the replay carrier must OMIT the flag (RG-2: catalogue copy)")
}

func TestReplayFallbackNote_CarrierRoundTrips_C17(t *testing.T) {
	entry := session.TranscriptEntry{
		ID: "entry-fbnote-1", Type: session.EntryTypeProviderFallback, Role: "system",
		Content:          "Answered by the Fallback model (model-fallback) because model-a was unavailable.",
		Timestamp:        time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC),
		Model:            "model-fallback",
		UnavailableModel: "model-a",
		UnavailableCode:  "rate_limited",
	}
	note := buildReplayFallbackNote(pmSessionID, entry)

	require.Equal(t, "replay_provider_fallback", note.Type)
	require.Equal(t, pmSessionID, note.SessionId)
	require.Equal(t, "entry-fbnote-1", note.EntryId)
	require.Equal(t, entry.Content, note.Message)
	require.NotNil(t, note.AnsweredModel)
	require.Equal(t, "model-fallback", *note.AnsweredModel)
	require.NotNil(t, note.UnavailableModel)
	require.Equal(t, "model-a", *note.UnavailableModel)
	require.NotNil(t, note.UnavailableCode)
	require.Equal(t, "rate_limited", *note.UnavailableCode)
	require.NotContains(t, note.Message, pmSentinel, "the note carrier never carries raw provider text (C-2)")
}
