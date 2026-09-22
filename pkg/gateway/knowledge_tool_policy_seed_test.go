// Omnipus — knowledge tool registration and boot policy coverage.
// ADR-090 defines current role permissions; ADR-077 defines sparse overrides.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// knowledgeToolNames is ADR-068 D15.3's six-tool enumeration plus KB-1/KB-2's
// two additions (defect-list-knowledge-base-ux-2026-09-08.md,
// founder-ratified 2026-09-08: knowledge_list, knowledge_base_create),
// stated here independently of both catalogs so this file can assert the
// REGISTRY contains them rather than asking the registry what it contains.
var knowledgeToolNames = []string{
	"knowledge_describe", "knowledge_find", "knowledge_read", "knowledge_list",
	"knowledge_edit", "knowledge_restructure", "knowledge_configure", "knowledge_base_create",
}

// seededBootConfig reproduces the boot composition pkg/gateway's
// RunContextWithOptions performs before it validates coverage:
// config.DefaultConfig() (which populates the global sandbox.tool_policies
// ceiling) followed by coreagent.SeedConfig (which populates every agent's
// own policy map). Each caller gets its own config — the repair path MUTATES
// the config it is handed.
func seededBootConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg),
		"SeedConfig reported no-op on a fresh DefaultConfig() — expected a fresh seed")
	return cfg
}

// knowledgeGaps filters a gap/repair list down to the knowledge family.
func knowledgeGaps(gaps []config.CoverageGap) []config.CoverageGap {
	var out []config.CoverageGap
	for _, g := range gaps {
		if strings.HasPrefix(g.ToolName, "knowledge_") {
			out = append(out, g)
		}
	}
	return out
}

// TestKnowledgeTools_InCoverageUniverse guards the precondition every other
// test in this file depends on, and it is not a formality: it is the single
// assertion that keeps them from passing vacuously.
//
// config.ValidateToolPolicyCoverage returns nil for an EMPTY knownTools set,
// and config.RepairIncompleteToolPolicyCoverage derives its gap list from
// that same call — so for any tool name the universe does not contain, both
// functions report nothing, forever, no matter what the seed does or does not
// say. A knowledge tool missing from buildKnownBuiltinToolNames() is
// therefore not merely uncovered; it is INVISIBLE, and "zero knowledge
// backfills" becomes a statement about the harness rather than about the
// seed.
func TestKnowledgeTools_InCoverageUniverse(t *testing.T) {
	known := buildKnownBuiltinToolNames()
	require.NotEmpty(t, known, "the coverage universe must be populated")
	for _, name := range knowledgeToolNames {
		_, ok := known[name]
		assert.Truef(t, ok,
			"%q must be in buildKnownBuiltinToolNames() (pkg/gateway/gateway.go) — a name "+
				"absent from the coverage universe is invisible to BOTH the boot validator and "+
				"the load-path repair, so every 'no gaps' assertion about it is vacuous "+
				"(ADR-067 FR-071)", name)
	}
}

// TestBoot_ZeroToolPolicyGaps is spec test 34 (FR-070, AC-17.1): a
// fresh-install boot composition, checked against the REAL live tool
// registry, has zero (agent x tool) coverage gaps — and needs no repair to
// get there.
//
// Both halves matter. The gap check alone would still pass if the seeding
// were missing and the repair had already backfilled it to deny, because the
// boot path repairs BEFORE it validates; asserting the repair had NOTHING to
// do is what distinguishes "seeded" from "silently denied".
func TestBoot_ZeroToolPolicyGaps(t *testing.T) {
	cfg := seededBootConfig(t)
	known := buildKnownBuiltinToolNames()
	require.NotEmpty(t, known)

	// ADR-077 D3 retired the per-agent deny-backfill; the real boot sequence
	// is now repairAndValidateToolPolicyCoverage (migrate legacy keys →
	// reconcile the global ceiling → hard-validate). A fresh install must
	// come through it with zero remaining gaps.
	remaining := repairAndValidateToolPolicyCoverage(cfg)
	for _, g := range remaining {
		t.Logf("gap: %s", g.String())
	}
	assert.Emptyf(t, remaining, "a fresh install must validate with NO coverage gaps, got %d", len(remaining))

	gaps := config.ValidateToolPolicyCoverage(cfg, known)
	for _, g := range gaps {
		t.Logf("gap: %s", g.String())
	}
	assert.Emptyf(t, gaps, "a fresh install must boot with zero coverage gaps, got %d", len(gaps))
}

// TestBoot_NoKnowledgeToolDenyBackfill is spec test 35 (FR-071, AC-17.2), in
// two halves — and the second is what makes the first mean anything.
//
// (a) With the builtin registry POPULATED — never a hand-assembled config —
// loading a freshly seeded configuration backfills ZERO knowledge_* entries.
//
// (b) POSITIVE CONTROL. Delete one seeded entry and the repair returns
// EXACTLY that one. Without (b), half (a) is unfalsifiable: coverage
// validation returns nothing when the tool registry is empty, and the repair
// derives its gap list from that same call, so a harness that never populates
// the registry reports green with the seeding entirely absent.
//
// The control deletes BOTH sides of the pair because coverage is OR-based:
// a tool is covered when the global ceiling OR the agent's own map has an
// entry. Deleting only Mia's entry would leave the global ceiling covering
// her, produce zero gaps, and make the control look broken when it is the
// expectation that is wrong.
//
// Why FR-071 needs a test at all: the failure it guards is SILENT. The boot
// path (repairAndValidateToolPolicyCoverage, pkg/gateway/gateway.go) repairs
// BEFORE it validates, and the repair writes an explicit ToolPolicyDeny and
// logs one WARN. Boot does not abort. A forgotten knowledge tool ships
// denied, the feature is dead, and a single log line is the only evidence.
func TestBoot_NoKnowledgeToolDenyBackfill(t *testing.T) {
	known := buildKnownBuiltinToolNames()
	require.NotEmpty(t, known, "the coverage universe must be populated")
	for _, name := range knowledgeToolNames {
		_, ok := known[name]
		require.Truef(t, ok,
			"%q is absent from the coverage universe — this test cannot measure anything "+
				"until it is present (see TestKnowledgeTools_InCoverageUniverse)", name)
	}

	t.Run("seeded config leaves no knowledge gap for boot to report", func(t *testing.T) {
		cfg := seededBootConfig(t)

		// ADR-077 D3: no deny-backfill exists any more. What the seed must
		// guarantee instead is that the real boot sequence (legacy-key
		// migration → ceiling reconcile → hard validation) finds nothing
		// missing — the SEED, never a reconcile or a code branch, is the
		// source of every knowledge_* posture.
		remaining := repairAndValidateToolPolicyCoverage(cfg)
		kg := knowledgeGaps(remaining)
		for _, g := range kg {
			t.Logf("gap: %s", g.String())
		}
		assert.Emptyf(t, kg,
			"the SEED must cover every knowledge_* posture — got %d knowledge gaps", len(kg))
		assert.Empty(t, remaining,
			"nothing at all should be reported missing on a fresh install")
	})

	t.Run("positive control: a deleted ceiling entry is reconciled back from the shipped default", func(t *testing.T) {
		const (
			// (mia, environment_setup) is the control pair: the ADR-090 §6.5
			// Ask posture is a deliberately explicit per-agent seed (kept even
			// though it equals the ceiling), and the shipped ceiling carries
			// the same ask (pkg/config/defaults.go) — so BOTH sides of the
			// OR exist to delete. knowledge_find lost its per-agent seed
			// under ADR-090 (only the ceiling posture remains), so it can no
			// longer serve as the both-sides control.
			victimAgent = string(coreagent.IDMia)
			victimTool  = "environment_setup"
		)
		cfg := seededBootConfig(t)

		// Both sides of the OR, so the pair is genuinely uncovered.
		_, hadGlobal := cfg.Sandbox.ToolPolicies[victimTool]
		require.Truef(t, hadGlobal,
			"%q must have a global ceiling entry to delete (pkg/config/defaults.go)", victimTool)
		delete(cfg.Sandbox.ToolPolicies, victimTool)

		var victim *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == victimAgent {
				victim = &cfg.Agents.List[i]
			}
		}
		require.NotNilf(t, victim, "agent %q must be seeded", victimAgent)
		require.NotNil(t, victim.Tools)
		_, hadAgent := victim.Tools.Builtin.Policies[victimTool]
		require.Truef(t, hadAgent,
			"(%s, %s) must be seeded per-agent to delete (pkg/coreagent/role_policies_adr090.go)",
			victimAgent, victimTool)
		delete(victim.Tools.Builtin.Policies, victimTool)

		// ADR-077: the repair no longer writes per-agent deny entries. What
		// heals a deleted CATALOG ceiling entry is ReconcileToolPolicyCeiling
		// (step 2 of repairAndValidateToolPolicyCoverage), which restores the
		// SHIPPED default from pkg/config/defaults.go — a data-sourced answer,
		// not a code-branch deny. This control proves the instrument can see:
		// the deletion is noticed and healed, and validation reports no gap.
		remaining := repairAndValidateToolPolicyCoverage(cfg)
		assert.Emptyf(t, remaining,
			"after the ceiling reconcile no gap may remain — got %d", len(remaining))

		restored, ok := cfg.Sandbox.ToolPolicies[victimTool]
		require.Truef(t, ok,
			"ReconcileToolPolicyCeiling must restore the deleted catalog tool %q to the ceiling", victimTool)
		assert.Equal(t, "ask", restored,
			"restored to the SHIPPED default (defaults.go: environment_setup=ask), never a "+
				"code-branch deny — ADR-077's whole point")

		// And no per-agent entry was resurrected: sparse per-agent maps only
		// ever tighten; the deleted override legally falls back to the ceiling.
		_, agentEntryCameBack := victim.Tools.Builtin.Policies[victimTool]
		assert.False(t, agentEntryCameBack,
			"no per-agent deny-backfill may exist any more (ADR-077 D3, retired)")
	})
}

// TestKnowledgeTools_SeededPostureMatchesADR090 verifies sparse defaults through
// the real strictest-wins resolver, including an operator's tighter ceiling.
func TestKnowledgeTools_SeededPostureMatchesADR090(t *testing.T) {
	cfg := seededBootConfig(t)
	global := make(map[string]config.ToolPolicy, len(cfg.Sandbox.ToolPolicies))
	for name, policy := range cfg.Sandbox.ToolPolicies {
		global[name] = config.ToolPolicy(policy)
	}
	require.Len(t, cfg.Agents.List, len(adr090KnowledgePosture))
	for _, agent := range cfg.Agents.List {
		want, ok := adr090KnowledgePosture[agent.ID]
		require.True(t, ok, "unexpected seeded role %s", agent.ID)
		require.NotNil(t, agent.Tools)
		policy := &tools.ToolPolicyCfg{Policies: agent.Tools.Builtin.Policies, GlobalPolicies: global}
		for _, name := range knowledgeToolNames {
			expected := want.write
			for _, readName := range knowledgeReadToolNames {
				if name == readName {
					expected = want.read
				}
			}
			assert.Equal(t, expected, tools.ResolveEffectivePolicy(policy, name), "%s / %s", agent.ID, name)
			previous := global[name]
			global[name] = config.ToolPolicyDeny
			assert.Equal(t, "deny", tools.ResolveEffectivePolicy(policy, name), "global deny must constrain %s / %s", agent.ID, name)
			global[name] = previous
		}
	}
}
