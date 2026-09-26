// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// fallback_retry.go — provider-messages spec §7.4: the per-candidate retry
// loop's policy brain (C-8/C-9/C-12/D14). The retry loop itself lives in
// fallback.go::Execute; this file holds the wait policy, the ctx-carried
// retry observer, and the per-turn wait budget.
package providers

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Retry loop constants (spec §7.4).
const (
	// maxAttemptsPerCandidate is the C-8 attempt cap: 3 TOTAL calls
	// including the first (attempt numbers 2..3 are the retries).
	maxAttemptsPerCandidate = 3

	// retryAfterCeiling is the C-9 ceiling: a Retry-After at or below this
	// is honored exactly; above it the candidate is skipped to fallback
	// with NO cooldown mark (A-4).
	retryAfterCeiling = 120 * time.Second

	// retryBackoffBase and retryBackoffCap shape the standard backoff for
	// absent/zero/malformed retry-after: 2 s x 2^(n-1) capped at 30 s,
	// with +/-25% jitter. First step: 1.5-2.5 s.
	retryBackoffBase = 2 * time.Second
	retryBackoffCap  = 30 * time.Second

	// MaxWaitBudgetPerTurn is the D14 per-turn total-wait cap. It is
	// checked BEFORE each wait is scheduled (all-or-nothing Reserve) so a
	// wait is either waited in full or never started — never truncated.
	MaxWaitBudgetPerTurn = 10 * time.Minute
)

// RetryInfo carries one "about to re-call this candidate" decision from the
// chain to the RetryObserver (C-8/C-11): the failing attempt's identity,
// the attempt number of the call ABOUT to be made, the per-candidate cap,
// and the wall-clock facts the provider_retry frame renders.
type RetryInfo struct {
	Provider    string
	Model       string
	Attempt     int       // the call about to be made (2..3)
	MaxAttempts int       // maxAttemptsPerCandidate
	Reason      string    // failover reason of the failure being retried
	SentAt      time.Time // server wall clock at emission
	RetryAt     time.Time // SentAt + the full wait
}

// RetryObserver receives one RetryInfo per DECIDED in-place retry — fired
// before the wait, never after the wait was refused by the D14 budget or
// skipped by the C-9 ceiling (no retry will happen; nothing to observe).
type RetryObserver func(RetryInfo)

// retryWait is one decided wait: duration plus kind.
type retryWait struct {
	duration time.Duration
	honored  bool // true = the provider's Retry-After, waited exactly
}

// nextRetryWait decides the wait after a rate-limit-class failure.
// retryAfterSeconds is the boundary-parsed value (0 = absent/malformed/
// past): in [1,120] it is honored exactly (no jitter); 0 takes the
// standard backoff 2 s x 2^(attemptNo-1) capped at 30 s with +/-25%
// jitter. attemptNo is the number of the call that JUST failed (1-based).
// Values above the ceiling never reach here — the caller skips to
// fallback (A-4) without a wait.
func nextRetryWait(attemptNo int, retryAfterSeconds int, jitter float64) retryWait {
	if retryAfterSeconds > 0 {
		return retryWait{duration: time.Duration(retryAfterSeconds) * time.Second, honored: true}
	}
	base := retryBackoffBase << (attemptNo - 1)
	if base > retryBackoffCap || base <= 0 {
		base = retryBackoffCap
	}
	wait := base + time.Duration(float64(base)*0.25*(2*jitter-1))
	if wait < 0 {
		wait = 0
	}
	return retryWait{duration: wait, honored: false}
}

// retryAfterOf reads the Retry-After seconds the parse layer (§7.2)
// captured on the failing error's chain: 0 when absent/unparseable/past.
// The chain never re-parses headers — the boundary owns the facts.
func retryAfterOf(err error) int {
	if err == nil {
		return 0
	}
	var pe *common.ProviderError
	if errors.As(err, &pe) {
		return pe.RetryAfterSeconds
	}
	return 0
}

// retryJitter is the jitter source for backoff waits (returns [0,1)).
// Package-level so the same-package D14 test can pin it deterministically.
var retryJitter = rand.Float64

// --- ctx-carried retry observer and per-turn wait budget -------------------

type retryObserverKey struct{}
type waitBudgetKey struct{}

// WithRetryObserver attaches obs to ctx for one chain Execute call; the
// chain fires it per decided in-place retry (see RetryObserver).
func WithRetryObserver(ctx context.Context, obs RetryObserver) context.Context {
	return context.WithValue(ctx, retryObserverKey{}, obs)
}

func retryObserverFrom(ctx context.Context) RetryObserver {
	if ctx == nil {
		return nil
	}
	obs, _ := ctx.Value(retryObserverKey{}).(RetryObserver)
	return obs
}

// WaitBudget is the D14 per-turn total-wait cap, carried on the turn's
// context. Reserve is ALL-OR-NOTHING: a wait is admitted only when the
// budget can cover it in full — a wait is never truncated mid-way.
type WaitBudget struct {
	mu   sync.Mutex
	left time.Duration
}

// NewWaitBudget returns a budget of d. The agent loop attaches one per
// turn (maxWaitBudgetPerTurn).
func NewWaitBudget(d time.Duration) *WaitBudget {
	return &WaitBudget{left: d}
}

// Reserve atomically admits a wait of d: true when the remaining budget
// covered it in full (deducted), false when it did not (nothing taken —
// the candidate is skipped instead of waiting a truncated slice).
func (b *WaitBudget) Reserve(d time.Duration) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if d > b.left {
		return false
	}
	b.left -= d
	return true
}

// Remaining reports the unused budget (diagnostics/tests).
func (b *WaitBudget) Remaining() time.Duration {
	if b == nil {
		return MaxWaitBudgetPerTurn
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.left
}

// WithWaitBudget attaches b to ctx for one chain Execute call.
func WithWaitBudget(ctx context.Context, b *WaitBudget) context.Context {
	return context.WithValue(ctx, waitBudgetKey{}, b)
}

func waitBudgetFrom(ctx context.Context) *WaitBudget {
	if ctx == nil {
		return nil
	}
	b, _ := ctx.Value(waitBudgetKey{}).(*WaitBudget)
	return b
}
