// browser_inspect_test.go — ADR-039 D-B3 (best-effort DOM inspect) tests for
// pkg/gateway/browser_inspect.go's POST /api/v1/browser/inspect handler.
//
// Covers the trust-boundary logic that does NOT require a live Chromium tab
// (mirrors browser_ws_test.go's own scope note for the sibling live-view
// socket): auth rejection via withAuth, the method gate, the
// LiveViewEnabled config gate, request validation, and the "no
// BrowserManager for this agent" best-effort path. Real DOM resolution
// (BrowserManager.InspectPoint's actual document.elementFromPoint round
// trip) is covered by pkg/tools/browser/inspect_test.go's skipIfNoBrowser-
// gated E2E pair, plus the UAT matrix — not re-tested here.
//
// Traces to: docs/internal/architecture/ADR-039-user-initiated-browsing-and-annotate.md (D-B3)

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

	"github.com/elicify-ai/omnipus/pkg/agent"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newBrowserInspectTestAPI builds a restAPI via the shared newTestRestAPIWithHome
// helper (rest_extra_test.go) with tools.browser.live_view_enabled=true (the
// ADR-038/039 operator default — see config/defaults.go), so tests exercise
// the endpoint's OWN logic rather than tripping the config gate by accident.
func newBrowserInspectTestAPI(t *testing.T) *restAPI {
	t.Helper()
	api := newTestRestAPIWithHome(t)
	api.agentLoop.GetConfig().Tools.Browser.LiveViewEnabled = true
	return api
}

// inspectRequestBody marshals a generated.BrowserInspectRequest for use as a
// request body — the one legal cross-boundary type per Constraint #8.
func inspectRequestBody(t *testing.T, req gen.BrowserInspectRequest) *strings.Reader {
	t.Helper()
	data, err := json.Marshal(req)
	require.NoError(t, err)
	return strings.NewReader(string(data))
}

// ---------------------------------------------------------------------------
// Auth rejection (withAuth)
// ---------------------------------------------------------------------------

// TestHandleBrowserInspect_NoAuth_Rejected verifies the endpoint is genuinely
// gated by withAuth: with a bearer token configured, a request with no
// Authorization header is rejected with 401 before any business logic runs.
// BDD: Given OMNIPUS_BEARER_TOKEN is configured,
// When POST /api/v1/browser/inspect is called with no Authorization header,
// Then the response is 401 Unauthorized.
func TestHandleBrowserInspect_NoAuth_Rejected(t *testing.T) {
	// newBrowserInspectTestAPI (via newTestRestAPIWithHome) itself calls
	// t.Setenv("OMNIPUS_BEARER_TOKEN", "") — the env var must be set AFTER
	// that call, or this Setenv would be silently clobbered back to empty.
	api := newBrowserInspectTestAPI(t)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "test-inspect-token-abc123")
	handler := api.withAuth(api.HandleBrowserInspect)

	body := inspectRequestBody(t, gen.BrowserInspectRequest{AgentId: "a1", SessionId: "s1", X: 1, Y: 1})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/browser/inspect", body)
	r.Header.Set("Content-Type", "application/json")
	handler(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestHandleBrowserInspect_WrongToken_Rejected verifies a bearer token that
// does not match the configured one is rejected with 401.
func TestHandleBrowserInspect_WrongToken_Rejected(t *testing.T) {
	api := newBrowserInspectTestAPI(t)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "test-inspect-token-abc123")
	handler := api.withAuth(api.HandleBrowserInspect)

	body := inspectRequestBody(t, gen.BrowserInspectRequest{AgentId: "a1", SessionId: "s1", X: 1, Y: 1})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/browser/inspect", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer totally-wrong-token")
	handler(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestHandleBrowserInspect_ValidToken_Proceeds verifies a correct bearer
// token is accepted and the request reaches the handler's own logic (proven
// here by getting the best-effort 200/ok:false "no manager" response rather
// than a 401) — i.e. withAuth is wired, not just present.
func TestHandleBrowserInspect_ValidToken_Proceeds(t *testing.T) {
	api := newBrowserInspectTestAPI(t)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "test-inspect-token-abc123")
	handler := api.withAuth(api.HandleBrowserInspect)

	body := inspectRequestBody(t, gen.BrowserInspectRequest{AgentId: "no-such-agent", SessionId: "s1", X: 1, Y: 1})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/browser/inspect", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-inspect-token-abc123")
	handler(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp gen.BrowserInspectResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Ok)
}

// ---------------------------------------------------------------------------
// Method gate
// ---------------------------------------------------------------------------

// TestHandleBrowserInspect_WrongMethod_Rejected verifies a non-POST method is
// rejected with 405, mirroring HandleCreateBackup's method-check idiom.
func TestHandleBrowserInspect_WrongMethod_Rejected(t *testing.T) {
	api := newBrowserInspectTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/browser/inspect", nil)
	api.HandleBrowserInspect(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ---------------------------------------------------------------------------
// LiveViewEnabled config gate
// ---------------------------------------------------------------------------

// TestHandleBrowserInspect_LiveViewDisabled_Forbidden verifies the endpoint
// refuses to run at all when tools.browser.live_view_enabled=false — the same
// operator kill-switch the live-view WS (browser_ws.go) honors.
// BDD: Given tools.browser.live_view_enabled=false,
// When POST /api/v1/browser/inspect is called with an otherwise-valid body,
// Then the response is 403 naming the config key.
func TestHandleBrowserInspect_LiveViewDisabled_Forbidden(t *testing.T) {
	api := newTestRestAPIWithHome(t) // LiveViewEnabled left at its zero value (false)

	body := inspectRequestBody(t, gen.BrowserInspectRequest{AgentId: "a1", SessionId: "s1", X: 1, Y: 1})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/browser/inspect", body)
	r.Header.Set("Content-Type", "application/json")
	api.HandleBrowserInspect(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "live_view_enabled")
}

// ---------------------------------------------------------------------------
// Request validation
// ---------------------------------------------------------------------------

// TestHandleBrowserInspect_Validation table-tests every request-shape
// rejection the handler is responsible for (unconditionally, regardless of
// gateway.validate_inbound — see HandleBrowserInspect's doc comment): empty
// agent_id, empty session_id, and negative x/y coordinates each produce 400.
func TestHandleBrowserInspect_Validation(t *testing.T) {
	cases := []struct {
		name string
		req  gen.BrowserInspectRequest
	}{
		{"empty agent_id", gen.BrowserInspectRequest{AgentId: "", SessionId: "s1", X: 1, Y: 1}},
		{"empty session_id", gen.BrowserInspectRequest{AgentId: "a1", SessionId: "", X: 1, Y: 1}},
		{"negative x", gen.BrowserInspectRequest{AgentId: "a1", SessionId: "s1", X: -5, Y: 1}},
		{"negative y", gen.BrowserInspectRequest{AgentId: "a1", SessionId: "s1", X: 1, Y: -5}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newBrowserInspectTestAPI(t)

			body := inspectRequestBody(t, tc.req)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/browser/inspect", body)
			r.Header.Set("Content-Type", "application/json")
			api.HandleBrowserInspect(w, r)

			assert.Equal(t, http.StatusBadRequest, w.Code, "case %q must be rejected with 400", tc.name)
		})
	}
}

// TestHandleBrowserInspect_MalformedJSON_Rejected verifies a non-JSON body is
// rejected with 400 rather than a panic or 500.
func TestHandleBrowserInspect_MalformedJSON_Rejected(t *testing.T) {
	api := newBrowserInspectTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/browser/inspect", strings.NewReader("{not json"))
	r.Header.Set("Content-Type", "application/json")
	api.HandleBrowserInspect(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ---------------------------------------------------------------------------
// No BrowserManager for the target agent (best-effort path)
// ---------------------------------------------------------------------------

// TestHandleBrowserInspect_NoManagerForAgent verifies that a well-formed
// request against an agent id with no registered BrowserManager produces a
// 200 OK with ok=false + a reason naming the agent — best-effort per the ADR
// (never a 404/500 for this case, since the annotate-and-discuss feature
// must degrade gracefully to the image-only path).
// BDD: Given no agent named "agent-that-does-not-exist" has a BrowserManager,
// When POST /api/v1/browser/inspect targets that agent id,
// Then 200 OK with {"ok":false,"reason":"...agent-that-does-not-exist..."}.
func TestHandleBrowserInspect_NoManagerForAgent(t *testing.T) {
	api := newBrowserInspectTestAPI(t)

	body := inspectRequestBody(t, gen.BrowserInspectRequest{
		AgentId: "agent-that-does-not-exist", SessionId: "sess-1", X: 10, Y: 10,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/browser/inspect", body)
	r.Header.Set("Content-Type", "application/json")
	api.HandleBrowserInspect(w, r)

	require.Equal(t, http.StatusOK, w.Code, "missing manager must be a soft ok:false, never an HTTP error")
	var resp gen.BrowserInspectResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Ok)
	require.NotNil(t, resp.Reason)
	assert.Contains(t, *resp.Reason, "agent-that-does-not-exist")
	assert.Nil(t, resp.Tag)
	assert.Nil(t, resp.Text)
	assert.Nil(t, resp.Html)
}

// ---------------------------------------------------------------------------
// InspectPoint hard-error branch + Ok:true success marshaling
// (7-reviewer MEDIUM finding: test coverage)
// ---------------------------------------------------------------------------

// newBrowserInspectTestAPIWithMutate mirrors newTestRestAPIWithHome
// (rest_extra_test.go) exactly, except it applies mutate to cfg BEFORE the
// AgentLoop (and hence its per-agent BrowserManager — registerSharedTools
// captures cfg.Tools.Browser.ProfileDir once, at construction time, per
// pkg/agent/loop.go) is built. newTestRestAPIWithHome/newBrowserInspectTestAPI
// have no such hook (they mutate the config AFTER construction, which is too
// late to influence how the manager itself gets built), so the two tests
// below — which need control over the manager's profile dir and headless
// mode before registration — duplicate that helper's shape locally rather
// than changing a widely-shared helper's signature for one file.
func newBrowserInspectTestAPIWithMutate(t *testing.T, mutate func(cfg *config.Config)) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			// This test resolves the default agent (browser capture is keyed to
			// it). There is no implicit "main" sentinel to be that agent any
			// more (ADR-064).
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	cfg.Tools.Browser.LiveViewEnabled = true
	if mutate != nil {
		mutate(cfg)
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
		taskLock:      task.TaskFileLock,
	}
}

// TestHandleBrowserInspect_InspectPointHardError_SoftErrorWithReason covers
// the ONE hard-error branch InspectPoint can return (HandleBrowserInspect's
// `err != nil` path) — the manager could not even produce a tab session — at
// the gateway layer, mirroring pkg/tools/browser/inspect_test.go's
// TestInspectPoint_SessionError trick (force ensureStarted() to fail) but
// via a knob actually reachable through config.BrowserToolConfig, which has
// no ExecPath field of its own (unlike browser.BrowserConfig): put a REGULAR
// FILE where a directory has to be, so the MkdirAll that precedes launch
// fails deterministically and near-instantly with ENOTDIR, before ever
// touching exec discovery or CDP — no real Chromium involved, no skip needed.
//
// WHICH path has to be the file changed under ADR-075, and getting it wrong
// is silent: BrowserPool no longer launches into cfg.ProfileDir itself. It
// treats that value's PARENT as the profile ROOT (pool.go's profileRoot() =
// filepath.Dir(filepath.Clean(cfg.ProfileDir))) and launches each workspace
// into its own <root>/ws-<id> beneath it. So pointing ProfileDir straight at
// a regular file stopped blocking anything — the parent is a perfectly good
// directory, MkdirAll succeeded, a REAL Chrome launched, and this test spent
// ~44s doing the one thing its own comment promises it never does before
// failing on ok=true. The blocker therefore has to sit one level UP, at the
// profile root, which is what ProfileDir's parent now is.
// BDD: Given the target agent's BrowserManager has a ProfileDir that is
// actually a file (not a directory),
// When POST /api/v1/browser/inspect is called for that agent,
// Then the response is 200 OK with ok=false and a reason naming the
// failure — never a 5xx (best-effort per the ADR) — and the failure is
// logged (HandleBrowserInspect's slog.Warn call), not silently swallowed.
func TestHandleBrowserInspect_InspectPointHardError_SoftErrorWithReason(t *testing.T) {
	tmpDir := t.TempDir()
	blockerPath := filepath.Join(tmpDir, "profile-dir-is-actually-a-file")
	require.NoError(t, os.WriteFile(blockerPath, []byte("not a directory"), 0o600))

	api := newBrowserInspectTestAPIWithMutate(t, func(cfg *config.Config) {
		// blockerPath becomes the profile ROOT (see the note above), so every
		// per-workspace MkdirAll under it fails on a non-directory component.
		cfg.Tools.Browser.ProfileDir = filepath.Join(blockerPath, "profiles")
	})

	defaultAgent := api.agentLoop.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "test fixture must seed at least one agent")
	_, outcome := api.agentLoop.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome,
		"registerSharedTools must have built a browser even though its "+
			"profile dir is unusable — the failure only surfaces lazily, on first Session() call")

	body := inspectRequestBody(t, gen.BrowserInspectRequest{
		AgentId: defaultAgent.ID, SessionId: "sess-hard-error", X: 10, Y: 10,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/browser/inspect", body)
	r.Header.Set("Content-Type", "application/json")
	api.HandleBrowserInspect(w, r)

	require.Equal(t, http.StatusOK, w.Code, "InspectPoint's hard-error case must still be a soft ok:false, never a 5xx")
	var resp gen.BrowserInspectResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Ok)
	require.NotNil(t, resp.Reason)
	assert.Contains(t, *resp.Reason, "inspect failed")
	assert.Nil(t, resp.Tag)
	assert.Nil(t, resp.Text)
	assert.Nil(t, resp.Html)
}

// --- Ok:true success marshaling (real Chromium required) ---

const browserInspectSuccessHTML = `<!DOCTYPE html>
<html>
<head><title>Inspect Endpoint Test</title></head>
<body style="margin:0">
  <button id="target" style="position:absolute;left:10px;top:10px;width:100px;height:40px;">Click me</button>
</body>
</html>`

// TestHandleBrowserInspect_Success_MarshalsTagTextHtml is a real, end-to-end
// (real headless Chromium) check that a resolved element's Ok=true response
// marshals Tag/Text/Html as non-nil pointers with the expected content — the
// success branch the 7-reviewer gate flagged as uncovered at the gateway
// layer. The Ok:false paths above are all covered without a browser;
// Ok:true genuinely requires InspectPoint to resolve a real DOM element
// against a real page (BrowserManager.InspectPoint is a concrete method, not
// an interface seam, so this isn't mockable). Gated by
// browserWSSkipIfNoBrowser (browser_ws_test.go, same package) exactly like
// this file's sibling full-round-trip WS test: SKIP in this devpod's
// no-working-Chromium environment (per
// pkg/tools/browser/inspect_test.go's own note that this exact behavior is
// otherwise a UAT-matrix item), REAL coverage wherever a working browser is
// available.
// BDD: Given a page with a <button> at a known box, loaded on the agent's
// shared live-view tab,
// When POST /api/v1/browser/inspect targets a point inside that box,
// Then 200 OK with ok=true, tag="button", text containing "Click me", and
// html containing "<button".
func TestHandleBrowserInspect_Success_MarshalsTagTextHtml(t *testing.T) {
	browserWSSkipIfNoBrowser(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, browserInspectSuccessHTML)
	})
	pageSrv := httptest.NewServer(mux)
	t.Cleanup(pageSrv.Close)

	tmpDir := t.TempDir()
	api := newBrowserInspectTestAPIWithMutate(t, func(cfg *config.Config) {
		cfg.Tools.Browser.Headless = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		cfg.Tools.Browser.PageTimeoutSec = 30
	})

	defaultAgent := api.agentLoop.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "test fixture must seed at least one agent")
	mgr, outcome := api.agentLoop.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)
	t.Cleanup(mgr.Shutdown)

	tabCtx, err := mgr.Session(mgr.OperatorSessionID())
	require.NoError(t, err)
	navCtx, cancel := context.WithTimeout(tabCtx, mgr.PageTimeout())
	defer cancel()
	require.NoError(t, chromedp.Run(navCtx, chromedp.Navigate(pageSrv.URL)))

	body := inspectRequestBody(t, gen.BrowserInspectRequest{
		AgentId: defaultAgent.ID, SessionId: "sess-success", X: 30, Y: 20, // inside the button's [10,10]-[110,50] box
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/browser/inspect", body)
	r.Header.Set("Content-Type", "application/json")
	api.HandleBrowserInspect(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp gen.BrowserInspectResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Ok)
	require.NotNil(t, resp.Tag)
	require.NotNil(t, resp.Text)
	require.NotNil(t, resp.Html)
	assert.Equal(t, "button", *resp.Tag)
	assert.Contains(t, *resp.Text, "Click me")
	assert.Contains(t, strings.ToLower(*resp.Html), "<button")
	assert.Nil(t, resp.Reason, "a successful resolve must not carry a Reason")
}
