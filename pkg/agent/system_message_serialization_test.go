// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for #505 (agent: async-delegate system-message turns
// run unserialized against a live session): an async-origin system message
// (AsyncNotifier.Notify → Run()'s Channel=="system" dispatch) must be routed
// through the per-session sessionWorker instead of a bare goroutine, so the
// reconstructed turn never runs concurrently against the same session's live
// turn. Evidence from UAT A-17: one session ran up to five turns at once,
// each started as a background delegation finished, ending in SIGKILL
// recovery of orphaned tool calls.
package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// serializationGateProvider records provider-call concurrency and holds every
// call until released, so the test can pin a live turn and observe whether
// anything else on the same session starts a provider call before it ends.
type serializationGateProvider struct {
	mu        sync.Mutex
	active    int
	maxActive int
	entries   int
	released  atomic.Bool
	finalMsg  string
}

func (p *serializationGateProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.entries++
	p.active++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	p.mu.Unlock()
	for !p.released.Load() {
		time.Sleep(5 * time.Millisecond)
	}
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	return &providers.LLMResponse{Content: p.finalMsg}, nil
}

func (p *serializationGateProvider) GetDefaultModel() string { return "serialization-gate-mock" }

// countActiveTurnStates counts registered live turnStates across every
// session — the turn-level concurrency observable the #505 assertions use.
func countActiveTurnStates(al *AgentLoop) int {
	n := 0
	al.activeTurnStates.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

// awaitOutboundCount drains msgBus's outbound channel until want messages with
// exactly content have arrived (or 15 s pass), then keeps draining for settle
// so an extra duplicate copy is counted too, and returns the total seen.
func awaitOutboundCount(msgBus *bus.MessageBus, content string, want int, settle time.Duration) int {
	seen := 0
	deadline := time.After(15 * time.Second)
	for seen < want {
		select {
		case out := <-msgBus.OutboundChan():
			if out.Content == content {
				seen++
			}
		case <-deadline:
			return seen
		}
	}
	quiet := time.After(settle)
	for {
		select {
		case out := <-msgBus.OutboundChan():
			if out.Content == content {
				seen++
			}
		case <-quiet:
			return seen
		}
	}
}

func (p *serializationGateProvider) stats() (entries, maxActive int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entries, p.maxActive
}

func (p *serializationGateProvider) waitEntries(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if entries, _ := p.stats(); entries >= want {
			return
		}
		if time.Now().After(deadline) {
			entries, maxActive := p.stats()
			t.Fatalf("timed out waiting for %d provider calls, got %d (maxActive=%d)", want, entries, maxActive)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRun_SystemMessage_SerializedAgainstLiveSessionTurn drives the REAL
// dispatch loop (Run + bus + sessionWorker): a live user turn on a webchat
// session, then two async delegate completions for the same agent+session.
// Never two concurrent provider calls for the session, all three turns run.
func TestRun_SystemMessage_SerializedAgainstLiveSessionTurn(t *testing.T) {
	provider := &serializationGateProvider{finalMsg: "turn done"}
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession("chat", "webchat", delegate.ID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	runCtx, cancelRun := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- al.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runErrCh:
		case <-time.After(5 * time.Second):
			t.Error("Run() did not stop within 5 s")
		}
	})

	// Live user turn on the session (default routing, SessionID stamped the
	// way the gateway's WS handler stamps it).
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()
	require.NoError(t, msgBus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel:   "webchat",
		ChatID:    "direct-one",
		Sender:    bus.SenderInfo{CanonicalID: "webchat_user"},
		SessionID: meta.ID,
		Content:   "go",
	}))
	provider.waitEntries(t, 1)

	// Two async delegate completions for the same agent, same session — the
	// exact shape of UAT A-17's pile-up.
	for _, marker := range []string{"first delegate done", "second delegate done"} {
		require.NoError(t, al.asyncNotifier.Notify(context.Background(), AsyncNotifyEvent{
			Channel:             "webchat",
			ChatID:              "direct-one",
			AgentID:             delegate.ID,
			TranscriptSessionID: meta.ID,
			SourceKind:          "delegate",
			Content:             marker,
		}))
	}

	// While the live turn still holds the provider, NEITHER system message may
	// start a TURN on this session: they must sit in the session worker's
	// inbox. Turn registration (activeTurnStates) is the direct observable —
	// #505 is about concurrent TURNS interleaving their transcript writes, so
	// provider-call concurrency alone would under-detect it.
	time.Sleep(400 * time.Millisecond)
	assert.Equal(t, 1, countActiveTurnStates(al),
		"REGRESSION (#505): an async-origin system message started a turn while the same session's live turn was still running")
	entries, maxActive := provider.stats()
	assert.Equal(t, 1, entries,
		"REGRESSION (#505): an async-origin system message started a provider call while the same session's live turn was still running")
	assert.Equal(t, 1, maxActive, "never two concurrent provider calls for one session")

	// Release and confirm both completions still ran, strictly serially.
	provider.released.Store(true)
	provider.waitEntries(t, 3)
	_, maxActive = provider.stats()
	assert.Equal(t, 1, maxActive,
		"the two reconstructed system-message turns must run one after the other, never concurrently")

	// Each of the three turns delivers its reply exactly once. A system turn's
	// reply is published by processSystemMessage itself; the worker must not
	// publish it a second time.
	assert.Equal(t, 3, awaitOutboundCount(msgBus, "turn done", 3, 500*time.Millisecond),
		"each turn's reply must be delivered exactly once — no duplicate copy of a system-message turn's reply")
}

// TestRun_SystemMessage_NoLiveWorker_GetsOwnSerializedWorker covers the
// spawn path: with no live worker for the session, the system message gets
// its own worker under the scope this session's user traffic resolves to —
// so a user message arriving WHILE that system turn runs is serialized onto
// the same worker rather than racing it.
func TestRun_SystemMessage_NoLiveWorker_GetsOwnSerializedWorker(t *testing.T) {
	provider := &serializationGateProvider{finalMsg: "turn done"}
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession("chat", "webchat", delegate.ID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	runCtx, cancelRun := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- al.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runErrCh:
		case <-time.After(5 * time.Second):
			t.Error("Run() did not stop within 5 s")
		}
	})

	// Async completion FIRST (no live turn on the session anywhere).
	require.NoError(t, al.asyncNotifier.Notify(context.Background(), AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct-one",
		AgentID:             delegate.ID,
		TranscriptSessionID: meta.ID,
		SourceKind:          "delegate",
		Content:             "background result",
	}))
	provider.waitEntries(t, 1)

	// User message for the same session arrives while that turn still holds
	// the provider: it must land on the SAME worker (serialized), not spawn a
	// concurrent turn.
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()
	require.NoError(t, msgBus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel:   "webchat",
		ChatID:    "direct-one",
		Sender:    bus.SenderInfo{CanonicalID: "webchat_user"},
		SessionID: meta.ID,
		Content:   "and now me",
	}))

	time.Sleep(400 * time.Millisecond)
	entries, maxActive := provider.stats()
	assert.Equal(t, 1, entries,
		"the user message must queue behind the running system-message turn on the same session")
	assert.Equal(t, 1, maxActive)

	provider.released.Store(true)
	provider.waitEntries(t, 2)
	_, maxActive = provider.stats()
	assert.Equal(t, 1, maxActive)

	assert.Equal(t, 2, awaitOutboundCount(msgBus, "turn done", 2, 500*time.Millisecond),
		"the system turn and the queued user turn must each deliver their reply exactly once")
}

// TestSessionWorker_Enqueue_SystemMessageSkipsSteering is the unit contract
// behind the no-deadlock property: a system message arriving while its
// session's worker is mid-turn lands in the WORKER INBOX (to run as its own
// serialized turn), never in the in-turn steering queue.
func TestSessionWorker_Enqueue_SystemMessageSkipsSteering(t *testing.T) {
	al, _ := newConcurrentTestAgentLoop(t)
	defer al.Close()

	const scope = "agent:default:session:skip-steering-test"
	w := newSessionWorker(scope, al, func() {})
	al.sessionWorkers.Store(scope, w)
	// Deliberately no runLoop: enqueue is exercised standalone; the buffered
	// inbox keeps the message without a consumer.
	w.inTurn.Store(true)
	defer w.inTurn.Store(false)

	sysMsg := bus.InboundMessage{Channel: "system", ChatID: "webchat:c1", Content: "done"}
	defer al.sessionWorkers.Delete(scope)
	require.True(t, w.enqueue(sysMsg), "a system message on a worker with room must be accepted")

	assert.Equal(t, 1, len(w.inbox),
		"a system message mid-turn must queue in the worker inbox, not the steering queue")
	assert.Equal(t, 0, al.pendingSteeringCountForScope(scope),
		"a system message must never be steered into a live turn's continuation")
}

// TestSessionWorker_Enqueue_SystemMessageInboxFull_NotDropped: a background
// result must never be lost to a full worker inbox. Before this contract a
// full inbox dropped the message and sent the "busy" reply to the internal
// system channel, which nobody reads — the result silently vanished. Now
// enqueue reports it as not delivered so Run() falls back to its unserialized
// dispatch, and a user message keeps the old drop-with-reply behavior.
func TestSessionWorker_Enqueue_SystemMessageInboxFull_NotDropped(t *testing.T) {
	al, msgBus := newConcurrentTestAgentLoop(t)
	defer al.Close()

	const scope = "agent:default:session:inbox-full-test"
	w := newSessionWorker(scope, al, func() {})
	al.sessionWorkers.Store(scope, w)
	defer al.sessionWorkers.Delete(scope)
	// No runLoop: fill the buffered inbox to capacity by hand.
	for i := 0; i < cap(w.inbox); i++ {
		w.inbox <- bus.InboundMessage{Channel: "web", ChatID: "c1", Content: fmt.Sprintf("queued %d", i)}
	}

	sysMsg := bus.InboundMessage{
		Channel: "system", ChatID: "webchat:c1", Content: "background result",
		AsyncTranscriptSessionID: "sess-inbox-full",
	}
	assert.False(t, w.enqueue(sysMsg),
		"a system message that cannot be queued must be reported as not delivered, not dropped")
	assert.Equal(t, cap(w.inbox), len(w.inbox), "the refused system message must not displace anything")
	assert.False(t, al.dispatchSessionWorker(scope, sysMsg),
		"dispatch must report the system message as not delivered so Run() falls back instead of losing it")
	select {
	case out := <-msgBus.OutboundChan():
		t.Fatalf("an undeliverable system message must not publish a busy reply, got %q", out.Content)
	default:
	}

	// Control: a user message on the same full inbox keeps the old semantics
	// (dropped, announced to the user).
	assert.True(t, w.enqueue(bus.InboundMessage{Channel: "web", ChatID: "c1", Content: "user follow-up"}))
	select {
	case out := <-msgBus.OutboundChan():
		assert.Contains(t, out.Content, "could not be queued")
	case <-time.After(2 * time.Second):
		t.Fatal("a user message dropped on a full inbox must still get the busy reply")
	}
}
