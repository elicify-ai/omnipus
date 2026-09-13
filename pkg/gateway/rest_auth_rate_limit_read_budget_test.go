package gateway

// UAT 2026-09-13 D-109 (and D-135, the same limiter): ordinary UI work —
// deleting a few dozen files, opening ten base views — tripped the per-IP
// limiter on the LISTING refreshes those actions trigger, because reads and
// writes shared one bucket sized for mutations and pre-auth traffic. These
// tests pin the two-budget rule in withRateLimit: an authenticated GET/HEAD
// draws on a separate, larger read budget; everything else — every write,
// and every request that has not passed authentication — stays on the
// limiter's strict budget; and a refused request always says when to retry.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func rateLimitTestRequest(method, ip string, authenticated bool) *http.Request {
	req := httptest.NewRequest(method, "/api/v1/library/workspaces", nil)
	req.RemoteAddr = ip + ":4242"
	if authenticated {
		user := &config.UserConfig{Username: "tester"}
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey{}, user))
	}
	return req
}

func serveRateLimited(t *testing.T, h http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func assertRefusedWithRetryAfter(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	require.Equal(t, http.StatusTooManyRequests, w.Code, "%s must be refused", what)
	ra, err := strconv.Atoi(w.Header().Get("Retry-After"))
	require.NoError(t, err, "%s: Retry-After must be an integer number of seconds", what)
	assert.GreaterOrEqual(t, ra, 1, "%s: Retry-After must tell the caller when to come back", what)
	assert.Contains(t, w.Body.String(), "retry after")
}

// TestWithRateLimit_AuthenticatedReadsUseSeparateLargerBudget: with a strict
// limit of 3, an authenticated client can issue 3*readBudgetMultiplier GETs
// before the first 429, the writes it interleaves do not eat into that read
// budget (and vice versa), and both refusals carry Retry-After.
func TestWithRateLimit_AuthenticatedReadsUseSeparateLargerBudget(t *testing.T) {
	const strict = 3
	limiter := newAPIRateLimiter(strict, time.Minute)
	calls := 0
	h := withRateLimit(limiter, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})
	const ip = "198.51.100.7"

	// Exhaust the WRITE budget first: the D-109 shape is many deletes.
	for i := 1; i <= strict; i++ {
		w := serveRateLimited(t, h, rateLimitTestRequest(http.MethodDelete, ip, true))
		require.Equal(t, http.StatusOK, w.Code, "write %d of %d must pass", i, strict)
	}
	assertRefusedWithRetryAfter(t, serveRateLimited(t, h, rateLimitTestRequest(http.MethodDelete, ip, true)), "write beyond the strict budget")

	// Listing refreshes AFTER the write budget is gone must still pass: that
	// is the defect — before the fix the 4th request of any kind was a 429.
	readBudget := strict * readBudgetMultiplier
	for i := 1; i <= readBudget; i++ {
		w := serveRateLimited(t, h, rateLimitTestRequest(http.MethodGet, ip, true))
		require.Equal(t, http.StatusOK, w.Code, "authenticated GET %d of %d must pass on the read budget", i, readBudget)
	}
	assertRefusedWithRetryAfter(t, serveRateLimited(t, h, rateLimitTestRequest(http.MethodGet, ip, true)), "read beyond the read budget")
	// HEAD is a read too and shares the (now exhausted) read budget.
	assertRefusedWithRetryAfter(t, serveRateLimited(t, h, rateLimitTestRequest(http.MethodHead, ip, true)), "HEAD beyond the read budget")
	assert.Equal(t, strict+readBudget, calls, "the handler ran exactly once per allowed request")

	// Another client is unaffected by this one's exhaustion.
	w := serveRateLimited(t, h, rateLimitTestRequest(http.MethodGet, "198.51.100.8", true))
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestWithRateLimit_UnauthenticatedReadsStayStrict: the wider read budget
// exists for signed-in UI use only. A GET that has not passed
// authentication (login, onboarding, the pre-auth provider routes) is
// counted against the strict budget exactly as before.
func TestWithRateLimit_UnauthenticatedReadsStayStrict(t *testing.T) {
	const strict = 3
	limiter := newAPIRateLimiter(strict, time.Minute)
	h := withRateLimit(limiter, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	const ip = "203.0.113.9"
	for i := 1; i <= strict; i++ {
		w := serveRateLimited(t, h, rateLimitTestRequest(http.MethodGet, ip, false))
		require.Equal(t, http.StatusOK, w.Code, "anonymous GET %d of %d must pass", i, strict)
	}
	assertRefusedWithRetryAfter(t, serveRateLimited(t, h, rateLimitTestRequest(http.MethodGet, ip, false)), "anonymous GET beyond the strict budget")
	// A write from the same anonymous client shares that strict bucket.
	assertRefusedWithRetryAfter(t, serveRateLimited(t, h, rateLimitTestRequest(http.MethodPost, ip, false)), "anonymous POST after the strict budget is gone")
}

// TestWithRateLimit_WritesNeverBorrowTheReadBudget: an authenticated client
// that has used up its strict budget with writes cannot get a fourth write
// through by any method other than a read.
func TestWithRateLimit_WritesNeverBorrowTheReadBudget(t *testing.T) {
	limiter := newAPIRateLimiter(2, time.Minute)
	h := withRateLimit(limiter, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	const ip = "192.0.2.44"
	for _, m := range []string{http.MethodPost, http.MethodPut} {
		require.Equal(t, http.StatusOK, serveRateLimited(t, h, rateLimitTestRequest(m, ip, true)).Code)
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		assertRefusedWithRetryAfter(t, serveRateLimited(t, h, rateLimitTestRequest(m, ip, true)), m+" after the strict budget is gone")
	}
	// The read budget is untouched by all of that.
	assert.Equal(t, http.StatusOK, serveRateLimited(t, h, rateLimitTestRequest(http.MethodGet, ip, true)).Code)
	// rateLimitAllows (the inline form used by the provider dispatcher)
	// delegates to withRateLimit and so inherits the same rule.
	w := httptest.NewRecorder()
	assert.False(t, rateLimitAllows(w, rateLimitTestRequest(http.MethodPost, ip, true), limiter))
	assertRefusedWithRetryAfter(t, w, "rateLimitAllows write after the strict budget is gone")
}
