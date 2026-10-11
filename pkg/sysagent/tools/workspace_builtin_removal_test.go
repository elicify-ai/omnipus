// workspace_builtin_removal_test.go: D8 on the agent-tool writer (founder
// ruling 2026-10-11, S1/S6): update_workspace enforces the SAME built-in
// roster rule the REST PUT enforces — built-ins cannot leave the default
// workspace (error result, no write) — and every removal garbage-collects the
// removed member's member_config (hidden_from_sidebar included) so a re-add
// starts visible.

package systools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// seedDefaultWorkspaceRecord writes a default-workspace record directly (the
// boot default shape: is_default true, seeded team, stored member configs).
func seedDefaultWorkspaceRecord(t *testing.T, home, id, memberConfigsJSON string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := `{"id":"` + id + `","name":"Default WS","status":"active","is_default":true,` +
		`"core_team":["jim","mia"],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"` +
		`,"member_configs":` + memberConfigsJSON + `}`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(rec), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readWorkspaceRecordRaw returns the raw workspace record bytes.
func readWorkspaceRecordRaw(t *testing.T, home, id string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "workspaces", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestWorkspaceUpdateTool_BuiltinRemovalFromDefaultRefused proves the tool
// refuses to remove a built-in roster member from the default workspace with
// the SAME rule the REST PUT enforces, and that the refusal is zero-write.
func TestWorkspaceUpdateTool_BuiltinRemovalFromDefaultRefused(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "jim", "mia")
	const wsID = "01JTOOLDEFWSREFUSED00001"
	seedDefaultWorkspaceRecord(t, home, wsID,
		`{"jim":{"heartbeat":{"enabled":true,"interval_minutes":10,"body":"Check."}}}`)
	before := readWorkspaceRecordRaw(t, home, wsID)

	result := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": wsID, "revision": currentWorkspaceRevision(t, home, wsID),
		"core_team": []any{"jim"}, // drops built-in Mia
	})
	if !result.IsError {
		t.Fatalf("built-in removal from the default workspace must be refused; got success: %s", result.ForLLM)
	}
	errBody := parseError(t, result.ForLLM)
	errObj, _ := errBody["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("error body missing error object: %s", result.ForLLM)
	}
	if code, _ := errObj["code"].(string); code != "BUILTIN_ROSTER_DEFAULT" {
		t.Fatalf("refusal code = %v, want BUILTIN_ROSTER_DEFAULT; body=%s", errObj["code"], result.ForLLM)
	}
	if msg, _ := errObj["message"].(string); msg != "Built-in agents can't be removed from this workspace" {
		t.Fatalf("refusal message = %q, want the founder-fixed text verbatim", msg)
	}

	// Zero-write: the record is byte-identical after the refusal.
	after := readWorkspaceRecordRaw(t, home, wsID)
	if string(before) != string(after) {
		t.Fatalf("a refused tool removal must not write: record changed\nbefore=%s\nafter=%s", before, after)
	}
}

// TestWorkspaceUpdateTool_CustomRemovalGCsHiddenFlag proves S6 on the tool
// path: removing a member garbage-collects the member's member_config
// (hidden_from_sidebar included) so re-adding the agent starts visible.
func TestWorkspaceUpdateTool_CustomRemovalGCsHiddenFlag(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "jim", "custom1")
	created := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Tool GC WS", "core_team": []any{"jim", "custom1"},
	})
	if created.IsError {
		t.Fatalf("create failed: %s", created.ForLLM)
	}
	id := workspaceID(t, created.ForLLM)

	// Hide the custom member by writing the record directly (what an existing
	// install's file carries), then re-read the revision the update needs.
	seedPath := filepath.Join(home, "workspaces", id+".json")
	var rec map[string]any
	if err := json.Unmarshal(readWorkspaceRecordRaw(t, home, id), &rec); err != nil {
		t.Fatal(err)
	}
	rec["member_configs"] = map[string]any{
		"custom1": map[string]any{"hidden_from_sidebar": true},
	}
	seeded, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seedPath, seeded, 0o600); err != nil {
		t.Fatal(err)
	}

	tool := systools.NewWorkspaceUpdateTool(deps)
	remove := tool.Execute(context.Background(), map[string]any{
		"id": id, "revision": currentWorkspaceRevision(t, home, id),
		"core_team": []any{"jim"},
	})
	if remove.IsError {
		t.Fatalf("custom-agent removal from a non-default workspace must succeed: %s", remove.ForLLM)
	}

	// The member_config — flag included — is garbage-collected.
	var afterRemove map[string]any
	if err := json.Unmarshal(readWorkspaceRecordRaw(t, home, id), &afterRemove); err != nil {
		t.Fatal(err)
	}
	mcs, _ := afterRemove["member_configs"].(map[string]any)
	if entry, exists := mcs["custom1"]; exists {
		t.Fatalf("removed member's member_config must be garbage-collected; got %v", entry)
	}

	// Re-add starts visible: no hidden flag resurrects.
	readd := tool.Execute(context.Background(), map[string]any{
		"id": id, "revision": currentWorkspaceRevision(t, home, id),
		"core_team": []any{"jim", "custom1"},
	})
	if readd.IsError {
		t.Fatalf("re-add must succeed: %s", readd.ForLLM)
	}
	var afterReadd map[string]any
	if err := json.Unmarshal(readWorkspaceRecordRaw(t, home, id), &afterReadd); err != nil {
		t.Fatal(err)
	}
	mcs2, _ := afterReadd["member_configs"].(map[string]any)
	if entry, exists := mcs2["custom1"]; exists {
		if hidden, _ := entry.(map[string]any)["hidden_from_sidebar"].(bool); hidden {
			t.Fatalf("re-add must start visible: the GC'd flag must not resurrect; entry=%v", entry)
		}
	}
}
