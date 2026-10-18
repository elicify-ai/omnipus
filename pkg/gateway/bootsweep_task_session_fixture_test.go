package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/require"
)

const bootsweepTaskReceiptEnv = "OMNIPUS_BOOTSWEEP_TASK_RECEIPT"

// not-wire-format: private subprocess receipt, never a gateway payload.
type bootsweepTaskCrashReceipt struct {
	Boot   i1CrashReceipt `json:"Boot"`
	TaskID string         `json:"TaskID"`
}

// bootsweepWriteTaskReceipt runs the real MAIN task path and publishes its
// receipt only after the external provider is entered. The parent kills this
// process; graceful Close must not hide a missing durable session write.
func bootsweepWriteTaskReceipt(t *testing.T, receiptPath string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		List:     []config.AgentConfig{{ID: "bootsweep-worker", Name: "Bootsweep worker", Type: config.AgentTypeCustom}},
	}}
	cfg.Sandbox = config.DefaultConfig().Sandbox
	provider := &uatD2bFinalProvider{entered: make(chan context.Context, 1), release: make(chan struct{})}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	store := agent.GetTaskStore(al)
	require.NotNil(t, store)
	api := &restAPI{agentLoop: al, homePath: home, taskStore: store}
	executor := agent.GetTaskExecutor(al)
	executor.SetMainSessionResolver(taskMainResolver{api: api})
	tk := &task.Task{Title: "bootsweep member", Prompt: "Complete the crash-recovery task.",
		Action: task.ActionLLM, AgentID: "bootsweep-worker", WorkspaceID: testHarnessWorkspaceMembershipID,
		Status: task.StatusNext, Priority: 3,
		Criteria: []task.AcceptanceCriterion{{Kind: "prose", Text: "The task explains crash recovery.",
			Author: task.CriterionAuthor{Kind: "user", ID: "admin"}}}}
	require.NoError(t, store.Create(tk))
	dod := []task.AcceptanceCriterion{{Kind: "prose", Text: "The explanation is recorded on the task.",
		Author: task.CriterionAuthor{Kind: "user", ID: "admin"}}}
	require.NoError(t, api.syncTaskGoalRecord(tk, true, dod, true))
	require.NoError(t, executor.ExecuteTask(context.Background(), tk.ID, nil))
	select {
	case <-provider.entered:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("real task did not enter the provider before the crash")
	}
	bound, err := store.Get(tk.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusInProgress, bound.Status)
	require.NotEmpty(t, bound.SessionID)
	rec, err := al.GetSessionLifecycleStore().Load(bound.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleRunning, rec.State)
	require.NotNil(t, rec.ExecutionID, "instrument: the task really owns an admitted execution")
	meta, err := al.GetSessionStore().GetMeta(bound.SessionID)
	require.NoError(t, err)
	require.NotEmpty(t, meta.ParentSessionID, "instrument: MAIN task must be a real child")
	raw, err := json.Marshal(bootsweepTaskCrashReceipt{TaskID: tk.ID, Boot: i1CrashReceipt{
		Config: cfg, Agents: cfg.Agents.List, Root: bound.SessionID,
		LifecycleDir: al.GetSessionLifecycleStore().Dir(), InboxDir: filepath.Join(home, "session_messages"),
		SessionsDir: al.GetSessionStore().BaseDir(),
	}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(receiptPath+".tmp", raw, 0600))
	require.NoError(t, os.Rename(receiptPath+".tmp", receiptPath))
	select {} // A real SIGKILL, not a task Stop or graceful shutdown.
}

func bootsweepKillTask(t *testing.T) bootsweepTaskCrashReceipt {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ready.json")
	log, err := os.Create(filepath.Join(dir, "killed-task.log"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Close()) })
	cmd := exec.Command(os.Args[0], "-test.run=^TestBootsweepTaskSession_RestartVisibility$", "-test.v")
	cmd.Env = append(os.Environ(), bootsweepTaskReceiptEnv+"="+path, "TMPDIR="+dir, "TMP="+dir, "TEMP="+dir)
	cmd.Stdout, cmd.Stderr = log, log
	require.NoError(t, cmd.Start())
	joined := false
	t.Cleanup(func() {
		if !joined {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	deadline := time.NewTimer(cancelTestTurnStartDeadline)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var raw []byte
	for {
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			raw = data
			break
		}
		require.True(t, errors.Is(readErr, os.ErrNotExist), "read subprocess receipt: %v", readErr)
		select {
		case <-tick.C:
		case <-deadline.C:
			output, readLogErr := os.ReadFile(log.Name())
			require.NoError(t, readLogErr)
			t.Fatalf("task never entered the provider before SIGKILL:\n%s", output)
		}
	}
	require.NoError(t, cmd.Process.Kill())
	waitErr := cmd.Wait()
	joined = true
	require.Error(t, waitErr, "instrument: the task process must die before graceful cleanup")
	var receipt bootsweepTaskCrashReceipt
	require.NoError(t, json.Unmarshal(raw, &receipt))
	receipt.Boot.Config.Agents.List = receipt.Boot.Agents
	return receipt
}
