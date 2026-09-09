// Omnipus — plan_correct Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- harness ---------------------------------------------------------------

// correctionSpy is a stand-in for the engine's AppendCorrection. Every test
// that asserts a rejection asserts against `calls` — the property that matters
// is that a rejected correction NEVER REACHES THE ENGINE, so nothing can
// mutate, not merely that some error string came back.
type correctionSpy struct {
	calls    []CorrectionRequest
	callers  []CorrectionCaller
	planIDs  []string
	revision string
	honest   bool
	err      error
}

func (s *correctionSpy) fn(_ context.Context, planID string, caller CorrectionCaller, req CorrectionRequest) (string, bool, error) {
	s.planIDs = append(s.planIDs, planID)
	s.callers = append(s.callers, caller)
	s.calls = append(s.calls, req)
	if s.err != nil {
		return "", false, s.err
	}
	rev := s.revision
	if rev == "" {
		rev = "rev-1"
	}
	return rev, s.honest, nil
}

// planFixture is a running, supervision-parked plan with its member set.
type planFixture struct {
	planStore *plan.Store
	taskStore *task.Store
	planID    string
}

// newParkedPlan builds a RUNNING plan parked at awaiting_supervision, owned by
// agent "jim", in workspace "ws-1".
func newParkedPlan(t *testing.T) planFixture {
	t.Helper()
	return newPlanInPhase(t, plan.PhaseAwaitingSupervision)
}

func newPlanInPhase(t *testing.T, phase plan.PlanPhase) planFixture {
	t.Helper()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := &plan.Plan{
		WorkspaceID:  "ws-1",
		Title:        "Ship the parser",
		OwnerAgentID: "jim",
		Owner:        "jim",
		CreatedBy:    "jim",
		DoD: []task.AcceptanceCriterion{
			{Kind: task.KindProse, Text: "the parser round-trips every fixture",
				Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "jim"}},
		},
	}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	approved := plan.StateApproved
	if _, err := planStore.Update(p.ID, plan.Patch{State: &approved}); err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	running := plan.StateRunning
	if _, err := planStore.Update(p.ID, plan.Patch{State: &running, PlanPhase: &phase}); err != nil {
		t.Fatalf("run plan: %v", err)
	}
	return planFixture{planStore: planStore, taskStore: taskStore, planID: p.ID}
}

// addMember attaches a member task to the fixture's plan.
func (f planFixture) addMember(t *testing.T, id string, status task.Status, criteria []task.AcceptanceCriterion) *task.Task {
	t.Helper()
	m := &task.Task{
		ID: id, Title: id, WorkspaceID: "ws-1", PlanID: f.planID,
		AgentID: "ava", Status: task.StatusNext, Criteria: criteria,
	}
	if err := f.taskStore.Create(m); err != nil {
		t.Fatalf("create member %q: %v", id, err)
	}
	if status != task.StatusNext {
		if _, err := f.taskStore.Update(id, task.Patch{Status: &status}); err != nil {
			t.Fatalf("set member %q status %s: %v", id, status, err)
		}
	}
	got, err := f.taskStore.Get(id)
	if err != nil {
		t.Fatalf("get member %q: %v", id, err)
	}
	return got
}

// tool wires a PlanCorrectTool over the fixture with the given engine spy.
func (f planFixture) tool(spy *correctionSpy) *PlanCorrectTool {
	tool := NewPlanCorrectTool(f.planStore, f.taskStore)
	tool.SetAppendCorrection(spy.fn)
	return tool
}

// snapshot captures the plan record and every member's status, so a test can
// assert that a rejection changed NOTHING.
func (f planFixture) snapshot(t *testing.T) string {
	t.Helper()
	p, err := f.planStore.Get(f.planID)
	if err != nil {
		t.Fatalf("snapshot: get plan: %v", err)
	}
	members, err := f.taskStore.List(task.Filter{PlanID: f.planID})
	if err != nil {
		t.Fatalf("snapshot: list members: %v", err)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "state=%s phase=%s dod=%d", p.State, p.EffectivePlanPhase(), len(p.DoD))
	for i := range members {
		fmt.Fprintf(&sb, " |%s=%s(crit:%d)", members[i].ID, members[i].Status, len(members[i].Criteria))
	}
	return sb.String()
}

func supervisorCtx() context.Context {
	return WithAgentID(context.Background(), PlanSupervisorAgentID)
}

func proseCriterion(text string) task.AcceptanceCriterion {
	return task.AcceptanceCriterion{
		// ADR-080 D-TYPES: Judgment set explicitly to boolean (prose's default)
		// so this fixture matches the shape task.NormalizeCriteria would
		// produce for real persisted data — criterionKey now folds judgment
		// into its identity, so a fixture left at the zero value would mismatch
		// production-path criteria (which are always normalize-backfilled) on
		// an otherwise-identical criterion.
		ID: "crit-" + text, Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: text,
		Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "jim"},
	}
}

func criterionArg(text string) map[string]any {
	return map[string]any{"kind": "prose", "text": text}
}

func tailMemberArg(title string, criteriaTexts ...string) map[string]any {
	crit := make([]any, 0, len(criteriaTexts))
	for _, c := range criteriaTexts {
		crit = append(crit, criterionArg(c))
	}
	return map[string]any{"title": title, "criteria": crit}
}

// checkCriterionDone builds a `kind: check` AcceptanceCriterion for a DONE
// (superseded) member fixture, mirroring proseCriterion's fixed-id style.
func checkCriterionDone(text, command string, expectedExitCode int) task.AcceptanceCriterion {
	return task.AcceptanceCriterion{
		// ADR-080 D-TYPES: Judgment set explicitly (check's deterministic
		// judgment is always boolean) — same rationale as proseCriterion above.
		ID: "crit-" + text, Kind: task.KindCheck, Judgment: task.JudgmentBoolean, Text: text,
		Check:  &task.CriterionCheck{Command: command, ExpectedExitCode: expectedExitCode},
		Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "jim"},
	}
}

// checkCriterionArg builds the raw tool-call argument for a `kind: check`
// criterion on a tail member (criterionArg's check-kind counterpart).
func checkCriterionArg(text, command string, expectedExitCode int) map[string]any {
	return map[string]any{
		"kind": "check", "text": text,
		"check": map[string]any{"command": command, "expected_exit_code": expectedExitCode},
	}
}

// tailMemberWithCriteriaArgs builds a tail-member arg from already-built
// criterion args — unlike tailMemberArg, which only builds bare-text prose
// criteria, this accepts any criterionArg/checkCriterionArg mix.
func tailMemberWithCriteriaArgs(title string, criteria ...map[string]any) map[string]any {
	crit := make([]any, 0, len(criteria))
	for _, c := range criteria {
		crit = append(crit, c)
	}
	return map[string]any{"title": title, "criteria": crit}
}

// --- FR-009/FR-010: authority ---------------------------------------------

// TestPlanCorrect_DeniesEveryNonSupervisorCaller proves the OUTCOME: a caller
// that is not PlanSupervisor cannot correct a plan — the engine is never
// reached, so nothing can mutate — and the denial reveals neither the plan's
// owner id nor whether the plan exists at all.
//
// The plan's OWN owner ("jim") is in the table on purpose: FR-011 gives the
// Owner no correction role whatsoever, so "it is my plan" must not be a way in.
func TestPlanCorrect_DeniesEveryNonSupervisorCaller(t *testing.T) {
	t.Parallel()
	callers := []struct {
		name    string
		agentID string
	}{
		{"the plan's own owner agent", "jim"},
		{"a seeded core agent", "ava"},
		{"a user-created agent with no policy entry", "agent-7f3c"},
		{"a caller with no identity at all", ""},
		{"an agent whose id merely contains the supervisor's", "not-plansupervisor"},
	}
	for _, c := range callers {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newParkedPlan(t)
			f.addMember(t, "m-done", task.StatusDone, []task.AcceptanceCriterion{proseCriterion("parses ints")})
			spy := &correctionSpy{}
			tool := f.tool(spy)
			before := f.snapshot(t)

			res := tool.Execute(WithAgentID(context.Background(), c.agentID), map[string]any{
				"plan_id":              f.planID,
				"verb":                 "append",
				"falsified_assumption": "the parser handled floats",
				"tail_members":         []any{tailMemberArg("handle floats", "floats parse")},
			})

			// Outcome 1: denied.
			if !res.IsError {
				t.Fatalf("caller %q was NOT denied: %s", c.agentID, res.ForLLM)
			}
			// Outcome 2: the engine was never reached, so nothing could mutate.
			if len(spy.calls) != 0 {
				t.Fatalf("caller %q reached the correction engine %d time(s)", c.agentID, len(spy.calls))
			}
			if after := f.snapshot(t); after != before {
				t.Fatalf("plan changed under a denied caller:\n before %s\n after  %s", before, after)
			}
			// Outcome 3: the denial leaks nothing.
			for _, secret := range []string{"jim", f.planID, "owner"} {
				if strings.Contains(strings.ToLower(res.ForLLM), strings.ToLower(secret)) {
					t.Errorf("denial leaked %q: %s", secret, res.ForLLM)
				}
			}
		})
	}
}

// TestPlanCorrect_DenialIsNotAnExistenceOracle proves the denial for a plan
// that does NOT exist is byte-identical to the denial for one that does. The
// tool loads no plan until the identity gate has passed, so a non-holder
// cannot use it to enumerate plan ids.
func TestPlanCorrect_DenialIsNotAnExistenceOracle(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	spy := &correctionSpy{}
	tool := f.tool(spy)
	ctx := WithAgentID(context.Background(), "ava")

	args := func(planID string) map[string]any {
		return map[string]any{
			"plan_id": planID, "verb": "append",
			"falsified_assumption": "x",
			"tail_members":         []any{tailMemberArg("t", "c")},
		}
	}
	existing := tool.Execute(ctx, args(f.planID))
	missing := tool.Execute(ctx, args("plan-that-does-not-exist"))
	otherMissing := tool.Execute(ctx, args("another-nonexistent-plan"))

	if !existing.IsError || !missing.IsError || !otherMissing.IsError {
		t.Fatal("expected all three calls to be denied")
	}
	if existing.ForLLM != missing.ForLLM || missing.ForLLM != otherMissing.ForLLM {
		t.Fatalf("denials differ — the tool is an existence oracle:\n existing:    %q\n missing:     %q\n missing(2):  %q",
			existing.ForLLM, missing.ForLLM, otherMissing.ForLLM)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("engine reached %d time(s) by a denied caller", len(spy.calls))
	}
}

// TestPlanCorrect_UnwiredEngine_FailsClosed proves an unwired correction hook
// denies rather than silently reporting success (FR-004).
func TestPlanCorrect_UnwiredEngine_FailsClosed(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	tool := NewPlanCorrectTool(f.planStore, f.taskStore) // no SetAppendCorrection

	res := tool.Execute(supervisorCtx(), map[string]any{
		"plan_id": f.planID, "verb": "append",
		"falsified_assumption": "x",
		"tail_members":         []any{tailMemberArg("t", "c")},
	})
	if !res.IsError {
		t.Fatalf("unwired engine reported success: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "not wired") {
		t.Errorf("unexpected message: %s", res.ForLLM)
	}
}

// --- FR-030 / FR-030b / US-3: the supersede integrity boundary -------------

// TestPlanCorrect_SupersedeCannotDiscountEvidenceAlone is the US-3 property
// test. A plan whose ONLY defect is an unmet acceptance criterion must not be
// able to reach a MET verdict by discounting the evidence that failed it.
//
// Each row is a distinct discounting strategy, and each asserts the same
// OUTCOME: the correction never reaches the engine, so the failing member's
// outcome is never marked ignored-by-judge and the plan's own record is
// untouched. The one row that IS allowed through is the one that pairs the
// supersede with real replacement work held to the SAME criteria — which is
// what "replacement work" has to mean for the guarantee to be worth anything.
func TestPlanCorrect_SupersedeCannotDiscountEvidenceAlone(t *testing.T) {
	t.Parallel()
	// The member that satisfied the plan's unmet criterion badly. It is `done`
	// (supersede's only legal target) and carries the three criteria the
	// replacement must inherit.
	criteria := []task.AcceptanceCriterion{
		proseCriterion("parses ints"),
		proseCriterion("parses floats"),
		proseCriterion("rejects malformed input"),
	}

	// Only the PAIRING rule (FR-030 — at least one tail member) is exercised
	// here now. FR-030b (every criterion must be carried) used to be exercised
	// by this same table via "throwaway member" / "partial member" cases that
	// expected REJECTION — that expectation named the OLD, unsatisfiable
	// mechanism (the caller had to reproduce the superseded criteria itself).
	// Those cases moved to TestPlanCorrect_SupersedeAutoInheritsMissingCriteria,
	// which proves the stronger, currently-true property: whatever the caller
	// omits is backfilled, so the correction is ACCEPTED and the criterion is
	// never actually dropped — see InheritSupersededCriteria.
	cases := []struct {
		name        string
		tailMembers []any
		wantReject  bool
	}{
		{
			name:        "bare supersede — discount with no replacement work at all",
			tailMembers: nil,
			wantReject:  true,
		},
		{
			name:        "supersede paired with an empty tail-member array",
			tailMembers: []any{},
			wantReject:  true,
		},
		{
			name: "supersede paired with replacement work carrying every criterion",
			tailMembers: []any{
				tailMemberArg("redo the parser", "parses ints", "parses floats", "rejects malformed input"),
			},
			wantReject: false,
		},
		{
			name: "supersede whose criteria are spread across two replacement members",
			tailMembers: []any{
				tailMemberArg("redo numeric parsing", "parses ints", "parses floats"),
				tailMemberArg("harden error handling", "rejects malformed input"),
			},
			wantReject: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newParkedPlan(t)
			f.addMember(t, "m-parser", task.StatusDone, criteria)
			spy := &correctionSpy{}
			tool := f.tool(spy)
			before := f.snapshot(t)

			args := map[string]any{
				"plan_id":              f.planID,
				"verb":                 "supersede",
				"superseded_member_id": "m-parser",
				"falsified_assumption": "we assumed the parser handled every fixture",
			}
			if tc.tailMembers != nil {
				args["tail_members"] = tc.tailMembers
			}
			res := tool.Execute(supervisorCtx(), args)

			if !tc.wantReject {
				if res.IsError {
					t.Fatalf("legitimate paired supersede was rejected: %s", res.ForLLM)
				}
				if len(spy.calls) != 1 {
					t.Fatalf("engine reached %d time(s), want 1", len(spy.calls))
				}
				if spy.calls[0].SupersededMemberID != "m-parser" {
					t.Errorf("superseded_member_id = %q", spy.calls[0].SupersededMemberID)
				}
				if len(spy.calls[0].TailMembers) != len(tc.tailMembers) {
					t.Errorf("tail members reaching the engine = %d, want %d",
						len(spy.calls[0].TailMembers), len(tc.tailMembers))
				}
				return
			}

			// Outcome 1: rejected.
			if !res.IsError {
				t.Fatalf("discounting strategy was ACCEPTED: %s", res.ForLLM)
			}
			// Outcome 2: the engine never ran, so no member's outcome was ever
			// marked ignored-by-judge — the plan cannot have reached done this way.
			if len(spy.calls) != 0 {
				t.Fatalf("a rejected supersede still reached the correction engine %d time(s)", len(spy.calls))
			}
			// Outcome 3: nothing on disk changed.
			if after := f.snapshot(t); after != before {
				t.Fatalf("a rejected supersede mutated the plan:\n before %s\n after  %s", before, after)
			}
		})
	}
}

// TestPlanCorrect_SupersedeAutoInheritsMissingCriteria proves the G-11 fix:
// FR-030b's identity rule is now SATISFIABLE for the one real caller
// (PlanSupervisor), because the tool backfills whatever the caller's tail
// members don't already carry (InheritSupersededCriteria) BEFORE checking
// (RequireCriteriaInheritance) — rather than rejecting a submission the
// caller structurally cannot complete (the supervision wake never shows it a
// member's criteria detail).
//
// This replaces two sub-cases that used to live in
// TestPlanCorrect_SupersedeCannotDiscountEvidenceAlone and asserted
// REJECTION for exactly these inputs — that assertion named the OLD,
// unsatisfiable mechanism. The property that matters (nothing the superseded
// member required is ever actually missing from the committed replacement) is
// asserted here directly against what reaches the engine, which is strictly
// stronger than asserting the caller's raw input already contained it.
func TestPlanCorrect_SupersedeAutoInheritsMissingCriteria(t *testing.T) {
	t.Parallel()
	criteria := []task.AcceptanceCriterion{
		proseCriterion("parses ints"),
		proseCriterion("parses floats"),
		proseCriterion("rejects malformed input"),
	}

	// unionTexts collects every criterion Text across every tail member the
	// engine actually received.
	unionTexts := func(members []task.Task) map[string]bool {
		out := make(map[string]bool)
		for i := range members {
			for _, c := range members[i].Criteria {
				out[c.Text] = true
			}
		}
		return out
	}

	t.Run("throwaway member carrying none of the superseded criteria is now accepted, and the criteria are backfilled", func(t *testing.T) {
		t.Parallel()
		f := newParkedPlan(t)
		f.addMember(t, "m-parser", task.StatusDone, criteria)
		spy := &correctionSpy{}
		res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
			"plan_id":              f.planID,
			"verb":                 "supersede",
			"superseded_member_id": "m-parser",
			"falsified_assumption": "we assumed the parser handled every fixture",
			"tail_members":         []any{tailMemberArg("touch a file", "a file exists")},
		})
		if res.IsError {
			t.Fatalf("supersede with an incomplete tail member was rejected instead of backfilled: %s", res.ForLLM)
		}
		if len(spy.calls) != 1 {
			t.Fatalf("engine reached %d time(s), want 1", len(spy.calls))
		}
		got := unionTexts(spy.calls[0].TailMembers)
		for _, want := range []string{"parses ints", "parses floats", "rejects malformed input"} {
			if !got[want] {
				t.Errorf("committed tail members are missing superseded criterion %q — it was DROPPED, not backfilled", want)
			}
		}
		// The caller's own (unrelated) criterion is preserved, not overwritten.
		if !got["a file exists"] {
			t.Error("the caller's own tail-member criterion was discarded by the backfill")
		}
	})

	t.Run("partial member carrying 1 of 3 criteria is accepted, and only the missing 2 are backfilled (no duplicate of the one already present)", func(t *testing.T) {
		t.Parallel()
		f := newParkedPlan(t)
		f.addMember(t, "m-parser", task.StatusDone, criteria)
		spy := &correctionSpy{}
		res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
			"plan_id":              f.planID,
			"verb":                 "supersede",
			"superseded_member_id": "m-parser",
			"falsified_assumption": "we assumed the parser handled every fixture",
			"tail_members":         []any{tailMemberArg("redo the parser", "parses ints")},
		})
		if res.IsError {
			t.Fatalf("supersede with a partial tail member was rejected instead of backfilled: %s", res.ForLLM)
		}
		if len(spy.calls) != 1 {
			t.Fatalf("engine reached %d time(s), want 1", len(spy.calls))
		}
		members := spy.calls[0].TailMembers
		if len(members) != 1 {
			t.Fatalf("tail members reaching the engine = %d, want 1 (backfill augments the existing member, adds none)", len(members))
		}
		got := unionTexts(members)
		for _, want := range []string{"parses ints", "parses floats", "rejects malformed input"} {
			if !got[want] {
				t.Errorf("committed tail member is missing superseded criterion %q", want)
			}
		}
		// Exactly 3 criteria total: the caller's 1 plus the 2 backfilled — no
		// duplicate of "parses ints" (already present, so not re-added).
		if n := len(members[0].Criteria); n != 3 {
			t.Errorf("tail member carries %d criteria, want exactly 3 (1 caller-authored + 2 backfilled, no duplicate)", n)
		}
	})
}

// TestPlanCorrect_SupersedeCheckCriterionAutoInherited_NeverDowngraded
// reproduces the exact live G-11 defect (observed via a real LLM,
// Conformance_t3_PlanningReplanningE2E): the superseded member carries a
// `kind: check` criterion, and PlanSupervisor — never shown the member's
// criteria detail by its supervision wake (buildSupervisionTargetsText,
// pkg/agent/plan_engine.go, renders id | status | title only) — cannot know
// the check's exact command. It authors a plausible but DIFFERENT command for
// what it believes is "the same" criterion: same text (learned from a prior
// rejection's error message, which echoes .Text), command "true" against the
// real original's "exit 0", both expecting exit 0.
//
// This proves two things at once:
//  1. The correction is now ACCEPTED — before this fix, every such attempt
//     was rejected (verified live: 4 varied attempts, all rejected).
//  2. The fix did NOT weaken the guard into a text-based comparison (the
//     rejected alternative (b)): the caller's own check and the backfilled
//     original check are BOTH present as distinct criteria. A criterion with
//     matching TEXT but a different command is never treated as "covering"
//     the original — so a real check can never be silently downgraded to
//     whatever command the caller guessed.
func TestPlanCorrect_SupersedeCheckCriterionAutoInherited_NeverDowngraded(t *testing.T) {
	t.Parallel()
	const critText = "m2 own criterion trivially passes (member reaches done)"
	criteria := []task.AcceptanceCriterion{
		checkCriterionDone(critText, "exit 0", 0),
	}
	f := newParkedPlan(t)
	f.addMember(t, "m2", task.StatusDone, criteria)
	spy := &correctionSpy{}
	res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
		"plan_id":              f.planID,
		"verb":                 "supersede",
		"superseded_member_id": "m2",
		"falsified_assumption": "m2's own criterion trivially passes regardless of real work done",
		"tail_members": []any{
			tailMemberWithCriteriaArgs("redo m2 for real",
				checkCriterionArg(critText, "true", 0)),
		},
	})
	if res.IsError {
		t.Fatalf("supersede against a check-carrying member, with the same criterion text but a "+
			"different command, was rejected: %s", res.ForLLM)
	}
	if len(spy.calls) != 1 {
		t.Fatalf("engine reached %d time(s), want 1", len(spy.calls))
	}
	members := spy.calls[0].TailMembers
	if len(members) != 1 {
		t.Fatalf("tail members reaching the engine = %d, want 1", len(members))
	}
	crits := members[0].Criteria
	if len(crits) != 2 {
		t.Fatalf("tail member carries %d criteria, want exactly 2 (caller's own check + the backfilled "+
			"original — never coalesced): %+v", len(crits), crits)
	}
	var sawCallerCheck, sawBackfilledCheck bool
	for _, c := range crits {
		if c.Kind != task.KindCheck || c.Check == nil {
			t.Fatalf("criterion %q is not a check: %+v", c.Text, c)
		}
		switch c.Check.Command {
		case "true":
			sawCallerCheck = true
		case "exit 0":
			sawBackfilledCheck = true
			if c.ID != "" {
				t.Errorf("backfilled criterion carries id %q — it must be cleared so the store mints a "+
					"fresh one at create time, never aliasing the superseded member's own criterion", c.ID)
			}
			if c.Status != task.CritPending {
				t.Errorf("backfilled criterion status = %q, want pending (a fresh, unjudged instance)", c.Status)
			}
		default:
			t.Errorf("unexpected check command %q", c.Check.Command)
		}
	}
	if !sawCallerCheck {
		t.Error("the caller's own check criterion (command \"true\") is missing — it must never be discarded")
	}
	if !sawBackfilledCheck {
		t.Error("the superseded member's real check criterion (command \"exit 0\") was not backfilled — " +
			"the caller's near-miss guess was allowed to stand in for it, which is exactly the downgrade the fix must prevent")
	}
}

// TestPlanCorrect_SupersedeOfCriteriaLessMember proves the vacuous case: a
// done member carrying ZERO acceptance criteria can be superseded (there is
// nothing to inherit), but the pairing rule still applies, so the BARE form is
// still rejected.
func TestPlanCorrect_SupersedeOfCriteriaLessMember(t *testing.T) {
	t.Parallel()

	t.Run("bare form is still rejected", func(t *testing.T) {
		t.Parallel()
		f := newParkedPlan(t)
		f.addMember(t, "m-empty", task.StatusDone, nil)
		spy := &correctionSpy{}
		res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
			"plan_id": f.planID, "verb": "supersede",
			"superseded_member_id": "m-empty",
			"falsified_assumption": "x",
		})
		if !res.IsError {
			t.Fatalf("bare supersede accepted: %s", res.ForLLM)
		}
		if len(spy.calls) != 0 {
			t.Fatalf("engine reached %d time(s)", len(spy.calls))
		}
	})

	t.Run("paired form is applied", func(t *testing.T) {
		t.Parallel()
		f := newParkedPlan(t)
		f.addMember(t, "m-empty", task.StatusDone, nil)
		spy := &correctionSpy{}
		res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
			"plan_id": f.planID, "verb": "supersede",
			"superseded_member_id": "m-empty",
			"falsified_assumption": "x",
			"tail_members":         []any{tailMemberArg("do it properly", "it actually works")},
		})
		if res.IsError {
			t.Fatalf("paired supersede of a criteria-less member rejected: %s", res.ForLLM)
		}
		if len(spy.calls) != 1 {
			t.Fatalf("engine reached %d time(s), want 1", len(spy.calls))
		}
	})
}

// TestPlanCorrect_SupersedeRejectsNonDoneMember proves supersede only ever
// targets a `done` member, and that a member of ANOTHER plan is rejected
// without naming that plan (ownership is decided before status, so the
// adjudicator never sees another plan's member status).
func TestPlanCorrect_SupersedeRejectsNonDoneMember(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	f.addMember(t, "m-failed", task.StatusFailed, []task.AcceptanceCriterion{proseCriterion("c")})
	// A member of a DIFFERENT plan, in the same task store.
	foreign := &task.Task{
		ID: "m-foreign", Title: "foreign", WorkspaceID: "ws-1", PlanID: "plan-other",
		AgentID: "ava", Status: task.StatusDone,
	}
	if err := f.taskStore.Create(foreign); err != nil {
		t.Fatalf("create foreign member: %v", err)
	}
	spy := &correctionSpy{}
	tool := f.tool(spy)

	failedRes := tool.Execute(supervisorCtx(), map[string]any{
		"plan_id": f.planID, "verb": "supersede",
		"superseded_member_id": "m-failed",
		"falsified_assumption": "x",
		"tail_members":         []any{tailMemberArg("t", "c")},
	})
	if !failedRes.IsError {
		t.Fatalf("supersede of a failed member accepted: %s", failedRes.ForLLM)
	}

	foreignRes := tool.Execute(supervisorCtx(), map[string]any{
		"plan_id": f.planID, "verb": "supersede",
		"superseded_member_id": "m-foreign",
		"falsified_assumption": "x",
		"tail_members":         []any{tailMemberArg("t", "c")},
	})
	if !foreignRes.IsError {
		t.Fatalf("supersede of another plan's member accepted: %s", foreignRes.ForLLM)
	}
	if strings.Contains(foreignRes.ForLLM, "plan-other") {
		t.Errorf("rejection leaked the other plan's id: %s", foreignRes.ForLLM)
	}
	if strings.Contains(foreignRes.ForLLM, string(task.StatusDone)) {
		t.Errorf("rejection leaked the other plan's member status: %s", foreignRes.ForLLM)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("engine reached %d time(s)", len(spy.calls))
	}
}

// --- D-01 / FR-029: the supervision-eligible phase set ---------------------

// TestPlanCorrect_PhaseGate proves the OUTCOME D-01 exists for: a STALLED plan
// is correctable. Under a gate keyed on awaiting_supervision alone, every
// correction a stall wake provoked was rejected 100% of the time — the stall
// limb had a wake and no way to act on it.
func TestPlanCorrect_PhaseGate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		phase      plan.PlanPhase
		wantAccept bool
	}{
		{plan.PhaseAwaitingSupervision, true},
		{plan.PhaseStalled, true},
		{plan.PhaseDispatching, false},
		{plan.PhaseIdle, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.phase), func(t *testing.T) {
			t.Parallel()
			f := newPlanInPhase(t, tc.phase)
			spy := &correctionSpy{}
			res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
				"plan_id": f.planID, "verb": "append",
				"falsified_assumption": "the DAG could not converge",
				"tail_members":         []any{tailMemberArg("unblock the DAG", "the join member can start")},
			})
			if tc.wantAccept {
				if res.IsError {
					t.Fatalf("phase %s rejected: %s", tc.phase, res.ForLLM)
				}
				if len(spy.calls) != 1 {
					t.Fatalf("phase %s: engine reached %d time(s), want 1", tc.phase, len(spy.calls))
				}
				return
			}
			if !res.IsError {
				t.Fatalf("phase %s accepted: %s", tc.phase, res.ForLLM)
			}
			if len(spy.calls) != 0 {
				t.Fatalf("phase %s: engine reached %d time(s), want 0", tc.phase, len(spy.calls))
			}
		})
	}
}

// TestPlanCorrect_RejectsNonRunningPlan proves a plan that is not running
// cannot be corrected, and that the engine is never reached.
func TestPlanCorrect_RejectsNonRunningPlan(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	failed := plan.StateFailed
	reason := plan.FailedReasonStoppedByUser
	if _, err := f.planStore.Update(f.planID, plan.Patch{State: &failed, FailedReason: &reason}); err != nil {
		t.Fatalf("fail plan: %v", err)
	}
	spy := &correctionSpy{}
	res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
		"plan_id": f.planID, "verb": "abandon", "falsified_assumption": "x",
	})
	if !res.IsError {
		t.Fatalf("correction of a failed plan accepted: %s", res.ForLLM)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("engine reached %d time(s)", len(spy.calls))
	}
}

// --- FR-046: the parameter schema ------------------------------------------

// TestPlanCorrect_RejectsUnknownVerb proves the verb enum is closed — "" and
// any near-miss are rejected before the engine is reached.
func TestPlanCorrect_RejectsUnknownVerb(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"", "replace", "APPEND", "supersede_member", "retry"} {
		t.Run("verb="+verb, func(t *testing.T) {
			t.Parallel()
			f := newParkedPlan(t)
			spy := &correctionSpy{}
			res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
				"plan_id": f.planID, "verb": verb, "falsified_assumption": "x",
			})
			if !res.IsError {
				t.Fatalf("verb %q accepted: %s", verb, res.ForLLM)
			}
			if len(spy.calls) != 0 {
				t.Fatalf("verb %q reached the engine", verb)
			}
		})
	}
}

// TestPlanCorrect_RequiresFalsifiedAssumption proves the diagnosis is
// mandatory on every verb — a correction with no stated falsified assumption
// is not a diagnosis, it is a guess.
func TestPlanCorrect_RequiresFalsifiedAssumption(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"append", "supersede", "targeted_retry", "abandon"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			f := newParkedPlan(t)
			spy := &correctionSpy{}
			res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
				"plan_id": f.planID, "verb": verb, "falsified_assumption": "   ",
			})
			if !res.IsError {
				t.Fatalf("%s with a blank falsified_assumption accepted: %s", verb, res.ForLLM)
			}
			if len(spy.calls) != 0 {
				t.Fatalf("%s reached the engine", verb)
			}
		})
	}
}

// TestPlanCorrect_VerbFieldMatrix proves each verb rejects the fields it does
// not accept, rather than silently ignoring them. The engine sets Members and
// Edges from the request unconditionally for EVERY verb, so a targeted_retry
// carrying 50 tail members would create all 50 — a field this tool merely
// ignored would still be applied downstream.
func TestPlanCorrect_VerbFieldMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args map[string]any
	}{
		{"targeted_retry carrying tail_members", map[string]any{
			"verb": "targeted_retry", "retried_member_id": "m-failed",
			"tail_members": []any{tailMemberArg("smuggled", "c")},
		}},
		{"targeted_retry carrying tail_edges", map[string]any{
			"verb": "targeted_retry", "retried_member_id": "m-failed",
			"tail_edges": []any{map[string]any{"from": "m-done", "to": "m-failed"}},
		}},
		{"targeted_retry carrying superseded_member_id", map[string]any{
			"verb": "targeted_retry", "retried_member_id": "m-failed",
			"superseded_member_id": "m-done",
		}},
		{"append carrying superseded_member_id", map[string]any{
			"verb": "append", "superseded_member_id": "m-done",
			"tail_members": []any{tailMemberArg("t", "c")},
		}},
		{"append carrying retried_member_id", map[string]any{
			"verb": "append", "retried_member_id": "m-failed",
			"tail_members": []any{tailMemberArg("t", "c")},
		}},
		{"supersede carrying retried_member_id", map[string]any{
			"verb": "supersede", "superseded_member_id": "m-done", "retried_member_id": "m-failed",
			"tail_members": []any{tailMemberArg("t", "c")},
		}},
		{"abandon carrying tail_members", map[string]any{
			"verb": "abandon", "tail_members": []any{tailMemberArg("t", "c")},
		}},
		{"abandon carrying superseded_member_id", map[string]any{
			"verb": "abandon", "superseded_member_id": "m-done",
		}},
		{"append with no tail_members at all", map[string]any{"verb": "append"}},
		{"supersede with no superseded_member_id", map[string]any{
			"verb": "supersede", "tail_members": []any{tailMemberArg("t", "c")},
		}},
		{"targeted_retry with no retried_member_id", map[string]any{"verb": "targeted_retry"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newParkedPlan(t)
			f.addMember(t, "m-done", task.StatusDone, nil)
			f.addMember(t, "m-failed", task.StatusFailed, nil)
			spy := &correctionSpy{}
			args := map[string]any{"plan_id": f.planID, "falsified_assumption": "x"}
			for k, v := range tc.args {
				args[k] = v
			}
			res := f.tool(spy).Execute(supervisorCtx(), args)
			if !res.IsError {
				t.Fatalf("accepted: %s", res.ForLLM)
			}
			if len(spy.calls) != 0 {
				t.Fatalf("reached the engine %d time(s)", len(spy.calls))
			}
		})
	}
}

// TestPlanCorrect_PayloadCaps proves the four FR-046 caps reject before any
// mutation. They bound the payload the engine processes while it holds the
// process-wide plan-decision mutex for the whole of AppendCorrection.
func TestPlanCorrect_PayloadCaps(t *testing.T) {
	t.Parallel()

	tooManyMembers := make([]any, 0, maxTailMembers+1)
	for i := 0; i <= maxTailMembers; i++ {
		tooManyMembers = append(tooManyMembers, tailMemberArg(fmt.Sprintf("m%d", i), "c"))
	}
	tooManyEdges := make([]any, 0, maxTailEdges+1)
	for i := 0; i <= maxTailEdges; i++ {
		tooManyEdges = append(tooManyEdges, map[string]any{"from": "m-done", "to": "m-done"})
	}

	cases := []struct {
		name string
		args map[string]any
	}{
		{"tail_members over the cap", map[string]any{"tail_members": tooManyMembers}},
		{"tail_edges over the cap", map[string]any{
			"tail_members": []any{tailMemberArg("t", "c")}, "tail_edges": tooManyEdges,
		}},
		{"a member title over the byte cap", map[string]any{
			"tail_members": []any{tailMemberArg(strings.Repeat("x", maxMemberTitleBytes+1), "c")},
		}},
		{"a 10 KB member title", map[string]any{
			"tail_members": []any{tailMemberArg(strings.Repeat("y", 10*1024), "c")},
		}},
		{"falsified_assumption over the text cap", map[string]any{
			"falsified_assumption": strings.Repeat("z", maxTextBytes+1),
			"tail_members":         []any{tailMemberArg("t", "c")},
		}},
		{"reason over the text cap", map[string]any{
			"reason":       strings.Repeat("z", maxTextBytes+1),
			"tail_members": []any{tailMemberArg("t", "c")},
		}},
		{"a member description the task store would reject mid-commit", map[string]any{
			"tail_members": []any{map[string]any{
				"title":       "t",
				"description": strings.Repeat("d", taskStoreMaxDescriptionBytes+1),
				"criteria":    []any{criterionArg("c")},
			}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newParkedPlan(t)
			f.addMember(t, "m-done", task.StatusDone, nil)
			spy := &correctionSpy{}
			args := map[string]any{"plan_id": f.planID, "verb": "append", "falsified_assumption": "x"}
			for k, v := range tc.args {
				args[k] = v
			}
			res := f.tool(spy).Execute(supervisorCtx(), args)
			if !res.IsError {
				t.Fatalf("over-cap payload accepted: %s", res.ForLLM)
			}
			if len(spy.calls) != 0 {
				t.Fatalf("over-cap payload reached the engine")
			}
		})
	}
}

// TestPlanCorrect_MemberIDsAreSystemMinted proves the OUTCOME that retires the
// silent-drop class: the schema accepts no caller-supplied member id, and two
// tail members with identical titles still get distinct ids.
//
// The engine's apply func skips a tail member whose id already exists — right
// for intent-log replay, and silent data loss for a caller reusing an id it
// read off the plan: the member is never created, the correction reports
// success, and the plan proceeds believing the work was added.
func TestPlanCorrect_MemberIDsAreSystemMinted(t *testing.T) {
	t.Parallel()

	t.Run("a caller-supplied id is rejected outright", func(t *testing.T) {
		t.Parallel()
		f := newParkedPlan(t)
		existing := f.addMember(t, "m-existing", task.StatusDone, nil)
		spy := &correctionSpy{}
		res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
			"plan_id": f.planID, "verb": "append", "falsified_assumption": "x",
			"tail_members": []any{map[string]any{
				"id": existing.ID, "title": "collide", "criteria": []any{criterionArg("c")},
			}},
		})
		if !res.IsError {
			t.Fatalf("caller-supplied id accepted: %s", res.ForLLM)
		}
		if len(spy.calls) != 0 {
			t.Fatal("caller-supplied id reached the engine")
		}
	})

	t.Run("identical titles still get distinct ids", func(t *testing.T) {
		t.Parallel()
		f := newParkedPlan(t)
		spy := &correctionSpy{}
		res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
			"plan_id": f.planID, "verb": "append", "falsified_assumption": "x",
			"tail_members": []any{
				tailMemberArg("retry the flaky step", "it passes"),
				tailMemberArg("retry the flaky step", "it passes"),
			},
		})
		if res.IsError {
			t.Fatalf("append rejected: %s", res.ForLLM)
		}
		if len(spy.calls) != 1 {
			t.Fatalf("engine reached %d time(s)", len(spy.calls))
		}
		members := spy.calls[0].TailMembers
		if len(members) != 2 {
			t.Fatalf("tail members = %d, want 2", len(members))
		}
		if members[0].ID == "" || members[1].ID == "" {
			t.Fatalf("a tail member reached the engine with an EMPTY id — the engine's apply func skips those, so the member would silently never be created: %+v", members)
		}
		if members[0].ID == members[1].ID {
			t.Fatalf("both tail members got id %q", members[0].ID)
		}
		// The minted ids must also be reported back, so the adjudicator can
		// name the new members in a later correction.
		var out struct {
			CreatedMemberIDs []string `json:"created_member_ids"`
		}
		if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
			t.Fatalf("parse result %q: %v", res.ForLLM, err)
		}
		if len(out.CreatedMemberIDs) != 2 {
			t.Fatalf("created_member_ids = %v", out.CreatedMemberIDs)
		}
	})
}

// TestPlanCorrect_TailMembersAreDispatchable proves a created tail member
// carries everything the plan dispatcher needs: the plan's workspace, plan id,
// a real assignee, a dispatchable status and its criteria. A member missing
// any of these is created and then immediately dead.
func TestPlanCorrect_TailMembersAreDispatchable(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	spy := &correctionSpy{}
	res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
		"plan_id": f.planID, "verb": "append",
		"falsified_assumption": "the fixture set was incomplete",
		"tail_members":         []any{tailMemberArg("add the missing fixtures", "every fixture round-trips")},
	})
	if res.IsError {
		t.Fatalf("append rejected: %s", res.ForLLM)
	}
	m := spy.calls[0].TailMembers[0]
	if m.PlanID != f.planID {
		t.Errorf("plan_id = %q, want %q", m.PlanID, f.planID)
	}
	if m.WorkspaceID != "ws-1" {
		t.Errorf("workspace_id = %q, want ws-1", m.WorkspaceID)
	}
	if m.Status != task.StatusNext {
		t.Errorf("status = %q, want next (dispatchable)", m.Status)
	}
	if m.AgentID == "" {
		t.Error("tail member has no assignee — the task executor fails it with `agent \"\" not found`")
	}
	if m.AgentID != "jim" {
		t.Errorf("assignee = %q, want the plan's owner agent jim", m.AgentID)
	}
	if len(m.Criteria) != 1 {
		t.Errorf("criteria = %d, want 1", len(m.Criteria))
	}
	if m.CreatedBy != PlanSupervisorAgentID {
		t.Errorf("created_by = %q, want %q", m.CreatedBy, PlanSupervisorAgentID)
	}
}

// TestPlanCorrect_SupersedeReplacementInheritsAssignee proves a supersede's
// replacement work goes to the agent whose work it replaces.
func TestPlanCorrect_SupersedeReplacementInheritsAssignee(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	f.addMember(t, "m-done", task.StatusDone, []task.AcceptanceCriterion{proseCriterion("c")})
	spy := &correctionSpy{}
	res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
		"plan_id": f.planID, "verb": "supersede", "superseded_member_id": "m-done",
		"falsified_assumption": "x",
		"tail_members":         []any{tailMemberArg("redo it", "c")},
	})
	if res.IsError {
		t.Fatalf("supersede rejected: %s", res.ForLLM)
	}
	if got := spy.calls[0].TailMembers[0].AgentID; got != "ava" {
		t.Errorf("replacement assignee = %q, want ava (the superseded member's assignee)", got)
	}
}

// TestPlanCorrect_RequiresMemberCriteria proves a tail member with no
// acceptance criteria is rejected: an unjudgeable member cannot contribute to
// a Definition-of-Done verdict, so adding one is a way to look busy without
// being accountable.
func TestPlanCorrect_RequiresMemberCriteria(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	spy := &correctionSpy{}
	res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
		"plan_id": f.planID, "verb": "append", "falsified_assumption": "x",
		"tail_members": []any{map[string]any{"title": "vague work", "criteria": []any{}}},
	})
	if !res.IsError {
		t.Fatalf("criteria-less tail member accepted: %s", res.ForLLM)
	}
	if len(spy.calls) != 0 {
		t.Fatal("reached the engine")
	}
}

// --- FR-046: tail edges ----------------------------------------------------

// TestPlanCorrect_TailEdgesRejectCycle proves a correction whose edges would
// close a cycle is rejected before any mutation. A cyclic plan graph has no
// dispatchable member, so — combined with a once-per-park supervision wake —
// it would strand the plan permanently.
func TestPlanCorrect_TailEdgesRejectCycle(t *testing.T) {
	t.Parallel()

	t.Run("cycle among new members", func(t *testing.T) {
		t.Parallel()
		f := newParkedPlan(t)
		spy := &correctionSpy{}
		res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
			"plan_id": f.planID, "verb": "append", "falsified_assumption": "x",
			"tail_members": []any{
				map[string]any{"ref": "a", "title": "A", "criteria": []any{criterionArg("c")}},
				map[string]any{"ref": "b", "title": "B", "criteria": []any{criterionArg("c")}},
			},
			"tail_edges": []any{
				map[string]any{"from": "a", "to": "b"},
				map[string]any{"from": "b", "to": "a"},
			},
		})
		if !res.IsError {
			t.Fatalf("cycle accepted: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "cycle") {
			t.Errorf("rejection does not name the cycle: %s", res.ForLLM)
		}
		if len(spy.calls) != 0 {
			t.Fatal("cycle reached the engine")
		}
	})

	t.Run("cycle closed through an existing member's dependency", func(t *testing.T) {
		t.Parallel()
		f := newParkedPlan(t)
		f.addMember(t, "m1", task.StatusNext, nil)
		m2 := &task.Task{
			ID: "m2", Title: "m2", WorkspaceID: "ws-1", PlanID: f.planID,
			AgentID: "ava", Status: task.StatusNext, BlockedBy: []string{"m1"},
		}
		if err := f.taskStore.Create(m2); err != nil {
			t.Fatalf("create m2: %v", err)
		}
		spy := &correctionSpy{}
		// m1 -> m2 already exists; adding m2 -> m1 closes the cycle.
		res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
			"plan_id": f.planID, "verb": "append", "falsified_assumption": "x",
			"tail_members": []any{tailMemberArg("t", "c")},
			"tail_edges":   []any{map[string]any{"from": "m2", "to": "m1"}},
		})
		if !res.IsError {
			t.Fatalf("cycle through an existing edge accepted: %s", res.ForLLM)
		}
		if len(spy.calls) != 0 {
			t.Fatal("cycle reached the engine")
		}
	})

	t.Run("a valid chain is accepted", func(t *testing.T) {
		t.Parallel()
		f := newParkedPlan(t)
		f.addMember(t, "m1", task.StatusDone, nil)
		spy := &correctionSpy{}
		res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
			"plan_id": f.planID, "verb": "append", "falsified_assumption": "x",
			"tail_members": []any{
				map[string]any{"ref": "a", "title": "A", "criteria": []any{criterionArg("c")}},
				map[string]any{"ref": "b", "title": "B", "criteria": []any{criterionArg("c")}},
			},
			"tail_edges": []any{
				map[string]any{"from": "m1", "to": "a"},
				map[string]any{"from": "a", "to": "b"},
			},
		})
		if res.IsError {
			t.Fatalf("valid chain rejected: %s", res.ForLLM)
		}
		edges := spy.calls[0].TailEdges
		if len(edges) != 2 {
			t.Fatalf("edges = %d, want 2", len(edges))
		}
		newIDs := map[string]bool{}
		for _, m := range spy.calls[0].TailMembers {
			newIDs[m.ID] = true
		}
		// The refs must have been resolved to the MINTED ids, never passed
		// through as literals — the engine wires edges by task id.
		if edges[0].FromTaskID != "m1" || !newIDs[edges[0].ToTaskID] {
			t.Errorf("edge 0 endpoints unresolved: %+v", edges[0])
		}
		if !newIDs[edges[1].FromTaskID] || !newIDs[edges[1].ToTaskID] {
			t.Errorf("edge 1 endpoints unresolved: %+v", edges[1])
		}
	})
}

// TestPlanCorrect_TailEdgesRejectBadEndpoints proves an edge naming something
// that is not a member of this plan — or naming the member being superseded,
// or itself — is rejected before any mutation.
func TestPlanCorrect_TailEdgesRejectBadEndpoints(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		verb string
		args map[string]any
	}{
		{"an unknown member id", "append", map[string]any{
			"tail_members": []any{map[string]any{"ref": "a", "title": "A", "criteria": []any{criterionArg("c")}}},
			"tail_edges":   []any{map[string]any{"from": "no-such-member", "to": "a"}},
		}},
		{"a member of another plan", "append", map[string]any{
			"tail_members": []any{map[string]any{"ref": "a", "title": "A", "criteria": []any{criterionArg("c")}}},
			"tail_edges":   []any{map[string]any{"from": "m-foreign", "to": "a"}},
		}},
		{"an unknown ref", "append", map[string]any{
			"tail_members": []any{map[string]any{"ref": "a", "title": "A", "criteria": []any{criterionArg("c")}}},
			"tail_edges":   []any{map[string]any{"from": "a", "to": "b"}},
		}},
		{"a self-edge", "append", map[string]any{
			"tail_members": []any{map[string]any{"ref": "a", "title": "A", "criteria": []any{criterionArg("c")}}},
			"tail_edges":   []any{map[string]any{"from": "a", "to": "a"}},
		}},
		{"an empty endpoint", "append", map[string]any{
			"tail_members": []any{map[string]any{"ref": "a", "title": "A", "criteria": []any{criterionArg("c")}}},
			"tail_edges":   []any{map[string]any{"from": "", "to": "a"}},
		}},
		{"the member being superseded", "supersede", map[string]any{
			"superseded_member_id": "m-done",
			"tail_members": []any{map[string]any{
				"ref": "a", "title": "A", "criteria": []any{criterionArg("parses ints")},
			}},
			"tail_edges": []any{map[string]any{"from": "m-done", "to": "a"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newParkedPlan(t)
			f.addMember(t, "m-done", task.StatusDone, []task.AcceptanceCriterion{proseCriterion("parses ints")})
			foreign := &task.Task{
				ID: "m-foreign", Title: "foreign", WorkspaceID: "ws-1", PlanID: "plan-other",
				AgentID: "ava", Status: task.StatusNext,
			}
			if err := f.taskStore.Create(foreign); err != nil {
				t.Fatalf("create foreign: %v", err)
			}
			spy := &correctionSpy{}
			args := map[string]any{"plan_id": f.planID, "verb": tc.verb, "falsified_assumption": "x"}
			for k, v := range tc.args {
				args[k] = v
			}
			res := f.tool(spy).Execute(supervisorCtx(), args)
			if !res.IsError {
				t.Fatalf("bad endpoint accepted: %s", res.ForLLM)
			}
			if len(spy.calls) != 0 {
				t.Fatal("bad endpoint reached the engine")
			}
		})
	}
}

// TestPlanCorrect_RejectsDuplicateAndCollidingRefs proves a request-local ref
// cannot shadow an existing member id or another tail member's ref — either
// would silently re-point an edge at the wrong task.
func TestPlanCorrect_RejectsDuplicateAndCollidingRefs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		members []any
	}{
		{"two members sharing a ref", []any{
			map[string]any{"ref": "a", "title": "A", "criteria": []any{criterionArg("c")}},
			map[string]any{"ref": "a", "title": "B", "criteria": []any{criterionArg("c")}},
		}},
		{"a ref shadowing an existing member id", []any{
			map[string]any{"ref": "m-done", "title": "A", "criteria": []any{criterionArg("c")}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newParkedPlan(t)
			f.addMember(t, "m-done", task.StatusDone, nil)
			spy := &correctionSpy{}
			res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
				"plan_id": f.planID, "verb": "append", "falsified_assumption": "x",
				"tail_members": tc.members,
			})
			if !res.IsError {
				t.Fatalf("colliding ref accepted: %s", res.ForLLM)
			}
			if len(spy.calls) != 0 {
				t.Fatal("colliding ref reached the engine")
			}
		})
	}
}

// --- happy paths -----------------------------------------------------------

// TestPlanCorrect_TargetedRetry proves the verb reaches the engine naming only
// the failed member, with no tail work attached.
func TestPlanCorrect_TargetedRetry(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	f.addMember(t, "m-failed", task.StatusFailed, nil)
	spy := &correctionSpy{}
	res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
		"plan_id": f.planID, "verb": "targeted_retry", "retried_member_id": "m-failed",
		"falsified_assumption": "we assumed the network was reachable",
		"reason":               "transient DNS failure",
	})
	if res.IsError {
		t.Fatalf("targeted_retry rejected: %s", res.ForLLM)
	}
	if len(spy.calls) != 1 {
		t.Fatalf("engine reached %d time(s)", len(spy.calls))
	}
	got := spy.calls[0]
	if got.Verb != plan.RevisionTargetedRetry {
		t.Errorf("verb = %q", got.Verb)
	}
	if got.RetriedMemberID != "m-failed" {
		t.Errorf("retried_member_id = %q", got.RetriedMemberID)
	}
	if len(got.TailMembers) != 0 || len(got.TailEdges) != 0 {
		t.Errorf("targeted_retry carried tail work: %+v / %+v", got.TailMembers, got.TailEdges)
	}
	if got.Reason != "transient DNS failure" {
		t.Errorf("reason = %q", got.Reason)
	}
}

// TestPlanCorrect_Abandon proves the honest exit is a reachable, first-class
// verb carrying only its diagnosis — an adjudicator that concludes "nothing
// here can fix this" has an observable artefact instead of silence.
func TestPlanCorrect_Abandon(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	f.addMember(t, "m-failed", task.StatusFailed, nil)
	spy := &correctionSpy{}
	res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
		"plan_id": f.planID, "verb": "abandon",
		"falsified_assumption": "we assumed the upstream API exposed a bulk endpoint; it does not",
		"reason":               "no decomposition of this plan can satisfy the DoD",
	})
	if res.IsError {
		t.Fatalf("abandon rejected: %s", res.ForLLM)
	}
	if len(spy.calls) != 1 {
		t.Fatalf("engine reached %d time(s)", len(spy.calls))
	}
	got := spy.calls[0]
	if got.Verb != plan.RevisionAbandon {
		t.Errorf("verb = %q, want abandon", got.Verb)
	}
	if len(got.TailMembers) != 0 || len(got.TailEdges) != 0 ||
		got.SupersededMemberID != "" || got.RetriedMemberID != "" {
		t.Errorf("abandon mutates a member: %+v", got)
	}
	if !strings.Contains(got.FalsifiedAssumption, "bulk endpoint") {
		t.Errorf("falsified_assumption = %q", got.FalsifiedAssumption)
	}
}

// TestPlanCorrect_ResultReportsTheCorrection proves the tool's own result is a
// usable read surface (D-03): verb, falsified assumption and the target member
// id all come back, matching what the audit entry records.
func TestPlanCorrect_ResultReportsTheCorrection(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	f.addMember(t, "m-done", task.StatusDone, []task.AcceptanceCriterion{proseCriterion("parses ints")})
	spy := &correctionSpy{revision: "rev-plan-42-1"}
	res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
		"plan_id": f.planID, "verb": "supersede", "superseded_member_id": "m-done",
		"falsified_assumption": "the parser's int path was never exercised",
		"tail_members":         []any{tailMemberArg("redo it", "parses ints")},
	})
	if res.IsError {
		t.Fatalf("supersede rejected: %s", res.ForLLM)
	}
	var out struct {
		PlanID              string   `json:"plan_id"`
		RevisionID          string   `json:"revision_id"`
		Verb                string   `json:"verb"`
		FalsifiedAssumption string   `json:"falsified_assumption"`
		SupersededMemberID  string   `json:"superseded_member_id"`
		CreatedMemberIDs    []string `json:"created_member_ids"`
		HonestExit          bool     `json:"honest_exit"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("parse result %q: %v", res.ForLLM, err)
	}
	if out.PlanID != f.planID || out.RevisionID != "rev-plan-42-1" || out.Verb != "supersede" {
		t.Errorf("unexpected result: %+v", out)
	}
	if out.SupersededMemberID != "m-done" {
		t.Errorf("superseded_member_id = %q", out.SupersededMemberID)
	}
	if !strings.Contains(out.FalsifiedAssumption, "int path") {
		t.Errorf("falsified_assumption = %q", out.FalsifiedAssumption)
	}
	if len(out.CreatedMemberIDs) != 1 {
		t.Errorf("created_member_ids = %v", out.CreatedMemberIDs)
	}
	if out.HonestExit {
		t.Error("honest_exit set on a correction the engine did not flag")
	}
}

// TestPlanCorrect_HonestExitIsReported proves an engine honest-exit is
// surfaced rather than reported as an ordinary success — the plan was FAILED,
// and the adjudicator must not go on believing it corrected it.
func TestPlanCorrect_HonestExitIsReported(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	spy := &correctionSpy{honest: true}
	res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
		"plan_id": f.planID, "verb": "append", "falsified_assumption": "x",
		"tail_members": []any{tailMemberArg("t", "c")},
	})
	if res.IsError {
		t.Fatalf("append rejected: %s", res.ForLLM)
	}
	var out struct {
		HonestExit bool   `json:"honest_exit"`
		Note       string `json:"note"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if !out.HonestExit {
		t.Fatalf("honest exit not reported: %s", res.ForLLM)
	}
	if !strings.Contains(out.Note, "failed") {
		t.Errorf("note does not say the plan failed: %q", out.Note)
	}
}

// TestPlanCorrect_EngineErrorIsSurfaced proves an engine rejection is reported
// as an error, never swallowed into a success the adjudicator would act on.
func TestPlanCorrect_EngineErrorIsSurfaced(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	spy := &correctionSpy{err: fmt.Errorf("plan_engine: AppendCorrection: commit: disk full")}
	res := f.tool(spy).Execute(supervisorCtx(), map[string]any{
		"plan_id": f.planID, "verb": "append", "falsified_assumption": "x",
		"tail_members": []any{tailMemberArg("t", "c")},
	})
	if !res.IsError {
		t.Fatalf("engine failure reported as success: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "disk full") {
		t.Errorf("engine error not surfaced: %s", res.ForLLM)
	}
}

// TestPlanCorrect_CallerCarriesTranscriptSession proves the engine receives
// the caller's identity AND its transcript session, which the engine's owner
// gate and audit trail both read.
func TestPlanCorrect_CallerCarriesTranscriptSession(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	spy := &correctionSpy{}
	ctx := WithTranscriptSessionID(supervisorCtx(), "sess-super-1")
	res := f.tool(spy).Execute(ctx, map[string]any{
		"plan_id": f.planID, "verb": "append", "falsified_assumption": "x",
		"tail_members": []any{tailMemberArg("t", "c")},
	})
	if res.IsError {
		t.Fatalf("append rejected: %s", res.ForLLM)
	}
	if spy.callers[0].AgentID != PlanSupervisorAgentID {
		t.Errorf("caller agent = %q", spy.callers[0].AgentID)
	}
	if spy.callers[0].SessionID != "sess-super-1" {
		t.Errorf("caller session = %q, want sess-super-1", spy.callers[0].SessionID)
	}
	if spy.planIDs[0] != f.planID {
		t.Errorf("plan id = %q", spy.planIDs[0])
	}
}

// TestPlanCorrect_SchemaExposesNoDoDOrOwner proves the tool's parameter schema
// is structurally incapable of redefining what success means or who is
// accountable for it — a correction adds work, it does not move the goalposts.
func TestPlanCorrect_SchemaExposesNoDoDOrOwner(t *testing.T) {
	t.Parallel()
	tool := NewPlanCorrectTool(nil, nil)
	props, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatal("parameters have no properties object")
	}
	for _, forbidden := range []string{"dod", "owner_agent_id", "bounds", "goal", "state"} {
		if _, present := props[forbidden]; present {
			t.Errorf("schema exposes %q", forbidden)
		}
	}
	members, ok := props["tail_members"].(map[string]any)
	if !ok {
		t.Fatal("tail_members is not an object schema")
	}
	items, ok := members["items"].(map[string]any)
	if !ok {
		t.Fatal("tail_members has no items schema")
	}
	memberProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("tail_members items have no properties")
	}
	if _, present := memberProps["id"]; present {
		t.Error("tail member schema exposes an id — member ids must be system-minted")
	}
	if _, present := memberProps["agent_id"]; present {
		t.Error("tail member schema exposes agent_id — the assignee is derived, not chosen")
	}
}

// TestPlanCorrect_NilStores_FailsClosed proves metadata-only construction
// cannot be executed against.
func TestPlanCorrect_NilStores_FailsClosed(t *testing.T) {
	t.Parallel()
	tool := NewPlanCorrectTool(nil, nil)
	tool.SetAppendCorrection((&correctionSpy{}).fn)
	res := tool.Execute(supervisorCtx(), map[string]any{
		"plan_id": "p1", "verb": "abandon", "falsified_assumption": "x",
	})
	if !res.IsError {
		t.Fatalf("nil stores reported success: %s", res.ForLLM)
	}
}
