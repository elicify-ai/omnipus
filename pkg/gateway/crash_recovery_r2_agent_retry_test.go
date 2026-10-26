//go:build goolm && stdjson

package gateway

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// SF3 / DESIGN 4: both agent-side tools read Failed before either admission.
// The winner is held AFTER the real launcher persisted its session and BEFORE
// binding. The losing real executor reservation refusal may not undo that win.
func TestCrashRecovery_R2AgentRetryLoserCannotRollbackWinner(t *testing.T) {
	api, old, closed := crashTaskAfterBoot(t)
	p := &r2RetryProvider{crashTaskProvider: newCrashTaskProvider()}
	p.resultCommitted = make(chan struct{}, 8)
	work, err := workspace.EnsureWorkDir(api.homePath, old.WorkspaceID)
	require.NoError(t, err)
	p.path = filepath.Join(work, "agent-retry-winner.txt")
	inst, found := api.agentLoop.GetRegistry().GetAgent("mia")
	require.True(t, found)
	inst.Provider = p
	inst.Tools.RegisterReplacing(tools.NewAppendFileTool(work, true))
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"append_file": config.ToolPolicyAllow, "goal_claim": config.ToolPolicyAllow}})
	t.Cleanup(func() { p.open(); api.taskExecutor.Drain(10 * time.Second) })
	observed := &r2ObservedLauncher{SessionLauncher: agent.NewSteerLauncher(api.agentLoop), entered: make(chan steer.LaunchResult, 1), release: make(chan struct{})}
	openLaunch := sync.OnceFunc(func() { close(observed.release) })
	t.Cleanup(openLaunch)
	api.taskExecutor.SetSessionLauncher(observed)
	ready := make(chan string, 2)
	releaseA, releaseB := make(chan struct{}), make(chan struct{})
	openA := sync.OnceFunc(func() { close(releaseA) })
	openB := sync.OnceFunc(func() { close(releaseB) })
	t.Cleanup(openA)
	t.Cleanup(openB)
	makeCaller := func(name string, release <-chan struct{}) *tools.TaskRunTool {
		tool := tools.NewTaskRunTool(api.taskStore)
		// This existing policy-precheck boundary follows the actual store Get.
		// Only eligibility is fixed; task transition, reservation and launch are real.
		tool.SetDelegationDenyChecker(func(context.Context, string, string) *tools.DelegationDenial {
			ready <- name
			<-release
			return nil
		})
		tool.SetStartTaskNow(api.taskExecutor.StartTaskNow)
		return tool
	}
	a, b := makeCaller("A", releaseA), makeCaller("B", releaseB)
	resultA, resultB := make(chan *tools.ToolResult, 1), make(chan *tools.ToolResult, 1)
	ctx := tools.WithWorkspaceID(context.Background(), old.WorkspaceID)
	go func() { resultA <- a.Execute(ctx, map[string]any{"task_id": old.ID}) }()
	go func() { resultB <- b.Execute(ctx, map[string]any{"task_id": old.ID}) }()
	assert.ElementsMatch(t, []string{"A", "B"}, []string{gatewayCrashAwait(t, ready), gatewayCrashAwait(t, ready)}, "instrument: both callers hold the SAME genuine Failed snapshot")
	openA()
	launched := gatewayCrashAwait(t, observed.entered)
	require.NotEmpty(t, launched.SessionID)
	winning, err := api.taskStore.Get(old.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusInProgress, winning.Status, "instrument: A owns the locked Retry transition")
	require.Empty(t, winning.SessionID, "instrument: winner is held before durable binding, not after admission completed")
	openB()
	loser := gatewayCrashAwait(t, resultB)
	require.NotNil(t, loser)
	assert.True(t, loser.IsError, "no caller may claim a confirmed running session before binding")
	preserved, err := api.taskStore.Get(old.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusInProgress, preserved.Status, "loser may reverse only a transition it owns, never A's successful Retry")
	assert.Equal(t, winning.StartedAt, preserved.StartedAt, "loser must preserve winner's timer/admission identity")
	openLaunch()
	winner := gatewayCrashAwait(t, resultA)
	require.NotNil(t, winner)
	assert.False(t, winner.IsError, "the real winning launch succeeds: %s", winner.ContentForLLM())
	gatewayCrashAwait(t, p.resultCommitted)
	after, err := api.taskStore.Get(old.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusInProgress, after.Status, "a reported successful launch cannot leave the stored task Failed")
	assert.Equal(t, launched.SessionID, after.SessionID)
	runs, err := api.taskStore.ListRuns(old.ID)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	for _, run := range runs {
		if run.RunID == closed.RunID {
			assert.Equal(t, closed, run, "old crash attempt is immutable")
		} else {
			assert.Equal(t, launched.SessionID, run.SessionID)
		}
	}
	data, err := os.ReadFile(p.path)
	require.NoError(t, err)
	assert.Equal(t, "RETRY\n", string(data), "only the real winner's effect executes")
}
