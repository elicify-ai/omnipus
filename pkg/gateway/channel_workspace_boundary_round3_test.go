package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// Capture a used conversation before changing connector configuration. Q1 B
// requires the entire old identity/archive to survive, including unfiled chats.
func round3ChannelUnchanged(t *testing.T, api *restAPI, sessionID string) func() {
	t.Helper()
	store := api.agentLoop.GetSessionStore()
	require.NoError(t, store.AddMessage(sessionID, "user", "preserved old conversation "+sessionID))
	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	archive, err := store.ReadArchive(t.Context(), sessionID)
	require.NoError(t, err)
	require.Len(t, archive, 1, "positive control: old history is present")
	return func() {
		t.Helper()
		afterMeta, metaErr := store.GetMeta(sessionID)
		require.NoError(t, metaErr)
		require.Equal(t, meta, afterMeta, "connector changes must not relabel old conversations")
		afterArchive, archiveErr := store.ReadArchive(t.Context(), sessionID)
		require.NoError(t, archiveErr)
		require.Equal(t, archive, afterArchive, "connector changes must not transfer or erase old history")
	}
}

func round3RequireChannelPair(t *testing.T, api *restAPI, instance, workspaceID, agentID string) {
	t.Helper()
	binding := api.agentLoop.GetConfig().Channels[instance]
	require.Equal(t, workspaceID, binding.WorkspaceID)
	require.NotNil(t, binding.Identity)
	require.Equal(t, config.ChannelIdentityKindAgent, binding.Identity.Kind)
	require.Equal(t, agentID, binding.Identity.ID)
}
