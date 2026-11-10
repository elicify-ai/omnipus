package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-047 / OSA-9 W8 / chief Q4 (2026-10-14): client minting is superseded.
// These are REAL socket/frame tests. No removed handleChatMessage kickoff
// argument and no fake session-mint or recipient handler is used.
type l1WU10ClientKickoffCase struct {
	sessionKind string
	agentID     string
	pending     bool
	missing     bool
	recipient   bool
	marker      any
	malformed   bool
}

func l1WU10FramesThroughPong(t *testing.T, conn *websocket.Conn, frame gen.MessageFrame) []gen.ErrorFrame {
	t.Helper()
	require.NoError(t, conn.WriteJSON(frame))
	// The application pong is ordered AFTER message handling on the real
	// read loop: a completion barrier, never a sleep/latency oracle.
	require.NoError(t, conn.WriteJSON(gen.PingFrame{Type: "ping"}))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(busDeliveryTimeout)))
	var frames []gen.ErrorFrame
	for {
		_, raw, readErr := conn.ReadMessage()
		require.NoError(t, readErr, "actual frame handling must reach the ordered pong barrier")
		var decoded gen.ErrorFrame
		require.NoError(t, json.Unmarshal(raw, &decoded))
		if decoded.Type == "pong" {
			return frames
		}
		frames = append(frames, decoded)
	}
}

func l1WU10AssertClientKickoffRefused(t *testing.T, row l1WU10ClientKickoffCase) {
	t.Helper()
	h := newL1WU10SetupHarness(t)
	const targetID = "01WU10CLIENTKICKOFFTARGET"
	if !row.missing {
		require.NoError(t, workspace.SaveRecord(h.api.homePath, workspace.Workspace{
			ID: targetID, Name: "Client must not start this setup", Status: "active",
			CoreTeam: []string{"mia"}, SetupPending: row.pending,
		}))
	}
	var recordBefore []byte
	if !row.missing {
		var readErr error
		recordBefore, readErr = os.ReadFile(filepath.Join(h.api.homePath, "workspaces", targetID+".json"))
		require.NoError(t, readErr)
	}
	metasBefore, listErr := h.store.ListSessions()
	require.NoError(t, listErr)
	transcriptsBefore := make(map[string][]session.TranscriptEntry, len(metasBefore))
	for _, meta := range metasBefore {
		entries, transcriptErr := h.store.ReadTranscript(meta.ID)
		require.NoError(t, transcriptErr)
		transcriptsBefore[meta.ID] = entries
	}
	handler := newWSHandler(h.bus, h.loop, "")
	handler.home = h.api.homePath
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	conn := dialTestWS(t, srv)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	sendWSAuthFrameDevMode(t, conn)
	id := row.agentID
	if id == "" {
		id = "mia"
	}
	frame := gen.MessageFrame{Type: "message", Content: "client-authored setup text must never become an offer",
		AgentId: &id, Metadata: map[string]any{"workspace_id": targetID, "workspace_setup_kickoff": row.marker}}
	switch row.sessionKind {
	case "omnipus":
		frame.SessionId = &h.mainID
	case "mia":
		frame.SessionId = &h.miaMainID
	case "unknown":
		unknown := "unknown-client-session"
		frame.SessionId = &unknown
	}
	if row.recipient {
		// Successor to the retired recipient-level kickoff refusal row:
		// the frame seam must refuse BEFORE routing this valid other pair.
		data, encodeErr := json.Marshal(map[string]any{
			"type": frame.Type, "content": frame.Content, "agent_id": id, "session_id": frame.SessionId,
			"metadata": frame.Metadata, "recipient": map[string]any{"agent_id": "omnipus", "workspace_id": h.defaultID},
		})
		require.NoError(t, encodeErr)
		require.NoError(t, json.Unmarshal(data, &frame), "recipient still uses the generated MessageFrame shape, never a handwritten wire struct")
	}
	frames := l1WU10FramesThroughPong(t, conn, frame)
	var errors []gen.ErrorFrame
	for _, response := range frames {
		if response.Type == "error" {
			errors = append(errors, response)
		}
		assert.NotEqual(t, "session_started", response.Type, "Q4: no client kickoff may mint a session")
		assert.NotEqual(t, "user_message", response.Type, "Q4: marker cannot be demoted to an ordinary user write")
	}
	assert.Len(t, errors, 1, "Q4: exactly one visible frame-seam refusal, not a silently accepted kickoff")
	if len(errors) == 1 {
		if row.malformed {
			assert.Equal(t, "malformed workspace_setup_kickoff metadata", errors[0].Message)
		} else {
			// Binding chief wording, 2026-10-14; do not read an implementation
			// constant to generate this independently supplied expected value.
			assert.Equal(t, "workspace setup is started by the server when a workspace is created; a client kickoff is refused", errors[0].Message)
			assert.Equal(t, frame.SessionId, errors[0].SessionId, "echo a supplied session id; absent/null when none was supplied")
		}
	}
	metasAfter, afterErr := h.store.ListSessions()
	require.NoError(t, afterErr)
	assert.Equal(t, metasBefore, metasAfter, "refusal precedes all session work")
	for _, meta := range metasBefore {
		entries, transcriptErr := h.store.ReadTranscript(meta.ID)
		require.NoError(t, transcriptErr)
		assert.Equal(t, transcriptsBefore[meta.ID], entries, "no main/other-session transcript entry from a client kickoff")
	}
	if !row.missing {
		recordAfter, readErr := os.ReadFile(filepath.Join(h.api.homePath, "workspaces", targetID+".json"))
		require.NoError(t, readErr)
		assert.Equal(t, recordBefore, recordAfter, "setup_pending and the entire target record stay untouched")
	} else {
		_, statErr := os.Stat(filepath.Join(h.api.homePath, "workspaces", targetID+".json"))
		assert.ErrorIs(t, statErr, os.ErrNotExist)
	}
	h.assertNoFurtherWake(t)
}

func TestWS_WorkspaceSetupKickoff_TrueWithoutSession_RefusedBeforeAnyWrites(t *testing.T) {
	l1WU10AssertClientKickoffRefused(t, l1WU10ClientKickoffCase{pending: true, marker: true})
}

func TestWS_WorkspaceSetupKickoff_WithSessionID_RefusedBeforeAnyWrites(t *testing.T) {
	for _, kind := range []string{"omnipus", "mia", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			l1WU10AssertClientKickoffRefused(t, l1WU10ClientKickoffCase{sessionKind: kind, pending: true, marker: true})
		})
	}
}

func TestWS_WorkspaceSetupKickoff_ConsumedOrUnknownWorkspaceStillRefusedAtFrame(t *testing.T) {
	t.Run("consumed", func(t *testing.T) {
		l1WU10AssertClientKickoffRefused(t, l1WU10ClientKickoffCase{pending: false, marker: true})
	})
	t.Run("unknown", func(t *testing.T) {
		l1WU10AssertClientKickoffRefused(t, l1WU10ClientKickoffCase{missing: true, marker: true})
	})
}

func TestWS_WorkspaceSetupKickoff_MalformedAgentStillRefusedAtFrame(t *testing.T) {
	l1WU10AssertClientKickoffRefused(t, l1WU10ClientKickoffCase{agentID: "../invalid-agent", pending: true, marker: true})
}

func TestWS_WorkspaceSetupKickoff_RecipientAlsoRefusedBeforeRoutingAndWrites(t *testing.T) {
	// Successor to the retired "workspace setup kickoff" row in
	// ws_recipient_refusals_test.go, under the chief's Q4 ruling.
	l1WU10AssertClientKickoffRefused(t, l1WU10ClientKickoffCase{sessionKind: "mia", pending: true, recipient: true, marker: true})
}

func TestWS_WorkspaceSetupKickoff_MalformedMetadataType_RejectsNotDemotes(t *testing.T) {
	for _, row := range []struct {
		name  string
		value any
	}{{"string", "true"}, {"null", nil}, {"number", 1}} {
		t.Run(row.name, func(t *testing.T) {
			l1WU10AssertClientKickoffRefused(t, l1WU10ClientKickoffCase{pending: true, marker: row.value, malformed: true})
		})
	}
}

func TestWS_WorkspaceSetupKickoff_FalseOrAbsentRemainsOrdinaryUserMessage(t *testing.T) {
	for _, explicitFalse := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "false"}[explicitFalse], func(t *testing.T) {
			h := newL1WU10SetupHarness(t)
			handler := newWSHandler(h.bus, h.loop, "")
			handler.home = h.api.homePath
			t.Cleanup(handler.Wait)
			srv := httptest.NewServer(handler)
			t.Cleanup(srv.Close)
			conn := dialTestWS(t, srv)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			sendWSAuthFrameDevMode(t, conn)
			id := "mia"
			frame := gen.MessageFrame{Type: "message", Content: "ordinary user instrument", AgentId: &id, SessionId: &h.miaMainID,
				Metadata: map[string]any{"workspace_id": h.defaultID}}
			if explicitFalse {
				frame.Metadata["workspace_setup_kickoff"] = false
			}
			frames := l1WU10FramesThroughPong(t, conn, frame)
			for _, response := range frames {
				require.NotEqual(t, "error", response.Type, "false/absent marker must be ordinary, not rejected; a semantic refusal is RED, not a bus timeout")
			}
			msg := awaitInboundMessage(t, h.bus, "ordinary false/absent marker control")
			assert.Equal(t, frame.Content, msg.Content)
			assert.Equal(t, h.miaMainID, msg.SessionID)
			// Ordinary chat binding lives on the existing session; the new
			// setup-owner-wake metadata contract is not imposed on this control.
			meta, metaErr := h.store.GetMeta(msg.SessionID)
			require.NoError(t, metaErr)
			assert.Equal(t, h.defaultID, meta.WorkspaceID)
			assert.Equal(t, "mia", msg.Metadata["agent_id"])
			assert.True(t, msg.UserInitiated)
			assert.True(t, msg.OperatorPrompt)
			assert.Empty(t, msg.Metadata["owner_wake"])
			assert.Empty(t, msg.Metadata["workspace_setup_offer"])
			h.assertNoFurtherWake(t)
			entries, readErr := h.store.ReadTranscript(h.miaMainID)
			require.NoError(t, readErr)
			require.Len(t, entries, 2)
			assert.Equal(t, "user", entries[1].Role)
			assert.Equal(t, frame.Content, entries[1].Content)
		})
	}
}
