// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Founder ruling: /clear typed while a turn runs is accepted, stops the turn
// through the one Stop, and then clears. It is never steered into the turn as
// text and never refused for being busy.

// stoppableProvider blocks every model call until its context is canceled (the
// Stop), recording what it was asked.
type stoppableProvider struct {
	mu      sync.Mutex
	calls   int
	sawText []string
	started chan struct{}
	once    sync.Once
}

func newStoppableProvider() *stoppableProvider {
	return &stoppableProvider{started: make(chan struct{})}
}

func (p *stoppableProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	for _, m := range messages {
		p.sawText = append(p.sawText, m.Content)
	}
	p.mu.Unlock()
	p.once.Do(func() { close(p.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}
func (p *stoppableProvider) GetDefaultModel() string { return "stoppable" }
func (p *stoppableProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}
func (p *stoppableProvider) sawContent(s string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range p.sawText {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

// stuckProvider ignores its context: the turn never settles until released.
type stuckProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newStuckProvider() *stuckProvider {
	return &stuckProvider{started: make(chan struct{}), release: make(chan struct{})}
}

func (p *stuckProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.once.Do(func() { close(p.started) })
	<-p.release
	return &providers.LLMResponse{Content: "late"}, nil
}
func (p *stuckProvider) GetDefaultModel() string { return "stuck" }

type busyClearFixture struct {
	al    *AgentLoop
	w     *sessionWorker
	sid   string
	store *session.UnifiedStore
	out   chan bus.OutboundMessage
}

func newBusyClearFixture(t *testing.T, provider providers.LLMProvider) *busyClearFixture {
	t.Helper()
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	sid := newTestSteeringSession(t, al, "")
	adr093UseProvider(t, al, provider)
	scope := "agent:" + testDefaultAgentID + ":session:" + sid
	w := newSessionWorker(scope, al, func() {})
	go w.runLoop()
	t.Cleanup(func() {
		w.cancel()
		<-w.done
	})
	f := &busyClearFixture{al: al, w: w, sid: sid, store: al.GetSessionStore(), out: make(chan bus.OutboundMessage, 64)}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case m := <-al.bus.OutboundChan():
				select {
				case f.out <- m:
				default:
				}
			case <-stop:
				return
			}
		}
	}()
	return f
}

func (f *busyClearFixture) human(content string) bus.InboundMessage {
	m := adr093HumanMessage(content, f.sid)
	m.Sender.CanonicalID = "webchat:tester"
	return m
}

// persistClearRow writes the person's /clear row the way the inbound path does
// before dispatch, and returns the message that names it.
func (f *busyClearFixture) persistClearRow(t *testing.T) bus.InboundMessage {
	t.Helper()
	require.NoError(t, f.store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
		ID: "clear-row-1", Role: "user", Content: "/clear", AgentID: testDefaultAgentID,
	}))
	m := f.human("/clear")
	m.TranscriptEntryID = "clear-row-1"
	return m
}

func (f *busyClearFixture) markerCount(t *testing.T) int {
	t.Helper()
	entries, err := f.store.ReadTranscript(f.sid)
	require.NoError(t, err)
	n := 0
	for _, e := range entries {
		if session.IsClearMarker(e) {
			n++
		}
	}
	return n
}

func waitUntilClear(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestClearWhileBusy_StopsTheTurnThenClears(t *testing.T) {
	prov := newStoppableProvider()
	f := newBusyClearFixture(t, prov)

	require.True(t, f.w.enqueue(f.human("first question")))
	select {
	case <-prov.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first turn never reached the model")
	}
	require.True(t, f.w.inTurn.Load(), "precondition: the worker is inside the turn")

	require.True(t, f.w.enqueue(f.persistClearRow(t)))

	waitUntilClear(t, "the /clear marker after the turn was stopped", 15*time.Second, func() bool { return f.markerCount(t) == 1 })
	require.Equal(t, 1, prov.callCount(), "the stopped turn made one model call and the /clear never reached the model")
	require.False(t, prov.sawContent("/clear"), "/clear must not be steered into the turn as text")

	full, err := f.store.ReadTranscript(f.sid)
	require.NoError(t, err)
	ids := make([]string, 0, len(full))
	for _, e := range full {
		ids = append(ids, e.ID)
	}
	require.Contains(t, ids, "clear-row-1", "the queued /clear row stays persisted (the transcript is whole)")
	view, err := f.store.ReadTranscriptView(f.sid)
	require.NoError(t, err)
	require.True(t, session.IsClearMarker(view.Entries[0]), "the new chat view starts at the marker")
	require.True(t, view.HasEarlier)

	inst, ok := f.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	require.Empty(t, inst.Sessions.GetHistory("agent:"+testDefaultAgentID+":session:"+f.sid), "the model window moved")
}

// When the Stop cannot settle the turn the person is told, nothing is cleared,
// and the /clear does not run later on its own.
func TestClearWhileBusy_UnsettledStopIsAVisibleFailure(t *testing.T) {
	old := clearStopSettleTimeout
	clearStopSettleTimeout = 300 * time.Millisecond
	t.Cleanup(func() { clearStopSettleTimeout = old })
	prov := newStuckProvider()
	f := newBusyClearFixture(t, prov)
	t.Cleanup(func() { close(prov.release) })

	require.True(t, f.w.enqueue(f.human("first question")))
	select {
	case <-prov.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first turn never reached the model")
	}
	require.True(t, f.w.enqueue(f.persistClearRow(t)))

	var reply string
	waitUntilClear(t, "the visible /clear failure", 10*time.Second, func() bool {
		select {
		case m := <-f.out:
			if strings.Contains(m.Content, "/clear was not performed") {
				reply = m.Content
				return true
			}
		default:
		}
		return false
	})
	require.Contains(t, reply, "Nothing was cleared")
	require.Zero(t, f.markerCount(t), "no marker: the clear did not happen")

	// Let the stuck turn finish; the /clear must not now run by itself.
	prov.release <- struct{}{}
	time.Sleep(500 * time.Millisecond)
	require.Zero(t, f.markerCount(t), "a failed /clear never runs late")
}

// An unauthenticated sender cannot ask for the Stop, so the /clear is refused
// visibly and the running turn is left alone.
func TestClearWhileBusy_UnauthenticatedSenderFailsVisibly(t *testing.T) {
	prov := newStoppableProvider()
	f := newBusyClearFixture(t, prov)

	require.True(t, f.w.enqueue(f.human("first question")))
	select {
	case <-prov.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first turn never reached the model")
	}
	m := f.persistClearRow(t)
	m.Sender.CanonicalID = ""
	require.True(t, f.w.enqueue(m))

	waitUntilClear(t, "the visible /clear failure", 10*time.Second, func() bool {
		select {
		case o := <-f.out:
			return strings.Contains(o.Content, "/clear was not performed")
		default:
			return false
		}
	})
	require.True(t, f.w.inTurn.Load(), "the turn was not stopped")
	require.Zero(t, f.markerCount(t))
}

// On an idle worker /clear takes the ordinary command path, unchanged.
func TestClearWhenIdle_RunsAsAnOrdinaryCommand(t *testing.T) {
	prov := newStoppableProvider()
	f := newBusyClearFixture(t, prov)

	require.True(t, f.w.enqueue(f.persistClearRow(t)))
	waitUntilClear(t, "the /clear marker on an idle worker", 15*time.Second, func() bool { return f.markerCount(t) == 1 })
	require.Zero(t, prov.callCount(), "a command never reaches the model")
}

// stoppableProviderReplying answers every call at once with a fixed text.
type replyingProvider struct {
	mu    sync.Mutex
	calls int
	text  string
}

func newStoppableProviderReplying(text string) *replyingProvider {
	return &replyingProvider{text: text}
}

func (p *replyingProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return &providers.LLMResponse{Content: p.text}, nil
}
func (p *replyingProvider) GetDefaultModel() string { return "replying" }
func (p *replyingProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// Delta review R1 #1: a /clear typed during a turn longer than the worker idle
// timeout used to be lost - the worker's idle timer (reset only on a receive)
// had already fired, so the worker exited when the stopped turn ended, and
// stopThenClear pushed the /clear into its dead inbox. The clear must happen (or
// be visibly refused), never vanish.
func TestClearWhileBusy_TurnLongerThanTheIdleTimeoutStillClears(t *testing.T) {
	old := workerIdleTimeout
	workerIdleTimeout = 300 * time.Millisecond
	t.Cleanup(func() { workerIdleTimeout = old })

	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	sid := newTestSteeringSession(t, al, "")
	prov := newStoppableProvider()
	adr093UseProvider(t, al, prov)
	scope := "agent:" + testDefaultAgentID + ":session:" + sid
	stopDrain := make(chan struct{})
	t.Cleanup(func() { close(stopDrain) })
	go func() {
		for {
			select {
			case <-al.bus.OutboundChan():
			case <-stopDrain:
				return
			}
		}
	}()
	t.Cleanup(func() {
		if v, ok := al.sessionWorkers.Load(scope); ok {
			if w, isWorker := v.(*sessionWorker); isWorker {
				w.cancel()
			}
		}
	})

	human := func(content string) bus.InboundMessage {
		m := adr093HumanMessage(content, sid)
		m.Sender.CanonicalID = "webchat:tester"
		return m
	}
	require.True(t, al.dispatchSessionWorker(scope, human("long running work")))
	select {
	case <-prov.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first turn never reached the model")
	}
	// The turn outlives the worker idle timeout several times over.
	time.Sleep(3 * workerIdleTimeout)

	store := al.GetSessionStore()
	require.NoError(t, store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: "clear-row-long", Role: "user", Content: "/clear", AgentID: testDefaultAgentID,
	}))
	clearMsg := human("/clear")
	clearMsg.TranscriptEntryID = "clear-row-long"
	require.True(t, al.dispatchSessionWorker(scope, clearMsg))

	markers := func() int {
		entries, err := store.ReadTranscript(sid)
		require.NoError(t, err)
		n := 0
		for _, e := range entries {
			if session.IsClearMarker(e) {
				n++
			}
		}
		return n
	}
	waitUntilClear(t, "the /clear to happen after the long turn was stopped", 20*time.Second, func() bool { return markers() == 1 })
}

// S1-07 (1 of 3): the idle window starts when the worker goes IDLE, not when it
// received the message. The idle timer used to be reset only on a receive, so a
// turn that outlived workerIdleTimeout left an already-fired timer behind and
// the worker retired the instant the turn ended. The R1 #1 fix drains and
// resets the timer after processTurn returns; this test pins exactly that, with
// an EMPTY inbox at the moment the turn ends, so the only thing that can keep
// the worker alive is the post-turn reset.
func TestSessionWorker_LongTurnDoesNotRetireTheWorker(t *testing.T) {
	old := workerIdleTimeout
	workerIdleTimeout = 300 * time.Millisecond
	t.Cleanup(func() { workerIdleTimeout = old })

	prov := newStuckProvider()
	f := newBusyClearFixture(t, prov)
	t.Cleanup(func() { close(prov.release) })

	require.True(t, f.w.enqueue(f.human("first question")))
	select {
	case <-prov.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first turn never reached the model")
	}
	// The turn outlives the idle timeout several times over.
	time.Sleep(3 * workerIdleTimeout)

	// Let the turn finish and wait until its reply has been published (which
	// happens inside processTurn, before the timer is reset).
	prov.release <- struct{}{}
	waitUntilClear(t, "the long turn's reply", 10*time.Second, func() bool {
		select {
		case <-f.out:
			return true
		default:
			return false
		}
	})

	// Well under workerIdleTimeout, so a worker that is merely idle cannot have
	// retired yet — only a stale, already-fired timer could have taken it out.
	time.Sleep(workerIdleTimeout / 6)
	require.False(t, f.w.exiting.Load(),
		"S1-07: a turn longer than the idle timeout must not leave the worker exiting")
	require.True(t, f.w.enqueue(f.human("second question")),
		"S1-07: the same worker must still accept the next message after a long turn")
}

// S1-07 (2 of 3): pushInbox must refuse while the worker is exiting. runLoop
// sets exiting under inboxMu at the moment it decides to terminate, so a push
// that loses the race is refused rather than accepted into an inbox nobody
// drains — the caller then re-dispatches it to a fresh worker. Accepting it
// would strand the message silently.
func TestSessionWorker_PushInboxRefusesWhileExiting(t *testing.T) {
	al, _ := newConcurrentTestAgentLoop(t)
	defer al.Close()

	w := newSessionWorker("agent:"+testDefaultAgentID+":session:exit-guard", al, func() {})
	t.Cleanup(w.cancel)

	if !w.pushInbox(makeSessionMsg("exit-guard", "first")) {
		t.Fatal("instrument: a live worker must accept a push")
	}
	if n := w.waitingInboxCount(); n != 1 {
		t.Fatalf("instrument: the accepted message must be waiting, got %d", n)
	}

	w.exiting.Store(true)
	if w.pushInbox(makeSessionMsg("exit-guard", "second")) {
		t.Fatal("S1-07: a dying worker must refuse the push — accepting it strands the message in an inbox nobody drains")
	}
	if n := w.waitingInboxCount(); n != 1 {
		t.Fatalf("the refused message must not be queued; inbox holds %d", n)
	}
}
