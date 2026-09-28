// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — RED regression pack, #798 fix round 3. One test per
// confirmed gate finding; the oracle is the spec text, never the
// implementation. Findings pinned here: A1 (architect BLOCKER), A2
// (architect MAJOR 2), A3 (architect MAJOR 3), A6 (architect MAJOR 6 /
// security-lead F-1), A8 (architect MAJOR 8 / CR1), CR2 (code-reviewer
// MAJOR 2), CR3 (code-reviewer MAJOR 3).
//
// TEST PLAN (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: the six confirmed isolation gaps of the #798
//     fix-3 gate, pinned as failing tests on 1d2774d51.
//
//   - Specification source (oracle — never the implementation):
//     docs/internal/specs/adr-094-preview-isolation-spec.md — FR-013
//     (line 957), FR-014 (line 958), FR-020 (line 964), FR-028 (line 972),
//     DS-3 row 10, DS-4 row 10, S-7.2; the gate reports under
//     coordination/logs/gwsec-798-gate/ only SELECTED the findings; the
//     expected values below come from the spec text.
//
//   - Unit boundary: the REAL production chain
//     (buildProductionMiddlewareChain over real registrations) in
//     httptest servers; the REAL previewLabelLimiters singleton; the REAL
//     audit pipeline; the REAL bearer resolver with the existing bcrypt
//     compare test hook (pkg/config/bcrypt.go::
//     SetCompareHashAndPasswordForTest — the A8 seam already exists; no
//     production change needed).
//
//   - RED shape: each finding's test fails on 1d2774d51 at the assertion
//     that names the spec violation. Rows labelled "pin" pass today and
//     guard behaviour the fix must not weaken.
//
//   - CHECK mutations: A1 — emit buildWorkspaceCSP on the static path (the
//     bug itself). A2 — reject ALL dot-segments again. A3 — run the SW
//     refusal before the Mode 1 branch again. A6 — remove eviction /
//     suppression. A8 — drop the JWT pre-filter. CR2 — fill implicit 80
//     for exact-host only.
//
//   - Known gaps: Mode 1 redirect passthrough is not re-tested here
//     (pinned by TestPreviewRedirectRule_Mode1Passthrough, passing).
//     Architect findings 4/5 (tool text, user docs) are prose — not Go
//     tests; they stay with their own reviews.

package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// ---------------------------------------------------------------------------
// A1 — architect BLOCKER: Mode 2 STATIC responses are not confined.
// setPreviewStaticHeaders writes buildWorkspaceCSP and swaps in the preview
// CSP only when a Mode 1 label is on the context
// (preview_response_policy.go::setPreviewStaticHeaders). FR-014 (spec line
// 958): "The Mode 2 CSP response header MUST be byte-identical to the spec
// template ... a static tripwire treats the literal template as the oracle".
//
// Why TestPreviewCSPHeaderSet missed it: its three rows drive the Mode 2
// PROXIED branch (rest_preview.go::510 calls buildPreviewCSP) and the two
// Mode 1 rows — no row serves a STATIC registration through
// serveStaticFile, so the static path's wrong CSP never faced the template
// oracle. This test closes that gap.
// ---------------------------------------------------------------------------

func TestFix3Mode2StaticCSP_MatchesTemplate(t *testing.T) {
	h := piRedNewGuardHarness(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "app.js"), []byte("console.log('f3red');\n"), 0o644))
	token, _, err := h.api.servedSubdirs.Register("f3red-static-agent", dir, time.Hour)
	require.NoError(t, err, "static registration must succeed (fixture binding)")

	prefix := "/preview/f3red-static-agent/" + token
	origin := "http://localhost:" + h.port

	resp := h.piRedGuardGet(t, "", prefix+"/app.js", http.MethodGet, nil)
	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"fixture binding: the static registration must serve a 200 (got %d)", resp.StatusCode)

	// Oracle: the spec template, transcribed by piRedMode2CSPTemplate —
	// the expected value is derived from the spec, not from the response.
	want := piRedMode2CSPTemplate(origin, "ws://localhost:"+h.port, prefix)
	wantWire := strings.TrimSpace(strings.ReplaceAll(want, "\n", " "))
	assert.Equal(t, wantWire, resp.Header.Get("Content-Security-Policy"),
		"RED (FR-014, architect A1): the Mode 2 STATIC CSP must be byte-identical to the spec template — "+
			"today setPreviewStaticHeaders keeps buildWorkspaceCSP (connect-src 'self'; form-action 'self'), "+
			"so a static preview can fetch/POST /api/v1 with the session cookie")
}

// ---------------------------------------------------------------------------
// A2 / CR3 — architect MAJOR 2 + code-reviewer MAJOR 3: the Mode 2 redirect
// rule 502s legal targets. FR-013 (spec line 957): "resolve (WHATWG,
// dot-segments/%2e normalised) → emit if alias-origin + in-prefix → else
// re-root raw root-relative values under the prefix and re-check → else
// 502, no Location". Two confirmed misses:
//   - A2a: any in-prefix dot-segment path 502s before resolution
//     (preview_response_policy.go::pathHasDotSegments rejects first), but
//     DS-2 row 6 502s only when the NORMALISED path leaves the prefix.
//   - CR3: the prefix is url.PathEscape'd (rest_preview.go::proxyDevRequest)
//     but compared against the DECODED r.URL.Path, so for agent id "a b"
//     (legal per validation.EntityID) the in-prefix Location
//     /preview/a%20b/<token>/dashboard fails the prefix check and 502s on
//     the reserved-root rule.
// ---------------------------------------------------------------------------

// f3redRedirectHarness is piRedNewRedirectHarness parameterized by agent id
// (CR3 needs an agent id that requires percent-encoding on the wire).
type f3redRedirectHarness struct {
	api      *restAPI
	srv      *httptest.Server
	port     string
	devToken string

	mu           sync.Mutex
	upStatus     int
	upLoc        string
	upstreamHits atomic.Int32
}

func f3redNewRedirectHarness(t *testing.T, agentID string) *f3redRedirectHarness {
	t.Helper()
	_ = os.Unsetenv("OMNIPUS_BEARER_TOKEN")

	api, _ := newPreviewRouteTestAPI(t)
	h := &f3redRedirectHarness{api: api}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h.mu.Lock()
		status, loc := h.upStatus, h.upLoc
		h.mu.Unlock()
		if loc != "" {
			w.Header().Set("Location", loc)
		}
		h.upstreamHits.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(upstream.Close)

	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg
	devEntry, regErr := reg.Register(agentID, upstreamPort(t, upstream.URL), 0, "npm run dev", 10)
	require.NoError(t, regErr)
	h.devToken = devEntry.Token

	mainMux := http.NewServeMux()
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})
	chain := buildProductionMiddlewareChain(api, mainMux)
	h.srv = httptest.NewServer(chain)
	t.Cleanup(h.srv.Close)
	h.port = fmt.Sprintf("%d", upstreamPort(t, h.srv.URL))

	// Fixture parity (A-2): the canonical origin must name THIS harness's
	// port and preview must be on.
	h.api.agentLoop.GetConfig().Gateway.PublicURL = "http://localhost:" + h.port
	h.api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(true)

	return h
}

// f3redEmit programs the upstream's next response.
func (h *f3redRedirectHarness) f3redEmit(status int, location string) {
	h.mu.Lock()
	h.upStatus, h.upLoc = status, location
	h.mu.Unlock()
}

// f3redProxyGet drives a GET through the Mode 2 dev proxy (no redirect
// following) and returns the client-visible response.
func (h *f3redRedirectHarness) f3redProxyGet(t *testing.T, escapedAgent, sub string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		h.srv.URL+"/preview/"+escapedAgent+"/"+h.devToken+"/"+sub, nil)
	require.NoError(t, err)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestFix3Mode2Redirect_InPrefixDotSegmentsEmitted(t *testing.T) {
	h := f3redNewRedirectHarness(t, "f3red-dot-seg-agent")
	prefix := "/preview/f3red-dot-seg-agent/" + h.devToken

	// A2a (RED): the upstream redirects to an in-prefix dot-segment path.
	// DS-2 row 6: 502 only when the NORMALISED path leaves the prefix —
	// this one normalises to prefix+"/next", inside, so FR-013 resolves and
	// EMITS it. Expected emit: the resolved value (prefix+"/next"), the
	// same relative-emit convention the spec's verdict table uses for
	// in-prefix targets.
	h.f3redEmit(http.StatusFound, prefix+"/foo/../next")
	resp := h.f3redProxyGet(t, "f3red-dot-seg-agent", "page")
	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusFound, resp.StatusCode,
		"FR-013: an in-prefix redirect keeps the upstream status — got %d", resp.StatusCode)
	assert.Equal(t, prefix+"/next", resp.Header.Get("Location"),
		"RED (FR-013, A2a): an in-prefix dot-segment Location must be RESOLVED and emitted — "+
			"today pathHasDotSegments 502s before resolution")
}

func TestFix3Mode2Redirect_PercentEncodedAgentIDEmitted(t *testing.T) {
	// "a b" is a legal agent id (validation.EntityID rejects only "", /\,
	// ".." and NUL) and must be percent-encoded on the wire (the case
	// ADR-094 says the builder must handle).
	h := f3redNewRedirectHarness(t, "a b")
	escaped := url.PathEscape("a b") // "a%20b"
	prefix := "/preview/" + escaped + "/" + h.devToken

	// CR3 (RED): the upstream emits the CLIENT-VISIBLE in-prefix Location
	// (percent-encoded prefix, as the gateway itself mints it). Expected:
	// in-prefix emit unchanged — the same convention as the plain-agent
	// in-prefix row. Today the rule compares the decoded URL path against
	// the encoded prefix, misses, falls to the reserved-root check and 502s.
	h.f3redEmit(http.StatusFound, prefix+"/dashboard")
	resp := h.f3redProxyGet(t, escaped, "page")
	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusFound, resp.StatusCode,
		"FR-013: an in-prefix redirect keeps the upstream status — got %d", resp.StatusCode)
	assert.Equal(t, prefix+"/dashboard", resp.Header.Get("Location"),
		"RED (FR-013, CR3): a percent-encoded agent id's in-prefix Location must be recognised in-prefix and emitted — "+
			"today the encoded prefix is compared to the decoded path and the target 502s")
}

// ---------------------------------------------------------------------------
// A3 — architect MAJOR 3: the service-worker refusal runs on Mode 1.
// FR-028 (spec line 972): "FR-010/FR-011 apply to main-Host requests only".
// HandlePreview today refuses `Service-Worker: script` /
// `Sec-Fetch-Dest: serviceworker` BEFORE the Mode 1 label branch
// (rest_preview.go::HandlePreview), so a Mode 1 app's own service worker —
// a normal full app, on its own origin where it cannot see gateway cookies —
// is refused. RED row: the Mode 1 SW request must reach the app's upstream.
// Pin row: the main-host /preview/ refusal must survive the fix.
// ---------------------------------------------------------------------------

func TestFix3Mode1ServiceWorker_NotRefused(t *testing.T) {
	h := piRedNewGuardHarness(t)
	label := piRedMintGuardLabel(t, h)

	t.Run("mode1_sw_request_reaches_the_app", func(t *testing.T) {
		h.upstreamHits.Store(0)
		resp := h.piRedGuardGet(t, label+".localhost:"+h.port, "/", http.MethodGet, map[string]string{
			"Service-Worker": "script",
			"Sec-Fetch-Dest": "serviceworker",
		})
		t.Cleanup(func() { _ = resp.Body.Close() })
		require.Equal(t, http.StatusOK, resp.StatusCode,
			"RED (FR-028, A3): a Mode 1 app's own service-worker request must be served — got %d", resp.StatusCode)
		assert.Equal(t, int32(1), h.upstreamHits.Load(),
			"the SW request must reach the dev upstream — today the refusal fires before the Mode 1 branch")
	})

	t.Run("pin_main_host_preview_sw_still_refused", func(t *testing.T) {
		h.upstreamHits.Store(0)
		prefix := "/preview/" + piRedGuardAgent + "/" + h.devToken
		resp := h.piRedGuardGet(t, "", prefix+"/app.js", http.MethodGet, map[string]string{
			"Service-Worker": "script",
			"Sec-Fetch-Dest": "serviceworker",
		})
		t.Cleanup(func() { _ = resp.Body.Close() })
		assert.GreaterOrEqual(t, resp.StatusCode, http.StatusBadRequest,
			"pin (FR-028): the main-host /preview/ SW refusal must survive the fix")
		assert.Equal(t, int32(0), h.upstreamHits.Load(),
			"pin: a refused main-host SW request must not reach the upstream")
	})
}

// ---------------------------------------------------------------------------
// A6 — architect MAJOR 6 / security-lead F-1: per-label limiter state grows
// without bound and preview.label_unknown audit entries are unsuppressed.
// The unauthenticated label host lets any local process (and any web page
// via RFC 6761 *.localhost) mint unlimited grammar-valid labels; each
// allocates a permanent map bucket (preview_host_dispatch.go::
// previewLabelLimiter.allow never deletes) and each miss writes an
// unsuppressed Warn into the HMAC-chained audit chain. The audit half's
// in-repo precedent is the suppression window the same audit file already
// applies to dev.upstream_unreachable (rest_preview_audit.go::
// markFirstUpstreamFailure). Both rows are PROPERTY assertions — the
// mechanism (cap, TTL sweep, LRU; window or first-in-window) is GREEN's
// choice (A-2: the fixture adapts, the property stands).
// ---------------------------------------------------------------------------

// f3redLabelCount is deliberately large: well above any legitimate hard cap
// GREEN might pick (so a cap-based fix still passes) and far above what any
// bounded design may retain for 5000 unique attacker labels.
const f3redLabelCount = 5000

func TestFix3PreviewLabelLimiter_BoundedUnderUniqueLabels(t *testing.T) {
	labels := make([]string, 0, f3redLabelCount)
	for i := 0; i < f3redLabelCount; i++ {
		labels = append(labels, fmt.Sprintf("f3red-%06d", i))
	}

	// First sight: every label is admitted with a full bucket today — and
	// must be under any fix that keeps FR-027's per-label semantics.
	for _, lb := range labels {
		require.True(t, previewLabelLimiters.allow(lb),
			"fixture binding: a label's first sight must be admitted")
	}

	// Age every f3red bucket to 24h old, then one more allow() as the
	// lazy-sweep trigger. If GREEN's eviction runs on a janitor, GREEN
	// adapts this trigger (A-2); the property below is the oracle.
	previewLabelLimiters.mu.Lock()
	for key, b := range previewLabelLimiters.buckets {
		if strings.HasPrefix(key, "f3red-") {
			b.last = time.Now().Add(-24 * time.Hour)
		}
	}
	previewLabelLimiters.mu.Unlock()
	previewLabelLimiters.allow("f3red-sweep-probe")

	// The property: NOT all 5000 buckets are retained.
	previewLabelLimiters.mu.Lock()
	retained := 0
	for key := range previewLabelLimiters.buckets {
		// Exclude the probe's own bucket: the property is about the 5000
		// attacker labels, and a post-fix cap of exactly 5000 must pass.
		if strings.HasPrefix(key, "f3red-") && key != "f3red-sweep-probe" {
			retained++
		}
	}
	// Cleanup: remove every bucket my labels created.
	for key := range previewLabelLimiters.buckets {
		if strings.HasPrefix(key, "f3red-") {
			delete(previewLabelLimiters.buckets, key)
		}
	}
	previewLabelLimiters.mu.Unlock()

	assert.Less(t, retained, f3redLabelCount,
		"RED (A6/F-1): after 5000 unique labels + aging + a sweep trigger the limiter must not retain all buckets — "+
			"today allow() allocates a bucket per grammar-valid label and nothing ever deletes one")
}

func TestFix3PreviewLabelUnknownAudit_Suppressed(t *testing.T) {
	api, root := newShellModeAuditAPI(t)

	// Drive servePreviewByLabel directly (post-admit path): nil registries
	// make every label unknown. One recorder + one fixed remote — all
	// misses share one key, which is exactly the flood shape (one source,
	// many unique labels).
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for i := 0; i < 8; i++ {
		api.servePreviewByLabel(w, req, fmt.Sprintf("f3red-audit-%d", i), time.Now())
	}

	events := f3redAuditEvents(t, root, "preview.label_unknown")
	assert.NotEmpty(t, events,
		"fixture binding: the miss must still be audited once — the fix bounds the flood, it must not silence it")
	assert.Less(t, len(events), 8,
		"RED (A6/F-1 audit half): eight rapid unique-label misses from one remote must not produce eight audit entries — "+
			"today every miss writes an unsuppressed Warn into the HMAC-chained chain")
}

// f3redAuditEvents returns every audit entry with the given event under
// root (modeled on shellModeChanges).
func f3redAuditEvents(t *testing.T, root, event string) []audit.Entry {
	t.Helper()
	var out []audit.Entry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" ||
			filepath.Base(filepath.Dir(path)) != "system" {
			return nil
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var e audit.Entry
			if json.Unmarshal(sc.Bytes(), &e) == nil && e.Event == event {
				out = append(out, e)
			}
		}
		return sc.Err()
	})
	require.NoError(t, err)
	return out
}

// ---------------------------------------------------------------------------
// A8 / CR1 — architect MAJOR 8: a foreign Bearer pays bcrypt on the dev
// proxy. FR-020 (spec line 964): the Authorization filter's cost pre-filter
// is "id-tagged → one indexed hash, JWT-shaped → zero bcrypt compares,
// legacy shape → bounded account scan". Today gatewayOwnsBearer calls
// resolveBearerIdentity with no shape pre-filter, so a JWT-shaped Bearer
// (no omnipus_ id tag) walks VerifyCLIToken → VerifyTokenAgainst's bcrypt
// compare — one compare per proxied request, attacker-loopable with no
// limiter on registerPreviewEndpoints.
//
// Seam: pkg/config/bcrypt.go::SetCompareHashAndPasswordForTest — the test
// hook the suite already uses (auth_bcrypt_budget_test.go). No production
// change needed to observe the compare count.
// ---------------------------------------------------------------------------

func f3redBcryptHash(t *testing.T, secret string) config.BcryptHash {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	require.NoError(t, err)
	return config.BcryptHash(h)
}

func TestFix3GatewayOwnsBearer_JWTShapedBearerZeroBcrypt(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	// The gateway must have REAL credentials configured, or today's bug
	// (a compare against them) could not be observed at all.
	cliRaw := "omnipus_f3redcl1_" + strings.Repeat("b", 52)
	userRaw := "omnipus_f3red001_" + strings.Repeat("a", 52)
	cfg := &config.Config{}
	cfg.Gateway.CLIToken = &config.TokenEntry{ID: "f3redcl1", Hash: f3redBcryptHash(t, config.TokenSecret(cliRaw))}
	cfg.Gateway.Users = []config.UserConfig{
		{Username: "f3red-user", Tokens: []config.TokenEntry{
			{ID: "f3red001", Hash: f3redBcryptHash(t, config.TokenSecret(userRaw))},
		}},
	}

	compares := 0
	restore := config.SetCompareHashAndPasswordForTest(func(hashed, pw []byte) error {
		compares++
		return bcrypt.CompareHashAndPassword(hashed, pw)
	})
	t.Cleanup(restore)

	// A JWT-shaped token: three dot-separated base64url segments, no
	// omnipus_ id tag (FR-020's own shape definition).
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJmM3JlZC1wcmV2aWV3In0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJVadQssw5c"

	t.Run("jwt_bearer_zero_compares_and_not_owned", func(t *testing.T) {
		compares = 0
		owned := gatewayOwnsBearer(cfg, "Bearer "+jwt)
		assert.False(t, owned,
			"a foreign JWT-shaped Bearer must not be treated as the gateway's own (forward it to the app)")
		assert.Equal(t, 0, compares,
			"RED (FR-020, A8): a JWT-shaped Bearer must be forwarded with ZERO bcrypt compares — "+
				"today it reaches VerifyCLIToken/VerifyTokenAgainst and pays a compare per request")
	})

	t.Run("pin_id_tagged_cli_token_still_one_compare_and_owned", func(t *testing.T) {
		compares = 0
		owned := gatewayOwnsBearer(cfg, "Bearer "+cliRaw)
		assert.True(t, owned, "pin: the gateway's own CLI token must still be recognised")
		assert.Equal(t, 1, compares, "pin (FR-020): the id-tagged token pays exactly its one indexed compare")
	})

	t.Run("pin_legacy_user_token_owned", func(t *testing.T) {
		compares = 0
		owned := gatewayOwnsBearer(cfg, "Bearer "+userRaw)
		assert.True(t, owned, "pin: a legacy user token must still be recognised")
		assert.LessOrEqual(t, compares, 1,
			"pin (FR-020): the legacy shape stays a bounded scan (one user configured here)")
	})

	t.Run("pin_non_bearer_authorization_never_compares", func(t *testing.T) {
		compares = 0
		owned := gatewayOwnsBearer(cfg, "Basic Zml4M3JlZDpmM3JlZA==")
		assert.False(t, owned, "pin: a non-Bearer Authorization is not the gateway's")
		assert.Equal(t, 0, compares, "pin: a non-Bearer header pays no bcrypt at all")
	})
}

// ---------------------------------------------------------------------------
// CR2 — code-reviewer MAJOR 2: implicit port 80 never enters the preview
// mux. DS-3 row 10's edge rule (spec): "An absent port equals the canonical
// origin's port (implicit 80)" — on an http://localhost install the tool
// mints a PORTLESS isolated_url and the browser sends `Host: <label>.
// localhost` (default port omitted). Today previewPortAgrees fills the
// origin port as "80" and compares it to the Host's empty port, so the
// request falls through to the main mux: the user's primary link serves the
// SPA, and the API is reachable under the label host. RED row: a portless
// label Host must dispatch to the preview handler (the dev upstream is
// reached). Pin row: the ported Host keeps dispatching.
// ---------------------------------------------------------------------------

func TestFix3Mode1Dispatch_PortlessHostDispatches(t *testing.T) {
	h := piRedNewGuardHarness(t)
	// The implicit-80 install: the canonical origin carries NO port.
	h.api.agentLoop.GetConfig().Gateway.PublicURL = "http://localhost"
	label := middleware.PreviewLabelForToken(h.devToken)
	require.NotEmpty(t, label, "fixture binding: the dev token must derive its label")

	t.Run("portless_label_host_dispatches_to_preview", func(t *testing.T) {
		h.upstreamHits.Store(0)
		resp := h.piRedGuardGet(t, label+".localhost", "/", http.MethodGet, nil)
		t.Cleanup(func() { _ = resp.Body.Close() })
		require.Equal(t, http.StatusOK, resp.StatusCode,
			"RED (DS-3 row 10, CR2): the portless label Host must dispatch to the preview handler — got %d "+
				"(today previewPortAgrees compares the Host's empty port to the origin's implicit 80 and falls through)", resp.StatusCode)
		assert.Equal(t, int32(1), h.upstreamHits.Load(),
			"the dispatched request must reach the dev upstream — today it falls through to the main mux")
	})

	t.Run("pin_ported_label_host_still_dispatches", func(t *testing.T) {
		// On the portless install the explicit :80 form is equally valid.
		h.upstreamHits.Store(0)
		resp := h.piRedGuardGet(t, label+".localhost:80", "/", http.MethodGet, nil)
		t.Cleanup(func() { _ = resp.Body.Close() })
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"pin (DS-3 row 10): the explicit-port form must keep dispatching")
		assert.Equal(t, int32(1), h.upstreamHits.Load(),
			"pin: the explicit-port label Host reaches the upstream")
	})
}
