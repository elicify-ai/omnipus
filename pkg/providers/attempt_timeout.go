package providers

// Issue #1485 — per-attempt limit of a fallback-chain call.
//
// The chain gives every candidate attempt its own wall-clock limit
// (FallbackChain.candidateBudget). Until #1485 that limit was one fixed value
// for every candidate and every request (120 s by default), so a very large
// prompt that needed longer than 120 s to answer was cut on every candidate
// and the user saw minutes of silence followed by a generic failure.
//
// Now the caller can attach an AttemptTimeoutFunc to the chain ctx. The chain
// asks it once per attempt, with the candidate actually being called, and
// uses a positive answer as that attempt's limit instead of the chain's
// static cap. The agent loop answers with:
//
//  1. the model's explicit request_timeout when the operator set one
//     (RequestTimeoutReporter) — a deliberate per-model ceiling, honoured
//     both ways (longer or shorter than the default);
//  2. otherwise ScaledAttemptTimeout(estimated input tokens).
//
// The streaming silence limit (common.DefaultStreamStallTimeout, per model
// stream_stall_timeout) is unchanged and independent of this.

import (
	"context"
	"time"
)

const (
	// attemptTimeoutTokensPerStep is the request size the base limit covers,
	// and the size of each further block that adds attemptTimeoutStep.
	attemptTimeoutTokensPerStep = 100_000
	// attemptTimeoutStep is added for every further started block.
	attemptTimeoutStep = 60 * time.Second
	// attemptTimeoutCeiling bounds the scaled default: a silent provider must
	// still hand over to the next candidate in reasonable time.
	attemptTimeoutCeiling = 10 * time.Minute
)

// AttemptTimeoutFunc resolves the per-attempt limit for one candidate. A
// non-positive answer means "no opinion": the chain's static cap applies.
type AttemptTimeoutFunc func(provider, model string) time.Duration

type attemptTimeoutKey struct{}

// WithAttemptTimeout attaches fn to ctx for one chain Execute call.
func WithAttemptTimeout(ctx context.Context, fn AttemptTimeoutFunc) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, attemptTimeoutKey{}, fn)
}

func attemptTimeoutFrom(ctx context.Context) AttemptTimeoutFunc {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(attemptTimeoutKey{}).(AttemptTimeoutFunc)
	return fn
}

// ScaledAttemptTimeout is the size-aware default per-attempt limit: the
// shipped 120 s covers requests up to 100,000 estimated input tokens, each
// further started 100,000 tokens adds 60 s, and the result never exceeds
// 10 minutes. A 439,000-token request (the #1485 incident) gets 360 s.
func ScaledAttemptTimeout(estimatedInputTokens int) time.Duration {
	if estimatedInputTokens <= attemptTimeoutTokensPerStep {
		return defaultPerCandidateTimeout
	}
	extra := estimatedInputTokens - attemptTimeoutTokensPerStep
	steps := (extra + attemptTimeoutTokensPerStep - 1) / attemptTimeoutTokensPerStep
	// Bound steps before multiplying so a huge estimate cannot overflow.
	maxSteps := int((attemptTimeoutCeiling - defaultPerCandidateTimeout) / attemptTimeoutStep)
	if steps >= maxSteps {
		return attemptTimeoutCeiling
	}
	return defaultPerCandidateTimeout + time.Duration(steps)*attemptTimeoutStep
}

// RequestTimeoutReporter is implemented by providers that know whether the
// operator configured an explicit request_timeout for them. A zero answer
// means "not configured — the shipped default applies".
type RequestTimeoutReporter interface {
	ExplicitRequestTimeout() time.Duration
}
