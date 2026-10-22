//go:build goolm && stdjson

package gateway

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// DESIGN Decision 4 / RED A: use Board's status-only PATCH and the REAL
// launcher. A timer or a recording launcher alone cannot satisfy the oracle.
const crashTaskMarker = "CRASH_RETRY_EFFECT_REQUEST"

type crashTaskProvider struct {
	mu              sync.Mutex
	calls           int
	taskCalls       int
	entered         chan bool
	resultCommitted chan struct{}
	release         chan struct{}
	once            sync.Once
	retry           bool
	path            string
}

func newCrashTaskProvider() *crashTaskProvider {
	return &crashTaskProvider{entered: make(chan bool, 16), resultCommitted: make(chan struct{}, 1), release: make(chan struct{})}
}
func (p *crashTaskProvider) open() { p.once.Do(func() { close(p.release) }) }
func (p *crashTaskProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	isTask := false
	for _, m := range messages {
		if m.Role == "user" && strings.Contains(m.Content, crashTaskMarker) {
			isTask = true
		}
	}
	p.mu.Lock()
	p.calls++
	if isTask {
		p.taskCalls++
	}
	n := p.taskCalls
	p.mu.Unlock()
	p.entered <- isTask
	if p.retry && isTask && n == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "retry-effect", Name: "append_file", Arguments: map[string]any{"path": p.path, "content": "RETRY\n"}}}, FinishReason: "tool_calls"}, nil
	}
	if p.retry && isTask && n == 2 {
		p.resultCommitted <- struct{}{}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return nil, context.Canceled
	}
}
func (*crashTaskProvider) GetDefaultModel() string { return "crash-task-fixture" }
func gatewayCrashAwait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("fixture barrier not reached (not an assertion-level RED)")
		var z T
		return z
	}
}

func gatewayCrashCopy(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(src, path)
		if e != nil {
			return e
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if d.Type()&os.ModeSymlink != 0 {
			v, e := os.Readlink(path)
			if e != nil {
				return e
			}
			return os.Symlink(v, target)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("refuse nonregular snapshot source %s", path)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(target, b, 0600)
	})
}

func crashTaskAfterBoot(t *testing.T) (*restAPI, *task.Task, task.TaskRun) {
	t.Helper()
	p := newCrashTaskProvider()
	api := newTestRestAPIAlignedStoresWithProvider(t, p)
	t.Setenv("HOME", api.homePath)
	api.agentLoop.GetConfig().Performance.MaxParallelAgents = 4 // two busy controls plus a real Retry admission.
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})
	tsk := createTaskViaAPI(t, api, "Retry after real crash cut", wsID)
	w := patchTask(t, api, tsk.Id, `{"agent_id":"mia","prompt":"`+crashTaskMarker+`","max_attempts":3,"status":"next"}`)
	require.Equal(t, http.StatusOK, w.Code, "setup assignment: %s", w.Body.String())
	w = patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w.Code, "first real start: %s", w.Body.String())
	require.True(t, gatewayCrashAwait(t, p.entered), "the first task actually enters its provider")
	before, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err)
	require.NotEmpty(t, before.SessionID)
	runs, err := api.taskStore.ListRuns(tsk.Id)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.True(t, runs[0].IsOpen())
	// Exact persisted crash state is captured while the provider cannot finish.
	// Restore at the same temp-home path after the old writer has fully settled.
	snapshot := t.TempDir()
	require.NoError(t, gatewayCrashCopy(api.homePath, snapshot))
	drained := make(chan struct{})
	go func() { api.taskExecutor.Drain(10 * time.Second); close(drained) }()
	p.open()
	gatewayCrashAwait(t, drained)
	cfg := api.agentLoop.GetConfig()
	api.agentLoop.Close()
	require.NoError(t, os.RemoveAll(api.homePath))
	require.NoError(t, gatewayCrashCopy(snapshot, api.homePath))
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), newCrashTaskProvider())
	api.agentLoop = al
	api.taskStore = task.New(filepath.Join(api.homePath, "tasks"))
	api.taskExecutor = agent.GetTaskExecutor(al)
	api.reconcileStuckTasks()
	old, err := api.taskStore.Get(tsk.Id)
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, old.Status)
	require.Equal(t, before.SessionID, old.SessionID, "boot preserves the old binding/history until explicit Retry")
	closed, err := api.taskStore.ListRuns(tsk.Id)
	require.NoError(t, err)
	require.Len(t, closed, 1)
	require.Equal(t, task.StatusFailed, closed[0].Status)
	require.False(t, closed[0].IsOpen())
	return api, old, closed[0]
}

func TestCrashRecovery_ManualRetryCreatesFreshRun(t *testing.T) {
	for _, busy := range []int{0, 2} {
		t.Run(fmt.Sprintf("busy_helpers_%d", busy), func(t *testing.T) {
			api, old, closed := crashTaskAfterBoot(t)
			p := newCrashTaskProvider()
			p.retry = true
			work, err := workspace.EnsureWorkDir(api.homePath, old.WorkspaceID)
			require.NoError(t, err)
			p.path = filepath.Join(work, "retry.txt")
			inst, ok := api.agentLoop.GetRegistry().GetAgent("mia")
			require.True(t, ok)
			inst.Provider = p
			inst.Tools.RegisterReplacing(tools.NewAppendFileTool(work, true))
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"append_file": config.ToolPolicyAllow, "goal_claim": config.ToolPolicyAllow}})
			t.Cleanup(func() { p.open(); api.taskExecutor.Drain(10 * time.Second) })
			launcher := agent.NewSteerLauncher(api.agentLoop)
			for i := 0; i < busy; i++ {
				parent, err := api.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
				require.NoError(t, err)
				require.NoError(t, api.agentLoop.GetSessionStore().SetMeta(parent.ID, session.MetaPatch{WorkspaceID: &old.WorkspaceID}))
				launched, err := launcher.Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent.ID, TargetAgentID: "mia", Task: "hold independent busy work", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: fmt.Sprintf("busy-%d", i)}})
				require.NoError(t, err)
				d, err := launcher.Dispatch(context.Background(), launched.SessionID, launched.Generation)
				require.NoError(t, err)
				require.Equal(t, steer.DispatchRunning, d.State)
				require.False(t, gatewayCrashAwait(t, p.entered), "independent busy helper is held at the provider")
			}
			w := patchTask(t, api, old.ID, `{"status":"in_progress"}`)
			require.Equal(t, http.StatusOK, w.Code, "Board Retry: %s", w.Body.String())
			runs, err := api.taskStore.ListRuns(old.ID)
			require.NoError(t, err)
			require.Len(t, runs, 2, "A: Board-style Retry must create exactly one additional manual run; a timer alone admits zero")
			fresh, err := api.taskStore.Get(old.ID)
			require.NoError(t, err)
			assert.NotEqual(t, old.SessionID, fresh.SessionID, "A: stale binding must be retired, not reused")
			var newRun *task.TaskRun
			for i := range runs {
				if runs[i].RunID != closed.RunID {
					newRun = &runs[i]
				} else {
					assert.Equal(t, closed, runs[i], "old run remains byte-for-field historical")
				}
			}
			require.NotNil(t, newRun)
			assert.Equal(t, task.RunKindManual, newRun.Kind)
			assert.Equal(t, fresh.SessionID, newRun.SessionID)
			rec, err := api.agentLoop.GetSessionLifecycleStore().Load(fresh.SessionID)
			require.NoError(t, err)
			require.NotNil(t, rec.ExecutionID, "fresh run has a real execution admission")
			gatewayCrashAwait(t, p.resultCommitted)
			bytes, err := os.ReadFile(p.path)
			require.NoError(t, err)
			assert.Equal(t, "RETRY\n", string(bytes), "exactly one new task effect")
			// Repeated status-only Retry while live cannot mint another attempt.
			w = patchTask(t, api, old.ID, `{"status":"in_progress"}`)
			require.Equal(t, http.StatusOK, w.Code)
			again, err := api.taskStore.ListRuns(old.ID)
			require.NoError(t, err)
			assert.Equal(t, runs, again, "repeated live Retry is admission-idempotent")
		})
	}
}

func TestCrashRecovery_StaleBindingCannotHideLaunchRefusal(t *testing.T) {
	api, old, closed := crashTaskAfterBoot(t)
	api.taskExecutor = nil
	w := patchTask(t, api, old.ID, `{"status":"in_progress"}`)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "A: stale binding must not bypass the visible nil-executor refusal")
	fresh, err := api.taskStore.Get(old.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusFailed, fresh.Status, "a refused Retry is not a successful In Progress timer")
	runs, err := api.taskStore.ListRuns(old.ID)
	require.NoError(t, err)
	assert.Equal(t, []task.TaskRun{closed}, runs, "refusal creates no phantom attempt")
}
