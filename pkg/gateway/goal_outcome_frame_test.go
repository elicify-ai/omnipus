// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_outcome_frame_test.go pins the gateway half of the goal outcome line
// (founder decision 2026-09-14; contracts/components/schemas/GoalOutcomeFrame.yaml):
// the live goal_outcome frame (the event forwarder) and the frame replayed
// from the persisted `system_subtype: goal_outcome` transcript entry carry the
// SAME message id and the SAME outcome, so the SPA shows one line per ending.
package gateway

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// goalOutcomeFixture is one rounds_exhausted ending as the agent persists it:
// the entry is marshalled and read back, exactly like a transcript on disk.
func goalOutcomeFixture(t *testing.T) (session.TranscriptEntry, generated.GoalOutcome) {
	t.Helper()
	endedAt := time.Date(2026, 9, 14, 6, 29, 31, 829960000, time.UTC)
	reason := "The file is 5 bytes; the criterion also requires exactly 500 bytes."
	total := 2
	outcome := generated.GoalOutcome{
		GoalId:        "goal_01M2F71KTWW6B2ADSGB3XVMS69",
		GoalText:      "write e8-impossible.txt",
		Ending:        generated.GoalOutcomeEndingRoundsExhausted,
		RoundsUsed:    20,
		MaxRounds:     20,
		JudgeReason:   &reason,
		CriteriaTotal: &total,
		EndedAt:       endedAt,
	}
	written := session.TranscriptEntry{
		ID:            fmt.Sprintf("goal-outcome-%s-%d", outcome.GoalId, endedAt.UnixNano()),
		Type:          session.EntryTypeSystem,
		Role:          "system",
		Content:       `Goal "write e8-impossible.txt" did not reach a MET verdict within 20 round(s).`,
		Timestamp:     endedAt,
		SystemSubtype: session.SystemSubtypeGoalOutcome,
		GoalOutcome:   &outcome,
	}
	raw, err := json.Marshal(written)
	require.NoError(t, err)
	var readBack session.TranscriptEntry
	require.NoError(t, json.Unmarshal(raw, &readBack))
	require.NotNil(t, readBack.GoalOutcome, "the outcome must survive the transcript's JSON round trip")
	return readBack, outcome
}

func TestGoalOutcomeFrame_LiveAndReplayCarryTheSameIDAndOutcome(t *testing.T) {
	const sessionID = "session_goal_outcome"
	entry, outcome := goalOutcomeFixture(t)

	// Live: the real EventBus sync tap turns the agent's event into a frame
	// (#823: goal_outcome is produced once by the session hub, not per
	// connection). This tab is on a DIFFERENT session, so it receives the
	// unsequenced broadcast copy (BE-DESIGN.md §1.4) — byte-for-byte what
	// replay emits, which is exactly what this test compares.
	bus := agent.NewEventBus()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(8)
	bus.SetSyncTap(h.hubSyncTap)
	bindTestConnToSession(h, "chat-goal", "some-other-session", wc)
	bus.Emit(agent.Event{
		Kind:    agent.EventKindGoalOutcome,
		Payload: agent.GoalOutcomePayload{SessionID: sessionID, MessageID: entry.ID, Outcome: outcome},
	})
	bus.Close()
	require.Len(t, ch, 1, "exactly one frame for one goal ending")
	liveRaw := <-ch

	// Replay: the persisted entry streamed back.
	sink := &sliceSink{}
	entries := []session.TranscriptEntry{entry}
	_, err := streamReplay(t.Context(), sessionID, entries, computeReplayStats(entries), sink.emit, nil, nil, nil)
	require.NoError(t, err)
	var replayRaw [][]byte
	for _, f := range sink.frames {
		var head struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		require.NoError(t, json.Unmarshal(f, &head))
		if head.Type == string(generated.WsFrameTypeGoalOutcome) {
			replayRaw = append(replayRaw, f)
		}
		assert.NotEqual(t, entry.Content, head.Content,
			"the outcome entry must not ALSO replay as a plain text bubble (%s)", head.Type)
	}
	require.Len(t, replayRaw, 1, "replay must emit exactly one goal_outcome frame for the entry")

	var live, replayed generated.GoalOutcomeFrame
	require.NoError(t, json.Unmarshal(liveRaw, &live))
	require.NoError(t, json.Unmarshal(replayRaw[0], &replayed))
	assert.Equal(t, entry.ID, live.MessageId, "the live frame's message id is the persisted entry's id")
	assert.Equal(t, entry.ID, replayed.MessageId, "the replayed frame's message id is the persisted entry's id")
	assert.Equal(t, sessionID, live.SessionId)
	assert.Equal(t, sessionID, replayed.SessionId)
	assert.JSONEq(t, string(liveRaw), string(replayRaw[0]), "live and replayed frames for one ending must be identical")

	// The frame carries the contract's fields verbatim.
	assert.Equal(t, "goal_outcome", replayed.Type)
	assert.Equal(t, "rounds_exhausted", replayed.Outcome.Ending)
	assert.Equal(t, 20, replayed.Outcome.RoundsUsed)
	assert.Equal(t, 20, replayed.Outcome.MaxRounds)
	require.NotNil(t, replayed.Outcome.JudgeReason)
	assert.Equal(t, *outcome.JudgeReason, *replayed.Outcome.JudgeReason)
	require.NotNil(t, replayed.Outcome.CriteriaTotal)
	assert.Equal(t, 2, *replayed.Outcome.CriteriaTotal)
	ended, perr := time.Parse(time.RFC3339Nano, replayed.Outcome.EndedAt)
	require.NoError(t, perr)
	assert.True(t, ended.Equal(outcome.EndedAt), "ended_at %q must be the terminal transition's instant", replayed.Outcome.EndedAt)
}

func TestGoalOutcomeFrame_OptionalFieldsStayAbsent(t *testing.T) {
	frame := goalOutcomeFrame("s1", "goal-outcome-g-1", generated.GoalOutcome{
		GoalId: "g", GoalText: "stop me", Ending: generated.GoalOutcomeEndingStoppedByUser,
		RoundsUsed: 0, MaxRounds: 20, EndedAt: time.Unix(1, 0).UTC(),
	})
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "judge_reason", "an absent reason is omitted, never a placeholder")
	assert.NotContains(t, string(raw), "criteria_total")
}
