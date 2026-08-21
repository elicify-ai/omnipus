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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
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

// TestSetChannelRouting_Bound_RestampsExistingSessions verifies that binding
// an UNBOUND channel to a workspace re-stamps sessions that already existed
// on that channel before the bind, not only sessions created afterward.
func TestSetChannelRouting_Bound_RestampsExistingSessions(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	// A conversation that already exists BEFORE the channel is ever bound.
	existing := seedExistingChannelSession(t, api, "whatsapp.eu", "peer-1", "mia", "")
	require.Empty(t, existing.WorkspaceID, "precondition: session starts with no workspace")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, w.Code, "bind must succeed: %s", w.Body.String())

	store := api.agentLoop.GetSessionStore()
	after, err := store.GetMeta(existing.ID)
	require.NoError(t, err)
	assert.Equal(t, "sales", after.WorkspaceID,
		"binding the channel to a workspace must re-stamp an already-existing session's workspace_id")
}

// TestSetChannelRouting_Rebind_UpdatesExistingSessions verifies that MOVING
// a channel from workspace W1 to W2 re-stamps sessions off W1 onto W2 —
// not just newly-bound state that only affects future sessions.
func TestSetChannelRouting_Rebind_UpdatesExistingSessions(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "w1", "active", []string{"mia", "ray"}, false)
	writeTestWorkspaceJSON(t, api, "w2", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	// Bind to w1 first.
	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"w1","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, w.Code)

	// A conversation created while w1 was bound.
	existing := seedExistingChannelSession(t, api, "whatsapp.eu", "peer-2", "ray", "w1")
	require.Equal(t, "w1", existing.WorkspaceID)

	// Re-bind the SAME instance to w2.
	w2 := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"w2","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, w2.Code, "rebind must succeed: %s", w2.Body.String())

	store := api.agentLoop.GetSessionStore()
	after, err := store.GetMeta(existing.ID)
	require.NoError(t, err)
	assert.Equal(t, "w2", after.WorkspaceID,
		"re-binding a channel to a different workspace must move existing sessions off the stale workspace")
}

// TestSetChannelRouting_Unbind_ClearsExistingSessions verifies that
// explicitly UNBINDING a previously-bound channel clears the stale
// workspace_id off existing sessions rather than leaving them pinned to a
// workspace the channel is no longer routed through.
func TestSetChannelRouting_Unbind_ClearsExistingSessions(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, w.Code)

	existing := seedExistingChannelSession(t, api, "whatsapp.eu", "peer-3", "ray", "sales")
	require.Equal(t, "sales", existing.WorkspaceID)

	// Unbind: PUT with no workspace_id at all (legacy/unbound flow).
	wu := setChannelRoutingReq(t, api, "whatsapp.eu", `{"default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, wu.Code, "unbind must succeed: %s", wu.Body.String())

	store := api.agentLoop.GetSessionStore()
	after, err := store.GetMeta(existing.ID)
	require.NoError(t, err)
	assert.Empty(t, after.WorkspaceID,
		"unbinding a channel must clear the stale workspace_id off its existing sessions")
}

// TestSetChannelRouting_Bound_UnboundChannelSessionsUnaffected verifies that
// binding one channel instance's workspace does NOT touch sessions
// belonging to a different, still-unbound channel type — the restamp must
// be scoped to the channel actually being bound.
func TestSetChannelRouting_Bound_UnboundChannelSessionsUnaffected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")
	seedChannelInstance(t, api, "telegram")

	// An existing session on telegram, which stays unbound throughout.
	telegramSession := seedExistingChannelSession(t, api, "telegram", "peer-9", "mia", "")
	require.Empty(t, telegramSession.WorkspaceID)

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"sales","default_agent_id":"ray"}`)
	require.Equal(t, http.StatusOK, w.Code)

	store := api.agentLoop.GetSessionStore()
	after, err := store.GetMeta(telegramSession.ID)
	require.NoError(t, err)
	assert.Empty(t, after.WorkspaceID,
		"an unrelated, never-bound channel's existing sessions must be unaffected by another channel's bind")
}

// TestSetChannelRouting_SiblingInstanceIsUntouched is the defect this fix
// exists to prevent, and the reason the session record needed an instance key.
//
// An install can hold a hundred WhatsApp numbers, each bound to its own
// (workspace, agent) pair under ADR-029. Every one of their sessions records
// Channel=="whatsapp". A restamp that matched on the bare TYPE would relabel
// all hundred when an operator re-bound one of them — inflicting on the other
// ninety-nine exactly the stale-workspace bug the restamp was written to fix,
// and silently moving their delegation trust, memory rooms and task placement
// to the wrong workspace.
//
// The first version of this fix did match by type. It only became visible
// because someone asked whether that was acceptable behaviour.
func TestSetChannelRouting_SiblingInstanceIsUntouched(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	addAgentsToAPI(t, api, []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	})
	writeTestWorkspaceJSON(t, api, "emea", "active", []string{"mia", "ray"}, false)
	seedChannelInstance(t, api, "whatsapp.eu")
	seedChannelInstance(t, api, "whatsapp.us")

	eu := seedExistingChannelSession(t, api, "whatsapp.eu", "peer-eu", "mia", "")
	us := seedExistingChannelSession(t, api, "whatsapp.us", "peer-us", "ray", "americas")

	w := setChannelRoutingReq(t, api, "whatsapp.eu",
		`{"workspace_id":"emea","default_agent_id":"mia"}`)
	require.Equal(t, http.StatusOK, w.Code, "bind must succeed: %s", w.Body.String())

	store := api.agentLoop.GetSessionStore()

	afterEU, err := store.GetMeta(eu.ID)
	require.NoError(t, err)
	assert.Equal(t, "emea", afterEU.WorkspaceID, "the bound instance's own session must be restamped")

	afterUS, err := store.GetMeta(us.ID)
	require.NoError(t, err)
	assert.Equal(t, "americas", afterUS.WorkspaceID,
		"a SIBLING instance of the same channel type must be left completely alone — "+
			"matching on the bare type would have moved this conversation to the wrong workspace")
}
