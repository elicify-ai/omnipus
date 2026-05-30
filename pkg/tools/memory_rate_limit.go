// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package tools — memory-write rate limiter (v0.2 #155 item 6).
//
// Threat model:
//
//	An attacker who has successfully prompt-injected one agent can call
//	`remember` (or `retrospective`) in a tight loop to:
//	  1. Fill the disk with junk MEMORY.md entries (DoS via disk pressure)
//	  2. Pollute the recall corpus so unrelated agents return tainted hits
//	  3. Burn LLM dollars indirectly (every remember call ends a turn cleanly,
//	     but the next turn still costs a request)
//
//	The defense is a sliding-window rate limiter at the tool boundary, with
//	two scopes:
//
//	  - per-agent: 60 writes/min default — narrow blast radius, one
//	    compromised agent can't DoS the others.
//	  - per-caller: 600 writes/min default — looser ceiling on the originating
//	    HTTP/WS client (one IP may host multiple agents, e.g. a fanout
//	    workflow). Caller identity comes from the tool execution context
//	    (channel:chat_id pair) — not literal HTTP IP, because tool calls run
//	    in the agent loop, not the request handler. See callerIdentity below.
//
//	Both buckets must allow the request; if either is exhausted, the call
//	is rejected with `IsError: true`, an error_kind of "rate_limited", a
//	Retry-After hint encoded in the message, and an audit entry recording
//	the deny decision.
//
// Algorithm:
//
//	Sliding-window counter (same shape as gateway/rest_auth.go's
//	apiRateLimiter). Each bucket keeps a slice of timestamps of allowed
//	requests; on each call we evict timestamps older than `window`, then
//	check if the remaining count is < limit. We deliberately match the
//	gateway implementation byte-for-byte for the hot path so the two
//	limiters share the same operational characteristics: O(N) eviction
//	in the worst case but amortized O(1) under steady state.
//
// Concurrency:
//
//	A single sync.Mutex per limiter (not per bucket) — buckets are short
//	slices and the lock window is microseconds. A sharded map would only
//	pay off at >>1k QPS per limiter; we are nowhere near that. The lock
//	guards both `agents` and `callers` maps.
//
// Lifecycle:
//
//	The agent loop owns one MemoryRateLimiter and propagates it to the
//	tool registry via SetMemoryRateLimiter, mirroring the SetAuditLogger
//	wire-up. RememberTool and RetrospectiveTool implement
//	memoryRateLimiterAware (see registry.go) and pick it up at registration
//	time. A nil limiter (test default) bypasses the gate entirely — same
//	convention as nil auditLogger.

package tools

import (
	"sync"
	"time"
)

// MemoryRateLimitDecision is the outcome of a single rate-limit check.
//
// Allowed=true: the request is within both buckets and may proceed. Other
// fields are zero/empty.
//
// Allowed=false: the request was rejected. RetryAfter is the time the
// caller should wait before retrying (the conservative max of the two
// buckets' wait times). Scope is the bucket that tripped first
// ("agent" or "caller"); when both are exhausted simultaneously, "agent"
// wins because it carries the higher-blast-radius signal.
type MemoryRateLimitDecision struct {
	Allowed    bool
	RetryAfter time.Duration
	Scope      string
}

// MemoryRateLimitConfig configures a MemoryRateLimiter at construction.
// Zero values fall back to documented defaults so a misconfigured caller
// still gets a working limiter rather than a no-op.
type MemoryRateLimitConfig struct {
	// PerAgentLimit is the max writes per agent in Window. Default 60.
	PerAgentLimit int
	// PerCallerLimit is the max writes per caller (channel:chat_id) in
	// Window. Default 600.
	PerCallerLimit int
	// Window is the sliding window duration. Default 1 minute.
	Window time.Duration
}

// MemoryRateLimiter rate-limits memory-write tool calls (remember,
// retrospective) per-agent and per-caller. The zero value is unsafe —
// always use NewMemoryRateLimiter or pass nil to bypass the gate.
type MemoryRateLimiter struct {
	mu             sync.Mutex
	agents         map[string]*memoryRateBucket
	callers        map[string]*memoryRateBucket
	perAgentLimit  int
	perCallerLimit int
	window         time.Duration
	// nowFn is overridable for deterministic tests. Production callers leave
	// it nil; we resolve to time.Now at call time.
	nowFn func() time.Time
}

// memoryRateBucket holds the timestamps of allowed requests within the
// current window. Slice grows up to `limit` then evicts on each call.
type memoryRateBucket struct {
	timestamps []time.Time
}

// NewMemoryRateLimiter constructs a MemoryRateLimiter from the given
// config, applying defaults for any zero fields.
func NewMemoryRateLimiter(cfg MemoryRateLimitConfig) *MemoryRateLimiter {
	if cfg.PerAgentLimit <= 0 {
		cfg.PerAgentLimit = 60
	}
	if cfg.PerCallerLimit <= 0 {
		cfg.PerCallerLimit = 600
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Minute
	}
	return &MemoryRateLimiter{
		agents:         make(map[string]*memoryRateBucket),
		callers:        make(map[string]*memoryRateBucket),
		perAgentLimit:  cfg.PerAgentLimit,
		perCallerLimit: cfg.PerCallerLimit,
		window:         cfg.Window,
	}
}

// PerAgentLimit returns the configured per-agent ceiling. Exposed so the
// caller can include it in error messages and audit entries.
func (l *MemoryRateLimiter) PerAgentLimit() int { return l.perAgentLimit }

// PerCallerLimit returns the configured per-caller ceiling.
func (l *MemoryRateLimiter) PerCallerLimit() int { return l.perCallerLimit }

// Window returns the configured window duration.
func (l *MemoryRateLimiter) Window() time.Duration { return l.window }

// Allow checks both the per-agent and per-caller buckets. Returns the
// MemoryRateLimitDecision with Allowed=true on success; Allowed=false plus
// RetryAfter+Scope on failure. The check is atomic across the two buckets
// — if either is exhausted we DO NOT consume a slot from the other. This
// preserves the contract that "60 per agent and 600 per caller" — a
// rejected call should not eat the agent's budget just because the caller
// budget is full.
//
// agentID may be "" (e.g. system tools without a calling agent). An empty
// agentID is treated as a single shared "anonymous" bucket — this is the
// same fail-closed behavior the gateway uses for anonymous IPs. Same for
// callerID.
func (l *MemoryRateLimiter) Allow(agentID, callerID string) MemoryRateLimitDecision {
	if l == nil {
		// Nil limiter == no rate limiting (test/dev default).
		return MemoryRateLimitDecision{Allowed: true}
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)

	// Resolve buckets, creating empty ones if first contact.
	agentKey := emptyAsAnonymous(agentID)
	callerKey := emptyAsAnonymous(callerID)
	agentBucket := l.bucketLocked(l.agents, agentKey)
	callerBucket := l.bucketLocked(l.callers, callerKey)

	// Evict expired timestamps from BOTH buckets so the over-limit decision
	// reflects the current window for both. Without this we could reject on
	// stale data when one bucket has been idle for >window.
	agentBucket.evictBefore(cutoff)
	callerBucket.evictBefore(cutoff)

	// Per-agent budget gate. Wins over per-caller when both are full.
	if len(agentBucket.timestamps) >= l.perAgentLimit {
		return MemoryRateLimitDecision{
			Allowed:    false,
			RetryAfter: agentBucket.retryAfter(now, l.window),
			Scope:      "agent",
		}
	}
	// Per-caller budget gate.
	if len(callerBucket.timestamps) >= l.perCallerLimit {
		return MemoryRateLimitDecision{
			Allowed:    false,
			RetryAfter: callerBucket.retryAfter(now, l.window),
			Scope:      "caller",
		}
	}

	// Both buckets have room — record the request in BOTH atomically while
	// the lock is still held. This is what makes the two-bucket gate
	// equivalent to a single transactional check.
	agentBucket.timestamps = append(agentBucket.timestamps, now)
	callerBucket.timestamps = append(callerBucket.timestamps, now)
	return MemoryRateLimitDecision{Allowed: true}
}

// bucketLocked returns the bucket for key, creating it if absent. Caller
// must hold l.mu.
func (l *MemoryRateLimiter) bucketLocked(m map[string]*memoryRateBucket, key string) *memoryRateBucket {
	b, ok := m[key]
	if !ok {
		b = &memoryRateBucket{}
		m[key] = b
	}
	return b
}

// now returns the current time, going through nowFn if set (for tests).
func (l *MemoryRateLimiter) now() time.Time {
	if l.nowFn != nil {
		return l.nowFn()
	}
	return time.Now()
}

// evictBefore drops timestamps older than cutoff in-place. The slice's
// underlying array is preserved across calls so steady-state usage avoids
// allocator pressure.
func (b *memoryRateBucket) evictBefore(cutoff time.Time) {
	start := 0
	for start < len(b.timestamps) && b.timestamps[start].Before(cutoff) {
		start++
	}
	b.timestamps = b.timestamps[start:]
}

// retryAfter returns the wait until the oldest timestamp in the bucket
// expires. Caller must have already evicted expired entries (the result is
// nonsense if the oldest entry is already past cutoff). Returns at minimum
// 1 second if a positive wait is computed: HTTP Retry-After is integer
// seconds and 0 would tell the client "retry immediately".
func (b *memoryRateBucket) retryAfter(now time.Time, window time.Duration) time.Duration {
	if len(b.timestamps) == 0 {
		// Should not happen on the over-limit path (bucket has at least
		// `limit` entries), but guard anyway: zero slots used == no wait.
		return 0
	}
	oldest := b.timestamps[0]
	wait := oldest.Add(window).Sub(now)
	if wait <= 0 {
		// Defensive: window expired between evictBefore and retryAfter.
		return time.Second
	}
	if wait < time.Second {
		// Round up to the nearest second so the Retry-After header is
		// always >= 1.
		return time.Second
	}
	return wait
}

// emptyAsAnonymous coerces an empty string to a single sentinel so the
// "" key doesn't accidentally bypass the limit when one caller's identity
// is unset. All anonymous callers share the same bucket — they are
// indistinguishable, so they should compete for the same budget.
func emptyAsAnonymous(s string) string {
	if s == "" {
		return "<anonymous>"
	}
	return s
}
