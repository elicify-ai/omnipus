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

	commonWork := []string{"read_file", "list_directory", "grep", "list_mounts", "library_list", "library_read", "remember", "recall_memory", "recall_conversation", "send_message", "message_parent", "goal_claim"}
	switch id {
	case IDMia:
		grant(allow, append(commonWork, "AskUserQuestion", "set_goal", "write_file", "edit_file", "append_file", "search_web", "fetch_url", "switch_agent", "send_file", "create_task", "update_task", "list_tasks", "set_todos", "read_inbox", "search_email", "read_message", "bash", "find_skills", "browser_navigate", "browser_click", "browser_type", "browser_screenshot", "browser_get_text", "browser_wait", "browser_list_tabs", "browser_switch_tab", "browser_close_tab", "browser_open_tab", "browser_select_option", "browser_press_key", "browser_hover", "browser_snapshot", "browser_handle_dialog", "browser_handover")...)
		grant(ask, "send_email", "reply", "request_mount", "browser_upload_file")
	case IDJim:
		grant(allow, append(commonWork, "AskUserQuestion", "set_goal", "search_web", "fetch_url", "switch_agent", "send_file", "create_task", "update_task", "list_tasks", "list_jobs", "set_todos", "delegate", "create_plan", "execute_plan", "stop_plan", "find_skills", "list_skills")...)
		grant(ask, "send_email", "reply", "request_mount")
	case IDAva:
		grant(allow, append(commonWork, "AskUserQuestion", "set_goal", "search_web", "fetch_url", "switch_agent", "list_agents", "create_agent", "update_agent", "delete_agent", "list_models", "find_skills", "list_skills", "install_skill", "create_skill", "edit_skill", "remove_skill", "list_mcp_servers", "get_workspace", "list_workspaces", "update_workspace")...)
		grant(ask, "send_email", "reply", "request_mount")
	case IDAdmin:
		grant(allow, "AskUserQuestion", "set_goal", "goal_claim", "read_file", "write_file", "edit_file", "append_file", "list_directory", "grep", "list_mounts", "bash", "send_message", "switch_agent", "add_mcp_server", "list_mcp_servers", "list_providers", "configure_provider", "test_provider", "list_models", "list_channels", "configure_channel", "enable_channel", "test_channel", "run_doctor", "get_usage")
		grant(ask, "request_mount", "remove_mcp_server", "disable_channel")
	case IDPlanner:
		grant(allow, append(commonWork, "create_task", "update_task", "list_tasks", "delegate")...)
		grant(ask, "send_email", "reply", "request_mount")
	case IDResearcher:
		grant(allow, append(commonWork, "search_web", "fetch_url")...)
		grant(ask, "send_email", "reply", "request_mount")
	case IDWorker:
		grant(allow, append(commonWork, "bash", "write_file", "edit_file", "append_file", "search_web", "fetch_url", "send_file", "update_task", "list_tasks", "set_todos", "delegate", "serve_web")...)
		grant(ask, "send_email", "reply", "request_mount")
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
	// currently equals the ceiling.
	for _, name := range []string{"ToolSearch", "Skill", "grep", "send_email", "reply", "add_mcp_server"} {
		result[name] = intended[name]
	}
	return result
}
