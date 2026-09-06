// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_compile_test.go covers the ADR-053 Phase-2 SMART compiler, feasibility
// gate, echo-&-confirm / amendment, non-verdict classifier, escalate-once, and
// the app-level OVERALL token budget (§1/§6, G-7/G-8/G-14, D9/D11/D12, N-6/N-12).
// Scoped unit tests — run with -tags goolm,stdjson -run '^Test...$' -p 1.
package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// fakeFeasibilityContext is a test FeasibilityContext: toolPolicies maps a tool
// name to its effective policy (default "allow" for unlisted); bashReachable
// toggles the check-criterion runner.
type fakeFeasibilityContext struct {
	toolPolicies  map[string]string
	bashReachable bool
}

func (f fakeFeasibilityContext) EffectiveToolPolicy(toolName string) string {
	if p, ok := f.toolPolicies[toolName]; ok {
		return p
	}
	return "allow"
}

func (f fakeFeasibilityContext) BashReachable() bool { return f.bashReachable }

// --- FR-110/FR-113: compile intent → criteria + echo-confirm + amendment -----

func TestGoalCompile_EchoConfirm_Amendment(t *testing.T) {
	fc := fakeFeasibilityContext{bashReachable: true}

	// Each marker kind compiles to the right criterion kind.
	res := compileGoalIntent(
		"land the feature — [search: 5] [tests pass] and the README reads well",
		fc, "tester")
	if res.Rejection != nil {
		t.Fatalf("unexpected rejection: %+v", res.Rejection)
	}
	g := res.Goal
	var sawBehavior, sawCheck, sawProse bool
	for _, c := range g.Criteria {
		switch c.Kind {
		case task.KindBehavior:
			sawBehavior = true
			if c.Behavior == nil || c.Behavior.Tool != "search_web" {
				t.Errorf("behavior criterion: want tool=search_web, got %+v", c.Behavior)
			}
			if c.Behavior.EffectiveMinCount() != 5 {
				t.Errorf("behavior min_count: want 5, got %d", c.Behavior.EffectiveMinCount())
			}
		case task.KindCheck:
			sawCheck = true
			if c.Check == nil || c.Check.Command != "go test ./..." || c.Check.ExpectedExitCode != 0 {
				t.Errorf("check criterion: want go test exit 0, got %+v", c.Check)
			}
		case task.KindProse:
			sawProse = true
		}
	}
	if !sawBehavior || !sawCheck || !sawProse {
		t.Fatalf("want all three kinds (behavior+check+prose), got behavior=%v check=%v prose=%v",
			sawBehavior, sawCheck, sawProse)
	}

	// The echo includes the literal commands verbatim (FR-113).
	echo := formatGoalEcho(g)
	if !strings.Contains(echo, "go test ./...") {
		t.Errorf("echo must include literal commands verbatim, got: %s", echo)
	}
	if !strings.Contains(echo, "search_web") {
		t.Errorf("echo must include the behavior tool, got: %s", echo)
	}

	// Confirm detection (FR-113/D11): a chat reply confirms.
	if !IsGoalConfirm("confirm") || !IsGoalConfirm("  YES  ") {
		t.Error("IsGoalConfirm must accept confirm/yes")
	}
	if IsGoalConfirm("some other text") {
		t.Error("IsGoalConfirm must reject non-confirm text")
	}

	// Amendment diff (N-6): re-statement is added/changed/dropped, never silent.
	// Build current + proposed with MATCHING texts so a shape-only change is
	// detected as "changed" (not add+drop). The behavior criterion's min_count
	// drops 5→3; the prose criterion is dropped (absent from proposed).
	au := task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"}
	current := &CompiledGoal{Intent: g.Intent, Prompt: g.Prompt, Criteria: []task.AcceptanceCriterion{
		{ID: "b", Kind: task.KindBehavior, Text: "perform web searches",
			Behavior: &task.CriterionBehavior{Tool: "search_web", MinCount: intp(5)}, Author: au},
		{ID: "p", Kind: task.KindProse, Text: "the README reads well", Author: au},
	}}
	proposed := &CompiledGoal{
		Intent: "land the feature — [search: 3]",
		Prompt: "land the feature",
		Criteria: []task.AcceptanceCriterion{
			{ID: "b2", Kind: task.KindBehavior, Text: "perform web searches", // SAME text, min 5→3
				Behavior: &task.CriterionBehavior{Tool: "search_web", MinCount: intp(3)}, Author: au},
		}, // prose dropped
	}
	amd := diffGoalAmendment(current, proposed)
	if !amd.HasChanges() {
		t.Fatal("amendment must report changes (behavior changed, prose dropped)")
	}
	if len(amd.Changed) == 0 {
		t.Errorf("amendment must list the changed behavior criterion (5→3): %+v", amd)
	}
	if len(amd.Dropped) == 0 {
		t.Error("amendment must list the dropped prose criterion")
	}
}

func intp(v int) *int { return &v }

// --- FR-111/D9: feasibility gate rejects out-of-policy + unjudgeable --------

func TestFeasibilityGate_RejectsOutOfPolicy(t *testing.T) {
	// search_web is denied → behavior criterion unreachable → rejected at compile.
	fc := fakeFeasibilityContext{
		toolPolicies:  map[string]string{"search_web": "deny"},
		bashReachable: true,
	}
	res := compileGoalIntent("find it — [search: 3]", fc, "tester")
	if res.Rejection == nil {
		t.Fatal("want rejection for out-of-policy behavior tool, got nil")
	}
	if !strings.Contains(res.Rejection.Reason, "search_web") || !strings.Contains(res.Rejection.Reason, "deny") {
		t.Errorf("rejection reason should name the denied tool, got: %s", res.Rejection.Reason)
	}
	if res.Goal != nil {
		t.Error("no rejected criterion may persist — Goal must be nil on rejection")
	}

	// bash unreachable → check criterion rejected.
	fc2 := fakeFeasibilityContext{toolPolicies: map[string]string{}, bashReachable: false}
	res2 := compileGoalIntent("verify — [tests pass]", fc2, "tester")
	if res2.Rejection == nil || !strings.Contains(res2.Rejection.Reason, "bash") {
		t.Fatalf("want bash-unreachable rejection, got: %+v", res2.Rejection)
	}
}

func TestFeasibilityGate_RejectsUnjudgeable(t *testing.T) {
	fc := fakeFeasibilityContext{bashReachable: true}
	// Pure hedging with no observable referent → semantically unjudgeable (D9).
	res := compileGoalIntent("feels good vibes", fc, "tester")
	if res.Rejection == nil {
		t.Fatal("want rejection for semantically unjudgeable prose, got nil")
	}
	// ADR-074 D4a rewrote the reason plain-language-first: it must explain
	// the problem in user terms, not internal vocabulary.
	if !strings.Contains(res.Rejection.Reason, "can't be verified as written") {
		t.Errorf("rejection reason should be the plain-language unjudgeable text, got: %s", res.Rejection.Reason)
	}

	// A substantive prose statement passes the gate.
	res2 := compileGoalIntent("the deployment guide explains the rollback steps", fc, "tester")
	if res2.Rejection != nil {
		t.Fatalf("judgeable prose must pass the gate, got rejection: %+v", res2.Rejection)
	}
}

// --- FR-116/FR-137: non-verdict classifier keys on mechanism-ran ------------

func TestNonVerdictClassifier_MechanismRanPredicate(t *testing.T) {
	cases := []struct {
		name string
		out  VerifierTurnOutcome
		want NonVerdictClass
	}{
		{"mechanism blocked → unable_to_verify (re-run, never scored)",
			VerifierTurnOutcome{MechanismRan: false}, NonVerdictUnableToVerify},
		{"ran but no judgment → criterion_unjudgeable (unmet + escalate-once)",
			VerifierTurnOutcome{MechanismRan: true, FormedJudgment: false}, NonVerdictCriterionUnjudgeable},
		{"ran and judged → normal verdict (not a non-verdict)",
			VerifierTurnOutcome{MechanismRan: true, FormedJudgment: true}, NonVerdictNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyNonVerdict(c.out)
			if got != c.want {
				t.Errorf("classifyNonVerdict(%+v) = %q, want %q", c.out, got, c.want)
			}
		})
	}
}

// --- FR-116/m-4: unable_to_verify re-runs bounded → persistently-blocked -----

func TestUnableToVerify_BoundedReruns(t *testing.T) {
	tr := NewUnableToVerifyTracker(3) // default
	cid := "crit-1"
	// First 3 consecutive unable_to_verify results are NOT persistently-blocked.
	for i := 1; i <= 3; i++ {
		if blocked := tr.NoteUnableToVerify(cid); blocked {
			t.Fatalf("attempt %d: want not-yet-persistently-blocked, got blocked", i)
		}
	}
	// The 4th (exceeding maxReruns=3) escalates as persistently-blocked.
	if blocked := tr.NoteUnableToVerify(cid); !blocked {
		t.Fatal("after maxReruns exceeded, want persistentlyBlocked=true")
	}
	// Reset clears the blocker (mechanism succeeded on retry).
	tr.Reset(cid)
	if tr.Consecutive(cid) != 0 {
		t.Fatalf("after Reset, Consecutive=%d want 0", tr.Consecutive(cid))
	}
	if blocked := tr.NoteUnableToVerify(cid); blocked {
		t.Fatal("after Reset, a single unable_to_verify must not be persistently-blocked")
	}
}

// --- FR-115/R§8.1: criterion_unjudgeable → unmet + escalate exactly once ----

func TestCriterionUnjudgeable_FailClosedEscalateOnce(t *testing.T) {
	gate := NewUnjudgeableEscalationGate()
	goalID := "goal-42"
	if !gate.ShouldEscalate(goalID) {
		t.Error("first criterion_unjudgeable for a goal must escalate (FR-115)")
	}
	if gate.ShouldEscalate(goalID) {
		t.Error("second criterion_unjudgeable for the same goal must NOT re-escalate (exactly once)")
	}
	// A different goal gets its own one escalation.
	if !gate.ShouldEscalate("goal-43") {
		t.Error("a different goal must get its own escalation")
	}
	// Reset (on /goal clear or confirmed amendment) re-allows one escalation.
	gate.Reset(goalID)
	if !gate.ShouldEscalate(goalID) {
		t.Error("after Reset (new generation), escalation must be re-allowed")
	}
}

// --- FR-138/M2: owner remediates criterion_unjudgeable by re-statement ------

func TestCriterionUnjudgeable_OwnerRemediation(t *testing.T) {
	// The honest terminal when the owner does nothing is judge_rounds_exhausted.
	if FailedReasonJudgeRoundsExhausted != "judge_rounds_exhausted" {
		t.Errorf("wrong terminal constant: %q", FailedReasonJudgeRoundsExhausted)
	}
	// Remediation = a re-statement amendment that fixes the criterion. The
	// amendment diff shows the dropped (mis-compiled) + added (corrected) criteria.
	fc := fakeFeasibilityContext{bashReachable: true}
	current := &CompiledGoal{Intent: "feels good", Prompt: "feels good",
		Criteria: []task.AcceptanceCriterion{{ID: "p", Kind: task.KindProse, Text: "feels good",
			Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"}}}}
	proposedRes := compileGoalIntent("the README explains rollback — [tests pass]", fc, "tester")
	if proposedRes.Rejection != nil {
		t.Fatalf("corrected re-statement must compile, got: %+v", proposedRes.Rejection)
	}
	amd := diffGoalAmendment(current, proposedRes.Goal)
	if len(amd.Dropped) == 0 || len(amd.Added) == 0 {
		t.Errorf("remediation amendment must drop the mis-compiled criterion and add the corrected one: %+v", amd)
	}
}

// --- FR-114/N-12: /goal clear cancels in-flight compilation -----------------

func TestGoalClear_CancelsInflightCompilation(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	// Start an active goal (set + ADR-074 D4a confirm), then begin a
	// re-statement amendment (pending).
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal the feature lands correctly", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal the feature lands correctly and tests pass", UserInitiated: true}, agentInst, &opts)
	mid, _ := store.GetMeta(sid)
	if mid.GoalPendingJSON == "" {
		t.Fatal("precondition: a pending amendment must exist after re-state")
	}

	// /goal clear cancels BOTH the active goal AND the in-flight compilation.
	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal clear", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled {
		t.Fatalf("/goal clear: matched=%v handled=%v", matched, handled)
	}
	if !strings.Contains(reply, "cleared") {
		t.Fatalf("/goal clear reply should say cleared, got: %s", reply)
	}
	after, _ := store.GetMeta(sid)
	if after.GoalCondition != "" || after.GoalCriteriaJSON != "" || after.GoalPendingJSON != "" {
		t.Errorf("after clear, all goal state must be empty: condition=%q criteria=%q pending=%q",
			after.GoalCondition, after.GoalCriteriaJSON, after.GoalPendingJSON)
	}
}

// --- FR-171..FR-174/D12: app-level OVERALL token budget ---------------------

func TestTokenBudgetDebit_AtomicOnePool(t *testing.T) {
	t.Run("atomic_one_pool_ignores_IsPrivilegedAgent", func(t *testing.T) {
		// cap 1000 tokens; the pool is agent-agnostic (FR-172 — no IsPrivilegedAgent).
		tb := NewTokenBudget(1000, nil)
		if tb.Cap() != 1000 {
			t.Fatalf("Cap=%d want 1000", tb.Cap())
		}
		// Debit accumulates in one pool regardless of caller.
		if consumed, _ := tb.Debit(400); consumed != 400 {
			t.Errorf("after 400 debit, consumed=%d want 400", consumed)
		}
		if consumed, _ := tb.Debit(400); consumed != 800 {
			t.Errorf("after 800 total, consumed=%d want 800", consumed)
		}
		// Not yet exhausted at 800/1000.
		if tb.Exhausted() {
			t.Error("at 800/1000, must not be exhausted")
		}
		// Cross the cap → exhausted (graceful-wind-down gate tripped at boundary).
		if _, exhausted := tb.Debit(250); !exhausted {
			t.Error("at 1050/1000, Debit must report exhausted=true")
		}
		if !tb.Exhausted() {
			t.Error("Exhausted() must report true after crossing the cap")
		}
		// Overshoot is bounded (1050, not unbounded) and the counter is not corrupted (FR-173/M5).
		if tb.Consumed() != 1050 {
			t.Errorf("consumed=%d want 1050 (bounded overshoot, counter intact)", tb.Consumed())
		}
		if tb.Remaining() != 0 {
			t.Errorf("Remaining=%d want 0 when over cap", tb.Remaining())
		}
	})

	t.Run("concurrent_debits_race_clean", func(t *testing.T) {
		// 100 goroutines each debiting 10 tokens → consumed must be exactly 1000
		// (no lost debits / counter corruption — FR-173 atomic RMW under one lock).
		tb := NewTokenBudget(0, nil) // unbounded; accounting only
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tb.Debit(10)
			}()
		}
		wg.Wait()
		if got := tb.Consumed(); got != 1000 {
			t.Fatalf("concurrent debit: consumed=%d want 1000 (counter corrupted)", got)
		}
	})

	t.Run("graceful_wind_down_not_mid_turn", func(t *testing.T) {
		// FR-174/R§8.3c: the brake is consulted at a BOUNDARY (Exhausted), never
		// mid-turn. Debit never blocks/hard-fails — it always returns, recording
		// the spend; the caller checks Exhausted() at the next boundary.
		tb := NewTokenBudget(100, nil)
		// A single huge debit still completes (no panic/hard-fail).
		consumed, _ := tb.Debit(500)
		if consumed != 500 {
			t.Fatalf("debit must always complete (no mid-turn hard-fail): consumed=%d", consumed)
		}
		if !tb.Exhausted() {
			t.Error("after overshoot, Exhausted() must be true (boundary gate)")
		}
	})
}

// --- FR-175/FR-176: default 0 = unbounded + advisory + token≠dollar ---------

func TestTokenBudget_UnsetUnbounded_Advisory(t *testing.T) {
	// FR-175: default cap 0 == unbounded sentinel.
	tb := NewTokenBudget(0, nil)
	if !tb.IsUnbounded() {
		t.Error("cap 0 must be unbounded")
	}
	if tb.Exhausted() {
		t.Error("unbounded pool must never report exhausted")
	}
	tb.Debit(1_000_000)
	if tb.Exhausted() {
		t.Error("unbounded pool must never brake even after a large debit")
	}
	if tb.Remaining() != -1 {
		t.Errorf("unbounded Remaining=%d want -1", tb.Remaining())
	}

	// FR-176/R§8.3b: set-time token≠dollar warning is surfaced.
	warn := SetTimeWarning()
	if !strings.Contains(warn, "token") || !strings.Contains(strings.ToLower(warn), "dollar") {
		t.Errorf("SetTimeWarning must mention token vs dollar, got: %s", warn)
	}
	// R§8.3a: unbounded advisory is surfaced.
	adv := UnboundedAdvisory()
	if !strings.Contains(strings.ToLower(adv), "unbounded") {
		t.Errorf("UnboundedAdvisory must mention unbounded, got: %s", adv)
	}
}

// --- FR-177: restart-gated ceiling + boot reconciliation -------------------

func TestTokenBudget_RestartGatedPersister(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tb.json")
	// First instance: debit some, persist.
	tb1 := NewTokenBudget(1000, NewTokenBudgetPersister(path))
	tb1.Debit(300)
	// Second instance (restart): reconciles consumed from disk.
	tb2 := NewTokenBudget(1000, NewTokenBudgetPersister(path))
	if got := tb2.Consumed(); got != 300 {
		t.Fatalf("after restart, consumed=%d want reconciled 300", got)
	}
	// The ceiling is restart-gated: a SetCap change does not carry live debits
	// backward, but the consumed counter is preserved across it (FR-177).
	tb2.SetCap(500)
	if tb2.Cap() != 500 {
		t.Fatalf("SetCap(500): Cap=%d", tb2.Cap())
	}
	if tb2.Consumed() != 300 {
		t.Fatalf("consumed must be preserved across cap change: %d", tb2.Consumed())
	}
}

// --- sanity: the marker parser + excision keeps prose readable -------------

func TestParseIntentMarkers_Excision(t *testing.T) {
	criteria, prose, _ := parseIntentMarkers(
		"ship it: [search: 2] then [check: go build . exit:0] and docs read well", "tester")
	if len(criteria) != 2 {
		t.Fatalf("want 2 marker criteria, got %d", len(criteria))
	}
	// Prose remainder keeps the natural-language fragments, whitespace-collapsed.
	if !strings.Contains(prose, "ship it") || !strings.Contains(prose, "docs read well") {
		t.Errorf("prose remainder lost natural language: %q", prose)
	}
	if strings.Contains(prose, "[search") || strings.Contains(prose, "[check") {
		t.Errorf("prose remainder must not contain raw markers: %q", prose)
	}
}

func TestCompileGoalIntent_CheckTrueSteering(t *testing.T) {
	res := compileGoalIntent("[check: true exit:0] please continue", nil, "user")
	if res.Rejection != nil {
		t.Fatalf("unexpected rejection: %+v", res.Rejection)
	}
	if res.Goal == nil || len(res.Goal.Criteria) != 1 {
		t.Fatalf("want exactly 1 criterion, got %+v", res.Goal)
	}
	c := res.Goal.Criteria[0]
	if c.Kind != task.KindCheck {
		t.Fatalf("kind = %q, want check", c.Kind)
	}
	if c.Check == nil || c.Check.Command != "true" || c.Check.ExpectedExitCode != 0 {
		t.Fatalf("check = %+v, want command=true exit=0", c.Check)
	}
}
