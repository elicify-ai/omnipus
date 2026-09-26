package gateway

// N5 — MC-10/MC-44 shape on the unauthenticated serve prefix: past the
// serve-path rate limit the answer is 429, and the 429 carries the MC-10
// isolation header set (CSP, no-referrer, nosniff, no-store) — the header
// block is applied BEFORE the limiter precisely so refusals are safe to
// render. Oracle: spec MC-10 ("every response on this prefix, refusals
// included, carries the policy") + the serve limiter bound; expectations
// derive from the spec header block, not from the handler.

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// useTightServeLimiter swaps the package serve limiter for a tiny one so
// the limit is reachable in three requests. The swap must happen BEFORE
// registration (serveHandler reads the package variable at registration
// time) — same pattern as useFreshMailPreviewServeLimiter.
func useTightServeLimiter(t *testing.T, n int) {
	t.Helper()
	previous := mailPreviewServeLimiter
	mailPreviewServeLimiter = newAPIRateLimiter(n, time.Minute)
	t.Cleanup(func() { mailPreviewServeLimiter = previous })
}

func TestMailPreviewServe_429CarriesIsolationHeaders(t *testing.T) {
	useTightServeLimiter(t, 2)
	env := newMailRedEnv(t)
	requireMailLive(t, env.mux, http.MethodGet, "/mail-preview/html/unknown-token-n5", "MC-10/MC-44 serve prefix")

	// One fixed client IP: the limiter is per-IP.
	ip := nextMailIP()
	for i := 1; i <= 2; i++ {
		rec := mailDo(env.mux, http.MethodGet, "/mail-preview/html/unknown-token-n5", ip, false, "")
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("MC-44: serve request %d of 2 was already 429 — the tight limiter did not apply", i)
		}
		if rec.Code != http.StatusNotFound {
			t.Fatalf("serve request %d = %d, want 404 for an unknown token while under the limit. body=%s", i, rec.Code, rec.Body.String())
		}
	}

	over := mailDo(env.mux, http.MethodGet, "/mail-preview/html/unknown-token-n5", ip, false, "")
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("MC-44: request 3 from the same IP = %d, want 429 past the serve limit. body=%s", over.Code, over.Body.String())
	}
	if got := over.Header().Get("Content-Security-Policy"); got != mailPreviewCSP() {
		t.Fatalf("MC-10: the 429's CSP = %q, want the isolation policy (the header block must precede the limiter)", got)
	}
	for k, want := range map[string]string{
		"Referrer-Policy":        "no-referrer",
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
	} {
		if got := over.Header().Get(k); got != want {
			t.Fatalf("MC-10: the 429's %s = %q, want %q", k, got, want)
		}
	}
	require.NotEmpty(t, over.Header().Get("Retry-After"), "the 429 must carry Retry-After")
}
