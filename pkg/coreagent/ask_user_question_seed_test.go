// Omnipus — AskUserQuestion Constraint-#6 seeding tests (spec Test 14, the
// policy-seeding unit half; askuserquestion-tool-spec v3 US-7 S1).
//
// Asserts the three seeding sites move together: the allStaticToolNames
// catalog literal, every agent's per-agent policy (allow for the human-facing
// roster, explicit deny for Judge/PlanSupervisor), and the global
// sandbox.tool_policies ceiling. All posture assertions resolve through the
// REAL compositor merge (resolveFor), never a seed literal — see
// list_jobs_seed_test.go's header for why.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// TestAskUserQuestion_InStaticCatalogAndCeiling pins seeding sites (a) and
// (c): the catalog literal and the global ceiling both carry the name, and
// the ceiling is "allow" (never an ask-gate on asking — US-7 S1).
func TestAskUserQuestion_InStaticCatalogAndCeiling(t *testing.T) {
	inCatalog := false
	for _, n := range coreagent.AllStaticToolNames() {
		if n == "AskUserQuestion" {
			inCatalog = true
			break
		}
	}
	require.True(t, inCatalog, "AskUserQuestion must be in allStaticToolNames (Constraint #6 site a)")

	cfg := config.DefaultConfig()
	require.Equal(t, "allow", cfg.Sandbox.ToolPolicies["AskUserQuestion"],
		"the global sandbox.tool_policies ceiling must carry a literal allow for AskUserQuestion (site c); "+
			"an ask here would gate asking a question behind an approval to ask a question")
}

// TestAskUserQuestion_ResolvedPostureAcrossSeededRoster pins the resolved
// posture for EVERY seeded agent as a complete partition (the
// list_jobs_seed_test.go pattern): allow for the human-facing roster — the
// four base agents and the delegation-tier specialists (whose delegated runs
// are rejected at the tool's own owner-session gate, not by policy) — and
// deny for the two System Agents, which can never be session owners and whose
// deliberately minimal seeds must not advertise an always-erroring tool.
func TestAskUserQuestion_ResolvedPostureAcrossSeededRoster(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	want := map[coreagent.CoreAgentID]string{
		coreagent.IDMia: "allow",
		coreagent.IDJim: "allow",
		coreagent.IDAva: "allow",
		coreagent.IDRay: "allow",
		// Subagent tier: allow (US-7 S1 — "core roster, subagent tier"). The
		// Worker's sparse map inherits the ceiling's allow; the other three
		// carry an explicit allow override.
		coreagent.IDWorker:     "allow",
		coreagent.IDPlanner:    "allow",
		coreagent.IDExplorer:   "allow",
		coreagent.IDResearcher: "allow",
		// System Agents: explicit deny via their denyAllThenOverride stamps.
		coreagent.IDJudge:          "deny",
		coreagent.IDPlanSupervisor: "deny",
	}

	seeded := 0
	for _, a := range cfg.Agents.List {
		if a.Tools == nil {
			continue
		}
		seeded++
		expected, known := want[coreagent.CoreAgentID(a.ID)]
		require.Truef(t, known,
			"agent %q is seeded but has no stated AskUserQuestion posture — US-7 S1 requires a DECISION "+
				"for every seeded agent; add it to this table and the seed map together", a.ID)
		assert.Equalf(t, expected, resolveFor(t, cfg, a.ID, "AskUserQuestion", nil),
			"(%s, AskUserQuestion) must RESOLVE %q through the real compositor merge", a.ID, expected)
	}
	require.Equal(t, len(want), seeded,
		"sanity: the expectation table and the seeded roster must partition each other exactly")
}

// TestAskUserQuestion_CustomAgentDefaultAllowlist pins the customs' seed:
// NewCustomAgentToolsCfg (the single shared seed for both agent-creation
// paths) carries an explicit allow.
func TestAskUserQuestion_CustomAgentDefaultAllowlist(t *testing.T) {
	toolsCfg := coreagent.NewCustomAgentToolsCfg()
	require.NotNil(t, toolsCfg)
	got, present := toolsCfg.Builtin.Policies["AskUserQuestion"]
	require.True(t, present, "customs' default policy map must carry an explicit AskUserQuestion entry")
	assert.Equal(t, config.ToolPolicyAllow, got,
		"a fresh custom agent is human-facing: AskUserQuestion seeds allow")
}
