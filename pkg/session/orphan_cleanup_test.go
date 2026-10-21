// Contract test: Plan 3 §1 acceptance decision — orphaned session directories
// (sessions whose agent has been deleted) still participate in retention policy.
//
// BDD: Given sessions exist for a deleted agent, When retention sweep runs,
//
//	Then orphaned sessions are swept just like active-agent sessions.
//
// Acceptance decision: Plan 3 §1 "Orphan session directories: kept under retention policy; shown with 'removed' badge"
// Traces to: temporal-puzzling-melody.md §4 Axis-1, pkg/session/orphan_cleanup_test.go

package session_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func newUnifiedStoreForExternalTest(t *testing.T) *session.UnifiedStore {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err, "NewUnifiedStore must succeed")
	// See pkg/session's u2NewTestStore doc comment / issue #634: without
	// this, the store's background stats-flusher goroutine outlives this
	// test and can pollute a later in-package test's FR-101 lock-recorder
	// trace via the shared package-level seam.
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestOrphanSessionsParticipateInRetention verifies that session JSONL files from
// a no-longer-present agent are still subject to the standard retention sweep.
//
// Traces to: temporal-puzzling-melody.md §4 Axis-1 — TestOrphanSessionsParticipateInRetention
func TestOrphanSessionsParticipateInRetention(t *testing.T) {
	// Step 1: Instantiate the real UnifiedStore in an owned native fixture root.
	store := newOrphanRetentionStore(t)

	// Step 2: Create a session for agent "orphan-agent".
	meta, err := store.NewSession(session.SessionTypeChat, "", "orphan-agent")
	require.NoError(t, err, "NewSession must succeed")
	sessionID := meta.ID

	// Step 3: Simulate agent deletion — we do not modify a config file here since
	// the UnifiedStore does not gate retention on agent existence. "Orphan" simply
	// means the owning agent has been removed from the system; the session directory
	// remains on disk. RetentionSweep treats all session directories identically.

	// The real canonical/day writers create both rolled files with explicit
	// membership and a healthy derived index. Only file age is planted here;
	// no raw id-only archive or separate history-readiness outcome is accepted.
	store = seedOrphanRetentionCanonicalDays(t, store, sessionID)
	staleDir := store.BaseDir()
	staleFile := filepath.Join(staleDir, sessionID, "2026-01-01.jsonl")
	require.FileExists(t, staleFile)
	staleTime := time.Now().Add(-30 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(staleFile, staleTime, staleTime))

	// Step 4 (additional): The writer-created recent rolled file is within the
	// retention window and must NOT be deleted; its real mtime needs no override.
	recentFile := filepath.Join(staleDir, sessionID, "2026-04-21.jsonl")
	require.FileExists(t, recentFile)

	// Step 5: Call store.RetentionSweep(7).
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	assert.Equal(t, 1, removed, "only the stale orphan file must be swept")

	// The stale file must be gone.
	_, statErr := os.Stat(staleFile)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "stale orphan session file must be deleted by retention sweep")

	// The recent file must still be present.
	_, statErr = os.Stat(recentFile)
	assert.NoError(t, statErr, "recent orphan session file must survive retention sweep")
}
