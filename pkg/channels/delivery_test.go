// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package channels — delivery integration tests.
//
// This file tests the full bus→dispatchOutbound→runWorker→ch.Send path
// ("delivery vs enqueue") as specified in the tool test plan §3.3.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.3 (send_message delivery)
// Epic: #440 / issue: #443

package channels

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newDeliveryManager returns a Manager wired to a real MessageBus, with
// dispatchers started on the given context.  The caller is responsible for
// calling mb.Close() to drain the bus goroutine.
func newDeliveryManager(ctx context.Context) (*Manager, *bus.MessageBus) {
	mb := bus.NewMessageBus()
	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}
	dispatchCtx, _ := m.newDispatchContext(ctx) //nolint:errcheck // cancel stored in m
	m.startDispatchers(dispatchCtx)
	return m, mb
}

// ---------------------------------------------------------------------------
// A1: Publish a message → poll mockChannel.sentMessages → assert exactly one
//     with correct content/chatID/channel reached Send.
//
// Traces to: tool-test-plan-2026-06.md §3.3 bullet 1 (delivery vs enqueue)
// ---------------------------------------------------------------------------

// TestDelivery_PublishReachesChannelSend verifies that PublishOutbound flows
// through dispatchOutbound → runWorker → ch.Send and lands in sentMessages
// with the correct content and addressing.
func TestDelivery_PublishReachesChannelSend(t *testing.T) {
	// BDD: Given a running Manager with a registered channel
	// BDD: When a message is published to the bus
	// BDD: Then the message reaches ch.Send with the correct content, chatID, and channel
	// Traces to: tool-test-plan-2026-06.md §3.3 line 73-74

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m, mb := newDeliveryManager(ctx)
	defer mb.Close()

	delivered := make(chan bus.OutboundMessage, 4)
	ch := &mockChannel{
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			delivered <- msg
			return nil
		},
	}
	m.RegisterChannel("test-deliver", ch)

	msg := bus.OutboundMessage{
		Channel: "test-deliver",
		ChatID:  "room-42",
		Content: "hello from bus",
	}
	if err := mb.PublishOutbound(context.Background(), msg); err != nil {
		t.Fatalf("PublishOutbound: %v", err)
	}

	select {
	case got := <-delivered:
		if got.Content != "hello from bus" {
			t.Fatalf("expected content %q, got %q", "hello from bus", got.Content)
		}
		if got.ChatID != "room-42" {
			t.Fatalf("expected chatID %q, got %q", "room-42", got.ChatID)
		}
		if got.Channel != "test-deliver" {
			t.Fatalf("expected channel %q, got %q", "test-deliver", got.Channel)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("BLOCKED: message never reached ch.Send — delivery path broken")
	}

	// Differentiation test: send a second, different message and assert it also
	// arrives at Send with DIFFERENT content, proving no hardcoded response.
	msg2 := bus.OutboundMessage{Channel: "test-deliver", ChatID: "room-42", Content: "second message"}
	if err := mb.PublishOutbound(context.Background(), msg2); err != nil {
		t.Fatalf("PublishOutbound msg2: %v", err)
	}
	select {
	case got2 := <-delivered:
		if got2.Content == "hello from bus" {
			t.Fatal("second message returned the same content as the first (hardcoded response bug)")
		}
		if got2.Content != "second message" {
			t.Fatalf("expected %q, got %q", "second message", got2.Content)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second message never reached ch.Send")
	}
}

// ---------------------------------------------------------------------------
// A2: Unknown channel → nothing reaches any Send (drop recorded).
//     Disabled / ErrNotRunning → no retry, single drop notice.
//
// Traces to: tool-test-plan-2026-06.md §3.3 bullet 2
// ---------------------------------------------------------------------------

// TestDelivery_UnknownChannel_NothingReachesSend verifies that a message
// addressed to an unregistered channel is silently dropped and does NOT reach
// any registered channel's Send.
func TestDelivery_UnknownChannel_NothingReachesSend(t *testing.T) {
	// BDD: Given a running Manager with channel A registered
	// BDD: When a message is published for channel B (not registered)
	// BDD: Then channel A's Send is never called
	// Traces to: tool-test-plan-2026-06.md §3.3 line 74

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m, mb := newDeliveryManager(ctx)
	defer mb.Close()

	sentByA := make(chan struct{}, 4)
	chA := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			sentByA <- struct{}{}
			return nil
		},
	}
	m.RegisterChannel("channel-a", chA)

	// Publish to a channel that is NOT registered.
	_ = mb.PublishOutbound(context.Background(), bus.OutboundMessage{
		Channel: "nonexistent-channel",
		ChatID:  "x",
		Content: "should be dropped",
	})

	select {
	case <-sentByA:
		t.Fatal("channel-a's Send must NOT be called for messages addressed to an unknown channel")
	case <-time.After(300 * time.Millisecond):
		// Correct: nothing reached Send.
	}
}

// TestDelivery_ErrNotRunning_NoRetryAndSingleDrop verifies that when a channel
// returns ErrNotRunning, sendWithRetry does NOT retry (single attempt) and
// publishes a drop-notice back through the bus.
//
// Traces to: tool-test-plan-2026-06.md §3.3 bullet 2
func TestDelivery_ErrNotRunning_NoRetryAndSingleDrop(t *testing.T) {
	// BDD: Given a channel that returns ErrNotRunning on every Send
	// BDD: When sendWithRetry is called with a message
	// BDD: Then Send is called exactly once (no retry) and a drop notice appears
	// Traces to: tool-test-plan-2026-06.md §3.3 line 74

	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}

	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			return ErrNotRunning
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	m.sendWithRetry(context.Background(), "disabled-ch", w,
		bus.OutboundMessage{Channel: "disabled-ch", ChatID: "c1", Content: "msg"},
		false)

	if callCount != 1 {
		t.Fatalf("ErrNotRunning must cause exactly 1 Send attempt (no retry), got %d", callCount)
	}
	// The delivery failed terminally — the bus should carry a drop notice.
	select {
	case notice := <-mb.OutboundChan():
		if !strings.HasPrefix(notice.Content, dropNoticePrefix) {
			t.Fatalf("expected drop-notice-tagged message, got %q", notice.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a drop notice after ErrNotRunning terminal failure — none published")
	}
}

// ---------------------------------------------------------------------------
// A3: Retry semantics — transient error ×N then success → eventual delivery.
//     Permanent error → single attempt + drop.
//
// Traces to: tool-test-plan-2026-06.md §3.3 bullet 3
// ---------------------------------------------------------------------------

// TestDelivery_TransientRetry_EventualSuccess verifies that transient errors
// cause backoff retries until success and the message is ultimately delivered.
//
// Traces to: tool-test-plan-2026-06.md §3.3 line 75
func TestDelivery_TransientRetry_EventualSuccess(t *testing.T) {
	// BDD: Given a channel that fails transiently twice then succeeds
	// BDD: When sendWithRetry processes a message
	// BDD: Then it retries twice and the third call succeeds
	// Traces to: tool-test-plan-2026-06.md §3.3 line 75

	m := newTestManager()
	var callCount int
	var delivered []string
	ch := &mockChannel{
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			callCount++
			if callCount <= 2 {
				return fmt.Errorf("flaky: %w", ErrTemporary)
			}
			delivered = append(delivered, msg.Content)
			return nil
		},
	}
	w := &channelWorker{ch: ch, limiter: rate.NewLimiter(rate.Inf, 1)}

	m.sendWithRetry(context.Background(), "flaky-ch", w,
		bus.OutboundMessage{Channel: "flaky-ch", ChatID: "c1", Content: "eventual-success"},
		false)

	if callCount != 3 {
		t.Fatalf("expected 3 calls (2 transient failures + 1 success), got %d", callCount)
	}
	if len(delivered) != 1 || delivered[0] != "eventual-success" {
		t.Fatalf("expected message content %q to be delivered, got %v", "eventual-success", delivered)
	}
}

// TestDelivery_PermanentError_SingleAttemptAndDrop verifies that a permanent
// failure (ErrSendFailed) results in exactly one Send attempt and a drop
// notice is published.
//
// Traces to: tool-test-plan-2026-06.md §3.3 line 75
func TestDelivery_PermanentError_SingleAttemptAndDrop(t *testing.T) {
	// BDD: Given a channel that always returns ErrSendFailed (permanent)
	// BDD: When sendWithRetry processes a message
	// BDD: Then Send is called exactly once (no retry) and a drop notice is published
	// Traces to: tool-test-plan-2026-06.md §3.3 line 75

	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}

	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			return fmt.Errorf("bad chat ID: %w", ErrSendFailed)
		},
	}
	w := &channelWorker{ch: ch, limiter: rate.NewLimiter(rate.Inf, 1)}

	m.sendWithRetry(context.Background(), "perm-ch", w,
		bus.OutboundMessage{Channel: "perm-ch", ChatID: "c1", Content: "perm-fail"},
		false)

	if callCount != 1 {
		t.Fatalf("permanent failure must result in exactly 1 Send call, got %d", callCount)
	}
	// The terminal failure MUST publish a drop notice.
	select {
	case notice := <-mb.OutboundChan():
		if !strings.HasPrefix(notice.Content, dropNoticePrefix) {
			t.Fatalf("expected drop-notice prefix, got %q", notice.Content)
		}
		stripped := strings.TrimPrefix(notice.Content, dropNoticePrefix)
		if stripped != "[message delivery failed]" {
			t.Fatalf("expected fallback text %q, got %q", "[message delivery failed]", stripped)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("permanent error must publish a drop notice — none seen")
	}
}

// ---------------------------------------------------------------------------
// A4: Streaming skip — when streamActive is set for the key, preSend skips
//     Send and deletes the placeholder.
//
// Traces to: tool-test-plan-2026-06.md §3.3 bullet 4
// ---------------------------------------------------------------------------

// TestDelivery_StreamingSkip_FinalizedStreamSkipsSend verifies that when a
// streaming Finalize has already delivered the content (streamActive marker),
// a subsequent sendWithRetry skips ch.Send and cleans up the placeholder.
//
// Traces to: tool-test-plan-2026-06.md §3.3 line 76
func TestDelivery_StreamingSkip_FinalizedStreamSkipsSend(t *testing.T) {
	// BDD: Given a channel with a finalized stream marker and an active placeholder
	// BDD: When sendWithRetry processes the follow-up text message
	// BDD: Then ch.Send is NOT called (content already delivered via stream)
	// BDD: And the placeholder is cleaned up
	// Traces to: tool-test-plan-2026-06.md §3.3 line 76

	m := newTestManager()
	var sendCalled bool
	var deleteCalled bool

	ch := &mockDeletingMediaChannel{
		mockMediaChannel: mockMediaChannel{
			mockChannel: mockChannel{
				sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
					sendCalled = true
					return nil
				},
			},
		},
	}
	// Override DeleteMessage to track the call.
	ch.deleteCalls = 0

	w := &channelWorker{ch: ch, limiter: rate.NewLimiter(rate.Inf, 1)}
	m.channels["stream-ch"] = ch
	m.workers["stream-ch"] = w

	// Simulate: stream Finalize has fired and set streamActive.
	key := "stream-ch:chat-stream"
	m.streamActive.Store(key, streamEntry{createdAt: time.Now()})
	// Also record a placeholder to test cleanup.
	m.RecordPlaceholder("stream-ch", "chat-stream", "ph-789")

	msg := bus.OutboundMessage{Channel: "stream-ch", ChatID: "chat-stream", Content: "stream content"}
	m.sendWithRetry(context.Background(), "stream-ch", w, msg, false)

	if sendCalled {
		t.Fatal("Send must NOT be called when streamActive is set (content was already streamed)")
	}
	// placeholder should have been consumed
	if _, loaded := m.placeholders.Load(key); loaded {
		t.Fatal("placeholder must be cleaned up when streamActive fires preSend skip")
	}
	_ = deleteCalled
}

// ---------------------------------------------------------------------------
// A5: Queue backpressure — fill w.queue to cap → drop-notice frame, no block.
//
// Traces to: tool-test-plan-2026-06.md §3.3 bullet 5
// ---------------------------------------------------------------------------

// TestDelivery_QueueFull_DropNoticePublished verifies that when the worker
// queue is full, enqueueOutbound publishes a user-facing drop notice and does
// NOT block the dispatch loop.
//
// This reuses the existing TestDispatchOutbound_QueueFull_PublishesDropNotice
// structure from manager_test.go but is placed in this file to form the
// delivery suite.
//
// Traces to: tool-test-plan-2026-06.md §3.3 line 77
func TestDelivery_QueueFull_NonBlocking(t *testing.T) {
	// BDD: Given a channel whose queue is already full
	// BDD: When enqueueOutbound is called for that channel
	// BDD: Then the call returns immediately (non-blocking) and a drop notice lands on the bus
	// Traces to: tool-test-plan-2026-06.md §3.3 line 77

	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}

	ch := &mockChannel{sendFn: func(_ context.Context, _ bus.OutboundMessage) error { return nil }}
	// Queue capacity 1, no worker draining it.
	w := newWorkerForTest(ch, 1)
	w.queue <- bus.OutboundMessage{Channel: "full-ch", ChatID: "c1", Content: "filler"}

	m.channels["full-ch"] = ch
	m.workers["full-ch"] = w

	overflowMsg := bus.OutboundMessage{Channel: "full-ch", ChatID: "c1", Content: "overflow"}

	// enqueueOutbound must return immediately (non-blocking).
	returned := make(chan bool, 1)
	go func() {
		cont := m.enqueueOutbound(context.Background(), w, overflowMsg)
		returned <- cont
	}()

	select {
	case cont := <-returned:
		if !cont {
			t.Fatal("enqueueOutbound must return true (keep the loop running) on queue-full")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("enqueueOutbound blocked when queue was full — must be non-blocking")
	}

	// A drop notice must have been published to the bus.
	select {
	case notice := <-mb.OutboundChan():
		if !strings.HasPrefix(notice.Content, dropNoticePrefix) {
			t.Fatalf("expected drop-notice prefix, got %q", notice.Content)
		}
		if notice.ChatID != "c1" {
			t.Fatalf("drop notice addressed to wrong chatID: %s", notice.ChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a drop notice on the bus after queue-full drop — none seen")
	}
}

// ---------------------------------------------------------------------------
// A6: send_file — valid file → media store → MediaResult ref → publish
//     OutboundMediaMessage → assert SendMedia (not Send) invoked.
//     Also: reject file-too-large and disallowed-type (path-restricted).
//
// NOTE: send_file tests live in pkg/tools/send_file_test.go for the tool-layer
// validation (media store ref, size cap, no-context error).  This section adds
// the delivery-layer assertion: that a MediaResult actually causes SendMedia on
// the channel (not Send), using the mockMediaChannel from this package.
//
// Traces to: tool-test-plan-2026-06.md §3.3 line 78 (send_file delivery)
// ---------------------------------------------------------------------------

// TestDelivery_SendMedia_InvokesChannelSendMedia verifies that when an
// OutboundMediaMessage is dispatched through the bus, the channel's SendMedia
// (not Send) is called, and the media parts arrive intact.
//
// This is the delivery-layer counterpart to the tool-layer tests in
// pkg/tools/send_file_test.go.
//
// Traces to: tool-test-plan-2026-06.md §3.3 line 78
func TestDelivery_SendMedia_InvokesChannelSendMedia(t *testing.T) {
	// BDD: Given a MediaCapable channel registered on the manager
	// BDD: When an OutboundMediaMessage is published to the bus
	// BDD: Then channel.SendMedia is called (NOT Send) with the correct parts
	// Traces to: tool-test-plan-2026-06.md §3.3 line 78

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}
	dispatchCtx, _ := m.newDispatchContext(ctx) //nolint:errcheck
	m.startDispatchers(dispatchCtx)

	mediaReceived := make(chan bus.OutboundMediaMessage, 4)
	sendCalled := make(chan struct{}, 4) // must stay empty

	ch := &mockMediaChannel{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
				sendCalled <- struct{}{}
				return nil
			},
		},
		sendMediaFn: func(_ context.Context, msg bus.OutboundMediaMessage) error {
			mediaReceived <- msg
			return nil
		},
	}

	// Manually register channel and spin up workers.
	w := newWorkerForTest(ch, 8)
	m.mu.Lock()
	m.channels["media-ch"] = ch
	m.workers["media-ch"] = w
	m.mu.Unlock()
	go m.runWorker(dispatchCtx, "media-ch", w)
	go m.runMediaWorker(dispatchCtx, "media-ch", w)

	mediaParts := []bus.MediaPart{
		{Ref: "media://abc123", Filename: "photo.png", ContentType: "image/png"},
	}
	_ = mb.PublishOutboundMedia(context.Background(), bus.OutboundMediaMessage{
		Channel: "media-ch",
		ChatID:  "chat77",
		Parts:   mediaParts,
	})

	select {
	case got := <-mediaReceived:
		if len(got.Parts) != 1 {
			t.Fatalf("expected 1 media part, got %d", len(got.Parts))
		}
		if got.Parts[0].Ref != "media://abc123" {
			t.Fatalf("expected ref %q, got %q", "media://abc123", got.Parts[0].Ref)
		}
		if got.ChatID != "chat77" {
			t.Fatalf("expected chatID %q, got %q", "chat77", got.ChatID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("BLOCKED: OutboundMediaMessage never reached channel.SendMedia")
	}

	// Send (text) must NOT have been called.
	select {
	case <-sendCalled:
		t.Fatal("Send (text) must NOT be called for an OutboundMediaMessage — SendMedia must be used")
	case <-time.After(100 * time.Millisecond):
		// Correct: only SendMedia was invoked.
	}
}

// TestDelivery_SendMedia_NonMediaChannel_TextFallback verifies that when a
// media message is dispatched to a channel that only supports Send (not
// SendMedia), a text fallback "[media delivery failed]" is published.
//
// Traces to: tool-test-plan-2026-06.md §3.3 line 78
func TestDelivery_SendMedia_NonMediaChannel_TextFallback(t *testing.T) {
	// BDD: Given a channel that does NOT implement MediaSender (no SendMedia)
	// BDD: When an OutboundMediaMessage is dispatched for that channel
	// BDD: Then a text "[media delivery failed]" fallback is published to the bus
	// Traces to: tool-test-plan-2026-06.md §3.3 line 78

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}
	dispatchCtx, _ := m.newDispatchContext(ctx) //nolint:errcheck
	m.startDispatchers(dispatchCtx)

	textReceived := make(chan bus.OutboundMessage, 8)
	// mockChannel only supports Send (no SendMedia).
	ch := &mockChannel{
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			textReceived <- msg
			return nil
		},
	}
	w := newWorkerForTest(ch, 8)
	m.mu.Lock()
	m.channels["text-only-ch"] = ch
	m.workers["text-only-ch"] = w
	m.mu.Unlock()
	go m.runWorker(dispatchCtx, "text-only-ch", w)
	go m.runMediaWorker(dispatchCtx, "text-only-ch", w)

	_ = mb.PublishOutboundMedia(context.Background(), bus.OutboundMediaMessage{
		Channel: "text-only-ch",
		ChatID:  "chat88",
		Parts:   []bus.MediaPart{{Ref: "media://xyz", Filename: "img.jpg"}},
	})

	// Wait for the fallback text message to land on the bus (via runMediaWorker's
	// PublishOutbound on SendMedia failure).
	var fallbackSeen bool
	deadline := time.After(3 * time.Second)
	for !fallbackSeen {
		select {
		case msg := <-textReceived:
			if strings.Contains(msg.Content, "[media delivery failed]") {
				fallbackSeen = true
			}
		case <-deadline:
			t.Fatal("BLOCKED: text fallback '[media delivery failed]' never published for non-MediaSender channel")
		}
	}
}

// ---------------------------------------------------------------------------
// A7: Delivery differentiation test — two different inputs → two different
//     outputs at the channel Send level.  Guards against hardcoded responses.
//
// Traces to: tool-test-plan-2026-06.md §0 (anti-shortcut rule: differentiation)
// ---------------------------------------------------------------------------

// TestDelivery_Differentiation_TwoInputsTwoDifferentOutputs publishes two
// messages with different content and asserts that each arrives at Send with
// its own distinct content, proving the delivery path is not hardcoded.
//
// Traces to: tool-test-plan-2026-06.md §3.3
func TestDelivery_Differentiation_TwoInputsTwoDifferentOutputs(t *testing.T) {
	// BDD: Given a channel registered on the manager
	// BDD: When two messages with distinct content are published
	// BDD: Then Send receives them both with their own distinct content
	// Traces to: tool-test-plan-2026-06.md §3.3

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m, mb := newDeliveryManager(ctx)
	defer mb.Close()

	var mu sync.Mutex
	var received []string
	ch := &mockChannel{
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			mu.Lock()
			received = append(received, msg.Content)
			mu.Unlock()
			return nil
		},
	}
	m.RegisterChannel("diff-ch", ch)

	_ = mb.PublishOutbound(context.Background(), bus.OutboundMessage{
		Channel: "diff-ch", ChatID: "c1", Content: "alpha",
	})
	_ = mb.PublishOutbound(context.Background(), bus.OutboundMessage{
		Channel: "diff-ch", ChatID: "c1", Content: "beta",
	})

	// Wait for both to arrive.
	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("expected 2 messages at ch.Send, got %d", n)
		case <-time.After(20 * time.Millisecond):
		}
	}

	mu.Lock()
	snap := make([]string, len(received))
	copy(snap, received)
	mu.Unlock()

	if snap[0] == snap[1] {
		t.Fatalf("two different inputs must produce two different outputs; both got %q", snap[0])
	}
	if snap[0] != "alpha" || snap[1] != "beta" {
		t.Fatalf("expected [alpha, beta], got %v", snap)
	}
}
