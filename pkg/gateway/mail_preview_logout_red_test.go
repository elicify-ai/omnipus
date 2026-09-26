package gateway

// N2 — MC-43: a mail HTML-preview token is an unauthenticated bearer
// credential in a URL path; it must die with the session that minted it.
// Direct mail analog of preview_token_revocation_test.go::
// TestPreviewToken_InvalidatedOnLogout, with the same positive-control
// discipline: mint -> serve 200 with the real bytes -> real logout ->
// serve 404 without the bytes, isolation headers still on the refusal.
// Expectations derive from MC-43 + the FR-003d consequence, not from the
// handler.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// seedMailLogoutUser gives the env one gateway.users row so the real
// HandleLogout completes 204 instead of 500ing on the minimal fixture
// config. Mirrors what newTestRestAPIWithUser seeds; revokeUserToken
// tolerates a row with no token set (the env bearer is not a user token).
func seedMailLogoutUser(t *testing.T, homePath, username string) {
	t.Helper()
	cfgPath := homePath + "/config.json"
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	m["gateway"] = map[string]any{
		// public_url must survive the rewrite: HandleLogout round-trips
		// config.json through safeUpdateConfigJSON and swaps the live config,
		// so a fixture config missing the key would drift the CSP origin.
		"public_url": mailRedOrigin,
		"users":      []any{map[string]any{"username": username}},
	}
	b, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, b, 0o600))
}

func TestMailPreviewToken_InvalidatedOnLogout(t *testing.T) {
	env := newMailRedEnv(t)
	seedMailLogoutUser(t, env.api.homePath, "mail-red-user")

	// Stage one HTML message; the mint is the ONE live IMAP fetch (§2.3a).
	imapPort, cl := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, cl, "INBOX", []byte(mailPreviewHTMLRaw), nil)

	// Mint through the real handler, as the env bearer credential.
	mintBody := fmt.Sprintf(`{"workspace_id":%q,"agent_id":%q,"folder":"inbox","message_ref":"uid:1:1","load_remote":false}`,
		mailRedWS, mailRedAgent)
	mint := mailDo(env.mux, http.MethodPost, "/api/v1/mail/html-preview-token", nextMailIP(), true, mintBody)
	if mint.Code != http.StatusOK {
		t.Fatalf("mint = %d, want 200 before logout can be tested. body=%s", mint.Code, mint.Body.String())
	}
	var minted struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(mint.Body.Bytes(), &minted))
	if minted.Token == "" {
		t.Fatal("mint response carried no token")
	}

	// Positive control: the grant genuinely worked before logout.
	srv := mailDo(env.mux, http.MethodGet, "/mail-preview/html/"+minted.Token, nextMailIP(), false, "")
	if srv.Code != http.StatusOK {
		t.Fatalf("positive control: serve before logout = %d, want 200 — a token that never worked also fails after logout, which proves nothing. body=%s",
			srv.Code, srv.Body.String())
	}
	if !strings.Contains(srv.Body.String(), "<p>Hello there</p>") {
		t.Fatalf("positive control: served body %q lacks the granted bytes", srv.Body.String())
	}

	// The real logout event, presenting the SAME credential the mint carried
	// (PreviewSessionKey derives b:digest(bearer) on both, so the keys match
	// by construction). User context injected the way withAuth would.
	lg := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	lg.Header.Set("Authorization", "Bearer "+mailRedToken)
	lg = injectUser(lg, "mail-red-user")
	lrec := httptest.NewRecorder()
	env.api.HandleLogout(lrec, lg)
	if lrec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204 — the revocation event must genuinely happen. body=%s", lrec.Code, lrec.Body.String())
	}

	// The grant is dead: bare 404, no granted bytes, and the refusal still
	// carries the MC-10 isolation header set (expiry alone is not revocation;
	// a refusal must also be safe to render).
	dead := mailDo(env.mux, http.MethodGet, "/mail-preview/html/"+minted.Token, nextMailIP(), false, "")
	if dead.Code != http.StatusNotFound {
		t.Fatalf("MC-43: after the minting session logged out, serve = %d, want 404 — the token outlived its session", dead.Code)
	}
	if strings.Contains(dead.Body.String(), "<p>Hello there</p>") {
		t.Fatal("MC-43: the post-logout refusal carried the granted bytes")
	}
	if got := dead.Header().Get("Content-Security-Policy"); got != mailPreviewCSP() {
		t.Fatalf("MC-43/MC-10: post-logout refusal CSP = %q, want the isolation policy", got)
	}
	for k, want := range map[string]string{
		"Referrer-Policy":        "no-referrer",
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
	} {
		if got := dead.Header().Get(k); got != want {
			t.Fatalf("MC-43/MC-10: post-logout refusal %s = %q, want %q", k, got, want)
		}
	}
}
