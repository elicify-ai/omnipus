package email

// Runtime RED bindings use W1 §3.1's indicative Go names. The production
// publisher may rename bindings without changing their semantics. Constructors,
// clock options and diagnostic accessors below are harness binding assumptions,
// NOT additional spec requirements. No stand-in pool/lease is implemented here.
//
// Required semantics: Acquire(ctx, LeaseRequest) (Lease, error), exclusive
// Lease.Client()/Release(), SELECT data and Generation, structural SelectError,
// non-blocking TryAcquire, presence-gated retention, and distinct busy/skip errors.
// Mailbox identity is pair + endpoint/TLS identity + non-secret generation.
// Credentials are resolved at establishment; password text is never an identity.
//
// Oracles were recorded before production reading in runtime-red-oracles.md
// (the dispatch receipt). W1 §4.2/4.4/4.6 defines 2/8 sockets+reservations,
// a 5 s capacity wait, 45 s total read, 30 s dial, and 2 min after completed use.
// Missing bindings produce compiler blockers, NOT behavioral RED evidence.
// GREEN and mutation proof are deferred to a separate CHECK instance.

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/require"
)

type stubMode int

const (
	stubNormal stubMode = iota
	stubStallGreeting
	stubStallFetch
	stubSendBye
	stubGarbage
	stubKillOnAccept
	stubDropStoreReply
)

// The scripted peer is the network edge, not the unit under test. It assigns
// session IDs, tracks actual sockets (including before LOGIN), and serves rows
// from each session's current SELECT state. Its reader observes TCP EOF even
// while a command handler is deliberately stalled; teardown joins both.
// Normal message/flag fixtures reuse startMemIMAP/startViewIMAPRaw directly.
type stubIMAPServer struct {
	mode atomic.Int32
	ln   net.Listener

	mu        sync.Mutex
	accepts   int
	logins    int
	open      int
	openMax   int
	cmdLog    []string
	selected  map[int]string
	conns     map[int]net.Conn
	fetchGate map[int]*poolTestGate
	stores    int // real server-side STORE effects, even if the reply is lost

	closing chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
}

func newStubIMAPServer(t *testing.T) *stubIMAPServer {
	t.Helper()
	return newStubIMAPServerOnAddr(t, "127.0.0.1:0")
}

func newStubIMAPServerOnAddr(t *testing.T, addr string) *stubIMAPServer {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	require.NoError(t, err, "test peer must listen before exercising the pool")
	s := &stubIMAPServer{
		ln: ln, selected: make(map[int]string), conns: make(map[int]net.Conn),
		fetchGate: make(map[int]*poolTestGate), closing: make(chan struct{}),
	}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(func() {
		s.close()
		done := make(chan struct{})
		go func() { s.wg.Wait(); close(done) }()
		expectResult(t, done, 3*time.Second, "all scripted-peer handlers terminate")
	})
	return s
}

func (s *stubIMAPServer) close() {
	s.once.Do(func() {
		close(s.closing)
		_ = s.ln.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, conn := range s.conns {
			_ = conn.Close()
		}
	})
}

func (s *stubIMAPServer) addr() string       { return s.ln.Addr().String() }
func (s *stubIMAPServer) setMode(m stubMode) { s.mode.Store(int32(m)) }

func (s *stubIMAPServer) holdFetchAt(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetchGate[id] = newPoolTestGate()
}

func (s *stubIMAPServer) releaseFetch(id int) {
	s.mu.Lock()
	gate := s.fetchGate[id]
	s.mu.Unlock()
	if gate != nil {
		gate.open()
	}
}

func (s *stubIMAPServer) killSessionByFolder(folder string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, selected := range s.selected {
		if selected == folder && s.conns[id] != nil {
			_ = s.conns[id].Close()
			return id
		}
	}
	return 0
}

func (s *stubIMAPServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.accepts++
		id := s.accepts
		s.open++
		if s.open > s.openMax {
			s.openMax = s.open
		}
		s.conns[id] = conn
		s.mu.Unlock()
		s.wg.Add(1)
		go s.handle(conn, id)
	}
}

func (s *stubIMAPServer) handle(conn net.Conn, id int) {
	defer s.wg.Done()
	lines := make(chan string, 16) // bounded protocol input, not a payload cache
	readerDone, handlerDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(lines)
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-handlerDone:
				return
			case <-s.closing:
				return
			}
		}
	}()
	defer func() {
		_ = conn.Close()
		close(handlerDone)
		<-readerDone // completion, not an intermediate counter
		s.mu.Lock()
		s.open--
		delete(s.conns, id)
		s.mu.Unlock()
	}()
	w := bufio.NewWriter(conn)
	switch stubMode(s.mode.Load()) {
	case stubStallGreeting:
		select {
		case <-readerDone: // client cancellation must close the real socket
		case <-s.closing:
		}
		return
	case stubKillOnAccept:
		fmt.Fprint(w, "* BYE dying\r\n")
		_ = w.Flush()
		return
	}
	fmt.Fprint(w, "* OK stub ready\r\n")
	if err := w.Flush(); err != nil {
		return
	}
	for {
		select {
		case <-s.closing:
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			if !s.answerCommand(w, id, line, readerDone) || w.Flush() != nil {
				return
			}
		}
	}
}

func (s *stubIMAPServer) answerCommand(w *bufio.Writer, id int, line string, readerDone <-chan struct{}) bool {
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 {
		return false
	}
	tag, cmd, arg := parts[0], strings.ToUpper(parts[1]), ""
	if len(parts) == 3 {
		arg = parts[2]
	}
	loggedArg := arg
	if cmd == "SELECT" || cmd == "EXAMINE" {
		loggedArg = strings.Trim(arg, "\"")
	}
	s.mu.Lock()
	s.cmdLog = append(s.cmdLog, fmt.Sprintf("sess=%d %s %s", id, cmd, loggedArg))
	s.mu.Unlock()
	switch cmd {
	case "LOGIN":
		s.mu.Lock()
		s.logins++
		s.mu.Unlock()
		fmt.Fprintf(w, "%s OK [CAPABILITY IMAP4rev1] logged in\r\n", tag)
	case "CAPABILITY":
		fmt.Fprintf(w, "* CAPABILITY IMAP4rev1\r\n%s OK CAPABILITY\r\n", tag)
	case "SELECT", "EXAMINE":
		folder := strings.Trim(arg, "\"")
		if folder != "INBOX" && folder != "Sent" && folder != "Archive" && !strings.HasPrefix(folder, "F") {
			fmt.Fprintf(w, "%s NO [NONEXISTENT] No such folder\r\n", tag)
			break
		}
		s.mu.Lock()
		s.selected[id] = folder
		s.mu.Unlock()
		// These are seeded protocol values, not values copied from the pool.
		fmt.Fprint(w, "* FLAGS (\\Answered \\Flagged \\Deleted \\Seen \\Draft)\r\n* 2 EXISTS\r\n")
		fmt.Fprint(w, "* OK [UIDVALIDITY 4242] UIDs valid\r\n* OK [UIDNEXT 3] Next UID\r\n")
		fmt.Fprintf(w, "%s OK [READ-WRITE] SELECT completed\r\n", tag)
	case "STATUS":
		// Extract the mailbox only, not STATUS's requested item list.
		folder := strings.Trim(strings.SplitN(arg, " (", 2)[0], "\"")
		fmt.Fprintf(w, "* STATUS \"%s\" (MESSAGES 2 UNSEEN 1 UIDNEXT 3 UIDVALIDITY 4242)\r\n%s OK STATUS\r\n", folder, tag)
	case "UID":
		sub := strings.ToUpper(arg)
		switch {
		case strings.HasPrefix(sub, "FETCH"):
			return s.answerFetch(w, tag, id, readerDone)
		case strings.HasPrefix(sub, "STORE"):
			s.mu.Lock()
			s.stores++
			s.mu.Unlock()
			if stubMode(s.mode.Load()) == stubDropStoreReply {
				return false // effect happened, acknowledgment lost; replay is unsafe
			}
			fmt.Fprintf(w, "%s OK STORE\r\n", tag)
		case strings.HasPrefix(sub, "SEARCH"):
			fmt.Fprintf(w, "* SEARCH 1 2\r\n%s OK SEARCH\r\n", tag)
		default:
			fmt.Fprintf(w, "%s BAD unsupported UID command\r\n", tag)
		}
	case "FETCH":
		return s.answerFetch(w, tag, id, readerDone)
	case "LOGOUT":
		fmt.Fprintf(w, "* BYE logout\r\n%s OK LOGOUT\r\n", tag)
		_ = w.Flush()
		return false
	default:
		fmt.Fprintf(w, "%s OK %s\r\n", tag, cmd)
	}
	return true
}

func (s *stubIMAPServer) answerFetch(w *bufio.Writer, tag string, id int, readerDone <-chan struct{}) bool {
	switch stubMode(s.mode.Load()) {
	case stubStallFetch:
		select {
		case <-readerDone:
		case <-s.closing:
		}
		return false
	case stubSendBye:
		fmt.Fprint(w, "* BYE server says goodbye\r\n")
		_ = w.Flush()
		return false
	case stubGarbage:
		fmt.Fprint(w, "@@@not-a-response\r\n")
		_ = w.Flush()
		return false
	}
	s.mu.Lock()
	gate := s.fetchGate[id]
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate.ch:
		case <-readerDone:
			return false
		case <-s.closing:
			return false
		}
	}
	// Read SELECT state AFTER the pause. Sharing a socket lets another
	// borrower's SELECT overwrite it and produces the wrong folder's rows.
	s.mu.Lock()
	folder := s.selected[id]
	s.mu.Unlock()
	for uid := 1; uid <= 2; uid++ {
		body, flags := fmt.Sprintf("%s-row-%d", folder, uid), ""
		if uid == 1 {
			flags = "\\Seen"
		}
		fmt.Fprintf(w, "* %d FETCH (UID %d FLAGS (%s) BODY[] {%d}\r\n%s)\r\n", uid, uid, flags, len(body), body)
	}
	fmt.Fprintf(w, "%s OK FETCH\r\n", tag)
	return true
}

func (s *stubIMAPServer) acceptCount() int     { s.mu.Lock(); defer s.mu.Unlock(); return s.accepts }
func (s *stubIMAPServer) loginCount() int      { s.mu.Lock(); defer s.mu.Unlock(); return s.logins }
func (s *stubIMAPServer) openCount() int       { s.mu.Lock(); defer s.mu.Unlock(); return s.open }
func (s *stubIMAPServer) maxOpenObserved() int { s.mu.Lock(); defer s.mu.Unlock(); return s.openMax }
func (s *stubIMAPServer) storeCount() int      { s.mu.Lock(); defer s.mu.Unlock(); return s.stores }

func (s *stubIMAPServer) logEntries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmdLog...)
}

func (s *stubIMAPServer) liveSessionIDs() map[int]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int]string)
	for id := range s.conns {
		out[id] = s.selected[id]
	}
	return out
}

// Observe transport socket starts/closes independently of the server's EOF
// scheduling, so a transient ninth client socket cannot disappear undetected.
type poolDialProbe struct {
	starts atomic.Int32
	live   atomic.Int32
	peak   atomic.Int32
}

type poolTrackedConn struct {
	net.Conn
	probe *poolDialProbe
	once  sync.Once
}

func (c *poolTrackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.probe.live.Add(-1) })
	return err
}

func swapCountingDial(t *testing.T) *poolDialProbe {
	t.Helper()
	probe, prev := &poolDialProbe{}, imapDial
	imapDial = func(ctx context.Context, addr string, _ *tls.Config) (*imapclient.Client, error) {
		probe.starts.Add(1)
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		n := probe.live.Add(1)
		for old := probe.peak.Load(); n > old; old = probe.peak.Load() {
			if probe.peak.CompareAndSwap(old, n) {
				break
			}
		}
		return imapclient.New(&poolTrackedConn{Conn: conn, probe: probe}, nil), nil
	}
	t.Cleanup(func() { imapDial = prev })
	return probe
}

const testSweepInterval = 20 * time.Millisecond // test cadence; expiry oracle is W1's 2 min

func newSessionsForStub(t *testing.T, stub *stubIMAPServer, clock *poolTestClock) *MailSessions {
	t.Helper()
	return newSessionsForAddr(t, stub.addr(), clock)
}

func newSessionsForAddr(t *testing.T, _ string, clock *poolTestClock) *MailSessions {
	t.Helper()
	swapCountingDial(t)
	sessions := NewMailSessions(SessionsConfig{
		StateDir: t.TempDir(), Now: clock.Now, IdleSweepInterval: testSweepInterval,
		Credentials: func(string) (string, string, error) { return testIMAPUser, testIMAPPass, nil },
	})
	t.Cleanup(sessions.Close)
	return sessions
}

func poolRetainFor(t *testing.T, sessions *MailSessions, workspace string) {
	t.Helper()
	sessions.EnableRetention() // production integration activation, never a poison/test bypass
	sessions.Presence().Bind("authenticated-test-conn", "panel-"+workspace, workspace)
	require.Equal(t, 1, sessions.Presence().Count(workspace), "matching observer enables retention")
}

type poolTestClock struct {
	mu    sync.Mutex
	now   time.Time
	calls uint64
}

func newPoolTestClock() *poolTestClock {
	return &poolTestClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
}

func (c *poolTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.now
}

func (c *poolTestClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *poolTestClock) sweepsAfterAdvance(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	before := c.calls
	c.mu.Unlock()
	waitFor(t, 2*time.Second, "idle sweeper reads the advanced clock", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.calls >= before+2
	})
}

type sessionIdentity struct{ pair, endpoint, generation string }

func (id sessionIdentity) leaseRequest(folder string, retain, mutation bool) LeaseRequest {
	return LeaseRequest{PairKey: id.pair, Endpoint: id.endpoint, Generation: id.generation,
		Folder: folder, Retain: retain, Mutation: mutation}
}

func stubFetchRows(_ *testing.T, l Lease) ([]string, error) {
	fetched, err := l.Client().Fetch(imap.UIDSetNum(1, 2), &imap.FetchOptions{
		UID: true, Flags: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}},
	}).Collect()
	if err != nil {
		return nil, err
	}
	rows := make([]string, 0, len(fetched))
	for _, msg := range fetched {
		for _, section := range msg.BodySection {
			rows = append(rows, string(section.Bytes))
		}
	}
	return rows, nil
}

func stubStoreDeleted(_ *testing.T, l Lease, uid uint32) error {
	return l.Client().Store(imap.UIDSetNum(imap.UID(uid)), &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}, Silent: true,
	}, nil).Close()
}

func stubFetchUIDs(t *testing.T, l Lease) []uint32 {
	t.Helper()
	msgs, err := l.Client().Fetch(imap.UIDSetNum(1, 2), &imap.FetchOptions{UID: true, Flags: true}).Collect()
	require.NoError(t, err)
	uids := make([]uint32, 0, len(msgs))
	for _, msg := range msgs {
		uids = append(uids, uint32(msg.UID))
	}
	return uids
}

func testCtx(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func expectResult[T any](t *testing.T, ch <-chan T, timeout time.Duration, what string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(timeout):
		t.Fatalf("expected %s to complete within %s; actual: no completion", what, timeout)
		var zero T
		return zero
	}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	require.True(t, cond(), "expected %s; actual: condition still false after %s", what, timeout)
}

func poolAwaitCount(t *testing.T, get func() int, want int, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && get() != want {
		time.Sleep(2 * time.Millisecond)
	}
	require.Equal(t, want, get(), what)
}

type poolTestGate struct {
	ch   chan struct{}
	once sync.Once
}

func newPoolTestGate() *poolTestGate { return &poolTestGate{ch: make(chan struct{})} }
func (g *poolTestGate) open()        { g.once.Do(func() { close(g.ch) }) }
func itoa(n int) string              { return strconv.Itoa(n) }

func countStr(entries []string, needle string) int {
	count := 0
	for _, entry := range entries {
		if strings.Contains(entry, needle) {
			count++
		}
	}
	return count
}
