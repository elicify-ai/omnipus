package email

// RED (round 8, silent-failure-hunter F5 — LOW-MED). Oracle: FR-018/FR-036
// (failures never silent in the logs) and ReadFolderPage's own comment
// promise ("the page never silently drops rows"). The finding:
// pkg/email/transport.go::fetchMessages drops a fetched buffer with a nil
// Envelope via a bare `continue` — no log, no truncation flag — shrinking the
// page and desyncing TotalMatches from len(Messages) invisibly. The row drop
// itself stays (a row cannot be rendered without its envelope); the fix is
// the LOG: one slog.Warn per dropped row naming the condition.
//
// Fixture: a session wrapper that answers the FETCH with UID+FLAGS only —
// every row envelope-less — against the in-tree imapmemserver stack. The
// production entry driven is ReadInbox (panel list + read_inbox tool path).

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/require"
)

const (
	envDropUser = "envdrop@test.local"
	envDropPass = "s3cret"
)

// envDropSession answers FETCH with UID+FLAGS only — the client's buffers all
// carry a nil Envelope. Everything else delegates to the wrapped session.
type envDropSession struct{ imapserver.Session }

func (s *envDropSession) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	if options == nil || !options.Envelope {
		return s.Session.Fetch(w, numSet, options)
	}
	m1 := w.CreateMessage(1)
	m1.WriteUID(1)
	m1.WriteFlags(nil)
	if err := m1.Close(); err != nil {
		return err
	}
	m2 := w.CreateMessage(2)
	m2.WriteUID(2)
	m2.WriteFlags(nil)
	return m2.Close()
}

// startEnvDropIMAP boots the memserver stack behind the envDrop session and
// returns a production *Client wired to it (imapDial swapped to plaintext).
func startEnvDropIMAP(t *testing.T) *Client {
	t.Helper()
	// Source-less client by design; declare the legacy-dial world (order-
	// independence fixture) so the env-drop reads ride the dial regardless of
	// whether an earlier test wired the process-wide manager.
	pinLegacyDialWorld(t)
	mem := imapmemserver.New()
	user := imapmemserver.NewUser(envDropUser, envDropPass)
	require.NoError(t, user.Create("INBOX", nil))
	mem.AddUser(user)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &envDropSession{mem.NewSession()}, nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })

	acl, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	require.NoError(t, err)
	require.NoError(t, acl.Login(envDropUser, envDropPass).Wait())
	raw := []byte(buildMIME(map[string]string{"Content-Type": "text/plain; charset=utf-8"}, "envdrop body"))
	for range 2 {
		cmd := acl.Append("INBOX", int64(len(raw)), nil)
		_, werr := cmd.Write(raw)
		require.NoError(t, werr)
		require.NoError(t, cmd.Close())
		_, aerr := cmd.Wait()
		require.NoError(t, aerr)
	}
	require.NoError(t, acl.Close())

	prev := imapDial
	imapDial = func(_ context.Context, addr string, _ *tls.Config) (*imapclient.Client, error) {
		return imapclient.DialInsecure(addr, nil)
	}
	t.Cleanup(func() { imapDial = prev })

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	cl, err := NewClient(Account{IMAPHost: host, IMAPPort: port, SMTPHost: host, Username: envDropUser, Password: envDropPass})
	require.NoError(t, err)
	return cl
}

func TestFetchMessages_EnvelopeLessRowIsLoggedNotSilentlyDropped(t *testing.T) {
	var buf strings.Builder
	captureLogs(t, &buf)

	cl := startEnvDropIMAP(t)
	msgs, err := cl.ReadInbox(context.Background(), InboxOptions{Limit: 25})

	// The page mechanism itself must not hard-fail: the finding is the
	// invisible drop, not a crash. (Also proves the FETCH exchange ran.)
	require.NoError(t, err, "the read itself must not fail — the finding is the silent drop")
	require.Empty(t, msgs, "envelope-less buffers cannot render rows; the drop stays")

	require.Contains(t, buf.String(), "level=WARN",
		"FR-018/FR-036: dropping an envelope-less fetched row must log a WARN")
	require.Contains(t, strings.ToLower(buf.String()), "envelope",
		"FR-018/FR-036: the WARN must name the envelope-less condition")
}

func TestFetchMessages_Control_NormalMailboxReturnsRowsWithoutEnvelopeWarnings(t *testing.T) {
	var buf strings.Builder
	captureLogs(t, &buf)

	cl := startMemIMAP(t, [][]byte{
		mkMsg("Control one", "a@x.test", "body one"),
		mkMsg("Control two", "b@x.test", "body two"),
	}, nil)
	msgs, err := cl.ReadInbox(context.Background(), InboxOptions{Limit: 25})

	// Positive control: with envelopes present the page is served and the
	// instrument must stay quiet — this is the assertion that dies if the fix
	// logs unconditionally, and it proves the harness sees rows at all.
	require.NoError(t, err)
	require.Len(t, msgs, 2, "the control mailbox must serve both rows (instrument check)")
	require.NotContains(t, strings.ToLower(buf.String()), "envelope",
		"a normal mailbox must not produce envelope-drop warnings (the fix must log only the fault)")
}
