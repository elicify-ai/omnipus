// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_lifecycle_producer_test.go closes the FR-118/G-13 gap identified
// against ADR-053 §5: a task/plan-member dispatch session (created by
// createTaskSessionSync/StartTaskNow, task_executor.go) previously had NO
// production constructor of session.LifecycleRecord at all — the boot sweep
// (boot_sweep.go) could never see it, let alone reconcile it to
// failed(interrupted) after a crash. These tests drive the REAL production
// dispatch path (ExecuteTask/StartTaskNow) rather than hand-constructing a
// LifecycleRecord — hand-constructing one is exactly what boot_sweep_test.go
// already does, and exactly why that file stayed green while the real
// producer was dark (this file's own governing brief called that out as
// this codebase's signature defect shape).
package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- Test 1: mintTaskLifecycleRecord's OwnerScopeKind mapping ---------------

// TestMintTaskLifecycleRecord_OwnerScopeMapping pins mintTaskLifecycleRecord's
// documented owner-scope mapping: a plan-member task (PlanID != "") names its
// plan as the durable owner (OwnerScopePlan/PlanID); a standalone task falls
// back to OwnerScopeHuman, the same default pkg/tools/delegate.go's own
// top-level mint uses. Calls the REAL production function directly (not a
// hand-built LifecycleRecord) against the lightweight TaskExecutor harness
// already used by orchestrator_advance_test.go.
func TestMintTaskLifecycleRecord_OwnerScopeMapping(t *testing.T) {
	te, _ := newTestTaskExecutor(t)
	ls := session.NewLifecycleStore(filepath.Join(t.TempDir(), "session_lifecycle"))
	te.SetLifecycleStore(ls)

	planTask := &task.Task{ID: "t-plan", AgentID: "agent-1", WorkspaceID: "ws", PlanID: "plan-1"}
	te.mintTaskLifecycleRecord("sess-plan", planTask)

	rec, err := ls.Load("sess-plan")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if rec.State != session.LifecycleQueued {
		t.Errorf("state = %q, want queued", rec.State)
	}
	if rec.OwnerScopeKind != session.OwnerScopePlan {
		t.Errorf("owner_scope_kind = %q, want plan", rec.OwnerScopeKind)
	}
	if rec.OwnerScopeID != "plan-1" {
		t.Errorf("owner_scope_id = %q, want plan-1", rec.OwnerScopeID)
	}
	if rec.AgentID != "agent-1" || rec.WorkspaceID != "ws" {
		t.Errorf("agent_id/workspace_id = %q/%q, want agent-1/ws", rec.AgentID, rec.WorkspaceID)
	}

	standaloneTask := &task.Task{ID: "t-standalone", AgentID: "agent-1", WorkspaceID: "ws"}
	te.mintTaskLifecycleRecord("sess-standalone", standaloneTask)

	rec2, err := ls.Load("sess-standalone")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if rec2.OwnerScopeKind != session.OwnerScopeHuman {
		t.Errorf("owner_scope_kind = %q, want human", rec2.OwnerScopeKind)
	}
	if rec2.OwnerScopeID != "" {
		t.Errorf("owner_scope_id = %q, want empty for a standalone task", rec2.OwnerScopeID)
	}
}

// TestMintTaskLifecycleRecord_NilStore_NoOp verifies mintTaskLifecycleRecord
// (and therefore createTaskSessionSync/StartTaskNow's calls to it) is a
// silent no-op when no lifecycle store is wired — task dispatch must proceed
// exactly as it did before this wave in that configuration.
func TestMintTaskLifecycleRecord_NilStore_NoOp(t *testing.T) {
	te, _ := newTestTaskExecutor(t)
	// No SetLifecycleStore call — te.lifecycleStore stays nil.
	te.mintTaskLifecycleRecord("sess-x", &task.Task{ID: "t-x", AgentID: "agent-1"})
	// No panic, nothing to assert against — the absence of a crash is the test.
}

// --- Test 2: ExecuteTask happy path — producer + Running + Completed -------

// TestExecuteTask_LifecycleRecord_HappyPathReachesCompleted drives a task
// through the REAL ExecuteTask dispatch path (the same primitive
// CheckQueuedTasks, advanceBlockedTasks, SpawnTriggeredRun, and plan-member
// dispatch all funnel through) end to end, and asserts its durable S2
// lifecycle record was minted, transitioned to running, and finalized to
// completed — proving the happy path is not regressed by wiring the new
// producer (item 3 of the parent brief: no lost functionality on the path
// that already worked, extended here to prove the NEW path also behaves).
func TestExecuteTask_LifecycleRecord_HappyPathReachesCompleted(t *testing.T) {
	worker := &scriptedProvider{responseBody: "doing it\n[goal:evidence] verified against c1\nTASK_STATUS: success\nTASK_SUMMARY: done"}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judgeInst.Provider = metJudgeProviderC1()

	ls := session.NewLifecycleStore(filepath.Join(t.TempDir(), "session_lifecycle"))
	al.taskExecutor.SetLifecycleStore(ls)

	tk := &task.Task{
		Title: "lifecycle happy path", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want done (result: %s)", final.Status, final.Result)
	}
	if final.SessionID == "" {
		t.Fatal("final task has no session id")
	}

	// Poll for the lifecycle record's terminal state rather than reading it
	// once. waitForCompletionContractTerminal returns as soon as the TASK is
	// terminal, but the lifecycle record is transitioned by a SEPARATE write
	// (finalizeTaskLifecycle -> transitionTaskLifecycle), so a single read can
	// legitimately observe the record still "running". That is a test race,
	// not a product defect: it surfaced only under -race, where the extra
	// instrumentation widens the window between the two writes.
	var rec *session.LifecycleRecord
	deadline := time.Now().Add(10 * time.Second)
	for {
		var err error
		rec, err = ls.Load(final.SessionID)
		if err != nil {
			t.Fatalf("load lifecycle record for %q: %v", final.SessionID, err)
		}
		if rec.State == session.LifecycleCompleted || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rec.State != session.LifecycleCompleted {
		t.Errorf("lifecycle state = %q, want completed", rec.State)
	}
	if rec.AgentID != "native-agent" {
		t.Errorf("agent_id = %q, want native-agent", rec.AgentID)
	}
	if rec.OwnerScopeKind != session.OwnerScopeHuman {
		t.Errorf("owner_scope_kind = %q, want human (no PlanID on this task)", rec.OwnerScopeKind)
	}
}

// --- Test 3: THE GAP — boot sweep reconciles a crashed task session --------

// TestBootSweep_ReconcilesCrashedTaskDispatchSession is the test that would
// have caught the original gap. It dispatches a task through the REAL
// production StartTaskNow path (mirrors a REST "Run" click), intercepts
// immediately after the goroutine genuinely starts running (goroutineCtxHook
// — an existing test seam, not a fake production path), and simulates a
// process crash by simply never letting finishTaskRun run for it. It then
// constructs a FRESH PlanEngine sharing the SAME durable stores (as a real
// restarted process would resolve them from disk) and runs the boot sweep,
// asserting BOTH:
//
//  1. the durable S2 LifecycleRecord reconciles to failed(interrupted)
//     (FR-118/G-13's own contract), and
//  2. the OTHER store — session.UnifiedMeta.Status, the field GET
//     /api/v1/sessions and the SPA actually read — reconciles to
//     StatusInterrupted too. Before this wave's fix, assertion (1) failed
//     outright (no LifecycleRecord ever existed to sweep); the fix for (1)
//     alone would still have left (2) failing forever, which is the
//     user-visible half of the gap the parent brief called out explicitly.
func TestBootSweep_ReconcilesCrashedTaskDispatchSession(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	ls := session.NewLifecycleStore(filepath.Join(t.TempDir(), "session_lifecycle"))
	al.taskExecutor.SetLifecycleStore(ls)

	started := make(chan struct{})
	al.taskExecutor.goroutineCtxHook = func(_ context.Context, _ string) {
		close(started)
		// Simulate the process being killed -9 right here: do nothing further,
		// never reach finishTaskRun.
	}

	tk := &task.Task{
		Title: "will be killed mid-flight", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", WorkspaceID: "default", Status: task.StatusInProgress,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	sessionID, err := al.taskExecutor.StartTaskNow(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTaskNow: %v", err)
	}
	if sessionID == "" {
		t.Fatal("StartTaskNow returned an empty session id")
	}

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("goroutine hook never fired")
	}

	// Pre-sweep baseline: this is what "crashed mid-run" looks like on disk —
	// non-terminal lifecycle record, still-active session meta.
	preRec, err := ls.Load(sessionID)
	if err != nil {
		t.Fatalf("load pre-sweep lifecycle record: %v", err)
	}
	if preRec.State != session.LifecycleRunning {
		t.Errorf("pre-sweep lifecycle state = %q, want running", preRec.State)
	}

	sessStore := al.GetAgentStore("native-agent")
	if sessStore == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	preMeta, err := sessStore.GetMeta(sessionID)
	if err != nil {
		t.Fatalf("get pre-sweep meta: %v", err)
	}
	if preMeta.Status != session.StatusActive {
		t.Errorf("pre-sweep session status = %q, want active", preMeta.Status)
	}

	// Simulate a fresh process boot: a NEW PlanEngine instance sharing the
	// SAME durable lifecycle store and the SAME agentLoop (a restarted
	// process resolves both from disk/config exactly this way).
	pe := &PlanEngine{agentLoop: al}
	pe.SetLifecycleStore(ls)
	pe.SetBootSweepBudget(5 * time.Second)
	pe.SetSnapshotMaxBytes(DefaultSnapshotMaxBytes)

	res := pe.runBootSweep(context.Background())
	if len(res.SweptToFailed) != 1 || res.SweptToFailed[0] != sessionID {
		t.Fatalf("SweptToFailed = %v, want [%s]", res.SweptToFailed, sessionID)
	}

	// Assertion (1): the durable S2 record reconciles.
	swept, err := ls.Load(sessionID)
	if err != nil {
		t.Fatalf("load post-sweep lifecycle record: %v", err)
	}
	if swept.State != session.LifecycleFailed {
		t.Errorf("post-sweep lifecycle state = %q, want failed", swept.State)
	}
	if swept.FailedReason != failedReasonInterrupted {
		t.Errorf("post-sweep failed_reason = %q, want %q", swept.FailedReason, failedReasonInterrupted)
	}

	// Assertion (2): the API-visible store reconciles too.
	postMeta, err := sessStore.GetMeta(sessionID)
	if err != nil {
		t.Fatalf("get post-sweep meta: %v", err)
	}
	if postMeta.Status != session.StatusInterrupted {
		t.Errorf("post-sweep session status = %q, want interrupted — the two stores must agree "+
			"after a boot sweep, not just the internal S2 record", postMeta.Status)
	}
}

// --- Test 4: retention prune is explicit and age/state-gated ---------------

// TestLifecycleStore_PruneTerminal_AgeAndStateGated pins PruneTerminal's
// contract directly (ADR-053 "nothing grows unbounded by omission" applied
// to the S2 store, which had no prune path at all before this wave): a
// terminal record is kept while still within the retention window, removed
// once the caller-supplied `now` moves past it, and a non-terminal record is
// NEVER removed regardless of age — deleting a stranded non-terminal record
// out from under the boot sweep would be worse than the unbounded-growth
// problem this function exists to solve.
//
// PruneTerminal takes `now` as an explicit parameter specifically so this
// age gate is testable without a real 90-day wall-clock wait or an
// unavailable stale-write path: persistLocked always stamps UpdatedAt to
// the REAL time.Now() (there is no way to fabricate a backdated record
// through the public API), so "old" vs "recent" is expressed here as two
// separate PruneTerminal calls against the SAME real timestamp, one with
// `now` and one with `now` advanced past the retention window — not as two
// records artificially pre-aged relative to each other.
func TestLifecycleStore_PruneTerminal_AgeAndStateGated(t *testing.T) {
	ls := session.NewLifecycleStore(filepath.Join(t.TempDir(), "session_lifecycle"))
	now := time.Now().UTC()

	mustPersist := func(rec *session.LifecycleRecord) {
		t.Helper()
		if err := ls.Persist(rec); err != nil {
			t.Fatalf("persist %q: %v", rec.SessionID, err)
		}
	}

	mustPersist(&session.LifecycleRecord{
		SessionID: "done", State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, AgentID: "a",
	})
	mustPersist(&session.LifecycleRecord{
		SessionID: "still-running", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, AgentID: "a",
	})

	// Still within the 90-day window (now, not advanced): neither record is
	// touched — "done" is terminal but not yet past retention.
	removed, err := ls.PruneTerminal(90, now)
	if err != nil {
		t.Fatalf("PruneTerminal(now): %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0 (nothing past retention yet)", removed)
	}
	if !ls.Exists("done") || !ls.Exists("still-running") {
		t.Fatal("a record was pruned before it aged past retention")
	}

	// Advance past the retention window: "done" (terminal) is removed;
	// "still-running" (non-terminal) MUST survive regardless.
	future := now.Add(91 * 24 * time.Hour)
	removed, err = ls.PruneTerminal(90, future)
	if err != nil {
		t.Fatalf("PruneTerminal(future): %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (only \"done\", terminal and past retention)", removed)
	}
	if ls.Exists("done") {
		t.Error("\"done\" still exists after prune")
	}
	if !ls.Exists("still-running") {
		t.Error("\"still-running\" (non-terminal) was pruned — MUST never be removed regardless of age")
	}

	// retentionDays<=0 is a no-op, mirroring UnifiedStore.RetentionSweep.
	removed2, err := ls.PruneTerminal(0, future)
	if err != nil {
		t.Fatalf("PruneTerminal(0): %v", err)
	}
	if removed2 != 0 {
		t.Fatalf("PruneTerminal(0) removed = %d, want 0", removed2)
	}
}
