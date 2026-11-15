package gateway

// Issue #1664 — gateway-side RED tests: the waiting frame forwards the new
// attempt fields (B3), the loop's timeout retry reaches the chat as one
// provider_retry frame with error_code no_response (B5), and the two silent
// drops in hubProviderStatus become one logsafeWarn each (B11, B12).
// Oracles: /workspace/logs/fix-1664/verify-report.md §B3/B5/B11/B12 and the
// founder decisions of 2026-11-07. Harness: the #1478 frame tests' pmHub
// (provider_messages_frames_test.go); log capture through slog's default
// logger, swapped for the duration of a test and restored via t.Cleanup.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
)

// B3: the waiting frame forwards attempt (1-based) and max_attempts (today
// 3). The payload is built through its WIRE shape: the fields are new
// contract fields, so the struct literal cannot name them yet — unmarshal
// fills them once they exist, and the frame must carry them to the SPA.
// Today the frame has no such keys (the payload loses them) — that absence
// is the RED.
func TestIssue1664_B3_WaitingFrameForwardsAttemptAndMaxAttempts(t *testing.T) {
	h, ch := pmHub(t)
	sentAt := time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC)
	silentSince := sentAt.Add(-31 * time.Second)
	stallAt := silentSince.Add(7 * time.Second)
	payloadJSON := fmt.Sprintf(`{"State":"waiting","SessionID":%q,"SentAt":%q,"SilentSince":%q,"StallAt":%q,"attempt":1,"max_attempts":3}`,
		pmSessionID, sentAt.Format(time.RFC3339Nano), silentSince.Format(time.RFC3339Nano), stallAt.Format(time.RFC3339Nano))
	var p agent.ProviderStatusPayload
	require.NoError(t, json.Unmarshal([]byte(payloadJSON), &p), "SETUP: the payload's wire shape decodes")
	h.hubSyncTap(agent.Event{
		Kind:    agent.EventKindProviderStatus,
		Meta:    agent.EventMeta{TurnID: "turn-1664-b3", SessionKey: "chat-pm-1"},
		Payload: p,
	})
	require.Len(t, ch, 1, "one provider_status frame per waiting event")
	raw := <-ch
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Equal(t, "waiting", m["state"], "SETUP: the waiting frame is on the wire")
	require.Equal(t, float64(1), m["attempt"],
		"#1664 B3: the waiting frame carries the 1-based attempt so the count is visible from the first notice")
	require.Equal(t, float64(3), m["max_attempts"],
		"#1664 B3: founder Q1 A keeps 3 attempts; the frame shows the cap")
}

// B5: the loop's timeout retry reaches the chat as ONE provider_retry frame:
// error_code no_response, attempt = the next call (2..3), max_attempts 3,
// retry_at = the end of the backoff. Today websocket_forward_hub.go routes
// EventKindLLMRetry to hubContextWindowNotice, which sends nothing — the
// empty channel is the RED. The payload mirrors the loop's post-fix
// emission: the same named fields the #1280 path already forwards
// (the RetryInfo convention: attempt = the call about to be made,
// pkg/providers/fallback_retry.go).
func TestIssue1664_B5_TimeoutRetryBecomesNoResponseRetryFrame(t *testing.T) {
	h, ch := pmHub(t)
	sentAt := time.Date(2026, 11, 7, 14, 0, 30, 0, time.UTC)
	retryAt := sentAt.Add(2 * time.Second)
	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindLLMRetry,
		Meta: agent.EventMeta{TurnID: "turn-1664-b5", SessionKey: "chat-pm-1"},
		Payload: agent.LLMRetryPayload{
			Attempt: 2, MaxAttempts: 3, MaxRetries: 2,
			Reason: "timeout", Provider: "p", Model: "m",
			SentAt: sentAt, RetryAt: retryAt, SessionID: pmSessionID,
		},
	})
	require.Len(t, ch, 1,
		"#1664 B5: the timeout retry must reach the chat as one provider_retry frame; today the hub routes the loop's LLMRetry to hubContextWindowNotice and the backoff gap shows nothing")
	raw := <-ch
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Equal(t, "provider_retry", m["type"])
	require.Equal(t, "no_response", m["error_code"],
		"#1664 B5: a header timeout (or a stall with nothing streamed yet) is error_code no_response")
	require.Equal(t, float64(2), m["attempt"], "#1664 B5: the line counts the next call (attempt 2 of 3)")
	require.Equal(t, float64(3), m["max_attempts"], "#1664 B5: founder Q1 A keeps 3 attempts")
	require.Equal(t, pmJSONTime(t, retryAt), m["retry_at"], "#1664 B5: retry_at is the end of the backoff")
}

// TestIssue1664_B5_EmptyReplyRetryBecomesEmptyReplyRetryFrame — CHECK F5
// (round 3): the existing B5 wire test feeds only Reason "timeout". The
// empty stream's retry must take the same route WITHOUT losing its own
// cause: reason empty_reply → error_code empty_reply on the frame (the
// cause founder Q4 A's retry line renders), with the next call, the cap and
// the backoff's end and the transcript-session address. The routing key
// deliberately has no chat binding: the payload's transcript identity must
// reach the wire without relying on the routing-address fallback.
func TestIssue1664_B5_EmptyReplyRetryBecomesEmptyReplyRetryFrame(t *testing.T) {
	h, ch := pmHub(t)
	sentAt := time.Date(2026, 11, 8, 9, 0, 30, 0, time.UTC)
	retryAt := sentAt.Add(2 * time.Second)
	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindLLMRetry,
		Meta: agent.EventMeta{TurnID: "turn-1664-b5-empty", SessionKey: "unbound-routing-key-1664"},
		Payload: agent.LLMRetryPayload{
			Attempt: 2, MaxAttempts: 3, MaxRetries: 2,
			Reason: "empty_reply", Provider: "p", Model: "m",
			SentAt: sentAt, RetryAt: retryAt, SessionID: pmSessionID,
		},
	})
	require.Len(t, ch, 1,
		"#1664 F5: the empty_reply retry must reach the chat as one provider_retry frame")
	raw := <-ch
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Equal(t, "provider_retry", m["type"])
	require.Equal(t, "empty_reply", m["error_code"],
		"#1664 F5: the empty stream's retry keeps its OWN cause on the wire — never collapsed into no_response")
	require.Equal(t, float64(2), m["attempt"], "#1664 F5: the line counts the next call (attempt 2 of 3)")
	require.Equal(t, float64(3), m["max_attempts"], "#1664 F5: founder Q1 A keeps 3 attempts")
	require.Equal(t, pmJSONTime(t, retryAt), m["retry_at"], "#1664 F5: retry_at is the end of the backoff")
	require.Equal(t, pmJSONTime(t, sentAt), m["sent_at"], "#1664 F5: retain the frame's server-clock emission time")
	require.Equal(t, float64(2), m["retry_after_seconds"], "the frame's countdown agrees with its two-second backoff")
	require.Equal(t, pmSessionID, m["session_id"], "#1664 F5 (ADR-057): address the transcript session, never the routing key")
	require.Equal(t, "turn-1664-b5-empty", m["turn_id"])
	require.Equal(t, "p", m["provider"])
	require.Equal(t, "m", m["model"])
}

// warnCapture records WARN records through slog's default logger while a
// test runs, so a logsafeWarn call can be asserted exactly (message and the
// full attribute set). Installed per test, restored via t.Cleanup; gateway
// tests here run sequentially, never t.Parallel.
type warnCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

type warnCaptureHandler struct{ c *warnCapture }

func (h warnCaptureHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

func (h warnCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level != slog.LevelWarn {
		return nil
	}
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	h.c.records = append(h.c.records, r.Clone())
	return nil
}

func (h warnCaptureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h warnCaptureHandler) WithGroup(_ string) slog.Handler      { return h }

func captureWarnings(t *testing.T) *warnCapture {
	t.Helper()
	c := &warnCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(warnCaptureHandler{c}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

func (c *warnCapture) warns() []slog.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]slog.Record(nil), c.records...)
}

// warnAttrs flattens one record into (message, exact attribute map).
func warnAttrs(r slog.Record) (string, map[string]any) {
	m := map[string]any{}
	r.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.Any()
		return true
	})
	return r.Message, m
}

// B11: hubProviderStatus logs ONE logsafeWarn when the payload is not
// agent.ProviderStatusPayload — message exactly "ws: provider_status
// dropped: unexpected payload type", fields payload_type (%T), turn_id,
// session_key — and publishes no frame. Today it returns silently
// (websocket_forward_provider_status.go::hubProviderStatus).
func TestIssue1664_B11_DroppedProviderStatusLogsUnexpectedPayloadType(t *testing.T) {
	h, ch := pmHub(t)
	captured := captureWarnings(t)
	wrong := agent.LLMRetryPayload{}
	h.hubSyncTap(agent.Event{
		Kind:    agent.EventKindProviderStatus,
		Meta:    agent.EventMeta{TurnID: "turn-1664-b11", SessionKey: "chat-pm-1"},
		Payload: wrong,
	})
	pmAssertNoFrame(t, ch)
	warns := captured.warns()
	require.Len(t, warns, 1,
		"#1664 B11: exactly one warning for the dropped provider_status; today the drop is silent")
	msg, attrs := warnAttrs(warns[0])
	require.Equal(t, "ws: provider_status dropped: unexpected payload type", msg)
	require.Equal(t, fmt.Sprintf("%T", wrong), attrs["payload_type"], "B11: payload_type is the %T of the wrong payload")
	require.Equal(t, "turn-1664-b11", attrs["turn_id"], "B11: turn_id")
	require.Equal(t, "chat-pm-1", attrs["session_key"], "B11: session_key")
	require.Len(t, attrs, 3, "B11: exactly the three specified fields, nothing else (never provider text)")
}

// B12: hubProviderStatus logs ONE logsafeWarn when no session id can be
// found — message exactly "ws: provider_status dropped: no session for
// chat", fields state, turn_id, session_key; no frame is published, and the
// log never contains provider text (the attribute set is exactly the three
// specified fields, so none of the payload's times or other contents can
// leak). Today it returns silently.
func TestIssue1664_B12_DroppedProviderStatusWithoutSessionLogsNoSessionForChat(t *testing.T) {
	h, ch := pmHub(t)
	captured := captureWarnings(t)
	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindProviderStatus,
		Meta: agent.EventMeta{TurnID: "turn-1664-b12", SessionKey: "unbound-chat"},
		Payload: agent.ProviderStatusPayload{
			State:       agent.ProviderStatusWaiting,
			SentAt:      time.Date(2026, 11, 7, 14, 1, 0, 0, time.UTC),
			SilentSince: time.Date(2026, 11, 7, 14, 0, 29, 0, time.UTC),
		},
	})
	pmAssertNoFrame(t, ch)
	warns := captured.warns()
	require.Len(t, warns, 1, "#1664 B12: exactly one warning; today the drop is silent")
	msg, attrs := warnAttrs(warns[0])
	require.Equal(t, "ws: provider_status dropped: no session for chat", msg)
	require.Equal(t, agent.ProviderStatusWaiting, attrs["state"],
		"B12: the state is on the log, nothing else from the payload")
	require.Equal(t, "turn-1664-b12", attrs["turn_id"], "B12: turn_id")
	require.Equal(t, "unbound-chat", attrs["session_key"], "B12: session_key")
	require.Len(t, attrs, 3,
		"B12: exactly the three specified fields — the payload's times and any provider text never reach the log")
}
