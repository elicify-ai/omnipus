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

// ── Row 14, facts half — LOUD placeholder (no carrier exists yet) ───────────
//
// The spec adds provider/model identity + facts to the error frame, but the
// event payload that would carry them (agent.ErrorPayload) has no such
// fields today, and the spec names no field names for them (§4 C-16's
// "additive" grant leaves names to GREEN). A LOUD t.Fatal placeholder, not a
// skip: GREEN adds the carrier and rewrites this placeholder into the real
// wire assertions.

func TestHubError_ErrorFrameFacts_CarrierMissing(t *testing.T) {
	t.Fatal("BLOCKED: agent.ErrorPayload has no provider/model identity or facts carrier — required by §7.2/§4 C-16 " +
		"(the error frame's payload.llm_error.facts = {provider, model, request_id}, all optional; request_id is " +
		"Verbose-render-only; NO retry facts — OBS-103; Detail untouched — D1). GREEN adds the carrier additively " +
		"(field names GREEN's choice per §4) and satisfies: identity present → facts carries the failing attempt's " +
		"provider/model and the captured request_id; identity absent → facts absent; and no retry_after_seconds/" +
		"retry_at/attempts/max_attempts keys under facts.")
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

// ── Row 16, frame half — LOUD placeholder (event kind unnamed) ──────────────
//
// The spec names the provider_fallback frame's wire shape (§7.1 item 3) and
// its once-per-pair persistence (C-18/MIN-103) but deliberately names no
// event kind or payload shape for it — the row cannot be spelled against a
// named symbol. A LOUD t.Fatal placeholder, not a skip.

func TestHubSyncTap_ProviderFallbackFrame_UnnamedEventKind(t *testing.T) {
	t.Fatal("BLOCKED: the spec names the provider_fallback frame (§7.1 item 3: type provider_fallback; session_id, " +
		"turn_id, answered_model, unavailable_model, unavailable_code enum rate_limited|model_retired; +seq per the " +
		"#823 pattern; no raw error text — C-2) and its once-per-pair persistence (C-18/MIN-103), but names no event " +
		"kind or payload shape for the fallback note. GREEN names the emitter and satisfies: the hub forwards it to a " +
		"provider_fallback frame carrying named fields only (sentinel scan 1 covers it), unavailable_code ∈ " +
		"{rate_limited, model_retired}, and the once-per-pair note is transcript-persisted so replay carries it " +
		"(C-17/MAJ-104). The persisted-entry half of row 16 is the pkg/agent placeholder's business.")
}
