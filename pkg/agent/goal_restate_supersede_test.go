// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_restate_supersede_test.go reproduces UAT E-1: a second `/goal` issued
// while the first goal was still ACTIVE overwrote the record's prompt but left
// the definition, criteria and Definition of Done compiled for the first
// intent. The agent worked the new intent without re-registering, claimed, and
// the Judge adjudicated ONLY the stale ladder. The rule under test (ADR-081
// D1/US-5, work-first-goal-flow-spec round-2 M-7): a restate keeps the goal id
// and replaces the working prompt, and the record must never describe two
// different pieces of work.
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
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

const (
	e1AlphaIntent    = "write a file named l4e1-alpha.txt containing the word ALPHA"
	e1BetaIntent     = "write a file named l4e1-beta.txt containing the word BETA"
	e1AlphaCriterion = "A file named l4e1-alpha.txt exists and contains the word ALPHA."
)

// activateRegisteredAlphaGoal drives `/goal <alpha>` and registers an alpha
// ladder on the record, exactly the state UAT E-1's first turn left behind.
func activateRegisteredAlphaGoal(t *testing.T, al *AgentLoop, agentInst *AgentInstance) (processOptions, *goal.Goal) {
	t.Helper()
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal " + e1AlphaIntent, UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	g := seedCriteriaOntoActiveGoal(t, sid, e1AlphaIntent,
		[]task.AcceptanceCriterion{terminalTestCriterion(e1AlphaCriterion)})
	registered, err := goal.NewStore(config.OmnipusHomeDir()).Update(g.GoalID, func(cur *goal.Goal) error {
		cur.Definition = "Produce l4e1-alpha.txt whose content is the word ALPHA."
		return nil
	})
	if err != nil {
		t.Fatalf("fixture: set definition: %v", err)
	}
	return opts, registered
}

func TestGoalRestate_SupersedesTheRegisteredRecord_UATE1(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &noCallProvider{t: t}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	opts, before := activateRegisteredAlphaGoal(t, al, agentInst)
	sid := opts.TranscriptSessionID

	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal " + e1BetaIntent, UserInitiated: true}, agentInst, &opts)
	if !matched || handled || reply != "" {
		t.Fatalf("restate: matched=%v handled=%v reply=%q, want a same-turn restate (true,false,\"\")", matched, handled, reply)
	}
	if opts.UserMessage != e1BetaIntent {
		t.Fatalf("working prompt = %q, want the restated intent", opts.UserMessage)
	}

	after := goalRecordForSession(t, sid)
	if after.GoalID != before.GoalID {
		t.Fatalf("a restate must keep the goal id (round-2 M-7): got %q, want %q", after.GoalID, before.GoalID)
	}
	if after.Prompt != e1BetaIntent {
		t.Fatalf("Prompt = %q, want %q", after.Prompt, e1BetaIntent)
	}

	// The record must describe ONE piece of work: nothing compiled for alpha
	// may remain live on it.
	live := after.Definition + "\n"
	for _, c := range append(append([]task.AcceptanceCriterion{}, after.Criteria...), after.DoD...) {
		live += c.Text + "\n"
	}
	if strings.Contains(strings.ToLower(live), "alpha") {
		t.Fatalf("the restated record still carries the previous intent's compiled definition/criteria:\n%s", live)
	}
	if len(after.Criteria) != 0 {
		t.Fatalf("Criteria = %+v, want empty until the agent registers a record for the new intent", after.Criteria)
	}

	// The Judge is given the NEW intent, never the stale ladder.
	judged := compiledGoalCriteriaFor(goalRecordCompiledJSON(after), after.Prompt, sid)
	if len(judged) != 1 || judged[0].Text != e1BetaIntent {
		t.Fatalf("judged set = %+v, want exactly the restated intent %q", judged, e1BetaIntent)
	}

	// set_goal can register a fresh record (the seam reports no record).
	if _, cond, recJSON, err := (agentLoopGoalRecordAccess{al: al}).ReadGoalState(sid); err != nil || cond != e1BetaIntent || recJSON != "" {
		t.Fatalf("ReadGoalState = (cond=%q, record=%q, err=%v), want the new condition and no registered record", cond, recJSON, err)
	}

	// The alpha ladder is history, not erased.
	if len(after.SupersededCriteria) != 1 || len(after.SupersededCriteria[0].Criteria) != 1 ||
		after.SupersededCriteria[0].Criteria[0].Text != e1AlphaCriterion {
		t.Fatalf("SupersededCriteria = %+v, want the alpha ladder retained as one history entry", after.SupersededCriteria)
	}
}

// TestGoalAdjudication_DiscardsAVerdictForARestatedGoal covers the race the
// restate fix opens: a claim against the alpha ladder is already being judged
// when the operator restates the goal. The verdict judged work the record no
// longer describes and must not land — above all it must not end the NEW goal
// `met`.
func TestGoalAdjudication_DiscardsAVerdictForARestatedGoal(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	opts, g := activateRegisteredAlphaGoal(t, al, agentInst)
	sid := opts.TranscriptSessionID

	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(n int) (*providers.LLMResponse, error) {
		if n == 1 {
			// The restate lands while the Judge is mid-call.
			if _, err := goal.NewStore(config.OmnipusHomeDir()).Update(g.GoalID, func(cur *goal.Goal) error {
				_, rerr := cur.Restate(e1BetaIntent, newFloorDoD(), time.Now().UTC())
				return rerr
			}); err != nil {
				t.Errorf("fixture: restate during judging: %v", err)
			}
		}
		return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(true, "alpha file present")}, nil
	}}

	c, cleanup := newEventCollector(t, al)
	defer cleanup()
	result := &turnResult{finalContent: "[goal:evidence] wrote l4e1-alpha.txt\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)
	if result.goalDeferredAdjudication == nil {
		t.Fatal("precondition: the evidenced met claim must record a deferred adjudication")
	}
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)
	cleanup()

	after := readGoalRecord(t, g.GoalID)
	if after.State != generated.GoalStateActive {
		t.Fatalf("state = %q, want active — a verdict about the superseded alpha ladder must not end the restated goal (terminal reason %q)",
			after.State, after.TerminalReason)
	}
	if after.LatestVerdict != nil || after.Round != 0 {
		t.Fatalf("verdict recorded / round consumed for a superseded definition: verdict=%v round=%d", after.LatestVerdict, after.Round)
	}
	if after.Prompt != e1BetaIntent || len(after.Criteria) != 0 {
		t.Fatalf("record = prompt %q criteria %+v, want the restated recordless goal untouched by the stale verdict", after.Prompt, after.Criteria)
	}
	if n := len(terminalPayloads(goalStatusPayloadsFor(c, sid))); n != 0 {
		t.Fatalf("%d terminal goal-status frame(s) emitted, want 0", n)
	}
}
