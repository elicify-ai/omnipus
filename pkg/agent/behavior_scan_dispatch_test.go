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
	"time"

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

// --- StartedAt parse handling (sign-off finding 3) --------------------------

// TestJudgeCriteria_BehaviorRung_AttemptScope_ValidStartedAt_UsesRealCutoff
// is the "both branches" positive control for sign-off finding 3: a
// well-formed, RFC3339 Task.StartedAt is parsed and used as the real
// attempt-scope cutoff, so a call recorded BEFORE it (a prior attempt) is
// correctly excluded while a call recorded AT/AFTER it (the current attempt)
// is correctly counted. This is the behavior the malformed-StartedAt sibling
// test below must NOT silently degrade to "everything counts."
func TestJudgeCriteria_BehaviorRung_AttemptScope_ValidStartedAt_UsesRealCutoff(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = behaviorVerifierMustNotBeCalled(t)

	store := al.GetAgentStore("native-agent")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	attemptStart := baseTime.Add(1 * time.Hour)
	// A prior attempt's call, well before attemptStart — must NOT count.
	if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role: "assistant", AgentID: "native-agent", Timestamp: baseTime,
		ToolCalls: []session.ToolCall{{Tool: "web_search", Status: "success"}},
	}); err != nil {
		t.Fatalf("AppendTranscript (prior attempt): %v", err)
	}
	// The current attempt's own call, at-or-after attemptStart — must count.
	if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role: "assistant", AgentID: "native-agent", Timestamp: attemptStart,
		ToolCalls: []session.ToolCall{{Tool: "web_search", Status: "success"}},
	}); err != nil {
		t.Fatalf("AppendTranscript (current attempt): %v", err)
	}

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-valid-started-at", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "x",
		SessionID: meta.ID, StartedAt: attemptStart.Format(time.RFC3339),
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("task Create: %v", err)
	}

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          tk.ID,
		AssigneeAgentID: "native-agent",
		Criteria: []task.AcceptanceCriterion{{
			ID: "c1", Kind: task.KindBehavior,
			Behavior: &task.CriterionBehavior{Tool: "web_search", MinCount: intPtr(1), Scope: task.BehaviorScopeAttempt},
			Author:   task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
		}},
		Attempt: 2,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if !result.Verdict.Met {
		t.Fatalf("expected Met=true (exactly 1 call at-or-after the real StartedAt cutoff, min_count=1), got %+v",
			result.Verdict.PerCriterion)
	}
	if !strings.Contains(result.Verdict.PerCriterion[0].Reason, "observed 1") {
		t.Errorf("reason = %q, want it to report exactly 1 observed call (the prior-attempt call must be excluded)",
			result.Verdict.PerCriterion[0].Reason)
	}
}

// TestJudgeCriteria_BehaviorRung_AttemptScope_MalformedStartedAt_FailsClosed
// is the negative control (sign-off finding 3): when Task.StartedAt is set
// but fails to parse as RFC3339, an attempt-scoped criterion must NOT
// silently widen to whole-session counting (ScanBehaviorCriterionEntries's
// own doc comment: passing the zero time.Time for scope=="attempt"
// "degenerates to everything counts, equivalent to task_session"). Instead
// runBehaviorScan must fail the attempt scope CLOSED: even though the
// session has 5 successful calls of the criterion's tool (which would
// trivially satisfy min_count=1 under the old zero-time/whole-session
// degradation), the malformed cutoff must make the criterion come back
// UNMET, because none of those calls can be proven to belong to the CURRENT
// attempt.
func TestJudgeCriteria_BehaviorRung_AttemptScope_MalformedStartedAt_FailsClosed(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = behaviorVerifierMustNotBeCalled(t)

	store := al.GetAgentStore("native-agent")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// 5 successful calls, all comfortably satisfying min_count=1 under the
	// OLD (buggy) whole-session widening — proving the fix actually changed
	// the outcome, not just the log line.
	for i := 0; i < 5; i++ {
		if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
			Role: "assistant", AgentID: "native-agent", Timestamp: baseTime.Add(time.Duration(i) * time.Second),
			ToolCalls: []session.ToolCall{{Tool: "web_search", Status: "success"}},
		}); err != nil {
			t.Fatalf("AppendTranscript: %v", err)
		}
	}

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-malformed-started-at", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "x",
		SessionID: meta.ID, StartedAt: "not-a-valid-rfc3339-timestamp",
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("task Create: %v", err)
	}

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          tk.ID,
		AssigneeAgentID: "native-agent",
		Criteria: []task.AcceptanceCriterion{{
			ID: "c1", Kind: task.KindBehavior,
			Behavior: &task.CriterionBehavior{Tool: "web_search", MinCount: intPtr(1), Scope: task.BehaviorScopeAttempt},
			Author:   task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
		}},
		Attempt: 1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if result.Verdict.Met {
		t.Fatalf(
			"a malformed Task.StartedAt must fail the attempt scope CLOSED (unmet), never silently widen to "+
				"whole-session and pass — got Met=true with per-criterion=%+v",
			result.Verdict.PerCriterion,
		)
	}
	if !strings.Contains(result.Verdict.PerCriterion[0].Reason, "observed 0") {
		t.Errorf("reason = %q, want it to report 0 observed calls (fail-closed, not the whole session's 5)",
			result.Verdict.PerCriterion[0].Reason)
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
