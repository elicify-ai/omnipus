// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

const bindingWorkerID = "native-agent"

type bindingRunFixture struct {
	store    *task.Store
	executor *agent.TaskExecutor
	taskID   string
	fault    *bindingWriteFault
	worker   *bindingClaimWorker
	judge    *bindingMetJudge
}

func newBindingRunFixture(t *testing.T, mode bindingFaultMode, failDisposition bool) *bindingRunFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	seedBindingWorkspace(t, home)
	// Install before loop construction; LIFO cleanup closes/drains the loop
	// before restoring the package-global filesystem seam.
	fault := installBindingWriteFault(t, mode, failDisposition)
	worker := &bindingClaimWorker{}
	judge := &bindingMetJudge{}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "binding-test-model"},
			},
			List: []config.AgentConfig{
				{ID: bindingWorkerID, Name: "Binding worker", Type: config.AgentTypeWorker,
					Home: filepath.Join(home, "agents", bindingWorkerID)},
				{ID: string(coreagent.IDJudge), Name: "Judge", Type: config.AgentTypeSystem,
					Home: filepath.Join(home, "agents", string(coreagent.IDJudge))},
			},
		},
	}
	cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	// Bound the fixture to one ordinary goal round/run; a correct first claim
	// succeeds. These are not retry limits for the persistence code under test.
	cfg.Planning.GoalMaxRounds = 1
	loop, err := agent.NewAgentLoop(cfg, bus.NewMessageBus(), worker)
	if err != nil {
		t.Fatalf("binding fixture: construct real loop: %v", err)
	}
	t.Cleanup(loop.Close)
	judgeInstance, ok := loop.GetRegistry().GetAgent(string(coreagent.IDJudge))
	if !ok {
		t.Fatal("binding fixture: real Judge is not registered")
	}
	judgeInstance.Provider = judge
	store := agent.GetTaskStore(loop)
	if store == nil || store.Dir() != filepath.Join(home, "tasks") {
		t.Fatalf("binding fixture: task store not rooted in isolated application home: %v", store)
	}
	executor := agent.GetTaskExecutor(loop)
	if executor == nil {
		t.Fatal("binding fixture: real executor missing")
	}
	stored := createBindingTaskAndGoal(t, store, home)
	worker.store, worker.taskID = store, stored.ID
	fault.target(filepath.Join(store.Dir(), stored.ID+".json"))
	return &bindingRunFixture{store: store, executor: executor, taskID: stored.ID,
		fault: fault, worker: worker, judge: judge}
}

func seedBindingWorkspace(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("binding fixture: mkdir workspace records: %v", err)
	}
	record := workspace.Workspace{ID: "default", Name: "Binding test workspace", Status: "active",
		CoreTeam: []string{bindingWorkerID, string(coreagent.IDJudge)}, IsDefault: true}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("binding fixture: encode workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.json"), data, 0o600); err != nil {
		t.Fatalf("binding fixture: seed workspace: %v", err)
	}
	for _, id := range record.CoreTeam {
		if _, found := workspace.FindForAgent(home, id); !found {
			t.Fatalf("binding fixture: real workspace resolver cannot find member %q", id)
		}
	}
}

func createBindingTaskAndGoal(t *testing.T, store *task.Store, home string) *task.Task {
	t.Helper()
	one := 1 // One successful worker run suffices; do not hide the ownership failure in retries.
	tk := &task.Task{
		Title: "delegated report", Prompt: "Write report.md.", Action: task.ActionLLM,
		AgentID: bindingWorkerID, Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts: &one, DelegationDepth: 1,
		Criteria: []task.AcceptanceCriterion{bindingProseCriterion("report.md exists")},
	}
	if err := store.CreateByAgent(tk, bindingWorkerID); err != nil {
		t.Fatalf("binding fixture: create agent-authored task: %v", err)
	}
	stored, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("binding fixture: reread task: %v", err)
	}
	record, err := goal.New(generated.GoalOwnerKindTask, stored.ID, generated.GoalSourceTaskExplicit,
		stored.Prompt, "", stored.Criteria,
		[]task.AcceptanceCriterion{bindingProseCriterion("no credentials appear in report.md")},
		one, time.Now().UTC())
	if err != nil {
		t.Fatalf("binding fixture: construct task-owned goal: %v", err)
	}
	if err := goal.NewStore(home).Create(record); err != nil {
		t.Fatalf("binding fixture: persist task-owned goal: %v", err)
	}
	return stored
}

func bindingProseCriterion(text string) task.AcceptanceCriterion {
	return task.AcceptanceCriterion{Kind: task.KindProse, Text: text,
		Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: bindingWorkerID}}
}

func (f *bindingRunFixture) executeAndDrain(t *testing.T) (*task.Task, error) {
	t.Helper()
	// Drain itself has a log-only timeout. Put its budget AFTER the test
	// binary's deadline so it cannot return a timeout mistaken for completion.
	// Unlike loop.Close, Drain does not cancel worker context or stop turn intake.
	deadline, ok := t.Deadline()
	if !ok {
		t.Fatal("binding fixture requires a go test timeout to verify complete draining")
	}
	err := f.executor.ExecuteTask(context.Background(), f.taskID, nil)
	f.executor.Drain(time.Until(deadline) + time.Minute)
	stored, readErr := f.store.Get(f.taskID)
	if readErr != nil {
		t.Fatalf("binding fixture: read task after real executor drained: %v", readErr)
	}
	return stored, err
}
