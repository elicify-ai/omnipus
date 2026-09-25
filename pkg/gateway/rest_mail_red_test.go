package gateway

// RED — mail panel HTTP routes. Oracles are docs/internal/specs/email-mail-view-spec.md
// §2.3, MC-5, MC-6, MC-8, MC-13, MC-16, MC-20, MC-22, MC-27, round-2 MAJ-008.
// Expectations were written from that spec, not from a handler (there is no
// mail handler yet). A path that still falls through to the stock library's
// "404 page not found" is not a mail route: HandleWorkspaces already answers
// unknown /workspaces/{id}/… tails that way, and that answer is not the spec.

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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

const (
	mailRedToken  = "mail-red-token"
	mailRedWS     = "ws-mail"
	mailRedAgent  = "mia"
	mailRedOrigin = "http://mail.example.test"
)

// Closed set from §2.3 / MC-8. A body that names anything else is wrong.
var mailUpstreamClasses = map[string]bool{
	"timeout": true, "dns": true, "connect_refused": true, "auth_failed": true,
	"tls": true, "folder_missing": true, "server_error": true,
}

var mailRedIP atomic.Int32

func nextMailIP() string {
	n := mailRedIP.Add(1)
	return fmt.Sprintf("198.51.100.%d", 20+(n%200))
}

type mailRedEnv struct {
	api *restAPI
	mux *http.ServeMux
}

func newMailRedEnv(t *testing.T) *mailRedEnv {
	t.Helper()
	api := newTestRestAPIWithHome(t)
	t.Setenv("OMNIPUS_BEARER_TOKEN", mailRedToken)
	store := newUnlockedStore(t, api.homePath)
	require.NoError(t, store.Set(mailboxCredKey(mailRedAgent, mailRedWS), "s3cret"))
	api.credStore = store
	cfg := api.agentLoop.GetConfig()
	cfg.Gateway.PublicURL = mailRedOrigin
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID: mailRedAgent, Name: "Mia", Type: config.AgentTypeCustom, Home: api.homePath,
	})
	cfg.Mailboxes = config.MailboxesConfig{
		mailRedAgent: {
			mailRedWS: {
				Enabled: true, WorkspaceID: mailRedWS,
				IMAPHost: "127.0.0.1", IMAPPort: 59991,
				SMTPHost: "127.0.0.1", SMTPPort: 59991,
				Username:    "mailbox@test.local",
				PasswordRef: mailboxCredKey(mailRedAgent, mailRedWS),
			},
		},
	}
	seedWorkspaceFile(t, api.homePath, mailRedWS)
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	return &mailRedEnv{api: api, mux: mux}
}

func mailDo(mux *http.ServeMux, method, path, ip string, authed bool, body string) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = ip + ":4321"
	req.Header.Set("Content-Type", "application/json")
	if authed {
		req.Header.Set("Authorization", "Bearer "+mailRedToken)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func stdlibNotFound(rec *httptest.ResponseRecorder) bool {
	return rec.Code == http.StatusNotFound && strings.TrimSpace(rec.Body.String()) == "404 page not found"
}

// requireMailLive fails the test when the path is not a mail handler yet.
// The stock 404 is HandleWorkspaces' unknown-tail answer, or the mux default.
func requireMailLive(t *testing.T, mux *http.ServeMux, method, path, spec string) {
	t.Helper()
	rec := mailDo(mux, method, path, nextMailIP(), true, "")
	if stdlibNotFound(rec) {
		t.Fatalf("BLOCKED: %s %s not implemented — required by %s", method, path, spec)
	}
}

func mailFoldersPath() string {
	return "/api/v1/workspaces/" + mailRedWS + "/mail/" + mailRedAgent + "/folders"
}

func mailMessagesPath(folder string) string {
	return mailFoldersPath() + "/" + folder + "/messages"
}

func decodeMailErr(t *testing.T, rec *httptest.ResponseRecorder) gen.ErrorResponse {
	t.Helper()
	var er gen.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil {
		t.Fatalf("status %d body is not ErrorResponse (%v): %s", rec.Code, err, rec.Body.String())
	}
	return er
}

func assertNoUpstreamLeak(t *testing.T, body string) {
	t.Helper()
	for _, leak := range []string{"127.0.0.1", "connection refused", "dial tcp", "imap", "smtp"} {
		if strings.Contains(strings.ToLower(body), leak) {
			t.Fatalf("MC-8: raw upstream text %q leaked into the body: %s", leak, body)
		}
	}
}

func TestMailRoutes_UnauthenticatedIs401(t *testing.T) {
	env := newMailRedEnv(t)
	// §2.3: every /api/v1 mail route is session-authenticated. Checked only
	// after the route exists — a 401 from the parent /workspaces/ wrapper is
	// not this route's auth.
	routes := []struct {
		method, path, spec string
	}{
		{http.MethodGet, mailFoldersPath(), "spec §2.3 folders"},
		{http.MethodGet, mailMessagesPath("inbox") + "?limit=20", "spec §2.3 message page"},
		{http.MethodGet, mailMessagesPath("inbox") + "/uid:1:1", "spec §2.3 full message"},
		{http.MethodPost, mailMessagesPath("inbox") + "/uid:1:1/seen", "spec §2.3 seen"},
		{http.MethodGet, mailMessagesPath("inbox") + "/uid:1:1/attachments/1", "spec §2.3 attachment / MC-42"},
		{http.MethodPost, "/api/v1/workspaces/" + mailRedWS + "/mail/" + mailRedAgent + "/messages", "spec §2.3 manual send"},
		{http.MethodPut, mailMessagesPath("drafts") + "/uid:1:1", "spec §2.3 draft edit"},
		{http.MethodPost, mailMessagesPath("drafts") + "/uid:1:1/send", "spec §2.3 draft send"},
		{http.MethodDelete, mailMessagesPath("drafts") + "/uid:1:1", "spec §2.3 discard"},
		{http.MethodPost, "/api/v1/mail/html-preview-token", "spec §2.3 mint / MC-44"},
		{http.MethodGet, "/api/v1/workspaces/" + mailRedWS + "/mail/summary", "spec §2.3 summary / MC-23"},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			requireMailLive(t, env.mux, rt.method, rt.path, rt.spec)
			rec := mailDo(env.mux, rt.method, rt.path, nextMailIP(), false, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s: unauthenticated %s = %d, want 401. body=%s", rt.spec, rt.path, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMailFolders_UnknownSlugIs400(t *testing.T) {
	env := newMailRedEnv(t)
	path := mailMessagesPath("trash")
	requireMailLive(t, env.mux, http.MethodGet, path, "MC-5 / spec §2.3")
	rec := mailDo(env.mux, http.MethodGet, path, nextMailIP(), true, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("MC-5: folder slug trash = %d, want 400. body=%s", rec.Code, rec.Body.String())
	}
	er := decodeMailErr(t, rec)
	if er.Error == "" {
		t.Fatal("MC-5: 400 body has an empty ErrorResponse.error")
	}
}

func TestMailMessages_LimitBoundaries(t *testing.T) {
	// MC-6: limit <= 0 defaults to 20; limit > 100 is clamped to 100.
	// §2.3's "400 (limit out of range)" is the non-numeric case. A numeric
	// limit of 101 must not be rejected — MC-6 is the machine constraint (T18).
	env := newMailRedEnv(t)
	base := mailMessagesPath("inbox")
	requireMailLive(t, env.mux, http.MethodGet, base, "MC-6 / spec §2.3")
	over := mailDo(env.mux, http.MethodGet, base+"?limit=101", nextMailIP(), true, "")
	if over.Code == http.StatusBadRequest {
		t.Fatalf("MC-6: limit=101 was rejected (%s); the spec clamps it to 100", over.Body.String())
	}
	bad := mailDo(env.mux, http.MethodGet, base+"?limit=abc", nextMailIP(), true, "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("spec §2.3: non-numeric limit = %d, want 400. body=%s", bad.Code, bad.Body.String())
	}
	zero := mailDo(env.mux, http.MethodGet, base+"?limit=0", nextMailIP(), true, "")
	if zero.Code == http.StatusBadRequest {
		t.Fatalf("MC-6: limit=0 was rejected; the spec defaults it to 20. body=%s", zero.Body.String())
	}
}

func TestMailMessage_MalformedRefIs400(t *testing.T) {
	env := newMailRedEnv(t)
	path := mailMessagesPath("inbox") + "/not-a-ref"
	requireMailLive(t, env.mux, http.MethodGet, path, "spec §2.3 malformed ref")
	rec := mailDo(env.mux, http.MethodGet, path, nextMailIP(), true, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("spec §2.3: malformed ref = %d, want 400. body=%s", rec.Code, rec.Body.String())
	}
}

func TestMailMessage_MessageIDOver998Is400(t *testing.T) {
	// §2.3: Message-ID validation is <…@…>, at most 998 bytes, no CR/LF.
	env := newMailRedEnv(t)
	id := strings.Repeat("a", 999) + "@x.test"
	path := mailMessagesPath("inbox") + "/mid:" + id
	requireMailLive(t, env.mux, http.MethodGet, mailMessagesPath("inbox")+"/uid:1:1", "DS-4 / spec §2.3")
	rec := mailDo(env.mux, http.MethodGet, path, nextMailIP(), true, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("DS-4: Message-ID longer than 998 bytes = %d, want 400. body=%s", rec.Code, rec.Body.String())
	}
}

func TestMailFolders_UpstreamFailureIs502Class(t *testing.T) {
	// MC-8: nothing is listening on 127.0.0.1:59991, so the dial is refused at once.
	// The body carries one closed error class and no raw upstream text.
	env := newMailRedEnv(t)
	path := mailFoldersPath()
	requireMailLive(t, env.mux, http.MethodGet, path, "MC-8 / spec §2.3")
	rec := mailDo(env.mux, http.MethodGet, path, nextMailIP(), true, "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("MC-8: unreachable mail server = %d, want 502. body=%s", rec.Code, rec.Body.String())
	}
	er := decodeMailErr(t, rec)
	if er.Code == nil || !mailUpstreamClasses[*er.Code] {
		got := "<nil>"
		if er.Code != nil {
			got = *er.Code
		}
		t.Fatalf("MC-8: ErrorResponse.code = %s, want one of timeout|dns|connect_refused|auth_failed|tls|folder_missing|server_error", got)
	}
	if *er.Code != "connect_refused" {
		t.Fatalf("MC-8: dialing 127.0.0.1:59991 (nothing listening) coded %s, want connect_refused", *er.Code)
	}
	assertNoUpstreamLeak(t, rec.Body.String())
}

func TestMailPairMissing_Is404WithoutLeak(t *testing.T) {
	// MC-13: a pair with no mailbox is 404, and the body does not describe
	// another mailbox. This workspace was never seeded.
	env := newMailRedEnv(t)
	path := "/api/v1/workspaces/missing-ws/mail/mia/folders"
	requireMailLive(t, env.mux, http.MethodGet, mailFoldersPath(), "MC-13 / spec §2.3")
	rec := mailDo(env.mux, http.MethodGet, path, nextMailIP(), true, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("MC-13: pair with no mailbox = %d, want 404. body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "mailbox@test.local") || strings.Contains(rec.Body.String(), mailRedWS) {
		t.Fatalf("MC-13: 404 leaked another pair's mailbox: %s", rec.Body.String())
	}
}

func TestMailSend_ValidationBeforeDial(t *testing.T) {
	env := newMailRedEnv(t)
	path := "/api/v1/workspaces/" + mailRedWS + "/mail/" + mailRedAgent + "/messages"
	requireMailLive(t, env.mux, http.MethodPost, path, "MC-22 / MC-27 / spec §2.3")

	empty := gen.MailSendRequest{To: []string{}, Subject: "s", BodyMarkdown: "hello"}
	raw, err := json.Marshal(empty)
	require.NoError(t, err)
	rec := mailDo(env.mux, http.MethodPost, path, nextMailIP(), true, string(raw))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("US-5 AS-2: empty To = %d, want 400. body=%s", rec.Code, rec.Body.String())
	}

	addrs := make([]string, 51)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("person%d@example.test", i)
	}
	over := gen.MailSendRequest{To: addrs, Subject: "s", BodyMarkdown: "hello"}
	raw, err = json.Marshal(over)
	require.NoError(t, err)
	rec = mailDo(env.mux, http.MethodPost, path, nextMailIP(), true, string(raw))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("MC-27: 51st recipient = %d, want 400 before any SMTP connection. body=%s", rec.Code, rec.Body.String())
	}

	huge := gen.MailSendRequest{To: []string{"a@example.test"}, Subject: "s", BodyMarkdown: strings.Repeat("x", (1<<20)+1)}
	raw, err = json.Marshal(huge)
	require.NoError(t, err)
	rec = mailDo(env.mux, http.MethodPost, path, nextMailIP(), true, string(raw))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("MC-22: body of 1 MiB+1 = %d, want 400 and nothing transmitted. body=%s", rec.Code, rec.Body.String())
	}
}

func TestMailDraftEdit_PathAndBodyUIDMismatchIs400(t *testing.T) {
	// Round-2 MAJ-008: a uid: path and the body precondition must agree.
	// Disagreement is 400, not a silent accept. This is decided before IMAP.
	env := newMailRedEnv(t)
	path := mailMessagesPath("drafts") + "/uid:5:9"
	requireMailLive(t, env.mux, http.MethodPut, path, "spec §2.3 MAJ-008")
	body := gen.MailDraftUpdateRequest{
		To: []string{"a@example.test"}, Subject: "s", BodyMarkdown: "hello",
		Uidvalidity: 4, Uid: 9, KeepAttachmentParts: []int{},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	rec := mailDo(env.mux, http.MethodPut, path, nextMailIP(), true, string(raw))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("MAJ-008: path uidvalidity 5 vs body 4 = %d, want 400. body=%s", rec.Code, rec.Body.String())
	}
}

func TestMailDraftSend_UnreachableIs502NotStale(t *testing.T) {
	// MC-8 on the panel-send route: 127.0.0.1:59991 refuses the dial. That is not
	// MC-16's stale_draft 409 — staleness is a fact about a draft the server
	// still listed, and this mailbox cannot be listed. A 409 here would send
	// the human to re-approve mail the server never showed them.
	// MC-16's 409 itself needs a draft whose uid moved under a server this
	// package can dial. pkg/email's imapDial seam is unexported, so that case
	// cannot be staged from here; the path/body 400 above is the pre-dial half.
	env := newMailRedEnv(t)
	path := mailMessagesPath("drafts") + "/uid:1:1/send"
	requireMailLive(t, env.mux, http.MethodPost, path, "MC-8 / spec §2.3 panel send")
	body := gen.MailDraftSendRequest{
		To: []string{"a@example.test"}, Subject: "s", BodyMarkdown: "hello",
		Uidvalidity: 1, Uid: 1,
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	rec := mailDo(env.mux, http.MethodPost, path, nextMailIP(), true, string(raw))
	if rec.Code == http.StatusConflict {
		t.Fatalf("MC-8: an unreachable mailbox was reported as stale_draft. body=%s", rec.Body.String())
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("MC-8: panel send to a refused IMAP port = %d, want 502. body=%s", rec.Code, rec.Body.String())
	}
	er := decodeMailErr(t, rec)
	if er.Code == nil || *er.Code != "connect_refused" {
		got := "<nil>"
		if er.Code != nil {
			got = *er.Code
		}
		t.Fatalf("MC-8: panel send code = %s, want connect_refused", got)
	}
}

func TestMailMutations_EleventhRequestIs429(t *testing.T) {
	// MC-20: a dedicated limiter, 10 requests/minute per IP. The 11th is 429
	// with Retry-After. The first ten may fail for other reasons (no live
	// SMTP); they must not be 429, or the window is not 10.
	env := newMailRedEnv(t)
	path := "/api/v1/workspaces/" + mailRedWS + "/mail/" + mailRedAgent + "/messages"
	requireMailLive(t, env.mux, http.MethodPost, path, "MC-20 / spec §2.3")
	body := `{"to":["a@example.test"],"subject":"s","body_markdown":"hello"}`
	ip := nextMailIP()
	for i := 1; i <= 10; i++ {
		rec := mailDo(env.mux, http.MethodPost, path, ip, true, body)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("MC-20: request %d of 10 was already 429 — the limit is not 10/minute", i)
		}
	}
	rec := mailDo(env.mux, http.MethodPost, path, ip, true, body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("MC-20: 11th mutating request = %d, want 429. body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("MC-20: 429 is missing Retry-After")
	}
}

func TestMailPreviewMint_EleventhIs429AndDoesNotDial(t *testing.T) {
	// MC-44: the mint has its own per-IP limiter, 10/minute. The refused call
	// must not dial IMAP. The listener counts accepts on the mailbox port.
	env := newMailRedEnv(t)
	path := "/api/v1/mail/html-preview-token"
	requireMailLive(t, env.mux, http.MethodPost, path, "MC-44 / spec §2.3")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscan(portStr, &port)
	require.NoError(t, err)
	cfg := env.api.agentLoop.GetConfig()
	mb := cfg.Mailboxes[mailRedAgent][mailRedWS]
	mb.IMAPPort = port
	cfg.Mailboxes[mailRedAgent][mailRedWS] = mb
	var dials atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			_ = c.Close()
		}
	}()

	body := fmt.Sprintf(`{"workspace_id":%q,"agent_id":%q,"folder":"inbox","message_ref":"uid:1:1","load_remote":false}`, mailRedWS, mailRedAgent)
	ip := nextMailIP()
	for i := 1; i <= 10; i++ {
		rec := mailDo(env.mux, http.MethodPost, path, ip, true, body)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("MC-44: mint %d of 10 was already 429", i)
		}
	}
	before := dials.Load()
	rec := mailDo(env.mux, http.MethodPost, path, ip, true, body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("MC-44: 11th mint = %d, want 429. body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("MC-44: 429 is missing Retry-After")
	}
	if dials.Load() != before {
		t.Fatalf("MC-44: the refused mint dialed IMAP (%d accepts before, %d after)", before, dials.Load())
	}
}
