package gateway

// RED round 7 — the signature live-preview mint (spec §16 Signature editor
// row; architect decision, coordination/logs/email-arch-sigcsp.log, "Decision
// — exact contract shape" + "Confirmations"). Oracles:
//
//   - contracts/openapi.yaml /mail/signature-preview-token (commit bbe8e9acd)
//   - MailSignaturePreviewTokenRequest: signature_html 1..16384 chars (MC-1)
//   - MailSignaturePreviewTokenResponse: 43-char base64url token,
//     expires_in_seconds == 120 (the named 2-minute TTL)
//   - spec §5.3 MC-10 normative header set (verbatim; same posture as the
//     message preview's serve route — compared against the expectations of
//     TestMailPreviewHTML_NormativeHeaders, never against handler code)
//   - MC-40 anchor hardening; MC-43/MC-45 serve hygiene
//   - FR-003 signature policy: keeps inline style, tables, https+data img;
//     strips script, event handlers, javascript:, forms, iframes
//   - T38: the signature's https images ride /mail-preview/img/{token}/{index}
//     with NO load-images step (MIN-001 resolution)
//   - T79 parity: the mint NEVER dials IMAP (no folder/ref in the request)
//   - MC-44 discipline: dedicated per-IP limiter, 60 req/min, 429 +
//     Retry-After, never the shared html-preview-token mint limiter
//
// Expectations were derived from those sources only — never from the (not yet
// written) handler. A stock "404 page not found" means the route is not
// implemented yet and fails through requireMailLive's BLOCKED fatal.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// sigMintPath is the new session-authenticated mint route (decision: "New
// route | POST /api/v1/mail/signature-preview-token … same wrapping as the
// existing mint").
const sigMintPath = "/api/v1/mail/signature-preview-token"

// mintSignature mints with the test session (the authenticated arm). For the
// unauthenticated arm of the auth contract use mintSignatureNoAuth — a mint
// that silently rode the session token could never observe the contract's 401.
func mintSignature(t *testing.T, env *mailRedEnv, ip, html string) *httptest.ResponseRecorder {
	return mintSignatureAuthed(t, env, ip, html, true)
}

// mintSignatureNoAuth sends the same mint request with NO session/Bearer
// token (mailDo's authed=false omits the header entirely).
func mintSignatureNoAuth(t *testing.T, env *mailRedEnv, ip, html string) *httptest.ResponseRecorder {
	return mintSignatureAuthed(t, env, ip, html, false)
}

func mintSignatureAuthed(t *testing.T, env *mailRedEnv, ip, html string, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(gen.MintMailSignaturePreviewTokenJSONRequestBody{SignatureHtml: html})
	require.NoError(t, err)
	return mailDo(env.mux, http.MethodPost, sigMintPath, ip, authed, string(body))
}

func decodeSigMint(t *testing.T, rec *httptest.ResponseRecorder) gen.MailSignaturePreviewTokenResponse {
	t.Helper()
	var out gen.MailSignaturePreviewTokenResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "mint body must decode as the generated response type: %s", rec.Body.String())
	return out
}

// sigExactLen returns an HTML document of exactly n characters (MC-1 counts
// characters, not bytes).
func sigExactLen(t *testing.T, n int) string {
	t.Helper()
	const wrap = 3 + 4 // "<p>" + "</p>"
	require.Greater(t, n, wrap)
	return "<p>" + strings.Repeat("a", n-wrap) + "</p>"
}

// sigHostileFixture carries one clause per FR-003 strip/keep rule plus an
// anchor for MC-40: a handler on an allowed element, a script, meta, base,
// form (+input), iframe, a javascript: anchor — and the keeps: inline style,
// a table, a clean https anchor.
const sigHostileFixture = `<p onclick="evil()">handler</p>` +
	`<script>alert(1)</script>` +
	`<meta http-equiv="refresh" content="0">` +
	`<base href="https://evil.test/">` +
	`<form action="https://evil.test/"><input type="text" name="q"></form>` +
	`<iframe src="https://evil.test/"></iframe>` +
	`<p style="color:#333">Elicify GmbH — Berlin</p>` +
	`<table><tr><td>cell</td></tr></table>` +
	`<a href="https://example.test/">site</a>` +
	`<a href="javascript:alert(1)">bad</a>`

// assertSigServedSanitized runs the FR-003/MC-10(5)/MC-40 oracle against one
// served signature HTML: the strips, the keeps, and the hardened anchor.
func assertSigServedSanitized(t *testing.T, html string) {
	t.Helper()
	lower := strings.ToLower(html)
	for _, banned := range []string{"<script", "onclick", "<meta", "<base", "<form", "<iframe", "javascript:"} {
		if strings.Contains(lower, banned) {
			t.Fatalf("FR-003/MC-10(5): served signature still contains %q\nhtml: %s", banned, html)
		}
	}
	for _, kept := range []string{
		`style="color:#333"`,       // inline style kept (FR-003)
		"Elicify GmbH",             // the styled paragraph's text rode along
		"<table>", "<td>cell</td>", // tables kept (FR-003)
	} {
		if !strings.Contains(html, kept) {
			t.Fatalf("FR-003: served signature lost %q (sanitizer over-strips)\nhtml: %s", kept, html)
		}
	}
	// MC-40: every surviving anchor is hardened before the store holds it.
	if !strings.Contains(html, `target="_blank"`) || !strings.Contains(html, `rel="noopener noreferrer"`) {
		t.Fatalf("MC-40: served signature anchors not hardened (target=_blank + rel=noopener noreferrer)\nhtml: %s", html)
	}
}

// assertSigMC10Headers is the MC-10 normative header oracle, byte-for-byte the
// expectations of TestMailPreviewHTML_NormativeHeaders (the existing message
// preview serve test) — the decision serves signatures through the SAME
// posture with zero new serve surface.
func assertSigMC10Headers(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("MC-10: Content-Type = %q, want text/html; charset=utf-8", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != mailPreviewCSP() {
		t.Fatalf("MC-10: CSP mismatch\n got %s\nwant %s", got, mailPreviewCSP())
	}
	assertNoBannedSandbox(t, "CSP", rec.Header().Get("Content-Security-Policy"))
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "'self'") {
		t.Fatal("MC-10/MC-37: CSP contains 'self'")
	}
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "https:") {
		t.Fatal("MC-37: CSP img-src contains a https: source")
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("MC-10: Referrer-Policy = %q, want no-referrer", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("MC-10: X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("MC-10: Cache-Control = %q, want no-store", got)
	}
	if rec.Header().Get("X-Frame-Options") != "" {
		t.Fatal("MC-10: X-Frame-Options must not be set")
	}
}

func TestSignaturePreviewMint_UnauthenticatedIs401_AuthenticatedMints(t *testing.T) {
	// Contract security: BearerAuth on the mint (openapi security block) —
	// without a session 401; the authed sibling is the positive control that
	// the 401 comes from auth, not from a dead route.
	env := newMailRedEnv(t)
	requireMailLive(t, env.mux, http.MethodPost, sigMintPath, "architect decision / contracts /mail/signature-preview-token")

	rec := mintSignatureNoAuth(t, env, nextMailIP(), "<p>no auth</p>")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated mint = %d, want 401. body=%s", rec.Code, rec.Body.String())
	}
	// Positive control: the same body WITH the session mints (never 401).
	ok := mintSignature(t, env, nextMailIP(), "<p>authed</p>")
	if ok.Code != http.StatusOK {
		t.Fatalf("authed mint = %d, want 200 — the 401 above would be meaningless without this control. body=%s", ok.Code, ok.Body.String())
	}
}

func TestSignaturePreviewMint_InputBoundaries_400Outside16384Window(t *testing.T) {
	// MC-1 / MailSignaturePreviewTokenRequest: minLength 1, maxLength 16384.
	// Boundary lattice: min-1 (empty) and max+1 (16385) are 400; min (1) and
	// max (16384) mint 200. The 200s are the test's positive controls — a
	// handler that 400s everything would fail them.
	env := newMailRedEnv(t)
	requireMailLive(t, env.mux, http.MethodPost, sigMintPath, "MC-1 / MailSignaturePreviewTokenRequest")

	cases := []struct {
		name string
		html string
		want int
	}{
		{"empty is below the minimum", "", http.StatusBadRequest},
		{"one char is the minimum", "x", http.StatusOK},
		{"exactly 16384 chars is the maximum", sigExactLen(t, 16384), http.StatusOK},
		{"16385 chars is over the maximum", sigExactLen(t, 16385), http.StatusBadRequest},
	}
	for _, tc := range cases {
		rec := mintSignature(t, env, nextMailIP(), tc.html)
		if rec.Code != tc.want {
			t.Fatalf("%s: %d-char signature_html = %d, want %d. body=%s",
				tc.name, len(tc.html), rec.Code, tc.want, rec.Body.String())
		}
	}
}

func TestSignaturePreviewMint_ResponseShapeAndFrame_ServesSanitizedSignatureWithMC10Headers(t *testing.T) {
	// Response schema: decodes into gen.MailSignaturePreviewTokenResponse with
	// a 43-char token (schema minLength==maxLength==43) and
	// expires_in_seconds == 120 (the named 2-minute
	// MailSignaturePreviewTokenTTL). The token frames the EXISTING serve route
	// GET /mail-preview/html/{token} (decision: "zero new serve surface"),
	// which answers the SANITIZED signature (FR-003 strips, MC-40 anchors)
	// under the MC-10 normative header set.
	env := newMailRedEnv(t)
	requireMailLive(t, env.mux, http.MethodPost, sigMintPath, "MailSignaturePreviewTokenResponse / MC-10")

	mint := mintSignature(t, env, nextMailIP(), sigHostileFixture)
	if mint.Code != http.StatusOK {
		t.Fatalf("mint = %d, want 200. body=%s", mint.Code, mint.Body.String())
	}
	out := decodeSigMint(t, mint)
	if len(out.Token) != 43 {
		t.Fatalf("token = %q (%d chars), want exactly 43 base64url chars (MailSignaturePreviewTokenResponse.token)", out.Token, len(out.Token))
	}
	if out.ExpiresInSeconds != 120 {
		t.Fatalf("expires_in_seconds = %d, want 120 (the named 2-minute MailSignaturePreviewTokenTTL)", out.ExpiresInSeconds)
	}

	srv := mailDo(env.mux, http.MethodGet, "/mail-preview/html/"+out.Token, nextMailIP(), false, "")
	if srv.Code != http.StatusOK {
		t.Fatalf("serve = %d, want 200 for the freshly minted token. body=%s", srv.Code, srv.Body.String())
	}
	assertSigServedSanitized(t, srv.Body.String())
	assertSigMC10Headers(t, srv)
}

func TestSignaturePreviewMint_NeverDialsIMAP(t *testing.T) {
	// Decision: "this mint NEVER dials IMAP — the request carries no
	// workspace/agent/folder/ref" (T79 parity). The mailbox points at a
	// counting listener; the mint must open zero connections. The listener is
	// proven counting first: a test-side dial must tick it — otherwise a
	// broken counter would make zero vacuous.
	env := newMailRedEnv(t)
	requireMailLive(t, env.mux, http.MethodPost, sigMintPath, "T79 parity / decision")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	var dials atomic.Int32
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			dials.Add(1)
			_ = c.Close()
		}
	}()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscan(portStr, &port)
	require.NoError(t, err)
	pointMailboxAt(t, env, port, 1)

	// Instrument control: the counter sees a real connection.
	ctrl, cerr := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, cerr)
	require.NoError(t, ctrl.Close())
	waitForDials(t, &dials, 1)

	mint := mintSignature(t, env, nextMailIP(), "<p>no imap</p>")
	if mint.Code != http.StatusOK {
		t.Fatalf("mint = %d, want 200 before the zero-dial assertion can mean anything. body=%s", mint.Code, mint.Body.String())
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("signature mint dialed IMAP: %d accepts after mint, want 1 (the test-side control dial only)", got)
	}
}

// waitForDials polls the accept counter so the control dial cannot lose a
// race with the assertion.
func waitForDials(t *testing.T, counter *atomic.Int32, want int32) {
	t.Helper()
	require.Eventually(t, func() bool { return counter.Load() >= want }, 5*time.Second, 5*time.Millisecond,
		"instrument: dial counter never reached %d — the listener is not counting", want)
}

func TestSignaturePreviewMint_ReplacesPreviousSignatureToken(t *testing.T) {
	// Decision, token grant: "REPLACE-ON-MINT — a session holds at most one
	// live signature token (a new mint revokes the session's previous
	// signature token)". Both mints ride one session (same Bearer). The
	// second token serving 200 is the positive control — a corpus of only
	// 404s would prove nothing (TestMailPreview_NoRedirect's discipline).
	env := newMailRedEnv(t)
	requireMailLive(t, env.mux, http.MethodPost, sigMintPath, "decision REPLACE-ON-MINT")

	ip := nextMailIP()
	first := mintSignature(t, env, ip, "<p>signature version one</p>")
	if first.Code != http.StatusOK {
		t.Fatalf("first mint = %d, want 200. body=%s", first.Code, first.Body.String())
	}
	t1 := decodeSigMint(t, first).Token

	second := mintSignature(t, env, ip, "<p>signature version two</p>")
	if second.Code != http.StatusOK {
		t.Fatalf("second mint = %d, want 200. body=%s", second.Code, second.Body.String())
	}
	t2 := decodeSigMint(t, second).Token
	if t1 == t2 {
		t.Fatal("instrument: both mints returned the same token — replace-on-mint cannot be observed")
	}

	srv1 := mailDo(env.mux, http.MethodGet, "/mail-preview/html/"+t1, nextMailIP(), false, "")
	if srv1.Code != http.StatusNotFound {
		t.Fatalf("replaced token = %d, want 404 (revoked by the second mint). body=%s", srv1.Code, srv1.Body.String())
	}
	srv2 := mailDo(env.mux, http.MethodGet, "/mail-preview/html/"+t2, nextMailIP(), false, "")
	if srv2.Code != http.StatusOK {
		t.Fatalf("live token = %d, want 200. body=%s", srv2.Code, srv2.Body.String())
	}
	if !strings.Contains(srv2.Body.String(), "signature version two") {
		t.Fatalf("live token serves %q, want the second signature's HTML", srv2.Body.String())
	}
}

func TestSignaturePreviewMint_DoesNotCountAgainstMessagePreviewCap(t *testing.T) {
	// Decision: "message-preview tokens keep their separate cap-8-refuses
	// hygiene" — signature mints neither revoke message tokens nor consume
	// the message cap. Oracle: 2 signature mints + 8 message mints all
	// succeed (a shared counter would refuse the 8th), and the 9th MESSAGE
	// mint is still refused (MC-43 cap-8-refuses unchanged — the positive
	// control proving the cap exists at all; an 11th message mint stays
	// refused, never evicting).
	env := newMailRedEnv(t)
	requireMailLive(t, env.mux, http.MethodPost, sigMintPath, "decision (message-cap separation)")

	// Two signature mints from their own IP.
	sigIP := nextMailIP()
	for i := 1; i <= 2; i++ {
		rec := mintSignature(t, env, sigIP, fmt.Sprintf("<p>sig %d</p>", i))
		if rec.Code != http.StatusOK {
			t.Fatalf("signature mint %d = %d, want 200. body=%s", i, rec.Code, rec.Body.String())
		}
	}

	// One staged message, minted repeatedly from a second IP.
	imapPort, cl := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, cl, "INBOX", []byte(mailPreviewHTMLRaw), nil)
	msgPath := "/api/v1/mail/html-preview-token"
	requireMailLive(t, env.mux, http.MethodPost, msgPath, "MC-43 hygiene control")
	msgIP := nextMailIP()
	msgBody := fmt.Sprintf(`{"workspace_id":%q,"agent_id":%q,"folder":"inbox","message_ref":"uid:1:1","load_remote":false}`, mailRedWS, mailRedAgent)
	for i := 1; i <= 8; i++ {
		rec := mailDo(env.mux, http.MethodPost, msgPath, msgIP, true, msgBody)
		if rec.Code != http.StatusOK {
			t.Fatalf("message mint %d (after 2 signature mints) = %d, want 200 — signature tokens must not consume the message cap. body=%s",
				i, rec.Code, rec.Body.String())
		}
	}
	if rec := mailDo(env.mux, http.MethodPost, msgPath, msgIP, true, msgBody); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("9th message mint = %d, want 429 — the message cap-8-refuses hygiene must survive unchanged. body=%s",
			rec.Code, rec.Body.String())
	}
}

func TestSignaturePreviewMint_ServesSignatureImagesThroughProxyWithoutLoadImagesStep(t *testing.T) {
	// T38 (MIN-001 resolution): the signature's https images ride
	// /mail-preview/img/{token}/{index} WITHOUT any load-images step —
	// decision: "Mint extracts the sanitized HTML's https image URLs into
	// RemoteURLs and binds LoadRemote: true at mint". MC-10(2) confines the
	// frame's img-src to <origin>/mail-preview/ + data:, so "served through
	// the proxy" on the wire means: the served HTML's https img srcs are
	// rewritten onto the token-scoped img paths, first-seen order (0, 1);
	// data: images stay inline (FR-003); a plain-http img has no rendering
	// path (MC-41 https-only) and FR-003 strips it.
	//
	// Known gap, deliberate: the proxy's live https fetch itself is not
	// asserted here — MC-41's dial-time SSRF pins refuse loopback/private
	// sinks, so no in-process fixture can produce a 200 proxy dial; the
	// fetch's pins are T76's already-pinned surface.
	env := newMailRedEnv(t)
	requireMailLive(t, env.mux, http.MethodPost, sigMintPath, "T38 / decision image proxy")

	html := `<img src="https://logo.example.test/logo.png" alt="logo">` +
		`<img src="https://badge.example.test/b.png">` +
		`<img src="data:image/png;base64,AAAA">` +
		`<img src="http://plain.example.test/x.png">`
	mint := mintSignature(t, env, nextMailIP(), html)
	if mint.Code != http.StatusOK {
		t.Fatalf("mint = %d, want 200. body=%s", mint.Code, mint.Body.String())
	}
	token := decodeSigMint(t, mint).Token

	srv := mailDo(env.mux, http.MethodGet, "/mail-preview/html/"+token, nextMailIP(), false, "")
	if srv.Code != http.StatusOK {
		t.Fatalf("serve = %d, want 200. body=%s", srv.Code, srv.Body.String())
	}
	body := srv.Body.String()
	for _, want := range []string{
		`src="/mail-preview/img/` + token + `/0"`, // first https logo, no load-images step
		`src="/mail-preview/img/` + token + `/1"`, // second https image, next index
		`src="data:image/png;base64,AAAA"`,        // data: images stay inline (FR-003)
		`alt="logo"`,                              // the img element itself survives
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("T38: served signature lost %q\nhtml: %s", want, body)
		}
	}
	if strings.Contains(strings.ToLower(body), `src="http://`) {
		t.Fatalf("T38/MC-41: a plain-http image src survived the signature sanitizer\nhtml: %s", body)
	}
}

func TestSignaturePreviewMint_DedicatedLimiter_61stIs429AndDoesNotConsumeMessageMintLimiter(t *testing.T) {
	// MC-44 discipline per the decision: a DEDICATED per-IP limiter instance,
	// 60 req/min — the 61st request within a minute is 429 with Retry-After,
	// and the signature mints never consume the message mint's separate
	// 10/min limiter (the message mint from the same spent IP must still
	// reach its handler — anything but 429 proves the limiters are separate;
	// the message limiter's own refusal behaviour is TestMailPreviewMint_
	// EleventhIs429AndDoesNotDial's oracle).
	env := newMailRedEnv(t)
	requireMailLive(t, env.mux, http.MethodPost, sigMintPath, "MC-44 / decision (dedicated 60 req/min)")

	ip := nextMailIP()
	for i := 1; i <= 60; i++ {
		rec := mintSignature(t, env, ip, "<p>rate</p>")
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("signature mint %d of 60 was already 429 — the dedicated limit is not 60/min", i)
		}
	}
	rec := mintSignature(t, env, ip, "<p>rate</p>")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("61st signature mint = %d, want 429. body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("MC-44: 429 is missing Retry-After")
	}

	// The message mint limiter was not consumed: the same spent IP still
	// reaches the message mint handler (502 — its loopback IMAP dial fails —
	// proves passage; a 429 here would mean the shared limiter).
	msgRec := mailDo(env.mux, http.MethodPost, "/api/v1/mail/html-preview-token", ip, true,
		fmt.Sprintf(`{"workspace_id":%q,"agent_id":%q,"folder":"inbox","message_ref":"uid:1:1","load_remote":false}`, mailRedWS, mailRedAgent))
	if msgRec.Code == http.StatusTooManyRequests {
		t.Fatalf("MC-44: the signature limiter consumed the message mint limiter — same-IP message mint answered 429")
	}
	if stdlibNotFound(msgRec) {
		t.Fatalf("instrument: message mint route missing — the non-429 claim would be vacuous. body=%s", msgRec.Body.String())
	}
}
