// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"sync"
	"time"
)

// Rate limit for show_to_user (founder Q1, D-limit): 5 calls per minute per
// chat, sliding window.
const (
	showToUserRateLimit  = 5
	showToUserRateWindow = time.Minute
	// showToUserSweepAt bounds the map: once it holds this many chats, the
	// next Allow drops every chat whose window has emptied.
	showToUserSweepAt = 1024
)

// ShowToUserRateLimiter is the one budget every show_to_user instance shares
// (D-limit). It is keyed by the ROOT chat session id, not by agent or tool
// instance: tools are built per agent, so a per-instance counter would give
// each agent its own five calls in the same chat (BS-6.13). The counter is in
// memory and resets on process restart.
type ShowToUserRateLimiter struct {
	mu   sync.Mutex
	now  func() time.Time
	hits map[string][]time.Time
}

// NewShowToUserRateLimiter builds a limiter on the given clock; nil means
// time.Now. Tests pass a fake clock instead of sleeping.
func NewShowToUserRateLimiter(now func() time.Time) *ShowToUserRateLimiter {
	if now == nil {
		now = time.Now
	}
	return &ShowToUserRateLimiter{now: now, hits: make(map[string][]time.Time)}
}

// defaultShowToUserLimiter is the process-wide budget every production
// show_to_user instance receives.
var defaultShowToUserLimiter = NewShowToUserRateLimiter(nil)

// DefaultShowToUserRateLimiter returns the process-wide limiter.
func DefaultShowToUserRateLimiter() *ShowToUserRateLimiter { return defaultShowToUserLimiter }

// Allow records one call for rootSessionID and reports whether it fits the
// budget: at most 5 calls in any trailing one-minute window. A refused call is
// not recorded, so it does not extend the wait. An empty key is refused.
func (l *ShowToUserRateLimiter) Allow(rootSessionID string) bool {
	if l == nil || rootSessionID == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-showToUserRateWindow)
	if len(l.hits) >= showToUserSweepAt {
		for key, times := range l.hits {
			if len(times) == 0 || !times[len(times)-1].After(cutoff) {
				delete(l.hits, key)
			}
		}
	}
	kept := l.hits[rootSessionID][:0]
	for _, at := range l.hits[rootSessionID] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= showToUserRateLimit {
		l.hits[rootSessionID] = kept
		return false
	}
	l.hits[rootSessionID] = append(kept, now)
	return true
}
