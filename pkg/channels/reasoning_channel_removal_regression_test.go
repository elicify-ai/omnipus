// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// reasoning_channel_removal_regression_test.go is the regression-shape guard
// for the D1 deletion of the messenger reasoning channel (spec
// docs/internal/specs/thinking-reasoning-spec.md, US-9 acceptance scenario 3,
// Group F scenario "Normal messenger publishing is unaffected").
//
// What it pins: an ordinary message published to a channel built exactly the
// way every connector builds its base after the deletion — NewBaseChannel
// with the standard option set and NO reasoning wiring — still flows the real
// bus -> dispatchOutbound -> runWorker -> ch.Send path and lands with the
// exact content, chatID and channel it was published with. If the deletion
// ever breaks ordinary delivery (a botched options list in a connector
// constructor, a config-decode regression, a broken base registration), this
// test fails long before a user does.
//
// This test is green TODAY, before the deletion lands, by design: ordinary
// publishing works today and must keep working after. It is a regression
// guard, not a new-behaviour RED (small-size exemption from red-before-green
// applies; the RED evidence for the deletion itself lives in
// pkg/config/reasoning_channel_zero_trace_test.go and the
// check-no-reasoning-channel guard).
package channels

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// publishingChannel embeds the real production BaseChannel the same way every
// connector does and records what arrives at Send. It implements nothing the
// deletion touched.
type publishingChannel struct {
	*BaseChannel

	mu        sync.Mutex
	delivered []bus.OutboundMessage
}

func (c *publishingChannel) Start(_ context.Context) error { return nil }
func (c *publishingChannel) Stop(_ context.Context) error  { return nil }

func (c *publishingChannel) Send(_ context.Context, msg bus.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delivered = append(c.delivered, msg)
	return nil
}

// TestNormalPublishUnaffectedByReasoningChannelRemoval proves an ordinary
// messenger reply still delivers, byte-exactly, on a channel wired the
// post-deletion way. Traces to: US-9 acceptance scenario 3; Group F scenario
// "Normal messenger publishing is unaffected".
func TestNormalPublishUnaffectedByReasoningChannelRemoval(t *testing.T) {
	// BDD: Given a channel built with the standard option set and no
	//      reasoning wiring (the post-deletion construction shape)
	// BDD: When an ordinary message is published through the real bus
	// BDD: Then it reaches Send with its exact content, chatID and channel
	// Traces to: docs/internal/specs/thinking-reasoning-spec.md US-9 AS3

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}
	dispatchCtx, _ := m.newDispatchContext(ctx)
	m.startDispatchers(dispatchCtx)

	// The post-deletion construction shape: standard options only. Today the
	// connectors additionally pass the reasoning option with the config's
	// (normally empty) id; omitting it is behaviourally identical for normal
	// publishing, which never read it.
	ch := &publishingChannel{
		BaseChannel: NewBaseChannel(
			"regression-ch",
			struct{}{},
			mb,
			nil,
			WithMaxMessageLength(4000),
		),
	}
	w := newWorkerForTest(ch, 8)
	m.mu.Lock()
	m.channels["regression-ch"] = ch
	m.workers["regression-ch"] = w
	m.mu.Unlock()
	go m.runWorker(dispatchCtx, "regression-ch", w, m.config)

	msg := bus.OutboundMessage{
		Channel: "regression-ch",
		ChatID:  "room-42",
		Content: "ordinary connector reply",
	}
	if err := mb.PublishOutbound(context.Background(), msg); err != nil {
		t.Fatalf("PublishOutbound: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		ch.mu.Lock()
		n := len(ch.delivered)
		ch.mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("BLOCKED: ordinary message never reached Send after the " +
				"reasoning-channel removal — delivery path broken")
		case <-time.After(10 * time.Millisecond):
		}
	}

	ch.mu.Lock()
	got := ch.delivered[0]
	ch.mu.Unlock()

	if got.Channel != "regression-ch" {
		t.Fatalf("expected channel %q, got %q", "regression-ch", got.Channel)
	}
	if got.ChatID != "room-42" {
		t.Fatalf("expected chatID %q, got %q", "room-42", got.ChatID)
	}
	if got.Content != "ordinary connector reply" {
		t.Fatalf("expected content %q, got %q", "ordinary connector reply", got.Content)
	}
}

// TestNormalPublishDifferentiatedContentAfterRemoval proves the delivered
// content tracks what was published — two distinct messages arrive with their
// own distinct contents, so the delivery path cannot pass this test while
// replying with a hardcoded response.
func TestNormalPublishDifferentiatedContentAfterRemoval(t *testing.T) {
	// BDD: Given the same post-deletion channel wiring
	// BDD: When two messages with distinct contents are published
	// BDD: Then Send receives both, each with its own distinct content
	// Traces to: docs/internal/specs/thinking-reasoning-spec.md US-9 AS3

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}
	dispatchCtx, _ := m.newDispatchContext(ctx)
	m.startDispatchers(dispatchCtx)

	ch := &publishingChannel{
		BaseChannel: NewBaseChannel(
			"regression-ch",
			struct{}{},
			mb,
			nil,
			WithMaxMessageLength(4000),
		),
	}
	w := newWorkerForTest(ch, 8)
	m.mu.Lock()
	m.channels["regression-ch"] = ch
	m.workers["regression-ch"] = w
	m.mu.Unlock()
	go m.runWorker(dispatchCtx, "regression-ch", w, m.config)

	_ = mb.PublishOutbound(context.Background(), bus.OutboundMessage{
		Channel: "regression-ch", ChatID: "c1", Content: "alpha reply",
	})
	_ = mb.PublishOutbound(context.Background(), bus.OutboundMessage{
		Channel: "regression-ch", ChatID: "c1", Content: "beta reply",
	})

	deadline := time.After(3 * time.Second)
	for {
		ch.mu.Lock()
		n := len(ch.delivered)
		ch.mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("expected 2 messages at Send, got %d", n)
		case <-time.After(10 * time.Millisecond):
		}
	}

	ch.mu.Lock()
	snap := make([]string, len(ch.delivered))
	for i, msg := range ch.delivered {
		snap[i] = msg.Content
	}
	ch.mu.Unlock()

	if snap[0] == snap[1] {
		t.Fatalf("two different inputs must produce two different outputs; both got %q", snap[0])
	}
	if snap[0] != "alpha reply" || snap[1] != "beta reply" {
		t.Fatalf("expected [alpha reply, beta reply], got %v", snap)
	}
}
