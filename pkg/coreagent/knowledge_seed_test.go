// Omnipus — ADR-090 knowledge-base tool defaults.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// knowledgeSeedMatrix independently states the ratified ADR-090 defaults.
// All seven ordinary roles read; Mia, Jim and Worker write with Ask.
var knowledgeSeedMatrix = map[string]struct {
	read  config.ToolPolicy
	write config.ToolPolicy
}{
	string(coreagent.IDMia):        {read: config.ToolPolicyAllow, write: config.ToolPolicyAsk},
	string(coreagent.IDJim):        {read: config.ToolPolicyAllow, write: config.ToolPolicyAsk},
	string(coreagent.IDAva):        {read: config.ToolPolicyAllow, write: config.ToolPolicyDeny},
	string(coreagent.IDAdmin):      {read: config.ToolPolicyAllow, write: config.ToolPolicyDeny},
	string(coreagent.IDPlanner):    {read: config.ToolPolicyAllow, write: config.ToolPolicyDeny},
	string(coreagent.IDResearcher): {read: config.ToolPolicyAllow, write: config.ToolPolicyDeny},
	string(coreagent.IDWorker):     {read: config.ToolPolicyAllow, write: config.ToolPolicyAsk},
}

// Keep the family list independent of the catalog so new tools require an
// explicit decision about their role defaults.
var (
	// knowledge_list joins the READ set: it names the knowledge bases in
	// scope and reads nothing inside them (KB-2).
	knowledgeReadTools = []string{
		"knowledge_describe", "knowledge_find", "knowledge_read", "knowledge_list",
	}
	// knowledge_base_create joins the WRITE set: it creates a collection on
	// disk, the widest blast radius in the family (KB-1).
	knowledgeWriteTools = []string{
		"knowledge_edit", "knowledge_restructure", "knowledge_configure", "knowledge_base_create",
	}
)

// catalogKnowledgeNames returns every knowledge_* name the real static
// builtin catalog carries — read from coreagent.AllStaticToolNames(), never
// from a hand-copied list, so a tool added to the catalog is visible here.
func catalogKnowledgeNames(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, n := range coreagent.AllStaticToolNames() {
		if strings.HasPrefix(n, "knowledge_") {
			out = append(out, n)
		}
	}
	return out
}

// Pin family membership separately from the role policy checks.
func TestCoreAgentSeed_KnowledgeFamilyIsExactlyTheADRList(t *testing.T) {
	want := append(append([]string{}, knowledgeReadTools...), knowledgeWriteTools...)
	assert.ElementsMatch(t, want, catalogKnowledgeNames(t),
		"coreagent.AllStaticToolNames()'s knowledge_* family must match ADR-090 knowledge family — "+
			"if you added a knowledge tool, add it to knowledgeReadTools or knowledgeWriteTools "+
			"here AND state its posture for every seeded role")
}

// Verify the actual compositor result, including sparse inheritance and a
// stricter operator ceiling, rather than requiring redundant Allow entries.
func TestCoreAgentSeed_KnowledgeToolsCarrySeededPosture(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	for agentID, want := range knowledgeSeedMatrix {
		ac := findSeeded(t, cfg, agentID)
		require.NotNil(t, ac.Tools)
		for _, tier := range []struct {
			names  []string
			policy config.ToolPolicy
		}{
			{knowledgeReadTools, want.read}, {knowledgeWriteTools, want.write},
		} {
			for _, tool := range tier.names {
				assert.Equal(t, string(tier.policy), resolveFor(t, cfg, agentID, tool, nil), "%s %s", agentID, tool)
				if string(tier.policy) == cfg.Sandbox.ToolPolicies[tool] {
					assert.NotContains(t, ac.Tools.Builtin.Policies, tool, "matching ceiling must be inherited: %s %s", agentID, tool)
				} else {
					assert.Equal(t, tier.policy, ac.Tools.Builtin.Policies[tool], "tightening must be explicit: %s %s", agentID, tool)
				}
				ceiling := globalPolicyMap(cfg)
				ceiling[tool] = config.ToolPolicyDeny
				assert.Equal(t, "deny", resolveFor(t, cfg, agentID, tool, ceiling), "operator ceiling must win: %s %s", agentID, tool)
			}
		}
	}
}

// Hidden engine roles retain explicit knowledge-tool denials.
func TestCoreAgentSeed_KnowledgeToolsDeniedOffTheBaseRoster(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	names := catalogKnowledgeNames(t)
	require.NotEmpty(t, names, "the catalog must carry the knowledge family")

	for _, ac := range cfg.Agents.List {
		if _, isBase := knowledgeSeedMatrix[ac.ID]; isBase {
			continue
		}
		require.NotNilf(t, ac.Tools, "agent %q must carry a tools policy", ac.ID)
		for _, tool := range names {
			got, ok := ac.Tools.Builtin.Policies[tool]
			assert.Truef(t, ok, "hidden role %s must explicitly deny %s", ac.ID, tool)
			assert.Equalf(t, config.ToolPolicyDeny, got, "hidden role %s must deny %s", ac.ID, tool)
		}
	}
}
