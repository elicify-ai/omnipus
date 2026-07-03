package agent

import (
	"path/filepath"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// Per-turn tool-round cap resolution (P0 fix, 2026-07-03): the per-agent
// max_tool_iterations was persisted and displayed but NEVER applied — the
// runtime read only the global default (and fell back to 20 when that was 0,
// which the zero-clobbering UI bug made the de-facto state). The chain is now:
// per-agent (>0) → agents.defaults (>0) → 200.
func TestNewAgentInstance_MaxToolIterationsResolution(t *testing.T) {
	home := t.TempDir()

	mk := func(perAgent, def int) *AgentInstance {
		t.Helper()
		cfg := config.DefaultConfig()
		cfg.Agents.Defaults.Workspace = filepath.Join(home, "ws")
		cfg.Agents.Defaults.MaxToolIterations = def
		agentCfg := &config.AgentConfig{ID: "iter-test", Name: "IterTest", MaxToolIterations: perAgent}
		ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
		if ag == nil {
			t.Fatal("NewAgentInstance returned nil")
		}
		return ag
	}

	// Per-agent override wins.
	if got := mk(75, 300).MaxIterations; got != 75 {
		t.Fatalf("per-agent override: got %d, want 75", got)
	}
	// 0 = inherit the global default.
	if got := mk(0, 300).MaxIterations; got != 300 {
		t.Fatalf("inherit default: got %d, want 300", got)
	}
	// Both unset → 200 (the old emergency fallback of 20 is retired).
	if got := mk(0, 0).MaxIterations; got != 200 {
		t.Fatalf("final fallback: got %d, want 200", got)
	}
	// Negative values are treated as unset, not honored.
	if got := mk(-5, 0).MaxIterations; got != 200 {
		t.Fatalf("negative per-agent: got %d, want 200", got)
	}
}

// The shipped default itself was raised 50 → 200 (operator decision).
func TestDefaultConfig_MaxToolIterationsIs200(t *testing.T) {
	if got := config.DefaultConfig().Agents.Defaults.MaxToolIterations; got != 200 {
		t.Fatalf("shipped default: got %d, want 200", got)
	}
}
