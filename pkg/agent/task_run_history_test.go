// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Coverage for ADR-050's per-task run-history executor seam
// (docs/internal/architecture/ADR-050-task-run-history-model.md,
// docs/internal/specs/task-run-history-spec.md §3.2-3.4): TaskTriggerScheduler
// threading occurrenceMs into dispatch, TaskExecutor opening a TaskRun at
// claim and closing it at completion for both the marker path and the
// update_task-tool path, the Task.status mirror continuing to cycle exactly
// as before (regression), scheduled vs manual RunKind, and
// StartOccurrenceRun's idempotency against a concurrent run-open.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// drainEventsWithTimeout blocks on sub.C, collecting every event received
// (of any kind), until it has observed at least wantStatus
// EventKindTaskStatusChanged events AND wantRun EventKindTaskRunStatus
// events for taskID, or timeout elapses — then grabs any further
// already-buffered events non-blockingly. A blocking receive (unlike a bare
// non-blocking snapshot drain) is required here: a run's close events are
// published from the SAME goroutine that performs the task's completion
// writes, moments AFTER waitForCompletionContractTerminal's polling
// goroutine can observe those writes on disk — there is no happens-before
// edge between "the test goroutine observed the terminal write" and "the
// worker goroutine has already reached its LATER emit call", so only an
// actual channel receive (which the Go memory model does synchronize) can
// deterministically wait for those events rather than racing a snapshot
// read against them.
//
// Counting toward a fixed total event count (an earlier version of this
// helper stopped as soon as ANY minCount events of ANY kind arrived) is NOT
// sufficient: a single task execution also emits turn_start/llm_request/
// llm_response/turn_end (and possibly more) events interleaved with the two
// status-changed and two run-status events under test. If those unrelated
// events happen to arrive first — a timing detail sensitive to system load,
// which is exactly why this only reproduced under contention — the
// count-based loop could stop having collected 4 events none of which are
// the LATER completion signals, and the trailing non-blocking drain would
// then return immediately without them (they had not been emitted yet).
// Waiting for the SPECIFIC signals of interest (or a genuine timeout, which
// would indicate a real functional problem, not a race artifact) removes
// that hazard.
func drainEventsWithTimeout(
	t *testing.T, sub EventSubscription, taskID string, wantStatus, wantRun int, timeout time.Duration,
) []Event {
	t.Helper()
	var out []Event
	var statusCount, runCount int
	deadline := time.After(timeout)
	for statusCount < wantStatus || runCount < wantRun {
		select {
		case evt, ok := <-sub.C:
			if !ok {
				return out
			}
			out = append(out, evt)
			switch evt.Kind {
			case EventKindTaskStatusChanged:
				if p, ok := evt.Payload.(TaskStatusChangedPayload); ok && p.TaskID == taskID {
					statusCount++
				}
			case EventKindTaskRunStatus:
				if p, ok := evt.Payload.(TaskRunStatusPayload); ok && p.TaskID == taskID {
					runCount++
				}
			}
		case <-deadline:
			return out
		}
	}
	for {
		select {
		case evt, ok := <-sub.C:
			if !ok {
				return out
			}
			out = append(out, evt)
		default:
			return out
		}
	}
}

// TestTaskTrigger_RunScheduled_ThreadsOccurrenceMsToDispatch is a pure
// task_trigger.go-level test (stub dispatch, no real agent execution):
// RunScheduled must pass the fired job's Schedule.AtMS through to dispatch as
// occurrenceMs ONLY for an RRULE-backed recurring fire, and nil for a
// non-recurring (once) trigger — task-run-history-spec.md §3.2, ADR-050 RD3.
func TestTaskTrigger_RunScheduled_ThreadsOccurrenceMsToDispatch(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	t.Run("rrule_fire_passes_the_occurrence_instant", func(t *testing.T) {
		dtstart := secondAligned(clk.Now().Add(time.Minute))
		tsk := makeRruleTask(t, store, "agent-occms-rrule", "FREQ=DAILY", dtstart, "UTC")
		sched.OnTaskUpserted(tsk)

		jobs := jobsForTask(sched, tsk.ID)
		if len(jobs) != 1 || jobs[0].Schedule.AtMS == nil {
			t.Fatalf("setup: expected 1 armed job with AtMS set, got %+v", jobs)
		}
		wantOccurrenceMs := *jobs[0].Schedule.AtMS

		clk.Advance(2 * time.Minute)
		sched.RunDueJobs(clk.Now())
		sched.WaitForLane()

		occs := rec.occurrences()
		if len(occs) != 1 {
			t.Fatalf("expected 1 dispatch, got %d", len(occs))
		}
		if occs[0] == nil || *occs[0] != wantOccurrenceMs {
			t.Fatalf("occurrence_ms = %v, want %d", derefI64(occs[0]), wantOccurrenceMs)
		}
	})

	t.Run("once_fire_passes_nil", func(t *testing.T) {
		fireAt := clk.Now().Add(time.Minute).UnixMilli()
		tsk := makeTask(t, store, "agent-occms-once", &task.Trigger{
			Type:   task.TriggerOnce,
			Config: task.TriggerConfig{AtMs: int64P(fireAt)},
		})
		sched.OnTaskUpserted(tsk)

		clk.Advance(2 * time.Minute)
		sched.RunDueJobs(clk.Now())
		sched.WaitForLane()

		// occurrences() accumulates across BOTH sub-tests sharing the same
		// scheduler/recorder (subtests run sequentially, no t.Parallel here) —
		// the once fire is the SECOND dispatch recorded, after the rrule one.
		occs := rec.occurrences()
		if len(occs) != 2 {
			t.Fatalf("expected 2 total dispatches (rrule + once), got %d", len(occs))
		}
		if occs[1] != nil {
			t.Fatalf("once-trigger occurrence_ms = %v, want nil", *occs[1])
		}
	})
}

// TestTaskRunHistory_OpenedAtClaim_ClosedOnMarkerCompletion proves the
// end-to-end marker-completion path (ADR-050 §3.2/3.3): ExecuteTask's claim
// opens a TaskRun, and finishTaskRun's fallthrough to completeTaskWithResult
// closes it with the TASK_SUMMARY marker's own result — the same values the
// Task.status/result mirror carries.
func TestTaskRunHistory_OpenedAtClaim_ClosedOnMarkerCompletion(t *testing.T) {
	provider := &scriptedProvider{
		responseBody: "Implemented the thing.\n" +
			"TASK_STATUS: success\n" +
			"TASK_SUMMARY: All done.",
	}
	al := newNativeTaskCompletionTestLoop(t, provider)
	tk := newCompletionContractTask(t, al, "native-agent", "run history marker path")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want done (result: %s)", final.Status, final.Result)
	}

	// waitForRunClosed, not a raw ListRuns right after the Task.status mirror
	// goes terminal (Constraint #7 fix, matching MirrorStillCycles): the
	// mirror write (completeTaskWithResult's te.store.Update) happens BEFORE
	// closeRun in task_executor.go, so waitForCompletionContractTerminal can
	// observe the terminal mirror moments before the run's own EndedAt lands
	// — polling the run store directly is what actually waits on the
	// guarantee under test instead of racing it. See waitForRunClosed's own
	// doc comment below.
	run := waitForRunClosed(t, al, tk.ID, 5*time.Second)
	if run.Kind != task.RunKindScheduled {
		t.Errorf("kind = %q, want %q (ExecuteTask's default kind)", run.Kind, task.RunKindScheduled)
	}
	if run.OccurrenceMs != nil {
		t.Errorf("occurrence_ms = %v, want nil (ExecuteTask was called with nil)", *run.OccurrenceMs)
	}
	if run.Status != task.StatusDone {
		t.Errorf("run status = %q, want done", run.Status)
	}
	if run.Result != "All done." {
		t.Errorf("run result = %q, want %q", run.Result, "All done.")
	}
	if run.SessionID == "" || run.SessionID != final.SessionID {
		t.Errorf("run session_id = %q, want the task's own session %q", run.SessionID, final.SessionID)
	}
	if run.StartedAt == "" {
		t.Error("started_at must be set")
	}
	if run.EndedAt == nil || *run.EndedAt == "" {
		t.Error("ended_at must be set on a closed run")
	}
}

// TestTaskRunHistory_OpenedAtClaim_ClosedOnUpdateTaskToolCompletion proves the
// update_task-tool completion path (ADR-050 §3.3/RD5): finishTaskRun's
// task.IsTerminal branch — reached when the agent calls the real update_task
// tool instead of relying on the marker — ALSO closes the run, with no change
// to update_task itself, since the close is driven from the executor
// observing completion, not from the tool.
func TestTaskRunHistory_OpenedAtClaim_ClosedOnUpdateTaskToolCompletion(t *testing.T) {
	provider := newScriptedProvider() // empty; real responses patched in below
	al := newNativeTaskCompletionTestLoop(t, provider)

	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not found in registry")
	}
	agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"update_task": "allow"},
	})

	tk := newCompletionContractTask(t, al, "native-agent", "run history update_task path")

	provider.responses = []*providers.LLMResponse{
		{
			ToolCalls: []providers.ToolCall{{
				ID:   "call-update-task",
				Type: "function",
				Name: "update_task",
				Arguments: map[string]any{
					"task_id": tk.ID,
					"status":  "done",
					"result":  "Done via explicit update_task call.",
				},
			}},
		},
		{
			Content: "Wrapping up now, nothing further to report.",
		},
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want done (result: %s)", final.Status, final.Result)
	}

	// waitForRunClosed, not a raw ListRuns right after the Task.status mirror
	// goes terminal (Constraint #7 fix, matching MirrorStillCycles/
	// OpenedAtClaim_ClosedOnMarkerCompletion/SchedulerFire_...): the mirror
	// write happens BEFORE closeRun in task_executor.go, so
	// waitForCompletionContractTerminal can observe the terminal mirror
	// moments before the run's own EndedAt lands.
	run := waitForRunClosed(t, al, tk.ID, 5*time.Second)
	if run.Status != task.StatusDone {
		t.Errorf("run status = %q, want done", run.Status)
	}
	if run.Result != "Done via explicit update_task call." {
		t.Errorf("run result = %q, want the tool call's own result", run.Result)
	}
	if run.EndedAt == nil {
		t.Error("ended_at must be set — the update_task-tool completion path must also close the run")
	}
}

// TestTaskRunHistory_MirrorStillCyclesAndEmitsRunStatusFrames is the item-6
// regression: proves the Task.status mirror still cycles
// next→in_progress→done (the terminal-status hooks — emitStatusChanged at
// claim, onTaskComplete at completion — still fire exactly as before
// run-history was layered on, RD2), AND that the additive task_run_status
// WS-frame mechanism (§3.8) emits an open (in_progress) and a close (done)
// event carrying the same run_id, via the same EventBus emitEvent path
// task_status_changed already uses.
func TestTaskRunHistory_MirrorStillCyclesAndEmitsRunStatusFrames(t *testing.T) {
	provider := &scriptedProvider{
		responseBody: "Done.\n" +
			"TASK_STATUS: success\n" +
			"TASK_SUMMARY: mirror regression check.",
	}
	al := newNativeTaskCompletionTestLoop(t, provider)
	tk := newCompletionContractTask(t, al, "native-agent", "mirror regression")

	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want done (result: %s)", final.Status, final.Result)
	}

	events := drainEventsWithTimeout(t, sub, tk.ID, 2, 2, 5*time.Second)

	var statusEvents []TaskStatusChangedPayload
	var runEvents []TaskRunStatusPayload
	for _, evt := range events {
		switch evt.Kind {
		case EventKindTaskStatusChanged:
			if p, ok := evt.Payload.(TaskStatusChangedPayload); ok && p.TaskID == tk.ID {
				statusEvents = append(statusEvents, p)
			}
		case EventKindTaskRunStatus:
			if p, ok := evt.Payload.(TaskRunStatusPayload); ok && p.TaskID == tk.ID {
				runEvents = append(runEvents, p)
			}
		}
	}

	// Regression: the mirror's terminal-status hooks still fire exactly as
	// before — claim emits in_progress, completion emits done.
	if len(statusEvents) != 2 {
		t.Fatalf("expected 2 task_status_changed events (claim + completion), got %d: %+v",
			len(statusEvents), statusEvents)
	}
	if statusEvents[0].Status != string(task.StatusInProgress) {
		t.Errorf("first status event = %q, want in_progress", statusEvents[0].Status)
	}
	if statusEvents[1].Status != string(task.StatusDone) {
		t.Errorf("second status event = %q, want done", statusEvents[1].Status)
	}

	// The additive run-status frame: open (in_progress) then close (done),
	// same run_id both times.
	if len(runEvents) != 2 {
		t.Fatalf("expected 2 task_run_status events (open + close), got %d: %+v", len(runEvents), runEvents)
	}
	if runEvents[0].Status != string(task.StatusInProgress) {
		t.Errorf("first run event status = %q, want in_progress", runEvents[0].Status)
	}
	if runEvents[1].Status != string(task.StatusDone) {
		t.Errorf("second run event status = %q, want done", runEvents[1].Status)
	}
	if runEvents[0].RunID == "" || runEvents[0].RunID != runEvents[1].RunID {
		t.Errorf("run_id mismatch between open (%q) and close (%q) events", runEvents[0].RunID, runEvents[1].RunID)
	}
}

// TestTaskRunHistory_ScheduledVsManualKind proves ADR-050 RD5/RD7's kind
// split: ExecuteTask (the shape every trigger-fire/auto-advance/heartbeat
// dispatch funnels through) records task.RunKindScheduled by default, while
// StartOccurrenceRun (the new Run-now entry point) records
// task.RunKindManual.
func TestTaskRunHistory_ScheduledVsManualKind(t *testing.T) {
	provider := &scriptedProvider{
		responseBody: "Done.\nTASK_STATUS: success\nTASK_SUMMARY: kind check.",
	}
	al := newNativeTaskCompletionTestLoop(t, provider)

	t.Run("ExecuteTask_defaults_to_scheduled", func(t *testing.T) {
		tk := newCompletionContractTask(t, al, "native-agent", "kind scheduled")
		if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
			t.Fatalf("ExecuteTask: %v", err)
		}
		waitForCompletionContractTerminal(t, al, tk.ID)

		// waitForRunClosed, not a raw ListRuns right after the Task.status
		// mirror goes terminal (Constraint #7 fix): the mirror write happens
		// BEFORE closeRun in task_executor.go, so
		// waitForCompletionContractTerminal can observe the terminal mirror
		// moments before the run's own EndedAt lands.
		run := waitForRunClosed(t, al, tk.ID, 5*time.Second)
		if run.Kind != task.RunKindScheduled {
			t.Errorf("kind = %q, want %q", run.Kind, task.RunKindScheduled)
		}
	})

	t.Run("StartOccurrenceRun_records_manual", func(t *testing.T) {
		tk := newCompletionContractTask(t, al, "native-agent", "kind manual")
		if err := al.taskExecutor.StartOccurrenceRun(context.Background(), tk.ID, nil); err != nil {
			t.Fatalf("StartOccurrenceRun: %v", err)
		}
		waitForCompletionContractTerminal(t, al, tk.ID)

		// waitForRunClosed, not a raw ListRuns right after the Task.status
		// mirror goes terminal (Constraint #7 fix): the mirror write happens
		// BEFORE closeRun in task_executor.go, so
		// waitForCompletionContractTerminal can observe the terminal mirror
		// moments before the run's own EndedAt lands.
		run := waitForRunClosed(t, al, tk.ID, 5*time.Second)
		if run.Kind != task.RunKindManual {
			t.Errorf("kind = %q, want %q", run.Kind, task.RunKindManual)
		}
	})
}

// TestStartOccurrenceRun_IdempotentAgainstConcurrentSchedulerFire is BDD
// scenario 5 (task-run-history-spec.md §5): a scheduler fire that has already
// opened the run for a given (task_id, occurrence_ms), and a subsequent
// Run-now for the SAME occurrence, must produce exactly one run record — not
// two — and the later manual dispatch must not clobber the earlier run's
// identity (kind/session_id), only its terminal outcome via the shared
// close.
func TestStartOccurrenceRun_IdempotentAgainstConcurrentSchedulerFire(t *testing.T) {
	provider := &scriptedProvider{
		responseBody: "Done.\nTASK_STATUS: success\nTASK_SUMMARY: idempotency check.",
	}
	al := newNativeTaskCompletionTestLoop(t, provider)
	tk := newCompletionContractTask(t, al, "native-agent", "occurrence idempotency")

	occMs := int64(1_700_000_000_000)
	// Simulate a scheduler fire that already opened this occurrence's run a
	// moment before the user's Run-now click for the exact same occurrence.
	seeded, created, err := al.taskStore.OpenRun(tk.ID, &occMs, task.RunKindScheduled, "scheduler-session")
	if err != nil || !created {
		t.Fatalf("seed OpenRun: created=%v err=%v", created, err)
	}

	if startErr := al.taskExecutor.StartOccurrenceRun(context.Background(), tk.ID, &occMs); startErr != nil {
		t.Fatalf("StartOccurrenceRun: %v", startErr)
	}
	waitForCompletionContractTerminal(t, al, tk.ID)

	// waitForRunClosed, not a raw ListRuns right after the Task.status mirror
	// goes terminal (Constraint #7 fix): the mirror write happens BEFORE
	// closeRun in task_executor.go, so waitForCompletionContractTerminal can
	// observe the terminal mirror moments before the run's own EndedAt
	// lands. It also folds in the "exactly 1 run" idempotency assertion this
	// test itself is about — waitForRunClosed only ever returns once
	// ListRuns reports precisely one closed run.
	got := waitForRunClosed(t, al, tk.ID, 5*time.Second)
	if got.RunID != seeded.RunID {
		t.Errorf("run_id = %q, want the scheduler's pre-existing run_id %q — whichever OpenRun call "+
			"reaches the store first is authoritative", got.RunID, seeded.RunID)
	}
	if got.Kind != task.RunKindScheduled {
		t.Errorf("kind = %q, want %q — the LATER manual dispatch must not overwrite the earlier "+
			"scheduled run's kind", got.Kind, task.RunKindScheduled)
	}
	if got.SessionID != "scheduler-session" {
		t.Errorf("session_id = %q, want the scheduler's seeded session (unchanged by the later "+
			"manual dispatch's own, separately-created session)", got.SessionID)
	}
	if got.Status != task.StatusDone {
		t.Errorf("status = %q, want done — the real execution's completion must still close the "+
			"shared run", got.Status)
	}
}

// TestTaskRunHistory_SchedulerFire_RecordsOccurrenceMsAndScheduledKind is the
// full end-to-end proof (real TaskTriggerScheduler + real TaskExecutor + a
// scripted LLM): an RRULE trigger's fire records a TaskRun stamped with the
// fired occurrence's instant and RunKindScheduled, closed to Done with the
// execution's own session — the calendar join key task-run-history-spec.md
// §3.7's occurrence overlay ultimately reads.
func TestTaskRunHistory_SchedulerFire_RecordsOccurrenceMsAndScheduledKind(t *testing.T) {
	provider := &scriptedProvider{
		responseBody: "Done.\nTASK_STATUS: success\nTASK_SUMMARY: occurrence handled.",
	}
	al := newNativeTaskCompletionTestLoop(t, provider)

	clk := newTriggerFakeClock()
	storePath := filepath.Join(t.TempDir(), "triggers", "jobs.json")
	sched := NewTaskTriggerScheduler(storePath, al.taskStore, al.taskExecutor)
	sched.SetClock(clk)
	if err := sched.Start(); err != nil {
		t.Fatalf("sched.Start: %v", err)
	}
	t.Cleanup(sched.Stop)

	dtstart := secondAligned(clk.Now().Add(time.Minute))
	tk := &task.Task{
		Title:       "rrule occurrence run",
		Prompt:      "do the recurring task",
		Action:      task.ActionLLM,
		AgentID:     "native-agent",
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
		Trigger: &task.Trigger{
			Type: task.TriggerRecurring,
			Config: task.TriggerConfig{
				Rrule:     strP("FREQ=DAILY"),
				DtstartMs: int64P(dtstart),
				Tz:        strP("UTC"),
			},
		},
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	sched.OnTaskUpserted(tk)

	jobs := jobsForTask(sched, tk.ID)
	if len(jobs) != 1 || jobs[0].Schedule.AtMS == nil {
		t.Fatalf("setup: expected 1 armed job with AtMS set, got %+v", jobs)
	}
	wantOccurrenceMs := *jobs[0].Schedule.AtMS

	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want done (result: %s)", final.Status, final.Result)
	}

	// waitForRunClosed, not a raw ListRuns right after the Task.status mirror
	// goes terminal (Constraint #7 fix, matching MirrorStillCycles): the
	// mirror write (completeTaskWithResult's te.store.Update) happens BEFORE
	// closeRun in task_executor.go, so waitForCompletionContractTerminal can
	// observe the terminal mirror moments before the run's own EndedAt lands
	// — polling the run store directly is what actually waits on the
	// guarantee under test instead of racing it. See waitForRunClosed's own
	// doc comment below.
	run := waitForRunClosed(t, al, tk.ID, 5*time.Second)
	if run.Kind != task.RunKindScheduled {
		t.Errorf("kind = %q, want %q", run.Kind, task.RunKindScheduled)
	}
	if run.OccurrenceMs == nil || *run.OccurrenceMs != wantOccurrenceMs {
		t.Errorf("occurrence_ms = %v, want %d", derefI64(run.OccurrenceMs), wantOccurrenceMs)
	}
	if run.Status != task.StatusDone {
		t.Errorf("run status = %q, want done", run.Status)
	}
	if run.SessionID != final.SessionID {
		t.Errorf("run session_id = %q, want the task's own session %q", run.SessionID, final.SessionID)
	}
	if run.EndedAt == nil {
		t.Error("ended_at must be set on a closed run")
	}
}

// waitForRunClosed polls ListRuns until taskID has exactly one run whose
// EndedAt is set, or fails the test after timeout. Used instead of
// waitForCompletionContractTerminal/waitTaskTerminal for the M1/M5/L5
// coverage below: those helpers proxy run-closure via Task.status or session
// archival, but finishTaskRun's error branch sets session status to
// Interrupted (not Archived), and Task.status can go terminal (failTask)
// slightly BEFORE the run's own closeRun call lands — so polling the run
// store directly is the only way to deterministically observe the actual
// guarantee under test: that the run itself closes, with no reaper backstop
// to catch it later if it doesn't.
func waitForRunClosed(t *testing.T, al *AgentLoop, taskID string, timeout time.Duration) task.TaskRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		runs, err := al.taskStore.ListRuns(taskID)
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if len(runs) == 1 && runs[0].EndedAt != nil && *runs[0].EndedAt != "" {
			return runs[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run for task %s did not close within %s", taskID, timeout)
	return task.TaskRun{}
}

// TestTaskRunHistory_ExecutionErrorBranch_ClosesRunAsFailed is M1 coverage
// (task-run-history-spec.md §3.3): finishTaskRun's hard "agent execution
// error" branch (err != nil, e.g. a fatal external-CLI driver error) must
// close the open run as failed with a non-nil ended_at and the failure's own
// Result — this branch already closed the run before this change (it is the
// SIBLING branch M1 compares against), so this test is the regression lock
// pinning that it still does, now that the OTHER exit paths have been fixed
// to match it. There is no reaper backstop (operator decision 2026-07-20).
func TestTaskRunHistory_ExecutionErrorBranch_ClosesRunAsFailed(t *testing.T) {
	provider := &countingProvider{}
	al, _ := newExternalCLITaskTestLoop(t, provider)

	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindError,
			Err:  &runner.ErrorEvent{Message: "external CLI crashed for run-history test", Fatal: true},
		})
		fr.Cancel()
	}()

	tk := &task.Task{
		Title:       "run history execution error",
		Prompt:      "do the failing task",
		Action:      task.ActionLLM,
		AgentID:     "ext-agent",
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	run := waitForRunClosed(t, al, tk.ID, asyncTaskPollTimeout)
	if run.Status != task.StatusFailed {
		t.Errorf("run status = %q, want failed", run.Status)
	}
	// ADR-051 §RD5: the raw driver stderr is now sanitized through
	// TranslateLLMError before reaching the task run result. The result MUST
	// carry the generic failure copy, NOT the raw text.
	if strings.Contains(run.Result, "external CLI crashed for run-history test") {
		t.Errorf("run result = %q must NOT contain the raw driver stderr (ADR-051 sanitization)",
			run.Result)
	}
	if !strings.Contains(run.Result, "external-cli run failed") {
		t.Errorf("run result = %q, want it to mention the generic failure wrapper", run.Result)
	}

	final, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if final.Status != task.StatusFailed {
		t.Errorf("task status = %q, want failed (regression: the mirror must still cycle too)", final.Status)
	}
}

// TestTaskRunHistory_NoMarkerFailClosedBranch_ClosesRunAsFailed is M1/M5
// coverage for finishTaskRun's OTHER fail-closed branch
// (task-run-history-spec.md §3.3): a clean native response with no
// TASK_STATUS marker routes through completeTaskWithResult's fail-closed
// path (the store-update-succeeds branch) — the run must close as failed
// there too, with the same fail-closed Result text as the Task mirror.
func TestTaskRunHistory_NoMarkerFailClosedBranch_ClosesRunAsFailed(t *testing.T) {
	provider := &scriptedProvider{
		responseBody: "I looked into this and made some progress, but didn't finish.",
	}
	al := newNativeTaskCompletionTestLoop(t, provider)
	tk := newCompletionContractTask(t, al, "native-agent", "run history no marker")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want failed (result: %s)", final.Status, final.Result)
	}

	run := waitForRunClosed(t, al, tk.ID, 5*time.Second)
	if run.Status != task.StatusFailed {
		t.Errorf("run status = %q, want failed", run.Status)
	}
	if run.Result != final.Result {
		t.Errorf("run result = %q, want the same fail-closed result written to the task mirror %q",
			run.Result, final.Result)
	}
	if !strings.Contains(run.Result, "completion signal") {
		t.Errorf("run result = %q, want it to explain the missing completion signal", run.Result)
	}
}

// TestTaskRunHistory_StartTaskNow_OpensAndClosesManualRun is BLK-3 coverage
// (task-run-history-spec.md §3.2/3.4, operator decision 2026-07-20): the
// gateway's "Start Task"/"Create & Run now" UI both
// PATCH→in_progress→StartTaskNow→runTaskFromInProgress — the most common
// launch path in practice — must now open and close a TaskRun exactly like
// the ClaimForRun-guarded ExecuteTask path does: kind=RunKindManual,
// occurrence_ms=nil (no recurring-fire context).
func TestTaskRunHistory_StartTaskNow_OpensAndClosesManualRun(t *testing.T) {
	provider := &scriptedProvider{
		responseBody: "Done.\nTASK_STATUS: success\nTASK_SUMMARY: start task now run history.",
	}
	al := newNativeTaskCompletionTestLoop(t, provider)
	tk := newCompletionContractTask(t, al, "native-agent", "start task now run history")

	sessionID, err := al.taskExecutor.StartTaskNow(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTaskNow: %v", err)
	}
	if sessionID == "" {
		t.Fatal("StartTaskNow returned an empty session id")
	}

	run := waitForRunClosed(t, al, tk.ID, 5*time.Second)
	if run.Kind != task.RunKindManual {
		t.Errorf("kind = %q, want %q — StartTaskNow is a user-initiated launch", run.Kind, task.RunKindManual)
	}
	if run.OccurrenceMs != nil {
		t.Errorf("occurrence_ms = %v, want nil — StartTaskNow has no recurring-fire context", *run.OccurrenceMs)
	}
	if run.Status != task.StatusDone {
		t.Errorf("run status = %q, want done", run.Status)
	}
	if run.SessionID != sessionID {
		t.Errorf("run session_id = %q, want the session StartTaskNow returned %q", run.SessionID, sessionID)
	}

	final, gerr := al.taskStore.Get(tk.ID)
	if gerr != nil {
		t.Fatalf("get task: %v", gerr)
	}
	if final.Status != task.StatusDone {
		t.Errorf("task status = %q, want done (regression: the mirror must still cycle too)", final.Status)
	}
}

// TestCloseRun_DuplicateCloseAfterAlreadyClosedLogsInfoNotError is the
// regression for delta-review Fix 2 (2026-07-20): a SECOND closeRun call for
// a run that is already terminal — e.g. runTask's top-level panic-recovery
// defer re-invoking closeRun after a panic in POST-completion housekeeping
// (onTaskComplete / notifyParentIfAllSiblingsDone) that runs AFTER
// completeTaskWithResult already closed the run successfully — is a benign
// duplicate, not a stranded run. It must:
//  1. never mutate the already-closed run's terminal record (the store
//     layer rejects the duplicate with task.ErrRunAlreadyClosed and appends
//     nothing new), and
//  2. never log at ERROR ("permanently strands") for that specific,
//     distinguishable error — proven here by contrast with a genuine close
//     failure (an unknown run id), which must still log at ERROR.
func TestCloseRun_DuplicateCloseAfterAlreadyClosedLogsInfoNotError(t *testing.T) {
	te, store := newTestTaskExecutor(t)
	taskID := "task-close-dup"

	run, created, oerr := store.OpenRun(taskID, nil, task.RunKindManual, "session-a")
	if oerr != nil || !created {
		t.Fatalf("setup OpenRun: run=%+v created=%v err=%v", run, created, oerr)
	}
	active := &activeRun{runID: run.RunID}

	logFile := filepath.Join(t.TempDir(), "close-run-dup.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.ERROR)
	if ferr := logger.EnableFileLogging(logFile); ferr != nil {
		t.Fatalf("EnableFileLogging: %v", ferr)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	// First close: the real, successful completion (mirrors
	// completeTaskWithResult's own closeRun call).
	te.closeRun(taskID, active, task.StatusDone, "first result")

	closedRuns, lerr := store.ListRuns(taskID)
	if lerr != nil {
		t.Fatalf("ListRuns after first close: %v", lerr)
	}
	if len(closedRuns) != 1 || closedRuns[0].Status != task.StatusDone || closedRuns[0].Result != "first result" {
		t.Fatalf("unexpected state after first close: %+v", closedRuns)
	}

	// Second close: simulates the panic-recovery defer re-invoking closeRun
	// on the SAME already-closed *activeRun with a different (panic/failed)
	// outcome, exactly as runTask's defer would after a housekeeping panic.
	te.closeRun(taskID, active, task.StatusFailed, "panic during task execution: boom")

	afterDup, lerr2 := store.ListRuns(taskID)
	if lerr2 != nil {
		t.Fatalf("ListRuns after duplicate close: %v", lerr2)
	}
	if len(afterDup) != 1 || afterDup[0].Status != task.StatusDone || afterDup[0].Result != "first result" {
		t.Fatalf("duplicate close must not mutate the already-terminal run record, got %+v", afterDup)
	}

	logged, rerr := os.ReadFile(logFile)
	if rerr != nil {
		t.Fatalf("read log file: %v", rerr)
	}
	if strings.Contains(string(logged), "Could not close task run record") {
		t.Fatalf(
			"duplicate-close-of-an-already-closed-run must NOT log at ERROR (false on-call alert), got log:\n%s",
			logged,
		)
	}

	// Contrast: a GENUINE close failure (unknown run id) must still log at
	// ERROR — proving the guard is specific to ErrRunAlreadyClosed, not a
	// blanket demotion of every CloseRun error.
	te.closeRun(taskID, &activeRun{runID: "does-not-exist"}, task.StatusFailed, "x")
	logged2, rerr2 := os.ReadFile(logFile)
	if rerr2 != nil {
		t.Fatalf("read log file (2nd read): %v", rerr2)
	}
	if !strings.Contains(string(logged2), "Could not close task run record") {
		t.Fatalf("a genuine close failure (run not found) must still log at ERROR, got log:\n%s", logged2)
	}
}
