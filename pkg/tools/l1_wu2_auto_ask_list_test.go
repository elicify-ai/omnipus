package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: FR-021, BDD-3.3, §6.5; explicit registration is essential here:
// ClassifyAutoApprove's unknown-name fallback also asks and could fake a green.
func TestL1WU2AutoAskList_NewManagementToolsAreExplicitAndNeverAutoRun(t *testing.T) {
	table := AutoApproveClassTable()
	t.Run("known_read_positive_control", func(t *testing.T) {
		class, present := table["read_file"]
		require.True(t, present)
		assert.Equal(t, AutoRuns, class)
		assert.True(t, ClassifyAutoApprove(context.Background(), "read_file", nil, nil).Run)
	})
	for _, name := range []string{"delete_agent", "remove_skill", "remove_mcp_server", "add_mcp_server",
		"configure_provider", "enable_channel", "configure_channel", "connect_channel", "connect_mailbox",
		"set_agent_permissions", "remove_secret", "select_secret_version", "environment_setup"} {
		t.Run(name, func(t *testing.T) {
			class, present := table[name]
			assert.True(t, present, "FR-021: %s must be on the explicit D9 ask-list, not just unknown-name refusal", name)
			assert.Equal(t, AutoAsks, class)
			verdict := ClassifyAutoApprove(context.Background(), name, nil, map[string]any{})
			assert.False(t, verdict.Run, "BDD-3.3: Auto must still require a person's approval for %s", name)
			assert.Equal(t, AutoVerdictClassAsks, verdict.Class)
		})
	}
}
