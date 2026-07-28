// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// This file pins the OUTCOMES of wiring FR-037's write path (Store.CreateByAgent)
// into the agent tools, and of closing the list_tasks empty-principal leak.
// Every assertion is about persisted/returned data, never about the presence of
// a setter — a call site that compiles against CreateByAgent but is never
// reached would pass a "setter exists" test and fail every test here.

// newProvenanceTool builds a create_task tool with the delegation gate opened
// (this file's concern is provenance, not the trust set) and a bound workspace.
func newProvenanceTool(t *testing.T, store *task.Store) *TaskCreateTool {
	t.Helper()
	tool := NewTaskCreateTool(store)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
	return tool
}

// TestCreateTask_AgentPathStamps_RESTPathDoesNot proves the whole point of the
// field: a task created THROUGH THE AGENT TOOL carries the calling agent's id in
// created_by_agent_id, while a task created the way REST creates one (plain
// Store.Create, a human username in the mixed-namespace CreatedBy) carries
// nothing — even when that username collides exactly with the agent id.
func TestCreateTask_AgentPathStamps_RESTPathDoesNot(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := newProvenanceTool(t, store)

	// The REST/human path: plain Create, username in CreatedBy, colliding with
	// the agent id used below.
	human := &task.Task{
		Title:       "Human's private task",
		Action:      task.ActionLLM,
		Status:      task.StatusInbox,
		WorkspaceID: "ws-1",
		Owner:       "mia",
		CreatedBy:   "mia",
	}
	if err := store.Create(human); err != nil {
		t.Fatalf("seed REST-created task: %v", err)
	}

	ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1")
	res := tool.Execute(ctx, map[string]any{
		"title":    "Agent-created task",
		"prompt":   "do the work",
		"agent_id": "jim",
		"criteria": validCriteriaArg(),
	})
	if res.IsError {
		t.Fatalf("create_task failed: %s", res.ForLLM)
	}

	rows, err := store.List(task.Filter{WorkspaceID: "ws-1"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var agentTask, humanTask *task.Task
	for i := range rows {
		switch rows[i].Title {
		case "Agent-created task":
			agentTask = &rows[i]
		case "Human's private task":
			humanTask = &rows[i]
		}
	}
	if agentTask == nil || humanTask == nil {
		t.Fatalf("expected both tasks on disk, got %d rows", len(rows))
	}

	if agentTask.CreatedByAgentID != "mia" {
		t.Errorf("agent-created task: created_by_agent_id = %q, want %q — the create_task "+
			"tool is not writing FR-037 provenance", agentTask.CreatedByAgentID, "mia")
	}
	if humanTask.CreatedByAgentID != "" {
		t.Errorf("REST-created task must stay unattributed, got created_by_agent_id = %q",
			humanTask.CreatedByAgentID)
	}
	if humanTask.CreatedByAgent("mia") {
		t.Error("a human's task must never be attributed to an agent whose id equals the username")
	}
}

// TestCreateTask_NoPrincipal_RefusesAndPersistsNothing proves create_task fails
// CLOSED when the calling agent cannot be resolved: an error, and zero tasks on
// disk. Writing an unattributed delegated task would produce a record no agent
// can ever find again.
func TestCreateTask_NoPrincipal_RefusesAndPersistsNothing(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := newProvenanceTool(t, store)

	// Workspace bound, everything else valid — ONLY the agent id is missing.
	ctx := WithWorkspaceID(context.Background(), "ws-1")
	res := tool.Execute(ctx, map[string]any{
		"title":    "orphan",
		"prompt":   "do the work",
		"agent_id": "jim",
		"criteria": validCriteriaArg(),
	})
	if !res.IsError {
		t.Fatalf("expected an error when the calling agent cannot be resolved, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "cannot resolve the calling agent") {
		t.Errorf("unexpected error message: %s", res.ForLLM)
	}

	rows, err := store.List(task.Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected zero tasks persisted on an unresolvable principal, got %d", len(rows))
	}
}

// seedTwoAgentsAcrossWorkspaces writes four tasks: two owned/created by "mia"
// and two by "jim", spread over two workspaces, plus one REST-created task whose
// username collides with "mia".
func seedTwoAgentsAcrossWorkspaces(t *testing.T, store *task.Store) {
	t.Helper()
	seed := []*task.Task{
		{Title: "MIA-ASSIGNED", Action: task.ActionLLM, Status: task.StatusNext, WorkspaceID: "ws-1", AgentID: "mia"},
		{Title: "JIM-ASSIGNED-SECRET", Action: task.ActionLLM, Status: task.StatusNext, WorkspaceID: "ws-2", AgentID: "jim"},
	}
	for _, tk := range seed {
		if err := store.Create(tk); err != nil {
			t.Fatalf("seed %q: %v", tk.Title, err)
		}
	}
	byAgent := []struct {
		tk    *task.Task
		agent string
	}{
		{&task.Task{Title: "MIA-DELEGATED", Action: task.ActionLLM, Status: task.StatusNext, WorkspaceID: "ws-1", AgentID: "jim"}, "mia"},
		{&task.Task{Title: "JIM-DELEGATED-SECRET", Action: task.ActionLLM, Status: task.StatusNext, WorkspaceID: "ws-2", AgentID: "mia"}, "jim"},
	}
	for _, s := range byAgent {
		if err := store.CreateByAgent(s.tk, s.agent); err != nil {
			t.Fatalf("seed %q: %v", s.tk.Title, err)
		}
	}
	// A human whose username collides with the agent id "mia".
	human := &task.Task{
		Title: "HUMAN-PRIVATE-SECRET", Action: task.ActionLLM, Status: task.StatusNext,
		WorkspaceID: "ws-3", Owner: "mia", CreatedBy: "mia",
	}
	if err := store.Create(human); err != nil {
		t.Fatalf("seed human task: %v", err)
	}
}

// TestListTasks_EmptyPrincipal_ReturnsErrorAndZeroRows proves the disclosure is
// closed: with no resolvable calling agent, list_tasks returns an ERROR and no
// rows at all — not the entire store. task.Filter treats every empty field as
// "filter off", so before this fix an unguarded empty agent id returned every
// task in every workspace.
func TestListTasks_EmptyPrincipal_ReturnsErrorAndZeroRows(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	seedTwoAgentsAcrossWorkspaces(t, store)
	tool := NewTaskListTool(store)

	// Sanity: the store really does hold other agents' work, so a leak would
	// have something to leak.
	all, err := store.List(task.Filter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("expected 5 seeded tasks, got %d", len(all))
	}

	for _, role := range []string{"assignee", "delegator"} {
		// No WithAgentID on the context at all, and a whitespace-only id — both
		// must be treated as "no principal".
		for name, ctx := range map[string]context.Context{
			"absent":     context.Background(),
			"whitespace": WithAgentID(context.Background(), "   "),
		} {
			res := tool.Execute(ctx, map[string]any{"role": role})
			if !res.IsError {
				t.Fatalf("role=%s ctx=%s: expected an error for an empty principal, got: %s",
					role, name, res.ForLLM)
			}
			for _, secret := range []string{
				"JIM-ASSIGNED-SECRET", "JIM-DELEGATED-SECRET", "HUMAN-PRIVATE-SECRET",
				"MIA-ASSIGNED", "MIA-DELEGATED",
			} {
				if strings.Contains(res.ForLLM, secret) {
					t.Errorf("role=%s ctx=%s: task title %q leaked into the tool result: %s",
						role, name, secret, res.ForLLM)
				}
			}
		}
	}
}

// TestListTasks_ScopedToCallingAgent proves the non-empty path still returns
// exactly the caller's own rows and nobody else's, for both roles.
func TestListTasks_ScopedToCallingAgent(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	seedTwoAgentsAcrossWorkspaces(t, store)
	tool := NewTaskListTool(store)
	ctx := WithAgentID(context.Background(), "mia")

	titlesFor := func(role string) []string {
		t.Helper()
		res := tool.Execute(ctx, map[string]any{"role": role})
		if res.IsError {
			t.Fatalf("role=%s: %s", role, res.ForLLM)
		}
		var rows []task.Task
		if err := json.Unmarshal([]byte(res.ForLLM), &rows); err != nil {
			t.Fatalf("role=%s: unmarshal %q: %v", role, res.ForLLM, err)
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Title)
		}
		return out
	}

	assignee := titlesFor("assignee")
	if len(assignee) != 2 {
		t.Errorf("role=assignee: expected mia's 2 assigned tasks, got %v", assignee)
	}
	for _, got := range assignee {
		if got != "MIA-ASSIGNED" && got != "JIM-DELEGATED-SECRET" {
			t.Errorf("role=assignee returned a task not assigned to mia: %q", got)
		}
	}

	// role=delegator filters on created_by_agent_id, NOT the mixed-namespace
	// created_by — so the human "mia"'s task must NOT appear.
	delegator := titlesFor("delegator")
	if len(delegator) != 1 || delegator[0] != "MIA-DELEGATED" {
		t.Errorf("role=delegator: expected exactly [MIA-DELEGATED], got %v", delegator)
	}
	for _, got := range delegator {
		if got == "HUMAN-PRIVATE-SECRET" {
			t.Error("role=delegator disclosed a human's task to an agent whose id equals the username " +
				"— it is still filtering on the mixed-namespace created_by")
		}
	}
}

// TestSetTodos_StampsProvenanceOnNewCard proves the scratchpad card set_todos
// creates carries FR-037 provenance too, so it is findable by its author.
func TestSetTodos_StampsProvenanceOnNewCard(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1")
	res := tool.Execute(ctx, map[string]any{
		"goal":  "ship the thing",
		"todos": []any{map[string]any{"text": "step one", "status": "pending"}},
	})
	if res.IsError {
		t.Fatalf("set_todos failed: %s", res.ForLLM)
	}

	rows, err := store.List(task.Filter{CreatedByAgentID: "mia"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected the scratchpad card to be findable by created_by_agent_id=mia, got %d rows", len(rows))
	}
	if rows[0].Title != "ship the thing" {
		t.Errorf("unexpected card title %q", rows[0].Title)
	}
}
