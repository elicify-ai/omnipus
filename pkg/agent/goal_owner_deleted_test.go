// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_owner_deleted_test.go reproduces UAT E-3: a throwaway agent's chat goal
// was parked `blocked`, the agent was deleted, and the goal record stayed
// `state: active` forever while the keeper re-posted it — re-homed onto the
// default agent. The bar: "Goal fails honestly; no orphaned active goal."
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestEndGoalsOfDeletedAgent_EndsOnlyThatAgentsActiveChatGoals_UATE3(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	gs := goal.NewStore(config.OmnipusHomeDir())

	// A — the UAT shape: activated through `/goal` (route records the agent),
	// registered, then parked blocked.
	storeA, sidA := newGoalTestSession(t, al, agentInst.ID)
	optsA := processOptions{
		TranscriptStore: storeA, TranscriptSessionID: sidA,
		Channel: "webchat", ChatID: "cA", SessionKey: "skA", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal write e3-marker.txt", UserInitiated: true}, agentInst, &optsA)
	gA := seedCriteriaOntoActiveGoal(t, sidA, "write e3-marker.txt",
		[]task.AcceptanceCriterion{terminalTestCriterion("e3-marker.txt exists")})
	if _, err := gs.Update(gA.GoalID, func(cur *goal.Goal) error {
		return cur.RecordClaim(generated.GoalLatestClaimStatusBlocked, "", time.Now().UTC())
	}); err != nil {
		t.Fatalf("fixture: record blocked claim: %v", err)
	}
	al.goalSetBlocked(gA.GoalID, true)

	// B — the cold-boot shape: no in-memory route, so the working agent is
	// resolved from the session itself.
	storeB, sidB := newGoalTestSession(t, al, agentInst.ID)
	gidB := activateTestGoalRecord(t, sidB, "summarise the logs")

	// C — another agent's goal: must be left alone.
	_, sidC := newGoalTestSession(t, al, "other-agent")
	gidC := activateTestGoalRecord(t, sidC, "someone else's goal")

	c, cleanup := newEventCollector(t, al)
	defer cleanup()
	ended, err := al.EndGoalsOfDeletedAgent("native-agent", "Native Agent")
	cleanup()
	if err != nil {
		t.Fatalf("EndGoalsOfDeletedAgent: %v", err)
	}
	if ended != 2 {
		t.Fatalf("ended = %d, want 2 (the deleted agent's two active goals)", ended)
	}

	for _, tc := range []struct {
		name, sid, gid string
		wantCriteria   int
	}{
		{name: "A_blocked_registered", sid: sidA, gid: gA.GoalID, wantCriteria: 1},
		{name: "B_cold_boot", sid: sidB, gid: gidB, wantCriteria: 0},
	} {
		after := readGoalRecord(t, tc.gid)
		if after.State != generated.GoalStateCleared {
			t.Errorf("%s: state = %q, want %q — a deleted agent's goal must end, not stay active", tc.name, after.State, generated.GoalStateCleared)
		}
		if !strings.Contains(after.TerminalReason, "deleted") || !strings.Contains(after.TerminalReason, "native-agent") {
			t.Errorf("%s: terminal reason = %q, want it to say the agent native-agent was deleted", tc.name, after.TerminalReason)
		}
		if len(after.Criteria) != tc.wantCriteria {
			t.Errorf("%s: criteria = %d, want %d retained (D9: a transition, never an erasure)", tc.name, len(after.Criteria), tc.wantCriteria)
		}
		if activeGoalForSession(tc.sid) != nil {
			t.Errorf("%s: an active goal is still bound to the session", tc.name)
		}
		terminal := terminalPayloads(goalStatusPayloadsFor(c, tc.sid))
		if len(terminal) != 1 || terminal[0].State != goalPillCleared {
			t.Errorf("%s: terminal frames = %+v, want exactly one %q", tc.name, terminal, goalPillCleared)
		}
	}
	if al.goalIsBlocked(gA.GoalID) {
		t.Error("the blocked park flag survived the goal's end")
	}

	for name, s := range map[string]string{"A": sidA, "B": sidB} {
		store := storeA
		if name == "B" {
			store = storeB
		}
		entries, rerr := store.ReadTranscript(s)
		if rerr != nil {
			t.Fatalf("%s: read transcript: %v", name, rerr)
		}
		var noted bool
		for _, e := range entries {
			if e.Role == "system" && strings.Contains(e.Content, "Native Agent (native-agent)") && strings.Contains(e.Content, "was deleted") {
				noted = true
			}
		}
		if !noted {
			t.Errorf("%s: no system note tells the user the goal ended because its agent was deleted", name)
		}
	}

	if other := readGoalRecord(t, gidC); other.State != generated.GoalStateActive {
		t.Errorf("another agent's goal was ended too: state = %q", other.State)
	}
}

func TestEndGoalsOfDeletedAgent_RequiresAnAgentID(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	if n, err := al.EndGoalsOfDeletedAgent("  ", "x"); err == nil || n != 0 {
		t.Fatalf("EndGoalsOfDeletedAgent(blank) = (%d, %v), want (0, error)", n, err)
	}
}

// TestDispatchGoalAsyncFollowUp_ColdBootRouteRunsAsTheGoalsOwnAgent covers the
// sibling re-homing path: after a restart the keeper's route is rehydrated
// from the goal record, which carries no agent id. Dispatching with an empty
// AgentID made processSystemMessage run the push as the DEFAULT agent.
func TestDispatchGoalAsyncFollowUp_ColdBootRouteRunsAsTheGoalsOwnAgent(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := activateTestGoalRecord(t, sid, "keep going")
	if _, err := goal.NewStore(config.OmnipusHomeDir()).Update(gid, func(cur *goal.Goal) error {
		cur.SetRoute("telegram", "chat-9")
		return nil
	}); err != nil {
		t.Fatalf("fixture: persist route: %v", err)
	}

	al.dispatchGoalAsyncFollowUp(sid, gid, "goal_continue_push", "Keep working on the goal.")

	select {
	case msg := <-al.bus.InboundChan():
		if msg.AsyncOriginAgentID != agentInst.ID {
			t.Fatalf("keeper follow-up AsyncOriginAgentID = %q, want %q — an empty origin runs the goal as the default agent",
				msg.AsyncOriginAgentID, agentInst.ID)
		}
		if msg.AsyncTranscriptSessionID != sid {
			t.Fatalf("AsyncTranscriptSessionID = %q, want %q", msg.AsyncTranscriptSessionID, sid)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no keeper follow-up was published")
	}
}
