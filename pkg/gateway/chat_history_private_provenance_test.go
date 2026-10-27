package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// PSC=C: retained private provenance is checked through the real internal
// reader, not exposed as an uncontracted Message property or erased on disk.
func historyMigrationInternalSpawn(t *testing.T, store *session.UnifiedStore, sid string) []byte {
	t.Helper()
	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	require.Len(t, entries, 2, "both real canonical fixture entries exist before wire exclusion assertions")
	require.Equal(t, "parent-msg-1", entries[0].ID)
	require.Equal(t, "child-msg-1", entries[1].ID)
	require.Equal(t, "delegate-call-1", entries[1].ParentSpawnCallID, "FR-036 provenance survives at its actual archive/internal boundary")
	body, readErr := os.ReadFile(filepath.Join(store.BaseDir(), sid, "transcript.jsonl"))
	require.NoError(t, readErr)
	return body
}

func historyMigrationSpawnWire(t *testing.T, entries []generated.Message, body []byte, field string) {
	t.Helper()
	require.Len(t, entries, 2)
	require.Equal(t, []string{"parent-msg-1", "child-msg-1"}, messageIDs(entries), "acceptance order and every source row are preserved")
	for _, entry := range entries {
		require.NotNil(t, entry.HistoryPosition)
		require.Regexp(t, `^hp1_[A-Za-z0-9_-]{43}$`, *entry.HistoryPosition)
	}
	require.NotEqual(t, *entries[0].HistoryPosition, *entries[1].HistoryPosition, "different source occurrences have distinct origins")
	raw := historyMigrationEntryObjects(t, body, field)
	require.Len(t, raw, 2)
	for _, entry := range raw {
		require.NotContains(t, entry, "parent_spawn_call_id", "generated decoding alone silently ignores unknown keys and is not a privacy check")
	}
	require.Equal(t, "I'll delegate this research task.", raw[0]["content"])
	require.Equal(t, "assistant", raw[0]["role"])
	require.Equal(t, "jim", raw[0]["agent_id"])
	require.Equal(t, "[external-cli permission] requesting read access to /tmp/report.md", raw[1]["content"])
	require.Equal(t, "assistant", raw[1]["role"])
	require.Equal(t, "researcher", raw[1]["agent_id"])
	require.Equal(t, "z-ai/glm-5.2", raw[1]["model"])
	require.Equal(t, "child-turn-1", raw[1]["turn_id"])
}

func historyMigrationSpawnUnchanged(t *testing.T, store *session.UnifiedStore, sid string, before []byte) {
	t.Helper()
	after := historyMigrationInternalSpawn(t, store, sid)
	require.Equal(t, before, after, "page/detail reads neither normalize nor strip canonical spawn provenance bytes")
}
