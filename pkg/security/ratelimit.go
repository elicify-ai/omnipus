// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file implements per-agent and per-channel sliding-window rate limits
// (US-13) from the Wave 2 security spec.
//
// ADR-053 D12 ("no money caps") retires the SEC-26 global daily USD cost cap
// (formerly CheckGlobalCostCap / RecordSpend / SetDailyCostCap / GetDailyCost
// here). The sliding-window rate limits (LLM calls per agent per hour, tool
// calls per agent per minute) remain.

package security

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// IsPrivilegedAgent reports whether agentType represents a privileged agent
// (type "core") that is exempt from the SEC-26 per-agent sliding-window rate
// limits. Privileges flow from agent type, not from a hardcoded agent ID
// (FR-045).
//
// ADR-049 D3 / CRIT-002 (Planning & Goals epic): narrowed from
// `core || system` to `core` only. System Agents (e.g. the seeded Judge) are
// deliberately NON-privileged — the Judge makes out-of-turn internal-LLM
// calls that MUST be metered and rate-limited like any other non-core agent
// so a runaway goal/plan loop cannot spend without bound. The two remaining
// SEC-26 sliding-window gates (pkg/agent/loop.go LLM/hr and tool/min) thread
// the resolved AgentType through this predicate.
//
// Note: the SEC-26 daily USD cost cap that this predicate also gated has been
// retired per ADR-053 D12. This predicate now applies only to sliding-window
// rate limits.
func IsPrivilegedAgent(agentType string) bool {
	return agentType == "core"
}

// RateLimitScope identifies the scope of a rate limit.
type RateLimitScope string

const (
	// ScopeAgent is a per-agent sliding window limit.
	ScopeAgent RateLimitScope = "agent"
	// ScopeChannel is a per-channel outbound message limit.
	ScopeChannel RateLimitScope = "channel"
)

// RateLimitResult is returned by all rate limit checks.
type RateLimitResult struct {
	Allowed           bool
	RetryAfterSeconds float64 // > 0 when Allowed is false
	PolicyRule        string  // explains which limit was hit
}

// SlidingWindow is a thread-safe sliding window counter for rate limiting.
// It tracks a (scope, scopeID, resource) triple and enforces a maximum number
// of events within a rolling time window.
type SlidingWindow struct {
	mu         sync.Mutex
	events     []time.Time
	maxCount   int
	windowSize time.Duration
	scope      RateLimitScope
	scopeID    string
	resource   string
}

// NewSlidingWindow creates a SlidingWindow that allows at most limit events
// within window. A limit of 0 or less means unlimited: every Allow is allowed
// and is still recorded, so a later positive limit counts those events.
// scope, scopeID, and resource are used to construct policy rule messages in
// denied results.
func NewSlidingWindow(limit int, window time.Duration, scope RateLimitScope, scopeID, resource string) *SlidingWindow {
	// limit+1 is the historical prealloc. A negative limit makes that capacity
	// negative and panics in make; start empty and let append grow.
	capacity := 0
	if limit >= 0 {
		capacity = limit + 1
	}
	return &SlidingWindow{
		events:     make([]time.Time, 0, capacity),
		maxCount:   limit,
		windowSize: window,
		scope:      scope,
		scopeID:    scopeID,
		resource:   resource,
	}
}

// Allow records an event and reports whether it is within the limit.
// If denied, RetryAfterSeconds indicates when the next slot opens.
func (sw *SlidingWindow) Allow() RateLimitResult {
	return sw.allowAt(time.Now())
}

// allowAt is the internal implementation, accepting a clock value for testability.
func (sw *SlidingWindow) allowAt(now time.Time) RateLimitResult {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	cutoff := now.Add(-sw.windowSize)
	// Prune expired events from the front
	i := 0
	for i < len(sw.events) && sw.events[i].Before(cutoff) {
		i++
	}
	if i > 0 {
		sw.events = sw.events[i:]
	}

	// A limit of 0 or less means unlimited (docs/settings.md: enter 0 for
	// no limit; the contracts say "0 = unlimited"). Record the event: a
	// later raise to a positive limit must count calls that happened while
	// unlimited, the same way events already in the window are kept.
	// maxCount is read only under sw.mu, the same lock setLimit writes it under.
	if sw.maxCount <= 0 || len(sw.events) < sw.maxCount {
		sw.events = append(sw.events, now)
		return RateLimitResult{Allowed: true}
	}

	// Window is full — compute when the oldest event expires
	oldest := sw.events[0]
	retryAt := oldest.Add(sw.windowSize)
	retryAfter := retryAt.Sub(now).Seconds()
	if retryAfter < 0.001 {
		retryAfter = 0.001
	}

	rule := fmt.Sprintf("rate_limit: %s limit %d/%s exceeded for %s %q",
		sw.resource, sw.maxCount, sw.windowSize, sw.scope, sw.scopeID)

	slog.Info("ratelimit: sliding window rejected",
		"scope", sw.scope,
		"scope_id", sw.scopeID,
		"resource", sw.resource,
		"retry_after_seconds", retryAfter,
	)

	return RateLimitResult{
		Allowed:           false,
		RetryAfterSeconds: retryAfter,
		PolicyRule:        rule,
	}
}

// --------------------------------------------------------------------------
// RateLimiterRegistry — manages named SlidingWindows
// --------------------------------------------------------------------------

// RateLimiterRegistry manages per-agent and per-channel sliding windows.
// All counters are in-memory and reset on restart.
//
// ADR-053 D12: the SEC-26 global daily USD cost cap that this registry used
// to enforce (SetDailyCostCap / CheckGlobalCostCap / RecordSpend /
// GetDailyCost) has been retired. This registry now hosts ONLY the per-agent
// sliding-window rate limits (LLM calls per hour, tool calls per minute).
type RateLimiterRegistry struct {
	mu      sync.RWMutex
	windows map[string]*SlidingWindow
}

// NewRateLimiterRegistry creates an empty registry.
func NewRateLimiterRegistry() *RateLimiterRegistry {
	return &RateLimiterRegistry{
		windows: make(map[string]*SlidingWindow),
	}
}

// setLimit applies a changed limit to a live window (#1882). It takes the
// same lock as allowAt, so the limit and the recorded events change together
// and a concurrent Allow sees either the old or the new limit, never a mix.
// Events already recorded are kept. Raising to a higher positive limit lets
// the next call through once the kept count is under it; lowering to a smaller
// positive limit refuses calls until enough of them age out. A limit of 0 or
// less means unlimited (allowAt); calls made then are still recorded, so
// raising back to a positive limit counts them.
func (sw *SlidingWindow) setLimit(limit int) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.maxCount = limit
}

// GetOrCreate returns the SlidingWindow for the given key, creating it with
// the supplied limit and window duration if it does not yet exist. An
// existing window takes the supplied limit (#1882): callers pass the limit
// configured now, so a limit changed in Settings applies to the running
// limiter without a restart and without resetting the counts already used.
func (r *RateLimiterRegistry) GetOrCreate(
	key string,
	limit int,
	window time.Duration,
	scope RateLimitScope,
	scopeID, resource string,
) *SlidingWindow {
	r.mu.RLock()
	sw, ok := r.windows[key]
	r.mu.RUnlock()
	if ok {
		sw.setLimit(limit)
		return sw
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if sw, ok = r.windows[key]; ok {
		sw.setLimit(limit)
		return sw
	}
	sw = NewSlidingWindow(limit, window, scope, scopeID, resource)
	r.windows[key] = sw
	return sw
}
