// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the ADR-086 GOAL-FR-005 relocation (wave S2): PendingAskJSON
// moves off goal.json onto its own session-owned pending_ask.json, with
// SessionMeta/UnifiedMeta/MetaPatch's in-memory shape and semantics
// unchanged for every existing caller (pkg/askuser, pkg/gateway/replay.go).
//
// Traces to: docs/internal/specs/goal-entity-spec.md FR-005/FR-050,
// scenarios S-46/S-45; docs/internal/specs/adr-084-086-joint-delivery-plan.md
// wave S2, resolutions C-54/C-65. `pkg/session/unified_meta_files_test.go`,
// the file the spec's own traceability table names for these two test
// names, does not exist and must not be created (delivery-plan R-34) — this
// file is S2's real, in-write-set home for them.
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPendingAskDoesNotMoveToGoal (FR-005, S-46 — "the parked-question set
// survives the move"): PendingAskJSON persists to its own pending_ask.json,
// under the same "pending_ask" JSON key it used to carry inside goal.json,
// never into goal.json itself, and round-trips through SetMeta/GetMeta
// including across a store restart that forces a real disk read.
func TestPendingAskDoesNotMoveToGoal(t *testing.T) {
	store := newTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
	require.NoError(t, err)
	sid := meta.ID

	askSet := `{"questions":[{"id":"q1","text":"proceed?"}]}`
	require.NoError(t, store.SetMeta(sid, MetaPatch{PendingAskJSON: &askSet}))

	// pending_ask.json exists and carries the value under the SAME JSON key
	// the field used to carry inside goal.json (byte-identical relocation).
	pendingAskPath := filepath.Join(store.BaseDir(), sid, "pending_ask.json")
	data, err := os.ReadFile(pendingAskPath)
	require.NoError(t, err, "pending_ask.json must exist once PendingAskJSON is set")
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(data, &onDisk))
	assert.Equal(t, askSet, onDisk["pending_ask"], "must be stored under the pending_ask key")

	// goal.json, if it exists at all, must never carry a pending_ask key —
	// it left the goal group entirely (this SetMeta call never touched a
	// goal field, so goal.json legitimately does not exist yet; the
	// assertion covers both that case and a defensive check should it).
	goalPath := filepath.Join(store.BaseDir(), sid, "goal.json")
	if goalData, statErr := os.ReadFile(goalPath); statErr == nil {
		var goalOnDisk map[string]any
		require.NoError(t, json.Unmarshal(goalData, &goalOnDisk))
		_, hasKey := goalOnDisk["pending_ask"]
		assert.False(t, hasKey, "goal.json must never carry a pending_ask key after the relocation")
	} else {
		require.True(t, os.IsNotExist(statErr), "unexpected goal.json read error: %v", statErr)
	}

	// GetMeta (warm cache) reflects the value under the unchanged field
	// name — pkg/askuser and pkg/gateway need no change to keep working.
	got, err := store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, askSet, got.PendingAskJSON)

	// A FRESH store instance forces a real disk read — proves the new
	// pending_ask.json group composes correctly on cold load, not merely
	// from the still-warm cache of the store that wrote it.
	cold, err := NewUnifiedStore(store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = cold.Close() })
	gotCold, err := cold.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, askSet, gotCold.PendingAskJSON, "must survive a cold disk read")
}

// TestPendingAskWrite_FieldGroupIsolation proves the ADR-057 FR-084
// discipline extends to the new group: a PendingAskJSON-only SetMeta call
// creates ONLY pending_ask.json (lazy file creation, matching goal.json/
// loop.json's own property) and never disturbs goal.json/loop.json; a
// goal-only SetMeta call never disturbs an already-set PendingAskJSON.
func TestPendingAskWrite_FieldGroupIsolation(t *testing.T) {
	store := newTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
	require.NoError(t, err)
	sid := meta.ID

	goalPath := filepath.Join(store.BaseDir(), sid, "goal.json")
	loopPath := filepath.Join(store.BaseDir(), sid, "loop.json")
	pendingAskPath := filepath.Join(store.BaseDir(), sid, "pending_ask.json")

	_, statErr := os.Stat(pendingAskPath)
	require.True(t, os.IsNotExist(statErr), "pending_ask.json must not exist before any pending-ask write")

	askSet := `{"questions":[]}`
	require.NoError(t, store.SetMeta(sid, MetaPatch{PendingAskJSON: &askSet}))

	_, statErr = os.Stat(pendingAskPath)
	require.NoError(t, statErr, "pending_ask.json must be created lazily on first write")
	_, statErr = os.Stat(goalPath)
	assert.True(t, os.IsNotExist(statErr), "a PendingAskJSON-only SetMeta must not create goal.json")
	_, statErr = os.Stat(loopPath)
	assert.True(t, os.IsNotExist(statErr), "a PendingAskJSON-only SetMeta must not create loop.json")

	// Now a goal-only write — must not disturb the pending-ask value already
	// on disk, and must not rewrite pending_ask.json's content.
	beforeGoalWrite, err := os.ReadFile(pendingAskPath)
	require.NoError(t, err)

	// ADR-086 S6 retired the whole goal.json group, so the "unrelated write"
	// that proves group isolation is now a loop.json write. The property under
	// test is unchanged: writing one field group must not touch another's file.
	loopMode := "ship-it"
	require.NoError(t, store.SetMeta(sid, MetaPatch{LoopMode: &loopMode}))

	afterGoalWrite, err := os.ReadFile(pendingAskPath)
	require.NoError(t, err)
	assert.Equal(t, beforeGoalWrite, afterGoalWrite, "a goal-only SetMeta must not touch pending_ask.json's bytes")

	got, err := store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, askSet, got.PendingAskJSON, "PendingAskJSON must be unaffected by an unrelated field-group write")
	assert.Equal(t, loopMode, got.LoopMode, "the unrelated write must itself have landed, or this test proves nothing")

	pendingAskOnDisk, err := u5ReadPendingAskFile(filepath.Join(store.BaseDir(), sid))
	require.NoError(t, err)
	assert.Equal(t, askSet, pendingAskOnDisk.PendingAskJSON)
}

// TestPendingAskFile_AbsentVsCorrupt (BDD-62's absent/corrupt distinction,
// carried over verbatim to the new group): an absent pending_ask.json
// composes as the zero value with no error; a present-but-corrupt one
// surfaces an error from GetMeta rather than silently reading as empty.
func TestPendingAskFile_AbsentVsCorrupt(t *testing.T) {
	store := newTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
	require.NoError(t, err)
	sid := meta.ID

	got, err := store.GetMeta(sid)
	require.NoError(t, err)
	assert.Empty(t, got.PendingAskJSON, "an absent pending_ask.json must compose as empty, not error")

	pendingAskPath := filepath.Join(store.BaseDir(), sid, "pending_ask.json")
	require.NoError(t, os.WriteFile(pendingAskPath, []byte("{not valid json"), 0o600))

	// Force a cold read past the warm cache.
	cold, err := NewUnifiedStore(store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = cold.Close() })
	_, err = cold.GetMeta(sid)
	require.Error(t, err, "a corrupt pending_ask.json must surface an error, never a silently zeroed field")
}

// TestNoMigrationPathExists (FR-050, S-45 — strengthened per delivery-plan
// R-07's dissolution by operator decision D-F: assert no legacy value is
// EVER READ, not merely that none is written back). A pre-existing
// goal.json carrying the OLD pending_ask key (the shape u5GoalFile had
// before this wave) must load with that key silently dropped by
// json.Unmarshal — NEVER composed into PendingAskJSON. Only a real
// pending_ask.json is ever consulted for that field; there is no rescue, no
// shim, no legacy parse path, matching TestGoalMetaGreenfield's own
// no-back-compat contract for the sibling ADR-081 D9 field removal.
func TestNoMigrationPathExists(t *testing.T) {
	store := newTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
	require.NoError(t, err)
	sid := meta.ID

	staleGoalJSON := []byte(`{
		"goal_condition": "the feature ships",
		"goal_rounds_used": 1,
		"pending_ask": "{\"questions\":[{\"id\":\"stale-pre-S2\"}]}"
	}`)
	goalPath := filepath.Join(store.BaseDir(), sid, "goal.json")
	require.NoError(t, os.WriteFile(goalPath, staleGoalJSON, 0o600))

	cold, err := NewUnifiedStore(store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = cold.Close() })

	got, err := cold.GetMeta(sid)
	require.NoError(t, err, "a stale goal.json carrying a legacy pending_ask key must still load cleanly")
	// ADR-086 S6 retired goal.json in full and operator decision D-F forbids any
	// upgrade or migration path. So this assertion is now STRONGER than it was:
	// previously the legitimate goal fields were still read out of a stale
	// goal.json and only the pending_ask key was refused. Now the file is never
	// opened at all, so NOTHING in it reaches memory. Asserting the old
	// GoalCondition/GoalRoundsUsed values here would require reinstating the
	// legacy reader D-F prohibits.
	assert.Empty(t, got.PendingAskJSON, "a legacy pending_ask key inside a stale goal.json must NOT be rescued (D-F: no migration path)")

	// And a later write to a surviving field group must not resurrect it. The
	// stale goal.json is left on disk untouched — S3's retention pass removes
	// it, never a migration (R-34).
	newMode := "amended"
	require.NoError(t, cold.SetMeta(sid, MetaPatch{LoopMode: &newMode}))

	after, err := cold.GetMeta(sid)
	require.NoError(t, err)
	assert.Empty(t, after.PendingAskJSON, "a write to another group must not resurrect the stale pending_ask key")
	assert.Equal(t, newMode, after.LoopMode, "the write must itself have landed")

	stillStale, err := os.ReadFile(goalPath)
	require.NoError(t, err, "the stale goal.json is left alone, not rewritten and not deleted")
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(stillStale, &onDisk))
	_, hasStaleKey := onDisk["pending_ask"]
	assert.True(t, hasStaleKey, "the stale key is still on disk and simply never read — proof nothing migrated it")
}

// TestPendingAskClear proves the MetaPatch clear convention (pass an empty
// string) works identically to every other string field's clear semantics,
// and that writeMetaLocked's diff-based dispatch (used by
// pkg/session/unified_api.go's two call sites) also routes a PendingAskJSON
// change to pending_ask.json — not goal.json — exercising the code path
// SetMeta itself does not reach.
func TestPendingAskClear(t *testing.T) {
	store := newTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
	require.NoError(t, err)
	sid := meta.ID

	askSet := `{"questions":[{"id":"q1"}]}`
	require.NoError(t, store.SetMeta(sid, MetaPatch{PendingAskJSON: &askSet}))
	got, err := store.GetMeta(sid)
	require.NoError(t, err)
	require.Equal(t, askSet, got.PendingAskJSON)

	empty := ""
	require.NoError(t, store.SetMeta(sid, MetaPatch{PendingAskJSON: &empty}))
	got, err = store.GetMeta(sid)
	require.NoError(t, err)
	assert.Empty(t, got.PendingAskJSON, "an explicit empty-string patch must clear PendingAskJSON")

	pendingAskOnDisk, err := u5ReadPendingAskFile(filepath.Join(store.BaseDir(), sid))
	require.NoError(t, err)
	assert.Empty(t, pendingAskOnDisk.PendingAskJSON, "the clear must reach disk")
}

// TestWriteMetaLocked_PendingAskDiffDispatch exercises writeMetaLocked's
// (unified.go) diff-based dispatcher directly — the path
// pkg/session/unified_api.go's CreateSessionWithID/AppendTranscriptStrict
// call, which SetMeta's own tests never reach. A meta whose ONLY change
// from the currently-persisted value is PendingAskJSON must write
// pending_ask.json and MUST NOT rewrite goal.json/loop.json.
func TestWriteMetaLocked_PendingAskDiffDispatch(t *testing.T) {
	store := newTestStore(t)
	created, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
	require.NoError(t, err)
	sid := created.ID

	h := store.lockSession(sid)
	current, err := store.readMetaLocked(sid)
	require.NoError(t, err)
	h.Unlock()

	mutated := current.Clone()
	mutated.PendingAskJSON = `{"questions":[{"id":"direct-dispatch"}]}`

	h = store.lockSession(sid)
	err = store.writeMetaLocked(sid, mutated)
	h.Unlock()
	require.NoError(t, err)

	pendingAskPath := filepath.Join(store.BaseDir(), sid, "pending_ask.json")
	_, statErr := os.Stat(pendingAskPath)
	require.NoError(t, statErr, "writeMetaLocked must dispatch a PendingAskJSON-only change to pending_ask.json")

	goalPath := filepath.Join(store.BaseDir(), sid, "goal.json")
	_, statErr = os.Stat(goalPath)
	assert.True(t, os.IsNotExist(statErr), "writeMetaLocked must NOT create goal.json for a PendingAskJSON-only diff")

	onDisk, err := u5ReadPendingAskFile(filepath.Join(store.BaseDir(), sid))
	require.NoError(t, err)
	assert.Equal(t, mutated.PendingAskJSON, onDisk.PendingAskJSON)
}
