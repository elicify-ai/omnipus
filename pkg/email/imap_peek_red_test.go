package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// Oracles: MC-25, MC-36, FR-039, D38, scenarios B-12, B-53, B-54.
// The in-memory server is the real go-imap protocol peer (startMemIMAP's
// module). The client under test is *Client. Command text is the server's
// DebugWriter transcript, not a mock of Client.

const agentReadKeyword = "$OmnipusAgentRead"

func TestFetchCommands_PeekOnly(t *testing.T) {
	// MC-25 / B-12: list and read fetches must not issue a non-peek BODY[].
	cap := startCaptureIMAP(t, [][]byte{mkMsg("Hello", "ada@box.test", "body")}, nil, false)
	if _, err := cap.cl.ReadInbox(context.Background(), InboxOptions{Limit: 10}); err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if _, err := cap.cl.ReadMessage(context.Background(), 1); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var bad []string
	for _, line := range clientCommands(cap.log.String()) {
		if !strings.Contains(strings.ToUpper(line), " FETCH ") {
			continue
		}
		if strings.Contains(line, "BODY[") && !strings.Contains(line, "BODY.PEEK[") {
			bad = append(bad, line)
		}
	}
	if len(bad) != 0 {
		t.Fatalf("MC-25: non-peek BODY[] FETCH commands:\n%s", strings.Join(bad, "\n"))
	}
}

func TestAgentRead_SeenAndKeywordOneStore(t *testing.T) {
	// MC-36 / B-53: read_message's transport fetch is BODY.PEEK and exactly one
	// STORE adds \Seen and $OmnipusAgentRead together.
	cap := startCaptureIMAP(t, [][]byte{mkMsg("Hello", "ada@box.test", "body")}, nil, false)
	if _, err := cap.cl.ReadMessage(context.Background(), 1); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	stores := storeCommands(cap.log.String())
	if len(stores) != 1 {
		t.Fatalf("MC-36: client STORE commands = %d, want exactly 1 adding \\Seen and %s\ncommands:\n%s",
			len(stores), agentReadKeyword, strings.Join(clientCommands(cap.log.String()), "\n"))
	}
	line := strings.ToLower(stores[0])
	if !strings.Contains(line, "seen") || !strings.Contains(line, "omnipusagentread") {
		t.Fatalf("MC-36: STORE = %q, want both \\Seen and %s", stores[0], agentReadKeyword)
	}
	flags := inboxFlags(t, cap.addr, 1)
	if !flagPresent(flags, imap.FlagSeen) || !flagPresent(flags, imap.Flag(agentReadKeyword)) {
		t.Fatalf("MC-36: flags after read = %v, want \\Seen and %s (case-insensitive)", flags, agentReadKeyword)
	}
}

func TestAgentRead_KeywordRejectedFallback(t *testing.T) {
	// MC-36 / B-54: a server that rejects the keyword still gets exactly one
	// follow-up STORE of \Seen only. The read succeeds and the keyword is absent.
	cap := startCaptureIMAP(t, [][]byte{mkMsg("Hello", "ada@box.test", "body")}, nil, true)
	if _, err := cap.cl.ReadMessage(context.Background(), 1); err != nil {
		t.Fatalf("MC-36: keyword rejection must not fail the read: %v", err)
	}
	stores := storeCommands(cap.log.String())
	if len(stores) != 2 {
		t.Fatalf("MC-36: STORE commands = %d, want 2 (keyword attempt, then \\Seen-only)\n%s",
			len(stores), strings.Join(stores, "\n"))
	}
	if !strings.Contains(strings.ToLower(stores[0]), "omnipusagentread") {
		t.Fatalf("MC-36: first STORE = %q, want the keyword attempt", stores[0])
	}
	second := strings.ToLower(stores[1])
	if !strings.Contains(second, "seen") || strings.Contains(second, "omnipusagentread") {
		t.Fatalf("MC-36: fallback STORE = %q, want \\Seen only", stores[1])
	}
	flags := inboxFlags(t, cap.addr, 1)
	if !flagPresent(flags, imap.FlagSeen) {
		t.Fatalf("MC-36: flags after fallback = %v, want \\Seen", flags)
	}
	if flagPresent(flags, imap.Flag(agentReadKeyword)) {
		t.Fatalf("MC-36: keyword was stored on a rejecting server: %v", flags)
	}
}

type captureIMAP struct {
	cl   *Client
	addr string
	log  *lockedBuf
}

func startCaptureIMAP(t *testing.T, msgs [][]byte, seenUIDs map[int]bool, rejectKeyword bool) captureIMAP {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser(testIMAPUser, testIMAPPass)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create INBOX: %v", err)
	}
	mem.AddUser(user)
	log := &lockedBuf{}
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			if rejectKeyword {
				return &kwRejectSession{user: user}, nil, nil
			}
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
		DebugWriter:  log,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })

	appendRaw(t, ln.Addr().String(), msgs, seenUIDs)

	prev := imapDial
	imapDial = func(addr string, _ *tls.Config) (*imapclient.Client, error) {
		return imapclient.DialInsecure(addr, nil)
	}
	t.Cleanup(func() { imapDial = prev })

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	cl, err := NewClient(Account{
		IMAPHost: host,
		IMAPPort: port,
		SMTPHost: host,
		Username: testIMAPUser,
		Password: testIMAPPass,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return captureIMAP{cl: cl, addr: ln.Addr().String(), log: log}
}

func appendRaw(t *testing.T, addr string, msgs [][]byte, seenUIDs map[int]bool) {
	t.Helper()
	cl, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatalf("dial (append): %v", err)
	}
	defer cl.Close()
	if err = cl.Login(testIMAPUser, testIMAPPass).Wait(); err != nil {
		t.Fatalf("login (append): %v", err)
	}
	for i, raw := range msgs {
		var opts *imap.AppendOptions
		if seenUIDs[i+1] {
			opts = &imap.AppendOptions{Flags: []imap.Flag{imap.FlagSeen}}
		}
		cmd := cl.Append("INBOX", int64(len(raw)), opts)
		if _, err = cmd.Write(raw); err != nil {
			t.Fatalf("append write %d: %v", i, err)
		}
		if err = cmd.Close(); err != nil {
			t.Fatalf("append close %d: %v", i, err)
		}
		if _, err = cmd.Wait(); err != nil {
			t.Fatalf("append wait %d: %v", i, err)
		}
	}
}

func inboxFlags(t *testing.T, addr string, uid uint32) []imap.Flag {
	t.Helper()
	cl, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatalf("dial (flags): %v", err)
	}
	defer cl.Close()
	if err = cl.Login(testIMAPUser, testIMAPPass).Wait(); err != nil {
		t.Fatalf("login (flags): %v", err)
	}
	if _, err = cl.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("select: %v", err)
	}
	msgs, err := cl.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{Flags: true, UID: true}).Collect()
	if err != nil {
		t.Fatalf("fetch flags: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("fetch flags: %d messages, want 1", len(msgs))
	}
	return msgs[0].Flags
}

func clientCommands(log string) []string {
	var cmds []string
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "+") {
			continue
		}
		cmds = append(cmds, line)
	}
	return cmds
}

func storeCommands(log string) []string {
	var stores []string
	for _, line := range clientCommands(log) {
		if strings.Contains(strings.ToUpper(line), " STORE ") {
			stores = append(stores, line)
		}
	}
	return stores
}

func flagPresent(flags []imap.Flag, want imap.Flag) bool {
	for _, f := range flags {
		if strings.EqualFold(string(f), string(want)) {
			return true
		}
	}
	return false
}

// kwRejectSession is a real imapmemserver session that answers NO to a STORE
// containing the agent-read keyword. It is the peer, not a mock of Client.
type kwRejectSession struct {
	*imapmemserver.UserSession
	user *imapmemserver.User
}

func (s *kwRejectSession) Login(username, password string) error {
	if err := s.user.Login(username, password); err != nil {
		return err
	}
	s.UserSession = imapmemserver.NewUserSession(s.user)
	return nil
}

func (s *kwRejectSession) Close() error {
	if s.UserSession == nil {
		return nil
	}
	return s.UserSession.Close()
}

func (s *kwRejectSession) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	if flags != nil {
		for _, f := range flags.Flags {
			if strings.Contains(strings.ToLower(string(f)), "omnipusagentread") {
				return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "keywords disabled"}
			}
		}
	}
	return s.UserSession.Store(w, numSet, flags, options)
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
