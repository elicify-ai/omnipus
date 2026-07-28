// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
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

// ownerCaller returns the CorrectionCaller matching the owner identity seeded
// by mustSeedAwaitingCorrection (OwnerAgentID "owner", no OwnerSessionID).
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

	_, err := h.pe.AppendCorrection(ctx, "p-auto", ownerCaller(), CorrectionRequest{
		Verb:                CorrectionAppend,
		FalsifiedAssumption: "assumed the failed member would succeed",
		TailMembers: []task.Task{{
			ID: "m-tail", Title: "m-tail", WorkspaceID: "ws", Status: task.StatusNext,
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
	failed := failedMember("m-failed")
	mustSeedAwaitingCorrection(t, h, "p-sup", done, failed)

	_, err := h.pe.AppendCorrection(ctx, "p-sup", ownerCaller(), CorrectionRequest{
		Verb:                CorrectionSupersede,
		SupersededMemberID:  "m-supersede",
		FalsifiedAssumption: "assumed the done member's outcome was correct",
		Reason:              "the done member's result is wrong; ignore it and retry",
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

	_, err := h.pe.AppendCorrection(ctx, "p-retry", ownerCaller(), CorrectionRequest{
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

	_, err := h.pe.AppendCorrection(ctx, "p-imm", ownerCaller(), CorrectionRequest{
		Verb:   CorrectionAppend,
		Reason: "test immutability",
		TailMembers: []task.Task{{
			ID: "m-tail-imm", Title: "tail", WorkspaceID: "ws", Status: task.StatusNext,
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

	_, err := h.pe.AppendCorrection(ctx, "p-bad", ownerCaller(), CorrectionRequest{
		Verb:        CorrectionAppend,
		TailMembers: []task.Task{{ID: "m-x", Title: "x", WorkspaceID: "ws", Status: task.StatusNext}},
	})
	if err == nil {
		t.Fatal("AppendCorrection should reject a plan not in awaiting_supervision")
	}
}

// TestAppendCorrection_NonOwner_Rejected (sec-MAJOR-2, FR-143/G-11): only the
// plan's owner may correct it. A non-owner caller — wrong agent ID, empty
// agent identity, or a session that does not match the plan's durable
// OwnerSessionID — is rejected with ErrCorrectionNotOwner BEFORE any state
// mutation: no revision commits, no member resets, no phase change. The gate
// also runs before the phase check, so a non-owner cannot probe plan state
// via error differentiation.
func TestAppendCorrection_NonOwner_Rejected(t *testing.T) {
	ctx := context.Background()

	newReq := func() CorrectionRequest {
		return CorrectionRequest{
			Verb:   CorrectionAppend,
			Reason: "attempted correction",
			TailMembers: []task.Task{{
				ID: "m-tail-x", Title: "tail", WorkspaceID: "ws", Status: task.StatusNext,
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

	t.Run("wrong agent ID", func(t *testing.T) {
		h := newCorrectionHarness(t)
		mustSeedAwaitingCorrection(t, h, "p-no1", failedMember("m-failed-p-no1"))

		_, err := h.pe.AppendCorrection(ctx, "p-no1", CorrectionCaller{AgentID: "intruder"}, newReq())
		if !errors.Is(err, ErrCorrectionNotOwner) {
			t.Fatalf("want ErrCorrectionNotOwner, got %v", err)
		}
		assertNoMutation(t, h, "p-no1")
	})

	t.Run("empty agent identity", func(t *testing.T) {
		h := newCorrectionHarness(t)
		mustSeedAwaitingCorrection(t, h, "p-no2", failedMember("m-failed-p-no2"))

		_, err := h.pe.AppendCorrection(ctx, "p-no2", CorrectionCaller{}, newReq())
		if !errors.Is(err, ErrCorrectionNotOwner) {
			t.Fatalf("want ErrCorrectionNotOwner, got %v", err)
		}
		assertNoMutation(t, h, "p-no2")
	})

	t.Run("session mismatch when OwnerSessionID set", func(t *testing.T) {
		h := newCorrectionHarness(t)
		p := mustSeedAwaitingCorrection(t, h, "p-no3", failedMember("m-failed-p-no3"))
		ownerSession := "plan:p-no3"
		if _, err := h.plans.Update(p.ID, plan.Patch{OwnerSessionID: &ownerSession}); err != nil {
			t.Fatalf("set owner session: %v", err)
		}

		_, err := h.pe.AppendCorrection(ctx, "p-no3",
			CorrectionCaller{AgentID: "owner", SessionID: "session:someone-else"}, newReq())
		if !errors.Is(err, ErrCorrectionNotOwner) {
			t.Fatalf("want ErrCorrectionNotOwner, got %v", err)
		}
		assertNoMutation(t, h, "p-no3")
	})

	t.Run("owner with matching session accepted", func(t *testing.T) {
		h := newCorrectionHarness(t)
		p := mustSeedAwaitingCorrection(t, h, "p-yes1", failedMember("m-failed-p-yes1"))
		ownerSession := "plan:p-yes1"
		if _, err := h.plans.Update(p.ID, plan.Patch{OwnerSessionID: &ownerSession}); err != nil {
			t.Fatalf("set owner session: %v", err)
		}

		_, err := h.pe.AppendCorrection(ctx, "p-yes1",
			CorrectionCaller{AgentID: "owner", SessionID: "plan:p-yes1"}, newReq())
		if err != nil {
			t.Fatalf("owner correction with matching session should succeed: %v", err)
		}
	})

	t.Run("gate precedes phase check (no state probing)", func(t *testing.T) {
		h := newCorrectionHarness(t)
		// Plan is running but in dispatching phase (NOT awaiting correction).
		mustCreateRunningPlan(t, h.plans, "p-no4", "owner")

		_, err := h.pe.AppendCorrection(ctx, "p-no4", CorrectionCaller{AgentID: "intruder"}, newReq())
		if !errors.Is(err, ErrCorrectionNotOwner) {
			t.Fatalf("non-owner must get ErrCorrectionNotOwner even on a wrong-phase plan; got %v", err)
		}
	})
}
