package agent

// Smoke tests for bash's universal registration (ADR-036, superseding the
// quizzical-marinating-frog.md PR 5 workspace_shell_enabled gate this file
// used to cover):
//   - bash is registered for every agent, unconditionally — there is no more
//     experimental.workspace_shell_enabled flag to gate it (FR-B8).
//   - Jim's seeded "bash": deny tool policy (ADR-090 §5/§14: Jim does not
//     shell; coreagent.SeedConfig stamps the explicit override) governs
//     whether he may CALL it, not whether the tool exists in his registry.
//     Universal registration + an explicit per-agent deny is the intended
//     ADR-090 posture (ADR-077 R1's ceiling-riding allow is superseded for
//     Jim by a deliberately authored deny).

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestBash_RegisteredUnconditionally_NoExperimentalFlag verifies that bash is
// registered for a generic agent even when the (now-inert)
// experimental.workspace_shell_enabled config field is left nil — ADR-036
// retired that gate; registration is universal (FR-B8).
func TestBash_RegisteredUnconditionally_NoExperimentalFlag(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "test-agent", Name: "Test Agent"},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	al.WireTier13Deps(Tier13Deps{})

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	ag, ok := reg.GetAgent("test-agent")
	if !ok || ag == nil {
		t.Fatal("test-agent not found in registry")
	}

	rawTool, found := ag.Tools.Get("bash")
	if !found {
		t.Fatal("bash must be registered unconditionally (ADR-036 — no experimental flag gates it)")
	}
	if _, ok := rawTool.(*tools.ExecTool); !ok {
		t.Fatalf("bash is not *tools.ExecTool; got %T", rawTool)
	}
}

// TestBash_JimSeedPolicyAppliedInLoop verifies that Jim's seeded "bash":
// deny tool policy (ADR-090 §5 execution row — Jim "—" — implemented as an
// explicit per-agent deny override, ADR-090 §14 acceptance: "fresh Jim still
// cannot execute because his shipped Deny is explicit") is present after
// seeding, and that bash is registered for him like every other agent —
// with god mode off when no global god-mode switch is configured (ADR-035).
func TestBash_JimSeedPolicyAppliedInLoop(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			// Deliberately NO List here — coreagent.SeedConfig only seeds
			// the full core roster (with real tool policies) when
			// cfg.Agents.List starts EMPTY; pre-populating it makes
			// SeedConfig treat this as an existing install and skip
			// seeding properly.
		},
	}

	// Apply core agent seeds (adds Jim with the correct tool policies).
	coreagent.SeedConfig(cfg)

	// Sanity: Jim's seeded policy must carry an explicit "bash": deny override
	// (ADR-090 — Jim does not shell; the deny is deliberately authored, not an
	// omission, and must be persisted so a later ceiling raise leaves him deny).
	var jimCfg *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(coreagent.IDJim) {
			jimCfg = &cfg.Agents.List[i]
			break
		}
	}
	if jimCfg == nil {
		t.Fatal("jim not found in cfg.Agents.List after SeedConfig")
	}
	if jimCfg.Tools == nil || jimCfg.Tools.Builtin.Policies["bash"] != config.ToolPolicyDeny {
		t.Fatalf("expected Jim's seeded bash policy to be %q (ADR-090 explicit deny), got %v",
			config.ToolPolicyDeny, jimCfg.Tools)
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	al.WireTier13Deps(Tier13Deps{})

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	jimAgent, ok := reg.GetAgent("jim")
	if !ok || jimAgent == nil {
		t.Fatal("jim agent not found in registry after SeedConfig")
	}

	rawTool, found := jimAgent.Tools.Get("bash")
	if !found {
		t.Fatal("bash must be registered for Jim (universal registration, ADR-036)")
	}
	shellTool, isShellTool := rawTool.(*tools.ExecTool)
	if !isShellTool {
		t.Fatalf("bash tool for Jim is not *tools.ExecTool; got %T", rawTool)
	}

	// Verify Jim's bash tool was constructed with god mode off — this config
	// has no god-mode switch set, so GodModeActive(cfg) resolves false and the
	// tool must run through normal hardening (ADR-035).
	if shellTool.GodModeForTest() {
		t.Errorf("Jim's bash must not be in god mode when sandbox.god_mode is unset, got GodMode=true")
	}
}
