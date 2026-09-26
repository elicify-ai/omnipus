package gateway

// N3(b) — D7/D18/MC-9 over the wire: a sent message's copy lands in the
// Sent folder; a failed Sent APPEND never fails the send itself (200 +
// sent_saved=false + non-empty save_warning — never silent, never a 5xx).
// The send must actually SUCCEED first for both claims to mean anything,
// so each test drives a real send against a minimal local SMTP sink
// (D36 fake-server strategy). RED today: the production SMTP client
// (pkg/email/transport.go::sendSMTPWithSTARTTLS) unconditionally demands
// trusted-TLS STARTTLS on non-465 with no loopback exception mirroring
// imapDial, so no local sink can complete a send. When backend-lead adds
// the loopback plaintext exception, these tests go green unmodified: the
// sink answers EHLO/AUTH PLAIN/MAIL/RCPT/DATA/QUIT over plaintext loopback
// (PlainAuth sends credentials to 127.0.0.1 without TLS by net/smtp rule).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// smtpSink is a minimal plaintext SMTP endpoint that accepts everything.
type smtpSink struct {
	addr string
	ln   net.Listener

	mu       sync.Mutex
	bodies   []string
	dataDone int
	quits    int
}

func startSMTPSink(t *testing.T) *smtpSink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	sink := &smtpSink{ln: ln, addr: ln.Addr().String()}
	go sink.serve()
	return sink
}

func (s *smtpSink) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *smtpSink) handle(c net.Conn) {
	defer c.Close()
	w := bufio.NewWriter(c)
	rd := bufio.NewScanner(c)
	rd.Buffer(make([]byte, 64*1024), 8*1024*1024)
	say := func(line string) {
		_, _ = w.WriteString(line + "\r\n")
		_ = w.Flush()
	}
	say("220 sink.test ESMTP N3")
	inData := false
	var body strings.Builder
	for rd.Scan() {
		line := rd.Text()
		if inData {
			if line == "." {
				s.mu.Lock()
				s.bodies = append(s.bodies, body.String())
				s.dataDone++
				s.mu.Unlock()
				inData = false
				body.Reset()
				say("250 2.0.0 OK queued")
			} else {
				body.WriteString(line + "\n")
			}
			continue
		}
		up := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(up, "EHLO"):
			say("250-sink.test")
			say("250-AUTH PLAIN LOGIN")
			say("250 8BITMIME")
		case strings.HasPrefix(up, "HELO"):
			say("250 sink.test")
		case strings.HasPrefix(up, "STARTTLS"):
			say("454 4.7.0 TLS not available")
		case strings.HasPrefix(up, "AUTH"):
			say("235 2.7.0 Accepted")
		case strings.HasPrefix(up, "MAIL FROM"):
			say("250 2.1.0 OK")
		case strings.HasPrefix(up, "RCPT TO"):
			say("250 2.1.5 OK")
		case strings.HasPrefix(up, "DATA"):
			inData = true
			say("354 End data with <CR><LF>.<CR><LF>")
		case strings.HasPrefix(up, "QUIT"):
			s.mu.Lock()
			s.quits++
			s.mu.Unlock()
			say("221 2.0.0 bye")
			return
		default:
			say("250 2.1.0 OK")
		}
	}
}

// bodyCount reports how many DATA bodies the sink accepted.
func (s *smtpSink) acceptedBodies() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dataDone, append([]string(nil), s.bodies...)
}

// portOfAddr extracts the numeric port from a host:port address.
func portOfAddr(t *testing.T, addr string) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return port
}

// startIMAPNoSent is startPlainIMAP minus the Sent folder — the MC-9
// scenario shape (INBOX + Drafts only, so the Sent APPEND must fail).
func startIMAPNoSent(t *testing.T) int {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("mailbox@test.local", "s3cret")
	for _, name := range []string{"INBOX", "Drafts"} {
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
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return port
}

func TestMailSend_SentCopyLandsInSent(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, _ := startPlainIMAP(t)
	sink := startSMTPSink(t)
	smtpPort := portOfAddr(t, sink.addr)
	pointMailboxAt(t, env, imapPort, smtpPort)

	const marker = "N3 SENT COPY MARKER 7ff2a9"
	rec := mailDo(env.mux, http.MethodPost, "/api/v1/workspaces/"+mailRedWS+"/mail/"+mailRedAgent+"/messages", nextMailIP(), true,
		fmt.Sprintf(`{"to":["human@example.test"],"subject":%q,"body_markdown":"N3 sent-copy body"}`, marker))
	if rec.Code != http.StatusOK {
		t.Fatalf("BLOCKED behind the loopback-SMTP gap (D36 fake-server strategy): send = %d, want 200; body=%s - the production SMTP client demands trusted TLS (unconditional STARTTLS on non-465, pkg/email/transport.go::sendSMTPWithSTARTTLS), so no local sink can complete a send and the Sent-copy contract (D7/D18) cannot execute. Required by D7/D18/MC-9.", rec.Code, rec.Body.String())
	}
	var resp gen.MailSendResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	if !resp.SentSaved {
		t.Fatalf("sent_saved = false with a healthy Sent folder; body=%s", rec.Body.String())
	}
	if resp.SaveWarning != nil && *resp.SaveWarning != "" {
		t.Fatalf("save_warning = %q on a clean save, want empty", *resp.SaveWarning)
	}

	// Server-side: the copy is IN the Sent folder, and it is our message.
	srv, err := imapclient.DialInsecure(fmt.Sprintf("127.0.0.1:%d", imapPort), nil)
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Login("mailbox@test.local", "s3cret").Wait())
	sel, err := srv.Select("Sent", nil).Wait()
	require.NoError(t, err)
	if sel.NumMessages != 1 {
		t.Fatalf("D7: Sent holds %d messages after one send, want exactly 1", sel.NumMessages)
	}
	opts := &imap.FetchOptions{Flags: true, BodySection: []*imap.FetchItemBodySection{{}}}
	secs, ferr := srv.Fetch(imap.SeqSetNum(1), opts).Collect()
	require.NoError(t, ferr)
	require.Len(t, secs, 1)
	var raw []byte
	if len(secs[0].BodySection) > 0 {
		raw = secs[0].BodySection[0].Bytes
	}
	if !strings.Contains(string(raw), marker) {
		t.Fatalf("D7: the Sent row is not the sent message (marker %q absent from %d fetched bytes)", marker, len(raw))
	}
	n, bodies := sink.acceptedBodies()
	if n != 1 || len(bodies) == 0 || !strings.Contains(bodies[0], marker) {
		t.Fatalf("instrument: sink accepted %d DATA body(ies); the message itself must have been transmitted", n)
	}
}

func TestMailSend_SaveCopyFailureNeverFailsSend(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort := startIMAPNoSent(t)
	sink := startSMTPSink(t)
	pointMailboxAt(t, env, imapPort, portOfAddr(t, sink.addr))

	rec := mailDo(env.mux, http.MethodPost, "/api/v1/workspaces/"+mailRedWS+"/mail/"+mailRedAgent+"/messages", nextMailIP(), true,
		`{"to":["human@example.test"],"subject":"N3 MC-9","body_markdown":"send must not fail when the Sent APPEND does"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("BLOCKED behind the loopback-SMTP gap (D36 fake-server strategy): send = %d, want 200; body=%s - the production SMTP client demands trusted TLS (unconditional STARTTLS on non-465, pkg/email/transport.go::sendSMTPWithSTARTTLS), so no local sink can complete a send and the D18/MC-9 contract (a save-copy failure never fails the send) cannot execute. Required by D18/MC-9.", rec.Code, rec.Body.String())
	}
	var resp gen.MailSendResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	if resp.SentSaved {
		t.Fatal("MC-9: sent_saved = true although the mailbox has no Sent folder - the wire must not claim the save succeeded")
	}
	if resp.SaveWarning == nil || strings.TrimSpace(*resp.SaveWarning) == "" {
		t.Fatalf("MC-9: save_warning = %v, want a non-empty warning (never silent)", resp.SaveWarning)
	}
	n, bodies := sink.acceptedBodies()
	if n != 1 || len(bodies) == 0 || !strings.Contains(bodies[0], "N3 MC-9") {
		t.Fatalf("MC-9: sink accepted %d DATA body(ies) - the send itself must have succeeded for the contract to mean anything", n)
	}
}
