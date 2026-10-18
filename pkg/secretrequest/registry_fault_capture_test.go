package secretrequest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// These captures are real process-edge sinks, not mocked registry decisions.
// Redaction is deliberately off for the audit sink: it must not conceal a leak.
// No submitted sentinel is registered with the application log masker.
type registryAuditCall struct {
	Record secretrequest.Record
	Event  string
}

type registryFaultCapture struct {
	f             *matrixEnv
	audit         *audit.Logger
	auditPath     string
	logPath       string
	auditCalls    []registryAuditCall
	notifications []secretrequest.Record
}

func newRegistryFaultCapture(t *testing.T) *registryFaultCapture {
	t.Helper()
	f := newMatrixEnv(t)
	vault := filepath.Join(f.home, "vault")
	require.NoError(t, os.MkdirAll(vault, 0o700))
	store := credentials.NewStore(filepath.Join(vault, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x44}, 32)))
	t.Cleanup(store.Close)
	f.store = store
	c := &registryFaultCapture{f: f, logPath: filepath.Join(f.home, "registry-diagnostics.jsonl")}
	previousLevel, previousSlog := logger.GetLevel(), slog.Default()
	restoreConsole := logger.DisableConsole()
	require.NoError(t, logger.EnableFileLogging(c.logPath))
	logger.SetLevel(logger.DEBUG)
	slog.SetDefault(slog.New(logger.NewSlogHandler()))
	t.Cleanup(func() {
		slog.SetDefault(previousSlog)
		logger.DisableFileLogging()
		logger.SetLevel(previousLevel)
		restoreConsole()
	})
	auditDir := filepath.Join(f.home, "audit")
	var err error
	c.audit, err = audit.NewLogger(audit.LoggerConfig{
		Dir: auditDir, AuditLogRequested: true, HMACKey: bytes.Repeat([]byte{0x79}, 32),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.audit.Close()) })
	c.auditPath = filepath.Join(auditDir, "audit.jsonl")
	f.reg = secretrequest.NewRegistry(c.options(f.sessions, f.store))
	f.reg.SetSink(c.notify)
	// Verify both public log front-ends, not just a nonempty buffer.
	logger.InfoCF("registry-test", "native diagnostic capture control", map[string]any{"capture_control": "native"})
	slog.Info("structured diagnostic capture control", "capture_control", "structured")
	controls := c.logs(t)
	require.Equal(t, 1, countRegistryField(controls, "capture_control", "native"))
	require.Equal(t, 1, countRegistryField(controls, "capture_control", "structured"))
	return c
}

func (c *registryFaultCapture) options(sessions *session.UnifiedStore, store *credentials.Store) secretrequest.Options {
	opts := c.f.options()
	opts.Sessions = sessions
	opts.Credentials = func() (*credentials.Store, error) { return store, nil }
	opts.RecoveryCredentials = opts.Credentials
	opts.Audit = func(rec secretrequest.Record, event string) {
		c.auditCalls = append(c.auditCalls, registryAuditCall{Record: rec, Event: event})
		// Same public audit adapter as AgentLoop.SecretRequests, with no values.
		audit.Emit(context.Background(), c.audit, "secret.request."+event, audit.SeverityInfo, map[string]any{
			"request_id": rec.Card.RequestId, "session_id": rec.Card.SessionId,
			"agent_id": rec.Card.ActingAgentId, "state": rec.Card.State, "reason_code": rec.ReasonCode,
		})
	}
	return opts
}

func (c *registryFaultCapture) notify(rec secretrequest.Record) {
	c.notifications = append(c.notifications, rec)
}

// Reopen BOTH durable stores, so a cached Record or credential object cannot
// manufacture successful restart recovery. Production files are never copied.
func (c *registryFaultCapture) fresh(t *testing.T) *secretrequest.Registry {
	t.Helper()
	sessions, err := session.NewUnifiedStore(c.f.sessions.BaseDir())
	require.NoError(t, err)
	sessions.SetStatsFlushInterval(100 * 365 * 24 * time.Hour)
	t.Cleanup(func() { require.NoError(t, sessions.Close()) })
	store := credentials.NewStore(c.f.store.Path())
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x44}, 32)))
	t.Cleanup(store.Close)
	reg := secretrequest.NewRegistry(c.options(sessions, store))
	reg.SetSink(c.notify)
	return reg
}

func readRegistryJSONLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var entries []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal(line, &entry), "capture must contain actual structured JSON records")
		entries = append(entries, entry)
	}
	return entries
}

func (c *registryFaultCapture) logs(t *testing.T) []map[string]any {
	t.Helper()
	return readRegistryJSONLines(t, c.logPath)
}

func countRegistryField(entries []map[string]any, field string, want any) int {
	count := 0
	for _, entry := range entries {
		if entry[field] == want {
			count++
		}
	}
	return count
}

func (c *registryFaultCapture) auditTransitions(t *testing.T, id string, events ...string) []map[string]any {
	t.Helper()
	var matches []map[string]any
	for _, entry := range readRegistryJSONLines(t, c.auditPath) {
		fields, ok := entry["fields"].(map[string]any)
		if !ok || fields["request_id"] != id {
			continue
		}
		for _, event := range events {
			if entry["event"] == "secret.request."+event {
				matches = append(matches, entry)
			}
		}
	}
	return matches
}

func (c *registryFaultCapture) diskRecords(t *testing.T, sid string) []secretrequest.Record {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(c.f.sessions.BaseDir(), sid, "secret_requests.json"))
	require.NoError(t, err)
	var records []secretrequest.Record
	require.NoError(t, json.Unmarshal(raw, &records))
	return records
}

func (c *registryFaultCapture) assertValueFree(t *testing.T, sentinels ...string) {
	t.Helper()
	for label, path := range map[string]string{"native/structured log": c.logPath, "real audit": c.auditPath} {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NotEmpty(t, raw, "%s must have a positive capture", label)
		for _, sentinel := range sentinels {
			require.NotContains(t, string(raw), sentinel, "%s must never echo a value or raw lower-level error", label)
		}
	}
	requireValueFree(t, "whole audit callback records", c.auditCalls, sentinels...)
	requireValueFree(t, "whole notification records", c.notifications, sentinels...)
	requireValueFree(t, "reference-only dispatch", c.f.attempts, sentinels...)
}
