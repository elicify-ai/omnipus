//go:build !cgo

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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
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
