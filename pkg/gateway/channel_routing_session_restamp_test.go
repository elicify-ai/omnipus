// channel_routing_session_restamp_test.go — regression coverage for the
// "binding a channel to a workspace does not reach conversations that
// already exist" defect.
//
// pkg/agent/loop.go::resolveOrCreateChannelSession stamps workspace_id on a
// channel session ONLY at creation time (its own doc comment: "Already-
// existing sessions are NOT patched"). setChannelRouting previously wrote
// cfg.Channels[id].WorkspaceID and emitted an audit event but never touched
// any session already on disk for that channel — so an existing
// conversation kept routing to the (possibly empty, silently
// default-substituted per resolveEffectiveWorkspaceID) old workspace
// forever, even though new messages on the SAME conversation would resolve
// the new agent/workspace via routing. These tests pin the fix: binding,
// re-binding, and unbinding a channel's workspace must re-stamp every
// existing session whose Channel matches the instance's base type.
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
// Persisting the instance is what makes a restamp safe on an install with many
// instances of one platform. Seeding only the type here would make these tests
// pass against a restamp that matched by type — the exact defect they exist to
// prevent.
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
