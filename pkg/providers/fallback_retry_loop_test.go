/**
 * fallback_retry_loop_test.go — provider-messages spec RED tests:
 * TDD rows 8 (A-1/A-3/A-4 + chain rows), 9 (RG-? → C-12 Stop), 10a (C-16
 * identity, routing side), 23 (MAJ-101 verbatim chain rows), 24 (D14 BLOCKED
 * seam), 26 (chain half: 404 falls back, D12).
 *
 * Traces: A-1, A-3, A-4, MAJ-101, C-8, C-9, C-12, C-16, D12, D14; datasets
 * D1 (honored wait) and B rows of §10.
 *
 * Oracles come from the SPEC ONLY:
 *   - C-8: max_attempts = 3 TOTAL calls including the first, attempt numbers
 *     2..3; the candidate is marked failed ONCE after the third failure.
 *   - C-9: retry-after ≤ 120 s is honored (the chain waits exactly that long,
 *     first); over-ceiling (> 120 s) → no wait, straight to fallback, NO
 *     cooldown mark for the cap-skipped candidate; absent/malformed/past →
 *     the standard backoff schedule (2 s × 2^n capped at 30 s, ±25% jitter);
 *     D14: total wait per turn ≤ 10 min, waits never truncated.
 *   - C-12: the wait is context-cancellable; Stop ends the wait ≤ 1 s and no
 *     further attempt is made.
 *   - MAJ-101 (row 23): the retry loop lives INSIDE the real
 *     FallbackChain.Execute — these tests drive the real chain, not a stub
 *     (the spec is explicit that a stubbed chain cannot prove MAJ-101).
 *   - C-16 (row 10a routing half): the failing attempt's identity (provider +
 *     model) is what surfaces — the Fallback-exhaustion error names the
 *     candidate that actually failed last.
 *   - D12 (row 26 chain half): a 404 retirement verdict routes FailoverUnknown
 *     (retriable) so the configured Fallback model answers.
 *
 * Facts are fabricated through the REAL HTTP boundary — errors are built with
 * common.HandleErrorResponse from a fabricated response (status + headers +
 * body) — so retry-after facts flow into the chain exactly as production
 * (HandleErrorResponse → FallbackChain.Execute), never through test-side
 * field writes of facts the parse layer is supposed to produce.
 *
 * RED status (this branch, pre-GREEN): rows A-1/A-3/A-4/C-8/row 23/C-12/D12
 * are ASSERTION-RED (the in-chain retry loop does not exist — call counts and
 * wait times are wrong). The billing chain row and the C-16 identity row are
 * regression PINS (currently green — they exist to catch GREEN drifting the
 * no-retry-for-billing rule and the attempt-identity surface while it builds
 * the loop). Row 24 is compile-safe BLOCKED-pattern (see its comment).
 */

package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// mkResp builds the *http.Response HandleErrorResponse consumes.
func mkResp(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	header.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// peFromBoundary builds the error a real provider HTTP boundary produces via
// the REAL parse path, so retry-after facts reach the chain as fields exactly
// as production does. (This file deliberately does NOT set the fact fields by
// hand — the parse layer owns them, §7.2.)
func peFromBoundary(t *testing.T, status int, header http.Header, body string) error {
	t.Helper()
	err := common.HandleErrorResponse(mkResp(status, header, body), "https://api.example.com")
	if err == nil {
		t.Fatalf("HandleErrorResponse returned nil for status %d", status)
	}
	return err
}

func rateLimited(t *testing.T, retryAfter string) error {
	return peFromBoundary(t, 429,
		http.Header{"Retry-After": []string{retryAfter}},
		`{"error":{"message":"rate limited"}}`)
}

// flakyPrimaryRun returns a run closure that fails the given provider with
// the given error `failTimes` times, then succeeds with `okContent`.
func flakyPrimaryRun(failProvider string, failTimes int, failErr func() error, okContent string) func(ctx context.Context, provider, model string) (*LLMResponse, error) {
	calls := 0
	return func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		if provider == failProvider {
			calls++
			if calls <= failTimes {
				return nil, failErr()
			}
		}
		return &LLMResponse{Content: okContent, FinishReason: "stop"}, nil
	}
}

// countCalls wraps a run closure and counts calls per provider.
func countCalls(run func(ctx context.Context, provider, model string) (*LLMResponse, error), counter *map[string]int) func(ctx context.Context, provider, model string) (*LLMResponse, error) {
	return func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		(*counter)[provider]++
		return run(ctx, provider, model)
	}
}

func newChainForTest() (*FallbackChain, *CooldownTracker) {
	ct := NewCooldownTracker()
	return NewFallbackChain(ct), ct
}

func exhaustedFrom(t *testing.T, err error) *FallbackExhaustedError {
	t.Helper()
	var fex *FallbackExhaustedError
	if !errors.As(err, &fex) {
		t.Fatalf("want FallbackExhaustedError, got %T: %v", err, err)
	}
	return fex
}

// ── Row 8 / A-1: retry-after ≤ 120 s is honored exactly (C-9) ──────────────

func TestFallbackRetryLoop_HonorsRetryAfter_A1(t *testing.T) {
	fc, _ := newChainForTest()
	run := flakyPrimaryRun("openrouter", 1,
		func() error { return rateLimited(t, "1") },
		"answered by primary on call 2")

	start := time.Now()
	res, err := fc.Execute(context.Background(),
		[]FallbackCandidate{{Provider: "openrouter", Model: "m-a"}},
		run)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute returned error %v, want success on call 2 (A-1)", err)
	}
	if res.Provider != "openrouter" {
		t.Fatalf("result.Provider = %q, want the primary (A-1: the candidate retries in place)", res.Provider)
	}
	if elapsed < 900*time.Millisecond || elapsed > 1300*time.Millisecond {
		t.Fatalf("elapsed = %v, want ~1s (retry-after: 1 honored exactly; ±25%% backoff jitter must NOT apply to honored waits, C-9)", elapsed)
	}
}

// ── Row 8 / A-3: absent retry-after → standard backoff first step (C-9) ────

func TestFallbackRetryLoop_BackoffWhenNoHeader_A3(t *testing.T) {
	fc, _ := newChainForTest()
	run := flakyPrimaryRun("openrouter", 1,
		func() error { return rateLimited(t, "") },
		"answered by primary on call 2")

	start := time.Now()
	res, err := fc.Execute(context.Background(),
		[]FallbackCandidate{{Provider: "openrouter", Model: "m-a"}},
		run)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute returned error %v, want success on call 2 (A-3)", err)
	}
	if res.Provider != "openrouter" {
		t.Fatalf("result.Provider = %q, want the primary", res.Provider)
	}
	// Spec backoff: 2 s × 2^n capped at 30 s with ±25% jitter → first wait is
	// 1.5–2.5 s. The 200 ms upper pad is scheduler slack, not tolerance drift.
	if elapsed < 1450*time.Millisecond || elapsed > 2700*time.Millisecond {
		t.Fatalf("elapsed = %v, want the standard backoff first step 2 s ±25%% jitter (A-3/C-9)", elapsed)
	}
}

// ── Row 8 / C-8: the attempt cap is 3 TOTAL calls, one cooldown mark ───────

func TestFallbackRetryLoop_AttemptCapIs3_C8(t *testing.T) {
	fc, ct := newChainForTest()
	counter := map[string]int{}
	run := countCalls(flakyPrimaryRun("openrouter", 999,
		func() error { return rateLimited(t, "1") },
		"never reached for the primary"), &counter)

	res, err := fc.Execute(context.Background(),
		[]FallbackCandidate{{Provider: "openrouter", Model: "m-a"}, {Provider: "anthropic", Model: "claude-x"}},
		run)
	if err != nil {
		t.Fatalf("Execute returned error %v, want the Fallback model to answer", err)
	}
	if res.Provider != "anthropic" {
		t.Fatalf("result.Provider = %q, want the Fallback model after the primary exhausted its attempts", res.Provider)
	}
	if got := counter["openrouter"]; got != 3 {
		t.Fatalf("primary called %d times, want exactly 3 TOTAL calls including the first (C-8)", got)
	}
	if got := counter["anthropic"]; got != 1 {
		t.Fatalf("fallback called %d times, want 1", got)
	}
	key := ModelKey("openrouter", "m-a")
	if ct.IsAvailable(key) {
		t.Fatal("primary not marked failed after exhausting its attempts (C-8: one mark after the third failure)")
	}
	// Exactly ONE mark: a per-attempt mark (3 marks) would land on the
	// standard curve's third step, not the first.
	if got, want := ct.CooldownRemaining(key), calculateStandardCooldown(1); got != want {
		t.Fatalf("CooldownRemaining = %v, want the standard FIRST step %v — the candidate is marked ONCE, not per attempt (C-8)", got, want)
	}
}

// ── Row 8 / A-4: over-ceiling retry-after skips the retry, unmarked ────────

func TestFallbackRetryLoop_OverCeilingSkipsToFallback_A4(t *testing.T) {
	fc, ct := newChainForTest()
	counter := map[string]int{}
	run := countCalls(flakyPrimaryRun("openrouter", 999,
		func() error { return rateLimited(t, "3600") },
		"never reached for the primary"), &counter)

	start := time.Now()
	res, err := fc.Execute(context.Background(),
		[]FallbackCandidate{{Provider: "openrouter", Model: "m-a"}, {Provider: "anthropic", Model: "claude-x"}},
		run)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute returned error %v, want the Fallback model to answer", err)
	}
	if got := counter["openrouter"]; got != 1 {
		t.Fatalf("primary called %d times, want 1 (A-4: over-ceiling never retries in place)", got)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("elapsed = %v, want < 2 s (A-4: skip-to-fallback, never waits out a 3600 s header)", elapsed)
	}
	if res.Provider != "anthropic" {
		t.Fatalf("result.Provider = %q, want the Fallback model (A-4)", res.Provider)
	}
	// C-9: NO cooldown mark for the cap-skipped candidate.
	if key := ModelKey("openrouter", "m-a"); !ct.IsAvailable(key) {
		t.Fatal("cap-skipped primary was marked failed; C-9 says an over-ceiling skip must not cool the candidate down")
	}
}

// ── Row 23 / MAJ-101 (a): primary answers on call 2, unmarked ──────────────

func TestMAJ101_ChainRow_PrimaryAnswersOnCall2(t *testing.T) {
	fc, ct := newChainForTest()
	counter := map[string]int{}
	run := countCalls(flakyPrimaryRun("openrouter", 1,
		func() error { return rateLimited(t, "3") },
		"primary answered on the second attempt"), &counter)

	res, err := fc.Execute(context.Background(),
		[]FallbackCandidate{{Provider: "openrouter", Model: "m-a"}, {Provider: "anthropic", Model: "claude-x"}},
		run)
	if err != nil {
		t.Fatalf("Execute returned error %v, want success on call 2", err)
	}
	if got := counter["openrouter"]; got != 2 {
		t.Fatalf("primary called %d times, want 2 (429 once, retried in place per MAJ-101)", got)
	}
	if res.Provider != "openrouter" {
		t.Fatalf("result.Provider = %q, want the primary — the chain row is 'primary answers on call 2'", res.Provider)
	}
	if res.Response == nil || res.Response.Content != "primary answered on the second attempt" {
		t.Fatalf("response content = %+v, want the primary's answer", res.Response)
	}
	if key := ModelKey("openrouter", "m-a"); !ct.IsAvailable(key) {
		t.Fatal("primary was cooled down despite answering on call 2 — no failure mark may survive a success (MAJ-101 chain row)")
	}
	// retry-after: 3 is honored exactly.
	// (The wait happened before the second call; verified indirectly by the
	// call count — an exact-timing variant is A-1's test above.)
}

// ── Row 23 / MAJ-101 (b): 3 failures → EXACTLY one mark → fallback ─────────

func TestMAJ101_ChainRow_ExactlyOneMarkFailure(t *testing.T) {
	fc, ct := newChainForTest()
	counter := map[string]int{}
	run := countCalls(flakyPrimaryRun("openrouter", 999,
		func() error { return rateLimited(t, "1") },
		"never reached for the primary"), &counter)

	res, err := fc.Execute(context.Background(),
		[]FallbackCandidate{{Provider: "openrouter", Model: "m-a"}, {Provider: "anthropic", Model: "claude-x"}},
		run)
	if err != nil {
		t.Fatalf("Execute returned error %v, want the Fallback model to answer", err)
	}
	if got := counter["openrouter"]; got != 3 {
		t.Fatalf("primary called %d times, want 3 (the verbatim chain row: three total calls)", got)
	}
	if got := counter["anthropic"]; got != 1 {
		t.Fatalf("fallback called %d times, want 1", got)
	}
	if res.Provider != "anthropic" {
		t.Fatalf("result.Provider = %q, want the Fallback model", res.Provider)
	}
	key := ModelKey("openrouter", "m-a")
	if ct.IsAvailable(key) {
		t.Fatal("primary not marked after exhausting attempts")
	}
	// EXACTLY one mark: the distinguisher between "marked once after the last
	// failure" and "marked per attempt".
	if got, want := ct.CooldownRemaining(key), calculateStandardCooldown(1); got != want {
		t.Fatalf("CooldownRemaining = %v, want the standard first step %v (exactly one mark — MAJ-101 chain row)", got, want)
	}
}

// ── Row 8 / B row: billing never retries in place (pin) ────────────────────
//
// PIN, labelled per the file header: the in-chain retry loop does not exist
// yet, so "no in-chain retry" is trivially true today. The assertion becomes
// live the moment the loop lands — it exists so GREEN cannot teach the loop
// to retry billing (C-7/D2: billing is not a wait-it-out condition).

func TestFallbackRetryLoop_BillingAnswersByFallbackAtOnce(t *testing.T) {
	fc, ct := newChainForTest()
	counter := map[string]int{}
	run := countCalls(flakyPrimaryRun("openrouter", 999,
		func() error {
			return peFromBoundary(t, 402, nil, `{"error":{"message":"insufficient credits"}}`)
		},
		"never reached for the primary"), &counter)

	start := time.Now()
	res, err := fc.Execute(context.Background(),
		[]FallbackCandidate{{Provider: "openrouter", Model: "m-a"}, {Provider: "anthropic", Model: "claude-x"}},
		run)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute returned error %v, want the Fallback model to answer at once", err)
	}
	if got := counter["openrouter"]; got != 1 {
		t.Fatalf("billing candidate called %d times, want 1 (billing never retries in place, C-7/D2)", got)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("elapsed = %v, want < 2 s (fallback answers at once — no wait-it-out)", elapsed)
	}
	if res.Provider != "anthropic" {
		t.Fatalf("result.Provider = %q, want the Fallback model", res.Provider)
	}
	// C-7: the billing verdict takes the standard curve, not a lockout.
	if key := ModelKey("openrouter", "m-a"); ct.CooldownRemaining(key) != calculateStandardCooldown(1) {
		t.Fatalf("billing cooldown = %v, want the standard first step (C-7)", ct.CooldownRemaining(key))
	}
}

// ── Row 8 / chain row 1: both candidates 429 → each retries 3×, then marks ─

func TestFallbackRetryLoop_BothCandidatesExhaustAttempts_C8(t *testing.T) {
	fc, ct := newChainForTest()
	counter := map[string]int{}
	run := countCalls(func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		return nil, rateLimited(t, "1")
	}, &counter)

	_, err := fc.Execute(context.Background(),
		[]FallbackCandidate{{Provider: "openrouter", Model: "m-a"}, {Provider: "anthropic", Model: "claude-x"}},
		run)
	fex := exhaustedFrom(t, err)

	if got := counter["openrouter"]; got != 3 {
		t.Fatalf("primary called %d times, want 3 (each candidate retried in place up to the C-8 cap)", got)
	}
	if got := counter["anthropic"]; got != 3 {
		t.Fatalf("fallback called %d times, want 3", got)
	}
	if ct.IsAvailable(ModelKey("openrouter", "m-a")) || ct.IsAvailable(ModelKey("anthropic", "claude-x")) {
		t.Fatal("neither candidate may stay available after exhausting its attempts (chain row 1)")
	}
	// C-16: the exhaustion error names the LAST failing attempt.
	last := fex.LastProviderError()
	if last == nil || last.Provider != "anthropic" || last.Model != "claude-x" {
		t.Fatalf("LastProviderError = %+v, want the last candidate anthropic/claude-x (C-16)", last)
	}
}

// ── Row 9 / C-12: Stop ends the retry wait ≤ 1 s, no further attempt ───────

func TestStopEndsRetryWait_C12(t *testing.T) {
	fc, _ := newChainForTest()
	counter := map[string]int{}
	run := countCalls(flakyPrimaryRun("openrouter", 999,
		func() error { return rateLimited(t, "30") },
		"never reached"), &counter)

	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	_, err := fc.Execute(ctx,
		[]FallbackCandidate{{Provider: "openrouter", Model: "m-a"}},
		run)
	returnedAt := time.Since(start)

	// The distinguisher between "there was no wait to interrupt" (today's
	// abort-immediately shape) and "the wait was scheduled and cancelled":
	// Execute must still be inside its retry wait WHEN Stop fires, then
	// return within C-12's 1 s. Today the chain returns in ~1 ms — before
	// the cancel — so the first assertion is RED.
	if returnedAt < 150*time.Millisecond {
		t.Fatalf("Execute returned after %v — before Stop fired at 150 ms; C-12 requires a scheduled, context-cancellable retry wait", returnedAt)
	}
	if returnedAt > 1150*time.Millisecond {
		t.Fatalf("Execute returned after %v; C-12: Stop must end the wait within ~1 s", returnedAt)
	}
	if got := counter["openrouter"]; got != 1 {
		t.Fatalf("primary called %d times, want 1 — no further attempt after Stop (C-12)", got)
	}
	if err == nil {
		t.Fatal("Execute returned nil error after cancellation; want a cancellation-shaped error")
	}
	// The concrete error identity is GREEN's choice (the spec names no value);
	// today's user-abort convention is context.Canceled — asserted as the
	// soft expectation it is.
	if !errors.Is(err, context.Canceled) {
		t.Logf("note: error is %T (%v), today's convention is context.Canceled", err, err)
	}
}

// ── Row 10a / C-16: the failing fallback attempt's identity surfaces ───────

func TestFailingAttemptIdentity_SurfacesFallbackAttempt_C16(t *testing.T) {
	fc, ct := newChainForTest()
	// The primary is already cooling down, so the chain skips it.
	ct.MarkFailure(ModelKey("openrouter", "m-a"), FailoverRateLimit)

	authFail := peFromBoundary(t, 401, nil, `{"error":{"message":"Incorrect API key provided: sk-proj-****abcd"}}`)
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		return nil, authFail
	}

	_, err := fc.Execute(context.Background(),
		[]FallbackCandidate{{Provider: "openrouter", Model: "m-a"}, {Provider: "anthropic", Model: "claude-x"}},
		run)
	fex := exhaustedFrom(t, err)

	last := fex.LastProviderError()
	if last == nil || last.Provider != "anthropic" || last.Model != "claude-x" {
		t.Fatalf("LastProviderError = %+v, want the FALLBACK attempt's identity anthropic/claude-x (C-16: identity from the failing attempt, never the primary's)", last)
	}
	structured := fex.LastStructuredError()
	if structured == nil || structured.Status != 401 {
		t.Fatalf("LastStructuredError = %+v, want the 401 ProviderError of the fallback attempt (C-16)", structured)
	}
}

// ── Row 26 / chain half (D12): 404 retirement falls back ───────────────────

func TestModelRetired404_FallsBack_D12(t *testing.T) {
	fc, ct := newChainForTest()
	counter := map[string]int{}
	run := countCalls(flakyPrimaryRun("openrouter", 999,
		func() error {
			return peFromBoundary(t, 404, nil, `{"error":{"message":"This model has been decommissioned"}}`)
		},
		"never reached"), &counter)

	res, err := fc.Execute(context.Background(),
		[]FallbackCandidate{{Provider: "openrouter", Model: "m-a"}, {Provider: "anthropic", Model: "claude-x"}},
		run)
	if err != nil {
		t.Fatalf("Execute returned %v, want the Fallback model to answer (D12: a retired model routes FailoverUnknown, which is retriable)", err)
	}
	if got := counter["openrouter"]; got != 1 {
		t.Fatalf("primary called %d times, want 1 (a 404 is a skip-to-fallback, never a retry)", got)
	}
	if res.Provider != "anthropic" {
		t.Fatalf("result.Provider = %q, want the Fallback model (MR-4)", res.Provider)
	}
	if ct.IsAvailable(ModelKey("openrouter", "m-a")) {
		t.Fatal("primary should be marked failed after the 404 (one mark, standard curve)")
	}
}

// ── Row 24 / D14: the 10-minute total-wait cap — BLOCKED-pattern test ──────
//
// A real test cannot be written against a named symbol today: C-9/D14's
// "waits never truncated, total wait ≤ 10 min per turn" needs an injectable
// clock or wait function on FallbackChain so a test can prove the cap
// without literally sleeping 10+ minutes. No such seam exists (§2's symbols
// table names none). Per the RED rule this is a LOUD placeholder, not a
// skip: it fails the moment GREEN touches it, with the full oracle table
// GREEN must satisfy.
//
// Oracle table (from C-9/D14, spec-derived — GREEN implements against THIS):
//
//	| # | scenario                                          | expected                                    |
//	|---|---------------------------------------------------|---------------------------------------------|
//	| 1 | consecutive honored waits 90 s + 90 s             | both waited in full (never truncated)       |
//	| 2 | cumulative wait reaches 10 min; another 429 with  | no further wait; skip-to-fallback           |
//	|   | retry-after 30 s arrives                          | immediately, no cooldown mark for the skip  |
//	| 3 | backoff steps 2+4+8+…+30 s accumulate to > 10 min | cap fires at 10 min; fallback answers       |
//	| 4 | a single honored wait of 120 s (at the ceiling)   | waited in full (120 ≤ 600 cap)              |
func TestD14_TotalWaitCap_SeamMissing(t *testing.T) {
	t.Fatal("BLOCKED: D14 10-minute total-wait cap is untestable — no injectable clock/wait seam on FallbackChain (required by C-9/D14); GREEN must add the seam and satisfy the oracle table in this test's comment")
}
