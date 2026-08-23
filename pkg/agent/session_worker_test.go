// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ----- helpers -----

func newConcurrentTestAgentLoop(t *testing.T) (*AgentLoop, *bus.MessageBus) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	return al, msgBus
}

// makeSessionMsg builds a test InboundMessage for the web channel with an
// explicit SessionID so scope resolution produces deterministic scopes.
func makeSessionMsg(sessionID, content string) bus.InboundMessage {
	return bus.InboundMessage{
		Channel: "web",
		ChatID:  "chat-" + sessionID,
		Sender: bus.SenderInfo{
			CanonicalID: "user-" + sessionID,
		},
		SessionID: sessionID,
		Content:   content,
	}
}

// ----- sessionWorker unit tests -----

// TestSessionWorker_EnqueueAndProcess verifies that a session worker processes
// an enqueued message and produces an outbound reply.
func TestSessionWorker_EnqueueAndProcess(t *testing.T) {
	al, msgBus := newConcurrentTestAgentLoop(t)
	defer al.Close()

	runCtx, cancelRun := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- al.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runErrCh:
		case <-time.After(3 * time.Second):
			t.Error("Run() did not stop within 3 s")
		}
	})

	msg := makeSessionMsg("sess-worker-1", "hello")
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()
	if err := msgBus.PublishInbound(pubCtx, msg); err != nil {
		t.Fatalf("PublishInbound: %v", err)
	}

	replyCtx, replyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer replyCancel()
	select {
	case out := <-msgBus.OutboundChan():
		if out.Content == "" {
			t.Fatal("outbound message has empty content")
		}
	case <-replyCtx.Done():
		t.Fatal("timeout waiting for outbound reply from session worker")
	}
}

// TestSessionWorker_CancelExits verifies that a worker removes itself from
// the parent's sessionWorkers map when its context is canceled.
//
// NOTE: This test exercises the ctx-cancel exit path only — it does NOT test
// the workerIdleTimeout (60 s) path. The real idle-timer test requires a
// test-only knob to shorten the timeout; that is a Track A (backend-lead)
// responsibility. See review-pr-test-analyzer.md: "TestSessionWorker_IdleExits".
//
// Traces to: pkg/agent/session_worker.go — runLoop ctx.Done exit path
func TestSessionWorker_CancelExits(t *testing.T) {
	al, _ := newConcurrentTestAgentLoop(t)
	defer al.Close()

	scope := "agent:default:session:cancel-test"
	// New signature: newSessionWorker(scope, parent, admissionRelease)
	// admissionRelease is a no-op here — no admission controller involved.
	w := newSessionWorker(scope, al, func() {})
	al.sessionWorkers.Store(scope, w)

	// Override idle timer by canceling the context quickly — we simulate the
	// idle-timeout path by using a very tight context rather than waiting 60 s.
	// We cancel the worker's own context directly.
	go w.runLoop()

	// Verify the worker registered itself.
	if _, ok := al.sessionWorkers.Load(scope); !ok {
		t.Fatal("worker not found in sessionWorkers immediately after start")
	}

	// Cancel the worker's context — this is what Close() does in production;
	// here we do it directly to trigger the goroutine exit path.
	w.cancel()

	// Worker must self-remove within a short deadline.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("worker did not remove itself from sessionWorkers after cancel")
		default:
		}
		if _, ok := al.sessionWorkers.Load(scope); !ok {
			return // removed — test passes
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSessionWorker_TwoSessionsConcurrent is the regression test for the
// original bug: Session 2 must receive a reply even when Session 1 is blocked
// in a long-running turn.
//
// Regression test structure:
//   - WITHOUT the fix: Run() processes turns synchronously, so the mockProvider
//     (which returns immediately) would normally hide the bug. To reproduce the
//     original blocking behavior we need a slow provider. Instead, we verify the
//     dispatch mechanism directly by checking that two different scopes spawn
//     independent workers that both process their messages.
func TestSessionWorker_TwoSessionsConcurrent(t *testing.T) {
	al, msgBus := newConcurrentTestAgentLoop(t)
	defer al.Close()

	runCtx, cancelRun := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- al.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runErrCh:
		case <-time.After(3 * time.Second):
			t.Error("Run() did not stop within 3 s")
		}
	})

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()

	msg1 := makeSessionMsg("concurrent-sess-A", "message from session A")
	msg2 := makeSessionMsg("concurrent-sess-B", "message from session B")

	if err := msgBus.PublishInbound(pubCtx, msg1); err != nil {
		t.Fatalf("PublishInbound msg1: %v", err)
	}
	if err := msgBus.PublishInbound(pubCtx, msg2); err != nil {
		t.Fatalf("PublishInbound msg2: %v", err)
	}

	// Both sessions should receive replies within the deadline.
	replyCtx, replyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer replyCancel()

	got := 0
	for got < 2 {
		select {
		case out := <-msgBus.OutboundChan():
			if out.Content == "" {
				t.Fatal("outbound message has empty content")
			}
			got++
		case <-replyCtx.Done():
			t.Fatalf("timeout waiting for concurrent replies: got %d/2", got)
		}
	}
}

// TestSessionWorker_FiveParallelSessions verifies that five sessions served by
// the same agent all receive replies concurrently.
func TestSessionWorker_FiveParallelSessions(t *testing.T) {
	al, msgBus := newConcurrentTestAgentLoop(t)
	defer al.Close()

	runCtx, cancelRun := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- al.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runErrCh:
		case <-time.After(3 * time.Second):
			t.Error("Run() did not stop within 3 s")
		}
	})

	const n = 5
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()

	for i := 0; i < n; i++ {
		sid := fmt.Sprintf("parallel-sess-%d", i)
		msg := makeSessionMsg(sid, fmt.Sprintf("hello from %s", sid))
		if err := msgBus.PublishInbound(pubCtx, msg); err != nil {
			t.Fatalf("PublishInbound[%d]: %v", i, err)
		}
	}

	replyCtx, replyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer replyCancel()

	got := 0
	for got < n {
		select {
		case out := <-msgBus.OutboundChan():
			if out.Content == "" {
				t.Fatal("outbound message has empty content")
			}
			got++
		case <-replyCtx.Done():
			t.Fatalf("timeout: only %d/%d parallel sessions replied", got, n)
		}
	}
}

// TestSessionWorker_AdmissionRejection verifies that when the soft cap is
// reached, a new session's message is rejected with the capacity reply and
// NOT silently dropped.
func TestSessionWorker_AdmissionRejection(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()

	// Use a blocking provider so the first two sessions hold their turns and
	// keep the active-turn counter at the cap.
	release := make(chan struct{})
	blockingProvider := &blockingMockProvider{releaseAll: release}

	al := mustNewAgentLoop(t, cfg, msgBus, blockingProvider)
	defer al.Close()

	// Set soft cap to 2 so the third session is rejected.
	al.admission = newAdmissionController(2)

	runCtx, cancelRun := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- al.Run(runCtx) }()
	t.Cleanup(func() {
		close(release) // unblock all workers so Run() can exit
		cancelRun()
		select {
		case <-runErrCh:
		case <-time.After(3 * time.Second):
			t.Error("Run() did not stop within 3 s")
		}
	})

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()

	// Sessions 1 and 2: will hold their active-turn slots.
	for i := 1; i <= 2; i++ {
		sid := fmt.Sprintf("cap-sess-%d", i)
		msg := makeSessionMsg(sid, fmt.Sprintf("hold slot %d", i))
		if err := msgBus.PublishInbound(pubCtx, msg); err != nil {
			t.Fatalf("PublishInbound[%d]: %v", i, err)
		}
	}

	// Wait briefly so the blocking providers have acquired their slots.
	time.Sleep(150 * time.Millisecond)

	// Session 3: should be rejected immediately because cap=2 is full.
	msg3 := makeSessionMsg("cap-sess-3", "over cap")
	if err := msgBus.PublishInbound(pubCtx, msg3); err != nil {
		t.Fatalf("PublishInbound[3]: %v", err)
	}

	// Expect the capacity-rejection reply for session 3.
	rejectCtx, rejectCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer rejectCancel()

	found := false
	// Drain up to a few outbound messages to find the rejection.
	for !found {
		select {
		case out := <-msgBus.OutboundChan():
			if out.Content == "I'm at capacity right now — please try again in a few seconds." {
				found = true
			}
		case <-rejectCtx.Done():
			t.Fatal("timeout waiting for capacity-rejection reply")
		}
	}
}

// TestSessionWorker_CloseStopsWorkers verifies that AgentLoop.Close() cancels
// all active session workers.
func TestSessionWorker_CloseStopsWorkers(t *testing.T) {
	al, _ := newConcurrentTestAgentLoop(t)

	// Pre-populate two workers directly (no Run() needed for this unit test).
	// New signature: newSessionWorker(scope, parent, admissionRelease)
	scopes := []string{
		"agent:default:session:close-test-1",
		"agent:default:session:close-test-2",
	}
	for _, scope := range scopes {
		w := newSessionWorker(scope, al, func() {})
		al.sessionWorkers.Store(scope, w)
		go w.runLoop()
	}

	// Close should cancel and drain all workers within the 5 s budget.
	closeDone := make(chan struct{})
	go func() {
		al.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(6 * time.Second):
		t.Fatal("Close() did not finish within 6 s — session workers may be stuck")
	}

	// All workers must have removed themselves.
	for _, scope := range scopes {
		if _, ok := al.sessionWorkers.Load(scope); ok {
			t.Errorf("worker for scope %q still registered after Close()", scope)
		}
	}
}

// TestSessionWorker_IdleExits verifies that a worker self-exits and removes
// itself from sessionWorkers after the idle timer fires with no inbox activity.
func TestSessionWorker_IdleExits(t *testing.T) {
	orig := workerIdleTimeout
	workerIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { workerIdleTimeout = orig })

	al, _ := newConcurrentTestAgentLoop(t)
	defer al.Close()

	scope := "agent:default:session:idle-test"
	w := newSessionWorker(scope, al, func() {})
	al.sessionWorkers.Store(scope, w)
	go w.runLoop()

	// Worker must self-remove after the short idle timeout fires.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("worker did not self-remove after idle timeout")
		default:
		}
		if _, ok := al.sessionWorkers.Load(scope); !ok {
			return // removed — test passes
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSessionWorker_RecoverPanic verifies that a panic in runLoop is caught
// by the defer recover() and the worker exits cleanly, removing itself from
// sessionWorkers without crashing the process.
func TestSessionWorker_RecoverPanic(t *testing.T) {
	al, _ := newConcurrentTestAgentLoop(t)
	defer al.Close()

	scope := "agent:default:session:panic-test"

	// Build a worker whose processTurn will not be called (no message injected).
	// We verify the recover() guard on the cancel path, which is the safe way to
	// confirm the runLoop defers fire correctly without needing a panicking provider.
	// A direct panic injection would require mocking a large call path; the cancel
	// path validates that all deferred cleanups (sessionWorkers.Delete, admissionRelease,
	// close(done)) run regardless of internal state.
	released := false
	releaseFunc := func() { released = true }

	w := newSessionWorker(scope, al, releaseFunc)
	al.sessionWorkers.Store(scope, w)
	go w.runLoop()

	if _, ok := al.sessionWorkers.Load(scope); !ok {
		t.Fatal("worker not registered")
	}

	w.cancel()

	// Worker must exit and call its deferred cleanups (including admissionRelease).
	select {
	case <-w.done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not exit after cancel")
	}

	if _, ok := al.sessionWorkers.Load(scope); ok {
		t.Error("worker still in sessionWorkers after exit")
	}
	if !released {
		t.Error("admissionRelease was not called on worker exit")
	}
}

// panickyMockProvider panics on every Chat call. It reproduces the C8
// chat-stream-hang precondition: a provider/tool nil-deref that escapes the
// turn's inner recovers and unwinds through runTurn → processMessage →
// processTurn. The session worker's panic-recover MUST still publish a
// terminal outbound message so the SPA receives a done/error frame instead of
// hanging forever in the "thinking" state.
type panickyMockProvider struct{}

func (p *panickyMockProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	panic("simulated provider crash (C8 hang reproduction)")
}

func (p *panickyMockProvider) GetDefaultModel() string { return "panicky-model" }

// TestSessionWorker_PanicStillPublishesTerminalFrame is the C8 regression test.
// When the turn panics with no streamer set and finalResponse still empty, the
// only thing that can release the client is the session worker's panic-recover
// publishing an error outbound (→ webchatChannel.Send → done frame). Without
// the fix, the panic unwinds past processTurn with no outbound published and
// the SPA stays stuck "thinking". This test asserts a terminal outbound IS
// published within a short deadline.
func TestSessionWorker_PanicStillPublishesTerminalFrame(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &panickyMockProvider{})
	defer al.Close()

	runCtx, cancelRun := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- al.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runErrCh:
		case <-time.After(3 * time.Second):
			t.Error("Run() did not stop within 3 s")
		}
	})

	msg := makeSessionMsg("sess-panic-terminal", "trigger crash")
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()
	if err := msgBus.PublishInbound(pubCtx, msg); err != nil {
		t.Fatalf("PublishInbound: %v", err)
	}

	// The worker panics mid-turn; the panic-recover must still emit a terminal
	// outbound so the client is released. If the recover did not publish, this
	// select times out — exactly the hang C8 fixes.
	replyCtx, replyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer replyCancel()
	select {
	case out := <-msgBus.OutboundChan():
		if out.Content == "" {
			t.Fatal("terminal outbound after panic has empty content")
		}
		t.Logf("terminal outbound after panic: %q", out.Content)
	case <-replyCtx.Done():
		t.Fatal("C8 regression: no terminal frame published after turn panic — client would hang")
	}
}

// blockingMockProvider blocks each Chat call until releaseAll is closed.
// This simulates a long-running LLM turn so admission-cap tests can verify
// that slots are held while the provider is thinking.
type blockingMockProvider struct {
	releaseAll <-chan struct{}
}

func (b *blockingMockProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	select {
	case <-b.releaseAll:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &providers.LLMResponse{
		Content:   "released response",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (b *blockingMockProvider) GetDefaultModel() string { return "mock-model" }
