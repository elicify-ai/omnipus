// Tests for the fakemail fixture's optional flags (founder-review support):
// loopback-only listeners, -users provisioning, -deliver SMTP-to-INBOX
// delivery, the /inject control endpoint, and the no-flag consumer contract
// (the first stdout line).
//
// Oracles are derived from the fixture spec (the squad dispatch) and the
// existing consumer contract, never from running the implementation: the
// first-line JSON key set is pinned by tests/e2e/fixtures/fake-mail-server.ts
// (startFakeMail parses exactly imap/smtp/control/user/password), the
// loopback rule is Omnipus policy (plaintext IMAP/SMTP only to loopback), and
// delivery/inject semantics come from the dispatch. Every test drives the
// REAL IMAP/SMTP/control servers over real TCP sockets -- nothing is mocked.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// startSrv starts the fixture with the given config on ephemeral loopback
// ports and registers a cleanup.
func startSrv(t *testing.T, cfg config) *fakeServer {
	t.Helper()
	srv, err := run(cfg, io.Discard)
	if err != nil {
		t.Fatalf("run(%+v): %v", cfg, err)
	}
	t.Cleanup(srv.Stop)
	return srv
}

// twoReviewUsers is the dispatch's example -users list.
func twoReviewUsers(t *testing.T) []mailUser {
	t.Helper()
	users, err := parseUsers("agent@test.local:s3cret,alice@test.local:s3cret")
	if err != nil {
		t.Fatalf("parseUsers: %v", err)
	}
	return users
}

// TestDefaultModeFirstLineMatchesConsumerContract pins the no-flag stdout
// contract: exactly the five keys fake-mail-server.ts parses, with the
// single default user. Expected values come from that consumer
// (startFakeMail's JSON decode), not from the implementation.
func TestDefaultModeFirstLineMatchesConsumerContract(t *testing.T) {
	var out bytes.Buffer
	srv, err := run(config{}, &out)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer srv.Stop()

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatalf("first stdout line is not a JSON object: %v (line: %q)", err, out.String())
	}
	wantKeys := []string{"control", "imap", "password", "smtp", "user"}
	if !reflect.DeepEqual(sortedKeys(raw), wantKeys) {
		t.Fatalf("first line keys = %v, want exactly %v (consumer contract: fake-mail-server.ts)", sortedKeys(raw), wantKeys)
	}
	var line struct{ Imap, Smtp, Control, User, Password string }
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("decode first line: %v", err)
	}
	// Dispatch: "default stays the single mailbox@test.local user", with the
	// password the fixture has always used.
	if line.User != "mailbox@test.local" || line.Password != "s3cret" {
		t.Fatalf("user/password = %q/%q, want mailbox@test.local/s3cret", line.User, line.Password)
	}
	for what, addr := range map[string]string{"imap": line.Imap, "smtp": line.Smtp, "control": line.Control} {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatalf("%s address %q is not host:port: %v", what, addr, err)
		}
		if host != "127.0.0.1" {
			t.Fatalf("%s host = %q, want 127.0.0.1", what, host)
		}
	}
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// encoding/json emits map keys sorted; sort here for the exact-set compare.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// TestNonLoopbackListenAddressRefused covers the loopback-only rule
// (dispatch: "Loopback only -- refuse a non-loopback address"). Non-loopback
// and unresolvable addresses are refused; loopback ones are accepted.
func TestNonLoopbackListenAddressRefused(t *testing.T) {
	cases := []struct {
		name    string
		cfg     config
		wantSub string
	}{
		{"wildcard-ipv4-imap", config{imapAddr: "0.0.0.0:1143"}, "loopback"},
		{"empty-host-smtp", config{smtpAddr: ":1025"}, "loopback"},
		{"lan-address-control", config{ctrlAddr: "192.168.1.5:1180"}, "loopback"},
		{"unresolvable-host", config{imapAddr: "999.999.999.999:143"}, "resolve"},
	}
	for _, tc := range cases {
		t.Run("refuses-"+tc.name, func(t *testing.T) {
			srv, err := run(tc.cfg, io.Discard)
			if err == nil {
				srv.Stop()
				t.Fatalf("run(%+v) succeeded, want refusal mentioning %q", tc.cfg, tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("refusal %q does not mention %q", err, tc.wantSub)
			}
		})
	}
	// Accept cases are loopback forms every supported platform binds:
	// arbitrary 127.x addresses are only bindable on Linux (macOS rejects
	// them), so they are not exercised here.
	for _, addr := range []string{"127.0.0.1:0", "localhost:0", "[::1]:0"} {
		t.Run("accepts-"+addr, func(t *testing.T) {
			cfg := config{imapAddr: addr, smtpAddr: "127.0.0.1:0", ctrlAddr: "127.0.0.1:0"}
			srv, err := run(cfg, io.Discard)
			if err != nil {
				t.Fatalf("run with imap=%q: %v", addr, err)
			}
			srv.Stop()
		})
	}
}

// TestDeliverAppendsSMTPMailToRecipientINBOX covers dispatch req 3: with
// -deliver, SMTP mail to a known local user is ALSO appended to that user's
// INBOX (and still recorded in the sink). Expected message content derives
// from the sink's documented accumulation convention (payload lines joined
// with \n), the same convention mail-panel.spec.ts asserts against for
// recorded messages.
func TestDeliverAppendsSMTPMailToRecipientINBOX(t *testing.T) {
	srv := startSrv(t, config{users: twoReviewUsers(t), usersFlagGiven: true, deliver: true})

	sent := []string{"From: ada@example.test", "To: agent@test.local", "Subject: hi", "", "Hello agent."}
	smtpSend(t, srv.smtpAddr, "ada@example.test", "agent@test.local", sent)
	// The delivered copy is read back over IMAP, where servers may normalise
	// line endings to CRLF (RFC 3501); the spec's claim is the delivered
	// CONTENT, so both sides are compared as LF lines. The sink recording
	// below is compared byte-exact in the sink's documented LF convention.
	want := strings.Join(sent, "\n") + "\n"

	got := imapInboxBody(t, srv.imapAddr, "agent@test.local", "s3cret")
	if strings.ReplaceAll(got, "\r\n", "\n") != want {
		t.Fatalf("agent INBOX message = %q, want content %q", got, want)
	}
	if n := imapInboxCount(t, srv.imapAddr, "alice@test.local", "s3cret"); n != 0 {
		t.Fatalf("alice INBOX has %d messages, want 0 (no cross-delivery)", n)
	}
	sn := sinkSnapshot(t, srv.ctrlAddr)
	if sn.Count != 1 || len(sn.Messages) != 1 || sn.Messages[0] != want {
		t.Fatalf("sink = %d messages %q, want 1 message %q", sn.Count, sn.Messages, want)
	}
}

// TestDefaultModeSinksWithoutDelivering covers dispatch req 3's other half
// and req 0: without -deliver the default fixture records SMTP in the sink
// (exactly as today) and never touches the INBOX.
func TestDefaultModeSinksWithoutDelivering(t *testing.T) {
	srv := startSrv(t, config{})

	body := []string{"From: ada@example.test", "To: mailbox@test.local", "Subject: dropped", "", "Body only."}
	smtpSend(t, srv.smtpAddr, "ada@example.test", "mailbox@test.local", body)
	want := strings.Join(body, "\n") + "\n"

	if n := imapInboxCount(t, srv.imapAddr, "mailbox@test.local", "s3cret"); n != 0 {
		t.Fatalf("default-mode INBOX has %d messages, want 0 (no -deliver)", n)
	}
	sn := sinkSnapshot(t, srv.ctrlAddr)
	if sn.Count != 1 || len(sn.Messages) != 1 || sn.Messages[0] != want {
		t.Fatalf("sink = %d messages %q, want 1 message %q", sn.Count, sn.Messages, want)
	}
}

// TestParseUsers covers dispatch req 2 (multi-user list, default stays the
// single mailbox@test.local user) plus negative forms. Exact expected user
// lists come from the dispatch's documented syntax "addr:password,addr:password".
func TestParseUsers(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		want    []mailUser
		wantErr string
	}{
		{"default-when-empty", "", []mailUser{{addr: "mailbox@test.local", password: "s3cret"}}, ""},
		{"single", "agent@test.local:s3cret", []mailUser{{addr: "agent@test.local", password: "s3cret"}}, ""},
		{"multi", "agent@test.local:s3cret,alice@test.local:p2",
			[]mailUser{{addr: "agent@test.local", password: "s3cret"}, {addr: "alice@test.local", password: "p2"}}, ""},
		{"password-may-contain-colon", "u@t.test:pa:ss", []mailUser{{addr: "u@t.test", password: "pa:ss"}}, ""},
		{"missing-colon", "agent@test.local", nil, "address:password"},
		{"empty-password", "agent@test.local:", nil, "address:password"},
		{"missing-at", "agenttest.local:pw", nil, "@"},
		{"empty-entry", "agent@test.local:s3cret,,alice@test.local:p2", nil, "empty user"},
		{"duplicate", "a@t.test:pw,a@t.test:pw2", nil, "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseUsers(tc.spec)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("parseUsers(%q) succeeded with %+v, want error mentioning %q", tc.spec, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseUsers(%q): %v", tc.spec, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseUsers(%q) = %+v, want %+v", tc.spec, got, tc.want)
			}
		})
	}
}

// TestInjectAppendsSampleMessageForKnownUser covers dispatch req 4: a
// documented way to inject a sample inbound message for any known user
// (/append only reaches the first user). Injection is scoped to the named
// user only.
func TestInjectAppendsSampleMessageForKnownUser(t *testing.T) {
	srv := startSrv(t, config{users: twoReviewUsers(t), usersFlagGiven: true})

	// Unknown user is a 404, not a silent no-op.
	resp, err := http.Post("http://"+srv.ctrlAddr+"/inject", "application/json",
		strings.NewReader(`{"user":"nobody@test.local","subject":"x"}`))
	if err != nil {
		t.Fatalf("POST /inject: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("inject for unknown user status = %d, want 404", resp.StatusCode)
	}

	inject := map[string]string{
		"user": "alice@test.local", "subject": "Welcome Alice",
		"from": "ada@example.test", "text": "Hello Alice.",
	}
	b, _ := json.Marshal(inject)
	resp, err = http.Post("http://"+srv.ctrlAddr+"/inject", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST /inject: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("inject status = %d, want 200", resp.StatusCode)
	}

	got := imapInboxBody(t, srv.imapAddr, "alice@test.local", "s3cret")
	for _, want := range []string{"Subject: Welcome Alice", "To: alice@test.local", "Hello Alice."} {
		if !strings.Contains(got, want) {
			t.Fatalf("injected message %q does not contain %q", got, want)
		}
	}
	if n := imapInboxCount(t, srv.imapAddr, "agent@test.local", "s3cret"); n != 0 {
		t.Fatalf("agent INBOX has %d messages, want 0 (inject must not leak across users)", n)
	}
}

// --- helpers: real SMTP and IMAP clients, no mocks ---

// smtpSend drives a minimal plaintext SMTP session against the REAL sink:
// EHLO, MAIL FROM, RCPT TO, DATA, payload, dot, QUIT, asserting each reply
// prefix. Deadlines keep failures fast instead of hanging.
func smtpSend(t *testing.T, addr, from, rcpt string, payload []string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial smtp %s: %v", addr, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	r := bufio.NewReader(conn)
	reply := func(want string) {
		t.Helper()
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("smtp read after %q: %v", want, err)
		}
		if !strings.HasPrefix(line, want) {
			t.Fatalf("smtp reply = %q, want prefix %q", line, want)
		}
	}
	write := func(s string) {
		t.Helper()
		if _, err := fmt.Fprintf(conn, "%s\r\n", s); err != nil {
			t.Fatalf("smtp write %q: %v", s, err)
		}
	}
	reply("220")
	write("EHLO test.local")
	reply("250")
	write("MAIL FROM:<" + from + ">")
	reply("250")
	write("RCPT TO:<" + rcpt + ">")
	reply("250")
	write("DATA")
	reply("354")
	for _, line := range payload {
		write(line)
	}
	write(".")
	reply("250")
	write("QUIT")
	reply("221")
}

func imapLogin(t *testing.T, addr, user, pass string) *imapclient.Client {
	t.Helper()
	cl, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatalf("dial imap %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	if err := cl.Login(user, pass).Wait(); err != nil {
		t.Fatalf("imap login %s: %v", user, err)
	}
	return cl
}

func imapInboxCount(t *testing.T, addr, user, pass string) uint32 {
	t.Helper()
	cl := imapLogin(t, addr, user, pass)
	data, err := cl.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("select INBOX as %s: %v", user, err)
	}
	return data.NumMessages
}

// imapInboxBody returns the full body of message 1 in the user's INBOX.
func imapInboxBody(t *testing.T, addr, user, pass string) string {
	t.Helper()
	cl := imapLogin(t, addr, user, pass)
	if _, err := cl.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("select INBOX as %s: %v", user, err)
	}
	cmd := cl.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{{}},
	})
	bufs, err := cmd.Collect()
	if err != nil {
		t.Fatalf("fetch message 1 as %s: %v", user, err)
	}
	if len(bufs) != 1 {
		t.Fatalf("fetch returned %d messages, want 1", len(bufs))
	}
	if len(bufs[0].BodySection) != 1 {
		t.Fatalf("fetch returned %d body sections, want 1", len(bufs[0].BodySection))
	}
	return string(bufs[0].BodySection[0].Bytes)
}

type sinkState struct {
	Count    int      `json:"count"`
	Messages []string `json:"messages"`
}

func sinkSnapshot(t *testing.T, ctrlAddr string) sinkState {
	t.Helper()
	resp, err := http.Get("http://" + ctrlAddr + "/smtp")
	if err != nil {
		t.Fatalf("GET /smtp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /smtp status = %d", resp.StatusCode)
	}
	var sn sinkState
	if err := json.NewDecoder(resp.Body).Decode(&sn); err != nil {
		t.Fatalf("decode /smtp: %v", err)
	}
	return sn
}
