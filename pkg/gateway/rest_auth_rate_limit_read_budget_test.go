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

// TestWithRateLimit_ReadSizedLimiterNeverGetsCompanionBudget: a limiter
// whose declared ceiling was already chosen for reads (taskReadLimiter's
// 240/min is the contract for the calendar and task-run read routes) must
// count authenticated GET/HEAD against that ceiling directly. Before the fix
// withRateLimit multiplied every limiter's ceiling by readBudgetMultiplier
// for authenticated reads, silently turning 240/min into 1200/min
// (TestRestTasks_OccurrencesEndpoint's 241st-request 429 regression).
func TestWithRateLimit_ReadSizedLimiterNeverGetsCompanionBudget(t *testing.T) {
	const limit = 4
	limiter := newReadSizedAPIRateLimiter(limit, time.Minute)
	h := withRateLimit(limiter, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	const ip = "198.51.100.61"
	for i := 1; i <= limit; i++ {
		w := serveRateLimited(t, h, rateLimitTestRequest(http.MethodGet, ip, true))
		require.Equal(t, http.StatusOK, w.Code, "authenticated GET %d of %d must pass", i, limit)
	}
	assertRefusedWithRetryAfter(t, serveRateLimited(t, h, rateLimitTestRequest(http.MethodGet, ip, true)), "authenticated GET beyond a read-sized limit")
	assertRefusedWithRetryAfter(t, serveRateLimited(t, h, rateLimitTestRequest(http.MethodHead, ip, true)), "authenticated HEAD beyond a read-sized limit")
	// No companion bucket may exist, whatever path asks for one.
	assert.Same(t, limiter, limiter.readBudget(), "a read-sized limiter is its own read budget")
	assert.Nil(t, limiter.reads, "a read-sized limiter must never allocate a companion budget")

	// The inline form used by dispatcher switches inherits the same rule.
	w := httptest.NewRecorder()
	assert.False(t, rateLimitAllows(w, rateLimitTestRequest(http.MethodGet, ip, true), limiter))
	assertRefusedWithRetryAfter(t, w, "rateLimitAllows authenticated GET beyond a read-sized limit")
}

// TestAPIRateLimiters_ReadSizedClassification pins the per-limiter audit
// (fix4 rate-limit-reads): a limiter is read-sized when every request it
// counts is a GET, so its declared number IS the read ceiling and a
// companion budget would make that number dead. Limiters that guard writes,
// pre-auth traffic, or mixed read/write routes keep the D-109 companion
// budget (configLimiter is the D-109 limiter itself).
//
// platformAuthSessionLimiter/platformAuthStartLimiter/platformAuthClaimLimiter
// used to appear in these maps; ADR-0010 WP2 phase 2 moved them out of this
// package entirely (they are now editions/platform.Provider's own
// Host.NewRateLimiter/NewReadSizedRateLimiter-built instances, not this
// package's *apiRateLimiter) — their read-sized/write-sized classification
// is pinned by that package's own TestProvider_RateLimiterCeilings and by
// the seam proof in signin_provider_test.go instead.
func TestAPIRateLimiters_ReadSizedClassification(t *testing.T) {
	readSized := map[string]*apiRateLimiter{
		"taskReadLimiter":            taskReadLimiter,
		"validateLimiter":            validateLimiter,
		"signInStatusLimiter":        signInStatusLimiter,
		"libraryPreviewServeLimiter": libraryPreviewServeLimiter,
	}
	for name, l := range readSized {
		assert.Truef(t, l.readSized, "%s guards GET-only routes and must be read-sized", name)
	}
	writeOrPreAuthSized := map[string]*apiRateLimiter{
		"configLimiter":              configLimiter,
		"onboardingCompleteLimiter":  onboardingCompleteLimiter,
		"reauthLimiter":              reauthLimiter,
		"cliValidateLimiter":         cliValidateLimiter,
		"signInStartLimiter":         signInStartLimiter,
		"signInPollLimiter":          signInPollLimiter,
		"signInImportLimiter":        signInImportLimiter,
		"signInSignOutLimiter":       signInSignOutLimiter,
		"providerListAnonLimiter":    providerListAnonLimiter,
		"providerConfigWriteLimiter": providerConfigWriteLimiter,
		"providerTestLimiter":        providerTestLimiter,
		"providerEntitlementLimiter": providerEntitlementLimiter,
		"smokeTestLimiter":           smokeTestLimiter,
		"libraryPreviewMintLimiter":  libraryPreviewMintLimiter,
	}
	for name, l := range writeOrPreAuthSized {
		assert.Falsef(t, l.readSized, "%s is write/pre-auth-sized and keeps the companion read budget", name)
	}
}
