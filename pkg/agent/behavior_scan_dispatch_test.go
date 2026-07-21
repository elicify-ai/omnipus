// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// behavior_scan_dispatch_test.go covers runBehaviorScan's JudgeCriteria
// dispatch SEAM (7-reviewer gate item 10) — previously untested: every test
// in behavior_scan_test.go exercises ScanBehaviorCriterionEntries directly
// (the pure scanner), never the al.JudgeCriteria -> runBehaviorScan
// resolution path for each of the three scopes, and never proves the core
// SC-012 guarantee end to end — a `behavior`-kind criterion resolves WITHOUT
// ever dispatching the LLM verifier.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// behaviorVerifierMustNotBeCalled builds a fakeJudgeProvider that fails the
// test outright if the LLM verifier is ever dispatched — the strongest
// possible SC-012 assertion (a behavior-kind criterion must NEVER reach the
// verifier), stronger than a post-hoc callCount()==0 check.
func behaviorVerifierMustNotBeCalled(t *testing.T) *fakeJudgeProvider {
	t.Helper()
	return &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		t.Fatal("SC-012 violation: a pure behavior-kind criterion must never dispatch the LLM verifier")
		return nil, nil
	}}
}

func TestJudgeCriteria_BehaviorRung_TaskScope_Met_NoVerifierDispatch(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = behaviorVerifierMustNotBeCalled(t)

	store := al.GetAgentStore("native-agent")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
			Role: "assistant", AgentID: "native-agent",
			ToolCalls: []session.ToolCall{{Tool: "web_search", Status: "success"}},
		}); err != nil {
			t.Fatalf("AppendTranscript: %v", err)
		}
	}

	taskStore := GetTaskStore(al)
	tk := &task.Task{ID: "t-behavior-met", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "x", SessionID: meta.ID}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("task Create: %v", err)
	}

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          tk.ID,
		AssigneeAgentID: "native-agent",
		Criteria: []task.AcceptanceCriterion{{
			ID: "c1", Kind: task.KindBehavior,
			Behavior: &task.CriterionBehavior{Tool: "web_search", MinCount: intPtr(5), Scope: task.BehaviorScopeTaskSession},
			Author:   task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
		}},
		Attempt: 1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if !result.Verdict.Met {
		t.Fatalf("expected Met=true (5 web_search calls, min_count=5), got %+v", result.Verdict.PerCriterion)
	}
}

func TestJudgeCriteria_BehaviorRung_TaskScope_Unmet_NoVerifierDispatch(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = behaviorVerifierMustNotBeCalled(t)

	store := al.GetAgentStore("native-agent")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// Only 2 of the required 5 calls.
	for i := 0; i < 2; i++ {
		if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
			Role: "assistant", AgentID: "native-agent",
			ToolCalls: []session.ToolCall{{Tool: "web_search", Status: "success"}},
		}); err != nil {
			t.Fatalf("AppendTranscript: %v", err)
		}
	}

	taskStore := GetTaskStore(al)
	tk := &task.Task{ID: "t-behavior-unmet", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "x", SessionID: meta.ID}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("task Create: %v", err)
	}

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          tk.ID,
		AssigneeAgentID: "native-agent",
		Criteria: []task.AcceptanceCriterion{{
			ID: "c1", Kind: task.KindBehavior,
			Behavior: &task.CriterionBehavior{Tool: "web_search", MinCount: intPtr(5), Scope: task.BehaviorScopeTaskSession},
			Author:   task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
		}},
		Attempt: 1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if result.Verdict.Met {
		t.Fatalf("expected Met=false (only 2 of 5 required web_search calls), got %+v", result.Verdict.PerCriterion)
	}
}

func TestJudgeCriteria_BehaviorRung_GoalScope_ViaGoalSessionID(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = behaviorVerifierMustNotBeCalled(t)

	store := al.GetAgentStore("native-agent")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
			Role: "assistant", AgentID: "native-agent",
			ToolCalls: []session.ToolCall{{Tool: "send_message", Status: "success"}},
		}); err != nil {
			t.Fatalf("AppendTranscript: %v", err)
		}
	}

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeGoal,
		AssigneeAgentID: "native-agent",
		GoalSessionID:   meta.ID,
		Criteria: []task.AcceptanceCriterion{{
			ID: "c1", Kind: task.KindBehavior,
			Behavior: &task.CriterionBehavior{Tool: "send_message", MinCount: intPtr(3), Scope: task.BehaviorScopeTaskSession},
			Author:   task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
		}},
		Attempt: 1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if !result.Verdict.Met {
		t.Fatalf("expected Met=true via GoalSessionID (3 send_message calls, min_count=3), got %+v",
			result.Verdict.PerCriterion)
	}
}

func TestJudgeCriteria_BehaviorRung_PlanScope_FailClosedReject(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = behaviorVerifierMustNotBeCalled(t)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopePlan,
		PlanID:          "p1",
		AssigneeAgentID: "native-agent",
		Criteria: []task.AcceptanceCriterion{{
			ID: "c1", Kind: task.KindBehavior,
			Behavior: &task.CriterionBehavior{Tool: "web_search", MinCount: intPtr(1), Scope: task.BehaviorScopeTaskSession},
			Author:   task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
		}},
		Attempt: 1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if result.Verdict.Met {
		t.Fatal("plan-scope behavior criteria have no single session to scan and must fail closed")
	}
	if len(result.Verdict.PerCriterion) != 1 {
		t.Fatalf("perCriterion has %d entries, want 1", len(result.Verdict.PerCriterion))
	}
	if !strings.Contains(result.Verdict.PerCriterion[0].Reason, "not scannable") {
		t.Errorf("reason = %q, want it to explain plan-level behavior criteria are not scannable",
			result.Verdict.PerCriterion[0].Reason)
	}
}

func TestJudgeCriteria_BehaviorRung_NilBehaviorPayload_FailClosed(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = behaviorVerifierMustNotBeCalled(t)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-nil-behavior",
		AssigneeAgentID: "native-agent",
		Criteria: []task.AcceptanceCriterion{{
			ID: "c1", Kind: task.KindBehavior, Behavior: nil,
			Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
		}},
		Attempt: 1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if result.Verdict.Met {
		t.Fatal("a behavior-kind criterion with a nil payload must fail closed")
	}
	if !strings.Contains(result.Verdict.PerCriterion[0].Reason, "missing payload") {
		t.Errorf("reason = %q, want it to explain the missing payload", result.Verdict.PerCriterion[0].Reason)
	}
}

// TestJudgeCriteria_BehaviorRung_UnknownToolGuardTextSurfacesInVerdictReason
// proves res.UnknownTool is not silently dropped between the pure scanner
// (ScanBehaviorCriterionEntries) and the CriterionVerdict runBehaviorScan
// hands back to JudgeCriteria's caller — the bracketed unknown-tool guard
// text must survive into the verdict's own per-criterion Reason.
func TestJudgeCriteria_BehaviorRung_UnknownToolGuardTextSurfacesInVerdictReason(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = behaviorVerifierMustNotBeCalled(t)

	store := al.GetAgentStore("native-agent")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role: "assistant", AgentID: "native-agent",
		ToolCalls: []session.ToolCall{{Tool: "web_search", Status: "success"}},
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	taskStore := GetTaskStore(al)
	tk := &task.Task{ID: "t-unknown-tool", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "x", SessionID: meta.ID}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("task Create: %v", err)
	}

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          tk.ID,
		AssigneeAgentID: "native-agent",
		Criteria: []task.AcceptanceCriterion{{
			ID: "c1", Kind: task.KindBehavior,
			Behavior: &task.CriterionBehavior{Tool: "totally_not_real", MinCount: intPtr(1), Scope: task.BehaviorScopeTaskSession},
			Author:   task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
		}},
		Attempt: 1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if result.Verdict.Met {
		t.Fatal("an unknown tool must fail closed unmet")
	}
	if !strings.Contains(result.Verdict.PerCriterion[0].Reason, "unknown-tool guard") {
		t.Errorf("verdict reason %q must surface the unknown-tool guard text (res.UnknownTool)",
			result.Verdict.PerCriterion[0].Reason)
	}
}
