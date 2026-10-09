// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U9 (canonical task-family workspace merge), pkg/tools
// slice. Spec: docs/internal/specs/session-core-spec.md FR-046/048/049,
// BDD-08.5/08.6/08.7, C-TASK-TOOLS, DEL-23.
//
// FR-046: "Merge task families into canonical create/update/list optional
// workspace (absent=own)". FR-048: "Explicit foreign create MUST deliver
// assigned task to existing target team board Inbox ... no execution status/
// claim/.../run/session. Foreign run/start/... refuses before writes." FR-049:
// "Foreign creator MAY read only status of tasks it created in explicit
// destination."
//
// Oracles below are read from the spec: the canonical tools must ACCEPT an
// optional explicit workspace_id (absent = own), a foreign create lands Inbox,
// and a foreign run is refused before the dispatcher is ever reached. Today the
// canonical tools read the workspace ONLY from the turn context
// (TaskCreateTool.resolveWorkspaceID / ToolWorkspaceID), so every assertion here
// fails against current code.

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// u9SeedWorkspace writes a minimal workspace record under <home>/workspaces so
// the canonical tools' on-disk validation (workspace existence) can find it.
func u9SeedWorkspace(t *testing.T, home, id string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("u9SeedWorkspace: mkdir: %v", err)
	}
	data, _ := json.Marshal(map[string]any{"id": id, "name": id})
	if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600); err != nil {
		t.Fatalf("u9SeedWorkspace: write: %v", err)
	}
}

// u9ParamProperties returns the "properties" map of a tool's JSON-schema params.
func u9ParamProperties(t *testing.T, params map[string]any) map[string]any {
	t.Helper()
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("instrument: Parameters() has no properties map; got %T", params["properties"])
	}
	return props
}

// FR-046: create_task MUST accept an optional explicit workspace_id (omitted =
// own workspace). Today the property does not exist — the workspace is read
// only from the turn context.
func TestSessionCoreU9_CreateTaskExposesOptionalWorkspaceID(t *testing.T) {
	tool := NewTaskCreateTool(task.New(t.TempDir()))
	props := u9ParamProperties(t, tool.Parameters())
	if _, ok := props["workspace_id"]; !ok {
		t.Fatalf("FR-046/C-TASK-TOOLS: create_task must expose an optional workspace_id parameter; got properties %v", u9KeysOf(props))
	}
	// Optional: absent means own workspace, so it must NOT be required.
	if req, ok := tool.Parameters()["required"]; ok && containsString(toStringSlice(req), "workspace_id") {
		t.Fatal("FR-046: workspace_id must be OPTIONAL on create_task (absent = own workspace)")
	}
}

// FR-046: update_task MUST accept an optional explicit workspace_id.
func TestSessionCoreU9_UpdateTaskExposesOptionalWorkspaceID(t *testing.T) {
	tool := NewTaskUpdateTool(task.New(t.TempDir()))
	props := u9ParamProperties(t, tool.Parameters())
	if _, ok := props["workspace_id"]; !ok {
		t.Fatalf("FR-046/C-TASK-TOOLS: update_task must expose an optional workspace_id parameter; got properties %v", u9KeysOf(props))
	}
}

// FR-046: list_tasks MUST accept an optional explicit workspace_id.
func TestSessionCoreU9_ListTasksExposesOptionalWorkspaceID(t *testing.T) {
	tool := NewTaskListTool(task.New(t.TempDir()))
	props := u9ParamProperties(t, tool.Parameters())
	if _, ok := props["workspace_id"]; !ok {
		t.Fatalf("FR-046/C-TASK-TOOLS: list_tasks must expose an optional workspace_id parameter; got properties %v", u9KeysOf(props))
	}
}

// FR-048/BDD-08.5: a create with an explicit FOREIGN workspace_id must deliver
// the task into that target workspace with status Inbox (not-started) — zero
// trigger/run. Today the canonical create ignores workspace_id and lands the
// task in the turn's own workspace (W1) with status next.
func TestSessionCoreU9_ForeignCreateLandsInboxInTargetWorkspace(t *testing.T) {
	const ownWS = "01JXU9OWNWORKSPACE00000001"
	const foreignWS = "01JXU9FOREIGNWORKSPACE000001"

	home := t.TempDir()
	u9SeedWorkspace(t, home, ownWS)
	u9SeedWorkspace(t, home, foreignWS)

	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	tool.SetHome(home)
	// Allow the assignment (the delegation gate is not the subject of this
	// test — it is fail-closed when unwired); the subject is workspace handling.
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "creator-a")
	ctx = WithWorkspaceID(ctx, ownWS)

	res := tool.Execute(ctx, map[string]any{
		"title":        "foreign work",
		"prompt":       "deliver me to W2's board",
		"agent_id":     "assignee-b", // FR-048: the task is delivered ASSIGNED
		"workspace_id": foreignWS,    // explicit foreign destination
		"criteria":     validCriteriaArg(),
		"dod":          validDoDArg(),
	})
	if res.IsError {
		t.Fatalf("FR-048: foreign create_task must succeed as delivery, got error: %s", res.ForLLM)
	}

	id := taskIDFromResult(t, res.ForLLM)
	got, err := store.Get(id)
	if err != nil {
		t.Fatalf("load created task %q: %v", id, err)
	}
	if got.WorkspaceID != foreignWS {
		t.Fatalf("FR-048: foreign create must resolve to the explicit workspace %q, got %q", foreignWS, got.WorkspaceID)
	}
	if got.Status != task.StatusInbox {
		t.Fatalf("FR-048/BDD-08.5: foreign create must land status=inbox (not-started), got %q", got.Status)
	}
}

// FR-048/BDD-08.6: a foreign run/start MUST be refused BEFORE any write — the
// dispatcher must never be reached for a task in another workspace. Today
// run_task has no workspace check and dispatches regardless.
func TestSessionCoreU9_ForeignRunRefusedBeforeAnyWrite(t *testing.T) {
	const ownWS = "01JXU9OWNWORKSPACE00000002"
	const foreignWS = "01JXU9FOREIGNWORKSPACE000002"

	store := task.New(t.TempDir())
	tk := seedTask(t, store, "some-agent", "creator-a", foreignWS)

	dispatched := false
	tool := NewTaskRunTool(store)
	tool.SetStartTaskNow(func(context.Context, string) (string, error) {
		dispatched = true
		return "session-1", nil
	})

	ctx := WithAgentID(context.Background(), "creator-a")
	ctx = WithWorkspaceID(ctx, ownWS) // caller is in W1, the task lives in W2

	res := tool.Execute(ctx, map[string]any{"task_id": tk.ID})
	if res == nil || !res.IsError {
		t.Fatalf("FR-048/BDD-08.6: running a task in a foreign workspace must be REFUSED, got: %+v", res)
	}
	if dispatched {
		t.Fatal("FR-048/BDD-08.6: foreign run must be refused BEFORE writes; the dispatcher was reached")
	}
}

// --- small local helpers (avoid depending on other test files' helper names) ---

func u9KeysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// taskIDFromResult extracts "task_id" from a create_task result envelope.
func taskIDFromResult(t *testing.T, forLLM string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(forLLM), &m); err != nil {
		t.Fatalf("instrument: create_task result is not JSON: %v (%q)", err, forLLM)
	}
	id, _ := m["task_id"].(string)
	if id == "" {
		t.Fatalf("instrument: create_task result has no task_id: %q", forLLM)
	}
	return id
}