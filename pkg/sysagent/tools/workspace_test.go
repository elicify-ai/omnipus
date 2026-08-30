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
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
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

// newTestDepsWithHomeAndAgents behaves like newTestDepsWithHome but also seeds
// the live config's Agents.List with the given IDs (ID field only) — required
// for delegation-edge auto-seed tests now that seedDelegationEdgesForNewMembers
// presence-filters candidates against Deps.GetCfg().Agents.List: a core_team
// entry naming an agent absent from the
// live config must never seed an edge for it, so any test that expects a
// specific edge to be auto-seeded must first put that edge's endpoints in the
// config, exactly as a real install (SeedConfig-populated Agents.List) would.
func newTestDepsWithHomeAndAgents(t *testing.T, agentIDs ...string) (*systools.Deps, string) {
	t.Helper()
	deps, home := newTestDepsWithHome(t)
	cfg := deps.GetCfg()
	for _, id := range agentIDs {
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: id})
	}
	return deps, home
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
	// A workspace WITH a delegation graph (as the gateway seeds it). The graph
	// lives in the delegation store, NOT the workspace record — see
	// pkg/workspace/delegationstore.go.
	original := `{
		"id": "` + id + `",
		"name": "My Workspace",
		"status": "active",
		"core_team": ["mia","jim","ava","ray"],
		"is_default": true,
		"created_at": "2026-06-27T17:41:35Z",
		"updated_at": "2026-06-27T17:41:35Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	seedDelegationStoreForTest(t, home, id, `[
		{"from_agent":"mia","to_agent":"worker","modes":["task","background"]},
		{"from_agent":"jim","to_agent":"ava","modes":["task"]}
	]`)

	// Update the workspace exactly as Ava did: add a new agent to core_team.
	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":        id,
		"core_team": []any{"mia", "jim", "ava", "ray", "codereview"},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	// The delegation graph must survive the update.
	edges := delegationEdgesFromDisk(t, home, id)
	if len(edges) != 2 {
		t.Fatalf("delegation graph LOST/altered by update_workspace: got %v (want 2 edges)", edges)
	}
	// ...and must NOT have been copied into the child-writable workspace record.
	data, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("workspace JSON unparseable after update: %v", err)
	}
	if _, leaked := got["delegation"]; leaked {
		t.Errorf("update_workspace wrote the edge list back into the workspace record: %v", got["delegation"])
	}
	// Sanity: the core_team change still applied.
	if team, _ := got["core_team"].([]any); len(team) != 5 {
		t.Errorf("core_team update did not apply: got %v", got["core_team"])
	}
}

// TestWorkspaceUpdate_FullFieldRoundTrip asserts that a gateway-authored workspace
// file containing EVERY on-disk field (id, name, description, status, pinned,
// pin_order, core_team, owner, is_default, created_at, updated_at) survives an
// update_workspace round-trip with ALL fields intact. This is the acceptance
// proof of the unified-struct fix: the shared workspace.Workspace type ensures
// no field written by the gateway path can be silently dropped by the tool
// write path.
//
// `delegation` is deliberately NOT one of those fields any more (issue #636 —
// see pkg/workspace/delegationstore.go): the edge list moved to its own store
// because the workspace record is writable by the sandboxed child the edges
// constrain. The round-trip property still applies to it, so this test asserts
// it in its real location — the STORE record must be byte-identical after the
// rename — and additionally asserts nothing leaked back into the record.
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
		"owner": "alice",
		"is_default": false,
		"created_at": "2026-06-20T10:00:00Z",
		"updated_at": "2026-06-20T10:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	seedDelegationStoreForTest(t, home, id, `[
		{"from_agent":"jim","to_agent":"ava","modes":["await","task"],"depth":2},
		{"from_agent":"mia","to_agent":"ray"}
	]`)

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

	// The edge list must NEVER be written back into the workspace record — it
	// is writable by the principal the edges constrain, so a copy there is a
	// latent authorization source (issue #636).
	if _, leaked := got["delegation"]; leaked {
		t.Errorf("update_workspace wrote the edge list into the workspace record: %v", got["delegation"])
	}

	// Delegation graph must survive with correct structure, in the store.
	edges := delegationEdgesFromDisk(t, home, id)
	if len(edges) != 2 {
		t.Fatalf("delegation: want 2 edges in the delegation store, got %v", edges)
	}
	// First edge: jim→ava with modes and depth.
	e0 := edges[0]
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
	e1 := edges[1]
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

// TestWorkspaceCreate_LargeCoreTeamAccepted proves the 20-agent core_team cap
// is gone: a workspace created with 25 unique, registered agent IDs succeeds and
// persists all 25.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.10
func TestWorkspaceCreate_LargeCoreTeamAccepted(t *testing.T) {
	// 25 unique agent IDs, all registered in the live config exactly as a real
	// install would have them.
	ids := make([]string, 25)
	for i := range ids {
		ids[i] = fmt.Sprintf("agent-id-%02d", i)
	}
	deps, _ := newTestDepsWithHomeAndAgents(t, ids...)
	tool := systools.NewWorkspaceCreateTool(deps)

	bigTeam := make([]any, len(ids))
	for i, id := range ids {
		bigTeam[i] = id
	}
	result := tool.Execute(context.Background(), map[string]any{
		"name":      "Big Team",
		"core_team": bigTeam,
	})
	if result.IsError {
		t.Fatalf("expected success for a 25-member core_team, got error: %s", result.ForLLM)
	}

	m := parseSuccess(t, result.ForLLM)
	team, ok := m["core_team"].([]any)
	if !ok {
		t.Fatalf("core_team missing or wrong type in response: %#v", m["core_team"])
	}
	if len(team) != len(ids) {
		t.Fatalf("core_team has %d entries, want %d", len(team), len(ids))
	}
	got := make(map[string]struct{}, len(team))
	for _, v := range team {
		s, _ := v.(string)
		got[s] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := got[id]; !ok {
			t.Errorf("persisted core_team is missing agent id %q", id)
		}
	}
}

// TestWorkspaceCreate_SeedsDelegationEdgesForNewMembers verifies:
// create_workspace with a non-empty core_team must seed the same default
// delegation edges update_workspace's seedDelegationEdgesForNewMembers seeds
// for newly added members — otherwise, per ADR-037's fail-closed rule (no
// edge ⇒ deny), no member of a freshly-created team could delegate to any
// other. Mirrors TestWorkspaceUpdate_SeedsDelegationEdgesForNewMembers: team
// [ava, jim, worker] should seed exactly jim→ava, jim→worker, ava→worker
// (jim→ray is dropped — ray is not on the team).
func TestWorkspaceCreate_SeedsDelegationEdgesForNewMembers(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "ava", "jim", "worker")
	tool := systools.NewWorkspaceCreateTool(deps)

	result := tool.Execute(context.Background(), map[string]any{
		"name":      "Fresh Team",
		"core_team": []any{"ava", "jim", "worker"},
	})
	if result.IsError {
		t.Fatalf("create_workspace failed: %s", result.ForLLM)
	}

	// Success result should mention the seeded edges (Ava can relay it).
	m := parseSuccess(t, result.ForLLM)
	note, _ := m["delegation_seeded"].(string)
	if note == "" {
		t.Fatal("expected a delegation_seeded note in the success result, got none")
	}
	if !strings.Contains(note, "jim") || !strings.Contains(note, "worker") {
		t.Errorf("delegation_seeded note = %q, want it to mention jim and worker", note)
	}

	id := workspaceID(t, result.ForLLM)
	edges := delegationEdgesFromDisk(t, home, id)
	if len(edges) != 3 {
		t.Fatalf("expected exactly 3 seeded edges, got %d: %v", len(edges), edges)
	}
	if findEdge(edges, "jim", "ray") != nil {
		t.Error("jim→ray must be dropped — ray is not on the team")
	}
	if findEdge(edges, "jim", "ava") == nil {
		t.Error("expected jim→ava edge")
	}
	if findEdge(edges, "jim", "worker") == nil {
		t.Error("expected jim→worker edge")
	}
	if findEdge(edges, "ava", "worker") == nil {
		t.Error("expected ava→worker edge")
	}

	// The edge list must never be copied into the child-writable workspace
	// record itself — same invariant TestWorkspaceUpdate_PreservesDelegationGraph
	// checks for update_workspace.
	wsPath := filepath.Join(home, "workspaces", id+".json")
	data, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("workspace JSON unparseable: %v", err)
	}
	if _, leaked := raw["delegation"]; leaked {
		t.Errorf("create_workspace wrote the edge list back into the workspace record: %v", raw["delegation"])
	}
}

// TestWorkspaceCreate_NoCoreTeam_NoDelegationSeeded verifies that a
// core_team-less create never touches the delegation store and never emits a
// delegation_seeded note.
func TestWorkspaceCreate_NoCoreTeam_NoDelegationSeeded(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	tool := systools.NewWorkspaceCreateTool(deps)

	result := tool.Execute(context.Background(), map[string]any{"name": "Solo"})
	if result.IsError {
		t.Fatalf("create_workspace failed: %s", result.ForLLM)
	}
	m := parseSuccess(t, result.ForLLM)
	if _, hasNote := m["delegation_seeded"]; hasNote {
		t.Errorf("no core_team was supplied — delegation_seeded note should be absent, got %v", m["delegation_seeded"])
	}
	id := workspaceID(t, result.ForLLM)
	delegPath := filepath.Join(home, "entities", "delegation", id+".json")
	if _, err := os.Stat(delegPath); err == nil {
		t.Errorf("delegation store file should not exist for a core_team-less create: %s", delegPath)
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
		name, ok := wm["name"].(string)
		if !ok {
			t.Fatalf("wm[\"name\"] has unexpected type %T, want string", wm["name"])
		}
		names[name] = true
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

// ---- update_workspace: delegation-edge auto-seed ----
//
// update_workspace previously had no way to grow the per-workspace
// delegation graph (ADR-037's sole runtime delegation authority, fail-closed:
// no edge ⇒ deny). These tests prove the auto-seed: core_team changes seed
// the compiled-in default edges for NEWLY ADDED
// members only, never re-adding an edge among pre-existing members (so a
// deliberate prior removal via the Team tab stays removed) and never
// duplicating an edge that already exists.

// delegationEdgesFromDisk reads the workspace's PERSISTED delegation edges and
// returns them as []map[string]any for assertions.
//
// The one and only persisted location is the delegation store,
// $OMNIPUS_HOME/entities/delegation/<id>.json — NOT workspaces/<id>.json, which
// is writable by the sandboxed child the edges constrain (issue #636,
// pkg/workspace/delegationstore.go). Reading the record here would assert on a
// location nothing enforces from.
//
// An absent store record means "no edges" (the normal state for a workspace
// with no delegation) and yields an empty slice; an UNREADABLE one fails the
// test rather than reading as empty.
func delegationEdgesFromDisk(t *testing.T, home, id string) []map[string]any {
	t.Helper()
	path, err := workspacepkg.DelegationStorePath(home, id)
	if err != nil {
		t.Fatalf("delegation store path: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read delegation store record: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("delegation store JSON unparseable: %v", err)
	}
	rawEdges, _ := raw["delegation"].([]any)
	out := make([]map[string]any, 0, len(rawEdges))
	for _, e := range rawEdges {
		em, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("delegation entry is not an object: %v", e)
		}
		out = append(out, em)
	}
	return out
}

// seedDelegationStoreForTest writes a workspace's delegation edges to the
// delegation store from a raw JSON array literal — the pre-existing graph a
// test starts from. Raw JSON (rather than workspace.SaveDelegation) so a test
// can seed a legacy on-disk mode vocabulary the typed writer would normalise.
func seedDelegationStoreForTest(t *testing.T, home, id, edgesJSON string) {
	t.Helper()
	path, err := workspacepkg.DelegationStorePath(home, id)
	if err != nil {
		t.Fatalf("delegation store path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir delegation store: %v", err)
	}
	body := `{"workspace_id":"` + id + `","delegation":` + edgesJSON + `}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write delegation store record: %v", err)
	}
}

// findEdge returns the edge with the given from/to, or nil if absent.
func findEdge(edges []map[string]any, from, to string) map[string]any {
	for _, e := range edges {
		if e["from_agent"] == from && e["to_agent"] == to {
			return e
		}
	}
	return nil
}

// modesOf extracts an edge's "modes" field as []string (nil if absent).
func modesOf(edge map[string]any) []string {
	raw, _ := edge["modes"].([]any)
	if raw == nil {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, m := range raw {
		if s, ok := m.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TestWorkspaceUpdate_SeedsDelegationEdgesForNewMembers verifies scenario 1:
// team [ava] → update to [ava, jim, worker] seeds exactly the three edges
// whose endpoints are both on the new team AND touch a newly added member
// (jim→ava, jim→worker, ava→worker) — jim→ray is dropped because ray is off
// team, even though it's part of Jim's compiled-in seed. Also asserts Jim's
// 3-value seed vocabulary ([task, background, await]) collapses+dedupes to
// the 2-value trust-edge vocabulary ([task, direct]) per edgeModeCategory,
// and that depth (unset for Jim/Ava's seed) stays absent.
func TestWorkspaceUpdate_SeedsDelegationEdgesForNewMembers(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "ava", "jim", "worker")
	id := "01KW60SEED0000000000000001"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{
		"id": "` + id + `",
		"name": "Seed Test",
		"status": "active",
		"core_team": ["ava"],
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":        id,
		"core_team": []any{"ava", "jim", "worker"},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	// Success result should mention the seeded edges (Ava can relay it).
	m := parseSuccess(t, res.ForLLM)
	note, _ := m["delegation_seeded"].(string)
	if note == "" {
		t.Fatal("expected a delegation_seeded note in the success result, got none")
	}
	if !strings.Contains(note, "jim") || !strings.Contains(note, "worker") {
		t.Errorf("delegation_seeded note = %q, want it to mention jim and worker", note)
	}

	edges := delegationEdgesFromDisk(t, home, id)
	if len(edges) != 3 {
		t.Fatalf("expected exactly 3 seeded edges, got %d: %v", len(edges), edges)
	}
	if findEdge(edges, "jim", "ray") != nil {
		t.Error("jim→ray must be dropped — ray is not on the team")
	}

	jimAva := findEdge(edges, "jim", "ava")
	if jimAva == nil {
		t.Fatal("expected jim→ava edge")
	}
	jimWorker := findEdge(edges, "jim", "worker")
	if jimWorker == nil {
		t.Fatal("expected jim→worker edge")
	}
	avaWorker := findEdge(edges, "ava", "worker")
	if avaWorker == nil {
		t.Fatal("expected ava→worker edge (worker is newly added, even though ava pre-existed)")
	}

	// Jim's seed [task, background, await] must collapse+dedupe to [task, direct].
	wantModes := map[string]bool{"task": true, "direct": true}
	for name, edge := range map[string]map[string]any{"jim→ava": jimAva, "jim→worker": jimWorker} {
		modes := modesOf(edge)
		if len(modes) != 2 {
			t.Errorf("%s modes = %v, want exactly 2 (task, direct)", name, modes)
		}
		for _, mo := range modes {
			if !wantModes[mo] {
				t.Errorf("%s modes = %v contains unexpected mode %q", name, modes, mo)
			}
		}
		if _, hasDepth := edge["depth"]; hasDepth {
			t.Errorf("%s: depth should be absent (Jim's seed has no depth), got %v", name, edge["depth"])
		}
	}
}

// TestWorkspaceUpdate_SeedDedupesExistingEdge verifies scenario 2: when
// jim→worker already exists on disk before the update, applying the same
// core_team update as the previous test does NOT duplicate it — the
// pre-existing edge (and its original modes) is left exactly as-is, while
// the genuinely new edges (jim→ava, ava→worker) are still added.
func TestWorkspaceUpdate_SeedDedupesExistingEdge(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "ava", "jim", "worker")
	id := "01KW60SEED0000000000000002"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{
		"id": "` + id + `",
		"name": "Dedup Test",
		"status": "active",
		"core_team": ["ava"],
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	seedDelegationStoreForTest(t, home, id, `[
		{"from_agent":"jim","to_agent":"worker","modes":["task"]}
	]`)

	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":        id,
		"core_team": []any{"ava", "jim", "worker"},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	edges := delegationEdgesFromDisk(t, home, id)
	count := 0
	for _, e := range edges {
		if e["from_agent"] == "jim" && e["to_agent"] == "worker" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 jim→worker edge (no duplicate), got %d in %v", count, edges)
	}
	jimWorker := findEdge(edges, "jim", "worker")
	if modes := modesOf(jimWorker); len(modes) != 1 || modes[0] != "task" {
		t.Errorf("pre-existing jim→worker edge must be left untouched, got modes=%v", modes)
	}

	// The genuinely new edges must still be added.
	if findEdge(edges, "jim", "ava") == nil {
		t.Error("expected jim→ava edge to be seeded")
	}
	if findEdge(edges, "ava", "worker") == nil {
		t.Error("expected ava→worker edge to be seeded")
	}
}

// TestWorkspaceUpdate_SeedDoesNotResurrectRemovedEdge verifies scenario 3:
// a team of [ava, worker] with NO ava→worker edge (modeling a user's earlier
// deliberate removal of that edge via the Team tab) — adding ray to the team
// must seed only ray-involving edges (ray→worker; ray→researcher is dropped
// because researcher is off team) and must NOT resurrect ava→worker, even
// though both ava and worker are still on the team.
func TestWorkspaceUpdate_SeedDoesNotResurrectRemovedEdge(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "ava", "worker", "ray")
	id := "01KW60SEED0000000000000003"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{
		"id": "` + id + `",
		"name": "No Resurrect Test",
		"status": "active",
		"core_team": ["ava", "worker"],
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":        id,
		"core_team": []any{"ava", "worker", "ray"},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	edges := delegationEdgesFromDisk(t, home, id)
	if findEdge(edges, "ava", "worker") != nil {
		t.Error("ava→worker must NOT be resurrected — neither endpoint is newly added")
	}
	if findEdge(edges, "ray", "researcher") != nil {
		t.Error("ray→researcher must be dropped — researcher is not on the team")
	}
	rayWorker := findEdge(edges, "ray", "worker")
	if rayWorker == nil {
		t.Fatalf("expected ray→worker edge to be seeded, got edges=%v", edges)
	}
	if len(edges) != 1 {
		t.Errorf("expected exactly 1 seeded edge (ray→worker), got %d: %v", len(edges), edges)
	}
}

// TestWorkspaceUpdate_NoCoreTeamArg_DelegationUntouched verifies scenario 4:
// an update that does not include core_team at all must leave the existing
// delegation graph completely unchanged.
func TestWorkspaceUpdate_NoCoreTeamArg_DelegationUntouched(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	id := "01KW60SEED0000000000000004"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{
		"id": "` + id + `",
		"name": "Untouched Test",
		"status": "active",
		"core_team": ["ava", "jim"],
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	seedDelegationStoreForTest(t, home, id, `[
		{"from_agent":"jim","to_agent":"ava","modes":["task"]}
	]`)

	// Update only the name — no core_team key present in args at all.
	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":   id,
		"name": "Renamed Untouched Test",
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	m := parseSuccess(t, res.ForLLM)
	if _, hasNote := m["delegation_seeded"]; hasNote {
		t.Errorf(
			"no core_team arg was supplied — delegation_seeded note should be absent, got %v",
			m["delegation_seeded"],
		)
	}

	edges := delegationEdgesFromDisk(t, home, id)
	if len(edges) != 1 {
		t.Fatalf("expected the pre-existing 1 edge to survive untouched, got %d: %v", len(edges), edges)
	}
	if edges[0]["from_agent"] != "jim" || edges[0]["to_agent"] != "ava" {
		t.Errorf("edge changed: got %v", edges[0])
	}
}

// TestWorkspaceUpdate_RemovalOnly_NoNewEdgesNoGC verifies scenario 5: an
// update that only REMOVES a core_team member adds no new edges (added is
// empty, so the auto-seed never runs), and documents the tool's existing
// (unchanged by this fix) behavior of NOT garbage-collecting edges that
// reference the removed member — workspaceDelegationTeamSet unions core_team
// with existing edge endpoints, so a stale edge continues to validate and
// survives the write untouched.
func TestWorkspaceUpdate_RemovalOnly_NoNewEdgesNoGC(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	id := "01KW60SEED0000000000000005"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{
		"id": "` + id + `",
		"name": "Removal Test",
		"status": "active",
		"core_team": ["ava", "jim", "worker"],
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	seedDelegationStoreForTest(t, home, id, `[
		{"from_agent":"jim","to_agent":"ava","modes":["task","direct"]},
		{"from_agent":"jim","to_agent":"worker","modes":["task","direct"]},
		{"from_agent":"ava","to_agent":"worker","modes":["task","direct"]}
	]`)

	// Remove "worker" from core_team — no additions.
	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":        id,
		"core_team": []any{"ava", "jim"},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	m := parseSuccess(t, res.ForLLM)
	if _, hasNote := m["delegation_seeded"]; hasNote {
		t.Errorf("a removal-only update must not seed edges, got delegation_seeded=%v", m["delegation_seeded"])
	}

	edges := delegationEdgesFromDisk(t, home, id)
	if len(edges) != 3 {
		t.Fatalf(
			"removal-only update must not GC existing edges (unchanged pre-existing behavior), got %d: %v",
			len(edges),
			edges,
		)
	}
	if findEdge(edges, "jim", "ava") == nil || findEdge(edges, "jim", "worker") == nil ||
		findEdge(edges, "ava", "worker") == nil {
		t.Errorf("all 3 pre-existing edges must survive unchanged, got %v", edges)
	}

	team, _ := m["core_team"].([]any)
	if len(team) != 2 {
		t.Errorf("core_team should now have 2 members, got %v", team)
	}
}

// TestWorkspaceUpdate_SeedSkipsAgentsAbsentFromConfig verifies that
// seedDelegationEdgesForNewMembers must presence-filter
// candidate edges against the live config's Agents.List, not trust core_team
// blindly. Config here only registers "ava" and "jim" — "worker" is added to
// core_team but deliberately absent from config (modeling a deleted agent, or
// a stale/typo'd core_team entry that happens to collide with the compiled
// "worker" seed key). Team [ava] → [ava, jim, worker]: jim→ava must still be
// seeded (both endpoints present in config), but jim→worker and ava→worker
// must NOT be seeded — "worker" is absent from config even though it is
// present on core_team and matches a real coreagent.SeedDelegationEdges key.
func TestWorkspaceUpdate_SeedSkipsAgentsAbsentFromConfig(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "ava", "jim") // "worker" NOT registered in config
	id := "01KW60SEED0000000000000006"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{
		"id": "` + id + `",
		"name": "Presence Filter Test",
		"status": "active",
		"core_team": ["ava"],
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":        id,
		"core_team": []any{"ava", "jim", "worker"},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	edges := delegationEdgesFromDisk(t, home, id)
	if findEdge(edges, "jim", "ava") == nil {
		t.Errorf("expected jim→ava edge to be seeded (both endpoints present in config), got %v", edges)
	}
	if findEdge(edges, "jim", "worker") != nil {
		t.Error("jim→worker must NOT be seeded — worker is absent from the live config")
	}
	if findEdge(edges, "ava", "worker") != nil {
		t.Error("ava→worker must NOT be seeded — worker is absent from the live config")
	}
	if len(edges) != 1 {
		t.Errorf("expected exactly 1 seeded edge (jim→ava), got %d: %v", len(edges), edges)
	}
}

// TestWorkspaceUpdate_CombinedAddRemove_SeedsAdditionsPreservesRemovedEdges
// verifies a single update_workspace call that
// both adds and removes core_team members in one shot. Team
// [ava, jim, worker] (with pre-existing edges jim→ava, jim→worker, ava→worker,
// as if seeded by an earlier call) → [ava, worker, ray] (jim removed, ray
// added). Expected: ray→worker is seeded (the addition, both endpoints on the
// NEW team and present in config) while ray→researcher is dropped (researcher
// is not on the team); the edges referencing removed member jim
// (jim→ava, jim→worker) survive un-GC'd, unchanged, per the tool's existing
// (unchanged by this fix) no-GC-on-removal behavior
// (workspaceDelegationTeamSet unions core_team with existing edge endpoints).
//
// Adapted from the simpler `[ava,jim,worker] → [ava,ray]` example:
// with worker also removed, NEITHER ava's nor ray's compiled seed target
// (worker for ava; worker/researcher for ray) would remain on the new team,
// so that exact composition seeds zero new edges and cannot demonstrate the
// "addition seeds a real edge" half of this test. Keeping worker on the new
// team is the smallest change that preserves the "combined add+remove in one
// call" scenario while still producing a demonstrable ray-involving seed.
func TestWorkspaceUpdate_CombinedAddRemove_SeedsAdditionsPreservesRemovedEdges(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "ava", "jim", "worker", "ray")
	id := "01KW60SEED0000000000000007"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{
		"id": "` + id + `",
		"name": "Combined Add Remove Test",
		"status": "active",
		"core_team": ["ava", "jim", "worker"],
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	seedDelegationStoreForTest(t, home, id, `[
		{"from_agent":"jim","to_agent":"ava","modes":["task","direct"]},
		{"from_agent":"jim","to_agent":"worker","modes":["task","direct"]},
		{"from_agent":"ava","to_agent":"worker","modes":["task","direct"]}
	]`)

	// Remove jim, add ray — in one call.
	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":        id,
		"core_team": []any{"ava", "worker", "ray"},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	m := parseSuccess(t, res.ForLLM)
	note, _ := m["delegation_seeded"].(string)
	if !strings.Contains(note, "ray") {
		t.Errorf("delegation_seeded note = %q, want it to mention ray", note)
	}

	edges := delegationEdgesFromDisk(t, home, id)

	// The addition: ray→worker seeded (both endpoints on NEW team + in config).
	if findEdge(edges, "ray", "worker") == nil {
		t.Errorf("expected ray→worker edge to be seeded, got %v", edges)
	}
	// ray→researcher dropped — researcher is not on the team.
	if findEdge(edges, "ray", "researcher") != nil {
		t.Error("ray→researcher must be dropped — researcher is not on the team")
	}

	// Edges referencing the removed member (jim) survive un-GC'd.
	if findEdge(edges, "jim", "ava") == nil {
		t.Error("jim→ava must survive un-GC'd even though jim was removed from core_team")
	}
	if findEdge(edges, "jim", "worker") == nil {
		t.Error("jim→worker must survive un-GC'd even though jim was removed from core_team")
	}

	// The pre-existing ava→worker edge is untouched (neither endpoint newly added).
	avaWorker := findEdge(edges, "ava", "worker")
	if avaWorker == nil {
		t.Fatal("expected the pre-existing ava→worker edge to survive")
	}
	if modes := modesOf(avaWorker); len(modes) != 2 {
		t.Errorf("pre-existing ava→worker edge must be left untouched, got modes=%v", modes)
	}

	if len(edges) != 4 {
		t.Errorf("expected exactly 4 edges (3 pre-existing + 1 new ray→worker), got %d: %v", len(edges), edges)
	}
}

// ---- update_workspace: SetupPending completion ----

// TestWorkspaceUpdate_InstallingTeamClearsSetupPending verifies that setting a
// non-empty core_team on a workspace with SetupPending=true clears the flag
// and notes it in the tool result. Scenario: a workspace was created via REST
// with SetupPending left true (the normal "Ava interviews the user on first
// open" flow), but the user asks Ava, in a different chat, to staff it
// directly via update_workspace before ever opening that workspace's chat.
// Installing the team through this tool call IS the setup completing.
func TestWorkspaceUpdate_InstallingTeamClearsSetupPending(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "ava")
	id := "01KW60SETUP000000000000001"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{
		"id": "` + id + `",
		"name": "Setup Pending Test",
		"status": "active",
		"core_team": ["ava"],
		"setup_pending": true,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":        id,
		"core_team": []any{"ava"},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	m := parseSuccess(t, res.ForLLM)
	if note, _ := m["setup_completed"].(string); note == "" {
		t.Error("expected a setup_completed note in the success result when SetupPending was cleared")
	}

	if setupPendingFromDisk(t, home, id) {
		t.Error("SetupPending must be false on disk after a core_team update installs a team")
	}
}

// TestWorkspaceUpdate_NoCoreTeamArg_SetupPendingUntouched verifies that an
// update with NO core_team argument at all (e.g. a plain rename) must never
// touch SetupPending, even if it is currently true.
func TestWorkspaceUpdate_NoCoreTeamArg_SetupPendingUntouched(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "ava")
	id := "01KW60SETUP000000000000002"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{
		"id": "` + id + `",
		"name": "Setup Pending Untouched Test",
		"status": "active",
		"core_team": ["ava"],
		"setup_pending": true,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":   id,
		"name": "Renamed Setup Pending Test",
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	m := parseSuccess(t, res.ForLLM)
	if note, hasNote := m["setup_completed"]; hasNote {
		t.Errorf("no core_team arg was supplied — setup_completed note should be absent, got %v", note)
	}

	if !setupPendingFromDisk(t, home, id) {
		t.Error("SetupPending must stay true — no core_team arg was supplied")
	}
}

// TestWorkspaceUpdate_CoreTeamOnNonPending_NoOp verifies that setting
// core_team on a workspace whose SetupPending is already false is a no-op on
// the flag (stays false, no setup_completed note) — this fix only fires when
// it actually FLIPS the flag.
func TestWorkspaceUpdate_CoreTeamOnNonPending_NoOp(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "ava", "jim")
	id := "01KW60SETUP000000000000003"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{
		"id": "` + id + `",
		"name": "Non Pending Test",
		"status": "active",
		"core_team": ["ava"],
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`
	if err := os.WriteFile(wsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":        id,
		"core_team": []any{"ava", "jim"},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}

	m := parseSuccess(t, res.ForLLM)
	if note, hasNote := m["setup_completed"]; hasNote {
		t.Errorf("workspace was never setup_pending — setup_completed note should be absent, got %v", note)
	}

	if setupPendingFromDisk(t, home, id) {
		t.Error("SetupPending must remain false — it was never true to begin with")
	}
}

// setupPendingFromDisk reads workspaces/<id>.json and returns its
// "setup_pending" bool field (false if absent — matches the omitempty tag).
func setupPendingFromDisk(t *testing.T, home, id string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "workspaces", id+".json"))
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("workspace JSON unparseable: %v", err)
	}
	sp, _ := raw["setup_pending"].(bool)
	return sp
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
