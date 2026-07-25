package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// testHomeCfg returns a *config.Config whose Agents.Defaults.Home resolves
// (via AgentHomeBasePath) to <home>/workspace — mirroring the real
// production default (OmnipusHomeDir()/workspace) — so
// filepath.Dir(cfg.AgentHomeBasePath()) recovers exactly `home`, the same
// derivation LoadAgentRosterFromEntityStore and NewAgentLoop both use.
func testHomeCfg(home string) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:      filepath.Join(home, "workspace"),
				ModelName: "gpt-4",
			},
		},
	}
}

func TestLoadAgentRosterFromEntityStore_PopulatesFromDisk(t *testing.T) {
	home := t.TempDir()
	store := agentstore.New(home)
	if err := store.Create("mia", &config.AgentConfig{Name: "Mia"}); err != nil {
		t.Fatalf("seed Create mia: %v", err)
	}
	if err := store.Create("jim", &config.AgentConfig{Name: "Jim"}); err != nil {
		t.Fatalf("seed Create jim: %v", err)
	}

	cfg := testHomeCfg(home)
	cfg.Agents.List = []config.AgentConfig{{ID: "stale-in-memory-fixture"}}

	skipped, err := LoadAgentRosterFromEntityStore(cfg)
	if err != nil {
		t.Fatalf("LoadAgentRosterFromEntityStore: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
	if len(cfg.Agents.List) != 2 {
		t.Fatalf("cfg.Agents.List = %+v, want 2 agents (mia, jim) — stale in-memory fixture must be replaced", cfg.Agents.List)
	}
	ids := map[string]bool{}
	for _, a := range cfg.Agents.List {
		ids[a.ID] = true
	}
	if !ids["mia"] || !ids["jim"] {
		t.Fatalf("cfg.Agents.List ids = %v, want mia and jim", ids)
	}
}

func TestLoadAgentRosterFromEntityStore_EmptyStoreIsSafe(t *testing.T) {
	home := t.TempDir()
	cfg := testHomeCfg(home)

	skipped, err := LoadAgentRosterFromEntityStore(cfg)
	if err != nil {
		t.Fatalf("LoadAgentRosterFromEntityStore on an empty store: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
	if len(cfg.Agents.List) != 0 {
		t.Fatalf("cfg.Agents.List = %+v, want empty (nothing seeded yet)", cfg.Agents.List)
	}
}

func TestLoadAgentRosterFromEntityStore_SurfacesSkippedIDs(t *testing.T) {
	home := t.TempDir()
	store := agentstore.New(home)
	if err := store.Create("good", &config.AgentConfig{Name: "Good"}); err != nil {
		t.Fatalf("seed Create good: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir(), "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write broken.json: %v", err)
	}

	cfg := testHomeCfg(home)
	skipped, err := LoadAgentRosterFromEntityStore(cfg)
	if err != nil {
		t.Fatalf("LoadAgentRosterFromEntityStore: %v", err)
	}
	if len(skipped) != 1 || skipped[0] != "broken" {
		t.Fatalf("skipped = %v, want exactly [broken]", skipped)
	}
	if len(cfg.Agents.List) != 1 || cfg.Agents.List[0].ID != "good" {
		t.Fatalf("cfg.Agents.List = %+v, want exactly [good]", cfg.Agents.List)
	}
	if len(cfg.SkippedAgentIDs) != 1 || cfg.SkippedAgentIDs[0] != "broken" {
		t.Fatalf("cfg.SkippedAgentIDs = %v, want exactly [broken]", cfg.SkippedAgentIDs)
	}
}

func TestLoadAgentRosterFromEntityStore_NilConfig(t *testing.T) {
	if _, err := LoadAgentRosterFromEntityStore(nil); err == nil {
		t.Fatal("expected an error for a nil config")
	}
}
