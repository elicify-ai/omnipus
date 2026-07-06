// Omnipus — System Agent Tool Tests: Workspace CRUD
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// ---- helpers ----

// workspaceID extracts the "id" string from a successful create_workspace response.
func workspaceID(t *testing.T, body string) string {
	t.Helper()
	m := parseSuccess(t, body)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("create_workspace response missing 'id' field: %s", body)
	}
	return id
}

// TestWorkspaceUpdate_PreservesDelegationGraph proves update_workspace does NOT
// destroy the per-workspace delegation graph (the runtime authority for
// who-may-delegate-to-whom). Regression for the lossy read→modify→write that
// dropped the `delegation` field because the tool's struct didn't model it.
func TestWorkspaceUpdate_PreservesDelegationGraph(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	id := "01KW52RBV3EZZPA9H8KSZWJS0K"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	// A workspace WITH a delegation graph (as the gateway seeds it).
	original := `{
		"id": "` + id + `",
		"name": "My Workspace",
		"status": "active",
		"core_team": ["mia","jim","ava","ray"],
		"is_default": true,
		"delegation": [
			{"from_agent":"mia","to_agent":"worker","modes":["task","background"]},
			{"from_agent":"jim","to_agent":"ava","modes":["task"]}
		],
		"created_at": "2026-06-27T17:41:35Z",
		"updated_at": "2026-06-27T17:41:35Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	// Update the workspace exactly as Ava did: add a new agent to core_team.
	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":        id,
		"core_team": []any{"mia", "jim", "ava", "ray", "codereview"},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	// The delegation graph must survive the update.
	data, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("workspace JSON unparseable after update: %v", err)
	}
	edges, ok := got["delegation"].([]any)
	if !ok || len(edges) != 2 {
		t.Fatalf("delegation graph LOST/altered by update_workspace: got %v (want 2 edges)", got["delegation"])
	}
	// Sanity: the core_team change still applied.
	if team, _ := got["core_team"].([]any); len(team) != 5 {
		t.Errorf("core_team update did not apply: got %v", got["core_team"])
	}
}

// TestWorkspaceUpdate_FullFieldRoundTrip asserts that a gateway-authored workspace
// file containing EVERY on-disk field (id, name, description, status, pinned,
// pin_order, core_team, repository, owner, is_default, delegation,
// created_at, updated_at) survives an update_workspace round-trip with ALL
// fields intact. This is the acceptance proof of the unified-struct fix: the
// shared workspace.Workspace type ensures no field written by the gateway path
// can be silently dropped by the tool write path.
func TestWorkspaceUpdate_FullFieldRoundTrip(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	id := "01KW52RBV3EZZPA9H8KSZWJS1A"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}

	// A gateway-authored workspace containing every field the gateway ever writes.
	original := `{
		"id": "` + id + `",
		"name": "Full Field Workspace",
		"description": "all fields present",
		"status": "active",
		"pinned": true,
		"pin_order": 3,
		"core_team": ["mia","jim","ava","ray"],
		"repository": "https://github.com/example/repo",
		"owner": "alice",
		"is_default": false,
		"delegation": [
			{"from_agent":"jim","to_agent":"ava","modes":["await","task"],"depth":2},
			{"from_agent":"mia","to_agent":"ray"}
		],
		"created_at": "2026-06-20T10:00:00Z",
		"updated_at": "2026-06-20T10:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	// Perform a minimal update — rename only. All other fields must be unchanged.
	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":   id,
		"name": "Renamed Workspace",
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	// Read back and assert every field survived.
	data, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("workspace JSON unparseable after update: %v", err)
	}

	// Name must have been updated.
	if got["name"] != "Renamed Workspace" {
		t.Errorf("name: want %q, got %q", "Renamed Workspace", got["name"])
	}
	// All read-only fields must survive unchanged.
	assertField := func(key string, want any) {
		t.Helper()
		switch w := want.(type) {
		case string:
			if got[key] != w {
				t.Errorf("field %q: want %v, got %v", key, w, got[key])
			}
		case bool:
			if got[key] != w {
				t.Errorf("field %q: want %v, got %v", key, w, got[key])
			}
		case float64:
			if got[key] != w {
				t.Errorf("field %q: want %v, got %v", key, w, got[key])
			}
		}
	}
	assertField("description", "all fields present")
	assertField("status", "active")
	assertField("pinned", true)
	assertField("pin_order", float64(3))
	assertField("repository", "https://github.com/example/repo")
	assertField("owner", "alice")
	// is_default=false is omitted by json:",omitempty" — reads back as nil/absent,
	// which correctly deserialises as the bool zero value false. Verify absence.
	if v, present := got["is_default"]; present && v != false {
		t.Errorf("is_default: want absent or false, got %v", v)
	}
	assertField("created_at", "2026-06-20T10:00:00Z")

	// core_team must survive.
	if team, ok := got["core_team"].([]any); !ok || len(team) != 4 {
		t.Errorf("core_team: want 4 entries, got %v", got["core_team"])
	}

	// Delegation graph must survive with correct structure.
	edges, ok := got["delegation"].([]any)
	if !ok || len(edges) != 2 {
		t.Fatalf("delegation: want 2 edges, got %v", got["delegation"])
	}
	// First edge: jim→ava with modes and depth.
	e0, _ := edges[0].(map[string]any)
	if e0["from_agent"] != "jim" || e0["to_agent"] != "ava" {
		t.Errorf("delegation edge 0 from/to: got %v→%v", e0["from_agent"], e0["to_agent"])
	}
	if modes, ok := e0["modes"].([]any); !ok || len(modes) != 2 {
		t.Errorf("delegation edge 0 modes: want 2, got %v", e0["modes"])
	}
	if depth, ok := e0["depth"].(float64); !ok || depth != 2 {
		t.Errorf("delegation edge 0 depth: want 2, got %v", e0["depth"])
	}
	// Second edge: mia→ray with nil modes/depth (omitempty).
	e1, _ := edges[1].(map[string]any)
	if e1["from_agent"] != "mia" || e1["to_agent"] != "ray" {
		t.Errorf("delegation edge 1 from/to: got %v→%v", e1["from_agent"], e1["to_agent"])
	}
	if _, hasDepth := e1["depth"]; hasDepth {
		t.Errorf("delegation edge 1: depth should be absent (omitempty), got %v", e1["depth"])
	}
}

// ---- create_workspace ----

// TestWorkspaceCreate_Happy verifies that create_workspace returns the workspace
// fields and writes a JSON file to disk under workspacesDir.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceCreate_Happy(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	tool := systools.NewWorkspaceCreateTool(deps)

	result := tool.Execute(context.Background(), map[string]any{
		"name":        "My Project",
		"description": "A test workspace",
	})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	m := parseSuccess(t, result.ForLLM)

	// Assert real content — not just "no crash".
	if m["name"] != "My Project" {
		t.Errorf("name = %v, want 'My Project'", m["name"])
	}
	if m["description"] != "A test workspace" {
		t.Errorf("description = %v, want 'A test workspace'", m["description"])
	}
	if m["status"] != "active" {
		t.Errorf("status = %v, want 'active'", m["status"])
	}
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatal("response is missing 'id'")
	}

	// Persistence test: verify the JSON file exists on disk.
	wsFile := filepath.Join(home, "workspaces", id+".json")
	data, err := os.ReadFile(wsFile)
	if err != nil {
		t.Fatalf("workspace file not written to disk: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("workspace file is not valid JSON: %v", err)
	}
	if raw["name"] != "My Project" {
		t.Errorf("disk name = %v, want 'My Project'", raw["name"])
	}
}

// TestWorkspaceCreate_Differentiation verifies that two different create calls
// produce two different IDs and names — proves the tool is not returning
// hardcoded data.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceCreate_Differentiation(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	tool := systools.NewWorkspaceCreateTool(deps)

	r1 := tool.Execute(context.Background(), map[string]any{"name": "Alpha"})
	r2 := tool.Execute(context.Background(), map[string]any{"name": "Beta"})

	if r1.IsError {
		t.Fatalf("create Alpha failed: %s", r1.ForLLM)
	}
	if r2.IsError {
		t.Fatalf("create Beta failed: %s", r2.ForLLM)
	}

	m1 := parseSuccess(t, r1.ForLLM)
	m2 := parseSuccess(t, r2.ForLLM)

	if m1["name"] == m2["name"] {
		t.Error("both workspaces have the same name — differentiation test failed")
	}
	if m1["id"] == m2["id"] {
		t.Error("both workspaces have the same id — differentiation test failed")
	}
}

// TestWorkspaceCreate_InvalidName covers validation: empty name and name > 200 chars.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceCreate_InvalidName(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	tool := systools.NewWorkspaceCreateTool(deps)
	ctx := context.Background()

	tests := []struct {
		name  string
		input map[string]any
	}{
		{"empty name", map[string]any{"name": ""}},
		{"name missing", map[string]any{}},
		{"name too long", map[string]any{"name": string(make([]byte, 201))}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(ctx, tc.input)
			if !result.IsError {
				t.Fatalf("expected error for %q, got success: %s", tc.name, result.ForLLM)
			}
			m := parseError(t, result.ForLLM)
			errBlock, _ := m["error"].(map[string]any)
			if errBlock["code"] != "INVALID_INPUT" {
				t.Errorf("code = %v, want INVALID_INPUT", errBlock["code"])
			}
		})
	}
}

// TestWorkspaceCreate_CoreTeamValidation checks that core_team > 20 unique entries is rejected.
// Note: the sanitizeCoreTeam function deduplicates entries, so the 21 entries must all be unique.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceCreate_CoreTeamValidation(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	tool := systools.NewWorkspaceCreateTool(deps)

	// 21 unique agent IDs to exceed the 20-entry limit after deduplication.
	bigTeam := make([]any, 21)
	for i := range bigTeam {
		bigTeam[i] = fmt.Sprintf("agent-id-%02d", i)
	}
	result := tool.Execute(context.Background(), map[string]any{
		"name":      "Too Big Team",
		"core_team": bigTeam,
	})
	if !result.IsError {
		t.Fatal("expected error for core_team > 20 unique entries, got success")
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "INVALID_INPUT" {
		t.Errorf("code = %v, want INVALID_INPUT", errBlock["code"])
	}
}

// ---- get_workspace ----

// TestWorkspaceGet_Found verifies that get_workspace returns the full workspace record.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceGet_Found(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	ctx := context.Background()

	// Create a workspace first.
	createResult := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{
		"name":        "Read Me",
		"description": "Testing get",
	})
	if createResult.IsError {
		t.Fatalf("create failed: %s", createResult.ForLLM)
	}
	id := workspaceID(t, createResult.ForLLM)

	getTool := systools.NewWorkspaceGetTool(deps)
	getResult := getTool.Execute(ctx, map[string]any{"id": id})
	if getResult.IsError {
		t.Fatalf("get failed: %s", getResult.ForLLM)
	}

	m := parseSuccess(t, getResult.ForLLM)
	if m["id"] != id {
		t.Errorf("id = %v, want %q", m["id"], id)
	}
	if m["name"] != "Read Me" {
		t.Errorf("name = %v, want 'Read Me'", m["name"])
	}
	if m["description"] != "Testing get" {
		t.Errorf("description = %v, want 'Testing get'", m["description"])
	}
}

// TestWorkspaceGet_NotFound verifies get_workspace returns an error for unknown IDs.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceGet_NotFound(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	tool := systools.NewWorkspaceGetTool(deps)

	result := tool.Execute(context.Background(), map[string]any{"id": "01JZZZZZZZZZZZZZZZZZZZZZZZ"})
	if !result.IsError {
		t.Fatalf("expected error for unknown id, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "WORKSPACE_NOT_FOUND" {
		t.Errorf("code = %v, want WORKSPACE_NOT_FOUND", errBlock["code"])
	}
}

// TestWorkspaceGet_MissingID verifies get_workspace rejects a missing id parameter.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceGet_MissingID(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	result := systools.NewWorkspaceGetTool(deps).Execute(context.Background(), map[string]any{})
	if !result.IsError {
		t.Fatalf("expected error for missing id, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "INVALID_INPUT" {
		t.Errorf("code = %v, want INVALID_INPUT", errBlock["code"])
	}
}

// ---- list_workspaces ----

// TestWorkspaceList_Empty verifies that list_workspaces returns an empty list when no workspaces exist.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceList_Empty(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	tool := systools.NewWorkspaceListTool(deps)

	result := tool.Execute(context.Background(), map[string]any{})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	m := parseSuccess(t, result.ForLLM)
	ws, _ := m["workspaces"].([]any)
	if len(ws) != 0 {
		t.Errorf("expected empty list, got %d workspaces", len(ws))
	}
}

// TestWorkspaceList_Populated verifies that list_workspaces returns all created workspaces.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceList_Populated(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	ctx := context.Background()
	createTool := systools.NewWorkspaceCreateTool(deps)

	// Create two workspaces.
	r1 := createTool.Execute(ctx, map[string]any{"name": "First Workspace"})
	r2 := createTool.Execute(ctx, map[string]any{"name": "Second Workspace"})
	if r1.IsError || r2.IsError {
		t.Fatalf("create failed: %s / %s", r1.ForLLM, r2.ForLLM)
	}

	result := systools.NewWorkspaceListTool(deps).Execute(ctx, map[string]any{})
	if result.IsError {
		t.Fatalf("list failed: %s", result.ForLLM)
	}
	m := parseSuccess(t, result.ForLLM)
	ws, _ := m["workspaces"].([]any)
	if len(ws) != 2 {
		t.Errorf("expected 2 workspaces, got %d", len(ws))
	}

	// Differentiation: verify both names are present.
	names := map[string]bool{}
	for _, w := range ws {
		wm, _ := w.(map[string]any)
		names[wm["name"].(string)] = true
	}
	if !names["First Workspace"] || !names["Second Workspace"] {
		t.Errorf("expected both workspace names in list, got: %v", names)
	}
}

// TestWorkspaceList_StatusFilter verifies that the status filter works correctly.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceList_StatusFilter(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	ctx := context.Background()
	createTool := systools.NewWorkspaceCreateTool(deps)
	updateTool := systools.NewWorkspaceUpdateTool(deps)
	listTool := systools.NewWorkspaceListTool(deps)

	r1 := createTool.Execute(ctx, map[string]any{"name": "Active WS"})
	r2 := createTool.Execute(ctx, map[string]any{"name": "Archived WS"})
	if r1.IsError || r2.IsError {
		t.Fatalf("create failed: %s / %s", r1.ForLLM, r2.ForLLM)
	}
	id2 := workspaceID(t, r2.ForLLM)

	// Archive the second workspace.
	if ur := updateTool.Execute(ctx, map[string]any{"id": id2, "status": "archived"}); ur.IsError {
		t.Fatalf("archive failed: %s", ur.ForLLM)
	}

	// Default filter (active) should return only the active one.
	activeResult := listTool.Execute(ctx, map[string]any{})
	if activeResult.IsError {
		t.Fatalf("list active failed: %s", activeResult.ForLLM)
	}
	activem := parseSuccess(t, activeResult.ForLLM)
	activeWS, _ := activem["workspaces"].([]any)
	if len(activeWS) != 1 {
		t.Errorf("expected 1 active workspace, got %d", len(activeWS))
	}

	// status=all should return both.
	allResult := listTool.Execute(ctx, map[string]any{"status": "all"})
	if allResult.IsError {
		t.Fatalf("list all failed: %s", allResult.ForLLM)
	}
	allm := parseSuccess(t, allResult.ForLLM)
	allWS, _ := allm["workspaces"].([]any)
	if len(allWS) != 2 {
		t.Errorf("expected 2 workspaces with status=all, got %d", len(allWS))
	}
}

// ---- update_workspace ----

// TestWorkspaceUpdate_Happy verifies that update_workspace persists new fields.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceUpdate_Happy(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	ctx := context.Background()

	cr := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{"name": "Old Name"})
	if cr.IsError {
		t.Fatalf("create failed: %s", cr.ForLLM)
	}
	id := workspaceID(t, cr.ForLLM)

	ur := systools.NewWorkspaceUpdateTool(deps).Execute(ctx, map[string]any{
		"id":          id,
		"name":        "New Name",
		"description": "Updated description",
		"pinned":      true,
	})
	if ur.IsError {
		t.Fatalf("update failed: %s", ur.ForLLM)
	}

	m := parseSuccess(t, ur.ForLLM)
	if m["name"] != "New Name" {
		t.Errorf("name = %v, want 'New Name'", m["name"])
	}
	if m["description"] != "Updated description" {
		t.Errorf("description = %v, want 'Updated description'", m["description"])
	}
	if m["pinned"] != true {
		t.Errorf("pinned = %v, want true", m["pinned"])
	}

	// Persistence test: verify disk was actually updated.
	wsFile := filepath.Join(home, "workspaces", id+".json")
	data, err := os.ReadFile(wsFile)
	if err != nil {
		t.Fatalf("workspace file not readable: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("workspace file is not valid JSON: %v", err)
	}
	if raw["name"] != "New Name" {
		t.Errorf("disk name = %v, want 'New Name'", raw["name"])
	}
}

// TestWorkspaceUpdate_NotFound verifies update_workspace returns an error for unknown IDs.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceUpdate_NotFound(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	tool := systools.NewWorkspaceUpdateTool(deps)

	result := tool.Execute(context.Background(), map[string]any{
		"id":   "01JZZZZZZZZZZZZZZZZZZZZZZZ",
		"name": "Should Not Work",
	})
	if !result.IsError {
		t.Fatalf("expected error for unknown id, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "WORKSPACE_NOT_FOUND" {
		t.Errorf("code = %v, want WORKSPACE_NOT_FOUND", errBlock["code"])
	}
}

// TestWorkspaceUpdate_InvalidStatus verifies update_workspace rejects invalid status values.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceUpdate_InvalidStatus(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	ctx := context.Background()

	cr := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{"name": "Status Test"})
	if cr.IsError {
		t.Fatalf("create failed: %s", cr.ForLLM)
	}
	id := workspaceID(t, cr.ForLLM)

	result := systools.NewWorkspaceUpdateTool(deps).Execute(ctx, map[string]any{
		"id":     id,
		"status": "deleted",
	})
	if !result.IsError {
		t.Fatalf("expected error for invalid status, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "INVALID_INPUT" {
		t.Errorf("code = %v, want INVALID_INPUT", errBlock["code"])
	}
}

// ---- delete_workspace ----

// TestWorkspaceDelete_ConfirmationGate verifies that delete without confirm=true is rejected.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceDelete_ConfirmationGate(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	ctx := context.Background()

	cr := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{"name": "Delete Test"})
	if cr.IsError {
		t.Fatalf("create failed: %s", cr.ForLLM)
	}
	id := workspaceID(t, cr.ForLLM)

	// Without confirm — must reject.
	noConfirmResult := systools.NewWorkspaceDeleteTool(deps).Execute(ctx, map[string]any{
		"id":      id,
		"confirm": false,
	})
	if !noConfirmResult.IsError {
		t.Fatal("expected error when confirm=false, got success")
	}
	m := parseError(t, noConfirmResult.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "CONFIRMATION_REQUIRED" {
		t.Errorf("code = %v, want CONFIRMATION_REQUIRED", errBlock["code"])
	}

	// File must still exist.
	wsFile := filepath.Join(home, "workspaces", id+".json")
	if _, err := os.Stat(wsFile); os.IsNotExist(err) {
		t.Error("workspace file was deleted even though confirm=false")
	}
}

// TestWorkspaceDelete_Happy verifies that delete_workspace with confirm=true removes the file.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceDelete_Happy(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	ctx := context.Background()

	cr := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{"name": "Gone Soon"})
	if cr.IsError {
		t.Fatalf("create failed: %s", cr.ForLLM)
	}
	id := workspaceID(t, cr.ForLLM)

	delResult := systools.NewWorkspaceDeleteTool(deps).Execute(ctx, map[string]any{
		"id":      id,
		"confirm": true,
	})
	if delResult.IsError {
		t.Fatalf("delete failed: %s", delResult.ForLLM)
	}

	dm := parseSuccess(t, delResult.ForLLM)
	if dm["deleted"] != true {
		t.Errorf("deleted = %v, want true", dm["deleted"])
	}
	if dm["id"] != id {
		t.Errorf("id = %v, want %q", dm["id"], id)
	}

	// Verify the file is gone from disk.
	wsFile := filepath.Join(home, "workspaces", id+".json")
	if _, err := os.Stat(wsFile); !os.IsNotExist(err) {
		t.Error("workspace file still present on disk after delete")
	}
}

// TestWorkspaceDelete_NotFound verifies delete returns error when workspace doesn't exist.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceDelete_NotFound(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	result := systools.NewWorkspaceDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      "01JZZZZZZZZZZZZZZZZZZZZZZZ",
		"confirm": true,
	})
	if !result.IsError {
		t.Fatalf("expected error for unknown id, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "WORKSPACE_NOT_FOUND" {
		t.Errorf("code = %v, want WORKSPACE_NOT_FOUND", errBlock["code"])
	}
}

// TestWorkspaceDelete_CascadeTasks verifies that deleting a workspace also deletes
// its associated GTD tasks (cascade delete) and reports the count.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceDelete_CascadeTasks(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	ctx := context.Background()

	// Create a workspace.
	cr := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{"name": "Cascade Test"})
	if cr.IsError {
		t.Fatalf("create workspace failed: %s", cr.ForLLM)
	}
	wsID := workspaceID(t, cr.ForLLM)

	// Create a task in the workspace by writing a task file directly so we
	// avoid a dependency on create_task_in_workspace. The cascade delete walks
	// the tasks directory looking for matching workspace_id fields.
	tasksDir := filepath.Join(home, "tasks")
	if err := os.MkdirAll(tasksDir, 0o700); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	taskID := "01JZ00000000000000000000T1"
	taskData := map[string]any{
		"id":           taskID,
		"name":         "Task in workspace",
		"status":       "inbox",
		"workspace_id": wsID,
		"created_at":   "2026-01-01T00:00:00Z",
		"updated_at":   "2026-01-01T00:00:00Z",
	}
	taskBytes, _ := json.Marshal(taskData)
	if err := os.WriteFile(filepath.Join(tasksDir, taskID+".json"), taskBytes, 0o600); err != nil {
		t.Fatalf("write task file: %v", err)
	}

	// Delete the workspace with confirm=true.
	delResult := systools.NewWorkspaceDeleteTool(deps).Execute(ctx, map[string]any{
		"id":      wsID,
		"confirm": true,
	})
	if delResult.IsError {
		t.Fatalf("delete failed: %s", delResult.ForLLM)
	}

	dm := parseSuccess(t, delResult.ForLLM)
	if dm["deleted"] != true {
		t.Errorf("deleted = %v, want true", dm["deleted"])
	}

	// The task should have been deleted.
	tasksDeleted, _ := dm["tasks_deleted"].(float64)
	if tasksDeleted < 1 {
		t.Errorf("tasks_deleted = %v, want >= 1 (cascade delete should have removed the task)", tasksDeleted)
	}
	taskFile := filepath.Join(tasksDir, taskID+".json")
	if _, err := os.Stat(taskFile); !os.IsNotExist(err) {
		t.Error("task file still present on disk after workspace cascade delete")
	}

	// Workspace file must also be gone.
	wsFile := filepath.Join(home, "workspaces", wsID+".json")
	if _, err := os.Stat(wsFile); !os.IsNotExist(err) {
		t.Error("workspace file still present on disk after delete")
	}
}

// TestWorkspaceDelete_PathTraversalRejected verifies that delete rejects IDs
// containing path traversal sequences.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceDelete_PathTraversalRejected(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	result := systools.NewWorkspaceDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      "../etc/passwd",
		"confirm": true,
	})
	if !result.IsError {
		t.Fatalf("expected error for path traversal id, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] == nil {
		t.Errorf("expected error code in response, got nil")
	}
}
