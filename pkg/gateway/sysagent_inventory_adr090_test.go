package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// buildExecutorTestAPI constructs the first registry instance before it writes
// the matching entity. agentstore.Create stamps CreatedAt onto that entity, so
// the two configs deliberately diverge until the production roster/upsert path
// runs. Align this fixture before testing later stale-publication transitions.
func publishPersistedTestAgent(t *testing.T, api *restAPI) {
	t.Helper()
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		return populateAgentsListFromEntityStoreStrict(cfg, api.homePath)
	}))
	_, err := api.agentLoop.UpsertAgentFast(api.agentLoop.GetConfig(), "test-agent")
	require.NoError(t, err)
}

func TestSysagentAgentActiveRevisionUsesRegisteredInstanceNotRefreshedConfig(t *testing.T) {
	api := buildExecutorTestAPI(t)
	publishPersistedTestAgent(t, api)
	store := agentstore.New(api.homePath)
	before, err := store.ReadState("test-agent")
	require.NoError(t, err)
	require.Equal(t, before.Revision, sysagentAgentActiveRevision(api.agentLoop, api.homePath, "test-agent"))

	result, err := store.MutateState("test-agent", before.Revision, func(agent *config.AgentConfig) error {
		agent.Description = "persisted but not published"
		return nil
	}, nil)
	require.NoError(t, err)
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		return populateAgentsListFromEntityStoreStrict(cfg, api.homePath)
	}))

	// The loop config now contains the saved update, but the registry still
	// contains the old instance. Config equality must not masquerade as proof.
	require.Empty(t, sysagentAgentActiveRevision(api.agentLoop, api.homePath, "test-agent"))

	_, err = api.agentLoop.UpsertAgentFast(api.agentLoop.GetConfig(), "test-agent")
	require.NoError(t, err)
	require.Equal(t, result.Revision, sysagentAgentActiveRevision(api.agentLoop, api.homePath, "test-agent"))
}

func TestSysagentAgentActiveRevisionTreatsSoulOnlyWriteAsLive(t *testing.T) {
	api := buildExecutorTestAPI(t)
	publishPersistedTestAgent(t, api)
	store := agentstore.New(api.homePath)
	before, err := store.ReadState("test-agent")
	require.NoError(t, err)
	soul := "updated soul read dynamically by the registered context builder"
	result, err := store.MutateState("test-agent", before.Revision, nil, &soul)
	require.NoError(t, err)

	// Agent instances read SOUL.md dynamically; no config-derived runtime state
	// changed, so the existing instance represents this revision immediately.
	require.Equal(t, result.Revision, sysagentAgentActiveRevision(api.agentLoop, api.homePath, "test-agent"))
}
