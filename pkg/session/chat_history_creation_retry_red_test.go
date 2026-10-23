package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gate0 F2 / D1a creation publication: a verified empty unpublished prefix is
// resumable; this is NOT adoption of arbitrary existing state or a rebuild.
func TestChatHistoryCreation_InterruptedMainCreationResumes(t *testing.T) {
	if os.Getenv(gate0CreationChildEnv) != "" {
		gate0CreationCrashChild(t)
		return
	}
	for _, point := range []string{"empty_checkpoint_ready", "before_identity_publish"} {
		for _, crash := range []bool{false, true} {
			name := fmt.Sprintf("%s_crash_%t", point, crash)
			t.Run(name, func(t *testing.T) {
				root, sid := gate0CreationIncomplete(t, point, crash)
				store, err := NewUnifiedStore(root)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, store.Close()) })
				archiveBefore := partitionBytes(t, store, sid)
				meta, err := store.GetOrCreateMainSession(gate0CreationWorkspace, gate0CreationAgent)
				if assert.NoError(t, err, "F2: same computed main MUST resume its verified empty unpublished baseline after actual creation interruption") {
					require.NotNil(t, meta)
					require.Equal(t, sid, meta.ID)
					require.Equal(t, SessionTypeMain, meta.Type)
					require.Equal(t, gate0CreationWorkspace, meta.WorkspaceID)
					require.Equal(t, gate0CreationAgent, meta.AgentID)
					require.NoError(t, VerifyChatHistoryIndex(context.Background(), filepath.Join(root, sid)))
					require.Empty(t, historyRead(t, store, sid, historyOptions(40)).Entries)
					again, repeatErr := store.GetOrCreateMainSession(gate0CreationWorkspace, gate0CreationAgent)
					require.NoError(t, repeatErr)
					require.Equal(t, meta.ID, again.ID)
					require.Equal(t, meta.CreatedAt, again.CreatedAt, "resume/repeat never mints a replacement identity")
				}
				require.Equal(t, archiveBefore, partitionBytes(t, store, sid), "empty resume never fabricates canonical content")
			})
		}
	}
	for _, fault := range []string{"nonempty_unpublished_archive", "inconsistent_unpublished_checkpoint", "conflicting_published_identity"} {
		t.Run("refuse_"+fault, func(t *testing.T) { gate0CreationMustRefuse(t, fault) })
	}
}

func gate0CreationMustRefuse(t *testing.T, fault string) {
	t.Helper()
	root, sid := gate0CreationIncomplete(t, "before_identity_publish", false)
	dir := filepath.Join(root, sid)
	switch fault {
	case "nonempty_unpublished_archive":
		// A deliberate filesystem fault AFTER the valid empty baseline. It is
		// not a canonical-writer positive or an authorization to adopt content.
		record := ArchiveRecord{TranscriptEntry: TranscriptEntry{ID: "do-not-adopt", Role: "user", Content: "existing canonical content",
			AgentID: "mia", Timestamp: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC), ViewMembership: ViewMembershipChat}}
		body, err := json.Marshal(record)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, archiveCurrentFile), append(body, '\n'), 0o600))
	case "inconsistent_unpublished_checkpoint":
		path := filepath.Join(dir, ChatHistoryIndexDirectory, "checkpoint.json")
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &fields))
		fields["rank"] = json.RawMessage("1") // root0/empty archive cannot prove this claimed occurrence
		body, err = json.Marshal(fields)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, body, 0o600))
	case "conflicting_published_identity":
		store, err := NewUnifiedStore(root)
		require.NoError(t, err)
		u1SeedStoredMainRecord(t, store, sid, "jim", gate0CreationWorkspace)
		require.NoError(t, store.Close())
	default:
		t.Fatalf("unknown creation refusal fixture %s", fault)
	}
	store, err := NewUnifiedStore(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	before := historyTree(t, dir)
	meta, err := store.GetOrCreateMainSession(gate0CreationWorkspace, gate0CreationAgent)
	require.Error(t, err, "F2 empty-resume exception must NOT adopt nonempty/inconsistent/conflicting state")
	require.Nil(t, meta)
	require.Equal(t, before, historyTree(t, dir), "refusal cannot rewrite prior identity, canonical data or derived evidence")
}
