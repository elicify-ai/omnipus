// Omnipus — ADR-055 / plan-supervisor-spec seeding tests.
//
// Covers FR-001 (the agent exists in all four places), FR-002 (seeded and
// re-enforced identity), FR-003 (never a chat target), FR-005 (the rubric
// constant), FR-006 (catalog surfaces + ceilings), FR-006b (the plan
// containment-parity seed rule), FR-007 (the explicit skill allowlist) and
// FR-008 (the exact one-tool grant).
//
// Every policy assertion that matters is made against the RESOLVED policy —
// tools.ResolveEffectivePolicy, the real production strictest-wins compositor
// merge — not against the seed literal. That distinction is the whole point:
// four prior reviews found seed maps that each looked plausible in isolation
// while the merged outcome was the opposite of the intent (the inspect_session
// inversion), and FR-006b exists because a tool was added to the catalog with
// an allow ceiling, named in no seed map, and therefore shipped DENIED to
// everyone with a fully green suite.
//
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
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestPlanSupervisorAgentID_MatchesToolsConstant pins the two independent
// declarations of the PlanSupervisor identity to each other.
//
// pkg/tools/plan_correct.go gates the entire correction path on the literal
// tools.PlanSupervisorAgentID — deliberately on the exact id rather than on
// Type == system, so a future System Agent cannot inherit correction rights by
// existing. pkg/coreagent independently declares IDPlanSupervisor as the id it
// SEEDS. If those two strings ever diverge, plan_correct denies the very agent
// the seed created and the correction loop is dead on every install, with both
// packages' own tests still green — neither can see the other's constant.
//
// The assertion lives HERE and not in pkg/tools because the import direction
// only works this way: pkg/coreagent may import pkg/tools (it already does, in
// tool_policy_effective_resolution_test.go), while pkg/tools must NOT import
// pkg/coreagent — pkg/sysagent/tools already imports pkg/coreagent, so the
// reverse edge would cycle. pkg/agent/plan_engine.go currently carries a THIRD
// copy of this literal (an unexported planSupervisorAgentID const, added
// because coreagent.IDPlanSupervisor did not exist yet); swapping it to this
// constant is pending and belongs to that file's owner.
func TestPlanSupervisorAgentID_MatchesToolsConstant(t *testing.T) {
	assert.Equal(t, tools.PlanSupervisorAgentID, string(coreagent.IDPlanSupervisor),
		"pkg/tools' correction-authority gate and pkg/coreagent's seeded System Agent id must be the "+
			"SAME string — plan_correct compares the caller id against tools.PlanSupervisorAgentID "+
			"exactly, so any divergence denies the seeded agent its only tool")
}

// --- FR-001: membership in all four places ---------------------------------

// TestSystemAgents_RosterMatchesSystemAgentIDs locks the two independent
// literals FR-001 names — SystemAgents() and systemAgentIDs (via
// IsSystemAgentID) — to the same id set. Membership is deliberately NOT
// derived between them, so omitting either leaves IsSystemAgentID and the
// seeded roster disagreeing: an agent that seeds but is not recognised as a
// System Agent (or the reverse) with nothing to catch it.
func TestSystemAgents_RosterMatchesSystemAgentIDs(t *testing.T) {
	for _, sa := range coreagent.SystemAgents() {
		assert.Truef(t, coreagent.IsSystemAgentID(sa.ID),
			"SystemAgents() lists %q but IsSystemAgentID says it is not a System Agent — "+
				"add it to systemAgentIDs", sa.ID)
		require.NotNilf(t, coreagent.SystemAgentByID(sa.ID),
			"SystemAgentByID must resolve %q", sa.ID)
	}
	// The reverse direction: every id systemAgentIDs recognises must actually
	// be seeded by the roster. IsSystemAgentID is consulted by real callers
	// (pkg/workspace's find_for_agent, pkg/agent's workspace_reroot) that
	// would otherwise special-case an agent nothing ever creates.
	for _, id := range []coreagent.CoreAgentID{coreagent.IDJudge, coreagent.IDPlanSupervisor} {
		require.Truef(t, coreagent.IsSystemAgentID(id), "%q must be a System Agent id", id)
		found := false
		for _, sa := range coreagent.SystemAgents() {
			if sa.ID == id {
				found = true
			}
		}
		assert.Truef(t, found, "IsSystemAgentID recognises %q but SystemAgents() does not seed it", id)
	}
}

// TestPlanSupervisor_NotCoreNotWorker asserts the System-Agents category stays
// DISJOINT from All()/BaseAgents()/the subagent tier for PlanSupervisor, the
// same way TestSystemAgents_RosterDisjointFromAll does for the Judge. ByID and
// IsCoreAgent iterate All(); a System Agent leaking into that list would be
// type-inferred as "core" and become a chat target.
func TestPlanSupervisor_NotCoreNotWorker(t *testing.T) {
	assert.False(t, coreagent.IsCoreAgent(string(coreagent.IDPlanSupervisor)),
		"PlanSupervisor must NOT be classified as a core agent")
	assert.False(t, coreagent.IsSubagentTierID(coreagent.IDPlanSupervisor),
		"PlanSupervisor must NOT be classified as subagent tier")
	assert.Nil(t, coreagent.ByID(coreagent.IDPlanSupervisor),
		"ByID (which iterates All()) must not find a System Agent")
	for _, a := range coreagent.All() {
		assert.NotEqual(t, coreagent.IDPlanSupervisor, a.ID, "All() must not contain PlanSupervisor")
	}
	for _, a := range coreagent.BaseAgents() {
		assert.NotEqual(t, coreagent.IDPlanSupervisor, a.ID, "BaseAgents() must not contain PlanSupervisor")
	}
}

// --- FR-002 / FR-003: seeded identity, and never a chat target -------------

// TestSeed_PlanSupervisorSystemAgent verifies FR-002's five seeded fields and
// FR-003's chat-target exclusion on a fresh install.
func TestSeed_PlanSupervisorSystemAgent(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg), "fresh SeedConfig must report modified=true")

	ps := findSeeded(t, cfg, string(coreagent.IDPlanSupervisor))

	assert.Equal(t, config.AgentTypeSystem, ps.Type, "Type=system")
	assert.True(t, ps.Locked, "Locked=true")
	assert.False(t, ps.Default, "Default=false — a System Agent can never be the routing default")
	require.NotNil(t, ps.MemoryEnabled, "MemoryEnabled must be explicitly set, not left nil")
	assert.False(t, *ps.MemoryEnabled, "MemoryEnabled=false")
	assert.False(t, ps.MemoryEnabledEffective(), "MemoryEnabledEffective() must resolve false")

	// FR-003. IsChatTarget() is !IsWorker() && !IsSystem(), and it guards both
	// Plan.OwnerAgentID write paths as well as chat/routing/delegation target
	// eligibility — so this single assertion is what keeps PlanSupervisor from
	// ever becoming the owner of a plan it is supposed to supervise.
	assert.False(t, ps.IsChatTarget(), "PlanSupervisor must never be a chat target")
	assert.True(t, ps.IsSystem(), "PlanSupervisor must report IsSystem()==true")

	// D-11: no special-cased model tier — the seed leaves Model/Provider unset
	// so an unconfigured PlanSupervisor falls back to the install default like
	// every other built-in agent, and the operator can configure it in the UI.
	assert.Nil(t, ps.Model, "the seed must not pin a model — D-11 leaves it to the install default")
}

// TestSeed_PlanSupervisorReEnforced_Tamper verifies FR-002's every-boot
// re-enforcement: a tampered PlanSupervisor is repaired on the next
// SeedConfig, while its operator-editable model survives.
func TestSeed_PlanSupervisorReEnforced_Tamper(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID != string(coreagent.IDPlanSupervisor) {
			continue
		}
		cfg.Agents.List[i].Locked = false
		cfg.Agents.List[i].Default = true
		cfg.Agents.List[i].Type = config.AgentTypeCustom
		cfg.Agents.List[i].Name = "Renamed"
		cfg.Agents.List[i].Tools.Builtin.Policies["bash"] = config.ToolPolicyAllow
		cfg.Agents.List[i].Tools.Builtin.Policies["plan_correct"] = config.ToolPolicyDeny
		cfg.Agents.List[i].Model = &config.AgentModelConfig{Primary: "operator/model"}
	}

	require.True(t, coreagent.SeedConfig(cfg), "re-enforcement must report modified=true after tamper")

	ps := findSeeded(t, cfg, string(coreagent.IDPlanSupervisor))
	assert.True(t, ps.Locked, "Locked re-enforced")
	assert.False(t, ps.Default, "stray Default cleared")
	assert.Equal(t, config.AgentTypeSystem, ps.Type, "Type re-enforced to system")
	assert.Equal(t, coreagent.PlanSupervisor().Name, ps.Name, "Name re-enforced")
	assert.Equal(t, config.ToolPolicyDeny, ps.Tools.Builtin.Policies["bash"],
		"a stray grant outside the one-tool set must be re-enforced back to deny")
	assert.Equal(t, config.ToolPolicyAllow, ps.Tools.Builtin.Policies["plan_correct"],
		"a stray DENY on the one granted tool must be repaired — otherwise the correction loop is silently dead")
	require.NotNil(t, ps.Model)
	assert.Equal(t, "operator/model", ps.Model.Primary, "operator model edit must survive re-enforcement (D-11)")
}

// TestSeed_PlanSupervisorMemoryReEnforced_BothDirections mirrors the Judge's
// bidirectional memory repair: nil (which resolves TRUE via
// MemoryEnabledEffective's "unset defaults to true" rule) and explicit true
// are both repaired back to explicit false.
func TestSeed_PlanSupervisorMemoryReEnforced_BothDirections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(*config.AgentConfig)
	}{
		{"tampered to explicit true", func(a *config.AgentConfig) { on := true; a.MemoryEnabled = &on }},
		{"tampered/reset to nil", func(a *config.AgentConfig) { a.MemoryEnabled = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			require.True(t, coreagent.SeedConfig(cfg))
			for i := range cfg.Agents.List {
				if cfg.Agents.List[i].ID == string(coreagent.IDPlanSupervisor) {
					tc.tamper(&cfg.Agents.List[i])
				}
			}
			require.True(t, coreagent.SeedConfig(cfg), "re-enforcement must report modified=true after tamper")

			ps := findSeeded(t, cfg, string(coreagent.IDPlanSupervisor))
			require.NotNil(t, ps.MemoryEnabled, "must be repaired back to an explicit value")
			assert.False(t, *ps.MemoryEnabled, "repaired value must be false")
		})
	}
}

// --- FR-007: the explicit, non-nil skill allowlist -------------------------

// TestSeed_PlanSupervisorSkillAllowlist_ExplicitNonNil verifies FR-007/N3.
//
// The failure this guards is silent and severe: a nil Skills slice means
// UNRESTRICTED at skill-resolution time, so seeding PlanSupervisor "like the
// Judge" (which leaves Skills unset) would hand the single most privileged
// agent in the system EVERY installed skill — including any an operator later
// pulls from ClawHub. Nothing about the config would look wrong.
func TestSeed_PlanSupervisorSkillAllowlist_ExplicitNonNil(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	ps := findSeeded(t, cfg, string(coreagent.IDPlanSupervisor))
	require.NotNil(t, ps.Skills,
		"PlanSupervisor's skill allowlist must be EXPLICIT — nil means unrestricted, i.e. every skill")
	assert.Equal(t, []string{"plan", "define-goal"}, ps.Skills,
		"PlanSupervisor is granted exactly these two skills — plan (the re-planning playbook its "+
			"rubric derives from) and define-goal (the ADR-074 D4 criteria-authoring quality bar, "+
			"renamed from define-done by ADR-080 D-SKILL); an explicit amendment of "+
			"plan-supervisor-spec FR-007/N3's original \"exactly one\"")
}

// TestSeed_PlanSupervisorSkillAllowlist_ReEnforced verifies the allowlist is
// repaired on every boot in BOTH failure directions — cleared to nil (which
// silently becomes unrestricted) and widened with an extra skill. This is
// stricter than the core-agent loop, which preserves an operator's skill
// edits, because for a System Agent the allowlist is a role invariant.
func TestSeed_PlanSupervisorSkillAllowlist_ReEnforced(t *testing.T) {
	// ADR-074 D4 note: the enforced allowlist is now exactly the PAIR
	// {plan, define-goal} (renamed from define-done by ADR-080 D-SKILL) —
	// the "widened with an extra skill" case below legalizes exactly this
	// shape for exactly these two names, and any OTHER widening (a third
	// skill, a substitution, a narrowing back to one) must still be
	// reverted to the pair on the next boot.
	for _, tc := range []struct {
		name   string
		tamper []string
	}{
		{"cleared to nil (would resolve UNRESTRICTED)", nil},
		{"widened with an extra skill", []string{"plan", "define-goal", "skill-authoring"}},
		{"replaced entirely", []string{"daily-briefing"}},
		{"narrowed back to plan alone", []string{"plan"}},
		{"reordered pair rewritten to canonical order", []string{"define-goal", "plan"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			require.True(t, coreagent.SeedConfig(cfg))
			for i := range cfg.Agents.List {
				if cfg.Agents.List[i].ID == string(coreagent.IDPlanSupervisor) {
					cfg.Agents.List[i].Skills = tc.tamper
				}
			}
			require.True(t, coreagent.SeedConfig(cfg), "re-enforcement must report modified=true after tamper")

			ps := findSeeded(t, cfg, string(coreagent.IDPlanSupervisor))
			assert.Equal(t, []string{"plan", "define-goal"}, ps.Skills,
				"the seeded allowlist must be restored exactly")
		})
	}
}

// TestSeed_JudgeSkillAllowlistUnchanged records — as a deliberate decision
// rather than an oversight — that ADR-055 did NOT narrow the Judge's skills.
// The Judge ships with no allowlist (i.e. unrestricted) today; narrowing it is
// out of this change's scope and would be a behaviour change smuggled in under
// a PlanSupervisor commit. If that gap is ever closed, this test is the place
// it gets recorded.
func TestSeed_JudgeSkillAllowlistUnchanged(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))
	j := findSeeded(t, cfg, string(coreagent.IDJudge))
	assert.Nil(t, j.Skills,
		"the Judge's (unrestricted) skill posture is unchanged by ADR-055 — "+
			"if you are narrowing it, update this test and say so explicitly")
}

// --- FR-008: the exact one-tool grant, as a complement ---------------------

// TestPlanSupervisorSeed_ExactlyPlanCorrect verifies FR-008 over the SEED
// literal: plan_correct allow, ToolSearch allow and Skill allow (the two
// structural floors every agent gets — CLAUDE.md constraint 6 / ADR-072 D1 —
// applying even to the most locked-down agent in the system), and every
// other name in the static catalog deny. Stated as a COMPLEMENT rather than
// a list, so a tool added to the catalog later can never silently land in
// PlanSupervisor's allow set.
func TestPlanSupervisorSeed_ExactlyPlanCorrect(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	ps := findSeeded(t, cfg, string(coreagent.IDPlanSupervisor))
	require.NotNil(t, ps.Tools, "PlanSupervisor must carry an explicit tools policy")
	pol := ps.Tools.Builtin.Policies
	catalog := coreagent.AllStaticToolNames()
	require.Len(t, pol, len(catalog),
		"policy must enumerate the whole static catalog, one literal entry each (Constraint #6)")

	allowedNames := map[string]bool{"plan_correct": true, "ToolSearch": true, "Skill": true}
	for _, name := range catalog {
		p, ok := pol[name]
		require.Truef(t, ok, "policy must enumerate tool %q (no default fallback)", name)
		if allowedNames[name] {
			assert.Equalf(t, config.ToolPolicyAllow, p, "%q must be allow", name)
			continue
		}
		assert.Equalf(t, config.ToolPolicyDeny, p,
			"%q must be deny — PlanSupervisor's grant is exactly plan_correct plus the ToolSearch "+
				"and Skill structural floors; if you are widening it further, amend this test "+
				"deliberately (the complement failing IS the guard working)", name)
	}

	// Named call-outs for the withheld grants the spec argues about at length,
	// so a future reader sees the decision rather than inferring it from the
	// sweep above. list_jobs is named for D-04 specifically: PlanSupervisor is
	// roster-blind BY DESIGN — an adjudicator that could see it had three
	// parked plans would have a reason to act outside the single wake it was
	// given, which is the opposite of "one correction per wake".
	for _, withheld := range []string{
		"bash", "write_file", "read_file", "list_directory",
		"inspect_session", "stop_plan", "execute_plan", "list_jobs",
	} {
		assert.Equalf(t, config.ToolPolicyDeny, pol[withheld],
			"%q is deliberately withheld from PlanSupervisor", withheld)
	}
}

// TestPlanSupervisorResolved_ExactlyPlanCorrect is the same complement, but
// through the REAL compositor merge against the real fresh-install ceiling.
// This is the assertion that actually matters: the seed literal above could be
// perfect while an "ask"/"deny" ceiling on plan_correct silently overrules it
// under strictest-wins and leaves the correction loop dead on every install —
// which is precisely what a deny ceiling did to the Judge's inspect_session.
func TestPlanSupervisorResolved_ExactlyPlanCorrect(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	id := string(coreagent.IDPlanSupervisor)
	assert.Equal(t, "allow", resolveFor(t, cfg, id, "plan_correct", nil),
		"(PlanSupervisor, plan_correct) must RESOLVE allow through the real compositor — "+
			"a per-agent allow under an ask/deny ceiling resolves to the ceiling, not the grant")
	assert.Equal(t, "allow", resolveFor(t, cfg, id, "ToolSearch", nil),
		"(PlanSupervisor, ToolSearch) must RESOLVE allow — the structural floor every agent "+
			"gets applies even to the most locked-down agent in the system")
	assert.Equal(t, "allow", resolveFor(t, cfg, id, "Skill", nil),
		"(PlanSupervisor, Skill) must RESOLVE allow — the ADR-072 D1 structural floor every "+
			"agent gets applies even to the most locked-down agent in the system")

	for _, name := range coreagent.AllStaticToolNames() {
		if name == "plan_correct" || name == "ToolSearch" || name == "Skill" {
			continue
		}
		assert.Equalf(t, "deny", resolveFor(t, cfg, id, name, nil),
			"(PlanSupervisor, %s) must resolve deny — its grant is exactly plan_correct plus "+
				"the ToolSearch and Skill structural floors", name)
	}
}

// TestPlanSupervisor_ZeroToolPolicyCoverageGaps verifies the Constraint #6
// boot matrix stays total for the new agent: no (plansupervisor, tool) pair
// falls through to a default. A gap here aborts boot with a listed agent x
// tool report, so this is a boot-blocker guard, not a style check.
func TestPlanSupervisor_ZeroToolPolicyCoverageGaps(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	known := make(map[string]struct{})
	for _, n := range coreagent.AllStaticToolNames() {
		known[n] = struct{}{}
	}
	for _, g := range config.ValidateToolPolicyCoverage(cfg, known) {
		assert.NotEqualf(t, string(coreagent.IDPlanSupervisor), g.AgentID,
			"PlanSupervisor must have ZERO coverage gaps; found gap for tool %q", g.ToolName)
	}
}

// --- FR-006b: plan containment parity, asserted as an OUTCOME --------------

// TestPlanContainmentParity_ResolvedPolicy is the load-bearing test for
// FR-006b. It asserts the PROPERTY, not the seed:
//
//	for every seeded agent that can own a plan:
//	    resolved(execute_plan) != deny  ⟹  resolved(stop_plan) != deny
//
// i.e. NO AGENT CAN START A PLAN IT CANNOT STOP.
//
// Four things about how it is written are deliberate:
//
//   - It walks the WHOLE seeded roster read from cfg.Agents.List, not a
//     hardcoded list of agent ids, so a new agent added to coreAgentSeed is
//     covered the day it lands rather than the day someone remembers to add
//     it here. That is the entire reason FR-006b is phrased as a rule over the
//     seed instead of a list of agents.
//   - It resolves through tools.ResolveEffectivePolicy — the real
//     strictest-wins global x agent merge — so it cannot be satisfied by a
//     seed map that merges away. Reading the seed maps directly is what let
//     the original defect ship green.
//   - It is an implication, not an equality. execute_plan and stop_plan do NOT
//     resolve to the same value today and are not required to: execute_plan's
//     ceiling is "ask" while stop_plan's is "allow", so Jim resolves
//     (ask, allow). Asserting equality would force the ceilings to mirror,
//     which is exactly the "symmetry" FR-006 forbids.
//   - The population is agents that can OWN a plan, tested with IsChatTarget()
//     — the exact predicate (!IsWorker() && !IsSystem()) the engine uses to
//     guard both Plan.OwnerAgentID write paths. This is a real exemption, not
//     a convenience: the Worker's sparse map omits execute_plan and therefore
//     INHERITS the ceiling's "ask", while FR-006b exception 1 requires its
//     stop_plan be an explicit "deny" — so on the raw policy numbers the
//     Worker looks like "can start, cannot stop". It is safe only because the
//     Worker can never be a plan's owner, so the premise is structurally
//     unreachable for it. The companion assertion below pins that premise, so
//     if the owner gate ever stops excluding workers this exemption fails
//     loudly instead of silently protecting nothing.
func TestPlanContainmentParity_ResolvedPolicy(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	require.NotEmpty(t, cfg.Agents.List, "precondition: the roster must be seeded")

	checked := 0
	for _, a := range cfg.Agents.List {
		if a.Tools == nil {
			continue
		}
		if !a.IsChatTarget() {
			// Cannot be a plan's owner_agent_id — see the doc comment. The
			// exemption's premise is asserted separately below.
			continue
		}
		exec := resolveFor(t, cfg, a.ID, "execute_plan", nil)
		stop := resolveFor(t, cfg, a.ID, "stop_plan", nil)
		checked++
		if exec == "deny" {
			continue
		}
		assert.NotEqualf(t, "deny", stop,
			"agent %q resolves %q for execute_plan but %q for stop_plan — it can START a plan it cannot STOP. "+
				"Seed rule (FR-006b): wherever execute_plan is seeded, seed stop_plan in the same map at the "+
				"same literal value; a sparse map that omits execute_plan needs an explicit stop_plan deny",
			a.ID, exec, stop)
	}
	require.GreaterOrEqual(t, checked, 4,
		"sanity: the sweep must have covered the four chat-target base agents, not silently skipped everything")
}

// TestPlanContainmentParity_NonOwnersCannotStartAPlan pins the premise the
// parity sweep above exempts non-chat-targets on. Every seeded agent that is
// NOT a chat target must be structurally incapable of owning a plan — that,
// not its tool policy, is what makes "resolves ask for execute_plan but deny
// for stop_plan" acceptable for the Worker.
//
// Worth stating plainly because it is the one place the containment story
// rests on something other than policy: the Worker resolves "ask" for
// execute_plan (its sparse map omits the tool, so it inherits the ceiling)
// while its stop_plan is an explicit deny. If the engine's owner gate ever
// stops keying on IsChatTarget, the Worker becomes an agent that can start a
// plan it cannot stop, and this test is what says so.
func TestPlanContainmentParity_NonOwnersCannotStartAPlan(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	for _, id := range []coreagent.CoreAgentID{
		coreagent.IDWorker, coreagent.IDPlanner, coreagent.IDExplorer,
		coreagent.IDResearcher, coreagent.IDJudge, coreagent.IDPlanSupervisor,
	} {
		a := findSeeded(t, cfg, string(id))
		assert.Falsef(t, a.IsChatTarget(),
			"%q must not be a chat target — IsChatTarget guards both Plan.OwnerAgentID write paths, "+
				"and it is the ONLY reason this agent may hold a non-deny execute_plan alongside a "+
				"deny stop_plan", id)
	}
}

// TestPlanContainmentParity_OwnerStopsOwnPlan pins the concrete case the
// "allow" stop_plan ceiling exists for (dataset B11): the orchestrator who is
// the only agent seeded to START a plan unprompted resolves ALLOW for
// stop_plan — unprompted containment. An "ask" ceiling on stop_plan resolves
// "ask" instead, and stopping a runaway plan depends on a human answering a
// prompt.
func TestPlanContainmentParity_OwnerStopsOwnPlan(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	assert.Equal(t, "allow", resolveFor(t, cfg, string(coreagent.IDJim), "stop_plan", nil),
		"(Jim, stop_plan) must resolve allow on a fresh install — he is the only agent seeded to start "+
			"a plan unprompted, so he must be able to stop one unprompted too")
	// The companion half. This asserted "ask" until 2026-07-28, describing the
	// two ceilings as "deliberately asymmetric". They were not: execute_plan's
	// "ask" ceiling was the ADR-052 defect silently overruling Jim's seeded
	// "allow", and stop_plan only escaped it because its own ceiling had
	// already been raised for this very test's reason. Both now resolve allow,
	// which is what FR-006b's parity rule asks for in the first place — an
	// agent that can start a plan unprompted can stop one unprompted.
	assert.Equal(t, "allow", resolveFor(t, cfg, string(coreagent.IDJim), "execute_plan", nil),
		"(Jim, execute_plan) must resolve allow on a fresh install — parity with his stop_plan; "+
			"an 'ask' here means the global ceiling has been tightened back and is overruling his seed")
}

// TestPlanContainmentParity_WorkerExplicitDeny covers FR-006b's exception 1.
// The Worker's map is SPARSE (absent key == inherit the global ceiling), and
// stop_plan's ceiling is "allow" — so leaving stop_plan absent there would
// silently GRANT it. This is the same trap inspect_session already carries an
// explicit deny for in that map.
func TestPlanContainmentParity_WorkerExplicitDeny(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	worker := findSeeded(t, cfg, string(coreagent.IDWorker))
	require.NotNil(t, worker.Tools)
	for _, name := range []string{"stop_plan", "plan_correct"} {
		p, ok := worker.Tools.Builtin.Policies[name]
		require.Truef(t, ok,
			"the Worker's sparse map must carry an EXPLICIT %q entry — an absent key inherits the "+
				"global ceiling, which is 'allow' for this tool", name)
		assert.Equalf(t, config.ToolPolicyDeny, p, "the Worker must be denied %q", name)
		assert.Equalf(t, "deny", resolveFor(t, cfg, string(coreagent.IDWorker), name, nil),
			"(Worker, %s) must RESOLVE deny", name)
	}
	// The Worker holds a non-deny execute_plan alongside a deny stop_plan. That
	// is FR-006b exception 1, and the premise it rests on is IsChatTarget()
	// == false (it can never be a Plan.OwnerAgentID, so "starts a plan it
	// cannot stop" is unreachable) — NOT, as this assertion used to claim, the
	// fact that its sparse map omitted execute_plan. Omission stopped meaning
	// "ask" on 2026-07-28 when the ceiling was raised to "allow"; the entry is
	// now explicit, and its RESOLVED value is unchanged at "ask".
	//
	// Assert the resolved value and the real premise, so this test fails if
	// either the Worker's posture widens or it ever becomes a chat target.
	execPolicy, hasExec := worker.Tools.Builtin.Policies["execute_plan"]
	require.True(t, hasExec,
		"the Worker's sparse map must carry an EXPLICIT execute_plan entry — an absent key inherits "+
			"the global ceiling, which is now 'allow'")
	assert.Equal(t, config.ToolPolicyAsk, execPolicy,
		"the Worker must be seeded ask for execute_plan (unchanged posture, now stated explicitly)")
	assert.Equal(t, "ask", resolveFor(t, cfg, string(coreagent.IDWorker), "execute_plan", nil),
		"(Worker, execute_plan) must RESOLVE ask — raising the ceiling must not have widened it to allow")
	assert.False(t, worker.IsChatTarget(),
		"the Worker must not be a chat target — that, not the shape of its policy map, is what makes "+
			"'ask for execute_plan alongside deny for stop_plan' acceptable (FR-006b exception 1)")
}

// TestPlanContainmentParity_NewCustomAgent covers the FR-006b rule at the
// agent-CREATION path (NewCustomAgentToolsCfg — the shared seed both the REST
// POST /api/v1/agents handler and the create_agent tool call). A freshly
// created custom agent IS a chat target and therefore CAN own a plan, so it is
// inside the parity property's population; it satisfies the rule the other
// way, by being denied both tools. That map is fully enumerated
// (denyAllThenOverride), so unlike the Worker's sparse map it cannot inherit
// the "allow" ceiling by omission.
func TestPlanContainmentParity_NewCustomAgent(t *testing.T) {
	pol := coreagent.NewCustomAgentToolsCfg().Builtin.Policies
	for _, name := range []string{"execute_plan", "stop_plan", "plan_correct"} {
		p, ok := pol[name]
		require.Truef(t, ok, "a new custom agent's policy must enumerate %q", name)
		assert.Equalf(t, config.ToolPolicyDeny, p,
			"a new custom agent must start denied for %q — it opts in explicitly. If you ever grant "+
				"execute_plan here, FR-006b requires stop_plan beside it at the same value", name)
	}
}

// --- FR-006: the catalog surfaces --------------------------------------

// TestCatalog_MatchesGlobalCeilingEntryForEntry is the mechanical assertion
// FR-006 asks for in place of a quoted count: pkg/coreagent's
// allStaticToolNames literal and pkg/config's global sandbox.tool_policies
// ceiling must enumerate the EXACT SAME names, one for one.
//
// It lives here rather than in pkg/config because package config cannot
// import pkg/coreagent (pkg/coreagent already imports pkg/config — that
// direction is a cycle), which is the only reason defaults_test.go still
// carries a hardcoded count at all. Adding a tool to one side and not the
// other is caught here by NAME, so the failure message says which tool is
// missing from which surface instead of just "expected 85, got 86".
func TestCatalog_MatchesGlobalCeilingEntryForEntry(t *testing.T) {
	cfg := config.DefaultConfig()
	catalog := coreagent.AllStaticToolNames()

	inCatalog := make(map[string]bool, len(catalog))
	for _, n := range catalog {
		assert.Falsef(t, inCatalog[n], "duplicate entry %q in allStaticToolNames", n)
		inCatalog[n] = true
	}
	for _, n := range catalog {
		_, ok := cfg.Sandbox.ToolPolicies[n]
		assert.Truef(t, ok,
			"%q is in coreagent.allStaticToolNames but has NO global ceiling entry in "+
				"pkg/config/defaults.go — every catalog name needs one (Constraint #6)", n)
	}
	for n := range cfg.Sandbox.ToolPolicies {
		assert.Truef(t, inCatalog[n],
			"%q has a global ceiling entry in pkg/config/defaults.go but is NOT in "+
				"coreagent.allStaticToolNames — a dead ceiling entry, or a catalog omission "+
				"that ships the tool denied-by-default to every seeded agent", n)
	}
	assert.Equal(t, len(catalog), len(cfg.Sandbox.ToolPolicies),
		"the catalog and the global ceiling must be one-for-one")
}

// TestCatalog_ContainsADR055Tools is the targeted guard that both new names
// reached the catalog, mirroring TestSeedConfig_FreshInstall_ADR052ToolsFullyCovered.
// A name missing here does not merely fail a count: validateOverrideKeys
// PANICS on a seed override naming an unknown tool, so a seed referencing it
// would take down boot and every test in this package.
func TestCatalog_ContainsADR055Tools(t *testing.T) {
	inCatalog := make(map[string]bool)
	for _, n := range coreagent.AllStaticToolNames() {
		inCatalog[n] = true
	}
	for _, n := range []string{"plan_correct", "stop_plan"} {
		assert.Truef(t, inCatalog[n], "%q must be in coreagent.AllStaticToolNames()", n)
	}
}

// --- FR-005: the rubric constant ------------------------------------------

// TestPlanSupervisorDefaultRubric_CoversRequiredTopics guards the rubric
// against being gutted or replaced with a stub. It is deliberately a
// TOPIC-COVERAGE check, not a golden-text comparison: the spec marks the text
// as a first draft open to tuning (RISK-12), so pinning it byte-for-byte would
// make every prompt-engineering improvement a test edit. What must NOT change
// silently is the set of rules it carries — each of these is a rule the
// adjudication either depends on or is unsafe without.
func TestPlanSupervisorDefaultRubric_CoversRequiredTopics(t *testing.T) {
	r := coreagent.PlanSupervisorDefaultRubric
	require.NotEmpty(t, strings.TrimSpace(r), "the rubric must not be empty")

	for _, tc := range []struct {
		needle string
		why    string
	}{
		{"Plan Supervisor", "the role statement"},
		{"DEFINITION-OF-DONE UNMET", "the DoD-unmet wake kind"},
		{"STALLED", "the stall wake kind — a stall must not be answered with a DoD verdict"},
		{"immutable", "DoD immutability, the one non-negotiable rule"},
		{"SUPERSEDE", "verb selection"},
		{"TARGETED-RETRY", "verb selection"},
		{"APPEND", "verb selection"},
		{"ABANDON", "the honest exit as a first-class verb"},
		{"falsified assumption", "the audit trail every correction must carry"},
		{"One correction per wake", "the boundary that keeps the adjudicator from churning"},
		{"plan_correct", "the single tool it is allowed to call"},
	} {
		assert.Containsf(t, r, tc.needle,
			"the rubric must still cover %s (looking for %q) — if you are intentionally rewording it, "+
				"update this needle rather than dropping the rule", tc.why, tc.needle)
	}
}

// TestSystemAgentDefaultSoul verifies the id-generic accessor the
// soul-materialising seam reads, so pkg/agent's write helper never has to
// hardcode a per-agent constant reference.
func TestSystemAgentDefaultSoul(t *testing.T) {
	assert.Equal(t, coreagent.JudgeDefaultRubric,
		coreagent.SystemAgentDefaultSoul(coreagent.IDJudge))
	assert.Equal(t, coreagent.PlanSupervisorDefaultRubric,
		coreagent.SystemAgentDefaultSoul(coreagent.IDPlanSupervisor))
	assert.Empty(t, coreagent.SystemAgentDefaultSoul(coreagent.IDMia),
		"a core agent's prompt lives in the compiled prompts map, not here")
	assert.Empty(t, coreagent.SystemAgentDefaultSoul("nope"),
		"an unknown id must return empty, never a fallback soul")

	// Every System Agent must have a default soul: a System Agent has no
	// compiled prompt entry (both are excluded from All() and from init()'s
	// compiled-prompt invariant), so an empty default here means it boots with
	// no identity at all.
	for _, sa := range coreagent.SystemAgents() {
		assert.NotEmptyf(t, coreagent.SystemAgentDefaultSoul(sa.ID),
			"System Agent %q has no default soul — it has no compiled prompt either, so it would "+
				"boot with an empty identity", sa.ID)
	}
}
