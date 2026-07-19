//go:build !cgo

// replay_judge_verdict_test.go — review r2 RV1: streamReplay must not render
// a persisted EntryTypeJudgeVerdict transcript entry as a raw-JSON system
// chat bubble on WS reconnect. It must emit a typed judge_verdict frame
// instead — the same frame type/shape a live push would use, which the SPA
// routes to the activity store (useJudgeActivityStore), never the thread.

package gateway

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// judgeVerdictEntry builds an EntryTypeJudgeVerdict TranscriptEntry with
// Content = json.Marshal(verdict) — mirrors
// task_executor.go's writeJudgeVerdictTranscript / goal_loop.go's
// writeGoalVerdictTranscript exactly.
func judgeVerdictEntry(t *testing.T, verdict task.JudgeVerdict) session.TranscriptEntry {
	t.Helper()
	payload, err := json.Marshal(verdict)
	require.NoError(t, err)
	return session.TranscriptEntry{
		ID:      "judge-entry-1",
		Type:    session.EntryTypeJudgeVerdict,
		Role:    "system",
		Content: string(payload),
		AgentID: verdict.JudgeAgentID,
	}
}

// TestStreamReplay_JudgeVerdict_EmitsTypedFrame_NotRawSystemBubble proves the
// RV1 fix: a judge_verdict transcript entry replays as a typed frame
// (type="judge_verdict") carrying the parsed verdict fields, never as a
// generic replay_message system bubble with raw JSON content.
func TestStreamReplay_JudgeVerdict_EmitsTypedFrame_NotRawSystemBubble(t *testing.T) {
	verdict := task.JudgeVerdict{
		ID:           "verdict-1",
		Scope:        "task",
		TaskID:       "task-abc",
		Round:        2,
		Met:          true,
		Model:        "test-judge-model",
		JudgedAt:     "2026-07-19T00:00:00Z",
		JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{
			{CriterionID: "c1", Met: true, Reason: "looks good"},
		},
	}
	entries := []session.TranscriptEntry{judgeVerdictEntry(t, verdict)}

	frames, n := runReplay(t, entries)
	require.Equal(t, 1, n, "exactly one content frame must be emitted for one judge_verdict entry")
	// runReplay's sink also captures the trailing "done" frame (FR-I-004,
	// emitted outside framesEmitted's count) — exactly 2 frames total.
	require.Len(t, frames, 2)

	f := frames[0]
	assert.Equal(t, "judge_verdict", f.Type, "must emit a typed judge_verdict frame")
	assert.Empty(t, f.Content, "must NOT emit generic replay_message content (no raw-JSON system bubble)")
	assert.Equal(t, "verdict-1", f.ID)
	assert.True(t, f.Met)
	assert.Equal(t, 2, f.Round)
	assert.Equal(t, "judge", f.JudgeAgentID)
	assert.Equal(t, "task-abc", f.TaskID)
	assert.Equal(t, "test-judge-model", f.Model)
	require.Len(t, f.PerCriterion, 1)
	assert.Equal(t, "c1", f.PerCriterion[0]["criterion_id"])
}

// TestStreamReplay_JudgeVerdict_MalformedContent_SkippedNotCrashed proves a
// judge_verdict entry with unparseable Content is skipped (logged, not
// emitted) rather than crashing replay or falling through to a raw bubble.
func TestStreamReplay_JudgeVerdict_MalformedContent_SkippedNotCrashed(t *testing.T) {
	entries := []session.TranscriptEntry{
		{ID: "bad-verdict", Type: session.EntryTypeJudgeVerdict, Role: "system", Content: "not valid json"},
		assistantEntry("hello after the bad entry", "agent-a"),
	}
	frames, n := runReplay(t, entries)
	require.Equal(t, 1, n, "the malformed verdict entry must be skipped, not emitted")
	require.Len(t, frames, 2) // the surviving replay_message + trailing done frame
	assert.Equal(t, "replay_message", frames[0].Type)
	assert.Equal(t, "hello after the bad entry", frames[0].Content)
}
