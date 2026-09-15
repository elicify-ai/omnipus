// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_wiring_adr084_test.go covers the review findings of 2026-09-11
// whose common shape was "the component was built correctly and never
// connected":
//
//   - finding 3: the verifier-session registry key and the two goal-side
//     lookup keys disagreed for a task-owned goal.
//   - finding 4: WithReadConfined / WithVerifierAdjudicationID had no
//     production caller, so every Judge turn ran unconfined with an empty
//     audit correlation id.
//   - finding 5: RegisterVerifierBudget / RegisterVerifierCapture had no
//     production caller, so the tool-call and byte caps never fired and the
//     injection capture accumulated nothing; VerifierGodModeRefusalReason was
//     never consulted, so a full Judge turn ran under god mode.
//
// EVERY test here drives a REAL adjudication through al.JudgeCriteria and
// asserts on what the PRODUCTION path did. None of them registers a budget, a
// capture or a ctx seam itself — that is the whole point: the pre-existing
// tests passed precisely because they supplied the wiring the product did
// not.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// verdictJSON is the structured verdict block parseJudgeResponse requires.
const verdictJSON = `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`

// wiringJudgeProvider records the message list handed to EVERY LLM call of
// the verifier's turn, so a test can see the tool-result text the model
// actually received on the next round — which is where a budget refusal
// surfaces.
type wiringJudgeProvider struct {
	mu       sync.Mutex
	calls    int
	messages [][]providers.Message
	lastCtx  context.Context
	respond  func(callNum int) *providers.LLMResponse
}

func (p *wiringJudgeProvider) Chat(
	ctx context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.lastCtx = ctx
	snapshot := make([]providers.Message, len(msgs))
	copy(snapshot, msgs)
	p.messages = append(p.messages, snapshot)
	p.mu.Unlock()
	return p.respond(n), nil
}

func (p *wiringJudgeProvider) GetDefaultModel() string { return "fake-judge-model" }

func (p *wiringJudgeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// admittedToolText returns every tool-result message text the model was shown
// across all rounds, in order.
func (p *wiringJudgeProvider) admittedToolText() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, round := range p.messages {
		for _, m := range round {
			if m.Role == "tool" {
				out = append(out, m.Content)
			}
		}
	}
	return out
}

func (p *wiringJudgeProvider) capturedTurnCtx() context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastCtx
}

// toolCallingJudgeResponder makes the verifier's turn ask for one tool call on
// each of its first nCalls rounds, then emit the verdict. read_file is used
// because it is a real, named tool on the Judge's read-only surface; whether
// it is allowed, denied or unknown in a given harness is irrelevant to this
// test — JUDGE-FR-051 counts CALLS, and every outcome is admitted through the
// same choke point (admitToolResult).
func toolCallingJudgeResponder(nCalls int) func(int) *providers.LLMResponse {
	return func(callNum int) *providers.LLMResponse {
		if callNum <= nCalls {
			return &providers.LLMResponse{
				ToolCalls: []providers.ToolCall{{
					ID:        "judge-call-" + string(rune('0'+callNum)),
					Name:      "read_file",
					Arguments: map[string]any{"path": "README.md"},
				}},
			}
		}
		return &providers.LLMResponse{Content: verdictJSON}
	}
}

func proseInputForTask(taskID string) JudgeCriteriaInput {
	return JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          taskID,
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the thing is done")},
		Attempt:         1,
		ClaimText:       "done",
	}
}

// --- finding 5: the per-adjudication budget is bound by PRODUCTION ---------

// TestVerifierBudget_ProductionDispatchEnforcesToolCallCap is the proof the
// finding demanded: a REAL adjudication, with the test registering NO budget
// of its own, must refuse the tool call that exceeds the operator's configured
// cap.
//
// Before the fix this could not fail: RegisterVerifierBudget had no production
// call site, so verifierBudgetForTurn returned nil for every real turn and the
// refusal branch in runTurn's tool loop was dead.
func TestVerifierBudget_ProductionDispatchEnforcesToolCallCap(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Judge.ToolCallCap = 1 // operator config — the ONLY budget source
	})

	p := &wiringJudgeProvider{respond: toolCallingJudgeResponder(2)}
	judgeInst.Provider = p

	result := al.JudgeCriteria(context.Background(), proseInputForTask("t-budget-cap"))
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if p.callCount() < 3 {
		t.Fatalf("the verifier turn made %d LLM calls, want at least 3 (two tool rounds + the verdict)",
			p.callCount())
	}

	var refusals int
	for _, text := range p.admittedToolText() {
		if strings.Contains(text, "Adjudication budget cap reached") {
			refusals++
		}
	}
	if refusals == 0 {
		t.Fatal("the second tool call was NOT refused — the adjudication budget was never bound to the " +
			"verifier's turn (JUDGE-FR-051/FR-052); this test registers no budget itself, by design")
	}
	if refusals != 1 {
		t.Errorf("got %d cap refusals, want exactly 1 (the first call is under the cap)", refusals)
	}
}

// TestVerifierBudget_UnderCapNeverRefuses is the negative control for the test
// above: with the same two tool calls and a cap that accommodates them, no
// refusal text appears. Without this, the assertion above would also pass on
// an implementation that refused unconditionally.
func TestVerifierBudget_UnderCapNeverRefuses(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Judge.ToolCallCap = 10
	})

	p := &wiringJudgeProvider{respond: toolCallingJudgeResponder(2)}
	judgeInst.Provider = p

	result := al.JudgeCriteria(context.Background(), proseInputForTask("t-budget-under-cap"))
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	for _, text := range p.admittedToolText() {
		if strings.Contains(text, "Adjudication budget cap reached") {
			t.Fatal("a tool call was refused while under the configured cap")
		}
	}
}

// TestVerifierBudget_UnregisteredAfterTheTurn proves the defer half of the
// contract: nothing is left behind in the process-wide registry once the
// adjudication returns. A leak here would silently cap an unrelated later
// turn that happened to reuse the id.
func TestVerifierBudget_UnregisteredAfterTheTurn(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Judge.ToolCallCap = 1
	})
	p := &wiringJudgeProvider{respond: toolCallingJudgeResponder(1)}
	judgeInst.Provider = p

	if result := al.JudgeCriteria(context.Background(), proseInputForTask("t-budget-leak")); result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}

	verifierBudgetsMu.Lock()
	budgets := len(verifierBudgets)
	verifierBudgetsMu.Unlock()
	if budgets != 0 {
		t.Errorf("%d budget(s) left registered after the adjudication returned, want 0", budgets)
	}

	verifierCapturesMu.Lock()
	captures := len(verifierCaptures)
	verifierCapturesMu.Unlock()
	if captures != 0 {
		t.Errorf("%d capture(s) left registered after the adjudication returned, want 0", captures)
	}
}

// --- finding 5: the god-mode capability gate is consulted ------------------

// TestVerifierAdjudication_RefusedUnderGodMode proves JUDGE-FR-057's refusal
// happens in production: under god mode (which floors every tool at allow) the
// adjudication is refused with the machine-readable reason, and — the part
// FR-057 is explicit about — no Judge turn runs at all.
func TestVerifierAdjudication_RefusedUnderGodMode(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Sandbox.GodMode = true
	})
	al.SetAllowGodMode(true) // publishes the process-wide availability atomic
	t.Cleanup(func() { al.SetAllowGodMode(false) })

	p := &wiringJudgeProvider{respond: func(int) *providers.LLMResponse {
		return &providers.LLMResponse{Content: verdictJSON}
	}}
	judgeInst.Provider = p

	result := al.JudgeCriteria(context.Background(), proseInputForTask("t-godmode"))
	if !result.Unavailable {
		t.Fatalf("adjudication must be refused under god mode; got verdict %+v", result.Verdict)
	}
	if !strings.HasPrefix(result.Reason, VerifierGodModeRefusalReasonPrefix) {
		t.Errorf("Reason = %q, want the %q prefix so the operator sees a distinct, actionable state",
			result.Reason, VerifierGodModeRefusalReasonPrefix)
	}
	if p.callCount() != 0 {
		t.Errorf("the Judge ran %d LLM call(s) under god mode; FR-057 requires the refusal BEFORE any turn",
			p.callCount())
	}
}

// TestVerifierAdjudication_GodModeOffStillAdjudicates is the control: the gate
// must refuse ONLY under god mode.
func TestVerifierAdjudication_GodModeOffStillAdjudicates(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	p := &wiringJudgeProvider{respond: func(int) *providers.LLMResponse {
		return &providers.LLMResponse{Content: verdictJSON}
	}}
	judgeInst.Provider = p

	result := al.JudgeCriteria(context.Background(), proseInputForTask("t-godmode-off"))
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable with god mode off: %s", result.Reason)
	}
	if !result.Verdict.Met {
		t.Errorf("verdict = %+v, want met", result.Verdict)
	}
}

// --- finding 4: the two ctx seams are set at the dispatch ------------------

// TestVerifierAdjudication_TurnCtxCarriesReadConfinementAndAdjudicationID
// pins JUDGE-FR-060/FR-084: the verifier's OWN turn ctx — the one every tool
// call it makes derives from — must carry the read-confinement posture and a
// non-empty adjudication id. Both seams shipped with tests that called them
// directly and no production caller at all, so a real Judge turn resolved
// ReadConfined=false (read_file could reach another session's transcript) and
// every audit entry carried an empty correlation id.
func TestVerifierAdjudication_TurnCtxCarriesReadConfinementAndAdjudicationID(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	p := &wiringJudgeProvider{respond: func(int) *providers.LLMResponse {
		return &providers.LLMResponse{Content: verdictJSON}
	}}
	judgeInst.Provider = p

	if result := al.JudgeCriteria(context.Background(), proseInputForTask("t-ctx-seams")); result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}

	turnCtx := p.capturedTurnCtx()
	if turnCtx == nil {
		t.Fatal("the verifier's LLM call must have received a ctx")
	}
	if !tools.ReadConfined(turnCtx) {
		t.Error("the verifier turn's ctx must carry JUDGE-FR-060's read-confinement posture " +
			"(tools.WithReadConfined) — otherwise a Judge turn can read outside its working directory")
	}
	if tools.VerifierAdjudicationID(turnCtx) == "" {
		t.Error("the verifier turn's ctx must carry a non-empty adjudication id (JUDGE-FR-084) so the " +
			"audit entries its tool calls produce can be correlated back to this adjudication")
	}
}

// --- finding 3: one unit key, agreed by the registration and both lookups --

// TestVerifierUnitID_GoalScopeIgnoresTaskID is the unit-level half: ADR-086
// legalises a goal-scope input that also carries TaskID (a running task's own
// goal), and the key must stay the goal key — not silently become the task
// key, which is what the old scope-blind ladder did.
func TestVerifierUnitID_GoalScopeIgnoresTaskID(t *testing.T) {
	in := JudgeCriteriaInput{
		Scope:         task.VerdictScopeGoal,
		TaskID:        "t-owner",
		GoalID:        "g-1",
		GoalSessionID: "sess-9",
	}
	got := verifierUnitID(in)
	if want := verifierUnitForGoal("sess-9"); got != want {
		t.Errorf("verifierUnitID(goal scope with TaskID) = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, "task:") {
		t.Fatal("a task-owned goal must NOT register under the task key: /goal clear and the " +
			"duplicate-dispatch guard both look up the goal key, so they would never find it")
	}
}

// TestVerifierUnitID_GoalScopeKeyMatchesTheGoalSideLookups pins the agreement
// itself against the EXACT expressions the two lookups use —
// cancelGoalVerifierIfAny (goal_loop.go) and goalAdjudicationInFlight
// (goal_triggers.go) both call verifierUnitForGoal(sessionID). If a later
// change re-points the registration at GoalID, this fails.
func TestVerifierUnitID_GoalScopeKeyMatchesTheGoalSideLookups(t *testing.T) {
	const sessionID = "sess-lookup"
	cancelLookupKey := verifierUnitForGoal(sessionID)   // cancelGoalVerifierIfAny
	inFlightLookupKey := verifierUnitForGoal(sessionID) // goalAdjudicationInFlight

	for _, in := range []JudgeCriteriaInput{
		{Scope: task.VerdictScopeGoal, GoalSessionID: sessionID},
		{Scope: task.VerdictScopeGoal, GoalSessionID: sessionID, GoalID: "g-2"},
		{Scope: task.VerdictScopeGoal, GoalSessionID: sessionID, TaskID: "t-2"},
		{Scope: task.VerdictScopeGoal, GoalSessionID: sessionID, TaskID: "t-2", GoalID: "g-2"},
	} {
		got := verifierUnitID(in)
		if got != cancelLookupKey || got != inFlightLookupKey {
			t.Errorf("registration key %q does not match the goal-side lookup key %q (input %+v)",
				got, cancelLookupKey, in)
		}
	}
}

// TestRunVerifierAdjudication_TaskOwnedGoalRegistersUnderTheGoalKey is the
// end-to-end half: a REAL adjudication of a task-owned goal must publish its
// live verifier session under the key `/goal clear` will look up.
func TestRunVerifierAdjudication_TaskOwnedGoalRegistersUnderTheGoalKey(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	spy := &spyRegistry{}
	SetVerifierSessionRegistry(spy)
	t.Cleanup(func() { SetVerifierSessionRegistry(nil) })

	judgeInst.Provider = &wiringJudgeProvider{respond: func(int) *providers.LLMResponse {
		return &providers.LLMResponse{Content: verdictJSON}
	}}

	const goalSession = "sess-task-owned-goal"
	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeGoal,
		TaskID:          "t-owning-the-goal", // legal under ADR-086 C-08
		GoalID:          "g-owned",
		GoalSessionID:   goalSession,
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the thing is done")},
		Attempt:         1,
		ClaimText:       "done",
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if len(spy.registerCalls) != 1 {
		t.Fatalf("Register called %d times, want 1", len(spy.registerCalls))
	}
	want := verifierUnitForGoal(goalSession)
	if got := spy.registerCalls[0].unitID; got != want {
		t.Errorf("registered unitID = %q, want %q (the key cancelGoalVerifierIfAny looks up)", got, want)
	}
	if len(spy.unregisterCalls) != 1 || spy.unregisterCalls[0] != want {
		t.Errorf("Unregister calls = %v, want exactly one for %q", spy.unregisterCalls, want)
	}
}

// --- the Judge's identity is unchanged by the new dispatch ----------------

// TestDispatchVerifierTurn_RunsAsTheJudgeInItsOwnVerifierSession guards the
// one real risk of owning the turnState locally (so the budget can be bound
// to its turnID): that the turn stops being the same turn. It must still run
// under the Judge's identity, in the pre-created verifier-typed session.
func TestDispatchVerifierTurn_RunsAsTheJudgeInItsOwnVerifierSession(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	spy := &spyRegistry{}
	SetVerifierSessionRegistry(spy)
	t.Cleanup(func() { SetVerifierSessionRegistry(nil) })

	judgeInst.Provider = &wiringJudgeProvider{respond: func(int) *providers.LLMResponse {
		return &providers.LLMResponse{Content: verdictJSON}
	}}

	result := al.JudgeCriteria(context.Background(), proseInputForTask("t-identity"))
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if result.Verdict.JudgeAgentID != string(coreagent.IDJudge) {
		t.Errorf("JudgeAgentID = %q, want %q", result.Verdict.JudgeAgentID, string(coreagent.IDJudge))
	}
	if len(spy.registerCalls) != 1 {
		t.Fatalf("Register called %d times, want 1", len(spy.registerCalls))
	}
	sessionID := spy.registerCalls[0].sessionID
	store := al.GetAgentStore(string(coreagent.IDJudge))
	if store == nil {
		t.Fatal("judge session store not available")
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil {
		t.Fatalf("registered sessionID %q does not resolve to a real session: %v", sessionID, err)
	}
	if meta.Type != "verifier" {
		t.Errorf("session Type = %q, want verifier", meta.Type)
	}
}

// --- finding 6: the Judge's diff feed is secret-guarded in production -----

// TestResolveVerifierDiffText_SecretInWorkingTreeNeverReachesTheJudgePrompt
// is the end-to-end half of review finding 6. The unit-level guard lives in
// pkg/gitevidence (workingdiff_secret_test.go); this proves the JUDGE's own
// call site actually passes a scanner, which is the half that was missing:
// resolveVerifierDiffText opened the evidence repo with gitevidence.Open(dir)
// and no options at all, so DiffWorkingTree read work/.env raw and rendered
// the key straight into the prompt sent to the external model every round.
//
// The assertion distinguishes the two failure modes from the fix:
//   - secret present in the text -> unguarded repo (the reported defect).
//   - text empty -> the repo was opened unguarded and DiffWorkingTree's own
//     fail-closed guard refused it, i.e. the scanner never reached Open.
//   - path named, patch withheld -> guarded, which is the contract.
func TestResolveVerifierDiffText_SecretInWorkingTreeNeverReachesTheJudgePrompt(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	const wsID = "ws-diff-secret"
	const secret = "sk-judgepromptleakcanary0123456789ab"
	home := config.OmnipusHomeDir()
	workDir := workspace.WorkDir(home, wsID)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".env"), []byte("OPENAI_API_KEY="+secret+"\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "notes.md"), []byte("# what changed\nthe feature works\n"), 0o600); err != nil {
		t.Fatalf("write notes.md: %v", err)
	}

	diffText, _ := al.resolveVerifierDiffText(JudgeCriteriaInput{
		Scope:       task.VerdictScopeTask,
		TaskID:      "t-diff-secret",
		WorkspaceID: wsID,
	})

	if strings.Contains(diffText, secret) {
		t.Fatal("SECRET LEAK: the workspace diff fed to the Judge's prompt carries the API key from work/.env")
	}
	if diffText == "" {
		t.Fatal("the diff feed produced nothing at all — the evidence repo was opened without a secret " +
			"scanner, so DiffWorkingTree refused the read (fail-closed). The judge call site must pass one.")
	}
	if !strings.Contains(diffText, ".env") {
		t.Error("the secret-bearing path must still be NAMED in the evidence — the Judge must not be told " +
			"nothing changed there")
	}
	if !strings.Contains(diffText, "the feature works") {
		t.Error("a clean file's real patch must still reach the Judge — the exclusion is per-path")
	}
}
