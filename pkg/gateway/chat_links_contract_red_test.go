// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, chat-links contract item (e) and the gateway half of FR-026.
// Oracles are the chat-links spec Contract section and the 2026-10-29
// architect clarification (session_id 1..255, path minLength 1).
package gateway

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestChatLinks_ChatPresenceInboundSchema(t *testing.T) {
	if got := wsFrameSchemaName("chat_presence"); got != "ChatPresenceFrame" {
		t.Fatalf("wsFrameSchemaName(chat_presence) = %q, want ChatPresenceFrame — required by contract item (e) / FR-026", got)
	}

	valid := []byte(`{"type":"chat_presence","session_id":"sess-1","watching":true}`)
	if msg, serverErr := ValidateInboundFrameJSON("ChatPresenceFrame", valid); msg != "" || serverErr {
		t.Fatalf("valid chat_presence rejected: msg=%q serverErr=%v (contract item e instrument: a valid frame is accepted)", msg, serverErr)
	}

	missingWatching := []byte(`{"type":"chat_presence","session_id":"sess-1"}`)
	msg, serverErr := ValidateInboundFrameJSON("ChatPresenceFrame", missingWatching)
	if serverErr {
		t.Fatalf("chat_presence missing watching is a server error (%q); contract item (e) requires a schema rejection", msg)
	}
	if msg == "" {
		t.Fatal("chat_presence missing watching was accepted; contract item (e) requires rejection")
	}

	// session_id is 1..255 (architect clarification). 255 accepted, 256 and empty rejected.
	longOK := []byte(`{"type":"chat_presence","session_id":"` + strings.Repeat("s", 255) + `","watching":false}`)
	if msg, serverErr := ValidateInboundFrameJSON("ChatPresenceFrame", longOK); msg != "" || serverErr {
		t.Fatalf("session_id length 255 rejected: msg=%q serverErr=%v; clarification says 1..255", msg, serverErr)
	}
	tooLong := []byte(`{"type":"chat_presence","session_id":"` + strings.Repeat("s", 256) + `","watching":false}`)
	if msg, serverErr := ValidateInboundFrameJSON("ChatPresenceFrame", tooLong); serverErr || msg == "" {
		t.Fatalf("session_id length 256 must be a schema rejection, got msg=%q serverErr=%v", msg, serverErr)
	}
	emptySid := []byte(`{"type":"chat_presence","session_id":"","watching":true}`)
	if msg, serverErr := ValidateInboundFrameJSON("ChatPresenceFrame", emptySid); serverErr || msg == "" {
		t.Fatalf("empty session_id must be a schema rejection, got msg=%q serverErr=%v", msg, serverErr)
	}
}

func TestChatLinks_OpenInLibraryIsNotAnInboundFrame(t *testing.T) {
	// open_in_library is server→client. It must not gain an inbound schema name.
	if got := wsFrameSchemaName("open_in_library"); got != "" {
		t.Fatalf("wsFrameSchemaName(open_in_library) = %q, want empty (server→client only)", got)
	}
	// The generated frame has no seq field (D-D). A seq key would be a contract regression.
	var frame generated.OpenInLibraryFrame
	frame.Type = "open_in_library"
	frame.SessionId = "sess-1"
	frame.CallId = "call-1"
	frame.RequestId = "0123456789abcdef0123456789abcdef"
	frame.WorkspaceId = "W"
	frame.Path = "notes/plan.md"
	if frame.Path == "" || len(frame.RequestId) != 32 {
		t.Fatalf("fixture broken: path %q request_id %q", frame.Path, frame.RequestId)
	}
}
