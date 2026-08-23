// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// media_workspace_id_paths_test.go — FIX 1 (re-review) regression coverage
// for the four processOptions{} construction sites that still left
// WorkspaceID unset after the first fix wave only covered processMessage
// (media_workspace_id_test.go): ProcessScheduled, processSystemMessage,
// spawnSubTurn, and continueWithSteeringMessages. A fifth site,
// processTaskDirect, is also covered here even though it was already "moot"
// in practice (webchatChannel.SendMedia ignores WorkspaceID) — the fix still
// makes ts.opts.WorkspaceID itself correct for every other consumer.
//
// Each test drives the real production code path (no direct opts
// inspection) and asserts the resulting bus.OutboundMediaMessage.WorkspaceID
// carries the expected workspace — the same "did the byte actually travel
// end-to-end" style FIX 1's original regression test used.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newMediaWorkspaceIDTestLoop builds a minimal AgentLoop (single default
// agent, handledMediaProvider driving a scripted handled_media_tool call)
// plus a media store and a channel manager with a fakeMediaChannel
// registered under channelName, so tests only need to assert on
// sentMedia[0].WorkspaceID. Mirrors media_workspace_id_test.go's setup.
// newMediaWorkspaceIDTestLoopOnly is newMediaWorkspaceIDTestLoop for tests that
// need only the loop — it keeps the call site free of a run of blank
// identifiers (dogsled).
func newMediaWorkspaceIDTestLoopOnly(t *testing.T, channelName string) *AgentLoop {
	t.Helper()
	al, _, _, _ := newMediaWorkspaceIDTestLoop(t, channelName) //nolint:dogsled // single funnel point
	return al
}

func newMediaWorkspaceIDTestLoop(
	t *testing.T, channelName string,
) (al *AgentLoop, defaultAgent *AgentInstance, ch *fakeMediaChannel, imagePath string) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	provider := &handledMediaProvider{}
	al = mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	store := media.NewFileMediaStore()
	al.SetMediaStore(store)
	ch = &fakeMediaChannel{fakeChannel: fakeChannel{id: "rid-" + channelName}}
	al.SetChannelManager(newStartedTestChannelManager(t, msgBus, store, channelName, ch))

	imagePath = filepath.Join(tmpDir, "screen-"+channelName+".png")
	require.NoError(t, os.WriteFile(imagePath, []byte("fake screenshot"), 0o644))

	defaultAgent = al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "expected default agent")
	defaultAgent.Tools.Register(&handledMediaTool{store: store, path: imagePath})
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"handled_media_tool": "allow"},
	})

	return al, defaultAgent, ch, imagePath
}

// TestProcessScheduled_MediaToolDelivery_StampsWorkspaceID is the
// ProcessScheduled regression: WorkspaceID is resolved from the scheduled
// session's own meta (the mechanism ProcessScheduled now shares with
// processMessage) and must reach the delivered media.
func TestProcessScheduled_MediaToolDelivery_StampsWorkspaceID(t *testing.T) {
	al, defaultAgent, telegramChannel, _ := newMediaWorkspaceIDTestLoop(t, "telegram")

	meta, err := al.GetSessionStore().NewScheduledSession(defaultAgent.ID)
	require.NoError(t, err)
	ws := "sales"
	require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &ws}))

	reply, err := al.ProcessScheduled(
		context.Background(), defaultAgent.ID, meta.ID,
		"take a screenshot of the screen and send it to me", "telegram", "chat1",
	)
	require.NoError(t, err)
	assert.Equal(t, "Here is the screenshot.", reply)

	require.Len(t, telegramChannel.sentMedia, 1,
		"expected exactly 1 synchronously sent media message")
	assert.Equal(t, "sales", telegramChannel.sentMedia[0].WorkspaceID,
		"a scheduled run's tool media must carry the workspace stamped on its own session meta")
}

// TestProcessScheduled_MediaToolDelivery_NoSessionWorkspace_EmptyWorkspaceID
// is the negative companion: a scheduled session with no WorkspaceID stamped
// on its meta (today's default — nothing currently binds a schedule to a
// workspace) must still deliver media with an empty WorkspaceID, never a
// guessed one.
func TestProcessScheduled_MediaToolDelivery_NoSessionWorkspace_EmptyWorkspaceID(t *testing.T) {
	al, defaultAgent, telegramChannel, _ := newMediaWorkspaceIDTestLoop(t, "telegram")

	meta, err := al.GetSessionStore().NewScheduledSession(defaultAgent.ID)
	require.NoError(t, err)

	reply, err := al.ProcessScheduled(
		context.Background(), defaultAgent.ID, meta.ID,
		"take a screenshot of the screen and send it to me", "telegram", "chat1",
	)
	require.NoError(t, err)
	assert.Equal(t, "Here is the screenshot.", reply)

	require.Len(t, telegramChannel.sentMedia, 1)
	assert.Equal(t, "", telegramChannel.sentMedia[0].WorkspaceID)
}

// TestProcessSystemMessage_MediaToolDelivery_StampsWorkspaceID is the
// processSystemMessage regression: a reconstructed delegate-completion /
// async-notify turn (msg.Channel == "system") resolves WorkspaceID from the
// SAME session its output persists into (AsyncTranscriptSessionID), mirroring
// processMessage's own resolution.
func TestProcessSystemMessage_MediaToolDelivery_StampsWorkspaceID(t *testing.T) {
	al, defaultAgent, telegramChannel, _ := newMediaWorkspaceIDTestLoop(t, "telegram")

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	ws := "sales"
	require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &ws}))

	resp, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel: "system",
		// originChannel/originChatID parse from ChatID as "channel:chat_id".
		ChatID: "telegram:chat1",
		Sender: bus.SenderInfo{CanonicalID: "delegate"},
		Content: "Task 'research' completed.\n\nResult:\n" +
			"take a screenshot of the screen and send it to me",
		AsyncOriginAgentID:       defaultAgent.ID,
		AsyncTranscriptSessionID: meta.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, "Here is the screenshot.", resp)

	require.Len(t, telegramChannel.sentMedia, 1,
		"expected exactly 1 synchronously sent media message")
	assert.Equal(t, "sales", telegramChannel.sentMedia[0].WorkspaceID,
		"a reconstructed system-message turn's tool media must carry the workspace "+
			"stamped on the origin session it persists into")
}

// TestSpawnSubTurn_MediaToolDelivery_InheritsParentWorkspaceID is the
// spawnSubTurn regression: a delegated child turn (self-delegation — no
// TargetAgentID, execSource == the parent's own agent) inherits the PARENT
// turn's WorkspaceID, the same session/room-context field Channel/ChatID/
// SenderID/TranscriptSessionID/TranscriptStore already inherit from
// parentTS — NOT the target's own agent-identity fields ADR-032 protects.
func TestSpawnSubTurn_MediaToolDelivery_InheritsParentWorkspaceID(t *testing.T) {
	al, defaultAgent, telegramChannel, _ := newMediaWorkspaceIDTestLoop(t, "telegram")

	// spawnSubTurn's real production path mints the child via
	// al.GetSessionStore().CreateSessionWithID(childID,
	// parentTS.transcriptSessionID, ...) — FR-082 requires reading the
	// PARENT's own meta.json to copy its Owner field, so the parent must be
	// a REAL, store-backed session, not a bare/zero-value id (see the
	// identical fix and rationale in admission_adr057_test.go). Leaving
	// transcriptSessionID unset here is a stale test-harness shape, not a
	// production one.
	sharedStore := al.GetSessionStore()
	require.NotNil(t, sharedStore, "AgentLoop has no shared session store")
	parentMeta, err := sharedStore.NewSession(session.SessionTypeChannel, "telegram", defaultAgent.ID)
	require.NoError(t, err, "mint a real parent session")

	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-1",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, 4),
		session:             &ephemeralSessionStore{},
		agent:               defaultAgent,
		agentID:             defaultAgent.ID,
		channel:             "telegram",
		chatID:              "chat1",
		transcriptSessionID: parentMeta.ID,
		routingSessionID:    session.RoutingSessionID(parentMeta.ID),
		opts:                processOptions{WorkspaceID: "sales"},
	}

	ctx := withSpawnToolCallID(context.Background(), "call_research1")
	result, err := spawnSubTurn(ctx, al, parentTS, SubTurnConfig{
		SystemPrompt: "take a screenshot of the screen and send it to me",
		Model:        defaultAgent.Model,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, telegramChannel.sentMedia, 1,
		"expected exactly 1 synchronously sent media message from the child turn")
	assert.Equal(t, "sales", telegramChannel.sentMedia[0].WorkspaceID,
		"a delegated child turn's tool media must carry the PARENT turn's workspace")
}

// TestSpawnSubTurn_MediaToolDelivery_UnboundParent_EmptyWorkspaceID is the
// negative companion: a parent turn with no WorkspaceID must not leak a
// guessed workspace onto its delegated child's media.
func TestSpawnSubTurn_MediaToolDelivery_UnboundParent_EmptyWorkspaceID(t *testing.T) {
	al, defaultAgent, telegramChannel, _ := newMediaWorkspaceIDTestLoop(t, "telegram")

	// See TestSpawnSubTurn_MediaToolDelivery_InheritsParentWorkspaceID's
	// comment: the parent must be a REAL, store-backed session for
	// spawnSubTurn's FR-082 parent-Owner read to succeed.
	sharedStore := al.GetSessionStore()
	require.NotNil(t, sharedStore, "AgentLoop has no shared session store")
	parentMeta, err := sharedStore.NewSession(session.SessionTypeChannel, "telegram", defaultAgent.ID)
	require.NoError(t, err, "mint a real parent session")

	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-1",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, 4),
		session:             &ephemeralSessionStore{},
		agent:               defaultAgent,
		agentID:             defaultAgent.ID,
		channel:             "telegram",
		chatID:              "chat1",
		transcriptSessionID: parentMeta.ID,
		routingSessionID:    session.RoutingSessionID(parentMeta.ID),
		// opts.WorkspaceID left unset.
	}

	ctx := withSpawnToolCallID(context.Background(), "call_research2")
	result, err := spawnSubTurn(ctx, al, parentTS, SubTurnConfig{
		SystemPrompt: "take a screenshot of the screen and send it to me",
		Model:        defaultAgent.Model,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, telegramChannel.sentMedia, 1)
	assert.Equal(t, "", telegramChannel.sentMedia[0].WorkspaceID)
}

// TestContinueWithSteeringMessages_MediaToolDelivery_StampsWorkspaceID is
// the continueWithSteeringMessages regression: the new workspaceID parameter
// (threaded by Continue from AgentLoop.resolveWorkspaceIDForContinuation)
// reaches the continued turn's tool media.
func TestContinueWithSteeringMessages_MediaToolDelivery_StampsWorkspaceID(t *testing.T) {
	al, defaultAgent, telegramChannel, _ := newMediaWorkspaceIDTestLoop(t, "telegram")

	resp, err := al.continueWithSteeringMessages(
		context.Background(), defaultAgent,
		"agent:"+defaultAgent.ID+":main", "telegram", "chat1", "sales",
		[]providers.Message{{Role: "user", Content: "please continue"}},
	)
	require.NoError(t, err)
	assert.Equal(t, "Here is the screenshot.", resp)

	require.Len(t, telegramChannel.sentMedia, 1)
	assert.Equal(t, "sales", telegramChannel.sentMedia[0].WorkspaceID,
		"a steering-continued turn's tool media must carry the resolved workspace")
}

// TestBuildContinuationTarget_ResolvesWorkspaceID_FromSessionMeta proves the
// PRIMARY resolution mechanism resolveWorkspaceIDForContinuation shares with
// processMessage: when the inbound message already carries a SessionID
// (always true for webchat), the session's own meta.WorkspaceID wins.
func TestBuildContinuationTarget_ResolvesWorkspaceID_FromSessionMeta(t *testing.T) {
	al := newMediaWorkspaceIDTestLoopOnly(t, "telegram")

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	ws := "sales"
	require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &ws}))

	target, err := al.buildContinuationTarget(bus.InboundMessage{
		Channel:   "telegram",
		ChatID:    "chat1",
		SessionID: meta.ID,
		Sender:    bus.SenderInfo{CanonicalID: "user1"},
	})
	require.NoError(t, err)
	require.NotNil(t, target)
	assert.Equal(t, "sales", target.WorkspaceID)
}

// TestBuildContinuationTarget_ResolvesWorkspaceID_FromChannelBinding proves
// the FALLBACK resolution mechanism: when the message has no SessionID (the
// common case for a channel message session_worker's own msg copy never saw
// mutated — see resolveWorkspaceIDForContinuation's doc comment), the bound
// channel instance's own configured WorkspaceID is used instead — the exact
// value resolveOrCreateChannelSession would have seeded a new session with.
func TestBuildContinuationTarget_ResolvesWorkspaceID_FromChannelBinding(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram.sales": {
				Type:        "telegram",
				Enabled:     true,
				WorkspaceID: "sales",
			},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	target, err := al.buildContinuationTarget(bus.InboundMessage{
		Channel:    "telegram",
		InstanceID: "telegram.sales",
		ChatID:     "chat1",
		Sender:     bus.SenderInfo{CanonicalID: "user1"},
		// No SessionID — the session_worker mutation-visibility gap case.
	})
	require.NoError(t, err)
	require.NotNil(t, target)
	assert.Equal(t, "sales", target.WorkspaceID)
}

// TestProcessTaskDirect_MediaToolDelivery_StampsWorkspaceID is the
// processTaskDirect (native) regression: WorkspaceID is read back from
// tools.ToolWorkspaceID(ctx) — the same context value processTaskDirectExternalCLI
// already reads a few lines below in production, seeded by the task executor
// before calling processTaskDirect. Channel is hardcoded to "webchat" so the
// fake channel is registered under that name.
func TestProcessTaskDirect_MediaToolDelivery_StampsWorkspaceID(t *testing.T) {
	al, defaultAgent, webchatChannel, _ := newMediaWorkspaceIDTestLoop(t, "webchat")

	ctx := tools.WithWorkspaceID(context.Background(), "sales")
	result, err := al.processTaskDirect(
		ctx, defaultAgent.ID, "take a screenshot of the screen and send it to me",
		"task-sess-1", "task:1",
	)
	require.NoError(t, err)
	assert.Equal(t, "Here is the screenshot.", result)

	require.Len(t, webchatChannel.sentMedia, 1)
	assert.Equal(t, "sales", webchatChannel.sentMedia[0].WorkspaceID)
}
