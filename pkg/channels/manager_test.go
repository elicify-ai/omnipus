package channels

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// mockChannel is a test double that delegates Send to a configurable function.
type mockChannel struct {
	BaseChannel
	mu                sync.Mutex // guards the recorded-call fields for concurrent Send/SendPlaceholder/EditMessage
	sendFn            func(ctx context.Context, msg bus.OutboundMessage) error
	sentMessages      []bus.OutboundMessage
	placeholdersSent  int
	editedMessages    int
	lastPlaceholderID string
}

func (m *mockChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	m.mu.Lock()
	m.sentMessages = append(m.sentMessages, msg)
	m.mu.Unlock()
	return m.sendFn(ctx, msg)
}

func (m *mockChannel) Start(ctx context.Context) error { return nil }
func (m *mockChannel) Stop(ctx context.Context) error  { return nil }

func (m *mockChannel) SendPlaceholder(ctx context.Context, chatID string) (string, error) {
	const id = "mock-ph-123"
	m.mu.Lock()
	m.placeholdersSent++
	m.lastPlaceholderID = id
	m.mu.Unlock()
	return id, nil
}

func (m *mockChannel) EditMessage(ctx context.Context, chatID, messageID, content string) error {
	m.mu.Lock()
	m.editedMessages++
	m.mu.Unlock()
	return nil
}

type mockMediaChannel struct {
	mockChannel
	sendMediaFn       func(ctx context.Context, msg bus.OutboundMediaMessage) error
	sentMediaMessages []bus.OutboundMediaMessage
}

func (m *mockMediaChannel) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) error {
	m.mu.Lock()
	m.sentMediaMessages = append(m.sentMediaMessages, msg)
	m.mu.Unlock()
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

// mockSequentialMediaChannel simulates the shape every real MediaSender
// implementation shares (Telegram/Feishu/Matrix/Slack): SendMedia iterates
// msg.Parts front-to-back, "delivering" each one in turn, and — on a
// genuine mid-loop send/upload failure — classifies the error via the same
// shared ClassifyMediaSendError helper the real channels call, using
// however many parts THIS invocation has already delivered.
//
// This is deliberately generic (not a copy of one specific channel) because
// it exercises the actual defect mechanism end-to-end through
// Manager.sendMediaWithRetry: the manager retries the ENTIRE message with
// the same, unmutated bus.OutboundMediaMessage, so any real channel's
// per-part loop necessarily starts over from part 0 on every retry.
type mockSequentialMediaChannel struct {
	mockChannel
	mu          sync.Mutex
	delivered   []string // ref of every part actually "delivered", across ALL SendMedia calls
	callCount   int
	failPartRef string // ref that fails, but ONLY on the very first SendMedia call
}

func (m *mockSequentialMediaChannel) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) error {
	m.mu.Lock()
	m.callCount++
	call := m.callCount
	m.mu.Unlock()

	sentCount := 0
	for _, part := range msg.Parts {
		if call == 1 && part.Ref == m.failPartRef {
			// A genuine send/upload failure on this part, after sentCount
			// earlier parts in THIS call already went out — exactly what
			// Telegram/Feishu/Matrix/Slack hit when the platform API call
			// for one part fails partway through a multi-part message.
			return ClassifyMediaSendError("mock", sentCount, fmt.Errorf("simulated transient send failure"))
		}
		m.mu.Lock()
		m.delivered = append(m.delivered, part.Ref)
		m.mu.Unlock()
		sentCount++
	}
	return nil
}

// TestSendMediaWithRetry_PartialFailureDoesNotReDeliverSucceededParts is the
// CRITICAL regression test for the sendfile-fix review finding: before the
// fix, sendMediaWithRetry retried the whole SendMedia call on ErrTemporary
// regardless of partial progress. Here part 1 succeeds, part 2 fails on the
// first attempt (simulating a transient 5xx/network blip) but WOULD succeed
// on a retry (call 2 never fails) — the exact trigger from the bug report:
// "part 1 uploads fine; part 2 hits a transient blip... If a later attempt
// succeeds, sendMediaWithRetry returns nil... no indication a duplicate
// went out."
//
// Before the fix: attempt 1 delivers part1, fails on part2 with a bare
// ErrTemporary; the manager backs off and retries with the SAME msg;
// attempt 2 re-delivers part1 (again!), then part2 and part3 succeed, and
// sendMediaWithRetry returns nil — clean success, with part1 silently
// delivered twice.
//
// After the fix: the part2 failure is classified permanent (ErrSendFailed)
// because sentCount(1) > 0, so the manager does not retry at all. Part1 is
// delivered exactly once, and the caller gets a real error instead of a
// false "success".
func TestSendMediaWithRetry_PartialFailureDoesNotReDeliverSucceededParts(t *testing.T) {
	m := newTestManager()
	ch := &mockSequentialMediaChannel{failPartRef: "media://part2"}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	msg := bus.OutboundMediaMessage{
		Channel: "test",
		ChatID:  "chat1",
		Parts: []bus.MediaPart{
			{Ref: "media://part1"},
			{Ref: "media://part2"},
			{Ref: "media://part3"},
		},
	}

	err := m.sendMediaWithRetry(context.Background(), "test", w, msg)

	part1Count := 0
	for _, ref := range ch.delivered {
		if ref == "media://part1" {
			part1Count++
		}
	}
	if part1Count != 1 {
		t.Fatalf(
			"CRITICAL: part1 must be delivered exactly once, got %d (delivered=%v, SendMedia calls=%d)",
			part1Count, ch.delivered, ch.callCount,
		)
	}

	// Documented trade-off of the chosen fix: a mid-loop failure after a
	// partial delivery is now permanent, so the manager must not retry at
	// all (a genuinely-transient single-part failure that would have
	// succeeded on retry is no longer retried once ANY part in the message
	// has already gone out).
	if ch.callCount != 1 {
		t.Fatalf("expected exactly 1 SendMedia call (no retry after partial delivery), got %d", ch.callCount)
	}
	if err == nil {
		t.Fatal("expected sendMediaWithRetry to return an error, not silent success, when a part failed")
	}
	if !errors.Is(err, ErrSendFailed) {
		t.Fatalf("expected the mid-loop failure to be classified permanent (ErrSendFailed), got %v", err)
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

	go m.runWorker(ctx, "test", w, m.config)

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

// TestChannelTypeForRateLimit_SecondInstance guards the multi-instance rate-
// limit bug: a second instance of a rate-limited channel type (e.g.
// "discord.2") is keyed by instance ID in m.channels/m.workers, but
// channelRateConfig is keyed by TYPE. Manager.channelTypeForRateLimit must
// resolve the instance ID back to its configured type via m.config.Channels
// so the second instance gets the same conservative rate as the first,
// instead of silently falling back to defaultRateLimit.
func TestChannelTypeForRateLimit_SecondInstance(t *testing.T) {
	m := &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		config: &config.Config{
			Channels: map[string]config.ChannelInstanceConfig{
				"discord":   {Type: "discord", Enabled: true},
				"discord.2": {Type: "discord", Enabled: true},
			},
		},
	}

	got := m.channelTypeForRateLimit("discord.2")
	if got != "discord" {
		t.Fatalf("expected instance %q to resolve to type %q, got %q", "discord.2", "discord", got)
	}

	// End-to-end: a worker built from the resolved type must carry Discord's
	// configured 1 msg/s limit, not the 10 msg/s default.
	w := newChannelWorker(m.channelTypeForRateLimit("discord.2"), &mockChannel{})
	wantRate := rate.Limit(channelRateConfig["discord"])
	if w.limiter.Limit() != wantRate {
		t.Fatalf("second discord instance: expected rate %v, got %v (regressed to defaultRateLimit=%v)",
			wantRate, w.limiter.Limit(), rate.Limit(defaultRateLimit))
	}
}

// TestChannelTypeForRateLimit_Fallback covers the two cases where no config
// entry exists for the instance ID: a nil m.config (matches
// NewManagerForTesting / ad-hoc test managers) and a config with no matching
// Channels entry (matches synthetic registrations like RegisterChannel's
// "webchat", which has no config.Channels entry). Both must fall back to
// treating the instance ID as the type, preserving pre-existing behavior for
// legacy single-instance channels where instanceID == type.
func TestChannelTypeForRateLimit_Fallback(t *testing.T) {
	nilConfig := &Manager{}
	if got := nilConfig.channelTypeForRateLimit("webchat"); got != "webchat" {
		t.Fatalf("nil config: expected fallback to instanceID %q, got %q", "webchat", got)
	}

	noEntry := &Manager{config: &config.Config{Channels: map[string]config.ChannelInstanceConfig{}}}
	if got := noEntry.channelTypeForRateLimit("telegram"); got != "telegram" {
		t.Fatalf("no config entry: expected fallback to instanceID %q, got %q", "telegram", got)
	}
}

// TestChannelTypeForRateLimit_ConfigEntryWithEmptyType_LogsAtWarn covers the
// third, distinct fallback case: a config.Channels entry EXISTS for the
// instance but its Type is empty. Unlike the two benign cases in
// TestChannelTypeForRateLimit_Fallback (no entry at all — a synthetic
// registration with nothing to look up), this means a genuinely configured
// instance's Type went out of sync with config.normalizeChannelMap's
// invariant that Type is always populated — a real desync bug. This
// project's shipped default LogLevel is "warn" (pkg/config/defaults.go), so
// this case must log at WARN, not DEBUG, or the one diagnostic line
// explaining why the instance silently fell back to the wrong rate limit
// would never reach a default-level operator's logs.
func TestChannelTypeForRateLimit_ConfigEntryWithEmptyType_LogsAtWarn(t *testing.T) {
	m := &Manager{
		config: &config.Config{
			Channels: map[string]config.ChannelInstanceConfig{
				"desynced": {Type: "", Enabled: true},
			},
		},
	}

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "ratelimit-desync.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	got := m.channelTypeForRateLimit("desynced")
	if got != "desynced" {
		t.Fatalf("expected fallback to instanceID %q, got %q", "desynced", got)
	}

	logged, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logFile, err)
	}
	logStr := string(logged)
	if !strings.Contains(logStr, "desync") {
		t.Errorf("expected the WARN-level log to explain the config/instance desync; got:\n%s", logStr)
	}
	if !strings.Contains(logStr, "desynced") {
		t.Errorf("expected the WARN-level log to name the instance %q; got:\n%s", "desynced", logStr)
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

	go m.runWorker(ctx, "test", w, m.config)

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
	go m.runWorker(ctx, "eps", w, m.config)

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
	go m.runWorker(ctx, "eta", w, m.config)

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

// TestBootFatalError_MatrixIsNonFatal asserts that a matrix construction
// failure (e.g. the init_stub.go factory on a non-goolm build) does NOT abort
// boot (bootFatalError returns nil), while a failure of any other channel IS
// boot-fatal. Mirrors TestBootFatalError_WhatsAppNativeIsNonFatal.
func TestBootFatalError_MatrixIsNonFatal(t *testing.T) {
	// Only matrix failed → non-fatal → boot proceeds.
	onlyMatrix := []ChannelInitError{
		{
			Name:    "matrix",
			Channel: "matrix",
			Err:     errors.New("matrix channel requires a goolm-tagged build"),
		},
	}
	if err := bootFatalError(onlyMatrix); err != nil {
		t.Fatalf("matrix failure must be non-fatal, got: %v", err)
	}

	// A namespaced matrix instance ("matrix.eu") is also non-fatal.
	namespacedMatrix := []ChannelInitError{
		{Name: "matrix.eu", Channel: "matrix", Err: errors.New("matrix channel requires a goolm-tagged build")},
	}
	if err := bootFatalError(namespacedMatrix); err != nil {
		t.Fatalf("namespaced matrix failure must be non-fatal, got: %v", err)
	}

	// A non-matrix failure is still fatal, and the matrix failure must not be
	// folded into the fatal error.
	withTelegram := []ChannelInitError{
		{Name: "matrix", Channel: "matrix", Err: errors.New("matrix channel requires a goolm-tagged build")},
		{Name: "telegram", Channel: "Telegram", Err: errors.New("bad token")},
	}
	err := bootFatalError(withTelegram)
	if err == nil {
		t.Fatal("a non-matrix channel failure must abort boot")
	}
	if !strings.Contains(err.Error(), "Telegram") {
		t.Fatalf("fatal error must mention the failing non-matrix channel, got: %v", err)
	}
	if strings.Contains(err.Error(), "goolm") {
		t.Fatalf("matrix must be excluded from the boot-fatal error, got: %v", err)
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
	_ = m.initChannels(map[string]config.ChannelInstanceConfig{})
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
	cc := map[string]config.ChannelInstanceConfig{
		"telegram": {
			Type:     "telegram",
			Enabled:  true,
			TokenRef: "tg-token-ref",
		},
	}

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
	cc := map[string]config.ChannelInstanceConfig{
		"telegram": {
			Type:     "telegram",
			Enabled:  true,
			TokenRef: "tg-token-ref",
		},
	}
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
	_ = m.initChannels(map[string]config.ChannelInstanceConfig{})
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

// TestReload_RemovedChannel_NoRaceWithInFlightDispatch is a deterministic -race
// regression test for the sibling bug flagged (but not fixed) in f5cfa241: Reload
// cancels the OLD dispatch context and then, via unregisterChannelLocked (run
// from deferFuncs at the end of Reload), closes a removed channel's worker queue
// WITHOUT waiting for the OLD dispatchOutbound/dispatchOutboundMedia goroutines
// to actually exit.
//
// Cancelling is not sufficient: enqueueOutbound (and its media counterpart)
// select on `w.queue <- msg` vs `<-ctx.Done()`, and a select whose send case is
// already runnable can be taken even after cancellation — so a send racing the
// close is possible. It PANICS (recovered by safeEnqueue, so this test does not
// rely on catching an unhandled panic) and, independent of the panic, is an
// unsynchronized concurrent close/send on the same channel — exactly the shape
// `go test -race` caught for the StopAll sibling.
//
// This test floods the bus with outbound messages for "mock" from several
// publisher goroutines so the manager-level dispatchOutbound goroutine is
// continuously taking m.mu.RLock() to resolve "mock"'s worker and selecting on
// its queue while Reload concurrently removes "mock" (a channel-less new
// config). With the bug present this is flaky-but-real under `go test -race`
// (verified below); with the fix (Reload waits for the OLD dispatch
// generation's WaitGroup, with m.mu released, before any deferFunc can close a
// queue) it is race-clean.
//
// Deliberately does NOT go through StartAll: StartAll also spins up
// runWorker/runMediaWorker, which have their own, separate, pre-existing
// unsynchronized read of m.config (manager.go:1179, unrelated to the bug under
// test here) that a flood of delivered messages would trigger concurrently
// with Reload's m.config write — a real bug, but a DIFFERENT one, and its
// flakiness would contaminate this test's verdict on the dispatch/close race.
// This test instead wires only the manager-level dispatch generation directly
// (mirroring what StartAll does, minus the per-channel workers) and drains
// "mock"'s worker with a minimal stand-in goroutine (see below) instead of the
// real runWorker, so unregisterChannelLocked's drain-wait behaves normally
// without ever touching m.config.
func TestReload_RemovedChannel_NoRaceWithInFlightDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		bus:           bus.NewMessageBus(),
		config:        &config.Config{},
		channelHashes: make(map[string]string),
	}

	// Seed a single mock channel and a non-empty hash so a Reload with an empty
	// (channel-less) config yields removed=["mock"] — the path that drives
	// unregisterChannelLocked's close(w.queue).
	ch := &mockChannel{sendFn: func(context.Context, bus.OutboundMessage) error { return nil }}
	m.channels["mock"] = ch
	m.channelHashes["mock"] = "seed-hash"

	// Start ONLY the manager-level dispatch generation — dispatchOutbound,
	// dispatchOutboundMedia, and runTTLJanitor — the goroutines whose exit
	// Reload must now wait for before a deferFunc closes "mock"'s queue.
	dispatchCtx, dcancel := m.newDispatchContext(ctx)
	defer dcancel()
	m.startDispatchers(dispatchCtx)

	// Wire "mock"'s worker directly instead of via StartAll (see doc comment
	// above). A fake drain goroutine (not runWorker) keeps w.queue/w.mediaQueue
	// mostly empty — as real drainage would — so the send case in
	// dispatchOutbound's enqueue select has room to stay "ready" for most of the
	// test, not just an initial burst of defaultChannelQueueSize messages before
	// the queue fills and stays full (at which point ctx.Done() would become the
	// ONLY ready case and the dispatcher would exit cleanly well before Reload
	// ever closes the queue, masking the race entirely).
	//
	// It mirrors runWorker's actual shutdown contract exactly — select on a
	// queue read OR dispatchCtx.Done(), close w.done/mediaDone on exit — WITHOUT
	// touching m.config. Watching dispatchCtx (not just queue-closed) matters:
	// Reload's "restart workers for unchanged channels" step also waits on
	// <-w.done for every entry still in m.workers (which "mock" still is, until
	// its deferFunc runs much later) and only cancels dispatchCtx, never closes
	// the queue, before that wait — a range-only drain that ignores ctx would
	// deadlock Reload right there.
	w := newChannelWorker(m.channelTypeForRateLimit("mock"), ch)
	go func() {
		defer close(w.done)
		for {
			select {
			case _, ok := <-w.queue:
				if !ok {
					return
				}
			case <-dispatchCtx.Done():
				return
			}
		}
	}()
	go func() {
		defer close(w.mediaDone)
		for {
			select {
			case _, ok := <-w.mediaQueue:
				if !ok {
					return
				}
			case <-dispatchCtx.Done():
				return
			}
		}
	}()
	m.workers["mock"] = w

	// Flood the bus with outbound messages targeting "mock" so dispatchOutbound is
	// continuously mid-loop (RLock resolve -> select on w.queue<-msg) for the
	// entire duration of the concurrent Reload below.
	publishStop := make(chan struct{})
	var publishers sync.WaitGroup
	for i := 0; i < 16; i++ {
		publishers.Add(1)
		go func() {
			defer publishers.Done()
			for {
				select {
				case <-publishStop:
					return
				default:
					_ = m.bus.PublishOutbound(ctx, bus.OutboundMessage{
						Channel: "mock", ChatID: "1", Content: "x",
					})
				}
			}
		}()
	}

	// Reload with an empty config removes "mock" while the flood above is live —
	// this is the interleaving the old code raced.
	if err := m.Reload(ctx, &config.Config{}, credentials.SecretBundle{}); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	close(publishStop)
	publishers.Wait()

	if _, ok := m.GetChannel("mock"); ok {
		t.Fatalf("expected mock channel to be removed after Reload")
	}
}

// TestRunWorker_NoConfigRaceAcrossReload is a deterministic -race regression
// test for a separate, real, pre-existing data race: runWorker (manager.go
// ~:1179, pre-fix) read m.config directly with NO lock while Reload
// (manager.go ~:1661) writes m.config under m.mu.Lock(). `go test -race`
// reproduced this reliably in isolation ("WARNING: DATA RACE ... Read ...
// (*Manager).runWorker() ... manager.go:1179" vs "Previous write ... Reload"),
// independent of the removed-channel/dispatch-close race the sibling test
// above covers — it fires even when no channel is ever removed.
//
// This test drives ONLY production entry points (Reload, PublishOutbound) —
// never runWorker directly — so it stays meaningful against the actual fix
// (runWorker now takes a config snapshot captured by its caller while holding
// m.mu.Lock(), instead of reading m.config itself) rather than merely
// asserting a call signature. A test that called runWorker directly with a
// pre-captured snapshot would trivially pass regardless of whether the
// production fix is present or reverted, and would fail to compile against
// the reverted (3-arg) signature — neither of which proves anything about the
// real bug.
//
// It registers a real channel instance ("mock-race") via a factory and keeps
// its config hash IDENTICAL across many repeated Reload calls (only the
// unrelated SplitOnMarker flag toggles, which does not affect the per-channel
// hash toChannelHashes computes). An unchanged hash routes every Reload
// through the "restart workers for existing (unchanged) channels" path
// (manager.go, in Reload), which tears down and relaunches runWorker's
// goroutine on every single call — exactly the repeated write/restart
// interleaving the race needed, with messages continuously flowing into the
// worker's queue (and being read as m.config, pre-fix) throughout.
func TestRunWorker_NoConfigRaceAcrossReload(t *testing.T) {
	withFactory(t, "mock-race", func(*config.Config, string, credentials.SecretBundle, *bus.MessageBus) (Channel, error) {
		return &mockChannel{sendFn: func(context.Context, bus.OutboundMessage) error { return nil }}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		bus:           bus.NewMessageBus(),
		config:        &config.Config{},
		channelHashes: make(map[string]string),
	}

	newCfg := func(splitOnMarker bool) *config.Config {
		cfg := &config.Config{
			Channels: map[string]config.ChannelInstanceConfig{
				"mock-race": {Type: "mock-race", Enabled: true},
			},
		}
		cfg.Agents.Defaults.SplitOnMarker = splitOnMarker
		return cfg
	}

	// Bootstrap: this Reload adds "mock-race" (nothing existed before), starting
	// its channel and spinning up the first runWorker generation.
	if err := m.Reload(ctx, newCfg(false), credentials.SecretBundle{}); err != nil {
		t.Fatalf("bootstrap Reload: %v", err)
	}

	// Flood the bus with outbound messages for "mock-race" so dispatchOutbound
	// keeps pushing onto the worker's queue — and runWorker keeps dequeuing and
	// (pre-fix) reading m.config — for the entire duration of the repeated
	// Reload calls below.
	publishStop := make(chan struct{})
	var publishers sync.WaitGroup
	for i := 0; i < 16; i++ {
		publishers.Add(1)
		go func() {
			defer publishers.Done()
			for {
				select {
				case <-publishStop:
					return
				default:
					_ = m.bus.PublishOutbound(ctx, bus.OutboundMessage{
						Channel: "mock-race", ChatID: "1", Content: "x",
					})
				}
			}
		}()
	}

	// Repeated Reloads with an unchanged channel hash: every call restarts
	// "mock-race"'s runWorker generation on a fresh dispatchCtx while writing a
	// brand new m.config, racing the OLD (still-draining) generation's read.
	for i := 0; i < 60; i++ {
		if err := m.Reload(ctx, newCfg(i%2 == 0), credentials.SecretBundle{}); err != nil {
			t.Fatalf("Reload iteration %d: %v", i, err)
		}
	}

	close(publishStop)
	publishers.Wait()

	if _, ok := m.GetChannel("mock-race"); !ok {
		t.Fatalf("expected mock-race channel to still be registered after repeated Reloads")
	}
}

// withFactory registers a channel factory for the duration of a test and restores
// the previous registration (if any) on cleanup, so package-global factory state
// does not leak between tests.
func withFactory(t *testing.T, name string, f ChannelFactory) {
	t.Helper()
	factoriesMu.Lock()
	prev, had := factories[name]
	factories[name] = f
	factoriesMu.Unlock()
	t.Cleanup(func() {
		factoriesMu.Lock()
		if had {
			factories[name] = prev
		} else {
			delete(factories, name)
		}
		factoriesMu.Unlock()
	})
}

// removeFactoryForTest ensures no factory is registered under name for the duration
// of a test, restoring any previous registration on cleanup.
func removeFactoryForTest(t *testing.T, name string) {
	t.Helper()
	factoriesMu.Lock()
	prev, had := factories[name]
	delete(factories, name)
	factoriesMu.Unlock()
	t.Cleanup(func() {
		factoriesMu.Lock()
		if had {
			factories[name] = prev
		} else {
			delete(factories, name)
		}
		factoriesMu.Unlock()
	})
}

// TestReload_AddedChannelMissingFromMap_NoPanic is the regression guard for the
// gateway-crash bug (part 1: defense-in-depth). It drives a real Reload that
// enables WhatsApp while NO factory is registered for "whatsapp_native", so
// initChannels records a failure and does NOT construct the channel — yet the
// reload diff still lists "whatsapp" (the instanceID) in `added`. Before the
// fix, the added-start loop hit m.channels[n] where n was nil and panicked.
// The nil guard must instead record a degraded failure and return cleanly.
//
// ADR-029 / WS-B update: m.channels and m.channelHashes are now keyed by
// instanceID ("whatsapp"), not the factory name ("whatsapp_native").
func TestReload_AddedChannelMissingFromMap_NoPanic(t *testing.T) {
	// Ensure no factory is registered for the WhatsApp native name for this test, and
	// restore whatever was there afterward. unregisterFactory removes the entry; the
	// cleanup runs even though we never set a replacement.
	removeFactoryForTest(t, "whatsapp_native")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		bus:           bus.NewMessageBus(),
		config:        &config.Config{},
		channelHashes: make(map[string]string),
	}
	if err := m.StartAll(ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}

	cfg := config.DefaultConfig()
	waInst := cfg.Channels["whatsapp"]
	waInst.Enabled = true
	cfg.Channels["whatsapp"] = waInst

	// This Reload MUST NOT panic. recover() turns a panic into a test failure with a
	// clear message instead of crashing the test binary (mirrors the gateway crash).
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Reload panicked on an added channel missing from m.channels "+
					"(the gateway-crash regression): %v", r)
			}
		}()
		if err := m.Reload(ctx, cfg, credentials.SecretBundle{}); err != nil {
			t.Fatalf("Reload returned error: %v", err)
		}
	}()

	// The missing channel must be recorded as a degraded failure, not started.
	// After ADR-029 / WS-B, the channel is NOT registered under the factory name
	// "whatsapp_native" or the instanceID "whatsapp" — both must be absent.
	if _, ok := m.GetChannel("whatsapp_native"); ok {
		t.Fatalf("whatsapp_native must not be registered when its factory is missing")
	}
	if _, ok := m.GetChannel("whatsapp"); ok {
		t.Fatalf("whatsapp must not be registered when the factory is missing")
	}
	failed := m.FailedChannels()
	found := false
	for _, f := range failed {
		// Failure is recorded under the instanceID or the factory name — either is OK.
		if f.Name == "whatsapp_native" || f.Name == "whatsapp" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a recorded channel failure for whatsapp/whatsapp_native, got: %+v", failed)
	}
}

// TestReload_EnableWhatsApp_StartsNativeChannel is the regression guard for
// name consistency after ADR-029 / WS-B. It registers a mock factory under the
// REGISTERED name "whatsapp_native" (no real whatsmeow / network), enables the
// "whatsapp" config key, and asserts the Reload:
//   - resolves the instanceID so the channel is constructed and started.
//   - registers the channel under instanceID "whatsapp" (not factory name).
//   - commits the hash under instanceID "whatsapp" so subsequent reloads stable.
//
// Pre-ADR-029 the channel was registered as "whatsapp_native". WS-B changes
// the invariant: m.channels["whatsapp"] is the correct post-reload entry.
func TestReload_EnableWhatsApp_StartsNativeChannel(t *testing.T) {
	started := make(chan struct{}, 1)
	withFactory(
		t,
		"whatsapp_native",
		func(*config.Config, string, credentials.SecretBundle, *bus.MessageBus) (Channel, error) {
			return &mockChannelStartSignal{started: started}, nil
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		bus:           bus.NewMessageBus(),
		config:        &config.Config{},
		channelHashes: make(map[string]string),
	}
	if err := m.StartAll(ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}

	cfg := config.DefaultConfig()
	waInst2 := cfg.Channels["whatsapp"]
	waInst2.Enabled = true
	cfg.Channels["whatsapp"] = waInst2

	if err := m.Reload(ctx, cfg, credentials.SecretBundle{}); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// After WS-B: channel is registered under instanceID "whatsapp", not factory name.
	if _, ok := m.GetChannel("whatsapp"); !ok {
		t.Fatalf("expected channel to be registered under instanceID \"whatsapp\" after enabling WhatsApp")
	}
	// The factory name "whatsapp_native" must NOT be a separate entry in m.channels.
	if _, ok := m.GetChannel("whatsapp_native"); ok {
		t.Fatalf("channel must NOT be registered under factory name \"whatsapp_native\" (use instanceID)")
	}

	// Start must have been called on the constructed channel.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("whatsapp channel was never Start()ed after Reload")
	}

	// The committed hash must be keyed by instanceID so subsequent reloads are stable.
	m.mu.RLock()
	_, hasInstanceID := m.channelHashes["whatsapp"]
	_, hasFactoryName := m.channelHashes["whatsapp_native"]
	m.mu.RUnlock()
	if !hasInstanceID {
		t.Fatalf("channelHashes must be keyed by instanceID \"whatsapp\"; got %v", m.channelHashes)
	}
	if hasFactoryName {
		t.Fatalf("channelHashes must NOT contain factory name \"whatsapp_native\" key; got %v", m.channelHashes)
	}
}

// mockChannelStartSignal is a mockChannel whose Start signals a channel, letting a
// test confirm the manager actually started it.
type mockChannelStartSignal struct {
	mockChannel
	started chan struct{}
}

func (m *mockChannelStartSignal) Start(context.Context) error {
	select {
	case m.started <- struct{}{}:
	default:
	}
	return nil
}

// TestReload_EnableConfigureDisable_NeverLeavesStaleDegradedFailure is the
// regression guard for the UAT-reported CRITICAL/infra defect: "enabling then
// disabling a channel can permanently degrade the gateway until a full
// restart" (docs/internal/qa/uat-report-full-tool-catalog-batch1-2026-09-02.md
// §2, finding 5).
//
// Root cause: enable_channel("irc") is a normal, documented two-step flow —
// "Enabling a channel with no credentials configured is allowed but the
// channel will fail to connect — configure it with configure_channel first"
// (pkg/sysagent/tools/channel.go's ChannelEnableTool.Description). While IRC
// is enabled but missing its required "server" field, initChannels's
// prerequisite-checker branch skips construction without ever registering it
// in m.channels — so it is NEVER hash-committed to m.channelHashes (the
// added-loop below deletes it from `list` on failure, exactly like a real
// construction failure would). A channel that was never hash-committed can
// never appear in a LATER reload's `removed` diff either (compareChannels
// only emits `removed` for keys present in the OLD m.channelHashes) — so
// disable_channel("irc") afterward produces neither an `added` nor a
// `removed` entry for it, and the pre-fix added-only scoped clear left its
// ChannelInitError orphaned in m.failedChannels forever: no future reload's
// added/removed diff could ever reach an instanceID that was never
// successfully committed, so FailedChannels() (and therefore /health) stayed
// "degraded" until a full gateway restart rebuilt m.failedChannels from
// scratch.
//
// This test drives the REAL enable -> reload -> configure(unrelated field,
// still missing "server") -> reload -> disable -> reload cycle through the
// production Manager.Reload path (not a hand-copied simulation of its
// algorithm, unlike several of the older tests above) and asserts:
//  1. after the first reload (enabled, unconfigured), FailedChannels reports
//     a SPECIFIC, actionable reason naming the missing field — not the
//     generic "config/registration name mismatch or skipped init" text,
//     which reads as an internal bug for this ordinary, expected case.
//  2. after the final reload (disabled), FailedChannels is empty — the
//     stale entry must not survive being disabled, regardless of the
//     added/removed diff bookkeeping.
func TestReload_EnableConfigureDisable_NeverLeavesStaleDegradedFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		bus:           bus.NewMessageBus(),
		config:        &config.Config{},
		channelHashes: make(map[string]string),
	}
	if err := m.StartAll(ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}

	// Step 1: enable_channel("irc") — Enabled=true, no "server" configured yet.
	// Mirrors ChannelEnableTool.Execute, which sets nothing but Enabled=true.
	cfg := config.DefaultConfig()
	ircInst := cfg.Channels["irc"]
	ircInst.Type = "irc"
	ircInst.Enabled = true
	cfg.Channels["irc"] = ircInst

	if err := m.Reload(ctx, cfg, credentials.SecretBundle{}); err != nil {
		t.Fatalf("Reload after enable: %v", err)
	}

	failedAfterEnable := m.FailedChannels()
	var ircFailure *ChannelInitError
	for i := range failedAfterEnable {
		if failedAfterEnable[i].Name == "irc" {
			ircFailure = &failedAfterEnable[i]
			break
		}
	}
	if ircFailure == nil {
		t.Fatalf("expected an \"irc\" entry in FailedChannels() after enabling with no server "+
			"configured, got: %+v", failedAfterEnable)
	}
	msg := ircFailure.Error()
	if strings.Contains(msg, "config/registration name mismatch or skipped init") {
		t.Errorf("expected a specific, actionable missing-config reason for irc, got the generic "+
			"internal-bug-sounding fallback text: %q", msg)
	}
	if !strings.Contains(msg, "server") {
		t.Errorf("expected the failure reason to name the missing \"server\" field, got: %q", msg)
	}
	if _, ok := m.GetChannel("irc"); ok {
		t.Fatalf("irc must not be registered while its prerequisites are unmet")
	}

	// Step 2: configure_channel("irc", token="...") — sets an UNRELATED field
	// (configure_channel has no "server" parameter at all; see
	// ChannelConfigureTool.Parameters), so the "server" prerequisite is still
	// unmet after this step. Mirrors the UAT repro exactly: configure_channel
	// cannot satisfy IRC's prerequisite regardless of what is passed to it.
	ircInst2 := cfg.Channels["irc"]
	ircInst2.TokenRef = "channel_irc_token"
	cfg.Channels["irc"] = ircInst2
	if err := m.Reload(ctx, cfg, credentials.SecretBundle{}); err != nil {
		t.Fatalf("Reload after configure: %v", err)
	}
	failedAfterConfigure := m.FailedChannels()
	found := false
	for _, f := range failedAfterConfigure {
		if f.Name == "irc" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected irc to still be reported as failed after configure_channel (which cannot "+
			"satisfy IRC's \"server\" prerequisite), got: %+v", failedAfterConfigure)
	}

	// Step 3: disable_channel("irc") — Enabled=false. Mirrors ChannelDisableTool.
	ircInst3 := cfg.Channels["irc"]
	ircInst3.Enabled = false
	cfg.Channels["irc"] = ircInst3
	if err := m.Reload(ctx, cfg, credentials.SecretBundle{}); err != nil {
		t.Fatalf("Reload after disable: %v", err)
	}

	// The regression: this must be EMPTY. Before the fix, "irc" was never
	// hash-committed (steps 1 and 2 both deleted it from the pending hash set
	// on failure), so this reload's added/removed diff contains neither
	// "irc" added nor "irc" removed — the stale failure entry from step 1/2
	// was orphaned and FailedChannels() (and therefore /health) stayed
	// "degraded" forever, surviving every subsequent reload.
	failedAfterDisable := m.FailedChannels()
	for _, f := range failedAfterDisable {
		if f.Name == "irc" {
			t.Fatalf("disabling irc must clear its stale failure entry — a disabled channel is a "+
				"clean state, not a degraded one — got: %+v", failedAfterDisable)
		}
	}

	// A follow-up reload with nothing changed must also stay clean — this is
	// the "idempotent cycle" acceptance bar: enable->configure->disable->reload
	// must never leave the gateway permanently degraded.
	if err := m.Reload(ctx, cfg, credentials.SecretBundle{}); err != nil {
		t.Fatalf("Reload after disable (idempotent follow-up): %v", err)
	}
	for _, f := range m.FailedChannels() {
		if f.Name == "irc" {
			t.Fatalf("a follow-up reload with irc still disabled must not resurrect its failure: %+v",
				m.FailedChannels())
		}
	}
}
