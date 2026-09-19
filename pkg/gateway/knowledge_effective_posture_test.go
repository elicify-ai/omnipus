// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Verify ADR-090 knowledge permissions through the live registry and policy
// filter. A policy entry's existence alone does not prove the tool is usable.
// The expected matrix below comes from the approved role responsibilities,
// independently of the seed: ordinary roles can read, Mia/Jim/General Purpose
// ask before writes, and hidden roles have no knowledge access.

package gateway

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// adr090KnowledgePosture is the founder-approved matrix, independent of the seed.
var adr090KnowledgePosture = map[string]struct{ read, write string }{
	"mia": {"allow", "ask"}, "jim": {"allow", "ask"}, "ava": {"allow", "deny"},
	"admin": {"allow", "deny"}, "planner": {"allow", "deny"}, "researcher": {"allow", "deny"},
	"worker": {"allow", "ask"}, "judge": {"deny", "deny"}, "plansupervisor": {"deny", "deny"},
}

// TestKnowledgeTools_ADR090PostureIsWhatTheTurnActuallyResolves verifies the
// live registry and policy filter, including deliberate denials.
func TestKnowledgeTools_ADR090PostureIsWhatTheTurnActuallyResolves(t *testing.T) {
	al, _, _ := kwLoop(t)
	for agentID, want := range adr090KnowledgePosture {
		inst, ok := al.GetRegistry().GetAgent(routing.NormalizeAgentID(agentID))
		require.True(t, ok, "role %s must exist", agentID)
		_, resolved := tools.FilterToolsByPolicy(inst.Tools.GetAll(), inst.AgentType, inst.LoadToolPolicy())
		for _, name := range knowledgeToolNames {
			expected := want.write
			for _, readName := range knowledgeReadToolNames {
				if name == readName {
					expected = want.read
				}
			}
			got, survived := resolved[name]
			if expected == "deny" {
				assert.False(t, survived, "%s must not be offered %s", agentID, name)
				continue
			}
			require.True(t, survived, "%s must be offered %s", agentID, name)
			assert.Equal(t, expected, got, "%s / %s", agentID, name)
		}
	}
}

// TestKnowledgeTools_EveryAgentResolvesAnExplicitVerdictForAllEight covers the
// agents D15.3 does not name — the Worker, Planner, Explorer, Researcher,
// Judge and Plan Supervisor.
//
// The design states no posture for them, so this asserts the only thing it
// does require of them: a verdict that came from an explicit, literal,
// wildcard-free entry rather than from a gap. It is the coverage claim,
// checked from the registry side and for ALL agents rather than the four the
// effective-posture test above happened to reach.
func TestKnowledgeTools_EveryAgentResolvesAnExplicitVerdictForAllEight(t *testing.T) {
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
					"posture governs nothing (ADR-068 D15.3)", agentID, name)

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
		"expected one check per (agent x tool) pair: %d agents x %d tools",
		len(agentIDs), len(knowledgeToolNames))
}
