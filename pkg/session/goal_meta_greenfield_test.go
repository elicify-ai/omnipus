// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// TestGoalMetaGreenfield (ADR-086 GOAL-FR-005, wave S6, "the deletion
// half"): the goal loop state that used to live in SessionMeta/UnifiedMeta/
// MetaPatch's Goal* fields, and goal.json's on-disk group, is gone. The
// goal is its own stored entity now (pkg/goal.Store, ADR-086) — a session
// composes no goal data at all, from disk or anywhere else.
//
// This supersedes the wave-1b/ADR-081 version of this test (delivery-plan
// §5's S2→S6 chain: S2 left this file's ORIGINAL content untouched, S6
// rewrites it here). The old version proved the retired confirm-gate
// fields (GoalPendingJSON/GoalClarificationJSON) were dropped while the
// SURVIVING Goal* fields (GoalCondition, GoalCriteriaJSON, the FR-010/
// FR-014b/FR-031 additions) round-tripped through SetMeta/GetMeta. Now
// there is nothing left to round-trip — every one of those fields is
// retired too — so this version proves the opposite: a pre-existing
// goal.json, whatever it carries (old confirm-gate keys, a legitimate
// pre-S6 active record, or both at once), is read by nothing, written by
// nothing, and never resurfaces through GetMeta or on the wire (D-F: no
// migration, no rescue — and, symmetrically, no error either; the file is
// just inert).
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoalMetaGreenfield(t *testing.T) {
	t.Run("stale_goal_json_never_read_and_never_surfaces_on_the_wire", func(t *testing.T) {
		store := newTestStore(t)
		meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
		require.NoError(t, err)
		sid := meta.ID

		// A goal.json shaped like a pre-S6 install: the retired confirm-gate
		// draft/clarification keys AND a legitimate active-goal record — the
		// exact combination the pre-S6 version of this test proved loaded
		// correctly. Post-S6, none of it composes into anything: there is no
		// field left on UnifiedMeta to hold any of it.
		staleGoalJSON := []byte(`{
			"goal_id": "goal_pre_s6_01",
			"goal_condition": "the feature ships",
			"goal_rounds_used": 2,
			"goal_max_rounds": 8,
			"goal_latest_reason": "still working",
			"goal_criteria": "{\"intent\":\"the feature ships\",\"prompt\":\"the feature ships\",\"criteria\":[{\"id\":\"c1\",\"kind\":\"prose\",\"judgment\":\"boolean\",\"text\":\"it ships\",\"author\":{\"kind\":\"user\",\"id\":\"tester\"},\"status\":\"pending\"}]}",
			"goal_pending": "{\"intent\":\"stale pending draft\",\"prompt\":\"stale pending draft\",\"criteria\":[]}",
			"goal_clarification": "{\"intent\":\"stale intent\",\"question\":\"stale question\",\"asked_at\":\"2026-01-01T00:00:00Z\"}",
			"goal_question_rounds_used": 1,
			"goal_zero_output_pushes": 2,
			"goal_route_channel": "telegram",
			"goal_route_chat_id": "chat-42"
		}`)
		goalPath := filepath.Join(store.baseDir, sid, "goal.json")
		require.NoError(t, os.WriteFile(goalPath, staleGoalJSON, 0o600))

		// A FRESH store instance forces a real disk read — the store that
		// created the session still has the pre-write value cached.
		cold, err := NewUnifiedStore(store.baseDir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = cold.Close() })

		got, err := cold.GetMeta(sid)
		require.NoError(t, err, "a stale goal.json must load without error — D-F: no migration, no rescue, and no failure either, it is simply never opened")

		// The wire representation (what the SPA and every generated type
		// actually see, since UnifiedMeta embeds SessionMeta byte-for-byte)
		// must carry NONE of the goal_* keys. This proves both that the Go
		// fields are gone from the type AND that the stale disk content
		// never leaks onto the wire through some other path.
		raw, err := json.Marshal(got)
		require.NoError(t, err)
		var onWire map[string]any
		require.NoError(t, json.Unmarshal(raw, &onWire))
		for _, key := range []string{
			"goal_id", "goal_condition", "goal_rounds_used", "goal_max_rounds",
			"goal_latest_reason", "goal_started_at", "goal_last_activity_at",
			"goal_criteria", "goal_pending", "goal_clarification",
			"goal_question_rounds_used", "goal_zero_output_pushes",
			"goal_route_channel", "goal_route_chat_id", "goal_route_session_key", "goal_route_agent_id",
		} {
			_, present := onWire[key]
			assert.False(t, present, "the wire representation must never carry %q — every Goal* field left session meta in wave S6", key)
		}
		// pending_ask, by contrast, is NOT a goal field (wave S2) and must
		// still round-trip normally — this stale goal.json carries no
		// "pending_ask" key of its own, so it must compose as absent, not
		// be confused with the unrelated goal_pending key above.
		assert.Empty(t, got.PendingAskJSON)

		// A write that touches an unrelated group (Title, identity-only)
		// must not disturb the stale goal.json bytes — this store no longer
		// has any writer that even knows the file exists.
		before, err := os.ReadFile(goalPath)
		require.NoError(t, err)
		title := "renamed"
		require.NoError(t, cold.SetMeta(sid, MetaPatch{Title: &title}))
		after, err := os.ReadFile(goalPath)
		require.NoError(t, err)
		assert.Equal(t, before, after, "goal.json must be byte-identical after an unrelated SetMeta — nothing in this store writes goal.json anymore")
	})

	t.Run("a_new_session_never_gets_a_goal_json", func(t *testing.T) {
		store := newTestStore(t)
		meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
		require.NoError(t, err)
		sid := meta.ID

		goalPath := filepath.Join(store.baseDir, sid, "goal.json")
		_, statErr := os.Stat(goalPath)
		assert.True(t, os.IsNotExist(statErr), "a brand-new session must never gain a goal.json — there is no writer left that creates one")

		// Even a MetaPatch write on a forced-flush field (Status) — the
		// kind of call that used to cascade into the goal group's writer
		// when SetMeta's goalTouched dispatch still existed — creates no
		// goal.json.
		status := StatusArchived
		require.NoError(t, store.SetMeta(sid, MetaPatch{Status: &status}))
		_, statErr = os.Stat(goalPath)
		assert.True(t, os.IsNotExist(statErr), "goal.json must still not exist after a SetMeta touching an unrelated group")

		// And writeMetaLocked's own diff-based dispatcher (unified_api.go's
		// two call sites reach it, not SetMeta) must not create it either —
		// there is no writeGoal branch left to accidentally fire.
		h := store.lockSession(sid)
		current, err := store.readMetaLocked(sid)
		require.NoError(t, err)
		h.Unlock()
		mutated := current.Clone()
		mutated.Title = "direct-dispatch-write"
		h = store.lockSession(sid)
		writeErr := store.writeMetaLocked(sid, mutated)
		h.Unlock()
		require.NoError(t, writeErr)
		_, statErr = os.Stat(goalPath)
		assert.True(t, os.IsNotExist(statErr), "writeMetaLocked must still never create goal.json — there is no writeGoal branch left")
	})
}
