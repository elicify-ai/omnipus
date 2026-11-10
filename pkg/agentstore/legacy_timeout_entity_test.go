package agentstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestLegacyEntityWithTimeoutSecondsSavesWithoutIt (issue #1306, T5): an agent
// entity written by an older release carries a per-agent timeout_seconds. It
// must load, survive an ordinary update with every other setting intact, and
// the saved file must no longer carry the retired key.
func TestLegacyEntityWithTimeoutSecondsSavesWithoutIt(t *testing.T) {
	home := t.TempDir()
	store := New(home)
	if err := store.Create("legacy", &config.AgentConfig{ID: "legacy", Name: "Legacy Worker", Description: "before"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	path := filepath.Join(home, "entities", "agents", "legacy.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read entity: %v", err)
	}
	var doc map[string]any
	if uerr := json.Unmarshal(raw, &doc); uerr != nil {
		t.Fatalf("parse entity: %v", uerr)
	}
	doc["timeout_seconds"] = 900
	legacy, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(path, legacy, 0o600); werr != nil {
		t.Fatal(werr)
	}

	if _, uerr := store.Update("legacy", func(cfg *config.AgentConfig) error {
		cfg.Description = "after"
		return nil
	}); uerr != nil {
		t.Fatalf("Update(legacy entity) = %v; an old entity with timeout_seconds must stay editable", uerr)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]any
	if uerr := json.Unmarshal(saved, &after); uerr != nil {
		t.Fatalf("saved entity is not JSON: %v", uerr)
	}
	if _, present := after["timeout_seconds"]; present {
		t.Fatalf("saved entity still carries the retired timeout_seconds: %s", saved)
	}
	reopened, err := New(home).Get("legacy")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.Name != "Legacy Worker" || reopened.Description != "after" {
		t.Fatalf("sibling settings lost across the save: name=%q description=%q", reopened.Name, reopened.Description)
	}
}
