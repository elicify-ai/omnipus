// Omnipus — knowledge_tasks is a READ, and is seeded like one.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// # What this file pins, and where the expectation comes from
//
// knowledge_tasks lists the checkbox lines in a knowledge base. It mutates
// nothing. The oracle for that is NOT pkg/coreagent (the thing under test)
// and NOT the ADR's section headings — it is what the tool is specified and
// built to do:
//
//   - ADR-067 D7 prints the name under a heading called "Authoring", but
//     neither the ADR nor the spec states a single requirement, scenario,
//     user story or test for it. Round-1 review finding M-14 recorded exactly
//     that gap for knowledge_link, knowledge_set_property,
//     knowledge_append_section and knowledge_tasks; review rounds 2, 3 and 4
//     never answered it. A heading with no requirement under it is a layout
//     choice, and an oracle cannot be built on one.
//   - The implementation is unambiguous. pkg/knowledge's TasksTool walks the
//     collection, reads notes and regex-matches "- [ ]" / "- [x]" lines. It
//     opens no writer, calls none of author.go's or rename.go's primitives,
//     and emits no mutation audit record — ADR-067 D19 requires one per
//     mutation, and this tool writes none because it performs none. It is
//     rate-limited through the RETRIEVAL limiter, and it answers an
//     out-of-scope collection with FR-053's EMPTY RESULT SET, which is the
//     read contract; every authoring tool refuses instead.
//   - The only other available semantics agree: the operator's `ev` CLI files
//     `tasks` with `read`, `links`, `backlinks` and `unresolved`, apart from
//     `create`, `set`, `append`, `move` and `rename`.
//
// # The defect this locks out
//
// Seeded as authoring, knowledge_tasks resolved "ask" for Mia and Ray while
// knowledge_search resolved "allow" for the same agents. Mia is the install's
// DEFAULT agent, so hers was the prompt an operator actually met: an approval
// modal to LIST the checkboxes in notes whose full TEXT the same agent could
// already read, unprompted, through knowledge_search in the same turn. The
// prompt withheld nothing, so it protected nothing — and a prompt that
// protects nothing is not a harmless extra confirmation. It is training to
// click through the prompts that do protect something, which here are
// knowledge_create, knowledge_move and knowledge_rename, seeded "ask" on the
// same agent and writing to the operator's real disk.
//
// # Why the assertions are shaped as a RELATION, not a value table
//
// The claim being defended is "this tool is a read", not "this tool is
// allow". Pinning literal values would re-state D17's matrix a fourth time
// and would have to be edited by anyone legitimately re-tuning the roster's
// posture — at which point the read/write classification could be quietly
// dropped along with it. Asserting instead that knowledge_tasks resolves to
// whatever the OTHER TWO READS resolve to, for every seeded agent, survives
// any such re-tuning and fails the moment the classification is reverted.
//
// Because a pure relation can be satisfied by moving every read in the wrong
// direction at once, TestKnowledgeTasks_DefaultAgentIsNeverPromptedToListTasks
// below adds an absolute, independently-derived anchor for the two agents the
// defect was live on.
//
// Constraint #6 note: an ABSENT entry is not a loud failure here. The boot
// path's repairAndValidateToolPolicyCoverage repairs BEFORE it validates, and
// the repair writes an explicit "deny" plus one WARN line — boot does not
// abort. A forgotten knowledge tool therefore ships silently denied with the
// feature dead, which is why presence is asserted separately from posture.

package coreagent_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// ktReadFamily is the knowledge-base READ surface: the tools that answer
// questions about a collection without changing it. knowledge_tasks belongs
// here for the reasons in this file's header.
//
// ktPeerReads is the same list minus knowledge_tasks — the tools whose
// posture knowledge_tasks must match.
const ktTasks = "knowledge_tasks"

var ktPeerReads = []string{"knowledge_search", "knowledge_graph"}

// ktStrictness ranks the three verdicts from most permissive to most
// restrictive, so a test can say "never STRICTER than" rather than only
// "equal to". The runtime merge is strictest-wins (deny > ask > allow,
// pkg/tools' resolveEffectivePolicyWith), and this ordering is that one.
func ktStrictness(t *testing.T, policy string) int {
	t.Helper()
	switch policy {
	case string(config.ToolPolicyAllow):
		return 0
	case string(config.ToolPolicyAsk):
		return 1
	case string(config.ToolPolicyDeny):
		return 2
	}
	t.Fatalf("unknown tool policy %q — the three verdicts are allow, ask and deny", policy)
	return 0
}

// ktSeededConfig returns a real fresh-install config with the roster seeded,
// plus every seeded agent's id. It is the same composition pkg/gateway's boot
// sequence performs, so a seed that is correct in coreAgentSeed but never
// reaches the persisted agent record still fails.
func ktSeededConfig(t *testing.T) (*config.Config, []string) {
	t.Helper()
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg),
		"expected a fresh seed on a fresh DefaultConfig()")

	ids := make([]string, 0, len(cfg.Agents.List))
	for _, ac := range cfg.Agents.List {
		ids = append(ids, ac.ID)
	}
	require.NotEmptyf(t, ids,
		"no agents were seeded, so every assertion below would pass over an empty roster — "+
			"the vacuous-green shape docs/internal/false-green-patterns.md exists to catch")
	return cfg, ids
}

// TestKnowledgeTasks_IsSeededWithTheReadsForEverySeededAgent is the seed-side
// assertion: on EVERY seeded agent's own literal policy map, knowledge_tasks
// carries the identical posture to knowledge_search and knowledge_graph.
//
// "Every seeded agent" is deliberate. D17 states a posture for the four base
// agents only, but the Worker, the three specialists, the Judge and the Plan
// Supervisor all carry knowledge entries too, and the Worker's map is SPARSE
// — an absent key there inherits the global "allow" ceiling rather than
// denying. Checking the whole roster is what makes "for each seeded agent"
// mean the roster rather than the four names someone remembered.
func TestKnowledgeTasks_IsSeededWithTheReadsForEverySeededAgent(t *testing.T) {
	cfg, ids := ktSeededConfig(t)

	checked := 0
	for _, id := range ids {
		ac := findSeeded(t, cfg, id)
		require.NotNilf(t, ac.Tools, "agent %q must carry a tools policy", id)
		policies := ac.Tools.Builtin.Policies

		got, ok := policies[ktTasks]
		require.Truef(t, ok,
			"(%s, %s) has NO explicit per-agent entry. Constraint #6 admits no default for a "+
				"static builtin, and this does not abort boot: the load path repairs before it "+
				"validates, backfilling deny with a single WARN, so the tool ships dead (FR-071)",
			id, ktTasks)

		for _, peer := range ktPeerReads {
			want, peerOK := policies[peer]
			require.Truef(t, peerOK,
				"(%s, %s) has no explicit entry either, so this test has no read posture to "+
					"compare against and would pass vacuously", id, peer)
			assert.Equalf(t, want, got,
				"agent %q seeds %s=%q but %s=%q. %s is a READ — it walks the collection and "+
					"regex-matches checkbox lines, opens no writer and emits no mutation audit "+
					"record — so it must be seeded exactly as the other reads are. Seeding it "+
					"with the authoring tools is what put an approval prompt in front of a "+
					"listing whose every byte %s already returns unprompted",
				id, peer, want, ktTasks, got, ktTasks, peer)
		}
		checked++
	}
	require.Equalf(t, len(ids), checked,
		"only %d of %d seeded agents were checked", checked, len(ids))
}

// TestKnowledgeTasks_ResolvesWithTheReadsForEverySeededAgent is the same
// claim run through the REAL production compositor
// (tools.ResolveEffectivePolicy, via resolveFor), which merges the per-agent
// map with pkg/config/defaults.go's global ceiling strictest-wins.
//
// The seed-side test above structurally cannot see the failure this one
// catches: a ceiling entry in defaults.go tighter than the per-agent seed
// silently overrules it, leaving the seed reading exactly right while the
// grant is dead on every install. That precise defect has landed in this
// codebase five times already (inspect_session, the three ADR-052 plan tools,
// plan_correct/stop_plan, list_jobs — each documented in core.go).
func TestKnowledgeTasks_ResolvesWithTheReadsForEverySeededAgent(t *testing.T) {
	cfg, ids := ktSeededConfig(t)

	checked := 0
	for _, id := range ids {
		got := resolveFor(t, cfg, id, ktTasks, nil)
		for _, peer := range ktPeerReads {
			want := resolveFor(t, cfg, id, peer, nil)
			assert.Equalf(t, want, got,
				"agent %q RESOLVES %s to %q but %s to %q, through the real strictest-wins "+
					"merge. Either the per-agent seed disagrees with the read family or the "+
					"global ceiling in pkg/config/defaults.go is quietly overruling it",
				id, ktTasks, got, peer, want)
		}
		checked++
	}
	require.Equalf(t, len(ids), checked,
		"only %d of %d seeded agents were checked", checked, len(ids))
}

// TestKnowledgeTasks_IsNeverStricterThanSearch states the harm directly, and
// in the one direction that matters, so it keeps meaning something even if
// the read family's postures are ever deliberately split apart.
//
// An agent that may read a note's full text through knowledge_search must
// never need an operator's approval to list that same note's checkbox lines.
// The listing is strictly less than the search already returns, so the prompt
// can withhold nothing — it can only teach the operator that these prompts
// are noise, and the prompts standing next to it (knowledge_create,
// knowledge_move, knowledge_rename) are the ones that are not.
func TestKnowledgeTasks_IsNeverStricterThanSearch(t *testing.T) {
	cfg, ids := ktSeededConfig(t)

	for _, id := range ids {
		tasks := resolveFor(t, cfg, id, ktTasks, nil)
		search := resolveFor(t, cfg, id, "knowledge_search", nil)

		assert.LessOrEqualf(t, ktStrictness(t, tasks), ktStrictness(t, search),
			"agent %q resolves %s=%q, STRICTER than knowledge_search=%q. The tool returns a "+
				"strict subset of what knowledge_search already returns for the same notes, so "+
				"the extra gate protects nothing and only devalues the prompts that do",
			id, ktTasks, tasks, search)
	}
}

// TestKnowledgeTasks_DefaultAgentIsNeverPromptedToListTasks is the absolute
// anchor the relational tests above need.
//
// Every other assertion in this file is a relation, and a relation is
// satisfiable by moving BOTH sides the wrong way — seed the whole read family
// "ask" and they all still pass while the operator is prompted for every
// search. This one names the two agents the defect was live on and the value
// they must resolve to.
//
// Mia is the install's default agent (config.Agents.Defaults.DefaultAgentID,
// seeded to her by SeedConfig), so she is the agent an operator actually
// meets. Ray is the Scout, for whom surveying a collection is the whole job.
// Neither may resolve to "ask" or "deny" on a read.
func TestKnowledgeTasks_DefaultAgentIsNeverPromptedToListTasks(t *testing.T) {
	cfg, _ := ktSeededConfig(t)

	require.Equalf(t, string(coreagent.IDMia), cfg.Agents.Defaults.DefaultAgentID,
		"this test's premise is that Mia is the agent an operator meets on a fresh install; "+
			"the seeded default agent is now %q, so re-point the assertion at that agent "+
			"rather than deleting it", cfg.Agents.Defaults.DefaultAgentID)

	for _, id := range []coreagent.CoreAgentID{coreagent.IDMia, coreagent.IDRay} {
		got := resolveFor(t, cfg, string(id), ktTasks, nil)
		assert.Equalf(t, string(config.ToolPolicyAllow), got,
			"agent %q resolves %s to %q. Listing the checkboxes in a knowledge base is a READ, "+
				"and this agent already holds knowledge_search over the same notes unprompted — "+
				"so %q here is an approval modal that withholds nothing from anyone and teaches "+
				"the operator to dismiss the knowledge_create / knowledge_move prompts sitting "+
				"beside it, which are the ones with something behind them",
			id, ktTasks, got, got)
	}
}

// TestKnowledgeTasks_GlobalCeilingMatchesTheReads guards pkg/config/defaults.go
// on its own terms.
//
// The ceiling grants nothing by itself — it caps how far a per-agent policy
// may be granted. But because the merge is strictest-wins, a ceiling on
// knowledge_tasks tighter than the one on knowledge_search would drag every
// agent's read posture down while both files still read as intended. That is
// the failure mode recorded five times over in defaults.go's own comments.
func TestKnowledgeTasks_GlobalCeilingMatchesTheReads(t *testing.T) {
	cfg := config.DefaultConfig()

	got, ok := cfg.Sandbox.ToolPolicies[ktTasks]
	require.Truef(t, ok,
		"%s has no global ceiling entry. Every static builtin needs a literal, wildcard-free "+
			"one (Constraint #6), and the catalog/ceiling pair is asserted one-for-one by "+
			"TestCatalog_MatchesGlobalCeilingEntryForEntry", ktTasks)

	for _, peer := range ktPeerReads {
		want, peerOK := cfg.Sandbox.ToolPolicies[peer]
		require.Truef(t, peerOK, "%s has no global ceiling entry to compare against", peer)
		assert.Equalf(t, want, got,
			"global ceiling: %s=%q but %s=%q. Under the strictest-wins merge a tighter ceiling "+
				"here silently overrules every agent's seeded read posture",
			peer, want, ktTasks, got)
	}
}

// TestKnowledgeTasks_ClassifiedAsRetrievalInTheSharedMatrix keeps this file
// and knowledge_seed_test.go from drifting into disagreement.
//
// knowledge_seed_test.go drives D17's posture matrix off knowledgeRetrievalTools
// / knowledgeAuthoringTools. If knowledge_tasks were moved back into the
// authoring list there, that file would go on passing — it would simply
// assert the authoring posture for it — and only this line would notice.
func TestKnowledgeTasks_ClassifiedAsRetrievalInTheSharedMatrix(t *testing.T) {
	assert.Containsf(t, knowledgeRetrievalTools, ktTasks,
		"%s must sit in knowledgeRetrievalTools. It reads; see this file's header for the "+
			"evidence, which is the tool's behaviour rather than ADR-067 D7's section heading",
		ktTasks)
	assert.NotContainsf(t, knowledgeAuthoringTools, ktTasks,
		"%s must not sit in knowledgeAuthoringTools — that classification is what seeded it "+
			"'ask' for Mia and Ray", ktTasks)

	// Belt and braces: the two lists must still partition the real catalog,
	// so moving the name between them cannot lose it.
	union := append(append([]string{}, knowledgeRetrievalTools...), knowledgeAuthoringTools...)
	assert.ElementsMatchf(t, union, catalogKnowledgeNames(t),
		"the retrieval/authoring split must still cover exactly the catalog's knowledge_* "+
			"family (%s)", fmt.Sprintf("%d names", len(union)))
}
