//go:build goolm && stdjson

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

type crashTaskResultCut struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *crashTaskResultCut) open() { c.once.Do(func() { close(c.release) }) }
func (c *crashTaskResultCut) BeforeTool(_ context.Context, r *agent.ToolCallHookRequest) (*agent.ToolCallHookRequest, agent.HookDecision, error) {
	return r, agent.HookDecision{Action: agent.HookActionContinue}, nil
}
func (c *crashTaskResultCut) AfterTool(ctx context.Context, r *agent.ToolResultHookResponse) (*agent.ToolResultHookResponse, agent.HookDecision, error) {
	if r.Tool != "append_file" {
		return r, agent.HookDecision{Action: agent.HookActionContinue}, nil
	}
	c.entered <- struct{}{}
	select {
	case <-c.release:
	case <-ctx.Done():
	}
	return r, agent.HookDecision{Action: agent.HookActionAbortTurn, Reason: "fixture crash cut after replacement before result"}, nil
}

// DESIGN Q15: fresh task binding is not acceptance of old unresolved effects.
// Capture an ACTUAL task-origin append at AfterTool, not a fabricated task chat.
func TestCrashRecovery_FreshTaskRetryCannotBypassPriorUncertainty(t *testing.T) {
	oldProvider := newCrashTaskProvider()
	oldProvider.retry = true
	api := newTestRestAPIAlignedStoresWithProvider(t, oldProvider)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})
	work, err := workspace.EnsureWorkDir(api.homePath, wsID)
	require.NoError(t, err)
	oldProvider.path = filepath.Join(work, "uncertain-task.txt")
	inst, ok := api.agentLoop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.Tools.RegisterReplacing(tools.NewAppendFileTool(work, true))
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"append_file": "allow", "goal_claim": "allow"}})
	cut := &crashTaskResultCut{entered: make(chan struct{}, 1), release: make(chan struct{})}
	require.NoError(t, api.agentLoop.MountHook(agent.NamedHook("actual-task-result-crash-cut", cut)))
	t.Cleanup(cut.open)
	t.Cleanup(oldProvider.open)
	tk := createTaskViaAPI(t, api, "task with uncertain append", wsID)
	w := patchTask(t, api, tk.Id, `{"agent_id":"mia","prompt":"`+crashTaskMarker+`","max_attempts":3,"status":"next"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = patchTask(t, api, tk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	gatewayCrashAwait(t, cut.entered)
	before, err := api.taskStore.Get(tk.Id)
	require.NoError(t, err)
	require.NotEmpty(t, before.SessionID)
	bytes, err := os.ReadFile(oldProvider.path)
	require.NoError(t, err)
	require.Equal(t, "RETRY\n", string(bytes))
	snapshot := t.TempDir()
	require.NoError(t, gatewayCrashCopy(api.homePath, snapshot))
	require.NoError(t, os.RemoveAll(filepath.Join(snapshot, "sessions", before.SessionID, "file_effects")))
	drained := make(chan struct{})
	go func() { api.taskExecutor.Drain(10 * time.Second); close(drained) }()
	cut.open()
	oldProvider.open()
	gatewayCrashAwait(t, drained)
	cfg := api.agentLoop.GetConfig()
	api.agentLoop.Close()
	require.NoError(t, os.RemoveAll(api.homePath))
	require.NoError(t, gatewayCrashCopy(snapshot, api.homePath))
	next := newCrashTaskProvider()
	next.retry = true
	next.path = oldProvider.path
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), next)
	api.agentLoop = al
	api.taskStore = task.New(filepath.Join(api.homePath, "tasks"))
	api.taskExecutor = agent.GetTaskExecutor(al)
	inst, ok = al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.Tools.RegisterReplacing(tools.NewAppendFileTool(work, true))
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"append_file": "allow", "goal_claim": "allow"}})
	api.reconcileStuckTasks()
	w = patchTask(t, api, tk.Id, `{"status":"in_progress"}`)
	next.open()
	api.taskExecutor.Drain(10 * time.Second)
	bytes, err = os.ReadFile(oldProvider.path)
	require.NoError(t, err)
	assert.Equal(t, "RETRY\n", string(bytes), "neither old nor freshly bound task execution may blindly redo the uncertain append")
	record, err := al.GetSessionLifecycleStore().Load(before.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "interrupted", string(record.State), "prior unresolved occurrence is durably held even if a Retry replaces its binding")
	fields := crashTaskLifecycleJSON(t, record)
	assert.Equal(t, "reconciliation_required", fields["interruption_reason"])
	next.mu.Lock()
	calls := next.taskCalls
	next.mu.Unlock()
	assert.Equal(t, 0, calls, "no recovered provider call may precede machine proof/explicit acceptance")
	if w.Code < 400 {
		fresh, err := api.taskStore.Get(tk.Id)
		require.NoError(t, err)
		assert.NotEqual(t, task.StatusInProgress, fresh.Status, "ordinary success with only a timer is not an uncertainty report")
	}
}

func crashTaskLifecycleJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	bytes, err := json.Marshal(value)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(bytes, &fields))
	return fields
}
