// Tests for POST /api/v1/system/cli-validate (ADR-030 §11 / spec §TDD 8-12,
// 21-27). Covers the pure classification (unknown-cli / empty / non-regular /
// handshake / unauthenticated), the reason/detail/ok mappings (no raw stderr),
// and the endpoint hardening: create-parity auth (non-admin allowed, unlike an
// admin route), the dedicated rate limiter (429), the per-caller in-flight cap,
// and the {cli, resolved_path, reason} audit event.

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
)

// writeScript writes an executable shell script and returns its path.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fakecli")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return p
}

// --- Pure classification (validateCLI) ---

// TestValidateCLI_UnknownCLI: an unsupported cli is rejected WITHOUT a spawn.
func TestValidateCLI_UnknownCLI(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	// A real executable path is supplied to prove the short-circuit is on the
	// cli check, not the path — it must never be spawned.
	resp := api.validateCLI(context.Background(), "gemini-cli", "/bin/sh")
	assert.Equal(t, gen.CliValidateResponseReasonUnknownCli, resp.Reason)
	assert.False(t, resp.Ok)
	assert.Nil(t, resp.ResolvedPath, "unknown-cli must not resolve a path")
	assert.Nil(t, resp.Version)
	require.NotNil(t, resp.Detail)
	assert.Equal(t, "unsupported cli", *resp.Detail)
}

// TestValidateCLI_EmptyPathMissing: an empty cli_path short-circuits to
// missing-binary (never a $PATH fallback — FR-014).
func TestValidateCLI_EmptyPathMissing(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	resp := api.validateCLI(context.Background(), "claude-code", "")
	assert.Equal(t, gen.CliValidateResponseReasonMissingBinary, resp.Reason)
	assert.False(t, resp.Ok)
	assert.Nil(t, resp.ResolvedPath)
	require.NotNil(t, resp.Detail)
	assert.Equal(t, "not found", *resp.Detail)
}

// TestValidateCLI_NonRegularOrMissingTarget: a directory, a non-executable
// regular file, and a nonexistent path all classify missing-binary WITHOUT a
// spawn (FR-013 pre-spawn guard).
func TestValidateCLI_NonRegularOrMissingTarget(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	dir := t.TempDir()
	// Non-executable regular file.
	plain := filepath.Join(dir, "plain")
	require.NoError(t, os.WriteFile(plain, []byte("data"), 0o644))

	cases := map[string]string{
		"directory":     dir,
		"nonexecutable": plain,
		"nonexistent":   filepath.Join(dir, "nope-does-not-exist"),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			if name == "nonexecutable" && runtime.GOOS == "windows" {
				t.Skip("exec bit not meaningful on Windows")
			}
			resp := api.validateCLI(context.Background(), "claude-code", path)
			assert.Equal(t, gen.CliValidateResponseReasonMissingBinary, resp.Reason,
				"%s target must classify missing-binary", name)
			assert.Nil(t, resp.ResolvedPath)
		})
	}
}

// TestValidateCLI_BareNameResolvedViaPATH proves the MAJ-004 fix: a BARE cli
// name (not an absolute path) that resolves on $PATH must NOT be blocked as
// missing-binary by the pre-spawn guard — the runtime spawn resolves it the same
// way (US-4). Before the fix the guard os.Stat'd the raw bare name (a $PATH-less
// stat) and wrongly classified missing-binary. Here the bare "claude" resolves
// to a real fake on $PATH, so the guard passes and the spawn lands on
// unauthenticated (runs + version, no creds) — anything but missing-binary.
func TestValidateCLI_BareNameResolvedViaPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX")
	}
	dir := t.TempDir()
	// A fake "claude" on $PATH that prints a version and exits 0.
	bin := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\necho 'claude 1.2.3'\n"), 0o755))
	t.Setenv("PATH", dir)

	// Clear every claude-code credential source so the handshake lands on
	// unauthenticated deterministically (proving the guard let the spawn happen).
	empty := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CONFIG_DIR", empty)
	t.Setenv("HOME", empty)

	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	// BARE NAME, not an absolute path.
	resp := api.validateCLI(context.Background(), "claude-code", "claude")
	assert.NotEqual(t, gen.CliValidateResponseReasonMissingBinary, resp.Reason,
		"a bare name resolvable on $PATH must NOT be blocked missing-binary (MAJ-004 / US-4)")
	assert.Equal(t, gen.CliValidateResponseReasonUnauthenticated, resp.Reason,
		"the bare name resolved, ran, and reported a version but has no creds → unauthenticated")
	require.NotNil(t, resp.Version)
	assert.Equal(t, "1.2.3", *resp.Version)
}

// TestValidateCLI_HandshakeFailed: a runnable target that does not print a
// version → handshake-failed, resolved path surfaced, no version. Detail is the
// fixed message (never raw stderr).
func TestValidateCLI_HandshakeFailed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX")
	}
	// Exits non-zero and prints only to stderr — must NOT leak into detail.
	script := writeScript(t, "#!/bin/sh\necho 'secret stderr do-not-leak' 1>&2\nexit 3\n")
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	resp := api.validateCLI(context.Background(), "claude-code", script)
	assert.Equal(t, gen.CliValidateResponseReasonHandshakeFailed, resp.Reason)
	assert.False(t, resp.Ok)
	require.NotNil(t, resp.ResolvedPath, "handshake-failed surfaces the resolved path")
	assert.Nil(t, resp.Version)
	require.NotNil(t, resp.Detail)
	assert.Equal(t, "did not run or returned no version", *resp.Detail)
	assert.NotContains(t, *resp.Detail, "secret stderr", "detail must never carry raw stderr")
}

// TestValidateCLI_Unauthenticated: a runnable target that prints a version but
// has no credentials → unauthenticated (ok=true, version populated). Verifies
// ReasonOK-adjacent mapping and the fixed detail.
func TestValidateCLI_Unauthenticated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX")
	}
	// Clear every claude-code credential source so the handshake lands on
	// unauthenticated deterministically.
	empty := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CONFIG_DIR", empty)
	t.Setenv("HOME", empty)

	script := writeScript(t, "#!/bin/sh\necho 'claude 1.2.3'\nexit 0\n")
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	resp := api.validateCLI(context.Background(), "claude-code", script)
	assert.Equal(t, gen.CliValidateResponseReasonUnauthenticated, resp.Reason)
	assert.True(t, resp.Ok, "unauthenticated reports ok=true (runs + version)")
	require.NotNil(t, resp.Version)
	assert.Equal(t, "1.2.3", *resp.Version)
	require.NotNil(t, resp.ResolvedPath)
	require.NotNil(t, resp.Detail)
	assert.Equal(t, "installed; not logged in", *resp.Detail)
}

// --- Pure mapping functions ---

// TestMapValidateReason covers all runner.FailureReason values, including the
// ReasonOK("")→ok mapping (FR-018) and the fail-closed default.
func TestMapValidateReason(t *testing.T) {
	// runner.ReasonOK is the empty string; it must map to "ok" (FR-018).
	assert.Equal(t, gen.CliValidateResponseReasonOk, mapValidateReason(""))
	assert.Equal(t, gen.CliValidateResponseReasonMissingBinary, mapValidateReason("missing-binary"))
	assert.Equal(t, gen.CliValidateResponseReasonHandshakeFailed, mapValidateReason("handshake-failed"))
	assert.Equal(t, gen.CliValidateResponseReasonUnauthenticated, mapValidateReason("unauthenticated"))
	assert.Equal(t, gen.CliValidateResponseReasonUnknownCli, mapValidateReason("unknown-cli"))
	// Unrecognized reason fails closed to handshake-failed (blocks Create).
	assert.Equal(t, gen.CliValidateResponseReasonHandshakeFailed, mapValidateReason("some-future-reason"))
}

// TestReasonReportsOK: ok/unauthenticated true; everything else false.
func TestReasonReportsOK(t *testing.T) {
	assert.True(t, reasonReportsOK(gen.CliValidateResponseReasonOk))
	assert.True(t, reasonReportsOK(gen.CliValidateResponseReasonUnauthenticated))
	assert.False(t, reasonReportsOK(gen.CliValidateResponseReasonMissingBinary))
	assert.False(t, reasonReportsOK(gen.CliValidateResponseReasonHandshakeFailed))
	assert.False(t, reasonReportsOK(gen.CliValidateResponseReasonUnknownCli))
}

// TestClassifiedDetail asserts every detail is drawn from the fixed allowlisted
// set — the guarantee that raw stderr can never reach the response (FR-017).
func TestClassifiedDetail(t *testing.T) {
	allow := map[string]bool{
		"OK":                                 true,
		"installed; not logged in":           true,
		"not found":                          true,
		"did not run or returned no version": true,
		"unsupported cli":                    true,
		"validation failed":                  true,
	}
	for _, reason := range []gen.CliValidateResponseReason{
		gen.CliValidateResponseReasonOk,
		gen.CliValidateResponseReasonUnauthenticated,
		gen.CliValidateResponseReasonMissingBinary,
		gen.CliValidateResponseReasonHandshakeFailed,
		gen.CliValidateResponseReasonUnknownCli,
		"unexpected",
	} {
		d := classifiedDetail(reason)
		assert.True(t, allow[d], "detail %q for reason %q must be in the fixed allowlist", d, reason)
	}
}

// --- Concurrency cap (unit) ---

// TestInflightLimiter proves the per-caller cap: the (limit+1)th acquire fails,
// a release frees exactly one slot, and distinct callers are independent.
func TestInflightLimiter(t *testing.T) {
	l := newInflightLimiter(2)
	assert.True(t, l.acquire("bob"), "1st acquire under cap")
	assert.True(t, l.acquire("bob"), "2nd acquire at cap boundary")
	assert.False(t, l.acquire("bob"), "3rd acquire must be rejected (over cap)")
	// A different caller is unaffected by bob's slots.
	assert.True(t, l.acquire("alice"), "distinct caller has its own budget")
	// Releasing one of bob's slots frees exactly one.
	l.release("bob")
	assert.True(t, l.acquire("bob"), "acquire succeeds after a release")
	assert.False(t, l.acquire("bob"), "back at cap after re-acquire")
}

// TestHandleSystemCliValidate_InflightCapHandler drives the real HANDLER (not
// just the limiter unit) under concurrency: two in-flight requests for ONE caller
// occupy the per-caller cap (2) while blocked inside an injected spawn, so a 3rd
// request from the SAME caller is rejected fast with 429 + Retry-After — and a
// DIFFERENT caller (distinct key) is unaffected, acquiring its own slot and
// reaching the spawn. Proves the cap is per-caller and enforced at the handler,
// not merely on the limiter (FR-013).
func TestHandleSystemCliValidate_InflightCapHandler(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX")
	}
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	// A real regular executable so the pre-spawn guard (LookPath + regular-file)
	// passes and control reaches the injected connection test. Its body is never
	// run — cliTestConnection is swapped for a blocking stand-in below.
	realBin := writeScript(t, "#!/bin/sh\nexit 0\n")
	body := `{"cli":"claude-code","cli_path":"` + realBin + `"}`

	entered := make(chan struct{}, 8) // one signal per spawn that reaches the test
	release := make(chan struct{})    // closed once to unblock all held spawns
	var once sync.Once
	closeRelease := func() { once.Do(func() { close(release) }) }
	// Close on any exit path (including t.Fatalf's Goexit) so blocked goroutines
	// never leak and never leave global in-flight slots occupied for later tests.
	defer closeRelease()

	orig := cliTestConnection
	cliTestConnection = func(_ context.Context, _, _ string) runner.ConnectionTestResult {
		entered <- struct{}{}
		<-release
		return runner.ConnectionTestResult{OK: false, Reason: runner.ReasonUnauthenticated, CLIVersion: "1.2.3"}
	}
	defer func() { cliTestConnection = orig }()

	waitEntered := func(n int) {
		for i := 0; i < n; i++ {
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatalf("timed out waiting for spawn entry %d/%d", i+1, n)
			}
		}
	}

	// Two concurrent holders for caller "bob" (makeNonAdminCtxRequest → user:bob).
	bobCodes := make(chan int, 2)
	fireBob := func() {
		req := makeNonAdminCtxRequest(http.MethodPost, "/api/v1/system/cli-validate", body)
		w := httptest.NewRecorder()
		api.HandleSystemCliValidate(w, req)
		bobCodes <- w.Code
	}
	go fireBob()
	go fireBob()
	waitEntered(2) // both bob requests now hold a slot each, blocked in the spawn

	// 3rd bob request is OVER the per-caller cap → immediate 429 (never enters the
	// spawn, so no `entered` signal), with a Retry-After header.
	req3 := makeNonAdminCtxRequest(http.MethodPost, "/api/v1/system/cli-validate", body)
	w3 := httptest.NewRecorder()
	api.HandleSystemCliValidate(w3, req3)
	require.Equal(t, http.StatusTooManyRequests, w3.Code,
		"3rd concurrent request from the same caller must be 429; body=%s", w3.Body.String())
	assert.NotEmpty(t, w3.Header().Get("Retry-After"), "429 must carry Retry-After")

	// A DIFFERENT caller (IP-keyed, no user context → key != user:bob) is NOT
	// blocked by bob's cap: it acquires its own slot and reaches the spawn.
	diffCode := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/system/cli-validate", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.9:1234"
		w := httptest.NewRecorder()
		api.HandleSystemCliValidate(w, req)
		diffCode <- w.Code
	}()
	waitEntered(1) // the different caller reached the spawn → unaffected by bob's cap

	// Release everyone; all three that reached the spawn must succeed (200).
	closeRelease()
	for i := 0; i < 2; i++ {
		select {
		case code := <-bobCodes:
			assert.Equal(t, http.StatusOK, code, "a held bob request must complete 200 after release")
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for a held bob request to complete")
		}
	}
	select {
	case code := <-diffCode:
		assert.Equal(t, http.StatusOK, code, "the different caller must complete 200")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the different caller to complete")
	}
}

// --- Endpoint: create-parity auth ---

// TestHandleSystemCliValidate_CreateParity_NonAdminAllowed proves the handler
// reaches a non-"admin" caller ("bob") — the same parity as createAgent
// (plain withAuth, no additional gate). Single-user model: there is no
// admin-vs-non-admin distinction left to contrast against here — that
// coverage now lives in TestHandleSystemCliValidate_RealMux_NotBypassGated,
// which contrasts cli-validate (create-parity) against a genuinely
// bypass-gated route (sandbox-config) under dev_mode_bypass=true.
func TestHandleSystemCliValidate_CreateParity_NonAdminAllowed(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	body := `{"cli":"claude-code","cli_path":""}` // missing-binary, no spawn
	req := makeNonAdminCtxRequest(http.MethodPost, "/api/v1/system/cli-validate", body)

	w := httptest.NewRecorder()
	api.HandleSystemCliValidate(w, req)
	require.Equal(t, http.StatusOK, w.Code,
		"a non-\"admin\" caller must reach cli-validate (create-parity); body=%s", w.Body.String())
}

// TestHandleSystemCliValidate_RealMux_NotBypassGated exercises the REAL
// registerAdditionalEndpoints chain: under dev_mode_bypass a high-blast-radius
// route (sandbox-config) returns 503 via RequireNotBypass, but cli-validate is
// create-parity (plain withAuth) and returns 200 — proving no RequireNotBypass
// wrapping at registration.
func TestHandleSystemCliValidate_RealMux_NotBypassGated(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	bypassCfg := &config.Config{}
	bypassCfg.Gateway.DevModeBypass = true

	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/cli-validate",
		strings.NewReader(`{"cli":"claude-code","cli_path":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer dev-mode-bypass-sentinel")
	req.RemoteAddr = "198.51.100.10:4444" // isolate this test's rate-limit window
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, bypassCfg))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code,
		"cli-validate must NOT be RequireNotBypass gated (create-parity); got body: %s", w.Body.String())
	assert.NotEqual(t, http.StatusServiceUnavailable, w.Code)
	assert.NotEqual(t, http.StatusForbidden, w.Code)
}

// --- Endpoint: dedicated rate limiter ---

// TestHandleSystemCliValidate_RateLimited fires more than the dedicated limit
// (20/min) from one IP through the real mux chain and asserts the overflow
// request is 429 with a Retry-After header.
func TestHandleSystemCliValidate_RateLimited(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	// Inject dev-bypass so withAuth admits the request (no users/token in the
	// harness) and the request reaches the dedicated rate limiter.
	bypassCfg := &config.Config{}
	bypassCfg.Gateway.DevModeBypass = true

	const ip = "203.0.113.77:5555" // dedicated IP so the window is not shared
	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/system/cli-validate",
			strings.NewReader(`{"cli":"claude-code","cli_path":""}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer dev-mode-bypass-sentinel")
		req.RemoteAddr = ip
		return req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, bypassCfg))
	}
	fire := func() int {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, newReq())
		return w.Code
	}

	// The limiter allows 20/min; the first 20 pass, the 21st is throttled.
	for i := 0; i < 20; i++ {
		code := fire()
		require.Equalf(t, http.StatusOK, code, "request %d of 20 must pass the limiter", i+1)
	}
	lw := httptest.NewRecorder()
	mux.ServeHTTP(lw, newReq())
	assert.Equal(t, http.StatusTooManyRequests, lw.Code, "21st request must be rate-limited")
	assert.NotEmpty(t, lw.Header().Get("Retry-After"), "429 must carry Retry-After")
}

// --- Endpoint: audit ---

// TestHandleSystemCliValidate_AuditEmitted asserts exactly ONE audit event per
// call carrying the authenticated caller (User — M-1) plus the {cli,
// requested_path, resolved_path, reason} details (FR-013 / SC-009), including on
// a no-spawn (missing-binary) classification.
func TestHandleSystemCliValidate_AuditEmitted(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	require.NoError(t, err)
	api.auditor = logger

	// Inject an authenticated non-admin caller ("bob") so the audit User is
	// populated from the request context (M-1). A raw path with surrounding
	// whitespace proves the RAW requested value is recorded even though the
	// classifier trims it — the target is still empty→missing-binary (no spawn).
	req := makeNonAdminCtxRequest(http.MethodPost, "/api/v1/system/cli-validate",
		`{"cli":"claude-code","cli_path":"   "}`)
	w := httptest.NewRecorder()
	api.HandleSystemCliValidate(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	require.NoError(t, logger.Close())
	entries := readAuditLog(t, auditDir)

	var found []map[string]any
	for _, e := range entries {
		if e["event"] == audit.EventCliValidate {
			found = append(found, e)
		}
	}
	require.Len(t, found, 1, "exactly one cli.validate audit event per call")
	assert.Equal(t, "bob", found[0]["user"], "audit entry must attribute the authenticated caller (M-1)")
	details, ok := found[0]["details"].(map[string]any)
	require.True(t, ok, "audit entry must carry details")
	assert.Equal(t, "claude-code", details["cli"])
	assert.Equal(t, "missing-binary", details["reason"])
	reqPath, hasRequested := details["requested_path"]
	assert.True(t, hasRequested, "audit details must record the raw requested_path even on a no-spawn denial")
	assert.Equal(t, "   ", reqPath, "requested_path must be the RAW (untrimmed) caller value")
	_, hasResolved := details["resolved_path"]
	assert.True(t, hasResolved, "audit details must include resolved_path")
}
