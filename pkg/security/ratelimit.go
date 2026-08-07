// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file implements per-agent and per-channel sliding-window rate limits
// (US-13) from the Wave 2 security spec.
//
// ADR-053 D12 ("no money caps") retires the SEC-26 global daily USD cost cap
// (formerly CheckGlobalCostCap / RecordSpend / SetDailyCostCap / GetDailyCost
// here) — the only app-level spend brake is now TokenBudget
// (pkg/agent/budget.go). The sliding-window rate limits (LLM calls per agent
// per hour, tool calls per agent per minute) remain.

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
// retired per ADR-053 D12 (token budget is the sole spend brake). This
// predicate now applies only to sliding-window rate limits.
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
// within window. scope, scopeID, and resource are used to construct policy rule
// messages in denied results.
func NewSlidingWindow(limit int, window time.Duration, scope RateLimitScope, scopeID, resource string) *SlidingWindow {
	return &SlidingWindow{
		events:     make([]time.Time, 0, limit+1),
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

	if len(sw.events) < sw.maxCount {
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
// GetDailyCost) has been retired — the only app-level spend brake is now
// pkg/agent.TokenBudget. This registry now hosts ONLY the per-agent
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

// GetOrCreate returns the SlidingWindow for the given key, creating it with
// the supplied limit and window duration if it does not yet exist.
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
		return sw
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if sw, ok = r.windows[key]; ok {
		return sw
	}
	sw = NewSlidingWindow(limit, window, scope, scopeID, resource)
	r.windows[key] = sw
	return sw
}
