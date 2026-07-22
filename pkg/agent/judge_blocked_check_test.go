// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_blocked_check_test.go covers the ADR-053 Phase-2 blocked-check honesty
// fix (R§8.1, G-3, FR-116/FR-137/FR-138) wired into JudgeCriteria: a machine/
// behavior check whose verification MECHANISM could not run resolves
// unable_to_verify (re-run, NEVER scored as absent evidence, bounded by the
// UnableToVerifyTracker → persistently-blocked escalation), and a prose
// criterion whose verifier turn RAN but formed no judgment resolves
// criterion_unjudgeable (unmet + escalate-to-owner once). Also covers the
// G-3/G-15 real workspace-diff feed into the prose Judge's context.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gitevidence"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// resetVerifierBlockedCheckSeams restores the process-wide tracker/gate to
// fresh defaults so one test's unable_to_verify counts / escalate-once markers
// cannot leak into another. Called by every test in this file.
func resetVerifierBlockedCheckSeams(t *testing.T) {
	t.Helper()
	SetVerifierUnableToVerifyTracker(NewUnableToVerifyTracker(UnableToVerifyMaxRerunsDefault))
	SetVerifierUnjudgeableEscalationGate(NewUnjudgeableEscalationGate())
}

// recordingJudgeProvider is a fakeJudgeProvider that also captures the last
// user-message content handed to Chat — so a test can assert what the engine
// plumbed into the verifier's prompt (here: the real workspace diff).
type recordingJudgeProvider struct {
	mu      sync.Mutex
	calls   int
	lastMsg string
	chatFn  func(callNum int) (*providers.LLMResponse, error)
}

func (r *recordingJudgeProvider) Chat(
	_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	r.mu.Lock()
	r.calls++
	for _, m := range msgs {
		if m.Role == "user" {
			r.lastMsg = m.Content
		}
	}
	fn := r.chatFn
	r.mu.Unlock()
	return fn(r.calls)
}

func (r *recordingJudgeProvider) GetDefaultModel() string { return "recording-judge" }
func (r *recordingJudgeProvider) capturedLastUserMessage() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastMsg
}

// --- Blocked machine check: unable_to_verify, re-run, never absent ---------

// TestBlockedCheck_UnableToVerify is the G-3 BDD scenario ("Blocked machine
// check returns unable-to-verify, never absent evidence"): a bash policy-deny
// (the MECHANISM could not run under the agent's own policy, MAJ-13) resolves
// unable_to_verify → Unavailable (round not consumed, re-run), and the bash
// tool is NEVER invoked. It is NEVER scored as an unmet/absent-evidence
// verdict — the live blind-judge bug this fixes.
func TestBlockedCheck_UnableToVerify(t *testing.T) {
	resetVerifierBlockedCheckSeams(t)
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	fakeBash := &fakeBashTool{result: &tools.ToolResult{ForLLM: "ok"}}
	workerInst.Tools.Register(fakeBash)
	// Intentionally do NOT call allowBashPolicy: no entry resolves to deny
	// (Constraint #6 — no default-policy fallback), so the mechanism is
	// policy-blocked → unable_to_verify.

	in := JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-blocked-utv",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "echo ok", 0)},
		Attempt:         1,
	}
	result := al.JudgeCriteria(context.Background(), in)
	if !result.Unavailable {
		t.Fatalf("a policy-blocked check must be Unavailable (unable_to_verify), got verdict met=%v", result.Verdict.Met)
	}
	if !strings.Contains(result.Reason, "unable_to_verify") {
		t.Errorf("reason %q must mention unable_to_verify", result.Reason)
	}
	if got := fakeBash.callCount(); got != 0 {
		t.Errorf("the bash mechanism must NOT be invoked when policy-blocked; got %d calls", got)
	}
}

// TestBlockedCheck_UnableToVerify_BoundedPersistentlyBlocked proves the re-run
// loop is bounded (m-4/FR-116): after unable_to_verify_max_reruns (K) CONSECUTIVE
// unable_to_verify results on the SAME check, the criterion escalates to the
// owner as persistently-blocked and is THEN scored unmet (so the goal burns
// rounds toward honest failure instead of looping forever).
func TestBlockedCheck_UnableToVerify_BoundedPersistentlyBlocked(t *testing.T) {
	// Use a small K so the escalation is reachable in few iterations.
	const k = 2
	SetVerifierUnableToVerifyTracker(NewUnableToVerifyTracker(k))
	SetVerifierUnjudgeableEscalationGate(NewUnjudgeableEscalationGate())
	t.Cleanup(func() { resetVerifierBlockedCheckSeams(t) })

	var escalationMu sync.Mutex
	escalatedCriterion := ""
	escalatedStreak := 0
	prev := unableToVerifyEscalateFn
	unableToVerifyEscalateFn = func(unitKey, criterionID string, streak int) {
		escalationMu.Lock()
		defer escalationMu.Unlock()
		escalatedCriterion = criterionID
		escalatedStreak = streak
	}
	t.Cleanup(func() { unableToVerifyEscalateFn = prev })

	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	workerInst.Tools.Register(&fakeBashTool{result: &tools.ToolResult{ForLLM: "ok"}})
	// No allowBashPolicy → policy-blocked every call.

	in := JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-blocked-bounded", // SAME taskID across calls → same tracker key
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "echo ok", 0)},
		Attempt:         1,
	}
	// Calls 1..k: count increments but stays <= k → withheld → Unavailable.
	for i := 1; i <= k; i++ {
		r := al.JudgeCriteria(context.Background(), in)
		if !r.Unavailable {
			t.Fatalf("call %d: want Unavailable (withheld, not yet persistent), got verdict met=%v", i, r.Verdict.Met)
		}
	}
	// Next call: count > k → persistently-blocked → scored UNMET (a real
	// verdict, NOT Unavailable) + the escalation fires synchronously.
	finalRes := al.JudgeCriteria(context.Background(), in)
	if finalRes.Unavailable {
		t.Fatal("after K consecutive unable_to_verify, the persistently-blocked check must be scored (a verdict), not stay Unavailable forever")
	}
	if finalRes.Verdict.Met {
		t.Fatal("the persistently-blocked criterion must resolve unmet (honest failure path)")
	}
	if !strings.Contains(finalRes.Verdict.PerCriterion[0].Reason, "unable_to_verify") {
		t.Errorf("persistently-blocked reason must mention unable_to_verify, got %q", finalRes.Verdict.PerCriterion[0].Reason)
	}
	escalationMu.Lock()
	id, streak := escalatedCriterion, escalatedStreak
	escalationMu.Unlock()
	if id != "c1" {
		t.Errorf("escalation criterionID = %q, want c1", id)
	}
	if streak <= k {
		t.Errorf("escalation streak = %d, want > %d (K)", streak, k)
	}
}

// --- Blocked behavior check: unable_to_verify ------------------------------

// TestBehaviorCheck_BlockedUnableToVerify proves the behavior-scan MECHANISM
// (read the session's tool-call log) resolves unable_to_verify when the
// transcript cannot be read — never scored as absent evidence.
func TestBehaviorCheck_BlockedUnableToVerify(t *testing.T) {
	resetVerifierBlockedCheckSeams(t)
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	one := 1
	bc := task.AcceptanceCriterion{
		ID:   "b1",
		Kind: task.KindBehavior,
		Text: "called web_search 3x",
		Behavior: &task.CriterionBehavior{
			Tool:     "web_search",
			MinCount: &one,
			Scope:    task.BehaviorScopeTaskSession,
		},
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
	}
	// The task "t-beh-blocked" has no session recorded (and no readable task
	// in this minimal harness) → the transcript-read mechanism could not run.
	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-beh-blocked",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{bc},
		Attempt:         1,
	})
	if !result.Unavailable {
		t.Fatalf("a behavior check with no scannable session must be Unavailable (unable_to_verify), got verdict met=%v", result.Verdict.Met)
	}
	if !strings.Contains(result.Reason, "unable_to_verify") {
		t.Errorf("reason %q must mention unable_to_verify", result.Reason)
	}
}

// TestBehaviorCheck_MinCount_PureScanner is the deterministic min_count happy
// path at the pure-scanner level (a real judgment, not a non-verdict): 3
// successful calls satisfy min_count=2. The full runBehaviorScan dispatch path
// is exercised by the blocked test above; this pins the count semantics.
func TestBehaviorCheck_MinCount_PureScanner(t *testing.T) {
	calls := []session.ToolCall{
		{Tool: "web_search", Status: "success"},
		{Tool: "web_search", Status: "success"},
		{Tool: "web_search", Status: "success"},
	}
	entries := []session.TranscriptEntry{{Timestamp: time.Now(), ToolCalls: calls}}
	two := 2
	res := ScanBehaviorCriterionEntries(entries, task.CriterionBehavior{
		Tool: "web_search", MinCount: &two, Scope: task.BehaviorScopeTaskSession,
	}, time.Time{})
	if !res.Met {
		t.Errorf("3 successful calls >= min_count 2 → want met, got unmet: %s", res.Reason)
	}
	if res.Observed != 3 {
		t.Errorf("observed = %d, want 3", res.Observed)
	}
}

// --- criterion_unjudgeable: ran but no judgment → unmet + escalate-once -----

// TestCriterionUnjudgeable_RanNoJudgment_EscalateOnce proves a prose criterion
// whose verifier turn RAN but formed no judgment resolves criterion_unjudgeable:
// unmet for the adjudication (AND-combine) + exactly ONE owner escalation per
// unit (FR-115/FR-138). Here the scripted verifier returns empty content.
func TestCriterionUnjudgeable_RanNoJudgment_EscalateOnce(t *testing.T) {
	resetVerifierBlockedCheckSeams(t)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: ""}, nil // ran, no judgment
	}}

	var escMu sync.Mutex
	escalations := 0
	var escalatedID string
	prev := unjudgeableEscalateFn
	unjudgeableEscalateFn = func(unitKey, criterionID string) {
		escMu.Lock()
		defer escMu.Unlock()
		escalations++
		escalatedID = criterionID
	}
	t.Cleanup(func() { unjudgeableEscalateFn = prev })

	runOnce := func(attempt int) JudgeCriteriaResult {
		return al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
			Scope:           task.VerdictScopeTask,
			TaskID:          "t-unjudgeable", // SAME taskID → same gate key
			AssigneeAgentID: "native-agent",
			Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the feature works")},
			Attempt:         attempt,
			ClaimText:       "done",
		})
	}

	result := runOnce(1)
	if result.Unavailable {
		t.Fatalf("criterion_unjudgeable must be a real unmet verdict, not Unavailable: %s", result.Reason)
	}
	if result.Verdict.Met {
		t.Fatal("criterion_unjudgeable must resolve unmet (AND-combine)")
	}
	escMu.Lock()
	id := escalatedID
	escMu.Unlock()
	if id != "c1" {
		t.Errorf("escalated criterionID = %q, want c1", id)
	}

	// Second adjudication for the SAME unit: still unmet, but the escalate-once
	// gate must NOT fire a second time (FR-138).
	result2 := runOnce(2)
	if result2.Verdict.Met {
		t.Fatal("second adjudication: criterion_unjudgeable must still resolve unmet")
	}
	escMu.Lock()
	got := escalations
	escMu.Unlock()
	if got != 1 {
		t.Errorf("escalate-once gate: expected exactly 1 escalation across 2 adjudications, got %d", got)
	}
}

// --- Real workspace diff fed into the prose Judge (G-3/G-15) ---------------

// TestJudge_DiffEvidence_FedIntoProseJudge proves the write-set-scoped
// workspace diff from the Phase-1 git evidence layer reaches the prose Judge's
// prompt — the real file changes, not a transcript window alone.
func TestJudge_DiffEvidence_FedIntoProseJudge(t *testing.T) {
	resetVerifierBlockedCheckSeams(t)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	// Materialize a workspace work/ dir under the loop's OMNIPUS_HOME and
	// plant one real boundary commit so AttemptDiff has something to report.
	home := config.OmnipusHomeDir()
	wsID := "wsdiff"
	workDir := filepath.Join(home, "workspaces", wsID, "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	repo, err := gitevidence.Open(workDir, gitevidence.WithRedactor(func(s string) string { return s }))
	if err != nil {
		t.Fatalf("gitevidence.Open: %v", err)
	}
	const changedBody = "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(changedBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(gitevidence.BoundaryAttempt,
		gitevidence.CommitMeta{TaskID: "t-diff-feed", AgentID: "native-agent"},
		[]string{"main.go"}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Recording Judge provider: returns a clean unmet verdict, but captures
	// the user-message content so we can assert the diff text is present.
	rec := &recordingJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": false, "criteria": [{"id":"c1","met":false,"reason":"no evidence"}]}`,
		}, nil
	}}
	judgeInst.Provider = rec

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-diff-feed",
		AssigneeAgentID: "native-agent",
		WorkspaceID:     wsID,
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the feature works")},
		Attempt:         1,
		ClaimText:       "done",
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	msg := rec.capturedLastUserMessage()
	if !strings.Contains(msg, "Workspace file diff") {
		t.Errorf("prose Judge prompt must contain the workspace-diff section header")
	}
	if !strings.Contains(msg, "main.go") {
		t.Errorf("prose Judge prompt must contain the changed file 'main.go' from the real diff")
	}
	// The patch is a unified diff, so added lines carry a "+" prefix; assert a
	// content fragment that survives that format (proves the real diff body —
	// not just a filename header — reached the Judge).
	if !strings.Contains(msg, "func main()") {
		t.Errorf("prose Judge prompt must contain the real diff body (func main()), not just a transcript window; msg was:\n%s", msg)
	}
}
