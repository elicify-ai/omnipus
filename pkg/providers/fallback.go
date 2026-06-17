package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// defaultPerCandidateTimeout is used when no explicit per-candidate timeout is
// configured and the parent context has no deadline (or its deadline is already
// past). It matches the HTTP provider default request timeout.
const defaultPerCandidateTimeout = 120 * time.Second

// minCandidateBudget is the floor applied to the fair-split per-candidate budget
// when many candidates are competing for a small parent deadline. Without a floor,
// an early candidate in a large chain could receive a sub-second slice.
// Set to 5 seconds — enough for a fast provider to respond before the next
// candidate in line gets its turn.
const minCandidateBudget = 5 * time.Second

// FallbackChain orchestrates model fallback across multiple candidates.
type FallbackChain struct {
	cooldown            *CooldownTracker
	perCandidateTimeout time.Duration // 0 means pass parent ctx unchanged (legacy behavior, NewFallbackChain)
}

// FallbackCandidate represents one model/provider to try.
type FallbackCandidate struct {
	Provider string
	Model    string
}

// FallbackResult contains the successful response and metadata about all attempts.
type FallbackResult struct {
	Response *LLMResponse
	Provider string
	Model    string
	Attempts []FallbackAttempt
}

// FallbackAttempt records one attempt in the fallback chain.
type FallbackAttempt struct {
	Provider string
	Model    string
	Error    error
	Reason   FailoverReason
	Duration time.Duration
	Skipped  bool // true if skipped due to cooldown
}

// NewFallbackChain creates a new fallback chain with the given cooldown tracker.
// Each candidate inherits the parent context deadline (legacy behavior).
// Use NewFallbackChainWithTimeout to give each candidate its own budget.
func NewFallbackChain(cooldown *CooldownTracker) *FallbackChain {
	if cooldown == nil {
		panic("cooldown must not be nil")
	}
	return &FallbackChain{cooldown: cooldown}
}

// NewFallbackChainWithTimeout creates a fallback chain that gives each candidate
// its own timeout budget so a primary timeout does not strand later candidates.
//
// perCandidateTimeout is the maximum time allowed for a single candidate attempt.
// When the parent context still has remaining budget, each candidate gets at most
// (remaining / candidatesLeft), capped by perCandidateTimeout if both apply.
// When the parent deadline is already exhausted, each candidate gets a fresh
// context derived from context.WithoutCancel(parent) with a deadline of
// perCandidateTimeout, while still observing user cancellation on the original ctx.
// Pass 0 for perCandidateTimeout to use the default (120s).
func NewFallbackChainWithTimeout(cooldown *CooldownTracker, perCandidateTimeout time.Duration) *FallbackChain {
	if cooldown == nil {
		panic("cooldown must not be nil")
	}
	if perCandidateTimeout <= 0 {
		perCandidateTimeout = defaultPerCandidateTimeout
	}
	return &FallbackChain{cooldown: cooldown, perCandidateTimeout: perCandidateTimeout}
}

// candidateBudget computes the context and cancel function to use for one attempt.
//
// It returns an attemptCtx that:
//   - Observes user cancellation from the original ctx (context.Canceled propagates).
//   - Has its own deadline so an exhausted parent deadline does not kill the attempt
//     in ~1ms.
//
// budget is derived as follows:
//  1. If fc.perCandidateTimeout <= 0 (field not set; only possible via NewFallbackChain):
//     returns ctx unchanged — no per-candidate deadline is applied (legacy pass-through).
//  2. If the parent ctx has a live (positive) remaining deadline:
//     budget = max(remaining/candidatesLeft, minCandidateBudget)
//     If fc.perCandidateTimeout > 0: budget = min(budget, fc.perCandidateTimeout)
//  3. If the parent ctx has no deadline, or the deadline is already past:
//     budget = fc.perCandidateTimeout (always positive here; 0 is handled in case 1)
//
// The caller MUST call the returned cancel func immediately after the attempt
// returns to release resources (do not defer inside a loop).
func (fc *FallbackChain) candidateBudget(
	ctx context.Context,
	candidatesLeft int,
) (attemptCtx context.Context, attemptCancel context.CancelFunc) {
	pct := fc.perCandidateTimeout
	if pct <= 0 {
		// perCandidateTimeout not set — pass ctx through unchanged (legacy behavior).
		return ctx, func() {}
	}

	var budget time.Duration
	deadline, hasDeadline := ctx.Deadline()
	remaining := time.Until(deadline)

	if hasDeadline && remaining > 0 {
		// Parent still has live budget: split fairly, apply floor, cap at perCandidateTimeout.
		budget = remaining / time.Duration(candidatesLeft)

		// Floor: prevent early candidates from receiving a sub-second slice when many
		// candidates compete for a small remaining budget. Only apply the floor when
		// the per-candidate cap (pct) is itself >= minCandidateBudget — if the operator
		// explicitly chose a tight pct (e.g. 50ms), respect it rather than overriding
		// with a 5-second floor they did not request.
		if budget < minCandidateBudget && pct >= minCandidateBudget {
			budget = minCandidateBudget
		}
		if budget > pct {
			budget = pct
		}

		if budget <= remaining {
			// The budget fits within the parent's remaining time — attach directly.
			return context.WithTimeout(ctx, budget)
		}
		// The floor raised budget above the parent's remaining time. Detach from the
		// parent deadline so this candidate gets its full floor budget, but preserve
		// user cancellation via AfterFunc bridge (same pattern as exhausted-parent).
	} else {
		budget = pct
	}

	// Parent has no deadline, is already exhausted, or the floor raised the budget
	// above what remains — give a fresh budget detached from the parent deadline.
	// We detach from the expired/short parent deadline by starting from WithoutCancel,
	// then bridge user cancellation manually via context.AfterFunc.
	base := context.WithoutCancel(ctx)
	attemptCtx, attemptCancel = context.WithTimeout(base, budget)

	// Bridge: if the original ctx is canceled by the user, cancel attemptCtx too.
	stopBridge := context.AfterFunc(ctx, attemptCancel)
	outerCancel := attemptCancel
	attemptCancel = func() {
		stopBridge()
		outerCancel()
	}
	return attemptCtx, attemptCancel
}

// ResolveCandidates parses model config into a deduplicated candidate list.
func ResolveCandidates(cfg ModelConfig, defaultProvider string) []FallbackCandidate {
	return ResolveCandidatesWithLookup(cfg, defaultProvider, nil)
}

func ResolveCandidatesWithLookup(
	cfg ModelConfig,
	defaultProvider string,
	lookup func(raw string) (resolved string, ok bool),
) []FallbackCandidate {
	seen := make(map[string]bool)
	var candidates []FallbackCandidate

	addCandidate := func(raw string, kind string) {
		candidateRaw := strings.TrimSpace(raw)
		if lookup != nil {
			if resolved, ok := lookup(candidateRaw); ok {
				candidateRaw = resolved
			} else if kind == "fallback" && candidateRaw != "" && !strings.Contains(candidateRaw, "/") {
				// Lookup didn't recognize this fallback slug and it has no
				// provider prefix to anchor on — the next LLM call will
				// likely fail with a confusing 404 unless the defaultProvider
				// is a passthrough that accepts arbitrary slugs. Emit a WARN
				// so an operator auditing the config load can spot the typo
				// before it bites at runtime. Without this, a typo'd fallback
				// silently routes through defaultProvider and the agent loop
				// surfaces the failure several stack frames later with no
				// breadcrumb pointing to the bad config value
				// (silent-failure-A #4). Scoped to fallbacks only — the
				// primary is the operator's deliberate choice and shouldn't
				// spam the log on every miss.
				logger.WarnCF(
					"providers.fallback",
					"addCandidate: lookup did not resolve fallback slug; using defaultProvider passthrough",
					map[string]any{
						"raw":             candidateRaw,
						"defaultProvider": defaultProvider,
					},
				)
			}
		}

		ref := ParseModelRef(candidateRaw, defaultProvider)
		if ref == nil {
			return
		}
		key := ModelKey(ref.Provider, ref.Model)
		if seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, FallbackCandidate{
			Provider: ref.Provider,
			Model:    ref.Model,
		})
	}

	// Primary first.
	addCandidate(cfg.Primary, "primary")

	// Then fallbacks.
	for _, fb := range cfg.Fallbacks {
		addCandidate(fb, "fallback")
	}

	return candidates
}

// Execute runs the fallback chain for text/chat requests.
// It tries each candidate in order, respecting cooldowns and error classification.
//
// Behavior:
//   - Candidates in cooldown are skipped (logged as skipped attempt).
//   - context.Canceled on the original ctx aborts immediately (user abort, no fallback).
//   - Non-retriable errors (format) abort immediately.
//   - Retriable errors (including per-candidate DeadlineExceeded) trigger fallback.
//   - Success marks provider as good (resets cooldown).
//   - If all fail, returns aggregate error with all attempts.
//
// When the chain was built with NewFallbackChainWithTimeout, each candidate
// receives its own fresh deadline so an exhausted parent context deadline (from
// the primary timing out) does not kill fallback candidates in ~1ms.
func (fc *FallbackChain) Execute(
	ctx context.Context,
	candidates []FallbackCandidate,
	run func(ctx context.Context, provider, model string) (*LLMResponse, error),
) (*FallbackResult, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("fallback: no candidates configured")
	}

	result := &FallbackResult{
		Attempts: make([]FallbackAttempt, 0, len(candidates)),
	}

	// uncooledLeft tracks candidates not yet skipped due to cooldown, used to
	// compute fair per-candidate budget splits.
	uncooledLeft := len(candidates)

	for i, candidate := range candidates {
		// Check original context for user cancellation before each attempt.
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, context.Canceled
		}

		// Check cooldown (per provider/model, not just provider).
		// This allows multi-key failover where different keys use different model names.
		cooldownKey := ModelKey(candidate.Provider, candidate.Model)
		if !fc.cooldown.IsAvailable(cooldownKey) {
			uncooledLeft--
			remaining := fc.cooldown.CooldownRemaining(cooldownKey)
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Skipped:  true,
				Reason:   FailoverRateLimit,
				Error: fmt.Errorf(
					"%s in cooldown (%s remaining)",
					cooldownKey,
					remaining.Round(time.Second),
				),
			})
			continue
		}

		// Give each candidate its own budget so an exhausted parent deadline does
		// not cause fallback candidates to return DeadlineExceeded in ~1ms.
		left := uncooledLeft
		if left < 1 {
			left = 1
		}
		attemptCtx, attemptCancel := fc.candidateBudget(ctx, left)
		uncooledLeft--

		// Execute the run function with the per-candidate context.
		start := time.Now()
		resp, err := run(attemptCtx, candidate.Provider, candidate.Model)
		elapsed := time.Since(start)
		attemptCancel() // release resources immediately; do not defer inside the loop

		if err == nil {
			// Success.
			fc.cooldown.MarkSuccess(cooldownKey)
			result.Response = resp
			result.Provider = candidate.Provider
			result.Model = candidate.Model
			return result, nil
		}

		// User abort on the ORIGINAL ctx: abort immediately, no fallback.
		if errors.Is(ctx.Err(), context.Canceled) {
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Error:    err,
				Duration: elapsed,
			})
			return nil, context.Canceled
		}

		// Classify the error.
		failErr := ClassifyError(err, candidate.Provider, candidate.Model)

		if failErr == nil {
			// Unclassifiable error: do not fallback, return immediately.
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Error:    err,
				Duration: elapsed,
			})
			return nil, fmt.Errorf("fallback: unclassified error from %s/%s: %w",
				candidate.Provider, candidate.Model, err)
		}

		// Non-retriable error: abort immediately.
		if !failErr.IsRetriable() {
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Error:    failErr,
				Reason:   failErr.Reason,
				Duration: elapsed,
			})
			return nil, failErr
		}

		// Retriable error: mark failure and continue to next candidate.
		fc.cooldown.MarkFailure(cooldownKey, failErr.Reason)
		result.Attempts = append(result.Attempts, FallbackAttempt{
			Provider: candidate.Provider,
			Model:    candidate.Model,
			Error:    failErr,
			Reason:   failErr.Reason,
			Duration: elapsed,
		})

		// If this was the last candidate, return aggregate error.
		if i == len(candidates)-1 {
			return nil, &FallbackExhaustedError{Attempts: result.Attempts}
		}
	}

	// All candidates were skipped (all in cooldown).
	return nil, &FallbackExhaustedError{Attempts: result.Attempts}
}

// ExecuteImage runs the fallback chain for image/vision requests.
// Simpler than Execute: no cooldown checks (image endpoints have different rate limits).
// Image dimension/size errors abort immediately (non-retriable).
//
// When the chain was built with NewFallbackChainWithTimeout, each candidate
// receives its own fresh deadline (same logic as Execute).
func (fc *FallbackChain) ExecuteImage(
	ctx context.Context,
	candidates []FallbackCandidate,
	run func(ctx context.Context, provider, model string) (*LLMResponse, error),
) (*FallbackResult, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("image fallback: no candidates configured")
	}

	result := &FallbackResult{
		Attempts: make([]FallbackAttempt, 0, len(candidates)),
	}

	remaining := len(candidates)

	for i, candidate := range candidates {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, context.Canceled
		}

		left := remaining
		if left < 1 {
			left = 1
		}
		attemptCtx, attemptCancel := fc.candidateBudget(ctx, left)
		remaining--

		start := time.Now()
		resp, err := run(attemptCtx, candidate.Provider, candidate.Model)
		elapsed := time.Since(start)
		attemptCancel() // release resources immediately; do not defer inside the loop

		if err == nil {
			result.Response = resp
			result.Provider = candidate.Provider
			result.Model = candidate.Model
			return result, nil
		}

		// User abort on the ORIGINAL ctx: abort immediately, no fallback.
		if errors.Is(ctx.Err(), context.Canceled) {
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Error:    err,
				Duration: elapsed,
			})
			return nil, context.Canceled
		}

		// Image dimension/size errors are non-retriable.
		errMsg := strings.ToLower(err.Error())
		if IsImageDimensionError(errMsg) || IsImageSizeError(errMsg) {
			result.Attempts = append(result.Attempts, FallbackAttempt{
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Error:    err,
				Reason:   FailoverFormat,
				Duration: elapsed,
			})
			return nil, &FailoverError{
				Reason:   FailoverFormat,
				Provider: candidate.Provider,
				Model:    candidate.Model,
				Wrapped:  err,
			}
		}

		// Any other error: record and try next.
		result.Attempts = append(result.Attempts, FallbackAttempt{
			Provider: candidate.Provider,
			Model:    candidate.Model,
			Error:    err,
			Duration: elapsed,
		})

		if i == len(candidates)-1 {
			return nil, &FallbackExhaustedError{Attempts: result.Attempts}
		}
	}

	return nil, &FallbackExhaustedError{Attempts: result.Attempts}
}

// FallbackExhaustedError indicates all fallback candidates were tried and failed.
type FallbackExhaustedError struct {
	Attempts []FallbackAttempt
}

func (e *FallbackExhaustedError) Error() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("fallback: all %d candidates failed:", len(e.Attempts)))
	for i, a := range e.Attempts {
		if a.Skipped {
			sb.WriteString(fmt.Sprintf("\n  [%d] %s/%s: skipped (cooldown)", i+1, a.Provider, a.Model))
		} else {
			sb.WriteString(fmt.Sprintf("\n  [%d] %s/%s: %v (reason=%s, %s)",
				i+1, a.Provider, a.Model, a.Error, a.Reason, a.Duration.Round(time.Millisecond)))
		}
	}
	return sb.String()
}
