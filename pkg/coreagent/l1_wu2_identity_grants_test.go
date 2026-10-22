package coreagent_test

import (
	"slices"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent oracles: omnipus-system-agent-spec §6.5, FR-001/007/008,
// BDD-1.1/1.6. No expected grants are copied from a production inventory.
func TestL1WU2Seed_FreshInstallHasOneLockedOmnipusAndNoRetiredAgents(t *testing.T) {
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	t.Run("ordinary_chat_positive_control", func(t *testing.T) {
		mia := l1WU2SeededAgent(t, cfg, "mia")
		assert.Equal(t, "Mia", mia.Name)
		assert.Equal(t, config.AgentTypeCore, mia.Type)
		assert.True(t, mia.Locked)
	})
	counts := map[string]int{}
	for _, ac := range cfg.Agents.List {
		counts[ac.ID]++
	}
	assert.Zero(t, counts["ava"], "FR-008: Ava must not be seeded")
	assert.Zero(t, counts["admin"], "FR-008: Admin must not be seeded")
	if counts["omnipus"] == 0 {
		t.Fatal("BLOCKED: Omnipus seed not implemented — required by FR-001 / BDD-1.1")
	}
	require.Equal(t, 1, counts["omnipus"], "FR-001: exactly one Omnipus")
	omnipus := l1WU2SeededAgent(t, cfg, "omnipus")
	assert.Equal(t, "Omnipus", omnipus.Name)
	assert.Equal(t, config.AgentTypeCore, omnipus.Type)
	assert.True(t, omnipus.Locked, "FR-001: identity is locked")
	assert.True(t, coreagent.IsCoreAgent("omnipus"), "Omnipus is a real core chat target")
	assert.Empty(t, coreagent.GetPrompt("ava"), "FR-008: no retired Ava prompt")
	assert.Empty(t, coreagent.GetPrompt("admin"), "FR-008: no retired Admin prompt")
	require.NotEmpty(t, coreagent.GetPrompt("omnipus"), "Omnipus must have a compiled identity")
	before := len(cfg.Agents.List)
	coreagent.SeedConfig(cfg)
	assert.Len(t, cfg.Agents.List, before, "seeding twice must not duplicate the identity")
}

func l1WU2SeededAgent(t *testing.T, cfg *config.Config, id string) config.AgentConfig {
	t.Helper()
	for _, ac := range cfg.Agents.List {
		if ac.ID == id {
			return ac
		}
	}
	t.Fatalf("BLOCKED: seed for %s not implemented — required by FR-001 / §6.5", id)
	return config.AgentConfig{}
}

// The complete allow surface is transcribed from spec §6.5. Adding any
// out-of-role tool to the production catalogue must still leave it explicit Deny.
func l1WU2OmnipusSupport() []string {
	return []string{
		"list_agents", "get_agent", "get_agent_tools", "read_agent_metadata",
		"list_workspaces", "get_workspace", "list_models", "list_providers", "test_provider",
		"list_mcp_servers", "list_channels", "test_channel", "list_skills", "find_skills",
		"run_doctor", "get_usage", "list_secrets", "AskUserQuestion", "send_message",
		"answer_request", "message_parent", "delegate", "Skill", "ToolSearch", "remember",
		"recall_memory", "recall_conversation", "run_retrospective", "set_goal", "goal_claim",
		"read_file", "write_file", "edit_file", "append_file", "list_directory", "grep",
		"list_mounts", "knowledge_describe", "knowledge_find", "knowledge_read", "knowledge_list",
		"search_web", "fetch_url", "create_agent", "update_agent", "delete_agent",
		// Founder 2026-10-18: create yes; delete deferred to #1363 (explicit Deny).
		"create_workspace", "update_workspace", "create_skill", "edit_skill", "remove_skill", "install_skill",
		"add_mcp_server", "remove_mcp_server", "configure_provider", "enable_channel",
		"disable_channel", "configure_channel", "connect_channel", "connect_mailbox",
		"set_agent_permissions", "request_secret", "select_secret_version", "remove_secret",
		"read_conversation", "list_mailboxes", "environment_setup", "bash",
	}
}

func TestL1WU2OmnipusPolicies_ExactSupportSetAndNoPerAgentAsk(t *testing.T) {
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	ac := l1WU2SeededAgent(t, cfg, "omnipus")
	require.NotNil(t, ac.Tools)
	support := l1WU2OmnipusSupport()
	catalog := coreagent.AllStaticToolNames()
	for _, name := range support {
		assert.Contains(t, catalog, name, "§6.5: granted tool %s must not be invisible to the catalogue", name)
	}
	ask := []string{"delete_agent", "remove_skill", "remove_mcp_server", "add_mcp_server",
		"configure_provider", "enable_channel", "configure_channel", "connect_channel",
		"connect_mailbox", "set_agent_permissions", "remove_secret", "select_secret_version",
		"environment_setup", "bash"}
	global := map[string]config.ToolPolicy{}
	for name, p := range cfg.Sandbox.ToolPolicies {
		global[name] = config.ToolPolicy(p)
	}
	pc := &tools.ToolPolicyCfg{Policies: ac.Tools.Builtin.Policies, GlobalPolicies: global}
	for _, name := range catalog {
		t.Run(name, func(t *testing.T) {
			want := "deny"
			if slices.Contains(support, name) {
				want = "allow"
				if slices.Contains(ask, name) {
					want = "ask"
				}
			} else {
				p, present := ac.Tools.Builtin.Policies[name]
				assert.True(t, present, "FR-007: out-of-role tool needs explicit per-agent Deny")
				assert.Equal(t, config.ToolPolicyDeny, p)
			}
			assert.Equal(t, want, tools.ResolveEffectivePolicy(pc, name), "BDD-1.6 normal mode")
			assert.NotEqual(t, config.ToolPolicyAsk, ac.Tools.Builtin.Policies[name], "FR-022: Omnipus stores no per-agent Ask")
			if slices.Contains(ask, name) {
				_, present := ac.Tools.Builtin.Policies[name]
				assert.False(t, present, "§6.5: ask-list tools ride the ceiling, not an agent override")
			}
			pc.GodMode = true
			godWant := "deny"
			if slices.Contains(support, name) {
				godWant = "allow"
			}
			assert.Equal(t, godWant, tools.ResolveEffectivePolicy(pc, name), "FR-023: God Mode floors only the global ceiling")
			pc.GodMode = false
		})
	}
}

func TestL1WU2SecretAndManagementGrants_AllNativeRolesAndCustomMain(t *testing.T) {
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	// §6.5: main agents get private-secret tools; workers and engine agents do not.
	rows := []struct {
		id      string
		private bool
	}{
		{"mia", true}, {"jim", true}, {"worker", false}, {"planner", false},
		{"researcher", false}, {"judge", false}, {"plansupervisor", false},
	}
	newNames := []string{"request_secret", "list_secrets", "remove_secret", "select_secret_version",
		"connect_channel", "connect_mailbox", "set_agent_permissions", "read_conversation", "list_mailboxes"}
	catalog := coreagent.AllStaticToolNames()
	for _, name := range newNames {
		assert.Contains(t, catalog, name, "FR-007/021: new tool must be classified explicitly")
	}
	for _, row := range rows {
		t.Run(row.id, func(t *testing.T) {
			ac := l1WU2SeededAgent(t, cfg, row.id)
			require.NotNil(t, ac.Tools)
			for _, name := range newNames {
				want := config.ToolPolicyDeny
				if row.private && slices.Contains([]string{"request_secret", "list_secrets", "read_conversation"}, name) {
					want = config.ToolPolicyAllow
				}
				pc, _ := tools.BuildFallbackPolicyCfg(cfg, row.id)
				assert.Equal(t, string(want), tools.ResolveEffectivePolicy(pc, name), "%s/%s independent §6.5 oracle", row.id, name)
				if want == config.ToolPolicyDeny {
					p, present := ac.Tools.Builtin.Policies[name]
					assert.True(t, present, "%s/%s needs explicit Deny", row.id, name)
					assert.Equal(t, config.ToolPolicyDeny, p)
				}
			}
			for _, name := range l1WU2ManagementNames() {
				assert.Equal(t, config.ToolPolicyDeny, ac.Tools.Builtin.Policies[name], "%s must not inherit management %s", row.id, name)
			}
		})
	}
	t.Run("custom_main", func(t *testing.T) {
		custom := coreagent.NewCustomAgentToolsCfg()
		require.NotNil(t, custom)
		assert.Equal(t, config.ToolPolicyAllow, custom.Builtin.Policies["read_file"], "ordinary read positive control")
		for _, name := range []string{"request_secret", "list_secrets", "read_conversation"} {
			assert.Equal(t, config.ToolPolicyAllow, custom.Builtin.Policies[name], "§6.5 custom main override for %s", name)
		}
		for _, name := range l1WU2ManagementNames() {
			p, present := custom.Builtin.Policies[name]
			assert.True(t, present, "new custom main must classify %s", name)
			assert.Equal(t, config.ToolPolicyDeny, p)
		}
	})
}

func l1WU2ManagementNames() []string {
	// Spec §6.5 management family, OSA-3 and explicit not-granted workspace/config tools.
	return []string{"create_agent", "update_agent", "delete_agent", "create_workspace", "update_workspace",
		"delete_workspace", "create_skill", "edit_skill", "remove_skill", "install_skill",
		"add_mcp_server", "remove_mcp_server", "configure_provider", "enable_channel", "disable_channel",
		"configure_channel", "connect_channel", "connect_mailbox", "set_agent_permissions",
		"remove_secret", "select_secret_version", "set_config"}
}
