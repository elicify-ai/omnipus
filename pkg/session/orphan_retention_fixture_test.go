package session_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func newOrphanRetentionStore(t *testing.T) *session.UnifiedStore {
	t.Helper()
	home, rootErr := os.MkdirTemp(os.TempDir(), "omnipus-e2e-gwproc-orphan-")
	require.NoError(t, rootErr)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(home)) })
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), []byte("{}\n"), 0o600))
	store, storeErr := session.NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
	require.NoError(t, storeErr)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

// External tests use the existing Section-G driver in this SAME native test
// executable. It uses the real canonical writer and product rollover, restoring
// its private clock afterward. No production clock/export or archive shim.
func seedOrphanRetentionCanonicalDays(t *testing.T, store *session.UnifiedStore, sid string) *session.UnifiedStore {
	t.Helper()
	home := filepath.Dir(store.BaseDir())
	days := make([]map[string]any, 0, 3)
	for index, day := range []string{"2026-01-01", "2026-04-21", "2026-04-22"} {
		at, parseErr := time.Parse("2006-01-02", day)
		require.NoError(t, parseErr)
		entry := session.TranscriptEntry{
			ID:   []string{"orphan-entry", "recent-entry", "current-entry"}[index],
			Type: session.EntryTypeMessage, Role: "user", Content: "genuine canonical orphan retention row",
			AgentID: "orphan-agent", Timestamp: at, ViewMembership: session.ViewMembershipChat,
		}
		days = append(days, map[string]any{"day": day, "entries": []session.TranscriptEntry{entry}})
	}
	// A private fixture plan, not a gateway wire type or replacement index.
	plan := map[string]any{"schema_version": 1, "data_root": home, "action": "seed_days", "session_id": sid, "days": days}
	encoded, encodeErr := json.Marshal(plan)
	require.NoError(t, encodeErr)
	planPath := filepath.Join(t.TempDir(), "orphan-canonical-days.json")
	require.NoError(t, os.WriteFile(planPath, encoded, 0o600))
	require.NoError(t, store.Close(), "parent writer/flusher joined before native fixture child")
	binary, binaryErr := os.Executable()
	require.NoError(t, binaryErr)
	command := exec.Command(binary, "-test.run", "^TestChatHistoryFixtureDriver$", "-test.v")
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "OMNIPUS_CHAT_HISTORY_FIXTURE_PLAN=") {
			command.Env = append(command.Env, item)
		}
	}
	command.Env = append(command.Env, "OMNIPUS_CHAT_HISTORY_FIXTURE_PLAN="+planPath)
	output, childErr := command.CombinedOutput() // Actual child exit/Wait, not a sleep oracle.
	require.NoError(t, childErr, "canonical/day fixture failed: %s", output)
	require.Contains(t, string(output), "--- PASS: TestChatHistoryFixtureDriver ")
	found := 0
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		const prefix = "CHAT_HISTORY_FIXTURE_RESULT="
		if !bytes.HasPrefix(line, []byte(prefix)) {
			continue
		}
		found++
		var receipt map[string]any
		require.NoError(t, json.Unmarshal(bytes.TrimPrefix(line, []byte(prefix)), &receipt))
		require.Equal(t, map[string]any{"schema_version": float64(1), "action": "seed_days", "session_id": sid, "written_records": float64(3), "rolled_partitions": float64(2), "removed_partitions": float64(0)}, receipt)
	}
	require.Equal(t, 1, found, "one exact real fixture receipt")
	reopened, reopenErr := session.NewUnifiedStoreWithHome(store.BaseDir(), home)
	require.NoError(t, reopenErr)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	require.NoError(t, session.VerifyChatHistoryIndex(context.Background(), filepath.Join(reopened.BaseDir(), sid)), "genuine day writers preserve the real healthy baseline before sweep")
	entries, readErr := reopened.ReadTranscript(sid)
	require.NoError(t, readErr)
	require.Len(t, entries, 3)
	for index, entry := range entries {
		require.Equal(t, []string{"orphan-entry", "recent-entry", "current-entry"}[index], entry.ID)
		require.Equal(t, session.ViewMembershipChat, entry.ViewMembership)
		require.Equal(t, "orphan-agent", entry.AgentID)
	}
	return reopened
}
