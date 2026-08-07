// Omnipus — ADR-056 / list_jobs seeding tests.
//
// Covers the ROSTER VISIBILITY seed rule stated on coreAgentSeed: list_jobs is
// seeded "allow" for the four base agents and denied to everyone else.
//
// Every assertion here is made against the RESOLVED policy —
// tools.ResolveEffectivePolicy, the real production strictest-wins global x
// agent merge — never against a seed literal. That is not stylistic: this
// branch's reviews found four separate controls whose tests were green while
// the control was broken, every one of them because the test read the seed map
// instead of the merge. A seed "allow" under an "ask" ceiling resolves "ask";
// an ABSENT key in a sparse map resolves to the ceiling, which for list_jobs is
// "allow" — i.e. omission GRANTS. Neither failure is visible in a seed literal.
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

// TestListJobs_ResolvedPostureAcrossSeededRoster is the load-bearing assertion
// for the ROSTER VISIBILITY rule: it pins the resolved list_jobs policy for
// EVERY seeded agent, as a complete partition rather than a spot check, so a
// new agent added to coreAgentSeed cannot land with an unconsidered posture.
//
// The allow set is exactly the four base agents. They are the only durable,
// user-addressable identities and the only agents that can be a plan's
// OwnerAgentID (IsChatTarget) — for anyone else, list_jobs' agent-identity
// scope means "every concurrent run of this role in the installation" rather
// than "this run's work".
func TestListJobs_ResolvedPostureAcrossSeededRoster(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	want := map[coreagent.CoreAgentID]string{
		// The four base agents: chat targets, therefore possible plan owners.
		coreagent.IDMia: "allow",
		coreagent.IDJim: "allow",
		coreagent.IDAva: "allow",
		coreagent.IDRay: "allow",
		// The delegation-only tier. The Worker's map is SPARSE, so its "deny"
		// is only reachable because coreAgentSeed names list_jobs explicitly —
		// an omission there would inherit the "allow" ceiling. The other three
		// are fully-enumerated (denyAllThenOverride), so their deny is stamped.
		coreagent.IDWorker:     "deny",
		coreagent.IDPlanner:    "deny",
		coreagent.IDExplorer:   "deny",
		coreagent.IDResearcher: "deny",
		// System Agents. The Judge is a verifier with no background work of its
		// own; PlanSupervisor is roster-blind by design (D-04).
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
			"agent %q is seeded but has no stated list_jobs posture — ADR-056's ROSTER VISIBILITY rule "+
				"(pkg/coreagent/core.go, coreAgentSeed) requires a DECISION for every seeded agent, not a "+
				"default. Add it to this table and to the seed map together", a.ID)
		assert.Equalf(t, expected, resolveFor(t, cfg, a.ID, "list_jobs", nil),
			"(%s, list_jobs) must RESOLVE %q through the real compositor merge", a.ID, expected)
	}
	require.Equal(t, len(want), seeded,
		"sanity: every agent in the expectation table must actually be seeded, and vice versa — "+
			"a table entry with no seeded agent passes vacuously")
}

// TestListJobs_WorkerExplicitDeny_NotInherited is the targeted guard for the
// one entry that is a trap rather than a preference.
//
// The Worker's seed is a SPARSE tightenGlobalCeiling map: an absent key means
// "inherit the global ceiling", and list_jobs' ceiling is "allow". So deleting
// the Worker's list_jobs entry does not deny the tool — it GRANTS it, silently,
// to the one identity shared by every generic delegated session in the
// installation at once. This asserts the entry is present AND explicit, not
// merely that the resolved value happens to be deny.
func TestListJobs_WorkerExplicitDeny_NotInherited(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	require.Equal(t, "allow", cfg.Sandbox.ToolPolicies["list_jobs"],
		"precondition: this test only means something while the global ceiling is 'allow' — "+
			"if the ceiling changed, re-derive whether the Worker still needs an explicit entry")

	worker := findSeeded(t, cfg, string(coreagent.IDWorker))
	require.NotNil(t, worker.Tools)
	p, ok := worker.Tools.Builtin.Policies["list_jobs"]
	require.True(t, ok,
		"the Worker's sparse map must carry an EXPLICIT \"list_jobs\" entry — an absent key inherits "+
			"the global ceiling, which is 'allow' for this tool")
	assert.Equal(t, config.ToolPolicyDeny, p, "the Worker must be denied list_jobs")
	assert.Equal(t, "deny", resolveFor(t, cfg, string(coreagent.IDWorker), "list_jobs", nil),
		"(Worker, list_jobs) must RESOLVE deny")
}

// TestListJobs_CeilingDoesNotThrottleTheGrant is the inspect_session/plan_correct
// defect check, applied to list_jobs before it can land a third time.
//
// The failure it guards is not "the seed is wrong" — it is "the seed is right
// and the ceiling silently overrules it". A ceiling of "ask" would drag every
// per-agent "allow" here down to "ask", which for Jim specifically means the
// only agent whose stop_plan resolves "allow" (i.e. can contain a runaway plan
// with no human in the loop) can no longer FIND the plan id that stop_plan
// takes. Containment would still look granted and would still depend on a
// human answering a prompt.
func TestListJobs_CeilingDoesNotThrottleTheGrant(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	jim := string(coreagent.IDJim)
	assert.Equal(t, "allow", resolveFor(t, cfg, jim, "list_jobs", nil),
		"(Jim, list_jobs) must resolve allow — a per-agent allow under an ask/deny ceiling resolves "+
			"to the ceiling, not to the grant")
	assert.Equal(t, "allow", resolveFor(t, cfg, jim, "stop_plan", nil),
		"companion half: stop_plan resolves allow, which is the whole reason list_jobs must not be "+
			"gated — the two are only useful together")

	// The same tool under a hypothetical "ask" ceiling, to prove the mechanism
	// this test is defending against is real and not theoretical.
	tightened := map[string]config.ToolPolicy{"list_jobs": config.ToolPolicyAsk}
	assert.Equal(t, "ask", resolveFor(t, cfg, jim, "list_jobs", tightened),
		"mechanism check: an 'ask' ceiling really does merge Jim's seeded 'allow' down to 'ask' — "+
			"which is exactly why pkg/config/defaults.go seeds 'allow'")
}

// TestListJobs_InStaticCatalog is the boot-blocker guard. validateOverrideKeys
// PANICS on a seed override naming a tool that is not in allStaticToolNames, so
// the seed entries added by ADR-056 would take down boot and every test in this
// package if the catalog entry were ever removed while a seed still named it.
// Failing here says so directly instead of via a panic in an unrelated test.
func TestListJobs_InStaticCatalog(t *testing.T) {
	inCatalog := make(map[string]bool)
	for _, n := range coreagent.AllStaticToolNames() {
		inCatalog[n] = true
	}
	assert.True(t, inCatalog["list_jobs"],
		"\"list_jobs\" must be in coreagent.AllStaticToolNames() — the seed maps name it, and "+
			"validateOverrideKeys panics on an override key that is not in the catalog")
}
