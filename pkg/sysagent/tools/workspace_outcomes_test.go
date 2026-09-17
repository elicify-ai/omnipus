package systools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

func outcomeTestDeps(t *testing.T) *Deps {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{}
	cfg.Agents.List = []config.AgentConfig{{ID: "jim"}, {ID: "ava"}}
	return &Deps{Home: home, GetCfg: func() *config.Config { return cfg }}
}

func decodeWorkspaceOutcome(t *testing.T, raw string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode result: %v\n%s", err, raw)
	}
	return got
}

func installWorkspaceOutcomeSeams(t *testing.T, save func(string, string, []workspacepkg.DelegationEdge) error, remove func(string, string) error) {
	t.Helper()
	oldSave, oldRemove := saveWorkspaceDelegation, deleteWorkspaceEntity
	if save != nil {
		saveWorkspaceDelegation = save
	}
	if remove != nil {
		deleteWorkspaceEntity = remove
	}
	t.Cleanup(func() {
		saveWorkspaceDelegation = oldSave
		deleteWorkspaceEntity = oldRemove
	})
}

func TestWorkspaceUpdateReportsActualPartialStateWhenDelegationSaveFails(t *testing.T) {
	deps := outcomeTestDeps(t)
	id := "workspace-update-partial"
	initial := workspacepkg.Workspace{ID: id, Name: "Before", Status: "active", CreatedAt: nowISO(), UpdatedAt: nowISO(), CoreTeam: []string{"jim", "ava"}}
	if err := writeEntity(workspacesDir(deps.Home), id, initial); err != nil {
		t.Fatal(err)
	}
	oldEdges := []workspacepkg.DelegationEdge{{FromAgent: "jim", ToAgent: "ava", Modes: []workspacepkg.DelegationMode{workspacepkg.ModeDirect}}}
	if err := workspacepkg.SaveDelegation(deps.Home, id, oldEdges); err != nil {
		t.Fatal(err)
	}
	reviewed, err := workspacepkg.ReadState(deps.Home, id)
	if err != nil {
		t.Fatal(err)
	}
	installWorkspaceOutcomeSeams(t, func(string, string, []workspacepkg.DelegationEdge) error { return errors.New("graph disk full") }, nil)

	result := NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": id, "revision": reviewed.Revision, "name": "After", "delegation": []any{},
	})
	if !result.IsError {
		t.Fatalf("result must be an error: %s", result.ForLLM)
	}
	got := decodeWorkspaceOutcome(t, result.ForLLM)
	after, err := workspacepkg.ReadState(deps.Home, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Workspace.Name != "After" || !reflect.DeepEqual(after.Delegation, oldEdges) {
		t.Fatalf("actual state = %+v", after)
	}
	if got["persistence_status"] != "partial" || got["activation_status"] != "not_attempted" || got["error_stage"] != "save_delegation" {
		t.Fatalf("outcome statuses = %#v", got)
	}
	if got["revision"] != after.Revision {
		t.Fatalf("revision=%v want %s", got["revision"], after.Revision)
	}
	if !reflect.DeepEqual(got["changed_fields"], []any{"name"}) {
		t.Fatalf("changed_fields=%#v", got["changed_fields"])
	}
	actual := got["actual_state"].(map[string]any)
	if actual["name"] != "After" || len(actual["delegation"].([]any)) != 1 {
		t.Fatalf("actual_state=%#v", actual)
	}
}

func TestWorkspaceCreateReportsNoPersistenceWhenDelegationSaveFailsAndRollbackSucceeds(t *testing.T) {
	deps := outcomeTestDeps(t)
	installWorkspaceOutcomeSeams(t, func(string, string, []workspacepkg.DelegationEdge) error { return errors.New("graph disk full") }, nil)

	result := NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{"name": "Create", "core_team": []any{"jim", "ava"}})
	if !result.IsError {
		t.Fatalf("result must be an error: %s", result.ForLLM)
	}
	got := decodeWorkspaceOutcome(t, result.ForLLM)
	if got["persistence_status"] != "none" || got["activation_status"] != "not_attempted" || got["error_stage"] != "save_delegation" {
		t.Fatalf("outcome=%#v", got)
	}
	if !reflect.DeepEqual(got["changed_fields"], []any{}) {
		t.Fatalf("changed_fields=%#v", got["changed_fields"])
	}
	id := got["id"].(string)
	if _, err := os.Stat(filepath.Join(workspacesDir(deps.Home), id+".json")); !os.IsNotExist(err) {
		t.Fatalf("rolled-back entity still exists: %v", err)
	}
}

func TestWorkspaceCreateReportsSurvivingPartialStateWhenRollbackFails(t *testing.T) {
	deps := outcomeTestDeps(t)
	installWorkspaceOutcomeSeams(t,
		func(string, string, []workspacepkg.DelegationEdge) error { return errors.New("graph disk full") },
		func(string, string) error { return errors.New("entity delete refused") },
	)

	result := NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{"name": "Create", "core_team": []any{"jim", "ava"}})
	if !result.IsError {
		t.Fatalf("result must be an error: %s", result.ForLLM)
	}
	got := decodeWorkspaceOutcome(t, result.ForLLM)
	if got["persistence_status"] != "partial" || got["activation_status"] != "not_attempted" || got["error_stage"] != "rollback_workspace" {
		t.Fatalf("outcome=%#v", got)
	}
	if !reflect.DeepEqual(got["changed_fields"], []any{"workspace"}) {
		t.Fatalf("changed_fields=%#v", got["changed_fields"])
	}
	id := got["id"].(string)
	after, err := workspacepkg.ReadState(deps.Home, id)
	if err != nil {
		t.Fatal(err)
	}
	if got["revision"] != after.Revision {
		t.Fatalf("revision=%v want %s", got["revision"], after.Revision)
	}
	actual := got["actual_state"].(map[string]any)
	if actual["name"] != "Create" || len(actual["delegation"].([]any)) != 0 {
		t.Fatalf("actual_state=%#v", actual)
	}
}
