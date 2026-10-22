//go:build goolm && stdjson

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

type crashMainResolver struct{ id string }

func (r crashMainResolver) EligibleMain(ws, agentID string) (string, bool, error) {
	return r.id, ws == testHarnessWorkspaceMembershipID && agentID == testDefaultAgentID, nil
}

func crashTaskCeiling(t *testing.T, steered bool) (*crashHarness, *task.Task, string) {
	t.Helper()
	gate := make(chan struct{})
	p := &crashScript{gate: gate, entered: make(chan int, 32)}
	h := crashNew(t, p)
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	h.al.GetConfig().Planning.GoalMaxRounds = 2
	inst := h.al.GetRegistry().primaryChatAgentForTest()
	inst.MaxIterations = 2
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"read_file": "allow", "goal_claim": "allow"}})
	work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	path := filepath.Join(work, "task-read.txt")
	require.NoError(t, os.WriteFile(path, []byte("read only\n"), 0600))
	p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall(fmt.Sprintf("task-read-%d", i), "read_file", map[string]any{"path": path})}, FinishReason: "tool_calls"}, nil
	}
	attempts := 1
	criterion := task.AcceptanceCriterion{ID: "criterion", Kind: task.KindProse, Text: "read the requested file", Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "owner"}}
	tk := &task.Task{Title: "actual task ceiling", Prompt: "keep reading until explicitly continued", Action: task.ActionLLM, AgentID: testDefaultAgentID, WorkspaceID: testHarnessWorkspaceMembershipID, Status: task.StatusNext, MaxAttempts: &attempts, Criteria: []task.AcceptanceCriterion{criterion}}
	store := GetTaskStore(h.al)
	require.NoError(t, store.Create(tk))
	g, err := goal.New(gen.GoalOwnerKindTask, tk.ID, gen.GoalSourceTaskExplicit, tk.Prompt, "", tk.Criteria, []task.AcceptanceCriterion{{Kind: task.KindProse, Text: "report completion honestly", Author: criterion.Author}}, 2, time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.NoError(t, goal.NewStore(h.home).Create(g))
	te := GetTaskExecutor(h.al)
	if steered {
		main, err := h.al.GetSessionStore().GetOrCreateMainSession(tk.WorkspaceID, tk.AgentID)
		require.NoError(t, err)
		te.SetMainSessionResolver(crashMainResolver{id: main.ID})
	}
	progress := task.StatusInProgress
	_, err = store.Update(tk.ID, task.Patch{Status: &progress})
	require.NoError(t, err)
	sid, err := te.StartTaskNow(context.Background(), tk.ID)
	require.NoError(t, err)
	require.NotEmpty(t, sid)
	crashAwait(t, p.entered)
	done := make(chan struct{})
	go func() { te.wg.Wait(); close(done) }()
	close(gate)
	crashAwaitDone(t, done)
	current, err := store.Get(tk.ID)
	require.NoError(t, err)
	return h, current, g.GoalID
}

func TestCrashRecovery_TaskCeilingEndsExecutionWithoutSpendingGoalOrAttempt(t *testing.T) {
	for _, steered := range []bool{false, true} {
		t.Run(fmt.Sprintf("steered_%v", steered), func(t *testing.T) {
			h, tk, goalID := crashTaskCeiling(t, steered)
			assert.Equal(t, 2, len(h.p.calls()), "ceiling ends this execution: no automatic next task turn")
			assert.Equal(t, task.StatusInProgress, tk.Status)
			assert.Equal(t, 0, tk.AttemptCount, "ending an execution does not charge a task attempt")
			rec := h.record(t, tk.SessionID)
			assert.Equal(t, "interrupted", string(rec.State))
			assert.Equal(t, "tool_limit", crashFields(t, rec)["interruption_reason"])
			g, err := goal.NewStore(h.home).Get(goalID)
			require.NoError(t, err)
			assert.Equal(t, "active", string(g.State))
			assert.Equal(t, 0, g.Round)
			assert.Equal(t, 0, g.AttemptsUsed)
			runs, err := GetTaskStore(h.al).ListRuns(tk.ID)
			require.NoError(t, err)
			require.Len(t, runs, 1)
			assert.True(t, runs[0].IsOpen(), "Task/Plan/run outcomes remain separate from Interrupted execution")
			te := GetTaskExecutor(h.al)
			te.mu.Lock()
			_, running := te.running[tk.ID]
			te.mu.Unlock()
			assert.False(t, running, "ended task releases its actual execution slot")
		})
	}
}

func TestCrashRecovery_TaskReconciliationHoldDoesNotSpendGoalOrCloseRun(t *testing.T) {
	crashRequireInterrupted(t)
	h := crashNew(t, &crashScript{})
	inst := h.al.GetRegistry().primaryChatAgentForTest()
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"append_file": "allow", "goal_claim": "allow"}})
	attempts := 1
	tk := &task.Task{Title: "task with unresolved old effect", Prompt: "continue only after proof", Action: task.ActionLLM, AgentID: testDefaultAgentID, WorkspaceID: testHarnessWorkspaceMembershipID, Status: task.StatusNext, MaxAttempts: &attempts, Criteria: []task.AcceptanceCriterion{{ID: "criterion", Kind: task.KindProse, Text: "do not blindly repeat uncertain writes", Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "owner"}}}}
	store := GetTaskStore(h.al)
	require.NoError(t, store.Create(tk))
	parent, err := h.al.GetSessionStore().GetOrCreateMainSession(tk.WorkspaceID, tk.AgentID)
	require.NoError(t, err)
	launched, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent.ID, TargetAgentID: testDefaultAgentID, Task: tk.Prompt, Origin: steer.Origin{Kind: steer.OriginKindTask, TaskID: tk.ID, CallID: "task-uncertainty"}})
	require.NoError(t, err)
	progress := task.StatusInProgress
	_, err = store.Update(tk.ID, task.Patch{Status: &progress, SessionID: &launched.SessionID})
	require.NoError(t, err)
	g, err := goal.New(gen.GoalOwnerKindTask, tk.ID, gen.GoalSourceTaskExplicit, tk.Prompt, "", tk.Criteria, []task.AcceptanceCriterion{{Kind: task.KindProse, Text: "report actual outcome", Author: tk.Criteria[0].Author}}, 2, time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.NoError(t, g.Activate(launched.SessionID, time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)))
	require.NoError(t, goal.NewStore(h.home).Create(g))
	require.NoError(t, h.al.GetSessionLifecycleStore().Mutate(launched.SessionID, func(rec *session.LifecycleRecord) error { rec.GoalRef = g.GoalID; return nil }))
	run, opened, err := store.OpenRun(tk.ID, nil, task.RunKindManual, launched.SessionID, nil)
	require.NoError(t, err)
	require.True(t, opened)
	work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	path := filepath.Join(work, "task-unknown-current.txt")
	require.NoError(t, os.WriteFile(path, []byte("A\n"), 0600))
	crashAppend(t, h, launched.SessionID, providers.Message{Role: "user", Content: "old task write has no saved result"})
	crashAppend(t, h, launched.SessionID, providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{crashCall("old-task-write", "append_file", map[string]any{"path": path, "content": "A\n"})}})
	_, err = NewSteerLauncher(h.al).Dispatch(context.Background(), launched.SessionID, launched.Generation)
	require.NoError(t, err)
	te := GetTaskExecutor(h.al)
	done := make(chan struct{})
	go func() { te.wg.Wait(); close(done) }()
	crashAwaitDone(t, done)
	assert.Equal(t, 0, len(h.p.calls()), "Unknown ends the task execution before another model turn")
	ended := h.record(t, launched.SessionID)
	assert.Equal(t, "interrupted", string(ended.State))
	assert.Equal(t, "reconciliation_required", crashFields(t, ended)["interruption_reason"])
	current, err := store.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusInProgress, current.Status)
	assert.Equal(t, 0, current.AttemptCount)
	active, err := goal.NewStore(h.home).Get(g.GoalID)
	require.NoError(t, err)
	assert.Equal(t, "active", string(active.State))
	assert.Equal(t, 0, active.Round)
	assert.Equal(t, 0, active.AttemptsUsed)
	runs, err := store.ListRuns(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, []task.TaskRun{*run}, runs)
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "A\n", string(bytes))
}

func TestCrashRecovery_SteeredTaskExplicitContinuationReusesOpenRun(t *testing.T) {
	crashRequireInterrupted(t)
	h, tk, goalID := crashTaskCeiling(t, true)
	old := h.record(t, tk.SessionID)
	require.Equal(t, "interrupted", string(old.State))
	runs, err := GetTaskStore(h.al).ListRuns(tk.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.True(t, runs[0].IsOpen())
	saved := runs[0]
	for range 2 {
		h.reboot(t, &crashScript{})
		current, err := GetTaskStore(h.al).Get(tk.ID)
		require.NoError(t, err)
		assert.Equal(t, task.StatusInProgress, current.Status)
		assert.Equal(t, 0, len(h.p.calls()))
	}
	p := &crashScript{entered: make(chan int, 32)}
	work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall(fmt.Sprintf("continued-task-%d", i), "read_file", map[string]any{"path": filepath.Join(work, "task-read.txt")})}, FinishReason: "tool_calls"}, nil
	}
	h.al.GetRegistry().primaryChatAgentForTest().Provider = p
	h.al.GetRegistry().primaryChatAgentForTest().StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"read_file": "allow", "goal_claim": "allow"}})
	gen, err := NewSteerCanceller(h.al.GetSessionLifecycleStore()).Revive(context.Background(), tk.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "owner"})
	require.NoError(t, err)
	require.Equal(t, old.Generation+1, gen)
	_, err = NewSteerLauncher(h.al).Dispatch(context.Background(), tk.SessionID, gen)
	require.NoError(t, err)
	te := GetTaskExecutor(h.al)
	done := make(chan struct{})
	go func() { te.wg.Wait(); close(done) }()
	crashAwaitDone(t, done)
	after, err := GetTaskStore(h.al).ListRuns(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, []task.TaskRun{saved}, after, "existing continuation reuses the same open run, not fresh Retry")
	g, err := goal.NewStore(h.home).Get(goalID)
	require.NoError(t, err)
	assert.Equal(t, "active", string(g.State))
	assert.Equal(t, 0, g.Round)
	assert.Equal(t, 0, g.AttemptsUsed)
	fresh := h.record(t, tk.SessionID)
	assert.Equal(t, gen, fresh.Generation)
	assert.NotEqual(t, old.ExecutionID, fresh.ExecutionID)
}
