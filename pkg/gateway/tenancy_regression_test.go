//go:build !cgo

// Regression tests for #406 tenancy fixes.
//
// BDD scenarios:
//   Scenario: canAccess table — admin, owner==caller, owner!=caller, owner=="" multi-user, owner=="" single-user
//   Scenario: ToolSessionOwner/WithSessionOwner round-trip
//   Scenario: cross-owner milestone FK → "milestone not found"
//   Scenario: system.task.create Rule-2 — no project → task.owner == session owner
//   Scenario: multi-user: user B cannot access user A's project/task; single-user: shared
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

// ── #406: canAccess table test ────────────────────────────────────────────────

// TestCanAccess_Table drives the full caller.canAccess rule table:
//
//	Admin caller → all resources accessible regardless of owner.
//	Owner == caller → allow.
//	Owner != caller → deny.
//	Owner == "" with MultiUser=false → allow (back-compat).
//	Owner == "" with MultiUser=true, non-admin → deny (SEC-2 multi-user gate).
//
// Guards against: regressions in the five access-control branches.
// Traces to: pkg/gateway/rest_projects.go caller.canAccess — #406
func TestCanAccess_Table(t *testing.T) {
	tests := []struct {
		name          string
		callerUser    string
		callerRole    config.UserRole
		multiUser     bool
		resourceOwner string
		wantAccess    bool
	}{
		{
			name:          "admin can access any owner",
			callerUser:    "admin",
			callerRole:    config.UserRoleAdmin,
			multiUser:     true,
			resourceOwner: "alice",
			wantAccess:    true,
		},
		{
			name:          "admin can access unowned resource in multi-user",
			callerUser:    "admin",
			callerRole:    config.UserRoleAdmin,
			multiUser:     true,
			resourceOwner: "",
			wantAccess:    true,
		},
		{
			name:          "owner==caller → allow",
			callerUser:    "alice",
			callerRole:    config.UserRoleUser,
			multiUser:     true,
			resourceOwner: "alice",
			wantAccess:    true,
		},
		{
			name:          "owner!=caller → deny",
			callerUser:    "bob",
			callerRole:    config.UserRoleUser,
			multiUser:     true,
			resourceOwner: "alice",
			wantAccess:    false,
		},
		{
			name:          "unowned with MultiUser=false → allow (back-compat)",
			callerUser:    "alice",
			callerRole:    config.UserRoleUser,
			multiUser:     false,
			resourceOwner: "",
			wantAccess:    true,
		},
		{
			name:          "unowned with MultiUser=true, non-admin → deny",
			callerUser:    "alice",
			callerRole:    config.UserRoleUser,
			multiUser:     true,
			resourceOwner: "",
			wantAccess:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := caller{
				Username:  tc.callerUser,
				Role:      tc.callerRole,
				MultiUser: tc.multiUser,
			}
			got := c.canAccess(tc.resourceOwner)
			assert.Equal(t, tc.wantAccess, got,
				"canAccess(%q) for caller=%+v must be %v", tc.resourceOwner, c, tc.wantAccess)
		})
	}
}

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

// ── #406: cross-owner milestone FK → "milestone not found" ───────────────────

// TestBoardTask_CrossOwnerMilestoneFK_MilestoneNotFound asserts that a board task
// POST referencing another user's milestone (with no project_id) returns a 400
// with "milestone not found" — the anti-enumeration oracle fix.
//
// BDD:
//
//	Given a milestone owned by "alice",
//	When "bob" (non-admin) POSTs a task referencing alice's milestone_id,
//	Then 400 is returned with "milestone not found" (not "access denied").
//
// Guards against: the old oracle where different callers got different HTTP codes.
// Traces to: pkg/gateway/rest_board.go validateMilestoneFK — #406
func TestBoardTask_CrossOwnerMilestoneFK_MilestoneNotFound(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Write a milestone owned by "alice" directly.
	milestoneID := "01JXMILESTONE00000000ALICE"
	milestonesDir := filepath.Join(api.homePath, "milestones")
	require.NoError(t, os.MkdirAll(milestonesDir, 0o700))
	milestoneJSON := fmt.Sprintf(
		`{"id":%q,"project_id":"","name":"Alice Milestone","owner":"alice","created_at":%q,"updated_at":%q}`,
		milestoneID,
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	)
	require.NoError(t, os.WriteFile(filepath.Join(milestonesDir, milestoneID+".json"), []byte(milestoneJSON), 0o600))

	// "bob" attempts to create a task referencing alice's milestone (no project_id).
	body := fmt.Sprintf(`{"name":"Bob Task","milestone_id":%q}`, milestoneID)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/board/tasks"
	// Inject bob as a non-admin user.
	r = r.WithContext(contextWithUserRole(r.Context(), "bob", config.UserRoleUser))
	// MultiUser mode: config must have > 1 user for canAccess to deny unowned resources.
	// We force the test by injecting a multi-user config: alice and bob are both registered.
	api.agentLoop.GetConfig().Gateway.Users = []config.UserConfig{
		{Username: "alice", Role: config.UserRoleUser},
		{Username: "bob", Role: config.UserRoleUser},
	}

	api.HandleBoardTasks(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"cross-owner milestone_id reference must return 400; body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "milestone not found",
		"error body must contain 'milestone not found', not 'access denied'")
}

// ── #406: multi-user isolation: user B cannot access user A's project ─────────

// TestTenancy_MultiUser_CrossOwnerProject_Returns404 asserts that in multi-user
// mode, user B GET/PUT/DELETE on user A's project returns 404.
//
// BDD:
//
//	Given a project owned by "alice" and multi-user mode,
//	When "bob" GETs, PUTs, or DELETEs the project,
//	Then 404 is returned.
//
// Guards against: tenancy enforcement missing on GET/PUT/DELETE paths.
// Traces to: pkg/gateway/rest_projects.go HandleProjects SEC-2 canAccess — #406
func TestTenancy_MultiUser_CrossOwnerProject_Returns404(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create project as alice (admin so it can be created freely).
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/projects",
		strings.NewReader(`{"name":"Alice Project"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/projects"
	r = r.WithContext(contextWithUserRole(r.Context(), "alice", config.UserRoleAdmin))
	api.HandleProjects(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	var proj gen.Project
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &proj))

	// Enable multi-user mode by adding two users to the config.
	api.agentLoop.GetConfig().Gateway.Users = []config.UserConfig{
		{Username: "alice", Role: config.UserRoleAdmin},
		{Username: "bob", Role: config.UserRoleUser},
	}

	methods := []struct {
		method string
		body   string
	}{
		{http.MethodGet, ""},
		{http.MethodPut, `{"name":"Modified"}`},
		{http.MethodDelete, ""},
	}

	for _, tc := range methods {
		t.Run("bob_"+tc.method, func(t *testing.T) {
			var bodyReader *strings.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			} else {
				bodyReader = strings.NewReader("")
			}
			ww := httptest.NewRecorder()
			rr := httptest.NewRequest(tc.method, "/api/v1/projects/"+proj.Id, bodyReader)
			if tc.body != "" {
				rr.Header.Set("Content-Type", "application/json")
			}
			rr.URL.Path = "/api/v1/projects/" + proj.Id
			// bob is a non-admin user — must not access alice's project.
			rr = rr.WithContext(contextWithUserRole(rr.Context(), "bob", config.UserRoleUser))
			api.HandleProjects(ww, rr)
			assert.Equal(t, http.StatusNotFound, ww.Code,
				"bob's %s on alice's project must return 404; body=%s", tc.method, ww.Body.String())
		})
	}
}

// TestTenancy_SingleUser_SharedProject_Accessible asserts that in single-user
// mode (one user in config), any authenticated user can access unowned projects.
//
// BDD:
//
//	Given a project with no owner and single-user mode,
//	When any user GETs the project,
//	Then 200 is returned.
//
// Guards against: over-restriction in single-user deployments.
// Traces to: pkg/gateway/rest_projects.go caller.canAccess MultiUser=false path — #406
func TestTenancy_SingleUser_SharedProject_Accessible(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create a project with no explicit owner (dev-bypass → admin, empty owner).
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/projects",
		strings.NewReader(`{"name":"Shared Project"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/projects"
	api.HandleProjects(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	var proj gen.Project
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &proj))

	// Single-user mode: only one user in config.
	api.agentLoop.GetConfig().Gateway.Users = []config.UserConfig{
		{Username: "alice", Role: config.UserRoleUser},
	}

	// alice can GET the unowned project in single-user mode.
	ww := httptest.NewRecorder()
	rr := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+proj.Id, nil)
	rr.URL.Path = "/api/v1/projects/" + proj.Id
	rr = rr.WithContext(contextWithUserRole(rr.Context(), "alice", config.UserRoleUser))
	api.HandleProjects(ww, rr)
	assert.Equal(t, http.StatusOK, ww.Code,
		"alice can access unowned project in single-user mode; body=%s", ww.Body.String())
}

// ── #406: system.task.create Rule-2 — session owner stamp ────────────────────

// TestTaskCreateTool_Rule2_SessionOwner asserts that when system.task.create is
// called with a session owner in context (no project_id), the created task's owner
// is set to the session owner.
//
// BDD:
//
//	Given ToolSessionOwner(ctx) == "alice" and no project_id,
//	When system.task.create is executed,
//	Then the written task's owner == "alice".
//
// Guards against: Rule-2 session owner stamp being skipped or overwritten.
// Traces to: pkg/sysagent/tools/task.go TaskCreateTool.Execute Rule-2 — #406
func TestTaskCreateTool_Rule2_SessionOwner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	tool := systools.NewTaskCreateTool(&systools.Deps{Home: home})

	// Inject session owner "alice" via context (Rule-2).
	ctx := tools.WithSessionOwner(context.Background(), "alice")

	result := tool.Execute(ctx, map[string]any{
		"name": "Alice's personal task",
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
		Name  string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(data, &diskTask))

	assert.Equal(t, "alice", diskTask.Owner,
		"task owner must equal the session owner (Rule-2); got %q", diskTask.Owner)
	assert.Equal(t, "Alice's personal task", diskTask.Name,
		"task name must match the input")
}

// TestTaskCreateTool_Rule2_NoSession_OwnerEmpty asserts that when there is no
// session owner in context and no project_id, the task owner is "" (Rule-3).
//
// Guards against: Rule-2 fabricating an owner when none was provided.
// Traces to: pkg/sysagent/tools/task.go TaskCreateTool.Execute Rule-3 — #406
func TestTaskCreateTool_Rule2_NoSession_OwnerEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	tool := systools.NewTaskCreateTool(&systools.Deps{Home: home})

	// No session owner injected (plain context).
	result := tool.Execute(context.Background(), map[string]any{
		"name": "Unowned task",
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
		"task owner must be empty (Rule-3) when no session owner and no project_id; got %q", diskTask.Owner)
}

// ── #406: multi-user board task isolation ────────────────────────────────────

// TestTenancy_MultiUser_CrossOwnerBoardTask_Returns404 asserts that in multi-user
// mode, user B cannot GET/PUT/DELETE/start a board task owned by user A.
//
// Guards against: board task endpoints missing SEC-2 canAccess enforcement.
// Traces to: pkg/gateway/rest_board.go HandleBoardTasks SEC-2 canAccess — #406
func TestTenancy_MultiUser_CrossOwnerBoardTask_Returns404(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create a task as alice (admin so POST succeeds without owner complications).
	taskName := "AliceTask_" + t.Name()
	taskBody := fmt.Sprintf(`{"name":%q,"status":"inbox"}`, taskName)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/board/tasks", strings.NewReader(taskBody))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/board/tasks"
	r = r.WithContext(contextWithUserRole(r.Context(), "alice", config.UserRoleAdmin))
	api.HandleBoardTasks(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	var task gen.BoardTask
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &task))

	// Write the task file with owner="alice" directly (the POST via dev-bypass doesn't stamp owner).
	taskPath := filepath.Join(api.homePath, "tasks", task.Id+".json")
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

	methods := []struct {
		method string
		body   string
	}{
		{http.MethodGet, ""},
		{http.MethodPut, `{"status":"next"}`},
		{http.MethodDelete, ""},
	}
	for _, tc := range methods {
		t.Run("bob_"+tc.method, func(t *testing.T) {
			bodyStr := tc.body
			ww := httptest.NewRecorder()
			rr := httptest.NewRequest(tc.method, "/api/v1/board/tasks/"+task.Id, strings.NewReader(bodyStr))
			if tc.body != "" {
				rr.Header.Set("Content-Type", "application/json")
			}
			rr.URL.Path = "/api/v1/board/tasks/" + task.Id
			rr = rr.WithContext(contextWithUserRole(rr.Context(), "bob", config.UserRoleUser))
			api.HandleBoardTasks(ww, rr)
			assert.Equal(t, http.StatusNotFound, ww.Code,
				"bob's %s on alice's task must return 404; body=%s", tc.method, ww.Body.String())
		})
	}
}
