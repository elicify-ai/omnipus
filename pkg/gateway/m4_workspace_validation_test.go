//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

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
)

// writeWorkspaceRecord writes a minimal on-disk workspace record so
// workspace.Exists / ResolveDefaultID observe it under home.
func writeWorkspaceRecord(t *testing.T, home, id string, isDefault bool) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	rec := map[string]any{"id": id, "is_default": isDefault}
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), data, 0o644))
}

// mintedSessionMetaWorkspace drives handleChatMessage for a fresh session and
// returns the WorkspaceID stamped on the minted session's meta.
func mintedSessionMetaWorkspace(
	t *testing.T,
	handler *WSHandler,
	msgBus *bus.MessageBus,
	chatID, workspaceID string,
) string {
	t.Helper()
	wc := makeTestConn()
	handler.handleChatMessage(
		context.Background(),
		chatID,
		"", // frameSessionID empty → mint a new session
		"do it",
		"",          // agentID
		nil,         // mediaRefs
		"",          // modelName
		workspaceID, // workspaceID under test
		wc,
	)
	var sessionID string
	select {
	case msg := <-msgBus.InboundChan():
		sessionID = msg.SessionID
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage")
	}
	require.NotEmpty(t, sessionID, "handleChatMessage must mint a session")
	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store, "session store must resolve the minted session")
	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	require.NotNil(t, meta)
	return meta.WorkspaceID
}

// TestHandleChatMessage_UnknownWorkspaceID_DropsBinding proves the M4 fix: a
// frame carrying a non-existent workspace_id MUST NOT stamp the bogus id onto
// the session (which would land created tasks on an invisible board). Instead
// the binding is dropped and the session falls back to the default (empty
// binding here → resolveWorkspaceID resolves the real default at task time).
func TestHandleChatMessage_UnknownWorkspaceID_DropsBinding(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home
	// Only a real, existing workspace lives on disk.
	writeWorkspaceRecord(t, home, "01JXWORKSPACEREAL00000000001", true)

	got := mintedSessionMetaWorkspace(t, handler, msgBus, "chat-m4-bogus", "01JXWORKSPACEBOGUS0000000099")
	assert.Empty(t, got,
		"a non-existent workspace_id must be dropped (not stamped) so the task falls back to the default board")
}

// TestHandleChatMessage_KnownWorkspaceID_Binds proves the happy path still
// binds: a workspace_id that exists on disk is stamped onto the session.
func TestHandleChatMessage_KnownWorkspaceID_Binds(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home
	const wantWS = "01JXWORKSPACEREAL00000000002"
	writeWorkspaceRecord(t, home, wantWS, true)

	got := mintedSessionMetaWorkspace(t, handler, msgBus, "chat-m4-known", wantWS)
	assert.Equal(t, wantWS, got, "an existing workspace_id must be bound to the minted session")
}

// TestHandleChatMessage_FrameDecode_BindsWorkspace decodes a real MessageFrame
// JSON carrying metadata.workspace_id through the same extraction path the WS
// read loop uses, then drives handleChatMessage and asserts the session binds.
// This closes the gap where the existing test passed workspaceID directly,
// bypassing the metadata decode.
func TestHandleChatMessage_FrameDecode_BindsWorkspace(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home
	const wantWS = "01JXWORKSPACEFRAME000000001"
	writeWorkspaceRecord(t, home, wantWS, true)

	// Build the exact wire frame the SPA sends for a workspace chat.
	frame := generated.MessageFrame{
		Type:     string(generated.WsFrameTypeMessage),
		Content:  "create a task",
		Metadata: map[string]any{"workspace_id": wantWS},
	}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)

	// Decode + extract exactly as the read loop does (websocket.go ~744-747).
	var decoded generated.MessageFrame
	require.NoError(t, json.Unmarshal(raw, &decoded))
	var workspaceID string
	if v, ok := decoded.Metadata["workspace_id"].(string); ok {
		workspaceID = v
	}
	require.Equal(t, wantWS, workspaceID, "metadata.workspace_id must survive decode")

	got := mintedSessionMetaWorkspace(t, handler, msgBus, "chat-m4-frame", workspaceID)
	assert.Equal(t, wantWS, got,
		"a workspace_id decoded from a real MessageFrame must bind to the minted session")
}
