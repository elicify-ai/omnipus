package email

// RED (combined feature-gate review, item 1, code-reviewer — CRITICAL).
// Oracle: pkg/email/append.go::AppendMessage calls cmd.Write/cmd.Close/
// cmd.Wait directly with no context-cancellation wrapper and no socket
// deadline — unlike dialSMTPRaw (pkg/email/transport.go, explicit
// conn.SetDeadline) and unlike every OTHER IMAP command in this package,
// which runs through runIMAP's goroutine + ctx-bounded select
// (pkg/email/transport.go::runIMAP, lines ~326-347). A stalling/malicious
// IMAP server that accepts the APPEND command but never completes it can
// therefore block AppendMessage forever, leaking the goroutine and the TCP
// connection on every Sent-folder save or draft create/update.
//
// This proves the gap without risking a genuine indefinite hang: the
// fixture server accepts and fully drains the APPEND literal off the wire —
// exactly what a real stalling server does before it stalls — but never
// writes the tagged response, and the caller supplies a short context
// deadline. A correctly wired AppendMessage would return promptly with a
// context.DeadlineExceeded-flavored error; the current, unwired
// AppendMessage blocks past the deadline instead, so the test enforces its
// own hard wall-clock limit (well above the context deadline, well below
// anything that would stall the suite) and fails loudly rather than
// hanging.

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/require"
)

const (
	stallAppendUser = "stallappend@test.local"
	stallAppendPass = "s3cret"
)

// stallAppendSession answers every command normally EXCEPT Append: it drains
// the literal off the wire (a real stalling server still reads the bytes
// before it stalls) and then blocks forever — no tagged response is ever
// written for that command, simulating a server that accepted the APPEND
// but never completes it.
type stallAppendSession struct{ imapserver.Session }

func (s *stallAppendSession) Append(_ string, r imap.LiteralReader, _ *imap.AppendOptions) (*imap.AppendData, error) {
	buf := make([]byte, 4096)
	for {
		if _, err := r.Read(buf); err != nil {
			break
		}
	}
	select {}
}

// startStallAppendIMAP boots the memserver stack behind the stalling Append
// session and returns a production *Client wired to it (imapDial swapped to
// plaintext, matching every other in-package fixture).
func startStallAppendIMAP(t *testing.T) *Client {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser(stallAppendUser, stallAppendPass)
	// dialIMAP always SELECTs INBOX after login (transport.go::dialIMAP) —
	// it must exist even though this fixture only appends to Drafts.
	require.NoError(t, user.Create("INBOX", nil))
	require.NoError(t, user.Create("Drafts", nil))
	mem.AddUser(user)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &stallAppendSession{mem.NewSession()}, nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })

	prev := imapDial
	imapDial = func(_ context.Context, addr string, _ *tls.Config) (*imapclient.Client, error) {
		return imapclient.DialInsecure(addr, nil)
	}
	t.Cleanup(func() { imapDial = prev })

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	cl, err := NewClient(Account{
		IMAPHost: host, IMAPPort: port, SMTPHost: host,
		Username: stallAppendUser, Password: stallAppendPass,
	})
	require.NoError(t, err)
	return cl
}

func TestAppendMessage_ReturnsPromptlyOnContextDeadlineAgainstStallingServer(t *testing.T) {
	cl := startStallAppendIMAP(t)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	raw := []byte("From: a@x.test\r\nTo: b@x.test\r\nSubject: s\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nbody\r\n")

	done := make(chan error, 1)
	go func() {
		_, _, err := cl.AppendMessage(ctx, "Drafts", []string{"\\Draft"}, raw)
		done <- err
	}()

	// Hard wall-clock guard: far above the 500ms context deadline (so a
	// correctly wired call has ample margin to return) but bounded, so a
	// genuine hang fails the test instead of stalling the suite/CI.
	select {
	case err := <-done:
		require.Error(t, err, "AppendMessage against a stalling server must return an error, not succeed")
		require.True(t, errors.Is(err, context.DeadlineExceeded),
			"AppendMessage error = %v, want a context.DeadlineExceeded-flavored error — "+
				"append.go::AppendMessage does not wrap cmd.Write/cmd.Close/cmd.Wait in any "+
				"context-cancellation or deadline mechanism", err)
	case <-time.After(5 * time.Second):
		t.Fatal("AppendMessage did not return within 5s of a 500ms context deadline against a " +
			"stalling IMAP server — pkg/email/append.go::AppendMessage calls cmd.Write/cmd.Close/" +
			"cmd.Wait with no context-cancellation wrapper and no socket deadline, unlike every " +
			"other IMAP command in this package (runIMAP's goroutine + ctx-bounded select) and " +
			"unlike dialSMTPRaw's conn.SetDeadline — a stalling server hangs the call and leaks " +
			"the goroutine and TCP connection indefinitely")
	}
}
