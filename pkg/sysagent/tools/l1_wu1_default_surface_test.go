package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-120 / §6.4: no default tool argument or get_agent default response.
// Parameters are runtime tool definitions, not a source-text grep.
func TestL1WU1AgentTools_DefaultArgumentAndResponseAreRetired(t *testing.T) {
	deps, _ := newTestDepsWithRealSave(t)
	t.Setenv("OMNIPUS_HOME", deps.Home)
	for _, tool := range systools.AllTools(deps) {
		if tool.Name() != "create_agent" && tool.Name() != "update_agent" {
			continue
		}
		t.Run(tool.Name()+"_schema", func(t *testing.T) {
			props, ok := tool.Parameters()["properties"].(map[string]any)
			require.True(t, ok)
			assert.Contains(t, props, "name", "known editable field proves the schema inspection is active")
			assert.NotContains(t, props, "default", "FR-120: the retired concept must not be offered to agents")
		})
	}
	ac := config.AgentConfig{ID: "no-default-subject", Name: "No default subject", Type: config.AgentTypeCustom}
	require.NoError(t, agentstore.New(deps.Home).Create(ac.ID, &ac))
	result := systools.NewAgentGetTool(deps).Execute(context.Background(), map[string]any{"id": ac.ID})
	require.False(t, result.IsError)
	body := parseSuccess(t, result.ForLLM)
	assert.Equal(t, ac.ID, body["id"], "real read-back positive control")
	assert.NotContains(t, body, "default", "FR-120: tool read-back omits default even when false")
}
