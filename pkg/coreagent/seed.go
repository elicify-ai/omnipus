// seed.go: SeedConfig — write the fresh-install roster and defaults into a config

package coreagent

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// allStaticToolNames is the complete, hardcoded enumeration of every static
// builtin tool name known to the platform:
//
//   - 35 general builtin tools (pkg/tools/*.go, excluding pkg/tools/browser and
//     the dynamic MCP-adapter tool names, which are per-server and can't be
//     statically enumerated — see the Constraint #6 MCP exception). The count
//     was stated as 31 until plan-supervisor-spec FR-006 surface 1 required
//     this comment corrected in the same edit: recall_conversation (the 4th
//     memory tool) and message_parent (ADR-053 §5.1) were both added to the
//     literal below without the prose being updated. ADR-056's list_jobs then
//     took it from 33 to 34, and ADR-072's Skill tool took it from 34 to 35.
//   - 11 browser-automation tools (pkg/tools/browser/tools.go +
//     pkg/tools/browser/tabs.go).
//   - 35 sysagent management tools (pkg/sysagent/tools/*.go).
//   - 4 ADR-052 (autonomous agent plan execution) planning/verifier tools —
//     create_plan, execute_plan, run_task, inspect_session. Registered here
//     ahead of / independent of their pkg/tools|pkg/sysagent/tools
//     implementation landing (this literal and buildKnownBuiltinToolNames,
//     pkg/gateway/gateway.go, are the two catalogs the tool-policy-coverage
//     invariant is checked against — FR-027), so a seeded-override
//     referencing one of these four names does not panic boot via
//     validateOverrideKeys below.
//   - 2 ADR-055 (PlanSupervisor) supervision/containment tools — plan_correct
//     and stop_plan, registered here for the same reason and under the same
//     rule as the ADR-052 four.
//   - 6 ADR-068 (vault-records knowledge base) tools, split by BLAST RADIUS
//     rather than read/write — 3 read (knowledge_describe, knowledge_find,
//     knowledge_read), knowledge_edit (one named file), knowledge_restructure
//     (cascading rename/move/trash) and knowledge_configure (the schema/view
//     control plane) — registered here for the same reason and under the
//     same rule as the ADR-052 four. See D15.3/FR-070. Supersedes ADR-067's
//     nine (knowledge_search, knowledge_graph, knowledge_create,
//     knowledge_link, knowledge_set_property, knowledge_append_section,
//     knowledge_tasks, knowledge_move, knowledge_rename), now retired from
//     this literal; the split below says why the last two carry their own
//     policy line rather than sharing knowledge_edit's.
//   - ADR-056's list_jobs is counted in the 34 general tools above (it is a
//     ScopeGeneral tool in pkg/tools, not a separate tier); it is called out
//     here only because its seeded posture is a rule of its own — see
//     coreAgentSeed's ROSTER VISIBILITY rule.
//   - 1 ADR-081 (D11, unified-search-and-grep-spec.md FR-008/FR-009) tool —
//     grep, the recursive file-name/content search agent tool. Registered
//     here ahead of / independent of its own implementation package landing,
//     under exactly the same rule and for the same two reasons as the
//     ADR-052 four and the ADR-068 six above (validateOverrideKeys panics on
//     an override key absent from this literal; the tool-policy-coverage
//     universe derives its gap list from this same set, via
//     pkg/gateway/gateway.go's buildKnownBuiltinToolNames). FR-009's founder
//     ruling: global ceiling "allow", EXPLICIT "allow" seeded for every
//     agent tier — Mia/Jim/Ava/Admin, the Worker, the specialist tier, and
//     every system agent — a read-only tool confined to the calling agent's
//     own workspace root and mounts (FR-020), with no posture left to
//     silent inheritance anywhere in the roster.
//
// Do NOT treat the per-category counts above as the authority for the total:
// they are prose and go stale (they twice did). The mechanical assertion
// len(AllStaticToolNames()) == len(config.DefaultConfig().Sandbox.ToolPolicies)
// (TestCatalog_MatchesGlobalCeilingEntryForEntry) is what actually enforces
// this literal and pkg/config/defaults.go's global ceiling stay one-for-one.
//
// This is a hardcoded Go literal, NOT computed by importing pkg/tools or
// pkg/sysagent/tools: pkg/sysagent/tools/agent.go already imports
// pkg/coreagent, so the reverse import would create a cycle. A boot-time hard
// validator (pkg/gateway) independently enumerates the real tool registry and
// aborts boot on any agent × tool coverage gap, so a drift here is caught
// loudly rather than silently falling back to a default.
var allStaticToolNames = []string{
	// General builtin tools.
	"bash",
	"read_file", "write_file", "list_directory", "edit_file", "append_file",
	// library_list / library_read (D3, library-spec): scoped facades over a
	// workspace's own .library/ dual-write directory (D-1) — see
	// pkg/agent.LibraryDirName / pkg/tools/library_tool.go.
	"library_list", "library_read",
	// request_mount (ADR-063 FR-7.2): seeded "ask" everywhere — the whole
	// point is that the operator approves each folder.
	"request_mount",
	// list_mounts (ADR-068 §4): the read-only counterpart to request_mount —
	// it enumerates folders the operator has ALREADY approved and mutates
	// nothing, so it is seeded "allow", not "ask". An "ask" here would
	// re-introduce a human prompt for reading back a grant the human just
	// made. This name MUST stay in this literal: validateOverrideKeys panics
	// on an override key absent from it.
	"list_mounts",
	"search_web", "fetch_url",
	"send_message", "switch_agent", "send_file",
	"find_skills", "install_skill",
	// Skill (ADR-072 D1): the on-demand skill load/search tool, wired into
	// this literal alongside ToolSearch below — see its "Structural floor"
	// comment on the seed entries for why every agent carries an explicit
	// entry for it.
	"Skill",
	"delegate", "message_parent",
	// AskUserQuestion (askuserquestion-tool-spec v3, ADR-074 D4b): the
	// owner-session structured clarification card. Seeded ALLOW for every
	// human-facing agent (core roster, subagent tier, customs' default
	// allowlist) — asking the user is the safety-increasing direction, and an
	// `ask`-gate on asking (approval to ask a question) is absurd; do not
	// "harden" it later. Judge and PlanSupervisor resolve explicit DENY via
	// their denyAllThenOverride stamps (they can never be session owners; an
	// advertised always-erroring tool violates their minimal seeds).
	"AskUserQuestion",
	// set_goal (ADR-088 D2, work-first-goal-flow-spec FR-004): the validated
	// write-path over the goal record (definition/criteria/DoD), seeded
	// ALLOW for every human-facing agent alongside AskUserQuestion — it can
	// only ever touch the CALLING session's own record, and refuses on a
	// delegated sub-turn or a goalless session via its own scope
	// preconditions, not via policy. Judge and PlanSupervisor resolve
	// explicit DENY via their denyAllThenOverride stamps (they never run
	// /goal sessions); Worker resolves explicit DENY via adr090SparseRolePolicies
	// (same "ceiling is allow, so absence GRANTS" trap plan_correct/
	// stop_plan/inspect_session already document).
	"set_goal",
	// goal_claim (ADR-084 D12, JUDGE-FR-087-FR-089): the tool-call claim
	// channel, seeded per Constraint #6/ADR-077 in every per-agent seed map.
	// Its values match set_goal's everywhere EXCEPT the Worker, which is allow
	// here and deny for set_goal: a native task run finishes only through an
	// upheld goal_claim (ADR-084 §11, ADR-043 §8, issue #710) and the Worker
	// takes task runs, while set_goal stays refused on task sessions. Its own
	// preconditions (not policy) refuse a delegated sub-turn that is not a
	// task's own run, and a goalless session. An unnamed override key here
	// PANICS validateOverrideKeys at boot.
	"goal_claim",
	"list_tasks", "create_task", "update_task", "delete_task", "list_agents",
	"remember", "recall_memory", "run_retrospective", "recall_conversation",
	"serve_web",
	"set_todos",
	"read_inbox", "search_email", "read_message", "send_email", "reply",
	"ToolSearch",
	// ADR-056 — the unified read-only background-job roster (plans owned,
	// subagents delegated, standalone tasks assigned to or created by the
	// caller). Listed in the general block because that is what it is
	// (ScopeGeneral, pkg/tools/list_jobs.go), not in the ADR-052/055 planning
	// groups below.
	"list_jobs",

	// Browser automation tools.
	"browser_navigate", "browser_click", "browser_type", "browser_screenshot",
	"browser_get_text", "browser_wait", "browser_evaluate",
	// Browser tab-management tools (ADR-041 D3).
	"browser_list_tabs", "browser_switch_tab", "browser_close_tab", "browser_open_tab",
	// ADR-075 D2 — the interaction verbs and the accessibility snapshot.
	//
	// THIS BLOCK AND THE POLICY MAPS BELOW ARE ONE COMMIT, NOT AN ORDERING.
	// validateOverrideKeys PANICS on an override key absent from this literal,
	// so the per-agent maps cannot precede it — and this literal must not
	// precede THEM either: denyAllThenOverride stamps an explicit deny for
	// every name here that an agent does not override, which COMPLETES policy
	// coverage with no signal anywhere that the seed itself is one-sided.
	// Landing this line alone ships tools that are registered, listed in the
	// catalog, and refuse every call on every agent, with no signal anywhere.
	// See the capability spec §2.5.
	//
	// browser_upload_file's NAME is here while its REGISTRATION is held by
	// FR-029 (issue #659). Held means unregistered, not unseeded: the catalog
	// drift test compares this literal against the metadata catalog, and a
	// seeded name with no registration is inert.
	"browser_select_option", "browser_press_key", "browser_hover",
	"browser_snapshot", "browser_upload_file",
	// ADR-075 D2 Stream C — the dialog recovery verb. Same one-commit rule as
	// the block above, and the same reason: this name landing alone would make
	// denyAllThenOverride stamp an explicit deny on every seeded agent, which
	// COMPLETES coverage and suppresses the single WARN the repair pass would
	// otherwise log. The result would be a registered recovery verb that
	// refuses on every agent, on a green build, with no diagnostic anywhere —
	// and the tool whose entire job is to un-wedge a tab is the worst possible
	// one to ship silently inert.
	"browser_handle_dialog",
	// browser_handover (ADR-085 BROWSER-FR-046/FR-051, C-70): the agent's own
	// stand-down verb — defers its own browser calls and hands the wheel to
	// the operator. Registered here, in Mia/Jim/Ava's role defaults and in
	// pkg/config/defaults.go's ceiling, all in one commit — the same
	// one-commit rule as the block above: an unnamed override key here
	// PANICS validateOverrideKeys at boot, and landing this name alone ships
	// a tool that resolves deny on every agent with no signal anywhere.
	"browser_handover",

	// Sysagent management tools.
	"create_workspace", "update_workspace", "delete_workspace", "list_workspaces", "get_workspace",
	"read_agent_metadata",
	"configure_provider", "list_providers", "test_provider", "list_models",
	"run_doctor", "get_usage",
	"add_mcp_server", "remove_mcp_server", "list_mcp_servers",
	"create_skill", "edit_skill",
	"create_task_in_workspace", "update_task_in_workspace", "delete_task_in_workspace", "list_tasks_in_workspace",
	"remove_skill", "list_skills",
	"enable_channel", "configure_channel", "disable_channel", "list_channels", "test_channel",
	"get_config", "set_config",
	"create_agent", "get_agent", "get_agent_tools", "update_agent", "delete_agent",

	// ADR-052 — autonomous agent plan execution: planning tools + the
	// verifier-role-only inspect_session tool.
	"create_plan", "execute_plan", "run_task", "inspect_session",

	// ADR-055 (plan-supervisor-spec FR-006) — the supervision/containment
	// pair. plan_correct is PlanSupervisor's ONLY tool (the correction verb
	// set: append / supersede / targeted_retry / abandon); stop_plan is the
	// plan owner's containment tool. Both are listed here BEFORE any seed
	// map names them, because validateOverrideKeys panics on an override key
	// that is not in this literal.
	"plan_correct", "stop_plan",

	// ADR-068 D15.3 (FR-070) — the knowledge-base tool family, SIX names
	// split by BLAST RADIUS rather than by read/write, superseding ADR-067's
	// nine (knowledge_search, knowledge_graph, knowledge_create,
	// knowledge_link, knowledge_set_property, knowledge_append_section,
	// knowledge_tasks, knowledge_move, knowledge_rename — all RETIRED from
	// this literal; their Go implementations survive only because
	// pkg/gateway/rest_knowledge.go's own REST endpoints, independent of the
	// agent tool-calling surface, still call them). Listed here ahead of /
	// independent of the pkg/knowledge / pkg/vaultprops implementation
	// landing, under exactly the same rule as the ADR-052 four and the
	// ADR-055 pair above, and for two distinct reasons:
	//
	//  1. validateOverrideKeys PANICS on a seed override naming a tool that
	//     is not in this literal, so these names must exist here before
	//     coreAgentSeed below can state a posture for any of them.
	//  2. Being in the catalog is what makes the seeding FALSIFIABLE.
	//     config.ValidateToolPolicyCoverage and
	//     config.RepairIncompleteToolPolicyCoverage both derive their gap
	//     list from this universe (via pkg/gateway's
	//     buildKnownBuiltinToolNames, which mirrors this literal), and both
	//     return NOTHING for a name the universe does not contain. A
	//     knowledge tool omitted here is therefore not merely uncovered — it
	//     is invisible to the boot-time check, and every test asserting "no
	//     knowledge tool was backfilled to deny" passes vacuously.
	//
	// FR-071 is the failure this guards: the boot path repairs BEFORE it
	// validates (pkg/gateway/gateway.go's
	// repairAndValidateToolPolicyCoverage), and the repair backfills a gap
	// with an explicit "deny" plus one WARN line. Boot does NOT abort. A
	// forgotten knowledge tool ships silently denied with the feature dead.
	//
	// THE SPLIT, AND WHY IT REPLACES read/write AS THE SEEDING AXIS:
	//
	//   - READ tier — touch nothing outside what the caller asked for.
	//     knowledge_describe (orientation: schema, saved views, index
	//     state), knowledge_find (the one retrieval surface: words, typed
	//     filter, saved views, relations, tasks — the FR-076a replacement
	//     for the retired knowledge_tasks), knowledge_read (one note or one
	//     section).
	//   - knowledge_edit — mutates exactly the ONE file the caller named
	//     (create / set_property / append_section / link / replace_body,
	//     unified — ADR-068 folds knowledge_create, knowledge_link,
	//     knowledge_set_property and knowledge_append_section into this one
	//     name because all four have the identical blast radius: one
	//     caller-named file).
	//   - knowledge_restructure — rename / move / trash / restore. CASCADES:
	//     a rename/move rewrites inbound links in every note that referenced
	//     the target, none of which the caller named — the ADR-067
	//     knowledge_move/knowledge_rename pair's own justification for being
	//     one operation ("the same act — a note's path changes and every
	//     inbound link must follow") extended to trash/restore, which
	//     touches the SAME journal/link-rewrite machinery.
	//   - knowledge_configure — the control plane: a record-type schema
	//     edit reclassifies every record of that type already on disk, and a
	//     saved view changes what a name resolves to for every future
	//     caller. Neither touches a note's bytes, but both change what
	//     EXISTING notes MEAN — a strictly wider blast radius than
	//     knowledge_edit's one file, which is why it carries its own policy
	//     line rather than sharing knowledge_edit's.
	"knowledge_describe", "knowledge_find", "knowledge_read",
	// knowledge_list (KB-2a, defect-list-knowledge-base-ux-2026-09-08.md,
	// founder-ratified 2026-09-08) — also read tier, listed here for the
	// SAME two reasons this whole block states: validateOverrideKeys panics
	// on an unknown name, and the coverage universe (buildKnownBuiltinToolNames)
	// must contain it or a gap in it is invisible to the boot-time check.
	"knowledge_list",
	"knowledge_edit",
	"knowledge_restructure",
	"knowledge_configure",
	// knowledge_base_create (KB-1, same defect list) — makes a NEW knowledge
	// base in the workspace's own Library, distinct from knowledge_edit's
	// create op (which adds a note to one that already exists). Listed here
	// for the same two reasons.
	"knowledge_base_create",

	// ADR-081 D11 (unified-search-and-grep-spec.md FR-008/FR-009) — the
	// file-search/grep agent tool. Listed here ahead of / independent of its
	// own implementation package landing, under exactly the same rule as the
	// ADR-052 four and the ADR-068 six above: validateOverrideKeys PANICS on
	// a seed override naming a tool absent from this literal, and the
	// tool-policy-coverage universe (config.ValidateToolPolicyCoverage /
	// RepairIncompleteToolPolicyCoverage, via pkg/gateway's
	// buildKnownBuiltinToolNames, which mirrors this literal) derives its
	// gap list from this same set — a name missing here is invisible to
	// both, not merely uncovered (FR-071's failure mode). FR-009's founder
	// ruling: ceiling "allow", EXPLICIT "allow" seeded for every agent
	// tier (Mia/Jim/Ava/Admin, the Worker, the specialist tier, every system
	// agent) — a read-only tool confined to the calling agent's own
	// workspace root and mounts only (FR-020), with no posture left to
	// silent inheritance anywhere in the roster.
	"grep",

	// ADR-090 environment setup (docs/internal/specs/adr-090-environment-setup-spec.md
	// ES-FR-01) — the environment setup tool. Installation execution is
	// GENERIC under the founder's 2026-09-18 Option A ruling: dependency
	// knowledge lives in skills and task plans, never in tool
	// implementation. Shipped at the
	// global ceiling as "ask": the existing tool-approval mechanism IS the
	// approval. Founder ruling (2026-09-18): setup permission is set at BOTH
	// levels — the ceiling asks, AND every agent permitted to set up
	// environments carries an explicit stored "ask" of its own (Mia, General
	// Purpose, Admin via the deliberate-posture list in
	// adr090SparseRolePolicies; new custom native agents via the constructor
	// below), so a later ceiling raise loosens none of them. Jim, Ava,
	// Planner and Researcher persist an explicit deny; Judge and Plan
	// Supervisor stay locked deny via systemAgentSeed. Same one-commit rule
	// as every block above — the ceiling entry (pkg/config/defaults.go's
	// defaultToolPoliciesGeneral), the per-agent seeds and this literal land
	// together.
	"environment_setup",
}

// AllStaticToolNames returns a copy of the full static builtin tool-name
// catalog this package's seed functions enumerate against (denyAllThenOverride,
// etc.). Exported so other packages (e.g. pkg/gateway's tool-catalog drift
// test) can verify their own independently-derived "all known tools" set
// stays in sync with this one, without needing to hand-copy it.
func AllStaticToolNames() []string {
	out := make([]string, len(allStaticToolNames))
	copy(out, allStaticToolNames)
	return out
}

// validateOverrideKeys panics if any key in overrides is not a member of
// allStaticToolNames. Shared typo/rename-safety net for denyAllThenOverride
// for the fixed system roles: every call site's override map is a hardcoded Go
// literal (never user/request/config-derived — NewCustomAgentToolsCfg runs
// per agent-creation request, but its override keys are still compile-time
// literals, not data), so a panic — an immediate, loud test/boot/first-call
// failure — is the right disposition here, not an error return that a
// caller could plausibly ignore.
func validateOverrideKeys(overrides map[string]config.ToolPolicy) {
	for name := range overrides {
		known := false
		for _, n := range allStaticToolNames {
			if n == name {
				known = true
				break
			}
		}
		if !known {
			panic(fmt.Sprintf(
				"tool-policy override for unknown tool %q — not in allStaticToolNames (typo, or a tool renamed since this seed was written?)",
				name,
			))
		}
	}
}

// denyAllThenOverride returns a fully-enumerated policy map covering every
// name in allStaticToolNames, starting every tool at "deny" and then applying
// the given overrides. This is the mechanism the no-default-policy-fallback
// rule requires: every tool-policy decision is an explicit, literal entry —
// there is no DefaultPolicy field and no code-level allow/deny fallback. The
// returned map is an independent allocation — callers may mutate it safely.
//
// overrides keys MUST already be members of allStaticToolNames — see
// validateOverrideKeys. Coverage validation only checks for MISSING keys,
// never extra/unrecognized ones, so a typo'd or retired tool name here would
// otherwise leave the tool the caller actually meant to override stuck at its
// "deny" default with no signal anywhere.
func denyAllThenOverride(overrides map[string]config.ToolPolicy) map[string]config.ToolPolicy {
	validateOverrideKeys(overrides)
	out := make(map[string]config.ToolPolicy, len(allStaticToolNames))
	for _, name := range allStaticToolNames {
		out[name] = config.ToolPolicyDeny
	}
	for name, policy := range overrides {
		out[name] = policy
	}
	return out
}

// coreAgentSeed returns the ordinary built-in's sparse overrides relative to
// the global ceiling. ADR090RolePolicyInventory defines the role defaults;
// users may edit these capabilities without changing the protected identity.
func coreAgentSeed(id CoreAgentID) map[string]config.ToolPolicy {
	return adr090SparseRolePolicies(id)
}

// coreAgentSkills returns fresh-install role assignments. Installed packages
// are available only when assigned; an empty assignment grants no skills.
func coreAgentSkills(id CoreAgentID) []string {
	switch id {
	case IDMia:
		return []string{"interview", "handoff", "define-goal", "inbox-triage", "elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"}
	case IDJim:
		return []string{"interview", "orchestrate", "plan", "define-goal"}
	case IDAva:
		return []string{"interview", "agent-authoring", "skill-authoring", "tool-mapping", "skill-mapping", "delegation-graph", "workspace-team"}
	case IDAdmin:
		return []string{"interview", "mcp-install", "provider-setup", "channel-setup", "doctor"}
	case IDPlanner:
		return []string{"plan", "define-goal"}
	case IDResearcher:
		return []string{"deep-research"}
	case IDWorker:
		return []string{"elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"}
	default:
		return nil
	}
}

// HasSystemAllowsInConstructorSeed returns true if the named core agent's
// constructor seed contains explicit system.* allow entries (FR-062).
// Today only Ava qualifies. This is the predicate for the boot-time
// "critical abort on corrupt config" path.
func HasSystemAllowsInConstructorSeed(agentID string) bool {
	return CoreAgentID(agentID) == IDAva
}

// coreAgentDelegation supplies fresh workspace graph defaults: Jim can assign
// Planner, Researcher, General Purpose, himself and Ava for configuration proposals;
// Planner can ask Researcher;
// General Purpose can create same-role helpers. Other roles have no seeded edges.
// Task/background/await are agent delegation modes. Workspace creation translates
// background/await into the graph's direct mode; this return value is not a graph.
func coreAgentDelegation(id CoreAgentID) *config.DelegationPolicy {
	ref := func(agentID CoreAgentID) config.AgentRef {
		return config.AgentRef{Kind: config.AgentRefKindLocal, ID: string(agentID)}
	}
	switch id {
	case IDJim:
		return &config.DelegationPolicy{
			To: []config.AgentRef{ref(IDPlanner), ref(IDResearcher), ref(IDWorker), ref(IDJim), ref(IDAva)},
			Modes: []config.DelegationMode{
				config.DelegationModeTask,
				config.DelegationModeBackground,
				config.DelegationModeAwait,
			},
		}
	case IDPlanner:
		// Planner can gather research before authoring the plan.
		return &config.DelegationPolicy{
			To: []config.AgentRef{ref(IDResearcher)},
			Modes: []config.DelegationMode{
				config.DelegationModeAwait,
				config.DelegationModeTask,
			},
			Depth: intPtr(2),
		}
	case IDWorker:
		return &config.DelegationPolicy{
			To:    []config.AgentRef{ref(IDWorker)},
			Modes: []config.DelegationMode{config.DelegationModeTask, config.DelegationModeBackground, config.DelegationModeAwait},
		}
	default:
		// Other roles receive no onward delegation by default.
		return nil
	}
}

// intPtr returns a pointer to v. Used to set DelegationPolicy.Depth in seeds.
func intPtr(v int) *int { return &v }

// boolPtr returns a pointer to v. Used to set AgentConfig.MemoryEnabled in
// seeds (ADR-052 FR-039) — a pointer is required to distinguish "never set"
// (nil, defaults to true) from an explicit false.
func boolPtr(v bool) *bool { return &v }

// timePtr returns a pointer to v. Used to set AgentConfig.CreatedAt in seeds
// (ADR-054 D2) — a pointer is required to distinguish "never set" (nil, a
// pre-ADR-054 record) from a genuinely-zero timestamp, mirroring UpdatedAt.
func timePtr(v time.Time) *time.Time { return &v }

// SeedDelegationEdges returns the seeded canonical delegation policy for a
// core agent ID (ADR-037, Wave 2). AgentConfig.DelegationPolicy — the field
// this used to be copied onto at boot — has been removed entirely; the
// per-workspace delegation graph (pkg/workspace/delegation.go) is the sole
// runtime delegation-enforcement mechanism. pkg/gateway's
// defaultWorkspaceDelegationEdges now calls this directly to bootstrap a
// fresh workspace's delegation graph, instead of reading
// cfg.Agents.List[i].DelegationPolicy (which no longer exists). Returns nil
// for any ID with no seeded delegation policy.
func SeedDelegationEdges(id CoreAgentID) *config.DelegationPolicy {
	return coreAgentDelegation(id)
}

// FreshSelfDelegationMaxDepth is the ADR-090 FR-006 pin for a freshly seeded
// Jim→Jim or Worker→Worker workspace edge: 3, or the lower configured global
// ceiling. Applied per-edge by SeededEdgeDepth — never as a role-wide
// DelegationPolicy.Depth, which would also clamp Jim's staff edges.
const FreshSelfDelegationMaxDepth = 3

// SeededEdgeDepth returns the depth pointer a freshly seeded workspace edge
// should carry. Permitted self-edges (Jim, General Purpose) get an explicit
// min(FreshSelfDelegationMaxDepth, ceiling) so a raised global cap cannot
// deepen a fresh self-chain beyond 3, and a lowered cap cannot cause
// Validate to drop the edge. Every other pair copies policyDepth (nil stays
// inherit). ceiling is the already-resolved effective global cap
// (delegationDepthCeiling / workspaceDelegationDepthCeiling); non-positive
// is treated as unset and the pin stays 3.
func SeededEdgeDepth(from, to string, policyDepth *int, ceiling int) *int {
	if from == to && (from == string(IDJim) || from == string(IDWorker)) {
		d := FreshSelfDelegationMaxDepth
		if ceiling > 0 && ceiling < d {
			d = ceiling
		}
		return &d
	}
	if policyDepth == nil {
		return nil
	}
	d := *policyDepth
	return &d
}

// seedMu owns SeedConfig's read-all-then-append sequence (ADR-054 D6 rule 4,
// M-7). SeedConfig builds an `existing` set from the current roster, then
// appends any missing core agent — with no lock, two concurrent callers (e.g.
// a boot racing a hot-reload re-seed, or two goroutines in a test) can each
// observe "Mia missing" and both append her, leaving two "mia" entries.
// Today's only production call site (pkg/gateway's boot sequence) invokes
// SeedConfig once, single-threaded, before serving traffic — so this is a
// defense-in-depth close of the race the ADR named, not a fix for an observed
// double-seed in production. This is an IN-PROCESS mutex only; it does not
// protect against two separate OS processes racing the same config.json —
// that is the cross-process pidfile/lockfile concern D3/D4 assign elsewhere
// (pkg/entity, out of this package's scope).
var seedMu sync.Mutex

// seedConfig carries the shared state of SeedConfig across its stages.
type seedConfig struct {
	cfg            *config.Config
	existing       map[string]bool
	modified       bool
	isFreshInstall bool
}

// SeedConfig ensures all core agents exist in cfg.Agents.List with Locked=true
// and with the correct constructor-seeded tool policy (FR-010, FR-022).
//
// Creates missing agents and re-enforces Locked=true + identity fields on
// existing core agents (prevents config tampering from downgrading protection).
// Policy seeds are applied to agents that have no existing Tools config — agents
// that were manually configured via the SPA keep their existing policy entries.
//
// Returns true if config was modified (caller should save).
func SeedConfig(cfg *config.Config) bool {
	sc := &seedConfig{cfg: cfg}

	seedMu.Lock()
	defer seedMu.Unlock()

	sc.existing = make(map[string]bool, len(sc.cfg.Agents.List))
	for _, a := range sc.cfg.Agents.List {
		sc.existing[a.ID] = true
	}

	sc.modified = false

	sc.seedFreshInstallDefaults()

	// Re-enforce identity fields on existing core agents (tamper protection + rename).
	for i := range sc.cfg.Agents.List {
		ca := ByID(CoreAgentID(sc.cfg.Agents.List[i].ID))
		if ca == nil {
			continue
		}
		a := &sc.cfg.Agents.List[i]
		if !a.Locked {
			a.Locked = true
			sc.modified = true
		}
		if a.Name != ca.Name {
			a.Name = ca.Name
			sc.modified = true
		}
		if a.Description != ca.Description {
			a.Description = ca.Description
			sc.modified = true
		}
		if a.Color != ca.Color {
			a.Color = ca.Color
			sc.modified = true
		}
		if a.Icon != ca.Icon {
			a.Icon = ca.Icon
			sc.modified = true
		}
		// Fresh-install-only skill-allowlist seed (ADR-072 D5.1, FR-034).
		// Under D5, an empty/absent Skills list means "the operator granted
		// nothing" — a valid, deliberate state — not "never configured". This
		// block used to run on every boot (guarded only by len(a.Skills)==0),
		// framed as an idempotent migration for installs that predated
		// allowlists (FR-9.4). D5.1 is greenfield with no such installs to
		// migrate (§6.2), and re-running it on every boot would silently
		// restore a grant list the operator later emptied on purpose — the
		// exact ADR-054 D6.4 "reports success, doesn't stick" failure mode.
		// Gating on isFreshInstall (same flag as the AutoRecap/DefaultAgentID
		// seeds above) makes this fire once, on the very first boot, and
		// never again. Do NOT restore this to an unconditional migration.
		if sc.isFreshInstall && len(a.Skills) == 0 {
			if seedSkills := coreAgentSkills(ca.ID); len(seedSkills) > 0 {
				a.Skills = seedSkills
				sc.modified = true
			}
		}

		// Idempotent Type re-enforcement (tamper protection). The subagent tier
		// (worker + specialists) MUST carry Type=worker and base agents Type=core —
		// these classify routing and chat-target eligibility, so a tampered/absent
		// Type is corrected on every boot. A subagent-tier agent can never be the
		// default: clear a stray Default flag too.
		wantType := config.AgentTypeCore
		if IsSubagentTierID(ca.ID) {
			wantType = config.AgentTypeWorker
		}
		if a.Type != wantType {
			a.Type = wantType
			sc.modified = true
		}
		if IsSubagentTierID(ca.ID) {
			if a.Default {
				a.Default = false
				sc.modified = true
			}
			// Idempotent executor migration: the seeded subagent-tier agents run
			// native. Fill the executor only when the existing entry has none, so an
			// operator who pointed it at an external-cli/remote runtime keeps their
			// choice.
			if a.Subagents == nil {
				a.Subagents = &config.SubagentsConfig{}
			}
			if a.Subagents.Executor == nil {
				a.Subagents.Executor = &config.ExecutorConfig{Kind: config.ExecutorKindNative}
				sc.modified = true
			}
		}

		// ADR-037: the per-agent delegation-policy migration that used to live here
		// (seeding AgentConfig.DelegationPolicy on existing agents) is retired —
		// that field no longer exists. Delegation seeding now happens only at
		// workspace-creation time via SeedDelegationEdges, reading coreAgentDelegation
		// directly (see pkg/gateway/rest_workspace_delegation.go's
		// defaultWorkspaceDelegationEdges) — there is nothing left to migrate onto
		// AgentConfig itself.

		// ADR-036: bash is now universally registered (like the old `exec`),
		// governed exclusively by ToolPolicyCfg — there is no more
		// experimental.workspace_shell_enabled gate to default here. Jim's
		// "bash": allow entry (coreAgentSeed above / the re-enforcement loop's
		// existing policy repair) is the only thing needed for him to get
		// shell access on a fresh install.

		// Heartbeat is now workspace-scoped (ADR-027): per-agent heartbeat fields
		// are decommissioned. No migration needed — the workspace handler seeds
		// heartbeat on first opt-in. Existing per-agent heartbeat_enabled /
		// heartbeat_interval in config.json are ignored (unknown fields on load).
	}

	for _, ca := range All() {
		if sc.existing[string(ca.ID)] {
			continue
		}
		policies := coreAgentSeed(ca.ID)
		isSubagentTier := IsSubagentTierID(ca.ID)
		// Mia is the default agent on fresh installs: she appears first in the
		// All() list and is the friendliest entry-point for new users. A subagent
		// (worker or specialist) is NEVER the default — it is not a chat target.
		// Only set Default=true on the fresh-seed path (here). The re-enforcement
		// loop above intentionally does NOT touch the Default field on existing
		// entries so operator choices survive config reload.
		isDefault := ca.ID == IDMia
		// Type: the subagent tier (worker + specialists) is Type=worker; every
		// other seeded agent is a base/core agent.
		agentType := config.AgentTypeCore
		if isSubagentTier {
			agentType = config.AgentTypeWorker
		}
		newAgent := config.AgentConfig{
			ID:          string(ca.ID),
			Name:        ca.Name,
			Description: ca.Description,
			Color:       ca.Color,
			Icon:        ca.Icon,
			Type:        agentType,
			Locked:      true,
			Default:     isDefault,
			CreatedAt:   timePtr(time.Now().UTC()),
			// Per-agent skill allowlist (FR-9.4): default-DENY enforced at skill
			// resolution. Nil for agents with no seeded skills (unrestricted).
			Skills: coreAgentSkills(ca.ID),
			// ADR-037: no more AgentConfig.DelegationPolicy field to seed here —
			// the workspace-graph seed (SeedDelegationEdges, reading
			// coreAgentDelegation) is applied at workspace-creation time instead
			// (pkg/gateway/rest_workspace_delegation.go's
			// defaultWorkspaceDelegationEdges), not at agent-creation time.
			Tools: &config.AgentToolsCfg{
				Builtin: config.AgentBuiltinToolsCfg{
					Policies: policies,
				},
			},
		}
		// Subagent-tier agents carry an executor (Spec-4): the seeded worker AND the
		// specialists run native (inside the Omnipus agent loop). Stored on the
		// EXISTING Subagents.Executor field — no parallel field.
		if isSubagentTier {
			newAgent.Subagents = &config.SubagentsConfig{
				Executor: &config.ExecutorConfig{Kind: config.ExecutorKindNative},
			}
		}
		sc.cfg.Agents.List = append(sc.cfg.Agents.List, newAgent)
		sc.modified = true
	}

	// --- System Agents (ADR-049 D3) ---
	// Seeded via a path SEPARATE from the All() core/worker loops above so a
	// System Agent (the Judge) is never classified as core (ByID/IsCoreAgent
	// iterate All(), which excludes SystemAgents()). Every identity/type/locked/
	// tool-policy field is re-enforced on EVERY boot (tamper protection, mirrors
	// the core re-enforcement loop); only Model/Provider and the soul
	// (SOUL.md, lazily materialized from JudgeDefaultRubric — ADR-052 FR-038)
	// are operator-editable and therefore preserved across boots.
	if seedSystemAgents(sc.cfg, sc.existing) {
		sc.modified = true
	}

	// ADR-074 D4: one-shot, marker-keyed, additive-only define-done migration
	// for existing installs. Runs AFTER the seeding loops so a fresh install's
	// just-seeded lists (which already contain define-goal via coreAgentSkills
	// — ADR-080 D-SKILL renamed the seeded grant — take no append via the
	// define-goal guard inside applyDefineDoneSkillsMigration below) and only
	// the marker is recorded.
	// ADR-090 is a fresh-build roster. Historical skill and policy migrations
	// remain defined for old release branches but do not run on this seed path.

	return sc.modified
}

// seedFreshInstallDefaults enables recap defaults and selects Mia when the roster is empty.
func (sc *seedConfig) seedFreshInstallDefaults() {
	// Fresh-install defaults: enable recap + bootstrap recap so new installs
	// get session summaries out of the box. Only fires when NO agents exist
	// yet (the agents list is empty — the hallmark of a first boot). Existing
	// configs keep their stored values; SeedConfig runs on every boot so
	// touching these fields unconditionally would override operator changes.
	sc.isFreshInstall = len(sc.existing) == 0
	if sc.isFreshInstall {
		if !sc.cfg.Agents.Defaults.AutoRecapEnabled {
			sc.cfg.Agents.Defaults.AutoRecapEnabled = true
			sc.modified = true
		}
		if !sc.cfg.Agents.Defaults.BootstrapRecapEnabled {
			sc.cfg.Agents.Defaults.BootstrapRecapEnabled = true
			sc.modified = true
		}
	}

	// RELEASE BLOCKER fix: Mia being "the default agent" on a fresh install
	// was previously ONLY expressed via the per-entity AgentConfig.Default
	// stamp on her fresh-seed record below (isDefault := ca.ID == IDMia) —
	// but ADR-054 D6.4 moved default-agent RESOLUTION entirely to the
	// settings singleton (cfg.Agents.Defaults.DefaultAgentID; see
	// pkg/agent.AgentRegistry.GetDefaultAgent and
	// pkg/routing.RouteResolver.resolveDefaultAgentID) and nothing ever seeded
	// THAT field, so a fresh install had NO configured default at all:
	// webchat and channel routing each fell back to a DIFFERENT priority-2/3
	// default and disagreed. Seed the singleton here, on the exact same
	// isFreshInstall gate the AutoRecap seed above uses, so a fresh install's
	// actual resolved default matches the documented "Mia is default" intent.
	// The per-entity Default:true stamp on Mia's fresh-seed record below is
	// UNCHANGED (kept for backward display compatibility per config.go's
	// ADR-054 D6.4 note) — this only adds the singleton write alongside it.
	// Guarded by "still empty" so an operator's pre-boot env override
	// (OMNIPUS_DEFAULT_AGENT_ID) is never clobbered, and so this is a
	// fresh-install-only seed, not a re-enforcement that would overwrite an
	// operator's later choice on every subsequent boot.
	if sc.isFreshInstall && strings.TrimSpace(sc.cfg.Agents.Defaults.DefaultAgentID) == "" {
		sc.cfg.Agents.Defaults.DefaultAgentID = string(IDMia)
		sc.modified = true
	}
}

// ToolPolicyUpdateWorkerGoalClaimAllow is the marker recorded in
// config.seeded_tool_policy_updates once the one-time Worker goal_claim
// update has run on an install. Exported so pkg/gateway can persist it into
// config.json after SeedConfig (SeedConfig itself does no file I/O).
const ToolPolicyUpdateWorkerGoalClaimAllow = "adr084-worker-goal-claim-allow"

// applyWorkerGoalClaimAllowUpdate moves the Worker's stored goal_claim from
// "deny" to "allow", once per install.
//
// Why: a native task run completes only when its goal_claim is upheld by the
// Judge (ADR-084 §11, ADR-043 §8, issue #710), so an agent that resolves
// goal_claim deny can never finish a task assigned to it. The founder
// decided on 2026-09-15 that goal_claim is allowed by default for every
// agent. Fresh installs get that from the seed; installs seeded before the
// change stored the old value and keep it forever unless something updates
// it, because stored per-agent policy is data that neither SeedConfig nor
// config.ReconcileToolPolicyCeiling rewrites (ADR-076 D2, ADR-077 D2).
//
// Scope, from git history: the only seed that ever wrote an explicit
// goal_claim deny for an agent this update may touch is the Worker's
// (coreAgentSeed's IDWorker branch, commit 1addf6e1d, 2026-09-12). The Judge
// and PlanSupervisor also carry deny, but their whole tool policy is
// re-applied from systemAgentSeed on every boot (seedSystemAgents), so their
// value is the seed's current value and is not this update's to change. Every
// other seed has always written allow, so a deny on any other agent is an
// operator's choice and is left alone.
//
// A stored deny on the Worker cannot be told apart from an operator who set
// the same value, so the update flips it exactly once: the marker is written
// in the same pass, and once it is present this function does nothing, so a
// deny the operator sets afterwards stays. Semantics:
//   - Marker present: no-op.
//   - Marker absent: for the agent with id "worker" only, a stored goal_claim
//     of exactly "deny" becomes "allow". An absent entry stays absent (it
//     already resolves from the ceiling); "ask" and "allow" are left as they
//     are. No other key and no other agent is touched. The marker is recorded
//     whether or not anything flipped.
//
// This is not the retired fail-closed backfill (ADR-077 D3): it never adds an
// entry, never denies anything, runs once, and changes one named value on one
// named agent.
//
// Returns true when it modified cfg (always, when the marker was absent,
// because recording the marker is itself a modification).
func applyWorkerGoalClaimAllowUpdate(cfg *config.Config) bool {
	for _, marker := range cfg.SeededToolPolicyUpdates {
		if marker == ToolPolicyUpdateWorkerGoalClaimAllow {
			return false
		}
	}
	const goalClaim = "goal_claim"
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		if a.ID != string(IDWorker) || a.Tools == nil {
			continue
		}
		if a.Tools.Builtin.Policies[goalClaim] == config.ToolPolicyDeny {
			a.Tools.Builtin.Policies[goalClaim] = config.ToolPolicyAllow
		}
	}
	cfg.SeededToolPolicyUpdates = append(cfg.SeededToolPolicyUpdates, ToolPolicyUpdateWorkerGoalClaimAllow)
	return true
}

// SkillsMigrationDefineDone is the ADR-074 D4 marker recorded in
// config.seeded_skill_grants once the one-shot define-done allowlist migration
// has run on an install. Exported so pkg/gateway can persist the marker into
// config.json after SeedConfig (SeedConfig itself is a pure config-struct
// mutation with zero filesystem side effects — see its doc comment).
const SkillsMigrationDefineDone = "adr074-define-done"

// applyDefineDoneSkillsMigration is the ADR-074 D4 marker-keyed migration.
//
// Background: the fresh-install gate on the core-roster skill seed
// (isFreshInstall && len(a.Skills)==0 above) makes adding "define-done" to
// coreAgentSkills a silent no-op on every EXISTING install, and ADR-072 D5.1
// explicitly prohibits re-running the seed ("would silently restore a grant
// list the operator later emptied on purpose"). This migration is the narrow,
// argued exception ADR-074 D4 records: it appends a grant that has NEVER
// existed before, which cannot restore anything — additive-only, run once,
// keyed by the SkillsMigrationDefineDone marker.
//
// Semantics, exactly as ratified:
//   - Marker present → no-op in full (second boot is byte-identical).
//   - Marker absent → for each CORE-ROSTER agent whose compiled-in seed
//     carries an allowlist (coreAgentSkills != nil): append "define-done"
//     only when the live list is non-nil AND non-empty AND lacks it AND
//     lacks its ADR-080 rename "define-goal" (a list that already carries
//     the renamed grant — e.g. a genuinely fresh install seeded directly
//     from coreAgentSkills, which now returns "define-goal" — is already
//     granted in substance; appending the OLD name onto it would reintroduce
//     define-done onto an install that never had it, defeating the D-SKILL
//     rename this same boot's applyDefineGoalRenameMigration performs).
//   - Nil stays nil (unrestricted already resolves every installed skill).
//   - Empty [] stays empty (an operator who zeroed the list opted out —
//     respected, per ADR-072 D5.1).
//   - User-created agents and System Agents are never touched (ByID only
//     resolves the core/worker roster; PlanSupervisor's grant propagates via
//     seedSystemAgents' exact-equality re-enforcement instead).
//   - The marker is recorded in the SAME SeedConfig pass as the appends, so
//     both land in one config mutation; the caller persists them together.
//
// Returns true when it modified cfg (it always does when the marker was
// absent, because writing the marker is itself a modification).
func applyDefineDoneSkillsMigration(cfg *config.Config) bool {
	for _, marker := range cfg.SeededSkillGrants {
		if marker == SkillsMigrationDefineDone {
			return false
		}
	}
	const (
		skillDefineDone = "define-done"
		skillDefineGoal = "define-goal"
	)
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		ca := ByID(CoreAgentID(a.ID))
		if ca == nil {
			// Not a core-roster agent (user-created, or a System Agent —
			// ByID iterates All(), which excludes SystemAgents()).
			continue
		}
		if coreAgentSkills(ca.ID) == nil {
			// A roster agent whose seed grants no skills (e.g. the worker):
			// the migration introduces no grant it never seeded.
			continue
		}
		if len(a.Skills) == 0 {
			// Nil stays nil; operator-emptied [] stays empty.
			continue
		}
		alreadyGranted := false
		for _, s := range a.Skills {
			if s == skillDefineDone || s == skillDefineGoal {
				alreadyGranted = true
				break
			}
		}
		if !alreadyGranted {
			a.Skills = append(a.Skills, skillDefineDone)
		}
	}
	cfg.SeededSkillGrants = append(cfg.SeededSkillGrants, SkillsMigrationDefineDone)
	return true
}

// SkillsMigrationDefineGoalRename is the ADR-080 D-SKILL marker recorded in
// config.seeded_skill_grants once the one-shot "define-done"→"define-goal"
// allowlist-REWRITE migration has run on an install. Exported so
// pkg/gateway can gate the matching skill-DIRECTORY cleanup (deleting the
// orphaned $OMNIPUS_HOME/skills/define-done/, ADR-080 §151 step 2) on the
// SAME marker after SeedConfig returns, mirroring how SkillsMigrationDefineDone
// gates persistSeededSkillGrants.
//
// Deliberately a NEW, distinct marker — never a rename of
// SkillsMigrationDefineDone itself, whose value ("adr074-define-done") stays
// exactly as ADR-074 recorded it (history, permanently). The literal chosen
// here ("adr080-define-goal-rename") also carries none of "migrat"/"legacy"/
// "alias"/"deprecat"/"retired"/"backcompat"/"back_compat" (case-insensitive),
// the token set scripts/check-greenfield-providers.sh's SC-009 scan forbids
// in pkg/providers and pkg/config — though as a pkg/coreagent constant this
// marker sits outside those two scanned roots regardless.
const SkillsMigrationDefineGoalRename = "adr080-define-goal-rename"

// applyDefineGoalRenameMigration is the ADR-080 D-SKILL one-shot,
// marker-keyed REWRITE migration (ADR-080 §151 step 1).
//
// Background: ADR-080 D-SKILL renames the built-in criteria-authoring skill
// "define-done" → "define-goal". coreAgentSkills/systemAgentSkills above now
// seed "define-goal" for every fresh grant, so an install that already holds
// the OLD token — either seeded by an earlier release, or just appended by
// applyDefineDoneSkillsMigration immediately above (the pre-ADR-074 →
// post-ADR-080 double-upgrade case) — is left holding "define-done" in its
// allowlist unless this migration rewrites it in place.
//
// Semantics, exactly as ratified (ADR-080 §151.1):
//   - Marker present → no-op in full (second boot is byte-identical).
//   - Marker absent → for EVERY agent in cfg.Agents.List — core-roster,
//     user-created, AND System Agents alike. Unlike SkillsMigrationDefineDone
//     this is NOT restricted to the core roster: it is a pure rename of an
//     ALREADY-granted permission, never a new grant, so ADR-072 D5.1's
//     "never restore a grant the operator removed" concern does not apply —
//     there is nothing to restore, only a token to relabel. When the live
//     Skills list is non-nil AND non-empty AND contains "define-done" AND
//     lacks "define-goal": REPLACE the token in its existing slot (rewrite,
//     not append), preserving the list's order.
//   - Nil stays nil (unrestricted already resolves every installed skill).
//   - Empty [] stays empty (an operator who zeroed the list opted out —
//     respected, per ADR-072 D5.1 — same discipline as
//     applyDefineDoneSkillsMigration).
//   - A list that already carries "define-goal" is left alone even if it
//     (unusually) also still carries "define-done" — there is nothing to
//     rewrite INTO, and a dedup rule is out of scope for what is meant to
//     stay a narrow, mechanical token substitution.
//
// Returns true when it modified cfg (it always does when the marker was
// absent, because writing the marker is itself a modification).
func applyDefineGoalRenameMigration(cfg *config.Config) bool {
	for _, marker := range cfg.SeededSkillGrants {
		if marker == SkillsMigrationDefineGoalRename {
			return false
		}
	}
	const (
		skillDefineDone = "define-done"
		skillDefineGoal = "define-goal"
	)
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		if len(a.Skills) == 0 {
			// Nil stays nil; operator-emptied [] stays empty.
			continue
		}
		hasDefineGoal := false
		defineDoneIdx := -1
		for idx, s := range a.Skills {
			if s == skillDefineGoal {
				hasDefineGoal = true
			}
			if s == skillDefineDone {
				defineDoneIdx = idx
			}
		}
		if hasDefineGoal || defineDoneIdx == -1 {
			// Already renamed, or never carried the old token — nothing to
			// rewrite.
			continue
		}
		a.Skills[defineDoneIdx] = skillDefineGoal
	}
	cfg.SeededSkillGrants = append(cfg.SeededSkillGrants, SkillsMigrationDefineGoalRename)
	return true
}

// NewCustomAgentToolsCfg returns the default AgentToolsCfg for a newly created
// custom/subagent/subagent_3p agent (FR-008, FR-022). Every new agent starts
// fully-enumerated and deny-by-default (via denyAllThenOverride) — there is
// no DefaultPolicy field and no allow-by-default fallback. Only a narrow,
// conservative read-only surface is allowed out of the box (plus the
// structural ToolSearch floor every agent gets — CLAUDE.md constraint 6, see
// the "ToolSearch" entry below); the operator opts in explicitly (via the
// tool picker or tools.builtin.policies) for anything else, including bash
// and every system-management tool (create_agent, set_config,
// add_mcp_server, …), which all stay denied.
//
// Callers should embed this into config.AgentConfig.Tools when constructing a
// new agent via the REST API or create_agent tool.
//
// bash:deny rationale (CRIT-001/FR-B12): pkg/tools/compositor.go's
// passesScopeGate does NOT hard-deny ScopeCore tools (which "bash" is) for
// custom agents — it defers to the merged policy. Denying it here explicitly
// (rather than relying on an absent map entry) is what keeps a fresh agent
// from getting shell access with zero configuration. This is the SINGLE
// shared seed location for both agent-creation paths (the REST
// POST /api/v1/agents handler in pkg/gateway/rest.go's createAgent, and the
// LLM-driven system.agent.create tool in
// pkg/sysagent/tools/agent.go's AgentCreateTool.Execute) — both call this
// constructor rather than seeding independently, so the two paths cannot
// drift out of sync again. Renamed from "exec" to "bash" by ADR-036 (the
// tool-consolidation work this seed anticipated — see the migration in
// pkg/config/shell_tool_policy_migration.go for existing persisted "exec"
// policy entries).
func NewCustomAgentToolsCfg() *config.AgentToolsCfg {
	allow := config.ToolPolicyAllow
	ask := config.ToolPolicyAsk
	policies := &config.AgentToolsCfg{
		Builtin: config.AgentBuiltinToolsCfg{
			Policies: denyAllThenOverride(map[string]config.ToolPolicy{
				// AskUserQuestion (spec US-7 S1): customs' default allowlist
				// carries it — every human-facing agent may ask the user
				// structured clarification questions; never `ask`-gate asking.
				"AskUserQuestion": allow,
				// set_goal (ADR-088 D2): customs' default allowlist carries
				// it too — every human-facing agent may author its own
				// session's goal record, seeded alongside AskUserQuestion.
				"set_goal": allow,
				// goal_claim (ADR-084 D12, JUDGE-FR-089): customs' default
				// allowlist carries it too, seeded alongside set_goal — see
				// that entry's comment above. R-12: this is the SEVENTH
				// per-agent policy map, and every new tool name in this
				// delivery gets an explicit, intended entry here too — an
				// absent key is not an unknown key, so validateOverrideKeys
				// does not panic, but a fresh custom agent would silently
				// resolve the tool to the denyAllThenOverride floor (deny)
				// with no test noticing.
				"goal_claim": allow,
				// browser_handover (ADR-085 BROWSER-FR-051, C-70): EXPLICIT
				// deny, not absence, for the same R-12 reason directly
				// above. Customs' default allowlist grants no browser_*
				// tool at all (see the conservative initial allow-list
				// below), so it holds no browser action set to stand down
				// from — an operator who wants a custom agent driving the
				// browser changes this entry on their own install.
				"browser_handover": config.ToolPolicyDeny,
				// Conservative initial allow-list: read-only filesystem +
				// persistent memory. Everything else — bash included — stays
				// denied until the operator opts in.
				"read_file":      allow,
				"list_directory": allow,
				// library_list/library_read (D3, library-spec) are part of
				// the same read-only filesystem surface as read_file/
				// list_directory above — a fresh agent can find and read
				// whatever the operator uploaded to this workspace's chat.
				"library_list":  allow,
				"library_read":  allow,
				"request_mount": ask,
				// environment_setup (ADR-090 ES-FR-01, founder ruling
				// 2026-09-18: setup permission is set at BOTH levels):
				// new custom native agents stamp an explicit per-agent
				// "ask", the same deliberate-posture data Mia, General
				// Purpose and Admin carry — NOT an omission riding the
				// ceiling — so a later ceiling raise loosens no one.
				// Precedent: request_mount above (ceiling ask, per-agent
				// ask; strictest-wins keeps ask+ask → ask). Both creation
				// routes (REST create, create_agent) and the
				// get_agent_tools preview read this ONE constructor, so
				// creation default and preview cannot drift. A saved
				// operator override (deny included) always wins over this
				// default.
				"environment_setup":   ask,
				"list_mounts":         allow,
				"remember":            allow,
				"recall_memory":       allow,
				"run_retrospective":   allow,
				"recall_conversation": allow,
				// Structural floor (CLAUDE.md constraint 6): every agent needs
				// ToolSearch to reach ANY tiered (lazy/search-only) tool at all —
				// seeded here as real data rather than the retired compositor.go
				// hardcoded force-allow.
				"ToolSearch": allow,
				// Structural floor (ADR-072 D1, mirroring the ToolSearch
				// structural floor immediately above): every agent needs the
				// Skill tool to load ANY skill's content at all — the "# Skills"
				// menu advertises skills but nothing else can ever load one.
				"Skill": allow,
				// ADR-081 D11 (FR-009), FOUNDER RULING (2026-09-07, updated
				// after this constructor originally shipped grep as a
				// hardcoded deny like every other unopted-in tool): "There
				// must not be any tool default to deny — the global policy
				// sets the default, not any hardcoded default." grep is
				// EXPLICIT "allow" here, not merely omitted: this map is
				// built via denyAllThenOverride, which fully enumerates
				// every static builtin name with a literal value (no sparse
				// variant exists for this constructor — see
				// TestAgentConstructor_CustomAgent_DenyByDefaultFullCoverage's ElementsMatch pin and
				// pkg/gateway/rest.go's createAgent, which validates a
				// CALLER-submitted map for completeness via
				// ValidateSubmittedToolPolicyMap under the same
				// fully-enumerated contract), so an explicit "allow" is the
				// only way to make a brand-new custom agent's grep resolve
				// to the global ceiling's "allow" instead of a hardcoded
				// per-agent deny that would otherwise BEAT the ceiling under
				// strictest-wins. This closes the one gap the original
				// ADR-081 D11 governance change deliberately left open (a
				// fresh custom agent still denied grep by default,
				// unlike every pre-existing agent's upgrade-path backfill,
				// which already resolves allow — see
				// tool_policy_catalog_drift.go's matching MV-8 exception).
				"grep": allow,
			}),
		},
	}
	return policies
}
