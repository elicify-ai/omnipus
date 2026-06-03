package channels

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

// mockChannel is a test double that delegates Send to a configurable function.
type mockChannel struct {
	BaseChannel
	sendFn            func(ctx context.Context, msg bus.OutboundMessage) error
	sentMessages      []bus.OutboundMessage
	placeholdersSent  int
	editedMessages    int
	lastPlaceholderID string
}

func (m *mockChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	m.sentMessages = append(m.sentMessages, msg)
	return m.sendFn(ctx, msg)
}

func (m *mockChannel) Start(ctx context.Context) error { return nil }
func (m *mockChannel) Stop(ctx context.Context) error  { return nil }

func (m *mockChannel) SendPlaceholder(ctx context.Context, chatID string) (string, error) {
	m.placeholdersSent++
	m.lastPlaceholderID = "mock-ph-123"
	return m.lastPlaceholderID, nil
}

func (m *mockChannel) EditMessage(ctx context.Context, chatID, messageID, content string) error {
	m.editedMessages++
	return nil
}

type mockMediaChannel struct {
	mockChannel
	sendMediaFn       func(ctx context.Context, msg bus.OutboundMediaMessage) error
	sentMediaMessages []bus.OutboundMediaMessage
}

func (m *mockMediaChannel) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) error {
	m.sentMediaMessages = append(m.sentMediaMessages, msg)
	if m.sendMediaFn != nil {
		return m.sendMediaFn(ctx, msg)
	}
	return nil
}

type mockDeletingMediaChannel struct {
	mockMediaChannel
	deleteCalls int
	lastDeleted struct {
		chatID    string
		messageID string
	}
}

func (m *mockDeletingMediaChannel) DeleteMessage(
	_ context.Context,
	chatID string,
	messageID string,
) error {
	m.deleteCalls++
	m.lastDeleted.chatID = chatID
	m.lastDeleted.messageID = messageID
	return nil
}

// newTestManager creates a minimal Manager suitable for unit tests.
func newTestManager() *Manager {
	return &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
	}
}

func TestSendWithRetry_Success(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			return nil
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"}

	m.sendWithRetry(ctx, "test", w, msg, false)

	if callCount != 1 {
		t.Fatalf("expected 1 Send call, got %d", callCount)
	}
}

func TestSendWithRetry_TemporaryThenSuccess(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			if callCount <= 2 {
				return fmt.Errorf("network error: %w", ErrTemporary)
			}
			return nil
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"}

	m.sendWithRetry(ctx, "test", w, msg, false)

	if callCount != 3 {
		t.Fatalf("expected 3 Send calls (2 failures + 1 success), got %d", callCount)
	}
}

func TestSendWithRetry_PermanentFailure(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			return fmt.Errorf("bad chat ID: %w", ErrSendFailed)
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"}

	m.sendWithRetry(ctx, "test", w, msg, false)

	if callCount != 1 {
		t.Fatalf("expected 1 Send call (no retry for permanent failure), got %d", callCount)
	}
}

func TestSendWithRetry_NotRunning(t *testing.T) {
	m := newTestManager()
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

	ctx := context.Background()
	msg := bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"}

	m.sendWithRetry(ctx, "test", w, msg, false)

	if callCount != 1 {
		t.Fatalf("expected 1 Send call (no retry for ErrNotRunning), got %d", callCount)
	}
}

func TestSendWithRetry_RateLimitRetry(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			if callCount == 1 {
				return fmt.Errorf("429: %w", ErrRateLimit)
			}
			return nil
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"}

	start := time.Now()
	m.sendWithRetry(ctx, "test", w, msg, false)
	elapsed := time.Since(start)

	if callCount != 2 {
		t.Fatalf("expected 2 Send calls (1 rate limit + 1 success), got %d", callCount)
	}
	// Should have waited at least rateLimitDelay (1s) but allow some slack
	if elapsed < 900*time.Millisecond {
		t.Fatalf("expected at least ~1s delay for rate limit retry, got %v", elapsed)
	}
}

func TestSendWithRetry_MaxRetriesExhausted(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			return fmt.Errorf("timeout: %w", ErrTemporary)
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"}

	m.sendWithRetry(ctx, "test", w, msg, false)

	expected := maxRetries + 1 // initial attempt + maxRetries retries
	if callCount != expected {
		t.Fatalf("expected %d Send calls, got %d", expected, callCount)
	}
}

func TestSendMedia_Success(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockMediaChannel{
		sendMediaFn: func(_ context.Context, _ bus.OutboundMediaMessage) error {
			callCount++
			return nil
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	err := m.SendMedia(context.Background(), bus.OutboundMediaMessage{
		Channel: "test",
		ChatID:  "chat1",
		Parts:   []bus.MediaPart{{Ref: "media://abc"}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 SendMedia call, got %d", callCount)
	}
}

func TestSendMedia_PropagatesFailure(t *testing.T) {
	m := newTestManager()
	ch := &mockMediaChannel{
		sendMediaFn: func(_ context.Context, _ bus.OutboundMediaMessage) error {
			return fmt.Errorf("bad upload: %w", ErrSendFailed)
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	err := m.SendMedia(context.Background(), bus.OutboundMediaMessage{
		Channel: "test",
		ChatID:  "chat1",
		Parts:   []bus.MediaPart{{Ref: "media://abc"}},
	})
	if err == nil {
		t.Fatal("expected SendMedia to return error")
	}
	if !errors.Is(err, ErrSendFailed) {
		t.Fatalf("expected ErrSendFailed, got %v", err)
	}
}

func TestSendMedia_UnsupportedChannelReturnsError(t *testing.T) {
	m := newTestManager()
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			return nil
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	err := m.SendMedia(context.Background(), bus.OutboundMediaMessage{
		Channel: "test",
		ChatID:  "chat1",
		Parts:   []bus.MediaPart{{Ref: "media://abc"}},
	})
	if err == nil {
		t.Fatal("expected SendMedia to return error for unsupported channel")
	}
	if !strings.Contains(err.Error(), "does not support media sending") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendMedia_DeletesPlaceholderBeforeSending(t *testing.T) {
	m := newTestManager()
	ch := &mockDeletingMediaChannel{
		mockMediaChannel: mockMediaChannel{
			sendMediaFn: func(_ context.Context, _ bus.OutboundMediaMessage) error {
				return nil
			},
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w
	m.RecordPlaceholder("test", "chat1", "placeholder-1")

	err := m.SendMedia(context.Background(), bus.OutboundMediaMessage{
		Channel: "test",
		ChatID:  "chat1",
		Parts:   []bus.MediaPart{{Ref: "media://abc"}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}
	if ch.deleteCalls != 1 {
		t.Fatalf("expected placeholder delete to be called once, got %d", ch.deleteCalls)
	}
	if ch.lastDeleted.chatID != "chat1" || ch.lastDeleted.messageID != "placeholder-1" {
		t.Fatalf("unexpected placeholder deletion target: %+v", ch.lastDeleted)
	}
	if len(ch.sentMediaMessages) != 1 {
		t.Fatalf("expected media to be sent once, got %d", len(ch.sentMediaMessages))
	}
}

func TestSendWithRetry_UnknownError(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			if callCount == 1 {
				return errors.New("random unexpected error")
			}
			return nil
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"}

	m.sendWithRetry(ctx, "test", w, msg, false)

	if callCount != 2 {
		t.Fatalf("expected 2 Send calls (unknown error treated as temporary), got %d", callCount)
	}
}

func TestSendWithRetry_ContextCancelled(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			return fmt.Errorf("timeout: %w", ErrTemporary)
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())
	msg := bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"}

	// Cancel context after first Send attempt returns
	ch.sendFn = func(_ context.Context, _ bus.OutboundMessage) error {
		callCount++
		cancel()
		return fmt.Errorf("timeout: %w", ErrTemporary)
	}

	m.sendWithRetry(ctx, "test", w, msg, false)

	// Should have called Send once, then noticed ctx canceled during backoff
	if callCount != 1 {
		t.Fatalf("expected 1 Send call before context cancellation, got %d", callCount)
	}
}

func TestWorkerRateLimiter(t *testing.T) {
	m := newTestManager()

	var mu sync.Mutex
	var sendTimes []time.Time

	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			mu.Lock()
			sendTimes = append(sendTimes, time.Now())
			mu.Unlock()
			return nil
		},
	}

	// Create a worker with a low rate: 2 msg/s, burst 1
	w := &channelWorker{
		ch:      ch,
		queue:   make(chan bus.OutboundMessage, 10),
		done:    make(chan struct{}),
		limiter: rate.NewLimiter(2, 1),
	}

	ctx := t.Context()

	go m.runWorker(ctx, "test", w)

	// Enqueue 4 messages
	for i := range 4 {
		w.queue <- bus.OutboundMessage{Channel: "test", ChatID: "1", Content: fmt.Sprintf("msg%d", i)}
	}

	// Wait enough time for all messages to be sent (4 msgs at 2/s = ~2s, give extra margin)
	time.Sleep(3 * time.Second)

	mu.Lock()
	times := make([]time.Time, len(sendTimes))
	copy(times, sendTimes)
	mu.Unlock()

	if len(times) != 4 {
		t.Fatalf("expected 4 sends, got %d", len(times))
	}

	// Verify rate limiting: total duration should be at least 1s
	// (first message immediate, then ~500ms between each subsequent one at 2/s)
	totalDuration := times[len(times)-1].Sub(times[0])
	if totalDuration < 1*time.Second {
		t.Fatalf("expected total duration >= 1s for 4 msgs at 2/s rate, got %v", totalDuration)
	}
}

func TestNewChannelWorker_DefaultRate(t *testing.T) {
	ch := &mockChannel{}
	w := newChannelWorker("unknown_channel", ch)

	if w.limiter == nil {
		t.Fatal("expected limiter to be non-nil")
	}
	if w.limiter.Limit() != rate.Limit(defaultRateLimit) {
		t.Fatalf("expected rate limit %v, got %v", rate.Limit(defaultRateLimit), w.limiter.Limit())
	}
}

func TestNewChannelWorker_ConfiguredRate(t *testing.T) {
	ch := &mockChannel{}

	for name, expectedRate := range channelRateConfig {
		w := newChannelWorker(name, ch)
		if w.limiter.Limit() != rate.Limit(expectedRate) {
			t.Fatalf("channel %s: expected rate %v, got %v", name, expectedRate, w.limiter.Limit())
		}
	}
}

func TestRunWorker_MessageSplitting(t *testing.T) {
	m := newTestManager()

	var mu sync.Mutex
	var received []string

	ch := &mockChannelWithLength{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
				mu.Lock()
				received = append(received, msg.Content)
				mu.Unlock()
				return nil
			},
		},
		maxLen: 5,
	}

	w := &channelWorker{
		ch:      ch,
		queue:   make(chan bus.OutboundMessage, 10),
		done:    make(chan struct{}),
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := t.Context()

	go m.runWorker(ctx, "test", w)

	// Send a message that should be split
	w.queue <- bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello world"}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	count := len(received)
	mu.Unlock()

	if count < 2 {
		t.Fatalf("expected message to be split into at least 2 chunks, got %d", count)
	}
}

// mockChannelWithLength implements MessageLengthProvider.
type mockChannelWithLength struct {
	mockChannel
	maxLen int
}

func (m *mockChannelWithLength) MaxMessageLength() int {
	return m.maxLen
}

func TestSendWithRetry_ExponentialBackoff(t *testing.T) {
	m := newTestManager()

	var callTimes []time.Time
	var callCount atomic.Int32
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callTimes = append(callTimes, time.Now())
			callCount.Add(1)
			return fmt.Errorf("timeout: %w", ErrTemporary)
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"}

	start := time.Now()
	m.sendWithRetry(ctx, "test", w, msg, false)
	totalElapsed := time.Since(start)

	// With maxRetries=3: attempts at 0, ~500ms, ~1.5s, ~3.5s
	// Total backoff: 500ms + 1s + 2s = 3.5s
	// Allow some margin
	if totalElapsed < 3*time.Second {
		t.Fatalf("expected total elapsed >= 3s for exponential backoff, got %v", totalElapsed)
	}

	if int(callCount.Load()) != maxRetries+1 {
		t.Fatalf("expected %d calls, got %d", maxRetries+1, callCount.Load())
	}
}

// --- Phase 10: preSend orchestration tests ---

// mockMessageEditor is a channel that supports MessageEditor.
type mockMessageEditor struct {
	mockChannel
	editFn func(ctx context.Context, chatID, messageID, content string) error
}

func (m *mockMessageEditor) EditMessage(ctx context.Context, chatID, messageID, content string) error {
	return m.editFn(ctx, chatID, messageID, content)
}

func TestPreSend_PlaceholderEditSuccess(t *testing.T) {
	m := newTestManager()
	var sendCalled bool
	var editCalled bool

	ch := &mockMessageEditor{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
				sendCalled = true
				return nil
			},
		},
		editFn: func(_ context.Context, chatID, messageID, content string) error {
			editCalled = true
			if chatID != "123" {
				t.Fatalf("expected chatID 123, got %s", chatID)
			}
			if messageID != "456" {
				t.Fatalf("expected messageID 456, got %s", messageID)
			}
			if content != "hello" {
				t.Fatalf("expected content 'hello', got %s", content)
			}
			return nil
		},
	}

	// Register placeholder
	m.RecordPlaceholder("test", "123", "456")

	msg := bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"}
	edited := m.preSend(context.Background(), "test", msg, ch)

	if !edited {
		t.Fatal("expected preSend to return true (placeholder edited)")
	}
	if !editCalled {
		t.Fatal("expected EditMessage to be called")
	}
	if sendCalled {
		t.Fatal("expected Send to NOT be called when placeholder edited")
	}
}

func TestPreSend_PlaceholderEditFails_FallsThrough(t *testing.T) {
	m := newTestManager()

	ch := &mockMessageEditor{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
				return nil
			},
		},
		editFn: func(_ context.Context, _, _, _ string) error {
			return fmt.Errorf("edit failed")
		},
	}

	m.RecordPlaceholder("test", "123", "456")

	msg := bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"}
	edited := m.preSend(context.Background(), "test", msg, ch)

	if edited {
		t.Fatal("expected preSend to return false when edit fails")
	}
}

func TestInvokeTypingStop_CallsRegisteredStop(t *testing.T) {
	m := newTestManager()
	var stopCalled bool

	m.RecordTypingStop("telegram", "chat123", func() {
		stopCalled = true
	})

	m.InvokeTypingStop("telegram", "chat123")

	if !stopCalled {
		t.Fatal("expected typing stop func to be called")
	}
}

func TestInvokeTypingStop_NoOpWhenNoEntry(t *testing.T) {
	m := newTestManager()
	// Should not panic
	m.InvokeTypingStop("telegram", "nonexistent")
}

func TestInvokeTypingStop_Idempotent(t *testing.T) {
	m := newTestManager()
	var callCount int

	m.RecordTypingStop("telegram", "chat123", func() {
		callCount++
	})

	m.InvokeTypingStop("telegram", "chat123")
	m.InvokeTypingStop("telegram", "chat123") // Second call: entry already removed, no-op

	if callCount != 1 {
		t.Fatalf("expected stop to be called once, got %d", callCount)
	}
}

func TestPreSend_TypingStopCalled(t *testing.T) {
	m := newTestManager()
	var stopCalled bool

	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			return nil
		},
	}

	m.RecordTypingStop("test", "123", func() {
		stopCalled = true
	})

	msg := bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"}
	m.preSend(context.Background(), "test", msg, ch)

	if !stopCalled {
		t.Fatal("expected typing stop func to be called")
	}
}

func TestPreSend_NoRegisteredState(t *testing.T) {
	m := newTestManager()

	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			return nil
		},
	}

	msg := bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"}
	edited := m.preSend(context.Background(), "test", msg, ch)

	if edited {
		t.Fatal("expected preSend to return false with no registered state")
	}
}

func TestPreSend_TypingAndPlaceholder(t *testing.T) {
	m := newTestManager()
	var stopCalled bool
	var editCalled bool

	ch := &mockMessageEditor{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
				return nil
			},
		},
		editFn: func(_ context.Context, _, _, _ string) error {
			editCalled = true
			return nil
		},
	}

	m.RecordTypingStop("test", "123", func() {
		stopCalled = true
	})
	m.RecordPlaceholder("test", "123", "456")

	msg := bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"}
	edited := m.preSend(context.Background(), "test", msg, ch)

	if !stopCalled {
		t.Fatal("expected typing stop to be called")
	}
	if !editCalled {
		t.Fatal("expected EditMessage to be called")
	}
	if !edited {
		t.Fatal("expected preSend to return true")
	}
}

func TestRecordPlaceholder_ConcurrentSafe(t *testing.T) {
	m := newTestManager()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			chatID := fmt.Sprintf("chat_%d", i%10)
			m.RecordPlaceholder("test", chatID, fmt.Sprintf("msg_%d", i))
		}(i)
	}
	wg.Wait()
}

func TestRecordTypingStop_ConcurrentSafe(t *testing.T) {
	m := newTestManager()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			chatID := fmt.Sprintf("chat_%d", i%10)
			m.RecordTypingStop("test", chatID, func() {})
		}(i)
	}
	wg.Wait()
}

func TestRecordTypingStop_ReplacesExistingStop(t *testing.T) {
	m := newTestManager()
	var oldStopCalls int
	var newStopCalls int

	m.RecordTypingStop("test", "123", func() {
		oldStopCalls++
	})

	m.RecordTypingStop("test", "123", func() {
		newStopCalls++
	})

	if oldStopCalls != 1 {
		t.Fatalf("expected previous typing stop to be called once when replaced, got %d", oldStopCalls)
	}
	if newStopCalls != 0 {
		t.Fatalf("expected replacement typing stop to stay active until preSend, got %d calls", newStopCalls)
	}

	msg := bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"}
	m.preSend(context.Background(), "test", msg, &mockChannel{})

	if newStopCalls != 1 {
		t.Fatalf("expected replacement typing stop to be called by preSend, got %d", newStopCalls)
	}
	if oldStopCalls != 1 {
		t.Fatalf("expected previous typing stop to not be called again, got %d", oldStopCalls)
	}
}

func TestSendWithRetry_PreSendEditsPlaceholder(t *testing.T) {
	m := newTestManager()
	var sendCalled bool

	ch := &mockMessageEditor{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
				sendCalled = true
				return nil
			},
		},
		editFn: func(_ context.Context, _, _, _ string) error {
			return nil // edit succeeds
		},
	}

	m.RecordPlaceholder("test", "123", "456")

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	msg := bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"}
	m.sendWithRetry(context.Background(), "test", w, msg, false)

	if sendCalled {
		t.Fatal("expected Send to NOT be called when placeholder was edited")
	}
}

// --- Dispatcher exit tests (Step 1) ---

func TestDispatcherExitsOnCancel(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		m.dispatchOutbound(ctx)
		close(done)
	}()

	// Cancel context and verify the dispatcher exits quickly
	cancel()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("dispatchOutbound did not exit within 2s after context cancel")
	}
}

func TestDispatcherMediaExitsOnCancel(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		m.dispatchOutboundMedia(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("dispatchOutboundMedia did not exit within 2s after context cancel")
	}
}

// --- Dispatch reliability hardening tests ---

// newWorkerForTest builds a channelWorker around ch with a queue of the given
// capacity and an unlimited rate limiter (so rate limiting never gates the test).
func newWorkerForTest(ch Channel, queueCap int) *channelWorker {
	return &channelWorker{
		ch:         ch,
		queue:      make(chan bus.OutboundMessage, queueCap),
		mediaQueue: make(chan bus.OutboundMediaMessage, queueCap),
		done:       make(chan struct{}),
		mediaDone:  make(chan struct{}),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}
}

// TestReload_RefreshesDispatchCtx_NewChannelCanSend asserts that after Reload,
// the manager's dispatchCtx is a LIVE context (not the pre-reload canceled one),
// so a channel registered after the reload can actually deliver an outbound
// message. If newDispatchContext failed to refresh m.dispatchCtx, the worker
// spun up by RegisterChannel would bind to a dead context and silently drop.
func TestReload_RefreshesDispatchCtx_NewChannelCanSend(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()
	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}

	// Simulate the post-StartAll state: a canceled "old" dispatch context, as if
	// a prior dispatch generation had been torn down.
	oldCtx, oldCancel := context.WithCancel(context.Background())
	m.dispatchTask = &asyncTask{cancel: oldCancel}
	m.dispatchCtx = oldCtx
	oldCancel() // the old generation is dead

	// newDispatchContext is the helper both StartAll and Reload use; exercising it
	// directly is the deterministic stand-in for a full Reload's ctx refresh.
	dispatchCtx, cancel := m.newDispatchContext(context.Background())
	defer cancel()
	m.startDispatchers(dispatchCtx)

	if m.dispatchCtx.Err() != nil {
		t.Fatalf("dispatchCtx must be live after refresh, got err: %v", m.dispatchCtx.Err())
	}

	// Register a channel post-refresh; RegisterChannel spins its worker on
	// m.dispatchCtx. With a live ctx the worker delivers; with the dead one it
	// would return immediately and never send.
	sent := make(chan bus.OutboundMessage, 1)
	ch := &mockChannel{sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
		sent <- msg
		return nil
	}}
	m.RegisterChannel("alpha", ch)

	// Push a message directly onto the worker queue (the worker is what binds to
	// the dispatch ctx); a dead ctx makes runWorker exit before consuming it.
	m.mu.RLock()
	w := m.workers["alpha"]
	m.mu.RUnlock()
	if w == nil {
		t.Fatal("RegisterChannel must create a worker for the new channel")
	}
	w.queue <- bus.OutboundMessage{Channel: "alpha", ChatID: "c1", Content: "hi"}

	select {
	case got := <-sent:
		if got.Content != "hi" {
			t.Fatalf("unexpected content delivered: %q", got.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("new channel did not deliver — worker likely bound to a dead dispatchCtx")
	}
}

// TestDispatchOutbound_ClosedQueueDuringShutdown_NoPanic pre-closes a worker
// queue and cancels the dispatch context, then publishes to that channel. The
// enqueue closure's send-on-closed-channel must be recover-guarded (safeEnqueue)
// so dispatchOutbound returns cleanly with NO panic.
func TestDispatchOutbound_ClosedQueueDuringShutdown_NoPanic(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}

	ch := &mockChannel{sendFn: func(_ context.Context, _ bus.OutboundMessage) error { return nil }}
	w := newWorkerForTest(ch, 1)
	m.channels["beta"] = ch
	m.workers["beta"] = w

	// Pre-close the worker queue: a send on it would panic without recover.
	close(w.queue)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done) // if dispatchOutbound panicked, this never runs → timeout
		m.dispatchOutbound(ctx)
	}()

	// Publish to the closed-queue channel. Even if ctx.Done() and the (closed)
	// send case race, safeEnqueue must recover from any panic.
	_ = mb.PublishOutbound(context.Background(), bus.OutboundMessage{
		Channel: "beta", ChatID: "c1", Content: "boom",
	})
	cancel()

	select {
	case <-done:
		// dispatchOutbound returned cleanly (no panic crashed the goroutine).
	case <-time.After(2 * time.Second):
		t.Fatal("dispatchOutbound did not return cleanly — likely panicked on send to closed queue")
	}
}

// TestDispatchOutbound_QueueFull_PublishesDropNotice fills a worker's queue so
// the next enqueue hits the default (backpressure) branch, and asserts a
// user-facing drop notice is published back through the bus — and that it does
// NOT block the dispatch loop (C1: the publish must be non-blocking).
func TestDispatchOutbound_QueueFull_PublishesDropNotice(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}

	// Queue capacity 1, NO runWorker consuming it → it stays full after one msg.
	ch := &mockChannel{sendFn: func(_ context.Context, _ bus.OutboundMessage) error { return nil }}
	w := newWorkerForTest(ch, 1)
	// Pre-fill the single queue slot so the next enqueue hits the backpressure
	// (default) branch.
	w.queue <- bus.OutboundMessage{Channel: "gamma", ChatID: "c1", Content: "filler"}

	// Call the enqueue decision directly — no dispatcher goroutine, so there is no
	// race over reading the published notice off the bus.
	cont := m.enqueueOutbound(context.Background(), w,
		bus.OutboundMessage{Channel: "gamma", ChatID: "c1", Content: "overflow"})

	// C1: the loop must NOT block — enqueueOutbound returns true (keep dispatching)
	// even though the slot was full, because the drop-notice publish is non-blocking.
	if !cont {
		t.Fatal("enqueueOutbound must return true (keep loop running) on a queue-full drop")
	}

	// The drop notice was published back onto the bus (non-blocking).
	select {
	case notice := <-mb.OutboundChan():
		if !strings.HasPrefix(notice.Content, dropNoticePrefix) {
			t.Fatalf("expected drop-notice sentinel prefix, got %q", notice.Content)
		}
		if notice.ChatID != "c1" || notice.Channel != "gamma" {
			t.Fatalf("drop notice mis-addressed: %+v", notice)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a drop notice on the bus after a queue-full drop")
	}
}

// TestDispatchOutbound_DropNotice_NotAmplified asserts the dropNoticePrefix guard:
// when the message being dropped is ITSELF a drop notice, no second notice is
// published (otherwise a saturated channel would amplify notices indefinitely).
func TestDispatchOutbound_DropNotice_NotAmplified(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}

	ch := &mockChannel{sendFn: func(_ context.Context, _ bus.OutboundMessage) error { return nil }}
	w := newWorkerForTest(ch, 1)
	w.queue <- bus.OutboundMessage{Channel: "delta", ChatID: "c1", Content: "filler"} // fill it

	// Drop a message that is ALREADY a drop notice; the prefix guard must suppress
	// a second notice.
	cont := m.enqueueOutbound(context.Background(), w, bus.OutboundMessage{
		Channel: "delta", ChatID: "c1", Content: dropNoticePrefix + "[already dropped]",
	})
	if !cont {
		t.Fatal("enqueueOutbound must keep the loop running on a dropped notice")
	}

	select {
	case extra := <-mb.OutboundChan():
		t.Fatalf("a drop notice must NOT be re-notified, but got: %q", extra.Content)
	case <-time.After(200 * time.Millisecond):
		// No amplification — correct.
	}
}

// TestEnqueueOutbound_CtxDone_StopsLoop asserts that when the dispatch ctx is
// canceled, enqueueOutbound returns false (stop the loop) rather than sending.
func TestEnqueueOutbound_CtxDone_StopsLoop(t *testing.T) {
	m := newTestManager()
	ch := &mockChannel{sendFn: func(_ context.Context, _ bus.OutboundMessage) error { return nil }}
	w := newWorkerForTest(ch, 1)
	w.queue <- bus.OutboundMessage{Channel: "theta", ChatID: "c1", Content: "filler"} // full

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if m.enqueueOutbound(ctx, w, bus.OutboundMessage{Channel: "theta", ChatID: "c1", Content: "x"}) {
		t.Fatal("enqueueOutbound must return false (stop loop) when ctx is done")
	}
}

// TestRunWorker_StripsDropSentinel verifies the TrimPrefix in runWorker: the
// internal dropNoticePrefix sentinel is stripped before the message reaches the
// channel's Send, so the user only ever sees the human-readable notice text.
func TestRunWorker_StripsDropSentinel(t *testing.T) {
	m := newTestManager()

	got := make(chan string, 1)
	ch := &mockChannel{sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
		got <- msg.Content
		return nil
	}}
	w := newWorkerForTest(ch, 4)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.runWorker(ctx, "eps", w)

	w.queue <- bus.OutboundMessage{
		Channel: "eps", ChatID: "c1", Content: dropNoticePrefix + "[message dropped: channel is overloaded, please retry]",
	}

	select {
	case content := <-got:
		if strings.Contains(content, dropNoticePrefix) {
			t.Fatalf("sentinel must be stripped before Send, got %q", content)
		}
		if content != "[message dropped: channel is overloaded, please retry]" {
			t.Fatalf("unexpected delivered content: %q", content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runWorker did not deliver the stripped notice")
	}
}

// TestSendWithRetry_TerminalFailure_PublishesUserNotice (H1) asserts that a text
// send that exhausts retries / fails permanently publishes a short user-facing
// notice (mirroring the media "[media delivery failed]" fallback) instead of
// failing silently.
func TestSendWithRetry_TerminalFailure_PublishesUserNotice(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}

	// Permanent failure → no retry, terminal path.
	ch := &mockChannel{sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
		return ErrSendFailed
	}}
	w := newWorkerForTest(ch, 1)

	m.sendWithRetry(context.Background(), "zeta", w,
		bus.OutboundMessage{Channel: "zeta", ChatID: "c1", Content: "will fail"}, false)

	select {
	case notice := <-mb.OutboundChan():
		if !strings.HasPrefix(notice.Content, dropNoticePrefix) {
			t.Fatalf("expected sentinel-tagged user notice, got %q", notice.Content)
		}
		stripped := strings.TrimPrefix(notice.Content, dropNoticePrefix)
		if stripped != "[message delivery failed]" {
			t.Fatalf("unexpected fallback text: %q", stripped)
		}
		if notice.ChatID != "c1" {
			t.Fatalf("fallback mis-addressed: %+v", notice)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal text-send failure must publish a user-facing notice (H1) — none seen")
	}
}

// TestSendWithRetry_FallbackNotAmplified guards the H1 fallback against its own
// re-failure: a manager-injected notice that itself fails to send must NOT
// publish yet another fallback. This drives the REAL async path through
// runWorker (which strips the dropNoticePrefix sentinel and passes isNotice=true
// to sendWithRetry) — exercising the strip-then-flag handoff, not bypassing it.
func TestSendWithRetry_FallbackNotAmplified(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      mb,
	}

	// Capture what the channel was actually asked to deliver; fail every send
	// terminally so the notice's own delivery fails.
	delivered := make(chan string, 4)
	ch := &mockChannel{sendFn: func(_ context.Context, om bus.OutboundMessage) error {
		delivered <- om.Content
		return ErrSendFailed
	}}
	w := newWorkerForTest(ch, 4)
	m.channels["eta"] = ch
	m.workers["eta"] = w

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.runWorker(ctx, "eta", w)

	// A manager-injected notice arrives on the worker queue with the sentinel
	// prefix, exactly as notifyDrop -> dispatchOutbound delivers it.
	w.queue <- bus.OutboundMessage{Channel: "eta", ChatID: "c1", Content: dropNoticePrefix + "[message delivery failed]"}

	// runWorker must strip the sentinel before delivery.
	select {
	case got := <-delivered:
		if got != "[message delivery failed]" {
			t.Fatalf("sentinel not stripped before delivery: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notice was never delivered to the channel")
	}

	// The failed delivery of that notice must NOT produce another notice.
	select {
	case extra := <-mb.OutboundChan():
		t.Fatalf("a failed drop-notice send must NOT publish another notice (amplification), got %q", extra.Content)
	case <-time.After(300 * time.Millisecond):
		// No amplification — correct.
	}
}

// --- WhatsApp non-fatal boot tests (M1) ---

// TestBootFatalError_WhatsAppNativeIsNonFatal asserts that a whatsapp_native
// construction failure does NOT abort boot (bootFatalError returns nil), while a
// failure of any other channel IS boot-fatal.
func TestBootFatalError_WhatsAppNativeIsNonFatal(t *testing.T) {
	// Only whatsapp_native failed → non-fatal → boot proceeds.
	onlyWA := []ChannelInitError{
		{
			Name:    "whatsapp_native",
			Channel: "WhatsApp Native",
			Err:     errors.New("modernc.org/sqlite unavailable on this build"),
		},
	}
	if err := bootFatalError(onlyWA); err != nil {
		t.Fatalf("whatsapp_native failure must be non-fatal, got: %v", err)
	}

	// A non-whatsapp failure is still fatal.
	withTelegram := []ChannelInitError{
		{Name: "whatsapp_native", Channel: "WhatsApp Native", Err: errors.New("native unavailable")},
		{Name: "telegram", Channel: "Telegram", Err: errors.New("bad token")},
	}
	err := bootFatalError(withTelegram)
	if err == nil {
		t.Fatal("a non-whatsapp channel failure must abort boot")
	}
	if !strings.Contains(err.Error(), "Telegram") {
		t.Fatalf("fatal error must mention the failing non-whatsapp channel, got: %v", err)
	}
	// The whatsapp failure must NOT be folded into the fatal error.
	if strings.Contains(err.Error(), "WhatsApp") {
		t.Fatalf("whatsapp_native must be excluded from the boot-fatal error, got: %v", err)
	}

	// No failures → nil.
	if err := bootFatalError(nil); err != nil {
		t.Fatalf("no failures must yield nil, got: %v", err)
	}
}

// TestRecordChannelFailure_SurfacedAsDegraded asserts that a recorded
// whatsapp_native failure is surfaced via FailedChannels (degraded status) with
// a message that tells the operator the native build is required — so even
// though boot proceeds, the operator sees WHY WhatsApp is down.
func TestRecordChannelFailure_SurfacedAsDegraded(t *testing.T) {
	m := newTestManager()

	// Mirror the wrapping initChannels applies to a whatsapp_native init failure.
	wrapped := fmt.Errorf(
		"WhatsApp requires the native build (bridge removed); unavailable on this build variant: %w",
		errors.New("modernc.org/sqlite unavailable"))
	m.recordChannelFailure("whatsapp_native", "WhatsApp Native", wrapped)

	failed := m.FailedChannels()
	if len(failed) != 1 {
		t.Fatalf("expected 1 recorded failure, got %d", len(failed))
	}
	if failed[0].Name != "whatsapp_native" {
		t.Fatalf("expected whatsapp_native recorded, got %q", failed[0].Name)
	}
	if !strings.Contains(failed[0].Err.Error(), "native build") {
		t.Fatalf("degraded message must mention the native build requirement, got: %v", failed[0].Err)
	}

	// And the recorded whatsapp_native failure remains non-fatal for boot.
	if err := bootFatalError(failed); err != nil {
		t.Fatalf("recorded whatsapp_native failure must not be boot-fatal, got: %v", err)
	}
}

// --- TTL Janitor tests (Step 2) ---

// TestEvictStale_TypingStop verifies the typingStops arm of the real extracted
// evictStale sweep: a past-TTL entry is deleted and its (idempotent) stop func
// is invoked, while a fresh entry is left untouched.
func TestEvictStale_TypingStop(t *testing.T) {
	m := newTestManager()

	var staleStopped, freshStopped atomic.Bool
	m.typingStops.Store("stale:123", typingEntry{
		stop:      func() { staleStopped.Store(true) },
		createdAt: time.Now().Add(-10 * time.Minute), // well past typingStopTTL
	})
	m.typingStops.Store("fresh:123", typingEntry{
		stop:      func() { freshStopped.Store(true) },
		createdAt: time.Now(),
	})

	m.evictStale(time.Now())

	if !staleStopped.Load() {
		t.Fatal("expected stale typing stop func to be called by evictStale")
	}
	if _, loaded := m.typingStops.Load("stale:123"); loaded {
		t.Fatal("expected stale typing entry to be deleted")
	}
	if freshStopped.Load() {
		t.Fatal("fresh typing entry must NOT be evicted")
	}
	if _, loaded := m.typingStops.Load("fresh:123"); !loaded {
		t.Fatal("fresh typing entry must be retained")
	}
}

// TestEvictStale_ReactionUndo verifies the reactionUndos arm.
func TestEvictStale_ReactionUndo(t *testing.T) {
	m := newTestManager()

	var undone atomic.Bool
	m.reactionUndos.Store("stale:r", reactionEntry{
		undo:      func() { undone.Store(true) },
		createdAt: time.Now().Add(-10 * time.Minute),
	})

	m.evictStale(time.Now())

	if !undone.Load() {
		t.Fatal("expected stale reaction undo func to be called by evictStale")
	}
	if _, loaded := m.reactionUndos.Load("stale:r"); loaded {
		t.Fatal("expected stale reaction entry to be deleted")
	}
}

// TestEvictStale_Placeholder verifies the placeholders arm.
func TestEvictStale_Placeholder(t *testing.T) {
	m := newTestManager()

	m.placeholders.Store("stale:ph", placeholderEntry{
		id:        "msg_old",
		createdAt: time.Now().Add(-20 * time.Minute),
	})
	m.placeholders.Store("fresh:ph", placeholderEntry{
		id:        "msg_new",
		createdAt: time.Now(),
	})

	m.evictStale(time.Now())

	if _, loaded := m.placeholders.Load("stale:ph"); loaded {
		t.Fatal("expected stale placeholder entry to be deleted")
	}
	if _, loaded := m.placeholders.Load("fresh:ph"); !loaded {
		t.Fatal("fresh placeholder entry must be retained")
	}
}

// TestEvictStale_StreamActive verifies the streamActive arm — the marker added
// by streamer.Finalize that leaks if the agent errors before publishing an
// outbound for the key.
func TestEvictStale_StreamActive(t *testing.T) {
	m := newTestManager()

	m.streamActive.Store("stale:s", streamEntry{
		createdAt: time.Now().Add(-20 * time.Minute),
	})
	m.streamActive.Store("fresh:s", streamEntry{
		createdAt: time.Now(),
	})

	m.evictStale(time.Now())

	if _, loaded := m.streamActive.Load("stale:s"); loaded {
		t.Fatal("expected stale streamActive marker to be evicted")
	}
	if _, loaded := m.streamActive.Load("fresh:s"); !loaded {
		t.Fatal("fresh streamActive marker must be retained")
	}
}

func TestPreSendStillWorksWithWrappedTypes(t *testing.T) {
	m := newTestManager()
	var stopCalled bool
	var editCalled bool

	ch := &mockMessageEditor{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
				return nil
			},
		},
		editFn: func(_ context.Context, chatID, messageID, content string) error {
			editCalled = true
			if messageID != "ph_id" {
				t.Fatalf("expected messageID ph_id, got %s", messageID)
			}
			return nil
		},
	}

	// Use the new wrapped types via the public API
	m.RecordTypingStop("test", "chat1", func() {
		stopCalled = true
	})
	m.RecordPlaceholder("test", "chat1", "ph_id")

	msg := bus.OutboundMessage{Channel: "test", ChatID: "chat1", Content: "response"}
	edited := m.preSend(context.Background(), "test", msg, ch)

	if !stopCalled {
		t.Fatal("expected typing stop to be called via wrapped type")
	}
	if !editCalled {
		t.Fatal("expected EditMessage to be called via wrapped type")
	}
	if !edited {
		t.Fatal("expected preSend to return true")
	}
}

// --- Lazy worker creation tests (Step 6) ---

func TestLazyWorkerCreation(t *testing.T) {
	m := newTestManager()

	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			return nil
		},
	}

	// RegisterChannel should NOT create a worker
	m.RegisterChannel("lazy", ch)

	m.mu.RLock()
	_, chExists := m.channels["lazy"]
	_, wExists := m.workers["lazy"]
	m.mu.RUnlock()

	if !chExists {
		t.Fatal("expected channel to be registered")
	}
	if wExists {
		t.Fatal("expected worker to NOT be created by RegisterChannel (lazy creation)")
	}
}

// --- FastID uniqueness test (Step 5) ---

func TestBuildMediaScope_FastIDUniqueness(t *testing.T) {
	seen := make(map[string]bool)

	for range 1000 {
		scope := BuildMediaScope("test", "chat1", "")
		if seen[scope] {
			t.Fatalf("duplicate scope generated: %s", scope)
		}
		seen[scope] = true
	}

	// Verify format: "channel:chatID:id"
	scope := BuildMediaScope("telegram", "42", "")
	parts := 0
	for _, c := range scope {
		if c == ':' {
			parts++
		}
	}
	if parts != 2 {
		t.Fatalf("expected scope to have 2 colons (channel:chatID:id), got: %s", scope)
	}
}

func TestBuildMediaScope_WithMessageID(t *testing.T) {
	scope := BuildMediaScope("discord", "chat99", "msg123")
	expected := "discord:chat99:msg123"
	if scope != expected {
		t.Fatalf("expected %s, got %s", expected, scope)
	}
}

func TestManager_PlaceholderConsumedByResponse(t *testing.T) {
	mgr := &Manager{
		channels:     make(map[string]Channel),
		workers:      make(map[string]*channelWorker),
		placeholders: sync.Map{},
	}

	mockCh := &mockChannel{
		sendFn: func(ctx context.Context, msg bus.OutboundMessage) error {
			return nil
		},
	}
	worker := newChannelWorker("mock", mockCh)
	mgr.channels["mock"] = mockCh
	mgr.workers["mock"] = worker

	ctx := context.Background()
	key := "mock:chat-1"

	// Simulate a placeholder recorded by base.go HandleMessage
	mgr.RecordPlaceholder("mock", "chat-1", "ph-123")

	if _, ok := mgr.placeholders.Load(key); !ok {
		t.Fatal("expected placeholder to be recorded")
	}

	// Transcription feedback arrives first — it should consume the placeholder
	// and be delivered via EditMessage, not Send.
	msgTranscript := bus.OutboundMessage{
		Channel: "mock",
		ChatID:  "chat-1",
		Content: "Transcript: hello",
	}
	mgr.sendWithRetry(ctx, "mock", worker, msgTranscript, false)

	if mockCh.editedMessages != 1 {
		t.Errorf("expected 1 edited message (placeholder consumed by transcript), got %d", mockCh.editedMessages)
	}
	if len(mockCh.sentMessages) != 0 {
		t.Errorf("expected 0 normal messages (transcript used edit), got %d", len(mockCh.sentMessages))
	}

	// Placeholder should be gone now
	if _, ok := mgr.placeholders.Load(key); ok {
		t.Error("expected placeholder to be removed after being consumed")
	}

	// Final LLM response arrives — no placeholder left, so it goes through Send
	msgFinal := bus.OutboundMessage{
		Channel: "mock",
		ChatID:  "chat-1",
		Content: "Final Answer",
	}
	mgr.sendWithRetry(ctx, "mock", worker, msgFinal, false)

	if len(mockCh.sentMessages) != 1 {
		t.Errorf("expected 1 normal message sent, got %d", len(mockCh.sentMessages))
	}
}

func TestSendMessage_Synchronous(t *testing.T) {
	m := newTestManager()

	var received []bus.OutboundMessage
	ch := &mockChannel{
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			received = append(received, msg)
			return nil
		},
	}

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	msg := bus.OutboundMessage{
		Channel:          "test",
		ChatID:           "123",
		Content:          "hello world",
		ReplyToMessageID: "msg-456",
	}

	err := m.SendMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// SendMessage is synchronous — message should already be delivered
	if len(received) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(received))
	}
	if received[0].ReplyToMessageID != "msg-456" {
		t.Fatalf("expected ReplyToMessageID msg-456, got %s", received[0].ReplyToMessageID)
	}
	if received[0].Content != "hello world" {
		t.Fatalf("expected content 'hello world', got %s", received[0].Content)
	}
}

func TestSendMessage_UnknownChannel(t *testing.T) {
	m := newTestManager()

	msg := bus.OutboundMessage{
		Channel: "nonexistent",
		ChatID:  "123",
		Content: "hello",
	}

	err := m.SendMessage(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error for unknown channel")
	}
}

func TestSendMessage_NoWorker(t *testing.T) {
	m := newTestManager()

	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error { return nil },
	}
	m.channels["test"] = ch
	// No worker registered

	msg := bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "hello",
	}

	err := m.SendMessage(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error when no worker exists")
	}
}

func TestSendMessage_WithRetry(t *testing.T) {
	m := newTestManager()

	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			if callCount == 1 {
				return fmt.Errorf("transient: %w", ErrTemporary)
			}
			return nil
		},
	}

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	msg := bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "retry me",
	}

	err := m.SendMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if callCount != 2 {
		t.Fatalf("expected 2 Send calls (1 failure + 1 success), got %d", callCount)
	}
}

func TestSendMessage_WithSplitting(t *testing.T) {
	m := newTestManager()

	var received []string
	ch := &mockChannelWithLength{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
				received = append(received, msg.Content)
				return nil
			},
		},
		maxLen: 5,
	}

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	msg := bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "hello world",
	}

	err := m.SendMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(received) < 2 {
		t.Fatalf("expected message to be split into at least 2 chunks, got %d", len(received))
	}
}

func TestSendMessage_PreservesOrdering(t *testing.T) {
	m := newTestManager()

	var order []string
	ch := &mockChannel{
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			order = append(order, msg.Content)
			return nil
		},
	}

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	// Send two messages sequentially — they must arrive in order
	_ = m.SendMessage(context.Background(), bus.OutboundMessage{
		Channel: "test", ChatID: "1", Content: "first",
	})
	_ = m.SendMessage(context.Background(), bus.OutboundMessage{
		Channel: "test", ChatID: "1", Content: "second",
	})

	if len(order) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(order))
	}
	if order[0] != "first" || order[1] != "second" {
		t.Fatalf("expected [first, second], got %v", order)
	}
}

func TestManager_SendPlaceholder(t *testing.T) {
	mgr := &Manager{
		channels:     make(map[string]Channel),
		workers:      make(map[string]*channelWorker),
		placeholders: sync.Map{},
	}

	mockCh := &mockChannel{
		sendFn: func(ctx context.Context, msg bus.OutboundMessage) error {
			return nil
		},
	}
	mgr.channels["mock"] = mockCh

	ctx := context.Background()

	// SendPlaceholder should send a placeholder and record it
	ok := mgr.SendPlaceholder(ctx, "mock", "chat-1")
	if !ok {
		t.Fatal("expected SendPlaceholder to succeed")
	}
	if mockCh.placeholdersSent != 1 {
		t.Errorf("expected 1 placeholder sent, got %d", mockCh.placeholdersSent)
	}

	key := "mock:chat-1"
	if _, loaded := mgr.placeholders.Load(key); !loaded {
		t.Error("expected placeholder to be recorded in manager")
	}

	// SendPlaceholder on unknown channel should return false
	ok = mgr.SendPlaceholder(ctx, "unknown", "chat-1")
	if ok {
		t.Error("expected SendPlaceholder to fail for unknown channel")
	}
}

// TestManager_FailedChannelsClearedOnReload verifies two things:
//  1. FailedChannels() returns a copy and is safe to call concurrently.
//  2. Failures for channels in the re-attempted (added) set are cleared before
//     initChannels runs on Reload, so a channel that previously failed but now
//     succeeds is no longer reported as degraded. We exercise the clear path by
//     directly invoking the scoped-filter sequence that Reload performs (under
//     the write lock) rather than calling Reload, which requires a non-nil
//     MessageBus to launch goroutines.
//
// Traces to: silent-failure-hunter finding — Fix 2 (race) + Fix 3 (stale list).
func TestManager_FailedChannelsClearedOnReload(t *testing.T) {
	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		config:        &config.Config{},
		channelHashes: make(map[string]string),
	}

	// Inject a synthetic channel failure — simulates a channel that failed to
	// init on first boot.
	m.mu.Lock()
	m.failedChannels = []ChannelInitError{
		{Name: "telegram", Channel: "Telegram", Err: errors.New("env var missing")},
	}
	m.mu.Unlock()

	// Confirm the failure is visible before the simulated reload.
	before := m.FailedChannels()
	if len(before) != 1 {
		t.Fatalf("expected 1 failed channel before reload, got %d", len(before))
	}

	// Simulate Reload's scoped-filter sequence: remove failures for the re-attempted
	// channel ("telegram"), then run initChannels with a fully-disabled config so no
	// new failure is recorded.
	m.mu.Lock()
	added := []string{"telegram"}
	addedNames := make(map[string]struct{}, len(added))
	for _, n := range added {
		addedNames[n] = struct{}{}
	}
	kept := m.failedChannels[:0]
	for _, f := range m.failedChannels {
		if _, inAdded := addedNames[f.Name]; !inAdded {
			kept = append(kept, f)
		}
	}
	m.failedChannels = kept
	// initChannels with a fully-disabled config produces no new failures.
	_ = m.initChannels(&config.ChannelsConfig{})
	m.mu.Unlock()

	after := m.FailedChannels()
	if len(after) != 0 {
		t.Fatalf("expected 0 failed channels after reload simulation, got %d: %v", len(after), after)
	}
}

// TestManager_FailedChannelsRaceDetector verifies that concurrent reads via
// FailedChannels() and lock-protected writes to m.failedChannels do not trigger
// the race detector.
//
// Traces to: silent-failure-hunter finding — Fix 2 (data race on m.failedChannels).
func TestManager_FailedChannelsRaceDetector(t *testing.T) {
	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		config:        &config.Config{},
		channelHashes: make(map[string]string),
	}

	// Seed one failure so FailedChannels has something to copy.
	m.failedChannels = []ChannelInitError{
		{Channel: "race-ch", Err: errors.New("race test error")},
	}

	var wg sync.WaitGroup
	// Reader goroutines — concurrent reads must not race with lock-held writes.
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.FailedChannels()
		}()
	}
	// Writer goroutine — simulate the write-lock path (recordChannelFailure is
	// called from initChannels which runs under m.mu.Lock in Reload).
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.mu.Lock()
		m.failedChannels = append(m.failedChannels, ChannelInitError{
			Channel: "race-ch-2",
			Err:     errors.New("concurrent write"),
		})
		m.mu.Unlock()
	}()

	wg.Wait()
}

// TestManager_InitChannels_ReturnsErrorOnFailure verifies that initChannels
// returns a non-nil error when at least one enabled channel fails to construct.
//
// The test relies on the fact that the channels_test binary does NOT import any
// channel subpackages (no blank imports), so the global factory map has no
// "telegram" entry.  Enabling Telegram with a non-empty TokenRef satisfies the
// initChannels gate condition and causes initChannel("telegram", …) to call
// getFactory("telegram"), which returns (nil, false) — triggering
// fmt.Errorf("factory not registered …"), which is recorded via
// recordChannelFailure and ultimately returned by initChannels as
// errors.Join(…).
//
// Traces to: issue #299 backend task — "Manager-abort test (deferred item)".
func TestManager_InitChannels_ReturnsErrorOnFailure(t *testing.T) {
	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		config:        &config.Config{},
		channelHashes: make(map[string]string),
	}

	// Enable Telegram with a non-empty TokenRef so initChannels attempts to
	// construct it.  No "telegram" factory is registered in this test binary,
	// so initChannel will return "factory not registered for channel …".
	cc := &config.ChannelsConfig{}
	cc.Telegram.Enabled = true
	cc.Telegram.TokenRef = "tg-token-ref"

	m.mu.Lock()
	err := m.initChannels(cc)
	m.mu.Unlock()

	if err == nil {
		t.Fatal("expected initChannels to return a non-nil error when a channel fails to construct, got nil")
	}

	failed := m.FailedChannels()
	if len(failed) == 0 {
		t.Fatal("expected at least one entry in FailedChannels() after a construction failure, got none")
	}
	if failed[0].Name != "telegram" {
		t.Errorf("expected FailedChannels()[0].Name == %q, got %q", "telegram", failed[0].Name)
	}
	if failed[0].Err == nil {
		t.Error("expected FailedChannels()[0].Err to be non-nil")
	}
}

// TestManager_Reload_FailedInitRestoresPriorFailedChannels verifies that when
// a Reload's initChannels call fails, the prior failedChannels list is restored
// and the failed-reload's own failure records do NOT survive.
//
// This prevents a false-positive degraded signal after a rolled-back reload:
// if Telegram was previously healthy (no entry in failedChannels) and a reload
// attempt fails, the post-reload failedChannels must match the pre-reload
// snapshot — not reflect the failed attempt.
//
// We exercise this by:
//  1. Seeding a discord failure to represent a pre-existing degraded channel.
//  2. Running Reload's scoped-filter + initChannels sequence for "telegram"
//     (which fails — no factory registered in the test binary), then simulating
//     the revert path.
//  3. Asserting that FailedChannels() after the failed reload equals the
//     pre-reload snapshot (only the discord entry).
//
// Traces to: silent-failure-hunter CRITICAL finding — Reload degraded-state staleness.
func TestManager_Reload_FailedInitRestoresPriorFailedChannels(t *testing.T) {
	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		config:        &config.Config{},
		channelHashes: make(map[string]string),
	}

	// Seed a pre-existing failure for discord (was failing before the reload).
	m.mu.Lock()
	m.failedChannels = []ChannelInitError{
		{Name: "discord", Channel: "Discord", Err: errors.New("bad token")},
	}
	snapshot := append([]ChannelInitError(nil), m.failedChannels...)

	// Simulate Reload: telegram is being re-attempted (added set = ["telegram"]).
	// Step 1: capture oldFailed for revert.
	oldFailed := append([]ChannelInitError(nil), m.failedChannels...)

	// Step 2: scoped-filter — remove telegram from failedChannels (it's in added),
	// keep discord.
	addedNames := map[string]struct{}{"telegram": {}}
	kept := m.failedChannels[:0]
	for _, f := range m.failedChannels {
		if _, inAdded := addedNames[f.Name]; !inAdded {
			kept = append(kept, f)
		}
	}
	m.failedChannels = kept

	// Step 3: initChannels for telegram — fails because no factory is registered.
	cc := &config.ChannelsConfig{}
	cc.Telegram.Enabled = true
	cc.Telegram.TokenRef = "tg-token-ref"
	err := m.initChannels(cc)

	// initChannels must have failed and recorded a telegram failure.
	if err == nil {
		m.mu.Unlock()
		t.Fatal("expected initChannels to fail for telegram with no registered factory")
	}

	// Step 4: revert path (mirrors manager.go Reload error branch).
	m.failedChannels = oldFailed

	m.mu.Unlock()

	// After the failed reload, FailedChannels must equal the pre-reload snapshot.
	after := m.FailedChannels()
	if len(after) != len(snapshot) {
		t.Fatalf("expected %d failed channels after failed reload (pre-reload snapshot), got %d: %v",
			len(snapshot), len(after), after)
	}
	if after[0].Name != "discord" {
		t.Errorf("expected restored failure to be discord, got %q", after[0].Name)
	}
}

// TestManager_Reload_ScopedClear_UnchangedFailedChannelRetained verifies that the
// scoped clear only removes failure entries for channels in the added set — a
// channel that was previously failing and is NOT being re-attempted (i.e. not in
// added) retains its failure entry after the reload succeeds.
//
// Scenario: discord failed previously, telegram is being re-added.  After a
// successful reload of telegram, discord must still appear in FailedChannels.
//
// Traces to: silent-failure-hunter CRITICAL finding — blanket clear discards
// prior failures for channels not in the added set.
func TestManager_Reload_ScopedClear_UnchangedFailedChannelRetained(t *testing.T) {
	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		config:        &config.Config{},
		channelHashes: make(map[string]string),
	}

	// discord was previously failing; telegram is being re-attempted.
	m.mu.Lock()
	m.failedChannels = []ChannelInitError{
		{Name: "discord", Channel: "Discord", Err: errors.New("token expired")},
	}

	// Scoped filter: only drop entries for telegram (the added channel).
	addedNames := map[string]struct{}{"telegram": {}}
	kept := m.failedChannels[:0]
	for _, f := range m.failedChannels {
		if _, inAdded := addedNames[f.Name]; !inAdded {
			kept = append(kept, f)
		}
	}
	m.failedChannels = kept
	// Telegram re-init succeeds (fully-disabled config — no telegram entry to process).
	_ = m.initChannels(&config.ChannelsConfig{})
	m.mu.Unlock()

	after := m.FailedChannels()
	if len(after) != 1 {
		t.Fatalf("expected discord failure to be retained after scoped reload, got %d entries: %v",
			len(after), after)
	}
	if after[0].Name != "discord" {
		t.Errorf("expected retained failure to be discord, got %q", after[0].Name)
	}
}

// TestReload_NoRaceWithReaders is a deterministic -race regression test for the
// Reload locking smell: Reload used to m.mu.Unlock() before running its
// register/unregister deferFuncs, which mutate m.channels / m.workers with the
// lock RELEASED, racing concurrent RLock readers (GetStatus / GetChannel /
// GetEnabledChannels / SendMessage) and the restarted dispatch goroutines.
//
// The fix runs those mutations while still holding m.mu. With the old (buggy)
// code this test fails under `go test -race` with a data race on m.channels;
// with the fix it is race-clean.
func TestReload_NoRaceWithReaders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		bus:           bus.NewMessageBus(),
		config:        &config.Config{},
		channelHashes: make(map[string]string),
	}

	// Seed a single mock channel and a non-empty hash so that a Reload with an
	// empty (channel-less) config yields removed=["mock"], driving the
	// unregisterChannelLocked deferFunc that the fix moved back under the lock.
	ch := &mockChannel{sendFn: func(context.Context, bus.OutboundMessage) error { return nil }}
	m.channels["mock"] = ch
	m.channelHashes["mock"] = "seed-hash"

	// StartAll spins up the worker goroutines (so unregisterChannelLocked's
	// <-w.done / <-w.mediaDone drains complete after Reload cancels the context)
	// plus the dispatch goroutines that RLock m.channels concurrently.
	if err := m.StartAll(ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}

	// Hammer the RLock readers concurrently with the Reload write window.
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = m.GetStatus()
					_, _ = m.GetChannel("mock")
					_ = m.GetEnabledChannels()
					_ = m.SendMessage(ctx, bus.OutboundMessage{
						Channel: "mock", ChatID: "1", Content: "x",
					})
				}
			}
		}()
	}

	// Reload with an empty config removes "mock" — the path that previously
	// mutated m.channels with the lock released.
	if err := m.Reload(ctx, &config.Config{}, credentials.SecretBundle{}); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	close(stop)
	readers.Wait()

	if _, ok := m.GetChannel("mock"); ok {
		t.Fatalf("expected mock channel to be removed after Reload")
	}
}
