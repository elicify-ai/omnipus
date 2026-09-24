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
	grant(allow, "ToolSearch", "Skill")

	commonWork := []string{"read_file", "list_directory", "grep", "list_mounts", "library_list", "library_read", "remember", "recall_memory", "recall_conversation", "send_message", "message_parent", "goal_claim", "read_inbox", "search_email", "read_message", "knowledge_describe", "knowledge_find", "knowledge_read", "knowledge_list"}
	switch id {
	case IDMia:
		grant(allow, append(commonWork, "AskUserQuestion", "set_goal", "write_file", "edit_file", "append_file", "search_web", "fetch_url", "switch_agent", "send_file", "create_task", "update_task", "list_tasks", "set_todos", "find_skills")...)
		// environment_setup (ADR-090 ES-FR-01, founder ruling 2026-09-18):
		// Ask — and the sparse seed RETAINS it as an explicit stored entry
		// (deliberate posture list in adr090SparseRolePolicies) even though
		// it equals the ceiling, so a later ceiling raise leaves Mia at Ask.
		// Jim/Ava/Planner/Researcher get no grant and classify explicit
		// Deny in this inventory.
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
		grant(ask, "send_email", "reply", "request_mount", "browser_upload_file", "environment_setup", "bash")
	case IDJim:
		grant(allow, append(commonWork, "AskUserQuestion", "set_goal", "search_web", "fetch_url", "switch_agent", "send_file", "create_task", "update_task", "list_tasks", "list_jobs", "set_todos", "delegate", "create_plan", "execute_plan", "stop_plan", "find_skills", "list_skills")...)
		grant(ask, "send_email", "reply", "request_mount", "browser_upload_file")
	case IDAva:
		grant(allow, append(commonWork, "AskUserQuestion", "set_goal", "search_web", "fetch_url", "switch_agent", "list_agents", "get_agent", "get_agent_tools", "create_agent", "update_agent", "delete_agent", "list_models", "find_skills", "list_skills", "install_skill", "create_skill", "edit_skill", "remove_skill", "list_mcp_servers", "get_workspace", "list_workspaces", "update_workspace")...)
		grant(ask, "send_email", "reply", "request_mount")
	case IDAdmin:
		grant(allow, "remember", "recall_memory", "recall_conversation", "AskUserQuestion", "set_goal", "goal_claim", "read_file", "write_file", "edit_file", "append_file", "list_directory", "grep", "list_mounts", "send_message", "switch_agent", "add_mcp_server", "list_mcp_servers", "list_providers", "configure_provider", "test_provider", "list_models", "list_channels", "configure_channel", "enable_channel", "test_channel", "run_doctor", "get_usage")
		grant(allow, "knowledge_describe", "knowledge_find", "knowledge_read", "knowledge_list")
		// environment_setup (ADR-090 ES-FR-01): Ask — explicit stored entry
		// (deliberate posture). Admin's cross-workspace FILESYSTEM authority
		// is a runtime authority fact, never a tool-policy change; the
		// approval stays with the user.
		//
		// bash (re-pointed 2026-09-24, ADR-092): see IDMia's identical note
		// above — the global ceiling shipped "ask", so Admin's intended
		// bash posture is Ask too, listed explicitly rather than silently
		// defaulting to it.
		grant(ask, "request_mount", "remove_mcp_server", "disable_channel", "environment_setup", "bash")
	case IDPlanner:
		grant(allow, append(commonWork, "search_web", "fetch_url", "create_task", "update_task", "list_tasks", "delegate")...)
		grant(ask, "send_email", "reply", "request_mount")
	case IDResearcher:
		grant(allow, append(commonWork, "search_web", "fetch_url")...)
		grant(ask, "send_email", "reply", "request_mount")
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
		grant(ask, "send_email", "reply", "request_mount", "environment_setup", "bash")
	}
	if id == IDMia || id == IDJim || id == IDWorker {
		grant(ask, "knowledge_edit", "knowledge_restructure", "knowledge_configure", "knowledge_base_create")
	}
	if id == IDMia || id == IDJim || id == IDAva {
		grant(allow, "browser_navigate", "browser_click", "browser_type", "browser_screenshot", "browser_get_text", "browser_wait", "browser_evaluate", "browser_list_tabs", "browser_switch_tab", "browser_close_tab", "browser_open_tab", "browser_select_option", "browser_press_key", "browser_hover", "browser_snapshot", "browser_handle_dialog", "browser_handover")
		grant(ask, "browser_upload_file")
	}
	return result
}

func adr090SparseRolePolicies(id CoreAgentID) map[string]config.ToolPolicy {
	intended := ADR090RolePolicyInventory(id)
	ceiling := config.DefaultConfig().Sandbox.ToolPolicies
	result := make(map[string]config.ToolPolicy)
	for _, name := range allStaticToolNames {
		if intended[name] != config.ToolPolicy(ceiling[name]) {
			result[name] = intended[name]
		}
	}
	// These entries communicate deliberate workflow posture even where it
	// currently equals the ceiling. environment_setup (ADR-090 ES-FR-01,
	// founder ruling 2026-09-18) rides this list so Mia, General Purpose
	// and Admin PERSIST an explicit "ask" of their own — setup permission
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
