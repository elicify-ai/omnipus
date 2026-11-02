//go:build linux || darwin

package gateway

// Oracle: t8-rebase-note-6b-replay.md (6b F1): a delivered one-time fire
// whose real task run started but never finished is replayed once at physical
// boot. This is deliberately not 18.30's never-started, missed occurrence.
// Each phase is a separate OS process. Only the external model is controlled;
// task creation/pairing, cron delivery, executor, run journal and gateway boot
// remain real. The replay is finished by a deliberate provider error, not by
// fabricating a successful goal verdict. A finished failed run must not replay
// on the third process start either. Successful-work acceptance is not claimed.
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const t8ReplayPhaseEnv = "OMNIPUS_T8_ONCE_REPLAY_PHASE"
const t8ReplayDirEnv = "OMNIPUS_T8_ONCE_REPLAY_DIR"
const t8ReplayTestName = "TestT8Boot_StartedUnfinishedOnceReplaysExactlyOnce"
const t8ReplayWorker = "t8-replay-worker"

var errT8ReplayProvider = errors.New("T8 artificial provider failure finishes the replay")

// not-wire-format: private subprocess receipt, never a gateway/SPA payload.
type t8OnceCrashReceipt struct {
	PID       int    `json:"pid"`
	TaskID    string `json:"task_id"`
	TaskDir   string `json:"task_dir"`
	RunID     string `json:"run_id"`
	SessionID string `json:"session_id"`
	AtMS      int64  `json:"at_ms"`
}

type t8OnceReplayProvider struct {
	block bool
	calls atomic.Int32
}

func (*t8OnceReplayProvider) GetDefaultModel() string { return "t8-physical-once-fixture" }
func (p *t8OnceReplayProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	if p.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, errT8ReplayProvider
}

func t8BootOnceProcess(t *testing.T, dir string, block bool) (*agent.AgentLoop, *services, *t8OnceReplayProvider) {
	t.Helper()
	home := filepath.Join(dir, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	t.Setenv("HOME", home)
	t.Setenv(config.EnvHome, home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Gateway:  config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Planning: config.PlanningConfig{GoalMaxRounds: 1, TaskMaxAttempts: 1},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: t8ReplayWorker, Name: "T8 replay worker", Type: config.AgentTypeCustom}},
		},
	}
	cfg.Sandbox = config.DefaultConfig().Sandbox
	provider := &t8OnceReplayProvider{block: block}
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	al := mustAgentLoop(t, cfg, mb, provider)
	rs, err := setupAndStartServices(context.Background(), cfg, credentials.SecretBundle{}, al, mb, home, newUnlockedStore(t, home), &SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false)
	require.NoError(t, err, "instrument: the real physical gateway boot completes")
	t.Cleanup(func() { t8StopServicesOnce(rs) })
	require.NotNil(t, rs.TaskTrigger)
	return al, rs, provider
}

// t8StoppedServices records services already stopped: stopAndCleanupServices
// closes channels and is not idempotent, and a test may join the services
// explicitly before its t.Cleanup runs.
var t8StoppedServices sync.Map

func t8StopServicesOnce(rs *services) {
	if _, loaded := t8StoppedServices.LoadOrStore(rs, struct{}{}); loaded {
		return
	}
	stopAndCleanupServices(rs, 5*time.Second, false)
}

func t8WriteReplayReceipt(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path+".tmp", raw, 0o600))
	require.NoError(t, os.Rename(path+".tmp", path))
}

func t8ReadCrashReceipt(t *testing.T, dir string) t8OnceCrashReceipt {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "ready.json"))
	require.NoError(t, err)
	var receipt t8OnceCrashReceipt
	require.NoError(t, json.Unmarshal(raw, &receipt))
	return receipt
}

func t8CrashStartedOnce(t *testing.T, dir string) {
	t.Helper()
	al, rs, provider := t8BootOnceProcess(t, dir, true)
	store := agent.GetTaskStore(al)
	attempts := 1 // An intentionally failed replay needs one attempt, not a retry campaign.
	tk := &task.Task{Title: "T8 started unfinished once", Prompt: "Exercise physical once recovery.", Action: task.ActionLLM,
		AgentID: t8ReplayWorker, WorkspaceID: testHarnessWorkspaceMembershipID, Status: task.StatusInbox, MaxAttempts: &attempts,
		Criteria: []task.AcceptanceCriterion{{Kind: task.KindProse, Text: "The recovery exercise is recorded.", Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "t8-person"}}}}
	require.NoError(t, store.Create(tk))
	api := rs.restAPIRef
	require.NotNil(t, api)
	dod := []task.AcceptanceCriterion{{Kind: task.KindProse, Text: "The task run reaches a recorded terminal result.", Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "t8-person"}}}
	require.NoError(t, api.syncTaskGoalRecord(tk, true, dod, true))
	// Establish a genuinely future once, then wait for the real wall clock.
	// No loaded scheduler state, marker or TaskRun is written by the test.
	at := time.Now().Add(100 * time.Millisecond).UnixMilli()
	trigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}
	updated, err := store.Update(tk.ID, task.Patch{Trigger: &trigger})
	require.NoError(t, err)
	rs.TaskTrigger.OnTaskUpserted(updated)
	require.Eventually(t, func() bool { return time.Now().UnixMilli() >= at }, cancelTestTurnStartDeadline, time.Millisecond)
	rs.TaskTrigger.RunDueJobs(time.Now())
	rs.TaskTrigger.WaitForLane()
	require.Eventually(t, func() bool { return provider.calls.Load() == 1 }, cancelTestTurnStartDeadline, 10*time.Millisecond, "instrument: real cron delivery entered the model")
	bound, err := store.Get(tk.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusInProgress, bound.Status)
	runs, err := store.ListRuns(tk.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1, "instrument: exactly one real pre-crash run exists")
	require.Nil(t, runs[0].EndedAt, "the started run is genuinely unfinished, not the missed class")
	require.Equal(t, task.RunKindScheduled, runs[0].Kind)
	require.Equal(t, bound.SessionID, runs[0].SessionID)
	require.NotEmpty(t, bound.SessionID)
	t8WriteReplayReceipt(t, filepath.Join(dir, "ready.json"), t8OnceCrashReceipt{PID: os.Getpid(), TaskID: tk.ID, TaskDir: store.Dir(), RunID: runs[0].RunID, SessionID: bound.SessionID, AtMS: at})
	select {} // Parent must SIGKILL this process: no graceful Stop/Close or closed run.
}

func t8ReplayAfterCrash(t *testing.T, dir string) {
	t.Helper()
	before := t8ReadCrashReceipt(t, dir)
	require.NotEqual(t, before.PID, os.Getpid(), "replay must cross an actual process boundary")
	require.GreaterOrEqual(t, time.Now().UnixMilli(), before.AtMS, "the once is past at restart")
	al, rs, provider := t8BootOnceProcess(t, dir, false)
	store := agent.GetTaskStore(al)
	// Collect the real boot-restored jobs, but do not invoke recovery by hand:
	// a missing boot recovery call must not pass through a test-owned repair.
	rs.TaskTrigger.RunDueJobs(time.Now())
	rs.TaskTrigger.WaitForLane()
	var runs []task.TaskRun
	settled := assert.Eventually(t, func() bool {
		var err error
		runs, err = store.ListRuns(before.TaskID)
		if err != nil || len(runs) != 2 {
			return false
		}
		return runs[0].EndedAt != nil && runs[1].EndedAt != nil
	}, cancelTestTurnStartDeadline, 10*time.Millisecond, "6b F1 replays the delivered started-unfinished once exactly once at physical boot, then finishes the replay")
	if !settled {
		finalRuns, readErr := store.ListRuns(before.TaskID)
		t.Fatalf("6b F1 actual post-boot state: runs=%+v read_error=%v provider_calls=%d", finalRuns, readErr, provider.calls.Load())
	}
	require.Positive(t, provider.calls.Load(), "a fabricated run row is not actual replay execution")
	old, replay := 0, 0
	for _, run := range runs {
		if run.RunID == before.RunID {
			old++
		} else {
			replay++
			require.NotEmpty(t, run.SessionID)
			require.Equal(t, task.RunKindScheduled, run.Kind)
		}
	}
	require.Equal(t, 1, old, "the abandoned original remains one historical run")
	require.Equal(t, 1, replay, "exactly one new real run, not a repeated fire")
	t8WriteReplayReceipt(t, filepath.Join(dir, "finished.json"), runs)
	t.Logf("physical replay finished: original=%s; total runs=%d; provider calls=%d", before.RunID, len(runs), provider.calls.Load())
}

func t8VerifyFinishedOnceNotReplayed(t *testing.T, dir string) {
	t.Helper()
	before := t8ReadCrashReceipt(t, dir)
	raw, err := os.ReadFile(filepath.Join(dir, "finished.json"))
	require.NoError(t, err)
	var finished []task.TaskRun
	require.NoError(t, json.Unmarshal(raw, &finished))
	require.Len(t, finished, 2)
	al, rs, provider := t8BootOnceProcess(t, dir, false)
	rs.TaskTrigger.RunDueJobs(time.Now())
	rs.TaskTrigger.WaitForLane()
	rs.TaskTrigger.RunRecoverySweep()
	rs.TaskTrigger.WaitForLane()
	// Join ordinary owners before observing absence; do not leave a pending
	// asynchronous dispatch beyond the assertion or the test's cleanup.
	t8StopServicesOnce(rs)
	al.Close()
	runs, err := agent.GetTaskStore(al).ListRuns(before.TaskID)
	require.NoError(t, err)
	assert.Equal(t, finished, runs, "a finished replay is never delivered again on the third physical start")
	assert.Zero(t, provider.calls.Load(), "no third-process model execution for the completed once")
}

func t8OncePhaseCommand(dir, phase string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^"+t8ReplayTestName+"$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), t8ReplayPhaseEnv+"="+phase, t8ReplayDirEnv+"="+dir)
	return cmd
}

func t8KillStartedOnceProcess(t *testing.T, dir string) {
	t.Helper()
	log, err := os.Create(filepath.Join(dir, "crash-process.log"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Close()) })
	cmd := t8OncePhaseCommand(dir, "crash")
	cmd.Stdout, cmd.Stderr = log, log
	require.NoError(t, cmd.Start())
	joined := false
	t.Cleanup(func() {
		if !joined {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	ready := filepath.Join(dir, "ready.json")
	started := assert.Eventually(t, func() bool { _, statErr := os.Stat(ready); return statErr == nil }, cancelTestTurnStartDeadline, 10*time.Millisecond, "crash child must publish its actual started/open run receipt")
	if !started {
		output, readErr := os.ReadFile(log.Name())
		t.Fatalf("crash process did not establish the required open run: log_error=%v\n%s", readErr, output)
	}
	receipt := t8ReadCrashReceipt(t, dir)
	require.Equal(t, cmd.Process.Pid, receipt.PID)
	require.NoError(t, cmd.Process.Kill())
	waitErr := cmd.Wait()
	joined = true
	var exitErr *exec.ExitError
	require.ErrorAs(t, waitErr, &exitErr)
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	require.True(t, status.Signaled())
	require.Equal(t, syscall.SIGKILL, status.Signal(), "a clean shutdown is not crash evidence")
	// Read the persisted store after the child is dead, before any next boot
	// can repair it. A pre-kill receipt alone cannot prove the crash snapshot.
	require.NotEmpty(t, receipt.TaskDir)
	store := task.New(receipt.TaskDir)
	persisted, err := store.Get(receipt.TaskID)
	require.NoError(t, err)
	require.Equal(t, task.StatusInProgress, persisted.Status)
	require.Equal(t, receipt.SessionID, persisted.SessionID)
	runs, err := store.ListRuns(receipt.TaskID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, receipt.RunID, runs[0].RunID)
	require.Equal(t, receipt.SessionID, runs[0].SessionID)
	require.Equal(t, task.RunKindScheduled, runs[0].Kind)
	require.Nil(t, runs[0].EndedAt, "SIGKILL preserved an actually started, unfinished run")
	t.Logf("SIGKILL and persisted open run verified: pid=%d task=%s open_run=%s scheduled_at_ms=%d", receipt.PID, receipt.TaskID, receipt.RunID, receipt.AtMS)
}

func TestT8Boot_StartedUnfinishedOnceReplaysExactlyOnce(t *testing.T) {
	if phase := os.Getenv(t8ReplayPhaseEnv); phase != "" {
		dir := os.Getenv(t8ReplayDirEnv)
		require.NotEmpty(t, dir)
		switch phase {
		case "crash":
			t8CrashStartedOnce(t, dir)
		case "replay":
			t8ReplayAfterCrash(t, dir)
		case "finished":
			t8VerifyFinishedOnceNotReplayed(t, dir)
		default:
			t.Fatalf("unknown private replay phase %q", phase)
		}
		return
	}
	dir := t.TempDir()
	t8KillStartedOnceProcess(t, dir)
	for _, phase := range []string{"replay", "finished"} {
		output, err := t8OncePhaseCommand(dir, phase).CombinedOutput()
		t.Logf("%s process output:\n%s", phase, output)
		require.NoError(t, err, "physical %s process must satisfy 6b F1", phase)
		assert.Regexp(t, regexp.MustCompile(`(?m)^--- PASS: `+regexp.QuoteMeta(t8ReplayTestName)+` \(`), string(output), "a zero-exit helper that matched no test is not a replay receipt")
	}
}
