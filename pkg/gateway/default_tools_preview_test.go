package gateway

import (
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SPEC.md compact output: every creation kind is previewable and uses its
// own Table column. Existing and preview inventories must agree after creation.
func defaultToolsAssertPreviewSchema(t *testing.T) {
	t.Helper()
	params := systools.NewAgentGetToolsTool(nil).Parameters()
	properties, ok := params["properties"].(map[string]any)
	require.True(t, ok, "preview tool must advertise its parameters")
	selector, ok := properties["new_agent_type"].(map[string]any)
	require.True(t, ok, "preview must advertise its type selector")
	assert.ElementsMatch(t, []string{"Main", "Subagent", "subagent_3p"}, selector["enum"],
		"SPEC.md: every creation kind must be an advertised preview kind")
}

func defaultToolsAssertPreview(t *testing.T, api *restAPI, stored *config.AgentConfig, created *tools.ToolPolicyCfg, rows []defaultToolsPolicyCase, kind string) {
	t.Helper()
	catalog := api.builtinRegistry.All()
	columnKind := "worker"
	if kind == "Main" {
		columnKind = "main"
	}
	t.Run("new_agent_type", func(t *testing.T) {
		raw := defaultToolsInventory(t, api.homePath, api.agentLoop.GetConfig(), catalog, map[string]any{"new_agent_type": kind})
		policies := defaultToolsParseCompact(t, raw, fmt.Sprintf("preview: %s | kind: %s", kind, columnKind), catalog)
		defaultToolsAssertInventoryPolicies(t, policies, created, rows, kind)
	})
	t.Run("existing", func(t *testing.T) {
		state, err := agentstore.New(api.homePath).ReadState(stored.ID)
		require.NoError(t, err)
		header := fmt.Sprintf("agent: %s | name: %s | kind: %s | revision: %s", stored.ID, stored.Name, columnKind, state.Revision)
		raw := defaultToolsInventory(t, api.homePath, api.agentLoop.GetConfig(), catalog, map[string]any{"id": stored.ID})
		policies := defaultToolsParseCompact(t, raw, header, catalog)
		defaultToolsAssertInventoryPolicies(t, policies, created, rows, kind)
	})
}

func defaultToolsAssertInventoryPolicies(t *testing.T, policies map[string]string, created *tools.ToolPolicyCfg, rows []defaultToolsPolicyCase, kind string) {
	t.Helper()
	expected := make(map[string]string, len(rows))
	for _, row := range rows {
		expected[row.name] = defaultToolsWant(row, kind)
	}
	for _, name := range defaultToolsAdminOnlyNames() {
		expected[name] = "deny"
	}
	for name, actual := range policies {
		t.Run(name, func(t *testing.T) {
			if want, specified := expected[name]; specified {
				assert.Equal(t, want, actual, "SPEC.md Table: %s compact inventory/%s", kind, name)
			}
			assert.Equal(t, tools.ResolveEffectivePolicy(created, name), actual,
				"SPEC.md compact output: %s inventory/%s must match effective policy after real creation", kind, name)
		})
	}
}
