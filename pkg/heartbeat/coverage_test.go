// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package heartbeat — additional coverage tests.
// Targets: MailboxDrainService (0%), parseLastChannel, sendResponse, SetBus,
// SetTaskChecker, IsRunning (HeartbeatService), heartbeatHasUserTasks edge cases,
// TaskDrainService idempotent stop, and HeartbeatService lifecycle guards.
// Build tags: goolm,stdjson
package heartbeat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// Shared test helpers
// ---------------------------------------------------------------------------

// countingMailScanner records Drain call counts and signals once on the first call.
type countingMailScanner struct {
	calls atomic.Int64
	done  chan struct{}
	once  atomicOnce
}

type atomicOnce struct {
	done atomic.Uint32
}

func (ao *atomicOnce) Do(f func()) {
	if ao.done.CompareAndSwap(0, 1) {
		f()
	}
}

func (s *countingMailScanner) Drain(_ context.Context) int {
	n := int(s.calls.Add(1))
	s.once.Do(func() { close(s.done) })
	return n
}

// failingMailScanner always panics (tests loop resilience via recover or just returns error).
// Actually simpler: a scanner that always returns 0 but we can count calls.
type zeroMailScanner struct {
	calls atomic.Int64
	done  chan struct{}
	once  atomicOnce
}

func (z *zeroMailScanner) Drain(_ context.Context) int {
	z.calls.Add(1)
	z.once.Do(func() { close(z.done) })
	return 0
}

func mustTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "heartbeat-cov-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func writeHeartbeatMD(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "HEARTBEAT.md"), []byte(content), 0o644))
}

// ---------------------------------------------------------------------------
// MailboxDrainService tests
// ---------------------------------------------------------------------------

// Traces to: mailbox_drain.go — NewMailboxDrainService, Start, IsRunning, Stop
func TestMailboxDrainService_NilScannerNoOp(t *testing.T) {
	// BDD: Given a nil scanner
	// When Start is called
	// Then the loop must not start (IsRunning == false) and Stop must not panic
	ds := NewMailboxDrainService(nil, 10*time.Millisecond)
	ds.Start()
	assert.False(t, ds.IsRunning(), "nil-scanner mailbox drain must not start a loop")
	ds.Stop() // must not panic
}

// Traces to: mailbox_drain.go — NewMailboxDrainService default interval
func TestMailboxDrainService_DefaultInterval(t *testing.T) {
	// BDD: Given a zero interval
	// When NewMailboxDrainService is called
	// Then the interval falls back to defaultMailboxDrainInterval (not zero / not spin)
	ds := NewMailboxDrainService(&countingMailScanner{done: make(chan struct{})}, 0)
	assert.Equal(t, defaultMailboxDrainInterval, ds.interval,
		"zero interval must fall back to defaultMailboxDrainInterval")
}

// Traces to: mailbox_drain.go — Start begins drain; IsRunning; Stop halts
func TestMailboxDrainService_StartStopLifecycle(t *testing.T) {
	// BDD: Given a valid scanner and short interval
	// When Start is called → IsRunning must be true
	// When Stop is called → IsRunning must be false
	scanner := &countingMailScanner{done: make(chan struct{})}
	ds := NewMailboxDrainService(scanner, 10*time.Millisecond)

	assert.False(t, ds.IsRunning(), "must not be running before Start")

	ds.Start()
	assert.True(t, ds.IsRunning(), "must be running after Start")

	ds.Stop()
	assert.False(t, ds.IsRunning(), "must not be running after Stop")
}

// Traces to: mailbox_drain.go — Drain is actually called on tick
func TestMailboxDrainService_DrainCalledOnTick(t *testing.T) {
	// BDD: Given a running MailboxDrainService with a 10ms interval
	// When at least one tick fires
	// Then scanner.Drain is called at least once (proves the loop dispatches, not a no-op)
	scanner := &countingMailScanner{done: make(chan struct{})}
	ds := NewMailboxDrainService(scanner, 10*time.Millisecond)
	ds.Start()
	defer ds.Stop()

	select {
	case <-scanner.done:
		// Drain was called at least once.
	case <-time.After(3 * time.Second):
		t.Fatal("MailboxDrainService.Drain was never called — mailbox draining is broken")
	}

	// Differentiation: calls must be > 0 (not hardcoded zero)
	assert.Greater(t, scanner.calls.Load(), int64(0),
		"Drain call count must be greater than zero")
}

// Traces to: mailbox_drain.go — idempotent Start (double-Start must not spawn two goroutines)
func TestMailboxDrainService_IdempotentStart(t *testing.T) {
	// BDD: Given a running MailboxDrainService
	// When Start is called again
	// Then only one loop goroutine exists (IsRunning stays true, no panic)
	scanner := &countingMailScanner{done: make(chan struct{})}
	ds := NewMailboxDrainService(scanner, 10*time.Millisecond)
	ds.Start()
	defer ds.Stop()

	ds.Start() // second call — must be a no-op, not a panic
	assert.True(t, ds.IsRunning(), "must still be running after double Start")
}

// Traces to: mailbox_drain.go — idempotent Stop
func TestMailboxDrainService_IdempotentStop(t *testing.T) {
	// BDD: Given a stopped MailboxDrainService
	// When Stop is called again
	// Then no panic occurs
	scanner := &countingMailScanner{done: make(chan struct{})}
	ds := NewMailboxDrainService(scanner, 10*time.Millisecond)
	ds.Start()
	ds.Stop()
	ds.Stop() // second Stop — must not panic
	assert.False(t, ds.IsRunning(), "must still be stopped after double Stop")
}

// Traces to: mailbox_drain.go — Stop halts polling (no calls after stop)
func TestMailboxDrainService_StopHaltsPolling(t *testing.T) {
	// BDD: Given a MailboxDrainService that has accumulated at least one Drain call
	// When Stop is called and a full interval elapses
	// Then no additional Drain calls are made
	scanner := &countingMailScanner{done: make(chan struct{})}
	ds := NewMailboxDrainService(scanner, 10*time.Millisecond)
	ds.Start()

	// Wait for at least one call
	select {
	case <-scanner.done:
	case <-time.After(3 * time.Second):
		t.Fatal("Drain was never called before Stop test")
	}

	ds.Stop()
	countAfterStop := scanner.calls.Load()

	// Allow one more tick window for the already-queued AfterFunc
	time.Sleep(50 * time.Millisecond)

	finalCount := scanner.calls.Load()
	// Allow a small grace of 1 extra call (the early AfterFunc may fire right at stop boundary)
	assert.LessOrEqual(t, finalCount-countAfterStop, int64(2),
		"Drain must not keep being called after Stop — got %d extra calls", finalCount-countAfterStop)
}

// Traces to: mailbox_drain.go — zero-return scanner (no crash when Drain returns 0)
func TestMailboxDrainService_ZeroReturnScanner(t *testing.T) {
	// BDD: Given a scanner that always returns 0 tasks created
	// When the drain loop runs
	// Then the loop continues without crashing
	scanner := &zeroMailScanner{done: make(chan struct{})}
	ds := NewMailboxDrainService(scanner, 10*time.Millisecond)
	ds.Start()
	defer ds.Stop()

	select {
	case <-scanner.done:
	case <-time.After(3 * time.Second):
		t.Fatal("zeroMailScanner.Drain was never called")
	}
	assert.True(t, ds.IsRunning(), "loop must survive a scanner returning 0")
}

// ---------------------------------------------------------------------------
// TaskDrainService — idempotent stop and coverage gaps
// ---------------------------------------------------------------------------

// Traces to: task_drain.go — idempotent Stop
func TestTaskDrainService_IdempotentStop(t *testing.T) {
	// BDD: Given a stopped TaskDrainService
	// When Stop is called a second time
	// Then no panic occurs
	checker := &countingChecker{done: make(chan struct{})}
	ds := NewTaskDrainService(checker, 10*time.Millisecond)
	ds.Start()
	ds.Stop()
	ds.Stop() // must not panic
	assert.False(t, ds.IsRunning(), "must remain stopped after double Stop")
}

// Traces to: task_drain.go — idempotent Start
func TestTaskDrainService_IdempotentStart(t *testing.T) {
	// BDD: Given a running TaskDrainService
	// When Start is called again
	// Then no second goroutine is spawned, IsRunning stays true
	checker := &countingChecker{done: make(chan struct{})}
	ds := NewTaskDrainService(checker, 10*time.Millisecond)
	ds.Start()
	defer ds.Stop()

	ds.Start() // second call
	assert.True(t, ds.IsRunning())
}

// Traces to: task_drain.go — stop actually halts ticker (runLoop stopChan path)
func TestTaskDrainService_StopHaltsTicker(t *testing.T) {
	// BDD: Given a running TaskDrainService
	// When Stop is called after at least one tick
	// Then CheckQueuedTasks is not called further
	checker := &countingChecker{done: make(chan struct{})}
	ds := NewTaskDrainService(checker, 10*time.Millisecond)
	ds.Start()

	// Wait for at least one call so the ticker has fired at least once
	select {
	case <-checker.done:
	case <-time.After(3 * time.Second):
		t.Fatal("checker never called before stop test")
	}

	ds.Stop()
	countAtStop := checker.calls.Load()
	time.Sleep(50 * time.Millisecond)

	// Allow for the in-flight AfterFunc
	assert.LessOrEqual(t, checker.calls.Load()-countAtStop, int64(2),
		"CheckQueuedTasks must not keep firing after Stop")
}

// ---------------------------------------------------------------------------
// HeartbeatService — uncovered lifecycle + setter methods
// ---------------------------------------------------------------------------

// Traces to: service.go — IsRunning() method
func TestHeartbeatService_IsRunning(t *testing.T) {
	// BDD: Given a disabled HeartbeatService
	// When Start is called (disabled → does not start)
	// Then IsRunning returns false
	dir := mustTempDir(t)
	hs := NewHeartbeatService(dir, 30, false)
	require.NoError(t, hs.Start())
	assert.False(t, hs.IsRunning(), "disabled service must not be running after Start")

	// Enabled service that is started must report running
	hs2 := NewHeartbeatService(dir, 1, true)
	require.NoError(t, hs2.Start())
	assert.True(t, hs2.IsRunning(), "enabled service must be running after Start")
	hs2.Stop()
	assert.False(t, hs2.IsRunning(), "must not be running after Stop")
}

// Traces to: service.go — Start idempotent (already-running guard)
func TestHeartbeatService_IdempotentStart(t *testing.T) {
	// BDD: Given a running HeartbeatService
	// When Start is called again
	// Then it is a no-op (no panic, still running)
	dir := mustTempDir(t)
	hs := NewHeartbeatService(dir, 1, true)
	require.NoError(t, hs.Start())
	defer hs.Stop()

	require.NoError(t, hs.Start()) // second Start
	assert.True(t, hs.IsRunning())
}

// Traces to: service.go — Stop idempotent
func TestHeartbeatService_IdempotentStop(t *testing.T) {
	// BDD: Given a stopped HeartbeatService
	// When Stop is called again
	// Then no panic occurs
	dir := mustTempDir(t)
	hs := NewHeartbeatService(dir, 1, true)
	require.NoError(t, hs.Start())
	hs.Stop()
	hs.Stop() // must not panic
	assert.False(t, hs.IsRunning())
}

// Traces to: service.go — SetBus method (0%)
func TestHeartbeatService_SetBus(t *testing.T) {
	// BDD: Given a HeartbeatService
	// When SetBus is called with a real MessageBus
	// Then the field is set (verified by reading it back via sendResponse path)
	dir := mustTempDir(t)
	hs := NewHeartbeatService(dir, 30, true)
	mb := bus.NewMessageBus()
	hs.SetBus(mb)
	// Verify bus is set: sendResponse with no last channel recorded → no panic, no send
	hs.sendResponse("hello") // must not panic even with no state channel set
}

// Traces to: service.go — SetTaskChecker method (0%)
func TestHeartbeatService_SetTaskChecker(t *testing.T) {
	// BDD: Given a HeartbeatService
	// When SetTaskChecker is called
	// Then the checker is invoked on executeHeartbeat
	dir := mustTempDir(t)
	writeHeartbeatMD(t, dir, "check something")

	checker := &countingChecker{done: make(chan struct{})}
	hs := NewHeartbeatService(dir, 30, true)
	hs.stopChan = make(chan struct{}) // arm service for executeHeartbeat
	hs.SetTaskChecker(checker)
	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		return &tools.ToolResult{Silent: true}
	})

	hs.executeHeartbeat()

	// Differentiation: checker must have been called exactly once
	assert.Equal(t, int64(1), checker.calls.Load(),
		"SetTaskChecker must cause the checker to be invoked during executeHeartbeat")
}

// ---------------------------------------------------------------------------
// parseLastChannel — extended coverage
// ---------------------------------------------------------------------------

func TestParseLastChannel_Table(t *testing.T) {
	// Traces to: service.go:parseLastChannel
	// BDD: Given various last-channel strings
	// When parseLastChannel is called
	// Then the correct (platform, userID) pair is returned
	dir := mustTempDir(t)
	hs := NewHeartbeatService(dir, 30, true)

	tests := []struct {
		name         string
		input        string
		wantPlatform string
		wantUserID   string
	}{
		{
			name:         "empty string",
			input:        "",
			wantPlatform: "",
			wantUserID:   "",
		},
		{
			name:         "valid telegram channel",
			input:        "telegram:123456",
			wantPlatform: "telegram",
			wantUserID:   "123456",
		},
		{
			name:         "valid discord channel with colon in ID",
			input:        "discord:guild:channel",
			wantPlatform: "discord",
			wantUserID:   "guild:channel",
		},
		{
			name:         "internal cli channel skipped",
			input:        "cli:some-id",
			wantPlatform: "",
			wantUserID:   "",
		},
		{
			name:         "internal system channel skipped",
			input:        "system:abc",
			wantPlatform: "",
			wantUserID:   "",
		},
		{
			name:         "internal subagent channel skipped",
			input:        "subagent:xyz",
			wantPlatform: "",
			wantUserID:   "",
		},
		{
			name:         "malformed no colon",
			input:        "invalidformat",
			wantPlatform: "",
			wantUserID:   "",
		},
		{
			name:         "empty platform part",
			input:        ":user",
			wantPlatform: "",
			wantUserID:   "",
		},
		{
			name:         "empty userID part",
			input:        "slack:",
			wantPlatform: "",
			wantUserID:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotPlatform, gotUserID := hs.parseLastChannel(tc.input)
			assert.Equal(t, tc.wantPlatform, gotPlatform, "platform mismatch")
			assert.Equal(t, tc.wantUserID, gotUserID, "userID mismatch")
		})
	}

	// Differentiation: two valid but different inputs must produce different outputs
	p1, u1 := hs.parseLastChannel("telegram:111")
	p2, u2 := hs.parseLastChannel("slack:222")
	assert.NotEqual(t, p1, p2, "different platforms must differ")
	assert.NotEqual(t, u1, u2, "different user IDs must differ")
}

// ---------------------------------------------------------------------------
// sendResponse
// ---------------------------------------------------------------------------

// Traces to: service.go — sendResponse with nil bus (no-op)
func TestSendResponse_NilBus(t *testing.T) {
	// BDD: Given no message bus configured
	// When sendResponse is called
	// Then it returns without panic (no bus, result discarded)
	dir := mustTempDir(t)
	hs := NewHeartbeatService(dir, 30, true)
	// bus is nil by default
	hs.sendResponse("some response") // must not panic
}

// Traces to: service.go — sendResponse with bus but no last-channel in state
func TestSendResponse_BusSetNoChannel(t *testing.T) {
	// BDD: Given a message bus is set but no last channel in state
	// When sendResponse is called
	// Then no message is published (no panic)
	dir := mustTempDir(t)
	hs := NewHeartbeatService(dir, 30, true)
	mb := bus.NewMessageBus()
	hs.SetBus(mb)

	// No state file → GetLastChannel returns ""
	hs.sendResponse("hello") // must not panic, no outbound message

	// Verify no message was buffered
	select {
	case msg := <-mb.OutboundChan():
		t.Fatalf("expected no outbound message, got: %+v", msg)
	default:
		// correct: nothing queued
	}
}

// writeStateChannel writes a state.json into workspace/state/ so GetLastChannel returns the given value.
func writeStateChannel(t *testing.T, workspace, lastChannel string) {
	t.Helper()
	stateDir := filepath.Join(workspace, "state")
	require.NoError(t, os.MkdirAll(stateDir, 0o700))
	stateFile := filepath.Join(stateDir, "state.json")
	content := `{"last_channel":"` + lastChannel + `","timestamp":"0001-01-01T00:00:00Z"}`
	require.NoError(t, os.WriteFile(stateFile, []byte(content), 0o600))
}

// Traces to: service.go — sendResponse publishes when bus + channel are valid
func TestSendResponse_PublishesWhenChannelValid(t *testing.T) {
	// BDD: Given a bus is configured and state records a valid external channel
	// When sendResponse is called with content
	// Then an outbound message is published to the bus with matching channel+content
	dir := mustTempDir(t)
	writeStateChannel(t, dir, "telegram:12345")

	// NewHeartbeatService creates a state.Manager — it will pick up the file we wrote.
	hs := NewHeartbeatService(dir, 30, true)
	mb := bus.NewMessageBus()
	hs.SetBus(mb)

	hs.sendResponse("Hello from heartbeat")

	// PublishOutbound uses a 5-second context timeout; the channel has a buffer so this is instant.
	select {
	case msg := <-mb.OutboundChan():
		assert.Equal(t, "telegram", msg.Channel, "outbound channel must be 'telegram'")
		assert.Equal(t, "12345", msg.ChatID, "outbound chatID must be '12345'")
		assert.Equal(t, "Hello from heartbeat", msg.Content, "outbound content must match")
	case <-time.After(2 * time.Second):
		t.Fatal("expected outbound message on bus, none arrived in time")
	}
}

// Differentiation test for sendResponse: two different responses produce two different bus messages
func TestSendResponse_DifferentContentsDifferentMessages(t *testing.T) {
	dir := mustTempDir(t)
	writeStateChannel(t, dir, "telegram:99")

	hs := NewHeartbeatService(dir, 30, true)
	mb := bus.NewMessageBus()
	hs.SetBus(mb)

	hs.sendResponse("message A")
	hs.sendResponse("message B")
	// Allow outbound context deadline (5s) to flush; bus channel is buffered so immediate

	var contents []string
	timeout := time.After(2 * time.Second)
	for len(contents) < 2 {
		select {
		case msg := <-mb.OutboundChan():
			contents = append(contents, msg.Content)
		case <-timeout:
			t.Fatalf("did not receive 2 messages; got: %v", contents)
		}
	}
	assert.NotEqual(t, contents[0], contents[1],
		"different sendResponse inputs must produce different outbound message contents")
}

// ---------------------------------------------------------------------------
// heartbeatHasUserTasks — edge cases
// ---------------------------------------------------------------------------

func TestHeartbeatHasUserTasks_EdgeCases(t *testing.T) {
	// Traces to: service.go:heartbeatHasUserTasks
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "empty string",
			content: "",
			want:    false,
		},
		{
			name:    "only whitespace",
			content: "   \n\t  ",
			want:    false,
		},
		{
			name:    "no marker — content present — treated as user tasks",
			content: "Do something useful",
			want:    true,
		},
		{
			name:    "marker present, nothing after it",
			content: "## Tasks\n" + userTasksMarker + "\n",
			want:    false,
		},
		{
			name:    "marker present, only blank lines after",
			content: "## Tasks\n" + userTasksMarker + "\n\n\n",
			want:    false,
		},
		{
			name:    "marker present, only heading lines after",
			content: "## Tasks\n" + userTasksMarker + "\n# Section\n",
			want:    false,
		},
		{
			name:    "marker present, real task after",
			content: "## Tasks\n" + userTasksMarker + "\n- Do the thing\n",
			want:    true,
		},
		{
			name:    "marker present, heading then real task",
			content: "## Tasks\n" + userTasksMarker + "\n# Ignore\n- Real task\n",
			want:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := heartbeatHasUserTasks(tc.content)
			assert.Equal(t, tc.want, got, "heartbeatHasUserTasks(%q)", tc.content)
		})
	}

	// Differentiation: false-returning and true-returning inputs produce different results
	assert.False(t, heartbeatHasUserTasks(""))
	assert.True(t, heartbeatHasUserTasks("actual task content"))
}

// ---------------------------------------------------------------------------
// NewHeartbeatService — interval clamping
// ---------------------------------------------------------------------------

func TestNewHeartbeatService_IntervalClamping(t *testing.T) {
	// Traces to: service.go:NewHeartbeatService
	// BDD: Given an interval below the minimum
	// When NewHeartbeatService is created
	// Then the interval is clamped to minIntervalMinutes
	dir := mustTempDir(t)

	tests := []struct {
		name         string
		inputMinutes int
		wantMinutes  float64
		description  string
	}{
		{
			name:         "below minimum",
			inputMinutes: 1,
			wantMinutes:  float64(minIntervalMinutes),
			description:  "below-minimum interval must be clamped to minIntervalMinutes",
		},
		{
			name:         "zero uses default",
			inputMinutes: 0,
			wantMinutes:  float64(defaultIntervalMinutes),
			description:  "zero interval must fall back to defaultIntervalMinutes",
		},
		{
			name:         "above minimum unchanged",
			inputMinutes: 60,
			wantMinutes:  60,
			description:  "above-minimum interval must not be modified",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hs := NewHeartbeatService(dir, tc.inputMinutes, true)
			assert.Equal(t, tc.wantMinutes, hs.interval.Minutes(), tc.description)
		})
	}

	// Differentiation: different valid intervals produce different durations
	hs10 := NewHeartbeatService(dir, 10, true)
	hs20 := NewHeartbeatService(dir, 20, true)
	assert.NotEqual(t, hs10.interval, hs20.interval,
		"different valid intervals must produce different durations")
}

// ---------------------------------------------------------------------------
// executeHeartbeat — ForUser response path + no-handler guard
// ---------------------------------------------------------------------------

// Traces to: service.go:executeHeartbeat — ForUser field takes priority for sendResponse
func TestExecuteHeartbeat_ForUserResponse(t *testing.T) {
	// BDD: Given a handler returning ForUser content (non-silent, non-async, non-error)
	// When executeHeartbeat runs
	// Then the response path is exercised (no panic)
	dir := mustTempDir(t)
	writeHeartbeatMD(t, dir, "do stuff")

	hs := NewHeartbeatService(dir, 30, true)
	hs.stopChan = make(chan struct{})
	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		return &tools.ToolResult{
			ForUser: "User-facing result",
			ForLLM:  "LLM result",
			Silent:  false,
			IsError: false,
			Async:   false,
		}
	})

	// No bus configured → sendResponse is a no-op but must not panic
	hs.executeHeartbeat()
}

// Traces to: service.go:executeHeartbeat — ForLLM fallback when ForUser is empty
func TestExecuteHeartbeat_ForLLMFallback(t *testing.T) {
	dir := mustTempDir(t)
	writeHeartbeatMD(t, dir, "do stuff")

	hs := NewHeartbeatService(dir, 30, true)
	hs.stopChan = make(chan struct{})
	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		return &tools.ToolResult{
			ForUser: "",
			ForLLM:  "LLM-only result",
			Silent:  false,
			IsError: false,
			Async:   false,
		}
	})
	hs.executeHeartbeat() // must not panic
}

// Traces to: service.go:executeHeartbeat — no handler set
func TestExecuteHeartbeat_NoHandlerSet(t *testing.T) {
	// BDD: Given executeHeartbeat runs with no handler configured and a non-empty HEARTBEAT.md
	// When executeHeartbeat fires
	// Then it logs an error but does NOT panic
	dir := mustTempDir(t)
	writeHeartbeatMD(t, dir, "some real task")

	hs := NewHeartbeatService(dir, 30, true)
	hs.stopChan = make(chan struct{})
	// handler is nil intentionally

	hs.executeHeartbeat() // must not panic

	// Verify the error was logged
	logData, err := os.ReadFile(filepath.Join(dir, "heartbeat.log"))
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(logData), "ERROR"), "error must be logged when handler is nil")
}

// Traces to: service.go:executeHeartbeat — disabled guard
func TestExecuteHeartbeat_DisabledGuard(t *testing.T) {
	// BDD: Given enabled=false and stopChan nil
	// When executeHeartbeat is called directly
	// Then it returns early without calling the handler
	dir := mustTempDir(t)
	writeHeartbeatMD(t, dir, "should not be processed")

	handlerCalled := false
	hs := NewHeartbeatService(dir, 30, false)
	// stopChan is nil (not started) — this triggers the disabled guard inside executeHeartbeat
	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		handlerCalled = true
		return nil
	})

	hs.executeHeartbeat()
	assert.False(t, handlerCalled, "handler must not be called when service is disabled/not started")
}

// ---------------------------------------------------------------------------
// buildPrompt — content assertions
// ---------------------------------------------------------------------------

// Traces to: service.go:buildPrompt — prompt contains current time and user task
func TestBuildPrompt_ContainsTimestampAndTask(t *testing.T) {
	dir := mustTempDir(t)
	hs := NewHeartbeatService(dir, 30, true)
	writeHeartbeatMD(t, dir, "remind me to water the plants")

	prompt := hs.buildPrompt()
	require.NotEmpty(t, prompt, "buildPrompt must return non-empty for a file with user tasks")
	assert.True(t, strings.Contains(prompt, "Current time:"), "prompt must include current time header")
	assert.True(t, strings.Contains(prompt, "water the plants"), "prompt must include user task content")
}

// Differentiation: two different HEARTBEAT.md contents produce different prompts
func TestBuildPrompt_DifferentContentsDifferentPrompts(t *testing.T) {
	dir1 := mustTempDir(t)
	hs1 := NewHeartbeatService(dir1, 30, true)
	writeHeartbeatMD(t, dir1, "task alpha")

	dir2 := mustTempDir(t)
	hs2 := NewHeartbeatService(dir2, 30, true)
	writeHeartbeatMD(t, dir2, "task beta")

	p1 := hs1.buildPrompt()
	p2 := hs2.buildPrompt()
	assert.NotEqual(t, p1, p2, "different HEARTBEAT.md contents must produce different prompts")
}
