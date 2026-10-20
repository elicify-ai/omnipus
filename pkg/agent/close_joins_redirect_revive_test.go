package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// closeJoinStore is what the turn path needs from the agent's store: the
// session store plus the context-window store it type-asserts for.
type closeJoinStore interface {
	session.SessionStore
	session.ContextWindowStore
}

// closeJoinReadGate wraps the agent's real store and, once armed, parks the
// dispatched steered turn's first read of one session key until released.
// Close's own shutdown marker (writeTurnCancelledRestartForActiveTurns) only
// writes (AddMessage/Save), so the gate holds the turn and never Close.
type closeJoinReadGate struct {
	closeJoinStore
	key       atomic.Value // string
	armed     atomic.Bool
	entered   chan struct{}
	enterOnce sync.Once
	release   chan struct{}
	stack     atomic.Value // string: the parked goroutine's stack
}

func (g *closeJoinReadGate) hold(key string) {
	if !g.armed.Load() {
		return
	}
	if want, _ := g.key.Load().(string); want == "" || want != key {
		return
	}
	// Only the dispatched turn's own goroutine is parked; the dispatcher
	// (the redirect waiter, via reconstructSteeredTurn) reads the same key
	// before it spawns the turn and must pass through.
	buf := make([]byte, 64<<10)
	stack := string(buf[:runtime.Stack(buf, false)])
	if !strings.Contains(stack, "runDispatchedSteeredTurn") {
		return
	}
	g.enterOnce.Do(func() {
		g.stack.Store(stack)
		close(g.entered)
	})
	<-g.release
}

func (g *closeJoinReadGate) GetHistory(key string) []providers.Message {
	g.hold(key)
	return g.closeJoinStore.GetHistory(key)
}

func (g *closeJoinReadGate) ReadArchive(ctx context.Context, key string) ([]memory.ArchivedMessage, error) {
	g.hold(key)
	return g.closeJoinStore.ReadArchive(ctx, key)
}

func (g *closeJoinReadGate) Projection(key string) memory.ProjectionMeta {
	g.hold(key)
	return g.closeJoinStore.Projection(key)
}

func (g *closeJoinReadGate) WindowView(ctx context.Context, key string) (session.WindowView, error) {
	g.hold(key)
	return g.closeJoinStore.WindowView(ctx, key)
}

// newSteerALCallerCloses is newSteerAL without its t.Cleanup(al.Close): the
// test below must run Close itself, concurrently, and Close is not safe to
// call twice.
func newSteerALCallerCloses(t *testing.T) *AgentLoop {
	t.Helper()
	al, _, _, _, _ := newTestAgentLoop(t) //nolint:dogsled // the caller owns Close
	home := al.GetConfig().Agents.Defaults.Home
	al.SetSessionMessagingStores(
		session.NewMessageInboxStore(filepath.Join(home, "session_messages")),
		session.NewLifecycleStore(filepath.Join(home, "session_lifecycle")),
	)
	epochDir := filepath.Join(home, "boot_epoch")
	if err := os.MkdirAll(epochDir, 0o700); err != nil {
		t.Fatalf("SETUP create boot epoch dir: %v", err)
	}
	boot := session.NewBootEpochStore(epochDir)
	if epoch, err := boot.Mint(); err != nil || epoch == 0 {
		t.Fatalf("SETUP Mint genuine boot epoch: epoch=%d err=%v", epoch, err)
	}
	al.SetBootEpochStore(boot)
	return al
}

func closeActiveRequestDrainStarted(al *AgentLoop) bool {
	al.activeRequests.mu.Lock()
	defer al.activeRequests.mu.Unlock()
	return al.activeRequests.waitStarted
}

// TestClose_JoinsTheTurnARedirectWaiterDispatchesDuringClose pins the
// shutdown leak behind the TestOneStop_RedirectStopHalfUsesTheSameThreeSecondTimeline
// "TempDir RemoveAll cleanup: directory not empty" reds: delegate redirect's
// resume waiter (steer_redirect.go::awaitStoppedAndRevive) is an active
// request, so Close waits for it in waitActiveRequestsDrain — but by then
// Close has already passed drainSteeredTurns, so the steered turn the waiter
// dispatches (reviveStoppedSession -> goSteeredTurn) is joined by nothing and
// keeps writing the agent home after Close returns.
//
// The waiter is held inside its revive until Close is waiting on the
// active-request drain (i.e. past the steered-turn drain); the dispatched
// turn is then parked in its first history read. Close must not return while
// that turn runs — or, if Close refuses the turn instead, no turn may start
// after Close returned.
func TestClose_JoinsTheTurnARedirectWaiterDispatchesDuringClose(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al := newSteerALCallerCloses(t)
	wireSteerCompletionDeps(t, al)
	provider := &oneStopRedirectProvider{
		entered: make(chan struct{}), softCancelled: make(chan struct{}), release: make(chan struct{}),
	}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: registered agent missing")
	}
	inst.Provider = provider
	backing, ok := inst.Sessions.(closeJoinStore)
	if !ok {
		t.Fatalf("SETUP: the agent's session store %T is not a context-window store", inst.Sessions)
	}
	gate := &closeJoinReadGate{closeJoinStore: backing, entered: make(chan struct{}), release: make(chan struct{})}
	inst.Sessions = gate
	var releaseGateOnce sync.Once
	releaseGate := func() { releaseGateOnce.Do(func() { close(gate.release) }) }

	var reviveTarget atomic.Value // string
	inRevive := make(chan struct{})
	var inReviveOnce sync.Once
	releaseRevive := make(chan struct{})
	var releaseReviveOnce sync.Once
	releaseReviveFn := func() { releaseReviveOnce.Do(func() { close(releaseRevive) }) }
	reviveAfterAvailabilityTestHook = func(id string) {
		if want, _ := reviveTarget.Load().(string); want == "" || id != want {
			return
		}
		inReviveOnce.Do(func() { close(inRevive) })
		<-releaseRevive
	}

	closed := make(chan struct{})
	var closeStarted atomic.Bool
	startClose := func() {
		if closeStarted.CompareAndSwap(false, true) {
			go func() { al.Close(); close(closed) }()
		}
	}
	// Registered after every t.TempDir above, so it runs before their
	// removal: unblock everything, Close exactly once, join the steered
	// turns, then clear the package hook.
	t.Cleanup(func() {
		releaseReviveFn()
		releaseGate()
		provider.open()
		startClose()
		select {
		case <-closed:
		case <-time.After(45 * time.Second):
			t.Error("CLEANUP: Close did not return within 45s")
		}
		joined := make(chan struct{})
		go func() { al.steerAdmission().turns.Wait(); close(joined) }()
		select {
		case <-joined:
		case <-time.After(10 * time.Second):
			t.Error("CLEANUP: steered turns did not join within 10s")
		}
		reviveAfterAvailabilityTestHook = nil
	})

	parentID := newTestSteeringSession(t, al, "ws-close-join-redirect")
	child := launchGoalBearingChild(t, al, parentID, "call-close-join-redirect", goalChildLaunchOptions{live: true})
	reviveTarget.Store(child.SessionID)
	gate.key.Store(child.SessionID)
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: the helper never reached its provider")
	}
	res := delegateToolFor(t, al).Execute(tools.WithTranscriptSessionID(context.Background(), parentID),
		map[string]any{"action": "redirect", "session_id": child.SessionID, "text": "do the other thing instead"})
	if res == nil || res.IsError {
		t.Fatalf("SETUP delegate redirect: %+v", res)
	}
	// Let the stopped turn return so the stop lands and the waiter revives.
	provider.open()
	select {
	case <-inRevive:
	case <-time.After(15 * time.Second):
		t.Fatal("SETUP: the redirect waiter never reached its revive")
	}

	gate.armed.Store(true)
	startClose()
	// The waiter is an active request: once Close waits on the
	// active-request drain it has already passed drainSteeredTurns.
	deadline := time.Now().Add(15 * time.Second)
	for !closeActiveRequestDrainStarted(al) {
		if time.Now().After(deadline) {
			t.Fatal("SETUP: Close never reached the active-request drain")
		}
		time.Sleep(5 * time.Millisecond)
	}
	releaseReviveFn()

	select {
	case <-gate.entered:
		stack, _ := gate.stack.Load().(string)
		if !strings.Contains(stack, "runDispatchedSteeredTurn") {
			t.Fatalf("INSTRUMENT: the parked history read is not the dispatched steered turn:\n%s", stack)
		}
		select {
		case <-closed:
			t.Fatal("AgentLoop.Close returned while the steered turn the redirect waiter dispatched during Close was still running; that turn keeps writing the agent home after Close")
		case <-time.After(3 * time.Second):
		}
		releaseGate()
		select {
		case <-closed:
		case <-time.After(40 * time.Second):
			t.Fatal("Close did not return after the dispatched turn was released")
		}
	case <-closed:
		select {
		case <-gate.entered:
			t.Fatal("a steered turn started after AgentLoop.Close returned; it writes the agent home after Close")
		case <-time.After(2 * time.Second):
		}
	case <-time.After(40 * time.Second):
		t.Fatal("neither the dispatched turn nor Close progressed within 40s")
	}
}
