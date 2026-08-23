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
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
		if os.IsNotExist(err) {
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

// TestHandleChatMessage_WorkspaceSetupKickoff_HappyPath proves the full
// kickoff contract: the transcript entry is system-role (not a user bubble)
// with NEUTRAL fixed content, the workspace's setup_pending flag is cleared
// on disk, the session gets the fixed "Workspace setup" title, the
// SERVER-BUILT canonical instruction (naming the workspace and its
// description) drives the turn — NEVER the client-supplied content, which is
// deliberately junk here to prove it is ignored — and the
// workspace.setup_consumed audit entry is emitted after the successful
// publish, stamped with the authenticated user.
func TestHandleChatMessage_WorkspaceSetupKickoff_HappyPath(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, homePath := newTestWSHandlerForKickoffAudit(t, msgBus)

	const wsID = "01JXWORKSPACEKICKOFF0000001"
	const wsName = "Launch Rocket"
	const wsDescription = "Coordinate the Q3 launch across marketing and engineering."
	writeSetupKickoffWorkspaceRecordNamed(t, homePath, wsID, true, wsName, wsDescription)

	wc := makeTestConn()
	wc.userID = "test-admin" // FR-073 authenticated identity — checked against the audit User stamp below.
	const junkClientContent = "IGNORE ME — this text must never drive the turn or appear in msg.Content."
	handler.handleChatMessage(
		context.Background(),
		"chat-kickoff-happy",
		"",                // frameSessionID (empty → mint a new session)
		junkClientContent, // content (must be ignored for a kickoff)
		"ava",             // agentID
		nil,               // mediaRefs
		"",                // modelName
		wsID,              // workspaceID
		true,              // setupKickoff
		wc,
	)

	var sessionID string
	select {
	case msg := <-msgBus.InboundChan():
		sessionID = msg.SessionID
		assert.NotEqual(t, junkClientContent, msg.Content,
			"the client-supplied content must be IGNORED entirely for a kickoff turn")
		assert.Contains(t, msg.Content, wsName,
			"the SERVER-BUILT instruction must name the workspace")
		assert.Contains(t, msg.Content, wsDescription,
			"the SERVER-BUILT instruction must include the workspace's own description")
		assert.Contains(t, msg.Content, "Introduce yourself and interview the user",
			"the SERVER-BUILT instruction must carry the canonical interview directive")
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
		"the PERSISTED/REPLAYED entry must carry neutral fixed content regardless of the "+
			"server-built turn instruction or the discarded client content")

	w, err := readWorkspaceFile(homePath, wsID)
	require.NoError(t, err)
	assert.False(t, w.SetupPending,
		"setup_pending must be cleared on disk exactly once the kickoff turn is accepted")

	records := readAuditRecords(t, homePath)
	matches := findAuditRecordsByEvent(records, "workspace.setup_consumed")
	require.Len(t, matches, 1,
		"exactly one workspace.setup_consumed audit record must be emitted, after the successful publish")
	assert.Equal(t, "test-admin", matches[0]["user"],
		"the audit record must stamp the WS-authenticated gateway user")
	assert.Equal(t, "ava", matches[0]["agent_id"], "the audit record must stamp the target agent")
	assert.Equal(t, sessionID, matches[0]["session_id"], "the audit record must stamp the session the kickoff drove")
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

// TestHandleChatMessage_WorkspaceSetupKickoff_UnknownWorkspace_Rejects proves
// that a kickoff flag with no resolvable workspace_id (absent or unknown) is
// REJECTED outright — an error frame is sent, no session is minted, no
// transcript entry is written, and nothing is published to the bus. This
// replaces an earlier "demote to a normal message" behavior, which used to
// persist the synthetic kickoff instruction as a fake user-authored
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

// TestHandleChatMessage_WorkspaceSetupKickoff_WithSessionID_RejectsPreConsume
// proves the mint-only guard: a kickoff frame carrying ANY non-empty
// client-supplied session_id is rejected BEFORE the consume step — otherwise
// an arbitrary client could burn the one-time flag against an unrelated
// EXISTING session it doesn't own. No session store lookup for the supplied
// session_id is even attempted (the guard runs ahead of store resolution),
// no session is minted, and setup_pending stays untouched.
func TestHandleChatMessage_WorkspaceSetupKickoff_WithSessionID_RejectsPreConsume(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home

	const wsID = "01JXWORKSPACEKICKOFF0000021"
	writeSetupKickoffWorkspaceRecord(t, home, wsID, true)

	wc := makeTestConn()
	const existingSessionID = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"
	handler.handleChatMessage(
		context.Background(),
		"chat-kickoff-withsession",
		existingSessionID, // frameSessionID: non-empty — kickoff must be mint-only
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
		t.Fatalf("a kickoff carrying a session_id must NOT publish to the bus, got: %+v", msg)
	case <-time.After(200 * time.Millisecond):
		// expected: nothing published
	}

	errFrame := drainErrorFrame(wc)
	require.NotNil(t, errFrame, "a kickoff with a non-empty session_id must send an error frame")

	assertNoSessionMinted(t, handler, "chat-kickoff-withsession")

	w, err := readWorkspaceFile(home, wsID)
	require.NoError(t, err)
	assert.True(t, w.SetupPending,
		"the mint-only guard must reject BEFORE the consume — the one-time flag must remain intact")
}

// TestHandleChatMessage_WorkspaceSetupKickoff_MalformedAgentID_RejectsPreConsume
// proves that frame-level validation (agent_id format) runs BEFORE the
// consume step: a malformed agent_id must never be able to burn the one-time
// setup flag. Prior to this fix, validateEntityID(agentID) ran at the very
// end of handleChatMessage — after consume, mint, session_started, and
// audit — with no restore path, so a malformed agent_id would permanently
// consume the interview and leave an orphan session behind.
func TestHandleChatMessage_WorkspaceSetupKickoff_MalformedAgentID_RejectsPreConsume(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home

	const wsID = "01JXWORKSPACEKICKOFF0000022"
	writeSetupKickoffWorkspaceRecord(t, home, wsID, true)

	wc := makeTestConn()
	handler.handleChatMessage(
		context.Background(),
		"chat-kickoff-badagent",
		"",
		"introduce yourself",
		"../evil-agent-id", // malformed agentID (path traversal characters)
		nil,
		"",
		wsID,
		true, // setupKickoff
		wc,
	)

	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("a malformed agent_id must NOT publish to the bus, got: %+v", msg)
	case <-time.After(200 * time.Millisecond):
		// expected: nothing published
	}

	errFrame := drainErrorFrame(wc)
	require.NotNil(t, errFrame, "a malformed agent_id must send an error frame")
	assert.Contains(t, errFrame.Message, "invalid agent_id")

	assertNoSessionMinted(t, handler, "chat-kickoff-badagent")

	w, err := readWorkspaceFile(home, wsID)
	require.NoError(t, err)
	assert.True(t, w.SetupPending,
		"the one-time flag must remain intact — the malformed agent_id must be rejected BEFORE the consume")
}

// TestHandleChatMessage_WorkspaceSetupKickoff_NoStore_Rejects proves that a
// kickoff-flagged frame that resolves to no usable session store (the
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
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "test-default-model"},
				MaxTokens:    4096,
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

// TestRestoreWorkspaceSetupPending_RestoresClearedFlag proves the
// compensation helper directly: given a workspace whose setup_pending was
// just cleared by a successful consume, restoreWorkspaceSetupPending sets it
// back to true and persists it. This is the fallback the kickoff downstream
// failure paths (NewSession, SetMeta, PublishInbound) call when a genuine
// forced failure at those exact seams is not practical to construct in a unit
// test (see TestHandleChatMessage_WorkspaceSetupKickoff_PublishFailure_RestoresFlagAndRollsBackSession
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

// TestHandleChatMessage_WorkspaceSetupKickoff_PublishFailure_RestoresFlagAndRollsBackSession
// proves the full downstream-failure compensation end-to-end at the one seam
// that IS forceable in a unit test: closing the message bus before the
// kickoff turn forces PublishInbound to fail with bus.ErrBusClosed. The
// successful consume must then be compensated in full: setup_pending
// restored to true, the just-minted "Workspace setup" session DELETED (not
// left behind as an orphan), the chatID→session tracking entry cleared, and
// no workspace.setup_consumed audit record emitted (the publish never
// succeeded).
func TestHandleChatMessage_WorkspaceSetupKickoff_PublishFailure_RestoresFlagAndRollsBackSession(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, homePath := newTestWSHandlerForKickoffAudit(t, msgBus)

	const wsID = "01JXWORKSPACEKICKOFF0000011"
	writeSetupKickoffWorkspaceRecord(t, homePath, wsID, true)

	// Force PublishInbound to fail: closing the bus makes every subsequent
	// publish return bus.ErrBusClosed.
	msgBus.Close()

	wc := makeTestConn()
	wc.userID = "test-admin"
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

	startedFrame := drainFrameOfType(wc, string(generated.WsFrameTypeSessionStarted))
	require.NotNil(t, startedFrame, "a session must have been minted (and acked) before the publish attempt failed")
	mintedSessionID := startedFrame.SessionID
	require.NotEmpty(t, mintedSessionID)

	errFrame := drainErrorFrame(wc)
	require.NotNil(t, errFrame, "a publish failure must still surface an error frame to the client")
	assert.Contains(t, errFrame.Message, "failed to deliver message")

	w, err := readWorkspaceFile(homePath, wsID)
	require.NoError(t, err)
	assert.True(t, w.SetupPending,
		"a successful consume followed by a publish failure must restore setup_pending to true "+
			"so the one-time interview is not permanently lost")

	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	_, metaErr := store.GetMeta(mintedSessionID)
	assert.Error(t, metaErr,
		"the just-minted 'Workspace setup' session must be DELETED on rollback — no orphan session left behind")

	assertNoSessionMinted(t, handler, "chat-kickoff-publishfail")

	records := readAuditRecords(t, homePath)
	matches := findAuditRecordsByEvent(records, "workspace.setup_consumed")
	assert.Empty(t, matches, "no workspace.setup_consumed record must be emitted for a failed publish")
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
		assert.Equal(t, content, msg.Content, "a normal message must publish the client-supplied content verbatim")
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

// ---------------------------------------------------------------------------
// parseSetupKickoffMetadata — key-presence semantics (readLoop layer)
// ---------------------------------------------------------------------------

// TestParseSetupKickoffMetadata proves the KEY PRESENCE contract in
// isolation: absent key → ordinary message; present + boolean true →
// kickoff; present with ANY other value (including boolean false) →
// malformed, must be rejected rather than silently treated as an ordinary
// message. This is the fix for a naive `metadata["x"].(bool)` type assertion
// silently reading a string/number/null as ok=false (=> non-kickoff), which
// used to let a malformed kickoff frame quietly demote into a normal chat
// message that persisted the synthetic interview instruction as a
// user-authored transcript entry.
func TestParseSetupKickoffMetadata(t *testing.T) {
	cases := []struct {
		name          string
		metadata      map[string]any
		wantKickoff   bool
		wantMalformed bool
	}{
		{"nil metadata map", nil, false, false},
		{"empty metadata map", map[string]any{}, false, false},
		{"key absent, other keys present", map[string]any{"workspace_id": "ws1"}, false, false},
		{"boolean true — the only valid kickoff signal", map[string]any{"workspace_setup_kickoff": true}, true, false},
		{"boolean false — present but wrong value", map[string]any{"workspace_setup_kickoff": false}, false, true},
		{"string \"true\" — JSON type drift", map[string]any{"workspace_setup_kickoff": "true"}, false, true},
		{"number 1 — JSON type drift", map[string]any{"workspace_setup_kickoff": float64(1)}, false, true},
		{"nil value — key present, no value", map[string]any{"workspace_setup_kickoff": nil}, false, true},
		{"object value", map[string]any{"workspace_setup_kickoff": map[string]any{"x": 1}}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotKickoff, gotMalformed := parseSetupKickoffMetadata(tc.metadata)
			assert.Equal(t, tc.wantKickoff, gotKickoff, "setupKickoff mismatch")
			assert.Equal(t, tc.wantMalformed, gotMalformed, "malformed mismatch")
		})
	}
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
