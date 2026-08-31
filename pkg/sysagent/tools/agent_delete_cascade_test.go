// Omnipus — System Agent Tool Tests: delete_agent cascade (F8)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// TestAgentDelete_RefusesDefaultAgent proves the F8 guard: an agent that is
// the configured default agent (cfg.Agents.Defaults.DefaultAgentID) cannot
// be deleted, and the guard runs BEFORE any destructive action — the entity
// record must survive a rejected delete.
func TestAgentDelete_RefusesDefaultAgent(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	store := agentstore.New(home)
	if err := store.Create("default-agent", &config.AgentConfig{
		ID:   "default-agent",
		Name: "Default Agent",
	}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}
	deps.GetCfg().Agents.Defaults.DefaultAgentID = "default-agent"

	result := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      "default-agent",
		"confirm": true,
	})
	if !result.IsError {
		t.Fatal("expected error when deleting the configured default agent, got success")
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "AGENT_IS_DEFAULT" {
		t.Errorf("expected error code AGENT_IS_DEFAULT, got %v", errBlock["code"])
	}
	if _, err := store.Get("default-agent"); err != nil {
		t.Errorf("default agent entity record must survive a rejected delete, Get error = %v", err)
	}
}

// TestAgentDelete_CascadeDeletesSoleOwnedSessionAndUploads proves that
// delete_agent removes a session in the shared session store that belongs
// SOLELY to the deleted agent, together with its uploads directory.
func TestAgentDelete_CascadeDeletesSoleOwnedSessionAndUploads(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	store := agentstore.New(home)
	if err := store.Create("victim", &config.AgentConfig{ID: "victim", Name: "Victim"}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}

	sessStore, storeErr := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if storeErr != nil {
		t.Fatalf("test setup: open session store: %v", storeErr)
	}
	meta, metaErr := sessStore.NewSession(session.SessionTypeChat, "webchat", "victim")
	if metaErr != nil {
		t.Fatalf("test setup: create session: %v", metaErr)
	}
	sessionID := meta.ID

	// Seed an upload file for this session, exactly as a real upload would land.
	uploadDir := filepath.Join(home, "uploads", sessionID)
	if err := os.MkdirAll(uploadDir, 0o700); err != nil {
		t.Fatalf("test setup: mkdir uploads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(uploadDir, "file.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("test setup: write upload file: %v", err)
	}
	if err := sessStore.Close(); err != nil {
		t.Fatalf("test setup: close session store: %v", err)
	}

	result := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      "victim",
		"confirm": true,
	})
	if result.IsError {
		t.Fatalf("delete failed: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	if n, _ := body["sessions_deleted"].(float64); n != 1 {
		t.Errorf("sessions_deleted = %v, want 1", body["sessions_deleted"])
	}
	if warn, ok := body["cascade_warnings"]; ok {
		t.Errorf("unexpected cascade_warnings: %v", warn)
	}

	sessionDir := filepath.Join(home, "sessions", sessionID)
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Errorf("session directory %s still present after cascade delete (err=%v)", sessionDir, err)
	}
	if _, err := os.Stat(uploadDir); !os.IsNotExist(err) {
		t.Errorf("upload directory %s still present after cascade delete (err=%v)", uploadDir, err)
	}
}

// TestAgentDelete_PreservesSharedSession proves the deliberately conservative
// choice: a session shared with another (still-live) agent is NOT deleted —
// only sole-owned sessions are. Deleting a whole shared conversation because
// one of its participants is being removed would destroy the other agent's
// history, which this operation must not do.
func TestAgentDelete_PreservesSharedSession(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	store := agentstore.New(home)
	if err := store.Create("victim", &config.AgentConfig{ID: "victim", Name: "Victim"}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}
	if err := store.Create("survivor", &config.AgentConfig{ID: "survivor", Name: "Survivor"}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}

	sessStore, storeErr := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if storeErr != nil {
		t.Fatalf("test setup: open session store: %v", storeErr)
	}
	meta, metaErr := sessStore.NewSession(session.SessionTypeChat, "webchat", "victim")
	if metaErr != nil {
		t.Fatalf("test setup: create session: %v", metaErr)
	}
	sessionID := meta.ID
	// A mid-conversation agent switch makes this a MULTI-agent (joined)
	// session: AgentIDs becomes ["victim","survivor"].
	if err := sessStore.SwitchAgent(sessionID, "survivor"); err != nil {
		t.Fatalf("test setup: switch agent: %v", err)
	}
	if err := sessStore.Close(); err != nil {
		t.Fatalf("test setup: close session store: %v", err)
	}

	result := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      "victim",
		"confirm": true,
	})
	if result.IsError {
		t.Fatalf("delete failed: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	if n, _ := body["sessions_deleted"].(float64); n != 0 {
		t.Errorf("sessions_deleted = %v, want 0 (shared session must not be deleted)", body["sessions_deleted"])
	}
	if n, _ := body["sessions_preserved_shared"].(float64); n != 1 {
		t.Errorf("sessions_preserved_shared = %v, want 1", body["sessions_preserved_shared"])
	}

	sessionDir := filepath.Join(home, "sessions", sessionID)
	if _, err := os.Stat(sessionDir); err != nil {
		t.Errorf("shared session directory %s must survive, stat error = %v", sessionDir, err)
	}
}

// TestAgentDelete_UnassignsTasksButPreservesCreatedByAttribution proves the
// task-cascade policy: a task ASSIGNED to the deleted agent is unassigned
// (AgentID cleared) but not deleted, while a task the agent merely CREATED
// keeps that historical attribution untouched.
func TestAgentDelete_UnassignsTasksButPreservesCreatedByAttribution(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	store := agentstore.New(home)
	if err := store.Create("victim", &config.AgentConfig{ID: "victim", Name: "Victim"}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}

	tasksDir := filepath.Join(home, "tasks")
	if err := os.MkdirAll(tasksDir, 0o700); err != nil {
		t.Fatalf("test setup: mkdir tasks: %v", err)
	}
	writeTask := func(id string, extra map[string]any) {
		t.Helper()
		data := map[string]any{
			"id":           id,
			"title":        "Task " + id,
			"status":       "inbox",
			"workspace_id": "some-ws",
			"created_at":   "2026-01-01T00:00:00Z",
			"updated_at":   "2026-01-01T00:00:00Z",
		}
		for k, v := range extra {
			data[k] = v
		}
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("marshal task %s: %v", id, err)
		}
		if err := os.WriteFile(filepath.Join(tasksDir, id+".json"), raw, 0o600); err != nil {
			t.Fatalf("write task %s: %v", id, err)
		}
	}
	assignedTaskID := "01JZ00000000000000000AS01"
	createdTaskID := "01JZ00000000000000000CR01"
	writeTask(assignedTaskID, map[string]any{"agent_id": "victim"})
	writeTask(createdTaskID, map[string]any{"created_by_agent_id": "victim"})

	result := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      "victim",
		"confirm": true,
	})
	if result.IsError {
		t.Fatalf("delete failed: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	if n, _ := body["tasks_unassigned"].(float64); n != 1 {
		t.Errorf("tasks_unassigned = %v, want 1", body["tasks_unassigned"])
	}

	readTask := func(id string) map[string]any {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(tasksDir, id+".json"))
		if err != nil {
			t.Fatalf("read task %s: %v", id, err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal task %s: %v", id, err)
		}
		return m
	}

	assigned := readTask(assignedTaskID)
	if v, ok := assigned["agent_id"]; ok && v != "" {
		t.Errorf("assigned task's agent_id = %v, want cleared (empty/absent)", v)
	}

	created := readTask(createdTaskID)
	if created["created_by_agent_id"] != "victim" {
		t.Errorf("created task's created_by_agent_id = %v, want unchanged %q (historical attribution "+
			"must survive)", created["created_by_agent_id"], "victim")
	}
}

// TestAgentDelete_CleansWorkspaceCoreTeamAndDelegationEdges proves the
// dangling-reference cascade: the deleted agent is removed from every
// workspace's core_team, and every delegation edge naming it (from either
// side) is dropped — while an edge that does NOT name it survives untouched.
func TestAgentDelete_CleansWorkspaceCoreTeamAndDelegationEdges(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	store := agentstore.New(home)
	if err := store.Create("victim", &config.AgentConfig{ID: "victim", Name: "Victim"}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}

	wsID := "01KW00000000000000000WS01"
	wsDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(wsDir, 0o700); err != nil {
		t.Fatalf("test setup: mkdir workspaces: %v", err)
	}
	wsPath := filepath.Join(wsDir, wsID+".json")
	wsJSON := `{
		"id": "` + wsID + `",
		"name": "Cascade WS",
		"status": "active",
		"core_team": ["victim", "keeper"],
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(wsJSON), 0o600); err != nil {
		t.Fatalf("test setup: write workspace: %v", err)
	}
	seedDelegationStoreForTest(t, home, wsID, `[
		{"from_agent":"victim","to_agent":"keeper","modes":["task"]},
		{"from_agent":"keeper","to_agent":"victim","modes":["task"]},
		{"from_agent":"keeper","to_agent":"keeper2","modes":["task"]}
	]`)

	result := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      "victim",
		"confirm": true,
	})
	if result.IsError {
		t.Fatalf("delete failed: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	if n, _ := body["workspaces_updated"].(float64); n != 1 {
		t.Errorf("workspaces_updated = %v, want 1", body["workspaces_updated"])
	}
	if n, _ := body["delegation_edges_removed"].(float64); n != 2 {
		t.Errorf("delegation_edges_removed = %v, want 2", body["delegation_edges_removed"])
	}

	data, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read workspace after delete: %v", err)
	}
	var wsData map[string]any
	if err := json.Unmarshal(data, &wsData); err != nil {
		t.Fatalf("unmarshal workspace: %v", err)
	}
	team, _ := wsData["core_team"].([]any)
	for _, m := range team {
		if m == "victim" {
			t.Errorf("workspace core_team still names deleted agent: %v", team)
		}
	}
	found := false
	for _, m := range team {
		if m == "keeper" {
			found = true
		}
	}
	if !found {
		t.Errorf("workspace core_team lost an unrelated member: %v", team)
	}

	edges := delegationEdgesFromDisk(t, home, wsID)
	if len(edges) != 1 {
		t.Fatalf("delegation edges after cascade = %v, want exactly 1 surviving edge", edges)
	}
	if edges[0]["from_agent"] != "keeper" || edges[0]["to_agent"] != "keeper2" {
		t.Errorf("unexpected surviving edge: %v", edges[0])
	}
}

// TestAgentDelete_StoreDeleteFailure_NoDestructiveCascade proves the bug-1
// reorder fix: store.Delete(id) — the authoritative entity-record delete —
// must run BEFORE the irreversible sessions/tasks cascade. This forces
// store.Delete to fail (by replacing the agent's entity JSON file with a
// non-empty directory, so the underlying os.Remove fails with ENOTEMPTY —
// uid-independent, unlike a permission-bit trick, which root ignores; see
// pkg/agent/plan_engine_test.go's TestPlanEngine_HasActivePlansOwnedBy_
// FailsClosedOnStoreError for the same technique's rationale) and asserts
// that a sole-owned session and an assigned task BOTH survive the rejected
// delete untouched. Before the fix, the cascade ran first: a subsequent
// store.Delete failure was reported as a bare SAVE_FAILED while the session
// and task were already destroyed/unassigned — directly contradicting this
// tool's own Description() promise that "a step that fails partway through
// is reported in the response rather than silently swallowed".
func TestAgentDelete_StoreDeleteFailure_NoDestructiveCascade(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	store := agentstore.New(home)
	if err := store.Create("victim", &config.AgentConfig{ID: "victim", Name: "Victim"}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}

	sessStore, storeErr := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if storeErr != nil {
		t.Fatalf("test setup: open session store: %v", storeErr)
	}
	meta, metaErr := sessStore.NewSession(session.SessionTypeChat, "webchat", "victim")
	if metaErr != nil {
		t.Fatalf("test setup: create session: %v", metaErr)
	}
	sessionID := meta.ID
	if err := sessStore.Close(); err != nil {
		t.Fatalf("test setup: close session store: %v", err)
	}

	tasksDir := filepath.Join(home, "tasks")
	if err := os.MkdirAll(tasksDir, 0o700); err != nil {
		t.Fatalf("test setup: mkdir tasks: %v", err)
	}
	taskID := "01JZ00000000000000000RD01"
	taskJSON := `{
		"id": "` + taskID + `",
		"title": "Reorder Task",
		"status": "inbox",
		"workspace_id": "some-ws",
		"agent_id": "victim",
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(filepath.Join(tasksDir, taskID+".json"), []byte(taskJSON), 0o600); err != nil {
		t.Fatalf("test setup: write task: %v", err)
	}

	// Force store.Delete("victim") to fail: replace the entity's data file
	// with a non-empty directory so the underlying os.Remove fails with
	// ENOTEMPTY regardless of the running user's uid.
	entityPath := filepath.Join(home, "entities", "agents", "victim.json")
	if err := os.Remove(entityPath); err != nil {
		t.Fatalf("test setup: remove entity file: %v", err)
	}
	if err := os.MkdirAll(entityPath, 0o700); err != nil {
		t.Fatalf("test setup: mkdir in place of entity file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entityPath, "blocker.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("test setup: seed blocker file: %v", err)
	}

	result := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      "victim",
		"confirm": true,
	})
	if !result.IsError {
		t.Fatalf("expected error when store.Delete fails, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "SAVE_FAILED" {
		t.Errorf("expected error code SAVE_FAILED, got %v", errBlock["code"])
	}

	// The sole-owned session must survive: it must not have been deleted
	// before the (failed) authoritative entity delete.
	sessionDir := filepath.Join(home, "sessions", sessionID)
	if _, err := os.Stat(sessionDir); err != nil {
		t.Errorf("session directory %s must survive a rejected delete, stat error = %v", sessionDir, err)
	}

	// The task's agent_id must still be "victim": it must not have been
	// unassigned before the (failed) authoritative entity delete.
	data, err := os.ReadFile(filepath.Join(tasksDir, taskID+".json"))
	if err != nil {
		t.Fatalf("read task after rejected delete: %v", err)
	}
	var taskData map[string]any
	if err := json.Unmarshal(data, &taskData); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	if taskData["agent_id"] != "victim" {
		t.Errorf("task agent_id = %v, want unchanged %q (must survive a rejected delete)",
			taskData["agent_id"], "victim")
	}
}

// TestAgentDelete_RefusesAgentOwningActivePlan proves the bug-2 guard,
// ported from the REST deleteAgent handler (pkg/gateway/rest.go,
// "agent_owns_active_plans"): an agent that owns at least one State=running
// Plan cannot be deleted, and the guard runs BEFORE any destructive action —
// the entity record must survive the rejected delete.
func TestAgentDelete_RefusesAgentOwningActivePlan(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	store := agentstore.New(home)
	if err := store.Create("plan-owner", &config.AgentConfig{ID: "plan-owner", Name: "Plan Owner"}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}

	planStore := plan.New(filepath.Join(home, "plans"))
	if err := planStore.Create(&plan.Plan{
		ID:           "01JZ00000000000000000PL01",
		Title:        "Active Plan",
		WorkspaceID:  "some-ws",
		OwnerAgentID: "plan-owner",
		State:        plan.StateRunning,
	}); err != nil {
		t.Fatalf("test setup: create running plan: %v", err)
	}
	deps.PlanStore = planStore

	result := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      "plan-owner",
		"confirm": true,
	})
	if !result.IsError {
		t.Fatalf("expected error when deleting an agent that owns an active plan, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "AGENT_OWNS_ACTIVE_PLANS" {
		t.Errorf("expected error code AGENT_OWNS_ACTIVE_PLANS, got %v", errBlock["code"])
	}
	if _, err := store.Get("plan-owner"); err != nil {
		t.Errorf("plan-owning agent's entity record must survive a rejected delete, Get error = %v", err)
	}
}

// TestAgentDelete_UnassignsWorkflowStatusTaskReference proves the
// isValidTaskStatus-filter removal: cascadeUnassignAgentTasks must clear a
// dangling agent_id reference from a task regardless of its status string,
// not just the six canonical GTD statuses. A task record sitting in some
// other ("workflow") status is exactly the case the filter used to skip,
// silently leaving a reference to the just-deleted agent on disk.
func TestAgentDelete_UnassignsWorkflowStatusTaskReference(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	store := agentstore.New(home)
	if err := store.Create("victim", &config.AgentConfig{ID: "victim", Name: "Victim"}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}

	tasksDir := filepath.Join(home, "tasks")
	if err := os.MkdirAll(tasksDir, 0o700); err != nil {
		t.Fatalf("test setup: mkdir tasks: %v", err)
	}
	taskID := "01JZ00000000000000000WF01"
	taskJSON := `{
		"id": "` + taskID + `",
		"title": "Workflow Status Task",
		"status": "workflow_review",
		"workspace_id": "some-ws",
		"agent_id": "victim",
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(filepath.Join(tasksDir, taskID+".json"), []byte(taskJSON), 0o600); err != nil {
		t.Fatalf("test setup: write task: %v", err)
	}

	result := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      "victim",
		"confirm": true,
	})
	if result.IsError {
		t.Fatalf("delete failed: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	if n, _ := body["tasks_unassigned"].(float64); n != 1 {
		t.Errorf("tasks_unassigned = %v, want 1 (workflow-status task must still be unassigned)", body["tasks_unassigned"])
	}

	data, err := os.ReadFile(filepath.Join(tasksDir, taskID+".json"))
	if err != nil {
		t.Fatalf("read task after delete: %v", err)
	}
	var taskData map[string]any
	if err := json.Unmarshal(data, &taskData); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	if v, ok := taskData["agent_id"]; ok && v != "" {
		t.Errorf("workflow-status task's agent_id = %v, want cleared (empty/absent)", v)
	}
	// The task's non-canonical status is untouched — only the dangling
	// agent reference was cleared.
	if taskData["status"] != "workflow_review" {
		t.Errorf("task status = %v, want unchanged %q", taskData["status"], "workflow_review")
	}
}
