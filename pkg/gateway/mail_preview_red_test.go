package gateway

// RED — /mail-preview/ serve routes and the attachment download headers.
// Oracles: spec §2.3a, the MC-10 normative header block (verbatim), MC-37,
// MC-38, MC-39, MC-41, MC-42, MC-45, and T62. A stock "404 page not found"
// means the prefix is not registered.

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// mailPreviewCSP is the MC-10 normative policy with the one origin this
// fixture configures (mailRedOrigin). Copied from the spec's header block.
func mailPreviewCSP() string {
	return "sandbox allow-popups allow-popups-to-escape-sandbox; default-src 'none'; " +
		"script-src 'none'; style-src 'unsafe-inline'; " +
		"img-src " + mailRedOrigin + "/mail-preview/ data:; " +
		"font-src data:; media-src data:; connect-src 'none'; form-action 'none'; " +
		"frame-src 'none'; object-src 'none'; base-uri 'none'"
}

// The seven tokens MC-39 bans in both the CSP sandbox directive and the iframe
// attribute. allow-popups is required and is not in this list.
var mailSandboxBanned = []string{
	"allow-scripts", "allow-same-origin", "allow-forms", "allow-top-navigation",
	"allow-downloads", "allow-modals", "allow-pointer-lock",
}

func assertNoBannedSandbox(t *testing.T, where, value string) {
	t.Helper()
	for _, tok := range mailSandboxBanned {
		if strings.Contains(value, tok) {
			t.Fatalf("MC-39: %s contains banned sandbox token %q: %s", where, tok, value)
		}
	}
}

func TestMailServeRoutes_TokenOnly404Only(t *testing.T) {
	// MC-45: the serve routes take only the token. A session cookie without a
	// real token is 404, never 401. Expired, unknown and revoked are the same
	// bytes. No frame-ancestors and no X-Frame-Options.
	env := newMailRedEnv(t)
	unknown := "/mail-preview/html/" + strings.Repeat("a", 43)
	other := "/mail-preview/html/" + strings.Repeat("b", 43)
	requireMailLive(t, env.mux, http.MethodGet, unknown, "MC-45 / spec §2.3a")

	plain := mailDo(env.mux, http.MethodGet, unknown, nextMailIP(), false, "")
	req := httptest.NewRequest(http.MethodGet, other, nil)
	req.RemoteAddr = nextMailIP() + ":4321"
	req.AddCookie(&http.Cookie{Name: "omnipus-session", Value: "not-a-session"})
	cookieRec := httptest.NewRecorder()
	env.mux.ServeHTTP(cookieRec, req)

	for _, rec := range []*httptest.ResponseRecorder{plain, cookieRec} {
		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("MC-45: a serve route answered 401. Token-only routes never do. body=%s", rec.Body.String())
		}
		if rec.Code != http.StatusNotFound {
			t.Fatalf("MC-45: unknown token = %d, want 404. body=%s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Frame-Options") != "" {
			t.Fatalf("MC-45: X-Frame-Options was set (%q); the spec forbids it", rec.Header().Get("X-Frame-Options"))
		}
		if strings.Contains(strings.ToLower(rec.Header().Get("Content-Security-Policy")), "frame-ancestors") {
			t.Fatal("MC-45: CSP contains frame-ancestors; the spec forbids it")
		}
	}
	if plain.Body.String() != cookieRec.Body.String() {
		t.Fatalf("MC-45: cookie-without-token body differs from unknown-token body\nno cookie: %q\ncookie: %q",
			plain.Body.String(), cookieRec.Body.String())
	}
}

func TestMailPreview_NoRedirect(t *testing.T) {
	// MC-10(1) / T62: nothing under /mail-preview/ redirects, and a dot-segment
	// path must not be sent to /api/. Non-vacuous only once the prefix exists.
	env := newMailRedEnv(t)
	token := strings.Repeat("c", 43)
	live := "/mail-preview/html/" + token
	requireMailLive(t, env.mux, http.MethodGet, live, "MC-10(1) / T62")

	var apiHits atomic.Int32
	outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiHits.Add(1)
		}
		env.mux.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(outer)
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	targets := []string{
		live,
		"/mail-preview/html/" + token + "/../../api/v1/state",
		"/mail-preview/part/" + token + "/0",
		"/mail-preview/img/" + token + "/0",
		"/mail-preview/html/" + token + "/./index.html",
		"/mail-preview//html//" + token,
	}
	for _, target := range targets {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
			got := sendRawPreviewRequest(t, addr, method, target, nil)
			if got.status >= 300 && got.status < 400 {
				t.Fatalf("MC-10(1): %s %s redirected %d Location=%q", method, target, got.status, got.location)
			}
			if got.location != "" {
				t.Fatalf("MC-10(1): %s %s wrote Location %q", method, target, got.location)
			}
		}
	}
	if apiHits.Load() != 0 {
		t.Fatalf("MC-10(1): a /mail-preview/ request was dispatched to /api/ (%d hits)", apiHits.Load())
	}
}

func TestMailPreview_NoRedirectHarnessSeesAStdlibMuxRedirect(t *testing.T) {
	// Positive control for the tripwire above, mirroring
	// TestLibraryPreview_NoRedirectHarnessSeesAStdlibMuxRedirect. The standard
	// library mux cleans a dot-segment path and redirects. If this test cannot
	// see that redirect, "nothing redirected" above proves nothing.
	std := http.NewServeMux()
	std.HandleFunc("/mail-preview/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	std.HandleFunc("/api/v1/state", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	srv := httptest.NewServer(std)
	t.Cleanup(srv.Close)
	got := sendRawPreviewRequest(t, srv.Listener.Addr().String(), http.MethodGet,
		"/mail-preview/tok/../../api/v1/state", nil)
	if got.status < 300 || got.status >= 400 {
		t.Fatalf("T62 control: stdlib mux did not redirect a dot-segment path (got %d) — the harness cannot see redirects", got.status)
	}
	if got.location != "/api/v1/state" {
		t.Fatalf("T62 control: Location = %q, want /api/v1/state", got.location)
	}
}

func TestMailPreviewHTML_NormativeHeaders(t *testing.T) {
	// MC-10 verbatim header set, once a token can be served. Mint is the only
	// IMAP fetch; the serve route must answer from the store.
	env := newMailRedEnv(t)
	mintPath := "/api/v1/mail/html-preview-token"
	requireMailLive(t, env.mux, http.MethodPost, mintPath, "MC-10 / spec §2.3a")
	body := fmt.Sprintf(`{"workspace_id":%q,"agent_id":%q,"folder":"inbox","message_ref":"uid:1:1","load_remote":false}`, mailRedWS, mailRedAgent)
	mint := mailDo(env.mux, http.MethodPost, mintPath, nextMailIP(), true, body)
	if mint.Code != http.StatusOK {
		t.Fatalf("MC-10: mint = %d, want 200 so the serve headers can be checked. body=%s", mint.Code, mint.Body.String())
	}
	var minted struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(mint.Body.Bytes(), &minted))
	if len(minted.Token) < 43 {
		t.Fatalf("MC-43: token %q is shorter than a 256-bit base64url value (43 chars)", minted.Token)
	}
	srv := mailDo(env.mux, http.MethodGet, "/mail-preview/html/"+minted.Token, nextMailIP(), false, "")
	if srv.Code != http.StatusOK {
		t.Fatalf("MC-10: serve = %d, want 200. body=%s", srv.Code, srv.Body.String())
	}
	if got := srv.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("MC-10: Content-Type = %q, want text/html; charset=utf-8", got)
	}
	if got := srv.Header().Get("Content-Security-Policy"); got != mailPreviewCSP() {
		t.Fatalf("MC-10: CSP mismatch\n got %s\nwant %s", got, mailPreviewCSP())
	}
	assertNoBannedSandbox(t, "CSP", srv.Header().Get("Content-Security-Policy"))
	if strings.Contains(srv.Header().Get("Content-Security-Policy"), "'self'") {
		t.Fatal("MC-10/MC-37: CSP contains 'self'")
	}
	if strings.Contains(srv.Header().Get("Content-Security-Policy"), "https:") {
		t.Fatal("MC-37: CSP img-src contains a https: source")
	}
	if got := srv.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("MC-10: Referrer-Policy = %q, want no-referrer", got)
	}
	if got := srv.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("MC-10: X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := srv.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("MC-10: Cache-Control = %q, want no-store", got)
	}
	if srv.Header().Get("X-Frame-Options") != "" {
		t.Fatal("MC-10: X-Frame-Options must not be set")
	}
}

func TestMailPreview_NoOriginOmitsHostSources(t *testing.T) {
	// MC-38: no derivable origin → host sources omitted, never 'self' or https:.
	env := newMailRedEnv(t)
	cfg := env.api.agentLoop.GetConfig()
	cfg.Gateway.PublicURL = ""
	cfg.Gateway.Host = "0.0.0.0"
	path := "/mail-preview/html/" + strings.Repeat("d", 43)
	requireMailLive(t, env.mux, http.MethodGet, path, "MC-38")
	// A 404 still carries the policy if the handler sets headers first (the
	// Library serve path does). Either the 404 or a later 200 must show it.
	rec := mailDo(env.mux, http.MethodGet, path, nextMailIP(), false, "")
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatalf("MC-38: no-origin response carried no CSP (status %d). body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(csp, "'self'") || strings.Contains(csp, "https:") || strings.Contains(csp, "http:") {
		t.Fatalf("MC-38: no-origin CSP still has a host source: %s", csp)
	}
}

func TestMailImageProxy_DoesNotFetchCallerURLs(t *testing.T) {
	// MC-41: only a URL recorded at mint is fetched. A caller-supplied query
	// is not a way in. https-only, so an http sink must see nothing.
	env := newMailRedEnv(t)
	var hits atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, "not-really")
	}))
	t.Cleanup(sink.Close)
	token := strings.Repeat("e", 43)
	path := "/mail-preview/img/" + token + "/0?url=" + sink.URL + "/secret.png"
	requireMailLive(t, env.mux, http.MethodGet, path, "MC-41 / spec §2.3a")
	rec := mailDo(env.mux, http.MethodGet, path, nextMailIP(), false, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("MC-41: caller-supplied image URL = %d, want 404. body=%s", rec.Code, rec.Body.String())
	}
	if hits.Load() != 0 {
		t.Fatalf("MC-41: the proxy fetched a caller-supplied URL (%d hits on %s)", hits.Load(), sink.URL)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); rec.Code == http.StatusOK && got != "nosniff" {
		t.Fatalf("MC-41: image response nosniff = %q", got)
	}
}

func TestMailImageProxy_RefusesLoopbackAtDial(t *testing.T) {
	// MC-41 dial-time pin: a name that answers 200 on loopback is still refused
	// when the address is loopback. This subtest is reachable only after mint
	// records the URL. Until a message can be staged, the route check above is
	// the RED signal; the loopback listener must stay at zero accepts.
	env := newMailRedEnv(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	var hits atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			hits.Add(1)
			_ = c.Close()
		}
	}()
	path := "/mail-preview/img/" + strings.Repeat("f", 43) + "/0"
	requireMailLive(t, env.mux, http.MethodGet, path, "MC-41 dial-time")
	rec := mailDo(env.mux, http.MethodGet, path, nextMailIP(), false, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("MC-41: unknown image index = %d, want 404. body=%s", rec.Code, rec.Body.String())
	}
	if hits.Load() != 0 {
		t.Fatalf("MC-41: an unknown token still dialed %s (%d accepts)", ln.Addr(), hits.Load())
	}
}
