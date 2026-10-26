package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// not-wire-format: paired TEST INPUT container. Frames below come from the
// actual loop, media store, gateway hub and WebSocket, never a copied error
// serializer. Expected SPA running-state values live in the consumer test.
type issue1435R3FrameInputs struct {
	SessionID       string `json:"session_id"`
	TurnID          string `json:"turn_id"`
	MessageID       string `json:"message_id"`
	NoticeIndex     int    `json:"notice_index"`
	ProviderWasHeld bool   `json:"provider_was_held"`
	Frames          []any  `json:"frames"`
}

func TestIssue1435R3_RealGatewayNoticeInputs(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	provider := newControllableStreamProvider(providerRound{tokens: []string{"Working on the remaining request.", " Still progressing."}})
	paused, resume := provider.pauseAfter(0, 0)
	var once sync.Once
	open := func() { once.Do(func() { close(resume) }) }
	defer open() // Release the provider before server cleanup even when an assertion fails.
	server, handler, _ := newControllableStreamTestServer(t, provider)
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)
	handler.agentLoop.SetMediaStore(store)
	owner, err := handler.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "accepted.txt")
	require.NoError(t, os.WriteFile(file, []byte("accepted file"), 0o600))
	ref, err := store.Store(file, media.MediaMeta{Filename: "accepted.txt", ContentType: "text/plain", CleanupPolicy: media.CleanupPolicyForgetOnly}, media.SessionUploadScopePrefix+owner.ID)
	require.NoError(t, err)
	_, err = store.Resolve(ref)
	require.NoError(t, err, "instrument: the attachment really was accepted")
	require.NoError(t, store.ReleaseAll(media.SessionUploadScopePrefix+owner.ID))
	_, err = store.Resolve(ref)
	require.ErrorIs(t, err, media.ErrNotFound)
	connection := dialTestWS(t, server)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	sendWSAuthFrameDevMode(t, connection)
	request, err := json.Marshal(wsClientFrameTestHelper{Type: "message", Content: "Continue processing the text even if the attachment is unavailable.", SessionID: owner.ID, AgentID: "mia", Media: []string{ref}})
	require.NoError(t, err)
	require.NoError(t, connection.WriteMessage(websocket.TextMessage, request))
	inputs := issue1435R3FrameInputs{SessionID: "1435-round3-session", NoticeIndex: -1}
	var actualTurn, actualMessage string
	read := func() map[string]any {
		require.NoError(t, connection.SetReadDeadline(time.Now().Add(20*time.Second)))
		_, raw, err := connection.ReadMessage()
		require.NoError(t, err, "real gateway must deliver its frame, not a fixture timeout")
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		return frame
	}
	for {
		frame := read()
		if frame["session_id"] != owner.ID {
			continue
		}
		inputs.Frames = append(inputs.Frames, frame)
		if payload, ok := frame["payload"].(map[string]any); ok {
			if llm, ok := payload["llm_error"].(map[string]any); ok && llm["code"] == "media_unavailable" {
				inputs.NoticeIndex = len(inputs.Frames) - 1
			}
		}
		require.NotEqual(t, "done", frame["type"], "a nonfatal missing-ref notice must not stop the real backend before provider work")
		if frame["type"] == "token" {
			actualTurn, _ = frame["turn_id"].(string)
			actualMessage, _ = frame["message_id"].(string)
			break
		}
	}
	select {
	case <-paused:
		inputs.ProviderWasHeld = true
	case <-time.After(20 * time.Second):
		t.Fatal("instrument: provider never reached its controlled in-flight pause")
	}
	require.GreaterOrEqual(t, inputs.NoticeIndex, 0, "the missing-ref notice must have crossed the actual gateway before completion")
	require.NotEmpty(t, actualTurn)
	require.NotEmpty(t, actualMessage)
	open()
	for {
		frame := read()
		if frame["session_id"] != owner.ID {
			continue
		}
		inputs.Frames = append(inputs.Frames, frame)
		if frame["type"] == "done" {
			break
		}
	}
	inputs.TurnID, inputs.MessageID = "1435-round3-turn", "1435-round3-reply"
	for _, frame := range inputs.Frames {
		issue1435R3CanonicalizeFrameInput(frame, owner.ID, actualTurn, actualMessage)
	}
	encoded, err := json.MarshalIndent(inputs, "", "  ")
	require.NoError(t, err)
	path := filepath.Join("..", "..", "tests", "fixtures", "attachments-1435-round3", "media-unavailable-frames.json")
	if os.Getenv("UPDATE_1435_R3_INPUTS") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, append(encoded, '\n'), 0o600))
	}
	paired, err := os.ReadFile(path)
	require.NoError(t, err, "SPA producer INPUT fixture must be recorded before its consumer runs")
	require.JSONEq(t, string(encoded), string(paired), "SPA input fixture must stay paired with the actual gateway producer; it is NOT the browser-state oracle")
	t.Logf("actual gateway frames=%d, notice index=%d, provider held=%t, real done delivered", len(inputs.Frames), inputs.NoticeIndex, inputs.ProviderWasHeld)
}

func issue1435R3CanonicalizeFrameInput(value any, sessionID, turnID, messageID string) {
	switch object := value.(type) {
	case []any:
		for _, child := range object {
			issue1435R3CanonicalizeFrameInput(child, sessionID, turnID, messageID)
		}
	case map[string]any:
		// A user_message frame's `id` is minted by the gateway per run (the
		// echoed user row, NOT the streamed reply's message_id, which the
		// session/turn/reply renames above already cover). Normalise it like
		// every other generated identifier so a non-recording run compares
		// stable; only this frame type is affected — every other `id` field
		// is still compared exactly.
		if object["type"] == "user_message" {
			if _, ok := object["id"].(string); ok {
				object["id"] = "1435-round3-user-message"
			}
		}
		for key, child := range object {
			if text, ok := child.(string); ok {
				switch text {
				case sessionID:
					object[key] = "1435-round3-session"
				case turnID:
					object[key] = "1435-round3-turn"
				case messageID:
					object[key] = "1435-round3-reply"
				}
			}
			lower := strings.ToLower(key)
			if lower == "boot_id" {
				object[key] = "1435-round3-boot"
			}
			if strings.HasSuffix(lower, "_at") || lower == "timestamp" {
				if _, ok := child.(string); ok {
					object[key] = "2026-10-26T00:00:00Z"
				}
			}
			// Timing-only performance metrics are opaque INPUT facts. Preserve
			// error types, codes, seq, routing and lifecycle flags verbatim.
			if strings.Contains(lower, "duration") || strings.Contains(lower, "latency") || strings.HasSuffix(lower, "_ms") {
				if _, ok := child.(float64); ok {
					object[key] = float64(1)
				}
			}
			issue1435R3CanonicalizeFrameInput(child, sessionID, turnID, messageID)
		}
	}
}
