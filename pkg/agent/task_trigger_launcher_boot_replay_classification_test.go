package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
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
// Fix-direction note, updated 2026-11-01: the delivered fix (a92efc83e,
// pkg/agent/task_trigger_boot_hold.go) is the readiness-hold shape — a
// boot-owed fire waits until the launcher, lifecycle store and main resolver
// are all wired — so this fixture's observed production order stands
// unchanged. A gateway-order fix, had it been chosen, would instead have
// required updating this fixture's order (by the test author) to mirror the
// new production order; the oracles below are never weakened to pass.

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

// expectSessionPlaneLikeTheGateway marks the executor's session plane
// expected, exactly as the gateway does before it starts the schedulers
// (pkg/gateway/gateway_boot.go::setupAndStartServices →
// TaskExecutor.ExpectSessionPlane, 9a82f7ecf). On a tree without the seam
// (the pre-fix ebb5d3922, where the drain dispatches into the unwired
// window) the method does not exist, so the call goes through a local
// interface assertion: the row then drives the raw unwired boot — the
// defect itself — and the absence is logged, never faked.
type sessionPlaneExpecter interface{ ExpectSessionPlane() }

func expectSessionPlaneLikeTheGateway(t *testing.T, p *u2bLauncherProcess) {
	t.Helper()
	if e, ok := any(GetTaskExecutor(p.loop)).(sessionPlaneExpecter); ok {
		e.ExpectSessionPlane()
		return
	}
	t.Logf("seam TaskExecutor.ExpectSessionPlane absent on this tree: the boot cannot mark " +
		"the plane expected, so the drain dispatches into the unwired window (the defect under test)")
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
	// fire dispatches (or, under the fix, is held and later released), and only
	// then is the plane wired. The worker is released up front: a fixed replay
	// really executes, and a held provider must not be what stops it.
	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
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
	// (or, under the fix, is held and later released), plane wired only
	// afterwards. The worker is released up front: a fixed replay really
	// executes, and a held provider must not be what stops it.
	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
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

// completedWorkerForU2b returns an already-released provider worker, for a
// boot whose fire may really execute once the plane is wired — the same shape
// bootU2bLauncherProcess uses for its own workers.
func completedWorkerForU2b() *u2bAsyncWorker {
	worker := newU2bAsyncWorker()
	worker.complete()
	return worker
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

// TestU2bBootReplay_HalfWiredWindow_FireWaitsForPlane_ThenExecutesOnce_NoThirdRun
// pins the half-wired window — the enforcement plane wired while the
// executor's lifecycle store, launcher and main resolver are not — as a STATE
// precondition, and requires the owed fire to survive it: dispatched in the
// window (pre-fix) or held and released once the plane is complete (under the
// a92efc83e hold), the replay must execute to a real judged outcome with a
// persisted runtime classification, exactly 2 runs, and never a third.
// Oracle sources: rows-rerun.md U2b-crash and the re-run-2 scope ("the replay
// actually executing (real outcome), exactly 2 runs"; "the 3rd 'Run now' run
// must not appear"). No outcome assertion names the refusal: the refusal was
// the pre-fix symptom, never the spec.
func TestU2bBootReplay_HalfWiredWindow_FireWaitsForPlane_ThenExecutesOnce_NoThirdRun(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker) // eager wiring = production steady state
	fire := p1.fireStarted(t, worker)
	image := u2bAsyncSnapshot(t, home)
	p1.stop() // Join old writers before restoring the unchanged live crash image.
	u2bAsyncRestore(t, home, image)

	// Boot 2 in the half-wired state: enforcement wired, executor store,
	// launcher and main resolver not — the fire becomes due in exactly this
	// state. The worker is released up front: once the plane is wired the
	// replay really executes, and a held provider must not be what stops it.
	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	wireEnforcementOnly(t, p2)
	p2.physicalBoot(t)
	p2.scheduler.RunDueJobs(p2.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p2.scheduler.WaitForLane)
	// State precondition at the moment the fire became due — about wiring,
	// never about outcomes.
	require.NotNil(t, p2.loop.GetSessionLifecycleStore(), "state: the enforcement plane is wired")
	require.Nil(t, GetTaskExecutor(p2.loop).getLifecycleStore(), "state: the executor's lifecycle store is not wired")
	require.Nil(t, GetTaskExecutor(p2.loop).launcher, "state: no launcher is wired yet")
	require.Nil(t, GetTaskExecutor(p2.loop).mainResolver, "state: no main resolver is wired yet")
	// The production wiring order completes the plane — installTestSession
	// Launcher's stores and launcher, then the resolver LAST, as buildRESTAPI
	// does. This is what releases a held fire and what makes any replay
	// dispatched in the window executable.
	wireLikeTheGatewayLatesWiresIt(t, p2)
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

	// THE classification oracle: the replayed session must have a persisted
	// runtime classification — a dispatch the wired enforcement can observe is
	// a dispatch whose classification must exist, never be excused.
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

// TestU2bBootReplay_HeldFire_SchedulerStopsFirst_KeptAndReplayedOnceAtNextBoot
// is addendum-10 C1: a fire held in the unwired window whose scheduler stops
// before the plane is wired must keep its job, and the next physical boot must
// replay it exactly once — with a persisted runtime classification and a real
// outcome. Oracle: rows-rerun.md U2b-crash (exactly 2 runs, replay executing),
// and the fix's own contract (task_trigger_boot_hold.go::runHeldBootOwedFire:
// a stop before the wiring leaves the debt for the next boot).
func TestU2bBootReplay_HeldFire_SchedulerStopsFirst_KeptAndReplayedOnceAtNextBoot(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	fire := p1.fireStarted(t, worker)
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)

	// Boot 2: NOTHING is wired — the plain unwired window. The worker is
	// released up front: pre-fix the fire dispatches here and its replay must
	// not hang the fixture; under the fix the fire is held and this worker is
	// never called in boot 2.
	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	p2.physicalBoot(t)
	p2.scheduler.RunDueJobs(p2.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p2.scheduler.WaitForLane)
	u2bAsyncJoin(t, "boot-2 execution, whatever the boot decided", p2.loop.taskExecutor.wg.Wait)
	require.NotEmpty(t, p2.storedJobs(t, fire.taskID),
		"C1: a fire held for the unwired window keeps its job while held")
	p2.stop() // The scheduler stops first; the plane was never wired in boot 2.
	require.NotEmpty(t, p2.storedJobs(t, fire.taskID),
		"C1: the stop must not consume or drop the held fire's job")

	// Boot 3, fully wired and steady: exactly one replay, real outcome.
	p3 := bootU2bLauncherProcess(t, home, clk)
	runs := p3.runs(t, fire.taskID)
	original, replay := requireU2bAsyncReplay(t, fire, runs)
	require.Equal(t, "abandoned: gateway restarted mid-execution", original.Result)
	rec, err := p3.loop.GetSessionLifecycleStore().Load(replay.SessionID)
	require.NoError(t, err,
		"C1: the next boot's replay must carry a persisted runtime classification")
	require.NotNil(t, rec)
	p3.requireMainChild(t, fire.taskID, replay.SessionID)
	require.Empty(t, p3.storedJobs(t, fire.taskID), "the replayed fire's job is settled")
	p3.runDueAndJoin(t)
	require.Equal(t, runs, p3.runs(t, fire.taskID), "another tick adds no replay")
	p3.stop()
	p4 := bootU2bLauncherProcess(t, home, clk)
	require.Equal(t, runs, p4.runs(t, fire.taskID), "a further boot adds nothing")
	after, err := p4.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err, "C1: the task is not deleted")
	require.Equal(t, task.StatusDone, after.Status)
}

// TestU2bBootReplay_HeldFire_TaskDeletedWhileHeld_NeverRuns_JobRemoved is
// addendum-10 C2: a fire held in the unwired window must not run when the task
// is deleted while it is held, even though the plane is wired afterwards (the
// release must find the fire superseded), and the job must be removed.
// Oracle: the fix's own contract (task_trigger_boot_hold.go::runHeldBootOwed
// Fire's superseded branch) over pkg/agent/task_trigger.go::OnTaskDeleted and
// the AgentLoop.NotifyTaskDeleted notification production uses.
func TestU2bBootReplay_HeldFire_TaskDeletedWhileHeld_NeverRuns_JobRemoved(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	fire := p1.fireStarted(t, worker)
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)

	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	p2.physicalBoot(t)
	p2.scheduler.RunDueJobs(p2.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p2.scheduler.WaitForLane)
	u2bAsyncJoin(t, "boot-2 execution, whatever the boot decided", p2.loop.taskExecutor.wg.Wait)
	runsWhileHeld := p2.runs(t, fire.taskID)
	require.Len(t, runsWhileHeld, 1,
		"C2: the held fire must not run while the plane is unwired — a run here means nothing held it")

	// The task is deleted while the fire is held, through the production
	// notification the loop itself uses.
	_, err := p2.loop.taskStore.Delete(fire.taskID)
	require.NoError(t, err)
	p2.loop.NotifyTaskDeleted(fire.taskID)
	require.Empty(t, p2.storedJobs(t, fire.taskID),
		"C2: deleting the task removes the held fire's job")

	// Wiring the plane now (the release condition) must not resurrect the fire.
	wireLikeTheGatewayLatesWiresIt(t, p2)
	u2bAsyncJoin(t, "release after deletion", p2.loop.taskExecutor.wg.Wait)
	require.Equal(t, runsWhileHeld, p2.runs(t, fire.taskID),
		"C2: the superseded fire must not run once the plane is wired")
	require.Empty(t, p2.storedJobs(t, fire.taskID))
	p2.stop()
	p3 := bootU2bLauncherProcess(t, home, clk)
	require.Empty(t, p3.storedJobs(t, fire.taskID), "C2: no job is recreated for a deleted task")
	_, err = p3.loop.taskStore.Get(fire.taskID)
	require.Error(t, err, "C2: the deleted task is not resurrected by a later boot")
}

// TestU2bBootReplay_QueueDrain_UnwiredWindow_TaskKeepsClassification_AndRunsOnce
// is addendum-10 C3, updated for addendum 11: the drain-path regression
// control. Its earlier shape expected a run from a tick whose launcher and
// resolver were never wired — the exact isolated-run behaviour the drain
// hold (round A) removes — so it now wires the plane fully, late and in
// production order (as the half-wired fire test does) before expecting the
// run. The outcome oracles are unchanged: exactly one run, a persisted
// runtime classification, a real judged outcome, never dispatched twice.
// Production path this pins: the queue drain (sole caller
// pkg/heartbeat/task_drain.go, first tick one second after Start) dispatches
// `next` tasks through pkg/agent/task_executor.go::CheckQueuedTasks once the
// session plane is wired.
func TestU2bBootReplay_QueueDrain_UnwiredWindow_TaskKeepsClassification_AndRunsOnce(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	p1 := newU2bLauncherProcess(t, home, clk, completedWorkerForU2b())
	queued := newCompletionContractTask(t, p1.loop, "native-agent", "U2b queue-drain task")
	// No trigger is armed: the cron lane can never fire it — only the queue
	// drain can dispatch it, which isolates the path under test.
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)

	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	expectSessionPlaneLikeTheGateway(t, p2) // the gateway marks the plane expected before the schedulers start
	wireEnforcementOnly(t, p2)
	p2.physicalBoot(t)
	// The production wiring order completes the plane before the drain is
	// expected to dispatch: stores, then executor store + launcher, then the
	// main resolver LAST.
	wireLikeTheGatewayLatesWiresIt(t, p2)
	u2bAsyncJoin(t, "release after full wiring", p2.loop.taskExecutor.wg.Wait)

	// One drain tick through the drain's own production entry.
	p2.loop.taskExecutor.CheckQueuedTasks(context.Background())
	u2bAsyncJoin(t, "queue-drain dispatch and execution", p2.loop.taskExecutor.wg.Wait)

	runs := p2.runs(t, queued.ID)
	require.Len(t, runs, 1, "C3: the queued task dispatches exactly once, after full wiring")
	qrec, err := p2.loop.GetSessionLifecycleStore().Load(runs[0].SessionID)
	require.NoError(t, err,
		"C3: a task the queue drain dispatches must carry a persisted runtime classification")
	require.NotNil(t, qrec)
	require.Equal(t, task.StatusDone, runs[0].Status, "C3: the drained task executes to a real outcome")
	require.Equal(t, u2bAsyncEvidence, runs[0].Result)
	tk, err := p2.loop.taskStore.Get(queued.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, tk.Status)

	// A further drain tick dispatches nothing: the task is terminal.
	p2.loop.taskExecutor.CheckQueuedTasks(context.Background())
	u2bAsyncJoin(t, "second drain tick", p2.loop.taskExecutor.wg.Wait)
	require.Equal(t, runs, p2.runs(t, queued.ID), "C3: the drained task is never dispatched twice")
}

// TestU2bBootReplay_DrainHold_NoStoreWindow_TickWaits_ThenRunsOnce is
// addendum-11 W1: the drain's tick lands while NO store is wired at all —
// production's startSchedulers-to-setupPlans window (gateway_boot.go: start
// schedulers :360, SetSessionMessagingStores :1029). A run dispatched there
// was minted nowhere (ebb5d3922's recordStore has neither store) and is
// refused once enforcement is wired: the same UAT error. Oracle: the tick
// dispatches nothing before the plane is fully wired; after wiring in
// production order the next tick dispatches exactly one run that executes to
// Done with a persisted classification; a further tick adds nothing; the
// task is not left stuck in queued.
//
// "Plane expected" is the gateway's own seam: TaskExecutor.ExpectSessionPlane,
// called before startSchedulers (9a82f7ecf), so the drain holds until the
// launcher, the executor's lifecycle store and the main resolver are all
// wired. An executor never marked (the existing no-plane CheckQueuedTasks
// unit fixtures) dispatches as before.
func TestU2bBootReplay_DrainHold_NoStoreWindow_TickWaits_ThenRunsOnce(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	p1 := newU2bLauncherProcess(t, home, clk, completedWorkerForU2b())
	queued := newCompletionContractTask(t, p1.loop, "native-agent", "U2b drain W1 task")
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)

	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	expectSessionPlaneLikeTheGateway(t, p2) // the gateway marks the plane expected before the schedulers start
	p2.physicalBoot(t)
	require.Nil(t, GetTaskExecutor(p2.loop).getLifecycleStore(), "state: no store is wired at tick 1")

	// Drain tick 1: NO store is wired (the W1 window).
	p2.loop.taskExecutor.CheckQueuedTasks(context.Background())
	u2bAsyncJoin(t, "drain tick 1 in the no-store window", p2.loop.taskExecutor.wg.Wait)
	require.Empty(t, p2.runs(t, queued.ID),
		"W1: the drain must dispatch nothing while the expected session plane is unwired — "+
			"a run minted nowhere is refused once enforcement is wired")

	// Wire in production order: stores (setupPlans), then executor store and
	// launcher, then the main resolver LAST (buildRESTAPI).
	wireEnforcementOnly(t, p2)
	wireLikeTheGatewayLatesWiresIt(t, p2)

	// Drain tick 2, after full wiring.
	p2.loop.taskExecutor.CheckQueuedTasks(context.Background())
	u2bAsyncJoin(t, "drain tick 2 after full wiring", p2.loop.taskExecutor.wg.Wait)

	runs := p2.runs(t, queued.ID)
	require.Len(t, runs, 1, "W1: exactly one run, dispatched after the plane is fully wired")
	w1rec, err := p2.loop.GetSessionLifecycleStore().Load(runs[0].SessionID)
	require.NoError(t, err, "W1: the dispatched task's session must have a persisted runtime classification")
	require.NotNil(t, w1rec)
	require.Equal(t, task.StatusDone, runs[0].Status, "W1: the drained task executes to a real outcome")
	require.Equal(t, u2bAsyncEvidence, runs[0].Result)
	tk, err := p2.loop.taskStore.Get(queued.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, tk.Status, "W1: the task is not left stuck in queued")

	// Tick 3 adds nothing: the task is terminal.
	p2.loop.taskExecutor.CheckQueuedTasks(context.Background())
	u2bAsyncJoin(t, "drain tick 3", p2.loop.taskExecutor.wg.Wait)
	require.Equal(t, runs, p2.runs(t, queued.ID), "W1: the drained task is never dispatched twice")
}

// TestU2bBootReplay_DrainHold_HalfWiredWindow_NoIsolatedRun_MainChildAfterWiring
// is addendum-11 W2: the drain's tick lands in the half-wired window — the
// session-messaging stores wired (setupPlans) while the launcher and main
// resolver are not yet (startPlanEngine/buildRESTAPI) — and the assignee owns
// an eligible main. A run dispatched there derives ISOLATED (no launcher, no
// resolver) instead of being that main's child: the run loses its parent,
// grants inheritance and per-chat Auto setting. Oracle: no isolated run from
// the half-wired tick; after full wiring in production order the next tick
// dispatches exactly one run as a MAIN child (requireMainChild evidence,
// which also proves the persisted classification), executing to Done; a
// further tick adds nothing. Same plane-expected seam as W1.
func TestU2bBootReplay_DrainHold_HalfWiredWindow_NoIsolatedRun_MainChildAfterWiring(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	p1 := newU2bLauncherProcess(t, home, clk, completedWorkerForU2b())
	queued := newCompletionContractTask(t, p1.loop, "native-agent", "U2b drain W2 task")
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)

	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	expectSessionPlaneLikeTheGateway(t, p2) // the gateway marks the plane expected before the schedulers start
	wireEnforcementOnly(t, p2)              // stores wired; launcher and resolver not
	require.Nil(t, GetTaskExecutor(p2.loop).getLifecycleStore(), "state: the executor's store is not wired at tick 1")
	require.Nil(t, GetTaskExecutor(p2.loop).launcher, "state: no launcher is wired yet")
	require.Nil(t, GetTaskExecutor(p2.loop).mainResolver, "state: no main resolver is wired yet")
	p2.physicalBoot(t)

	// Drain tick 1: the half-wired window (W2), assignee owns a main.
	p2.loop.taskExecutor.CheckQueuedTasks(context.Background())
	u2bAsyncJoin(t, "drain tick 1 in the half-wired window", p2.loop.taskExecutor.wg.Wait)
	require.Empty(t, p2.runs(t, queued.ID),
		"W2: the drain must dispatch nothing while the expected plane is only half wired — "+
			"a run here went isolated instead of being the assignee main's child")

	// Full wiring in production order; the resolver LAST.
	wireLikeTheGatewayLatesWiresIt(t, p2)

	// Drain tick 2, after full wiring.
	p2.loop.taskExecutor.CheckQueuedTasks(context.Background())
	u2bAsyncJoin(t, "drain tick 2 after full wiring", p2.loop.taskExecutor.wg.Wait)

	runs := p2.runs(t, queued.ID)
	require.Len(t, runs, 1, "W2: exactly one run, dispatched after the plane is fully wired")
	p2.requireMainChild(t, queued.ID, runs[0].SessionID) // classification + real launcher parentage + task origin
	require.Equal(t, task.StatusDone, runs[0].Status, "W2: the drained task executes to a real outcome")
	require.Equal(t, u2bAsyncEvidence, runs[0].Result)
	tk, err := p2.loop.taskStore.Get(queued.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, tk.Status, "W2: the task is not left stuck in queued")

	// Tick 3 adds nothing.
	p2.loop.taskExecutor.CheckQueuedTasks(context.Background())
	u2bAsyncJoin(t, "drain tick 3", p2.loop.taskExecutor.wg.Wait)
	require.Equal(t, runs, p2.runs(t, queued.ID), "W2: the drained task is never dispatched twice")
}

// TestU2bBootReplay_DrainHold_ResolverUnwiredWindow_TickWaits_ThenRunsAsMainChild
// is addendum-12 W3, and is the only drain row whose intermediate tick lands
// PAST the launcher and the executor's lifecycle store but BEFORE the main
// resolver — the exact state that distinguishes the fix's three-term
// plane-wired condition (pkg/agent/task_trigger_boot_hold.go::
// sessionPlaneWiredLocked: launcher AND lifecycleStore AND mainResolver)
// from a two-term one. W1's window has nothing wired and W2's wiring helper
// completes launcher, store and resolver in one step, so neither can see a
// two-term regression; this row's tick can. Oracle: with the plane marked
// expected and the resolver still unwired, the drain dispatches nothing — in
// particular no isolated run (with the store wired, a dispatched run would
// classify fine and complete Done, silently losing its main parent). Then
// the resolver is wired LAST (production order), the next tick dispatches
// exactly one run as a MAIN child (requireMainChild evidence, which also
// proves the persisted classification), executing to Done; a further tick
// adds nothing; the task is not left queued. The row's failability is
// proven by CHECK's mutant (b) ("drop mainResolver from the plane-wired
// condition"), aimed at sessionPlaneWiredLocked's mainResolver term.
func TestU2bBootReplay_DrainHold_ResolverUnwiredWindow_TickWaits_ThenRunsAsMainChild(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	p1 := newU2bLauncherProcess(t, home, clk, completedWorkerForU2b())
	queued := newCompletionContractTask(t, p1.loop, "native-agent", "U2b drain W3 task")
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)

	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	expectSessionPlaneLikeTheGateway(t, p2) // the gateway marks the plane expected before the schedulers start
	// Production order, stopped one wire short: stores, then the executor's
	// lifecycle store + launcher — the main resolver deliberately NOT yet.
	wireEnforcementOnly(t, p2)
	installTestSessionLauncher(t, p2.loop)
	require.NotNil(t, GetTaskExecutor(p2.loop).getLifecycleStore(), "state: the executor's store is wired")
	require.NotNil(t, GetTaskExecutor(p2.loop).launcher, "state: the launcher is wired")
	require.Nil(t, GetTaskExecutor(p2.loop).mainResolver, "state: the main resolver is NOT wired yet")
	p2.physicalBoot(t)

	// Drain tick 1: launcher and store wired, resolver not (the W3 window).
	p2.loop.taskExecutor.CheckQueuedTasks(context.Background())
	u2bAsyncJoin(t, "drain tick 1 with the resolver unwired", p2.loop.taskExecutor.wg.Wait)
	require.Empty(t, p2.runs(t, queued.ID),
		"W3: the drain must dispatch nothing until the main resolver is wired too — "+
			"a run here classified and completed but went isolated, losing its main parent")

	// The resolver LAST, completing the plane exactly as buildRESTAPI does.
	p2.loop.taskExecutor.SetMainSessionResolver(&u6FakeMainResolver{elig: map[[2]string]string{
		{"default", "native-agent"}: p2.mainID,
	}})

	// Drain tick 2, after full wiring.
	p2.loop.taskExecutor.CheckQueuedTasks(context.Background())
	u2bAsyncJoin(t, "drain tick 2 after full wiring", p2.loop.taskExecutor.wg.Wait)

	runs := p2.runs(t, queued.ID)
	require.Len(t, runs, 1, "W3: exactly one run, dispatched after the plane is fully wired")
	p2.requireMainChild(t, queued.ID, runs[0].SessionID) // classification + real launcher parentage + task origin
	require.Equal(t, task.StatusDone, runs[0].Status, "W3: the drained task executes to a real outcome")
	require.Equal(t, u2bAsyncEvidence, runs[0].Result)
	tk, err := p2.loop.taskStore.Get(queued.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, tk.Status, "W3: the task is not left stuck in queued")

	// Tick 3 adds nothing.
	p2.loop.taskExecutor.CheckQueuedTasks(context.Background())
	u2bAsyncJoin(t, "drain tick 3", p2.loop.taskExecutor.wg.Wait)
	require.Equal(t, runs, p2.runs(t, queued.ID), "W3: the drained task is never dispatched twice")
}

// TestU2bBootReplay_HeldFire_SchedulerStoppedThenPlaneWired_KeptForNextBoot
// is addendum-12 C1b: the same executor holds an owed fire, the SCHEDULER
// stops while the fire is still held, and only THEN is the plane wired — so
// the released callback (pkg/agent/task_trigger_boot_hold.go::
// runHeldBootOwedFire) takes its stop-first branch. C1 never wired that
// executor, so the branch never executed there and a mutant that removes
// the branch's early return survives it. Oracles: the released fire does
// not run on this boot; the job stays as durable debt (kept, enabled, with
// its Running flag — the next boot's replay marker); the next physical boot
// replays it exactly once, with a persisted classification, as a real MAIN
// child. The row depends on the stop-keeps-job early return at the top of
// runHeldBootOwedFire ("The job keeps its Running flag: the next physical
// boot replays it.").
func TestU2bBootReplay_HeldFire_SchedulerStoppedThenPlaneWired_KeptForNextBoot(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	fire := p1.fireStarted(t, worker)
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)

	// Boot 2: unwired, the owed fire is held (its job kept with Running).
	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	p2.physicalBoot(t)
	p2.scheduler.RunDueJobs(p2.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p2.scheduler.WaitForLane)
	u2bAsyncJoin(t, "boot-2 execution, whatever the boot decided", p2.loop.taskExecutor.wg.Wait)
	require.NotEmpty(t, p2.storedJobs(t, fire.taskID), "instrument: the fire is held")
	require.Len(t, p2.runs(t, fire.taskID), 1,
		"instrument: only boot 1's abandoned run exists — the held fire has not run")

	// The scheduler stops while the fire is still held; the executor is NOT
	// stopped and not yet wired.
	p2.scheduler.Stop()
	// THEN the plane is wired late, in production order: the release runs the
	// held callback now, and it must take the stop-first branch.
	wireLikeTheGatewayLatesWiresIt(t, p2)
	u2bAsyncJoin(t, "release after wiring on a stopped scheduler", p2.loop.taskExecutor.wg.Wait)
	require.Len(t, p2.runs(t, fire.taskID), 1,
		"C1b: a held fire released after the scheduler stopped must not run on this boot — "+
			"only boot 1's abandoned run may exist")
	kept := p2.storedJobs(t, fire.taskID)
	require.Len(t, kept, 1, "C1b: the stop-first branch keeps the held fire's job as durable debt")
	require.True(t, kept[0].Enabled, "C1b: the kept job stays enabled for the next boot")
	require.True(t, kept[0].State.Running, "C1b: the job keeps its Running flag — the next boot's replay marker")
	p2.stop()

	// Boot 3, fully wired steady state: the debt replays exactly once.
	p3 := bootU2bLauncherProcess(t, home, clk)
	runs := p3.runs(t, fire.taskID)
	original, replay := requireU2bAsyncReplay(t, fire, runs)
	require.Equal(t, "abandoned: gateway restarted mid-execution", original.Result)
	rec, err := p3.loop.GetSessionLifecycleStore().Load(replay.SessionID)
	require.NoError(t, err, "C1b: the next boot's replay must carry a persisted runtime classification")
	require.NotNil(t, rec)
	p3.requireMainChild(t, fire.taskID, replay.SessionID)
	require.Empty(t, p3.storedJobs(t, fire.taskID), "the replayed fire's job is settled")
	p3.runDueAndJoin(t)
	require.Equal(t, runs, p3.runs(t, fire.taskID), "another tick adds no replay")
	p3.stop()
	p4 := bootU2bLauncherProcess(t, home, clk)
	require.Equal(t, runs, p4.runs(t, fire.taskID), "a further boot adds nothing")
	after, err := p4.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err, "C1b: the task is not deleted")
	require.Equal(t, task.StatusDone, after.Status)
	p4.stop()
}

// TestU2bBootReplay_HeldFire_TriggerEditedWhileHeld_Superseded_OldFireNeverRuns
// is addendum-12 C2b: the held owed fire's trigger is EDITED while held — a
// real trigger change through the normal update path (task store Update with
// a new once AtMs, then NotifyTaskUpserted, exactly as the fixture arms the
// original) — and only then is the plane wired, releasing the held callback
// into pkg/agent/task_trigger_boot_hold.go::runHeldBootOwedFire's superseded
// branch. C2 caught a deletion later with the task-not-found guard, so a
// mutant that removes the superseded check survived it; the edit path is the
// only state that reaches that branch, because the edit's registration
// (pkg/agent/task_trigger.go::replaceJobLocked) deletes the bootOwedFire
// marker the branch tests. Oracles: the old held fire never runs; the old
// job is gone and exactly the edited trigger's job remains; the edited
// trigger then fires on its own new time, exactly once, as a real MAIN child
// with a persisted classification.
func TestU2bBootReplay_HeldFire_TriggerEditedWhileHeld_Superseded_OldFireNeverRuns(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	fire := p1.fireStarted(t, worker)
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)

	// Boot 2: unwired, the owed fire is held.
	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	p2.physicalBoot(t)
	p2.scheduler.RunDueJobs(p2.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p2.scheduler.WaitForLane)
	u2bAsyncJoin(t, "boot-2 execution, whatever the boot decided", p2.loop.taskExecutor.wg.Wait)
	require.NotEmpty(t, p2.storedJobs(t, fire.taskID), "instrument: the fire is held")
	require.Len(t, p2.runs(t, fire.taskID), 1,
		"instrument: only boot 1's abandoned run exists — the held fire has not run")

	// EDIT the trigger while held — a real trigger change through the normal
	// update path. The task is terminal here (boot 1's crash reconciled it to
	// failed), and OnTaskUpserted's terminal branch de-arms a terminal
	// once-task on a trigger-only edit, so the trigger change rides the same
	// normal-path update that re-queues the task — exactly what a person does
	// when re-scheduling a failed task: set it back to queued with a new once
	// time.
	next := task.StatusNext
	newAt := p2.clock.Now().Add(time.Minute).UnixMilli()
	newTrigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &newAt}}
	edited, err := p2.loop.taskStore.Update(fire.taskID, task.Patch{Status: &next, Trigger: &newTrigger})
	require.NoError(t, err)
	require.Equal(t, task.StatusNext, edited.Status, "instrument: the edit re-queues the failed task")
	p2.loop.NotifyTaskUpserted(edited)
	editedJobs := p2.storedJobs(t, fire.taskID)
	require.Len(t, editedJobs, 1, "C2b: the edit leaves exactly the edited trigger's job")
	require.NotEqual(t, fire.jobID, editedJobs[0].ID, "C2b: the new job replaces the held one, not the same job")

	// Wire the plane: the released callback must take the superseded branch
	// and remove the old job — never run the old fire.
	wireLikeTheGatewayLatesWiresIt(t, p2)
	u2bAsyncJoin(t, "release after the trigger edit", p2.loop.taskExecutor.wg.Wait)
	require.Len(t, p2.runs(t, fire.taskID), 1,
		"C2b: the superseded held fire must never run — the edit wins; "+
			"only boot 1's abandoned run may exist")

	// The edited trigger fires on its own new schedule, exactly once.
	p2.clock.Advance(2 * time.Minute)
	p2.runDueAndJoin(t)
	runs := p2.runs(t, fire.taskID)
	require.Len(t, runs, 2, "C2b: the edited trigger fires exactly once, at its own new time")
	// Run ordering is not guaranteed — identify the new fire's run by its
	// identity, exactly as requireU2bAsyncReplay does.
	var newFire task.TaskRun
	for _, r := range runs {
		if r.RunID != fire.run.RunID {
			newFire = r
			break
		}
	}
	require.NotEqual(t, fire.run.RunID, newFire.RunID, "C2b: the edited trigger's fire is a distinct run")
	p2.requireMainChild(t, fire.taskID, newFire.SessionID)
	rec, err := p2.loop.GetSessionLifecycleStore().Load(newFire.SessionID)
	require.NoError(t, err, "C2b: the edited trigger's run carries a persisted runtime classification")
	require.NotNil(t, rec)
	require.Equal(t, task.StatusDone, newFire.Status, "C2b: the edited trigger's fire executes to a real outcome")
	require.Equal(t, u2bAsyncEvidence, newFire.Result, "C2b: the edited trigger's fire judged its real result")
	require.Empty(t, p2.storedJobs(t, fire.taskID), "C2b: the fired once job is settled")
	p2.runDueAndJoin(t)
	require.Equal(t, runs, p2.runs(t, fire.taskID), "C2b: at most one run per the trigger's own time")
	p2.stop()
}

// TestU2bBootReplay_OrdinaryScheduledFire_UnwiredWindow_WaitsForPlane_ThenRunsOnce
// is addendum-12 W4, found by the developer while writing the docs and
// decided by this row: an ORDINARY scheduled fire — not an owed boot replay,
// not a queued Next task — whose time comes due after the scheduler starts
// but before the session plane is wired. Only holdBootOwedFire and
// CheckQueuedTasks wait for the plane; pkg/agent/task_trigger.go::RunScheduled
// does not, so a due ordinary fire dispatched in the window ran isolated and
// unclassified. Oracle: the fire due in the unwired window creates no run
// (and can therefore never record the wired enforcement's refusal,
// "session has no persisted runtime classification", as its outcome); after
// the plane is wired in production order, exactly one run exists — a real
// MAIN child (requireMainChild evidence, which also proves the persisted
// classification) whose result is the judged evidence, never the refusal
// text — executing to Done; a further tick adds nothing. The fire control is
// the scheduler's own fake clock and RunDueJobs, the same control the
// TestTaskTrigger / TestTriggerScheduler tests use — no bypass of
// RunScheduled. Expected RED on 931d7bc6d: the window tick dispatches (the
// no-run oracle fails), which is the gap the developer's RunScheduled fix
// closes.
func TestU2bBootReplay_OrdinaryScheduledFire_UnwiredWindow_WaitsForPlane_ThenRunsOnce(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	expectSessionPlaneLikeTheGateway(t, p2) // the gateway marks the plane expected before the schedulers start
	p2.physicalBoot(t)

	// A fresh one-time task, armed through the normal update path, its fire
	// still in the future at boot — an ordinary scheduled fire, nothing owed.
	queued := newCompletionContractTask(t, p2.loop, "native-agent", "U2b ordinary scheduled fire W4")
	fireAt := clk.Now().Add(time.Minute).UnixMilli()
	trigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &fireAt}}
	stored, err := p2.loop.taskStore.Update(queued.ID, task.Patch{Trigger: &trigger})
	require.NoError(t, err)
	p2.loop.NotifyTaskUpserted(stored)
	require.Len(t, p2.storedJobs(t, queued.ID), 1, "instrument: the ordinary once job is armed")
	require.Nil(t, GetTaskExecutor(p2.loop).getLifecycleStore(), "state: no store is wired in the window")
	require.Nil(t, GetTaskExecutor(p2.loop).launcher, "state: no launcher is wired in the window")
	require.Nil(t, GetTaskExecutor(p2.loop).mainResolver, "state: no main resolver is wired in the window")

	// The fire comes due IN the unwired window, through the real
	// RunScheduled path.
	clk.Advance(2 * time.Minute)
	p2.runDueAndJoin(t)
	require.Empty(t, p2.runs(t, queued.ID),
		"W4: an ordinary scheduled fire due in the unwired window must wait for the session "+
			"plane like the drain and the owed fires — dispatched here it ran isolated and "+
			"without its runtime classification")

	// Wire in production order: stores, executor store + launcher, resolver
	// LAST. Then a tick: exactly one run, judged, classified, a MAIN child.
	wireLikeTheGatewayLatesWiresIt(t, p2)
	p2.runDueAndJoin(t)
	runs := p2.runs(t, queued.ID)
	require.Len(t, runs, 1, "W4: exactly one run, after the plane is fully wired")
	p2.requireMainChild(t, queued.ID, runs[0].SessionID) // classification + real launcher parentage + task origin
	rec, err := p2.loop.GetSessionLifecycleStore().Load(runs[0].SessionID)
	require.NoError(t, err, "W4: the fire's run carries a persisted runtime classification")
	require.NotNil(t, rec)
	require.Equal(t, task.StatusDone, runs[0].Status, "W4: the fire executes to a real outcome")
	require.Equal(t, u2bAsyncEvidence, runs[0].Result,
		"W4: the outcome is the judged evidence — never the wired enforcement's refusal text")

	// Another tick adds nothing.
	p2.runDueAndJoin(t)
	require.Equal(t, runs, p2.runs(t, queued.ID), "W4: the fire is never dispatched twice")
	p2.stop()
}

// auditRowNamesTaskAtWarn reports whether rows contain an audit entry whose
// severity is WARN, whose event name is a REGISTERED audit event
// (audit.IsValidEventName — the row deliberately does not pin a new event's
// name), and whose payload names the given task id anywhere in its JSON.
func auditRowNamesTaskAtWarn(rows []map[string]any, taskID string) bool {
	for _, r := range rows {
		if sev, _ := r["severity"].(string); sev != "WARN" {
			continue
		}
		evt, _ := r["event"].(string)
		if evt == "" || !audit.IsValidEventName(audit.EventName(evt)) {
			continue
		}
		if strings.Contains(rsauditJSON(r), taskID) {
			return true
		}
	}
	return false
}

func rsauditJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

// TestU2bBootReplay_HeldFire_TransientReadError_JobKept_Audited_ReplayedAtNextBoot
// is addendum-12 W5 case (a), the chief's exact oracle. The defect
// (pkg/agent/task_trigger_boot_hold.go::runHeldBootOwedFire's error branch):
// when runScheduledFire returns any error other than cron.ErrRunHandedOff —
// here store.Get failing with a non-not-found decode error before any run is
// created — it logs the error and ALWAYS calls RemoveJob, so the owed replay
// is lost silently: no run, no failed TaskRun, no job for the next boot, and
// the engine cannot record it because the callback already returned
// ErrRunHandedOff. Required behaviour: a transient pre-start error (the task
// and its agent still exist) KEEPS the owed job, creates no run, writes a
// WARN audit entry naming the task, and the next boot — the task readable
// again — replays it exactly once with the persisted classification. The
// read failure is real, not a bypass: the task's own store file is corrupted
// (a genuine decode error through the concrete *task.Store), then restored.
func TestU2bBootReplay_HeldFire_TransientReadError_JobKept_Audited_ReplayedAtNextBoot(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	fire := p1.fireStarted(t, worker)
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)

	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	p2.physicalBoot(t)
	p2.scheduler.RunDueJobs(p2.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p2.scheduler.WaitForLane)
	u2bAsyncJoin(t, "boot-2 execution, whatever the boot decided", p2.loop.taskExecutor.wg.Wait)
	require.NotEmpty(t, p2.storedJobs(t, fire.taskID), "instrument: the fire is held")
	require.Len(t, p2.runs(t, fire.taskID), 1,
		"instrument: only boot 1's abandoned run exists — the held fire has not run")

	readAudit := swapAuditLogger(t, p2.loop)

	// The transient read failure, through the store's real seam: the task's
	// own file corrupts so store.Get fails with a non-not-found decode error.
	taskFile := filepath.Join(home, "tasks", fire.taskID+".json")
	originalFile, err := os.ReadFile(taskFile)
	require.NoError(t, err, "instrument: the task's store file exists")
	require.NoError(t, os.WriteFile(taskFile, []byte("{ corrupted for W5 case (a)"), 0o644))

	// Wire the plane: the released held fire hits the unreadable task.
	wireLikeTheGatewayLatesWiresIt(t, p2)
	u2bAsyncJoin(t, "release against an unreadable task", p2.loop.taskExecutor.wg.Wait)

	// The task is readable again from here on.
	require.NoError(t, os.WriteFile(taskFile, originalFile, 0o644), "restore: the transient error clears")
	kept := p2.storedJobs(t, fire.taskID)
	require.Len(t, kept, 1,
		"W5(a): a transient read error must not consume the owed fire — the job stays as durable debt")
	require.True(t, kept[0].Enabled, "W5(a): the kept job stays enabled for the next boot")
	require.True(t, kept[0].State.Running, "W5(a): the job keeps its Running flag — the next boot's replay marker")
	require.Len(t, p2.runs(t, fire.taskID), 1, "W5(a): no run is created for an unreadable task")
	require.True(t, auditRowNamesTaskAtWarn(readAudit(), fire.taskID),
		"W5(a): a WARN audit entry naming the task must record the failed fire attempt")

	p2.stop()
	// The next boot, the task readable again: the debt replays exactly once.
	p3 := bootU2bLauncherProcess(t, home, clk)
	runs := p3.runs(t, fire.taskID)
	originalRun, replay := requireU2bAsyncReplay(t, fire, runs)
	require.Equal(t, "abandoned: gateway restarted mid-execution", originalRun.Result)
	rec, err := p3.loop.GetSessionLifecycleStore().Load(replay.SessionID)
	require.NoError(t, err, "W5(a): the next boot's replay carries a persisted runtime classification")
	require.NotNil(t, rec)
	p3.requireMainChild(t, fire.taskID, replay.SessionID)
	require.Empty(t, p3.storedJobs(t, fire.taskID), "the replayed fire's job is settled")
	p3.runDueAndJoin(t)
	require.Equal(t, runs, p3.runs(t, fire.taskID), "another tick adds no replay")
	p3.stop()
}

// TestU2bBootReplay_HeldFire_TaskDeletedBeforeRelease_JobRemoved_Audited_NoReplay
// is addendum-12 W5 case (b1): the task is deleted (the real production path:
// store Delete plus NotifyTaskDeleted, as C2 does) before the held fire is
// released. Required behaviour: the job is removed, a WARN audit entry names
// the deleted task's id, and no replay happens at the next boot.
func TestU2bBootReplay_HeldFire_TaskDeletedBeforeRelease_JobRemoved_Audited_NoReplay(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	fire := p1.fireStarted(t, worker)
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)

	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	p2.physicalBoot(t)
	p2.scheduler.RunDueJobs(p2.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p2.scheduler.WaitForLane)
	u2bAsyncJoin(t, "boot-2 execution, whatever the boot decided", p2.loop.taskExecutor.wg.Wait)
	require.NotEmpty(t, p2.storedJobs(t, fire.taskID), "instrument: the fire is held")
	require.Len(t, p2.runs(t, fire.taskID), 1,
		"instrument: only boot 1's abandoned run exists — the held fire has not run")

	readAudit := swapAuditLogger(t, p2.loop)

	// The task is deleted before the held fire is released.
	_, err := p2.loop.taskStore.Delete(fire.taskID)
	require.NoError(t, err)
	p2.loop.NotifyTaskDeleted(fire.taskID)
	require.Empty(t, p2.storedJobs(t, fire.taskID), "instrument: the deletion removes the job")

	// Wire the plane: the release must handle the deleted task audibly.
	wireLikeTheGatewayLatesWiresIt(t, p2)
	u2bAsyncJoin(t, "release against a deleted task", p2.loop.taskExecutor.wg.Wait)

	require.Len(t, p2.runs(t, fire.taskID), 1, "W5(b1): no run exists for a deleted task")
	require.Empty(t, p2.storedJobs(t, fire.taskID), "W5(b1): the job is removed")
	require.True(t, auditRowNamesTaskAtWarn(readAudit(), fire.taskID),
		"W5(b1): a WARN audit entry naming the deleted task's id must record the dropped held fire")

	p2.stop()
	p3 := bootU2bLauncherProcess(t, home, clk)
	p3.runDueAndJoin(t)
	require.Len(t, p3.runs(t, fire.taskID), 1, "W5(b1): no replay at the next boot")
	p3.stop()
}

// TestU2bBootReplay_HeldFire_AgentGone_FailedRunRecorded_JobRemoved is
// addendum-12 W5 case (b2): the task exists but its AGENT is gone when the
// held fire is released. Required behaviour: a FAILED run is recorded in the
// task's run history (visible to the user — a task-level failed status with
// no run record is exactly the silent loss this row forbids), and then the
// job is removed; no replay at the next boot. The disappearance is modeled as
// a store-level assignee change to an agent the registry does not know,
// deliberately WITHOUT a trigger notification: NotifyTaskUpserted would
// re-arm the trigger job and supersede the held fire — a different case —
// while the scheduler's job and the owed marker must stay untouched here.
func TestU2bBootReplay_HeldFire_AgentGone_FailedRunRecorded_JobRemoved(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	fire := p1.fireStarted(t, worker)
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)

	p2 := newU2bUnwiredLauncherProcess(t, home, clk, completedWorkerForU2b())
	p2.physicalBoot(t)
	p2.scheduler.RunDueJobs(p2.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p2.scheduler.WaitForLane)
	u2bAsyncJoin(t, "boot-2 execution, whatever the boot decided", p2.loop.taskExecutor.wg.Wait)
	require.NotEmpty(t, p2.storedJobs(t, fire.taskID), "instrument: the fire is held")
	require.Len(t, p2.runs(t, fire.taskID), 1,
		"instrument: only boot 1's abandoned run exists — the held fire has not run")

	// The assignee stops existing while the fire is held.
	ghost := "retired-agent"
	_, err := p2.loop.taskStore.Update(fire.taskID, task.Patch{AgentID: &ghost})
	require.NoError(t, err)

	// Wire the plane: the release must record the failure, not lose it.
	wireLikeTheGatewayLatesWiresIt(t, p2)
	u2bAsyncJoin(t, "release against a vanished agent", p2.loop.taskExecutor.wg.Wait)

	runs := p2.runs(t, fire.taskID)
	require.Len(t, runs, 2,
		"W5(b2): the released fire's failure must be RECORDED as a failed run in the task's "+
			"history (the abandoned boot-1 run plus the new failure); a failed task without a "+
			"run record is the silent loss this row forbids")
	var failedRun *task.TaskRun
	for i := range runs {
		if runs[i].RunID != fire.run.RunID && runs[i].Status == task.StatusFailed {
			failedRun = &runs[i]
			break
		}
	}
	require.NotNil(t, failedRun, "W5(b2): the recorded failure is a failed run distinct from boot 1's")
	require.Empty(t, p2.storedJobs(t, fire.taskID),
		"W5(b2): the job is removed after the failure is recorded")

	p2.stop()
	p3 := bootU2bLauncherProcess(t, home, clk)
	p3.runDueAndJoin(t)
	require.Len(t, p3.runs(t, fire.taskID), 2, "W5(b2): no replay at the next boot")
	p3.stop()
}
