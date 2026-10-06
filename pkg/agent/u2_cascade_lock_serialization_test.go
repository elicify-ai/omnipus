// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Frozen ADR-20260928 D2/D7/T27, preserved by ADR-20261004 C6:
// serialize real traversal/stamping, NOT callbacks. Live effects are lock-free
// and remain bound to their selected immutable execution, even on same-generation
// resume. The obsolete callback-exclusion oracle is replaced, not retained.
package agent

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type u2StopResult struct {
	report steer.CancelReport
	err    error
}

func u2Owner() steer.Principal {
	return steer.Principal{Kind: steer.PrincipalKindHuman, ID: "cascade-owner"}
}

func u2AwaitStop(t *testing.T, done <-chan u2StopResult) steer.CancelReport {
	t.Helper()
	select {
	case result := <-done:
		if result.err != nil || len(result.report.Unreachable) != 0 {
			t.Fatalf("real Stop result=%+v err=%v, want no unreachable effects", result.report, result.err)
		}
		return result.report
	case <-time.After(5 * time.Second):
		t.Fatal("real Stop did not return after its effect barrier was released")
	}
	return steer.CancelReport{}
}
func u2AssertReached(t *testing.T, report steer.CancelReport, ids ...string) {
	t.Helper()
	if !reflect.DeepEqual(report.Reached, ids) {
		t.Errorf("real Stop reached=%v, want exactly %v", report.Reached, ids)
	}
}
func u2LaunchLive(t *testing.T, al *AgentLoop, parent, call string, gate *goalRunGate) (*session.LifecycleRecord, *turnState) {
	t.Helper()
	id, gen := launchParkedChild(t, al, parent, call, "real cascade selected execution")
	dispatchChild(t, al, id, gen, true)
	awaitGoalProvider(t, gate)
	rec, err := al.GetSessionLifecycleStore().Load(id)
	ts := al.getActiveTurnState(id)
	if err != nil || ts == nil || rec.ExecutionID == nil || al.executionClaimFor(rec) != al.tsExecutionClaim(ts, id) {
		t.Fatalf("SETUP: real selected execution missing/mismatching: record=%+v handle=%v err=%v", rec, ts, err)
	}
	return rec, ts
}

// Distinct named goroutines let the instrument observe which REAL lock blocks
// each call. A lifecycle-lock barrier pauses A in durable stamping; B must wait
// in cascade itself, not sneak into lifecycle traversal/stamping alongside A.
func u2TraversalA(c *SteerCanceller, id string, effect GenerationCancelFunc, done chan<- u2StopResult) {
	report, err := c.StopTurns(context.Background(), id, u2Owner(), true, effect)
	done <- u2StopResult{report: report, err: err}
}
func u2TraversalB(c *SteerCanceller, id string, effect GenerationCancelFunc, done chan<- u2StopResult) {
	report, err := c.StopTurns(context.Background(), id, u2Owner(), true, effect)
	done <- u2StopResult{report: report, err: err}
}
func u2Stacks() string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return string(buf[:n])
}

func TestCascade_TraversalAndStampingAreSerialized(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	rootGate, childGate := newGoalRunGate("root unused final", nil), newGoalRunGate("child unused final", nil)
	installGoalRunProvider(t, al, rootGate, childGate)
	parent := newTestSteeringSession(t, al, "ws-cascade-serialization")
	root, _ := u2LaunchLive(t, al, parent, "call-serialized-root", rootGate)
	child, _ := u2LaunchLive(t, al, root.SessionID, "call-serialized-child", childGate)
	c := al.steerCanceller()
	expected := map[string]*session.LifecycleRecord{root.SessionID: root, child.SessionID: child}
	var effectCalls atomic.Int32
	effectEntered := make(chan struct{}, 4)
	effectRelease := make(chan struct{})
	var releaseEffects sync.Once
	openEffects := func() { releaseEffects.Do(func() { close(effectRelease) }) }
	defer openEffects()
	effect := func(ctx context.Context, id string, gen int) (GenerationCancelResult, error) {
		// The real tree must be fully stamped BEFORE the first live effect (D7).
		for node, old := range expected {
			rec, err := c.Lifecycle.Load(node)
			if err != nil {
				return GenerationCancelResult{}, err
			}
			if rec.StopNote == nil || rec.StopEffect == nil || rec.StopEffect.ControlID == "" || rec.ExecutionID == nil || rec.StopEffect.Target.RunID != old.ExecutionID.RunID || rec.StopEffect.Target.BootSeq != old.ExecutionID.BootSeq || rec.StopEffect.Target.Generation != old.Generation {
				return GenerationCancelResult{}, fmt.Errorf("before effect %s:%d: node %s lacks its actual selected durable stamp: %+v", id, gen, node, rec)
			}
			accepted, err := c.Lifecycle.AcceptedStopEffects(node)
			if err != nil {
				return GenerationCancelResult{}, err
			}
			found := false
			for _, selected := range accepted {
				if reflect.DeepEqual(selected, *rec.StopEffect) {
					found = true
				}
			}
			if !found {
				return GenerationCancelResult{}, fmt.Errorf("node %s selected stamp has no durable accepted ledger pair", node)
			}
		}
		effectEntered <- struct{}{}
		<-effectRelease
		effectCalls.Add(1)
		return al.SteerGenerationCancel(ctx, id, gen)
	}
	lock := c.Lifecycle.Lock(root.SessionID)
	lock.Lock()
	var release sync.Once
	unlock := func() { release.Do(lock.Unlock) }
	defer unlock()
	a, b := make(chan u2StopResult, 1), make(chan u2StopResult, 1)
	go u2TraversalA(c, root.SessionID, effect, a)
	waitForGate(t, "A to block at the real lifecycle traversal/stamp boundary", func() bool {
		for _, stack := range strings.Split(u2Stacks(), "\n\n") {
			if strings.Contains(stack, "u2TraversalA(") && (strings.Contains(stack, "(*LifecycleStore).Load(") || strings.Contains(stack, "(*LifecycleStore).AcceptStopControl(")) {
				return true
			}
		}
		return false
	})
	if c.cascadeLock(root.SessionID).TryLock() {
		c.cascadeLock(root.SessionID).Unlock()
		t.Fatal("A reached durable traversal/stamping without owning the shared cascade serialization lock (D7)")
	}
	go u2TraversalB(c, root.SessionID, effect, b)
	var bStack string
	waitForGate(t, "B's real lock wait to be observable", func() bool {
		for _, stack := range strings.Split(u2Stacks(), "\n\n") {
			if strings.Contains(stack, "u2TraversalB(") && strings.Contains(stack, "(*SteerCanceller).cascade(") && strings.Contains(stack, "[sync.Mutex.Lock]") {
				bStack = stack
				return true
			}
		}
		return false
	})
	if strings.Contains(bStack, "(*LifecycleStore).") {
		t.Error("B entered lifecycle traversal/stamping while A still owned that section; actual durable sections were not serialized")
	}
	if got := effectCalls.Load(); got != 0 {
		t.Errorf("live effects=%d while real stamping is blocked, want zero", got)
	}
	unlock()
	for i := 0; i < 2; i++ {
		select {
		case <-effectEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("second real traversal did not reach its effect while the first effect was gated — cascade lock must not span live effects")
		}
	}
	openEffects()
	u2AssertReached(t, u2AwaitStop(t, a), root.SessionID, child.SessionID)
	u2AssertReached(t, u2AwaitStop(t, b), root.SessionID, child.SessionID)
	joinGoalFixtureRuns(t, al)
	if got := effectCalls.Load(); got < 2 {
		t.Fatalf("only %d real effects executed, want both selected tree nodes reached", got)
	}
	for id, old := range expected {
		rec, err := c.Lifecycle.Load(id)
		if err != nil || rec.State != session.LifecycleStopped || rec.Stop != nil || rec.Generation != old.Generation {
			t.Errorf("tree node %s after real effects: %+v err=%v, want same-generation landed stopped", id, rec, err)
		}
	}
}

// Hold the actual provider-cancel edge INSIDE the real hard-abort operation.
// The wrapper invokes the existing process-edge cancel; it never returns a
// fabricated GenerationCancelResult or changes an execution identity.
func TestCascade_LiveEffectReleasesLocksAndPinsSelectedHandle(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 2
	oldGate, otherGate := newGoalRunGate("old unused final", nil), newGoalRunGate("other final", nil)
	installGoalRunProvider(t, al, oldGate, otherGate)
	parent := newTestSteeringSession(t, al, "ws-lock-free-effect")
	old, ts := u2LaunchLive(t, al, parent, "call-lock-free-old", oldGate)
	c := al.steerCanceller()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	t.Cleanup(open)
	ts.mu.RLock()
	realProviderCancel := ts.providerCancel
	ts.mu.RUnlock()
	if realProviderCancel == nil {
		t.Fatal("SETUP: actual live provider cancel edge missing")
	}
	var cancels atomic.Int32
	ts.setProviderCancel(func() {
		if cancels.Add(1) == 1 {
			close(entered)
		}
		<-release
		realProviderCancel()
	})
	done := make(chan u2StopResult, 1)
	go func() {
		report, err := c.StopTurns(context.Background(), old.SessionID, u2Owner(), false, al.SteerGenerationCancel)
		done <- u2StopResult{report: report, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real selected Stop did not reach the provider-cancel edge")
	}
	if !c.cascadeLock(old.SessionID).TryLock() {
		t.Error("cascade lock spans actual provider cancellation (D2/D7/T27)")
	} else {
		c.cascadeLock(old.SessionID).Unlock()
	}
	if !c.Lifecycle.Lock(old.SessionID).TryLock() {
		t.Error("lifecycle lock spans actual provider cancellation (D2/T27)")
	} else {
		c.Lifecycle.Lock(old.SessionID).Unlock()
	}
	// A separate real admission reaches its provider while cancellation is held:
	// registry/admission synchronization cannot be held across the live effect.
	otherDone := make(chan struct{})
	var otherID string
	go func() {
		res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent, TargetAgentID: testDefaultAgentID, Task: "independent registry progress", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-lock-free-other"}})
		if err != nil {
			t.Errorf("independent Launch: %v", err)
			close(otherDone)
			return
		}
		otherID = res.SessionID
		if dr, err := NewSteerLauncher(al).Dispatch(context.Background(), res.SessionID, res.Generation); err != nil || dr.State != steer.DispatchRunning {
			t.Errorf("independent real Dispatch=%+v err=%v", dr, err)
		}
		close(otherDone)
	}()
	select {
	case <-otherDone:
	case <-time.After(5 * time.Second):
		t.Fatal("registry/admission progress blocked across actual provider cancel")
	}
	awaitGoalProvider(t, otherGate)
	otherTS := al.getActiveTurnState(otherID)
	if otherTS == nil || otherTS == ts || otherTS.hardAbortRequested() {
		t.Fatal("unrelated actual admission was absent or retargeted by the selected Stop")
	}
	// A repeated control can finish without waiting on the first live callback.
	repeated := make(chan u2StopResult, 1)
	go func() {
		report, err := c.StopTurns(context.Background(), old.SessionID, u2Owner(), false, al.SteerGenerationCancel)
		repeated <- u2StopResult{report: report, err: err}
	}()
	u2AssertReached(t, u2AwaitStop(t, repeated), old.SessionID)
	if cancels.Load() != 1 {
		t.Errorf("provider cancel calls=%d, want exactly one selected old handle", cancels.Load())
	}
	open()
	u2AssertReached(t, u2AwaitStop(t, done), old.SessionID)
	otherGate.open()
	joinGoalFixtureRuns(t, al)
	landed, err := c.Lifecycle.Load(old.SessionID)
	if err != nil || landed.State != session.LifecycleStopped || landed.Stop != nil || landed.ExecutionID == nil || *landed.ExecutionID != *old.ExecutionID {
		t.Fatalf("selected old execution did not land its own stopped state: %+v err=%v", landed, err)
	}
	other, err := c.Lifecycle.Load(otherID)
	if err != nil || other.State != session.LifecycleCompleted || other.StopNote != nil {
		t.Fatalf("unrelated real execution affected by Stop: %+v err=%v", other, err)
	}
}

type u2DelayedEffect struct {
	real    GenerationCancelFunc
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	retry   bool
}

func newU2DelayedEffect(actualCancel GenerationCancelFunc, retry bool) *u2DelayedEffect {
	return &u2DelayedEffect{real: actualCancel, entered: make(chan struct{}), release: make(chan struct{}), retry: retry}
}
func (g *u2DelayedEffect) open() { g.once.Do(func() { close(g.release) }) }
func (g *u2DelayedEffect) call(ctx context.Context, id string, gen int) (GenerationCancelResult, error) {
	if g.retry {
		if _, err := g.real(ctx, id, gen); err != nil {
			return GenerationCancelResult{}, err
		}
	}
	close(g.entered)
	<-g.release
	return g.real(ctx, id, gen)
}
func u2StartDelayedStop(t *testing.T, c *SteerCanceller, id string, gate *u2DelayedEffect) <-chan u2StopResult {
	t.Helper()
	t.Cleanup(gate.open)
	done := make(chan u2StopResult, 1)
	go func() {
		report, err := c.StopTurns(context.Background(), id, u2Owner(), true, gate.call)
		done <- u2StopResult{report: report, err: err}
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("old Stop did not reach its actual selected effect barrier")
	}
	return done
}

// T27's old-effect/new-RESUME sequence, with a newer-Stop positive control in
// EVERY case. The pending-newer-stop variants force the old callback to retain
// its own selection rather than borrow the current fence's replacement target.
func TestCascade_StaleEffectsSpareSameGenerationResumeAndNewerStopWorks(t *testing.T) {
	for _, queued := range []bool{false, true} {
		for _, newerPending := range []bool{false, true} {
			name := fmt.Sprintf("queued_%v_newer_stop_pending_%v", queued, newerPending)
			t.Run(name, func(t *testing.T) { u2StaleEffectCase(t, queued, newerPending) })
		}
	}
}

func u2StaleEffectCase(t *testing.T, queued, newerPending bool) {
	t.Helper()
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 1
	parent := newTestSteeringSession(t, al, "ws-selected-stale-effect")
	c := al.steerCanceller()
	oldGate, freshGate := newGoalRunGate("old answer must not publish", nil), newGoalRunGate("fresh answer unused", nil)
	var old *session.LifecycleRecord
	var oldTS *turnState
	var blocker *goalRunGate
	if queued {
		blocker = newGoalRunGate("blocker final", nil)
		installGoalRunProvider(t, al, blocker, freshGate)
		u2LaunchLive(t, al, parent, "call-stale-slot-blocker", blocker)
		id, gen := launchParkedChild(t, al, parent, "call-stale-queued-old", "selected old queued work")
		dispatchChild(t, al, id, gen, false)
		var err error
		old, err = c.Lifecycle.Load(id)
		if err != nil || old.ExecutionID == nil || old.State != session.LifecycleQueued {
			t.Fatalf("SETUP: real old queued admission missing: %+v err=%v", old, err)
		}
	} else {
		installGoalRunProvider(t, al, oldGate, freshGate)
		old, oldTS = u2LaunchLive(t, al, parent, "call-stale-active-old", oldGate)
	}
	// Queued variant pauses an actual callback retry AFTER the original selected
	// admission was removed/landed by the first real call. Active variant pauses
	// BEFORE the effect and lets the immutable selected turn's own abort/exit land.
	delayed := newU2DelayedEffect(al.SteerGenerationCancel, queued)
	oldDone := u2StartDelayedStop(t, c, old.SessionID, delayed)
	if !queued {
		if !oldTS.requestHardAbort() {
			t.Fatal("SETUP: selected old immutable handle was not actually aborted")
		}
		joinGoalFixtureRuns(t, al)
	}
	stopped, err := c.Lifecycle.Load(old.SessionID)
	if err != nil || stopped.State != session.LifecycleStopped || stopped.Stop != nil || stopped.StopNote == nil || stopped.Generation != old.Generation {
		t.Fatalf("selected old run failed to land stopped: %+v err=%v", stopped, err)
	}
	if stopped.StopEffect == nil || stopped.StopEffect.Target.RunID != old.ExecutionID.RunID || stopped.StopEffect.Target.BootSeq != old.ExecutionID.BootSeq {
		t.Fatal("SETUP: old Stop was not durably bound to its actual admitted run")
	}
	gen, err := c.Revive(context.Background(), old.SessionID, u2Owner())
	if err != nil || gen != old.Generation {
		t.Fatalf("real same-generation resume=%d err=%v, want %d", gen, err, old.Generation)
	}
	dispatchChild(t, al, old.SessionID, gen, !queued)
	var freshTS *turnState
	if !queued {
		awaitGoalProvider(t, freshGate)
		freshTS = al.getActiveTurnState(old.SessionID)
	}
	fresh, err := c.Lifecycle.Load(old.SessionID)
	if err != nil || fresh.ExecutionID == nil || fresh.ExecutionID.RunID == old.ExecutionID.RunID || fresh.ExecutionID.BootSeq != old.ExecutionID.BootSeq || fresh.Generation != old.Generation || fresh.StopNote != nil || fresh.StopEffect != nil || fresh.Stop != nil {
		t.Fatalf("fresh real admission is not distinct/clear in same generation and boot: %+v err=%v", fresh, err)
	}
	if queued && al.steerAdmission().queueLen() != 1 {
		t.Fatal("SETUP: replacement did not retain exactly one actual queued admission")
	}
	if !queued && (freshTS == nil || freshTS == oldTS || !freshTS.IsAlive() || freshTS.hardAbortRequested()) {
		t.Fatal("SETUP: replacement is not a fresh live immutable execution")
	}
	var newer *u2DelayedEffect
	var newerDone <-chan u2StopResult
	if newerPending {
		newer = newU2DelayedEffect(al.SteerGenerationCancel, false)
		newerDone = u2StartDelayedStop(t, c, old.SessionID, newer)
		fresh, err = c.Lifecycle.Load(old.SessionID)
		if err != nil || fresh.StopEffect == nil || fresh.StopEffect.Target.RunID != fresh.ExecutionID.RunID || fresh.StopEffect.ControlID == stopped.StopEffect.ControlID {
			t.Fatalf("SETUP: genuinely newer Stop did not select replacement: %+v err=%v", fresh, err)
		}
	}
	delayed.open()
	u2AwaitStop(t, oldDone)
	after, err := c.Lifecycle.Load(old.SessionID)
	if err != nil {
		t.Fatalf("Load(after old delayed effect): %v", err)
	}
	if after.State != fresh.State || after.Generation != fresh.Generation || !reflect.DeepEqual(after.ExecutionID, fresh.ExecutionID) || !reflect.DeepEqual(after.Stop, fresh.Stop) || !reflect.DeepEqual(after.StopNote, fresh.StopNote) || !reflect.DeepEqual(after.StopEffect, fresh.StopEffect) {
		t.Errorf("stale selected effect touched replacement state/note/fence/identity: before=%+v after=%+v (D2/D7/T27)", fresh, after)
	}
	if queued {
		if got := al.steerAdmission().queueLen(); got != 1 {
			t.Errorf("stale selected effect removed replacement's actual admission: queue=%d, want 1 (T27)", got)
		}
	} else if freshTS.hardAbortRequested() || !freshTS.IsAlive() {
		t.Errorf("stale selected effect aborted the fresh SAME-generation handle/provider; newer-pending=%v (T27)", newerPending)
	}
	// Positive control always drives the real newer selection/effect to stopped.
	if newerPending {
		newer.open()
		u2AssertReached(t, u2AwaitStop(t, newerDone), old.SessionID)
	} else {
		report, stopErr := c.StopTurns(context.Background(), old.SessionID, u2Owner(), false, al.SteerGenerationCancel)
		if stopErr != nil || len(report.Unreachable) != 0 {
			t.Fatalf("positive newer Stop report=%+v err=%v", report, stopErr)
		}
		u2AssertReached(t, report, old.SessionID)
	}
	if queued {
		if got := al.steerAdmission().queueLen(); got != 0 {
			t.Errorf("positive newer Stop did not remove replacement: queue=%d, want 0", got)
		}
		blocker.open()
	}
	joinGoalFixtureRuns(t, al)
	final, err := c.Lifecycle.Load(old.SessionID)
	if err != nil || final.State != session.LifecycleStopped || final.StopNote == nil || final.Stop != nil || final.Generation != old.Generation || final.ExecutionID == nil || *final.ExecutionID != *fresh.ExecutionID || final.StopEffect == nil || final.StopEffect.Target.RunID != fresh.ExecutionID.RunID {
		t.Fatalf("positive newer Stop did not land ONLY replacement's stopped state: %+v err=%v", final, err)
	}
	if !queued && !freshTS.hardAbortRequested() {
		t.Error("positive newer Stop never aborted the replacement's real provider handle")
	}
}
