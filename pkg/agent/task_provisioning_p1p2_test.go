// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: #1081 provisioning RED dispatch, founder Option A: retire successful
// session-less execution. P1 must return the provisioning error, durably fail
// the claimed task, release capacity, and never launch work or publish a start.
// P2's launcher-less missing-store branch must return an error + exactly empty
// session ID, release its reservation/capacity, and never launch or publish a
// start. Missing-store error wording is deliberately not invented here.
//
// Plan: missing UnifiedStore / real NewSession filesystem failure x native /
// external workers for P1; missing store x both workers for P2. Two successful
// provisioning controls prove the instruments see real launches. These are
// checklist tasks to avoid unrelated Judge/goal completion dependencies; the
// provisioning/claim/launch functions themselves remain real. No goroutine
// hook, fake task store, fake GetAgentStore, or mock inside the executor.
//
// GREEN and mutation checks belong to a DIFFERENT CHECK instance. Mutants to
// kill: swallowed provisioning errors, missing failed-task write, leaked permit
// or reservation, false in_progress emission, escaped run goroutine, and lost
// filesystem cause. P3, launcher-backed starts, metadata/goal/transcript write
// policies and P2's already-aborting NewSession-error branch are out of scope.
const provisioningProbeToolName = "provisioning_side_effect"

// Only process edges are doubled. Holding the edge until return-time snapshots
// prevents a mistakenly launched task from finishing early and masking a held
// slot/permit. No timeout or sleep is used to prove absence of execution.
type provisioningProvider struct {
	*testutil.ScenarioProvider
	gate  <-chan struct{}
	calls atomic.Int32
}

func (p *provisioningProvider) Chat(
	ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition,
	model string, options map[string]any,
) (*providers.LLMResponse, error) {
	p.calls.Add(1) // Count EVERY invocation, including exhausted scripts.
	select {
	case <-p.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return p.ScenarioProvider.Chat(ctx, messages, defs, model, options)
}

type provisioningProbeTool struct {
	tools.BaseTool
	calls atomic.Int32
}

func (*provisioningProbeTool) Name() string { return provisioningProbeToolName }
func (*provisioningProbeTool) Description() string {
	return "Count an observable process-edge side effect for the provisioning regression."
}
func (*provisioningProbeTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*provisioningProbeTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (p *provisioningProbeTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	p.calls.Add(1)
	return &tools.ToolResult{ForLLM: "provisioning side effect observed"}
}

type provisioningDriver struct {
	*runner.FakeRunner
	gate  <-chan struct{}
	calls atomic.Int32
}

func (d *provisioningDriver) Run(ctx context.Context, opts runner.RunOptions) (<-chan runner.RunEvent, error) {
	d.calls.Add(1)
	select {
	case <-d.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return d.FakeRunner.Run(ctx, opts)
}

type provisioningEvents struct {
	mu       sync.Mutex
	statuses []TaskStatusChangedPayload
	runs     []TaskRunStatusPayload
}

func (p *provisioningEvents) record(evt Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch evt.Kind {
	case EventKindTaskStatusChanged:
		if payload, ok := evt.Payload.(TaskStatusChangedPayload); ok {
			p.statuses = append(p.statuses, payload)
		}
	case EventKindTaskRunStatus:
		if payload, ok := evt.Payload.(TaskRunStatusPayload); ok {
			p.runs = append(p.runs, payload)
		}
	}
}

func (p *provisioningEvents) startCounts(taskID string) (statuses, runs int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.statuses {
		if s.TaskID == taskID && s.Status == string(task.StatusInProgress) {
			statuses++
		}
	}
	for _, r := range p.runs {
		if r.TaskID == taskID && r.Status == string(task.StatusInProgress) {
			runs++
		}
	}
	return statuses, runs
}

type provisioningFixture struct {
	loop         *AgentLoop
	executor     *TaskExecutor
	worker       *AgentInstance
	task         *task.Task
	provider     *provisioningProvider
	tool         *provisioningProbeTool
	driver       *provisioningDriver
	factoryCalls atomic.Int32
	events       *provisioningEvents
	unblock      func()
	readLog      func() string
	workDir      string
}

func newProvisioningFixture(t *testing.T, external, manual bool) *provisioningFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	gate := make(chan struct{})
	var gateOnce sync.Once
	f := &provisioningFixture{
		unblock: func() { gateOnce.Do(func() { close(gate) }) },
		events:  &provisioningEvents{},
		tool:    &provisioningProbeTool{},
		readLog: captureLogFile(t, logger.INFO),
		// ADR-046, Decision 2: a task turn works in its bound workspace,
		// never the agent home. The membership fixture sets no override.
		workDir: filepath.Join(home, "workspaces", testHarnessWorkspaceMembershipID, "work"),
	}
	f.provider = &provisioningProvider{
		ScenarioProvider: testutil.NewScenario().
			WithToolCall(provisioningProbeToolName, `{}`).
			WithText("TASK_STATUS: failure\n[goal:reason] provisioning instrument completed"),
		gate: gate,
	}
	workerCfg := config.AgentConfig{
		ID: "provisioning-worker", Name: "Provisioning Worker", Type: config.AgentTypeWorker,
		Home: filepath.Join(home, "worker"),
	}
	if external {
		workerCfg.Subagents = &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
		}
	}
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{
			Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "test-model"},
			MaxTokens: 4096, MaxToolIterations: 4,
		},
		List: []config.AgentConfig{workerCfg},
	}}
	// Start with the actual complete shipped ceiling, not a sparse fixture
	// that emits unrelated missing-policy errors. Add only the edge probe.
	cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	cfg.Sandbox.ToolPolicies[provisioningProbeToolName] = "allow"
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	f.loop = mustNewAgentLoop(t, cfg, msgBus, f.provider)
	t.Cleanup(f.loop.Close)
	t.Cleanup(f.unblock) // Unblock the edges before loop.Close on any fatal assertion.
	var ok bool
	f.worker, ok = f.loop.GetRegistry().GetAgent(workerCfg.ID)
	require.True(t, ok, "fixture worker must be really registered")
	f.worker.Tools.Register(f.tool)
	f.loop.SetEventSyncTap(f.events.record)
	f.executor = &TaskExecutor{
		agentLoop: f.loop, store: f.loop.taskStore,
		running: make(map[string]*taskSlot), dispatchSema: newDispatchSemaphore(1),
	}
	f.loop.taskExecutor = f.executor
	require.Nil(t, f.executor.launcher, "P2 must exercise the launcher-less owner")
	require.Nil(t, f.executor.goroutineCtxHook, "never replace the real run bodies with the test hook")
	assertProvisioningSemaphoreControl(t, f.executor)

	f.driver = &provisioningDriver{FakeRunner: runner.NewFakeRunner(), gate: gate}
	f.driver.InjectEvent(runner.RunEvent{
		Kind: runner.EventKindOutput,
		Output: &runner.OutputEvent{
			Text: "TASK_STATUS: failure\n[goal:reason] provisioning instrument completed",
		},
	})
	f.driver.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
	f.driver.Cancel() // Buffered finite output remains readable; no real process is spawned.
	previousFactory := newExternalDriver
	newExternalDriver = func(cli string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		f.factoryCalls.Add(1)
		assert.Equal(t, "claude-code", cli, "external instrument must see the configured driver")
		return f.driver, nil
	}
	t.Cleanup(func() { newExternalDriver = previousFactory })

	maxAttempts := 1 // Bound only the edge-double teardown if buggy dispatch escapes.
	f.task = &task.Task{
		Title: "provisioning must precede work", Prompt: "perform the provisioning side-effect probe",
		Action: task.ActionLLM, AgentID: workerCfg.ID, WorkspaceID: testHarnessWorkspaceMembershipID,
		Status: task.StatusNext, Scratchpad: true, MaxAttempts: &maxAttempts,
	}
	require.NoError(t, f.loop.taskStore.Create(f.task), "create the real writable task record")
	if manual {
		inProgress := task.StatusInProgress
		var err error
		f.task, err = f.loop.taskStore.Update(f.task.ID, task.Patch{Status: &inProgress})
		require.NoError(t, err, "manual start requires a real persisted in-progress task")
	}
	return f
}

func assertProvisioningSemaphoreControl(t *testing.T, te *TaskExecutor) {
	t.Helper()
	acquired, release := te.TryAcquireDispatchSema()
	require.True(t, acquired, "capacity-one instrument must admit its first reservation")
	require.Equal(t, 1, te.dispatchSema.InFlight(), "a held permit must be observable")
	second, secondRelease := te.TryAcquireDispatchSema()
	if second {
		secondRelease()
	}
	assert.False(t, second, "capacity-one instrument must detect a leaked held permit")
	release()
	reacquired, releaseAgain := te.TryAcquireDispatchSema()
	require.True(t, reacquired, "the instrument must observe capacity restored after release")
	releaseAgain()
	require.Equal(t, 0, te.dispatchSema.InFlight(), "semaphore control must leave no held permit")
}

func provisioningTaskOnDisk(t *testing.T, f *provisioningFixture) *task.Task {
	t.Helper()
	// Read the actual task JSON, independently of the executor's local pointer.
	path := filepath.Join(f.loop.taskStore.Dir(), f.task.ID+".json")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "reload the actual persisted task at %s", path)
	var persisted task.Task
	require.NoError(t, json.Unmarshal(data, &persisted), "task JSON must remain readable")
	require.Equal(t, f.task.ID, persisted.ID, "disk reader must inspect the dispatched task")
	return &persisted
}

// installProvisioningMissingStore is DELETED (session-core DEL-10): with ONE
// shared store there is no longer an agent that "lacks a UnifiedStore", so the
// context-only SessionManager refusal scenario cannot be built (architect
// DEL-10 table). The surviving refusal proof is the real NewSession write fault.

type provisioningWriteFault struct {
	base  string
	op    string
	cause error
}

func installProvisioningWriteFault(t *testing.T, f *provisioningFixture) provisioningWriteFault {
	t.Helper()
	store := f.loop.GetSessionStore()
	require.NotNil(t, store, "write-fault fixture must use the real NewSession implementation")
	base := store.BaseDir()
	backup := base + ".provisioning-backup"
	require.NoError(t, os.Rename(base, backup), "preserve the real session directory")
	t.Cleanup(func() {
		assert.NoError(t, os.Remove(base), "remove the regular-file obstruction")
		assert.NoError(t, os.Rename(backup, base), "restore the directory before loop teardown")
	})
	require.NoError(t, os.WriteFile(base, []byte("not a directory"), 0o600), "obstruct the session base with a regular file")
	meta, probeErr := store.NewSession(session.SessionTypeTask, "system", f.task.AgentID)
	require.Nil(t, meta, "the real filesystem probe must not create session metadata")
	var cause *os.PathError
	require.ErrorAs(t, probeErr, &cause, "the instrument must expose a genuine OS PathError")
	require.Equal(t, "mkdir", cause.Op, "the obstruction must fail session-directory creation, not another setup step")
	require.True(t, cause.Path == base || strings.HasPrefix(cause.Path, base+string(os.PathSeparator)),
		"the creation fault must originate at the obstructed session base: %s", cause.Path)
	return provisioningWriteFault{base: base, op: cause.Op, cause: cause.Err}
}

func assertProvisioningWriteCause(t *testing.T, got error, fault provisioningWriteFault, owner string) {
	t.Helper()
	var pathErr *os.PathError
	if assert.ErrorAs(t, got, &pathErr, "%s must preserve the original NewSession filesystem error", owner) {
		assert.Equal(t, fault.op, pathErr.Op, "%s must preserve the filesystem operation", owner)
		assert.True(t, pathErr.Path == fault.base || strings.HasPrefix(pathErr.Path, fault.base+string(os.PathSeparator)),
			"%s must preserve the obstructed storage path, got %s", owner, pathErr.Path)
		assert.True(t, errors.Is(got, fault.cause), "%s must preserve the underlying OS cause %v", owner, fault.cause)
	}
}

func assertProvisioningReservationsReleased(t *testing.T, f *provisioningFixture) {
	t.Helper()
	// Snapshot BEFORE releasing the process-edge gate or draining/cancelling.
	// Cleanup must never be what makes the failure-path release look correct.
	f.executor.mu.Lock()
	slots := len(f.executor.running)
	_, holdsTask := f.executor.running[f.task.ID]
	f.executor.mu.Unlock()
	assert.False(t, holdsTask, "provisioning refusal must not retain this task's reserved/live slot")
	assert.Equal(t, 0, slots, "provisioning refusal must leave the slot map empty before returning")
	assert.Equal(t, 0, f.executor.dispatchSema.InFlight(), "provisioning refusal must release its capacity before returning")
	acquired, release := f.executor.TryAcquireDispatchSema()
	assert.True(t, acquired, "capacity one must be genuinely reacquirable at the error return, before teardown")
	if acquired {
		release()
	}
}

func waitProvisioningDispatchDone(t *testing.T, f *provisioningFixture) {
	t.Helper()
	f.unblock()
	done := make(chan struct{})
	go func() {
		f.executor.wg.Wait()
		close(done)
	}()
	watchdog := time.NewTimer(30 * time.Second) // Hang guard only; not an absence oracle.
	defer watchdog.Stop()
	select {
	case <-done:
	case <-watchdog.C:
		t.Fatal("provisioning instrument could not join actual dispatch work; zero-work assertions are unproven")
	}
}

func provisioningStartedLogs(t *testing.T, f *provisioningFixture) []string {
	t.Helper()
	var starts []string
	scanner := bufio.NewScanner(strings.NewReader(f.readLog()))
	for scanner.Scan() {
		var record map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &record), "structured log instrument must parse every record")
		if record["component"] != "task_executor" || record["task_id"] != f.task.ID {
			continue
		}
		msg, ok := record["message"].(string)
		require.True(t, ok, "task_executor record for task %s must carry a string message, got %T",
			f.task.ID, record["message"])
		if msg == "runTask started" || msg == "runTaskFromInProgress started" {
			starts = append(starts, msg)
		}
	}
	require.NoError(t, scanner.Err(), "log reading errors cannot be mistaken for an empty launch log")
	return starts
}

func assertProvisioningNoWork(t *testing.T, f *provisioningFixture) {
	t.Helper()
	// wg completion, not a sleep or terminal status poll, closes the window in
	// which mistakenly launched work could still reach a process edge.
	waitProvisioningDispatchDone(t, f)
	statuses, runStarts := f.events.startCounts(f.task.ID)
	runs, err := f.loop.taskStore.ListRuns(f.task.ID)
	require.NoError(t, err, "read durable run history, never treat a read error as zero runs")
	starts := provisioningStartedLogs(t, f)
	assert.Equal(t, int32(0), f.provider.calls.Load(), "a provisioning refusal must make zero native provider calls")
	assert.Equal(t, int32(0), f.tool.calls.Load(), "a provisioning refusal must execute zero tools")
	assert.Equal(t, int32(0), f.factoryCalls.Load(), "a provisioning refusal must not even create an external driver")
	assert.Equal(t, int32(0), f.driver.calls.Load(), "a provisioning refusal must spawn zero external runs")
	assert.Equal(t, 0, statuses, "a provisioning refusal must publish zero task in_progress transitions")
	assert.Equal(t, 0, runStarts, "a provisioning refusal must publish zero run in_progress transitions")
	assert.Len(t, runs, 0, "a provisioning refusal must open no persisted run: the execution goroutine must not start")
	assert.Len(t, starts, 0, "a provisioning refusal must log neither actual run body as started")
	t.Logf("no-work receipt: provider=%d tool=%d external_factory=%d external_run=%d task_starts=%d run_starts=%d disk_runs=%d started_logs=%v",
		f.provider.calls.Load(), f.tool.calls.Load(), f.factoryCalls.Load(), f.driver.calls.Load(),
		statuses, runStarts, len(runs), starts)
	joined := provisioningTaskOnDisk(t, f)
	t.Logf("joined task receipt: status=%s session=%q result=%q", joined.Status, joined.SessionID, joined.Result)
}

func checkProvisioningP1Refusal(t *testing.T, external bool) {
	t.Helper()
	f := newProvisioningFixture(t, external, false)
	defer f.unblock()
	// The only buildable refusal now is the real NewSession write fault
	// (session-core DEL-10: one shared store, no missing-store agent).
	fault := installProvisioningWriteFault(t, f)

	// Exercise the owner helper too, without replacing it. The failure must
	// never be presented as successful empty-session creation.
	helperID, helperErr := f.executor.createTaskSessionSync(f.task)
	assert.Equal(t, "", helperID, "P1 helper must return no session on a provisioning refusal")
	assert.Error(t, helperErr, "P1 helper must report the real NewSession failure, never empty success")
	assertProvisioningWriteCause(t, helperErr, fault, "P1 helper")

	dispatchErr := f.executor.ExecuteTask(context.Background(), f.task.ID, nil)
	assert.Error(t, dispatchErr, "P1 ExecuteTask must abort and return the provisioning error, never launch session-less work")
	assertProvisioningWriteCause(t, dispatchErr, fault, "P1 ExecuteTask")
	persisted := provisioningTaskOnDisk(t, f)
	assert.Equal(t, task.StatusFailed, persisted.Status, "P1 claimed task must be durably Failed when ExecuteTask returns")
	assert.Equal(t, "", persisted.SessionID, "P1 provisioning failure must not bind a synthetic session ID")
	assert.Equal(t, 0, persisted.AttemptCount, "P1 refused provisioning is not an executed task attempt")
	assertProvisioningReservationsReleased(t, f)
	t.Logf("P1 return receipt: helper_session=%q helper_error=%v dispatch_error=%v disk_status=%s disk_session=%q",
		helperID, helperErr, dispatchErr, persisted.Status, persisted.SessionID)
	assertProvisioningNoWork(t, f)
	assert.Equal(t, task.StatusFailed, provisioningTaskOnDisk(t, f).Status,
		"P1 persisted Failed must still hold after any wrongly launched work has been joined")
}

func checkProvisioningInstruments(t *testing.T, external, manual bool) {
	t.Helper()
	f := newProvisioningFixture(t, external, manual)
	defer f.unblock()
	var returnedID string
	var dispatchErr error
	if manual {
		returnedID, dispatchErr = f.executor.StartTaskNow(context.Background(), f.task.ID)
	} else {
		dispatchErr = f.executor.ExecuteTask(context.Background(), f.task.ID, nil)
	}
	require.NoError(t, dispatchErr, "healthy provisioning control must be admitted")
	persisted := provisioningTaskOnDisk(t, f)
	require.NotEmpty(t, persisted.SessionID, "healthy control must persist its real session before returning")
	assert.NotEqual(t, "task:"+f.task.ID, persisted.SessionID, "control must bind real metadata, not a fallback ID")
	if manual {
		assert.Equal(t, persisted.SessionID, returnedID, "manual control must return the persisted real session ID")
	}
	meta, err := f.loop.GetSessionStore().GetMeta(persisted.SessionID)
	require.NoError(t, err, "control must prove that persisted session metadata really exists")
	assert.Equal(t, session.SessionTypeTask, meta.Type, "control must provision task-session metadata")
	waitProvisioningDispatchDone(t, f)

	statuses, runStarts := f.events.startCounts(f.task.ID)
	assert.Equal(t, 1, statuses, "status tap must see exactly one actual task start")
	assert.Equal(t, 1, runStarts, "status tap must see exactly one actual run start")
	runs, err := f.loop.taskStore.ListRuns(f.task.ID)
	require.NoError(t, err, "control run-history reader must inspect real persistent records")
	require.Len(t, runs, 1, "control must prove the run-history instrument sees a real launch")
	assert.Equal(t, persisted.SessionID, runs[0].SessionID, "control run must reference the provisioned session")
	starts := provisioningStartedLogs(t, f)
	if external {
		assert.Equal(t, []string{"runTaskFromInProgress started"}, starts, "INFO log instrument must see the manual run body")
		assert.Equal(t, int32(1), f.factoryCalls.Load(), "external control must exercise the real driver factory boundary")
		assert.Equal(t, int32(1), f.driver.calls.Load(), "external control must exercise driver.Run")
		opts := f.driver.RecordedRunOpts()
		require.Len(t, opts, 1, "external control must record the actual process invocation")
		assert.Equal(t, f.workDir, opts[0].WorkDir, "external control must receive the task's bound workspace folder (ADR-046 Decision 2)")
		assert.Contains(t, opts[0].Input, f.task.Prompt, "external control must receive this task's actual prompt")
		assert.Equal(t, int32(0), f.provider.calls.Load(), "external control must not silently use native execution")
		assert.Equal(t, int32(0), f.tool.calls.Load(), "external control must not execute the native probe tool")
	} else {
		assert.Equal(t, []string{"runTask started"}, starts, "INFO log instrument must see the scheduled run body")
		assert.Equal(t, int32(2), f.provider.calls.Load(), "native control must consume tool-call then final-text responses")
		assert.Equal(t, int32(1), f.tool.calls.Load(), "native control must prove the allowed side-effect tool is reached")
		assert.Equal(t, int32(0), f.factoryCalls.Load(), "native control must not create an external driver")
		assert.Equal(t, int32(0), f.driver.calls.Load(), "native control must not spawn an external run")
	}
	assertProvisioningReservationsReleased(t, f)
	t.Logf("instrument control: provider=%d tool=%d external_factory=%d external_run=%d task_starts=%d run_starts=%d disk_runs=%d started_logs=%v",
		f.provider.calls.Load(), f.tool.calls.Load(), f.factoryCalls.Load(), f.driver.calls.Load(),
		statuses, runStarts, len(runs), starts)
}

func TestTaskProvisioningStopsBeforeWork(t *testing.T) {
	// Never parallel: the process-edge driver factory, environment and logging
	// are scoped/restored for each serial row. One root keeps local RED narrow.
	t.Run("control_native_provisioned_run_is_observable", func(t *testing.T) {
		checkProvisioningInstruments(t, false, false)
	})
	t.Run("control_external_manual_provisioned_run_is_observable", func(t *testing.T) {
		checkProvisioningInstruments(t, true, true)
	})
	for _, worker := range []struct {
		name     string
		external bool
	}{{"native", false}, {"external", true}} {
		t.Run("P1_write_failure_"+worker.name+"_preserves_cause_and_persists_failed_without_work", func(t *testing.T) {
			checkProvisioningP1Refusal(t, worker.external)
		})
	}
}
