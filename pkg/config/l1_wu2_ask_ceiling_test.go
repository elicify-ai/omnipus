package config_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
)

// Oracle: omnipus-system-agent-spec FR-020/021, BDD-3.3 and dataset D5.
func TestL1WU2DefaultCeiling_OmnipusAskListAndPrivateSecretAllows(t *testing.T) {
	cfg := config.DefaultConfig()
	t.Run("lookup_positive_control", func(t *testing.T) {
		assert.Equal(t, "allow", cfg.Sandbox.ToolPolicies["read_file"])
		assert.Equal(t, "allow", cfg.Sandbox.ToolPolicies["get_agent"])
	})
	for _, name := range []string{
		"delete_agent", "remove_skill", "add_mcp_server", "configure_provider", "enable_channel", "configure_channel",
		"remove_mcp_server", "connect_channel", "connect_mailbox", "set_agent_permissions", "remove_secret",
		"select_secret_version", "environment_setup", "bash",
	} {
		t.Run(name, func(t *testing.T) {
			value, present := cfg.Sandbox.ToolPolicies[name]
			assert.True(t, present, "FR-021: missing new tools must not vanish from ceiling coverage")
			assert.Equal(t, "ask", value, "FR-020/021: shipped ceiling for %s", name)
		})
	}
	for _, name := range []string{"request_secret", "list_secrets", "read_conversation", "list_mailboxes"} {
		t.Run(name, func(t *testing.T) {
			value, present := cfg.Sandbox.ToolPolicies[name]
			assert.True(t, present, "§6.5: %s must have an explicit shipped ceiling", name)
			assert.Equal(t, "allow", value)
		})
	}
	// SPEC.md Q22=B: create_skill/edit_skill stay globally Allow so exempt Omnipus can ride the ceiling.
	for _, name := range []string{"create_agent", "update_agent", "update_workspace", "create_skill", "edit_skill", "install_skill", "disable_channel"} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, "allow", cfg.Sandbox.ToolPolicies[name], "OSA-3: summary-gated changes retain allow")
		})
	}
}
