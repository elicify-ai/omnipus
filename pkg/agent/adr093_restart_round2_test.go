// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// ADR-093 round 2 — RED pack for the 5-reviewer gate findings, driven through
// the REAL entry points (the run_task tool with the production StartTaskNowAs
// dispatcher behind it, the real task executor front door, the real
// goal_claim/judge loop). Findings and oracles:
//
//   - R1 (CR C1, ARCH A1, PRT F1, SFH SF-4): restarting a task with a bound
//     conversation through the public routes continues the SAME conversation
//     at generation+1; the task leaves Done into the executing state; the
//     worker starts once; the new run produces and stores a NEW adjudicated
//     result — never the previous one.
//   - R2 (CR C2, ARCH A2, SFH SF-3): restarting a task whose paired goal is
//     terminal (a genuine failure) reactivates the goal, so the resumed
//     worker's goal_claim succeeds and the task can finish. A goal still
//     active after a user Stop stays active (control).
//   - R3 (SFH SF-1): a lifecycle-record read failure during restart is
//     surfaced as a visible, typed error; it is never turned into a
//     misleading permanent task failure.
//   - R4 (SFH SF-2): a task that is mid-stop does not report restart success
//     before the stop-fence check; the visible refusal is returned.
//   - R5 (SEC-1): after Stop on a root chat, an automatic wake (no
//     steer_message_id, no new human instruction) does NOT start a turn that
//     launches or dispatches helpers; a NEW human instruction still restarts
//     it (founder case 6). SEC-2 is dismissed by the founder (Q89) — no test.
//
// Oracles are the gate findings and the amended ADR, never the
// implementation. Only the LLM providers are doubles (process edge).

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/workspace"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// round2HoldingWorker parks every turn until released — including through a
// Stop's context cancellation, so a run's slot stays occupied for the
// mid-stop probes — then answers without claiming. Process-edge double only.
type round2HoldingWorker struct {
	mu      sync.Mutex
	calls   int
	entered chan int
	release chan struct{}
	once    sync.Once
	// returnOnCancel makes the worker a well-behaved parked provider: it
	// returns when the turn's context is cancelled (a Stop). The default
	// (false) deliberately ignores cancellation so a run's slot stays
	// occupied through an in-flight stop — the SF-2 probe's whole point.
	returnOnCancel bool
}

func newRound2HoldingWorker() *round2HoldingWorker {
	return &round2HoldingWorker{entered: make(chan int, 16), release: make(chan struct{})}
}

func (w *round2HoldingWorker) Chat(
	ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	w.mu.Lock()
	w.calls++
	n := w.calls
	w.mu.Unlock()
	select {
	case w.entered <- n:
	default:
	}
	if w.returnOnCancel {
		select {
		case <-w.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &providers.LLMResponse{Content: "Turn finished."}, nil
	}
	<-w.release // holds the turn — and its run slot — until the test releases it
	return &providers.LLMResponse{Content: "Turn finished."}, nil
}

func (w *round2HoldingWorker) GetDefaultModel() string { return "round2-holding-worker" }

func (w *round2HoldingWorker) releaseAll() { w.once.Do(func() { close(w.release) }) }

func (w *round2HoldingWorker) callsSnapshot() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

func (w *round2HoldingWorker) waitEntered(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case <-w.entered:
	case <-time.After(within):
		t.Fatalf("no worker turn entered the provider within %s", within)
	}
}

// round2WireSelfRunEdge adds the caller→assignee self-edge run_task's
// delegation gate (authorizeInitiatedRun) reads from the task workspace's
// delegation store under the loop's isolated home. The delegation record is
// MERGED, never overwritten: the loop's own seed (seedDefaultDelegationGraph
// ForLoop) already put the launch gate's mesh there, and the workspace record
// next to it carries the team membership the turn's working-directory gate
// reads — clobbering either breaks the run for an unrelated reason.
func round2WireSelfRunEdge(t *testing.T) {
	t.Helper()
	storePath, pathErr := workspace.DelegationStorePath(config.OmnipusHomeDir(), testHarnessWorkspaceMembershipID)
	require.NoError(t, pathErr)
	require.NoError(t, os.MkdirAll(filepath.Dir(storePath), 0o700))
	existing := struct {
		WorkspaceID string      `json:"workspace_id"`
		Delegation  []graphEdge `json:"delegation,omitempty"`
	}{WorkspaceID: testHarnessWorkspaceMembershipID}
	if raw, rerr := os.ReadFile(storePath); rerr == nil {
		_ = json.Unmarshal(raw, &existing) // best-effort merge; corrupt file is rewritten below
	}
	for _, e := range existing.Delegation {
		if e.FromAgent == "native-agent" && e.ToAgent == "native-agent" {
			return // already present
		}
	}
	existing.Delegation = append(existing.Delegation, graphEdge{FromAgent: "native-agent", ToAgent: "native-agent"})
	data, merr := json.Marshal(existing)
	require.NoError(t, merr)
	require.NoError(t, os.WriteFile(storePath, data, 0o600))
}

// round2InstallExecutorFrontDoor wires what the production gateway boot wires
// and the goal-loop harness (built for ExecuteTask) omits: the durable
// messaging stores, a genuine boot epoch — dispatch admission refuses an
// unminted one — and the real SteerLauncher on the executor, so StartTaskNow
// takes the production launch+dispatch path.
func round2InstallExecutorFrontDoor(t *testing.T, al *AgentLoop) {
	t.Helper()
	home := config.OmnipusHomeDir()
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), lifecycle)
	al.taskExecutor.SetLifecycleStore(lifecycle)
	epochDir := filepath.Join(home, "boot_epoch")
	require.NoError(t, os.MkdirAll(epochDir, 0o700))
	boot := session.NewBootEpochStore(epochDir)
	epoch, mintErr := boot.Mint()
	require.NoError(t, mintErr, "setup: mint the boot epoch")
	require.NotZero(t, epoch, "setup: one genuine boot epoch")
	al.SetBootEpochStore(boot)
	al.taskExecutor.SetSessionLauncher(NewSteerLauncher(al))
}

// round2RunTaskThroughTool executes the REAL run_task tool registered on the
// worker agent (production wiring: loop_wire.go registers it with the real
// StartTaskNowAs dispatcher and the real delegation gate).
func round2RunTaskThroughTool(t *testing.T, al *AgentLoop, taskID string) *tools.ToolResult {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("worker agent not registered")
	}
	tool, ok := inst.Tools.Get("run_task")
	if !ok {
		t.Fatal("run_task is not registered on the worker agent")
	}
	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "native-agent"), testHarnessWorkspaceMembershipID)
	res := tool.Execute(ctx, map[string]any{"task_id": taskID})
	if res == nil {
		t.Fatal("run_task returned nil")
	}
	return res
}

// taskSessionIDAfterStart reads back the session id the started run bound.
func taskSessionIDAfterStart(t *testing.T, al *AgentLoop, taskID string) string {
	t.Helper()
	got, err := GetTaskStore(al).Get(taskID)
	require.NoError(t, err)
	require.NotEmpty(t, got.SessionID, "the started run must bind its session")
	return got.SessionID
}

// round2AwaitRecordTerminal waits for the bound record's terminal landing —
// it settles asynchronously after the task's verdict is stored.
func round2AwaitRecordTerminal(t *testing.T, al *AgentLoop, sid string) *session.LifecycleRecord {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		rec, err := al.GetSessionLifecycleStore().Load(sid)
		require.NoError(t, err)
		if rec.Terminal() {
			return rec
		}
		time.Sleep(20 * time.Millisecond)
	}
	rec, _ := al.GetSessionLifecycleStore().Load(sid)
	t.Fatalf("the bound record did not land terminal within 15s (last: %+v) — a WAIT timeout", rec)
	return nil
}

// round2AwaitGoalState waits for the paired goal record's state to settle.
func round2AwaitGoalState(t *testing.T, taskID string, want generated.GoalState) *goal.Goal {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		g := readTaskGoal(t, taskID)
		if g.State == want {
			return g
		}
		time.Sleep(20 * time.Millisecond)
	}
	g := readTaskGoal(t, taskID)
	t.Fatalf("the paired goal did not reach %q within 15s (last: %q) — a WAIT timeout", want, g.State)
	return nil
}

// round2RestartEvidence is the restarted run's own completion claim: distinct
// from round 1's tryLimitEvidence so the stored result identifies WHICH run
// adjudicated it (CR C1: a restart stores a NEW adjudicated result).
const round2RestartEvidence = "reconciled the restarted ledger export against the archive"

// TestRunTask_RestartsDoneTask_ContinuesBoundConversationWithNewResult is R1
// through the public agent route (CR C1 / ARCH A1 / PRT F1 / SFH SF-4). A
// task completes run 1 with an adjudicated result; an agent then restarts it
// through run_task. Required: the restart is admitted, continues the SAME
// bound conversation at generation+1, the worker starts once more, the judge
// re-adjudicates, and the task stores a NEW result — never the previous one.
// OLD: run_task refused the Done task outright ("already done"), so the
// restart was unreachable through the front door at all.
func TestRunTask_RestartsDoneTask_ContinuesBoundConversationWithNewResult(t *testing.T) {
	// Run 1 claims the shared round-1 evidence; the RESTARTED run claims its
	// own distinct evidence (turn script: second turn repeats), so the stored
	// result says which run adjudicated it.
	worker := newClaimingWorker(turnClaimMet(tryLimitEvidence), turnClaimMet(round2RestartEvidence))
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) {
		cfg.Planning.GoalMaxRounds = 2
		cfg.Planning.TaskMaxAttempts = 3
		cfg.Agents.Defaults.Home = filepath.Join(config.OmnipusHomeDir(), "agents")
	})
	judge := &b6ScriptedJudge{metFromCall: 1, reason: "met"}
	judgeInst.Provider = judge
	round2WireSelfRunEdge(t)
	round2InstallExecutorFrontDoor(t, al)

	taskID := createTaskThroughAgentTool(t, al)

	// Run 1 through the same public front door the restart uses: run_task
	// flips the task in_progress (StartTaskNow alone never does) and runs the
	// judged loop to a genuinely adjudicated Done.
	run1 := round2RunTaskThroughTool(t, al, taskID)
	if run1.IsError {
		t.Fatalf("setup: run 1 refused: %s", run1.ForLLM)
	}
	sid1 := taskSessionIDAfterStart(t, al, taskID)
	waitForTaskStatus(t, al, taskID, task.StatusDone, 60*time.Second)
	done1, gerr := GetTaskStore(al).Get(taskID)
	require.NoError(t, gerr)
	require.Equal(t, sid1, done1.SessionID, "setup: run 1 binds its session")
	result1 := done1.Result
	require.NotEmpty(t, result1, "setup: run 1 stored an adjudicated result")
	rec1 := round2AwaitRecordTerminal(t, al, sid1)
	turnsAfterRun1 := worker.turnsStarted()
	judgeCallsAfterRun1 := judge.callCount()

	// THE RED: restart the Done task through the PUBLIC run_task route.
	res := round2RunTaskThroughTool(t, al, taskID)
	if res.IsError {
		t.Fatalf("run_task refused the Done task's restart:\n%s\n— CR C1 / ARCH A1 / PRT F1 / SFH SF-4: an authorized restart of a bound, completed task must be reachable through the public route and continue the SAME conversation at generation+1 (old: \"already done\" refusal)", res.ForLLM)
	}

	// The restart runs the SAME conversation one generation forward, starts
	// the worker once more, adjudicates anew, and stores a NEW result.
	waitForTaskStatus(t, al, taskID, task.StatusDone, 60*time.Second)
	final, gerr := GetTaskStore(al).Get(taskID)
	require.NoError(t, gerr)
	require.Equal(t, sid1, final.SessionID, "the restart must continue the SAME bound conversation")
	require.NotEqual(t, result1, final.Result, "the restarted run must store a NEW adjudicated result, not the previous one")
	require.Contains(t, final.Result, round2RestartEvidence,
		"the stored result must be the SECOND run's own adjudicated claim, got %q", final.Result)
	rec2 := adr093Load(t, al, sid1)
	require.Equal(t, rec1.Generation+1, rec2.Generation, "the continuation mints the next generation on the bound session")
	require.Equal(t, sid1, rec2.ResumedFrom, "the continuation revives via resumed_from (transcript continuity)")
	require.Greater(t, worker.turnsStarted(), turnsAfterRun1, "the restarted run must start the worker again")
	require.GreaterOrEqual(t, judge.callCount(), judgeCallsAfterRun1+1, "the restarted run must be adjudicated anew")
}

// TestRunTask_RestartFailedTask_ReactivatesGoal_AndResumedWorkerFinishes is
// R2 (CR C2 / ARCH A2 / SFH SF-3). Run 1 genuinely fails: the judge rejects
// the claim, the goal's single try is spent, the paired goal lands exhausted
// and the task Failed. An agent restarts through run_task (failed tasks are
// re-runnable). Required: the restart REACTIVATES the paired goal so the
// resumed worker's goal_claim succeeds, the judge adjudicates the new claim,
// and the task finishes Done with a new result. OLD: the goal stayed
// exhausted, so goal_claim answered "this session has no active goal" and the
// restarted run could never finish normally.
func TestRunTask_RestartFailedTask_ReactivatesGoal_AndResumedWorkerFinishes(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet(tryLimitEvidence))
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) {
		cfg.Planning.GoalMaxRounds = 1
		cfg.Planning.TaskMaxAttempts = 1
		cfg.Agents.Defaults.Home = filepath.Join(config.OmnipusHomeDir(), "agents")
	})
	// Judge call 1 (run 1) unmet — the genuine failure; call 2+ (the
	// restarted run) met.
	judge := &b6ScriptedJudge{metFromCall: 2, reason: "the ledger was not updated"}
	judgeInst.Provider = judge
	round2WireSelfRunEdge(t)
	round2InstallExecutorFrontDoor(t, al)

	taskID := createTaskThroughAgentTool(t, al)
	run1 := round2RunTaskThroughTool(t, al, taskID)
	if run1.IsError {
		t.Fatalf("setup: run 1 refused: %s", run1.ForLLM)
	}
	sid1 := taskSessionIDAfterStart(t, al, taskID)
	waitForTaskStatus(t, al, taskID, task.StatusFailed, 60*time.Second)
	failed1, gerr := GetTaskStore(al).Get(taskID)
	require.NoError(t, gerr)
	result1 := failed1.Result
	rec1 := round2AwaitRecordTerminal(t, al, sid1)
	round2AwaitGoalState(t, taskID, generated.GoalStateExhausted)
	turnsAfterRun1 := worker.turnsStarted()
	judgeCallsAfterRun1 := judge.callCount()

	// Restart through the public route (failed tasks are re-runnable; the RED
	// is what happens to the goal and to the run's completion).
	res := round2RunTaskThroughTool(t, al, taskID)
	if res.IsError {
		t.Fatalf("run_task refused the failed task's restart:\n%s\n— the failure itself is out of scope; investigate the harness", res.ForLLM)
	}

	// RED 1: the paired goal must be reactivated (no longer exhausted) so the
	// resumed worker's goal_claim can succeed.
	goalAfter := readTaskGoal(t, taskID)
	if goalAfter.State == generated.GoalStateExhausted {
		t.Fatalf("the restarted task's paired goal is still exhausted — CR C2 / ARCH A2 / SFH SF-3: the restart must reactivate the paired goal before dispatch; a terminal goal makes the resumed worker's goal_claim fail with \"this session has no active goal\" so the run can never finish normally")
	}

	// RED 2: the resumed run finishes Done on a NEW adjudicated result.
	waitForTaskStatus(t, al, taskID, task.StatusDone, 60*time.Second)
	final, gerr := GetTaskStore(al).Get(taskID)
	require.NoError(t, gerr)
	require.NotEqual(t, result1, final.Result, "the resumed run must store its own adjudicated result")
	require.Equal(t, sid1, final.SessionID, "the restart continues the SAME bound conversation")
	rec2 := adr093Load(t, al, sid1)
	require.Equal(t, rec1.Generation+1, rec2.Generation, "the terminal bound session continues at generation+1")
	require.Equal(t, sid1, rec2.ResumedFrom, "the terminal revival mints the next generation via resumed_from")
	require.Greater(t, worker.turnsStarted(), turnsAfterRun1, "the restarted run must start the worker again")
	require.GreaterOrEqual(t, judge.callCount(), judgeCallsAfterRun1+1, "the resumed claim must be adjudicated anew")
}

// TestRunTask_UserStoppedTask_PairedGoalStaysActive is R2's control: a goal
// still active after a USER Stop stays active — a stop is not an
// adjudication, and the restart path must never "reactivate" (reset) a goal
// the user's Stop deliberately left live.
func TestRunTask_UserStoppedTask_PairedGoalStaysActive(t *testing.T) {
	holder := newRound2HoldingWorker()
	holder.returnOnCancel = true
	al, judgeInst := newGoalLoopTestLoop(t, holder, func(cfg *config.Config) {
		cfg.Planning.GoalMaxRounds = 2
		cfg.Planning.TaskMaxAttempts = 2
		cfg.Agents.Defaults.Home = filepath.Join(config.OmnipusHomeDir(), "agents")
	})
	judgeInst.Provider = &b6ScriptedJudge{metFromCall: 1, reason: "met"}
	t.Cleanup(holder.releaseAll)
	round2InstallExecutorFrontDoor(t, al)
	round2WireSelfRunEdge(t)

	taskID := createTaskThroughAgentTool(t, al)
	runStart := round2RunTaskThroughTool(t, al, taskID)
	if runStart.IsError {
		t.Fatalf("setup: run refused: %s", runStart.ForLLM)
	}
	sid := taskSessionIDAfterStart(t, al, taskID)
	holder.waitEntered(t, 30*time.Second)

	stop, stopErr := al.StopSession(context.Background(), StopRequest{SessionID: sid, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "round2"}})
	require.NoError(t, stopErr)
	require.NoError(t, stop.RootErr)
	holder.releaseAll()
	awaitSteeringRepairStopped(t, al, sid, 1)

	g := readTaskGoal(t, taskID)
	if g.State == generated.GoalStateExhausted || g.State == generated.GoalStateMet {
		t.Fatalf("the paired goal after a user Stop = %q — a Stop is not an adjudication: the goal a user stopped stays ACTIVE (CR C2 control; GoalStateForTerminalTask maps StopByUser to no terminal state)", g.State)
	}
}

// TestRunTask_RestartWithUnreadableBoundRecord_VisibleTypedErrorNotTaskFailure
// is R3 (SFH SF-1) at the public route: with the bound session's lifecycle
// record unreadable, the restart must surface a visible, typed error that
// identifies the READ FAILURE of the bound session, and must never turn the
// storage fault into a permanent task failure. Once the record reads again,
// the same restart is admitted.
func TestRunTask_RestartWithUnreadableBoundRecord_VisibleTypedErrorNotTaskFailure(t *testing.T) {
	al, inst := newRunTaskPolicyLoop(t, []graphEdge{{FromAgent: rtCaller, ToAgent: rtAssignee}})
	tk := createRunnableTask(t, al, "t-round3-unreadable", rtAssignee)
	if _, err := al.GetSessionStore().GetOrCreateMainSession(testWS, rtAssignee); err != nil {
		t.Fatalf("setup: create the bound session: %v", err)
	}
	boundID, err := session.MainSessionID(testWS, rtAssignee)
	require.NoError(t, err)
	if _, err := GetTaskStore(al).Update(tk.ID, task.Patch{SessionID: &boundID}); err != nil {
		t.Fatalf("setup: bind the session: %v", err)
	}
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID:      boundID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    testWS,
		AgentID:        rtAssignee,
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: tk.ID},
	}), "setup: seed the bound record")

	// Make every read of the bound record fail (the established i1_r4 trick:
	// the JSONL becomes a directory).
	ls := al.GetSessionLifecycleStore()
	recPath := filepath.Join(ls.Dir(), boundID+".jsonl")
	backup := recPath + ".round3-saved"
	require.NoError(t, os.Rename(recPath, backup))
	require.NoError(t, os.Mkdir(recPath, 0o700))
	restore := func() {
		require.NoError(t, os.Remove(recPath))
		require.NoError(t, os.Rename(backup, recPath))
	}
	t.Cleanup(func() {
		if _, statErr := os.Stat(backup); statErr == nil {
			restore()
		}
	})

	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), rtCaller), testWS)
	res := inst.Tools.Execute(ctx, "run_task", map[string]any{"task_id": tk.ID})
	if res == nil {
		t.Fatal("run_task returned nil")
	}
	if !res.IsError {
		t.Fatalf("run_task with an unreadable bound record must fail visibly, got success:\n%s", res.ForLLM)
	}
	// SFH SF-1: the surfaced error must identify the READ FAILURE of the bound
	// session — a typed cause — never disguise it as a classification gap or
	// fall through to a misleading dispatch failure.
	if !strings.Contains(res.ForLLM, "read bound session") {
		t.Fatalf("the restart error does not identify the bound-session read failure:\n%s\n— SFH SF-1: a lifecycle-record read failure during restart is surfaced as a visible, typed error; the storage fault must stay diagnosable (old: \"runtime classification was never persisted\", the read error swallowed)", res.ForLLM)
	}
	got, gerr := GetTaskStore(al).Get(tk.ID)
	require.NoError(t, gerr)
	require.NotEqual(t, task.StatusFailed, got.Status,
		"a storage read failure is about the store, never a permanent task failure (SFH SF-1)")

	// Control: once the record reads again, the same restart is admitted.
	restore()
	res2 := inst.Tools.Execute(ctx, "run_task", map[string]any{"task_id": tk.ID})
	if res2 == nil || res2.IsError {
		t.Fatalf("after the bound record reads again the restart must be admitted, got error: %s", res2.ForLLM)
	}
}

// TestRunTask_RestartWhileStopInFlight_RefusesVisiblyUntilTheStopLands is R4
// (SFH SF-2): a genuinely RUNNING task whose Stop is still in flight must not
// report restart success through the occupied-slot shortcut. The visible
// "retry once the stop has landed" refusal is required; after the stop lands,
// the same restart is admitted.
func TestRunTask_RestartWhileStopInFlight_RefusesVisiblyUntilTheStopLands(t *testing.T) {
	holder := newRound2HoldingWorker()
	al, judgeInst := newGoalLoopTestLoop(t, holder, func(cfg *config.Config) {
		cfg.Planning.GoalMaxRounds = 2
		cfg.Planning.TaskMaxAttempts = 2
		cfg.Agents.Defaults.Home = filepath.Join(config.OmnipusHomeDir(), "agents")
	})
	judgeInst.Provider = &b6ScriptedJudge{metFromCall: 1, reason: "met"}
	t.Cleanup(holder.releaseAll)
	round2WireSelfRunEdge(t)
	round2InstallExecutorFrontDoor(t, al)

	taskID := createTaskThroughAgentTool(t, al)
	runStart := round2RunTaskThroughTool(t, al, taskID)
	if runStart.IsError {
		t.Fatalf("setup: run refused: %s", runStart.ForLLM)
	}
	sid := taskSessionIDAfterStart(t, al, taskID)
	holder.waitEntered(t, 30*time.Second) // the worker is inside the model; the run slot is occupied
	bound, gerr := GetTaskStore(al).Get(taskID)
	require.NoError(t, gerr)
	require.Equal(t, sid, bound.SessionID, "setup: the run bound its session")

	stop, stopErr := al.StopSession(context.Background(), StopRequest{SessionID: sid, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "round2"}})
	require.NoError(t, stopErr)
	require.NoError(t, stop.RootErr)

	// THE RED: a restart while the stop is in flight. The worker still holds
	// the run slot, so the occupied-slot shortcut answers before any
	// stop-fence check.
	res := round2RunTaskThroughTool(t, al, taskID)
	if res == nil || !res.IsError {
		t.Fatalf("run_task while the task's stop is in flight reported success — SFH SF-2: the occupied-slot shortcut must not return restart success before the stop-fence check; the visible \"retry once the stop has landed\" refusal is required (got: %s)", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "retry once the stop has landed") {
		t.Fatalf("the in-flight refusal text does not carry the visible stop wording:\n%s\n— SFH SF-2: the caller must be told to wait for the stop to land, not left with a silent no-op", res.ForLLM)
	}

	// Land the stop; then the same restart is admitted.
	holder.releaseAll()
	awaitSteeringRepairStopped(t, al, sid, 1)
	res2 := round2RunTaskThroughTool(t, al, taskID)
	if res2.IsError {
		t.Fatalf("after the stop lands the restart must be admitted, got error:\n%s\n— founder case 6: once the stop has landed, the restart works", res2.ForLLM)
	}
	joinGoalFixtureRuns(t, al)
}

// TestAdr093StoppedRoot_AutomaticWakeStartsNoTurnAndNoHelper is R5 (SEC-1):
// after Stop on a root chat, an AUTOMATIC wake — a queued goal continuation
// or async notification carried on the system channel with no
// steer_message_id and no new human instruction — must NOT start a turn that
// can launch or dispatch helpers: the wake never resumes a stopped root. A
// NEW human instruction still restarts the conversation normally (founder
// case 6 — asserted here as the control).
func TestAdr093StoppedRoot_AutomaticWakeStartsNoTurnAndNoHelper(t *testing.T) {
	al, original, provider := r5AdmitRootWithAnswers(t,
		"pre-stop answer", "resumed after a new instruction", "helper work after restart")
	sid := original.SessionID

	r5StopRoot(t, al, original)
	provider.open(0)
	joinGoalFixtureRuns(t, al)
	landed := rootReopenedRecord(t, al, sid)
	if landed.State != session.LifecycleStopped || landed.Stop != nil || landed.StopNote == nil {
		t.Fatalf("setup: root after stop = %+v, want the landed stopped shape", landed)
	}

	entriesBefore := len(provider.Requests())
	records, listErr := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	require.NoError(t, listErr)
	recordsBefore := len(records)

	// The automatic wake: system channel, no steer_message_id metadata, no
	// human instruction. Delivered off-thread: today it starts a turn that
	// parks in the held provider, and this test must observe that entry
	// WITHOUT joining the turn. A refused wake (the required behaviour)
	// produces no entry at all — the bounded window below is the negative
	// wait; the established suite pattern for exactly this shape.
	wakeDone := make(chan struct{})
	go func() {
		_, _ = al.processSystemMessage(context.Background(), bus.InboundMessage{
			Channel:                  "system",
			ChatID:                   "webchat:" + sid,
			Content:                  "queued goal continuation",
			AsyncTranscriptSessionID: sid,
			AsyncOriginAgentID:       testDefaultAgentID,
		})
		close(wakeDone)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(provider.Requests()) > entriesBefore {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Whatever the wake started must never hold the test: release the held
	// answer and give a refused delivery a moment to return.
	provider.open(1)
	select {
	case <-wakeDone:
	case <-time.After(10 * time.Second):
	}

	// RED: the wake must not start a model turn on the stopped root.
	if entries := len(provider.Requests()); entries != entriesBefore {
		t.Fatalf("an automatic wake into a STOPPED root started a model turn (provider entries %d → %d) — SEC-1: after Stop, an automatic wake (no steer_message_id, no new human instruction) must not start a turn that launches or dispatches helpers; the wake never resumes a stopped root", entriesBefore, entries)
	}
	after := rootReopenedRecord(t, al, sid)
	if after.State != session.LifecycleStopped || after.Generation != landed.Generation {
		t.Fatalf("the wake changed the stopped root: before gen %d state %q, after gen %d state %q — a wake never revives a stopped root", landed.Generation, landed.State, after.Generation, after.State)
	}
	records2, listErr := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	require.NoError(t, listErr)
	if len(records2) != recordsBefore {
		t.Fatalf("the wake produced lifecycle records (%d → %d) — no helper may be launched or dispatched from a stopped root's automatic wake", recordsBefore, len(records2))
	}

	// Founder case 6 control: a NEW HUMAN instruction still restarts the
	// conversation, and delegation works in that new turn.
	msg := rootHumanMessage(sid)
	msg.GatewayUserID = "r5-owner"
	msg.Content = "Right, carry on and delegate the follow-up."
	if err := al.enqueueSteeringFromMessage(msg); err != nil {
		t.Fatalf("the new human instruction after Stop must restart the conversation: %v", err)
	}
	r1AwaitProvider(t, provider, 1)
	resumed := rootReopenedRecord(t, al, sid)
	if resumed.Generation != landed.Generation || resumed.State != session.LifecycleRunning || resumed.StopNote != nil {
		t.Fatalf("the new human instruction's revival = %+v, want the same-generation revival with the note cleared", resumed)
	}
	result := adr093AmendmentDelegateInSession(t, al, sid, "call-round5-after-stop")
	if result.IsError {
		t.Fatalf("delegation in the new human turn was refused:\n%s\n— founder case 6: a new instruction restarts the agent and delegation works", result.ForLLM)
	}
	childID := adr093ChildSessionIDFromResult(t, result.ForLLM, sid)
	if _, err := al.GetSessionLifecycleStore().Load(childID); err != nil {
		t.Fatalf("child lifecycle file missing after the post-stop delegation: %v", err)
	}
	provider.open(1)
	provider.open(2)
	joinCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !al.WaitForActiveRequestsContext(joinCtx) {
		t.Fatal("the resumed and delegated turns did not join")
	}
}
