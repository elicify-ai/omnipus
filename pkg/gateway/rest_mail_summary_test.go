package gateway

// T31 - TestMailSummaryEndpoint (spec section 7 row 31, B-18, MC-23).
// Oracle: contracts MailboxNewMailSummary - watcher_state is ok | error |
// backoff; "ok" means the last cycle succeeded (MAJ-019 "ok never lies about
// blindness": a mailbox never checked renders the never-checked shape, never
// ok). The backoff/error split is the contract's: error = last cycle failed
// with no deferred next attempt (next_attempt_at null); backoff = failing
// repeatedly, next_attempt_at carries the next try.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

func summaryPath() string {
	return "/api/v1/workspaces/" + mailRedWS + "/mail/summary"
}

func decodeSummary(t *testing.T, rec *httptest.ResponseRecorder) gen.MailSummaryList {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, "summary endpoint: %s", rec.Body.String())
	var out gen.MailSummaryList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "summary body: %s", rec.Body.String())
	return out
}

// runWatcherCycle stages a real, successful watcher cycle for the env's
// (mia, ws-mail) mailbox and returns the path of the state file it wrote.
// The transport is supplied by the caller so the cycle runs against the SAME
// fixture the subtest seeded.
func runWatcherCycle(t *testing.T, env *mailRedEnv, transport email.Transport) string {
	t.Helper()
	w, err := email.NewWatcher(email.WatcherConfig{
		AgentID:     mailRedAgent,
		WorkspaceID: mailRedWS,
		Transport:   transport,
		StateDir:    env.api.homePath,
	})
	require.NoError(t, err)
	require.NoError(t, w.Cycle(context.Background()))
	entries, err := os.ReadDir(filepath.Join(env.api.homePath, "email-watch"))
	require.NoError(t, err, "email-watch dir after a cycle")
	var stateFile string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			stateFile = filepath.Join(env.api.homePath, "email-watch", e.Name())
		}
	}
	require.NotEmpty(t, stateFile, "no watcher state file written by the cycle")
	return stateFile
}

func TestMailSummaryEndpoint(t *testing.T) {
	t.Run("never checked renders the honest never-checked shape, never ok", func(t *testing.T) {
		env := newMailRedEnv(t)
		requireMailLive(t, env.mux, http.MethodGet, summaryPath(), "MC-23 / spec section 2.3")
		// Instrument: no state file exists - the row cannot be read from one.
		entries, err := os.ReadDir(filepath.Join(env.api.homePath, "email-watch"))
		if err == nil && len(entries) > 0 {
			t.Fatalf("instrument: state file(s) exist %v - not a never-checked mailbox", entries)
		}
		out := decodeSummary(t, mailDo(env.mux, http.MethodGet, summaryPath(), nextMailIP(), true, ""))
		require.Len(t, out.Items, 1, "one enabled mailbox pair, one row")
		row := out.Items[0]
		require.Equal(t, mailRedAgent, row.AgentId)
		if string(row.WatcherState) == "ok" {
			t.Fatalf("MC-23/MAJ-019: no state file ever -> watcher_state = %q, but ok claims a cycle succeeded (ok never lies about blindness)", row.WatcherState)
		}
		if row.LastSuccessAt != nil {
			t.Fatalf("last_success_at = %v, want null - no cycle ever succeeded", row.LastSuccessAt)
		}
		if row.UnseenTotal != 0 {
			t.Fatalf("unseen_total = %d, want 0 for a mailbox never checked", row.UnseenTotal)
		}
	})

	t.Run("genuine success renders ok with the live counts", func(t *testing.T) {
		env := newMailRedEnv(t)
		imapPort, cl := startPlainIMAP(t)
		smtpPort, _ := listenCount(t)
		pointMailboxAt(t, env, imapPort, smtpPort)
		appendRaw(t, cl, "INBOX", []byte("From: a@b.test\r\nTo: mailbox@test.local\r\nSubject: s1\r\nMessage-ID: <s1@b.test>\r\n\r\none\r\n"), nil)
		appendRaw(t, cl, "INBOX", []byte("From: a@b.test\r\nTo: mailbox@test.local\r\nSubject: s2\r\nMessage-ID: <s2@b.test>\r\n\r\ntwo\r\n"), nil)
		cl2, err := email.NewClient(email.Account{
			IMAPHost: "127.0.0.1",
			IMAPPort: imapPort,
			SMTPHost: "127.0.0.1",
			SMTPPort: smtpPort,
			Username: "mailbox@test.local",
			Password: "s3cret",
		})
		require.NoError(t, err)
		_ = runWatcherCycle(t, env, cl2)
		out := decodeSummary(t, mailDo(env.mux, http.MethodGet, summaryPath(), nextMailIP(), true, ""))
		require.Len(t, out.Items, 1)
		row := out.Items[0]
		if string(row.WatcherState) != "ok" {
			t.Fatalf("after a successful cycle: watcher_state = %q, want ok", row.WatcherState)
		}
		if row.UnseenTotal != 2 {
			t.Fatalf("unseen_total = %d, want 2 (two appended, unseen messages)", row.UnseenTotal)
		}
		if row.LastSuccessAt == nil {
			t.Fatal("last_success_at null after a successful cycle, want a timestamp")
		}
		if row.LastSeenUid == nil || *row.LastSeenUid != 2 {
			got := "<nil>"
			if row.LastSeenUid != nil {
				got = fmt.Sprintf("%d", *row.LastSeenUid)
			}
			t.Fatalf("last_seen_uid = %s, want 2 (two messages -> uidnext-1 = 2)", got)
		}
	})

	t.Run("corrupt state file must not silently render ok", func(t *testing.T) {
		env := newMailRedEnv(t)
		stateFile := runWatcherCycle(t, env, watcherTransport(t))
		require.NoError(t, os.WriteFile(stateFile, []byte("{corrupt watcher state"), 0o600))
		out := decodeSummary(t, mailDo(env.mux, http.MethodGet, summaryPath(), nextMailIP(), true, ""))
		require.Len(t, out.Items, 1, "MC-23: a corrupt state file drops the row entirely - the mailbox vanishes from the panel")
		row := out.Items[0]
		if string(row.WatcherState) == "ok" {
			t.Fatalf("MC-23: corrupt state file -> watcher_state = %q - unreadable state must never render as ok", row.WatcherState)
		}
		require.NotNil(t, row.LastErrorClass, "last_error_class must name the failure")
		if *row.LastErrorClass == "" {
			t.Fatal("last_error_class empty for a corrupt state file")
		}
	})

	t.Run("error with a deferred next attempt renders backoff", func(t *testing.T) {
		env := newMailRedEnv(t)
		stateFile := runWatcherCycle(t, env, watcherTransport(t))
		future := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
		st := email.WatcherState{
			AgentID:        mailRedAgent,
			WorkspaceID:    mailRedWS,
			State:          "error",
			LastErrorClass: "timeout",
			NextAttemptAt:  future,
			Attempt:        1,
		}
		b, err := json.Marshal(st)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(stateFile, b, 0o600))
		out := decodeSummary(t, mailDo(env.mux, http.MethodGet, summaryPath(), nextMailIP(), true, ""))
		require.Len(t, out.Items, 1)
		row := out.Items[0]
		if string(row.WatcherState) != "backoff" {
			t.Fatalf("error state with next_attempt_at %s -> watcher_state = %q, want backoff (failing repeatedly, next try deferred)", future, row.WatcherState)
		}
		require.NotNil(t, row.NextAttemptAt, "next_attempt_at must carry the next try in the backoff shape")
	})

	t.Run("error without a deferred attempt renders error", func(t *testing.T) {
		env := newMailRedEnv(t)
		stateFile := runWatcherCycle(t, env, watcherTransport(t))
		st := email.WatcherState{
			AgentID:        mailRedAgent,
			WorkspaceID:    mailRedWS,
			State:          "error",
			LastErrorClass: "dns",
			Attempt:        1,
		}
		b, err := json.Marshal(st)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(stateFile, b, 0o600))
		out := decodeSummary(t, mailDo(env.mux, http.MethodGet, summaryPath(), nextMailIP(), true, ""))
		require.Len(t, out.Items, 1)
		row := out.Items[0]
		if string(row.WatcherState) != "error" {
			t.Fatalf("error state with no next_attempt_at -> watcher_state = %q, want error", row.WatcherState)
		}
		if row.NextAttemptAt != nil {
			t.Fatalf("next_attempt_at = %v, want null when no next attempt is deferred", row.NextAttemptAt)
		}
		require.NotNil(t, row.LastErrorClass)
		if *row.LastErrorClass != "dns" {
			t.Fatalf("last_error_class = %q, want dns", *row.LastErrorClass)
		}
	})
}

// watcherTransport dials a fresh in-memory IMAP fixture and returns a
// transport for a watcher cycle whose state file the subtest rewrites.
func watcherTransport(t *testing.T) email.Transport {
	t.Helper()
	imapPort, _ := startPlainIMAP(t)
	smtpPort, _ := listenCount(t)
	cl, err := email.NewClient(email.Account{
		IMAPHost: "127.0.0.1",
		IMAPPort: imapPort,
		SMTPHost: "127.0.0.1",
		SMTPPort: smtpPort,
		Username: "mailbox@test.local",
		Password: "s3cret",
	})
	require.NoError(t, err)
	return cl
}
