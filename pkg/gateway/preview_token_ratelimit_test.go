// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-067 stage 1 — FR-003k, spec §13 test 115
// (TestPreviewToken_RateLimitedAndCapped: "The path returns 429 past its
// window; the mint endpoint is limited; the ninth live token is refused").
//
// FR-003k gives two reasons, and the second is why this test's absence mattered
// more than a coverage gap:
//
//	"minting creates a credential in an in-memory store, so an uncapped caller
//	 is a memory-growth path; and the no-token-in-logs oracle IS a forced 429 on
//	 this path — without rate limiting that test captures nothing and passes."
//
// TestPreviewPath_TokenNeverLogged and test 118 both build their OWN
// newAPIRateLimiter(1, time.Minute) to force a 429. That proves withRateLimit
// redacts; it says nothing about whether the shipped route is limited at all.
// If libraryPreviewServeLimiter were deleted tomorrow, those tests would stay
// green and the token-redaction oracle they rest on would be unreachable in
// production. This file is what makes that claim true.
//
// ON WHERE THE NUMBERS COME FROM. The spec fixes exactly one of them: "at most
// 8 live tokens per session", asserted below against both the named constant
// and the literal 8. It deliberately fixes NO request-per-window figure, so the
// two limiter tests read the shipped limiter's own configuration and assert the
// property the spec does state — that a bound exists, that crossing it produces
// 429, and (§10.3) that even that refusal carries the isolation policy. What
// they must not do is hard-code a number the spec never chose, which would fail
// on any legitimate tuning change while proving nothing extra.
//
// Each test uses its own client IP. The limiters are process-wide singletons
// shared with every other test in this package, and the limiter is keyed by IP:
// without distinct addresses, one test exhausting a window would 429 unrelated
// tests in the same binary — a cross-test coupling that shows up as a flake
// nobody can reproduce alone.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// previewLimitRequest builds a request from a dedicated client address.
func previewLimitRequest(method, target, clientIP string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = clientIP + ":54321"
	return r
}

// TestPreviewToken_RateLimitedAndCapped is spec test 115.
func TestPreviewToken_RateLimitedAndCapped(t *testing.T) {
	t.Run("the serving path answers 429 past its window", func(t *testing.T) {
		f := newPreviewFixture(t)
		freezePreviewPolicyForTest(t)
		want := specIsolationPolicy(t, previewFixtureSources)

		limit := libraryPreviewServeLimiter.limit
		require.Positive(t, limit,
			"FR-003k: the shipped serving prefix must carry a real bound. A limiter with a "+
				"non-positive limit is not a stricter one — allow() would refuse everything, "+
				"or nothing, depending on the sign")

		// The rate limiter is what is under test, so the request deliberately
		// names an UNKNOWN token: the handler short-circuits to its 404 page
		// without touching the filesystem, and every request still passes
		// through the limiter. Serving a real file limit+1 times would measure
		// the same thing and the disk as well.
		target := tokenURL(strings.Repeat("r", PreviewTokenEncodedLen), "site/index.html")
		serve := f.routes.serveHandler()
		const clientIP = "198.51.100.11"

		var last *httptest.ResponseRecorder
		for i := 0; i < limit; i++ {
			last = httptest.NewRecorder()
			serve(last, previewLimitRequest(http.MethodGet, target, clientIP))
			require.NotEqual(t, http.StatusTooManyRequests, last.Code,
				"request %d of %d was refused early — the window is being shared with another "+
					"caller and the assertion below would not be measuring this test's traffic", i+1, limit)
		}

		rec := httptest.NewRecorder()
		serve(rec, previewLimitRequest(http.MethodGet, target, clientIP))

		require.Equal(t, http.StatusTooManyRequests, rec.Code,
			"FR-003k: the serving prefix must be rate-limited. Without this the "+
				"no-token-in-logs oracle (test 93/118) can never force the 429 it reads")
		assert.NotEmpty(t, rec.Header().Get("Retry-After"),
			"a 429 the caller cannot act on is an outage rather than a limit")
		assert.Equal(t, want, rec.Header().Get("Content-Security-Policy"),
			"§10.3: EVERY response on this path carries the policy — including the ones the "+
				"rate limiter produces, which is why serveHandler sets the headers BEFORE the "+
				"limiter runs rather than inside the handler it wraps")
	})

	t.Run("the mint endpoint is rate-limited", func(t *testing.T) {
		limit := libraryPreviewMintLimiter.limit
		require.Positive(t, limit, "FR-003k: minting issues a credential and must be bounded")

		// Driven through the PRODUCTION ROUTE TABLE, not by hand-wrapping the
		// handler. The limiter is applied in registerLibraryPreviewRoutes'
		// composition — a.withAuth(withRateLimit(limiter, handler)) — so a test
		// that wrapped the handler itself would assert that withRateLimit works
		// (it does; rest_auth_test.go covers it) while proving nothing about
		// whether the shipped mint endpoint is limited at all. Deleting the
		// limiter from the registration must fail this.
		api := newTestRestAPIWithHome(t)
		mux := http.NewServeMux()
		api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

		bypassCfg := &config.Config{}
		bypassCfg.Gateway.DevModeBypass = true
		const clientIP = "198.51.100.22"

		// The body is deliberately unusable: what is under test is the limiter,
		// which runs BEFORE the handler, so every one of these is a 400 that
		// still consumes a slot. A valid body would additionally trip the
		// per-session cap at eight and confuse the two refusals.
		//
		// The workspace id is malformed rather than merely absent, so the
		// handler answers 400 (validateEntityID) and not 404 (workspace not
		// found) — which keeps 404 free to mean the one thing the pre-condition
		// below uses it for: the route is not registered.
		post := func() *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodPost, libraryPreviewMintPath,
				strings.NewReader(`{"workspace_id":"bad/id","path":"x","scope":"file"}`))
			r.RemoteAddr = clientIP + ":54321"
			r.Header.Set("Authorization", "Bearer dev-mode-bypass-sentinel")
			r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "session-mint-limit"})
			r = r.WithContext(context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, bypassCfg))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, r)
			return rec
		}

		for i := 0; i < limit; i++ {
			rec := post()
			require.NotEqual(t, http.StatusTooManyRequests, rec.Code,
				"request %d of %d was refused early — the window is being shared with another "+
					"caller, so the assertion below would not be measuring this test", i+1, limit)
			require.NotEqual(t, http.StatusNotFound, rec.Code,
				"request %d reached no handler — the mint route is not registered, and a 404 "+
					"would never become a 429 no matter how many follow", i+1)
		}

		rec := post()
		require.Equal(t, http.StatusTooManyRequests, rec.Code,
			"FR-003k: the mint endpoint must be rate-limited — each successful call writes an "+
				"entry into an in-memory store, so an uncapped caller is a memory-growth path; "+
				"body: %s", rec.Body.String())
	})

	t.Run("the ninth live token in one session is refused", func(t *testing.T) {
		// The number IS in the spec: FR-003k, "at most 8 live tokens per
		// session". Asserted against the constant and against the literal, so
		// neither a renamed constant nor a quietly retuned value passes.
		require.Equal(t, 8, MaxLivePreviewTokensPerSession,
			"FR-003k fixes the per-session cap at 8")

		f := newPreviewFixture(t)
		for i := 0; i < MaxLivePreviewTokensPerSession+1; i++ {
			name := fmt.Sprintf("capfile-%d.txt", i)
			require.NoError(t, os.WriteFile(
				filepath.Join(f.workDir, name), []byte("capped"), 0o600))
		}

		// Eight DISTINCT paths: re-minting the same scope rotates the existing
		// token instead of adding one (FR-003m), so eight mints of one file
		// would leave exactly one live token and the ninth would succeed.
		for i := 0; i < MaxLivePreviewTokensPerSession; i++ {
			rec := f.mintRaw(t, map[string]any{
				"workspace_id": f.workspaceID,
				"path":         fmt.Sprintf("capfile-%d.txt", i),
				"scope":        "file",
			}, "session-cap")
			require.Equal(t, http.StatusCreated, rec.Code,
				"mint %d must succeed — the cap is %d: %s",
				i+1, MaxLivePreviewTokensPerSession, rec.Body.String())
		}

		ninth := f.mintRaw(t, map[string]any{
			"workspace_id": f.workspaceID,
			"path":         fmt.Sprintf("capfile-%d.txt", MaxLivePreviewTokensPerSession),
			"scope":        "file",
		}, "session-cap")
		require.Equal(t, http.StatusTooManyRequests, ninth.Code,
			"FR-003k: the ninth live token in one session must be refused; body: %s",
			ninth.Body.String())

		t.Run("a different session is unaffected", func(t *testing.T) {
			// The cap is per session, not global. A global counter would satisfy
			// the assertion above and lock every other operator out of previews
			// as soon as one of them opened eight.
			other := f.mintRaw(t, map[string]any{
				"workspace_id": f.workspaceID, "path": "capfile-0.txt", "scope": "file",
			}, "session-somebody-else")
			require.Equal(t, http.StatusCreated, other.Code, other.Body.String())
		})

		t.Run("releasing one frees a slot", func(t *testing.T) {
			// Otherwise the cap is a one-way ratchet: a session that ever opened
			// eight previews could never open another until every one expired,
			// which is a fifteen-minute lockout the operator cannot clear.
			var first gen.LibraryPreviewTokenResponse
			rec := f.mintRaw(t, map[string]any{
				"workspace_id": f.workspaceID, "path": "capfile-0.txt", "scope": "file",
			}, "session-cap")
			require.Equal(t, http.StatusCreated, rec.Code,
				"re-minting an EXISTING scope rotates in place and must not be capped: %s",
				rec.Body.String())
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &first))

			require.True(t, f.routes.tokens.InvalidateToken(first.Token),
				"pre-condition: the token must have been live")

			freed := f.mintRaw(t, map[string]any{
				"workspace_id": f.workspaceID,
				"path":         fmt.Sprintf("capfile-%d.txt", MaxLivePreviewTokensPerSession),
				"scope":        "file",
			}, "session-cap")
			assert.Equal(t, http.StatusCreated, freed.Code,
				"a revoked token must free its slot; body: %s", freed.Body.String())
		})
	})
}
