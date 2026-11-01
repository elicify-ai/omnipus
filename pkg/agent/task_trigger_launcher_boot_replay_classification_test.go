package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Train 6b UAT re-run 2, row U2b-crash (/workspace/logs/6b-uat/rows-rerun.md):
// a crashed one-time task on the DEFAULT launcher path (one-time task, Mia,
// default workspace, "Run isolated" UNCHECKED) must replay at boot with its
// runtime classification intact — exactly 2 runs, the replay executing to a
// real outcome, and a further stop/start adding nothing. On cut 2e8c1d237 the
// live UAT instance instead logged: agent execution failed error="processTask
// Direct: the task run was not dispatched: steer: dispatch: session has no
// persisted runtime classification: task run session …" — a third run that
// failed internally.
//
// Root shape this suite pins (from the UAT evidence, not from the code): the
// gateway's boot order (pkg/gateway/gateway_boot.go::setupAndStartServices)
// starts the task-trigger scheduler — whose owed fires dispatch immediately —
// BEFORE the session-messaging plane, the task executor's lifecycle store and
// the SteerLauncher are wired (setupPlans/startPlanEngine/buildRESTAPI). A
// replay dispatched in that window runs without a persisted runtime
// classification; once the plane is wired, any wired enforcement of the
// runtime invariant refuses the run. The earlier launcher-path boot tests
// stayed green because their fixture (installTestSessionLauncher) wires the
// whole plane EAGERLY, before any boot — a state production never boots in.
//
// These tests therefore mirror production's real wiring ORDER: boot 2 starts
// its scheduler unwired, lets the owed fire dispatch, and only then wires the
// plane exactly as the gateway later does. The classification oracle is
// deterministic in both race outcomes; the refusal and completion oracles are
// asserted in addition (they can only add failures, never turn one green).
//
// Fix-direction caveat, recorded for the architect: this fixture pins the
// boot-wiring order as observed in production. A fix that reorders the
// gateway (wire the plane before the scheduler starts) is legitimate, but it
// changes the order this fixture must mirror — the test author updates the
// fixture's order to the new production order; the oracles below are never
// weakened to pass.

// newU2bUnwiredLauncherProcess mirrors newU2bLauncherProcess in every respect
// except the wiring: the SAME config, workspace membership, persisted main,
// judge and scheduler, but NONE of the session-messaging plane — no
// session-messaging stores, no task-executor lifecycle store, no
// SteerLauncher, no main resolver. This is the state the gateway's task
// trigger scheduler actually starts in.
func newU2bUnwiredLauncherProcess(t *testing.T, home string, clk *triggerFakeClock, worker *u2bAsyncWorker) *u2bLauncherProcess {
	t.Helper()
	t.Setenv(config.EnvHome, home)
	agentHome := filepath.Join(home, "native-agent")
	require.NoError(t, os.MkdirAll(agentHome, 0o700))
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{
				{ID: "native-agent", Name: "Native Agent", Type: config.AgentTypeCustom, Home: agentHome},
				judgeAgentConfigForTaskTests(t),
			},
		},
	}
	cfg.Performance.MaxParallelAgents = 1
	cfg.Sandbox.ToolPolicies = map[string]string{"goal_claim": "allow"}
	require.True(t, cfg.Agents.List[0].IsChatTarget(), "instrument: workers cannot own an eligible main")
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: "default", Name: "U2b launcher workspace", Status: "active", CoreTeam: []string{"native-agent"},
	}))
	wsID, member := workspace.FindForAgentPreferring(home, "native-agent", "default")
	require.True(t, member)
	require.Equal(t, "default", wsID, "instrument: the assignee is actually a workspace member")

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), worker)
	bindMetSoftTierJudge(t, al)
	// Deliberately NOT installTestSessionLauncher(t, al) and NOT
	// SetMainSessionResolver: this process boots in the gateway's
	// startSchedulers state, before the plane is wired.
	require.Nil(t, al.GetSessionLifecycleStore(), "instrument: boot 2 starts with no session lifecycle store wired")
	require.Nil(t, GetTaskExecutor(al).launcher, "instrument: boot 2 starts with no SteerLauncher wired")

	main, err := al.GetSessionStore().GetOrCreateMainSessionOwned("default", "native-agent", "uat-person")
	require.NoError(t, err)
	fresh, err := session.NewUnifiedStore(al.GetSessionStore().BaseDir())
	require.NoError(t, err)
	persisted, err := fresh.GetMeta(main.ID)
	require.NoError(t, err)
	require.Equal(t, session.SessionTypeMain, persisted.Type)
	require.Equal(t, "native-agent", persisted.AgentID)
	require.Equal(t, "default", persisted.WorkspaceID)
	require.Equal(t, "uat-person", persisted.Owner)
	sched := NewTaskTriggerScheduler(filepath.Join(home, "tasks_triggers", "jobs.json"), al.taskStore, al.taskExecutor)
	sched.SetClock(clk)
	al.SetTaskTriggerScheduler(sched)
	p := &u2bLauncherProcess{
		u2bAsyncProcess: &u2bAsyncProcess{home: home, loop: al, scheduler: sched, clock: clk},
		mainID:          main.ID,
		closeDone:       make(chan struct{}),
	}
	t.Cleanup(p.stop)
	return p
}

// wireLikeTheGatewayLatesWiresIt mirrors the gateway's LATE half: the plane is
// wired only after the scheduler has started and the owed fire has dispatched
// (setupPlans → startPlanEngine → buildRESTAPI order).
func wireLikeTheGatewayLatesWiresIt(t *testing.T, p *u2bLauncherProcess) {
	t.Helper()
	installTestSessionLauncher(t, p.loop)
	p.loop.taskExecutor.SetMainSessionResolver(&u6FakeMainResolver{elig: map[[2]string]string{
		{"default", "native-agent"}: p.mainID,
	}})
}

func TestU2bBootReplay_LateWiredBoot_DefaultLauncherPath_KeepsRuntimeClassification_AndRunsOnce(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker) // eager wiring = production steady state
	fire := p1.fireStarted(t, worker)
	jobsAtDeath := p1.storedJobs(t, fire.taskID)
	require.Len(t, jobsAtDeath, 1, "instrument: the delivered once job is durably armed at the crash boundary")
	require.Equal(t, fire.jobID, jobsAtDeath[0].ID)
	require.True(t, jobsAtDeath[0].Enabled)
	require.True(t, jobsAtDeath[0].State.Running, "instrument: the in-flight fire stays durably owned at the crash boundary")
	t.Logf("default-launcher crash boundary: real MAIN run open; persisted jobs=%d", len(jobsAtDeath))
	image := u2bAsyncSnapshot(t, home)
	p1.stop() // Join old writers before restoring the unchanged live crash image.
	u2bAsyncRestore(t, home, image)

	// Boot 2 in production's real order: the scheduler starts UNWIRED, the owed
	// fire dispatches in that window, and only then is the plane wired.
	p2 := newU2bUnwiredLauncherProcess(t, home, clk, newU2bAsyncWorker())
	p2.physicalBoot(t)
	p2.scheduler.RunDueJobs(p2.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p2.scheduler.WaitForLane)
	wireLikeTheGatewayLatesWiresIt(t, p2)
	u2bAsyncJoin(t, "real task execution", p2.loop.taskExecutor.wg.Wait)

	runs := p2.runs(t, fire.taskID)
	require.Len(t, runs, 2,
		"U2b-crash: run history shows exactly the abandoned original plus one real replay, never a third refused attempt")
	original, replay := runs[0], runs[1]
	if replay.RunID == fire.run.RunID {
		original, replay = replay, original
	}
	require.Equal(t, fire.run.RunID, original.RunID, "boot retains the original run's identity")
	require.Equal(t, task.StatusFailed, original.Status)
	require.Equal(t, "abandoned: gateway restarted mid-execution", original.Result)

	// THE classification oracle (deterministic in both race outcomes): the
	// replayed session must have a persisted runtime classification. Its record
	// is minted at dispatch, which happened while no lifecycle store was wired;
	// without the fix nothing was ever persisted for it.
	rec, err := p2.loop.GetSessionLifecycleStore().Load(replay.SessionID)
	require.NoError(t, err,
		"U2b: the replayed run's session must have a persisted runtime classification; "+
			"without it a wired enforce refuses the replay (agent execution failed: processTaskDirect: "+
			"the task run was not dispatched: session has no persisted runtime classification)")
	require.NotNil(t, rec)
	// The replay must be a real launcher-path MAIN child of the assignee's main
	// with this task as its origin — the classification the launcher mints.
	p2.requireMainChild(t, fire.taskID, replay.SessionID)

	if strings.Contains(replay.Result, "no persisted runtime classification") {
		t.Logf("observed enforce-refusal mode: the replay was refused by the wired invariant")
	} else {
		t.Logf("observed silent-skip mode: the replay ran without any persisted classification")
	}
	// (a2) dispatch proceeded, never the not-dispatched refusal; (b)+(c) the
	// replay actually started a turn and carries its real judged outcome.
	require.NotContains(t, replay.Result, "no persisted runtime classification",
		"U2b: the replay must not fail with the unpersisted-classification refusal")
	require.Equal(t, task.StatusDone, replay.Status, "the replay's real worker claim is judged and completed")
	require.Equal(t, u2bAsyncEvidence, replay.Result)
	require.NotNil(t, replay.EndedAt)
	require.NotEmpty(t, replay.SessionID)
	require.NotEqual(t, original.SessionID, replay.SessionID)
	require.Equal(t, task.RunKindScheduled, replay.Kind)
	require.Nil(t, replay.OccurrenceMs, "a once fire is not a recurring occurrence")
	require.Empty(t, p2.storedJobs(t, fire.taskID), "the completed real replay settles its retained job")

	// (d) a further tick and a further boot add nothing; (e) the task survives.
	p2.runDueAndJoin(t)
	require.Equal(t, runs, p2.runs(t, fire.taskID), "another tick adds no replay")
	p2.stop()
	p3 := bootU2bLauncherProcess(t, home, clk)
	require.Equal(t, runs, p3.runs(t, fire.taskID), "a further stop/start adds nothing")
	after, err := p3.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err, "U2b-crash: the task is not deleted")
	require.Equal(t, task.StatusDone, after.Status)
}

// TestU2bBootReplay_OrderlyStop_LateWiredBoot_KeepsRuntimeClassification_AndRunsOnce
// is addendum 9 item 1: the orderly-shutdown variant of the same defect. After
// a graceful `ctl.sh stop` then `start`, both UAT instances still failed the
// replay with the same unpersisted-runtime-classification refusal (run count
// correct: 2 runs; the replay never executes). Same oracles as the crash
// variant: the replay dispatches and starts a turn, exactly 2 runs, and a
// further boot adds nothing.
func TestU2bBootReplay_OrderlyStop_LateWiredBoot_KeepsRuntimeClassification_AndRunsOnce(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker) // eager wiring = production steady state
	fire := p1.fireStarted(t, worker)
	jobsAtDeath := p1.storedJobs(t, fire.taskID)
	require.Len(t, jobsAtDeath, 1, "instrument: the delivered once job is durably armed at the stop boundary")
	t.Logf("default-launcher stop boundary: real MAIN run open; persisted jobs=%d", len(jobsAtDeath))
	// Orderly shutdown modelled at its exit boundary: the POST-shutdown bytes
	// are what boot 2 starts from (gracefulProcessExit restores exactly those).
	p1.gracefulProcessExit(t, fire.taskID)

	// Boot 2 in production's real order: scheduler unwired, owed fire dispatches
	// in that window, plane wired only afterwards.
	p2 := newU2bUnwiredLauncherProcess(t, home, clk, newU2bAsyncWorker())
	p2.physicalBoot(t)
	p2.scheduler.RunDueJobs(p2.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p2.scheduler.WaitForLane)
	wireLikeTheGatewayLatesWiresIt(t, p2)
	u2bAsyncJoin(t, "real task execution", p2.loop.taskExecutor.wg.Wait)

	runs := p2.runs(t, fire.taskID)
	require.Len(t, runs, 2,
		"U2b-stop: run history shows exactly the interrupted original plus one real replay")
	original, replay := runs[0], runs[1]
	if replay.RunID == fire.run.RunID {
		original, replay = replay, original
	}
	require.Equal(t, fire.run.RunID, original.RunID, "boot retains the original run's identity")
	require.Equal(t, task.StatusFailed, original.Status)
	require.NotNil(t, original.EndedAt)

	// Same classification oracle, same deterministic failure: the replay was
	// dispatched while no lifecycle store was wired, so nothing was persisted
	// for its session and a wired enforce refuses it.
	rec, err := p2.loop.GetSessionLifecycleStore().Load(replay.SessionID)
	require.NoError(t, err,
		"U2b-stop: the orderly restart's replayed run must have a persisted runtime classification; "+
			"without it a wired enforce refuses the replay (agent execution failed: processTaskDirect: "+
			"the task run was not dispatched: session has no persisted runtime classification)")
	require.NotNil(t, rec)
	p2.requireMainChild(t, fire.taskID, replay.SessionID)

	require.NotContains(t, replay.Result, "no persisted runtime classification",
		"U2b-stop: the replay must not fail with the unpersisted-classification refusal")
	require.Equal(t, task.StatusDone, replay.Status, "the replay's real worker claim is judged and completed")
	require.Equal(t, u2bAsyncEvidence, replay.Result)
	require.NotNil(t, replay.EndedAt)
	require.NotEmpty(t, replay.SessionID)
	require.NotEqual(t, original.SessionID, replay.SessionID)
	require.Equal(t, task.RunKindScheduled, replay.Kind)
	require.Nil(t, replay.OccurrenceMs, "a once fire is not a recurring occurrence")
	require.Empty(t, p2.storedJobs(t, fire.taskID), "the completed real replay settles its retained job")

	p2.runDueAndJoin(t)
	require.Equal(t, runs, p2.runs(t, fire.taskID), "another tick adds no replay")
	p2.stop()
	p3 := bootU2bLauncherProcess(t, home, clk)
	require.Equal(t, runs, p3.runs(t, fire.taskID), "a further stop/start adds nothing")
	after, err := p3.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err, "U2b-stop: the task is not deleted")
	require.Equal(t, task.StatusDone, after.Status)
}

// wireEnforcementOnly wires ONLY the loop's session-messaging plane — the
// enforcement side of the runtime invariant (session_messaging_wire.go's
// SetSessionMessagingStores → GetSessionLifecycleStore) — and deliberately
// leaves the task executor's lifecycle store and SteerLauncher unwired, as
// production's boot had them at the owed fire. Instance T's boot resolved the
// timing race in exactly this shape: the replay's session was minted with no
// lifecycle store while the wired enforcement refused it. Pre-wiring the
// enforcement side forces that refusal deterministically (no goroutine race),
// so the refusal mode's spec oracles are pinned independently of timing.
func wireEnforcementOnly(t *testing.T, p *u2bLauncherProcess) {
	t.Helper()
	home := config.OmnipusHomeDir()
	p.loop.SetSessionMessagingStores(
		session.NewMessageInboxStore(filepath.Join(home, "session_messages")),
		session.NewLifecycleStore(filepath.Join(home, "session_lifecycle")))
	require.NotNil(t, p.loop.GetSessionLifecycleStore(), "instrument: the enforcement plane is wired")
	require.Nil(t, GetTaskExecutor(p.loop).getLifecycleStore(), "instrument: the executor's store is still unwired")
	require.Nil(t, GetTaskExecutor(p.loop).launcher, "instrument: no SteerLauncher yet")
}

// TestU2bBootReplay_RefusedReplayMode_ExecutesOnce_NoThirdRun pins the
// refusal mode of the same defect — the mode instance T actually recorded —
// deterministically, and pins addendum 9's no-third-run requirement in that
// mode. Oracle sources: rows-rerun.md U2b-crash and the re-run-2 scope ("the
// replay actually executing (real outcome), exactly 2 runs"; "the 3rd 'Run
// now' run must not appear"), and task_run_loop.go's own settlement contract
// for a run that never ran the work: no attempt, no restart.
//
// After the fix, a replay dispatched with the enforcement plane already wired
// must still execute to a real judged outcome — the classification must EXIST
// by construction, not be excused — and the refused-run settlement must never
// re-arm the once fire or re-dispatch the task.
func TestU2bBootReplay_RefusedReplayMode_ExecutesOnce_NoThirdRun(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker) // eager wiring = production steady state
	fire := p1.fireStarted(t, worker)
	image := u2bAsyncSnapshot(t, home)
	p1.stop() // Join old writers before restoring the unchanged live crash image.
	u2bAsyncRestore(t, home, image)

	// Boot 2 in the half-wired state: enforcement wired, executor store and
	// launcher still unwired — the owed fire dispatches exactly like T's did.
	p2 := newU2bUnwiredLauncherProcess(t, home, clk, newU2bAsyncWorker())
	wireEnforcementOnly(t, p2)
	p2.physicalBoot(t)
	p2.scheduler.RunDueJobs(p2.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p2.scheduler.WaitForLane)
	u2bAsyncJoin(t, "real task execution", p2.loop.taskExecutor.wg.Wait)

	runs := p2.runs(t, fire.taskID)
	require.Len(t, runs, 2,
		"U2b: run history shows exactly the abandoned original plus one replay attempt")
	original, replay := runs[0], runs[1]
	if replay.RunID == fire.run.RunID {
		original, replay = replay, original
	}
	require.Equal(t, fire.run.RunID, original.RunID, "boot retains the original run's identity")
	require.Equal(t, "abandoned: gateway restarted mid-execution", original.Result)

	// Instrument precondition: this boot really is in T's refusal mode — the
	// classification-less replay was refused by the wired enforcement and the
	// task ended failed with no attempt used (task_run_loop.go's
	// ErrTaskRunNotDispatched settlement).
	failedTask, err := p2.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err)
	require.Contains(t, failedTask.Result, "The task could not be started:",
		"instrument: expected the deterministic refusal mode (no attempt used)")
	require.Contains(t, failedTask.Result, "no persisted runtime classification",
		"instrument: the refusal is the unpersisted-runtime-classification one")

	// THE classification oracle, now deterministic: the replayed session must
	// have a persisted runtime classification even though its dispatch saw no
	// executor store — enforcement could observe it, so it must exist.
	rec, err := p2.loop.GetSessionLifecycleStore().Load(replay.SessionID)
	require.NoError(t, err,
		"U2b: the replayed run's session must have a persisted runtime classification; "+
			"without it the wired enforce refuses the replay (agent execution failed: processTaskDirect: "+
			"the task run was not dispatched: session has no persisted runtime classification)")
	require.NotNil(t, rec)
	p2.requireMainChild(t, fire.taskID, replay.SessionID)

	// The replay must have actually executed to its real judged outcome — the
	// fix may not answer this state by excusing the enforcement.
	require.NotContains(t, replay.Result, "no persisted runtime classification",
		"U2b: the replay must not fail with the unpersisted-classification refusal")
	require.Equal(t, task.StatusDone, replay.Status, "the replay's real worker claim is judged and completed")
	require.Equal(t, u2bAsyncEvidence, replay.Result)
	require.NotNil(t, replay.EndedAt)
	require.Equal(t, task.RunKindScheduled, replay.Kind)
	require.Nil(t, replay.OccurrenceMs)

	// Addendum 9: a refused replay is not a replayable interruption. Its once
	// job must be gone, and neither a further tick nor a further boot may add
	// the third "Run now" run.
	require.Empty(t, p2.storedJobs(t, fire.taskID),
		"a refused replay must not keep its once job armed")
	p2.runDueAndJoin(t)
	require.Equal(t, runs, p2.runs(t, fire.taskID), "no third run may follow a refused replay")
	p2.stop()
	p3 := bootU2bLauncherProcess(t, home, clk)
	require.Equal(t, runs, p3.runs(t, fire.taskID), "no third run after a further boot either")
	after, err := p3.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err, "U2b: the task is not deleted")
	require.Equal(t, task.StatusDone, after.Status)
}

// fireIsolatedWhileProviderHeld is fireWhileProviderHeld with run_isolated on:
// the same MAIN-eligible assignee, but the task is forced onto the ISOLATED
// path (deriveTaskRunMode: run_isolated wins over an eligible main).
func (p *u2bLauncherProcess) fireIsolatedWhileProviderHeld(t *testing.T, worker *u2bAsyncWorker) u2bAsyncFire {
	t.Helper()
	require.NoError(t, p.scheduler.Start())
	tk := newCompletionContractTask(t, p.loop, "native-agent", "U2b isolated once task")
	isolated := true
	fireAt := p.clock.Now().Add(time.Minute).UnixMilli()
	trigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &fireAt}}
	stored, err := p.loop.taskStore.Update(tk.ID, task.Patch{RunIsolated: &isolated, Trigger: &trigger})
	require.NoError(t, err)
	require.True(t, stored.RunIsolated, "instrument: the isolated control task really runs isolated")
	p.loop.NotifyTaskUpserted(stored)
	jobs := p.storedJobs(t, tk.ID)
	require.Len(t, jobs, 1, "instrument: the once job is durably armed before firing")

	p.clock.Advance(2 * time.Minute)
	p.scheduler.RunDueJobs(p.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane while the provider is held", p.scheduler.WaitForLane)
	u2bAsyncJoin(t, "the real task's first provider call", func() { <-worker.entered })
	active, err := p.loop.taskStore.Get(tk.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusInProgress, active.Status, "instrument: dispatch returned but the task is still active")
	runs := p.runs(t, tk.ID)
	require.Len(t, runs, 1, "instrument: actual execution opened exactly one TaskRun")
	require.NotEmpty(t, runs[0].SessionID, "instrument: this is a real session-backed task run")
	rec, err := p.loop.GetSessionLifecycleStore().Load(runs[0].SessionID)
	require.NoError(t, err)
	require.Empty(t, rec.SteeringSessionID(),
		"instrument: run_isolated forces a parentless session even for an eligible main")
	return u2bAsyncFire{taskID: tk.ID, jobID: jobs[0].ID, run: runs[0]}
}

// TestU2bBootReplay_IsolatedTask_WiredBoot_ReplaysExactlyOnce is the positive
// control for the classification oracle: on the ISOLATED path with an eagerly
// wired boot (a state production reaches after the plane is wired), the
// replay already keeps its classification and runs exactly once. It must stay
// green — it proves the oracle can tell a working replay from the broken one.
func TestU2bBootReplay_IsolatedTask_WiredBoot_ReplaysExactlyOnce(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	fire := p1.fireIsolatedWhileProviderHeld(t, worker)
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)

	p2 := bootU2bLauncherProcess(t, home, clk)
	runs := p2.runs(t, fire.taskID)
	original, replay := requireU2bAsyncReplay(t, fire, runs)
	require.Equal(t, "abandoned: gateway restarted mid-execution", original.Result)
	rec, err := p2.loop.GetSessionLifecycleStore().Load(replay.SessionID)
	require.NoError(t, err, "the isolated replay also persists its runtime classification")
	require.NotNil(t, rec)
	require.Empty(t, rec.SteeringSessionID(), "the isolated replay stays parentless")
	require.Empty(t, p2.storedJobs(t, fire.taskID))
	p2.runDueAndJoin(t)
	require.Equal(t, runs, p2.runs(t, fire.taskID))
	p2.stop()
	p3 := bootU2bLauncherProcess(t, home, clk)
	require.Equal(t, runs, p3.runs(t, fire.taskID), "a further boot adds nothing to the isolated replay")
}

// TestU2bBootReplay_UserStoppedTask_LateWiredBoot_IsNeverReplayed is the guard
// control (rows-rerun.md U2b-user): a run the USER stopped is never replayed —
// and the guard must hold even when the boot dispatches through the unwired
// window, so the eventual fix cannot "repair" replays by replaying everything.
func TestU2bBootReplay_UserStoppedTask_LateWiredBoot_IsNeverReplayed(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	fire := p1.fireStarted(t, worker)
	result, err := p1.loop.StopSession(context.Background(), StopRequest{
		SessionID: fire.run.SessionID,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "uat-person"},
		Channel:   "web",
	})
	require.NoError(t, err)
	require.NoError(t, result.RootErr)
	require.True(t, result.Fired, "instrument: Stop reached the real running MAIN task")
	u2bAsyncJoin(t, "person-stopped MAIN execution and settlement", p1.loop.taskExecutor.wg.Wait)
	before := p1.runs(t, fire.taskID)
	require.Len(t, before, 1)
	require.Equal(t, task.StatusFailed, before[0].Status)
	stopped, err := p1.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err)
	require.NotEqual(t, InterruptedTaskResult, stopped.Result, "intentional Stop is not a replayable gateway interruption")
	record, err := p1.loop.GetSessionLifecycleStore().Load(fire.run.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, record.State)
	require.NotNil(t, record.StopNote)
	require.Empty(t, p1.storedJobs(t, fire.taskID), "person Stop settles, rather than retaining, the once fire")
	p1.stop()

	p2 := newU2bUnwiredLauncherProcess(t, home, clk, newU2bAsyncWorker())
	p2.physicalBoot(t)
	p2.scheduler.RunDueJobs(p2.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p2.scheduler.WaitForLane)
	wireLikeTheGatewayLatesWiresIt(t, p2)
	u2bAsyncJoin(t, "real task execution", p2.loop.taskExecutor.wg.Wait)
	require.Equal(t, before, p2.runs(t, fire.taskID),
		"U2b-user: a person-stopped run is never replayed, not even by an unwired-window boot")
	after, err := p2.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err)
	require.Equal(t, stopped.Status, after.Status)
	require.Equal(t, stopped.Result, after.Result)
	require.Equal(t, stopped.CancelReason, after.CancelReason)
	recordAfter, err := p2.loop.GetSessionLifecycleStore().Load(fire.run.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, recordAfter.State)
	require.Empty(t, p2.storedJobs(t, fire.taskID))
	p2.stop()
	p3 := bootU2bLauncherProcess(t, home, clk)
	require.Equal(t, before, p3.runs(t, fire.taskID), "even repeated boots cannot replay a person-stopped run")
}
