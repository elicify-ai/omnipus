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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
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

// --- verifier-session registry (FR-037) -------------------------------------

func TestVerifierSessionRegistry_DefaultRegisterUnregister(t *testing.T) {
	r := newDefaultVerifierSessionRegistry()
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
	spy := newDefaultVerifierSessionRegistry()
	SetVerifierSessionRegistry(spy)
	t.Cleanup(func() { SetVerifierSessionRegistry(nil) })

	if currentVerifierSessionRegistry() != VerifierSessionRegistry(spy) {
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

func (s *spyRegistry) Register(unitID, sessionID string) {
	s.registerCalls = append(s.registerCalls, struct{ unitID, sessionID string }{unitID, sessionID})
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
	if !strings.Contains(spy.registerCalls[0].sessionID, "agent:judge:verify:") {
		t.Errorf("sessionID = %q, want it to carry the agent:judge:verify: prefix", spy.registerCalls[0].sessionID)
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

func TestVerifierWindowFeed_GoalScope_ReturnsEmptyUntilCallerPassesSessionID(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	got := al.resolveVerifierWindowText(JudgeCriteriaInput{Scope: task.VerdictScopeGoal, AssigneeAgentID: "native-agent"})
	if got != "" {
		t.Errorf("goal scope must return \"\" until goal_loop.go passes a session id (WAVE-MERGE note), got %q", got)
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
