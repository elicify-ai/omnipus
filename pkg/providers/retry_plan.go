package providers

// Issue #1280 — the retry plan for provider and network failures.
//
// Founder decisions (2026-10-12): at least 5 retries with growing waits of
// about 2 s, 5 s, 10 s, 20 s and 40 s (~1.5 min in total), the provider's
// Retry-After honoured, and the retry state shown from the first failed
// attempt. Retried: no connection (DNS, refused, reset, TLS, connection
// lost), overloaded (429/5xx) and stopped mid-answer. Never retried: access
// problems (401/402/403) and rejected requests (unknown model, context too
// long).
//
// Where it runs: FallbackChain.Execute applies the plan to the LAST
// candidate the chain can still try (for a single-model agent, that model).
// An earlier candidate keeps the provider-messages C-8 behaviour (a rate
// limit retries in place up to 3 calls, any other retriable failure hands
// over to the next candidate at once), because a configured fallback model
// is the faster recovery. The C-9 Retry-After ceiling, the D14 per-turn wait
// budget, the C-10 streamed-bytes guard and the C-12 Stop all apply.
//
// A deadline or a silence stall is not a connection failure and is not part
// of the plan: each such attempt can already take minutes, and the agent
// loop's own timeout recovery (with context compaction) owns it.

import (
	"context"
	"errors"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Retry causes — the cause class the provider_retry frame carries as its
// error_code (plain words are the client's job, never raw provider text).
const (
	RetryCauseNetwork           = "network"
	RetryCauseRateLimited       = "rate_limited"
	RetryCauseOverloaded        = "overloaded"
	RetryCauseStreamInterrupted = "stream_interrupted"
)

// retryPlanWaits is founder decision Q1: the wait after the n-th failed call.
var retryPlanWaits = [...]time.Duration{ //nolint:gochecknoglobals // fixed policy table
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	20 * time.Second,
	40 * time.Second,
}

// RetryPlanMaxAttempts is the plan's total call count: the first call plus
// one retry per scheduled wait.
const RetryPlanMaxAttempts = len(retryPlanWaits) + 1

// retryPlanWait is the wait after call attemptNo (1-based) failed. A
// Retry-After in [1, retryAfterCeiling] replaces the scheduled step; the
// caller skips over-ceiling values before asking.
func retryPlanWait(attemptNo, retryAfterSeconds int) time.Duration {
	if retryAfterSeconds > 0 {
		return time.Duration(retryAfterSeconds) * time.Second
	}
	i := attemptNo - 1
	if i < 0 {
		i = 0
	}
	if i >= len(retryPlanWaits) {
		i = len(retryPlanWaits) - 1
	}
	return retryPlanWaits[i]
}

// RetryCauseOf reports the #1280 retry cause of a provider failure, or ""
// when the failure is not part of the retry plan (access problems, rejected
// requests, deadlines, silence stalls, a user Stop, unknown errors).
func RetryCauseOf(err error) string {
	if err == nil || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, common.ErrStreamStalled) {
		return ""
	}
	// A provider that failed after its 200 was committed: its own code
	// decides (a billing or auth failure must not be retried).
	var spe *common.StreamProviderError
	if errors.As(err, &spe) {
		switch spe.Kind() {
		case common.StreamErrorTransient:
			return RetryCauseStreamInterrupted
		case common.StreamErrorRateLimit:
			return RetryCauseRateLimited
		}
		return ""
	}
	fe := ClassifyError(err, "", "")
	if fe == nil {
		return ""
	}
	switch fe.Reason {
	case FailoverRateLimit:
		return RetryCauseRateLimited
	case FailoverTimeout:
		// Status first: a 5xx is an overloaded or failing provider, a 408
		// is the connection timing out.
		status := fe.Status
		var pe *common.ProviderError
		if status == 0 && errors.As(err, &pe) {
			status = pe.Status
		}
		switch {
		case status == 408:
			return RetryCauseNetwork
		case status >= 500:
			return RetryCauseOverloaded
		}
		if IsTransportFailure(err) {
			return RetryCauseNetwork
		}
	}
	return ""
}

// inPlaceRetryDecision is FallbackChain.inPlaceRetry's verdict for one
// failed call.
type inPlaceRetryDecision struct {
	retry         bool          // call this candidate again after wait
	skip          bool          // move on WITHOUT a cooldown mark (A-4 ceiling / D14 budget)
	wait          time.Duration // the full wait before the next call
	maxAttempts   int           // the cap the announced "attempt N of M" reads
	reason        string        // the cause the announcement carries
	planCandidate bool          // the issue #1280 plan governs this candidate
}

// inPlaceRetry decides whether candidates[i] is called again after its call
// attemptNo failed with err (classified failErr). The issue #1280 plan
// governs the last candidate the chain can still try; earlier candidates
// keep the provider-messages C-8 rate-limit retry. Both honour the C-9
// Retry-After ceiling, the D14 wait budget and the C-10 streamed-bytes
// guard; the caller announces the retry and owns the C-12 cancellable wait.
func (fc *FallbackChain) inPlaceRetry(
	ctx context.Context,
	candidates []FallbackCandidate,
	i, attemptNo int,
	err error,
	failErr *FailoverError,
	streamed bool,
) inPlaceRetryDecision {
	if streamed {
		return inPlaceRetryDecision{}
	}
	var d inPlaceRetryDecision
	ras := retryAfterOf(err)
	cause := ""
	if !fc.laterCandidateAvailable(candidates, i) {
		cause = RetryCauseOf(err)
	}
	switch {
	case cause != "":
		d.planCandidate = true
		if attemptNo >= RetryPlanMaxAttempts {
			return d
		}
		d.maxAttempts, d.reason = RetryPlanMaxAttempts, cause
		d.wait = retryPlanWait(attemptNo, ras)
	case failErr.Reason == FailoverRateLimit && attemptNo < maxAttemptsPerCandidate:
		d.maxAttempts, d.reason = maxAttemptsPerCandidate, string(failErr.Reason)
		d.wait = nextRetryWait(attemptNo, ras, retryJitter())
	default:
		return d
	}
	if ras > int(retryAfterCeiling.Seconds()) {
		// A-4: over-ceiling — skip the retries AND the mark; the next
		// candidate (if any) answers now.
		d.skip = true
		return d
	}
	// D14: reserve the FULL wait before scheduling it — a wait the budget
	// cannot cover in full is never started, and this candidate moves on
	// unmarked (never truncated).
	if !waitBudgetFrom(ctx).Reserve(d.wait) {
		d.skip = true
		return d
	}
	d.retry = true
	return d
}

// laterCandidateAvailable reports whether any candidate after index i can
// still be tried (not cooling down).
func (fc *FallbackChain) laterCandidateAvailable(candidates []FallbackCandidate, i int) bool {
	for _, c := range candidates[i+1:] {
		if fc.cooldown.IsAvailable(ModelKey(c.Provider, c.Model)) {
			return true
		}
	}
	return false
}
