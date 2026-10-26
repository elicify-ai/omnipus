package coreagent

import "github.com/elicify-ai/omnipus/pkg/config"

// ADR090RolePolicyInventory classifies every static tool for every shipped
// ordinary role. It is evidence and a lint input; the persisted ordinary seed
// is the sparse delta from the shipped global ceiling, not this complete map.
func ADR090RolePolicyInventory(id CoreAgentID) map[string]config.ToolPolicy {
	deny, allow, ask := config.ToolPolicyDeny, config.ToolPolicyAllow, config.ToolPolicyAsk
	result := make(map[string]config.ToolPolicy, len(allStaticToolNames))
	for _, name := range allStaticToolNames {
		result[name] = deny
	}
	grant := func(policy config.ToolPolicy, names ...string) {
		for _, name := range names {
			result[name] = policy
		}
	}
	if id == IDOmnipus {
		// Spec §6.5: Omnipus's EXACT supporting set rides the ceiling (allow,
		// or ask for the ask-list tools); everything else — the role-wide
		// grants below included — stays deny.
		ceiling := config.DefaultConfig().Sandbox.ToolPolicies
		for _, name := range omnipusSupportedTools {
			grant(config.ToolPolicy(ceiling[name]), name)
		}
		return result
	}
	// Drafting writes only a Drafts copy and can never send. Keep its
	// existing Allow grant explicit; mailbox setup may not overwrite a
	// saved Deny to recover an accidentally omitted creation default.
	grant(allow, "create_email_draft")
	grant(allow, "ToolSearch", "Skill")

	// Keep each role's existing work and knowledge grants. The shared
	// Main/Worker core below then adds the positive defaults from the
	// 2026-10-22 table, including received-attachment downloads at Allow.
	// Sending mail remains Ask. Hidden system roles use a separate seed.
	commonWork := []string{"read_file", "list_directory", "grep", "list_mounts", "library_list", "library_read", "remember", "recall_memory", "run_retrospective", "recall_conversation", "send_message", "answer_request", "message_parent", "goal_claim", "read_inbox", "search_email", "read_message", "list_email_attachments", "read_email_attachment", "knowledge_describe", "knowledge_find", "knowledge_read", "knowledge_list"}
	switch id {
	case IDMia:
		grant(allow, append(commonWork, "AskUserQuestion", "set_goal", "write_file", "edit_file", "append_file", "search_web", "fetch_url", "send_file", "create_task", "update_task", "list_tasks", "set_todos", "find_skills")...)
		// environment_setup (ADR-090 ES-FR-01, founder ruling 2026-09-18):
		// Ask — and the sparse seed RETAINS it as an explicit stored entry
		// (deliberate posture list in adr090SparseRolePolicies) even though
		// it equals the ceiling, so a later ceiling raise leaves Mia at Ask.
		// The other ordinary roles receive the same Ask through the
		// shared Main/Worker core below.
		//
		// bash (re-pointed 2026-09-24, ADR-092): the global ceiling shipped
		// "ask" for bash (pkg/config/defaults.go) so the D1 Ask/Auto/God
		// Mode selector and the D7/D8 pre-flights actually engage on a
		// fresh install — see that file's own comment. An "allow" grant
		// HERE would be silently overruled by that stricter ceiling under
		// strictest-wins (TestEffectiveResolution_SeededAgentAllow_
		// IsNeverOverruledByCeiling's own documented remediation: "change
		// the per-agent seed to match reality, not weaken the test"), so
		// Mia's intended bash posture is Ask, same as everyone else —
		// listed explicitly (matching environment_setup's own convention
		// above) rather than silently defaulting to it.
		grant(ask, "send_email", "reply", "download_email_attachment", "request_mount", "browser_upload_file", "environment_setup", "bash")
	case IDJim:
		grant(allow, append(commonWork, "AskUserQuestion", "set_goal", "search_web", "fetch_url", "send_file", "create_task", "update_task", "list_tasks", "list_jobs", "set_todos", "delegate", "create_plan", "execute_plan", "stop_plan", "find_skills", "list_skills")...)
		grant(ask, "send_email", "reply", "download_email_attachment", "request_mount", "browser_upload_file")
	case IDPlanner:
		grant(allow, append(commonWork, "search_web", "fetch_url", "create_task", "update_task", "list_tasks", "delegate")...)
		grant(ask, "send_email", "reply", "download_email_attachment", "request_mount")
	case IDResearcher:
		grant(allow, append(commonWork, "search_web", "fetch_url")...)
		grant(ask, "send_email", "reply", "download_email_attachment", "request_mount")
	case IDWorker:
		grant(allow, append(commonWork, "write_file", "edit_file", "append_file", "search_web", "fetch_url", "send_file", "update_task", "list_tasks", "set_todos", "delegate", "serve_web")...)
		// environment_setup (ADR-090 ES-FR-01): Ask — explicit stored entry
		// (deliberate posture), mirroring Mia; General Purpose performs
		// document workflows.
		//
		// bash (re-pointed 2026-09-24, ADR-092): see IDMia's identical note
		// above — the global ceiling shipped "ask", so the Worker's
		// intended bash posture is Ask too, listed explicitly rather than
		// silently defaulting to it.
		grant(ask, "send_email", "reply", "download_email_attachment", "request_mount", "environment_setup", "bash")
	}
	if id == IDMia || id == IDJim || id == IDWorker {
		grant(ask, "knowledge_edit", "knowledge_restructure", "knowledge_configure", "knowledge_base_create")
	}
	// The chat-target agents may request secrets and read their own
	// conversations. The shared Worker core below also permits listing
	// secrets, but not requesting one or reading other conversations.
	if id == IDMia || id == IDJim {
		grant(allow, "request_secret", "list_secrets", "read_conversation")
		grant(allow, "browser_navigate", "browser_click", "browser_type", "browser_screenshot", "browser_get_text", "browser_wait", "browser_evaluate", "browser_list_tabs", "browser_switch_tab", "browser_close_tab", "browser_open_tab", "browser_select_option", "browser_press_key", "browser_hover", "browser_snapshot", "browser_handle_dialog", "browser_handover")
		grant(ask, "browser_upload_file")
	}
	// Ordinary built-ins get at least the positive core set of their kind.
	// Apply this only while constructing defaults, never on saved operator
	// policies. Skip the core's denies so role-specific extras, knowledge
	// grants and deliberate shell postures remain unchanged.
	switch id {
	case IDMia, IDJim, IDPlanner, IDResearcher, IDWorker:
		core := NewCustomAgentToolsCfg()
		if IsSubagentTierID(id) {
			RestrictForWorkerTier(core)
		}
		for name, policy := range core.Builtin.Policies {
			if policy != deny {
				grant(policy, name)
			}
		}
	}
	return result
}

func adr090SparseRolePolicies(id CoreAgentID) map[string]config.ToolPolicy {
	if id == IDOmnipus {
		return omnipusSeedPolicies()
	}
	intended := ADR090RolePolicyInventory(id)
	ceiling := config.DefaultConfig().Sandbox.ToolPolicies
	result := make(map[string]config.ToolPolicy)
	// Q22=B: the ordinary core's create_skill/edit_skill Ask differs from
	// the Allow ceiling, so this delta persists both local Ask entries.
	// Omnipus returned above and keeps no local entry for either tool.
	for _, name := range allStaticToolNames {
		if intended[name] != config.ToolPolicy(ceiling[name]) {
			result[name] = intended[name]
		}
	}
	// These entries communicate deliberate workflow posture even where it
	// currently equals the ceiling. environment_setup (ADR-090 ES-FR-01,
	// founder ruling 2026-09-18) rides this list so ordinary Main/Worker
	// roles PERSIST an explicit "ask" of their own — setup permission
	// is set at both levels, so a later ceiling raise to allow leaves
	// their stored ask (until the operator explicitly changes or removes
	// it via the normal tool_policy edit paths). The deny roles are
	// unaffected: their deny differs from the ask ceiling and is already
	// persisted by the delta loop above.
	for _, name := range []string{"ToolSearch", "Skill", "grep", "send_email", "reply", "add_mcp_server", "environment_setup"} {
		result[name] = intended[name]
	}
	return result
}

// omnipusSupportedTools is Omnipus's exact supporting set, transcribed from
// omnipus-system-agent spec §6.5. Every name rides the global ceiling (no
// per-agent entry — Omnipus never carries a per-agent ask, FR-022); every
// other static tool is an explicit per-agent deny (FR-007).
var omnipusSupportedTools = []string{
	"list_agents", "get_agent", "get_agent_tools", "read_agent_metadata",
	"list_workspaces", "get_workspace", "list_models", "list_providers", "test_provider",
	"list_mcp_servers", "list_channels", "test_channel", "list_skills", "find_skills",
	"run_doctor", "get_usage", "list_secrets", "AskUserQuestion", "send_message",
	"answer_request", "message_parent", "delegate", "Skill", "ToolSearch", "remember",
	"recall_memory", "recall_conversation", "run_retrospective", "set_goal", "goal_claim",
	"read_file", "write_file", "edit_file", "append_file", "list_directory", "grep",
	"list_mounts", "knowledge_describe", "knowledge_find", "knowledge_read", "knowledge_list",
	"search_web", "fetch_url", "create_agent", "update_agent", "delete_agent",
	"create_workspace", "update_workspace", "create_skill", "edit_skill", "remove_skill", "install_skill",
	"add_mcp_server", "remove_mcp_server", "configure_provider", "enable_channel",
	"disable_channel", "configure_channel", "connect_channel", "connect_mailbox",
	"set_agent_permissions", "request_secret", "select_secret_version", "remove_secret",
	"read_conversation", "list_mailboxes", "environment_setup", "bash",
}

// OmnipusSupportedTools returns a copy of Omnipus's supporting set (§6.5).
func OmnipusSupportedTools() []string {
	out := make([]string, len(omnipusSupportedTools))
	copy(out, omnipusSupportedTools)
	return out
}

// omnipusSeedPolicies returns Omnipus's sparse per-agent map: an explicit
// deny for every static tool outside the supporting set and NO entry for any
// supported tool, so each supported tool resolves to the global ceiling —
// allow, or ask for the ask list (FR-020, FR-021) — and God Mode floors those
// to allow (FR-023) while the denies stay. Validated against the static
// catalog by denyAllThenOverride's key check.
func omnipusSeedPolicies() map[string]config.ToolPolicy {
	supported := make(map[string]config.ToolPolicy, len(omnipusSupportedTools))
	for _, name := range omnipusSupportedTools {
		supported[name] = config.ToolPolicyAllow
	}
	validateOverrideKeys(supported)
	out := make(map[string]config.ToolPolicy)
	for _, name := range allStaticToolNames {
		if _, ok := supported[name]; !ok {
			out[name] = config.ToolPolicyDeny
		}
	}
	return out
}
