package gateway

// IMAP fixture for the mail RED tests that need a real folder to answer.
// Same plaintext imapmemserver shape as pkg/email/imapserver_test.go::startMemIMAP
// (D36). The production client dials TLS today; a handler that cannot talk to
// this fixture cannot satisfy the spec's built-in fake server either.

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/require"
)

func pointMailboxAt(t *testing.T, env *mailRedEnv, imapPort, smtpPort int) {
	t.Helper()
	cfg := env.api.agentLoop.GetConfig()
	mb := cfg.Mailboxes[mailRedAgent][mailRedWS]
	mb.IMAPHost = "127.0.0.1"
	mb.IMAPPort = imapPort
	mb.SMTPHost = "127.0.0.1"
	mb.SMTPPort = smtpPort
	cfg.Mailboxes[mailRedAgent][mailRedWS] = mb
}

func startPlainIMAP(t *testing.T) (int, *imapclient.Client) {
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
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })

	cl, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })
	require.NoError(t, cl.Login("mailbox@test.local", "s3cret").Wait())
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscan(portStr, &port)
	require.NoError(t, err)
	return port, cl
}

func appendRaw(t *testing.T, cl *imapclient.Client, mailbox string, raw []byte, flags []imap.Flag) {
	t.Helper()
	cmd := cl.Append(mailbox, int64(len(raw)), &imap.AppendOptions{Flags: flags})
	_, err := cmd.Write(raw)
	require.NoError(t, err)
	require.NoError(t, cmd.Close())
	_, err = cmd.Wait()
	require.NoError(t, err)
}

func listenCount(t *testing.T) (int, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	var n atomic.Int32
	go func() {
		for {
			c, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			n.Add(1)
			_ = c.Close()
		}
	}()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscan(portStr, &port)
	require.NoError(t, err)
	return port, &n
}

func TestMailDraftSend_StaleUIDValidityIs409(t *testing.T) {
	// MC-16: path and body agree, but that uidvalidity is not the folder's.
	// Nothing may be handed to SMTP.
	env := newMailRedEnv(t)
	path := mailMessagesPath("drafts") + "/uid:999:1/send"
	requireMailLive(t, env.mux, http.MethodPost, path, "MC-16 / spec §2.3")
	imapPort, cl := startPlainIMAP(t)
	smtpPort, smtpHits := listenCount(t)
	pointMailboxAt(t, env, imapPort, smtpPort)
	appendRaw(t, cl, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})

	body := `{"to":["a@example.test"],"subject":"s","body_markdown":"hello","uidvalidity":999,"uid":1}`
	rec := mailDo(env.mux, http.MethodPost, path, nextMailIP(), true, body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("MC-16: uidvalidity 999 against a fresh Drafts folder = %d, want 409. body=%s", rec.Code, rec.Body.String())
	}
	er := decodeMailErr(t, rec)
	if er.Code == nil || *er.Code != "stale_draft" {
		got := "<nil>"
		if er.Code != nil {
			got = *er.Code
		}
		t.Fatalf("MC-16: code = %s, want stale_draft", got)
	}
	if smtpHits.Load() != 0 {
		t.Fatalf("MC-16: stale send opened %d SMTP connections; nothing may be transmitted", smtpHits.Load())
	}
}

func TestAttachmentDownload_HTMLIsNeverInline(t *testing.T) {
	// MC-42 / C8: an .html part is always Content-Disposition: attachment,
	// nosniff, extension-typed, RFC 6266 dual filename, and no CSP.
	env := newMailRedEnv(t)
	msgPath := mailMessagesPath("inbox") + "/uid:1:1"
	requireMailLive(t, env.mux, http.MethodGet, msgPath, "MC-42 / spec §2.3")
	imapPort, cl := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, cl, "INBOX", []byte(htmlAttachRaw), nil)

	part := mailDo(env.mux, http.MethodGet, msgPath+"/attachments/1", nextMailIP(), true, "")
	if part.Code == http.StatusNotFound {
		part = mailDo(env.mux, http.MethodGet, msgPath+"/attachments/2", nextMailIP(), true, "")
	}
	if part.Code != http.StatusOK {
		t.Fatalf("MC-42: html attachment download = %d, want 200. body=%s", part.Code, part.Body.String())
	}
	disp := part.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disp, "attachment") {
		t.Fatalf("MC-42: Content-Disposition = %q, want attachment (an .html part is never inline)", disp)
	}
	if strings.Contains(disp, "../") || strings.Contains(disp, `..\`) || strings.Contains(disp, "/evil") {
		t.Fatalf("MC-42: served filename still has a path separator: %q", disp)
	}
	if part.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("MC-42: nosniff missing, got %q", part.Header().Get("X-Content-Type-Options"))
	}
	if ct := part.Header().Get("Content-Type"); ct != "text/html" && ct != "text/html; charset=utf-8" {
		t.Fatalf("MC-42: extension-derived content type = %q, want text/html", ct)
	}
	if part.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("MC-42: attachment response must not carry a CSP")
	}
	if !strings.Contains(disp, "filename*") {
		t.Fatalf("MC-42: Content-Disposition lacks the RFC 6266 filename* form: %q", disp)
	}
}

const draftRaw = "From: mia@example.test\r\nTo: a@example.test\r\nSubject: draft\r\n" +
	"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\nMessage-ID: <draft@example.test>\r\n" +
	"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello\r\n"

const htmlAttachRaw = "From: a@b.test\r\nTo: mailbox@test.local\r\nSubject: files\r\n" +
	"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\nMessage-ID: <files@b.test>\r\n" +
	"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=bnd\r\n\r\n" +
	"--bnd\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
	"--bnd\r\nContent-Type: text/html; charset=utf-8\r\n" +
	"Content-Disposition: attachment; filename=\"../../evil.html\"\r\n\r\n" +
	"<script>alert(1)</script>\r\n--bnd--\r\n"
