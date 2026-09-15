// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_verdict_live_event_test.go pins the agent half of the live
// judge_verdict thread-card fix: TaskExecutor.writeJudgeVerdictTranscript
// (scope=task) and AgentLoop.writeGoalVerdictTranscript (scope=goal) each
// emit EXACTLY ONE EventKindJudgeVerdict, carrying the session the verdict's
// round belongs to, and ONLY once their transcript entry is durably saved —
// mirroring goal_outcome_test.go's "the live event carries its ID" pattern
// for the sibling goal_outcome event.
package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// judgeVerdictPayloadsFor returns the live judge_verdict events collected by
// c whose SessionID matches sid.
func judgeVerdictPayloadsFor(c *eventCollector, sid string) []JudgeVerdictPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []JudgeVerdictPayload
	for _, e := range c.events {
		if e.Kind != EventKindJudgeVerdict {
			continue
		}
		if p, ok := e.Payload.(JudgeVerdictPayload); ok && p.SessionID == sid {
			out = append(out, p)
		}
	}
	return out
}

func TestWriteGoalVerdictTranscript_EmitsLiveEventWithGoalSessionID(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	meta, err := store.NewSession(session.SessionTypeChat, "", "native-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sessionID := meta.ID
	events, cleanup := newEventCollector(t, al)
	defer cleanup()

	verdict := &task.JudgeVerdict{
		ID: "verdict-goal-1", Scope: task.VerdictScopeGoal, GoalSessionID: sessionID,
		Round: 3, Met: false, JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{{CriterionID: "c1", Met: false, Reason: "not yet"}},
	}
	al.writeGoalVerdictTranscript(store, sessionID, verdict)
	cleanup()

	live := judgeVerdictPayloadsFor(events, sessionID)
	if len(live) != 1 {
		t.Fatalf("%d live judge_verdict events for %q; want exactly 1", len(live), sessionID)
	}
	if live[0].Verdict.ID != verdict.ID {
		t.Errorf("live event verdict id = %q; want %q", live[0].Verdict.ID, verdict.ID)
	}
	if live[0].Verdict.Round != 3 {
		t.Errorf("live event verdict round = %d; want 3", live[0].Verdict.Round)
	}
}
