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
// kickoff contract: the transcript entry is system-role (not a user bubble),
// the workspace's setup_pending flag is cleared on disk, the session gets the
// fixed "Workspace setup" title, and the message is still published to the
// bus so Ava's greeting streams normally.
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
			"the kickoff instruction content must still drive the turn so Ava's reply streams normally")
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
	assert.Equal(t, kickoffContent, entry.Content)

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

// TestHandleChatMessage_WorkspaceSetupKickoff_UnknownWorkspace_Demotes proves
// that a kickoff flag with no resolvable workspace_id (absent or unknown) is
// demoted to a normal message: the transcript entry is Role "user" (not
// "system"), and the setup flow is never invoked.
func TestHandleChatMessage_WorkspaceSetupKickoff_UnknownWorkspace_Demotes(t *testing.T) {
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

	var sessionID string
	select {
	case msg := <-msgBus.InboundChan():
		sessionID = msg.SessionID
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage — a demoted kickoff must still publish normally")
	}
	require.NotEmpty(t, sessionID)

	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store)
	transcript, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, transcript, 1)
	assert.Equal(t, "user", transcript[0].Role,
		"a kickoff with no resolvable workspace_id must be demoted to a normal user-role entry")
	assert.NotEqual(t, session.EntryTypeSystem, transcript[0].Type)

	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, content, meta.Title,
		"a demoted kickoff must derive the session title from content, not use the fixed kickoff title")
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
