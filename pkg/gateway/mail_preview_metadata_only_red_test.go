package gateway

// RED — w5-integration claim 4: preview grants retain NO body. Every serve
// fetches live, and every previous preview security control still holds.
//
// Oracles (derived from the spec BEFORE reading the implementation;
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/w5-red-test-plan.md):
//   - w5 spec US-6.1/B-24/MC-11: white-box inspection of the minted MESSAGE
//     grant shows authorization/reference metadata only — "zero HTML
//     string, zero inline part bytes, zero attachment data" — and the grant
//     stays that way after serving (§4.2: the system must not "retain
//     reusable body/inline/attachment bytes, even in token grants or a
//     recent-preview keep").
//   - w5 spec US-6.2/B-25/MC-12: "each serve request performs its own
//     budget-gated live fetch… and holds bytes only for that request's
//     lifetime"; B-27: "the preview shows its error state, NEVER a stale
//     cached body".
//
// Mutation this pack must kill (check-integration-report.md §2, M13): a
// "recent-preview keep" — sanitized HTML cached per token so a second serve
// skips the live fetch. A structural white-box assertion catches the
// grant-stuffing form; the two-serve oracle catches every cache form,
// because a cached body serves 200 even with the upstream dead, while a
// live fetch must fail.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/require"
)

// startClosablePlainIMAP is the fixture harness (startPlainIMAP's shape)
// with the server handles returned, so a test can kill the upstream between
// two serves — the discriminator a body cache cannot survive.
func startClosablePlainIMAP(t *testing.T) (port int, client *imapclient.Client, shutdown func()) {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("mailbox@test.local", "s3cret")
	for _, name := range []string{"INBOX", "Sent", "Drafts"} {
		require.NoError(t, user.Create(name, nil))
	}
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	cl, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	require.NoError(t, err)
	require.NoError(t, cl.Login("mailbox@test.local", "s3cret").Wait())
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	var p int
	_, err = fmt.Sscan(portStr, &p)
	require.NoError(t, err)
	return p, cl, func() {
		_ = cl.Close()
		_ = srv.Close()
		_ = ln.Close()
	}
}

const previewServeFixtureHTML = "From: sender@test.local\r\n" +
	"To: inbox@test.local\r\n" +
	"Subject: metadata-only preview fixture\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<html><body><p>PREVIEW-SERVE-MARKER-4K9</p></body></html>"

func mintPreviewToken(t *testing.T, env *mailRedEnv, ref string) string {
	t.Helper()
	body := fmt.Sprintf(`{"workspace_id":%q,"agent_id":%q,"folder":"inbox","message_ref":%q,"load_remote":false}`,
		mailRedWS, mailRedAgent, ref)
	mint := mailDo(env.mux, http.MethodPost, "/api/v1/mail/html-preview-token", nextMailIP(), true, body)
	require.Equal(t, http.StatusOK, mint.Code, "mint must succeed for the metadata-only assertions (body=%s)", mint.Body.String())
	var minted struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(mint.Body.Bytes(), &minted))
	require.NotEmpty(t, minted.Token, "mint response carried no token")
	return minted.Token
}

func TestPreviewGrant_MessageGrantStoresMetadataOnly(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, cl, shutdown := startClosablePlainIMAP(t)
	defer shutdown()
	pointMailboxAt(t, env, imapPort, imapPort)
	appendRaw(t, cl, "INBOX", []byte(previewServeFixtureHTML), nil)

	token := mintPreviewToken(t, env, "uid:1:1")

	// White-box (MC-11): the MESSAGE grant in the store carries metadata
	// only. HTML/Inline exist on the grant type solely for the SIGNATURE
	// kind's operator-supplied preview; a message grant must leave both
	// empty forever.
	store := env.api.mailPreviewTokenStoreOf()
	require.NotNil(t, store)
	g, ok := store.lookup(token)
	require.True(t, ok, "the minted grant must be in the store")
	if g.Kind == mailPreviewKindSignature {
		t.Fatal("instrument error: minted a signature grant where a message grant was requested")
	}
	if g.HTML != "" {
		t.Fatalf("MC-11/US-6.1: the message grant carries an HTML payload (%d bytes) — a body cache by another name", len(g.HTML))
	}
	if g.Inline != nil {
		t.Fatalf("MC-11/US-6.1: the message grant carries %d inline part entries — inline bytes are body retention", len(g.Inline))
	}
	// The reference metadata the spec DOES require (US-6.1: pair/folder/ref/
	// load-remote identity, session binding, expiry).
	require.Equal(t, mailRedWS, g.WorkspaceID)
	require.Equal(t, mailRedAgent, g.AgentID)
	require.Equal(t, "inbox", g.Folder)
	require.Equal(t, "uid:1:1", g.Ref)
	require.False(t, g.ExpiresAt.IsZero(), "the grant must carry expiry metadata")
}

func TestPreviewServe_SecondServeFetchesLiveNeverStaleCache(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, cl, shutdown := startClosablePlainIMAP(t)
	pointMailboxAt(t, env, imapPort, imapPort)
	appendRaw(t, cl, "INBOX", []byte(previewServeFixtureHTML), nil)

	token := mintPreviewToken(t, env, "uid:1:1")

	servePath := "/mail-preview/html/" + token
	first := mailDo(env.mux, http.MethodGet, servePath, nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, first.Code, "serve #1 must succeed while the upstream is alive (body=%s)", first.Body.String())
	if cc := first.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("US-6.2: the preview serve must carry Cache-Control: no-store, got %q", cc)
	}

	// Kill the upstream. A second serve that still answers 200 is serving a
	// retained body — the "recent-preview keep" — because a LIVE fetch has
	// nothing left to fetch (B-27: the error state, never a stale cached
	// body).
	shutdown()

	second := mailDo(env.mux, http.MethodGet, servePath, nextMailIP(), true, "")
	if second.Code == http.StatusOK {
		t.Fatalf("US-6.2/MC-12: serve #2 returned 200 with the upstream dead — the serve path kept a body cache instead of performing its own live fetch (body=%s)", second.Body.String())
	}

	// The grant must STILL carry no body after a served preview: a per-token
	// HTML keep would have been written back by now.
	if g, ok := env.api.mailPreviewTokenStoreOf().lookup(token); ok {
		if g.HTML != "" || g.Inline != nil {
			t.Fatal("MC-11/§4.2: after serving, the grant carries body bytes — a recent-preview keep, which the request-only body rule forbids")
		}
	}
}
