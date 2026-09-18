package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

func TestAgentUpdateStorageFailureIsSanitizedAndTruthful(t *testing.T) {
	deps, _ := newTestDeps()
	store := agentstore.New(deps.Home)
	_, err := store.CreateState("storage-failure", &config.AgentConfig{Name: "Unchanged"}, "original")
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.ReadState("storage-failure")
	if err != nil {
		t.Fatal(err)
	}
	soul := filepath.Join(deps.Home, "agents", "storage-failure", "SOUL.md")
	if err = os.Remove(soul); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(soul, 0700); err != nil {
		t.Fatal(err)
	}
	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{"id": "storage-failure", "revision": before.Revision, "name": "must not persist"})
	if !result.IsError {
		t.Fatalf("expected storage failure: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, deps.Home) {
		t.Errorf("response leaked storage root: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	for key, want := range map[string]string{"code": "SAVE_FAILED", "persistence_status": "none", "activation_status": "not_attempted", "error_stage": "prepare"} {
		if body[key] != want {
			t.Errorf("%s=%v want %s", key, body[key], want)
		}
	}
	after, err := store.Get("storage-failure")
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "Unchanged" {
		t.Fatalf("failed preparation wrote name %q", after.Name)
	}
}
