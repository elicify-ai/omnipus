//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Rate-limit recovery test for the apiRateLimiter sliding window.
//
// BDD Scenario: "Rate-limit bucket recovers after the window expires"
//
// Given N consecutive requests that exhaust the per-IP bucket,
// When the sliding window expires,
// Then the next request is allowed (bucket has recovered).
//
// The audit found "no test proves the bucket recovers" — this closes that gap.
// Traces to: quizzical-marinating-frog.md — Wave V2.G stage 3, item 2 (Rank-9)

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAPIRateLimiter_BucketRecovery verifies that after the sliding window
// expires the rate limiter allows requests again.
//
// BDD: Given a per-IP rate limiter with limit=3, window=200ms,
//
//	When 3 requests exhaust the bucket,
//	Then subsequent requests within the window are rejected (429),
//	And after sleeping past the window, the next request is allowed.
//
// Traces to: quizzical-marinating-frog.md — Wave V2.G stage 3, item 2 (Rank-9)
func TestAPIRateLimiter_BucketRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping recovery test in short mode — requires real time.Sleep")
	}

	const (
		limit  = 3
		window = 200 * time.Millisecond
	)
	limiter := newAPIRateLimiter(limit, window)
	const ip = "10.0.0.1"

	// Step 1: exhaust the bucket with `limit` allowed requests.
	for i := 0; i < limit; i++ {
		allowed := limiter.allow(ip)
		require.True(t, allowed, "request %d/%d should be allowed (bucket not yet exhausted)", i+1, limit)
	}

	// Step 2: the (limit+1)th request must be rejected (bucket is full).
	rejected := limiter.allow(ip)
	assert.False(t, rejected, "request after limit must be rejected (bucket exhausted)")

	// Step 3: inspect Retry-After — must be > 0 so the client knows when to retry.
	retryAfter := limiter.retryAfter(ip)
	assert.Greater(t, retryAfter, 0, "Retry-After must be positive when bucket is exhausted")

	// Step 4: sleep past the window so all timestamps evict.
	time.Sleep(window + 50*time.Millisecond)

	// Step 5: after recovery the next request must be allowed again.
	recovered := limiter.allow(ip)
	assert.True(t, recovered, "bucket must recover after window expires (allow() must return true)")

	// Differentiation: a different IP must never be rate-limited by the first IP's exhaustion.
	otherAllowed := limiter.allow("10.0.0.2")
	assert.True(t, otherAllowed, "different IP must have its own independent bucket")
}

// TestWithRateLimit_Recovery_Returns429WithRetryAfterHeader verifies that the withRateLimit
// middleware correctly returns 429 with a Retry-After header when the bucket is
// exhausted.
//
// BDD: Given a rate limiter with limit=2,
//
//	When 3 requests are made from the same IP,
//	Then the 3rd request returns 429 with a Retry-After header.
//
// Traces to: quizzical-marinating-frog.md — Wave V2.G stage 3, item 2 (Rank-9)
func TestWithRateLimit_Recovery_Returns429WithRetryAfterHeader(t *testing.T) {
	const limit = 2
	limiter := newAPIRateLimiter(limit, 1*time.Minute)

	handler := withRateLimit(limiter, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	const ip = "192.168.0.5:54321"

	// First `limit` requests must pass through (200).
	for i := 0; i < limit; i++ {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
		r.RemoteAddr = ip
		w := httptest.NewRecorder()
		handler(w, r)
		require.Equal(t, http.StatusOK, w.Code, "request %d/%d must pass through", i+1, limit)
	}

	// (limit+1)th request must be rejected with 429 + Retry-After.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	r.RemoteAddr = ip
	w := httptest.NewRecorder()
	handler(w, r)

	assert.Equal(t, http.StatusTooManyRequests, w.Code, "over-limit request must return 429")

	retryAfterHeader := w.Header().Get("Retry-After")
	assert.NotEmpty(t, retryAfterHeader, "429 response must include Retry-After header")
	retryAfterSecs, err := strconv.Atoi(retryAfterHeader)
	require.NoError(t, err, "Retry-After header must be an integer (seconds)")
	assert.Greater(t, retryAfterSecs, 0, "Retry-After must be positive")
}

// TestWithRateLimit_RecoveryAfterWindowExpiry verifies the full
// exhaust → rejected → sleep → recovered lifecycle through the withRateLimit
// HTTP middleware (not just the limiter internals).
//
// BDD: Given a rate limiter with limit=2 and window=150ms,
//
//	When 3 requests exhaust the bucket (3rd returns 429),
//	And the client waits past the window,
//	Then the next request returns 200 again.
//
// Traces to: quizzical-marinating-frog.md — Wave V2.G stage 3, item 2 (Rank-9)
func TestWithRateLimit_RecoveryAfterWindowExpiry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping recovery test in short mode — requires real time.Sleep")
	}

	const (
		limit  = 2
		window = 150 * time.Millisecond
	)
	limiter := newAPIRateLimiter(limit, window)

	called := false
	handler := withRateLimit(limiter, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	const ip = "172.16.0.1:8080"

	// Exhaust the bucket.
	for i := 0; i < limit; i++ {
		r := httptest.NewRequest(http.MethodGet, "/test", nil)
		r.RemoteAddr = ip
		w := httptest.NewRecorder()
		handler(w, r)
		require.Equal(t, http.StatusOK, w.Code)
	}

	// Confirm the bucket is exhausted — next request must be 429.
	called = false
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.RemoteAddr = ip
	w := httptest.NewRecorder()
	handler(w, r)
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "bucket must be exhausted")
	assert.False(t, called, "inner handler must NOT be called when rate limited")

	// Wait for the window to expire.
	time.Sleep(window + 60*time.Millisecond)

	// After recovery: request must succeed and inner handler must be called.
	called = false
	r2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	r2.RemoteAddr = ip
	w2 := httptest.NewRecorder()
	handler(w2, r2)
	assert.Equal(t, http.StatusOK, w2.Code, "bucket must be recovered after window expires")
	assert.True(t, called, "inner handler must be called after recovery")
}
