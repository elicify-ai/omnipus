//go:build !cgo

// Regression tests for #406 tenancy fixes — updated for FR-1.9 (owner attribution-only).
//
// BDD scenarios:
//   Scenario: ToolSessionOwner/WithSessionOwner round-trip
//   Scenario: cross-owner milestone FK → accepted (201); owner gate removed (FR-1.9)
//   Scenario: system.task.create Rule-2 — no workspace → task.owner == session owner
//   Scenario: multi-user: user B CAN access user A's workspace/task (FR-1.9, owner attribution-only)
//   Scenario: single-user: shared workspace accessible
//
// Traces to: project-task-management-level1-spec.md — #406 tenancy regression

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// ── #406: ToolSessionOwner/WithSessionOwner round-trip ───────────────────────

// TestToolSessionOwner_WithSessionOwner_RoundTrip asserts that WithSessionOwner
// injects a value into context that ToolSessionOwner correctly extracts.
//
// Guards against: key mismatch or empty-string leak.
// Traces to: pkg/tools/base.go WithSessionOwner / ToolSessionOwner — #406
func TestToolSessionOwner_WithSessionOwner_RoundTrip(t *testing.T) {
	tests := []struct {
		owner string
	}{
		{"alice"},
		{"bob"},
		{""},
	}
	for _, tc := range tests {
		t.Run("owner="+tc.owner, func(t *testing.T) {
			ctx := tools.WithSessionOwner(context.Background(), tc.owner)
			got := tools.ToolSessionOwner(ctx)
			assert.Equal(t, tc.owner, got,
				"ToolSessionOwner must return the value injected by WithSessionOwner")
		})
	}
}

// TestToolSessionOwner_NoContext_ReturnsEmpty asserts that ToolSessionOwner on a
// plain context (no WithSessionOwner) returns "".
//
// Traces to: pkg/tools/base.go ToolSessionOwner — #406
func TestToolSessionOwner_NoContext_ReturnsEmpty(t *testing.T) {
	got := tools.ToolSessionOwner(context.Background())
	assert.Equal(t, "", got, "ToolSessionOwner on a bare context must return empty string")
}

// ── #406 + FR-1.9: cross-owner milestone FK — owner gate removed ──────────────

// TestTask_CrossOwnerMilestoneFK_Accepted asserts that a unified task POST
// referencing another user's milestone is accepted (201) after the owner gate
// removal (FR-1.9). Owner is attribution-only; milestone existence is the only check.
//
// BDD:
//
//	Given a milestone owned by "alice" and a workspace,
//	When "bob" POSTs a unified task referencing alice's milestone_id,
//	Then 201 is returned — the milestone FK is valid (milestone exists).
//
// Guards against: regression that re-introduces an owner check on milestone FK.
// Sprint 2: uses POST /api/v1/tasks (replaces /board/tasks); "title"+"action"+"workspace_id" required.
// Traces to: pkg/gateway/rest_tasks.go validateMilestoneFK — #406, FR-1.9
func TestTask_CrossOwnerMilestoneFK_Accepted(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create a workspace bob's task can belong to.
	wsID := createWorkspaceViaAPI(t, api, "BobWorkspace", "")

	// Write a milestone owned by "alice" directly (workspace_id=wsID so the FK is reachable).
	milestoneID := "01JXMILESTONE00000000ALICE"
	milestonesDir := filepath.Join(api.homePath, "milestones")
	require.NoError(t, os.MkdirAll(milestonesDir, 0o700))
	milestoneJSON := fmt.Sprintf(
		`{"id":%q,"workspace_id":%q,"name":"Alice Milestone","owner":"alice","created_at":%q,"updated_at":%q}`,
		milestoneID,
		wsID,
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	)
	require.NoError(t, os.WriteFile(filepath.Join(milestonesDir, milestoneID+".json"), []byte(milestoneJSON), 0o600))

	// "bob" creates a task referencing alice's milestone.
	// FR-1.9: the owner gate is gone; milestone existence is the only validation.
	body := fmt.Sprintf(`{"title":"Bob Task","action":"llm","workspace_id":%q,"milestone_id":%q}`, wsID, milestoneID)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	r = r.WithContext(contextWithUserRole(r.Context(), "bob", config.UserRoleUser))
	api.agentLoop.GetConfig().Gateway.Users = []config.UserConfig{
		{Username: "alice", Role: config.UserRoleUser},
		{Username: "bob", Role: config.UserRoleUser},
	}

	api.HandleTasks(w, r)

	assert.Equal(t, http.StatusCreated, w.Code,
		"cross-owner milestone_id reference must be accepted (201) after FR-1.9 gate removal; body=%s", w.Body.String())
}

// ── #406 + FR-1.9: multi-user workspace access — owner attribution only ───────

// TestTenancy_MultiUser_CrossOwnerWorkspace_Returns200 asserts that in multi-user
// mode, user B CAN access user A's workspace after FR-1.9 (owner attribution-only).
//
// BDD:
//
//	Given a workspace owned by "alice" and multi-user mode,
//	When "bob" GETs the workspace,
//	Then 200 is returned.
//
// Guards against: residual owner gate being re-introduced on workspace GET.
// Traces to: pkg/gateway/rest_workspaces.go HandleWorkspaces FR-1.9 — #406
func TestTenancy_MultiUser_CrossOwnerWorkspace_Returns200(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create workspace as alice (admin so it can be created freely).
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"Alice Workspace"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces"
	r = r.WithContext(contextWithUserRole(r.Context(), "alice", config.UserRoleAdmin))
	api.HandleWorkspaces(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "alice must be able to create a workspace; body=%s", w.Body.String())
	var ws gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ws))

	// Enable multi-user mode by adding two users to the config.
	api.agentLoop.GetConfig().Gateway.Users = []config.UserConfig{
		{Username: "alice", Role: config.UserRoleAdmin},
		{Username: "bob", Role: config.UserRoleUser},
	}

	// bob GETs alice's workspace — must succeed (FR-1.9: owner is attribution-only).
	ww := httptest.NewRecorder()
	rr := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+ws.Id, nil)
	rr.URL.Path = "/api/v1/workspaces/" + ws.Id
	rr = rr.WithContext(contextWithUserRole(rr.Context(), "bob", config.UserRoleUser))
	api.HandleWorkspaces(ww, rr)
	assert.Equal(t, http.StatusOK, ww.Code,
		"bob must be able to GET alice's workspace in multi-user mode (FR-1.9); body=%s", ww.Body.String())
}

// TestTenancy_SingleUser_SharedWorkspace_Accessible asserts that in single-user
// mode, any authenticated user can access unowned workspaces.
//
// BDD:
//
//	Given a workspace with no owner and single-user mode,
//	When any user GETs the workspace,
//	Then 200 is returned.
//
// Guards against: over-restriction in single-user deployments.
// Traces to: pkg/gateway/rest_workspaces.go HandleWorkspaces — #406, FR-1.9
func TestTenancy_SingleUser_SharedWorkspace_Accessible(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create a workspace with no explicit owner (dev-bypass → admin, empty owner).
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		strings.NewReader(`{"name":"Shared Workspace"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces"
	api.HandleWorkspaces(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "workspace creation must succeed; body=%s", w.Body.String())
	var ws gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ws))

	// Single-user mode: only one user in config.
	api.agentLoop.GetConfig().Gateway.Users = []config.UserConfig{
		{Username: "alice", Role: config.UserRoleUser},
	}

	// alice can GET the unowned workspace in single-user mode.
	ww := httptest.NewRecorder()
	rr := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+ws.Id, nil)
	rr.URL.Path = "/api/v1/workspaces/" + ws.Id
	rr = rr.WithContext(contextWithUserRole(rr.Context(), "alice", config.UserRoleUser))
	api.HandleWorkspaces(ww, rr)
	assert.Equal(t, http.StatusOK, ww.Code,
		"alice can access unowned workspace in single-user mode; body=%s", ww.Body.String())
}

// ── #406: system.task.create Rule-2 — session owner stamp ────────────────────

// TestTaskCreateTool_Rule2_SessionOwner asserts that when system.task.create is
// called with a session owner in context, the created task's owner is set to the
// session owner (Rule-2: workspace has no owner, so session owner fills it).
//
// BDD:
//
//	Given ToolSessionOwner(ctx) == "alice" and a workspace with no owner,
//	When system.task.create is executed with the workspace_id,
//	Then the written task's owner == "alice".
//
// Guards against: Rule-2 session owner stamp being skipped or overwritten.
// Traces to: pkg/sysagent/tools/task.go TaskCreateTool.Execute Rule-2 — #406
func TestTaskCreateTool_Rule2_SessionOwner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	// Seed a default workspace with no owner so Rule-2 (session owner stamp) fires.
	// The workspace must exist on disk at <home>/workspaces/<id>.json for the tool's
	// validateID + readWorkspaceFromDisk checks to pass.
	wsID := "01JXRULE2SESSIONOWNER00001"
	wsDir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)
	wsJSON := fmt.Sprintf(
		`{"id":%q,"name":"Rule2 Workspace","status":"active","pinned":false,"pin_order":0,"is_default":true,"created_at":%q,"updated_at":%q}`,
		wsID, now, now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), []byte(wsJSON), 0o600))

	tool := systools.NewTaskCreateTool(&systools.Deps{Home: home})

	// Inject session owner "alice" via context (Rule-2).
	ctx := tools.WithSessionOwner(context.Background(), "alice")

	result := tool.Execute(ctx, map[string]any{
		"name":         "Alice's personal task",
		"workspace_id": wsID,
	})

	require.NotNil(t, result, "Execute must return a non-nil ToolResult")
	require.False(t, result.IsError, "Execute must succeed; error=%s", result.ForLLM)

	// Parse the result to extract the task id.
	var resp struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &resp),
		"result content must be valid JSON; content=%s", result.ForLLM)
	require.NotEmpty(t, resp.ID, "created task must have a non-empty id")

	// Read the task from disk and assert owner == "alice".
	taskPath := filepath.Join(home, "tasks", resp.ID+".json")
	data, err := os.ReadFile(taskPath)
	require.NoError(t, err, "task file must exist on disk")

	var diskTask struct {
		Owner string `json:"owner"`
		Title string `json:"title"`
	}
	require.NoError(t, json.Unmarshal(data, &diskTask))

	assert.Equal(t, "alice", diskTask.Owner,
		"task owner must equal the session owner (Rule-2); got %q", diskTask.Owner)
	assert.Equal(t, "Alice's personal task", diskTask.Title,
		"task title must match the input name")
}

// TestTaskCreateTool_Rule2_NoSession_OwnerEmpty asserts that when there is no
// session owner in context and the workspace has no owner, the task owner is ""
// (Rule-3: no owner source → empty attribution).
//
// Guards against: Rule-2 fabricating an owner when none was provided.
// Traces to: pkg/sysagent/tools/task.go TaskCreateTool.Execute Rule-3 — #406
func TestTaskCreateTool_Rule2_NoSession_OwnerEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	// Seed a workspace with no owner so neither the workspace nor the session
	// provides an owner — Rule-3 path: task.owner remains "".
	wsID := "01JXRULE3NOSESSIONOWNER001"
	wsDir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	now := time.Now().UTC().Format(time.RFC3339)
	wsJSON := fmt.Sprintf(
		`{"id":%q,"name":"Rule3 Workspace","status":"active","pinned":false,"pin_order":0,"is_default":true,"created_at":%q,"updated_at":%q}`,
		wsID, now, now,
	)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, wsID+".json"), []byte(wsJSON), 0o600))

	tool := systools.NewTaskCreateTool(&systools.Deps{Home: home})

	// No session owner injected (plain context).
	result := tool.Execute(context.Background(), map[string]any{
		"name":         "Unowned task",
		"workspace_id": wsID,
	})

	require.NotNil(t, result)
	require.False(t, result.IsError, "Execute must succeed; error=%s", result.ForLLM)

	var resp struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &resp))

	data, err := os.ReadFile(filepath.Join(home, "tasks", resp.ID+".json"))
	require.NoError(t, err)

	var diskTask struct {
		Owner string `json:"owner,omitempty"`
	}
	require.NoError(t, json.Unmarshal(data, &diskTask))

	assert.Equal(t, "", diskTask.Owner,
		"task owner must be empty (Rule-3) when no session owner and no workspace_id; got %q", diskTask.Owner)
}

// ── #406 + FR-1.9: multi-user unified task access — owner attribution only ────

// TestTenancy_MultiUser_CrossOwnerTask_Returns200 asserts that in multi-user
// mode, user B CAN GET/PATCH a unified task owned by user A after FR-1.9
// (owner is attribution-only; access gate removed).
//
// BDD:
//
//	Given a unified task owned by "alice" and multi-user mode,
//	When "bob" GETs or PATCHes the task,
//	Then 200 is returned.
//
// Guards against: residual owner gate being re-introduced on task paths.
// Sprint 2: uses /api/v1/tasks and PATCH (replaces /board/tasks and PUT).
// Traces to: pkg/gateway/rest_tasks.go HandleTasks FR-1.9 — #406
func TestTenancy_MultiUser_CrossOwnerTask_Returns200(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create a workspace for alice's task.
	wsID := createWorkspaceViaAPI(t, api, "AliceWorkspace_CrossOwner", "")

	// Create a task as alice (admin so POST succeeds without owner complications).
	taskName := "AliceTask_" + t.Name()
	taskBody := fmt.Sprintf(`{"title":%q,"action":"llm","workspace_id":%q,"status":"inbox"}`, taskName, wsID)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(taskBody))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	r = r.WithContext(contextWithUserRole(r.Context(), "alice", config.UserRoleAdmin))
	api.HandleTasks(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	var createdTask gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createdTask))

	// Write the task file with owner="alice" directly (the POST via dev-bypass doesn't stamp owner).
	taskPath := filepath.Join(api.homePath, "tasks", createdTask.Id+".json")
	data, err := os.ReadFile(taskPath)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	raw["owner"] = "alice"
	updated, err := json.Marshal(raw)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(taskPath, updated, 0o600))

	// Enable multi-user mode.
	api.agentLoop.GetConfig().Gateway.Users = []config.UserConfig{
		{Username: "alice", Role: config.UserRoleAdmin},
		{Username: "bob", Role: config.UserRoleUser},
	}

	// bob GETs alice's task — FR-1.9: must succeed.
	t.Run("bob_GET", func(t *testing.T) {
		ww := httptest.NewRecorder()
		rr := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+createdTask.Id, nil)
		rr.URL.Path = "/api/v1/tasks/" + createdTask.Id
		rr = rr.WithContext(contextWithUserRole(rr.Context(), "bob", config.UserRoleUser))
		api.HandleTasks(ww, rr)
		assert.Equal(t, http.StatusOK, ww.Code,
			"bob's GET on alice's task must return 200 (FR-1.9); body=%s", ww.Body.String())
	})

	// bob PATCHes alice's task — FR-1.9: must succeed.
	// Sprint 2: PATCH replaces PUT for task updates.
	// Include description so the partial-task guard allows "next".
	t.Run("bob_PATCH", func(t *testing.T) {
		ww := httptest.NewRecorder()
		rr := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+createdTask.Id,
			strings.NewReader(`{"status":"next","description":"bob's update"}`))
		rr.Header.Set("Content-Type", "application/json")
		rr.URL.Path = "/api/v1/tasks/" + createdTask.Id
		rr = rr.WithContext(contextWithUserRole(rr.Context(), "bob", config.UserRoleUser))
		api.HandleTasks(ww, rr)
		assert.Equal(t, http.StatusOK, ww.Code,
			"bob's PATCH on alice's task must return 200 (FR-1.9); body=%s", ww.Body.String())
	})
}
