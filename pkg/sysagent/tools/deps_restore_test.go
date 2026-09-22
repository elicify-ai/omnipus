package systools_test

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

func restoreTestDeps(t *testing.T, cfg *config.Config, save func(*config.Config) error) *systools.Deps {
	t.Helper()
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }
	return &systools.Deps{
		Home:             t.TempDir(),
		ConfigPath:       filepath.Join(t.TempDir(), "config.json"),
		GetCfg:           getCfg,
		MutateConfig:     testMutateConfig(&mu, getCfg),
		SaveConfigLocked: save,
	}
}

// TestWithConfig_OmitemptyScalarRollback proves a save failure rolls back a
// JSON omitempty scalar. encoding/json omits empty default_agent_id from the
// snapshot, so Unmarshal-into-mutated-struct would leave the new value.
func TestWithConfig_OmitemptyScalarRollback(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Agents.Defaults.DefaultAgentID == "writer" {
		t.Fatal("fixture must not already name writer")
	}
	original := cfg.Agents.Defaults.DefaultAgentID
	deps := restoreTestDeps(t, cfg, func(*config.Config) error { return errors.New("disk full") })

	err := deps.WithConfig(func(cfg *config.Config) error {
		cfg.Agents.Defaults.DefaultAgentID = "writer"
		return nil
	})
	if err == nil {
		t.Fatal("expected save error")
	}
	if cfg.Agents.Defaults.DefaultAgentID != original {
		t.Errorf("DefaultAgentID=%q, want rolled-back %q", cfg.Agents.Defaults.DefaultAgentID, original)
	}
}

func TestWithConfig_SaveConfigLockedNil_RollsBackAfterFn(t *testing.T) {
	cfg := config.DefaultConfig()
	original := cfg.Agents.Defaults.DefaultAgentID
	deps := restoreTestDeps(t, cfg, nil)

	err := deps.WithConfig(func(cfg *config.Config) error {
		cfg.Agents.Defaults.DefaultAgentID = "writer"
		return nil
	})
	if err == nil {
		t.Fatal("expected error when SaveConfigLocked is nil")
	}
	if cfg.Agents.Defaults.DefaultAgentID != original {
		t.Errorf("DefaultAgentID=%q after nil saver, want rolled-back %q", cfg.Agents.Defaults.DefaultAgentID, original)
	}
}

func TestWithConfig_RollbackPreservesNonSerializedRuntimeState(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.SkippedAgentIDs = []string{"ghost"}
	cfg.Agents.List = []config.AgentConfig{{ID: "live-roster"}}
	deps := restoreTestDeps(t, cfg, func(*config.Config) error { return errors.New("disk full") })

	err := deps.WithConfig(func(cfg *config.Config) error {
		cfg.Agents.Defaults.DefaultAgentID = "writer"
		return nil
	})
	if err == nil {
		t.Fatal("expected save error")
	}
	if len(cfg.SkippedAgentIDs) != 1 || cfg.SkippedAgentIDs[0] != "ghost" {
		t.Errorf("json:\"-\" SkippedAgentIDs=%v, want [ghost]", cfg.SkippedAgentIDs)
	}
	if len(cfg.Agents.List) != 1 || cfg.Agents.List[0].ID != "live-roster" {
		t.Errorf("json:\"-\" Agents.List=%v, want live-roster preserved", cfg.Agents.List)
	}
}
