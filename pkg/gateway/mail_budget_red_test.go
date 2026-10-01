package gateway

// RED round 2 — A8 mail-operation budget, REST surface. Oracles: the landed
// wire contract (contracts/openapi.yaml retry param on the four dialing GETs
// + MailUnavailableError, commit ec2d6b995), spec §2.3/A8 (line 1826), MC-33,
// edge 16 / MIN-003, and the A8 safety property: the agent-tool path can
// never set retry=true, so the tool path and the automatic REST poll must be
// refused identically (proven at the budget unit and the tool-schema guard;
// see pkg/email/mail_budget_red_test.go's header).
//
// This file pins the HTTP shape:
//   - an automatic GET (no retry param, or retry=false) against a
//     backing-off mailbox must NOT dial IMAP and must return 503 with
//     MailUnavailableError{code:"backoff", last_error_class, next_attempt_at};
//   - the SAME mailbox with retry=true must dial and serve real folder data
//     (the human Retry click bypasses the backoff gate — and ONLY the
//     backoff gate: the semaphore and singleflight still apply, proven at
//     the budget unit).
//
// The 2-per-account semaphore and singleflight properties are proven at the
// budget unit (pkg/email/mail_budget_red_test.go) — the layer that owns
// those properties; re-proving them over HTTP would add timing surface, not
// coverage. Deliberate gap, restated for CHECK.

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// syncBuf is a mutex-protected protocol log. Any byte in it means a real
// IMAP session existed (the server emits its greeting and command traffic
// through the DebugWriter), so Len()==0 is the no-dial evidence.
type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuf) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Len()
}

// startCountingIMAP serves the three D5 folders over a plaintext loopback mem
// IMAP server with a protocol DebugWriter; returns (port, log). The email
// client dials plaintext on loopback (isLoopbackAddr exception, 89e16221f),
// so no TLS or imapDial override is involved.
func startCountingIMAP(t *testing.T) (int, *syncBuf) {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("mailbox@test.local", "s3cret")
	for _, name := range []string{"INBOX", "Sent", "Drafts"} {
		require.NoError(t, user.Create(name, nil))
	}
	mem.AddUser(user)
	log := &syncBuf{}
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
		DebugWriter:  log,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })
	go func() { _ = srv.Serve(ln) }()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return port, log
}

// mailWatchStatePath mirrors pkg/email/watcher.go::keyFor (unexported — the
// lowercase-alnum pair key; shape pinned by pkg/email/watcher_state_test.go)
// so the test writes the state file exactly where the production watcher and
// the summary endpoint read it (gateway_boot.go wires the watcher set with
// stg.homePath; rest_mail_summary.go reads a.homePath).
func mailWatchStatePath(t *testing.T, home, agentID, wsID string) string {
	t.Helper()
	clean := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return filepath.Join(home, "email-watch", clean(agentID)+"-"+clean(wsID)+".json")
}

// writeBackoffState persists a watcher state file in error/backoff: the last
// cycle failed with auth_failed and the next attempt is in the future.
func writeBackoffState(t *testing.T, home string, nextAt time.Time) {
	t.Helper()
	st := email.WatcherState{
		AgentID:        mailRedAgent,
		WorkspaceID:    mailRedWS,
		State:          "error",
		LastErrorClass: "auth_failed",
		NextAttemptAt:  nextAt.UTC().Format(time.RFC3339),
		Attempt:        1,
	}
	p := mailWatchStatePath(t, home, mailRedAgent, mailRedWS)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	b, err := json.Marshal(st)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, b, 0o600))
}

// the four dialing GETs from the wire contract (retry param carriers).
func dialingGETPaths() []string {
	base := "/api/v1/workspaces/" + mailRedWS + "/mail/" + mailRedAgent
	return []string{
		base + "/folders",
		base + "/folders/inbox/messages",
		base + "/folders/inbox/messages/uid:1:1",
		base + "/folders/inbox/messages/uid:1:1/attachments/0",
	}
}

// TestMailPanelGET_Backoff503_NoRetry — the automatic panel poll (no retry
// param) against a backing-off mailbox must not dial and must 503 with the
// typed body. RED today: the handlers dial directly with no gate, so every
// path returns 2xx/404/502 instead of the contract's 503 backoff refusal.
func TestMailPanelGET_Backoff503_NoRetry(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, proto := startCountingIMAP(t)
	pointMailboxAt(t, env, imapPort, imapPort)
	nextAt := time.Now().Add(10 * time.Minute)
	writeBackoffState(t, env.api.homePath, nextAt)

	for _, p := range dialingGETPaths() {
		t.Run(p, func(t *testing.T) {
			rec := mailDo(env.mux, http.MethodGet, p, nextMailIP(), true, "")
			require.Equal(t, http.StatusServiceUnavailable, rec.Code,
				"automatic GET during backoff must 503 (MailUnavailableError), got body: "+rec.Body.String())
			var mu gen.MailUnavailableError
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &mu),
				"body must decode as MailUnavailableError: "+rec.Body.String())
			require.Equal(t, gen.MailUnavailableErrorCodeBackoff, mu.Code, "code must be backoff")
			require.NotNil(t, mu.LastErrorClass)
			require.Equal(t, gen.MailUnavailableErrorLastErrorClass("auth_failed"), *mu.LastErrorClass)
			require.NotNil(t, mu.NextAttemptAt)
			require.WithinDuration(t, nextAt.UTC(), *mu.NextAttemptAt, time.Second,
				"next_attempt_at must echo the watcher state file's value")
			require.Zero(t, proto.Len(), "no IMAP dial may happen for an automatic GET during backoff")
		})
	}
}

// TestMailPanelGET_RetryTrue_DialsAndServes — the human Retry click
// (retry=true) on the SAME backing-off mailbox dials and serves real data.
// Green-on-arrival guard today (nothing gates anything yet, so retry=true
// trivially dials): its failability activates the moment the backoff gate
// exists — a gate that over-blocks (refuses retry=true too) dies here.
// RED evidence for the feature is TestMailPanelGET_Backoff503_NoRetry above.
func TestMailPanelGET_RetryTrue_DialsAndServes(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, proto := startCountingIMAP(t)
	pointMailboxAt(t, env, imapPort, imapPort)
	writeBackoffState(t, env.api.homePath, time.Now().Add(10*time.Minute))

	rec := mailDo(env.mux, http.MethodGet, mailFoldersPath()+"?retry=true", nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, rec.Code, "retry=true must bypass the backoff gate; body: "+rec.Body.String())
	var fl gen.MailFolderList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &fl))
	require.Len(t, fl.Folders, 3, "exactly the three D5 folders")
	got := map[string]bool{}
	for _, f := range fl.Folders {
		got[string(f.Slug)] = true
	}
	require.True(t, got["inbox"] && got["sent"] && got["drafts"], "folder slugs = %v", got)
	require.Positive(t, proto.Len(), "retry=true must actually dial IMAP")
}
