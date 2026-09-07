// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — ADR-067 FR-003e / MV-23: a preview token must not reach any
// log line OR any audit record.
//
// The spec states the requirement PER SITE precisely because a universal claim
// with a single-instance oracle ("no token in the logs") is satisfied by fixing
// one of six sites. These tests therefore do three separate things:
//
//  1. Drive REAL requests (a real 429, a real 503, a real 403) through the real
//     middleware and read the real slog records. Each drive asserts the status
//     occurred FIRST — MV-23 calls this out explicitly: if the path is not
//     rate-limited the capture is empty and a "no token in the capture"
//     assertion passes having tested nothing.
//  2. Unit-test the helper against expectations derived from ADR-067 §10.5's
//     route shapes, not from what the helper happens to return.
//  3. Enumerate the six recording sites from SOURCE, so a SEVENTH site added
//     later fails the build instead of silently leaking.

package gateway

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/gateway/pathredact"
)

// redactionFixtureToken is shaped like a real preview token: 32 random bytes in
// base64url (ADR-067 §10.5 "Shape"). Nothing about these tests depends on its
// value, only on it being a long, distinctive, unmistakable string.
const redactionFixtureToken = "s7Qb3nVfKp0dMxYh2Lr8TcWuZjEgAoIe1RyNvBt4XqU"

// ---------------------------------------------------------------------------
// slog capture
// ---------------------------------------------------------------------------

type redactionLogRecord struct {
	message string
	attrs   map[string]string
}

// redactionLogCapture is a slog.Handler that keeps every record so a test can assert on
// the actual bytes an operator would see.
type redactionLogCapture struct {
	mu      sync.Mutex
	records []redactionLogRecord
}

func (c *redactionLogCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *redactionLogCapture) Handle(_ context.Context, r slog.Record) error {
	rec := redactionLogRecord{message: r.Message, attrs: map[string]string{}}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.String()
		return true
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, rec)
	return nil
}

func (c *redactionLogCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *redactionLogCapture) WithGroup(string) slog.Handler      { return c }

func (c *redactionLogCapture) snapshot() []redactionLogRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]redactionLogRecord, len(c.records))
	copy(out, c.records)
	return out
}

// text flattens every captured message and attribute value into one string, so
// "the token appears nowhere in anything we logged" is a single assertion that
// cannot be satisfied by looking at the wrong field.
func (c *redactionLogCapture) text() string {
	var b strings.Builder
	for _, rec := range c.snapshot() {
		b.WriteString(rec.message)
		b.WriteString("\x00")
		keys := make([]string, 0, len(rec.attrs))
		for k := range rec.attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(rec.attrs[k])
			b.WriteString("\x00")
		}
	}
	return b.String()
}

// find returns the first captured record whose message equals msg.
func (c *redactionLogCapture) find(msg string) (redactionLogRecord, bool) {
	for _, rec := range c.snapshot() {
		if rec.message == msg {
			return rec, true
		}
	}
	return redactionLogRecord{}, false
}

// captureRedactionSlog installs a capturing handler as the default logger for the
// duration of the test and restores the previous default afterwards.
func captureRedactionSlog(t *testing.T) *redactionLogCapture {
	t.Helper()
	logs := &redactionLogCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(logs))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

// ---------------------------------------------------------------------------
// 1. The helper itself — expectations derived from ADR-067 §10.5 route shapes
// ---------------------------------------------------------------------------

// TestRedactRequestPath_PrefixDriven asserts the redaction contract on every
// credential-bearing route shape the gateway serves.
//
// The expected values come from the ROUTE DEFINITIONS, not from the helper:
//
//   - ADR-067 §10.5: "/library-preview/<token>/<path>"  → token is segment 2
//   - ADR-044 / rest_preview.go: "/preview/<agent>/<token>/…",
//     "/serve/<agent>/<token>/…", "/dev/<agent>/<token>/…" → token is segment 3
//
// The /library-preview/ rows are the ones the pre-existing sanitisePreviewPath
// gets wrong: its no-token fallback blanks the THIRD segment, which on that
// route is the filename, leaving the credential in place.
//
// Traces to: FR-003e, MV-23.
func TestRedactRequestPath_PrefixDriven(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "library preview token is the second segment",
			in:   "/library-preview/" + redactionFixtureToken + "/index.html",
			want: "/library-preview/" + pathredact.TokenPlaceholder + "/index.html",
		},
		{
			name: "library preview deep subresource keeps the remainder",
			in:   "/library-preview/" + redactionFixtureToken + "/assets/deep/site.css",
			want: "/library-preview/" + pathredact.TokenPlaceholder + "/assets/deep/site.css",
		},
		{
			name: "library preview bare token has no trailing path",
			in:   "/library-preview/" + redactionFixtureToken,
			want: "/library-preview/" + pathredact.TokenPlaceholder,
		},
		{
			name: "serve token is the third segment",
			in:   "/serve/agent-1/" + redactionFixtureToken + "/index.html",
			want: "/serve/agent-1/" + pathredact.TokenPlaceholder + "/index.html",
		},
		{
			name: "dev token is the third segment",
			in:   "/dev/agent-2/" + redactionFixtureToken + "/api/login",
			want: "/dev/agent-2/" + pathredact.TokenPlaceholder + "/api/login",
		},
		{
			name: "preview token is the third segment and trailing slash survives",
			in:   "/preview/agent-3/" + redactionFixtureToken + "/",
			want: "/preview/agent-3/" + pathredact.TokenPlaceholder + "/",
		},
		{
			name: "unrecognised shape on a token prefix falls back to the static placeholder",
			in:   "/library-preview/",
			want: pathredact.Placeholder,
		},
		{
			name: "token prefix with no token segment at all falls back",
			in:   "/serve/agent-1",
			want: pathredact.Placeholder,
		},
		{
			name: "ordinary API path is untouched so operators keep their triage data",
			in:   "/api/v1/config",
			want: "/api/v1/config",
		},
		{
			name: "empty path stays empty",
			in:   "",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := redactRequestPath(tc.in)
			assert.Equal(t, tc.want, got)
			if strings.Contains(tc.in, redactionFixtureToken) {
				assert.NotContains(t, got, redactionFixtureToken,
					"FR-003e: the token must not survive redaction")
			}
		})
	}
}

// TestRedactRequestPath_NeverReturnsRawOnATokenPrefix is the negative form of
// the fallback rule. FR-003e: "MUST fall back to a static placeholder, never
// the raw path." A helper that returned its input on an unrecognised shape
// would pass every positive case above and still leak.
func TestRedactRequestPath_NeverReturnsRawOnATokenPrefix(t *testing.T) {
	for prefix := range pathredact.TokenBearingPrefixes() {
		t.Run(prefix, func(t *testing.T) {
			// A path that is on the prefix but has no segment where the
			// credential is expected. The helper cannot know which segment to
			// blank, so it must return the constant.
			raw := prefix
			got := redactRequestPath(raw)
			assert.Equal(t, pathredact.Placeholder, got,
				"an unredactable token-bearing path must degrade to a constant, not to itself")
			assert.NotEqual(t, raw, got)
		})
	}
}

// TestRedactRequestPath_TokenPrefixTableMatchesSpec asserts the prefix table
// against the routes named in ADR-067 §10.5 and ADR-044 — not against itself.
// A new credential-in-path route added without a row here would be logged raw,
// and no other test in this file would notice.
func TestRedactRequestPath_TokenPrefixTableMatchesSpec(t *testing.T) {
	// key: route prefix, value: 0-based index of the credential segment once
	// the leading "/" is trimmed.
	want := map[string]int{
		"/library-preview/": 1, // ADR-067 §10.5: /library-preview/<token>/<path>
		"/preview/":         2, // ADR-044:       /preview/<agent>/<token>/…
		"/serve/":           2, // rest_preview.go legacyServePathPrefix
		"/dev/":             2, // rest_preview.go legacyDevPathPrefix
	}
	assert.Equal(t, want, pathredact.TokenBearingPrefixes())
}

// TestRedactRequestPath_ControlCharactersCannotForgeRecords covers the second
// half of "safe to write to a record". net/http percent-decodes r.URL.Path
// before a handler ever sees it, so "%0A" arrives as a real newline and would
// let a caller inject whole fake log lines around a redacted token.
func TestRedactRequestPath_ControlCharactersCannotForgeRecords(t *testing.T) {
	tests := []string{
		"/library-preview/" + redactionFixtureToken + "/a\nlevel=INFO msg=\"forged\"",
		"/api/v1/config\rX-Injected: 1",
		"/library-preview/" + redactionFixtureToken + "/\x00",
	}
	for _, in := range tests {
		got := redactRequestPath(in)
		assert.Equal(t, pathredact.Placeholder, got,
			"a path carrying control characters must collapse to the constant")
		assert.NotContains(t, got, "\n")
		assert.NotContains(t, got, "\r")
		assert.NotContains(t, got, redactionFixtureToken)
	}
}

// TestRedactRequestPath_Idempotent matters because two of the six sites sit on
// the same value (the CSRF reporter's slog fallback and its audit entry), and
// because a future site may redact a path that a caller already redacted.
func TestRedactRequestPath_Idempotent(t *testing.T) {
	for _, in := range []string{
		"/library-preview/" + redactionFixtureToken + "/index.html",
		"/serve/agent-1/" + redactionFixtureToken + "/index.html",
		"/api/v1/config",
		"/library-preview/",
	} {
		once := redactRequestPath(in)
		assert.Equal(t, once, redactRequestPath(once), "redaction must be idempotent for %q", in)
	}
}

// ---------------------------------------------------------------------------
// 2. Driven sites — a real request, a real status, real captured records
// ---------------------------------------------------------------------------

// TestPreviewPath_TokenNeverLogged is spec test 93. It drives a REAL 429
// through withRateLimit — the oracle the spec names, because reading the code
// is not the test.
//
// MV-23's trap is written into the assertions: the 429 is asserted BEFORE
// anything is said about the capture. Without that, a change that stopped
// rate-limiting the path would leave the capture empty and every
// "no token in the log" assertion would pass having tested nothing.
//
// Traces to: FR-003e, MV-23, spec §13 test 93.
func TestPreviewPath_TokenNeverLogged(t *testing.T) {
	logs := captureRedactionSlog(t)

	tokenPath := "/library-preview/" + redactionFixtureToken + "/index.html"
	limiter := newAPIRateLimiter(1, time.Minute)
	handler := withRateLimit(limiter, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// First request consumes the single-request budget.
	first := httptest.NewRecorder()
	handler(first, httptest.NewRequest(http.MethodGet, tokenPath, nil))
	require.Equal(t, http.StatusOK, first.Code,
		"pre-condition: the first request must be allowed, or the second one is not a rate-limit rejection")

	// Second request must be rejected — this is what makes the site log.
	second := httptest.NewRecorder()
	handler(second, httptest.NewRequest(http.MethodGet, tokenPath, nil))
	require.Equal(t, http.StatusTooManyRequests, second.Code,
		"MV-23: the 429 must actually happen — an empty capture would otherwise pass this test having tested nothing")

	rec, ok := logs.find("api: rate limit exceeded")
	require.True(t, ok, "the 429 must have produced a rate-limit log record; captured: %#v", logs.snapshot())

	assert.NotContains(t, logs.text(), redactionFixtureToken,
		"FR-003e: the preview token must appear in no captured log record")
	assert.Equal(t, "/library-preview/"+pathredact.TokenPlaceholder+"/index.html", rec.attrs["path"],
		"the logged path must be the redacted route, not the raw one")
}

// TestRequestPathRedaction_EveryLoggingSite is spec test 118. It is deliberately
// broader than test 93: FR-003e is stated per site because a universal claim
// with a single-instance oracle is satisfied by fixing ONE site.
//
// Coverage, stated honestly:
//
//   - Site 1 (rate-limit 429)      — driven end to end here.
//   - Site 2 (bypass-gate 503)     — driven end to end here.
//   - Sites 4+5 (CSRF reporter's slog fallback and its AUDIT entry, the worst
//     of the six because the audit chain is HMAC-linked and outlives log
//     rotation) — the real CSRF middleware is driven to a real 403 and the
//     route it hands to the reporter is asserted; the reporter closure in
//     gateway.go is then asserted from SOURCE, because it is constructed
//     inside setupServices and cannot be invoked from a package test.
//   - Site 3 (CSRF safe-method re-mint failure) — NOT drivable: it fires only
//     when crypto/rand fails, which has no injection seam. Covered by source.
//   - Site 6 (preview audit entry) — NOT drivable from this package:
//     agent.AgentLoop.auditLogger is unexported with no setter, so the test
//     harness's AuditLogger() is always nil and the entry is dropped before it
//     can be observed. Covered by source plus the /serve/ and /preview/ rows of
//     TestRedactRequestPath_PrefixDriven, which are the exact shapes that site
//     receives.
//
// Traces to: FR-003e, MV-23, spec §13 test 118.
func TestRequestPathRedaction_EveryLoggingSite(t *testing.T) {
	tokenPath := "/library-preview/" + redactionFixtureToken + "/index.html"

	t.Run("site1_rate_limit_429", func(t *testing.T) {
		logs := captureRedactionSlog(t)
		limiter := newAPIRateLimiter(1, time.Minute)
		handler := withRateLimit(limiter, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		first := httptest.NewRecorder()
		handler(first, httptest.NewRequest(http.MethodGet, tokenPath, nil))
		require.Equal(t, http.StatusOK, first.Code)

		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodGet, tokenPath, nil))
		require.Equal(t, http.StatusTooManyRequests, rec.Code, "the 429 must actually occur")

		got, ok := logs.find("api: rate limit exceeded")
		require.True(t, ok, "the 429 must have logged")
		assert.Equal(t, "/library-preview/"+pathredact.TokenPlaceholder+"/index.html", got.attrs["path"])
		assert.NotContains(t, logs.text(), redactionFixtureToken)
	})

	t.Run("site2_bypass_gate_503", func(t *testing.T) {
		logs := captureRedactionSlog(t)

		var innerCalled bool
		gated := middleware.RequireNotBypass(func(http.ResponseWriter, *http.Request) {
			innerCalled = true
		})

		cfg := &config.Config{}
		cfg.Gateway.DevModeBypass = true
		req := httptest.NewRequest(http.MethodPost, tokenPath, nil)
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, cfg))

		rec := httptest.NewRecorder()
		gated(rec, req)

		require.Equal(t, http.StatusServiceUnavailable, rec.Code,
			"the 503 must actually occur — otherwise nothing was logged and the assertions below are vacuous")
		require.False(t, innerCalled, "pre-condition: the gate must have short-circuited")

		got, ok := logs.find("gateway.admin_route_blocked_by_bypass_gate")
		require.True(t, ok, "the 503 must have logged; captured: %#v", logs.snapshot())
		assert.Equal(t, "/library-preview/"+pathredact.TokenPlaceholder+"/index.html", got.attrs["path"])
		assert.NotContains(t, logs.text(), redactionFixtureToken)
	})

	t.Run("site4_5_csrf_reporter_receives_a_route_that_redacts_clean", func(t *testing.T) {
		// The path here is /serve/<agent>/<token>/…, NOT /library-preview/…, and
		// the difference is deliberate.
		//
		// /library-preview/ is CSRF prefix-exempt (middleware.defaultExemptPrefixes)
		// so that a cookie-less POST gets FR-003j's 405 with the §10.3 policy
		// rather than a policy-free 403 from this gate. Driving it here would
		// therefore produce no rejection and no reporter call, and the subtest
		// would pass having asserted nothing — the exact false-green shape MV-23
		// warns about for the 429 oracle.
		//
		// /serve/ is not exempt and carries its token in the path just the same
		// (pathredact's second rule), so it exercises the identical property on a
		// route that genuinely reaches the gate.
		serveTokenPath := "/serve/agent-1/" + redactionFixtureToken + "/index.html"

		var (
			reported     string
			reportCalls  int
			handlerRan   bool
			nextHandlerH = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { handlerRan = true })
		)
		mw := middleware.CSRFMiddleware(
			middleware.WithReporter(func(_ *http.Request, _, route string) {
				reportCalls++
				reported = route
			}),
		)(nextHandlerH)

		req := httptest.NewRequest(http.MethodPost, serveTokenPath, strings.NewReader("{}"))
		req.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: "cookie-value"})
		req.Header.Set(middleware.CSRFHeaderName, "header-value")

		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		require.Equal(t, http.StatusForbidden, rec.Code,
			"the CSRF rejection must actually occur, or the reporter never fires")
		require.False(t, handlerRan, "pre-condition: the request must not have reached the handler")
		require.Equal(t, 1, reportCalls, "the reporter must have fired exactly once")

		// The middleware hands the raw route over deliberately (its documented
		// contract). What FR-003e requires is that the CONSUMER — gateway.go's
		// reporter closure, which writes the audit entry — redacts it. Prove
		// the redaction of exactly this value is clean; the closure is proved
		// to apply it by TestRequestPathRedaction_ReporterClosureRedactsRoute.
		assert.Equal(t, serveTokenPath, reported, "pre-condition: the reporter receives the request path")
		assert.Equal(t, "/serve/agent-1/"+pathredact.TokenPlaceholder+"/index.html",
			redactRequestPath(reported))
	})
}

// ---------------------------------------------------------------------------
// 3. Source enumeration — a SEVENTH site must fail the build
// ---------------------------------------------------------------------------

// redactionRecordingSite names one place in pkg/gateway that writes a request path into
// a log record or an audit entry. This inventory is the FR-003e claim written
// down: six sites, all redacted.
type redactionRecordingSite struct {
	file string
	what string
}

var expectedRedactionSites = []redactionRecordingSite{
	{file: "rest_auth.go", what: "withRateLimit: 429 rate-limit warning"},
	{file: "gateway.go", what: "CSRF mismatch reporter: slog fallback when no audit logger is wired"},
	{file: "gateway.go", what: "CSRF mismatch reporter: the AUDIT entry (HMAC-chained, outlives log rotation)"},
	{file: "rest_preview_audit.go", what: "emitPreviewAuditEntry: details[\"sanitized_path\"]"},
	{file: filepath.Join("middleware", "bypass_gate.go"), what: "RequireNotBypass: 503 forensic warning"},
	{file: filepath.Join("middleware", "csrf.go"), what: "CSRFMiddleware: safe-method cookie re-mint failure"},
}

// redactionImplFile is where redactRequestPath is DEFINED. Its own call
// to pathredact.RequestPath is the implementation, not a recording site.
const redactionImplFile = "preview_path_redact.go"

// redactionScannedDirs are the directories that hold the recording sites. Both are
// scanned so a new raw site in either package fails the build.
var redactionScannedDirs = []string{".", "middleware"}

// TestRequestPathRedaction_SourceInventory fails when a seventh recording site
// appears, and fails when an existing one stops redacting.
//
// Two independent properties, because each catches what the other misses:
//
//	(a) NO sink call anywhere in pkg/gateway or pkg/gateway/middleware may take
//	    a bare `*.URL.Path`. This catches a NEW raw site.
//	(b) The number of redaction calls, per file, matches the inventory above.
//	    This catches an EXISTING site quietly dropping its redaction — which
//	    (a) cannot see, because the value there arrives as a variable.
//
// Traces to: FR-003e ("stated per site"), MV-23.
func TestRequestPathRedaction_SourceInventory(t *testing.T) {
	rawSites, redactionCounts := scanRedactionSites(t)

	t.Run("no_sink_call_takes_a_raw_request_path", func(t *testing.T) {
		assert.Empty(t, rawSites,
			"FR-003e: these log/audit calls write a raw request path. A preview token lives in "+
				"that path (ADR-067 §10.5). Wrap the value in redactRequestPath (package gateway) "+
				"or pathredact.RequestPath (elsewhere), then add the site to expectedRedactionSites:\n%s",
			strings.Join(rawSites, "\n"))
	})

	t.Run("exactly_the_inventoried_sites_redact", func(t *testing.T) {
		want := map[string]int{}
		for _, site := range expectedRedactionSites {
			want[site.file]++
		}
		assert.Equal(t, want, redactionCounts,
			"the set of request-path redactions in pkg/gateway changed.\n"+
				"If you ADDED a site that records a request path, add it to expectedRedactionSites.\n"+
				"If a count DROPPED, a site stopped redacting and a preview token now reaches a log "+
				"or the audit chain (FR-003e).\ninventory: %+v", expectedRedactionSites)
	})

	t.Run("inventory_is_six_sites", func(t *testing.T) {
		// FR-003e counts six. If the product grows a seventh, this is the line
		// that says so out loud rather than letting the number drift.
		assert.Len(t, expectedRedactionSites, 6,
			"FR-003e enumerates six request-path recording sites")
	})
}

// TestRequestPathRedaction_ReporterClosureRedactsRoute guards the single worst
// site by name.
//
// gateway.go's CSRF mismatch reporter writes `route` into an audit.Entry. That
// record is HMAC-chained and outlives log rotation, so a token written there is
// the most durable leak the product can produce. The closure is built inside
// setupServices and cannot be invoked from a package test, so it is asserted
// from source: every "route" attribute inside a WithReporter closure must be a
// redaction call.
//
// It also pins the NUMBER of reporter implementations at one. The middleware
// hands the reporter a raw path by documented contract; that is only safe while
// every implementation redacts, so a second one must fail here.
//
// Traces to: FR-003e ("the worst is not a log at all").
func TestRequestPathRedaction_ReporterClosureRedactsRoute(t *testing.T) {
	fset := token.NewFileSet()
	var (
		reporterImpls int
		rawRouteUses  []string
	)

	for _, file := range redactionScanGoFiles(t, ".") {
		parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		require.NoError(t, err)

		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isRedactionSelectorCall(call, "middleware", "WithReporter") {
				return true
			}
			for _, arg := range call.Args {
				lit, isLit := arg.(*ast.FuncLit)
				if !isLit {
					continue
				}
				reporterImpls++
				rawRouteUses = append(rawRouteUses, redactionUnredactedRouteAttrs(fset, lit)...)
			}
			return true
		})
	}

	require.Equal(t, 1, reporterImpls,
		"expected exactly one middleware.WithReporter implementation. A new one must redact its "+
			"route before recording it (FR-003e) — update this test once it does.")
	assert.Empty(t, rawRouteUses,
		"a CSRF-mismatch reporter records `route` without redacting it. That value is the raw "+
			"r.URL.Path and can carry a live preview token into the HMAC-chained audit log:\n%s",
		strings.Join(rawRouteUses, "\n"))
}

// ---------------------------------------------------------------------------
// AST helpers
// ---------------------------------------------------------------------------

// redactionScanGoFiles lists the non-test .go files in dir, relative to the package dir.
func redactionScanGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	require.NotEmpty(t, out, "no Go files found in %s — the scan would pass vacuously", dir)
	return out
}

// scanRedactionSites parses pkg/gateway and pkg/gateway/middleware and returns
//
//	rawSites        — positions where a log/audit sink call takes a bare
//	                  `*.URL.Path` expression;
//	redactionCounts — per file (relative to the package dir), how many
//	                  request-path redaction calls it makes.
func scanRedactionSites(t *testing.T) (rawSites []string, redactionCounts map[string]int) {
	t.Helper()
	fset := token.NewFileSet()
	redactionCounts = map[string]int{}

	for _, dir := range redactionScannedDirs {
		for _, file := range redactionScanGoFiles(t, dir) {
			rel := filepath.Clean(file)
			rel = strings.TrimPrefix(rel, "./")
			if filepath.Base(rel) == redactionImplFile {
				continue // the definition, not a call site
			}

			parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
			require.NoError(t, err)

			ast.Inspect(parsed, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && isRedactionCall(call) {
					redactionCounts[rel]++
				}
				return true
			})

			// Taint is resolved PER FUNCTION, because a local named `path` in
			// one function has nothing to do with a local named `path` in
			// another. Walking the whole file at once would leak taint between
			// them and produce failures nobody can act on.
			for _, decl := range parsed.Decls {
				fn, isFn := decl.(*ast.FuncDecl)
				if !isFn || fn.Body == nil {
					continue
				}
				tainted := redactionTaintedLocals(fn)
				ast.Inspect(fn, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || !isRedactionRecordingSink(call) {
						return true
					}
					for _, pos := range redactionRawPathExprsIn(call, tainted) {
						rawSites = append(rawSites, fset.Position(pos).String())
					}
					return true
				})
			}
		}
	}
	return rawSites, redactionCounts
}

// isRedactionCall reports whether call is redactRequestPath(x) or
// pathredact.RequestPath(x).
func isRedactionCall(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name == "redactRequestPath"
	case *ast.SelectorExpr:
		pkg, ok := fn.X.(*ast.Ident)
		return ok && pkg.Name == "pathredact" && fn.Sel.Name == "RequestPath"
	}
	return false
}

// redactionSlogSinkMethods are the slog entry points that write a record.
var redactionSlogSinkMethods = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true, "Log": true,
	"DebugContext": true, "InfoContext": true, "WarnContext": true,
	"ErrorContext": true, "LogAttrs": true,
}

// redactionLoggerSinkMethods are pkg/logger's own entry points.
//
// THIS PACKAGE'S PRIMARY LOGGING API IS NOT slog. pkg/gateway makes dozens of
// non-test calls to logger.WarnCF / ErrorCF / InfoCF / DebugCF — including
// three inside rest_library_preview.go itself — and every one is a durable
// sink: pkg/logger/slog_bridge.go records that these write to
// $OMNIPUS_HOME/logs/gateway.log. An earlier version of this gate recognised
// only a `slog` receiver and a method literally named `Log`, so a new leak
// written in the package's own house style
// (`logger.WarnCF("rest", "…", map[string]any{"path": r.URL.Path})`) was
// invisible to it and the inventory subtests stayed green. The guard, not a
// reviewer, is supposed to catch the seventh site.
var redactionLoggerSinkMethods = map[string]bool{
	"Debug": true, "DebugC": true, "Debugf": true, "DebugCF": true,
	"Info": true, "InfoC": true, "InfoF": true, "Infof": true, "InfoCF": true,
	"Warn": true, "WarnC": true, "WarnF": true, "Warnf": true, "WarnCF": true,
	"Error": true, "ErrorC": true, "Errorf": true, "ErrorF": true, "ErrorCF": true,
	"Fatal": true, "Fatalf": true,
}

// isRedactionRecordingSink reports whether call writes a durable record: a slog
// call, a pkg/logger call, or a `.Log(...)` on an audit logger.
//
// A bare `reporter(...)` hand-off is deliberately NOT a sink — it records
// nothing itself. Its consumers are, and they are guarded by
// TestRequestPathRedaction_ReporterClosureRedactsRoute.
func isRedactionRecordingSink(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if pkg, isIdent := sel.X.(*ast.Ident); isIdent {
		switch pkg.Name {
		case "slog":
			return redactionSlogSinkMethods[sel.Sel.Name]
		case "logger":
			return redactionLoggerSinkMethods[sel.Sel.Name]
		}
	}
	// logger.Log(&audit.Entry{...}) — the audit chain.
	return sel.Sel.Name == "Log"
}

// redactionRawPathExprsIn returns the positions of every raw request path
// reaching a sink call: a literal `*.URL.Path` selector, or an identifier that
// the enclosing function derived from one without redacting it.
//
// The identifier half is not a refinement. The literal-selector-only version of
// this scan was blind to the two-line form
//
//	p := r.URL.Path
//	slog.Warn("…", "path", p)
//
// which is the shape a leak takes as soon as the value is used more than once —
// and the companion property (per-file redaction COUNTS matching the inventory)
// cannot compensate, because a file that adds a raw site adds no redaction call,
// so its count is unchanged and the two maps stay equal.
func redactionRawPathExprsIn(call *ast.CallExpr, tainted map[string]bool) []token.Pos {
	var found []token.Pos
	ast.Inspect(call, func(n ast.Node) bool {
		if inner, ok := n.(*ast.CallExpr); ok && isRedactionCall(inner) {
			return false // pruned: everything under it is redacted
		}
		if isRedactionURLPathExpr(n) {
			found = append(found, n.Pos())
			return true
		}
		if ident, ok := n.(*ast.Ident); ok && tainted[ident.Name] {
			found = append(found, ident.Pos())
		}
		return true
	})
	return found
}

// redactionTaintedLocals returns the names of locals in fn that ALIAS an
// unredacted request path.
//
// SCOPE, STATED EXACTLY, because a guard whose reach is misunderstood is worse
// than one that is narrow on purpose. A name is tainted when a single-value
// assignment or var declaration binds it to:
//
//	r.URL.Path            — the raw selector, directly
//	someAlreadyTaintedName — an alias of an alias
//
// and nothing else. Deriving a value from the path — strings.TrimPrefix,
// path.Base, a split segment — does NOT propagate taint.
//
// WHY NOT BROADER, since "anything derived from the path" is the intuitive
// rule. It was tried, and it does not work in this package: multi-value
// assignments (`x, err := f(tainted)`) taint `err`, `err` then appears in
// almost every log call in the file, and the gate reported 70 sites across nine
// files, none of them a leak. A guard that fails on everything is turned off,
// and then it guards nothing at all.
//
// WHAT THAT LEAVES UNCAUGHT, so nobody over-trusts this: a site that records a
// TRANSFORMED path (`p := strings.TrimPrefix(r.URL.Path, prefix)`) is invisible
// here. That form is caught by the companion property — such a site is a new
// recording site, and its file's redaction count will not match
// expectedRedactionSites — only when it lands in a file the inventory already
// names. The residual is real and is why FR-003e is stated per site rather than
// as a single universal claim.
//
// The fixpoint loop makes the result independent of AST walk order, which is
// not the same as source order once closures are involved.
func redactionTaintedLocals(fn *ast.FuncDecl) map[string]bool {
	tainted := map[string]bool{}

	// aliasesRawPath reports whether rhs is the raw selector itself or an
	// already-tainted name. Deliberately an equality test on the expression
	// shape, not a subtree search.
	aliasesRawPath := func(rhs ast.Expr) bool {
		if isRedactionURLPathExpr(rhs) {
			return true
		}
		ident, ok := rhs.(*ast.Ident)
		return ok && tainted[ident.Name]
	}

	for pass := 0; pass < 8; pass++ {
		before := len(tainted)
		ast.Inspect(fn, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.AssignStmt:
				if len(node.Lhs) != 1 || len(node.Rhs) != 1 || !aliasesRawPath(node.Rhs[0]) {
					return true
				}
				if ident, ok := node.Lhs[0].(*ast.Ident); ok && ident.Name != "_" {
					tainted[ident.Name] = true
				}
			case *ast.ValueSpec:
				if len(node.Names) != 1 || len(node.Values) != 1 || !aliasesRawPath(node.Values[0]) {
					return true
				}
				if node.Names[0].Name != "_" {
					tainted[node.Names[0].Name] = true
				}
			}
			return true
		})
		if len(tainted) == before {
			break
		}
	}
	return tainted
}

// isRedactionURLPathExpr reports whether n is the expression `<something>.URL.Path`.
func isRedactionURLPathExpr(n ast.Node) bool {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Path" {
		return false
	}
	inner, ok := sel.X.(*ast.SelectorExpr)
	return ok && inner.Sel.Name == "URL"
}

// isRedactionSelectorCall reports whether call is pkg.name(...).
func isRedactionSelectorCall(call *ast.CallExpr, pkg, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

// redactionUnredactedRouteAttrs returns the positions of every "route"-keyed value in
// lit that is not a redaction call. It covers both shapes the reporter uses:
// slog's alternating key/value arguments and the audit Details map literal.
func redactionUnredactedRouteAttrs(fset *token.FileSet, lit *ast.FuncLit) []string {
	var out []string
	ast.Inspect(lit, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			for i := 0; i+1 < len(node.Args); i++ {
				if redactionStringLitValue(node.Args[i]) == "route" && !isRedactionExpr(node.Args[i+1]) {
					out = append(out, fset.Position(node.Args[i+1].Pos()).String()+" (log attribute)")
				}
			}
		case *ast.KeyValueExpr:
			if redactionStringLitValue(node.Key) == "route" && !isRedactionExpr(node.Value) {
				out = append(out, fset.Position(node.Value.Pos()).String()+" (audit entry field)")
			}
		}
		return true
	})
	return out
}

func isRedactionExpr(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	return ok && isRedactionCall(call)
}

// redactionStringLitValue returns the unquoted value of a string literal, or "".
func redactionStringLitValue(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	unquoted, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return unquoted
}
