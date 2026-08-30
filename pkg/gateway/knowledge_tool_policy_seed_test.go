// Omnipus — ADR-068 D15.3 (FR-070/FR-071, AC-17.1/AC-17.2): the boot-side
// tool-policy tests for the knowledge-base tool family, superseding
// ADR-067 D17.
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

// knowledgeToolNames is ADR-068 D15.3's six-tool enumeration, stated here
// independently of both catalogs so this file can assert the REGISTRY
// contains them rather than asking the registry what it contains.
var knowledgeToolNames = []string{
	"knowledge_describe", "knowledge_find", "knowledge_read",
	"knowledge_edit", "knowledge_restructure", "knowledge_configure",
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

	repaired := config.RepairIncompleteToolPolicyCoverage(cfg, known)
	for _, g := range repaired {
		t.Logf("backfilled: %s", g.String())
	}
	assert.Emptyf(t, repaired, "a fresh install must need NO deny-backfill, got %d", len(repaired))

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

	t.Run("seeded config backfills no knowledge policy", func(t *testing.T) {
		cfg := seededBootConfig(t)

		repaired := config.RepairIncompleteToolPolicyCoverage(cfg, known)
		kg := knowledgeGaps(repaired)
		for _, g := range kg {
			t.Logf("backfilled: %s", g.String())
		}
		assert.Emptyf(t, kg,
			"the SEED, never the deny-backfill, must be the source of every knowledge_* "+
				"posture — got %d backfilled knowledge entries", len(kg))
		assert.Empty(t, repaired,
			"nothing at all should need backfilling on a fresh install")
	})

	t.Run("positive control: one deleted entry comes back as exactly one gap", func(t *testing.T) {
		const (
			victimAgent = string(coreagent.IDMia)
			victimTool  = "knowledge_find"
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
			"(%s, %s) must be seeded per-agent to delete (pkg/coreagent/core.go)",
			victimAgent, victimTool)
		delete(victim.Tools.Builtin.Policies, victimTool)

		repaired := config.RepairIncompleteToolPolicyCoverage(cfg, known)
		require.Lenf(t, repaired, 1,
			"exactly the one deleted (agent, tool) pair must come back as a backfill — got %d; "+
				"zero means this control measures nothing and half (a) above is vacuous",
			len(repaired))
		assert.Equal(t, config.CoverageGap{AgentID: victimAgent, ToolName: victimTool}, repaired[0])

		// And it came back DENIED, silently — the exact FR-071 failure mode.
		assert.Equal(t, config.ToolPolicyDeny, victim.Tools.Builtin.Policies[victimTool],
			"the repair backfills a gap to deny, which is why a forgotten knowledge tool "+
				"ships with the feature dead rather than failing boot")
	})
}

// TestKnowledgeTools_SeededPostureMatchesD17 asserts the other half of
// FR-071's scenario — "every knowledge tool carries its SEEDED posture" —
// at the level that actually decides what an agent may do: the RESOLVED
// policy, after the runtime global x agent merge.
//
// The seed literal alone is not sufficient evidence, and this codebase has
// the receipts. pkg/config/defaults.go records FOUR separate occasions
// (inspect_session, the ADR-052 plan-execution three, ADR-055's
// plan_correct/stop_plan, ADR-056's list_jobs) on which a per-agent "allow"
// was silently overruled by a too-tight global ceiling under strictest-wins
// (deny > ask > allow, pkg/tools/compositor.go:resolveEffectivePolicyWith).
// Each time, the grant was dead on every install while the seed data still
// read exactly as its ADR required, and each time the cost was paid in
// production before anyone noticed. An "ask" ceiling on the knowledge write
// tools would make it five: Jim's seeded "allow" would resolve "ask", and
// his one deliberate exception (he already holds unprompted bash, so an
// ask-gate on these three would gate nothing real for him — see
// pkg/coreagent/core.go's IDJim case) would quietly not exist.
//
// The expected matrix below is ADR-068 D15.3's, written out as data rather
// than derived from the code it checks.
func TestKnowledgeTools_SeededPostureMatchesD17(t *testing.T) {
	cfg := seededBootConfig(t)

	globalPolicies := make(map[string]config.ToolPolicy, len(cfg.Sandbox.ToolPolicies))
	for name, policy := range cfg.Sandbox.ToolPolicies {
		globalPolicies[name] = config.ToolPolicy(policy)
	}

	// D15.3: read tier "allow" for all four base agents; the three write
	// tools "allow" for Jim, "ask" for Ava/Mia/Ray.
	read := []string{"knowledge_describe", "knowledge_find", "knowledge_read"}
	write := []string{"knowledge_edit", "knowledge_restructure", "knowledge_configure"}
	writePosture := map[string]string{
		string(coreagent.IDJim): "allow",
		string(coreagent.IDAva): "ask",
		string(coreagent.IDMia): "ask",
		string(coreagent.IDRay): "ask",
	}

	want := make(map[string]map[string]string, len(writePosture))
	for agentID, writeWant := range writePosture {
		want[agentID] = make(map[string]string, len(read)+len(write))
		for _, name := range read {
			want[agentID][name] = "allow"
		}
		for _, name := range write {
			want[agentID][name] = writeWant
		}
	}

	seeded := make(map[string]map[string]config.ToolPolicy, len(cfg.Agents.List))
	for _, ac := range cfg.Agents.List {
		if ac.Tools == nil {
			continue
		}
		seeded[ac.ID] = ac.Tools.Builtin.Policies
	}

	for agentID, wantTools := range want {
		policies, ok := seeded[agentID]
		require.Truef(t, ok, "base agent %q must be seeded with a tool-policy map", agentID)
		for toolName, wantPolicy := range wantTools {
			got, hasEntry := policies[toolName]
			require.Truef(t, hasEntry,
				"agent %q has NO explicit entry for %q — Constraint #6 requires a literal, "+
					"wildcard-free entry per (agent, tool), and an absent one is filled with "+
					"deny by the load-path repair", agentID, toolName)
			assert.Equalf(t, wantPolicy, string(got),
				"agent %q, tool %q: seeded literal disagrees with ADR-067 D17's matrix",
				agentID, toolName)

			resolved := tools.ResolveEffectivePolicy(&tools.ToolPolicyCfg{
				Policies:       policies,
				GlobalPolicies: globalPolicies,
			}, toolName)
			assert.Equalf(t, wantPolicy, resolved,
				"agent %q, tool %q RESOLVES to %q, not the seeded %q. Under strictest-wins, a "+
					"global ceiling entry in pkg/config/defaults.go tighter than the per-agent "+
					"seed overrules it and the grant is dead on every install while the seed "+
					"still reads correctly — see that file's ADR-052/055/056 notes for the four "+
					"prior instances of exactly this defect",
				agentID, toolName, resolved, wantPolicy)
		}
	}
}
