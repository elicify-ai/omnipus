// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// knowledge_effective_posture_test.go — ADR-067 D17 checked as a GRANT rather
// than as the existence of a map key.
//
// # The hole this closes
//
// TestKnowledgeTools_EveryRegisteredKnowledgeToolHasAnExplicitPolicyEntry
// asserts `hasAgentEntry || hasGlobalEntry`. That proves an entry EXISTS. It
// does not prove the entry GRANTS anything, and the difference is not
// academic: coreagent's denyAllThenOverride synthesises an explicit `deny` for
// every name in the static-tool universe before applying the overrides, so a
// tool added to that universe and never granted satisfies the existence check
// perfectly while shipping dead. Constraint #6 is satisfied — the entry is
// explicit, literal and wildcard-free — and the feature does not work.
//
// The sibling that DOES check a grant (TestKnowledgeRetrievalTools_Survive
// TheTurnsPolicyFilter) covers the retrieval pair only, and only for the four
// base agents plus the Worker as a control. Seven of the nine tools, and the
// remaining agents, had no effective-posture assertion at all.
//
// # The oracle
//
// ADR-067 D17, quoted (as amended 2026-08-24): "retrieval tools
// (knowledge_search, knowledge_graph, knowledge_tasks) `allow` for all four
// core agents; authoring tools `allow` for Jim and Ava, `ask` for Mia and
// Ray." That sentence is transcribed below as data. It is
// NOT read back from pkg/config/defaults.go or pkg/coreagent/core.go — those
// are the things under test, and a test that asks the seed what the seed says
// passes for any seed.

package gateway

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// d17Posture is ADR-067 D17's seed matrix, transcribed from the ADR.
//
// The value is what an agent's turn must RESOLVE to, not what appears in a
// config map: "allow" means the model is offered the tool, "ask" means it is
// offered and every call goes through the approval modal. Either is a working
// tool. "deny" — or absence from the resolved set — is a dead one.
var d17Posture = map[string]struct{ retrieval, authoring string }{
	string(coreagent.IDMia): {retrieval: "allow", authoring: "ask"},
	string(coreagent.IDJim): {retrieval: "allow", authoring: "allow"},
	string(coreagent.IDAva): {retrieval: "allow", authoring: "allow"},
	string(coreagent.IDRay): {retrieval: "allow", authoring: "ask"},
}

// TestKnowledgeTools_D17PostureIsWhatTheTurnActuallyResolves runs every
// (agent × knowledge tool) pair through tools.FilterToolsByPolicy — the single
// primitive the turn engine uses to decide what the model is offered and the
// gateway uses to gate execution — and compares the resolved verdict against
// D17.
//
// Resolution is strictest-wins across the global ceiling and the per-agent
// map, so this also catches the failure the seed-side tests structurally
// cannot see: a tighter entry in pkg/config/defaults.go silently overruling a
// correct-looking per-agent grant in pkg/coreagent/core.go.
func TestKnowledgeTools_D17PostureIsWhatTheTurnActuallyResolves(t *testing.T) {
	al, _, _ := kwLoop(t)

	// RETRIEVAL HERE MEANS "POSTURED AS A READ", NOT "BUILT BY RetrievalTools".
	//
	// The two groupings are deliberately not the same set, and conflating them
	// is what put this test in the wrong. knowledgeRetrievalToolNames mirrors
	// knowledge.RetrievalToolNames(), which is derived from the tool objects
	// RetrievalTools() constructs — a WIRING group (who gets which deps). D17
	// classifies by BEHAVIOUR: does the call read, or does it write?
	//
	// knowledge_tasks is constructed alongside the authoring tools and always
	// has been; ADR-067 D7's 2026-08-24 amendment reclassifies its POSTURE as
	// retrieval, because TasksTool.Execute opens no writer and emits no
	// mutation audit record. The construction grouping is left alone on
	// purpose — moving it would change tool wiring to settle a policy question.
	retrieval := map[string]bool{}
	for _, n := range knowledgeRetrievalToolNames {
		retrieval[n] = true
	}
	retrieval["knowledge_tasks"] = true

	for agentID, want := range d17Posture {
		normalized := routing.NormalizeAgentID(agentID)
		inst, ok := al.GetRegistry().GetAgent(normalized)
		require.Truef(t, ok,
			"D17 names agent %q but it is not registered — without it this test measures "+
				"nothing for that row of the matrix", normalized)

		_, resolved := tools.FilterToolsByPolicy(
			inst.Tools.GetAll(), inst.AgentType, inst.LoadToolPolicy())

		checked := 0
		for _, name := range knowledgeToolNames {
			expected := want.authoring
			family := "authoring"
			if retrieval[name] {
				expected = want.retrieval
				family = "retrieval"
			}
			checked++

			got, survived := resolved[name]
			assert.Truef(t, survived,
				"agent %q resolves %q (%s) to DENY or drops it entirely, but ADR-067 D17 "+
					"seeds it %q. The model is never offered the tool, no operator sees a "+
					"failure, and the seed still reads correctly — an explicit policy entry "+
					"existing is not the same as it granting anything (Constraint #6)",
				normalized, name, family, expected)
			if survived {
				assert.Equalf(t, expected, got,
					"agent %q resolves %q (%s) to %q; D17 seeds %q. An 'ask' where D17 says "+
						"'allow' puts an approval modal in front of every call; an 'allow' "+
						"where D17 says 'ask' removes the operator's consent step from a tool "+
						"that writes to their own notes",
					normalized, name, family, got, expected)
			}
		}
		require.Equalf(t, len(knowledgeToolNames), checked,
			"agent %q was checked for %d of the %d seeded knowledge tools",
			normalized, checked, len(knowledgeToolNames))
	}
}

// TestKnowledgeTools_EveryAgentResolvesAnExplicitVerdictForAllNine covers the
// agents D17 does not name — the Worker, Planner, Explorer, Researcher, Judge
// and Plan Supervisor.
//
// D17 states no posture for them, so this asserts the only thing the ADR does
// require of them: a verdict that came from an explicit, literal, wildcard-free
// entry rather than from a gap. It is the coverage claim, checked from the
// registry side and for ALL agents rather than the five the effective-posture
// tests happened to reach.
func TestKnowledgeTools_EveryAgentResolvesAnExplicitVerdictForAllNine(t *testing.T) {
	al, _, cfg := kwLoop(t)

	agentIDs := al.GetRegistry().ListAgentIDs()
	require.NotEmpty(t, agentIDs)

	global := cfg.Sandbox.ToolPolicies
	pairs := 0

	for _, agentID := range agentIDs {
		inst, ok := al.GetRegistry().GetAgent(agentID)
		require.True(t, ok)

		registered := map[string]bool{}
		for _, tool := range inst.Tools.GetAll() {
			if strings.HasPrefix(tool.Name(), "knowledge_") {
				registered[tool.Name()] = true
			}
		}

		for _, name := range knowledgeToolNames {
			assert.Truef(t, registered[name],
				"agent %q has no %q in its EXECUTION registry. That registry is the only "+
					"thing a turn dispatches through and the only thing GET /agents/{id}/tools "+
					"reads, so an absent tool is uncallable AND ungrantable — and its seeded "+
					"posture governs nothing (ADR-067 D7/D17)", agentID, name)

			var agentEntry bool
			if inst.LoadToolPolicy() != nil {
				_, agentEntry = inst.LoadToolPolicy().Policies[name]
			}
			_, globalEntry := global[name]
			assert.Truef(t, agentEntry || globalEntry,
				"agent %q can reach %q with NO explicit entry in either its own policy map "+
					"or the global ceiling. The load path repairs that gap to DENY and then "+
					"validates, so boot succeeds and the tool is simply dead (FR-071)",
				agentID, name)
			pairs++
		}
	}

	require.Equalf(t, len(agentIDs)*len(knowledgeToolNames), pairs,
		"expected one check per (agent × tool) pair: %d agents × %d tools",
		len(agentIDs), len(knowledgeToolNames))
}
