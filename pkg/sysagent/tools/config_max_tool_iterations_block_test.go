// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// Backend-lead's own tests for the #904 review-gate fix (spec D8/D15,
// founder decision D21): set_config must not write the global tool-iteration
// limit or its env-import marker — in any key spelling, and not through a
// section write of agents.defaults — while the rest of agents.defaults stays
// writable.

import (
	"context"
	"strings"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

func TestSetConfig_RefusesGlobalToolIterationLimit(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value any
	}{
		{"leaf", "agents.defaults.max_tool_iterations", float64(900)},
		{"leaf upper-case", "AGENTS.DEFAULTS.MAX_TOOL_ITERATIONS", float64(900)},
		{"leaf mixed-case", "agents.defaults.Max_Tool_Iterations", float64(900)},
		{"under the leaf", "agents.defaults.max_tool_iterations.x", float64(900)},
		{"marker", "agents.defaults.max_tool_iterations_env_imported", false},
		{"section with the leaf", "agents.defaults", map[string]any{"max_tool_iterations": float64(900)}},
		{"section with an upper-case leaf", "agents.defaults", map[string]any{"MAX_TOOL_ITERATIONS": float64(900)}},
		{"section with the marker", "agents.defaults", map[string]any{"max_tool_iterations_env_imported": false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			cfg.Agents.Defaults.MaxToolIterations = 300
			cfg.Agents.Defaults.MaxToolIterationsEnvImported = true
			res := systools.NewConfigSetTool(deps).Execute(context.Background(),
				map[string]any{"key": tc.key, "value": tc.value})
			if !res.IsError {
				t.Fatalf("set_config %q = success, want refusal: %s", tc.key, res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, "Settings → Performance") {
				t.Errorf("refusal must point to Settings → Performance: %s", res.ForLLM)
			}
			if cfg.Agents.Defaults.MaxToolIterations != 300 || !cfg.Agents.Defaults.MaxToolIterationsEnvImported {
				t.Errorf("refused write changed the config: global=%d marker=%v",
					cfg.Agents.Defaults.MaxToolIterations, cfg.Agents.Defaults.MaxToolIterationsEnvImported)
			}
		})
	}
}

// Positive control: agents.defaults stays writable, as a leaf and as a
// section that does not touch the protected fields — and such a section
// write keeps the global.
//
// CHECK M3: this used `steering_mode` as the writable probe key, but that
// field is being deleted (DEL-04 — the user-selectable dequeue modes are
// removed). Retargeted to `temperature`, a stable, unprotected agents.defaults
// setting, preserving the test's intent: an unrelated key stays writable and
// such a write leaves the protected global untouched.
func TestSetConfig_AgentsDefaultsStillWritable(t *testing.T) {
	deps, cfg := newTestDeps(t)
	cfg.Agents.Defaults.MaxToolIterations = 300
	tool := systools.NewConfigSetTool(deps)

	res := tool.Execute(context.Background(), map[string]any{
		"key": "agents.defaults.temperature", "value": 0.5,
	})
	if res.IsError {
		t.Fatalf("leaf write refused: %s", res.ForLLM)
	}
	if tp := cfg.Agents.Defaults.Temperature; tp == nil || *tp != 0.5 {
		t.Fatalf("leaf write did not land: temperature = %v, want 0.5", tp)
	}
	res = tool.Execute(context.Background(), map[string]any{
		"key": "agents.defaults", "value": map[string]any{"temperature": 0.25},
	})
	if res.IsError {
		t.Fatalf("section write without the protected fields refused: %s", res.ForLLM)
	}
	if tp := cfg.Agents.Defaults.Temperature; tp == nil || *tp != 0.25 {
		t.Errorf("section write did not land: temperature = %v, want 0.25", tp)
	}
	if cfg.Agents.Defaults.MaxToolIterations != 300 {
		t.Errorf("section write changed the global to %d, want 300", cfg.Agents.Defaults.MaxToolIterations)
	}
}
