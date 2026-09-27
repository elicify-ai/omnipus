// config_max_tool_iterations_sidedoor_test.go — #904 security proof test for
// founder decision D21: the system agent's set_config must not change the
// global tool-iteration limit (agents.defaults.max_tool_iterations) or its
// env-import marker — the global is changed only in Settings → Performance,
// behind step-up re-auth (D8), and system-agent tools are bound by the same
// rules as the UI (D15). Spec: docs/internal/specs/tool-iteration-limit-spec.md,
// "Security and User Promises"; interview record D21.

package systools_test

import (
	"context"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

func TestSetConfig_RefusesGlobalMaxToolIterations(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value any
	}{
		{"the global itself", "agents.defaults.max_tool_iterations", float64(5)},
		{"the global via its parent object", "agents.defaults", map[string]any{"max_tool_iterations": float64(5)}},
		{"the import marker", "agents.defaults.max_tool_iterations_env_imported", false},
		{"the import marker via its parent object", "agents.defaults", map[string]any{"max_tool_iterations_env_imported": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			cfg.Agents.Defaults.MaxToolIterations = 250
			cfg.Agents.Defaults.MaxToolIterationsEnvImported = true

			res := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
				"key": tc.key, "value": tc.value,
			})
			if !res.IsError {
				t.Fatalf("set_config(%s) succeeded: %s — D21 closes this side door", tc.key, res.ForLLM)
			}
			errBlock, _ := parseError(t, res.ForLLM)["error"].(map[string]any)
			if errBlock["code"] != "INVALID_KEY" {
				t.Errorf("code = %v, want INVALID_KEY", errBlock["code"])
			}
			if got := cfg.Agents.Defaults.MaxToolIterations; got != 250 {
				t.Errorf("global = %d after a refused set_config, want 250 unchanged", got)
			}
			if !cfg.Agents.Defaults.MaxToolIterationsEnvImported {
				t.Error("import marker was cleared by a refused set_config")
			}
		})
	}

	// Control: the refusal is targeted — a sibling agents.defaults setting
	// still lands, so a tool that refuses everything cannot pass this test.
	t.Run("a sibling agents.defaults key is still writable", func(t *testing.T) {
		deps, cfg := newTestDeps(t)
		cfg.Agents.Defaults.MaxToolIterations = 250
		res := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
			"key": "agents.defaults.temperature", "value": 0.5,
		})
		if res.IsError {
			t.Fatalf("set_config(agents.defaults.temperature) refused: %s", res.ForLLM)
		}
		if tp := cfg.Agents.Defaults.Temperature; tp == nil || *tp != 0.5 {
			t.Errorf("temperature = %v, want 0.5", tp)
		}
		if cfg.Agents.Defaults.MaxToolIterations != 250 {
			t.Errorf("global = %d, want 250 untouched by a sibling write", cfg.Agents.Defaults.MaxToolIterations)
		}
	})
}
