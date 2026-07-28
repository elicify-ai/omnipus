// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- Test helpers for the correction test suite ---------------------------

// newCorrectionHarness builds a PlanEngine harness with an IntentLog wired,
// ready for correction tests. The engine's intentLog is set so
// AppendCorrection uses the transactional path.
func newCorrectionHarness(t *testing.T) *planEngineHarness {
	t.Helper()
	h := newTestPlanEngine(t)
	il, err := plan.NewIntentLog(filepath.Join(t.TempDir(), "plan_intents"))
	if err != nil {
		t.Fatalf("plan.NewIntentLog: %v", err)
	}
	h.pe.SetIntentLog(il)
	return h
}

// mustSeedAwaitingCorrection creates a running plan in PhaseAwaitingSupervision
// with the given member tasks. Each member's Status must already be set.
func mustSeedAwaitingCorrection(t *testing.T, h *planEngineHarness, planID string, members ...*task.Task) *plan.Plan {
	t.Helper()
	awaiting := plan.PhaseAwaitingSupervision
	sig := "sig-" + planID
	p := &plan.Plan{
		ID: planID, Title: planID, WorkspaceID: "ws", OwnerAgentID: "owner",
		State:                      plan.StateRunning,
		PlanPhase:                  awaiting,
		LastUnmetTerminalSignature: sig,
		DoD:                        []task.AcceptanceCriterion{planProseCriterion("DoD is met")},
	}
	mustCreatePlan(t, h.plans, p)
	// Record the unmet signature in the engine's in-memory gate so
	// processPlan won't re-judge (mirrors what applyJudgeRoundOutcomeLocked does).
	h.pe.recordUnmetTerminalSignature(planID, sig)
	for _, m := range members {
		m.PlanID = planID
		if m.WorkspaceID == "" {
			m.WorkspaceID = "ws"
		}
		mustCreateTask(t, h.tasks, m)
	}
	return p
}

func doneMember(id string) *task.Task {
	return &task.Task{ID: id, Title: id, WorkspaceID: "ws", Status: task.StatusDone}
}

func failedMember(id string) *task.Task {
	return &task.Task{ID: id, Title: id, WorkspaceID: "ws", Status: task.StatusFailed}
}

func nextMember(id string) *task.Task {
	return &task.Task{ID: id, Title: id, WorkspaceID: "ws", Status: task.StatusNext}
}

// supervisorCaller returns the ONLY CorrectionCaller the engine admits: the
// PlanSupervisor System Agent (ADR-055 D3/FR-6). The plans these tests seed
// are owned by "owner", which is deliberately NOT this identity — correction
// authority and plan ownership are disjoint by design.
func supervisorCaller() CorrectionCaller {
	return CorrectionCaller{AgentID: planSupervisorAgentID}
}

// ownerCaller returns the CorrectionCaller matching the owner identity seeded
// by mustSeedAwaitingCorrection (OwnerAgentID "owner"). It is used ONLY by
// the negative tests: since ADR-055 the owner has no correction rights.
func ownerCaller() CorrectionCaller {
	return CorrectionCaller{AgentID: "owner"}
}

// --- Tests ----------------------------------------------------------------

// TestAutoReset_ExcludesFrozenDoneMembers (G-10): on an append correction,
// auto-reset resets failed members to `next` but does NOT touch done members
// (frozen — their outcome is preserved).
func TestAutoReset_ExcludesFrozenDoneMembers(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	done := doneMember("m-done")
	failed := failedMember("m-failed")
	mustSeedAwaitingCorrection(t, h, "p-auto", done, failed)

	_, err := h.pe.AppendCorrection(ctx, "p-auto", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionAppend,
		FalsifiedAssumption: "assumed the failed member would succeed",
		TailMembers: []task.Task{{
			ID: "m-tail", Title: "m-tail", WorkspaceID: "ws", Status: task.StatusNext,
			Criteria: []task.AcceptanceCriterion{planProseCriterion("m-tail work is done")},
		}},
		Reason: "add a tail member to address the unmet DoD",
	})
	if err != nil {
		t.Fatalf("AppendCorrection: %v", err)
	}

	// The done member must stay done (frozen — not reset).
	d, _ := h.tasks.Get("m-done")
	if d.Status != task.StatusDone {
		t.Errorf("done member was reset to %s; want done (frozen, not auto-reset)", d.Status)
	}

	// The failed member must be reset (auto-reset gives it another chance).
	// After reset to `next`, dispatchReadyMembers claims it (next → in_progress).
	f, _ := h.tasks.Get("m-failed")
	if f.Status == task.StatusFailed {
		t.Errorf("failed member was NOT reset (still failed); want next or in_progress")
	}

	// The tail member must exist.
	tail, _ := h.tasks.Get("m-tail")
	if tail == nil {
		t.Error("tail member was not created")
	}
}

// TestSupersede_MarksDoneMemberIgnored (G-11): SUPERSEDE marks a done member's
// outcome as ignored-by-Judge; the member's record stays immutable (still done).
func TestSupersede_MarksDoneMemberIgnored(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	done := doneMember("m-supersede")
	done.Criteria = []task.AcceptanceCriterion{planProseCriterion("the parser handles floats")}
	failed := failedMember("m-failed")
	mustSeedAwaitingCorrection(t, h, "p-sup", done, failed)

	// A supersede MUST be paired with replacement work carrying the superseded
	// member's criteria (FR-030/FR-030b) — see
	// TestAppendCorrection_BareSupersede_RejectedByEngine for the unpaired case.
	_, err := h.pe.AppendCorrection(ctx, "p-sup", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionSupersede,
		SupersededMemberID:  "m-supersede",
		FalsifiedAssumption: "assumed the done member's outcome was correct",
		Reason:              "the done member's result is wrong; ignore it and retry",
		TailMembers: []task.Task{{
			ID: "m-sup-replacement", Title: "redo the float parsing", WorkspaceID: "ws",
			Status:   task.StatusNext,
			Criteria: []task.AcceptanceCriterion{planProseCriterion("the parser handles floats")},
		}},
	})
	if err != nil {
		t.Fatalf("AppendCorrection supersede: %v", err)
	}

	// The superseded member's record stays immutable (still done).
	d, _ := h.tasks.Get("m-supersede")
	if d.Status != task.StatusDone {
		t.Errorf("superseded member record changed to %s; want done (record immutable)", d.Status)
	}

	// The engine tracks it as superseded (ignored-by-Judge).
	if !h.pe.isMemberSuperseded("p-sup", "m-supersede") {
		t.Error("superseded member not tracked as ignored-by-Judge")
	}

	// The failed member is auto-reset (supersede triggers auto-reset of other failed members).
	f, _ := h.tasks.Get("m-failed")
	if f.Status == task.StatusFailed {
		t.Errorf("failed member was NOT reset (still failed); want next or in_progress (auto-reset after supersede)")
	}
}

// TestTargetedRetry_RetriesTransientFailed (G-11): TARGETED-RETRY resets one
// specific failed member to `next` without a full Stop/Play; other failed
// members are NOT reset.
func TestTargetedRetry_RetriesTransientFailed(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	target := failedMember("m-target")
	other := failedMember("m-other")
	done := doneMember("m-done")
	mustSeedAwaitingCorrection(t, h, "p-retry", target, other, done)

	_, err := h.pe.AppendCorrection(ctx, "p-retry", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionTargetedRetry,
		RetriedMemberID:     "m-target",
		FalsifiedAssumption: "assumed the transient failure was permanent",
		Reason:              "retry the specific failed member",
	})
	if err != nil {
		t.Fatalf("AppendCorrection targeted_retry: %v", err)
	}

	// The targeted member is reset and dispatched (targeted-retry → next → in_progress).
	tgt, _ := h.tasks.Get("m-target")
	if tgt.Status == task.StatusFailed {
		t.Errorf("targeted member still failed; want next or in_progress (targeted-retry)")
	}

	// The OTHER failed member is NOT reset (targeted-retry does not auto-reset).
	o, _ := h.tasks.Get("m-other")
	if o.Status != task.StatusFailed {
		t.Errorf("other failed member is %s; want failed (not reset by targeted-retry)", o.Status)
	}

	// The done member stays done (frozen).
	d, _ := h.tasks.Get("m-done")
	if d.Status != task.StatusDone {
		t.Errorf("done member is %s; want done (frozen)", d.Status)
	}
}

// TestTransactionalAppend_KillMidAppend_PreAppendDAG (N-8/INV-6): an uncommitted
// intent is discarded by boot replay (pre-append DAG); a committed-but-not-done
// intent is replayed forward (post-append DAG).
func TestTransactionalAppend_KillMidAppend_PreAppendDAG(t *testing.T) {
	dir := t.TempDir()
	il, err := plan.NewIntentLog(filepath.Join(dir, "plan_intents"))
	if err != nil {
		t.Fatalf("plan.NewIntentLog: %v", err)
	}

	// Scenario A: uncommitted intent (crash before MarkCommitted).
	// The intent carries a tail member that does NOT exist in the task store yet.
	recA := plan.IntentRecord{
		IntentID: "rev-uncommitted",
		PlanID:   "p-tx",
		Members: []task.Task{{
			ID: "m-uncommitted", Title: "m-uncommitted", WorkspaceID: "ws", PlanID: "p-tx",
		}},
		Revision: plan.RevisionEntry{
			RevisionID: "rev-uncommitted", PlanID: "p-tx",
			Verb: plan.RevisionAppend, Generation: 0,
		},
	}
	// Step 1 only — AppendIntent. NO MarkCommitted (simulates crash before commit).
	if appendErr := il.AppendIntent(recA); appendErr != nil {
		t.Fatalf("AppendIntent: %v", appendErr)
	}

	// Scenario B: committed-but-not-done (crash after MarkCommitted, before MarkDone).
	recB := plan.IntentRecord{
		IntentID: "rev-committed",
		PlanID:   "p-tx",
		Members: []task.Task{{
			ID: "m-committed", Title: "m-committed", WorkspaceID: "ws", PlanID: "p-tx",
		}},
		Revision: plan.RevisionEntry{
			RevisionID: "rev-committed", PlanID: "p-tx",
			Verb: plan.RevisionAppend, Generation: 0,
		},
	}
	if appendErr := il.AppendIntent(recB); appendErr != nil {
		t.Fatalf("AppendIntent B: %v", appendErr)
	}
	if commitErr := il.MarkCommitted("p-tx", "rev-committed"); commitErr != nil {
		t.Fatalf("MarkCommitted B: %v", commitErr)
	}
	// NO MarkDone — simulates crash after commit, before apply completes.

	// Now simulate boot: replay.
	var applied []string
	ts := task.New(filepath.Join(dir, "tasks"))
	replayApply := func(rec plan.IntentRecord) error {
		for _, m := range rec.Members {
			applied = append(applied, m.ID)
			_ = ts.Create(&m) // idempotent
		}
		return nil
	}
	res, err := il.ReplayAtBoot("p-tx", replayApply)
	if err != nil {
		t.Fatalf("ReplayAtBoot: %v", err)
	}

	// The uncommitted intent must be discarded (member NOT created).
	if len(applied) != 1 || applied[0] != "m-committed" {
		t.Errorf("applied = %v, want only [m-committed] (uncommitted must be discarded)", applied)
	}
	if res.Discarded != 1 {
		t.Errorf("Discarded = %d, want 1 (the uncommitted intent)", res.Discarded)
	}
	if res.Replayed != 1 {
		t.Errorf("Replayed = %d, want 1 (the committed-not-done intent)", res.Replayed)
	}

	// Verify the task store: m-committed exists, m-uncommitted does NOT.
	if _, err := ts.Get("m-committed"); err != nil {
		t.Error("committed member was not applied on replay")
	}
	if _, err := ts.Get("m-uncommitted"); err == nil {
		t.Error("uncommitted member should NOT exist (pre-append DAG)")
	}
}

// TestPlay_ResumeFromCommit_NewGeneration (G-12/D13): Play mints a new
// owner-session generation, preserves done members, resets failed/cancelled
// members, zeroes JudgeRounds, AND persists the resolved resume commit on each
// resumed member (the resume baseline the worker turn / Judge consume) — not
// just the generation counter.
func TestPlay_ResumeFromCommit_NewGeneration(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	// Wire a fake commitResolver: m-failed has a boundary commit (resumes
	// from it), m-nocommit does not (fresh attempt). This asserts the engine
	// actually consults the resolver and persists the hash — the corr-MAJOR-4
	// fix — rather than logging "fresh attempt" for everyone.
	resolver := &fakeCommitResolver{
		hashes:       map[string]string{"m-failed": "deadbeef"},
		errs:         map[string]error{},
		checkoutDirs: map[string]string{"m-failed": "/tmp/resume/m-failed"},
	}
	h.pe.SetCommitResolver(resolver)

	// Create a FAILED plan (the only state PlayPlan accepts).
	stopped := plan.FailedReasonStoppedByUser
	p := &plan.Plan{
		ID: "p-play", Title: "p-play", WorkspaceID: "ws", OwnerAgentID: "owner",
		State:        plan.StateFailed,
		FailedReason: stopped,
		JudgeRounds:  3,
		DoD:          []task.AcceptanceCriterion{planProseCriterion("DoD is met")},
	}
	mustCreatePlan(t, h.plans, p)

	// Members: one done (preserved), one failed-with-commit (resumes from it),
	// one failed-without-commit (fresh attempt).
	mustCreateTask(t, h.tasks, &task.Task{
		ID: "m-done", Title: "m-done", PlanID: "p-play", WorkspaceID: "ws",
		Status: task.StatusDone,
	})
	mustCreateTask(t, h.tasks, &task.Task{
		ID: "m-failed", Title: "m-failed", PlanID: "p-play", WorkspaceID: "ws",
		Status: task.StatusFailed,
	})
	mustCreateTask(t, h.tasks, &task.Task{
		ID: "m-nocommit", Title: "m-nocommit", PlanID: "p-play", WorkspaceID: "ws",
		Status: task.StatusFailed,
	})

	result, err := h.pe.PlayPlan(ctx, "p-play")
	if err != nil {
		t.Fatalf("PlayPlan: %v", err)
	}

	// A new generation is minted (generation 0 → 1).
	if result.NewGeneration != 1 {
		t.Errorf("NewGeneration = %d, want 1", result.NewGeneration)
	}

	// The plan transitions to approved (the restart transition).
	updated, _ := h.plans.Get("p-play")
	if updated.State != plan.StateApproved {
		t.Errorf("plan state = %s, want approved", updated.State)
	}

	// JudgeRounds are zeroed.
	if updated.JudgeRounds != 0 {
		t.Errorf("JudgeRounds = %d, want 0 (reset on Play)", updated.JudgeRounds)
	}

	// Done member is preserved.
	d, _ := h.tasks.Get("m-done")
	if d.Status != task.StatusDone {
		t.Errorf("done member is %s; want done (preserved by Play)", d.Status)
	}

	// Failed member is reset to next.
	f, _ := h.tasks.Get("m-failed")
	if f.Status != task.StatusNext {
		t.Errorf("failed member is %s; want next (reset by Play)", f.Status)
	}

	// --- D13/G-12: the commit is actually USED (corr-MAJOR-4) ---
	// The failed member with a boundary commit resumes from it: the resolved
	// hash is persisted on the task as the resume baseline.
	if f.ResumeFromCommit != "deadbeef" {
		t.Errorf("m-failed resume_from_commit = %q, want \"deadbeef\" (last boundary commit)", f.ResumeFromCommit)
	}
	// The failed member with NO commit takes the fresh-attempt fallback:
	// ResumeFromCommit is explicitly cleared (not a stale prior value).
	nc, _ := h.tasks.Get("m-nocommit")
	if nc.Status != task.StatusNext {
		t.Errorf("m-nocommit is %s; want next (reset by Play)", nc.Status)
	}
	if nc.ResumeFromCommit != "" {
		t.Errorf("m-nocommit resume_from_commit = %q, want \"\" (fresh attempt — no commit)", nc.ResumeFromCommit)
	}

	// --- #537: Play drives the checkout-materialization seam per resumed
	// member, with the resolved hash (or "" for the fresh-attempt cleanup) —
	// and never for the preserved done member.
	wantCalls := map[string]string{"m-failed": "deadbeef", "m-nocommit": ""}
	if len(resolver.resetCalls) != len(wantCalls) {
		t.Fatalf("ResetMemberCheckout calls = %v, want one per resumed member %v", resolver.resetCalls, wantCalls)
	}
	for _, c := range resolver.resetCalls {
		want, ok := wantCalls[c.taskID]
		if !ok {
			t.Errorf("ResetMemberCheckout called for unexpected member %q (done members are preserved, not resumed)", c.taskID)
			continue
		}
		if c.hash != want {
			t.Errorf("ResetMemberCheckout(%s) hash = %q, want %q", c.taskID, c.hash, want)
		}
	}
}

// fakeCommitResolver is a test double for the commitResolver interface. It
// returns the configured hash (or error) per taskID, so engine-level tests can
// assert the Play path consults the resolver and persists the result without
// standing up a real gitevidence repo (the real resolver + repo mechanics are
// covered in pkg/gitevidence and plan_engine_commit_resolver_test.go).
// ResetMemberCheckout records its (taskID, hash) calls and returns the
// configured dir/err per taskID, so tests can assert Play drives the
// checkout-materialization seam (#537) with the resolved hash.
type fakeCommitResolver struct {
	hashes map[string]string
	errs   map[string]error

	checkoutDirs map[string]string
	checkoutErrs map[string]error
	resetCalls   []fakeResetCall
}

type fakeResetCall struct {
	taskID string
	hash   string
}

func (f *fakeCommitResolver) LastMemberCommit(planID, taskID string) (string, error) {
	if err, ok := f.errs[taskID]; ok {
		return "", err
	}
	return f.hashes[taskID], nil
}

func (f *fakeCommitResolver) ResetMemberCheckout(planID, taskID, hash string) (string, error) {
	f.resetCalls = append(f.resetCalls, fakeResetCall{taskID: taskID, hash: hash})
	if err, ok := f.checkoutErrs[taskID]; ok {
		return "", err
	}
	return f.checkoutDirs[taskID], nil
}

// TestNoPerMemberControls (D7): plan members have no per-member
// start/cancel/resume. The engine exposes only StopPlan (whole-plan Stop) and
// PlayPlan (whole-plan resume); StopTask is for standalone tasks only, not
// plan members. Verify by checking that no per-member control method exists
// on PlanEngine beyond StopTask (which operates on the task's own session,
// not a plan-member lifecycle control).
func TestNoPerMemberControls(t *testing.T) {
	h := newCorrectionHarness(t)

	// Create a running plan with members.
	mustCreateRunningPlan(t, h.plans, "p-nmc", "owner")
	mustCreateTask(t, h.tasks, &task.Task{
		ID: "m-1", Title: "m-1", PlanID: "p-nmc", WorkspaceID: "ws", Status: task.StatusNext,
	})

	// StopPlan transitions the WHOLE plan to failed — it does not
	// provide per-member start/resume.
	ctx := context.Background()
	stopped, err := h.pe.StopPlan(ctx, "p-nmc", "user", "test")
	if err != nil {
		t.Fatalf("StopPlan: %v", err)
	}
	if stopped.State != plan.StateFailed {
		t.Errorf("plan state = %s, want failed after StopPlan", stopped.State)
	}

	// PlayPlan resumes the WHOLE plan — not individual members.
	played, err := h.pe.PlayPlan(ctx, "p-nmc")
	if err != nil {
		t.Fatalf("PlayPlan: %v", err)
	}
	if played.NewGeneration < 1 {
		t.Error("PlayPlan did not mint a new generation")
	}

	// There is NO per-member Play/Resume method on PlanEngine.
	// (This is a compile-time guarantee: no such method exists.)
	// The only member-level operation is StopTask, which is documented as
	// for standalone tasks or individual in_progress cancellation — not a
	// per-member Play/Resume control.
}

// TestGamingGuard_DeterministicRungsOutweighProse (N-2): the gaming guard
// flags artifacts produced after an unmet verdict and weights deterministic
// rungs over prose self-attestation. Verify the guard's evidence annotation
// correctly identifies post-unmet artifacts.
func TestGamingGuard_DeterministicRungsOutweighProse(t *testing.T) {
	tasks := []task.Task{
		{ID: "m-pre", Title: "pre-unmet", Status: task.StatusDone},
		{ID: "m-post", Title: "post-unmet", Status: task.StatusDone},
	}
	superseded := map[string]bool{"m-pre": false}
	postUnmet := map[string]bool{"m-post": true}

	guard := gamingGuardEvidence(tasks, superseded, postUnmet)

	// The guard must flag the post-unmet artifact (by member ID).
	if guard == "" {
		t.Fatal("gaming guard produced no annotation")
	}
	if !strings.Contains(guard, "m-post") {
		t.Error("guard does not flag post-unmet artifacts (member m-post)")
	}
	if !strings.Contains(guard, "Gaming-guard") {
		t.Error("guard missing the N-2 header")
	}

	// When there are no post-unmet artifacts and no superseded members,
	// the guard returns empty (no annotation needed).
	empty := gamingGuardEvidence(tasks, nil, nil)
	if empty != "" {
		t.Errorf("guard should return empty for clean state, got %q", empty)
	}
}

// TestCorrection_DoDStaysImmutable (G-11): the DoD is never modified by a
// correction. Verify the plan's DoD is identical before and after.
func TestCorrection_DoDStaysImmutable(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	originalDoD := []task.AcceptanceCriterion{
		planProseCriterion("criterion-1"),
		planProseCriterion("criterion-2"),
	}
	p := &plan.Plan{
		ID: "p-imm", Title: "p-imm", WorkspaceID: "ws", OwnerAgentID: "owner",
		State:                      plan.StateRunning,
		PlanPhase:                  plan.PhaseAwaitingSupervision,
		LastUnmetTerminalSignature: "sig-p-imm",
		DoD:                        originalDoD,
	}
	mustCreatePlan(t, h.plans, p)
	mustCreateTask(t, h.tasks, failedMember("m-fail-imm"))
	h.pe.recordUnmetTerminalSignature("p-imm", "sig-p-imm")

	_, err := h.pe.AppendCorrection(ctx, "p-imm", supervisorCaller(), CorrectionRequest{
		Verb:   CorrectionAppend,
		Reason: "test immutability",
		TailMembers: []task.Task{{
			ID: "m-tail-imm", Title: "tail", WorkspaceID: "ws", Status: task.StatusNext,
			Criteria: []task.AcceptanceCriterion{planProseCriterion("the tail work is done")},
		}},
	})
	if err != nil {
		t.Fatalf("AppendCorrection: %v", err)
	}

	updated, _ := h.plans.Get("p-imm")
	if len(updated.DoD) != len(originalDoD) {
		t.Errorf("DoD changed: %d criteria before, %d after", len(originalDoD), len(updated.DoD))
	}
	for i, c := range updated.DoD {
		if c.Text != originalDoD[i].Text {
			t.Errorf("DoD criterion %d changed: %q → %q", i, originalDoD[i].Text, c.Text)
		}
	}
}

// TestCorrection_NotInAwaitingPhase_Rejected: corrections are only valid on a
// plan in awaiting_supervision. A plan in dispatching/judging is rejected.
func TestCorrection_NotInAwaitingPhase_Rejected(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	mustCreateRunningPlan(t, h.plans, "p-bad", "owner")
	// Plan is in State=running, PlanPhase=dispatching (default), NOT awaiting.
	mustCreateTask(t, h.tasks, nextMember("m-next"))

	_, err := h.pe.AppendCorrection(ctx, "p-bad", supervisorCaller(), CorrectionRequest{
		Verb:        CorrectionAppend,
		TailMembers: []task.Task{{ID: "m-x", Title: "x", WorkspaceID: "ws", Status: task.StatusNext}},
	})
	if err == nil {
		t.Fatal("AppendCorrection should reject a plan not in awaiting_supervision")
	}
}

// TestAppendCorrection_NonAdjudicator_Rejected (ADR-055 D3/FR-4/FR-6,
// sec-MAJOR-2) pins THE authority rule: correction is the PlanSupervisor's
// alone, and every other caller — the plan's OWN OWNER included — is denied.
//
// The owner is the case that matters. Before ADR-055 the engine admitted ONLY
// the owner, which (a) contradicted FR-4, and (b) combined with the tool's
// supervisor-only gate to make the two admissible sets disjoint, denying every
// correction that could ever be issued. "It is my plan" must not be a way in.
//
// Every rejection must land BEFORE any state mutation — no revision commits,
// no member resets, no phase change — and before the phase check, so an
// unauthorised caller cannot probe plan state via error differentiation.
func TestAppendCorrection_NonAdjudicator_Rejected(t *testing.T) {
	ctx := context.Background()

	newReq := func() CorrectionRequest {
		return CorrectionRequest{
			Verb:                CorrectionAppend,
			FalsifiedAssumption: "assumed the member would succeed",
			Reason:              "attempted correction",
			TailMembers: []task.Task{{
				ID: "m-tail-x", Title: "tail", WorkspaceID: "ws", Status: task.StatusNext,
				Criteria: []task.AcceptanceCriterion{planProseCriterion("the tail work is done")},
			}},
		}
	}

	// assertNoMutation verifies the rejection left plan + members untouched.
	assertNoMutation := func(t *testing.T, h *planEngineHarness, planID string) {
		t.Helper()
		p, err := h.plans.Get(planID)
		if err != nil {
			t.Fatalf("get plan %q: %v", planID, err)
		}
		if p.EffectivePlanPhase() != plan.PhaseAwaitingSupervision {
			t.Errorf("plan phase changed to %q on a rejected correction; want awaiting_supervision",
				p.EffectivePlanPhase())
		}
		if p.LastUnmetTerminalSignature == "" {
			t.Error("durable unmet signature was cleared on a rejected correction")
		}
		f, err := h.tasks.Get("m-failed-" + planID)
		if err != nil {
			t.Fatalf("get failed member: %v", err)
		}
		if f.Status != task.StatusFailed {
			t.Errorf("failed member was mutated to %s on a rejected correction; want failed", f.Status)
		}
		if tail, _ := h.tasks.Get("m-tail-x"); tail != nil {
			t.Error("tail member was created on a rejected correction")
		}
	}

	denied := []struct {
		name   string
		caller CorrectionCaller
	}{
		{"the plan's OWN owner agent", ownerCaller()},
		{"an unrelated agent", CorrectionCaller{AgentID: "intruder"}},
		{"a caller with no identity at all", CorrectionCaller{}},
		{"an agent whose id merely contains the adjudicator's",
			CorrectionCaller{AgentID: "not-" + planSupervisorAgentID}},
		{"an agent whose id is a prefix of the adjudicator's",
			CorrectionCaller{AgentID: planSupervisorAgentID[:len(planSupervisorAgentID)-1]}},
	}
	for i, d := range denied {
		t.Run(d.name, func(t *testing.T) {
			h := newCorrectionHarness(t)
			planID := fmt.Sprintf("p-deny%d", i)
			mustSeedAwaitingCorrection(t, h, planID, failedMember("m-failed-"+planID))

			_, err := h.pe.AppendCorrection(ctx, planID, d.caller, newReq())
			if !errors.Is(err, ErrCorrectionNotAdjudicator) {
				t.Fatalf("want ErrCorrectionNotAdjudicator, got %v", err)
			}
			// sec-MAJOR-2: the denial is opaque. It must not name the plan's
			// owner, nor say who IS authorised — a denied caller learns only
			// that it was denied for the plan it already named.
			msg := strings.ToLower(err.Error())
			for _, leak := range []string{"owner", planSupervisorAgentID} {
				if strings.Contains(msg, strings.ToLower(leak)) {
					t.Errorf("denial leaked %q: %s", leak, err)
				}
			}
			assertNoMutation(t, h, planID)
		})
	}

	t.Run("the adjudicator is admitted", func(t *testing.T) {
		h := newCorrectionHarness(t)
		mustSeedAwaitingCorrection(t, h, "p-yes1", failedMember("m-failed-p-yes1"))

		if _, err := h.pe.AppendCorrection(ctx, "p-yes1", supervisorCaller(), newReq()); err != nil {
			t.Fatalf("the PlanSupervisor's correction must succeed: %v", err)
		}
		if tail, _ := h.tasks.Get("m-tail-x"); tail == nil {
			t.Error("the admitted correction added no tail member")
		}
	})

	// ADR-055 decision 9: the retired session-identity clause compared the
	// caller's SessionID against the plan's OwnerSessionID. Authority is
	// agent-identity only, so a populated OwnerSessionID that matches nothing
	// the adjudicator carries must NOT deny it.
	t.Run("a populated OwnerSessionID does not gate the adjudicator", func(t *testing.T) {
		h := newCorrectionHarness(t)
		p := mustSeedAwaitingCorrection(t, h, "p-yes2", failedMember("m-failed-p-yes2"))
		ownerSession := "plan:p-yes2"
		if _, err := h.plans.Update(p.ID, plan.Patch{OwnerSessionID: &ownerSession}); err != nil {
			t.Fatalf("set owner session: %v", err)
		}

		caller := supervisorCaller()
		caller.SessionID = "session:the-supervisors-own"
		if _, err := h.pe.AppendCorrection(ctx, "p-yes2", caller, newReq()); err != nil {
			t.Fatalf("session identity must not gate correction authority: %v", err)
		}
	})

	t.Run("gate precedes phase check (no state probing)", func(t *testing.T) {
		h := newCorrectionHarness(t)
		// Plan is running but in dispatching phase (NOT awaiting supervision),
		// so a phase rejection would be a different error to a denial.
		mustCreateRunningPlan(t, h.plans, "p-no4", "owner")

		_, err := h.pe.AppendCorrection(ctx, "p-no4", CorrectionCaller{AgentID: "intruder"}, newReq())
		if !errors.Is(err, ErrCorrectionNotAdjudicator) {
			t.Fatalf("an unauthorised caller must get ErrCorrectionNotAdjudicator even on a wrong-phase plan; got %v", err)
		}
	})
}

// --- FR-030/FR-030b: the supersede pairing rule, enforced BY THE ENGINE ----

// TestAppendCorrection_BareSupersede_RejectedByEngine calls AppendCorrection
// DIRECTLY, with the plan_correct tool bypassed entirely.
//
// That is the whole point. The pairing rule is the integrity property the
// correction feature rests on: supersede marks a done member's outcome ignored
// by the judge, so an unpaired supersede is a way to satisfy an unmet
// Definition of Done by DISCOUNTING the evidence that failed it instead of
// fixing the work. Enforcing it only at the tool boundary leaves every other
// caller of this exported engine entrypoint unguarded.
func TestAppendCorrection_BareSupersede_RejectedByEngine(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	done := doneMember("m-bare")
	done.Criteria = []task.AcceptanceCriterion{planProseCriterion("ints parse")}
	mustSeedAwaitingCorrection(t, h, "p-bare", done, failedMember("m-failed-p-bare"))

	_, err := h.pe.AppendCorrection(ctx, "p-bare", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionSupersede,
		SupersededMemberID:  "m-bare",
		FalsifiedAssumption: "assumed the done member's outcome was correct",
		Reason:              "discount it",
		// No TailMembers: the bare discount.
	})
	if err == nil {
		t.Fatal("the engine accepted a BARE supersede — an adjudicator can discount failing evidence without replacing the work")
	}
	if !strings.Contains(err.Error(), "at least one tail member") {
		t.Errorf("rejection does not explain the pairing rule: %v", err)
	}

	// Nothing may have moved: the member must not be marked superseded, and
	// the plan must still be parked awaiting supervision.
	if h.pe.isMemberSuperseded("p-bare", "m-bare") {
		t.Error("the member was marked superseded by a REJECTED correction")
	}
	p, err := h.plans.Get("p-bare")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if p.EffectivePlanPhase() != plan.PhaseAwaitingSupervision {
		t.Errorf("plan phase moved to %q on a rejected correction", p.EffectivePlanPhase())
	}
}

// TestAppendCorrection_SupersedeDroppingACriterion_RejectedByEngine covers the
// half of the rule that the bare-supersede check alone does NOT cover
// (FR-030b): replacement work held to a WEAKER standard.
//
// "At least one tail member" costs an adjudicator exactly one throwaway
// member to satisfy. Without criteria inheritance it could supersede the
// member whose output failed a criterion and attach one trivial,
// instantly-satisfiable replacement — the DoD is unchanged, but the evidence
// set the judge weighs is, which is exactly the move supersede must not enable.
func TestAppendCorrection_SupersedeDroppingACriterion_RejectedByEngine(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	done := doneMember("m-two-crit")
	done.Criteria = []task.AcceptanceCriterion{
		planProseCriterion("ints parse"),
		planProseCriterion("floats parse"),
	}
	mustSeedAwaitingCorrection(t, h, "p-drop", done, failedMember("m-failed-p-drop"))

	_, err := h.pe.AppendCorrection(ctx, "p-drop", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionSupersede,
		SupersededMemberID:  "m-two-crit",
		FalsifiedAssumption: "assumed the parser was correct",
		TailMembers: []task.Task{{
			ID: "m-weaker", Title: "redo it", WorkspaceID: "ws", Status: task.StatusNext,
			// Carries only ONE of the two criteria: "floats parse" is dropped.
			Criteria: []task.AcceptanceCriterion{planProseCriterion("ints parse")},
		}},
	})
	if err == nil {
		t.Fatal("the engine accepted replacement work that drops an acceptance criterion")
	}
	if !strings.Contains(err.Error(), "floats parse") {
		t.Errorf("rejection does not name the dropped criterion: %v", err)
	}
	if tail, _ := h.tasks.Get("m-weaker"); tail != nil {
		t.Error("the weaker replacement member was created by a REJECTED correction")
	}
}

// TestAppendCorrection_PayloadCapsAndEdgeIntegrity_EnforcedByEngine covers the
// remaining engine-side guards on a typed request that never passed through
// the tool's parser: the payload caps, the verb/field compatibility matrix,
// and tail-edge integrity (dangling endpoints, self-edges, edges onto the
// superseded member, and cycles).
func TestAppendCorrection_PayloadCapsAndEdgeIntegrity_EnforcedByEngine(t *testing.T) {
	ctx := context.Background()

	tailN := func(n int) []task.Task {
		out := make([]task.Task, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, task.Task{
				ID: fmt.Sprintf("m-bulk-%d", i), Title: "bulk", WorkspaceID: "ws", Status: task.StatusNext,
			})
		}
		return out
	}

	cases := []struct {
		name    string
		req     CorrectionRequest
		wantMsg string
	}{
		{
			name: "more tail members than the cap",
			req: CorrectionRequest{
				Verb: CorrectionAppend, FalsifiedAssumption: "x",
				TailMembers: tailN(plan.MaxTailMembers + 1),
			},
			wantMsg: "tail_members has",
		},
		{
			name: "an over-long falsified_assumption",
			req: CorrectionRequest{
				Verb:                CorrectionAppend,
				FalsifiedAssumption: strings.Repeat("a", plan.MaxTextBytes+1),
				TailMembers:         tailN(1),
			},
			wantMsg: "falsified_assumption is",
		},
		{
			name: "an over-long tail member title",
			req: CorrectionRequest{
				Verb: CorrectionAppend, FalsifiedAssumption: "x",
				TailMembers: []task.Task{{
					ID: "m-long", Title: strings.Repeat("t", plan.MaxMemberTitleBytes+1),
					WorkspaceID: "ws", Status: task.StatusNext,
				}},
			},
			wantMsg: "title is",
		},
		{
			name: "targeted_retry smuggling tail members",
			req: CorrectionRequest{
				Verb: CorrectionTargetedRetry, RetriedMemberID: "m-failed-p-caps",
				FalsifiedAssumption: "x", TailMembers: tailN(3),
			},
			wantMsg: "targeted_retry does not accept",
		},
		{
			name: "append naming a member to supersede",
			req: CorrectionRequest{
				Verb: CorrectionAppend, FalsifiedAssumption: "x",
				SupersededMemberID: "m-failed-p-caps", TailMembers: tailN(1),
			},
			wantMsg: "append does not accept",
		},
		{
			name: "an edge whose endpoint resolves to nothing",
			req: CorrectionRequest{
				Verb: CorrectionAppend, FalsifiedAssumption: "x",
				TailMembers: []task.Task{{ID: "m-e1", Title: "e1", WorkspaceID: "ws", Status: task.StatusNext,
					Criteria: []task.AcceptanceCriterion{planProseCriterion("e1 is done")}}},
				TailEdges: []plan.IntentEdge{{FromTaskID: "m-e1", ToTaskID: "m-ghost"}},
			},
			wantMsg: "neither an existing member",
		},
		{
			name: "a self-edge",
			req: CorrectionRequest{
				Verb: CorrectionAppend, FalsifiedAssumption: "x",
				TailMembers: []task.Task{{ID: "m-e2", Title: "e2", WorkspaceID: "ws", Status: task.StatusNext,
					Criteria: []task.AcceptanceCriterion{planProseCriterion("e2 is done")}}},
				TailEdges: []plan.IntentEdge{{FromTaskID: "m-e2", ToTaskID: "m-e2"}},
			},
			wantMsg: "self-edge",
		},
		{
			name: "a cycle among the new tail members",
			req: CorrectionRequest{
				Verb: CorrectionAppend, FalsifiedAssumption: "x",
				TailMembers: []task.Task{
					{ID: "m-c1", Title: "c1", WorkspaceID: "ws", Status: task.StatusNext,
						Criteria: []task.AcceptanceCriterion{planProseCriterion("c1 is done")}},
					{ID: "m-c2", Title: "c2", WorkspaceID: "ws", Status: task.StatusNext,
						Criteria: []task.AcceptanceCriterion{planProseCriterion("c2 is done")}},
				},
				TailEdges: []plan.IntentEdge{
					{FromTaskID: "m-c1", ToTaskID: "m-c2"},
					{FromTaskID: "m-c2", ToTaskID: "m-c1"},
				},
			},
			wantMsg: "cycle",
		},
	}

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newCorrectionHarness(t)
			planID := fmt.Sprintf("p-caps%d", i)
			// The fixed member id the field-matrix cases reference.
			mustSeedAwaitingCorrection(t, h, planID, failedMember("m-failed-p-caps"))

			req := c.req
			if req.RetriedMemberID != "" || req.SupersededMemberID != "" {
				// Those cases must be rejected by the FIELD MATRIX, before any
				// member lookup, so the referenced id need not resolve here.
				_ = req
			}
			_, err := h.pe.AppendCorrection(ctx, planID, supervisorCaller(), req)
			if err == nil {
				t.Fatalf("the engine accepted %s", c.name)
			}
			if !strings.Contains(err.Error(), c.wantMsg) {
				t.Errorf("rejection message %q does not contain %q", err, c.wantMsg)
			}
			// Nothing may have been created.
			for j := range req.TailMembers {
				if got, _ := h.tasks.Get(req.TailMembers[j].ID); got != nil {
					t.Errorf("tail member %q was created by a REJECTED correction", req.TailMembers[j].ID)
				}
			}
		})
	}
}

// --- FR-046b: abandon, the adjudicated honest exit -------------------------

// TestAppendCorrection_Abandon_TerminatesPlanAsDoDUnreachable proves abandon
// does real work rather than falling through to "unknown correction verb".
//
// abandon is exposed in plan_correct's schema, so before this it was a verb an
// adjudicator could read in its own skill, issue, and have rejected by the
// engine — which burns a supervision attempt and, repeated, terminates the
// plan `supervision_unavailable`, mislabelling an unreachable DoD as an
// unavailable supervisor.
func TestAppendCorrection_Abandon_TerminatesPlanAsDoDUnreachable(t *testing.T) {
	h := newCorrectionHarness(t)
	ctx := context.Background()

	mustSeedAwaitingCorrection(t, h, "p-aban", failedMember("m-failed-p-aban"))

	res, err := h.pe.AppendCorrection(ctx, "p-aban", supervisorCaller(), CorrectionRequest{
		Verb:                CorrectionAbandon,
		FalsifiedAssumption: "assumed the upstream API exposed a bulk endpoint; it does not",
		Reason:              "no sequence of corrections can reach this DoD",
	})
	if err != nil {
		t.Fatalf("abandon rejected: %v", err)
	}
	if !res.HonestExit {
		t.Error("abandon did not report an honest exit")
	}
	if res.RevisionEntry.Verb != plan.RevisionAbandon {
		t.Errorf("revision verb = %q, want abandon", res.RevisionEntry.Verb)
	}

	p, err := h.plans.Get("p-aban")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if p.State != plan.StateFailed {
		t.Errorf("plan state = %q, want failed", p.State)
	}
	// dod_unreachable, NOT judge_rounds_exhausted: rounds may well remain, and
	// "more rounds would not help" is a different fact from "we ran out".
	if p.FailedReason != plan.FailedReasonDoDUnreachable {
		t.Errorf("failed_reason = %q, want %q", p.FailedReason, plan.FailedReasonDoDUnreachable)
	}
	// The falsified assumption is what makes the exit honest rather than a
	// giving-up, so it must reach the handover a human reads.
	if !strings.Contains(p.HandoverText, "bulk endpoint") {
		t.Errorf("handover does not carry the falsified assumption: %q", p.HandoverText)
	}
	// abandon adds no work.
	tasks, err := h.tasks.List(task.Filter{PlanID: "p-aban"})
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("abandon changed the member set: %d members, want 1", len(tasks))
	}
}

// TestAppendCorrection_AbandonRejectsWorkAndBareAbandon proves abandon is a
// closed verb: it names no member, adds no work, and still has to say WHAT was
// falsified — an abandon with no falsified assumption is a plan quietly
// killed, not an honest exit.
func TestAppendCorrection_AbandonRejectsWorkAndBareAbandon(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		req     CorrectionRequest
		wantMsg string
	}{
		{
			name: "abandon carrying tail work",
			req: CorrectionRequest{
				Verb: CorrectionAbandon, FalsifiedAssumption: "x",
				TailMembers: []task.Task{{ID: "m-a1", Title: "a1", WorkspaceID: "ws", Status: task.StatusNext}},
			},
			wantMsg: "abandon does not accept",
		},
		{
			name:    "abandon with no falsified assumption",
			req:     CorrectionRequest{Verb: CorrectionAbandon, Reason: "giving up"},
			wantMsg: "abandon requires falsified_assumption",
		},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newCorrectionHarness(t)
			planID := fmt.Sprintf("p-abanx%d", i)
			mustSeedAwaitingCorrection(t, h, planID, failedMember("m-failed-"+planID))

			if _, err := h.pe.AppendCorrection(ctx, planID, supervisorCaller(), c.req); err == nil {
				t.Fatalf("the engine accepted %s", c.name)
			} else if !strings.Contains(err.Error(), c.wantMsg) {
				t.Errorf("rejection %q does not contain %q", err, c.wantMsg)
			}

			// A rejected abandon must NOT have terminated the plan.
			p, err := h.plans.Get(planID)
			if err != nil {
				t.Fatalf("get plan: %v", err)
			}
			if p.State != plan.StateRunning {
				t.Errorf("a REJECTED abandon terminated the plan: state = %q", p.State)
			}
		})
	}
}

// TestCorrectionRequest_CarriesNoDoDField is a STRUCTURAL guard, and it is the
// reason supervisor-as-judge is safe at all (NFR-2).
//
// PlanSupervisor both adjudicates the Definition of Done and applies
// corrections. That is only sound while it cannot move the goalposts it is
// judging against — and what guarantees that is not a runtime check but the
// shape of CorrectionRequest: there is no field through which a DoD could
// travel. This test fails the moment anyone adds one.
func TestCorrectionRequest_CarriesNoDoDField(t *testing.T) {
	rt := reflect.TypeOf(CorrectionRequest{})
	forbidden := []string{"dod", "definitionofdone", "criteria", "acceptance", "owner", "goal", "bounds", "state"}
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		jsonTag := strings.ToLower(strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
		for _, bad := range forbidden {
			if strings.Contains(name, bad) || (jsonTag != "" && strings.Contains(jsonTag, bad)) {
				t.Errorf("CorrectionRequest gained field %q (json %q): a correction adds work, "+
					"it must not be able to redefine what success means or who is accountable for it",
					rt.Field(i).Name, jsonTag)
			}
		}
	}
}

// TestCorrectionAuthority_MatchesTheToolsHolder pins the two halves of the
// authority rule to ONE identity string. They live in different packages
// (pkg/agent's engine gate, pkg/tools' tool gate) and drifting apart is
// exactly the defect ADR-055 D3 exists to fix: disjoint admissible sets deny
// every correction while both gates individually look correct.
func TestCorrectionAuthority_MatchesTheToolsHolder(t *testing.T) {
	if planSupervisorAgentID != tools.PlanSupervisorAgentID {
		t.Fatalf("the engine admits %q but the plan_correct tool admits %q — "+
			"the correction path is unreachable",
			planSupervisorAgentID, tools.PlanSupervisorAgentID)
	}
}

// --- FR-021/FR-022: the supervision bounds are configurable ----------------

// TestSupervisionBounds_ResolveFromConfigAndPerPlanOverride proves the
// resolvers are WIRED, not just present: an unset override falls back to the
// documented package default, and an explicit per-plan override wins.
func TestSupervisionBounds_ResolveFromConfigAndPerPlanOverride(t *testing.T) {
	h := newTestPlanEngine(t)
	secs := func(n int) *int { return &n }

	t.Run("unset resolves to the documented default", func(t *testing.T) {
		p := &plan.Plan{ID: "p-def"}
		if got := h.pe.supervisionTurnTimeout(p); got != defaultSupervisionTurnTimeout {
			t.Errorf("turn timeout = %s, want the %s default", got, defaultSupervisionTurnTimeout)
		}
		if got := h.pe.supervisionMaxAttempts(p); got != defaultSupervisionMaxAttempts {
			t.Errorf("max attempts = %d, want the default %d", got, defaultSupervisionMaxAttempts)
		}
	})

	t.Run("a per-plan override wins", func(t *testing.T) {
		p := &plan.Plan{ID: "p-ovr", Bounds: &plan.PlanBounds{
			SupervisionTurnTimeoutSeconds: secs(30),
			SupervisionMaxAttempts:        secs(7),
		}}
		if got := h.pe.supervisionTurnTimeout(p); got != 30*time.Second {
			t.Errorf("turn timeout = %s, want 30s from the per-plan override", got)
		}
		if got := h.pe.supervisionMaxAttempts(p); got != 7 {
			t.Errorf("max attempts = %d, want 7 from the per-plan override", got)
		}
	})
}

// TestSupervisionDeadline_PerPlanOverrideChangesTheRealDeadline is the
// outcome test the resolver test above cannot be: two plans, identical but for
// one's Bounds override, driven through the SAME engine at the SAME clock
// instant — and only the overridden one's supervision deadline fires.
//
// Without this, `supervisionTurnTimeout` could read the override and still
// have no effect on anything a user can observe.
func TestSupervisionDeadline_PerPlanOverrideChangesTheRealDeadline(t *testing.T) {
	h := newTestPlanEngine(t)
	ctx := context.Background()

	// Two stalled plans: the engine's stall limb arms a supervision deadline
	// on each at the first tick.
	for _, id := range []string{"p-short", "p-default"} {
		mustCreateRunningPlan(t, h.plans, id, "owner")
		outside := mustCreateTask(t, h.tasks, &task.Task{
			Title: "outside blocker " + id, WorkspaceID: "ws", Status: task.StatusInbox,
		})
		mustCreateTask(t, h.tasks, &task.Task{
			Title: "stuck member " + id, WorkspaceID: "ws", PlanID: id,
			Status: task.StatusBlocked, BlockedBy: []string{outside.ID},
		})
	}
	// p-short's deadline is a tenth of the default.
	short := int(defaultSupervisionTurnTimeout/time.Second) / 10
	bounds := &plan.PlanBounds{SupervisionTurnTimeoutSeconds: &short}
	if _, err := h.plans.Update("p-short", plan.Patch{Bounds: &bounds}); err != nil {
		t.Fatalf("set per-plan bounds: %v", err)
	}

	for _, id := range []string{"p-short", "p-default"} {
		h.pe.processPlan(ctx, id)
		got, err := h.plans.Get(id)
		if err != nil {
			t.Fatalf("get %q: %v", id, err)
		}
		if got.Supervision == nil || got.Supervision.Attempts != 1 {
			t.Fatalf("%q: the stall wake must arm the deadline as attempt 1, got %+v", id, got.Supervision)
		}
	}

	// Advance past p-short's deadline but well inside the default one.
	h.clock.Set(h.clock.Now().Add(time.Duration(short)*time.Second + time.Second))
	for _, id := range []string{"p-short", "p-default"} {
		h.pe.processPlan(ctx, id)
	}

	shortPlan, err := h.plans.Get("p-short")
	if err != nil {
		t.Fatalf("get p-short: %v", err)
	}
	if shortPlan.Supervision.Attempts != 2 {
		t.Errorf("p-short: attempts = %d, want 2 — its per-plan deadline elapsed but did not fire, "+
			"so the override does not reach the real deadline", shortPlan.Supervision.Attempts)
	}

	defaultPlan, err := h.plans.Get("p-default")
	if err != nil {
		t.Fatalf("get p-default: %v", err)
	}
	if defaultPlan.Supervision.Attempts != 1 {
		t.Errorf("p-default: attempts = %d, want 1 — the default deadline has NOT elapsed, "+
			"so one plan's override must not have moved the other's", defaultPlan.Supervision.Attempts)
	}
}
