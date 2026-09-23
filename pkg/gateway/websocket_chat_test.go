// websocket_chat_test.go: tests for handle an inbound chat message frame (model-name stamping, workspace setup kickoff, transcript attachments).

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readMessageStatusFrames(t *testing.T, wc *wsConn, count int) []generated.MessageStatusFrame {
	t.Helper()
	frames := make([]generated.MessageStatusFrame, 0, count)
	for len(frames) < count {
		select {
		case raw := <-wc.sendCh:
			var envelope struct {
				Type string `json:"type"`
			}
			require.NoError(t, json.Unmarshal(raw, &envelope))
			if envelope.Type != string(generated.WsFrameTypeMessageStatus) {
				continue
			}
			var frame generated.MessageStatusFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			frames = append(frames, frame)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %d message_status frames; got %d", count, len(frames))
		}
	}
	return frames
}

func TestHandleChatMessage_AcknowledgesPersistenceBeforeTurnStart(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	handler.handleChatMessageWithClientID(
		context.Background(), "chat-message-status", "", "hello", "", nil,
		"", "", false, "client-message-1", nil, wc,
	)

	frames := readMessageStatusFrames(t, wc, 1)
	require.Equal(t, "received", frames[0].State)
	_, ok := handler.GetStreamer(context.Background(), "webchat", "chat-message-status", frames[0].SessionId)
	require.True(t, ok)
	frames = append(frames, readMessageStatusFrames(t, wc, 1)...)
	require.Equal(t, []string{"received", "working"}, []string{frames[0].State, frames[1].State})
	for _, frame := range frames {
		assert.Equal(t, "client-message-1", frame.ClientMessageId)
		assert.NotEmpty(t, frame.SessionId)
	}
}

func TestHandleChatMessage_PublishFailureMarksClientMessageFailed(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	msgBus.Close()
	wc := makeTestConn()

	handler.handleChatMessageWithClientID(
		context.Background(), "chat-message-status-failed", "", "hello", "", nil,
		"", "", false, "client-message-failed", nil, wc,
	)

	frames := readMessageStatusFrames(t, wc, 2)
	require.Equal(t, []string{"received", "failed"}, []string{frames[0].State, frames[1].State})
}

// --- moved from websocket.go tests 2026-09-15 ---

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

// --- Task 1 (M4): workspace→turn binding ---

// TestHandleChatMessage_StampsWorkspaceOnSession proves that a chat frame
// carrying metadata.workspace_id binds the minted session to that workspace, so
// a task the agent creates during the turn resolves to the ACTIVE workspace
// (resolveWorkspaceID reads ToolWorkspaceID(ctx), which the loop seeds from the
// session's WorkspaceID).
func TestHandleChatMessage_StampsWorkspaceOnSession(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	const wantWS = "01JXWORKSPACE0000000000001"
	handler.handleChatMessage(
		context.Background(),
		"chat-m4-1", // chatID
		"",          // frameSessionID (empty → mint a new session)
		"do it",     // content
		"",          // agentID
		nil,         // mediaRefs
		"",          // modelName
		wantWS,      // workspaceID (active workspace)
		false,       // setupKickoff
		wc,
	)

	// Drain the inbound publish, then assert the minted session carries the
	// workspace binding on its meta.
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
	assert.Equal(t, wantWS, meta.WorkspaceID,
		"minted session must be bound to the active workspace (M4)")
}

// TestBuildTranscriptAttachments_WorkspaceRef is the D2 (library-spec,
// 2026-07-29 UAT) regression test: before the fix, websocket.go built a
// session.TranscriptEntry for every user message but NEVER set Attachments,
// even though the field has existed on TranscriptEntry all along — so a
// later turn (or a DIFFERENT AGENT after a handoff, exactly what happened in
// the UAT: Mia -> Ray) had no durable record of what was uploaded.
//
// This drives a REAL upload through HandleUpload (reusing the D-1 dual-write
// test harness) and then calls buildTranscriptAttachments with the resulting
// ref, exactly the way handleChatMessage does when it persists the user's
// TranscriptEntry — proving the persisted Attachments would carry the
// correct Type/Path/Size/MIMEType for that real, on-disk file.
func TestBuildTranscriptAttachments_WorkspaceRef(t *testing.T) {
	api := newUploadTestAPI(t)
	workspaceID := "ws-attachments"
	fileContent := "hello attachment"
	fileName := "notes.txt"

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("workspace_id", workspaceID))
	fw, err := w.CreateFormFile("file", fileName)
	require.NoError(t, err)
	_, _ = io.WriteString(fw, fileContent)
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()
	api.HandleUpload(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())

	var resp generated.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1)
	require.NotNil(t, resp.Files[0].Ref)
	ref := *resp.Files[0].Ref

	store := mediaStoreForWorkspace(api)

	attachments := buildTranscriptAttachments(store, []string{ref}, workspaceID)

	require.Len(t, attachments, 1, "the accepted ref must produce exactly one persisted attachment")
	got := attachments[0]
	assert.Equal(t, ".library/"+fileName, got.Path,
		"the persisted attachment must carry the EXACT work-relative path the D-1 dual-write staged — "+
			"a later turn, or a different agent after a handoff, must be able to act on this path directly")
	assert.Equal(t, int64(len(fileContent)), got.Size)
	assert.NotEmpty(t, got.MIMEType)
	// The persisted Type MUST be a member of the Attachment contract enum
	// (image|audio|video|file) — this value crosses the wire and the SPA
	// validates it with a strict Zod schema. An earlier revision asserted
	// "document" here, matching an implementation that used DetectFileClass
	// (the presentation-noun taxonomy used to phrase LLM guidance). That
	// combination shipped a BLOCKER found in live UAT on 2026-07-29: the SPA
	// rejected the entire messages payload and chat history refused to load
	// with "Backend response failed validation" after any document upload.
	// The test encoded the bug, so it could not catch it.
	assert.Equal(t, "file", got.Type,
		"a .txt must persist as the contract's media-category 'file', never the presentation-noun 'document'")
	assert.Contains(t, []string{"image", "audio", "video", "file"}, got.Type,
		"persisted attachment Type must be a member of the Attachment contract enum — anything else "+
			"fails the SPA's strict schema validation and breaks history loading entirely")
}

// TestBuildTranscriptAttachments_UnresolvableRefSkippedNotFatal verifies a
// ref that fails to resolve (deleted file, malformed ref) is skipped rather
// than aborting the whole message — an attachment record is best-effort
// metadata, not load-bearing for the turn itself.
func TestBuildTranscriptAttachments_UnresolvableRefSkippedNotFatal(t *testing.T) {
	api := newUploadTestAPI(t)
	store := mediaStoreForWorkspace(api)

	unresolvableRef := "media://workspace/ws-does-not-exist/00000000-0000-0000-0000-000000000000"
	attachments := buildTranscriptAttachments(store, []string{unresolvableRef}, "ws-does-not-exist")
	assert.Empty(t, attachments, "an unresolvable ref must be skipped, not panic or error out the caller")
}

// TestBuildTranscriptAttachments_NoStoreOrRefs verifies the nil/empty guards.
func TestBuildTranscriptAttachments_NoStoreOrRefs(t *testing.T) {
	assert.Nil(t, buildTranscriptAttachments(nil, []string{"media://workspace/ws1/id1"}, "ws1"))
	api := newUploadTestAPI(t)
	assert.Nil(t, buildTranscriptAttachments(mediaStoreForWorkspace(api), nil, "ws1"))
}

// TestHandleChatMessage_ForwardsModelNameToBus is the primary FR-010 happy-path
// test: a frame carrying metadata.model_name = "z-ai/glm-5-turbo" must result in
// a published bus.InboundMessage whose Metadata["model_name"] equals that exact
// string. Without this wire-up, the Wave 3 switch-compress machinery
// (handleModelSwitch, summarizeDroppedTurns, splitForSwitchCompress,
// fitWithinBudget) is functionally inert end-to-end because the consumer at
// pkg/agent/loop.go (inboundMetadata("model_name")) sits behind a
// producer that never writes the key.
func TestHandleChatMessage_ForwardsModelNameToBus(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	handler.handleChatMessage(
		context.Background(),
		"chat-model-1",     // chatID
		"",                 // frameSessionID (empty → mint a new session)
		"hello",            // content
		"",                 // agentID (no per-message agent override → default)
		nil,                // mediaRefs
		"z-ai/glm-5-turbo", // modelName
		"",                 // workspaceID (no active workspace)
		false,              // setupKickoff
		wc,
	)

	// Drain the inbound channel and assert the metadata key is present.
	select {
	case msg := <-msgBus.InboundChan():
		assert.Equal(t, "hello", msg.Content)
		require.NotNil(t, msg.Metadata, "msg.Metadata must be non-nil when model_name is set")
		assert.Equal(t, "z-ai/glm-5-turbo", msg.Metadata["model_name"],
			"bus.InboundMessage.Metadata[\"model_name\"] must carry the per-turn model override")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage — handleChatMessage did not publish")
	}
}

// TestHandleChatMessage_EmptyModelName_DoesNotSetKey verifies that an empty
// model_name does NOT set the metadata key — the agent falls back to its
// configured default model. Empty is treated as absent.
func TestHandleChatMessage_EmptyModelName_DoesNotSetKey(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	handler.handleChatMessage(
		context.Background(),
		"chat-model-2",
		"",
		"hello",
		"",
		nil,
		"",    // empty modelName
		"",    // workspaceID (no active workspace)
		false, // setupKickoff
		wc,
	)

	select {
	case msg := <-msgBus.InboundChan():
		if msg.Metadata != nil {
			_, hasKey := msg.Metadata["model_name"]
			assert.False(t, hasKey,
				"empty model_name must not produce a metadata key (server falls back to agent default)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage")
	}
}

// TestHandleChatMessage_WhitespaceModelName_DoesNotSetKey verifies that a
// whitespace-only model_name is treated as absent (TrimSpace is applied first).
// Without this guard, a stray " " from the SPA would crash the agent loop with
// an "unknown model" provider error.
func TestHandleChatMessage_WhitespaceModelName_DoesNotSetKey(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	handler.handleChatMessage(
		context.Background(),
		"chat-model-3",
		"",
		"hello",
		"",
		nil,
		"   \t\n", // whitespace only
		"",        // workspaceID (no active workspace)
		false,     // setupKickoff
		wc,
	)

	select {
	case msg := <-msgBus.InboundChan():
		if msg.Metadata != nil {
			_, hasKey := msg.Metadata["model_name"]
			assert.False(t, hasKey,
				"whitespace-only model_name must not produce a metadata key (TrimSpace strips it)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage")
	}
}

// TestHandleChatMessage_TrimsModelName verifies that surrounding whitespace is
// stripped from a non-empty model_name so the downstream resolver sees a
// canonical value (e.g. "z-ai/glm-5-turbo", not "  z-ai/glm-5-turbo  ").
func TestHandleChatMessage_TrimsModelName(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	handler.handleChatMessage(
		context.Background(),
		"chat-model-4",
		"",
		"hello",
		"",
		nil,
		"  z-ai/glm-5-turbo  ", // surrounding whitespace
		"",                     // workspaceID (no active workspace)
		false,                  // setupKickoff
		wc,
	)

	select {
	case msg := <-msgBus.InboundChan():
		require.NotNil(t, msg.Metadata)
		assert.Equal(t, "z-ai/glm-5-turbo", msg.Metadata["model_name"],
			"model_name must be TrimSpace'd before being written to Metadata")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage")
	}
}

// TestHandleChatMessage_ModelNameWithAgentID_BothKeysSet verifies that when
// both agentID and model_name are supplied, both metadata keys land on the
// bus.InboundMessage — neither path clobbers the other.
func TestHandleChatMessage_ModelNameWithAgentID_BothKeysSet(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	handler.handleChatMessage(
		context.Background(),
		"chat-model-5",
		"",
		"hello",
		"mia", // explicit agent_id
		nil,
		"z-ai/glm-5-turbo", // per-turn model
		"",                 // workspaceID (no active workspace)
		false,              // setupKickoff
		wc,
	)

	select {
	case msg := <-msgBus.InboundChan():
		require.NotNil(t, msg.Metadata)
		assert.Equal(t, "mia", msg.Metadata["agent_id"], "agent_id must remain")
		assert.Equal(
			t,
			"z-ai/glm-5-turbo",
			msg.Metadata["model_name"],
			"model_name must be added without clobbering agent_id",
		)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage")
	}
}

// TestHandleChatMessage_RejectsWorkerAgentID verifies RESIDUAL PATH 4: a chat
// frame that explicitly addresses a worker agentID must be rejected with an error
// frame and must NOT mint a live session for the worker. A worker is not a chat
// target.
func TestHandleChatMessage_RejectsWorkerAgentID(t *testing.T) {
	api, _ := newWorkerTestRestAPI(t)
	handler := newWSHandler(bus.NewMessageBus(), api.agentLoop, "")

	wc := makeTestConn()
	handler.handleChatMessage(
		context.Background(),
		"chat-worker-1", // chatID
		"",              // frameSessionID (empty → would mint a new session)
		"do the work",   // content
		"hans",          // agentID = worker
		nil,             // mediaRefs
		"",              // modelName (no per-turn override)
		"",              // workspaceID (no active workspace)
		false,           // setupKickoff
		wc,
	)

	// Drain frames: expect exactly one error frame, and no session_started frame.
	var sawError, sawSessionStarted bool
	for {
		select {
		case raw := <-wc.sendCh:
			var f replayFrameDecoder
			require.NoError(t, json.Unmarshal(raw, &f))
			switch f.Type {
			case string(generated.WsFrameTypeError):
				sawError = true
				assert.Contains(t, strings.ToLower(f.Message), "worker",
					"the error frame must explain a worker cannot be a chat target")
			case string(generated.WsFrameTypeSessionStarted):
				sawSessionStarted = true
			}
		default:
			require.True(t, sawError, "a worker chat frame must produce an error frame")
			require.False(t, sawSessionStarted, "a worker chat frame must NOT mint a session")
			return
		}
	}
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
