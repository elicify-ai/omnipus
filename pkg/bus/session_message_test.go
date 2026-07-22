// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package bus

import (
	"context"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestSessionMessage_RoundTripOverBus proves ADR-053 S3: a typed
// SessionMessage published on the bus's dedicated session-message channel
// is delivered intact — same kind, same payload — to a consumer, exactly
// like the existing InboundMessage/OutboundMessage channels this file
// already covers.
func TestSessionMessage_RoundTripOverBus(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	ctx := context.Background()

	var sm generated.SessionMessage
	if err := sm.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId:      "msg-1",
		SessionId:      "child-1",
		CreatedAt:      time.Now(),
		Depth:          1,
		SenderIdentity: "child-agent",
		Text:           "halfway done",
	}); err != nil {
		t.Fatalf("FromSessionMessageProgress failed: %v", err)
	}

	evt := SessionMessageEvent{
		TargetSessionID: "parent-1",
		Message:         sm,
	}

	if err := mb.PublishSessionMessage(ctx, evt); err != nil {
		t.Fatalf("PublishSessionMessage failed: %v", err)
	}

	got, ok := <-mb.SessionMessageChan()
	if !ok {
		t.Fatal("SessionMessageChan returned ok=false")
	}
	if got.TargetSessionID != "parent-1" {
		t.Errorf("TargetSessionID = %q, want %q", got.TargetSessionID, "parent-1")
	}
	kind, err := got.Message.Discriminator()
	if err != nil {
		t.Fatalf("Discriminator failed: %v", err)
	}
	if kind != "progress" {
		t.Errorf("kind = %q, want %q", kind, "progress")
	}
	progress, err := got.Message.AsSessionMessageProgress()
	if err != nil {
		t.Fatalf("AsSessionMessageProgress failed: %v", err)
	}
	if progress.Text != "halfway done" {
		t.Errorf("Text = %q, want %q", progress.Text, "halfway done")
	}
	if progress.MessageId != "msg-1" {
		t.Errorf("MessageId = %q, want %q", progress.MessageId, "msg-1")
	}
}

// TestSessionMessage_PublishAfterClose proves the session-message channel
// obeys the SAME closed-bus contract as inbound/outbound (ErrBusClosed, no
// panic on send-after-close) — it is a fourth channel on the SAME struct,
// not a separately-lifecycled mechanism.
func TestSessionMessage_PublishAfterClose(t *testing.T) {
	mb := NewMessageBus()
	mb.Close()

	var sm generated.SessionMessage
	if err := sm.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: "msg-2", SessionId: "child-1", CreatedAt: time.Now(), SenderIdentity: "child-agent", Text: "x",
	}); err != nil {
		t.Fatalf("FromSessionMessageProgress failed: %v", err)
	}

	err := mb.PublishSessionMessage(context.Background(), SessionMessageEvent{TargetSessionID: "p", Message: sm})
	if err != ErrBusClosed {
		t.Errorf("expected ErrBusClosed after Close(), got: %v", err)
	}
}
