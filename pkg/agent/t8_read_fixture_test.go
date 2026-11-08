package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

type t8ReadFixture struct {
	al   *AgentLoop
	ps   *plan.Store
	ts   *task.Store
	gs   *goal.Store
	home string
}

func t8ReadLoop(t *testing.T) *t8ReadFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "test-model"}}}}
	for _, id := range []string{"reader-a", "reader-b", "reader-c"} {
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", id)})
	}
	cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(al.Close)
	ps := plan.New(filepath.Join(home, "plans"))
	al.SetPlanStore(ps)
	return &t8ReadFixture{al: al, ps: ps, ts: al.taskStore, gs: tools.GoalStoreForTasks(al.taskStore), home: home}
}
func (f *t8ReadFixture) call(t *testing.T, caller, ws, name string, args map[string]any) *tools.ToolResult {
	t.Helper()
	inst, ok := f.al.GetRegistry().GetAgent(caller)
	require.True(t, ok, "real caller registered")
	ctx := tools.WithTranscriptSessionID(tools.WithSessionKey(tools.WithAgentID(context.Background(), caller), "t8-read-session"), "t8-read-session")
	if ws != "" {
		ctx = tools.WithWorkspaceID(ctx, ws)
	}
	return t8RegisteredCall(t, inst, ctx, name, args)
}
func t8ReadObject(t *testing.T, r *tools.ToolResult) map[string]any {
	t.Helper()
	require.NotNil(t, r)
	require.False(t, r.IsError, "read succeeds: %s", r.ForLLM)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.ForLLM), &out), "reader emits JSON, not prose: %s", r.ForLLM)
	return out
}
func (f *t8ReadFixture) task(t *testing.T, id, creator, assignee, ws string) *task.Task {
	t.Helper()
	tk := &task.Task{ID: id, Title: "T8 visible task", Prompt: "the stored instruction", Action: task.ActionLLM, Status: task.StatusInbox, WorkspaceID: ws, AgentID: assignee, CreatedBy: creator, CreatedByAgentID: creator}
	if creator == "" {
		tk.CreatedBy = "person"
	}
	require.NoError(t, f.ts.Create(tk))
	return tk
}
func (f *t8ReadFixture) plan(t *testing.T, id, owner, creator, kind, ws string) *plan.Plan {
	t.Helper()
	p := &plan.Plan{ID: id, Title: "T8 visible plan", Objective: "objective", WorkspaceID: ws, State: plan.StateDraft, OwnerAgentID: owner, CreatedBy: creator, CreatedByKind: kind}
	require.NoError(t, f.ps.Create(p))
	return p
}
func (f *t8ReadFixture) taskGoal(t *testing.T, tk *task.Task) *goal.Goal {
	t.Helper()
	criterion := task.AcceptanceCriterion{Kind: task.KindProse, Text: "the result was verified", Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "reader-a"}}
	g, err := goal.New(gen.GoalOwnerKindTask, tk.ID, gen.GoalSourceTaskExplicit, "goal prompt", "", []task.AcceptanceCriterion{criterion}, []task.AcceptanceCriterion{criterion}, 3, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, f.gs.Create(g))
	return g
}
