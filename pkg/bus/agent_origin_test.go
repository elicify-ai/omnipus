package bus

import (
	"testing"
)

// agent_origin_test.go — ADR-065 / channel-agent-ownership-spec.md FR-6:
// "bus.OutboundMessage MUST carry AgentID and WorkspaceID for
// agent-originated sends." These tests cover the carrier only (this package
// owns AgentID/WorkspaceID as struct fields + their wire encoding); the
// dispatch-time ownership re-check, the audit event, and the refusal counter
// are wired elsewhere (see the report to the lead for exact call sites).

// TestOutboundMessage_AgentOriginFields_SetGet proves AgentID and
// WorkspaceID are settable/readable on OutboundMessage.
func TestOutboundMessage_AgentOriginFields_SetGet(t *testing.T) {
	msg := OutboundMessage{
		Channel:     "telegram.acme",
		ChatID:      "123",
		Content:     "hi",
		AgentID:     "mia",
		WorkspaceID: "ws-sales",
	}
	if msg.Channel != "telegram.acme" || msg.ChatID != "123" || msg.Content != "hi" {
		t.Fatalf("carrier fields = %+v", msg)
	}
	if msg.AgentID != "mia" {
		t.Fatalf("expected AgentID mia, got %q", msg.AgentID)
	}
	if msg.WorkspaceID != "ws-sales" {
		t.Fatalf("expected WorkspaceID ws-sales, got %q", msg.WorkspaceID)
	}
}

// TestOutboundMessage_AgentOriginFields_JSONRoundTrip proves AgentID and
// WorkspaceID survive JSON marshal/unmarshal on OutboundMessage, using the
// exact wire tags ("agent_id", "workspace_id") a producer or the dispatch
// layer would rely on.
func TestOutboundMessage_AgentOriginFields_JSONRoundTrip(t *testing.T) {
	in := OutboundMessage{
		Channel:     "slack.acme",
		ChatID:      "C1",
		Content:     "hello",
		AgentID:     "ava",
		WorkspaceID: "ws-eng",
	}
	b := jsonMarshal(t, in)
	if has, s := jsonContains(b, `"agent_id":"ava"`); !has {
		t.Fatalf("expected agent_id in JSON, got %s", s)
	}
	if has, s := jsonContains(b, `"workspace_id":"ws-eng"`); !has {
		t.Fatalf("expected workspace_id in JSON, got %s", s)
	}

	var out OutboundMessage
	if err := jsonUnmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.AgentID != "ava" {
		t.Fatalf("round-trip AgentID mismatch: got %q", out.AgentID)
	}
	if out.WorkspaceID != "ws-eng" {
		t.Fatalf("round-trip WorkspaceID mismatch: got %q", out.WorkspaceID)
	}
	if out.Channel != "slack.acme" || out.ChatID != "C1" || out.Content != "hello" {
		t.Fatalf("unrelated fields corrupted by round-trip: %+v", out)
	}
}

// TestOutboundMessage_AgentOriginFields_OmitemptyOnSystemSend proves a
// system-originated message (AgentID/WorkspaceID left zero-value, exactly as
// the ~19 existing system producers — streamed replies, notifyDrop
// backpressure, schedule delivery, device notifications — do today) is
// byte-identical on the wire to what it was before this change: no
// "agent_id" or "workspace_id" key appears. This is the consumer
// compatibility guarantee — existing decoders that don't know these fields
// must see no difference for system sends.
func TestOutboundMessage_AgentOriginFields_OmitemptyOnSystemSend(t *testing.T) {
	systemMsg := OutboundMessage{
		Channel: "telegram",
		ChatID:  "123",
		Content: "[message dropped: channel is overloaded, please retry]",
	}
	b := jsonMarshal(t, systemMsg)
	if has, s := jsonContains(b, `"agent_id"`); has {
		t.Fatalf("system-originated OutboundMessage must omit agent_id, got %s", s)
	}
	if has, s := jsonContains(b, `"workspace_id"`); has {
		t.Fatalf("system-originated OutboundMessage must omit workspace_id, got %s", s)
	}
	// And it must match exactly what a pre-FR-6 system send would have
	// produced: only the fields that were already there.
	want := `{"channel":"telegram","chat_id":"123","content":"[message dropped: channel is overloaded, please retry]"}`
	if string(b) != want {
		t.Fatalf("system-originated OutboundMessage wire shape changed:\n got: %s\nwant: %s", b, want)
	}
}

// TestOutboundMediaMessage_AgentOriginFields_SetGet proves AgentID is
// settable/readable on OutboundMediaMessage (WorkspaceID already existed
// pre-ADR-065; AgentID is the new field added for consistency with
// OutboundMessage, since send_file is the sibling agent-originated path).
func TestOutboundMediaMessage_AgentOriginFields_SetGet(t *testing.T) {
	msg := OutboundMediaMessage{
		Channel:     "telegram.acme",
		ChatID:      "123",
		WorkspaceID: "ws-sales",
		AgentID:     "mia",
		Parts:       []MediaPart{{Type: "image", Ref: "media://abc"}},
	}
	if msg.Channel != "telegram.acme" || msg.ChatID != "123" || len(msg.Parts) != 1 {
		t.Fatalf("media carrier fields = %+v", msg)
	}
	if msg.AgentID != "mia" {
		t.Fatalf("expected AgentID mia, got %q", msg.AgentID)
	}
	if msg.WorkspaceID != "ws-sales" {
		t.Fatalf("expected WorkspaceID ws-sales, got %q", msg.WorkspaceID)
	}
}

// TestOutboundMediaMessage_AgentOriginFields_JSONRoundTrip proves AgentID
// survives JSON marshal/unmarshal on OutboundMediaMessage with the exact
// wire tag "agent_id", alongside the pre-existing "workspace_id".
func TestOutboundMediaMessage_AgentOriginFields_JSONRoundTrip(t *testing.T) {
	in := OutboundMediaMessage{
		Channel:     "slack.acme",
		ChatID:      "C1",
		WorkspaceID: "ws-eng",
		AgentID:     "ava",
		Parts:       []MediaPart{{Type: "file", Ref: "media://xyz", Filename: "report.pdf"}},
	}
	b := jsonMarshal(t, in)
	if has, s := jsonContains(b, `"agent_id":"ava"`); !has {
		t.Fatalf("expected agent_id in JSON, got %s", s)
	}
	if has, s := jsonContains(b, `"workspace_id":"ws-eng"`); !has {
		t.Fatalf("expected workspace_id in JSON, got %s", s)
	}

	var out OutboundMediaMessage
	if err := jsonUnmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.AgentID != "ava" {
		t.Fatalf("round-trip AgentID mismatch: got %q", out.AgentID)
	}
	if out.WorkspaceID != "ws-eng" {
		t.Fatalf("round-trip WorkspaceID mismatch: got %q", out.WorkspaceID)
	}
	if len(out.Parts) != 1 || out.Parts[0].Ref != "media://xyz" {
		t.Fatalf("Parts corrupted by round-trip: %+v", out.Parts)
	}
}

// TestOutboundMediaMessage_AgentID_OmitemptyPreservesExistingShape proves
// that adding AgentID does not disturb the wire shape of an
// OutboundMediaMessage built the way the pre-ADR-065 sole producer
// (pkg/agent/loop.go's send_file tool-media delivery block) already builds
// it — WorkspaceID set, AgentID left zero-value until the lead wires it at
// the call site. "omitempty" must still drop agent_id in that case.
func TestOutboundMediaMessage_AgentID_OmitemptyPreservesExistingShape(t *testing.T) {
	msg := OutboundMediaMessage{
		Channel:     "telegram",
		ChatID:      "123",
		WorkspaceID: "ws-sales",
		Parts:       []MediaPart{{Type: "image", Ref: "media://abc"}},
	}
	b := jsonMarshal(t, msg)
	if has, s := jsonContains(b, `"agent_id"`); has {
		t.Fatalf("OutboundMediaMessage with empty AgentID must omit agent_id, got %s", s)
	}
	if has, s := jsonContains(b, `"workspace_id":"ws-sales"`); !has {
		t.Fatalf("expected workspace_id to survive unchanged, got %s", s)
	}
}
