package coreagent_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func TestADR090_ManagementReadbackRealCatalogReachability(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = nil
	coreagent.SeedConfig(cfg)
	global := map[string]config.ToolPolicy{}
	for name, policy := range cfg.Sandbox.ToolPolicies {
		global[name] = config.ToolPolicy(policy)
	}
	for _, agent := range cfg.Agents.List {
		t.Run(agent.ID, func(t *testing.T) {
			require.NotNil(t, agent.Tools)
			_, policies := tools.FilterToolsByPolicy(systools.AllTools(nil), string(agent.Type), &tools.ToolPolicyCfg{GlobalPolicies: global, Policies: agent.Tools.Builtin.Policies})
			for _, name := range []string{"get_agent", "get_agent_tools"} {
				if agent.ID == "ava" {
					require.Equal(t, "allow", policies[name], "Ava must actually reach %s", name)
				} else {
					require.Empty(t, policies[name], "%s must not silently inherit management readback", agent.ID)
				}
			}
		})
	}
}

// Expected groups come from ADR-090 section 5, independently of the seed inventory.
func TestADR090_ApprovedSupportingGroups(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = nil
	coreagent.SeedConfig(cfg)
	for _, group := range []struct {
		name    string
		names   []string
		roles   map[string]bool
		verdict string
	}{
		{"browser", []string{"browser_navigate", "browser_click", "browser_type", "browser_screenshot", "browser_get_text", "browser_wait", "browser_evaluate", "browser_list_tabs", "browser_switch_tab", "browser_close_tab", "browser_open_tab", "browser_select_option", "browser_press_key", "browser_hover", "browser_snapshot", "browser_handle_dialog", "browser_handover"}, map[string]bool{"mia": true, "jim": true, "ava": true}, "allow"},
		{"browser-upload", []string{"browser_upload_file"}, map[string]bool{"mia": true, "jim": true, "ava": true}, "ask"},
		{"email-read", []string{"read_inbox", "search_email", "read_message"}, map[string]bool{"mia": true, "jim": true, "ava": true, "planner": true, "researcher": true, "worker": true}, "allow"},
		{"email-send", []string{"send_email", "reply"}, map[string]bool{"mia": true, "jim": true, "ava": true, "planner": true, "researcher": true, "worker": true}, "ask"},
		{"memory", []string{"remember", "recall_memory", "recall_conversation"}, map[string]bool{"mia": true, "jim": true, "ava": true, "admin": true, "planner": true, "researcher": true, "worker": true}, "allow"},
		{"web", []string{"search_web", "fetch_url"}, map[string]bool{"mia": true, "jim": true, "ava": true, "planner": true, "researcher": true, "worker": true}, "allow"},
	} {
		for _, agent := range cfg.Agents.List {
			for _, name := range group.names {
				t.Run(group.name+"/"+agent.ID+"/"+name, func(t *testing.T) {
					expected := "deny"
					if group.roles[agent.ID] {
						expected = group.verdict
					}
					require.Equal(t, expected, resolveFor(t, cfg, agent.ID, name, nil))
				})
			}
		}
	}
}
