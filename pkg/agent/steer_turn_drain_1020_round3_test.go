// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Round-three coverage for issue #1020, from the team-lead-approved
// "finishing" hand-off spec (founder ruling Q10: "the subagent decide what to
// do"):
//
//  1. The terminal hand-off starts atomically under the steering-queue lock:
//     it checks the queue is empty and marks the child "finishing".
//  2. Any steer or wake arriving after that point is ACCEPTED, never
//     refused — held as a "post-finish steer".
//  3. The child's current final is delivered as `<child>:<gen>:final`,
//     never replaced or deduplicated away.
//  4. Right after the terminal commit, a waiting post-finish steer revives
//     the child as a new generation carrying the steer; its next final is
//     `<child>:<gen+1>:final`.
//  5. The parent-facing text of that second final names the late steer.
//  6. If terminal delivery FAILS, the child is not terminal; waiting steers
//     become an ordinary continuation right away, in the SAME generation.
//
// None of items 2, 4, 5 or 6's continuation half exist in production as of
// bccd5be9a — steeringQueue.pushItemScopeChecked (steering.go) still refuses
// once steeringTerminalTransition.committing is true, and neither
// completeSteeredTurn (steer_completion.go) nor disposeSteeredTurnResult
// (steer_launcher.go) ever revives a generation or retries a continuation
// after anything but errCompleteSteeringPending. Every RED test below names
// the missing production symbol/call site in its failure message.
package agent

import (
	"context"
	"fmt"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// round3FailingDeliverer is S2's fault injector: onDeliver runs (as a side
// effect, synchronously, inside the real terminal-transition's prepare()
// window) BEFORE Deliver reports a hard failure — modeling "terminal
// delivery FAILS" (spec item 6) with a late steer landing during the same
// attempt.
type round3FailingDeliverer struct {
	onDeliver func() error
}

func (d *round3FailingDeliverer) Deliver(_ context.Context, _ steer.UpwardEvent) (steer.Delivery, error) {
	if d.onDeliver != nil {
		if err := d.onDeliver(); err != nil {
			return steer.Delivery{}, err
		}
	}
	return steer.Delivery{}, fmt.Errorf("round3 s2: simulated terminal delivery failure")
}

// TestSteeredTurnDrain1020Round3_LateSteerDuringFinishAcceptedThenRevivesNextGenerationFinal
// covers S1: a steer arrives during terminal delivery, after the empty
// check. It must be ACCEPTED (item 2), the generation-g final must still
// carry the pre-steer answer unchanged (item 3), and the held post-finish
// steer must revive the child to generation g+1 (item 4).
func TestSteeredTurnDrain1020Round3_LateSteerDuringFinishAcceptedThenRevivesNextGenerationFinal(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, rootID, "round3-s1-revive")
	startGeneration := child.Generation

	var hookErr error
	var hookCalls int
	completeStateWriteTestHook = func(sessionID string) {
		hookCalls++
		_, hookErr = al.EnqueueSteeringMessage(
			sessionID, testDefaultAgentID,
			providers.Message{Role: "user", Content: "ROUND3-S1-LATE-STEER"}, "round3-s1-late-steer")
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	if err := al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "pre-steer answer"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn: %v", err)
	}
	if hookCalls != 1 {
		t.Fatalf("completeStateWriteTestHook calls = %d, want exactly 1", hookCalls)
	}
	if hookErr != nil {
		t.Fatalf("BLOCKED: round-3 spec item 2 (a steer arriving after the finishing mark begins must be ACCEPTED, held as a post-finish steer, never refused) is not implemented — steeringQueue.pushItemScopeChecked (pkg/agent/steering.go) still refuses once steeringTerminalTransition.committing is true, returning errSteeringScopeClosed (%v) instead of holding the item; this also blocks verifying item 4's revival to generation %d", hookErr, startGeneration+1)
	}

	// Item 3: the pre-steer answer's own final, for generation
	// startGeneration, must still be delivered unchanged at its
	// deterministic id — the spec's own literal notation, "<child>:<gen>:final".
	msgs, _, _, drainErr := al.GetMessageInboxStore().Drain(rootID, child.SessionID, "", 10)
	if drainErr != nil {
		t.Fatalf("Drain(root, generation %d final): %v", startGeneration, drainErr)
	}
	wantGenFinalID := fmt.Sprintf("%s:%d:final", child.SessionID, startGeneration)
	var genFinal *generated.SessionMessage
	for i := range msgs {
		if messageIDOf(msgs[i]) == wantGenFinalID {
			genFinal = &msgs[i]
		}
	}
	if genFinal == nil {
		t.Fatalf("generation %d final (id %q) was not delivered unchanged; delivered message count = %d", startGeneration, wantGenFinalID, len(msgs))
	}
	handback, herr := genFinal.AsSessionMessageHandback()
	if herr != nil || handback.ResultSoFar != "pre-steer answer" {
		t.Fatalf("generation %d final content = %q (decode err %v), want unchanged %q", startGeneration, handback.ResultSoFar, herr, "pre-steer answer")
	}

	// Item 4: a held post-finish steer must revive the child to generation
	// startGeneration+1 immediately after the terminal commit landed above.
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.Generation != startGeneration+1 || rec.Terminal() {
		t.Fatalf("BLOCKED: round-3 spec item 4 (a held post-finish steer immediately revives the child to a new generation right after the terminal commit) is not implemented — no call site in completeSteeredTurn (pkg/agent/steer_completion.go) or steeringQueue.runTerminalTransition (pkg/agent/steering.go) ever invokes SteerCanceller.Revive or dispatchSteeredSession after a successful commit; child record generation=%d terminal=%v, want generation=%d running", rec.Generation, rec.Terminal(), startGeneration+1)
	}
}

// TestSteeredTurnDrain1020Round3_DeliveryFailureConsumesLateSteerAsSameGenerationContinuation
// covers S2: terminal delivery fails. The child must stay non-terminal, and
// the late steer accepted during that failed attempt must be consumed by an
// ordinary same-generation continuation right away (item 6) — never
// revived, never stranded.
func TestSteeredTurnDrain1020Round3_DeliveryFailureConsumesLateSteerAsSameGenerationContinuation(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default test agent is not registered")
	}
	agent.Provider = provider

	var child steer.LaunchResult
	var enqueueErr error
	deliverer := &round3FailingDeliverer{onDeliver: func() error {
		_, enqueueErr = al.EnqueueSteeringMessage(
			child.SessionID, testDefaultAgentID,
			providers.Message{Role: "user", Content: "ROUND3-S2-LATE-STEER"}, "round3-s2-late-steer")
		return nil
	}}
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(classifier), nil, deliverer)

	child = launchQueuedSteeredTurnDrainChild1020(t, al, testDefaultAgentID, "round3 s2 delivery failure")
	snapshot := setLifecycleState1020(t, al, child.SessionID, session.LifecycleRunning)
	ts, err := al.reconstructSteeredTurn(snapshot, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}

	al.disposeSteeredTurnResult(ts, snapshot, snapshot.Generation, turnResult{finalContent: "first answer"}, nil)

	if enqueueErr != nil {
		t.Fatalf("late steer during a failing terminal delivery was refused (%v); round-3 spec item 2 requires acceptance even when the delivery itself later fails", enqueueErr)
	}
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.Terminal() {
		t.Fatalf("child became terminal despite a failed terminal delivery; state=%q, want non-terminal per round-3 spec item 6", rec.State)
	}
	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("BLOCKED: round-3 spec item 6 (on terminal-delivery failure the lock is released and a waiting post-finish steer becomes an ordinary same-generation continuation right away) is not implemented — steer_launcher.go::disposeSteeredTurnResult only retries its loop on errCompleteSteeringPending, and completeSteeredTurn (pkg/agent/steer_completion.go) returns the raw delivery error on this path instead, so the accepted late steer is never drained; provider request count = %d, want exactly 1 (the immediate continuation)", len(requests))
	}
	if got := countMessagesContaining(requests[0], "ROUND3-S2-LATE-STEER"); got != 1 {
		t.Errorf("late steer occurrences in the continuation request = %d, want exactly 1", got)
	}
	if got := al.pendingSteeringCountForScope(child.SessionID); got != 0 {
		t.Errorf("pending steering count after continuation = %d, want 0 (nothing stays queued)", got)
	}
}

// TestSteeredTurnDrain1020Round3_DescendantWakeDuringOwnTerminalDeliveryNeverStranded
// covers S3: an upward wake (not a plain steer) arrives during the child's
// own terminal delivery, in the same committing window S1 exercises. It
// must never be refused, and it must never be accepted-yet-stranded
// (accepted with no error while the record ends up with no consumer).
func TestSteeredTurnDrain1020Round3_DescendantWakeDuringOwnTerminalDeliveryNeverStranded(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, rootID, "round3-s3-wake")

	var hookErr error
	var hookCalls int
	completeStateWriteTestHook = func(sessionID string) {
		hookCalls++
		hookErr = al.EnqueueSteeringWake(
			sessionID, testDefaultAgentID, sessionID,
			"round3-s3-descendant-wake",
			providers.Message{Role: "user", Content: "ROUND3-S3-DESCENDANT-WAKE"},
		)
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	if err := al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "pre-wake answer"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn: %v", err)
	}
	if hookCalls != 1 {
		t.Fatalf("completeStateWriteTestHook calls = %d, want exactly 1", hookCalls)
	}
	if hookErr != nil {
		t.Errorf("BLOCKED: round-3 spec item 2/S3 (a descendant's wake arriving during the child's own terminal delivery must be ACCEPTED, never refused) is not implemented — steeringQueue.pushItemScopeChecked (pkg/agent/steering.go) refused this wake (%v) because steeringTerminalTransition.committing was already true", hookErr)
	}
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if hookErr == nil && rec.Terminal() {
		if got := al.pendingSteeringCountForScope(child.SessionID); got != 0 {
			t.Errorf("descendant wake accepted (no error) but %d item(s) remain queued on a now-terminal child with no consumer — round-3 spec S3 requires it never be lost, but nothing revives or drains a post-finish wake today", got)
		}
	}
}

// TestSteeredTurnDrain1020Round3_StopRaceDuringWakeDeliveryNeverStrandsAcceptedWake
// covers S4: the exact pta round-2 finding. A Stop terminalises the child
// through reportSteeredSessionTerminalUpward — which does NOT take the
// steering-queue mutex — while an unrelated wake delivery is mid-flight,
// inside the same non-committing "finishing" window S1/S3 use. The wake
// must never be reported accepted with no live or durable consumer.
func TestSteeredTurnDrain1020Round3_StopRaceDuringWakeDeliveryNeverStrandsAcceptedWake(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, rootID, "round3-s4-stop-race")

	var enqueueErr error
	// Deterministic, single-goroutine reproduction: runTerminalTransition
	// installs the non-committing "finishing" mark before calling prepare,
	// and only clears it after prepare/transition return — so anything run
	// synchronously inside prepare() genuinely observes that open window.
	_, _, _ = al.steering.runTerminalTransition(child.SessionID,
		func() error {
			// Simulates a concurrent Stop cascade landing via the
			// terminal-report path (steer_cancel.go), which writes the
			// terminal state directly and never touches al.steering at
			// all — the exact bypass the pta finding names.
			al.reportSteeredSessionTerminalUpward(context.Background(), child.SessionID, child.Generation,
				session.LifecycleCancelled, steer.OutcomeInterrupted, "interrupted: the session was cancelled")
			// Simulates the paused wake delivery resuming and enqueuing
			// into the still-open (non-committing) transition.
			enqueueErr = al.EnqueueSteeringWake(
				child.SessionID, testDefaultAgentID, child.SessionID,
				"round3-s4-wake", providers.Message{Role: "user", Content: "ROUND3-S4-WAKE"})
			return nil
		},
		// Unreached in this exact interleaving: prepare's own enqueue
		// leaves the queue non-empty, so runTerminalTransition's own
		// recheck aborts before ever calling transition.
		func() (bool, error) { return false, nil })

	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if !rec.Terminal() {
		t.Fatalf("SETUP: child did not become terminal via the simulated Stop cascade; state=%q", rec.State)
	}
	if enqueueErr != nil {
		// A refusal is the safe outcome here — nothing further to check.
		return
	}
	if got := al.pendingSteeringCountForScope(child.SessionID); got != 0 {
		t.Errorf("BLOCKED: round-3 spec item 2/S4 (a wake accepted while racing a Stop's terminal write must be delivered to a live consumer or durably stored for boot recovery — never a bare success report with no consumer) is not implemented — the wake was reported accepted (no error) on a now-terminal child, and %d item(s) remain queued in the in-memory steeringQueue, which is not persisted and cannot be recovered by a restart", got)
	}
}

// TestSteeredTurnDrain1020Round3_NoLateSteerControlExactlyOneFinalNoRevival
// is S5's positive control: with no late steer at all, completion must
// still behave exactly as before this round's spec — exactly one final,
// no revival, the generation left unchanged.
func TestSteeredTurnDrain1020Round3_NoLateSteerControlExactlyOneFinalNoRevival(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, rootID, "round3-s5-control")
	startGeneration := child.Generation

	if err := al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "only answer"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn: %v", err)
	}

	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if !rec.Terminal() {
		t.Fatalf("child state = %q, want terminal with no late steer to hold it open", rec.State)
	}
	if rec.Generation != startGeneration {
		t.Errorf("child generation = %d, want unchanged %d: no late steer means no revival", rec.Generation, startGeneration)
	}

	msgs, _, _, drainErr := al.GetMessageInboxStore().Drain(rootID, child.SessionID, "", 10)
	if drainErr != nil {
		t.Fatalf("Drain(root): %v", drainErr)
	}
	if len(msgs) != 1 {
		t.Fatalf("delivered message count = %d, want exactly 1 final and no revival", len(msgs))
	}
	handback, herr := msgs[0].AsSessionMessageHandback()
	if herr != nil || handback.ResultSoFar != "only answer" {
		t.Errorf("final content = %q (decode err %v), want %q", handback.ResultSoFar, herr, "only answer")
	}
}
