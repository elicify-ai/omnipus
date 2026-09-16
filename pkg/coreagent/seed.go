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
//     agent tier — Jim/Mia/Ava/Ray, the Worker, the specialist tier, and
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
	// /goal sessions); Worker resolves explicit DENY via tightenGlobalCeiling
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
	// the operator. Registered here, in the four browser-capable agents'
	// per-agent seeds (IDJim, IDRay, IDExplorer, IDResearcher) and in
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
	"create_agent", "update_agent", "delete_agent",

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
	// tier (Jim/Mia/Ava/Ray, the Worker, the specialist tier, every system
	// agent) — a read-only tool confined to the calling agent's own
	// workspace root and mounts only (FR-020), with no posture left to
	// silent inheritance anywhere in the roster.
	"grep",
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
// and tightenGlobalCeiling: every call site's override map is a hardcoded Go
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

// tightenGlobalCeiling returns a SPARSE policy map containing only the given
// overrides (validated against allStaticToolNames — same typo/rename safety
// net as denyAllThenOverride). Unlike denyAllThenOverride, every tool NOT
// listed here is deliberately left absent from the returned map, not filled
// with "deny" — the boot-time/write-time coverage validator
// (config.ValidateToolPolicyCoverage) is OR-based per (agent, tool): a tool
// missing from an agent's own policy map is still covered as long as the
// global ceiling (sandbox.tool_policies, pkg/config/defaults.go) has an entry
// for it, and the runtime filter resolves global x agent as
// most-restrictive-wins (pkg/agent/instance.go:agentToolsCfgToPolicy) — so an
// absent key here means "inherit the global default for this tool," not
// "denied." Use this for a seed that is meant to track the global ceiling
// except for specific, named tightenings.
func tightenGlobalCeiling(overrides map[string]config.ToolPolicy) map[string]config.ToolPolicy {
	validateOverrideKeys(overrides)
	out := make(map[string]config.ToolPolicy, len(overrides))
	for name, policy := range overrides {
		out[name] = policy
	}
	return out
}

// coreAgentSeed returns the constructor-seeded policy map for the named core
// agent (FR-010, FR-022). For every agent EXCEPT IDWorker, the map is
// fully-enumerated (one literal entry per name in allStaticToolNames, built
// via denyAllThenOverride) so every tool is explicitly "allow", "ask", or
// "deny" with no default-policy fallback. IDWorker is the one deliberate
// exception — see the tightenGlobalCeiling call below.
//
// All four base agents (Mia, Jim, Ava, Ray) are LEAST-PRIVILEGE: deny-by-default
// with an explicit allow-list for exactly the tools their role needs. The
// seeded specialists (IDPlanner, IDExplorer, IDResearcher) are likewise
// deny-by-default with their own narrow, role-appropriate allow-list — the
// legacy allow-by-default + dead "system.*" wildcard rail is retired entirely;
// "system.*" matched zero real tool names (a leftover from a since-renamed
// tool family), so it never actually rationed anything.
//
// # SEED RULE — PLAN CONTAINMENT PARITY (plan-supervisor-spec FR-006b)
//
// WHEREVER "execute_plan" IS SEEDED, "stop_plan" MUST BE SEEDED IN THE SAME
// MAP AT THE SAME LITERAL POLICY VALUE.
//
// This is deliberately stated as a rule over the seed rather than as a list
// of agents, so that it survives a new agent being added to this function:
// the property it exists to guarantee is "no agent can start a plan it cannot
// stop". Add a new agent that seeds execute_plan and you MUST add stop_plan
// beside it — TestPlanContainmentParity_ResolvedPolicy asserts the resolved
// outcome across the WHOLE seeded roster through the real compositor, so
// forgetting is a red test, not a silent containment hole.
//
// Two exceptions, each with its own stated reason:
//
//  1. An agent that is NOT a chat target (today exactly one holds a non-deny
//     execute_plan: the Worker, via the sparse tightenGlobalCeiling map below)
//     may hold "execute_plan": ask alongside "stop_plan": deny. It is exempt
//     because it is structurally incapable of being a Plan.OwnerAgentID
//     (IsChatTarget gates both write paths), so "starts a plan it cannot stop"
//     is unreachable for it — asserted by
//     TestPlanContainmentParity_NonOwnersCannotStartAPlan, which is what this
//     exemption actually rests on. Note this exception used to be phrased as
//     "a sparse map that deliberately OMITS execute_plan"; the Worker now
//     carries all three plan-execution tools EXPLICITLY (see the map below —
//     omission stopped meaning "ask" the moment the ceiling was raised to
//     "allow"), so the exemption is restated over the property that was always
//     doing the work rather than over the omission that used to imply it. Its
//     stop_plan/plan_correct/inspect_session entries stay EXPLICIT deny for the
//     same ceiling-inheritance reason.
//  2. PlanSupervisor (systemAgentSeed below) holds NEITHER tool: its override
//     map names plan_correct and nothing else, so denyAllThenOverride gives it
//     stop_plan: deny. That is consistent with this rule (it does not seed
//     execute_plan either) and required by FR-008/FR-043 — the adjudicator
//     corrects, the owner contains.
//
// Note the requirement is over the seed LITERAL while the property that
// matters is over the RESOLVED policy. Those two used to differ for Jim:
// execute_plan's global ceiling was "ask", so his own seeded "allow" merged
// down to "ask" under strictest-wins while stop_plan's "allow" ceiling let
// that one resolve "allow". That gap was not a design — it was the ADR-052
// ceiling defect (see pkg/config/defaults.go), and since 2026-07-28 both
// ceilings are "allow" and Jim resolves "allow" for both. The parity rule is
// unchanged and is now satisfied at the resolved level as well as the literal
// one for every chat target.
//
// # SEED RULE — ROSTER VISIBILITY (ADR-056, list_jobs)
//
// "list_jobs" IS SEEDED "allow" FOR THE FOUR BASE AGENTS AND FOR NOBODY ELSE.
//
// list_jobs is strictly read-only, but its scope is AGENT IDENTITY, not
// session: it returns every plan, delegated session and standalone task
// recorded against the CALLING agent id. For the four base agents
// (Mia/Jim/Ava/Ray) that is a well-posed question — they are durable,
// user-addressable identities, and they are the only agents that can be a
// plan's OwnerAgentID (IsChatTarget, the same predicate FR-006b's parity sweep
// uses). For them the grant is also the DISCOVERY half of containment:
// stop_plan takes a plan id, and this is where an agent that did not itself
// just mint one gets it. Jim in particular resolves stop_plan "allow"
// specifically so a runaway plan can be contained with no human in the loop —
// leaving him unable to FIND the plan id hands that dependency straight back.
//
// The delegation-only tier (Worker/Planner/Explorer/Researcher) is denied. Each
// of those ids is occupied by many concurrent, unrelated delegated sessions at
// once, so for them "your own work" resolves to "every concurrent run of this
// role in the installation" rather than "this run's work" — a roster that is
// simultaneously useless and cross-talking. Nothing is lost: a leaf that needs
// the status of the one child it just minted already has delegate's own status
// sub-case, scoped to a handle it holds. The Judge and PlanSupervisor are
// denied for their own stated reasons (verifier-inapplicable; D-04
// roster-blindness — see systemAgentSeed).
//
// As with stop_plan, the Worker's SPARSE map needs an EXPLICIT deny rather than
// an omission: list_jobs' global ceiling is "allow", so an absent key there
// would silently GRANT it.
//
// # SEED RULE — KNOWLEDGE POSTURE (ADR-067 D17, FR-070/FR-071)
//
// EVERY knowledge_* NAME IN allStaticToolNames CARRIES AN EXPLICIT, LITERAL
// POSTURE FOR EACH OF THE FOUR BASE AGENTS: RETRIEVAL "allow" FOR ALL FOUR;
// AUTHORING "allow" FOR JIM AND AVA, "ask" FOR MIA AND RAY.
//
// Stated over the tool family rather than as a list of the nine names so it
// survives a tenth knowledge tool being added: add one to the catalog and it
// arrives here at denyAllThenOverride's "deny" for every agent — a posture
// nobody chose. That is not a loud failure. The deny is explicit, so coverage
// validation is satisfied and boot is clean; the tool is simply dead, with no
// signal anywhere. TestCoreAgentSeed_KnowledgeToolsCarrySeededPosture asserts
// the property over the whole catalog, so forgetting is a red test.
//
// Everyone else is deny: the specialist tier and every system agent reach it
// through denyAllThenOverride's fully-enumerated default, and the Worker's
// SPARSE map writes nine explicit denies out for the reason stated there (all
// nine ceilings are "allow", so an omission would GRANT).
//
// The returned map is an independent allocation — callers may mutate it safely.
func coreAgentSeed(id CoreAgentID) map[string]config.ToolPolicy {
	if id == IDWorker {
		return workerSeedPolicies()
	}
	if IsSubagentTierID(id) {
		return subagentTierSeedPolicies(id)
	}
	switch id {
	case IDAva:
		return avaSeedPolicies()
	case IDMia:
		return miaSeedPolicies()
	case IDRay:
		return raySeedPolicies()
	case IDJim:
		return jimSeedPolicies()
	}
	// Defensive fallback for an ID outside the known roster (All() only ever
	// passes Mia/Jim/Ava/Ray/Worker/Planner/Explorer/Researcher, so this branch
	// should be unreachable) — deny every known tool, no implicit allow.
	return denyAllThenOverride(nil)
}

// workerSeedPolicies is the Worker's seeded tool policy: sparse, tightening only listed tools below the global ceiling (see the comment inside).
func workerSeedPolicies() map[string]config.ToolPolicy {
	allow := config.ToolPolicyAllow
	deny := config.ToolPolicyDeny
	ask := config.ToolPolicyAsk
	// Worker tracks the seeded global tool-policy ceiling
	// (sandbox.tool_policies, pkg/config/defaults.go) for every tool NOT
	// listed here — a deliberate design choice (operator-confirmed), not
	// an oversight: everything absent from this map inherits the global
	// default via coverage-validator OR-semantics
	// (config.ValidateToolPolicyCoverage). Only the categories below are
	// tightened past the global ceiling to "deny": channels, providers,
	// platform, most of agents (list_agents stays open), most of tasks
	// (list_tasks/update_task/set_todos stay open), and workspaces.
	//
	// This sparseness is preserved across upgrades:
	// backfillToolPolicyCatalogDrift (tool_policy_catalog_drift.go) fills
	// a pre-existing agent's missing entries from THIS map's keys, never
	// from the full catalog, so a name deliberately left out here keeps
	// inheriting the ceiling. The corollary is the one to remember when
	// editing this map: a tool that should sit BELOW the ceiling must be
	// written out here explicitly, on the upgrade path exactly as on the
	// fresh-install path.
	//
	// EXCEPTION (ADR-081 D11, FR-009 — founder ruling): "grep" is the
	// first entry this map carries that is NOT a below-ceiling
	// tightening — its value ("allow") is IDENTICAL to the global
	// ceiling, so by every rule stated above it could simply be left
	// absent and inherit. It is written out anyway, deliberately
	// breaking the "every entry here tightens below the ceiling"
	// pattern: the grep founder ruling
	// (unified-search-and-grep-spec.md MV-8) requires an EXPLICIT
	// "allow" for every agent tier with no posture left to silent
	// inheritance — including the Worker, whose otherwise-sparse map
	// would normally leave a ceiling-matching "allow" implicit. See the
	// matching note on tool_policy_catalog_drift.go's "What it
	// deliberately does NOT do" section.
	return tightenGlobalCeiling(map[string]config.ToolPolicy{
		// --- Channels ---
		"enable_channel":    deny,
		"configure_channel": deny,
		"disable_channel":   deny,
		"list_channels":     deny,
		"test_channel":      deny,
		// --- Providers ---
		"configure_provider": deny,
		"list_providers":     deny,
		"test_provider":      deny,
		"list_models":        deny,
		// --- Platform ---
		"get_config": deny,
		"set_config": deny,
		"run_doctor": deny,
		"get_usage":  deny,
		// --- Agents (list_agents stays at the global default) ---
		"create_agent":        deny,
		"update_agent":        deny,
		"delete_agent":        deny,
		"read_agent_metadata": deny,
		// --- Tasks (update_task/set_todos/list_tasks stay at the global default) ---
		"create_task":              deny,
		"delete_task":              deny,
		"create_task_in_workspace": deny,
		"update_task_in_workspace": deny,
		"delete_task_in_workspace": deny,
		"list_tasks_in_workspace":  deny,
		// --- Workspaces ---
		"create_workspace": deny,
		"update_workspace": deny,
		"delete_workspace": deny,
		"list_workspaces":  deny,
		"get_workspace":    deny,
		// --- ADR-052 planning/verifier tools ---
		// create_plan/execute_plan/run_task are EXPLICIT "ask" here.
		//
		// They were deliberately ABSENT until 2026-07-28, inheriting
		// "ask" from the global ceiling (DS-6) — correct only for as
		// long as that ceiling stayed "ask". When the ceiling was
		// raised to "allow" (so Jim's own seeded "allow" could finally
		// resolve at all; see pkg/config/defaults.go's ADR-052 note),
		// absence here would have silently GRANTED all three to the
		// Worker: the exact "ceiling is allow, so absence GRANTS" trap
		// that inspect_session, stop_plan/plan_correct and list_jobs
		// below each already document, hit a fourth time. Caught by
		// tool_policy_effective_resolution_test.go — which is why that
		// test asserts the whole seeded roster, not just Jim.
		//
		// "ask", not "deny": this restores exactly the posture the
		// Worker had before the ceiling moved. Tightening it further
		// would be an unrelated policy change smuggled in on a bug fix.
		"create_plan":  ask,
		"execute_plan": ask,
		"run_task":     ask,
		// inspect_session, by contrast, is an EXPLICIT "deny" here
		// (fix-wave finding #2) — NOT absent: the global ceiling now
		// seeds inspect_session "allow" (raising the ceiling so the
		// Judge's own "allow" resolves cleanly under strictest-wins,
		// see defaults.go), so an absent entry here would silently
		// inherit that "allow" instead of the deny every non-Judge
		// agent must carry.
		"inspect_session": deny,
		// --- ADR-055 containment (FR-006b exception 1) ---
		// stop_plan is an EXPLICIT "deny" here for exactly the
		// inspect_session reason directly above, not for a new one:
		// its global ceiling is "allow" (pkg/config/defaults.go), so
		// leaving it ABSENT from this sparse map would silently GRANT
		// it to the Worker. A Worker can never be a plan's
		// owner_agent_id, so the grant would be unusable rather than
		// dangerous — but "unusable grant" is not a posture this
		// codebase ships (Constraint #6). plan_correct needs no entry
		// of its own: it is not in this map either, and its ceiling
		// grant is likewise held shut by the engine's exact-identity
		// gate — but the same "explicit beats inherited" reasoning
		// applies, so it is named too rather than left to inference.
		"stop_plan":    deny,
		"plan_correct": deny,
		// set_goal (ADR-088 D2) is an EXPLICIT "deny" here for exactly
		// the inspect_session/stop_plan/plan_correct reason directly
		// above: its global ceiling is "allow" (pkg/config/defaults.go),
		// so leaving it ABSENT from this sparse map would silently GRANT
		// it to the Worker. A generic delegated worker session should
		// never author its own goal record — the tool's own scope
		// preconditions already refuse it at delegation depth > 0, but
		// "unusable grant" is not a posture this codebase ships
		// (Constraint #6), so it is named too rather than left to
		// inference.
		"set_goal": deny,
		// goal_claim (ADR-084 D12) is an EXPLICIT "allow" here, and
		// unlike set_goal directly above it is not denied. A task can be
		// assigned to the Worker, and a native task run completes ONLY
		// when its goal_claim is upheld by the Judge (ADR-084 §11,
		// ADR-043 §8, issue #710): update_task refuses a status write on
		// the caller's own running task, so goal_claim is the only way a
		// Worker task run can finish. The tool's own preconditions allow
		// a task's own run to claim at any delegation depth and still
		// refuse a delegated sub-turn that is not a task run
		// (pkg/tools/goal_claim.go::Execute), so an ordinary delegated
		// Worker session still cannot claim its parent's goal.
		//
		// This entry was "deny" until 2026-09-15, on the premise that
		// the preconditions refused every Worker call anyway. Once task
		// runs could claim, that deny made every task assigned to the
		// Worker loop through its tries without ever finishing (live
		// smoke test on build f4e482561).
		//
		// Founder decision 2026-09-15: goal_claim is allowed by default
		// for every agent. Named explicitly rather than left absent
		// (which would also resolve allow from today's ceiling) so the
		// Worker's default is readable in its own stored map, and so a
		// fresh install stores exactly what the one-time update writes
		// on an install seeded with the old deny
		// (applyWorkerGoalClaimAllowUpdate). An operator can still set
		// deny afterwards; that value is kept.
		"goal_claim": allow,
		// --- ADR-056 roster visibility ---
		// Same "ceiling is allow, so absence GRANTS" trap once more, and
		// here the grant would not merely be unusable: the Worker id is
		// occupied by every generic delegated session in the installation
		// at once, so a Worker roster would enumerate sibling branches of
		// unrelated parent turns rather than its own work. See
		// coreAgentSeed's ROSTER VISIBILITY rule.
		"list_jobs": deny,
		// --- ADR-068 D15.3 knowledge-base tools ---
		// EXPLICIT deny, all six — the "ceiling is allow, so absence
		// GRANTS" trap once more (see inspect_session / stop_plan /
		// list_jobs above). D15.3 seeds a posture for the FOUR BASE
		// AGENTS and nobody else, and every other seeded agent reaches
		// deny via denyAllThenOverride's fully-enumerated default. This
		// sparse map is the one seed that would not, so the deny is
		// written out.
		//
		// Read-only knowledge_describe/knowledge_find/knowledge_read are
		// denied here for the same reason list_jobs is: the Worker id is
		// occupied by every generic delegated session in the
		// installation at once, so a grant to "the Worker" is a grant to
		// all of them. An operator who wants a delegated worker reading
		// a knowledge base changes this on their own install
		// (Constraint #6 — this is seeded data, not a branch).
		"knowledge_describe": deny,
		"knowledge_find":     deny,
		"knowledge_read":     deny,
		// knowledge_list (KB-2a) — the same Worker-id-is-shared reason as
		// the three read tools directly above.
		"knowledge_list":        deny,
		"knowledge_edit":        deny,
		"knowledge_restructure": deny,
		"knowledge_configure":   deny,
		// knowledge_base_create (KB-1) — the Worker cannot own a plan or
		// be addressed individually (see list_jobs/roster-visibility
		// reasoning above); a knowledge base "created by the Worker"
		// would be indistinguishable from one created by any other
		// delegated session sharing that id, so this stays denied for
		// the same reason every other Worker write above does.
		"knowledge_base_create": deny,
		// --- ADR-081 D11 (FR-009, founder ruling) ---
		// grep: EXPLICIT allow — the one entry in this map that MATCHES
		// the ceiling rather than tightening below it. See the EXCEPTION
		// note on this map's intro comment above: the founder ruling for
		// grep leaves no agent's posture, including the Worker's, to
		// silent ceiling inheritance.
		"grep": allow,
	})
}

// The delegation-only specialist tier (Planner/Explorer/Researcher) is a
// leaf/near-leaf surface: narrower and more predictable than the base
// agents. Deny-by-default; only the tools each role plausibly needs are
// allowed. None of these ever get bash or any system-management tool
// (create_agent, set_config, add_mcp_server, …) — those stay denied.
// subagentTierSeedPolicies returns that policy for one of the three.
func subagentTierSeedPolicies(id CoreAgentID) map[string]config.ToolPolicy {
	allow := config.ToolPolicyAllow
	ask := config.ToolPolicyAsk
	overrides := map[string]config.ToolPolicy{
		// Every leaf reports its result back.
		"send_message": allow,
		// AskUserQuestion (spec US-7 S1): allow for the whole human-facing
		// subagent tier. The tool's own owner-session gate rejects any
		// call from a DELEGATED run of these agents toward
		// message_parent(question:true); the seed keeps the tool usable
		// whenever one of them runs as a session owner.
		"AskUserQuestion": allow,
		// set_goal (ADR-088 D2): same reasoning as AskUserQuestion
		// immediately above — its own scope precondition refuses a
		// DELEGATED run (ToolDelegationDepth > 0), so the seed only ever
		// matters when one of these agents runs as a session owner.
		"set_goal": allow,
		// goal_claim (ADR-084 D12): allow. It matters whenever one of
		// these agents runs as a session owner, and whenever a task is
		// assigned to it: a native task run finishes only through an
		// upheld goal_claim (ADR-084 §11, issue #710), and the tool's own
		// precondition lets a task's own run claim at any delegation
		// depth while still refusing any other delegated sub-turn.
		"goal_claim": allow,
		// ADR-052 FR-005: every seeded agent OTHER than Jim is explicit
		// "ask" (never absent, never deny) for the three plan-execution
		// tools — an operator-approval prompt gates any attempted use.
		"create_plan":  ask,
		"execute_plan": ask,
		"run_task":     ask,
		// FR-006b seed rule: stop_plan rides with execute_plan, same map,
		// same literal value. See coreAgentSeed's doc comment.
		"stop_plan": ask,
		// ADR-081 D11 (FR-009, founder ruling): grep is allowed for the
		// WHOLE specialist tier — Planner, Explorer and Researcher alike
		// — unlike knowledge_describe/knowledge_find/knowledge_read
		// above, which are denied for this tier specifically because the
		// Worker/specialist ids are shared across every concurrent
		// delegated run of that role. grep carries no such identity
		// ambiguity: it is scoped per-call to the CALLING agent's own
		// workspace root and mounts (FR-020), so a grant to "the
		// Planner" never crosses into a sibling delegated session's
		// files the way a knowledge-base grant would. The founder ruling
		// grants it unprompted to every agent tier with no posture left
		// to silent inheritance.
		"grep": allow,
	}
	switch id {
	case IDPlanner:
		// Decomposes a goal into a task DAG, delegating to Explorer/
		// Researcher for context (bounded depth in the trust graph).
		// Read-only file access, full task-management surface, delegate,
		// and persistent memory to record decompositions. No browser —
		// the Planner only decomposes, it doesn't browse.
		overrides["read_file"] = allow
		overrides["list_directory"] = allow
		// Chat-uploaded files land in this workspace's library (D3,
		// library-spec) — matches the read_file/list_directory allowance above.
		overrides["library_list"] = allow
		overrides["library_read"] = allow
		overrides["request_mount"] = ask
		overrides["list_mounts"] = allow
		overrides["create_task"] = allow
		overrides["update_task"] = allow
		overrides["list_tasks"] = allow
		overrides["delegate"] = allow
		overrides["message_parent"] = allow
		overrides["remember"] = allow
		overrides["recall_memory"] = allow
		overrides["run_retrospective"] = allow
		overrides["recall_conversation"] = allow
		// Structural floor (CLAUDE.md constraint 6): every agent needs
		// ToolSearch to reach ANY tiered (lazy/search-only) tool at all —
		// seeded here as real data rather than the retired compositor.go
		// hardcoded force-allow.
		overrides["ToolSearch"] = allow
		// Structural floor (ADR-072 D1, mirroring the ToolSearch
		// structural floor immediately above): every agent needs the
		// Skill tool to load ANY skill's content at all — the "# Skills"
		// menu advertises skills but nothing else can ever load one.
		overrides["Skill"] = allow
	case IDExplorer:
		// File + memory exploration (internal context): read-only
		// filesystem, persistent memory, plus interactive/visual
		// browsing for pages that need rendering (NOT browser_evaluate).
		overrides["read_file"] = allow
		overrides["list_directory"] = allow
		// Chat-uploaded files land in this workspace's library (D3,
		// library-spec) — matches the read_file/list_directory allowance above.
		overrides["library_list"] = allow
		overrides["library_read"] = allow
		overrides["request_mount"] = ask
		overrides["list_mounts"] = allow
		overrides["remember"] = allow
		overrides["recall_memory"] = allow
		overrides["run_retrospective"] = allow
		overrides["recall_conversation"] = allow
		for _, b := range []string{
			"browser_navigate", "browser_click", "browser_type",
			"browser_screenshot", "browser_get_text", "browser_wait",
			// ADR-041 D3 — tab-management, same allow as the rest of the
			// interactive/visual browsing surface above.
			"browser_list_tabs", "browser_switch_tab", "browser_close_tab", "browser_open_tab",
			// ADR-075 D2 (FR-024) — parity with Jim and Ray on the new
			// interaction verbs and the accessibility snapshot. None of
			// the five is arbitrary-code-adjacent, which is the property
			// the existing ten-allow/one-deny carve-out actually turns on:
			// browser_evaluate stays denied here for the same reason it
			// always was.
			"browser_select_option", "browser_press_key", "browser_hover", "browser_snapshot",
			// ADR-075 D2 FR-035/A-12 — allow for every browser-capable
			// agent. A dialog wedges the tab for whoever hits it, so the
			// verb that clears it has to be held by everyone who can open
			// one. The dangerous half is guarded at the ARGUMENT, not
			// here: `accept` defaults to false, and accepting is refused
			// on a run with nobody to approve it. A tool policy cannot see
			// an argument, so it cannot make that distinction.
			"browser_handle_dialog",
			// browser_handover (ADR-085 BROWSER-FR-051, C-70): allow —
			// this agent holds the full browser action set above, so it
			// holds the verb that stands down from it.
			"browser_handover",
		} {
			overrides[b] = allow
		}
		// FR-021: browser_upload_file is ASK for every agent that HOLDS
		// the browser surface, delegation-tier workers included. A
		// per-agent deny here was proposed and overruled by the operator;
		// what answers the "nobody to approve an unattended ask" concern
		// is FR-029 — the tool is not registered at all until #659 lands —
		// not a tighter seed on this agent.
		overrides["browser_upload_file"] = ask
		// FR-030: the file:// refusal now points the agent at serve_web,
		// and a pointer to a tool this agent resolves DENY for is #242's
		// dead end relocated one failed tool call further away. This agent
		// already holds write access within its confinement, so the
		// marginal capability is serving an already-writable file over the
		// existing token-authenticated preview route.
		overrides["serve_web"] = allow
		// Structural floor (CLAUDE.md constraint 6): every agent needs
		// ToolSearch to reach ANY tiered (lazy/search-only) tool at all —
		// seeded here as real data rather than the retired compositor.go
		// hardcoded force-allow.
		overrides["ToolSearch"] = allow
		// Structural floor (ADR-072 D1, mirroring the ToolSearch
		// structural floor immediately above): every agent needs the
		// Skill tool to load ANY skill's content at all — the "# Skills"
		// menu advertises skills but nothing else can ever load one.
		overrides["Skill"] = allow
	case IDResearcher:
		// External-source research: web search/fetch, read-only file
		// access (for fetched/local docs), persistent memory, plus
		// interactive/visual browsing for sources that need it.
		overrides["search_web"] = allow
		overrides["fetch_url"] = allow
		overrides["read_file"] = allow
		// Chat-uploaded files land in this workspace's library (D3,
		// library-spec) — Researcher gets read access only (matches his
		// read_file-only allowance; he has no list_directory either).
		overrides["library_read"] = allow
		overrides["request_mount"] = ask
		overrides["list_mounts"] = allow
		overrides["remember"] = allow
		overrides["recall_memory"] = allow
		overrides["run_retrospective"] = allow
		overrides["recall_conversation"] = allow
		for _, b := range []string{
			"browser_navigate", "browser_click", "browser_type",
			"browser_screenshot", "browser_get_text", "browser_wait",
			// ADR-041 D3 — tab-management, same allow as the rest of the
			// interactive/visual browsing surface above.
			"browser_list_tabs", "browser_switch_tab", "browser_close_tab", "browser_open_tab",
			// ADR-075 D2 (FR-024) — parity with Jim and Ray on the new
			// interaction verbs and the accessibility snapshot. None of
			// the five is arbitrary-code-adjacent, which is the property
			// the existing ten-allow/one-deny carve-out actually turns on:
			// browser_evaluate stays denied here for the same reason it
			// always was.
			"browser_select_option", "browser_press_key", "browser_hover", "browser_snapshot",
			// ADR-075 D2 FR-035/A-12 — allow for every browser-capable
			// agent. A dialog wedges the tab for whoever hits it, so the
			// verb that clears it has to be held by everyone who can open
			// one. The dangerous half is guarded at the ARGUMENT, not
			// here: `accept` defaults to false, and accepting is refused
			// on a run with nobody to approve it. A tool policy cannot see
			// an argument, so it cannot make that distinction.
			"browser_handle_dialog",
			// browser_handover (ADR-085 BROWSER-FR-051, C-70): allow —
			// this agent holds the full browser action set above, so it
			// holds the verb that stands down from it.
			"browser_handover",
		} {
			overrides[b] = allow
		}
		// FR-021: browser_upload_file is ASK for every agent that HOLDS
		// the browser surface, delegation-tier workers included. A
		// per-agent deny here was proposed and overruled by the operator;
		// what answers the "nobody to approve an unattended ask" concern
		// is FR-029 — the tool is not registered at all until #659 lands —
		// not a tighter seed on this agent.
		overrides["browser_upload_file"] = ask
		// FR-030: the file:// refusal now points the agent at serve_web,
		// and a pointer to a tool this agent resolves DENY for is #242's
		// dead end relocated one failed tool call further away. This agent
		// already holds write access within its confinement, so the
		// marginal capability is serving an already-writable file over the
		// existing token-authenticated preview route.
		overrides["serve_web"] = allow
		// Structural floor (CLAUDE.md constraint 6): every agent needs
		// ToolSearch to reach ANY tiered (lazy/search-only) tool at all —
		// seeded here as real data rather than the retired compositor.go
		// hardcoded force-allow.
		overrides["ToolSearch"] = allow
		// Structural floor (ADR-072 D1, mirroring the ToolSearch
		// structural floor immediately above): every agent needs the
		// Skill tool to load ANY skill's content at all — the "# Skills"
		// menu advertises skills but nothing else can ever load one.
		overrides["Skill"] = allow
	}
	return denyAllThenOverride(overrides)
}

// avaSeedPolicies is Ava's seeded tool policy.
func avaSeedPolicies() map[string]config.ToolPolicy {
	allow := config.ToolPolicyAllow
	// Ava — the Builder. LEAST-PRIVILEGE: deny-by-default, allow only the
	// tools her role needs (build/maintain agents, author skills, assign a
	// team to a workspace). This replaces the old allow-by-default + "system.*"
	// deny rail, which the §7 tool rename silently broke — the renamed
	// management tools (create_workspace, set_config, …) no longer match the
	// "system.*" glob, so every former-system tool fell through to allow.
	ask := config.ToolPolicyAsk
	return denyAllThenOverride(map[string]config.ToolPolicy{
		// AskUserQuestion (spec US-7 S1): every human-facing agent may ask
		// the user structured clarification questions.
		"AskUserQuestion": allow,
		// set_goal (ADR-088 D2): every human-facing agent may author its
		// own session's goal record — seeded alongside AskUserQuestion.
		"set_goal": allow,
		// goal_claim (ADR-084 D12): every human-facing agent may claim
		// its own session's goal complete — seeded alongside set_goal.
		"goal_claim": allow,
		// Agent lifecycle — her core job. Delete is consent-gated (ask).
		"create_agent": allow,
		"update_agent": allow,
		"delete_agent": ask,
		"list_agents":  allow,
		// Model selection + slug research (research the exact slug; never guess).
		"list_models": allow,
		"search_web":  allow,
		"fetch_url":   allow,
		// Persistent memory (FR-016/FR-017) — remember the user's design prefs.
		"remember":            allow,
		"recall_memory":       allow,
		"run_retrospective":   allow,
		"recall_conversation": allow,
		// Communication / handoff (hand back to Mia/Jim when out of scope).
		"send_message": allow,
		"switch_agent": allow,
		// Skill discovery + authoring (FR-9.2). Authoring/install are
		// consent-gated (ask) so every skill-tree write routes through approval.
		"find_skills":   allow,
		"list_skills":   allow,
		"create_skill":  ask,
		"edit_skill":    ask,
		"install_skill": ask,
		// Assign a freshly-built team to a workspace via core_team. NOT
		// create/delete_workspace — workspace lifecycle is Jim/admin. The read
		// pair lets her find the workspace and see its current team first.
		"update_workspace": allow,
		"list_workspaces":  allow,
		"get_workspace":    allow,
		// ADR-052 FR-005: every seeded agent OTHER than Jim is explicit
		// "ask" (never absent, never deny) for the three plan-execution
		// tools — an operator-approval prompt gates any attempted use.
		"create_plan":  ask,
		"execute_plan": ask,
		"run_task":     ask,
		// FR-006b seed rule: stop_plan rides with execute_plan, same map,
		// same literal value. See coreAgentSeed's doc comment.
		"stop_plan": ask,
		// ADR-056 roster visibility: Ava is a chat target and can therefore
		// be a plan's owner (after an operator approves her "ask"), so she
		// must be able to find the plan she owns in order to stop it. See
		// coreAgentSeed's ROSTER VISIBILITY rule.
		"list_jobs": allow,
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
		// ADR-068 D15.3 — knowledge base, split by BLAST RADIUS rather
		// than by read/write (superseding ADR-067 D17's file). Retrieval
		// (knowledge_describe/knowledge_find/knowledge_read) is allow
		// for all four base agents: read-only, and scoped by the tool
		// itself to this agent's workspace mounts (ADR-068's own
		// isolation carries forward D7's). Ava was previously full-allow
		// on every ADR-067 write too, because under that model EVERY
		// write touched exactly one file she named — "she is a BUILDER,
		// so she holds the write half unprompted, the same way she
		// holds create_agent/update_agent" no longer holds unmodified,
		// because ADR-068's split changes what the writes DO:
		// knowledge_edit is still that one-file case (kept unprompted-
		// adjacent by staying "ask" rather than "allow" only because
		// this is the family's FIRST release under the new engine —
		// see coreAgentSeed's doc note on this being a conservative
		// default, not a permanent verdict on her role); but
		// knowledge_restructure CASCADES to files she never named
		// (every inbound-linking note gets rewritten on a rename/move/
		// trash) and knowledge_configure changes what EXISTING records
		// MEAN (a schema edit reclassifies every record of that type
		// already on disk — see its own execEditRecordType/
		// execCreateRecordType cascade report). Neither blast radius is
		// bounded by what Ava's own arguments named, which is precisely
		// the property that made every other ADR-067 write safe to grant
		// her unprompted. So all three writes are "ask" here, not
		// "allow" — a role-based exception for a specific agent is a
		// decision for a human operator to make on their own install
		// (Constraint #6), not a default this seed grants on Ava's
		// behalf for an operation whose full effect she cannot bound
		// from her own call.
		"knowledge_describe": allow,
		"knowledge_find":     allow,
		"knowledge_read":     allow,
		// knowledge_list (KB-2a, defect-list-knowledge-base-ux-2026-09-08.md,
		// founder-ratified 2026-09-08) — allow, same read-tier posture as the
		// three read tools above: it reports what already exists, touching
		// nothing.
		"knowledge_list":        allow,
		"knowledge_edit":        ask,
		"knowledge_restructure": ask,
		"knowledge_configure":   ask,
		// knowledge_base_create (KB-1, same defect list) — "ask", not "allow":
		// it creates a new folder+marker in the operator's own Library, an
		// effect this agent's own call cannot bound, matching the write-three's
		// own "ask" reasoning immediately above.
		"knowledge_base_create": ask,
		// ADR-081 D11 (FR-009, founder ruling): grep is unprompted allow
		// for every agent tier, including Ava — unlike the knowledge
		// writes just above, it mutates nothing (a read-only recursive
		// name/content search confined to her own workspace root and
		// mounts, FR-020), so none of the cascade/control-plane
		// reasoning that keeps those three at "ask" applies here.
		"grep": allow,
	})
}

// miaSeedPolicies is Mia's seeded tool policy.
func miaSeedPolicies() map[string]config.ToolPolicy {
	allow := config.ToolPolicyAllow
	// Mia — the Assistant (default agent). LEAST-PRIVILEGE: deny-by-default,
	// allow only the everyday-assistant surface (chat, memory, your tasks,
	// email, light lookups). She ROUTES heavy work
	// (build/shell/browser/research/admin) to Ava/Jim/Ray rather than doing
	// it — matching her persona, which already refuses shell/browser.
	ask := config.ToolPolicyAsk
	return denyAllThenOverride(map[string]config.ToolPolicy{
		// AskUserQuestion (spec US-7 S1): every human-facing agent may ask
		// the user structured clarification questions.
		"AskUserQuestion": allow,
		// set_goal (ADR-088 D2): every human-facing agent may author its
		// own session's goal record — seeded alongside AskUserQuestion.
		"set_goal": allow,
		// goal_claim (ADR-084 D12): every human-facing agent may claim
		// its own session's goal complete — seeded alongside set_goal.
		"goal_claim": allow,
		// Converse / route.
		"send_message": allow,
		"switch_agent": allow,
		"list_agents":  allow, // knows who to route to
		"send_file":    allow, // share an artifact in chat
		// Memory — her signature (memory-rich, cross-workspace recall).
		"remember":            allow,
		"recall_memory":       allow,
		"run_retrospective":   allow,
		"recall_conversation": allow,
		// Your tasks ("runs your tasks"). Delete is consent-gated (ask).
		"create_task": allow,
		"update_task": allow,
		"list_tasks":  allow,
		"delete_task": ask,
		"set_todos":   allow,
		// Email — her domain.
		"read_inbox":   allow,
		"read_message": allow,
		"reply":        allow,
		"send_email":   allow,
		"search_email": allow,
		// Light lookups + skill discovery (she uses summarize/daily-briefing).
		"search_web":  allow,
		"fetch_url":   allow,
		"find_skills": allow,
		// ADR-052 FR-005: every seeded agent OTHER than Jim is explicit
		// "ask" (never absent, never deny) for the three plan-execution
		// tools — an operator-approval prompt gates any attempted use.
		"create_plan":  ask,
		"execute_plan": ask,
		"run_task":     ask,
		// FR-006b seed rule: stop_plan rides with execute_plan, same map,
		// same literal value. See coreAgentSeed's doc comment.
		"stop_plan": ask,
		// ADR-056 roster visibility: "what of mine is still running?" is an
		// everyday-assistant question, and Mia already owns the task surface
		// it reports on. She is a chat target, so she can also own a plan
		// once an operator approves the "ask" above — and would then need
		// this to find it. See coreAgentSeed's ROSTER VISIBILITY rule.
		"list_jobs": allow,
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
		// ADR-068 D15.3 — knowledge base, split by BLAST RADIUS
		// (superseding ADR-067 D17's file). Retrieval
		// (knowledge_describe/knowledge_find/knowledge_read) is allow —
		// read-only, workspace-scoped by the tool itself, and this
		// supersedes the ADR-067-era distinction between knowledge_tasks
		// and knowledge_search: knowledge_find is now the ONE retrieval
		// surface (it answers task queries too, via `kind: task` —
		// FR-076a), so there is no longer a second, narrower read name
		// for a stricter posture to attach to.
		//
		// knowledge_edit, knowledge_restructure and knowledge_configure
		// are all "ask". Mia ROUTES heavy work rather than doing it, and
		// every one of these lands on the operator's REAL disk outside
		// the Library's audit path — so the everyday assistant asks
		// before writing there, exactly as she asks before delete_task.
		// This is the SAME "ask" ADR-067's knowledge_create/
		// knowledge_move carried for her; ADR-068 does not loosen it —
		// knowledge_restructure and knowledge_configure are, if
		// anything, a WIDER blast radius than the single-file writes
		// that already warranted asking (see coreAgentSeed's IDAva case
		// for the cascade/control-plane argument, which applies
		// identically here).
		"knowledge_describe": allow,
		"knowledge_find":     allow,
		"knowledge_read":     allow,
		// knowledge_list (KB-2a, defect-list-knowledge-base-ux-2026-09-08.md,
		// founder-ratified 2026-09-08) — allow, same read-tier posture as the
		// three read tools above: it reports what already exists, touching
		// nothing.
		"knowledge_list":        allow,
		"knowledge_edit":        ask,
		"knowledge_restructure": ask,
		"knowledge_configure":   ask,
		// knowledge_base_create (KB-1, same defect list) — "ask", not "allow":
		// it creates a new folder+marker in the operator's own Library, an
		// effect this agent's own call cannot bound, matching the write-three's
		// own "ask" reasoning immediately above.
		"knowledge_base_create": ask,
		// ADR-081 D11 (FR-009, founder ruling): grep is unprompted allow
		// for every agent tier, including Mia — unlike the knowledge
		// writes just above, it mutates nothing (a read-only recursive
		// name/content search confined to her own workspace root and
		// mounts, FR-020), so the "she asks before writing there"
		// reasoning above does not apply to a tool that never writes.
		"grep": allow,
	})
}

// raySeedPolicies is Ray's seeded tool policy.
func raySeedPolicies() map[string]config.ToolPolicy {
	allow := config.ToolPolicyAllow
	// Ray — the Scout / research analyst. LEAST-PRIVILEGE: deny-by-default,
	// allow only the research surface (search + read the web and local docs,
	// drive a browser for interactive sources, write up findings to files,
	// synthesize with memory, present with citations). No shell, no admin, no
	// task/agent management — he researches and reports, he doesn't build or run.
	ask := config.ToolPolicyAsk
	return denyAllThenOverride(map[string]config.ToolPolicy{
		// AskUserQuestion (spec US-7 S1): every human-facing agent may ask
		// the user structured clarification questions.
		"AskUserQuestion": allow,
		// set_goal (ADR-088 D2): every human-facing agent may author its
		// own session's goal record — seeded alongside AskUserQuestion.
		"set_goal": allow,
		// goal_claim (ADR-084 D12): every human-facing agent may claim
		// its own session's goal complete — seeded alongside set_goal.
		"goal_claim": allow,
		// Web research.
		"search_web": allow,
		"fetch_url":  allow,
		// Interactive / visual research (NOT browser_evaluate — arbitrary JS).
		"browser_navigate":   allow,
		"browser_click":      allow,
		"browser_type":       allow,
		"browser_get_text":   allow,
		"browser_wait":       allow,
		"browser_screenshot": allow,
		// ADR-041 D3 — tab-management, same allow as the rest of Ray's
		// interactive/visual browsing surface.
		"browser_list_tabs":  allow,
		"browser_switch_tab": allow,
		"browser_close_tab":  allow,
		"browser_open_tab":   allow,
		// ADR-075 D2 — the interaction verbs and the accessibility
		// snapshot. Same allow as the rest of Ray's browsing surface, and
		// for the same reason browser_evaluate above is NOT: none of these
		// five runs arbitrary code.
		"browser_select_option": allow,
		"browser_press_key":     allow,
		"browser_hover":         allow,
		"browser_snapshot":      allow,
		// ADR-075 D2 FR-035/A-12 — the dialog recovery verb, allow. See
		// the note on the delegation-tier seeds above: the consequential
		// half (`accept:true`) is an argument-level guard, not a policy
		// value, because policy cannot see arguments.
		"browser_handle_dialog": allow,
		// browser_handover (ADR-085 BROWSER-FR-051, C-70): allow. Ray
		// holds the full browser action set above, so he holds the verb
		// that stands down from it — an agent that can drive the
		// operator's browser must be able to hand it back.
		"browser_handover": allow,
		// FR-021 — ask, not deny. Attaching a file to a page on the
		// operator's signed-in session is the one browser verb that hands
		// their data outward, so it is consent-gated on every agent that
		// holds the browser surface.
		"browser_upload_file": ask,
		// FR-030 — the file:// refusal now names serve_web, so Ray must be
		// able to reach it; a pointer to a tool he resolves deny for is
		// #242's dead end one failed call further away.
		"serve_web": allow,
		// Local sources + writing up research results.
		"read_file":      allow,
		"list_directory": allow,
		"write_file":     allow,
		"append_file":    allow,
		"edit_file":      allow,
		// Chat-uploaded files land in this workspace's library (D3,
		// library-spec) — Ray needs to find and read them, matching his
		// read_file/list_directory allowance above.
		"library_list":  allow,
		"library_read":  allow,
		"request_mount": ask,
		"list_mounts":   allow,
		// Persistent memory (carries research context across sessions).
		"remember":            allow,
		"recall_memory":       allow,
		"run_retrospective":   allow,
		"recall_conversation": allow,
		// Deep-research delegation: fan out parallel research subagents
		// (delegate → many workers/Researcher) and poll them, then synthesize.
		// ADR-036 merged spawn/run_subagent/check_spawn_status into "delegate".
		"delegate": allow,
		// message_parent (ADR-053 §5.1): only actually callable when Ray
		// himself is running as a delegated child session, but seeded
		// allow here to mirror delegate's posture exactly.
		"message_parent": allow,
		// Present / route / share an artifact.
		"send_message": allow,
		"switch_agent": allow,
		"send_file":    allow,
		// Working aids (his summarize skill; a research checklist).
		"find_skills": allow,
		"set_todos":   allow,
		// ADR-052 FR-005: every seeded agent OTHER than Jim is explicit
		// "ask" (never absent, never deny) for the three plan-execution
		// tools — an operator-approval prompt gates any attempted use.
		"create_plan":  ask,
		"execute_plan": ask,
		"run_task":     ask,
		// FR-006b seed rule: stop_plan rides with execute_plan, same map,
		// same literal value. See coreAgentSeed's doc comment.
		"stop_plan": ask,
		// ADR-056 roster visibility: Ray fans out parallel research
		// subagents (delegate: allow above) and then synthesizes, so the
		// "which of my delegated children are still running?" roster is
		// directly on his critical path. See coreAgentSeed's ROSTER
		// VISIBILITY rule.
		"list_jobs": allow,
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
		// ADR-068 D15.3 — knowledge base, split by BLAST RADIUS
		// (superseding ADR-067 D17's file). Ray is the Scout: retrieval
		// (knowledge_describe/knowledge_find/knowledge_read) is squarely
		// his job and is allow — "what is still open in this vault?" is
		// a survey question, and knowledge_find now answers it directly
		// via `kind: task` (FR-076a), superseding the old
		// knowledge_tasks/knowledge_search split this comment used to
		// reason about separately.
		//
		// knowledge_edit, knowledge_restructure and knowledge_configure
		// are all "ask" — unchanged posture from ADR-067's knowledge_
		// create/knowledge_move: he researches and reports rather than
		// editing the operator's knowledge base, and his file writes go
		// to the workspace (write_file/append_file above), not to a
		// mounted vault. knowledge_restructure/knowledge_configure are a
		// WIDER blast radius than the single-file writes that already
		// warranted asking (see coreAgentSeed's IDAva case for the
		// cascade/control-plane argument), so there is no case for
		// loosening either past "ask" for a role whose job was never to
		// write there at all.
		"knowledge_describe": allow,
		"knowledge_find":     allow,
		"knowledge_read":     allow,
		// knowledge_list (KB-2a, defect-list-knowledge-base-ux-2026-09-08.md,
		// founder-ratified 2026-09-08) — allow, same read-tier posture as the
		// three read tools above: it reports what already exists, touching
		// nothing.
		"knowledge_list":        allow,
		"knowledge_edit":        ask,
		"knowledge_restructure": ask,
		"knowledge_configure":   ask,
		// knowledge_base_create (KB-1, same defect list) — "ask", not "allow":
		// it creates a new folder+marker in the operator's own Library, an
		// effect this agent's own call cannot bound, matching the write-three's
		// own "ask" reasoning immediately above.
		"knowledge_base_create": ask,
		// ADR-081 D11 (FR-009, founder ruling): grep is unprompted allow
		// for every agent tier, including Ray — and squarely his job: a
		// read-only recursive name/content search over local sources,
		// confined to his own workspace root and mounts (FR-020), is the
		// research surface this agent already holds (read_file,
		// list_directory, search_web, fetch_url above), not a write that
		// would warrant the "ask" his knowledge writes carry.
		"grep": allow,
	})
}

// jimSeedPolicies is Jim's seeded tool policy.
func jimSeedPolicies() map[string]config.ToolPolicy {
	allow := config.ToolPolicyAllow
	// Jim — the Planner & Orchestrator. LEAST-PRIVILEGE: deny-by-default,
	// allow only the tools his role needs (plan, delegate, manage tasks +
	// workspaces, run shell/browser). This replaces
	// the old allow-by-default + "system.*" deny rail, which the §7 tool
	// rename silently broke — renamed management tools (create_workspace,
	// set_config, …) no longer match the "system.*" glob, so every
	// former-system tool fell through to allow.
	ask := config.ToolPolicyAsk
	return denyAllThenOverride(map[string]config.ToolPolicy{
		// AskUserQuestion (spec US-7 S1): every human-facing agent may ask
		// the user structured clarification questions.
		"AskUserQuestion": allow,
		// set_goal (ADR-088 D2): every human-facing agent may author its
		// own session's goal record — seeded alongside AskUserQuestion.
		"set_goal": allow,
		// goal_claim (ADR-084 D12): every human-facing agent may claim
		// its own session's goal complete — seeded alongside set_goal.
		"goal_claim": allow,
		// File operations — read, write, and navigate the workspace.
		"read_file":      allow,
		"write_file":     allow,
		"edit_file":      allow,
		"append_file":    allow,
		"list_directory": allow,
		// Chat-uploaded files land in this workspace's library (D3,
		// library-spec) — Jim needs to find and read them, matching his
		// read_file/list_directory allowance above.
		"library_list":  allow,
		"library_read":  allow,
		"request_mount": ask,
		"list_mounts":   allow,
		// External lookups.
		"search_web": allow,
		"fetch_url":  allow,
		// Web serving — scaffolds and serves web apps in the sandbox.
		"serve_web": allow,
		// Shell execution — sandboxed shell, foreground + background
		// (ADR-036: exec/workspace_shell/workspace_shell_bg merged into
		// one universally-registered tool, governed by this policy alone).
		"bash": allow,
		// Communication / routing.
		"send_message": allow,
		"send_file":    allow,
		"switch_agent": allow,
		// Persistent memory (carries planning context across sessions).
		"remember":            allow,
		"recall_memory":       allow,
		"run_retrospective":   allow,
		"recall_conversation": allow,
		"set_todos":           allow,
		// Delegation — delegate to subagents, poll them, list who's available.
		// ADR-036 merged spawn/run_subagent/check_spawn_status into "delegate".
		"delegate": allow,
		// message_parent (ADR-053 §5.1): only actually callable when Jim
		// himself is running as a delegated child session, but seeded
		// allow here to mirror delegate's posture exactly.
		"message_parent": allow,
		"list_agents":    allow,
		// Task management (current workspace).
		"create_task": allow,
		"list_tasks":  allow,
		"update_task": allow,
		// Task management (cross-workspace).
		"create_task_in_workspace": allow,
		"list_tasks_in_workspace":  allow,
		"update_task_in_workspace": allow,
		// Workspace lifecycle — Jim manages workspaces (not just reads them).
		"get_workspace":    allow,
		"list_workspaces":  allow,
		"update_workspace": allow,
		"create_workspace": allow,
		// Skill discovery + installation (NOT authoring — that's Ava's domain).
		"find_skills":   allow,
		"list_skills":   allow,
		"install_skill": allow,
		// MCP server management. Jim may SEE the configured servers, but not
		// add one: an MCP server definition is a program the gateway launches
		// unconfined, so adding one escapes the sandbox through the front door
		// (config.json is in the ADR-062 secret set exactly so an agent cannot
		// write that entry with write_file). Denied in the global seed for the
		// same reason — see the long rationale on "add_mcp_server" in
		// pkg/config/defaults.go. Seeded data, not a code branch (CLAUDE.md
		// constraint 6): an operator who wants Jim installing MCP servers
		// changes this entry on their own install.
		"list_mcp_servers": allow,
		"add_mcp_server":   config.ToolPolicyDeny,
		// Browser automation (interactive/visual work in the sandboxed browser).
		// browser_evaluate (arbitrary JS) is operator-approved for Jim and stays
		// runtime-gated by sandbox.browser_evaluate_enabled regardless of policy.
		"browser_navigate":   allow,
		"browser_click":      allow,
		"browser_type":       allow,
		"browser_wait":       allow,
		"browser_get_text":   allow,
		"browser_screenshot": allow,
		"browser_evaluate":   allow,
		// ADR-041 D3 — tab-management, same allow as the rest of Jim's
		// browser automation surface.
		"browser_list_tabs":  allow,
		"browser_switch_tab": allow,
		"browser_close_tab":  allow,
		"browser_open_tab":   allow,
		// ADR-075 D2 — the interaction verbs and the accessibility
		// snapshot, same allow as the rest of Jim's browser surface.
		"browser_select_option": allow,
		"browser_press_key":     allow,
		"browser_hover":         allow,
		"browser_snapshot":      allow,
		// ADR-075 D2 FR-035/A-12 — the dialog recovery verb, allow. See
		// the note on the delegation-tier seeds above: the consequential
		// half (`accept:true`) is an argument-level guard, not a policy
		// value, because policy cannot see arguments.
		"browser_handle_dialog": allow,
		// browser_handover (ADR-085 BROWSER-FR-051, C-70): allow, same
		// reasoning as Ray's — Jim holds the full browser action set,
		// so he holds the verb that stands down from it.
		"browser_handover": allow,
		// FR-021 — ask even for Jim, who holds every other browser grant
		// including browser_evaluate. Attaching a file is the one verb
		// that hands the operator's data OUT of the machine, and the
		// consent gate is on the direction of travel, not on the agent.
		"browser_upload_file": ask,
		// Delete / remove operations are consent-gated (ask) — standing rule.
		"delete_task":              ask,
		"delete_task_in_workspace": ask,
		"delete_workspace":         ask,
		"remove_mcp_server":        ask,
		// ADR-052 FR-005/R2-06: Jim is the ONLY seeded agent granted
		// unprompted plan-execution — consistent with his orchestrator
		// role. Every other seeded agent gets an explicit "ask" instead
		// (never absent, never deny); the Judge gets "deny"
		// (systemAgentSeed, DS-6).
		//
		// These three RESOLVE to "allow" for Jim only because the global
		// ceiling for them is also "allow" (pkg/config/defaults.go). It
		// was "ask" until 2026-07-28, which — under the strictest-wins
		// global x agent merge — silently overruled all three entries
		// below and made this whole grant dead on every install. If you
		// are tightening the ceiling for any of these, you are reverting
		// that fix: tool_policy_effective_resolution_test.go will fail,
		// and it is telling you the truth.
		"create_plan":  allow,
		"execute_plan": allow,
		"run_task":     allow,
		// FR-006b seed rule: stop_plan rides with execute_plan, same map,
		// same literal value — so the orchestrator who is the only agent
		// seeded to START a plan unprompted is also the one seeded to STOP
		// it unprompted. Its ceiling has always been "allow", so it kept
		// resolving allow even while execute_plan's did not; both now do.
		"stop_plan": allow,
		// ADR-056 roster visibility: Jim is the only agent seeded to START
		// a plan unprompted and the only one whose stop_plan actually
		// RESOLVES allow, so he is the one agent for whom containment must
		// work with no human in the loop — which needs a plan id he did not
		// necessarily mint this turn. He is also the heaviest delegator.
		// See coreAgentSeed's ROSTER VISIBILITY rule.
		"list_jobs": allow,
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
		// ADR-068 D15.3 — knowledge base, split by BLAST RADIUS
		// (superseding ADR-067 D17's file). Retrieval
		// (knowledge_describe/knowledge_find/knowledge_read) is allow
		// for all four base agents.
		//
		// UNLIKE Ava/Mia/Ray above, Jim's writes (knowledge_edit,
		// knowledge_restructure, knowledge_configure) stay "allow" too,
		// and this is a DELIBERATE exception argued from Jim's own
		// already-seeded posture a few lines above ("bash": allow), not
		// an oversight that forgot to tighten him along with the other
		// three. An "ask" gate on knowledge_restructure/
		// knowledge_configure has real teeth for an agent who cannot
		// otherwise touch the operator's files — that is exactly why
		// Ava/Mia/Ray hold it. Jim already holds unprompted bash, and
		// bash can rewrite, rename or delete anything in a mounted
		// collection — including reproducing knowledge_restructure's
		// cascade or knowledge_configure's schema rewrite by hand, with
		// no prompt at all. Gating the knowledge-tool EQUIVALENTS behind
		// "ask" for him specifically would not reduce what he can do; it
		// would only make the orchestrator depend on a human to do
		// through the audited, journal-backed tool what he could
		// already do unaudited through bash. That is exactly the
		// "protects nothing" prompt this codebase's own seeding
		// philosophy warns against — training an operator to click
		// through confirmations that gate nothing real, which erodes
		// trust in the ones that do (see the analogous
		// knowledge_tasks-vs-knowledge_search reasoning this file
		// carried before ADR-068, now superseded but the same warning
		// still holds for Jim's case here).
		"knowledge_describe": allow,
		"knowledge_find":     allow,
		"knowledge_read":     allow,
		// knowledge_list (KB-2a, defect-list-knowledge-base-ux-2026-09-08.md,
		// founder-ratified 2026-09-08) — allow, same read-tier posture as the
		// three read tools above: it reports what already exists, touching
		// nothing.
		"knowledge_list":        allow,
		"knowledge_edit":        allow,
		"knowledge_restructure": allow,
		"knowledge_configure":   allow,
		// knowledge_base_create (KB-1, same defect list) — allow, the same
		// deliberate exception this case already argues for the write three
		// above: unprompted bash can already create arbitrary folders and
		// files, so gating the audited equivalent behind "ask" would protect
		// nothing real here either.
		"knowledge_base_create": allow,
		// ADR-081 D11 (FR-009, founder ruling): grep is unprompted allow
		// for every agent tier, Jim included — consistent with his
		// existing unprompted bash and knowledge-write grants above: a
		// read-only recursive search confined to his own workspace root
		// and mounts (FR-020) is a strictly narrower capability than
		// what he can already do unaudited through bash.
		"grep": allow,
	})
}

// coreAgentSkills returns the seeded per-agent skill allowlist (FR-9.4). The
// allowlist is enforced at skill-resolution time (default-DENY): a core agent
// can only resolve/invoke the skills returned here. The matrix:
//
//	summarize       → Mia, Ray, Explorer, Researcher
//	plan            → Jim, Planner
//	skill-authoring → Ava
//	daily-briefing  → Mia
//	define-goal     → every agent above (ADR-074 D4: any agent that authors
//	                  acceptance criteria or a Definition of Done carries the
//	                  one built-in criteria-authoring skill; renamed from
//	                  define-done by ADR-080 D-SKILL)
//
// Returns nil for an agent that has no seeded skills (no restriction seeded).
func coreAgentSkills(id CoreAgentID) []string {
	switch id {
	case IDMia:
		return []string{"summarize", "daily-briefing", "define-goal"}
	case IDRay:
		return []string{"summarize", "define-goal"}
	case IDJim:
		return []string{"plan", "define-goal"}
	case IDAva:
		return []string{"skill-authoring", "define-goal"}
	case IDPlanner:
		// The Planner decomposes goals into a task DAG — the plan skill is its core.
		return []string{"plan", "define-goal"}
	case IDExplorer, IDResearcher:
		// Explorer + Researcher synthesize what they find.
		return []string{"summarize", "define-goal"}
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

// coreAgentDelegation returns the seeded canonical unified delegation policy for
// a base agent so orchestration + worker fan-out work out of the box (fixes the
// historically empty Trust-Graph gap). The matrix:
//
//	Jim (Orchestrator) → [ava, ray, worker]      modes: [task, background, await]
//	Mia, Ray, Ava      → [worker]                 modes: [task, background]
//	Planner            → [explorer, researcher]   modes: [await, task]  depth: 2
//
// Every base agent can therefore offload labor to the general-purpose worker;
// Jim can additionally fan out to two base agents (Ava, Ray) plus the worker.
// The specialists are NOT in any base agent's to[] — only the Planner drives the
// Explorer/Researcher specialists (see the IDPlanner case below). Everything not
// listed stays deny-by-default. Returns nil for an agent with no seeded
// delegation (incl. Explorer/Researcher and the generic worker — leaves that do
// not delegate onward by default).
//
// The modes above are this SEED's own 3-value vocabulary (config.DelegationMode:
// task/background/await — the delegate tool's real runtime call parameter) and
// deliberately do NOT change when the workspace trust-edge vocabulary collapses
// to 2 values (workspace.DelegationMode: direct/task). pkg/gateway's
// defaultWorkspaceDelegationEdges (via agent.EdgeModeCategory) is the ONE seam
// that translates this matrix onto a fresh workspace's graph edges, collapsing
// background/await into a single "direct" entry per edge (deduped) — so e.g.
// Jim's seeded [task, background, await] becomes a graph edge with
// Modes: [task, direct], not three separate entries. This function's own
// output is never itself the graph; it stays a seed DTO consumed once, at
// workspace-creation time.
func coreAgentDelegation(id CoreAgentID) *config.DelegationPolicy {
	ref := func(agentID CoreAgentID) config.AgentRef {
		return config.AgentRef{Kind: config.AgentRefKindLocal, ID: string(agentID)}
	}
	switch id {
	case IDJim:
		return &config.DelegationPolicy{
			To: []config.AgentRef{ref(IDAva), ref(IDRay), ref(IDWorker)},
			Modes: []config.DelegationMode{
				config.DelegationModeTask,
				config.DelegationModeBackground,
				config.DelegationModeAwait,
			},
		}
	case IDMia, IDAva:
		return &config.DelegationPolicy{
			To: []config.AgentRef{ref(IDWorker)},
			Modes: []config.DelegationMode{
				config.DelegationModeTask,
				config.DelegationModeBackground,
			},
		}
	case IDRay:
		// Ray (Scout) runs a "deep research" mode: fan out MANY parallel research
		// subagents (the general worker + the dedicated Researcher) and synthesize
		// their findings. Background mode powers the parallel fan-out; await lets
		// him collect a sub-result synchronously when needed.
		return &config.DelegationPolicy{
			To: []config.AgentRef{ref(IDWorker), ref(IDResearcher)},
			Modes: []config.DelegationMode{
				config.DelegationModeTask,
				config.DelegationModeBackground,
				config.DelegationModeAwait,
			},
		}
	case IDPlanner:
		// The Planner gathers context before planning by delegating to Explorer
		// (internal files + memory) and Researcher (external sources). Bounded by
		// depth=2 so onward fan-out stays within the global subturn ceiling. This
		// is the bounded subagent-delegation unlock (M5) made concrete: a subagent
		// that carries a non-empty to[].
		return &config.DelegationPolicy{
			To: []config.AgentRef{ref(IDExplorer), ref(IDResearcher)},
			Modes: []config.DelegationMode{
				config.DelegationModeAwait,
				config.DelegationModeTask,
			},
			Depth: intPtr(2),
		}
	default:
		// Explorer, Researcher, and the generic worker are leaves: no onward
		// delegation by default. For IDWorker specifically this nil is
		// load-bearing, not incidental: the worker's tool-policy map
		// (coreAgentSeed's IDWorker branch) deliberately leaves "delegate"
		// absent so it inherits the global ceiling's "allow" — the delegate
		// tool's runtime gate (buildDelegationDenyChecker, ADR-037) requires a
		// matching edge in the per-workspace delegation graph before onward
		// delegation is reachable, so today "delegate: allow" on the worker is
		// inert (no such edge is seeded FROM the worker). If a future change
		// ever seeds a delegation edge FROM the worker, that edge plus this
		// inherited "allow" would combine to make onward delegation real —
		// revisit the worker's tool-policy overrides at the same time.
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
// for any ID with no seeded delegation policy (Explorer/Researcher, the
// generic worker, and any non-core/custom agent ID) — exactly what
// coreAgentDelegation returned for those IDs before this export existed.
func SeedDelegationEdges(id CoreAgentID) *config.DelegationPolicy {
	return coreAgentDelegation(id)
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
	if applyDefineDoneSkillsMigration(sc.cfg) {
		sc.modified = true
	}

	// ADR-080 D-SKILL: one-shot, marker-keyed REWRITE migration for installs
	// that already hold the old "define-done" token (seeded fresh by an
	// earlier release, or just appended by applyDefineDoneSkillsMigration
	// immediately above on an install upgrading straight from pre-ADR-074).
	// Must run AFTER applyDefineDoneSkillsMigration so both markers can land
	// in the SAME boot for that double-upgrade case, with the token already
	// renamed by the time this pass returns.
	if applyDefineGoalRenameMigration(sc.cfg) {
		sc.modified = true
	}

	// Founder decision 2026-09-15 (issue #710): goal_claim is allowed by
	// default for every agent that can own a goal or be assigned a task.
	// One-time, marker-keyed update so an install seeded before the Worker's
	// seed changed reaches the same default a fresh install gets. Runs AFTER
	// the seeding loops, so a fresh install's just-seeded Worker (already
	// allow) is left as-is and only the marker is recorded.
	if applyWorkerGoalClaimAllowUpdate(sc.cfg) {
		sc.modified = true
	}

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
	return &config.AgentToolsCfg{
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
				// per-agent policy map (the other six are coreAgentSeed's
				// core roster + IDWorker + the subagent tier, and
				// systemAgentSeed's Judge/PlanSupervisor), and every new
				// tool name in this delivery gets an explicit, intended
				// entry here too — an absent key is not an unknown key, so
				// validateOverrideKeys does not panic and a fresh custom
				// agent would silently resolve the tool to the
				// denyAllThenOverride floor (deny) with no test noticing.
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
				"library_list":        allow,
				"library_read":        allow,
				"request_mount":       ask,
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
}
