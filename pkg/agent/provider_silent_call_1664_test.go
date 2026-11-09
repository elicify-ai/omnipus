package agent

// Issue #1664 — RED tests for the silent model call and the clean empty
// stream. Oracles: /workspace/logs/fix-1664/verify-report.md §"Proposed
// behaviour" B1–B12 plus the founder decisions of 2026-11-07 (Q1 A: keep 3
// attempts and today's per-attempt wait; make "attempt N of 3" and each
// attempt's end time visible. Q2: the B4/B5/B10 texts exactly as written.
// Q3 A: an empty stream follows the silent-call rule — 3 attempts, a
// fallback model tried when configured). No expected value below was read
// off a run of the code under test; where a field does not exist yet the
// test says so and fails loudly instead of skipping.
//
// Harness: the #1478/#1485 patterns (provider_status_1478_test.go,
// provider_attempt_timeout_1485_test.go) — the wu22 harness with a scripted
// provider, the silence seams shortened (issue1478ShortSilence), and the
// fallback chain's own wait refused so no test sits in the chain's backoff.
// The entry point is the REAL response stage (callLLMWithRetries), so the
// loop's own timeout recovery — retryTimeout, the backoff, the retry
// events — is exercised, not bypassed. That backoff (base 2 s, full jitter,
// 500 ms floor) stays real; the tests accept its sub-6-second wall time and
// never a 120 s guard.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// issue1664StreamDelegate routes the loop's streaming gate to the harness's
// own streamer, so a StreamingProvider attempt actually streams (the gate
// consults bus.GetStreamer, not the turn's lastStreamer field).
type issue1664StreamDelegate struct{ s bus.Streamer }

func (d issue1664StreamDelegate) GetStreamer(context.Context, string, string, string) (bus.Streamer, bool) {
	return d.s, true
}

// issue1664CallWithHarness runs one response-stage round (callLLMWithRetries)
// on the wu22 harness with the given provider in the pool — issue1478Call's
// shape, generalized: onHarness runs after the harness exists and before the
// call. The round's final error is the response stage's rf.err.
func issue1664CallWithHarness(t *testing.T, provider providers.LLMProvider, onHarness func(*wu22Harness)) (*wu22Harness, time.Time, error) {
	t.Helper()
	h := wu22NewHarness(t, provider)
	candidates := []providers.FallbackCandidate{{Provider: "p", Model: "m"}}
	h.agent.Model, h.agent.Candidates = "m", candidates
	h.agent.StoreProviderPool(map[string]providers.LLMProvider{"p": provider})
	// Production chain shape; the wait is refused so tests never sit in the
	// chain's own backoff. The loop's timeout backoff below stays real.
	h.al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker()).
		WithRetryWait(func(ctx context.Context, _ time.Duration) error { return ctx.Err() })
	if onHarness != nil {
		onHarness(h)
	}
	rr := h.responseStage()
	rt := rr.rq.ri.rf.rt
	rt.activeCandidates, rt.activeProvider, rt.llmModel = candidates, provider, "m"
	// Seed the request the loop's first attempt sends (the retry loop reads
	// the fallbacks' callMessages field at every attempt).
	rr.rq.ri.rf.callMessages = []providers.Message{{Role: "user", Content: "hello"}}
	rr.rq.ri.rf.providerToolDefs = nil
	callStart := time.Now()
	rr.callLLMWithRetries()
	return h, callStart, rr.rq.ri.rf.err
}

func issue1664Call(t *testing.T, provider providers.LLMProvider) (*wu22Harness, time.Time, error) {
	t.Helper()
	return issue1664CallWithHarness(t, provider, nil)
}

// issue1664CallCount reads the scripted provider's call counter.
func issue1664CallCount(p *issue1478Provider) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// issue1664RetryEvents returns the loop's EventKindLLMRetry payloads in order.
func issue1664RetryEvents(h *wu22Harness) []LLMRetryPayload {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []LLMRetryPayload
	for _, e := range h.events {
		if e.Kind == EventKindLLMRetry {
			if p, ok := e.Payload.(LLMRetryPayload); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// issue1664TimeoutReporter is the scripted #1478 provider plus the
// operator's configured request_timeout (providers.RequestTimeoutReporter —
// the #1485 fake's shape), so the attempt's guard is the configured value.
type issue1664TimeoutReporter struct {
	*issue1478Provider
	explicit time.Duration
}

func (p *issue1664TimeoutReporter) ExplicitRequestTimeout() time.Duration { return p.explicit }

// B1: a waiting frame emitted before the first response byte carries
// stall_at = attempt start + guard, from the operator's configured
// request_timeout (7 s below) and never the 120 s constant
// (verify-report §B1). Today the watch emits a zero StallAt before the
// stream's own silence limit is known (provider_status.go::
// providerSilenceWatch.run) — that zero is what this test fails on.
func TestIssue1664_B1_WaitingNoticeCarriesConfiguredGuardEndTime(t *testing.T) {
	after := issue1478ShortSilence(t)
	const guard = 7 * time.Second // the operator's configured request_timeout
	provider := &issue1664TimeoutReporter{
		issue1478Provider: &issue1478Provider{script: func(_ context.Context, _ int) (*providers.LLMResponse, error) {
			time.Sleep(3 * after) // a call that says nothing for a while
			return &providers.LLMResponse{Content: "late"}, nil
		}},
		explicit: guard,
	}
	h, start, err := issue1664Call(t, provider)
	require.NoError(t, err)
	got := issue1478Statuses(h)
	require.NotEmpty(t, got, "the silent call must produce the waiting notice")
	require.Equal(t, ProviderStatusWaiting, got[0].State)
	// Tolerance is harness-setup latency (response-stage entry before the
	// watch starts), not clock slop; the B1 oracle is the exact equality
	// below, which this sanity check only anchors to THIS attempt.
	require.WithinDuration(t, start, got[0].SilentSince, 2*time.Second,
		"nothing arrived, so the silence is counted from the attempt start")
	require.Equal(t, guard, got[0].StallAt.Sub(got[0].SilentSince),
		"#1664 B1: the pre-first-byte notice must carry the attempt's end time = attempt start + the CONFIGURED request_timeout (7 s), never zero and never the 120 s constant")
}

// B3 (founder Q1 A): every waiting notice carries the 1-based attempt number
// and the attempt cap (today 3), visible from the FIRST notice. Both fields
// are new contract fields; until the developer adds them this test fails
// loudly naming them (never a skip).
func TestIssue1664_B3_WaitingNoticeCarriesAttemptCount(t *testing.T) {
	after := issue1478ShortSilence(t)
	provider := &issue1478Provider{script: func(_ context.Context, _ int) (*providers.LLMResponse, error) {
		time.Sleep(3 * after)
		return &providers.LLMResponse{Content: "late"}, nil
	}}
	h, _, err := issue1664Call(t, provider)
	require.NoError(t, err)
	got := issue1478Statuses(h)
	require.NotEmpty(t, got, "the silent call must produce the waiting notice")
	require.Equal(t, ProviderStatusWaiting, got[0].State)

	attempt := reflect.ValueOf(got[0]).FieldByName("Attempt")
	maxAttempts := reflect.ValueOf(got[0]).FieldByName("MaxAttempts")
	if !attempt.IsValid() || !maxAttempts.IsValid() {
		t.Fatalf("RED (missing contract fields): ProviderStatusPayload has no Attempt/MaxAttempts field (Attempt valid=%v, MaxAttempts valid=%v) — required by verify-report §B3 and founder Q1 A (2026-11-07): every waiting notice shows attempt N of 3; add the fields and set them (attempt 1 here, cap 3)",
			attempt.IsValid(), maxAttempts.IsValid())
	}
	require.Equal(t, int64(1), attempt.Int(), "B3: the first notice counts attempt 1 (1-based)")
	require.Equal(t, int64(3), maxAttempts.Int(),
		"B3: founder Q1 A keeps 3 attempts; the cap rides the first notice already")
}

// B5: the loop's timeout retry must carry the facts the provider_retry frame
// renders — the NEXT call's number (the repo's own RetryInfo convention,
// pkg/providers/fallback_retry.go: "the call about to be made (2..3)"), the
// attempt cap 3 (founder Q1 A), and retry_at = the end of the backoff.
// Today the timeout producer counts the FAILED call (Attempt 1 after the
// first failure) and leaves RetryAt/SentAt/MaxAttempts zero — and the hub
// drops the event entirely (covered gateway-side in
// provider_status_1664_test.go).
func TestIssue1664_B5_TimeoutRetryCarriesNextCallMaxAttemptsAndRetryAt(t *testing.T) {
	issue1478ShortSilence(t)
	provider := &issue1478Provider{script: func(_ context.Context, _ int) (*providers.LLMResponse, error) {
		return nil, context.DeadlineExceeded
	}}
	h, _, _ := issue1664Call(t, provider) // the exhausted round ends in error; the retry events are the oracle
	retries := issue1664RetryEvents(h)
	require.Len(t, retries, 2, "two timeout retries (attempts 2 and 3), got %d", len(retries))

	first := retries[0]
	require.Equal(t, 2, first.Attempt,
		"#1664 B5: the retry line counts the call ABOUT to be made — attempt 2 after the first call failed (the RetryInfo convention); today it counts the failed call (1)")
	require.Equal(t, 3, first.MaxAttempts,
		"#1664 B5: founder Q1 A keeps 3 attempts; the retry line shows the cap (today it carries MaxRetries=2)")
	require.Equal(t, "timeout", first.Reason,
		"the timeout cause is what the frame maps to error_code no_response")
	require.False(t, first.SentAt.IsZero(), "B5: the frame's sent_at needs the emission time (today it is zero)")
	require.Equal(t, first.Backoff, first.RetryAt.Sub(first.SentAt),
		"#1664 B5: retry_at is the END of this backoff (emission + backoff); today RetryAt is zero")
	// The unchanged backoff rule (B6 keeps it): base 2 s full jitter with the
	// 500 ms floor, so the first retry's wait lands in [0.5 s, 2 s].
	require.GreaterOrEqual(t, first.Backoff, 500*time.Millisecond)
	require.LessOrEqual(t, first.Backoff, 2*time.Second)
	require.Equal(t, 3, retries[1].Attempt, "B5: attempt 3 of 3 on the second retry")
}

// B6 (pin, passes today — characterization test): the policy founder Q1 A
// kept — exactly 3 calls for a silent call — and the existing turn_timed_out
// verdict after the last attempt. This test locks the baseline so the
// visibility work cannot silently change it. The channel-side single
// "Retrying — please wait..." notice is already pinned by
// pkg/gateway/webchat_progress_notice_test.go (progressNoticeText).
func TestIssue1664_B6_Pin_ThreeAttemptsEndInTurnTimedOut(t *testing.T) {
	issue1478ShortSilence(t)
	provider := &issue1478Provider{script: func(_ context.Context, _ int) (*providers.LLMResponse, error) {
		return nil, context.DeadlineExceeded
	}}
	h, _, err := issue1664Call(t, provider)
	require.Error(t, err, "a call that never answers must end the round in error")
	require.Equal(t, 3, issue1664CallCount(provider),
		"B6 pin: exactly 3 calls for a silent call (founder Q1 A kept the policy); got %d", issue1664CallCount(provider))
	require.ErrorIs(t, err, context.DeadlineExceeded, "the raw timeout cause survives the retries")
	require.Equal(t, "turn_timed_out", string(TranslateTurnError(err).Code),
		"B6 pin: the existing verdict after the last attempt is turn_timed_out")
	require.Len(t, issue1664RetryEvents(h), 2, "one retry line per retry (2), never one per attempt")
}

// B7 (pin, passes today): a Stop during the silent wait ends the round with
// no further attempt and no retry line. The Stop lands mid-call; the
// hardAbortRequested guard at the top of the loop's timeout recovery
// (loop_run_turn_response.go::retryTimeout) breaks before the TurnTimeout
// and LLMRetry events are emitted — that guard is what this pin exercises.
// The turn-level turn_canceled outcome of a hard stop is covered by
// hard_abort_finish_method_test.go
// (TestRunTurn_HardAbortViaInterruptSessionHard_RecordsCancelMethodHard).
func TestIssue1664_B7_Pin_StopDuringSilentWaitMakesNoFurtherAttempt(t *testing.T) {
	issue1478ShortSilence(t)
	holder := &struct{ h *wu22Harness }{}
	provider := &issue1478Provider{script: func(_ context.Context, call int) (*providers.LLMResponse, error) {
		if call == 1 {
			if holder.h != nil {
				holder.h.ts.requestHardAbort() // the Stop arrives mid-call
			}
			return nil, context.DeadlineExceeded // timeout-class: only the guard can stop the retry
		}
		return nil, context.DeadlineExceeded
	}}
	h, _, err := issue1664CallWithHarness(t, provider, func(h *wu22Harness) { holder.h = h })
	// The error's identity is not B7's oracle: with a hard abort in flight
	// the turn finalization maps the failure to its cancel outcome (covered
	// by the cited hard-abort test). B7's oracle is what the Stop prevented.
	require.Error(t, err, "the stopped round must not return success")
	require.Equal(t, 1, issue1664CallCount(provider),
		"B7 pin: the Stop ends the round — the hardAbortRequested guard allows no further attempt; got %d", issue1664CallCount(provider))
	require.Empty(t, issue1664RetryEvents(h),
		"B7 pin: no retry line follows a Stop (the guard breaks before the retry event)")
}

// B10 (founder Q3 A + Q2): a clean empty stream is NOT a success. The loop's
// STREAMING path — the real OpenAI-compatible transport against a server
// answering only [DONE] — must spend the silent-call attempt rule (3 calls,
// founder Q3 A) and end in the new provider_empty_reply code — attribution
// provider, retryable — never in the fixed "The model returned an empty
// response…" sentence. Today the empty stream parses as a successful empty
// answer (parseStreamResponse synthesizes finish_reason "stop" with nil
// error), so the response stage returns success and the fixed text is what
// the user gets.
func TestIssue1664_B10_EmptyStreamEndsInProviderEmptyReply(t *testing.T) {
	issue1478ShortSilence(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	provider, err := providers.NewHTTPProviderWithMaxTokensFieldAndRequestTimeout("k", server.URL, "", "", 0, nil)
	require.NoError(t, err, "SETUP: real transport provider")
	h, _, callErr := issue1664CallWithHarness(t, provider, func(h *wu22Harness) {
		h.al.bus.SetStreamDelegate(issue1664StreamDelegate{s: h.stream}) // the loop takes the streaming path
	})
	require.Error(t, callErr,
		"#1664 B8/B10: a clean empty stream must not be returned as success — today parseStreamResponse returns finish_reason \"stop\" with nil error and the fixed empty-response text answers the user")
	require.Equal(t, int32(3), calls.Load(),
		"#1664 B9/B10 (founder Q3 A): the empty stream follows the silent-call rule — 3 attempts; today only %d call happens and the empty answer is treated as success", calls.Load())
	translated := TranslateTurnError(callErr)
	require.Equal(t, "provider_empty_reply", string(translated.Code),
		"#1664 B10: the used-up attempts end in the new provider_empty_reply code")
	require.True(t, translated.Retryable, "#1664 B10: the code is retryable")
	require.Equal(t, generated.LLMErrorAttributionProvider, AttributionForCode(translated.Code),
		"#1664 B10: attribution provider")
	for _, finalText := range h.stream.finalized {
		require.NotContains(t, finalText, "The model returned an empty response",
			"#1664 B10: the fixed empty-response sentence is not used for this case")
	}
}

// TestIssue1664_B5_EmptyStreamRetryCarriesEmptyReplyReasonAndTranscriptSession —
// CHECK F5 (round 3): the empty stream's own retry line. B10 proved the
// empty stream gets the 3-attempt rule but never asserted its RETRY PAYLOAD:
// the reason must be empty_reply (what the gateway maps to error_code
// empty_reply and what founder Q4 A's sentence renders), the attempt is the
// next call (2), the cap is 3, the timing carries sent_at and
// retry_at = sent_at + backoff, and the session is the TRANSCRIPT session
// id — the ADR-057 consumer-set guard (loop.go::u9ToolExecSessionIDs), so
// the frame lands in the right chat even when the routing key differs.
func TestIssue1664_B5_EmptyStreamRetryCarriesEmptyReplyReasonAndTranscriptSession(t *testing.T) {
	issue1478ShortSilence(t)
	provider := &issue1478Provider{script: func(_ context.Context, _ int) (*providers.LLMResponse, error) {
		return nil, common.NewEmptyStreamError(nil)
	}}
	h, _, _ := issue1664CallWithHarness(t, provider, func(h *wu22Harness) {
		// Distinct on purpose: stamp the transcript session, not a
		// cancellation/routing address (ADR-057 consumer-set identity).
		h.ts.sessionKey = "routing-key-not-the-transcript-session"
	}) // the exhausted round ends in error; the retry events are the oracle
	retries := issue1664RetryEvents(h)
	require.Len(t, retries, 2,
		"the empty stream follows the silent-call rule (founder Q3 A): 3 attempts, 2 retry lines")
	require.NotEqual(t, h.sid, h.ts.sessionKey, "SETUP: transcript and routing identities must differ")

	first := retries[0]
	require.Equal(t, "empty_reply", first.Reason,
		"#1664 F5: the empty stream's retry line carries reason empty_reply — its own cause, never the timeout spelling")
	require.Equal(t, 2, first.Attempt,
		"#1664 F5: the retry line counts the call ABOUT to be made (the RetryInfo convention)")
	require.Equal(t, 3, first.MaxAttempts,
		"#1664 F5: founder Q1 A keeps 3 attempts; the line shows the cap")
	require.False(t, first.SentAt.IsZero(), "#1664 F5: sent_at is set (the frame's emission time)")
	require.Equal(t, first.Backoff, first.RetryAt.Sub(first.SentAt),
		"#1664 F5: retry_at is the END of this backoff")
	require.GreaterOrEqual(t, first.Backoff, 500*time.Millisecond)
	require.LessOrEqual(t, first.Backoff, 2*time.Second)
	require.Equal(t, h.sid, first.SessionID,
		"#1664 F5 (ADR-057): the retry payload is stamped with the TRANSCRIPT session id, not the routing key and never empty")
	require.Equal(t, "empty_reply", retries[1].Reason)
	require.Equal(t, 3, retries[1].Attempt, "the second retry announces the next call, attempt 3")
	require.Equal(t, 3, retries[1].MaxAttempts)
	require.Equal(t, h.sid, retries[1].SessionID, "the later retry also uses the transcript session")
	require.False(t, retries[1].SentAt.IsZero())
	require.Equal(t, retries[1].Backoff, retries[1].RetryAt.Sub(retries[1].SentAt))
}

// TestIssue1664_B7_StopDuringBackoffAfterTheRetryFrameEndsTurnCanceled —
// CHECK F5 (round 3): B7 stopped the call INSIDE the first provider call;
// the retry frame had never been emitted. This is the other half of the
// Stop story: the first attempt fails, the retry frame IS out, the loop
// sits in its real backoff — and NOW the user presses Stop. The turn ends
// CANCELED (a turn_canceled transcript entry, same record the hard-abort
// finish-method test asserts), no second provider call happens, and no
// second retry frame follows. Full-turn harness (ProcessScheduled), since
// the outcome record belongs to the turn level, not the response stage.
func TestIssue1664_B7_StopDuringBackoffAfterTheRetryFrameEndsTurnCanceled(t *testing.T) {
	issue1478ShortSilence(t)
	provider := &issue1478Provider{script: func(_ context.Context, _ int) (*providers.LLMResponse, error) {
		return nil, context.DeadlineExceeded
	}}
	al, inst, store, sid := newTruncationTestHarness(t, provider, 4, 0)
	candidates := []providers.FallbackCandidate{{Provider: "p", Model: "m"}}
	inst.Model, inst.Candidates = "m", candidates
	inst.StoreProviderPool(map[string]providers.LLMProvider{"p": provider})
	// Production chain shape; the CHAIN's own backoff is refused so the only
	// wait in play is the loop's real timeout backoff — the one the Stop
	// must land inside. The loop's backoff stays real (0.5–2 s).
	al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker()).
		WithRetryWait(func(ctx context.Context, _ time.Duration) error { return ctx.Err() })

	var mu sync.Mutex
	var retryFrames []LLMRetryPayload
	var terminalErrorCodes []string
	retrySeen := make(chan struct{}, 4)
	al.SetEventSyncTap(func(e Event) {
		if e.Kind == EventKindError {
			if p, ok := e.Payload.(ErrorPayload); ok {
				mu.Lock()
				terminalErrorCodes = append(terminalErrorCodes, p.Code)
				mu.Unlock()
			}
		}
		if e.Kind != EventKindLLMRetry {
			return
		}
		if p, ok := e.Payload.(LLMRetryPayload); ok {
			mu.Lock()
			retryFrames = append(retryFrames, p)
			mu.Unlock()
		}
		select {
		case retrySeen <- struct{}{}:
		default:
		}
	})
	t.Cleanup(func() { al.SetEventSyncTap(nil) })

	type outcome struct{ err error }
	resultCh := make(chan outcome, 1)
	go func() {
		_, runErr := al.ProcessScheduled(context.Background(), inst.ID, sid, "please answer", "webchat", sid)
		resultCh <- outcome{runErr}
	}()

	// Observe the announced retry before requesting Stop. Guard the setup:
	// a delayed test goroutine must not accidentally test a later call.
	select {
	case <-retrySeen:
	case <-time.After(5 * time.Second):
		t.Fatal("the first retry frame never appeared — the backoff this test must interrupt never started")
	}
	mu.Lock()
	firstRetry := retryFrames[0]
	mu.Unlock()
	require.Equal(t, 1, issue1664CallCount(provider), "SETUP: Stop must precede the announced second call")
	require.True(t, time.Now().Before(firstRetry.RetryAt), "SETUP: the announced retry backoff must still be pending")
	cancelOutcome, cancelErr := al.RequestCancel(context.Background(),
		CancelScope{SessionID: sid},
		CancelCanceller{UserID: "test-user", Channel: "web"},
		CancelHooks{})
	require.NoError(t, cancelErr)
	require.True(t, cancelOutcome.Fired, "the Stop must claim the in-flight turn")

	var final outcome
	select {
	case final = <-resultCh:
	case <-time.After(15 * time.Second):
		t.Fatal("the turn did not end after the Stop during the backoff")
	}

	mu.Lock()
	frames := append([]LLMRetryPayload(nil), retryFrames...)
	errorCodes := append([]string(nil), terminalErrorCodes...)
	mu.Unlock()
	// Separate subtests retain each assertion and report every promised
	// outcome even when the first call-count assertion is already RED.
	t.Run("no later provider call", func(t *testing.T) {
		require.Equal(t, 1, issue1664CallCount(provider),
			"#1664 F5: the Stop during the backoff allows no further provider call; got %d", issue1664CallCount(provider))
	})
	t.Run("no later retry frame", func(t *testing.T) {
		require.Len(t, frames, 1,
			"#1664 F5: exactly one retry frame — the one emitted BEFORE the Stop; no later frame follows")
	})
	t.Run("turn_canceled outcome", func(t *testing.T) {
		// A cancellation marker alone must not hide a later timeout error.
		// Clean abort may return nil; any returned/emitted failure must be
		// the cancellation verdict, never a provider-failure verdict (B7).
		if final.err != nil {
			assert.Equal(t, "turn_canceled", string(TranslateTurnError(final.err).Code), "Stop wins over provider failure in the final result")
		}
		for _, code := range errorCodes {
			assert.Equal(t, "turn_canceled", code, "no later timeout/empty-provider error may replace the cancellation outcome")
		}
		// Same durable outcome the hard-abort finish-method test asserts.
		require.Eventually(t, func() bool {
			entries, err := store.ReadTranscript(sid)
			if err != nil {
				return false
			}
			for i := range entries {
				if entries[i].Type == session.EntryTypeTurnCancelled {
					return true
				}
			}
			return false
		}, 5*time.Second, 20*time.Millisecond,
			"#1664 F5: the Stop during the backoff ends the turn CANCELED — a turn_canceled transcript entry")
	})
}

// issue1664StreamingChatProvider adapts the external provider edge: the
// multi-candidate loop calls Chat rather than ChatStream, so this test
// provider obtains each answer with the real HTTP streaming parser. It
// returns neither a scripted sentinel nor a canned answer to the loop.
// The fallback chain, retry logic and transcript writer remain unmodified.
// This does not claim the production multi-candidate path itself streams.
type issue1664StreamingChatProvider struct{ *providers.HTTPProvider }

func (p *issue1664StreamingChatProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	return p.HTTPProvider.ChatStream(ctx, messages, tools, model, options, nil, nil)
}

// F5: drive an actual primary empty stream through the entire turn, not
// just ClassifyError or a scripted FallbackChain callback. Both choices
// reach the real HTTP streaming parser through the provider-edge adapter
// above. The configured backup must supply the final answer and the
// successful assistant transcript entry.
func TestIssue1664_B9_PrimaryEmptyStreamBackupAnswersTheTurn(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("SETUP: decode provider request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		model, _ := request["model"].(string)
		assert.Equal(t, true, request["stream"], "both attempts must take the real streaming path")
		mu.Lock()
		calls = append(calls, model)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		switch model {
		case "primary-model":
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		case "backup-model":
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"backup answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
		default:
			t.Errorf("unexpected candidate model %q", model)
		}
	}))
	defer server.Close()
	provider, err := providers.NewHTTPProviderWithMaxTokensFieldAndRequestTimeout("k", server.URL, "", "", 0, nil)
	require.NoError(t, err)
	streamingProvider := &issue1664StreamingChatProvider{HTTPProvider: provider}
	al, inst, store, sid := newTruncationTestHarness(t, streamingProvider, 4, 0)
	inst.Model = "primary-model"
	inst.Candidates = []providers.FallbackCandidate{
		{Provider: "p", Model: "primary-model"},
		{Provider: "p", Model: "backup-model"},
	}
	inst.StoreProviderPool(map[string]providers.LLMProvider{"p": streamingProvider})
	// No transport streamer is advertised: the real non-streaming finalizer
	// must persist the backup reply obtained through the provider-edge adapter.

	answer, runErr := al.ProcessScheduled(context.Background(), inst.ID, sid, "answer using the configured model", "webchat", sid)
	require.NoError(t, runErr, "F5/Q3 A: an empty primary stream must allow the configured backup to complete the turn")
	require.Equal(t, "backup answer", answer, "the successful turn returns the backup's actual reply")
	mu.Lock()
	gotCalls := append([]string(nil), calls...)
	mu.Unlock()
	require.Equal(t, []string{"primary-model", "backup-model"}, gotCalls,
		"invoke the configured primary then the configured backup exactly once each")
	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	last := lastTruncationAssistantEntry(entries)
	require.NotNil(t, last, "a successful assistant reply must be durable")
	require.Equal(t, "backup answer", last.Content)
}
