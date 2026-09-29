package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// #904: the runtime cap comes from config.ResolveMaxToolIterations —
// min(global in force, own value). Expected values are from the spec's
// "Resolver" and "Saved global" datasets.
func TestNewAgentInstance_MaxIterationsFollowsCeilingResolver(t *testing.T) {
	home := t.TempDir()
	mk := func(own, global int) int {
		t.Helper()
		cfg := config.DefaultConfig()
		cfg.Agents.Defaults.Home = filepath.Join(home, "ws")
		cfg.Agents.Defaults.MaxToolIterations = global
		ag := NewAgentInstance(&config.AgentConfig{ID: "iter-ceiling", Name: "IterCeiling", MaxToolIterations: own},
			&cfg.Agents.Defaults, cfg, &mockProvider{})
		if ag == nil {
			t.Fatal("NewAgentInstance returned nil")
		}
		return ag.MaxIterations
	}
	cases := []struct {
		name        string
		own, global int
		want        int
	}{
		{"own above global is capped (D1)", 500, 200, 200},
		{"own just above global is capped", 201, 200, 200},
		{"own below global applies", 50, 200, 50},
		{"own above global after raise applies", 500, 600, 500},
		{"saved global above 1000 runs as 1000 (D13)", 0, 5000, 1000},
		{"own 1000 under saved 5000 applies", 1000, 5000, 1000},
		{"negative saved global runs as default (D17)", 0, -4, 200},
	}
	for _, tc := range cases {
		if got := mk(tc.own, tc.global); got != tc.want {
			t.Errorf("%s: own=%d global=%d → MaxIterations %d, want %d", tc.name, tc.own, tc.global, got, tc.want)
		}
	}
}

// TestToolLimitResponse_PointsToSettings pins the exact US-9 text (spec,
// Machine-Verifiable Constraints).
func TestToolLimitResponse_PointsToSettings(t *testing.T) {
	const want = "I've reached this agent's limit of tool steps for one turn without a final response. " +
		"An admin can raise the limit in Settings → Performance (\"Max tool calls per turn\"), " +
		"and each agent's own lower limit is on its profile's Advanced tab."
	if toolLimitResponse != want {
		t.Fatalf("toolLimitResponse = %q, want %q", toolLimitResponse, want)
	}
	if strings.Contains(toolLimitResponse, "config.json") {
		t.Fatal("toolLimitResponse must not mention config.json")
	}
}
