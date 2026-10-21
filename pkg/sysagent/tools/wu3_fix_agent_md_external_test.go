package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent_md follows soul's field rules (docs/agents.md, #1064): an external
// CLI worker does not support it, so update_agent refuses it there and writes
// no AGENT.md. A native custom agent accepts it (control).
func TestWU3AgentMDIsRefusedOnExternalWorkers(t *testing.T) {
	d, _ := wu3Deps(t)
	store := agentstore.New(d.Home)
	ext := config.AgentConfig{ID: "ext-worker", Name: "External", Type: config.AgentTypeWorker,
		Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "opencode"}}}
	require.True(t, ext.IsExternalCLIWorker(), "fixture is an external CLI worker")
	require.NoError(t, store.Create(ext.ID, &ext))
	state, err := store.ReadState(ext.ID)
	require.NoError(t, err)
	res := systools.NewAgentUpdateTool(d).Execute(context.Background(), map[string]any{
		"id": ext.ID, "revision": state.Revision, "agent_md": "# not for external workers\n",
	})
	wu3Error(t, res, "INVALID_INPUT")
	_, statErr := os.Stat(filepath.Join(d.Home, "agents", ext.ID, "AGENT.md"))
	assert.ErrorIs(t, statErr, os.ErrNotExist, "no AGENT.md written")

	native := config.AgentConfig{ID: "native-agent", Name: "Native", Type: config.AgentTypeCustom}
	require.NoError(t, store.Create(native.ID, &native))
	state, err = store.ReadState(native.ID)
	require.NoError(t, err)
	wu3Success(t, systools.NewAgentUpdateTool(d).Execute(context.Background(), map[string]any{
		"id": native.ID, "revision": state.Revision, "agent_md": "# native\n",
	}))
}
