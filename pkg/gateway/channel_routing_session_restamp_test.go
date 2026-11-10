// Existing channel conversations keep their original workspace under founder
// Q1 B. These fixtures retain both platform and instance identity so routing
// changes cannot accidentally alter a sibling instance's conversations.
package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// seedExistingChannelSession creates a session the way
// pkg/agent/loop.go::resolveOrCreateChannelSession does for a real inbound
// message: the bare type on Channel AND the instance key on InstanceID.
//
// Persist the instance too: preservation tests must distinguish sibling
// instances of one platform, not accidentally conflate them by channel type.
func seedExistingChannelSession(t *testing.T, api *restAPI, instanceID, chatID, agentID, initialWorkspaceID string) *session.UnifiedMeta {
	t.Helper()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be initialized in test harness")
	channelType, _ := config.ParseInstanceKey(instanceID)
	meta, err := store.NewChannelSession(channelType, instanceID, chatID, agentID, "existing convo")
	require.NoError(t, err)
	if initialWorkspaceID != "" {
		ws := initialWorkspaceID
		require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &ws}))
	}
	fresh, err := store.GetMeta(meta.ID)
	require.NoError(t, err)
	return fresh
}
