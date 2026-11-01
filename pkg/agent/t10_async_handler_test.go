//go:build goolm && stdjson

package agent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t10HeldClaimProvider struct {
	*claimingWorker
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *t10HeldClaimProvider) Chat(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	p.once.Do(func() { close(p.entered) })
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return p.claimingWorker.Chat(ctx, messages, defs, model, options)
}

// S-D.3 / TDD 11 / D-1.7: exercise the REAL TaskTriggerScheduler, executor,
// session launcher, open run and held paid-model boundary. A fake returning
// task handler could not prove that engine settlement precedes task completion.
// Preserve-behavior control; a passing base is not manufactured RED.
func TestT10_TaskDispatchHandlerSettlesWhileActualRunIsAlive(t *testing.T) {
	p := &t10HeldClaimProvider{claimingWorker: newClaimingWorker(turnClaimMet("async lifetime verified")), entered: make(chan struct{}), release: make(chan struct{})}
	al := t10NativeTaskLoop(t, p)
	s := t10TaskScheduler(t, filepath.Join(t.TempDir(), "cron", "jobs.json"), al.taskStore, al.taskExecutor)
	clk := newTriggerFakeClock()
	s.SetClock(clk)
	require.NoError(t, s.Start())
	al.SetTaskTriggerScheduler(s)
	var free sync.Once
	defer func() { free.Do(func() { close(p.release) }); s.Stop(); s.WaitForLane() }()
	restore := config.SetMemoryProviderForTest(func() (bool, bool) { return true, true }, func() (uint64, bool) { return 0, true })
	defer restore()
	tk := newCompletionContractTask(t, al, "native-agent", "real asynchronous task")
	at := clk.Now().Truncate(time.Second).Add(time.Hour).UnixMilli()
	trigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}
	tk, err := al.taskStore.Update(tk.ID, task.Patch{Trigger: &trigger})
	require.NoError(t, err)
	s.OnTaskUpserted(tk)
	clk.Advance(time.UnixMilli(at).Sub(clk.Now()))
	s.RunDueJobs(clk.Now())
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("actual task worker never entered")
	}
	settled := make(chan struct{})
	go func() { s.WaitForLane(); close(settled) }()
	select {
	case <-settled:
	case <-time.After(time.Second):
		t.Error("scheduler admission/handler lifetime was extended to the asynchronous task lifetime")
		free.Do(func() { close(p.release) })
		<-settled
	}
	rows, err := al.taskStore.ListRuns(tk.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, task.StatusInProgress, rows[0].Status)
	assert.Nil(t, rows[0].EndedAt, "handler settled while a genuine stored run is still open")
	assert.NotEmpty(t, rows[0].SessionID)
	free.Do(func() { close(p.release) })
	f := &t10T8Fixture{al: al}
	f.terminal(t, tk.ID)
}
