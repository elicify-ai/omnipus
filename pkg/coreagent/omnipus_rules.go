// omnipus_rules.go: the write-time rules every path applies to the Omnipus
// system agent (omnipus-system-agent spec FR-002, FR-022, FR-025).

package coreagent

import (
	"sort"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// OmnipusPerAgentAskRefusal is the exact refusal text for a per-agent "ask"
// policy on Omnipus (spec FR-022, §6.2: 422 on REST, an error result on the
// agent tools). Omnipus never carries a per-agent ask: its ask list rides the
// global ceiling, and "deny" stays accepted.
const OmnipusPerAgentAskRefusal = "Omnipus takes no per-agent Ask; set Ask globally or use Deny"

// OmnipusPerAgentAskViolation reports the first tool (sorted, so the message is
// deterministic) whose per-agent policy is "ask" when agentID is Omnipus. For
// any other agent, or a map without "ask", it returns ("", false).
func OmnipusPerAgentAskViolation(agentID string, policies map[string]config.ToolPolicy) (string, bool) {
	if CoreAgentID(agentID) != IDOmnipus {
		return "", false
	}
	names := make([]string, 0, len(policies))
	for name, policy := range policies {
		if policy == config.ToolPolicyAsk {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", false
	}
	sort.Strings(names)
	return names[0], true
}

// managementTools is the management family (spec §6.5 and OSA-3): agent,
// workspace, skill, connector, provider, channel, permission, secret and
// configuration change tools. create_agent refuses any non-deny grant of one
// of them to a new agent (FR-025) — only Omnipus holds them, through the
// ceiling.
var managementTools = map[string]bool{
	"create_agent": true, "update_agent": true, "delete_agent": true,
	"create_workspace": true, "update_workspace": true, "delete_workspace": true,
	"create_skill": true, "edit_skill": true, "remove_skill": true, "install_skill": true,
	"add_mcp_server": true, "remove_mcp_server": true, "configure_provider": true,
	"enable_channel": true, "disable_channel": true, "configure_channel": true,
	"connect_channel": true, "connect_mailbox": true, "set_agent_permissions": true,
	"remove_secret": true, "select_secret_version": true, "set_config": true,
}

// IsManagementTool reports whether name belongs to the management family.
func IsManagementTool(name string) bool { return managementTools[name] }

// ManagementGrantViolation reports the first management tool (sorted) that a
// new agent's requested policy map grants with anything other than "deny"
// (FR-025: both "allow" and "ask" are grants). ("", false) when there is none.
func ManagementGrantViolation(policies map[string]config.ToolPolicy) (string, bool) {
	names := make([]string, 0)
	for name, policy := range policies {
		if managementTools[name] && policy != config.ToolPolicyDeny {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", false
	}
	sort.Strings(names)
	return names[0], true
}

// ManagementGrantRefusal is the refusal text for FR-025, naming the tool.
func ManagementGrantRefusal(tool string) string {
	return "a new agent cannot be granted the management tool " + tool +
		" — management tools stay with Omnipus; set it to deny or leave it out"
}
