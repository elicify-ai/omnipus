package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// §4.2 shutdown/recovery exception: truthful failure with session_key;
// continue other sessions. No person-facing turn notice is owed at shutdown.
func wu22SeedOrphan(t *testing.T, h *wu22Harness, sid string) []providers.Message {
	t.Helper()
	want := []providers.Message{
		{Role: "user", Content: "WU22 recover this request"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "wu22-orphan", Name: "read_file"}}},
	}
	h.store.AddMessage(sid, want[0].Role, want[0].Content)
	h.store.AddFullMessage(sid, want[1])
	require.Equal(t, want, h.store.GetHistory(sid), "SETUP: real orphaned history was durably seeded")
	return want
}

// A rolled partition remains readable while the CURRENT append target is a
// directory. Re-point only the disk day mark (never provider messages or index
// rows), so GetHistory really reads the original addressed orphan while every
// new append fails. The future mark keeps this invariant independent of midnight.
func wu22BlockAppendKeepHistory(t *testing.T, store *session.UnifiedStore, sid string) func() {
	t.Helper()
	dir := filepath.Join(store.BaseDir(), sid)
	current := filepath.Join(dir, "transcript.jsonl")
	markPath := filepath.Join(dir, "transcript.day")
	mark, err := os.ReadFile(markPath)
	require.NoError(t, err)
	day := strings.TrimSpace(string(mark))
	require.NotEmpty(t, day)
	rolled := filepath.Join(dir, day+".jsonl")
	require.NoError(t, os.Rename(current, rolled))
	require.NoError(t, os.WriteFile(markPath, []byte("9999-12-31\n"), 0o600))
	require.NoError(t, os.Mkdir(current, 0o700))
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Remove(current))
			require.NoError(t, os.Rename(rolled, current))
			require.NoError(t, os.WriteFile(markPath, mark, 0o600))
		})
	}
	t.Cleanup(restore)
	return restore
}

func wu22AppLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shutdown.jsonl")
	oldLevel := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	require.NoError(t, logger.EnableFileLogging(path))
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(oldLevel) })
	return path
}

func wu22LogRows(t *testing.T, text string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row), "instrument: captured structured log must decode")
		rows = append(rows, row)
	}
	return rows
}

func wu22AssertTruthfulFailure(t *testing.T, rows []map[string]any, sid string) {
	t.Helper()
	failures := 0
	for _, row := range rows {
		if row["session_key"] != sid {
			continue
		}
		message, _ := row["message"].(string)
		if message == "" {
			message, _ = row["msg"].(string)
		}
		level, _ := row["level"].(string)
		level = strings.ToLower(level)
		if level == "error" || level == "warn" {
			if row["error"] != nil {
				failures++
			}
		}
		assert.False(t, strings.Contains(message, "written") || strings.Contains(message, "appended"),
			"§4.2: failed session cannot be reported written/appended: %v", row)
	}
	assert.Equal(t, 1, failures, "§4.2: exactly one caller failure report with session_key and error; backend's key-only log is not enough")
}

func wu22ActiveSibling(t *testing.T, h *wu22Harness) string {
	t.Helper()
	sibling, err := h.store.NewSession(session.SessionTypeChat, "webchat", h.agent.ID)
	require.NoError(t, err)
	wu22SeedOrphan(t, h, sibling.ID)
	ts := newTurnState(h.agent, processOptions{SessionKey: sibling.ID, TranscriptSessionID: sibling.ID, TranscriptStore: h.store}, h.al.newTurnEventScope(h.agent.ID, sibling.ID))
	h.al.activeTurnStates.Store(h.sid, h.ts)
	h.al.activeTurnStates.Store(sibling.ID, ts)
	t.Cleanup(func() { h.al.activeTurnStates.Delete(h.sid); h.al.activeTurnStates.Delete(sibling.ID) })
	return sibling.ID
}

func TestWU22ShutdownArchiveFailureIsTruthfulAndSiblingsContinue(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	seeded := wu22SeedOrphan(t, h, h.sid)
	sibling := wu22ActiveSibling(t, h)
	path := wu22AppLog(t)
	restore := wu22BlockAppendKeepHistory(t, h.store, h.sid)
	require.Equal(t, seeded, h.store.GetHistory(h.sid), "instrument: append fault does not hide the active history")
	h.al.writeTurnCancelledRestartForActiveTurns()
	restore()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	wu22AssertTruthfulFailure(t, wu22LogRows(t, string(data)), h.sid)
	assert.Equal(t, seeded, h.store.GetHistory(h.sid), "failed shutdown marker is not durable")
	siblingHistory := h.store.GetHistory(sibling)
	require.Len(t, siblingHistory, 3, "shutdown must keep going after one session fails")
	assert.Equal(t, providers.Message{Role: "system", Content: fmt.Sprintf(`{"type":"turn_canceled_restart","session_key":%q,"reason":"graceful_shutdown"}`, sibling)}, siblingHistory[2])
}

func TestWU22ShutdownHealthyStoreWritesBothMarkers(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	wu22SeedOrphan(t, h, h.sid)
	sibling := wu22ActiveSibling(t, h)
	path := wu22AppLog(t)
	h.al.writeTurnCancelledRestartForActiveTurns()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	rows := wu22LogRows(t, string(data))
	for _, sid := range []string{h.sid, sibling} {
		history := h.store.GetHistory(sid)
		require.Len(t, history, 3)
		assert.Equal(t, providers.Message{Role: "system", Content: fmt.Sprintf(`{"type":"turn_canceled_restart","session_key":%q,"reason":"graceful_shutdown"}`, sid)}, history[2])
		written := 0
		for _, row := range rows {
			if row["session_key"] == sid && row["level"] == "info" {
				message, _ := row["message"].(string)
				if strings.Contains(message, "written") {
					written++
				}
			}
		}
		assert.Equal(t, 1, written, "instrument: successful writes really are logged at the captured level")
	}
}

func wu22RecoveryLog(t *testing.T) *raceFreeLogBuffer {
	t.Helper()
	buffer := &raceFreeLogBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buffer
}

func TestWU22RecoveryArchiveFailureIsTruthfulAndSiblingsContinue(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	seeded := wu22SeedOrphan(t, h, h.sid)
	sibling, err := h.store.NewSession(session.SessionTypeChat, "webchat", h.agent.ID)
	require.NoError(t, err)
	wu22SeedOrphan(t, h, sibling.ID)
	buffer := wu22RecoveryLog(t)
	restore := wu22BlockAppendKeepHistory(t, h.store, h.sid)
	require.Equal(t, seeded, h.store.GetHistory(h.sid), "instrument: recovery can see the orphan despite the append fault")
	failedHistory := RecoverOrphanedToolCalls(h.store, h.sid, nil)
	cleanedSibling := RecoverOrphanedToolCalls(h.store, sibling.ID, nil)
	restore()
	wu22AssertTruthfulFailure(t, wu22LogRows(t, buffer.String()), h.sid)
	assert.Equal(t, seeded, failedHistory, "attempted marker is not a committed cancellation")
	assert.Equal(t, seeded, h.store.GetHistory(h.sid), "failed recovery did not persist the marker")
	assert.Equal(t, seeded[:1], cleanedSibling, "healthy sibling is recovered normally")
	siblingHistory := h.store.GetHistory(sibling.ID)
	require.Len(t, siblingHistory, 3)
	assert.Equal(t, providers.Message{Role: "system", Content: `{"type":"turn_canceled_restart","tool_call_id":"wu22-orphan","reason":"ungraceful_shutdown_recovery"}`}, siblingHistory[2])
}

func TestWU22RecoveryHealthyStoreWritesMarkerOnce(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	seeded := wu22SeedOrphan(t, h, h.sid)
	buffer := wu22RecoveryLog(t)
	for i := 0; i < 2; i++ { // second recovery is the idempotency control
		assert.Equal(t, seeded[:1], RecoverOrphanedToolCalls(h.store, h.sid, nil))
	}
	history := h.store.GetHistory(h.sid)
	require.Len(t, history, 3)
	assert.Equal(t, providers.Message{Role: "system", Content: `{"type":"turn_canceled_restart","tool_call_id":"wu22-orphan","reason":"ungraceful_shutdown_recovery"}`}, history[2])
	appended := 0
	for _, row := range wu22LogRows(t, buffer.String()) {
		if row["session_key"] == h.sid {
			message, _ := row["msg"].(string)
			if strings.Contains(message, "synthetic deny appended") {
				appended++
			}
		}
	}
	assert.Equal(t, 1, appended, "instrument: successful recovery append is actually reported, once")
	h.assertSiblingWritable(t)
}
