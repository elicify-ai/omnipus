// ws_auth_fastfail_test.go — regression tests for the WebSocket handshake
// that could never authenticate and never said so.
//
// THE DEFECT. Both WS auth paths (WSHandler.authenticateWS in websocket.go and
// BrowserWSHandler.authenticate in browser_ws.go) resolve the omnipus-session
// cookie first and, failing that, block up to ten seconds reading a legacy
// {"type":"auth","token":...} first frame. The SPA stopped sending that frame
// at the Wave-1 cookie cutover (src/lib/ws.ts: "no client-sent {type:'auth',
// token} frame is needed or possible"). So on any install where the cookie
// does not resolve, the server waited ten seconds for a frame that cannot
// arrive, logged one line to itself, and returned having written NOTHING to
// the client — no error frame, no close frame, no reason.
//
// Measured on a live gateway with gateway.dev_mode_bypass=true and
// gateway.users=[]: 55 WS auth failures, 0 successes, against 2023 successful
// REST AUTH-BYPASS hits. The SPA loaded, the agent picker populated, the
// screen said "Your agent is ready. Start a conversation below." — and chat
// could never connect. A tester lost a full run to it before diagnosing it.
//
// WHAT IS ASSERTED HERE. Not that dev_mode_bypass authenticates a WebSocket —
// by operator directive it deliberately does not, and these tests would fail
// if someone made it. What is asserted is that a doomed handshake DIES FAST
// AND SAYS WHY: an error frame the SPA can render, a 1008 close carrying a
// specific technical reason, and a distinguishable reason code per failure
// state (stale cookie / never signed in / nothing configured / dev-bypass).
//
// Why these tests did not exist before: every pre-existing dev-bypass WS test
// (TestBrowserWS_Auth_DevModeBypass_ConnectionProceeds,
// TestWSHandlerAuthNotRequired_NoFirstFrameNeeded, …) sends an auth frame
// first, i.e. models a client the SPA has not been for two waves. They were
// green throughout the outage. The distinguishing input is NOT sending one.

package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// wsRefusalFrame decodes the server→client error frame a rejected handshake
// emits. Test-only.
type wsRefusalFrame struct { // not-wire-format: decode-only test assertion target, never emitted over the WebSocket connection.
	Type    string `json:"type"`
	Message string `json:"message"`
}

// wsFastFailBudget is how long a browser handshake that cannot authenticate
// may take to reach a decisive outcome.
//
// It is deliberately far below the ten-second auth-frame deadline the defect
// exposed, and far above any plausible loopback round trip — so it cannot pass
// by accident on a slow machine, and cannot pass at all if the server goes
// back to waiting on the frame read. This is the assertion that actually
// fails against the unfixed code.
const wsFastFailBudget = 3 * time.Second

// dialWSAsBrowser dials path on srv with an Origin header, which is what makes
// the server treat the connection as a browser: a browser's WebSocket
// constructor is required to send Origin, and the gorilla dialer used by every
// other test in this package (and by omnipus run / the CLI readiness probe)
// does not. Origin is set to srv's own URL so wsCheckOrigin's same-origin rule
// admits the upgrade — the point of the test is the AUTH decision, not the
// origin decision.
func dialWSAsBrowser(t *testing.T, srv *httptest.Server, path string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, http.Header{"Origin": []string{srv.URL}})
	if resp != nil {
		resp.Body.Close()
	}
	require.NoError(t, err, "the upgrade itself must succeed — auth is decided after it, on the socket")
	return conn
}

// readWSAuthRefusal reads until the connection closes, returning the last
// error-frame message plus the close code and reason. It fails the test if the
// connection produces neither within budget — which is precisely the defect:
// the old code produced nothing at all and let the client's own read time out.
func readWSAuthRefusal(t *testing.T, conn *websocket.Conn, budget time.Duration) (msg string, closeCode int, closeReason string) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(budget)))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				return msg, closeErr.Code, closeErr.Text
			}
			t.Fatalf("no refusal arrived within %s — the server said nothing and let the client time out "+
				"(this is the silent-handshake defect); read error: %v", budget, err)
		}
		var frame wsRefusalFrame
		if json.Unmarshal(raw, &frame) == nil && frame.Type == "error" {
			msg = frame.Message
		}
	}
}

// assertDecisiveRefusal asserts the three things a refusal must carry: human
// copy for the SPA, the 1008 policy-violation code the SPA already routes to
// forceLogout, and a non-empty technical reason an operator can act on.
func assertDecisiveRefusal(t *testing.T, msg string, code int, reason string) {
	t.Helper()
	assert.NotEmpty(t, msg, "the SPA must receive a human-readable error frame, not an unexplained disconnect")
	assert.Equal(t, websocket.ClosePolicyViolation, code,
		"a refused handshake must close 1008 so the SPA stops reconnecting and routes to forceLogout")
	assert.NotEmpty(t, reason, "the close frame must carry a reason an operator can act on")
	assert.LessOrEqual(t, len(reason), wsCloseReasonMaxBytes,
		"a close reason over the control-frame limit is never transmitted at all")
}

// ---------------------------------------------------------------------------
// The reported configuration, end to end, on both sockets
// ---------------------------------------------------------------------------

// TestWSAuth_DevBypassNoUsers_BrowserSendsNoAuthFrame_RefusedFastWithReason is
// the defect, reproduced: dev_mode_bypass=true, gateway.users=[], a browser
// handshake with no cookie and no auth frame.
//
// BDD: Given dev_mode_bypass is on and no accounts are configured,
// When a browser opens the chat WebSocket and sends no auth frame,
// Then it is refused within seconds with an error frame and a 1008 close
// naming dev_mode_bypass — never a silent ten-second timeout.
func TestWSAuth_DevBypassNoUsers_BrowserSendsNoAuthFrame_RefusedFastWithReason(t *testing.T) {
	handler, _, _ := newTestWSHandler(t) // DevModeBypass: true, Users: nil
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialWSAsBrowser(t, srv, "/api/v1/chat/ws")
	t.Cleanup(func() { _ = conn.Close() })

	start := time.Now()
	msg, code, reason := readWSAuthRefusal(t, conn, wsFastFailBudget)
	elapsed := time.Since(start)

	assertDecisiveRefusal(t, msg, code, reason)
	assert.Less(t, elapsed, wsFastFailBudget,
		"the refusal must be immediate, not the tail of an auth-frame read deadline")
	assert.Contains(t, reason, "dev_mode_bypass",
		"the operator must be told why REST being open does not mean the WebSocket is")
	assert.Equal(t, wsAuthErrNoUsers, msg,
		"the user-facing copy must stay the vetted shared constant, not a raw protocol string")
}

// TestBrowserWSAuth_DevBypassNoUsers_BrowserSendsNoAuthFrame_RefusedFastWithReason
// is the same scenario on the browser-live socket, which carries an
// independent copy of the same handshake logic. The two must not drift: a fix
// applied to one and not the other leaves half the product silently broken.
func TestBrowserWSAuth_DevBypassNoUsers_BrowserSendsNoAuthFrame_RefusedFastWithReason(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil) // DevModeBypass: true, Users: nil
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialWSAsBrowser(t, srv, "/api/v1/browser/ws")
	t.Cleanup(func() { _ = conn.Close() })

	start := time.Now()
	msg, code, reason := readWSAuthRefusal(t, conn, wsFastFailBudget)
	elapsed := time.Since(start)

	assertDecisiveRefusal(t, msg, code, reason)
	assert.Less(t, elapsed, wsFastFailBudget,
		"browser-live must refuse as fast as chat does")
	assert.Contains(t, reason, "dev_mode_bypass")
}

// TestWSAuth_AccountsConfigured_BrowserWithoutCookie_RefusedFastAsNotSignedIn
// covers the other browser-side state an operator sees in the wild: accounts
// DO exist and the browser simply has no session cookie. Before the fix this
// was the same silent ten-second nothing; it must now be its own reason, since
// the user action differs (sign in, rather than finish onboarding).
func TestWSAuth_AccountsConfigured_BrowserWithoutCookie_RefusedFastAsNotSignedIn(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		hash, err := bcrypt.GenerateFromPassword([]byte("omnipus_"+strings.Repeat("3", 64)), bcrypt.MinCost)
		require.NoError(t, err)
		cfg.Gateway.Users = []config.UserConfig{
			{Username: "operator", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(hash)}}},
		}
	})
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialWSAsBrowser(t, srv, "/api/v1/browser/ws")
	t.Cleanup(func() { _ = conn.Close() })

	msg, code, reason := readWSAuthRefusal(t, conn, wsFastFailBudget)

	assertDecisiveRefusal(t, msg, code, reason)
	assert.Equal(t, wsAuthErrNotSignedIn, msg,
		"an account exists and no session was presented — that is 'not signed in', not 'setup incomplete'")
	assert.Contains(t, reason, "no session cookie")
}

// ---------------------------------------------------------------------------
// The read-failure path: still silent, or now loud?
// ---------------------------------------------------------------------------

// TestWSAuth_AuthFrameNeverArrives_ClientIsToldWhy covers the branch that used
// to be the whole bug in one line — `slog.Warn("ws: auth read failed");
// return false`, with nothing written to the client. A programmatic client
// keeps its full frame window (this test shortens it only so the assertion
// does not cost ten real seconds), but when that window closes it must now
// learn why instead of watching the socket evaporate.
func TestWSAuth_AuthFrameNeverArrives_ClientIsToldWhy(t *testing.T) {
	restore := wsAuthFrameDeadline
	wsAuthFrameDeadline = 150 * time.Millisecond
	t.Cleanup(func() { wsAuthFrameDeadline = restore })

	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// No Origin header: a programmatic client, which is NOT fast-failed
	// pre-read. It simply never sends its frame.
	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	msg, code, reason := readWSAuthRefusal(t, conn, wsFastFailBudget)

	assertDecisiveRefusal(t, msg, code, reason)
	assert.Contains(t, reason, "auth frame",
		"the reason must name the actual failure — no auth frame within the deadline")
}

// ---------------------------------------------------------------------------
// classifyWSAuthRefusal — the decision itself
// ---------------------------------------------------------------------------

// TestClassifyWSAuthRefusal_DistinguishesEveryFailureState pins the operator-
// facing diagnosis for each distinct cause. The point of the fix is not merely
// "something is now reported" but that the report tells the three real cases
// apart: stale cookie, never signed in, nothing configured — plus the
// dev-bypass case that motivated the work.
func TestClassifyWSAuthRefusal_DistinguishesEveryFailureState(t *testing.T) {
	withCookie := func(r *http.Request) *http.Request {
		r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "stale-value"})
		return r
	}
	accounts := []config.UserConfig{{Username: "operator"}}

	cases := []struct {
		name     string
		cfg      *config.Config
		req      *http.Request
		wantCode string
		wantMsg  string
	}{
		{
			name:     "nothing configured and bypass off is setup-incomplete",
			cfg:      &config.Config{Gateway: config.GatewayConfig{}},
			req:      httptest.NewRequest(http.MethodGet, "/api/v1/chat/ws", nil),
			wantCode: "no_auth_configured",
			wantMsg:  wsAuthErrNoUsers,
		},
		{
			name:     "dev bypass with no accounts is its own, nameable state",
			cfg:      &config.Config{Gateway: config.GatewayConfig{DevModeBypass: true}},
			req:      httptest.NewRequest(http.MethodGet, "/api/v1/chat/ws", nil),
			wantCode: "dev_bypass_not_on_websocket",
			wantMsg:  wsAuthErrNoUsers,
		},
		{
			name:     "a cookie that matched nothing is an expired session",
			cfg:      &config.Config{Gateway: config.GatewayConfig{Users: accounts}},
			req:      withCookie(httptest.NewRequest(http.MethodGet, "/api/v1/chat/ws", nil)),
			wantCode: "stale_session_cookie",
			wantMsg:  wsAuthErrInvalidToken,
		},
		{
			name:     "accounts exist and no cookie was sent is never-signed-in",
			cfg:      &config.Config{Gateway: config.GatewayConfig{Users: accounts}},
			req:      httptest.NewRequest(http.MethodGet, "/api/v1/chat/ws", nil),
			wantCode: "no_session_cookie",
			wantMsg:  wsAuthErrNotSignedIn,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OMNIPUS_BEARER_TOKEN", "")
			refusal, _ := classifyWSAuthRefusal(tc.req, tc.cfg)
			assert.Equal(t, tc.wantCode, refusal.code, "operator-facing reason code")
			assert.Equal(t, tc.wantMsg, refusal.userMessage, "user-facing copy")
			assert.NotEmpty(t, refusal.closeReason)
			assert.LessOrEqual(t, len(refusal.closeReason), wsCloseReasonMaxBytes,
				"a close reason over the control-frame limit is silently never sent")
		})
	}
}

// TestClassifyWSAuthRefusal_OnlyBrowsersAreFastFailed is the blast-radius
// guard. The fast-fail path keys on the Origin header, so it must fire for a
// browser and must NOT fire for a programmatic client — `omnipus run` and the
// CLI readiness probe authenticate by sending the auth frame AFTER the
// upgrade, and cutting their window would break both.
func TestClassifyWSAuthRefusal_OnlyBrowsersAreFastFailed(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{Gateway: config.GatewayConfig{DevModeBypass: true}}

	cli := httptest.NewRequest(http.MethodGet, "/api/v1/chat/ws", nil)
	_, futile := classifyWSAuthRefusal(cli, cfg)
	assert.False(t, futile,
		"a client with no Origin may still be about to send an auth frame — it keeps its window")

	browser := httptest.NewRequest(http.MethodGet, "/api/v1/chat/ws", nil)
	browser.Header.Set("Origin", "http://127.0.0.1:5000")
	_, futile = classifyWSAuthRefusal(browser, cfg)
	assert.True(t, futile,
		"a browser has already spent its only credential on the upgrade request")
}

// TestClassifyWSAuthRefusal_NeverAuthenticates is the security invariant in
// one assertion: the fast-fail gate is a refusal path only. It has no allow
// outcome to reach, so no configuration — including dev_mode_bypass — can turn
// it into a way onto the socket. This is what keeps the fix from widening the
// bypass beyond REST's posture.
func TestClassifyWSAuthRefusal_NeverAuthenticates(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	for _, cfg := range []*config.Config{
		{Gateway: config.GatewayConfig{}},
		{Gateway: config.GatewayConfig{DevModeBypass: true}},
		{Gateway: config.GatewayConfig{Users: []config.UserConfig{{Username: "operator"}}}},
		{Gateway: config.GatewayConfig{DevModeBypass: true, Users: []config.UserConfig{{Username: "operator"}}}},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/ws", nil)
		req.Header.Set("Origin", "http://127.0.0.1:5000")
		refusal, _ := classifyWSAuthRefusal(req, cfg)
		require.NotEmpty(t, refusal.code,
			"every classification is a refusal; there is no admit outcome to fall into")
		require.NotEmpty(t, refusal.userMessage)
	}
}

// TestTruncateCloseReason_BoundsToTheControlFrameLimit guards the quiet
// failure mode inside the fix itself: gorilla rejects a close frame whose
// payload exceeds 125 bytes, so an over-long reason would not be a truncated
// message — it would be NO message, i.e. the opaque disconnect all over again.
func TestTruncateCloseReason_BoundsToTheControlFrameLimit(t *testing.T) {
	assert.Equal(t, "short", truncateCloseReason("short"), "a reason under the limit is untouched")

	long := strings.Repeat("x", 400)
	assert.Len(t, truncateCloseReason(long), wsCloseReasonMaxBytes)

	// Multi-byte runes must not be cut mid-sequence: an invalid-UTF-8 close
	// reason violates RFC 6455 §5.5.1 and can be rejected outright.
	multibyte := strings.Repeat("é", 200) // 2 bytes per rune
	truncated := truncateCloseReason(multibyte)
	assert.LessOrEqual(t, len(truncated), wsCloseReasonMaxBytes)
	assert.True(t, utf8.ValidString(truncated), "truncation must land on a rune boundary")
}
