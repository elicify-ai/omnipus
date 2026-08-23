package agent

// session_workspace_id_test.go — ADR-029 TDD #20: session inherits workspace_id.
//
// F-5 / US-9 AC-1: when a bound channel instance creates a session, the session's
// workspace_id MUST equal the instance's WorkspaceID.
//
// The implementation path is:
//   processMessage → resolveOrCreateChannelSession(…, sessionWorkspaceID)
//     → store.NewChannelSession(…)
//     → store.SetMeta(id, MetaPatch{WorkspaceID: &workspaceID})
//
// We drive the full processMessage path (same as TestDriftDrop_SingleEmission)
// and then read the created session's meta to assert workspace_id.
//
// Traces to: channel-instance-workspace-binding-spec.md §7 TDD #20,
//            FR-022, US-9 AC-1, BDD "Session inherits the instance workspace".

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestSessionCreation_InheritsWorkspaceID verifies TDD #20 / FR-022:
// a session created by a bound instance has workspace_id == the instance's WorkspaceID.
//
// BDD: Given instance "whatsapp.eu" bound to workspace "sales" agent "ray",
// When an inbound creates a session via processMessage,
// Then the session's workspace_id == "sales".
func TestSessionCreation_InheritsWorkspaceID(t *testing.T) {
	// Build a temp home directory two levels deep (parent = sessions directory).
	tmpRoot := t.TempDir()
	workspace := filepath.Join(tmpRoot, "home", "workspace")
	require.NoError(t, os.MkdirAll(workspace, 0o700))
	t.Setenv("OMNIPUS_HOME", tmpRoot)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = workspace
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	}
	// Instance "whatsapp.eu" is bound to workspace "sales" with agent "ray".
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"whatsapp.eu": {
			Type:        "whatsapp",
			Enabled:     true,
			WorkspaceID: "sales",
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		},
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	registerInstance(t, al, cfg, tmpRoot, "mia", config.AgentTypeCustom)
	registerInstance(t, al, cfg, tmpRoot, "ray", config.AgentTypeCustom)

	require.NotNil(t, al.sharedSessionStore,
		"sharedSessionStore must be initialized — processMessage requires it to create channel sessions")

	// Precondition: store is empty before the inbound.
	sessions, err := al.sharedSessionStore.ListSessions()
	require.NoError(t, err)
	require.Empty(t, sessions, "precondition: shared session store must be empty")

	// Drive the full path.
	msg := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.eu",
		ChatID:     "15555550100@s.whatsapp.net",
		Content:    "hello from the sales workspace",
		Sender: bus.SenderInfo{
			DisplayName: "Alice",
		},
	}
	_, _, processErr := al.processMessage(context.Background(), msg)
	require.NoError(t, processErr, "processMessage must succeed for a valid bound inbound")

	// Retrieve the created session.
	sessions, err = al.sharedSessionStore.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1, "processMessage must create exactly 1 channel session")

	sessionID := sessions[0].ID
	meta, err := al.sharedSessionStore.GetMeta(sessionID)
	require.NoError(t, err)
	require.NotNil(t, meta)

	// Assert workspace_id == "sales" (the instance's WorkspaceID).
	assert.Equal(t, "sales", meta.WorkspaceID,
		"session workspace_id must equal the bound instance's WorkspaceID 'sales'")
	// Also confirm the session is linked to the correct channel and peer.
	assert.Equal(t, "whatsapp", meta.Channel,
		"session channel must be 'whatsapp'")
	assert.Equal(t, "15555550100@s.whatsapp.net", meta.PeerID,
		"session PeerID must match the ChatID from the message")
}

// TestSessionCreation_UnboundInstance_NoWorkspaceID verifies the unbound case:
// a session created by an unbound instance (no WorkspaceID in channel config)
// must have an empty workspace_id — the stamping must not leak workspace IDs
// from other instances.
//
// Traces to: channel-instance-workspace-binding-spec.md US-9 / regression guard.
func TestSessionCreation_UnboundInstance_NoWorkspaceID(t *testing.T) {
	tmpRoot := t.TempDir()
	workspace := filepath.Join(tmpRoot, "home", "workspace")
	require.NoError(t, os.MkdirAll(workspace, 0o700))
	t.Setenv("OMNIPUS_HOME", tmpRoot)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = workspace
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
	}
	// No bound instance — bare telegram (unbound, no WorkspaceID).
	cfg.Channels = map[string]config.ChannelInstanceConfig{}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	registerInstance(t, al, cfg, tmpRoot, "mia", config.AgentTypeCustom)

	require.NotNil(t, al.sharedSessionStore)

	msg := bus.InboundMessage{
		Channel: "telegram",
		ChatID:  "user999",
		Content: "hi",
		Sender:  bus.SenderInfo{DisplayName: "Bob"},
	}
	_, _, err := al.processMessage(context.Background(), msg)
	require.NoError(t, err)

	sessions, err := al.sharedSessionStore.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1, "processMessage must create exactly 1 channel session")

	meta, err := al.sharedSessionStore.GetMeta(sessions[0].ID)
	require.NoError(t, err)
	require.NotNil(t, meta)

	assert.Equal(t, "", meta.WorkspaceID,
		"unbound instance session must have empty workspace_id — no workspace was stamped")
}

// TestSessionCreation_TwoInstancesDifferentWorkspaces verifies US-9 AC-2:
// two same-type instances in different workspaces produce sessions with distinct
// workspace_ids and distinct session keys.
//
// Traces to: channel-instance-workspace-binding-spec.md US-9 AC-2,
//
//	BDD "Session inherits the instance workspace" (key-collision sub-check).
func TestSessionCreation_TwoInstancesDifferentWorkspaces(t *testing.T) {
	tmpRoot := t.TempDir()
	workspace := filepath.Join(tmpRoot, "home", "workspace")
	require.NoError(t, os.MkdirAll(workspace, 0o700))
	t.Setenv("OMNIPUS_HOME", tmpRoot)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = workspace
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	}
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"whatsapp.eu": {
			Type:        "whatsapp",
			Enabled:     true,
			WorkspaceID: "sales",
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		},
		"whatsapp.us": {
			Type:        "whatsapp",
			Enabled:     true,
			WorkspaceID: "ops",
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: "mia"},
		},
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	registerInstance(t, al, cfg, tmpRoot, "mia", config.AgentTypeCustom)
	registerInstance(t, al, cfg, tmpRoot, "ray", config.AgentTypeCustom)

	require.NotNil(t, al.sharedSessionStore)

	// Same ChatID on both instances — they must produce distinct sessions.
	const sharedChatID = "15555550100@s.whatsapp.net"

	msgEU := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.eu",
		ChatID:     sharedChatID,
		Content:    "message for sales",
	}
	msgUS := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.us",
		ChatID:     sharedChatID,
		Content:    "message for ops",
	}

	_, _, err := al.processMessage(context.Background(), msgEU)
	require.NoError(t, err, "processMessage for whatsapp.eu must succeed")
	_, _, err = al.processMessage(context.Background(), msgUS)
	require.NoError(t, err, "processMessage for whatsapp.us must succeed")

	sessions, err := al.sharedSessionStore.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 2,
		"two distinct instances with the same ChatID must produce two distinct sessions (no key collision)")

	// Map by PeerID+Channel to find both sessions.
	wsIDs := make(map[string]string) // instanceID → workspace_id
	for _, s := range sessions {
		meta, mErr := al.sharedSessionStore.GetMeta(s.ID)
		require.NoError(t, mErr)
		require.NotNil(t, meta)
		wsIDs[meta.WorkspaceID] = s.ID
	}

	assert.Contains(t, wsIDs, "sales", "one session must have workspace_id='sales' (whatsapp.eu)")
	assert.Contains(t, wsIDs, "ops", "one session must have workspace_id='ops' (whatsapp.us)")

	// The two session IDs must be distinct.
	assert.NotEqual(t, wsIDs["sales"], wsIDs["ops"],
		"sessions for different instances must have distinct IDs")
}
