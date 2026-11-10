package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/require"
)

type physicalLogCapture struct {
	mu            sync.Mutex
	messagePrefix string
	buf           strings.Builder
}

func (h *physicalLogCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *physicalLogCapture) Handle(_ context.Context, record slog.Record) error {
	if !strings.HasPrefix(record.Message, h.messagePrefix) {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf.WriteString(record.Message)
	record.Attrs(func(attr slog.Attr) bool {
		_, _ = fmt.Fprintf(&h.buf, " %s=%v", attr.Key, attr.Value.Any())
		return true
	})
	h.buf.WriteByte('\n')
	return nil
}

func (h *physicalLogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *physicalLogCapture) WithGroup(string) slog.Handler      { return h }

// captureOnePhysicalSafeRecord captures the physical records whose message
// starts with messagePrefix while invoke runs, requires EXACTLY ONE, requires
// no raw carriage return inside it, and returns the record text (message +
// attributes).
func captureOnePhysicalSafeRecord(t *testing.T, messagePrefix string, invoke func()) string {
	t.Helper()
	capture := &physicalLogCapture{messagePrefix: messagePrefix}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	defer slog.SetDefault(previous)

	invoke()
	capture.mu.Lock()
	got := capture.buf.String()
	capture.mu.Unlock()
	require.Equal(t, 1, strings.Count(got, "\n"), "log output must contain one physical record: %q", got)
	record := strings.TrimSuffix(got, "\n")
	require.NotContains(t, record, "\r", "record contains a raw carriage return: %q", got)
	return record
}

func requireOnePhysicalSafeLogRecord(t *testing.T, messagePrefix string, invoke func()) {
	t.Helper()
	record := captureOnePhysicalSafeRecord(t, messagePrefix, invoke)
	require.Contains(t, record, `\r\n`, "escaped control characters must remain visible: %q", record)
}

func addLogSafetyMailbox(t *testing.T, env *mailRedEnv, agentID string, imapPort, smtpPort int) {
	t.Helper()
	cfg := env.api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID: agentID, Name: "Log safety", Type: config.AgentTypeCustom, Home: env.api.homePath,
	})
	if cfg.Mailboxes == nil {
		cfg.Mailboxes = config.MailboxesConfig{}
	}
	cfg.Mailboxes[agentID] = map[string]config.MailboxConfig{
		mailRedWS: {
			Enabled: true, WorkspaceID: mailRedWS,
			IMAPHost: "127.0.0.1", IMAPPort: imapPort,
			SMTPHost: "127.0.0.1", SMTPPort: smtpPort,
			Username: "mailbox@test.local", PasswordRef: mailboxCredKey(agentID, mailRedWS),
		},
	}
	require.NoError(t, env.api.credStore.Set(mailboxCredKey(agentID, mailRedWS), "s3cret"))
}

func TestMailLogsEscapeForgedRecordsAtRepresentativeSinkInEachFile(t *testing.T) {
	const hostileAgentID = "agent\r\nforged=record"

	t.Run("rest_mail", func(t *testing.T) {
		// Re-derived for the w5 gateway redaction (US-7.4/MC-15, register
		// row 19): mailErr502 now logs the closed safe class ONLY — the raw
		// provider error never enters the record, so the old oracle's
		// vehicle (the raw text present but escaped) no longer exists. The
		// SAME escape-safety property is proven in its new, stronger form:
		// the forged payload and the raw provider text appear NOWHERE in
		// the record (class-only redaction), the record is still exactly
		// one physical record, no raw control character reaches the sink,
		// and the safe class is still named from the closed MC-8 set.
		// Re-introducing the raw error (the pre-redaction shape) fails this
		// subtest again — the mutation the old raw-text oracle could no
		// longer catch.
		record := captureOnePhysicalSafeRecord(t, "rest: mail upstream failure", func() {
			mailErr502(httptest.NewRecorder(), errors.New("remote\r\nforged=record"))
		})
		require.NotContains(t, record, "forged=record",
			"the forged attribute payload must not enter the record at all: %q", record)
		require.NotContains(t, record, "remote",
			"US-7.4 class-only: the raw provider error text must not enter the record: %q", record)
		require.Contains(t, record, "class=",
			"the safe class must still be named: %q", record)
		class := strings.TrimSpace(record[strings.LastIndex(record, "class=")+len("class="):])
		require.NotEmpty(t, class, "the class attribute must carry a value: %q", record)
		if !mailUpstreamClasses[class] {
			t.Fatalf("MC-8: the logged class %q must be from the closed upstream class set: %q", class, record)
		}
	})

	t.Run("rest_mailbox", func(t *testing.T) {
		requireOnePhysicalSafeLogRecord(t, "mailbox: agent not found", func() {
			grantEmailToolAllows(t.TempDir(), hostileAgentID)
		})
	})

	t.Run("rest_mail_send", func(t *testing.T) {
		env := newMailRedEnv(t)
		imapPort := startIMAPNoSent(t)
		sink := startSMTPSink(t)
		addLogSafetyMailbox(t, env, hostileAgentID, imapPort, portOfAddr(t, sink.addr))
		req := httptest.NewRequest(http.MethodPost, "/mail-send", strings.NewReader(
			`{"to":["human@example.test"],"subject":"log safety","body_markdown":"body"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		requireOnePhysicalSafeLogRecord(t, "rest: mail sent copy APPEND failed", func() {
			env.api.handleMailSendInner(rec, req, mailRedWS, hostileAgentID)
		})
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	})

	t.Run("rest_mail_draft", func(t *testing.T) {
		env := newMailRedEnv(t)
		imapPort, client := startFaultIMAP(t)
		smtpPort, _ := listenCount(t)
		addLogSafetyMailbox(t, env, hostileAgentID, imapPort, smtpPort)
		appendRaw(t, client, "Drafts", []byte(auditDraftRaw), []imap.Flag{imap.FlagDraft})
		uv := draftUIDValidity(t, client)
		req := httptest.NewRequest(http.MethodPut, "/mail-draft", strings.NewReader(draftUpdateRequest(uv, 1)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		requireOnePhysicalSafeLogRecord(t, "rest: old draft copy delete failed", func() {
			env.api.handleMailDraftUpdate(rec, req, mailRedWS, hostileAgentID,
				fmt.Sprintf("uid:%d:1", uv))
		})
		require.Less(t, rec.Code, 300, "body: %s", rec.Body.String())
	})

	t.Run("rest_mail_summary", func(t *testing.T) {
		env := newMailRedEnv(t)
		addLogSafetyMailbox(t, env, hostileAgentID, 1, 1)
		// The watcher transport rides the shared pool the production way
		// (rest_mailbox.go::buildMailboxes — wiring site 3): the send/draft
		// subtests above wired the process-wide session manager, so a bare
		// client here is the typed wiring error by design (FR-W1-2). The
		// pool's credential resolver reads the process runtime holder, so
		// the env replays production's boot step first, and the transport
		// is built through the shared pool under THIS pair's identity
		// scope.
		bootTestMailRuntime(t, env)
		watcher, err := email.NewWatcher(email.WatcherConfig{
			AgentID: hostileAgentID, WorkspaceID: mailRedWS,
			Transport: mailWatcherTransportForPair(t, env, hostileAgentID, mailRedWS), StateDir: env.api.homePath,
		})
		require.NoError(t, err)
		require.NoError(t, watcher.Cycle(context.Background()))
		entries, err := os.ReadDir(filepath.Join(env.api.homePath, "email-watch"))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		statePath := filepath.Join(env.api.homePath, "email-watch", entries[0].Name())
		require.NoError(t, os.WriteFile(statePath, []byte("{corrupt"), 0o600))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/mail-summary", nil)

		requireOnePhysicalSafeLogRecord(t, "rest: mail summary state load failed", func() {
			env.api.handleMailSummary(rec, req, mailRedWS)
		})
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	})
}
