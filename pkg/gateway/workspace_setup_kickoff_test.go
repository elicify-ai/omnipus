//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// workspace_setup_kickoff_test.go — unit tests for the workspace-setup kickoff
// turn (Unit B). The SPA sends a normal `message` frame whose metadata carries
// `workspace_id` plus `workspace_setup_kickoff: true` on a workspace's first
// open. handleChatMessage must: record the trigger as a system-role transcript
// entry (not a user bubble), clear the workspace's setup_pending flag exactly
// once (idempotency guard), give the session a fixed "Workspace setup" title,
// and otherwise run the turn normally so Ava's greeting streams live.
//
// See contracts/asyncapi.yaml metadata.workspace_setup_kickoff and
// pkg/workspace/workspace.go Workspace.SetupPending.

package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// writeSetupKickoffWorkspaceRecord writes a minimal on-disk workspace record
// carrying setup_pending, so workspace.Exists / readWorkspaceFile observe it
// under home.
func writeSetupKickoffWorkspaceRecord(t *testing.T, home, id string, setupPending bool) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	rec := map[string]any{
		"id":            id,
		"is_default":    false,
		"setup_pending": setupPending,
		"created_at":    time.Now().UTC().Format(time.RFC3339),
		"updated_at":    time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), data, 0o644))
}

// drainErrorFrame drains wc.sendCh (non-blocking) and returns the first
// error-type frame found, or nil if none arrived.
func drainErrorFrame(wc *wsConn) *replayFrameDecoder {
	for {
		select {
		case raw := <-wc.sendCh:
			var f replayFrameDecoder
			if err := json.Unmarshal(raw, &f); err != nil {
				continue
			}
			if f.Type == string(generated.WsFrameTypeError) {
				fCopy := f
				return &fCopy
			}
		default:
			return nil
		}
	}
}

// TestHandleChatMessage_WorkspaceSetupKickoff_HappyPath proves the full
// kickoff contract: the transcript entry is system-role (not a user bubble)
// with NEUTRAL fixed content (Fix 4 — never the client-supplied instruction
// verbatim), the workspace's setup_pending flag is cleared on disk, the
// session gets the fixed "Workspace setup" title, and the FULL instruction is
// still published to the bus so Ava's greeting streams normally and is driven
// by the real interview prompt.
func TestHandleChatMessage_WorkspaceSetupKickoff_HappyPath(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home

	const wsID = "01JXWORKSPACEKICKOFF0000001"
	writeSetupKickoffWorkspaceRecord(t, home, wsID, true)

	wc := makeTestConn()
	const kickoffContent = "The workspace was just created — introduce yourself and ask about its purpose."
	handler.handleChatMessage(
		context.Background(),
		"chat-kickoff-happy",
		"",             // frameSessionID (empty → mint a new session)
		kickoffContent, // content
		"ava",          // agentID
		nil,            // mediaRefs
		"",             // modelName
		wsID,           // workspaceID
		true,           // setupKickoff
		wc,
	)

	var sessionID string
	select {
	case msg := <-msgBus.InboundChan():
		sessionID = msg.SessionID
		assert.Equal(t, kickoffContent, msg.Content,
			"the FULL kickoff instruction must still drive the turn so Ava's reply streams normally — "+
				"only the persisted transcript entry is neutralized (Fix 4)")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage — the kickoff turn must still be published")
	}
	require.NotEmpty(t, sessionID, "handleChatMessage must mint a session for the kickoff turn")

	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store)

	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, "Workspace setup", meta.Title,
		"the kickoff instruction text must not leak into the sidebar as the session title")

	transcript, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, transcript, 1, "exactly one transcript entry must be written for the kickoff trigger")
	entry := transcript[0]
	assert.Equal(t, "system", entry.Role,
		"the kickoff trigger must be recorded as a system-role entry, not a user bubble")
	assert.Equal(t, session.EntryTypeSystem, entry.Type,
		"the kickoff trigger must be typed EntryTypeSystem")
	assert.Equal(t, "ava", entry.AgentID,
		"AgentID must stay the target agent (Ava) so replay/hydration attributes context to her")
	assert.Equal(t, "Workspace setup started.", entry.Content,
		"Fix 4: the PERSISTED/REPLAYED entry must carry neutral fixed content, never the "+
			"client-supplied kickoff instruction verbatim")

	w, err := readWorkspaceFile(home, wsID)
	require.NoError(t, err)
	assert.False(t, w.SetupPending,
		"setup_pending must be cleared on disk exactly once the kickoff turn is accepted")
}

// TestHandleChatMessage_WorkspaceSetupKickoff_Duplicate proves the
// idempotency guard: a second kickoff against a workspace whose setup_pending
// is already false (the first kickoff already ran, e.g. a second tab racing
// the first open) is rejected with an error frame — no new session minted,
// nothing published to the bus.
func TestHandleChatMessage_WorkspaceSetupKickoff_Duplicate(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home

	const wsID = "01JXWORKSPACEKICKOFF0000002"
	// setup_pending already false: the kickoff already ran once.
	writeSetupKickoffWorkspaceRecord(t, home, wsID, false)

	wc := makeTestConn()
	handler.handleChatMessage(
		context.Background(),
		"chat-kickoff-dup",
		"",
		"introduce yourself",
		"ava",
		nil,
		"",
		wsID,
		true, // setupKickoff
		wc,
	)

	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("a duplicate kickoff must NOT publish to the bus, got: %+v", msg)
	case <-time.After(200 * time.Millisecond):
		// expected: nothing published
	}

	errFrame := drainErrorFrame(wc)
	require.NotNil(t, errFrame, "a duplicate kickoff must send an error frame")
	assert.Contains(t, errFrame.Message, "already run")

	// No session should have been minted for this chatID.
	assertNoSessionMinted(t, handler, "chat-kickoff-dup")

	// The workspace file must be untouched (still setup_pending=false).
	w, err := readWorkspaceFile(home, wsID)
	require.NoError(t, err)
	assert.False(t, w.SetupPending)
}

// assertNoSessionMinted asserts that no session_id was tracked for chatID,
// i.e. no session was minted by a rejected duplicate-kickoff call.
func assertNoSessionMinted(t *testing.T, handler *WSHandler, chatID string) {
	t.Helper()
	handler.mu.Lock()
	defer handler.mu.Unlock()
	assert.Empty(t, handler.sessionIDs[chatID], "a duplicate/rejected kickoff must not mint or track a session")
}

// TestHandleChatMessage_WorkspaceSetupKickoff_UnknownWorkspace_Rejects proves
// (Fix 3) that a kickoff flag with no resolvable workspace_id (absent or
// unknown) is REJECTED outright — an error frame is sent, no session is
// minted, no transcript entry is written, and nothing is published to the
// bus. This replaces the earlier "demote to a normal message" behavior, which
// used to persist the synthetic kickoff instruction as a fake user-authored
// transcript entry and session title.
func TestHandleChatMessage_WorkspaceSetupKickoff_UnknownWorkspace_Rejects(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home
	// No workspace file written at all — workspaceID resolves to unknown.

	wc := makeTestConn()
	const content = "hello"
	handler.handleChatMessage(
		context.Background(),
		"chat-kickoff-unknown",
		"",
		content,
		"ava",
		nil,
		"",
		"01JXWORKSPACEDOESNOTEXIST001", // workspaceID (does not exist on disk)
		true,                           // setupKickoff
		wc,
	)

	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("a rejected kickoff must NOT publish to the bus, got: %+v", msg)
	case <-time.After(200 * time.Millisecond):
		// expected: nothing published
	}

	errFrame := drainErrorFrame(wc)
	require.NotNil(t, errFrame, "an unresolvable workspace_id kickoff must send an error frame")
	assert.Contains(t, errFrame.Message, "unknown workspace")

	assertNoSessionMinted(t, handler, "chat-kickoff-unknown")
}

// TestHandleChatMessage_WorkspaceSetupKickoff_NoStore_Rejects proves Fix 7:
// a kickoff-flagged frame that resolves to no usable session store (the
// store==nil degenerate path) is rejected the same way as every other
// kickoff-cannot-complete case, instead of silently falling through and
// publishing the raw kickoff instruction as an ordinary message.
//
// store==nil requires BOTH GetSessionStore() (the shared store) and
// GetAgentStore(targetAgentID) to return nil. The shared store is forced nil
// by pre-creating a regular FILE at $home/sessions before boot, so the agent
// loop's os.MkdirAll(homePath/"sessions") fails and shared-store init is
// skipped (see pkg/agent/loop.go's "Shared session store unavailable" path).
// GetAgentStore is forced nil by addressing an agentID that is never
// registered (NewAgentLoop auto-registers a "main" default agent even with an
// empty Agents.List, so an EMPTY agentID would resolve to that default and
// its own per-agent store — an explicit, unknown agentID sidesteps that
// default-resolution path entirely).
func TestHandleChatMessage_WorkspaceSetupKickoff_NoStore_Rejects(t *testing.T) {
	base := t.TempDir()
	workspaceDir := filepath.Join(base, "agentws")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	// Block shared-session-store init: "sessions" must fail to mkdir because a
	// regular file already occupies that path.
	require.NoError(t, os.WriteFile(filepath.Join(base, "sessions"), []byte("blocker"), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: workspaceDir,
				ModelName: "test-default-model",
				MaxTokens: 4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	require.Nil(t, al.GetSessionStore(), "precondition: shared session store init must have failed")

	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)
	handler.home = base

	const wsID = "01JXWORKSPACEKICKOFF0000009"
	writeSetupKickoffWorkspaceRecord(t, base, wsID, true)

	wc := makeTestConn()
	handler.handleChatMessage(
		context.Background(),
		"chat-kickoff-nostore",
		"",
		"introduce yourself",
		"totally-unregistered-agent-id", // agentID: never registered → GetAgentStore returns nil too
		nil,
		"",
		wsID,
		true, // setupKickoff
		wc,
	)

	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("a kickoff with no session store must NOT publish to the bus, got: %+v", msg)
	case <-time.After(200 * time.Millisecond):
		// expected: nothing published
	}

	errFrame := drainErrorFrame(wc)
	require.NotNil(t, errFrame, "a kickoff with no session store must send an error frame")

	// The workspace's setup_pending must be untouched — this path never even
	// reaches consumeWorkspaceSetupKickoff.
	w, err := readWorkspaceFile(base, wsID)
	require.NoError(t, err)
	assert.True(t, w.SetupPending, "setup_pending must be untouched when the kickoff never reaches the consume step")
}

// TestRestoreWorkspaceSetupPending_RestoresClearedFlag proves the Fix 2
// compensation helper directly: given a workspace whose setup_pending was
// just cleared by a successful consume, restoreWorkspaceSetupPending sets it
// back to true and persists it. This is the fallback the kickoff downstream
// failure paths (NewSession, existing-session GetMeta, PublishInbound) call
// when a genuine forced failure at those exact seams is not practical to
// construct in a unit test (see TestHandleChatMessage_WorkspaceSetupKickoff_PublishFailure_RestoresFlag
// below for one seam — bus.Close — that IS forceable end-to-end).
func TestRestoreWorkspaceSetupPending_RestoresClearedFlag(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home

	const wsID = "01JXWORKSPACEKICKOFF0000010"
	// Simulate the post-consume state: setup_pending already cleared.
	writeSetupKickoffWorkspaceRecord(t, home, wsID, false)

	handler.restoreWorkspaceSetupPending(wsID)

	w, err := readWorkspaceFile(home, wsID)
	require.NoError(t, err)
	assert.True(t, w.SetupPending, "restoreWorkspaceSetupPending must set setup_pending back to true")
}

// TestRestoreWorkspaceSetupPending_MissingWorkspace_NoPanic proves the
// best-effort contract: restoring against a workspace that no longer exists
// on disk (e.g. deleted concurrently) logs and returns without panicking or
// recreating the file.
func TestRestoreWorkspaceSetupPending_MissingWorkspace_NoPanic(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home

	require.NotPanics(t, func() {
		handler.restoreWorkspaceSetupPending("01JXWORKSPACEDOESNOTEXIST002")
	})

	_, err := readWorkspaceFile(home, "01JXWORKSPACEDOESNOTEXIST002")
	assert.Error(t, err, "a missing workspace must stay missing — restore must never recreate the file")
}

// TestHandleChatMessage_WorkspaceSetupKickoff_PublishFailure_RestoresFlag
// proves Fix 2 end-to-end at the one downstream seam that IS forceable in a
// unit test: closing the message bus before the kickoff turn forces
// PublishInbound to fail with bus.ErrBusClosed. The successful consume must
// then be compensated — setup_pending restored to true — even though a
// session was already minted and a transcript entry already written.
func TestHandleChatMessage_WorkspaceSetupKickoff_PublishFailure_RestoresFlag(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home

	const wsID = "01JXWORKSPACEKICKOFF0000011"
	writeSetupKickoffWorkspaceRecord(t, home, wsID, true)

	// Force PublishInbound to fail: closing the bus makes every subsequent
	// publish return bus.ErrBusClosed.
	msgBus.Close()

	wc := makeTestConn()
	handler.handleChatMessage(
		context.Background(),
		"chat-kickoff-publishfail",
		"",
		"introduce yourself and ask about its purpose",
		"ava",
		nil,
		"",
		wsID,
		true, // setupKickoff
		wc,
	)

	errFrame := drainErrorFrame(wc)
	require.NotNil(t, errFrame, "a publish failure must still surface an error frame to the client")
	assert.Contains(t, errFrame.Message, "failed to deliver message")

	w, err := readWorkspaceFile(home, wsID)
	require.NoError(t, err)
	assert.True(t, w.SetupPending,
		"Fix 2: a successful consume followed by a publish failure must restore setup_pending to true "+
			"so the one-time interview is not permanently lost")
}

// TestHandleChatMessage_NormalMessage_RegressionUnaffected proves a normal
// message frame (setupKickoff=false) behaves exactly as before the kickoff
// feature landed: Role "user", content-derived title.
func TestHandleChatMessage_NormalMessage_RegressionUnaffected(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	const content = "just a normal chat message"
	handler.handleChatMessage(
		context.Background(),
		"chat-normal-regression",
		"",
		content,
		"",
		nil,
		"",
		"",    // workspaceID
		false, // setupKickoff
		wc,
	)

	var sessionID string
	select {
	case msg := <-msgBus.InboundChan():
		sessionID = msg.SessionID
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage")
	}
	require.NotEmpty(t, sessionID)

	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store)
	transcript, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, transcript, 1)
	assert.Equal(t, "user", transcript[0].Role)
	assert.Empty(t, transcript[0].Type, "a normal message entry has empty Type (EntryTypeMessage default)")

	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, content, meta.Title, "a normal message must keep the content-derived title")
}
