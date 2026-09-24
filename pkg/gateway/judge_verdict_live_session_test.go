// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_verdict_live_session_test.go pins the gateway half of the live
// judge_verdict thread-card fix: the live judge_verdict frame (the event
// forwarder, EventKindJudgeVerdict) and the frame replayed from the persisted
// `judge_verdict` transcript entry carry the SAME (optional) session_id, so
// the SPA can anchor exactly one thread card per round in addition to the
// GLOBAL ActivityPanel push. Mirrors goal_outcome_frame_test.go's
// live-vs-replay pattern for the sibling goal_outcome frame.
package gateway

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// judgeVerdictLiveFixture is one task-scope MET verdict as the agent persists
// it: the entry is marshalled and read back, exactly like a transcript on
// disk (mirrors goalOutcomeFixture).
func judgeVerdictLiveFixture(t *testing.T) (session.TranscriptEntry, task.JudgeVerdict) {
	t.Helper()
	verdict := task.JudgeVerdict{
		ID:     "verdict-live-1",
		Scope:  task.VerdictScopeTask,
		TaskID: "task-live-1",
		Round:  2,
		Met:    true,
		PerCriterion: []task.CriterionVerdict{
			{CriterionID: "crit-1", Met: true, Reason: "go test passes"},
		},
		Model:        "z-ai/glm-5-turbo",
		JudgedAt:     "2026-09-14T12:05:00Z",
		JudgeAgentID: "judge",
	}
	payload, err := json.Marshal(verdict)
	require.NoError(t, err)
	written := session.TranscriptEntry{
		ID:      "task-live-1-judge-2",
		Type:    session.EntryTypeJudgeVerdict,
		Role:    "system",
		Content: string(payload),
		AgentID: verdict.JudgeAgentID,
	}
	raw, err := json.Marshal(written)
	require.NoError(t, err)
	var readBack session.TranscriptEntry
	require.NoError(t, json.Unmarshal(raw, &readBack))
	return readBack, verdict
}

func TestJudgeVerdictFrame_LiveAndReplayCarryTheSameSessionID(t *testing.T) {
	const sessionID = "session_judge_verdict_live"
	entry, verdict := judgeVerdictLiveFixture(t)

	// Live: the real event forwarder turns the agent's event into a frame.
	bus := agent.NewEventBus()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(8)
	done := runForwarder(h, wc, "chat-judge", bus)
	bus.Emit(agent.Event{
		Kind:    agent.EventKindJudgeVerdict,
		Payload: agent.JudgeVerdictPayload{SessionID: sessionID, Verdict: verdict},
	})
	bus.Close()
	<-done
	require.Len(t, ch, 1, "exactly one frame for one verdict")
	liveRaw := <-ch

	// Replay: the persisted entry streamed back.
	sink := &sliceSink{}
	entries := []session.TranscriptEntry{entry}
	_, err := streamReplay(t.Context(), sessionID, entries, computeReplayStats(entries), sink.emit, nil, nil, nil)
	require.NoError(t, err)
	var replayRaw [][]byte
	for _, f := range sink.frames {
		var head struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(f, &head))
		if head.Type == string(generated.WsFrameTypeJudgeVerdict) {
			replayRaw = append(replayRaw, f)
		}
	}
	require.Len(t, replayRaw, 1, "replay must emit exactly one judge_verdict frame for the entry")

	var live, replayed generated.JudgeVerdictFrame
	require.NoError(t, json.Unmarshal(liveRaw, &live))
	require.NoError(t, json.Unmarshal(replayRaw[0], &replayed))
	require.NotNil(t, live.SessionId, "the live frame must carry session_id so the SPA can anchor a thread card")
	require.NotNil(t, replayed.SessionId, "the replayed frame must carry session_id so a reload still shows the card")
	assert.Equal(t, sessionID, *live.SessionId)
	assert.Equal(t, sessionID, *replayed.SessionId)
	assert.JSONEq(t, string(liveRaw), string(replayRaw[0]), "live and replayed frames for one round must be identical")

	// The frame carries the contract's fields verbatim.
	assert.Equal(t, "judge_verdict", replayed.Type)
	assert.Equal(t, "task", replayed.Scope)
	require.NotNil(t, replayed.TaskId)
	assert.Equal(t, "task-live-1", *replayed.TaskId)
	assert.Equal(t, 2, replayed.Round)
	assert.True(t, replayed.Met)
}

// TestJudgeVerdictFrame_PlanScopeOmitsSessionID pins the pre-existing,
// deliberately unchanged panel-only case: a scope=plan verdict (never
// emitted through EventKindJudgeVerdict today — plan_engine.go writes no
// judge_verdict transcript entry) has no single owning chat session, so
// toJudgeVerdictFrame called with "" leaves session_id absent from the wire
// — never present-but-empty, which the SPA's SESSION_SCOPED handling would
// treat differently from "field not sent at all".
func TestJudgeVerdictFrame_PlanScopeOmitsSessionID(t *testing.T) {
	v := task.JudgeVerdict{
		ID: "verdict-plan-1", Scope: task.VerdictScopePlan, PlanID: "plan-1",
		Round: 1, Met: true, Model: "z-ai/glm-5-turbo",
		JudgedAt: "2026-09-14T12:00:00Z", JudgeAgentID: "judge",
	}
	f := toJudgeVerdictFrame("", v)
	assert.Nil(t, f.SessionId)
	raw, err := json.Marshal(f)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"session_id"`,
		"a plan-scope verdict has no owning session — the field must be omitted, not an empty string")
}
