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

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/stretchr/testify/require"
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
		false,       // setupKickoff
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
