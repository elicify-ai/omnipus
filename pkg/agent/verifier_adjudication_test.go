// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_adjudication_test.go covers ADR-052's judge->real-agent
// conversion at the seam level: the verifier-session registry (FR-037), the
// transcript-window feed (FR-032), the lazily-materialized verifier soul
// (FR-038), and memory-off ContextBuilder gating (FR-039). End-to-end
// fail-closed/D7-unavailable coverage through JudgeCriteria itself already
// lives in judge_test.go (TestJudge_ProseJudge_*, TestJudge_Unavailable_*)
// and continues to pass unmodified against the new runVerifierAdjudication
// internals — proof the conversion preserved JudgeCriteria's contract.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- verifierUnitID ---------------------------------------------------------

func TestVerifierUnitID_PrefersTaskThenPlanThenAgentFallback(t *testing.T) {
	if got := verifierUnitID(JudgeCriteriaInput{TaskID: "t1", PlanID: "p1"}); got != "task:t1" {
		t.Errorf("TaskID must win when both set, got %q", got)
	}
	if got := verifierUnitID(JudgeCriteriaInput{PlanID: "p1"}); got != "plan:p1" {
		t.Errorf("PlanID must be used when TaskID is empty, got %q", got)
	}
	if got := verifierUnitID(JudgeCriteriaInput{AssigneeAgentID: "mia"}); got != "goal-agent:mia" {
		t.Errorf("goal-scope (no TaskID/PlanID) must fall back to a per-agent key, got %q", got)
	}
}

// TestVerifierUnitID_GoalScopeUsesSessionIDKey covers verifierUnitID's
// primary goal-scope branch (GoalSessionID set) — previously untested
// (this wave's own comment sweep, item 12). This is the key /goal clear
// looks up to cancel an in-flight goal verifier (FR-037).
func TestVerifierUnitID_GoalScopeUsesSessionIDKey(t *testing.T) {
	got := verifierUnitID(JudgeCriteriaInput{GoalSessionID: "sess-123"})
	if want := "goal:sess-123"; got != want {
		t.Errorf("verifierUnitID(goal scope) = %q, want %q", got, want)
	}
	if got, want := verifierUnitID(JudgeCriteriaInput{TaskID: "t1", GoalSessionID: "sess-123"}), "task:t1"; got != want {
		t.Errorf("TaskID must win over GoalSessionID when both are somehow set, got %q want %q", got, want)
	}
}

// TestVerifierUnitID_MatchesSharedKeyHelpers pins verifierUnitID to the
// SAME shared helpers (verifier_registry.go) plan_engine.go's Stop fan-out
// uses — the F1 regression this wave closes was exactly the two sides
// disagreeing on this scheme.
func TestVerifierUnitID_MatchesSharedKeyHelpers(t *testing.T) {
	if got, want := verifierUnitID(JudgeCriteriaInput{TaskID: "t1"}), verifierUnitForTask("t1"); got != want {
		t.Errorf("task-scope key = %q, want it to equal verifierUnitForTask(...) = %q", got, want)
	}
	if got, want := verifierUnitID(JudgeCriteriaInput{PlanID: "p1"}), verifierUnitForPlan("p1"); got != want {
		t.Errorf("plan-scope key = %q, want it to equal verifierUnitForPlan(...) = %q", got, want)
	}
	if got, want := verifierUnitID(JudgeCriteriaInput{GoalSessionID: "s1"}), verifierUnitForGoal("s1"); got != want {
		t.Errorf("goal-scope key = %q, want it to equal verifierUnitForGoal(...) = %q", got, want)
	}
}

// --- verifier-session registry (FR-037) -------------------------------------

func TestVerifierSessionRegistry_DefaultRegisterUnregister(t *testing.T) {
	r := NewVerifierSessionRegistry()
	if _, ok := r.Lookup("task:t1"); ok {
		t.Fatal("a fresh registry must have no entries")
	}
	r.Register("task:t1", "sess-1")
	got, ok := r.Lookup("task:t1")
	if !ok || got != "sess-1" {
		t.Fatalf("Lookup after Register = (%q, %v), want (sess-1, true)", got, ok)
	}
	r.Unregister("task:t1")
	if _, ok := r.Lookup("task:t1"); ok {
		t.Fatal("Unregister must remove the entry")
	}
}

func TestVerifierSessionRegistry_SetOverrideAndRestoreDefault(t *testing.T) {
	spy := NewVerifierSessionRegistry()
	SetVerifierSessionRegistry(spy)
	t.Cleanup(func() { SetVerifierSessionRegistry(nil) })

	if currentVerifierSessionRegistry() != VerifierSessionPublisher(spy) {
		t.Fatal("currentVerifierSessionRegistry must return the overridden registry")
	}

	// Passing nil must restore a fresh, usable default, never leave the
	// package with no registry at all.
	SetVerifierSessionRegistry(nil)
	if currentVerifierSessionRegistry() == nil {
		t.Fatal("SetVerifierSessionRegistry(nil) must install a non-nil default")
	}
	currentVerifierSessionRegistry().Register("x", "y") // must not panic
}

// spyRegistry records Register/Unregister calls with call order, so a test
// can assert registration happened BEFORE the verifier turn was dispatched
// (FR-011 step 2 / FR-037's synchronous-assignment rule).
type spyRegistry struct {
	registerCalls   []struct{ unitID, sessionID string }
	unregisterCalls []string
}

func (s *spyRegistry) Register(unitID, sessionID string) error {
	s.registerCalls = append(s.registerCalls, struct{ unitID, sessionID string }{unitID, sessionID})
	return nil
}

func (s *spyRegistry) Unregister(unitID string) {
	s.unregisterCalls = append(s.unregisterCalls, unitID)
}

// TestRunVerifierAdjudication_RegistersSessionBeforeDispatch proves
// runVerifierAdjudication (a) registers a non-empty session id for the
// task-scoped unitID BEFORE the verifier LLM call fires, and (b)
// unregisters it once the call completes — the exact sequencing the
// plan-Stop fan-out (owned by a different wave) depends on to never miss an
// in-flight verifier session.
func TestRunVerifierAdjudication_RegistersSessionBeforeDispatch(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	spy := &spyRegistry{}
	SetVerifierSessionRegistry(spy)
	t.Cleanup(func() { SetVerifierSessionRegistry(nil) })

	var registeredBeforeCall bool
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		registeredBeforeCall = len(spy.registerCalls) == 1
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-registry",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
		ClaimText:       "done",
	})
	if result.Unavailable || !result.Verdict.Met {
		t.Fatalf("unexpected result: %+v", result)
	}

	if !registeredBeforeCall {
		t.Fatal("Register must be called BEFORE the verifier LLM call dispatches (FR-037)")
	}
	if len(spy.registerCalls) != 1 {
		t.Fatalf("Register called %d times, want 1", len(spy.registerCalls))
	}
	if spy.registerCalls[0].unitID != "task:t-registry" {
		t.Errorf("registered unitID = %q, want %q", spy.registerCalls[0].unitID, "task:t-registry")
	}
	if spy.registerCalls[0].sessionID == "" {
		t.Error("registered sessionID must not be empty")
	}
	// The registered id is the turn's transcriptSessionID — the pre-created
	// verifier session's meta.ID — because that is the only identifier
	// RequestCancelForSession matches on (the cancel-key contract; the
	// retired "agent:judge:verify:" sessionKey shape is exactly the value
	// cancel could never reach). With the Judge registered in this harness,
	// it must resolve to a real, verifier-typed session in the Judge's store.
	if strings.Contains(spy.registerCalls[0].sessionID, "agent:judge:verify:") {
		t.Errorf("sessionID = %q carries the retired sessionKey shape — cancel cannot match it", spy.registerCalls[0].sessionID)
	}
	judgeStore := al.GetAgentStore(string(coreagent.IDJudge))
	if judgeStore == nil {
		t.Fatal("judge session store not available")
	}
	meta, err := judgeStore.GetMeta(spy.registerCalls[0].sessionID)
	if err != nil {
		t.Fatalf("registered sessionID %q does not resolve to a real session: %v", spy.registerCalls[0].sessionID, err)
	}
	if meta.Type != session.SessionTypeVerifier {
		t.Errorf("registered session Type = %q, want %q", meta.Type, session.SessionTypeVerifier)
	}
	if len(spy.unregisterCalls) != 1 || spy.unregisterCalls[0] != "task:t-registry" {
		t.Errorf("Unregister calls = %v, want exactly one for task:t-registry", spy.unregisterCalls)
	}
}

// --- window feed (FR-032) ---------------------------------------------------

func TestVerifierWindowFeed_RenderSkipsDelegateChildEntries(t *testing.T) {
	entries := []session.TranscriptEntry{
		{Role: "user", Content: "top-level question"},
		{Role: "assistant", Content: "delegated narration", ParentSpawnCallID: "call-1"},
		{Role: "assistant", Content: "top-level answer"},
	}
	msgs := renderTranscriptEntriesForWindow(entries)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (delegate-child entry must be skipped): %+v", len(msgs), msgs)
	}
	if msgs[0].Content != "top-level question" || msgs[1].Content != "top-level answer" {
		t.Errorf("unexpected messages: %+v", msgs)
	}
}

func TestVerifierWindowFeed_RenderIncludesToolCallSummaries(t *testing.T) {
	entries := []session.TranscriptEntry{
		{
			Role:    "assistant",
			Content: "searching now",
			ToolCalls: []session.ToolCall{
				{Tool: "web_search", Status: "success"},
			},
		},
	}
	msgs := renderTranscriptEntriesForWindow(entries)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (content line + tool-call summary): %+v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[1].Content, "web_search") || !strings.Contains(msgs[1].Content, "success") {
		t.Errorf("tool-call summary missing expected fields: %q", msgs[1].Content)
	}
}

func TestVerifierWindowFeed_TrimsToTokenBudgetKeepingTail(t *testing.T) {
	entries := []session.TranscriptEntry{
		{Role: "user", Content: strings.Repeat("a", 2000)},
		{Role: "assistant", Content: strings.Repeat("b", 2000)},
		{Role: "user", Content: "recent-and-short"},
	}
	// A tiny budget can only fit the LAST message.
	got := renderVerifierWindowText(entries, 10)
	if strings.Contains(got, "aaaa") || strings.Contains(got, "bbbb") {
		t.Errorf("trimmed window must drop the older, larger messages: %q", got)
	}
	if !strings.Contains(got, "recent-and-short") {
		t.Errorf("trimmed window must retain the most recent message: %q", got)
	}
}

func TestVerifierWindowFeed_EmptyEntriesReturnsEmptyString(t *testing.T) {
	if got := renderVerifierWindowText(nil, 20000); got != "" {
		t.Errorf("empty entries must render \"\", got %q", got)
	}
}

func TestVerifierWindowFeed_PlanScope_ReturnsEmptyComposition(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	got := al.resolveVerifierWindowText(JudgeCriteriaInput{Scope: task.VerdictScopePlan, PlanID: "p1"})
	if got != "" {
		t.Errorf("plan scope must return \"\" (GS-04: no plan session; ExtraContext/ClaimText already "+
			"carry the structured composition), got %q", got)
	}
}

// TestVerifierWindowFeed_GoalScope_ReturnsEmptyWhenNoSessionID covers the
// defensive case: a goal-scope input with no GoalSessionID set (production
// callers — goal_loop.go's checkGoalLoopAfterTurn — always set it) must
// still degrade to an empty window, never panic or error.
func TestVerifierWindowFeed_GoalScope_ReturnsEmptyWhenNoSessionID(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	got := al.resolveVerifierWindowText(JudgeCriteriaInput{Scope: task.VerdictScopeGoal, AssigneeAgentID: "native-agent"})
	if got != "" {
		t.Errorf("goal scope with no GoalSessionID must return \"\", got %q", got)
	}
}

// TestVerifierWindowFeed_GoalScope_ReadsSessionTail proves the goal-scope
// window is read from the REAL chat session carrying the goal condition
// (goalSessionWindowText -> sessionWindowText -> UnifiedStore.ReadTranscript)
// — the positive counterpart to the empty-input case above, mirroring
// TestVerifierWindowFeed_TaskScope_ReadsSessionTail for the goal scope.
func TestVerifierWindowFeed_GoalScope_ReadsSessionTail(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("native-agent must have a UnifiedStore session store")
	}
	meta, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role: "user", Content: "run 5 web searches", AgentID: "native-agent",
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}
	if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role: "assistant", Content: "done, 5 searches completed", AgentID: "native-agent",
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	got := al.resolveVerifierWindowText(JudgeCriteriaInput{
		Scope: task.VerdictScopeGoal, AssigneeAgentID: "native-agent", GoalSessionID: meta.ID,
	})
	if !strings.Contains(got, "run 5 web searches") || !strings.Contains(got, "5 searches completed") {
		t.Errorf("goal-scope window must contain the goal session's real transcript, got %q", got)
	}
}

// TestVerifierWindowFeed_TaskScope_ReadsSessionTail proves the task-scope
// window is read from the task's OWN working session via the assignee
// agent's session store (the PartitionStore read path,
// UnifiedStore.ReadTranscript) — not fabricated, not empty when a real
// session with content exists.
func TestVerifierWindowFeed_TaskScope_ReadsSessionTail(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("native-agent must have a UnifiedStore session store")
	}
	meta, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role: "user", Content: "please run 5 web searches", AgentID: "native-agent",
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}
	if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role: "assistant", Content: "done, found 5 results", AgentID: "native-agent",
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	taskStore := GetTaskStore(al)
	if taskStore == nil {
		t.Fatal("AgentLoop must carry a task store")
	}
	tk := &task.Task{
		ID: "t-window", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "window test", SessionID: meta.ID,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("task Create: %v", err)
	}

	got := al.taskSessionWindowText("t-window", "native-agent")
	if !strings.Contains(got, "please run 5 web searches") || !strings.Contains(got, "found 5 results") {
		t.Errorf("task-scope window must contain the task session's transcript, got %q", got)
	}
}

func TestVerifierWindowFeed_TaskScope_MissingSessionReturnsEmpty(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	taskStore := GetTaskStore(al)
	tk := &task.Task{ID: "t-nosession", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "no session"}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("task Create: %v", err)
	}
	if got := al.taskSessionWindowText("t-nosession", "native-agent"); got != "" {
		t.Errorf("a task with no SessionID must yield an empty window (best-effort, never an error), got %q", got)
	}
	if got := al.taskSessionWindowText("does-not-exist", "native-agent"); got != "" {
		t.Errorf("an unresolvable task id must yield an empty window, got %q", got)
	}
}

// --- inspect_session target scope (FR-033/R3-10/R3-11, item 8) -------------

func TestResolveVerifierSessionScope_TaskScope(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-scope", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "scope test", SessionID: "sess-t",
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("task Create: %v", err)
	}
	got := al.resolveVerifierSessionScope(JudgeCriteriaInput{Scope: task.VerdictScopeTask, TaskID: "t-scope"})
	if len(got) != 1 || got[0] != "sess-t" {
		t.Errorf("task scope = %v, want [sess-t]", got)
	}
}

func TestResolveVerifierSessionScope_PlanScope_MemberSessionsOnly(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	taskStore := GetTaskStore(al)
	for _, tk := range []*task.Task{
		{ID: "m1", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "m1", PlanID: "p1", SessionID: "sess-m1"},
		{ID: "m2", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "m2", PlanID: "p1", SessionID: "sess-m2"},
		{ID: "other", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "other", PlanID: "p2", SessionID: "sess-other"},
	} {
		if err := taskStore.Create(tk); err != nil {
			t.Fatalf("task Create %s: %v", tk.ID, err)
		}
	}
	got := al.resolveVerifierSessionScope(JudgeCriteriaInput{Scope: task.VerdictScopePlan, PlanID: "p1"})
	want := map[string]bool{"sess-m1": true, "sess-m2": true}
	if len(got) != 2 {
		t.Fatalf("plan scope = %v, want exactly 2 sessions (p1's members only)", got)
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("plan scope leaked a non-p1-member session: %q (got=%v)", s, got)
		}
	}
}

func TestResolveVerifierSessionScope_GoalScope(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	got := al.resolveVerifierSessionScope(JudgeCriteriaInput{Scope: task.VerdictScopeGoal, GoalSessionID: "goal-sess"})
	if len(got) != 1 || got[0] != "goal-sess" {
		t.Errorf("goal scope = %v, want [goal-sess]", got)
	}
}

// TestRunVerifierAdjudication_PlumbsVerifierSessionScopeOntoTurnCtx proves the
// ctx runVerifierAdjudication dispatches the verifier's turn with actually
// carries the FR-033 target-session allowlist — end to end through
// processTaskDirect into the Judge's own LLM call — not just that
// resolveVerifierSessionScope computes the right list in isolation.
func TestRunVerifierAdjudication_PlumbsVerifierSessionScopeOntoTurnCtx(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-ctx-scope", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "x", SessionID: "sess-under-review",
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("task Create: %v", err)
	}

	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-ctx-scope",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
		ClaimText:       "done",
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}

	capturedCtx := fake.capturedCtx()
	if capturedCtx == nil {
		t.Fatal("the verifier's LLM call must have received a ctx")
	}
	if !tools.VerifierSessionScopeAllows(capturedCtx, "sess-under-review") {
		t.Error("the verifier turn's ctx must authorize the task's own session via WithVerifierSessionScope")
	}
	if tools.VerifierSessionScopeAllows(capturedCtx, "some-other-session") {
		t.Error("the verifier turn's ctx must NOT authorize a session outside the resolved scope")
	}
}

// --- verifier verdict dedup (any-unmet-wins, item 6) ------------------------

func TestDedupeJudgeCriteriaAnyUnmetWins(t *testing.T) {
	cases := []struct {
		name    string
		in      []judgeCriterionResponse
		wantMet bool
	}{
		{"met_then_unmet_stays_unmet", []judgeCriterionResponse{
			{ID: "c1", Met: true, Reason: "looks fine"},
			{ID: "c1", Met: false, Reason: "actually not fine"},
		}, false},
		{"unmet_then_met_stays_unmet", []judgeCriterionResponse{
			{ID: "c1", Met: false, Reason: "not fine"},
			{ID: "c1", Met: true, Reason: "fine after all"},
		}, false},
		{"met_then_met_stays_met", []judgeCriterionResponse{
			{ID: "c1", Met: true},
			{ID: "c1", Met: true},
		}, true},
		{"single_unmet", []judgeCriterionResponse{{ID: "c1", Met: false}}, false},
		{"single_met", []judgeCriterionResponse{{ID: "c1", Met: true}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dedupeJudgeCriteriaAnyUnmetWins(tc.in)
			entry, ok := got["c1"]
			if !ok {
				t.Fatalf("dedupe dropped id %q entirely", "c1")
			}
			if entry.Met != tc.wantMet {
				t.Errorf("dedupe(%+v)[c1].Met = %v, want %v (any-unmet-wins)", tc.in, entry.Met, tc.wantMet)
			}
		})
	}
	if got := dedupeJudgeCriteriaAnyUnmetWins(nil); len(got) != 0 {
		t.Errorf("dedupe(nil) = %v, want empty map", got)
	}
}

// --- verifier soul (FR-038) --------------------------------------------------

func TestVerifierSoul_EnsureVerifierSoul_SeedsDefaultWhenEmpty(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	_ = al

	if got := judgeRubricFromConfig(judgeInst); got != "" {
		t.Fatalf("a fresh judgeHome must have no SOUL.md content yet, got %q", got)
	}

	ensureVerifierSoul(judgeInst)

	got := judgeRubricFromConfig(judgeInst)
	if got != coreagent.JudgeDefaultRubric {
		t.Errorf("ensureVerifierSoul must seed coreagent.JudgeDefaultRubric, got %q", got)
	}
}

func TestVerifierSoul_EnsureVerifierSoul_NeverOverwritesOperatorEdit(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	_ = al

	// Simulate an operator's own soul edit landing on disk (e.g. via the
	// gateway's AgentProfile soul-edit path, out of this file's scope).
	const operatorEdit = "operator-customized verification standards"
	if err := os.MkdirAll(judgeInst.Home, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(judgeInst.Home+"/SOUL.md", []byte(operatorEdit), 0o644); err != nil {
		t.Fatalf("seeding operator edit: %v", err)
	}

	ensureVerifierSoul(judgeInst)

	got := judgeRubricFromConfig(judgeInst)
	if got != operatorEdit {
		t.Errorf("ensureVerifierSoul must never overwrite an existing non-empty soul, got %q", got)
	}
}

func TestVerifierSoul_EnsureVerifierSoul_OnlyAppliesToTheSeededJudge(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	custom, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent must be registered")
	}
	ensureVerifierSoul(custom)
	if got := judgeRubricFromConfig(custom); got != "" {
		t.Errorf("ensureVerifierSoul must be a no-op for a non-Judge agent, got %q", got)
	}
}

func TestJudgeRubricFromConfig_NilAgentReturnsEmpty(t *testing.T) {
	if got := judgeRubricFromConfig(nil); got != "" {
		t.Errorf("nil agent must resolve to \"\", got %q", got)
	}
}

// --- SeedSystemAgentSoulFile (shared write helper, gateway eager seed + this
// package's lazy backstop both call it) -------------------------------------

func TestSeedJudgeSoulFile_SeedsWhenMissing(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "judge")

	if err := SeedSystemAgentSoulFile(workspace, coreagent.IDJudge); err != nil {
		t.Fatalf("SeedSystemAgentSoulFile: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(workspace, "SOUL.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != coreagent.JudgeDefaultRubric {
		t.Errorf("SeedSystemAgentSoulFile must write coreagent.JudgeDefaultRubric for a missing file, got %q", string(got))
	}
}

// TestSeedJudgeSoulFile_TreatsZeroByteFileAsMissing proves a 0-byte SOUL.md
// (e.g. left behind by an interrupted write, or a workspace dir created
// without ever populating the file) is backfilled exactly like an absent
// file — not mistaken for "operator already put content here".
func TestSeedJudgeSoulFile_TreatsZeroByteFileAsMissing(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "judge")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	soulPath := filepath.Join(workspace, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte{}, 0o644); err != nil {
		t.Fatalf("WriteFile (0-byte seed): %v", err)
	}

	if err := SeedSystemAgentSoulFile(workspace, coreagent.IDJudge); err != nil {
		t.Fatalf("SeedSystemAgentSoulFile: %v", err)
	}

	got, err := os.ReadFile(soulPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != coreagent.JudgeDefaultRubric {
		t.Errorf("a 0-byte SOUL.md must be treated as missing and backfilled, got %q", string(got))
	}
}

// TestSeedJudgeSoulFile_WhitespaceOnlyFileIsTreatedAsMissing mirrors
// ensureVerifierSoul's own TrimSpace-based emptiness check (judgeRubricFromConfig
// callers use the same rule) so the two "is this soul really empty" checks
// never disagree.
func TestSeedJudgeSoulFile_WhitespaceOnlyFileIsTreatedAsMissing(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "judge")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	soulPath := filepath.Join(workspace, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte("   \n\t  "), 0o644); err != nil {
		t.Fatalf("WriteFile (whitespace-only seed): %v", err)
	}

	if err := SeedSystemAgentSoulFile(workspace, coreagent.IDJudge); err != nil {
		t.Fatalf("SeedSystemAgentSoulFile: %v", err)
	}

	got, err := os.ReadFile(soulPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != coreagent.JudgeDefaultRubric {
		t.Errorf("a whitespace-only SOUL.md must be treated as missing and backfilled, got %q", string(got))
	}
}

func TestSeedJudgeSoulFile_NeverOverwritesExistingContent(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "judge")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	const operatorEdit = "operator-customized verification standards"
	soulPath := filepath.Join(workspace, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte(operatorEdit), 0o644); err != nil {
		t.Fatalf("WriteFile (operator edit seed): %v", err)
	}

	if err := SeedSystemAgentSoulFile(workspace, coreagent.IDJudge); err != nil {
		t.Fatalf("SeedSystemAgentSoulFile: %v", err)
	}

	got, err := os.ReadFile(soulPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != operatorEdit {
		t.Errorf("SeedSystemAgentSoulFile must never overwrite existing non-empty content, got %q", string(got))
	}
}

func TestSeedJudgeSoulFile_EmptyWorkspaceErrors(t *testing.T) {
	if err := SeedSystemAgentSoulFile("   ", coreagent.IDJudge); err == nil {
		t.Error("SeedSystemAgentSoulFile(\"   \") must return an error, got nil")
	}
}

// --- memory-off ContextBuilder gating (FR-039) ------------------------------

func TestContextBuilder_MemoryEnabled_DefaultTrueIncludesMemorySection(t *testing.T) {
	dir := t.TempDir()
	cb := NewContextBuilder(dir)
	t.Cleanup(cb.Memory().Close)
	if err := cb.Memory().WriteLastSession("## Last session\nSomething useful happened."); err != nil {
		t.Fatalf("WriteLastSession: %v", err)
	}

	prompt := cb.BuildSystemPrompt()
	if !strings.Contains(prompt, "# Memory") {
		t.Error("a ContextBuilder that never calls WithMemoryEnabled must keep injecting # Memory (unchanged default)")
	}
	if !strings.Contains(prompt, "Something useful happened") {
		t.Error("the memory content itself must appear when memory is enabled")
	}
}

func TestContextBuilder_MemoryEnabled_FalseSuppressesMemorySection(t *testing.T) {
	dir := t.TempDir()
	cb := NewContextBuilder(dir).WithMemoryEnabled(false)
	t.Cleanup(cb.Memory().Close)
	if err := cb.Memory().WriteLastSession("## Last session\nSomething useful happened."); err != nil {
		t.Fatalf("WriteLastSession: %v", err)
	}

	prompt := cb.BuildSystemPrompt()
	if strings.Contains(prompt, "# Memory") {
		t.Error("WithMemoryEnabled(false) must suppress the # Memory section entirely (FR-039)")
	}
	if strings.Contains(prompt, "Something useful happened") {
		t.Error("memory content must not leak into the prompt when memory is disabled")
	}
}

func TestContextBuilder_MemoryEnabled_FalseStillBuildsRestOfPrompt(t *testing.T) {
	dir := t.TempDir()
	cb := NewContextBuilder(dir).WithMemoryEnabled(false).WithAgentInfo("verifier-x", "Verifier X")
	t.Cleanup(cb.Memory().Close)
	prompt := cb.BuildSystemPrompt()
	if !strings.Contains(prompt, "Verifier X") {
		t.Error("disabling memory must not affect identity/other prompt sections")
	}
}

// --- Judge-unavailability escalation (sign-off finding 1) ------------------

// TestRunVerifierAdjudication_UnavailabilityEscalatesAfterThreeConsecutive
// proves the CENTRAL fix for sign-off finding 1: every JudgeCriteria caller
// already handles a single Unavailable result with a WARN + silent pause —
// correct for a one-off transient blip, but a PERSISTENTLY down Judge then
// loops forever looking healthy to an operator scanning logs, because every
// individual WARN reads identically to an isolated hiccup. This asserts the
// per-unit consecutive-Unavailable streak escalates to ERROR once it crosses
// verifierUnavailabilityEscalateAt (3), keeps erroring on every subsequent
// occurrence (not just the Nth), and clears the moment a real verdict comes
// back — all observed via the verifierUnavailabilityEscalateFn seam (this
// file's own test hook, mirroring the existing judgeSleepFn/
// killProcessGroupFn pattern) rather than by scraping real log output.
func TestRunVerifierAdjudication_UnavailabilityEscalatesAfterThreeConsecutive(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(context.Context, time.Duration) error {
		return context.Canceled // give up immediately on D7 backoff — no real sleep
	}

	origEscalate := verifierUnavailabilityEscalateFn
	t.Cleanup(func() { verifierUnavailabilityEscalateFn = origEscalate })
	type escalation struct {
		unitID string
		streak int
	}
	var escalations []escalation
	verifierUnavailabilityEscalateFn = func(unitID string, streak int) {
		escalations = append(escalations, escalation{unitID, streak})
	}

	const taskID = "t-unavailability-escalation"
	unitID := verifierUnitForTask(taskID)
	resetVerifierUnavailabilityStreakForTest(t, unitID)

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("simulated provider outage") // every call fails -> Unavailable
	}}
	judgeInst.Provider = fake

	callOnce := func() JudgeCriteriaResult {
		t.Helper()
		return al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
			Scope:           task.VerdictScopeTask,
			TaskID:          taskID,
			AssigneeAgentID: "native-agent",
			Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
			Attempt:         1,
			ClaimText:       "done",
		})
	}

	for i := 1; i <= 2; i++ {
		result := callOnce()
		if !result.Unavailable {
			t.Fatalf("call %d: expected Unavailable=true, got %+v", i, result)
		}
		if len(escalations) != 0 {
			t.Fatalf("call %d: escalate fn must not fire before the streak crosses %d, got %d call(s): %+v",
				i, verifierUnavailabilityEscalateAt, len(escalations), escalations)
		}
	}

	// 3rd consecutive Unavailable crosses the threshold.
	if result := callOnce(); !result.Unavailable {
		t.Fatalf("call 3: expected Unavailable=true, got %+v", result)
	}
	if len(escalations) != 1 {
		t.Fatalf("call 3: escalate fn must fire exactly once at streak=%d, got %d call(s): %+v",
			verifierUnavailabilityEscalateAt, len(escalations), escalations)
	}
	if escalations[0].unitID != unitID || escalations[0].streak != 3 {
		t.Errorf("escalation = %+v, want unitID=%q streak=3", escalations[0], unitID)
	}

	// 4th consecutive: must escalate AGAIN, not just once ever.
	if result := callOnce(); !result.Unavailable {
		t.Fatalf("call 4: expected Unavailable=true, got %+v", result)
	}
	if len(escalations) != 2 {
		t.Fatalf("call 4: escalate fn must fire again (stay loud), got %d total call(s): %+v", len(escalations), escalations)
	}
	if escalations[1].streak != 4 {
		t.Errorf("call 4 streak = %d, want 4", escalations[1].streak)
	}

	// A genuine success (the Judge's LLM call actually completes) clears the
	// streak — this IS a real verdict, not another Unavailable outcome.
	fake.chatFn = func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}
	result := callOnce()
	if result.Unavailable {
		t.Fatalf("expected the recovered call to succeed, got Unavailable: %s", result.Reason)
	}
	if streak := verifierUnavailabilityStreakForTest(unitID); streak != 0 {
		t.Errorf("a successful adjudication must clear the streak, got %d", streak)
	}

	// Back to failing: the streak restarts from 1 — no immediate re-escalation.
	fake.chatFn = func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("simulated provider outage")
	}
	if result := callOnce(); !result.Unavailable {
		t.Fatalf("expected Unavailable=true after recovery+relapse, got %+v", result)
	}
	if len(escalations) != 2 {
		t.Errorf("streak restarting from 1 after a clear must not immediately re-escalate, got %d total escalations: %+v",
			len(escalations), escalations)
	}
}

// resetVerifierUnavailabilityStreakForTest clears unitID's entry in the
// process-wide streak map before and after the test, so this test's use of a
// distinctive unitID cannot leak state into (or be polluted by) any other
// test in this package that happens to share a unit id.
func resetVerifierUnavailabilityStreakForTest(t *testing.T, unitID string) {
	t.Helper()
	clearFn := func() {
		verifierUnavailabilityMu.Lock()
		delete(verifierUnavailabilityStreak, unitID)
		verifierUnavailabilityMu.Unlock()
	}
	clearFn()
	t.Cleanup(clearFn)
}

// verifierUnavailabilityStreakForTest reads back the current streak count for
// unitID, for test assertions only.
func verifierUnavailabilityStreakForTest(unitID string) int {
	verifierUnavailabilityMu.Lock()
	defer verifierUnavailabilityMu.Unlock()
	return verifierUnavailabilityStreak[unitID]
}

// --- D7 sanity: a turn-level error is Unavailable, never fail-closed-unmet -
//
// This is a thin, additional check on TOP of judge_test.go's
// TestJudge_Unavailable_ProviderError_RetriesWithBackoff (which already
// exercises this exact path through the new runVerifierAdjudication code
// and passes unmodified). Kept here, short, to document the contract
// alongside the rest of this wave's new tests.
func TestVerifierAdjudication_TurnError_NeverConsumesAttempt(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(context.Context, time.Duration) error {
		return context.Canceled // give up immediately, no real sleep
	}

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, context.DeadlineExceeded
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-d7",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
		ClaimText:       "done",
	})
	if !result.Unavailable {
		t.Fatalf("a turn-level error must surface as Unavailable, not a verdict: %+v", result)
	}
	if result.Verdict != nil {
		t.Error("no verdict/attempt may be recorded for an Unavailable outcome (D7)")
	}
}
