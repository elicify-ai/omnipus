// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// plan_engine_adr055_fixwave_test.go pins the three defects that made ADR-055's
// headline feature — an adjudicated correction to a running plan — non-functional
// in production while every test in the suite passed.
//
// All three shipped for the SAME reason: each side was tested against a fake of
// the other, so no test ever ran a correction end to end. The suite asserted
// that AppendCorrection was called, that a dispatch was requested, and that a
// map entry was written — never that the corrected plan actually made progress,
// that the correction was issuable at all, or that superseding changed anything
// the Judge sees.
//
// Every test here therefore asserts the OBSERVABLE PROPERTY, not the mechanism:
//   - the tail member reaches a terminal `done` AFTER the supervisor's turn ends
//   - the wake prompt is sufficient to drive the REAL plan_correct tool
//   - the superseded outcome is absent from the REAL judge input
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- Finding 1: dispatch must not inherit the caller's cancellation --------

// turnBoundDispatcher is a planTaskDispatcher that reproduces
// TaskExecutor.ExecuteTask's context handling EXACTLY, because that handling is
// the whole defect: ExecuteTask derives the member's execution context with a
// plain `context.WithCancel(ctx)` and does NOT detach from the caller (unlike
// its sibling StartTaskNow, which detaches from context.Background() and says
// why: "The goroutine must outlive the request"). Everything else about a real
// member turn is irrelevant here, so this fake keeps only that: claim the task,
// derive the context the same way, and run the work on a goroutine that
// outlives the dispatch call.
//
// The work goroutine blocks on `release` so the test controls the interleaving
// exactly — no sleeps, no racy select between two ready channels. It then does
// what runTask does at the end of a turn: check whether the execution context
// died, and if so fail the member with the same "execution error: context
// canceled" shape a real cancelled member reports.
type turnBoundDispatcher struct {
	store   *task.Store
	release chan struct{}
	wg      sync.WaitGroup

	mu       sync.Mutex
	gotCtx   context.Context //nolint:containedctx // captured for assertion, never used to call
	storeErr error
}

func (d *turnBoundDispatcher) ExecuteTask(ctx context.Context, taskID string) error {
	return d.executeTaskPlanVerified(ctx, taskID)
}

func (d *turnBoundDispatcher) ClearEvidenceGateStreak(string) {}

func (d *turnBoundDispatcher) executeTaskPlanVerified(ctx context.Context, taskID string) error {
	inProgress := task.StatusInProgress
	if _, err := d.store.Update(taskID, task.Patch{Status: &inProgress}); err != nil {
		return err
	}
	// VERBATIM the derivation at task_executor.go's ExecuteTask.
	taskCtx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.gotCtx = taskCtx
	d.mu.Unlock()

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer cancel()
		<-d.release // the member's real work finishes here
		if cerr := taskCtx.Err(); cerr != nil {
			failed := task.StatusFailed
			result := "execution error: " + cerr.Error()
			if _, err := d.store.Update(taskID, task.Patch{Status: &failed, Result: &result}); err != nil {
				d.recordStoreErr(err)
			}
			return
		}
		done := task.StatusDone
		result := "tail work complete"
		completed := time.Now().UTC().Format(time.RFC3339)
		if _, err := d.store.Update(taskID, task.Patch{
			Status: &done, Result: &result, CompletedAt: &completed,
		}); err != nil {
			d.recordStoreErr(err)
		}
	}()
	return nil
}

func (d *turnBoundDispatcher) recordStoreErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.storeErr == nil {
		d.storeErr = err
	}
}

func (d *turnBoundDispatcher) capturedCtx() context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.gotCtx
}

// TestCorrection_TailMemberSurvivesTheSupervisorsTurnEnding is the regression
// test for ADR-055 fix-wave finding 1.
//
// AppendCorrection is reached from the PlanSupervisor's own adjudication turn,
// so the ctx it receives is a DESCENDANT of that turn's context — and
// dispatchPlanTurn cancels that context unconditionally when the turn ends
// (`defer cancel()`). Pre-fix, AppendCorrection handed that same ctx to
// dispatchReadyMembers, which handed it to a dispatcher that does not detach.
// The sequence below is therefore the real production sequence, not a
// contrived one: the correction commits, the tail member is dispatched, the
// supervisor emits its closing message a second or two later, its turn ends,
// and the tail member is killed with context.Canceled before it can finish.
//
// The plan then goes straight back to all-terminal -> judge -> UNMET -> park ->
// wake -> correct, until the round budget runs out. No correction's work could
// ever complete.
//
// This asserts the OUTCOME (the member reaches `done`), and separately the
// mechanism that guarantees it (the context handed to the dispatcher is not
// cancelled by the caller's cancel). Either alone would be weaker: the outcome
// alone would not say why, and the mechanism alone is what every previous test
// settled for.
func TestCorrection_TailMemberSurvivesTheSupervisorsTurnEnding(t *testing.T) {
	h := newCorrectionHarness(t)
	disp := &turnBoundDispatcher{store: h.tasks, release: make(chan struct{})}
	h.pe.dispatcher = disp

	mustSeedAwaitingCorrection(t, h, "p-turnctx", doneMember("m-done"))

	// The PlanSupervisor's turn context (dispatchPlanTurn's turnCtx).
	turnCtx, endTurn := context.WithCancel(context.Background())

	if _, err := h.pe.AppendCorrection(turnCtx, "p-turnctx", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionAppend,
		FalsifiedAssumption: "assumed the existing member covered the float case",
		TailMembers: []task.Task{{
			ID: "m-tail", Title: "handle the float case", WorkspaceID: "ws",
			Status:   task.StatusNext,
			Criteria: []task.AcceptanceCriterion{planProseCriterion("floats are handled")},
		}},
	}); err != nil {
		t.Fatalf("AppendCorrection: %v", err)
	}

	// Precondition: the correction actually reached dispatch. Without this the
	// rest of the test could pass vacuously.
	dispatchedCtx := disp.capturedCtx()
	if dispatchedCtx == nil {
		t.Fatal("the tail member was never dispatched — this test cannot observe what it is for")
	}

	// The supervisor finishes its turn seconds later. This is the event that
	// killed the tail member pre-fix.
	endTurn()

	// Errorf, not Fatalf: this is the precise DIAGNOSIS, but the outcome
	// assertion below is the actual contract, and it must be allowed to run and
	// fail on its own rather than being masked by this one.
	if err := dispatchedCtx.Err(); err != nil {
		t.Errorf("the context handed to the dispatcher died with the supervisor's turn (%v): "+
			"dispatchReadyMembers passed the caller's cancellable context straight through, so every "+
			"member a correction dispatches is killed at turn end", err)
	}

	// The member's work completes after the turn ended — the ordinary case,
	// since real work outlasts the seconds-long adjudication turn.
	close(disp.release)
	disp.wg.Wait()

	if disp.storeErr != nil {
		t.Fatalf("the member goroutine could not write its outcome: %v", disp.storeErr)
	}

	tail, err := h.tasks.Get("m-tail")
	if err != nil {
		t.Fatalf("get tail member: %v", err)
	}
	if tail.Status != task.StatusDone {
		t.Fatalf("tail member is %s (result %q); want done — a correction whose work cannot finish "+
			"is not a correction", tail.Status, tail.Result)
	}
}

// --- Finding 2: the wake must carry what plan_correct requires -------------

// wakeField extracts `plan_id: <value>` from a supervision wake prompt.
// Returns "" when the prompt does not carry one at all, which is the pre-fix
// state and the thing under test.
func wakePlanID(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "plan_id:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// wakeMemberID extracts the first `- <id> | <status> | <title>` member line
// with the requested status. Returns "" when the prompt lists no such member.
func wakeMemberID(prompt, status string) string {
	for _, line := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(trimmed, "- ")
		if !ok {
			continue
		}
		parts := strings.Split(rest, " | ")
		if len(parts) < 3 {
			continue
		}
		if strings.TrimSpace(parts[1]) == status {
			return strings.TrimSpace(parts[0])
		}
	}
	return ""
}

// newWiredPlanCorrectTool builds the REAL plan_correct tool over the harness's
// real stores, with the engine hook wired exactly as loop.go wires it in
// production. Executing this tool exercises the tool's own argument parsing and
// validation — which is the point: the question is not "does the engine accept
// a typed request" but "could the PlanSupervisor, holding only this wake
// prompt and only this tool, have issued the call at all".
func newWiredPlanCorrectTool(h *planEngineHarness) *tools.PlanCorrectTool {
	pct := tools.NewPlanCorrectTool(h.plans, h.tasks)
	pct.SetAppendCorrection(func(
		ctx context.Context, planID string, caller tools.CorrectionCaller, req tools.CorrectionRequest,
	) (string, bool, error) {
		res, err := h.pe.AppendCorrection(ctx, planID, caller, req)
		if err != nil {
			return "", false, err
		}
		return res.RevisionID, res.HonestExit, nil
	})
	return pct
}

// supervisorToolCtx is the tool-call context of a real PlanSupervisor turn:
// the identity gate in plan_correct reads it via tools.ToolAgentID.
func supervisorToolCtx() context.Context {
	return tools.WithAgentID(context.Background(), tools.PlanSupervisorAgentID)
}

// TestSupervisionWakes_CarryEverythingPlanCorrectNeeds is the regression test
// for ADR-055 fix-wave finding 2.
//
// The wake prompt is the adjudication turn's ONLY input, and the PlanSupervisor
// is seeded exactly one tool — plan_correct — with every other static tool
// denied. It has no way to resolve a plan TITLE to the plan_id plan_correct
// requires, and no way to discover a member id for supersede/targeted_retry.
// Pre-fix all three wake texts identified the plan by title alone, so every
// wake asked for a correction the agent was structurally incapable of issuing,
// while each wake still burned an attempt off the supervision budget.
//
// This does NOT substring-match the prompt for an id. It PARSES the prompt the
// way the agent must, then feeds what it found to the real tool's own argument
// validation and asserts the correction lands. A prompt that fails to carry the
// ids produces "plan_id is required" from the tool, which is exactly the
// failure the adjudicator hit in production.
func TestSupervisionWakes_CarryEverythingPlanCorrectNeeds(t *testing.T) {
	criterion := planProseCriterion("the parser handles floats")

	// Each case builds one of the three supervision wake texts through the
	// engine's own builder — the same function whose output wakeSupervisor
	// hands to the turn — then acts on it with the real tool.
	cases := []struct {
		name  string
		build func(h *planEngineHarness, p *plan.Plan, tasks []task.Task) string
	}{
		{
			name: "dod_unmet",
			build: func(h *planEngineHarness, p *plan.Plan, _ []task.Task) string {
				return h.pe.buildDoDUnmetWakeText(p, 1, "criterion c1: the floats are still wrong")
			},
		},
		{
			name: "stalled",
			build: func(_ *planEngineHarness, p *plan.Plan, tasks []task.Task) string {
				return buildStallWakeText(p, "every remaining member is blocked", tasks)
			},
		},
		{
			name: "supervision_retry",
			build: func(h *planEngineHarness, p *plan.Plan, _ []task.Task) string {
				return h.pe.buildSupervisionRetryWakeText(p)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCorrectionHarness(t)
			planID := "p-wake-" + tc.name
			done := doneMember("m-wrong-outcome")
			done.Criteria = []task.AcceptanceCriterion{criterion}
			p := mustSeedAwaitingCorrection(t, h, planID, done)

			tasks, err := h.tasks.List(task.Filter{PlanID: planID})
			if err != nil {
				t.Fatalf("list members: %v", err)
			}
			prompt := tc.build(h, p, tasks)

			// --- What the agent can actually read out of its only input ---
			gotPlanID := wakePlanID(prompt)
			if gotPlanID == "" {
				t.Fatalf("the %s wake names no plan_id — PlanSupervisor cannot call plan_correct at all.\n"+
					"Prompt was:\n%s", tc.name, prompt)
			}
			gotMemberID := wakeMemberID(prompt, string(task.StatusDone))
			if gotMemberID == "" {
				t.Fatalf("the %s wake lists no done member id — supersede is unissuable.\nPrompt was:\n%s",
					tc.name, prompt)
			}

			// --- Feed it to the REAL tool's argument validation ---
			res := newWiredPlanCorrectTool(h).Execute(supervisorToolCtx(), map[string]any{
				"plan_id":              gotPlanID,
				"verb":                 string(plan.RevisionSupersede),
				"falsified_assumption": "assumed the member's float handling was correct",
				"superseded_member_id": gotMemberID,
				"tail_members": []any{map[string]any{
					"title": "redo the float parsing",
					"criteria": []any{map[string]any{
						"kind": "prose", "text": criterion.Text,
					}},
				}},
			})
			if res == nil {
				t.Fatal("plan_correct returned no result")
			}
			if res.IsError {
				t.Fatalf("a correction built ONLY from the %s wake prompt was rejected: %s\nPrompt was:\n%s",
					tc.name, res.ForLLM, prompt)
			}

			// The correction really landed — not merely "the tool did not error".
			after, err := h.plans.Get(planID)
			if err != nil {
				t.Fatalf("get plan: %v", err)
			}
			if after.Supervision == nil || after.Supervision.CorrectionRounds != 1 {
				t.Fatalf("plan records %+v correction rounds; want exactly 1 applied correction",
					after.Supervision)
			}
		})
	}
}

// TestSupervisionWake_AbandonIsIssuableWithNoMembers pins the one verb that
// needs the plan id and NOTHING else: a plan with no members at all must still
// produce a wake the adjudicator can act on. The target block therefore emits
// plan_id unconditionally, before any member list.
func TestSupervisionWake_AbandonIsIssuableWithNoMembers(t *testing.T) {
	h := newCorrectionHarness(t)
	p := mustSeedAwaitingCorrection(t, h, "p-wake-empty")

	prompt := h.pe.buildDoDUnmetWakeText(p, 1, "criterion c1: nothing was produced")
	gotPlanID := wakePlanID(prompt)
	if gotPlanID == "" {
		t.Fatalf("a memberless plan's wake names no plan_id, so not even abandon is issuable.\n"+
			"Prompt was:\n%s", prompt)
	}

	res := newWiredPlanCorrectTool(h).Execute(supervisorToolCtx(), map[string]any{
		"plan_id":              gotPlanID,
		"verb":                 string(plan.RevisionAbandon),
		"falsified_assumption": "assumed this plan had work attached to it",
	})
	if res == nil || res.IsError {
		t.Fatalf("abandon built only from the wake prompt was rejected: %+v", res)
	}
}

// --- Finding 3: supersede must change what the Judge sees -----------------

// TestSupersede_OutcomeWithheldFromJudgeClaimText is the regression test for
// ADR-055 fix-wave finding 3, and the property TestSupersede_MarksDoneMemberIgnored
// never checked.
//
// Pre-fix, supersede was inert end to end. It wrote an in-memory map entry that
// NOTHING on the judge path read: buildPlanClaimText printed every member
// verbatim with no supersession filter, and gamingGuardEvidence — the only
// function that would have told the Judge about it — had a test as its sole
// caller. So the adjudicator could supersede a done member whose outcome was
// wrong, attach replacement work carrying its criteria, satisfy both integrity
// rules, receive a success result — and the next judge round still saw the
// original, discredited claim, exactly as if nothing had happened. Both the
// PlanSupervisor's rubric and the plan_correct tool description promise that
// outcome is "ignored by the Judge".
//
// This asserts against the REAL JudgeCriteriaInput the engine builds, not
// against the map.
func TestSupersede_OutcomeWithheldFromJudgeClaimText(t *testing.T) {
	h := newCorrectionHarness(t)
	criterion := planProseCriterion("the parser handles floats")

	const wrongClaim = "WRONG-CLAIM-FLOATS-ALREADY-WORK"
	const replacementEvidence = "REPLACEMENT-EVIDENCE-FLOATS-NOW-WORK"

	wrong := doneMember("m-wrong")
	wrong.Criteria = []task.AcceptanceCriterion{criterion}
	wrong.Result = wrongClaim
	mustSeedAwaitingCorrection(t, h, "p-supjudge", wrong)

	// The replacement finishes its work with distinctive evidence, so the
	// claim text can be checked for the presence of the RIGHT outcome as well
	// as the absence of the wrong one.
	h.disp.onDispatch = func(taskID string) error {
		done := task.StatusDone
		result := replacementEvidence
		_, err := h.tasks.Update(taskID, task.Patch{Status: &done, Result: &result})
		return err
	}

	if _, err := h.pe.AppendCorrection(context.Background(), "p-supjudge", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionSupersede,
		SupersededMemberID:  "m-wrong",
		FalsifiedAssumption: "assumed the done member's float handling was correct",
		TailMembers: []task.Task{{
			ID: "m-replacement", Title: "redo the float parsing", WorkspaceID: "ws",
			Status:   task.StatusNext,
			Criteria: []task.AcceptanceCriterion{criterion},
		}},
	}); err != nil {
		t.Fatalf("AppendCorrection supersede: %v", err)
	}

	// Every member is terminal again -> the next plan judge round runs.
	h.pe.processPlan(context.Background(), "p-supjudge")
	h.pe.judgeWG.Wait()

	if h.judge.callCount() != 1 {
		t.Fatalf("plan judge ran %d time(s); want exactly 1 post-correction round", h.judge.callCount())
	}
	h.judge.mu.Lock()
	in := h.judge.calls[0]
	h.judge.mu.Unlock()

	if strings.Contains(in.ClaimText, wrongClaim) {
		t.Errorf("the superseded member's outcome reached the Judge verbatim — supersede changed nothing "+
			"the Judge sees.\nClaimText was:\n%s", in.ClaimText)
	}
	if !strings.Contains(in.ClaimText, "SUPERSEDED") {
		t.Errorf("the superseded member is not marked in the claim text, so the Judge cannot tell its "+
			"outcome was withheld deliberately rather than lost.\nClaimText was:\n%s", in.ClaimText)
	}
	if !strings.Contains(in.ClaimText, replacementEvidence) {
		t.Errorf("the replacement member's outcome is missing from the claim text — superseding must "+
			"redirect the criterion onto the replacement, not delete the evidence for it.\nClaimText was:\n%s",
			in.ClaimText)
	}
	if !strings.Contains(in.ExtraContext, "Gaming-guard") || !strings.Contains(in.ExtraContext, "m-wrong") {
		t.Errorf("the gaming-guard block does not name the superseded member, so the Judge is never told "+
			"which member's evidence was discounted.\nExtraContext was:\n%s", in.ExtraContext)
	}
}

// --- Finding 4a: a tail member that cannot be created must be rejected ----

// TestValidateCorrection_RejectsUncreatableTailMembers pins the non-tool caller
// class validateCorrection's own doc comment says it exists for.
//
// buildCorrectionApplyFunc silently `continue`s past a tail member with an
// empty ID. For SUPERSEDE that produced the exact outcome the verb is guarded
// against: requireSupersedePairing and RequireCriteriaInheritance both pass
// (they inspect the REQUEST), the commit creates nothing, the call reports
// success — and the plan is left with a discredited done outcome and no
// replacement work at all. That is the bare discount, reached through the door
// the two integrity rules do not cover.
func TestValidateCorrection_RejectsUncreatableTailMembers(t *testing.T) {
	criterion := planProseCriterion("the parser handles floats")

	// Most cases run as SUPERSEDE, the verb where an uncreatable tail member is
	// not merely wasteful but unsound: supersede DISCOUNTS a done member's
	// evidence, so a replacement that is never created leaves the plan with the
	// bare discount FR-030/FR-030b exist to forbid.
	//
	// The missing-criteria case runs BOTH verbs. Under supersede it is the
	// vacuous-inheritance gap: RequireCriteriaInheritance returns nil early
	// when the superseded member has no criteria of its own, so without this
	// rule a criteria-less member could be replaced by criteria-less work and
	// both integrity rules would report the pairing intact. Under append it is
	// the plan_correct schema rule (criteria required on every tail member)
	// that the engine must mirror or else be a bypass around.
	cases := []struct {
		name   string
		verb   CorrectionVerb
		member task.Task
		// supersededHasNoCriteria seeds the superseded member WITHOUT criteria,
		// which is what makes RequireCriteriaInheritance vacuous and lets the
		// request reach validateCorrectionTailMembers.
		supersededHasNoCriteria bool
		want                    string
	}{
		{
			name: "no id",
			verb: CorrectionSupersede,
			member: task.Task{
				Title: "redo the float parsing", WorkspaceID: "ws", Status: task.StatusNext,
				Criteria: []task.AcceptanceCriterion{criterion},
			},
			want: "has no id",
		},
		{
			name: "id collides with an existing member",
			verb: CorrectionSupersede,
			member: task.Task{
				ID: "m-wrong", Title: "redo the float parsing", WorkspaceID: "ws", Status: task.StatusNext,
				Criteria: []task.AcceptanceCriterion{criterion},
			},
			want: "already a member of this plan",
		},
		{
			name: "empty title",
			verb: CorrectionSupersede,
			member: task.Task{
				ID: "m-new", Title: "   ", WorkspaceID: "ws", Status: task.StatusNext,
				Criteria: []task.AcceptanceCriterion{criterion},
			},
			want: "empty title",
		},
		{
			// Reaches the rule through the vacuous-inheritance gap: the
			// superseded member for this subtest carries no criteria of its
			// own, so RequireCriteriaInheritance passes trivially.
			name: "no acceptance criteria (supersede, superseded member had none)",
			verb: CorrectionSupersede,
			member: task.Task{
				ID: "m-new", Title: "redo the float parsing", WorkspaceID: "ws", Status: task.StatusNext,
			},
			supersededHasNoCriteria: true,
			want:                    "no acceptance criteria",
		},
		{
			name: "no acceptance criteria (append)",
			verb: CorrectionAppend,
			member: task.Task{
				ID: "m-new", Title: "add the float parsing", WorkspaceID: "ws", Status: task.StatusNext,
			},
			want: "no acceptance criteria",
		},
	}

	// The duplicate-id case needs two tail members, so it does not fit the
	// single-member table above; it is asserted separately below.

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCorrectionHarness(t)
			wrong := doneMember("m-wrong")
			if !tc.supersededHasNoCriteria {
				wrong.Criteria = []task.AcceptanceCriterion{criterion}
			}
			mustSeedAwaitingCorrection(t, h, "p-badtail", wrong)

			req := CorrectionRequest{
				Verb:                tc.verb,
				FalsifiedAssumption: "assumed the done member's float handling was correct",
				TailMembers:         []task.Task{tc.member},
			}
			if tc.verb == CorrectionSupersede {
				req.SupersededMemberID = "m-wrong"
			}

			_, err := h.pe.AppendCorrection(context.Background(), "p-badtail", supervisorCaller(), req)
			if err == nil {
				t.Fatalf("%s with an uncreatable sole tail member (%s) was ACCEPTED — the correction "+
					"is recorded as applied while creating none of its work", tc.verb, tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not explain the defect (%q)", err, tc.want)
			}

			// Rejected means nothing changed: no revision counted, the plan is
			// still parked exactly as the wake left it.
			after, gerr := h.plans.Get("p-badtail")
			if gerr != nil {
				t.Fatalf("get plan: %v", gerr)
			}
			if after.Supervision != nil && after.Supervision.CorrectionRounds != 0 {
				t.Errorf("a rejected correction still counted %d round(s)", after.Supervision.CorrectionRounds)
			}
			if after.EffectivePlanPhase() != plan.PhaseAwaitingSupervision {
				t.Errorf("plan left at phase %s after a rejected correction; want it still parked",
					after.EffectivePlanPhase())
			}
			if h.pe.isMemberSuperseded("p-badtail", "m-wrong") {
				t.Error("the member was marked superseded by a correction that was rejected")
			}
		})
	}
}

// TestValidateCorrection_RejectsDuplicateTailMemberIDs covers the two-member
// half of the same hole: two tail members sharing one id. The FIRST is created
// and the SECOND hits the apply func's "already exists, idempotent replay"
// branch and is skipped — so a supersede paired with two replacement members
// silently delivers one, and a caller that split the superseded member's
// criteria across the pair delivers only half of them while
// RequireCriteriaInheritance (which inspects the request, where both are
// present) reports the pairing intact.
func TestValidateCorrection_RejectsDuplicateTailMemberIDs(t *testing.T) {
	h := newCorrectionHarness(t)
	criterion := planProseCriterion("the parser handles floats")
	wrong := doneMember("m-wrong")
	wrong.Criteria = []task.AcceptanceCriterion{criterion}
	mustSeedAwaitingCorrection(t, h, "p-duptail", wrong)

	_, err := h.pe.AppendCorrection(context.Background(), "p-duptail", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionSupersede,
		SupersededMemberID:  "m-wrong",
		FalsifiedAssumption: "assumed the done member's float handling was correct",
		TailMembers: []task.Task{
			{
				ID: "m-dup", Title: "redo the float parsing", WorkspaceID: "ws", Status: task.StatusNext,
				Criteria: []task.AcceptanceCriterion{criterion},
			},
			{
				ID: "m-dup", Title: "verify the float parsing", WorkspaceID: "ws", Status: task.StatusNext,
				Criteria: []task.AcceptanceCriterion{criterion},
			},
		},
	})
	if err == nil {
		t.Fatal("two tail members sharing one id were ACCEPTED — only the first is created, so the " +
			"correction delivers less replacement work than it reports")
	}
	if !strings.Contains(err.Error(), "more than once") {
		t.Errorf("error %q does not explain the duplicate id", err)
	}
}

// --- Finding 4b: a terminal plan must not read as still-supervisable ------

// TestAbandon_LeavesAPhaseConsistentWithFailedState pins ADR-055 fix-wave
// finding 4: abandon fails the plan directly out of PhaseAwaitingSupervision,
// and failPlanLocked did not patch the phase — so the terminated plan kept
// reporting plan_phase=awaiting_supervision forever.
//
// That is not cosmetic. plan_phase describes what a RUNNING plan is doing, and
// pkg/plan's contract is that it reads idle whenever State != running. Left at
// awaiting_supervision, the record keeps satisfying
// plan.IsSupervisionEligiblePhase (so it still reads as correctable) and keeps
// matching the boot sweep's FR-118 exemption (b), which spares an owner session
// from the failed(interrupted) sweep on exactly that phase — for a plan that
// will never be resumed.
func TestAbandon_LeavesAPhaseConsistentWithFailedState(t *testing.T) {
	h := newCorrectionHarness(t)
	mustSeedAwaitingCorrection(t, h, "p-abandon", doneMember("m-done"))

	res, err := h.pe.AppendCorrection(context.Background(), "p-abandon", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionAbandon,
		FalsifiedAssumption: "assumed the DoD was reachable from a done-only member set",
	})
	if err != nil {
		t.Fatalf("AppendCorrection abandon: %v", err)
	}
	if !res.HonestExit {
		t.Error("abandon did not report the honest-exit path")
	}

	p, err := h.plans.Get("p-abandon")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if p.State != plan.StateFailed {
		t.Fatalf("plan is %s; want failed", p.State)
	}
	if p.FailedReason != plan.FailedReasonDoDUnreachable {
		t.Errorf("failed_reason is %q; want %q", p.FailedReason, plan.FailedReasonDoDUnreachable)
	}
	if got := p.EffectivePlanPhase(); got != plan.PhaseIdle {
		t.Errorf("an abandoned (failed) plan reports plan_phase=%s; want %s — a terminal plan must not "+
			"keep reading as awaiting supervision", got, plan.PhaseIdle)
	}
	if plan.IsSupervisionEligiblePhase(p.EffectivePlanPhase()) {
		t.Error("a terminated plan still reports a supervision-eligible phase")
	}
}
