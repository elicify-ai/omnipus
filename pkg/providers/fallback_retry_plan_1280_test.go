// fallback_retry_plan_1280_test.go — issue #1280: provider and network
// failures are classified, retried at least 5 times with growing waits, and
// every decided retry is announced.
//
// Oracles come from the issue's founder decisions (2026-10-12):
//   - Q1: at least 5 retries with waits of about 2 s, 5 s, 10 s, 20 s, 40 s
//     (~1.5 min in total); a provider's Retry-After is honoured.
//   - Required behaviour 1: retry classes are no connection (DNS, refused,
//     reset, TLS, timeout, connection lost), overloaded (429/503/529) and
//     stopped mid-answer; access problems (401/402/403) and rejected
//     requests (unknown model, context too long) are NOT retried.
//   - Q2: the retry state is shown from the first failed attempt (one
//     RetryInfo per decided retry, before its wait).
//
// Scope decision (documented in docs/troubleshooting.md): the plan runs on
// the LAST candidate the chain can still try (for a single-model agent, that
// model). An earlier candidate keeps the provider-messages C-8 behaviour —
// a rate limit retries in place up to 3 calls, any other retriable failure
// hands over to the next candidate at once — because a configured fallback
// model is the faster recovery. A deadline or a silence stall is not a
// connection failure and is not part of the plan (each attempt can already
// take minutes; the agent loop's own timeout recovery owns it).
package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// issue1280PlanWaits is founder decision Q1, verbatim.
var issue1280PlanWaits = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second}

func TestIssue1280_RetryCauseClassifiesEveryProviderFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		// No connection.
		{"dns failure", errors.New(`Post "https://openrouter.ai/api/v1/chat/completions": dial tcp: lookup openrouter.ai: no such host`), "network"},
		{"dns timeout", errors.New("dial tcp: lookup openrouter.ai: i/o timeout"), "network"},
		{"connection refused", errors.New("dial tcp 127.0.0.1:443: connect: connection refused"), "network"},
		{"connection reset", errors.New("read tcp 10.0.0.1:443: read: connection reset by peer"), "network"},
		{"tls handshake", errors.New("net/http: TLS handshake timeout"), "network"},
		{"http2 connection lost", errors.New("http2: client connection lost"), "network"},
		{"http2 protocol error", errors.New("stream error: stream ID 3; PROTOCOL_ERROR"), "network"},
		{"request timeout 408", peFromBoundaryErr(408, `{"error":{"message":"request timeout"}}`), "network"},
		// Overloaded.
		{"rate limited 429", peFromBoundaryErr(429, `{"error":{"message":"rate limited"}}`), "rate_limited"},
		{"overloaded 503", peFromBoundaryErr(503, `{"error":{"message":"service unavailable"}}`), "overloaded"},
		{"overloaded 529", peFromBoundaryErr(529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`), "overloaded"},
		{"server error 500", peFromBoundaryErr(500, `{"error":{"message":"internal error"}}`), "overloaded"},
		// Stopped mid-answer (the provider failed after its 200 was committed).
		{"mid-stream transient", &common.StreamProviderError{Code: "server_error", Message: "upstream failed"}, "stream_interrupted"},
		{"mid-stream rate limit", &common.StreamProviderError{Code: "429", Message: "rate limited"}, "rate_limited"},
		// Never retried.
		{"auth 401", peFromBoundaryErr(401, `{"error":{"message":"invalid api key"}}`), ""},
		{"forbidden 403", peFromBoundaryErr(403, `{"error":{"message":"forbidden"}}`), ""},
		{"billing 402", peFromBoundaryErr(402, `{"error":{"message":"insufficient credits"}}`), ""},
		{"unknown model 400", peFromBoundaryErr(400, `{"error":{"code":"model_not_found","message":"The model does not exist"}}`), ""},
		{"context too long 400", peFromBoundaryErr(400, `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`), ""},
		{"mid-stream auth", &common.StreamProviderError{Code: "401", Message: "invalid key"}, ""},
		{"deadline is not a connection failure", fmt.Errorf("attempt: %w", context.DeadlineExceeded), ""},
		{"silence stall is not a connection failure", fmt.Errorf("stream: %w", common.ErrStreamStalled), ""},
		{"user stop", context.Canceled, ""},
		{"nil", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, RetryCauseOf(tc.err), "#1280: retry cause of %v", tc.err)
		})
	}
}

// peFromBoundaryErr builds a boundary error through the real parse path
// (common.HandleErrorResponse), without a *testing.T for table literals.
func peFromBoundaryErr(status int, body string) error {
	resp := mkResp(status, http.Header{}, body)
	defer resp.Body.Close()
	return common.HandleErrorResponse(resp, "https://api.example.com")
}

type issue1280Recorder struct {
	waits []time.Duration
	infos []RetryInfo
}

func issue1280Chain(rec *issue1280Recorder) (*FallbackChain, context.Context) {
	fc := NewFallbackChain(NewCooldownTracker())
	fc.waitFunc = func(ctx context.Context, d time.Duration) error {
		rec.waits = append(rec.waits, d)
		return ctx.Err()
	}
	ctx := WithRetryObserver(context.Background(), func(info RetryInfo) { rec.infos = append(rec.infos, info) })
	return fc, ctx
}

func TestIssue1280_SingleModelNetworkFailureRetriesFiveTimesOnTheFounderSchedule(t *testing.T) {
	rec := &issue1280Recorder{}
	fc, ctx := issue1280Chain(rec)
	calls := 0
	res, err := fc.Execute(ctx, []FallbackCandidate{{Provider: "openrouter", Model: "m"}},
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			calls++
			if calls <= 5 {
				return nil, errors.New("dial tcp: lookup openrouter.ai: no such host")
			}
			return &LLMResponse{Content: "back online"}, nil
		})
	require.NoError(t, err, "#1280: the sixth call (fifth retry) succeeds and the turn continues")
	require.Equal(t, "back online", res.Response.Content)
	require.Equal(t, 6, calls, "1 call + 5 retries")
	require.Equal(t, issue1280PlanWaits, rec.waits, "#1280 Q1: waits 2, 5, 10, 20, 40 s")
	require.Len(t, rec.infos, 5, "#1280 Q2: one announced retry per decided retry, from the first failure")
	for i, info := range rec.infos {
		require.Equal(t, i+2, info.Attempt, "attempt number of the call ABOUT to be made")
		require.Equal(t, 6, info.MaxAttempts, "of N total calls")
		require.Equal(t, "network", info.Reason, "cause class shown to the person")
		require.Equal(t, "openrouter", info.Provider)
		require.Equal(t, issue1280PlanWaits[i], info.RetryAt.Sub(info.SentAt), "announced wait equals the real wait")
	}
	require.Equal(t, 5, res.Retries, "the answering candidate's in-place retries are reported for the recovery note")
}

func TestIssue1280_ExhaustedPlanReportsItRanAndStopsAfterSixCalls(t *testing.T) {
	rec := &issue1280Recorder{}
	fc, ctx := issue1280Chain(rec)
	calls := 0
	_, err := fc.Execute(ctx, []FallbackCandidate{{Provider: "openrouter", Model: "m"}},
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			calls++
			return nil, errors.New("read tcp 10.0.0.1:443: read: connection reset by peer")
		})
	fex := exhaustedFrom(t, err)
	require.Equal(t, 6, calls, "#1280: exactly 1 + 5 calls, never more")
	require.Len(t, fex.Attempts, 1)
	require.Equal(t, 6, fex.Attempts[0].Calls, "the attempt record counts its calls")
	require.True(t, fex.Attempts[0].RetryPlan, "the attempt record says the #1280 plan ran, so the agent loop does not retry it again")
}

func TestIssue1280_RetryAfterIsHonouredInsideThePlan(t *testing.T) {
	rec := &issue1280Recorder{}
	fc, ctx := issue1280Chain(rec)
	calls := 0
	res, err := fc.Execute(ctx, []FallbackCandidate{{Provider: "p", Model: "m"}},
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			calls++
			switch calls {
			case 1:
				return nil, peFromBoundary(t, 503, http.Header{"Retry-After": []string{"7"}}, `{"error":{"message":"busy"}}`)
			case 2:
				return nil, peFromBoundary(t, 503, nil, `{"error":{"message":"busy"}}`)
			}
			return &LLMResponse{Content: "ok"}, nil
		})
	require.NoError(t, err)
	require.Equal(t, "ok", res.Response.Content)
	require.Equal(t, []time.Duration{7 * time.Second, 5 * time.Second}, rec.waits,
		"#1280 Q1: the provider's Retry-After (7 s) replaces the first step; the schedule continues with its second step")
	require.Equal(t, "overloaded", rec.infos[0].Reason)
}

func TestIssue1280_AccessAndRejectedRequestsAreNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  func() error
	}{
		{"401", func() error { return peFromBoundary(t, 401, nil, `{"error":{"message":"invalid api key"}}`) }},
		{"402", func() error { return peFromBoundary(t, 402, nil, `{"error":{"message":"insufficient credits"}}`) }},
		{"403", func() error { return peFromBoundary(t, 403, nil, `{"error":{"message":"forbidden"}}`) }},
		{"400 context too long", func() error {
			return peFromBoundary(t, 400, nil, `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &issue1280Recorder{}
			fc, ctx := issue1280Chain(rec)
			calls := 0
			_, err := fc.Execute(ctx, []FallbackCandidate{{Provider: "p", Model: "m"}},
				func(ctx context.Context, provider, model string) (*LLMResponse, error) {
					calls++
					return nil, tc.err()
				})
			require.Error(t, err)
			require.Equal(t, 1, calls, "#1280: %s is never retried", tc.name)
			require.Empty(t, rec.infos, "no retry is announced for %s", tc.name)
			require.Empty(t, rec.waits)
		})
	}
}

func TestIssue1280_DeadlineAndStallAreNotPartOfThePlan(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, common.ErrStreamStalled} {
		rec := &issue1280Recorder{}
		fc, ctx := issue1280Chain(rec)
		calls := 0
		_, err := fc.Execute(ctx, []FallbackCandidate{{Provider: "p", Model: "m"}},
			func(ctx context.Context, provider, model string) (*LLMResponse, error) {
				calls++
				return nil, failure
			})
		fex := exhaustedFrom(t, err)
		require.Equal(t, 1, calls, "%v: a slow or silent provider is not retried in place (each attempt may take minutes)", failure)
		require.False(t, fex.Attempts[0].RetryPlan)
	}
}

func TestIssue1280_EarlierCandidateHandsOverAndTheLastRunsThePlan(t *testing.T) {
	rec := &issue1280Recorder{}
	fc, ctx := issue1280Chain(rec)
	counter := map[string]int{}
	res, err := fc.Execute(ctx,
		[]FallbackCandidate{{Provider: "primary", Model: "a"}, {Provider: "fallback", Model: "b"}},
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			counter[provider]++
			if provider == "fallback" && counter[provider] > 3 {
				return &LLMResponse{Content: "fallback answered"}, nil
			}
			return nil, errors.New("http2: client connection lost")
		})
	require.NoError(t, err)
	require.Equal(t, "fallback answered", res.Response.Content)
	require.Equal(t, 1, counter["primary"], "an earlier candidate hands over at once on a connection failure (C-8 unchanged)")
	require.Equal(t, 4, counter["fallback"], "the last candidate runs the plan: 1 call + 3 retries until it answers")
	require.Equal(t, issue1280PlanWaits[:3], rec.waits)
	require.Equal(t, 3, res.Retries)
}

// TestIssue1280_SkipRecordCarriesTheRealFailureClass: the A-4 skip (a
// Retry-After over the ceiling) records the REAL failure class of the last
// failed call. With the plan governing network and overload failures on the
// last candidate, a skip is no longer rate-limit-only — a 503 with a huge
// Retry-After reaches it, and labelling that "rate limit" misreports the
// outage to anything that reads Attempts[].Reason. The real class of a 503
// in the FailoverReason taxonomy is FailoverTimeout (transientStatusCodes);
// what the record must never say again is the hardcoded rate limit.
func TestIssue1280_SkipRecordCarriesTheRealFailureClass(t *testing.T) {
	rec := &issue1280Recorder{}
	fc, ctx := issue1280Chain(rec)
	calls := 0
	_, err := fc.Execute(ctx, []FallbackCandidate{{Provider: "p", Model: "m"}},
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			calls++
			return nil, peFromBoundary(t, 503,
				http.Header{"Retry-After": []string{"9999"}},
				`{"error":{"message":"overloaded"}}`)
		})
	fex := exhaustedFrom(t, err)
	require.Equal(t, 1, calls, "A-4: a Retry-After over the ceiling skips the retries at once")
	require.Len(t, fex.Attempts, 1)
	skip := fex.Attempts[0]
	require.True(t, skip.Skipped, "the candidate is skipped without a cooldown mark")
	require.NotEqual(t, FailoverRateLimit, skip.Reason,
		"the skipped record must not hardcode a rate-limit label for a 503")
	require.Equal(t, FailoverTimeout, skip.Reason,
		"the skipped record must carry the last failed call's real class (503 → FailoverTimeout)")
}

// TestIssue1280_PlanRetriesThenDeadlineIsNotTaggedPlanRun: when the plan
// retries a connection failure and the LATER call then fails with a
// deadline — a class the plan explicitly does not govern — the concluding
// attempt record must not carry the plan flag. The flag is what tells the
// agent loop "the plan already owned this exhaustion", and on it the loop
// skips its own timeout recovery (context compaction retry), which the
// retry-plan contract says owns deadlines.
func TestIssue1280_PlanRetriesThenDeadlineIsNotTaggedPlanRun(t *testing.T) {
	rec := &issue1280Recorder{}
	fc, ctx := issue1280Chain(rec)
	calls := 0
	_, err := fc.Execute(ctx, []FallbackCandidate{{Provider: "p", Model: "m"}},
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			calls++
			if calls <= 3 {
				return nil, errors.New("dial tcp: lookup openrouter.ai: no such host")
			}
			return nil, context.DeadlineExceeded
		})
	fex := exhaustedFrom(t, err)
	require.Equal(t, 4, calls, "3 plan retries, then the call that times out")
	require.Equal(t, issue1280PlanWaits[:3], rec.waits, "the plan ran on the connection failures")
	require.Len(t, fex.Attempts, 1)
	require.Equal(t, FailoverTimeout, fex.Attempts[0].Reason, "the concluding failure is the deadline")
	require.False(t, fex.Attempts[0].RetryPlan,
		"the final failure is a deadline — outside the plan — so the loop's timeout recovery must not be suppressed")
}

func TestIssue1280_NoRetryOnceBytesStreamedThisAttempt(t *testing.T) {
	rec := &issue1280Recorder{}
	fc, ctx := issue1280Chain(rec)
	var streamed int64
	ctx = WithStreamedBytesCheck(ctx, func() int64 { return atomic.LoadInt64(&streamed) })
	calls := 0
	_, err := fc.Execute(ctx, []FallbackCandidate{{Provider: "p", Model: "m"}},
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			calls++
			atomic.StoreInt64(&streamed, 12)
			return nil, &common.StreamProviderError{Code: "server_error", Message: "upstream failed"}
		})
	require.Error(t, err)
	require.Equal(t, 1, calls, "C-10 holds inside the plan: a retry would duplicate visible text")
	require.Empty(t, rec.infos)
}
