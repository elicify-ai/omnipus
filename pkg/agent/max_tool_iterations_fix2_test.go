// max_tool_iterations_fix2_test.go — #904 gate ROUND 2 test gap, runtime
// side: the external-CLI turn cap's own-value branch.
//
// Spec: docs/internal/specs/tool-iteration-limit-spec.md (Approved) — D1
// (effective = min(global, own); an own value above the global is capped),
// D4 (external-CLI workers follow the same resolved limit), FR-002. Expected
// values are the spec's arithmetic, never observed output.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// prepareRunOptions' fallback (instance MaxIterations <= 0) resolves the cap
// from the live config WITH the agent's own stored value
// (external_dispatch.go::resolveExternalMaxTurns). Kills the mutant that
// passes nil as the own value (the global would then win).
func TestExternalDispatch_ZeroInstanceLimit_UsesOwnValueFromLiveRoster(t *testing.T) {
	cases := []struct {
		name        string
		global, own int
		want        int
	}{
		// D1: own 50 under global 200 → effective 50 (the own value wins).
		{"own 50 under global 200 runs at 50", 200, 50, 50},
		// D1 boundary: own equal to the global → that value.
		{"own 200 equal to global 200 runs at 200", 200, 200, 200},
		// D1 cap: own 300 above global 200 → capped at 200.
		{"own 300 above global 200 is capped at 200", 200, 300, 200},
		// FR-002 no own value → the global.
		{"no own value rides global 120", 120, 0, 120},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvHome, t.TempDir())
			al, ts := newExternalTestLoop(t, "claude-code", "")
			live := al.GetConfig()
			live.Agents.Defaults.MaxToolIterations = tc.global
			live.Agents.List = append(live.Agents.List, config.AgentConfig{ID: ts.agent.ID, MaxToolIterations: tc.own})
			ts.agent.MaxIterations = 0 // an instance built without NewAgentInstance
			fr, restore := withFakeDriver(t)
			defer restore()
			go func() {
				fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
				fr.Cancel()
			}()
			if _, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second); err != nil {
				t.Fatalf("runExternalCLISubTurn: %v", err)
			}
			opts := fr.RecordedRunOpts()
			if len(opts) != 1 {
				t.Fatalf("driver Run called %d times, want 1", len(opts))
			}
			if opts[0].MaxTurns != tc.want {
				t.Fatalf("MaxTurns = %d, want %d (global %d, own %d: effective = min(global, own), D1/D4)",
					opts[0].MaxTurns, tc.want, tc.global, tc.own)
			}
		})
	}
}

// Direct unit twin over the resolver call, so the own-value branch is pinned
// even if the dispatch harness changes: an agent absent from the roster rides
// the global; nil cfg is the shipped default.
func TestResolveExternalMaxTurns_OwnValueBranch(t *testing.T) {
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{MaxToolIterations: 200},
		List:     []config.AgentConfig{{ID: "other", MaxToolIterations: 10}, {ID: "worker", MaxToolIterations: 50}},
	}}
	if got := resolveExternalMaxTurns(cfg, "worker"); got != 50 {
		t.Errorf("worker own 50 under global 200: got %d, want 50", got)
	}
	if got := resolveExternalMaxTurns(cfg, "missing"); got != 200 {
		t.Errorf("agent absent from the roster: got %d, want the global 200", got)
	}
	if got := resolveExternalMaxTurns(nil, "worker"); got != config.DefaultMaxToolIterations {
		t.Errorf("nil config: got %d, want the shipped default %d", got, config.DefaultMaxToolIterations)
	}
}
