// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W2 D4 RED pack, part 2 — T27's stale-effect exclusion (ADR-20260928
// sub-agent control plane, frozen asset cd20cf8b, T27 row and D5). Every
// expected value derives from the ADR: "all delayed effects of the older
// Stop/Stop all remain bound to the execution they selected. They cannot
// remove the replacement's queued admission, interrupt its active turn,
// escalate against its provider call, clear its fence/note or land it
// stopped. A genuinely newer Stop captures the replacement identity and
// remains effective" (D5). The replacement runs at the SAME generation in
// the SAME boot — exactly the case generation-only targeting cannot rule.
//
// Scenario shape (T27): the old stop is paused AFTER its durable child
// fence and BEFORE its live effect; the selected run lands stopped; an
// explicit RESUME re-admits the child at the same generation; then the old
// callback (and, through the real delegate tool, the second queue-removal
// caller) is released against the replacement.
//
// The pause is a t27GatedCancel: a NORMAL injected GenerationCancelFunc
// that records the stamped target, blocks, and on release delegates to the
// production al.SteerGenerationCancel — nothing production is reimplemented
// or bypassed, and no test-only global hook exists (both are forbidden by
// T27's own oracle). The cascade stamps the fence/note before it invokes
// the callback, and every test POLLS the store until the fence is durable
// before arranging the rest, so the "after its durable child fence" half of
// the window is proven, not assumed. The ungated positive control
// (TestT27_NewStop_AfterResumeStillStopsReplacement) proves the same wiring
// really removes entries and aborts turns when a stop is genuinely newer —
// without it a green in the stale tests would be vacuous.
//
// The selected run's STOPPED LANDING during the window goes through
// al.reportSteeredSessionTerminalUpward — the production landing half the
// cancelled turn's own completion and the never-ran finalizer both call.
// It is invoked at its production entry point because a real turn exit
// cannot be deterministically interleaved with the gated callback without
// a forbidden test-only hook.
//
// Deferred to the identity carrier (W2b), deliberately not approximated:
// the soft delegate path's graceful-interrupt timing separation (no
// injectable seam between StopSubtree's stamp and its post-return
// Interrupt), the hard-escalation timer (pkg/tools cancel_grace backstop,
// timer-driven, cross-package), the late-turn completion landing (needs
// D2 CRIT-001's execution-identity commit check to even distinguish the
// flights — today same-generation commits are indistinguishable by the gap
// this pack pins), and any run_id distinctness assertion (no run_id exists
// on admission, record or callback; none is faked).
package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// t27ParkGate parks exactly one model call on its own release channel.
type t27ParkGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	final   string
}

func (g *t27ParkGate) letFinish() { g.once.Do(func() { close(g.release) }) }

// t27ParkProvider parks every turn on its OWN gate — unlike parkedProvider,
// whose single shared release channel ends every parked turn at once. T27
// needs one turn released (the old run's) while another stays live (the
// replacement's), so gates are staged per turn and released individually.
type t27ParkProvider struct {
	mu    sync.Mutex
	gates []*t27ParkGate
	next  int
}

func (p *t27ParkProvider) newGate(final string) *t27ParkGate {
	g := &t27ParkGate{entered: make(chan struct{}), release: make(chan struct{}), final: final}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gates = append(p.gates, g)
	return g
}

func (p *t27ParkProvider) releaseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, g := range p.gates {
		g.letFinish()
	}
}

func (p *t27ParkProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	if p.next >= len(p.gates) {
		p.mu.Unlock()
		return nil, fmt.Errorf("t27ParkProvider: unexpected model call #%d (no gate staged)", p.next+1)
	}
	g := p.gates[p.next]
	p.next++
	p.mu.Unlock()
	close(g.entered)
	select {
	case <-g.release:
		return &providers.LLMResponse{Content: g.final}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *t27ParkProvider) GetDefaultModel() string { return "t27-park-test" }

func installT27ParkProvider(t *testing.T, al *AgentLoop) *t27ParkProvider {
	t.Helper()
	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("test agent is not registered")
	}
	p := &t27ParkProvider{}
	agentInst.Provider = p
	t.Cleanup(p.releaseAll)
	return p
}

// t27GatedCancel wraps the production live-turn adapter so the old stop's
// live effect fires only when the test opens the gate. It is the "pause an
// old Stop after its durable fence and before its live effect" window T27
// specifies, built from a normal injected GenerationCancelFunc — on release
// it delegates to the real adapter, unchanged.
type t27GatedCancel struct {
	mu       sync.Mutex
	stamped  []string
	entered  chan struct{}
	open     chan struct{}
	once     sync.Once
	realTurn GenerationCancelFunc
}

func newT27GatedCancel(realTurn GenerationCancelFunc) *t27GatedCancel {
	return &t27GatedCancel{
		entered:  make(chan struct{}),
		open:     make(chan struct{}),
		realTurn: realTurn,
	}
}

func (g *t27GatedCancel) cancelTurn(ctx context.Context, sessionID string, generation int) (GenerationCancelResult, error) {
	g.mu.Lock()
	g.stamped = append(g.stamped, fmt.Sprintf("%s:%d", sessionID, generation))
	g.mu.Unlock()
	var once sync.Once
	once.Do(func() { close(g.entered) })
	<-g.open
	return g.realTurn(ctx, sessionID, generation)
}

func (g *t27GatedCancel) openGate() { g.once.Do(func() { close(g.open) }) }

// t27WaitFor polls cond until it holds or the timeout lapses.
func t27WaitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// t27StopLanded asserts the production landing of the selected run: stopped
// at the same generation, fence spent, lasting note retained (D2 CRIT-001).
func t27StopLanded(t *testing.T, lifecycle *session.LifecycleStore, sessionID string, generation int) {
	t.Helper()
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		t.Fatalf("Load(%s): %v", sessionID, err)
	}
	if rec.State != session.LifecycleStopped || rec.Generation != generation || rec.StopNote == nil {
		t.Fatalf("setup: the selected run did not land stopped at generation %d with a note: state=%q generation=%d note=%v",
			generation, rec.State, rec.Generation, rec.StopNote)
	}
}

// t27LandSelectedRunStopped drives the production landing half for the
// selected run while the old callback is gated (see file header).
func t27LandSelectedRunStopped(t *testing.T, al *AgentLoop, sessionID string, generation int) {
	t.Helper()
	al.reportSteeredSessionTerminalUpward(context.Background(), sessionID, generation,
		session.LifecycleStopped, steer.OutcomeInterrupted, "interrupted: the session was cancelled")
	t27StopLanded(t, al.GetSessionLifecycleStore(), sessionID, generation)
}

// t27ResumeAndRequeue runs the explicit same-generation RESUME and re-admits
// the replacement behind the blocker (queued case), asserting both halves.
// wantQueueLen is the expected start-queue length after the re-admission:
// the child's ORIGINAL admission entry is still queued whenever the old
// stop's removal half is still gated (removing it IS that gated effect), so
// the stale tests re-queue on top of it (2), while the ungated positive
// control re-queues after its own stop already removed the original (1).
func t27ResumeAndRequeue(t *testing.T, al *AgentLoop, canceller *SteerCanceller, sessionID string, generation, wantQueueLen int) {
	t.Helper()
	resumed, err := canceller.Revive(context.Background(), sessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "t27-owner"})
	if err != nil {
		t.Fatalf("Revive: %v", err)
	}
	if resumed != generation {
		t.Fatalf("RESUME returned generation %d, want %d (D2 CRIT-001: same-generation resume)", resumed, generation)
	}
	if _, dispatchErr := NewSteerLauncher(al).Dispatch(context.Background(), sessionID, generation); dispatchErr != nil {
		t.Fatalf("Dispatch(replacement): %v", dispatchErr)
	}
	rec, err := al.GetSessionLifecycleStore().Load(sessionID)
	if err != nil {
		t.Fatalf("Load(replacement): %v", err)
	}
	if rec.State != session.LifecycleQueued {
		t.Fatalf("setup: replacement record = %q, want queued behind the blocker", rec.State)
	}
	if got := al.steerAdmission().queueLen(); got != wantQueueLen {
		t.Fatalf("setup: start queue length after the re-admission = %d, want %d", got, wantQueueLen)
	}
}

// t27AwaitFinalState polls the record until the turn-exit completion has
// landed (the commit is asynchronous to ts.Finished), then returns it.
func t27AwaitFinalState(t *testing.T, lifecycle *session.LifecycleStore, sessionID string) *session.LifecycleRecord {
	t.Helper()
	t27WaitFor(t, 30*time.Second, "the turn-exit completion to land on the record", func() bool {
		rec, err := lifecycle.Load(sessionID)
		return err == nil && rec.State != session.LifecycleRunning && rec.State != session.LifecycleQueued
	})
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		t.Fatalf("Load(final): %v", err)
	}
	return rec
}

func TestT27_StaleStopCallback_QueuedReplacementKeepsQueueEntry(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 1
	park := installT27ParkProvider(t, al)
	lifecycle := al.GetSessionLifecycleStore()
	launcher := NewSteerLauncher(al)

	parentID := newTestSteeringSession(t, al, "ws-t27-stale-queued")
	blockerGate := park.newGate("blocker final")
	blockerID, blockerGen := launchSteeredChild(t, al, parentID, "call-t27-stale-blocker", "occupies the only slot")
	if _, err := launcher.Dispatch(context.Background(), blockerID, blockerGen); err != nil {
		t.Fatalf("Dispatch(blocker): %v", err)
	}
	select {
	case <-blockerGate.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the blocker never reached its provider")
	}

	childID, childGen := launchSteeredChild(t, al, parentID, "call-t27-stale-queued", "stopped while queued, then resumed")
	if _, err := launcher.Dispatch(context.Background(), childID, childGen); err != nil {
		t.Fatalf("Dispatch(child): %v", err)
	}

	gate := newT27GatedCancel(al.SteerGenerationCancel)
	defer gate.openGate()
	canceller := NewSteerCanceller(lifecycle)
	stopDone := make(chan error, 1)
	go func() {
		_, err := canceller.StopTurns(context.Background(), childID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "t27-owner"}, false, gate.cancelTurn)
		stopDone <- err
	}()
	select {
	case <-gate.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the old stop's live effect never reached the gate")
	}
	// The fence is durable BEFORE the gated effect (cascade order) — proven,
	// not assumed. And while gated, the replacement's own entry in the start
	// queue is untouched: the gate is demonstrably withholding the
	// session-id-keyed removal the ungated callback performs first thing.
	t27WaitFor(t, 10*time.Second, "the old stop's fence/note to become durable", func() bool {
		rec, err := lifecycle.Load(childID)
		return err == nil && rec.Stop != nil && rec.Stop.Generation == rec.Generation && rec.StopNote != nil
	})
	// While gated, the child's original admission entry is still queued —
	// removing it IS the gated live effect, so its presence here is the
	// barrier proof. (After the re-admission below the queue holds both
	// entries: the original and the replacement's.)
	if got := al.steerAdmission().queueLen(); got != 1 {
		t.Fatalf("barrier proof failed: start queue length while the old callback is gated = %d, want 1 — the gate must be withholding the live effect", got)
	}

	t27LandSelectedRunStopped(t, al, childID, childGen)
	t27ResumeAndRequeue(t, al, canceller, childID, childGen, 2)

	gate.openGate()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("old StopTurns: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the old stop never returned after the gate opened")
	}

	// The old callback may remove its OWN matching original admission; the
	// replacement's entry must survive it.
	if got := al.steerAdmission().queueLen(); got != 1 {
		t.Errorf("start queue length after the old stop's delayed effect = %d, want 1 — "+
			"the stale callback removed the replacement's queued admission along with its own: removeQueuedSession is keyed by session id, "+
			"so the old execution's effect and a genuinely newer stop of the same-generation replacement are indistinguishable "+
			"(D5/T27: the delayed effect stays bound to the execution it selected)", got)
	}
	if rec, err := lifecycle.Load(childID); err != nil {
		t.Fatalf("Load(after stale effect): %v", err)
	} else if rec.State != session.LifecycleQueued {
		t.Errorf("replacement record after the old callback = %q, want still queued — the stale effect must not land the replacement stopped (never-ran finalization included)", rec.State)
	}
	// Settle deterministically for cleanup: drop the replacement's entry so
	// the blocker's release cannot promote it mid-teardown (in either the
	// red or the future green world).
	al.steerAdmission().removeQueuedSession(childID)
}

func TestT27_StaleStopCallback_ActiveReplacementFinishesItsTurn(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 2
	park := installT27ParkProvider(t, al)
	lifecycle := al.GetSessionLifecycleStore()
	launcher := NewSteerLauncher(al)

	parentID := newTestSteeringSession(t, al, "ws-t27-stale-active")
	rec := launchRunningChild(t, al, parentID, "call-t27-stale-active")
	childID, childGen := rec.SessionID, rec.Generation

	gate := newT27GatedCancel(al.SteerGenerationCancel)
	defer gate.openGate()
	canceller := NewSteerCanceller(lifecycle)
	stopDone := make(chan error, 1)
	go func() {
		_, err := canceller.StopTurns(context.Background(), childID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "t27-owner"}, false, gate.cancelTurn)
		stopDone <- err
	}()
	select {
	case <-gate.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the old stop's live effect never reached the gate")
	}
	t27WaitFor(t, 10*time.Second, "the old stop's fence/note to become durable", func() bool {
		stamped, err := lifecycle.Load(childID)
		return err == nil && stamped.Stop != nil && stamped.Stop.Generation == stamped.Generation && stamped.StopNote != nil
	})

	t27LandSelectedRunStopped(t, al, childID, childGen)
	if resumed, err := canceller.Revive(context.Background(), childID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "t27-owner"}); err != nil || resumed != childGen {
		t.Fatalf("Revive = (%d, %v), want (%d, nil) — the replacement resumes at the SAME generation", resumed, err, childGen)
	}

	replacementGate := park.newGate("replacement-final-answer")
	dr, err := launcher.Dispatch(context.Background(), childID, childGen)
	if err != nil {
		t.Fatalf("Dispatch(replacement): %v", err)
	}
	if dr.State != steer.DispatchRunning {
		t.Fatalf("setup: Dispatch(replacement) = %+v, want running (the replacement is the ACTIVE turn)", dr)
	}
	select {
	case <-replacementGate.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the replacement never reached its provider")
	}
	ts := al.getActiveTurnState(childID)
	if ts == nil {
		t.Fatal("setup: no live turn registered for the replacement; this test would prove nothing")
	}
	// Barrier proof: with the old callback gated, the replacement's live
	// turn is untouched.
	if rec, err := lifecycle.Load(childID); err != nil {
		t.Fatalf("Load(while gated): %v", err)
	} else if rec.State != session.LifecycleRunning {
		t.Fatalf("barrier proof failed: replacement record while gated = %q, want running", rec.State)
	}

	gate.openGate()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("old StopTurns: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the old stop never returned after the gate opened")
	}

	// Let the replacement finish its own work normally, then wait out the
	// asynchronous completion commit before reading the record.
	replacementGate.letFinish()
	select {
	case <-ts.Finished():
	case <-time.After(30 * time.Second):
		t.Fatal("the replacement's turn never finished after its park was released")
	}
	final := t27AwaitFinalState(t, lifecycle, childID)
	if final.State != session.LifecycleCompleted {
		t.Errorf("replacement's record after finishing = %q (note=%v), want completed — "+
			"the old stop's delayed callback aborted the SAME-generation replacement's live turn: "+
			"requestCancelForGeneration matches generation only, so the stale effect and a genuinely newer stop "+
			"of the replacement are indistinguishable (D5/T27: the delayed effect stays bound to the execution it selected)",
			final.State, final.StopNote)
	}
	if final.StopNote != nil {
		t.Errorf("the replacement carries a stop note %+v after finishing its own work — the old stop's effect landed it stopped", final.StopNote)
	}
}

func TestT27_StaleStopCallback_DelegateToolQueueRemovalSparesReplacement(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 1
	park := installT27ParkProvider(t, al)
	lifecycle := al.GetSessionLifecycleStore()
	launcher := NewSteerLauncher(al)

	parentID := newTestSteeringSession(t, al, "ws-t27-stale-tool")
	blockerGate := park.newGate("blocker final")
	blockerID, blockerGen := launchSteeredChild(t, al, parentID, "call-t27-tool-blocker", "occupies the only slot")
	if _, err := launcher.Dispatch(context.Background(), blockerID, blockerGen); err != nil {
		t.Fatalf("Dispatch(blocker): %v", err)
	}
	select {
	case <-blockerGate.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the blocker never reached its provider")
	}

	childID, childGen := launchSteeredChild(t, al, parentID, "call-t27-tool-queued", "stopped while queued, then resumed")
	if _, err := launcher.Dispatch(context.Background(), childID, childGen); err != nil {
		t.Fatalf("Dispatch(child): %v", err)
	}

	// Wire the composition-root injection point (SetSteerCanceller — the
	// production wiring function gateway boot uses) with the gated
	// canceller, so the REAL delegate tool path — cancelDelegatedSubtree,
	// the SECOND queue-removal caller — is the old stop under test.
	gate := newT27GatedCancel(al.SteerGenerationCancel)
	defer gate.openGate()
	al.SetSteerCanceller(NewSteerCanceller(lifecycle, gate.cancelTurn))

	dt := delegateToolFor(t, al)
	toolDone := make(chan *tools.ToolResult, 1)
	go func() {
		ctx := tools.WithTranscriptSessionID(context.Background(), parentID)
		toolDone <- dt.Execute(ctx, map[string]any{"action": "stop_all", "session_id": childID, "hard": true})
	}()
	select {
	case <-gate.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the old stop's live effect never reached the gate")
	}
	t27WaitFor(t, 10*time.Second, "the old stop's fence/note to become durable", func() bool {
		rec, err := lifecycle.Load(childID)
		return err == nil && rec.Stop != nil && rec.Stop.Generation == rec.Generation && rec.StopNote != nil
	})
	if got := al.steerAdmission().queueLen(); got != 1 {
		t.Fatalf("barrier proof failed: start queue length while the old callback is gated = %d, want 1", got)
	}

	t27LandSelectedRunStopped(t, al, childID, childGen)
	t27ResumeAndRequeue(t, al, NewSteerCanceller(lifecycle), childID, childGen, 2)

	gate.openGate()
	select {
	case res := <-toolDone:
		if res.IsError {
			t.Fatalf("delegate cancel errored: %s", res.ForLLM)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the delegate cancel never returned after the gate opened")
	}

	// BOTH queue-removal callers fired here: SteerGenerationCancel's own and
	// cancelDelegatedSubtree's post-cascade loop. The old stop may remove its
	// OWN matching original admission; the replacement's entry must survive.
	if got := al.steerAdmission().queueLen(); got != 1 {
		t.Errorf("start queue length after the old delegate cancel's delayed effects = %d, want 1 — "+
			"the stale effect removed the replacement's queued admission (along with the original) through the delegate tool's "+
			"queue-removal caller(s) (D5/T27: both queue-removal callers are delayed effects bound to the execution the old stop selected)", got)
	}
	if rec, err := lifecycle.Load(childID); err != nil {
		t.Fatalf("Load(after stale effects): %v", err)
	} else if rec.State != session.LifecycleQueued {
		t.Errorf("replacement record after the old delegate cancel = %q, want still queued", rec.State)
	}
	al.steerAdmission().removeQueuedSession(childID)
}

// TestT27_NewStop_AfterResumeStillStopsReplacement is T27's positive
// control, ungated: a genuinely newer Stop — same wiring, no pause — must
// still remove the replacement's queued admission and abort its live turn.
// It doubles as the barrier proof for the stale tests: if this ever went
// green alongside them, the stale tests' green would mean "the callback
// never did anything", not "stale effects are excluded".
func TestT27_NewStop_AfterResumeStillStopsReplacement(t *testing.T) {
	t.Run("queued_replacement", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		wireSteerCompletionDeps(t, al)
		al.GetConfig().Performance.MaxParallelAgents = 1
		park := installT27ParkProvider(t, al)
		lifecycle := al.GetSessionLifecycleStore()
		launcher := NewSteerLauncher(al)

		parentID := newTestSteeringSession(t, al, "ws-t27-positive-queued")
		blockerGate := park.newGate("blocker final")
		blockerID, blockerGen := launchSteeredChild(t, al, parentID, "call-t27-pos-blocker", "occupies the only slot")
		if _, err := launcher.Dispatch(context.Background(), blockerID, blockerGen); err != nil {
			t.Fatalf("Dispatch(blocker): %v", err)
		}
		select {
		case <-blockerGate.entered:
		case <-time.After(30 * time.Second):
			t.Fatal("the blocker never reached its provider")
		}

		childID, childGen := launchSteeredChild(t, al, parentID, "call-t27-pos-queued", "stopped, resumed, stopped again")
		if _, err := launcher.Dispatch(context.Background(), childID, childGen); err != nil {
			t.Fatalf("Dispatch(child): %v", err)
		}

		owner := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "t27-owner"}
		canceller := NewSteerCanceller(lifecycle)
		stopTurn := al.SteerGenerationCancel
		if _, err := canceller.StopTurns(context.Background(), childID, owner, false, stopTurn); err != nil {
			t.Fatalf("first StopTurns: %v", err)
		}
		t27StopLanded(t, lifecycle, childID, childGen)
		if got := al.steerAdmission().queueLen(); got != 0 {
			t.Fatalf("setup: the ungated first stop must remove the child's own queued admission (start queue length = %d, want 0)", got)
		}
		t27ResumeAndRequeue(t, al, canceller, childID, childGen, 1)

		// The genuinely newer stop: ungated, same production wiring.
		if _, err := canceller.StopTurns(context.Background(), childID, owner, false, stopTurn); err != nil {
			t.Fatalf("newer StopTurns: %v", err)
		}
		if got := al.steerAdmission().queueLen(); got != 0 {
			t.Errorf("start queue length after the newer stop = %d, want 0 — a genuinely newer stop must remove the replacement's queued admission", got)
		}
		if rec, err := lifecycle.Load(childID); err != nil {
			t.Fatalf("Load(after newer stop): %v", err)
		} else if rec.State != session.LifecycleStopped || rec.StopNote == nil {
			t.Errorf("replacement record after the newer stop = state %q note %v, want stopped with a note", rec.State, rec.StopNote)
		}
		al.steerAdmission().removeQueuedSession(childID)
	})

	t.Run("active_replacement", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		wireSteerCompletionDeps(t, al)
		al.GetConfig().Performance.MaxParallelAgents = 2
		park := installT27ParkProvider(t, al)
		lifecycle := al.GetSessionLifecycleStore()
		launcher := NewSteerLauncher(al)

		parentID := newTestSteeringSession(t, al, "ws-t27-positive-active")
		rec := launchRunningChild(t, al, parentID, "call-t27-pos-active")
		childID, childGen := rec.SessionID, rec.Generation

		owner := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "t27-owner"}
		canceller := NewSteerCanceller(lifecycle)
		stopTurn := al.SteerGenerationCancel
		if _, err := canceller.StopTurns(context.Background(), childID, owner, false, stopTurn); err != nil {
			t.Fatalf("first StopTurns: %v", err)
		}
		t27StopLanded(t, lifecycle, childID, childGen)
		if resumed, err := canceller.Revive(context.Background(), childID, owner); err != nil || resumed != childGen {
			t.Fatalf("Revive = (%d, %v), want (%d, nil)", resumed, err, childGen)
		}

		replacementGate := park.newGate("replacement final")
		dr, err := launcher.Dispatch(context.Background(), childID, childGen)
		if err != nil {
			t.Fatalf("Dispatch(replacement): %v", err)
		}
		if dr.State != steer.DispatchRunning {
			t.Fatalf("setup: Dispatch(replacement) = %+v, want running", dr)
		}
		select {
		case <-replacementGate.entered:
		case <-time.After(30 * time.Second):
			t.Fatal("the replacement never reached its provider")
		}
		ts := al.getActiveTurnState(childID)
		if ts == nil {
			t.Fatal("setup: no live turn registered for the replacement")
		}

		// The genuinely newer stop aborts the replacement's live turn.
		if _, err := canceller.StopTurns(context.Background(), childID, owner, false, stopTurn); err != nil {
			t.Fatalf("newer StopTurns: %v", err)
		}
		select {
		case <-ts.Finished():
		case <-time.After(30 * time.Second):
			t.Fatal("the replacement's turn was not aborted by the genuinely newer stop")
		}
		final := t27AwaitFinalState(t, lifecycle, childID)
		if final.State != session.LifecycleStopped || final.StopNote == nil {
			t.Errorf("replacement record after the newer stop = state %q note %v, want stopped with a note", final.State, final.StopNote)
		}
	})
}
