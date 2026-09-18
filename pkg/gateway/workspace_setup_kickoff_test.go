// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// workspace_setup_kickoff_test.go — unit tests for the workspace-setup kickoff
// turn. The SPA sends a normal `message` frame whose metadata carries
// `workspace_id` plus `workspace_setup_kickoff: true` on a workspace's first
// open. handleChatMessage must: record the trigger as a system-role transcript
// entry (not a user bubble), clear the workspace's setup_pending flag exactly
// once (idempotency guard), give the session a fixed "Workspace setup" title,
// build the driving turn instruction SERVER-SIDE from the workspace's own
// name/description (never the client-supplied content), and otherwise run
// the turn normally so Ava's greeting streams live.
//
// See contracts/asyncapi.yaml metadata.workspace_setup_kickoff and
// pkg/workspace/workspace.go Workspace.SetupPending.

package gateway

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSetupKickoffWorkspaceRecord writes a minimal on-disk workspace record
// carrying setup_pending, so workspace.Exists / readWorkspaceFile observe it
// under home.
func writeSetupKickoffWorkspaceRecord(t *testing.T, home, id string, setupPending bool) {
	t.Helper()
	writeSetupKickoffWorkspaceRecordNamed(t, home, id, setupPending, "", "")
}

// writeSetupKickoffWorkspaceRecordNamed is writeSetupKickoffWorkspaceRecord
// plus an explicit name/description, for tests that assert the SERVER-BUILT
// kickoff instruction (built from the workspace's own name/description, never
// client-supplied content — see buildWorkspaceKickoffInstruction).
func writeSetupKickoffWorkspaceRecordNamed(t *testing.T, home, id string, setupPending bool, name, description string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	rec := map[string]any{
		"id":            id,
		"name":          name,
		"description":   description,
		"is_default":    false,
		"setup_pending": setupPending,
		"created_at":    time.Now().UTC().Format(time.RFC3339),
		"updated_at":    time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), data, 0o644))
}

// drainFrameOfType drains wc.sendCh (non-blocking) and returns the first
// frame of the given type found, or nil if none arrived. Frames of other
// types encountered along the way are discarded (not put back).
func drainFrameOfType(wc *wsConn, frameType string) *replayFrameDecoder {
	for {
		select {
		case raw := <-wc.sendCh:
			var f replayFrameDecoder
			if err := json.Unmarshal(raw, &f); err != nil {
				continue
			}
			if f.Type == frameType {
				fCopy := f
				return &fCopy
			}
		default:
			return nil
		}
	}
}

// drainErrorFrame drains wc.sendCh (non-blocking) and returns the first
// error-type frame found, or nil if none arrived.
func drainErrorFrame(wc *wsConn) *replayFrameDecoder {
	return drainFrameOfType(wc, string(generated.WsFrameTypeError))
}

// assertNoSessionMinted asserts that no session_id was tracked for chatID,
// i.e. no session was minted (or a minted session was fully rolled back) for
// a rejected/failed kickoff.
func assertNoSessionMinted(t *testing.T, handler *WSHandler, chatID string) {
	t.Helper()
	handler.mu.Lock()
	defer handler.mu.Unlock()
	assert.Empty(t, handler.sessionIDs[chatID], "a rejected/rolled-back kickoff must not leave a tracked session")
}

// newTestWSHandlerForKickoffAudit is newTestWSHandlerForModelName plus a real
// audit logger (Sandbox.AuditLog = true) wired to homePath/system/, and
// handler.home set to the SAME homePath so workspace records
// (homePath/workspaces/), sessions (homePath/sessions/), and audit
// (homePath/system/) all live under one root — mirroring how the real
// gateway wires home == agent-loop homePath. Used by the tests that assert
// on the workspace.setup_consumed audit entry.
func newTestWSHandlerForKickoffAudit(t *testing.T, msgBus *bus.MessageBus) (*WSHandler, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	homePath := t.TempDir()
	workspaceDir := filepath.Join(homePath, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o700))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "test-default-model"},
				MaxTokens:    4096,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)
	handler.home = homePath
	return handler, homePath
}

// readAuditRecords scans every *.jsonl file under homePath/system/ and
// returns every decoded record. Returns nil (not an error) if the system
// directory doesn't exist yet — a test asserting "no audit record was
// written" is a valid outcome, not a setup bug.
func readAuditRecords(t *testing.T, homePath string) []map[string]any {
	t.Helper()
	systemDir := filepath.Join(homePath, "system")
	entries, err := os.ReadDir(systemDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		require.NoError(t, err)
	}
	var records []map[string]any
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(systemDir, entry.Name()))
		require.NoError(t, err)
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var rec map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
				continue
			}
			records = append(records, rec)
		}
		require.NoError(t, scanner.Err())
		require.NoError(t, f.Close())
	}
	return records
}

// findAuditRecordsByEvent filters records down to those whose "event" field
// matches want.
func findAuditRecordsByEvent(records []map[string]any, want string) []map[string]any {
	var out []map[string]any
	for _, r := range records {
		if r["event"] == want {
			out = append(out, r)
		}
	}
	return out
}

// TestWS_WorkspaceSetupKickoff_MalformedMetadataType_RejectsNotDemotes drives
// the fix end-to-end through the REAL readLoop dispatch path (not
// handleChatMessage directly): a message frame whose
// metadata.workspace_setup_kickoff is the STRING "true" (a plausible
// client-side JSON-type slip) must be rejected with an error frame — never
// silently processed as an ordinary chat message (which would have persisted
// the junk content as a user-authored transcript entry and burned nothing,
// masking the client bug), and never allowed to reach the consume step.
func TestWS_WorkspaceSetupKickoff_MalformedMetadataType_RejectsNotDemotes(t *testing.T) {
	handler, msgBus, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	home := t.TempDir()
	handler.home = home

	const wsID = "01JXWORKSPACEKICKOFF0000030"
	writeSetupKickoffWorkspaceRecord(t, home, wsID, true)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	frame := map[string]any{
		"type":    "message",
		"content": "junk instruction the server must never process as a normal message",
		"metadata": map[string]any{
			"workspace_id":            wsID,
			"workspace_setup_kickoff": "true", // STRING, not boolean — malformed
		},
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	resp := readFrameOfType(t, conn, "error", 3*time.Second)
	assert.Contains(t, resp.Message, "malformed workspace_setup_kickoff metadata")

	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("a malformed kickoff metadata type must NOT be processed as a normal message, got: %+v", msg)
	case <-time.After(300 * time.Millisecond):
		// expected: nothing published, in either kickoff or normal-message form
	}

	w, err := readWorkspaceFile(home, wsID)
	require.NoError(t, err)
	assert.True(t, w.SetupPending, "malformed metadata must never reach the consume step")
}
