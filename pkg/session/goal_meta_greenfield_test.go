// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// TestGoalMetaGreenfield (ADR-081 D9/Migration, FR-024/FR-030, wave-1b test
// 18): the retired confirm-gate's pending-draft fields — GoalPendingJSON/
// GoalClarificationJSON, the old goal_pending/goal_clarification JSON keys
// in goal.json — are removed from UnifiedMeta/MetaPatch/u5GoalFile
// (greenfield, no shim, no legacy parse path). A stale on-disk goal.json
// carrying those keys must load cleanly (unknown JSON fields are silently
// dropped by encoding/json) and the stale keys must be ABSENT the next time
// goal.json is written. The new FR-010/FR-014b/FR-031 fields
// (GoalQuestionRoundsUsed, GoalZeroOutputPushes, the GoalRoute* group) must
// round-trip through SetMeta/GetMeta and survive a disk round-trip. A
// pre-upgrade ACTIVE goal record (GoalCondition + GoalCriteriaJSON written
// by the old compile path) must keep loading and working exactly as before
// (FR-030).
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGoalMetaGreenfield(t *testing.T) {
	t.Run("stale_pending_fields_ignored_and_dropped_on_next_write", func(t *testing.T) {
		store := newTestStore(t)
		meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
		require.NoError(t, err)
		sid := meta.ID

		// A pre-upgrade goal.json: the retired confirm-gate's pending-draft
		// fields under their OLD JSON keys, alongside a legitimate ACTIVE
		// goal record written by the old compile path (FR-030's case).
		staleGoalJSON := []byte(`{
			"goal_id": "goal_pre_upgrade_01",
			"goal_condition": "the feature ships",
			"goal_rounds_used": 2,
			"goal_max_rounds": 8,
			"goal_criteria": "{\"intent\":\"the feature ships\",\"prompt\":\"the feature ships\",\"criteria\":[{\"id\":\"c1\",\"kind\":\"prose\",\"judgment\":\"boolean\",\"text\":\"it ships\",\"author\":{\"kind\":\"user\",\"id\":\"tester\"},\"status\":\"pending\"}]}",
			"goal_pending": "{\"intent\":\"stale pending draft\",\"prompt\":\"stale pending draft\",\"criteria\":[]}",
			"goal_clarification": "{\"intent\":\"stale intent\",\"question\":\"stale question\",\"asked_at\":\"2026-01-01T00:00:00Z\"}"
		}`)
		goalPath := filepath.Join(store.baseDir, sid, "goal.json")
		require.NoError(t, os.WriteFile(goalPath, staleGoalJSON, 0o600))

		// A FRESH store instance forces a real disk read — the store that
		// created the session still has the pre-write value cached.
		cold, err := NewUnifiedStore(store.baseDir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = cold.Close() })

		got, err := cold.GetMeta(sid)
		require.NoError(t, err, "a stale pending-draft goal.json must load without error")
		// FR-030: the pre-upgrade ACTIVE record continues loading intact.
		if got.GoalCondition != "the feature ships" {
			t.Fatalf("GoalCondition = %q, want the pre-upgrade active condition", got.GoalCondition)
		}
		if got.GoalCriteriaJSON == "" {
			t.Fatal("GoalCriteriaJSON must survive from the pre-upgrade record")
		}
		if got.GoalRoundsUsed != 2 || got.GoalMaxRounds != 8 {
			t.Fatalf("rounds/max = %d/%d, want 2/8", got.GoalRoundsUsed, got.GoalMaxRounds)
		}

		// Write the record back (any SetMeta touching the goal group) and
		// verify the stale keys are GONE from the on-disk file — no shim, no
		// legacy parse path (FR-024).
		reason := "still working"
		require.NoError(t, cold.SetMeta(sid, MetaPatch{GoalLatestReason: &reason}))
		raw, err := os.ReadFile(goalPath)
		require.NoError(t, err)
		var onDisk map[string]any
		require.NoError(t, json.Unmarshal(raw, &onDisk))
		if _, ok := onDisk["goal_pending"]; ok {
			t.Fatal("goal_pending must be absent from goal.json after the next write")
		}
		if _, ok := onDisk["goal_clarification"]; ok {
			t.Fatal("goal_clarification must be absent from goal.json after the next write")
		}
		// The legitimate record must still be there post-write.
		if onDisk["goal_condition"] != "the feature ships" {
			t.Fatalf("goal_condition on disk = %v, want the pre-upgrade condition preserved", onDisk["goal_condition"])
		}
	})

	t.Run("new_fields_round_trip", func(t *testing.T) {
		store := newTestStore(t)
		meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
		require.NoError(t, err)
		sid := meta.ID

		rounds := 1
		pushes := 2
		ch, chatID, sk, ag := "telegram", "chat-42", "sk-1", "agent-1"
		require.NoError(t, store.SetMeta(sid, MetaPatch{
			GoalQuestionRoundsUsed: &rounds,
			GoalZeroOutputPushes:   &pushes,
			GoalRouteChannel:       &ch,
			GoalRouteChatID:        &chatID,
			GoalRouteSessionKey:    &sk,
			GoalRouteAgentID:       &ag,
		}))

		got, err := store.GetMeta(sid)
		require.NoError(t, err)
		if got.GoalQuestionRoundsUsed != 1 || got.GoalZeroOutputPushes != 2 {
			t.Fatalf("round-trip mismatch: rounds_used=%d zero_output_pushes=%d",
				got.GoalQuestionRoundsUsed, got.GoalZeroOutputPushes)
		}
		if got.GoalRouteChannel != "telegram" || got.GoalRouteChatID != "chat-42" ||
			got.GoalRouteSessionKey != "sk-1" || got.GoalRouteAgentID != "agent-1" {
			t.Fatalf("route round-trip mismatch: %+v", got)
		}

		// Cold read confirms it is actually persisted to disk, not just cached.
		cold, err := NewUnifiedStore(store.baseDir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = cold.Close() })
		gotCold, err := cold.GetMeta(sid)
		require.NoError(t, err)
		if gotCold.GoalQuestionRoundsUsed != 1 || gotCold.GoalZeroOutputPushes != 2 {
			t.Fatalf("cold round-trip mismatch: rounds_used=%d zero_output_pushes=%d",
				gotCold.GoalQuestionRoundsUsed, gotCold.GoalZeroOutputPushes)
		}
		if gotCold.GoalRouteChannel != "telegram" || gotCold.GoalRouteChatID != "chat-42" ||
			gotCold.GoalRouteSessionKey != "sk-1" || gotCold.GoalRouteAgentID != "agent-1" {
			t.Fatalf("cold route round-trip mismatch: %+v", gotCold)
		}

		// The on-disk JSON keys match the documented contract (FR-031).
		raw, err := os.ReadFile(filepath.Join(store.baseDir, sid, "goal.json"))
		require.NoError(t, err)
		var onDisk map[string]any
		require.NoError(t, json.Unmarshal(raw, &onDisk))
		for _, key := range []string{
			"goal_question_rounds_used", "goal_zero_output_pushes",
			"goal_route_channel", "goal_route_chat_id", "goal_route_session_key", "goal_route_agent_id",
		} {
			if _, ok := onDisk[key]; !ok {
				t.Fatalf("goal.json missing expected key %q: %v", key, onDisk)
			}
		}
	})
}
