// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_outcome_adr084_test.go's name and its citation in the joint
// delivery plan's test census (JUDGE-FR-014, FR-018, FR-062) trace to the
// judge spec's revision-2 three-state Outcome design. ADR-084 revision 9 +
// operator decision D-B + plan resolution C-02/D-H retire that design in
// full: there is no Outcome field, Met stays a bool, and a weak quote never
// rewrites a verdict (see judge_outcome_parse_adr084_test.go for that
// guarantee directly). This file's SURVIVING, adapted coverage is:
//
//   - JUDGE-FR-018: a criterion classified through noteNonVerdict's
//     persistently-blocked path is genuinely scored (unmet, a real
//     verdict) — and this wave's new couldNotVerifyIDs threading correctly
//     carries that classification all the way into
//     JudgeCriteriaResult.Reason as "could not verify", never "unmet"
//     (JUDGE-FR-022's actual worker-facing guarantee, proven end to end
//     here rather than only at the summarizeVerdict unit level — see
//     judge_summarize_adr084_test.go for that).
//   - JUDGE-FR-014: a criterion's evidence must not be gradable from a
//     session outside the adjudicated unit's own scope — proven here as
//     resolveVerifierSessionScope returning a scope that contains the
//     unit's own session and excludes an unrelated one (the descendant-
//     inclusion half of D1a is proven separately in
//     verifier_scope_descendants_adr084_test.go).
//
// JUDGE-FR-062 (the rubric's own wording about a nonexistant-path
// criterion) is RUBRIC CONTENT — coreagent.JudgeDefaultRubric, wave E11's
// file — and is out of this engine wave's scope; not implemented here.
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestJudgeCriteria_PersistentlyBlockedCheck_ReasonSaysCouldNotVerify is
// JUDGE-FR-018 + FR-022 proven end to end: a machine check whose bash
// policy is denied (the MECHANISM cannot run) is withheld K times, then —
// on the K+1th consecutive occurrence — genuinely scored unmet, and the
// OVERALL JudgeCriteriaResult.Reason a caller (task_executor.go,
// goal_triggers.go) actually forwards to the worker must say "could not
// verify", never "unmet criteria".
func TestJudgeCriteria_PersistentlyBlockedCheck_ReasonSaysCouldNotVerify(t *testing.T) {
	const k = 1
	SetVerifierUnableToVerifyTracker(NewUnableToVerifyTracker(k))
	SetVerifierUnjudgeableEscalationGate(NewUnjudgeableEscalationGate())
	t.Cleanup(func() {
		SetVerifierUnableToVerifyTracker(NewUnableToVerifyTracker(UnableToVerifyMaxRerunsDefault))
		SetVerifierUnjudgeableEscalationGate(NewUnjudgeableEscalationGate())
	})

	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	workerInst.Tools.RegisterReplacing(&fakeBashTool{result: &tools.ToolResult{ForLLM: "ok"}})
	// Deliberately NOT calling allowBashPolicy: no policy entry resolves to
	// deny (Constraint #6), so the check's MECHANISM cannot run at all.

	in := JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-cnv",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "echo ok", 0)},
		Attempt:         1,
	}
	// Call 1: withheld (count == k), not yet persistently blocked.
	if r := al.JudgeCriteria(context.Background(), in); !r.Unavailable {
		t.Fatalf("first call: want Unavailable (withheld), got verdict met=%v", r.Verdict.Met)
	}
	// Call 2: count > k → persistently-blocked → scored.
	final := al.JudgeCriteria(context.Background(), in)
	if final.Unavailable {
		t.Fatal("persistently-blocked check must be scored (a real verdict), not stay Unavailable")
	}
	if final.Verdict.Met {
		t.Fatal("persistently-blocked criterion must resolve unmet")
	}
	if !strings.Contains(final.Reason, "could not verify: c1") {
		t.Errorf("JudgeCriteriaResult.Reason must say \"could not verify: c1\", got %q", final.Reason)
	}
	if strings.Contains(final.Reason, "unmet criteria: c1") {
		t.Errorf("a persistently-blocked check must NEVER be labelled unmet in the worker-facing reason: %q", final.Reason)
	}
}

// TestResolveVerifierSessionScope_TaskScope_ExcludesUnrelatedSession is
// JUDGE-FR-014's negative guarantee: a task-scope adjudication's
// inspect_session scope must never contain a session that has no
// relationship (self or descendant) to the adjudicated unit's own session.
func TestResolveVerifierSessionScope_TaskScope_ExcludesUnrelatedSession(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	taskStore := GetTaskStore(al)
	if err := taskStore.Create(&task.Task{
		ID: "t-own", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "own", SessionID: "sess-own",
	}); err != nil {
		t.Fatalf("task Create: %v", err)
	}
	if err := taskStore.Create(&task.Task{
		ID: "t-other", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "other", SessionID: "sess-unrelated",
	}); err != nil {
		t.Fatalf("task Create: %v", err)
	}

	got := al.resolveVerifierSessionScope(JudgeCriteriaInput{Scope: task.VerdictScopeTask, TaskID: "t-own"})
	for _, s := range got {
		if s == "sess-unrelated" {
			t.Fatalf("resolveVerifierSessionScope leaked an unrelated task's session into scope: %v", got)
		}
	}
	found := false
	for _, s := range got {
		if s == "sess-own" {
			found = true
		}
	}
	if !found {
		t.Errorf("resolveVerifierSessionScope must at least contain the unit's own session: %v", got)
	}
}
