// rest_executor_smoketest_test.go — coverage for
// POST /api/v1/agents/executor-smoke-test (rest_executor_smoketest.go).
//
// Happy-path/fatal-error/timeout tests drive the REAL driver dispatch path
// (runner.NewDriver → ClaudeDriver.Run) against a fake stub-script binary via
// cli_path, the same fake-binary technique
// pkg/agent/runner/lifecycle_test.go's writeStubScript uses — proving the
// endpoint really calls through argv construction, NDJSON parsing, and event
// draining, not a mocked shortcut. Rate-limit/in-flight-cap tests inject a
// fake runner.ExternalAgentRunner via smokeTestNewDriver (mirroring how
// rest_clivalidate_test.go swaps cliTestConnection), since those tests only
// need to prove the HANDLER's own concurrency controls, not the dispatch
// path itself (already covered by the fake-binary tests).

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// postExecutorSmokeTest issues POST /api/v1/agents/executor-smoke-test with
// the given raw JSON body through the real HandleAgents carve-out (bypassing
// withAuth, matching how rest_executor_preview_test.go's postExecutorPreview
// exercises the sibling endpoint) and decodes the response.
//
// RemoteAddr is set to the test's own name: smokeTestLimiter and
// smokeTestInflight are PACKAGE-LEVEL globals shared across every test in
// this binary run, keyed by clientIP(r) (r.RemoteAddr, unparsed — any unique
// string works as the key). Without this, every call using
// httptest.NewRequest's fixed default RemoteAddr would collide in the same
// rate-limit bucket across unrelated tests (exactly what broke this suite
// before the fix — see git history), mirroring
// TestHandleSystemCliValidate_RateLimited's "dedicated IP so the window is
// not shared" convention.
func postExecutorSmokeTest(t *testing.T, api *restAPI, body string) (int, gen.ExecutorSmokeTestResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-smoke-test", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = t.Name() + ":0"
	api.HandleAgents(w, r)
	var resp gen.ExecutorSmokeTestResponse
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w.Code, resp
}

// --- (a) happy path: real driver dispatch against a fake claude-code binary ---

func TestPostAgentsExecutorSmokeTest_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	stub := writeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"result","subtype":"success","result":"2"}'
exit 0
`)
	body := fmt.Sprintf(`{"cli":"claude-code","cli_path":%q}`, stub)

	code, resp := postExecutorSmokeTest(t, api, body)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Ok)
	require.NotNil(t, resp.ResponseText)
	assert.Equal(t, "2", *resp.ResponseText)
	assert.Nil(t, resp.Error)
	assert.GreaterOrEqual(t, resp.DurationMs, 0)
}

// --- (b) fatal error event from the fake binary ---

func TestPostAgentsExecutorSmokeTest_FatalErrorEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	stub := writeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"result","subtype":"error","error":"boom happened"}'
exit 0
`)
	body := fmt.Sprintf(`{"cli":"claude-code","cli_path":%q}`, stub)

	code, resp := postExecutorSmokeTest(t, api, body)
	require.Equal(t, http.StatusOK, code, "domain failure still responds 200 per schema convention")
	assert.False(t, resp.Ok)
	require.NotNil(t, resp.Error)
	// Wave 1 (ADR-051 §RD5 MAJ-005): the response body's error field is
	// operator-visible and MUST be the sanitized generic copy, NEVER the
	// raw CLI stderr. "boom happened" is unknown-classification and maps
	// to the generic user-message copy. The raw stderr stays in
	// gateway.log only.
	// Read the copy from the catalogue rather than repeating it. A pasted
	// literal here went stale the moment the messages were rewritten, and it
	// was the THIRD hand-copy of the same string found in one pass (the other
	// two: pkg/api/generated/fixtures.go, and the two catalogues themselves,
	// which are now generated from the contract). The assertion that matters
	// is "the sanitized generic copy, not raw CLI stderr" — which is a
	// property of WHICH message is used, not of its exact wording.
	want := agent.UserMessageForCode(agent.CodeUnknown)
	assert.Equal(t, want, *resp.Error,
		"smoke-test response body must carry the sanitized generic copy, not the raw CLI stderr")
	assert.NotEqual(t, "boom happened", *resp.Error,
		"raw CLI stderr must NOT leak to the smoke-test response body (sanitizer regression)")
	assert.Nil(t, resp.ResponseText)
}

// --- Wave 1: sanitizer unit test (ADR-051 §RD5 MAJ-005) ---

// TestSanitizeSmokeTestErrorMessage_UnknownReturnsGenericCopy is the
// fail-closed invariant: when the classifier returns CodeUnknown (the
// raw stderr shape isn't matched by any pinned phrase), the smoke-test
// response carries a fixed generic copy — never the raw stderr verbatim.
//
// A regression here would re-leak raw CLI/provider text to operators
// hitting /api/v1/agents/executor-smoke-test, the same D-B incident
// shape ADR-051 was written to close.
func TestSanitizeSmokeTestErrorMessage_UnknownReturnsGenericCopy(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "totally novel raw stderr",
			raw:  "some brand new error shape nobody has seen before 12345",
		},
		{
			name: "empty raw",
			raw:  "",
		},
		{
			name: "raw with provider identity leak",
			raw:  "xai/grok: connection refused at api.x.ai",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeSmokeTestErrorMessage(tc.raw)
			assert.NotEmpty(t, got, "sanitized message must never be empty")
			if tc.raw != "" {
				assert.NotEqual(t, tc.raw, got,
					"raw input must not be returned verbatim — sanitization regression")
			}
			// Must be one of the canonical generic copies (either the
			// classifier's CodeUnknown fallback or another generic
			// user-message). Never raw.
			isKnown := false
			for _, code := range []agent.LLMErrorCode{
				agent.CodeMediaUnsupported, agent.CodeProviderRejected, agent.CodeRateLimited,
				agent.CodeNetwork, agent.CodeContentPolicy, agent.CodeContextTooLong, agent.CodeUnknown,
			} {
				if got == agent.UserMessageForCode(code) {
					isKnown = true
					break
				}
			}
			assert.True(t, isKnown || got == "The external CLI failed. See gateway.log for details.",
				"sanitized message must be a generic user-message copy or the fail-closed fallback; got=%q", got)
		})
	}
}

// --- (c) a run that never terminates within the (shortened) timeout ---

func TestPostAgentsExecutorSmokeTest_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	origTimeout := smokeTestTimeoutSeconds
	smokeTestTimeoutSeconds = 1
	defer func() { smokeTestTimeoutSeconds = origTimeout }()

	// "exec sleep" (not a bare "sleep" line) REPLACES the shell's own process
	// image via execve rather than forking a child — a bare "sleep 10" would
	// fork a grandchild that inherits a duplicate of the stdout pipe fd, so
	// killing the shell (what the driver's context-timeout cancellation does,
	// via exec.CommandContext's default cmd.Process.Kill()) would leave that
	// orphaned grandchild holding the pipe open for the full 10s regardless,
	// which is exactly what happened before this fix (the outer
	// smokeTestDrainGrace backstop fired instead of the driver's own prompt
	// timeout). With "exec", the sleeping process IS the direct child, so
	// killing it closes its stdout fd immediately.
	stub := writeScript(t, `#!/bin/sh
exec sleep 10
`)
	body := fmt.Sprintf(`{"cli":"claude-code","cli_path":%q}`, stub)

	start := time.Now()
	code, resp := postExecutorSmokeTest(t, api, body)
	elapsed := time.Since(start)

	require.Equal(t, http.StatusOK, code)
	assert.False(t, resp.Ok)
	require.NotNil(t, resp.Error)
	assert.Contains(t, strings.ToLower(*resp.Error), "timeout",
		"a bounded-out run must report a timeout-shaped error; got %q", *resp.Error)
	assert.Less(t, elapsed, 8*time.Second,
		"must bound out near smokeTestTimeoutSeconds(1s), not wait out the full 10s sleep")
}

// --- pre-spawn guard: missing binary never spawns ---

func TestPostAgentsExecutorSmokeTest_MissingBinary(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	code, resp := postExecutorSmokeTest(t, api,
		`{"cli":"claude-code","cli_path":"/nonexistent/path/to/claude-binary"}`)
	require.Equal(t, http.StatusOK, code)
	assert.False(t, resp.Ok)
	require.NotNil(t, resp.Error)
	assert.Nil(t, resp.ResponseText)
}

// --- consent: a permission request must be denied by default, not silently dropped ---

// permissionRequestFakeDriver is a runner.ExternalAgentRunner whose Run
// emits a single EventKindPermissionRequest, then blocks until Decide is
// called (recording it) or ctx ends, before closing its channel — mirroring
// how a real driver (e.g. ClaudeDriver.Decide) treats a denial as a
// run-canceling event rather than an instant channel close. Used to prove
// the smoke-test handler actually routes permission-request events through
// runner.ConsentDispatcher/RouteConsent (CRITICAL fix 1) instead of silently
// falling into a no-op default case and eventually timing out.
type permissionRequestFakeDriver struct {
	mu       sync.Mutex
	decided  bool
	decision runner.PermissionDecision
}

func (f *permissionRequestFakeDriver) Run(ctx context.Context, _ runner.RunOptions) (<-chan runner.RunEvent, error) {
	ch := make(chan runner.RunEvent, 1)
	ch <- runner.RunEvent{
		Kind: runner.EventKindPermissionRequest,
		PermissionRequest: &runner.PermissionRequestEvent{
			RequestID: "req-1",
			ToolName:  "shell",
		},
	}
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func (f *permissionRequestFakeDriver) Decide(d runner.PermissionDecision) {
	f.mu.Lock()
	f.decided = true
	f.decision = d
	f.mu.Unlock()
}
func (f *permissionRequestFakeDriver) Cancel()            {}
func (f *permissionRequestFakeDriver) Input(string) error { return nil }
func (f *permissionRequestFakeDriver) Resume(context.Context, string) (<-chan runner.RunEvent, error) {
	// Resume is unused by this fake's scenarios; return an already-closed
	// channel (rather than nil, nil) so a caller ranging over it sees an
	// immediately-completed empty run instead of a nil channel that would
	// block forever, and so the return isn't an ambiguous (nil, nil) (nilnil).
	ch := make(chan runner.RunEvent)
	close(ch)
	return ch, nil
}

func (f *permissionRequestFakeDriver) Test(context.Context) runner.ConnectionTestResult {
	return runner.ConnectionTestResult{}
}

var _ runner.ExternalAgentRunner = (*permissionRequestFakeDriver)(nil)

// TestPostAgentsExecutorSmokeTest_PermissionRequestDeniedByDefault is the
// CRITICAL fix 1 regression test: a driver attempting a tool call must be
// denied by default (FR-5.1) via the REAL runner.ConsentDispatcher/
// RouteConsent, must actually call driver.Decide(Allow:false) so the run is
// canceled, and must surface the specific smokeTestPermissionDeniedErr
// message — NOT the generic timeout message a silently-dropped
// EventKindPermissionRequest would previously fall through to.
func TestPostAgentsExecutorSmokeTest_PermissionRequestDeniedByDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	realBin := writeScript(t, "#!/bin/sh\nexit 0\n")
	fake := &permissionRequestFakeDriver{}
	orig := smokeTestNewDriver
	smokeTestNewDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return fake, nil
	}
	defer func() { smokeTestNewDriver = orig }()

	body := fmt.Sprintf(`{"cli":"claude-code","cli_path":%q}`, realBin)
	code, resp := postExecutorSmokeTest(t, api, body)
	require.Equal(t, http.StatusOK, code)
	assert.False(t, resp.Ok, "a tool-call attempt must be reported as a failed run")
	require.NotNil(t, resp.Error)
	assert.Equal(t, smokeTestPermissionDeniedErr, *resp.Error,
		"must surface the specific denial message, not a generic timeout")
	assert.Nil(t, resp.ResponseText)

	fake.mu.Lock()
	decided := fake.decided
	decision := fake.decision
	fake.mu.Unlock()
	assert.True(t, decided, "runner.ConsentDispatcher must call driver.Decide for the permission request")
	assert.False(t, decision.Allow, "the permission request must be DENIED by default (no consent handler wired)")
	assert.Equal(t, "req-1", decision.RequestID)
}

// --- dangerous cli_args filtering (defense in depth, mirrors executor-preview) ---

// capturingFakeDriver is a runner.ExternalAgentRunner whose Run records the
// exact RunOptions it was invoked with — used to assert on what actually
// reaches the driver (as opposed to what the operator originally typed).
type capturingFakeDriver struct {
	mu       sync.Mutex
	gotOpts  runner.RunOptions
	captured bool
}

func (f *capturingFakeDriver) Run(_ context.Context, opts runner.RunOptions) (<-chan runner.RunEvent, error) {
	f.mu.Lock()
	f.gotOpts = opts
	f.captured = true
	f.mu.Unlock()
	ch := make(chan runner.RunEvent, 1)
	ch <- runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "2"}}
	close(ch)
	return ch, nil
}
func (f *capturingFakeDriver) Decide(runner.PermissionDecision) {}
func (f *capturingFakeDriver) Cancel()                          {}
func (f *capturingFakeDriver) Input(string) error               { return nil }
func (f *capturingFakeDriver) Resume(context.Context, string) (<-chan runner.RunEvent, error) {
	// Resume is unused by this fake's scenarios; return an already-closed
	// channel (rather than nil, nil) so a caller ranging over it sees an
	// immediately-completed empty run instead of a nil channel that would
	// block forever, and so the return isn't an ambiguous (nil, nil) (nilnil).
	ch := make(chan runner.RunEvent)
	close(ch)
	return ch, nil
}

func (f *capturingFakeDriver) Test(context.Context) runner.ConnectionTestResult {
	return runner.ConnectionTestResult{}
}

var _ runner.ExternalAgentRunner = (*capturingFakeDriver)(nil)

// TestPostAgentsExecutorSmokeTest_DangerousCLIArgFiltered_ClaudeCode is the
// HIGH fix 5 regression test: a denylisted cli_args flag for claude-code
// (wire "claude-code", internal filter key "claude" — see runner.FilterKey)
// must be stripped before ever reaching RunOptions.CLIArgs, proving the
// shared runner.FilterKey mapping is wired correctly at THIS endpoint too
// (executor-preview already had equivalent coverage; smoke-test did not).
func TestPostAgentsExecutorSmokeTest_DangerousCLIArgFiltered_ClaudeCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	realBin := writeScript(t, "#!/bin/sh\nexit 0\n")
	fake := &capturingFakeDriver{}
	orig := smokeTestNewDriver
	smokeTestNewDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return fake, nil
	}
	defer func() { smokeTestNewDriver = orig }()

	body := fmt.Sprintf(
		`{"cli":"claude-code","cli_path":%q,"cli_args":"--add-dir /tmp/x --dangerously-skip-permissions"}`,
		realBin,
	)
	code, resp := postExecutorSmokeTest(t, api, body)
	require.Equal(t, http.StatusOK, code, "body=%+v", resp)
	assert.True(t, resp.Ok)

	fake.mu.Lock()
	gotArgs := fake.gotOpts.CLIArgs
	captured := fake.captured
	fake.mu.Unlock()
	require.True(t, captured, "driver.Run must have been called")
	for _, a := range gotArgs {
		assert.NotEqual(t, "--dangerously-skip-permissions", a,
			"the claude-code denylisted flag must be filtered before reaching RunOptions.CLIArgs; got=%v", gotArgs)
	}
	assert.Contains(t, gotArgs, "--add-dir", "benign cli_args tokens must still reach the driver; got=%v", gotArgs)
}

// --- (e) validation / method / auth ---

func TestPostAgentsExecutorSmokeTest_MissingCLI_400(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	code, _ := postExecutorSmokeTest(t, api, `{}`)
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestPostAgentsExecutorSmokeTest_UnknownCLI_400(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	code, _ := postExecutorSmokeTest(t, api, `{"cli":"gemini-cli"}`)
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestPostAgentsExecutorSmokeTest_InvalidJSON_400(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-smoke-test", strings.NewReader("{not json"))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostAgentsExecutorSmokeTest_MethodNotAllowed(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/executor-smoke-test", nil)
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestPostAgentsExecutorSmokeTest_Unauthenticated_401(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-smoke-test",
		strings.NewReader(`{"cli":"claude-code"}`))
	r.Header.Set("Content-Type", "application/json")
	// Unlike postExecutorSmokeTest's helper (which calls HandleAgents directly,
	// bypassing auth to isolate the handler logic itself, matching the sibling
	// executor-preview tests), this test exercises the REAL auth wrapping the
	// route is registered with (api.withAuth(api.HandleAgents) in gateway.go).
	handler := api.withAuth(api.HandleAgents)
	handler(w, r)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestPostAgentsExecutorSmokeTest_DoesNotShadowAgentLookup proves the
// "executor-smoke-test" reserved path segment does not swallow requests for
// a real (unrelated) agent ID — mirrors the sibling executor-preview test.
func TestPostAgentsExecutorSmokeTest_DoesNotShadowAgentLookup(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/some-other-agent-id", nil)
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- (d) rate limit + per-caller in-flight cap ---

// instantFakeDriver is a runner.ExternalAgentRunner whose Run immediately
// emits one output event and closes — used so the rate-limit test can fire
// several sequential calls fast, without real subprocess spawn/parse
// latency. Implements the full interface (compile-time asserted below).
type instantFakeDriver struct{ text string }

func (f *instantFakeDriver) Run(_ context.Context, _ runner.RunOptions) (<-chan runner.RunEvent, error) {
	ch := make(chan runner.RunEvent, 1)
	ch <- runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: f.text}}
	close(ch)
	return ch, nil
}
func (f *instantFakeDriver) Decide(runner.PermissionDecision) {}
func (f *instantFakeDriver) Cancel()                          {}
func (f *instantFakeDriver) Input(string) error               { return nil }
func (f *instantFakeDriver) Resume(context.Context, string) (<-chan runner.RunEvent, error) {
	// Resume is unused by this fake's scenarios; return an already-closed
	// channel (rather than nil, nil) so a caller ranging over it sees an
	// immediately-completed empty run instead of a nil channel that would
	// block forever, and so the return isn't an ambiguous (nil, nil) (nilnil).
	ch := make(chan runner.RunEvent)
	close(ch)
	return ch, nil
}

func (f *instantFakeDriver) Test(context.Context) runner.ConnectionTestResult {
	return runner.ConnectionTestResult{}
}

var _ runner.ExternalAgentRunner = (*instantFakeDriver)(nil)

// blockingFakeDriver is a runner.ExternalAgentRunner whose Run signals
// `entered` then blocks until `release` is closed (or ctx ends) before
// emitting a result — used to hold an in-flight slot deterministically for
// the concurrency-cap test, mirroring rest_clivalidate_test.go's
// cliTestConnection blocking stand-in.
type blockingFakeDriver struct {
	entered chan struct{}
	release chan struct{}
}

func (f *blockingFakeDriver) Run(ctx context.Context, _ runner.RunOptions) (<-chan runner.RunEvent, error) {
	ch := make(chan runner.RunEvent, 1)
	go func() {
		defer close(ch)
		f.entered <- struct{}{}
		select {
		case <-f.release:
		case <-ctx.Done():
			return
		}
		ch <- runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "2"}}
	}()
	return ch, nil
}
func (f *blockingFakeDriver) Decide(runner.PermissionDecision) {}
func (f *blockingFakeDriver) Cancel()                          {}
func (f *blockingFakeDriver) Input(string) error               { return nil }
func (f *blockingFakeDriver) Resume(context.Context, string) (<-chan runner.RunEvent, error) {
	// Resume is unused by this fake's scenarios; return an already-closed
	// channel (rather than nil, nil) so a caller ranging over it sees an
	// immediately-completed empty run instead of a nil channel that would
	// block forever, and so the return isn't an ambiguous (nil, nil) (nilnil).
	ch := make(chan runner.RunEvent)
	close(ch)
	return ch, nil
}

func (f *blockingFakeDriver) Test(context.Context) runner.ConnectionTestResult {
	return runner.ConnectionTestResult{}
}

var _ runner.ExternalAgentRunner = (*blockingFakeDriver)(nil)

// TestPostAgentsExecutorSmokeTest_InflightCapHandler drives the real HANDLER
// under concurrency: the per-caller cap is 1 (tighter than cli-validate's 2 —
// see smokeTestInflight's doc), so a SECOND concurrent request from the SAME
// caller is rejected fast with 429 + Retry-After while the first is still
// held inside an injected blocking driver; a DIFFERENT caller is unaffected.
func TestPostAgentsExecutorSmokeTest_InflightCapHandler(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX")
	}
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	// A real regular executable so the pre-spawn guard passes and control
	// reaches the injected fake driver. Its body is never actually run —
	// smokeTestNewDriver is swapped below.
	realBin := writeScript(t, "#!/bin/sh\nexit 0\n")
	body := fmt.Sprintf(`{"cli":"claude-code","cli_path":%q}`, realBin)

	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var once sync.Once
	closeRelease := func() { once.Do(func() { close(release) }) }
	defer closeRelease()

	orig := smokeTestNewDriver
	smokeTestNewDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return &blockingFakeDriver{entered: entered, release: release}, nil
	}
	defer func() { smokeTestNewDriver = orig }()

	waitEntered := func(n int) {
		for i := 0; i < n; i++ {
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatalf("timed out waiting for spawn entry %d/%d", i+1, n)
			}
		}
	}

	// First "bob" request: holds the ONE per-caller slot, blocked in the fake
	// driver. RemoteAddr is set to this test's own name so its rate-limit
	// bucket (smokeTestLimiter, IP-keyed) never collides with any other
	// test's — see postExecutorSmokeTest's doc for why that matters. The
	// in-flight cap itself is keyed by the authenticated "bob" identity
	// (cliValidateCallerKey prefers the user over IP), so this is purely
	// about isolating the separate rate-limit check.
	bobCode := make(chan int, 1)
	go func() {
		req := makeNonAdminCtxRequest(http.MethodPost, "/api/v1/agents/executor-smoke-test", body)
		req.RemoteAddr = t.Name() + ":0"
		w := httptest.NewRecorder()
		api.HandleAgents(w, req)
		bobCode <- w.Code
	}()
	waitEntered(1)

	// 2nd bob request is OVER the per-caller cap (1) → immediate 429 (never
	// reaches the driver, so no additional `entered` signal).
	req2 := makeNonAdminCtxRequest(http.MethodPost, "/api/v1/agents/executor-smoke-test", body)
	req2.RemoteAddr = t.Name() + ":0"
	w2 := httptest.NewRecorder()
	api.HandleAgents(w2, req2)
	require.Equal(t, http.StatusTooManyRequests, w2.Code, "body=%s", w2.Body.String())
	assert.NotEmpty(t, w2.Header().Get("Retry-After"), "429 must carry Retry-After")

	// A DIFFERENT caller (IP-keyed, no user context) is NOT blocked by bob's
	// cap: it acquires its own slot and reaches the driver.
	diffCode := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-smoke-test", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.44:2222"
		w := httptest.NewRecorder()
		api.HandleAgents(w, req)
		diffCode <- w.Code
	}()
	waitEntered(1) // the different caller reached the driver → unaffected by bob's cap

	closeRelease()
	select {
	case code := <-bobCode:
		assert.Equal(t, http.StatusOK, code, "the held bob request must complete 200 after release")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bob's held request to complete")
	}
	select {
	case code := <-diffCode:
		assert.Equal(t, http.StatusOK, code, "the different caller must complete 200")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the different caller to complete")
	}
}

// TestPostAgentsExecutorSmokeTest_RateLimited fires more than the dedicated
// limit (5/min) from one IP and asserts the overflow request is 429 with a
// Retry-After header. Uses the instant fake driver so 6 sequential calls run
// fast and never collide with the (per-caller, cap=1) in-flight limiter —
// each call fully completes (acquire+release) before the next fires.
func TestPostAgentsExecutorSmokeTest_RateLimited(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	realBin := writeScript(t, "#!/bin/sh\nexit 0\n")
	orig := smokeTestNewDriver
	smokeTestNewDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return &instantFakeDriver{text: "2"}, nil
	}
	defer func() { smokeTestNewDriver = orig }()

	body := fmt.Sprintf(`{"cli":"claude-code","cli_path":%q}`, realBin)
	const ip = "203.0.113.66:6666" // dedicated IP so this test's window is isolated
	fire := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-smoke-test", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		api.HandleAgents(w, req)
		return w.Code
	}

	// The limiter allows 5/min; the first 5 pass, the 6th is throttled.
	for i := 0; i < 5; i++ {
		code := fire()
		require.Equalf(t, http.StatusOK, code, "request %d of 5 must pass the limiter", i+1)
	}
	sixthReq := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-smoke-test", strings.NewReader(body))
	sixthReq.Header.Set("Content-Type", "application/json")
	sixthReq.RemoteAddr = ip
	w := httptest.NewRecorder()
	api.HandleAgents(w, sixthReq)
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "6th request must be rate-limited")
	assert.NotEmpty(t, w.Header().Get("Retry-After"), "429 must carry Retry-After")
}

// --- audit ---

// TestPostAgentsExecutorSmokeTest_AuditEmitted asserts exactly ONE audit
// event per call carrying the authenticated caller, plus {cli, binary, ok} —
// and specifically that response_text and cli_args are NEVER present in the
// audit details (the deliberate exclusion documented on
// auditExecutorSmokeTest).
func TestPostAgentsExecutorSmokeTest_AuditEmitted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	require.NoError(t, err)
	api.auditor = logger

	stub := writeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"result","subtype":"success","result":"2"}'
exit 0
`)
	req := makeNonAdminCtxRequest(http.MethodPost, "/api/v1/agents/executor-smoke-test",
		fmt.Sprintf(`{"cli":"claude-code","cli_path":%q,"cli_args":"--some-secret-looking-flag"}`, stub))
	req.RemoteAddr = t.Name() + ":0" // isolate this test's rate-limit window
	w := httptest.NewRecorder()
	api.HandleAgents(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	require.NoError(t, logger.Close())
	entries := readAuditLog(t, auditDir)

	var found []map[string]any
	for _, e := range entries {
		if e["event"] == audit.EventExecutorSmokeTest {
			found = append(found, e)
		}
	}
	require.Len(t, found, 1, "exactly one executor.smoke_test audit event per call")
	assert.Equal(t, "bob", found[0]["user"], "audit entry must attribute the authenticated caller")
	assert.Equal(t, "allow", found[0]["decision"])
	details, ok := found[0]["details"].(map[string]any)
	require.True(t, ok, "audit entry must carry details")
	assert.Equal(t, "claude-code", details["cli"])
	assert.Equal(t, true, details["ok"])
	assert.NotEmpty(t, details["binary"])
	_, hasResponseText := details["response_text"]
	assert.False(t, hasResponseText, "audit details must NEVER carry response_text")
	_, hasCLIArgs := details["cli_args"]
	assert.False(t, hasCLIArgs, "audit details must NEVER carry cli_args")
}

// --- HIGH fix 3: ephemeral workspace cleanup, all three exit paths ---

// assertNoSmokeTestRunsLeftover proves no per-run scratch directory remains
// under $OMNIPUS_HOME/executor-smoke-test-runs after a call has fully
// returned. A missing root (never created, or already empty) is fine — only
// a NON-empty root is a failure.
func assertNoSmokeTestRunsLeftover(t *testing.T, home string) {
	t.Helper()
	root := filepath.Join(home, smokeTestRunsSubdir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("reading %q: %v", root, err)
	}
	assert.Empty(t, entries, "no leftover executor-smoke-test-runs entries expected; found=%v", entries)
}

// TestPostAgentsExecutorSmokeTest_WorkspaceRemoved_Success proves the
// ephemeral scratch dir is gone after a normal successful run.
func TestPostAgentsExecutorSmokeTest_WorkspaceRemoved_Success(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	stub := writeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"result","subtype":"success","result":"2"}'
exit 0
`)
	body := fmt.Sprintf(`{"cli":"claude-code","cli_path":%q}`, stub)
	code, resp := postExecutorSmokeTest(t, api, body)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Ok)
	assertNoSmokeTestRunsLeftover(t, home)
}

// TestPostAgentsExecutorSmokeTest_WorkspaceRemoved_Error proves the
// ephemeral scratch dir is gone after a run that ends in a fatal error.
func TestPostAgentsExecutorSmokeTest_WorkspaceRemoved_Error(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	stub := writeScript(t, `#!/bin/sh
printf '%s\n' '{"type":"result","subtype":"error","error":"boom happened"}'
exit 0
`)
	body := fmt.Sprintf(`{"cli":"claude-code","cli_path":%q}`, stub)
	code, resp := postExecutorSmokeTest(t, api, body)
	require.Equal(t, http.StatusOK, code)
	assert.False(t, resp.Ok)
	assertNoSmokeTestRunsLeftover(t, home)
}

// delayedCloseFakeDriver simulates a still-alive subprocess: Run signals
// `entered`, then blocks until ctx is Done (mirroring a real driver's
// exec.CommandContext being a child of the SAME ctx the handler derives
// runCtx from), then waits teardownDelay before closing its event channel —
// modeling the real-world gap between "kill requested" (ctx canceled) and
// "the process has actually exited" (its event channel closes) that HIGH
// fix 3's drainUntilClosedOrGrace exists to wait out before the caller's
// `defer os.RemoveAll(workDir)` runs.
type delayedCloseFakeDriver struct {
	entered       chan struct{}
	teardownDelay time.Duration
}

func (f *delayedCloseFakeDriver) Run(ctx context.Context, _ runner.RunOptions) (<-chan runner.RunEvent, error) {
	ch := make(chan runner.RunEvent)
	go func() {
		defer close(ch)
		close(f.entered)
		<-ctx.Done()
		time.Sleep(f.teardownDelay)
	}()
	return ch, nil
}
func (f *delayedCloseFakeDriver) Decide(runner.PermissionDecision) {}
func (f *delayedCloseFakeDriver) Cancel()                          {}
func (f *delayedCloseFakeDriver) Input(string) error               { return nil }
func (f *delayedCloseFakeDriver) Resume(context.Context, string) (<-chan runner.RunEvent, error) {
	// Resume is unused by this fake's scenarios; return an already-closed
	// channel (rather than nil, nil) so a caller ranging over it sees an
	// immediately-completed empty run instead of a nil channel that would
	// block forever, and so the return isn't an ambiguous (nil, nil) (nilnil).
	ch := make(chan runner.RunEvent)
	close(ch)
	return ch, nil
}

func (f *delayedCloseFakeDriver) Test(context.Context) runner.ConnectionTestResult {
	return runner.ConnectionTestResult{}
}

var _ runner.ExternalAgentRunner = (*delayedCloseFakeDriver)(nil)

// TestPostAgentsExecutorSmokeTest_WorkspaceRemoved_ClientDisconnect is the
// HIGH fix 3 regression test: canceling the request context mid-run (client
// disconnect) must NOT let `defer os.RemoveAll(workDir)` race a still-alive
// subprocess. The handler must (a) actually wait — proven by elapsed time
// being at least the fake driver's simulated teardown delay, not returning
// the instant ctx is canceled — and (b) still bound that wait so it cannot
// hang forever, and (c) leave no leftover scratch directory either way.
func TestPostAgentsExecutorSmokeTest_WorkspaceRemoved_ClientDisconnect(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	realBin := writeScript(t, "#!/bin/sh\nexit 0\n")
	const teardownDelay = 300 * time.Millisecond
	fake := &delayedCloseFakeDriver{entered: make(chan struct{}), teardownDelay: teardownDelay}
	orig := smokeTestNewDriver
	smokeTestNewDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return fake, nil
	}
	defer func() { smokeTestNewDriver = orig }()

	body := fmt.Sprintf(`{"cli":"claude-code","cli_path":%q}`, realBin)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-smoke-test", strings.NewReader(body)).
		WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = t.Name() + ":0"
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		api.HandleAgents(w, req)
	}()

	select {
	case <-fake.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the driver's Run to start")
	}

	start := time.Now()
	cancel() // simulate a client disconnect mid-run

	var elapsed time.Duration
	select {
	case <-done:
		elapsed = time.Since(start)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the handler to return after client disconnect")
	}

	assert.GreaterOrEqual(
		t,
		elapsed,
		teardownDelay,
		"the handler must wait for the driver's event channel to actually close (drainUntilClosedOrGrace) before cleaning up, not return the instant ctx is canceled",
	)
	assert.Less(t, elapsed, smokeTestCancelGrace+2*time.Second,
		"the wait must still be bounded, not hang past smokeTestCancelGrace")
	assertNoSmokeTestRunsLeftover(t, home)
}

// --- HIGH fix 3: boot-time backstop sweep for orphaned scratch dirs ---

// TestSweepSmokeTestOrphans proves every directory under
// $OMNIPUS_HOME/executor-smoke-test-runs is removed, mirroring
// runner.ReapOrphans' "everything present at boot is an orphan" reasoning —
// this is the backstop for the race fixed above (a crash or an
// exceptionally slow teardown could still outrun drainUntilClosedOrGrace's
// bounded wait).
func TestSweepSmokeTestOrphans(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	root := filepath.Join(home, smokeTestRunsSubdir)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "run-orphan1"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "run-orphan2"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "run-orphan1", "marker.txt"), []byte("x"), 0o600))

	removed, errs := sweepSmokeTestOrphans()
	assert.Empty(t, errs)
	assert.Equal(t, 2, removed)

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries, "all orphan run dirs must be removed")
}

// TestSweepSmokeTestOrphans_NoRootIsNotAnError proves a fresh
// $OMNIPUS_HOME (no executor-smoke-test-runs directory yet) is handled as
// "nothing to sweep", not an error.
func TestSweepSmokeTestOrphans_NoRootIsNotAnError(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	removed, errs := sweepSmokeTestOrphans()
	assert.Equal(t, 0, removed)
	assert.Empty(t, errs)
}

// --- agent_id: run in a real agent's own bound workspace, never delete it ---

// newTestRestAPIWithWorkspaceAgent builds a restAPI whose config carries one
// real, saved custom agent with an explicit, PRE-CREATED Workspace directory
// (containing a marker file), mirroring board_task_hardening_test.go's
// newTestRestAPIWithAgent pattern. Used by the agent_id smoke-test tests
// below to assert the run actually happens in that EXACT directory (not a
// copy, not a scratch dir) and that the directory — and its pre-existing
// marker file — survive the request completely untouched, proving this
// handler never deletes a real agent's workspace.
func newTestRestAPIWithWorkspaceAgent(t *testing.T) (api *restAPI, agentID, workspace string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	// Isolates workspace.FindForAgent's $OMNIPUS_HOME/workspaces scan (the
	// CoreTeam-membership override) to this test's own tmpDir, so it can never
	// read a real, unmocked ~/.omnipus/workspaces on the machine running the
	// test — see TestPostAgentsExecutorSmokeTest_AgentIDIsCoreTeamMember below
	// for the positive case this same isolation makes exercisable.
	t.Setenv("OMNIPUS_HOME", tmpDir)
	agentID = "smoke-test-agent-01"
	workspace = filepath.Join(tmpDir, "agents", agentID)
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, "marker.txt"), []byte("pre-existing project file"), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{
					ID:   agentID,
					Name: "Smoke Test Agent",
					Type: config.AgentTypeCustom,
					Home: workspace,
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	// No workspace-membership auto-seed: these executor smoke tests set up their
	// OWN membership (none, or a specific workspace) and assert on the resolved
	// work dir; the shared harness default would pollute that resolution.
	al := mustAgentLoopNoWorkspaceSeed(t, cfg, msgBus, &restMockProvider{})
	api = &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
	}
	return api, agentID, workspace
}

// TestPostAgentsExecutorSmokeTest_AgentIDResolvesToRealWorkspace is the
// primary regression test for the architectural fix: a smoke-test request
// naming a real, saved agent_id must run in THAT agent's own persistent
// workspace directory — the exact path agent.ResolveAgentHome (and a
// real subagent_3p delegation via external_dispatch.go) would use — not the
// disposable ephemeral scratch directory, and that directory must survive
// the request (never removed), regardless of success/failure.
func TestPostAgentsExecutorSmokeTest_AgentIDResolvesToRealWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	api, agentID, workspace := newTestRestAPIWithWorkspaceAgent(t)

	realBin := writeScript(t, "#!/bin/sh\nexit 0\n")
	fake := &capturingFakeDriver{}
	orig := smokeTestNewDriver
	smokeTestNewDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return fake, nil
	}
	defer func() { smokeTestNewDriver = orig }()

	body := fmt.Sprintf(`{"cli":"claude-code","cli_path":%q,"agent_id":%q}`, realBin, agentID)
	code, resp := postExecutorSmokeTest(t, api, body)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Ok, "error=%v", resp.Error)
	assert.True(t, resp.UsedAgentWorkspace,
		"agent_id resolved to a real, saved agent — used_agent_workspace must be true")

	fake.mu.Lock()
	gotWorkDir := fake.gotOpts.WorkDir
	captured := fake.captured
	fake.mu.Unlock()
	require.True(t, captured, "driver.Run must have been called")
	assert.Equal(t, workspace, gotWorkDir,
		"the run must use the agent's OWN bound workspace, not a scratch directory")

	// The agent's workspace — and its pre-existing project file — must be
	// completely untouched by this handler: not removed, not emptied.
	info, statErr := os.Stat(workspace)
	require.NoError(t, statErr, "the agent's real workspace directory must still exist after the request")
	assert.True(t, info.IsDir())
	markerData, readErr := os.ReadFile(filepath.Join(workspace, "marker.txt"))
	require.NoError(t, readErr, "the agent's pre-existing marker file must survive the request")
	assert.Equal(t, "pre-existing project file", string(markerData))
}

// TestPostAgentsExecutorSmokeTest_AgentIDIsCoreTeamMember_UsesWorkspaceSharedDir
// proves the CoreTeam-membership override added alongside the
// pkg/agent/loop.go / external_dispatch.go fix: when agent_id names a real,
// saved agent that ALSO belongs to a real Workspace's core_team, the smoke
// test must run in that Workspace's dedicated project-work subdirectory
// ($OMNIPUS_HOME/workspaces/{id}/work/), not the agent's private
// $OMNIPUS_HOME/agents/{id}/ and not the workspace's own root directory
// (which also holds AGENT.md and the shared memory room) — mirroring exactly
// what a genuine dispatch to this agent would do, so the smoke test's "test
// the real thing" contract holds even for CoreTeam members.
func TestPostAgentsExecutorSmokeTest_AgentIDIsCoreTeamMember_UsesWorkspaceSharedDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	api, agentID, agentWorkspace := newTestRestAPIWithWorkspaceAgent(t)

	// Persist a real workspace record whose core_team includes this agent.
	home := os.Getenv("OMNIPUS_HOME")
	require.NotEmpty(t, home, "fixture must have set OMNIPUS_HOME")
	wsDir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	wsJSON := fmt.Sprintf(`{"id":"smoke-test-team-ws","core_team":[%q]}`, agentID)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "smoke-test-team-ws.json"), []byte(wsJSON), 0o644))

	realBin := writeScript(t, "#!/bin/sh\nexit 0\n")
	fake := &capturingFakeDriver{}
	orig := smokeTestNewDriver
	smokeTestNewDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return fake, nil
	}
	defer func() { smokeTestNewDriver = orig }()

	body := fmt.Sprintf(`{"cli":"claude-code","cli_path":%q,"agent_id":%q}`, realBin, agentID)
	code, resp := postExecutorSmokeTest(t, api, body)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Ok, "error=%v", resp.Error)
	assert.True(
		t,
		resp.UsedAgentWorkspace,
		"agent_id resolved to a real, saved agent — used_agent_workspace must be true even when overridden to the Workspace's shared dir",
	)

	fake.mu.Lock()
	gotWorkDir := fake.gotOpts.WorkDir
	captured := fake.captured
	fake.mu.Unlock()
	require.True(t, captured, "driver.Run must have been called")

	wsRoot := filepath.Join(home, "workspaces", "smoke-test-team-ws")
	wantDir := filepath.Join(wsRoot, "work")
	assert.Equal(t, wantDir, gotWorkDir,
		"CoreTeam membership must route the run to the Workspace's dedicated work/ directory")
	assert.NotEqual(t, agentWorkspace, gotWorkDir,
		"the run must NOT use the agent's private per-agent directory when it is a CoreTeam member")
	assert.NotEqual(t, wsRoot, gotWorkDir,
		"the run must NOT use the workspace's own root directory — only its work/ subdirectory")

	// The Workspace's dedicated work/ dir must actually exist (MkdirAll
	// applies to whichever directory was ultimately chosen).
	info, statErr := os.Stat(wantDir)
	require.NoError(t, statErr, "the workspace's work/ directory must exist after the request")
	assert.True(t, info.IsDir())

	// The agent's own private directory (and its marker file) must still be
	// completely untouched — CoreTeam override changes where the RUN happens,
	// not the agent's own directory's independent existence/contents.
	markerData, readErr := os.ReadFile(filepath.Join(agentWorkspace, "marker.txt"))
	require.NoError(t, readErr, "the agent's own pre-existing marker file must survive the request")
	assert.Equal(t, "pre-existing project file", string(markerData))
}

// runExecutorSmokeTestEphemeralScratchScenario is the shared assertion for
// both "no usable agent_id" paths (omitted entirely, or naming an agent that
// does not exist): the run must fall back to the SAME disposable ephemeral
// scratch directory under $OMNIPUS_HOME/executor-smoke-test-runs, removed
// after the request, with used_agent_workspace=false in the response.
// bodyFn builds the request body from the resolved cli_path; okMsg/usedMsg
// are the scenario-specific assertion messages.
func runExecutorSmokeTestEphemeralScratchScenario(
	t *testing.T,
	bodyFn func(cliPath string) string,
	okMsg, usedMsg string,
) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	realBin := writeScript(t, "#!/bin/sh\nexit 0\n")
	fake := &capturingFakeDriver{}
	orig := smokeTestNewDriver
	smokeTestNewDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return fake, nil
	}
	defer func() { smokeTestNewDriver = orig }()

	body := bodyFn(realBin)
	code, resp := postExecutorSmokeTest(t, api, body)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Ok, okMsg, resp.Error)
	assert.False(t, resp.UsedAgentWorkspace, usedMsg)

	fake.mu.Lock()
	gotWorkDir := fake.gotOpts.WorkDir
	captured := fake.captured
	fake.mu.Unlock()
	require.True(t, captured, "driver.Run must have been called")
	assert.True(t, strings.HasPrefix(gotWorkDir, filepath.Join(home, smokeTestRunsSubdir)),
		"expected an ephemeral scratch dir under %q, got %q", smokeTestRunsSubdir, gotWorkDir)

	assertNoSmokeTestRunsLeftover(t, home)
}

// TestPostAgentsExecutorSmokeTest_AgentIDOmitted_UsesEphemeralScratch proves
// that omitting agent_id (the create-wizard case, before an agent has been
// saved) preserves EXACTLY the pre-existing behavior.
func TestPostAgentsExecutorSmokeTest_AgentIDOmitted_UsesEphemeralScratch(t *testing.T) {
	runExecutorSmokeTestEphemeralScratchScenario(t,
		func(cliPath string) string {
			// No agent_id at all in the body.
			return fmt.Sprintf(`{"cli":"claude-code","cli_path":%q}`, cliPath)
		},
		"error=%v",
		"agent_id omitted — used_agent_workspace must be false",
	)
}

// TestPostAgentsExecutorSmokeTest_AgentIDNotFound_FallsBackToEphemeral proves
// that an agent_id naming an agent that does not exist (stale/deleted id)
// falls back to the SAME disposable ephemeral scratch directory behavior —
// not an error.
func TestPostAgentsExecutorSmokeTest_AgentIDNotFound_FallsBackToEphemeral(t *testing.T) {
	runExecutorSmokeTestEphemeralScratchScenario(t,
		func(cliPath string) string {
			return fmt.Sprintf(`{"cli":"claude-code","cli_path":%q,"agent_id":"does-not-exist-agent-id"}`, cliPath)
		},
		"a not-found agent_id must fall back to the ephemeral run, not fail; error=%v",
		"agent_id did not resolve — used_agent_workspace must be false",
	)
}
