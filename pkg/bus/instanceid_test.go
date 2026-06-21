package bus

import (
	"testing"
)

// TestInstanceID_PeerSetGet verifies the InstanceID field on Peer is settable
// and readable (ADR-019 FR-4b / NFR-1 — the field was added to all four bus
// structs so channels can carry their instance key directly instead of
// smuggling it through InboundMessage.Metadata).
func TestInstanceID_PeerSetGet(t *testing.T) {
	p := Peer{InstanceID: "telegram:bot1"}
	if p.InstanceID != "telegram:bot1" {
		t.Fatalf("expected InstanceID telegram:bot1, got %q", p.InstanceID)
	}
}

// TestInstanceID_SenderInfoSetGet verifies the InstanceID field on SenderInfo.
func TestInstanceID_SenderInfoSetGet(t *testing.T) {
	s := SenderInfo{InstanceID: "discord:main"}
	if s.InstanceID != "discord:main" {
		t.Fatalf("expected InstanceID discord:main, got %q", s.InstanceID)
	}
}

// TestInstanceID_InboundMessageSetGet verifies the InstanceID field on
// InboundMessage is settable and readable.
func TestInstanceID_InboundMessageSetGet(t *testing.T) {
	msg := InboundMessage{
		InstanceID: "slack:workspace-a",
	}
	if msg.InstanceID != "slack:workspace-a" {
		t.Fatalf("expected InstanceID slack:workspace-a, got %q", msg.InstanceID)
	}
}

// TestInstanceID_OutboundMessageSetGet verifies the InstanceID field on
// OutboundMessage is settable and readable.
func TestInstanceID_OutboundMessageSetGet(t *testing.T) {
	msg := OutboundMessage{
		InstanceID: "irc:libera",
	}
	if msg.InstanceID != "irc:libera" {
		t.Fatalf("expected InstanceID irc:libera, got %q", msg.InstanceID)
	}
}

// TestInstanceID_JSONRoundTrip verifies InstanceID survives JSON marshaling
// and unmarshalling on all four structs, and that the omitempty tag works
// (empty InstanceID is omitted from JSON output).
func TestInstanceID_JSONRoundTrip(t *testing.T) {
	// Peer
	peerJSON := `{"kind":"direct","id":"42","instance_id":"tg:bot2"}`
	var p Peer
	if err := jsonUnmarshal([]byte(peerJSON), &p); err != nil {
		t.Fatalf("unmarshal Peer: %v", err)
	}
	if p.InstanceID != "tg:bot2" {
		t.Fatalf("expected Peer.InstanceID tg:bot2, got %q", p.InstanceID)
	}

	// SenderInfo
	siJSON := `{"platform":"telegram","instance_id":"tg:bot2"}`
	var si SenderInfo
	if err := jsonUnmarshal([]byte(siJSON), &si); err != nil {
		t.Fatalf("unmarshal SenderInfo: %v", err)
	}
	if si.InstanceID != "tg:bot2" {
		t.Fatalf("expected SenderInfo.InstanceID tg:bot2, got %q", si.InstanceID)
	}

	// InboundMessage
	ibJSON := `{"channel":"telegram","chat_id":"123","session_key":"k","instance_id":"tg:bot2"}`
	var ib InboundMessage
	if err := jsonUnmarshal([]byte(ibJSON), &ib); err != nil {
		t.Fatalf("unmarshal InboundMessage: %v", err)
	}
	if ib.InstanceID != "tg:bot2" {
		t.Fatalf("expected InboundMessage.InstanceID tg:bot2, got %q", ib.InstanceID)
	}

	// OutboundMessage
	obJSON := `{"channel":"telegram","chat_id":"123","instance_id":"tg:bot2","content":"hi"}`
	var ob OutboundMessage
	if err := jsonUnmarshal([]byte(obJSON), &ob); err != nil {
		t.Fatalf("unmarshal OutboundMessage: %v", err)
	}
	if ob.InstanceID != "tg:bot2" {
		t.Fatalf("expected OutboundMessage.InstanceID tg:bot2, got %q", ob.InstanceID)
	}
}

// TestInstanceID_Omitempty verifies that an empty InstanceID is omitted from
// the JSON output of all four structs (backward compatibility — existing
// consumers that don't know about InstanceID must see no change).
func TestInstanceID_Omitempty(t *testing.T) {
	p := Peer{Kind: PeerDirect, ID: "1"}
	if has, _ := jsonContains(jsonMarshal(t, p), `"instance_id"`); has {
		t.Fatal("Peer with empty InstanceID should omit instance_id from JSON")
	}

	si := SenderInfo{Platform: "slack"}
	if has, _ := jsonContains(jsonMarshal(t, si), `"instance_id"`); has {
		t.Fatal("SenderInfo with empty InstanceID should omit instance_id from JSON")
	}

	ib := InboundMessage{Channel: "slack", ChatID: "C1", SessionKey: "k"}
	if has, _ := jsonContains(jsonMarshal(t, ib), `"instance_id"`); has {
		t.Fatal("InboundMessage with empty InstanceID should omit instance_id from JSON")
	}

	ob := OutboundMessage{Channel: "slack", ChatID: "C1", Content: "hi"}
	if has, _ := jsonContains(jsonMarshal(t, ob), `"instance_id"`); has {
		t.Fatal("OutboundMessage with empty InstanceID should omit instance_id from JSON")
	}
}
