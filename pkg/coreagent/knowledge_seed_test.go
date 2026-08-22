// Omnipus — ADR-067 D17 (FR-070/FR-071): the knowledge-base tool-policy seed.
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

// knowledgeSeedMatrix is ADR-067 D17's seed posture, written out as data so
// the assertion below reads as the ADR does: retrieval "allow" for all four
// base agents; authoring "allow" for Jim and Ava, "ask" for Mia and Ray.
//
// It is deliberately a SECOND, independent statement of the posture rather
// than something derived from the seed — a test that reads its expectation
// out of the code under test asserts only that the code equals itself.
var knowledgeSeedMatrix = map[string]struct {
	retrieval config.ToolPolicy
	authoring config.ToolPolicy
}{
	string(coreagent.IDJim): {retrieval: config.ToolPolicyAllow, authoring: config.ToolPolicyAllow},
	string(coreagent.IDAva): {retrieval: config.ToolPolicyAllow, authoring: config.ToolPolicyAllow},
	string(coreagent.IDMia): {retrieval: config.ToolPolicyAllow, authoring: config.ToolPolicyAsk},
	string(coreagent.IDRay): {retrieval: config.ToolPolicyAllow, authoring: config.ToolPolicyAsk},
}

// knowledgeRetrievalTools / knowledgeAuthoringTools split ADR-067 D7's tool
// list the way D17's posture matrix does. Also written out independently of
// the catalog, so that a name added to allStaticToolNames and to nothing else
// fails TestCoreAgentSeed_KnowledgeFamilyIsExactlyTheADRList below rather
// than silently inheriting a posture nobody chose.
var (
	knowledgeRetrievalTools = []string{"knowledge_search", "knowledge_graph"}
	knowledgeAuthoringTools = []string{
		"knowledge_create", "knowledge_link", "knowledge_set_property",
		"knowledge_append_section", "knowledge_tasks",
		"knowledge_move", "knowledge_rename",
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

// TestCoreAgentSeed_KnowledgeFamilyIsExactlyTheADRList pins the catalog side
// of the family to ADR-067 D7's enumeration. It exists so the posture test
// below cannot go quietly incomplete: add a tenth knowledge tool to
// allStaticToolNames and this fails, pointing whoever added it at the seed
// matrix they also have to state a posture in.
//
// Without this, a new knowledge tool would arrive at denyAllThenOverride's
// "deny" for every agent — an explicit entry, so coverage validation is
// satisfied and boot is clean, and the tool is simply dead with no signal
// anywhere. That silent-death shape is the whole reason FR-071 exists.
func TestCoreAgentSeed_KnowledgeFamilyIsExactlyTheADRList(t *testing.T) {
	want := append(append([]string{}, knowledgeRetrievalTools...), knowledgeAuthoringTools...)
	assert.ElementsMatch(t, want, catalogKnowledgeNames(t),
		"coreagent.AllStaticToolNames()'s knowledge_* family must match ADR-067 D7's list — "+
			"if you added a knowledge tool, add it to knowledgeRetrievalTools or "+
			"knowledgeAuthoringTools here AND state its posture in coreAgentSeed for all four "+
			"base agents (SEED RULE — KNOWLEDGE POSTURE, pkg/coreagent/core.go)")
}

// TestCoreAgentSeed_KnowledgeToolsCarrySeededPosture is the FR-070 assertion
// on the seed itself: every knowledge tool in the catalog carries an
// explicit, literal posture on each of the four base agents' own policy maps,
// and that posture is D17's, not deny.
//
// It reads the posture from a REAL SeedConfig-populated config (the same
// composition pkg/gateway's boot sequence performs), not from coreAgentSeed
// in isolation, so a seed that is correct but never reaches the persisted
// agent record still fails.
func TestCoreAgentSeed_KnowledgeToolsCarrySeededPosture(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg), "expected a fresh seed on a fresh DefaultConfig()")

	for agentID, want := range knowledgeSeedMatrix {
		ac := findSeeded(t, cfg, agentID)
		require.NotNilf(t, ac.Tools, "agent %q must carry a tools policy", agentID)
		policies := ac.Tools.Builtin.Policies

		for _, tool := range knowledgeRetrievalTools {
			got, ok := policies[tool]
			assert.Truef(t, ok,
				"(%s, %s) has NO explicit per-agent policy entry — Constraint #6 admits no "+
					"default, and the load-path repair would backfill this to deny with only a "+
					"WARN (FR-071)", agentID, tool)
			assert.Equalf(t, want.retrieval, got,
				"(%s, %s) must be seeded %q per ADR-067 D17", agentID, tool, want.retrieval)
		}
		for _, tool := range knowledgeAuthoringTools {
			got, ok := policies[tool]
			assert.Truef(t, ok,
				"(%s, %s) has NO explicit per-agent policy entry — see FR-071", agentID, tool)
			assert.Equalf(t, want.authoring, got,
				"(%s, %s) must be seeded %q per ADR-067 D17", agentID, tool, want.authoring)
		}
	}
}

// TestCoreAgentSeed_KnowledgeToolsDeniedOffTheBaseRoster is the other half of
// D17's matrix: it names the four base agents and NOBODY else, so every other
// seeded agent must hold an explicit deny.
//
// The Worker is the one that can actually regress. Its map is SPARSE
// (tightenGlobalCeiling), an absent key inherits the global ceiling, and all
// nine knowledge ceilings are "allow" — so dropping the Worker's explicit
// denies would silently GRANT the whole family to every generic delegated
// session in the installation. That is the same "ceiling is allow, so absence
// GRANTS" trap inspect_session, stop_plan and list_jobs each already carry a
// note about in core.go.
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
			assert.Truef(t, ok,
				"(%s, %s) has no explicit entry — for the Worker's sparse map an absent key "+
					"INHERITS the global \"allow\" ceiling, which grants the tool to every "+
					"delegated session at once", ac.ID, tool)
			assert.Equalf(t, config.ToolPolicyDeny, got,
				"(%s, %s) must be an explicit deny — ADR-067 D17 seeds a posture for the four "+
					"base agents and nobody else", ac.ID, tool)
		}
	}
}
