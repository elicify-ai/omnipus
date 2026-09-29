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
//
// Round-4 fixture correction (qa-lead, squad-lead ruling, on 098be2a05):
// S1 and S2's fixtures below used to inject a FRESH late steer on EVERY
// terminal write / delivery attempt — that only ever passed because
// production carried its own matching test-only clear
// (completeStateWriteTestHook = nil after firing), which 098be2a05 removed
// as a production-write-to-a-test-hook integrity violation. With the hook
// now firing unconditionally on every terminal write, S1's revived
// generation saw its OWN commit as "a new late steer arrived" and revived
// AGAIN, forever (an unbounded chain of detached goroutines that never let
// al.Close's drain settle), and S2's always-failing deliverer kept seeing a
// "fresh" late steer on every outer retry, running one continuation per
// attempt instead of the spec's one. Both fixtures below now inject their
// one late steer on the FIRST call only, and merely count every later one —
// the chain this produces is exactly what the round-3 spec (items 4 and 6)
// describes: one revival, then it terminates.
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

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

	// Round-4 fixture correction (see file header): inject the one late
	// steer on the FIRST hook call only — the one that fires during
	// startGeneration's own commit — and merely COUNT every later call.
	// completeStateWriteTestHook fires once per terminal write; with
	// exactly one injection, the chain is: startGeneration's commit
	// injects → revives to startGeneration+1 → that generation's OWN
	// commit fires the hook a second time, finds nothing new to inject,
	// and does not revive again. Two terminal writes total, derived below
	// at the point that counts them.
	var hookMu sync.Mutex
	var hookErr error
	var hookCalls int
	completeStateWriteTestHook = func(sessionID string) {
		hookMu.Lock()
		hookCalls++
		first := hookCalls == 1
		hookMu.Unlock()
		if !first {
			return
		}
		_, err := al.EnqueueSteeringMessage(
			sessionID, testDefaultAgentID,
			providers.Message{Role: "user", Content: "ROUND3-S1-LATE-STEER"}, "round3-s1-late-steer")
		hookMu.Lock()
		hookErr = err
		hookMu.Unlock()
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	if err := al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "pre-steer answer"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn: %v", err)
	}

	hookMu.Lock()
	firstHookErr := hookErr
	hookMu.Unlock()
	if firstHookErr != nil {
		t.Fatalf("BLOCKED: round-3 spec item 2 (a steer arriving after the finishing mark begins must be ACCEPTED, held as a post-finish steer, never refused) is not implemented — steeringQueue.pushItemScopeChecked (pkg/agent/steering.go) still refuses once steeringTerminalTransition.committing is true, returning errSteeringScopeClosed (%v) instead of holding the item; this also blocks verifying item 4's revival to generation %d", firstHookErr, startGeneration+1)
	}

	// Item 4: a held post-finish steer must revive the child to generation
	// startGeneration+1 immediately after the terminal commit landed
	// above. This is synchronous — ReviveStoppedSession's admission
	// commit (steer_launcher.go::dispatchSteeredSessionWithReservation's
	// commitSteeredDispatchState) runs inline inside completeSteeredTurn;
	// only the revived generation's own TURN EXECUTION is the detached
	// goroutine awaited below — so it already holds true the instant
	// completeSteeredTurn returned.
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.Generation != startGeneration+1 || rec.Terminal() {
		t.Fatalf("BLOCKED: round-3 spec item 4 (a held post-finish steer immediately revives the child to a new generation right after the terminal commit) is not implemented — no call site in completeSteeredTurn (pkg/agent/steer_completion.go) or steeringQueue.runTerminalTransition (pkg/agent/steering.go) ever invokes SteerCanceller.Revive or dispatchSteeredSession after a successful commit; child record generation=%d terminal=%v, want generation=%d running", rec.Generation, rec.Terminal(), startGeneration+1)
	}

	// The revived generation's own turn runs on a detached goroutine
	// (steer_launcher.go::dispatchSteeredSessionWithReservation's
	// al.goSteeredTurn call, joined by the SAME admission WaitGroup
	// AgentLoop.Close drains). Wait for it, bounded, before asserting the
	// chain's end shape — never a sleep-as-assertion.
	al.drainSteeredTurns(10 * time.Second)

	// The chain TERMINATES: exactly one revival, generation
	// startGeneration+1 is terminal, no startGeneration+2, nothing left
	// queued.
	rec, err = al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child) after the drain settled: %v", err)
	}
	if rec.Generation != startGeneration+1 || !rec.Terminal() {
		t.Fatalf("chain did not terminate at generation %d; generation=%d terminal=%v — with the fixture injecting only once, the revived generation's own commit must find nothing left to inject and stop there", startGeneration+1, rec.Generation, rec.Terminal())
	}
	if got := al.pendingSteeringCountForScope(child.SessionID); got != 0 {
		t.Errorf("pending steering count after the chain settled = %d, want 0: nothing may be left waiting with no consumer", got)
	}

	// Two terminal writes total: startGeneration's own commit (where the
	// fixture's one late steer is injected) and startGeneration+1's commit
	// (which sees no new item and does not revive again).
	hookMu.Lock()
	totalHookCalls := hookCalls
	hookMu.Unlock()
	if totalHookCalls != 2 {
		t.Fatalf("completeStateWriteTestHook total calls = %d, want exactly 2 (one terminal write for generation %d, one for the revived generation %d)", totalHookCalls, startGeneration, startGeneration+1)
	}

	// Item 3: the pre-steer answer's own final, for generation
	// startGeneration, must still be delivered unchanged at its
	// deterministic id — the spec's own literal notation, "<child>:<gen>:final".
	// Item 5: the revived generation's final must name the late steer via
	// the round-4 correction's literal prefix.
	msgs, _, _, drainErr := al.GetMessageInboxStore().Drain(rootID, child.SessionID, "", 10)
	if drainErr != nil {
		t.Fatalf("Drain(root): %v", drainErr)
	}
	wantGenFinalID := fmt.Sprintf("%s:%d:final", child.SessionID, startGeneration)
	wantNextGenFinalID := fmt.Sprintf("%s:%d:final", child.SessionID, startGeneration+1)
	var genFinal, nextGenFinal *generated.SessionMessage
	for i := range msgs {
		switch messageIDOf(msgs[i]) {
		case wantGenFinalID:
			genFinal = &msgs[i]
		case wantNextGenFinalID:
			nextGenFinal = &msgs[i]
		}
	}
	if genFinal == nil {
		t.Fatalf("generation %d final (id %q) was not delivered unchanged; delivered message count = %d", startGeneration, wantGenFinalID, len(msgs))
	}
	handback, herr := genFinal.AsSessionMessageHandback()
	if herr != nil || handback.ResultSoFar != "pre-steer answer" {
		t.Fatalf("generation %d final content = %q (decode err %v), want unchanged %q", startGeneration, handback.ResultSoFar, herr, "pre-steer answer")
	}

	if nextGenFinal == nil {
		t.Fatalf("generation %d final (id %q) was not delivered; delivered message count = %d", startGeneration+1, wantNextGenFinalID, len(msgs))
	}
	nextHandback, nherr := nextGenFinal.AsSessionMessageHandback()
	if nherr != nil {
		t.Fatalf("AsSessionMessageHandback(generation %d final): %v", startGeneration+1, nherr)
	}
	const wantPrefix = "Follow-up after a late instruction: "
	if !strings.HasPrefix(nextHandback.ResultSoFar, wantPrefix) {
		t.Errorf("generation %d final ResultSoFar = %q, must carry the late-instruction prefix (founder ruling Q10, round-4 correction item 4)", startGeneration+1, nextHandback.ResultSoFar)
	}
	// mockProvider (newAL's default provider, mock_provider_test.go) always
	// answers "Mock response" regardless of input — the revived generation
	// ran a REAL turn, not a value this test invented; dozens of other
	// tests in this package (e.g. loop_test.go) rely on the same fixed
	// reply as their oracle.
	if got := strings.TrimPrefix(nextHandback.ResultSoFar, wantPrefix); got != "Mock response" {
		t.Errorf("generation %d final ResultSoFar = %q, after stripping the prefix = %q, want %q", startGeneration+1, nextHandback.ResultSoFar, got, "Mock response")
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
	// Round-4 fixture correction (see file header): inject the one late
	// steer on the FIRST Deliver call only. Deliver still ALWAYS fails
	// (that is this deliverer's whole purpose) on every call, including
	// the second one disposeSteeredTurnResult's outer retry loop makes
	// after the continuation below lands — but with nothing new queued
	// that second time, completeSteeredTurn's prepare-failed branch has an
	// empty finishingItems buffer and returns the raw delivery error
	// instead of errCompleteSteeringPending, so the outer loop stops
	// after exactly the one continuation the round-3 spec's item 6
	// describes, and the child is left non-terminal.
	injected := false
	deliverer := &round3FailingDeliverer{onDeliver: func() error {
		if injected {
			return nil
		}
		injected = true
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
//
// Squad-lead review finding (receipt 1020r4c/s3-fake-wake-baseline.log):
// the ORIGINAL fixture called EnqueueSteeringWake with a fabricated
// message id ("round3-s3-descendant-wake") that was never stored anywhere.
// EnqueueSteeringWake's own doc comment says it "queues an upward wake
// into an already-live turn while RETAINING THE INBOX MESSAGE IDENTITY" —
// a wake is only the in-memory nudge for an entry that is already durable
// in the message inbox; the durable-wake fallback this test exercises has
// nothing to redeliver when no such entry exists, so the old fixture
// passed while proving nothing. Production's real writer, on the real
// upward path, is steer_audience.go::SteerUpwardDeliverer.Deliver's
// `res, appendErr := inbox.Append(ownerKey, msg)` (line ~390) — it durably
// stores the descendant's message BEFORE the sibling call at line ~472
// enqueues the wake using res.MessageID. This fixture now does the same:
// append a real wake-eligible entry into the CHILD's own inbox (child is
// the "owner" for its own descendant's message) via the same
// MessageInboxStore.Append, then enqueue the wake with THAT entry's real
// message id.
//
// Observable chosen for "never stranded": deliverEntryIsAcked
// (steer_audience.go) is production's own primitive for "is this durable
// entry still waiting for a consumer" — Drain still returning it means
// unacked; not returning it means acked. Right after the terminal commit
// the entry must still be unacked (proves the durable copy survived — only
// the round-4 correction's in-memory drop is expected here, never a
// durable loss). Then, bounded, this test waits to see whether ANYTHING
// gives the entry a consumer: either the child is revived to a new
// generation (whose own turn would then be the consumer), or the entry
// becomes acked by some other route. processFinishingItems's wake branch
// (pkg/agent/steer_completion.go) currently does neither — it only logs —
// so this is expected to stay RED until the backend-lead wires an actual
// durable-wake fallback for the post-finish case; the failure names that
// exact gap rather than a generic mismatch.
func TestSteeredTurnDrain1020Round3_DescendantWakeDuringOwnTerminalDeliveryNeverStranded(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, rootID, "round3-s3-wake")
	startGeneration := child.Generation

	inbox := al.GetMessageInboxStore()
	const descendantSessionID = "round3-s3-grandchild"
	var descendantMsg generated.SessionMessage
	if err := descendantMsg.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId:      descendantSessionID + ":1:final",
		SessionId:      descendantSessionID,
		CreatedAt:      time.Now().UTC(),
		Depth:          2,
		SenderIdentity: testDefaultAgentID,
		Mode:           generated.SessionMessageHandbackModeFinal,
		ResultSoFar:    "ROUND3-S3-DESCENDANT-WAKE",
		Artifacts:      []string{},
		OpenQuestions:  []string{},
	}); err != nil {
		t.Fatalf("FromSessionMessageHandback(descendant entry): %v", err)
	}
	// child is the OWNER here: it is the recipient of its own descendant's
	// upward message, exactly mirroring inbox.Append(ownerKey, msg) at
	// steer_audience.go::SteerUpwardDeliverer.Deliver.
	appendRes, appendErr := inbox.Append(child.SessionID, descendantMsg)
	if appendErr != nil {
		t.Fatalf("Append(descendant inbox entry): %v", appendErr)
	}

	var hookErr error
	var hookCalls int
	completeStateWriteTestHook = func(sessionID string) {
		hookCalls++
		hookErr = al.EnqueueSteeringWake(
			sessionID, testDefaultAgentID, sessionID,
			appendRes.MessageID,
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
		t.Fatalf("BLOCKED: round-3 spec item 2/S3 (a descendant's wake arriving during the child's own terminal delivery must be ACCEPTED, never refused) is not implemented — steeringQueue.pushItemScopeChecked (pkg/agent/steering.go) refused this wake (%v)", hookErr)
	}
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if !rec.Terminal() {
		t.Fatalf("child did not reach terminal; state=%q", rec.State)
	}
	if got := al.pendingSteeringCountForScope(child.SessionID); got != 0 {
		t.Errorf("descendant wake accepted (no error) but %d item(s) remain queued in the in-memory steering queue on a now-terminal child — round-3 spec S3 requires it never be lost", got)
	}

	// The durable copy must survive the in-memory drop: right after the
	// terminal commit the entry is still unacked (a consumer has not run
	// yet), never silently vanished.
	acked, ackedErr := deliverEntryIsAcked(inbox, child.SessionID, descendantSessionID, appendRes.MessageID)
	if ackedErr != nil {
		t.Fatalf("deliverEntryIsAcked (immediately after commit): %v", ackedErr)
	}
	if acked {
		t.Fatalf("descendant entry %q reported ACKED immediately after the terminal commit — no consumer has run yet, so this can only mean the durable copy itself was lost, not merely the in-memory one", appendRes.MessageID)
	}

	// Bounded wait for a real consumer: either the child is revived to a
	// new generation (whose own turn would then be the consumer), or the
	// entry becomes acked by some other route. Never a sleep-as-assertion
	// — matches the codebase's own poll idiom
	// (steer_boundary_containment_test.go::launchAndAwaitSteeredChild).
	al.drainSteeredTurns(3 * time.Second)
	deadline := time.Now().Add(3 * time.Second)
	var revived bool
	for time.Now().Before(deadline) {
		rec, err = al.GetSessionLifecycleStore().Load(child.SessionID)
		if err != nil {
			t.Fatalf("Load(child) while waiting for a consumer: %v", err)
		}
		revived = rec.Generation != startGeneration
		acked, ackedErr = deliverEntryIsAcked(inbox, child.SessionID, descendantSessionID, appendRes.MessageID)
		if ackedErr != nil {
			t.Fatalf("deliverEntryIsAcked (while waiting for a consumer): %v", ackedErr)
		}
		if revived || acked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !revived && !acked {
		t.Fatalf("BLOCKED: round-3 spec S3's durable-wake fallback for a post-finish wake is not implemented — processFinishingItems (pkg/agent/steer_completion.go) drops the in-memory wake with only an INFO log line (\"post-finish wake on a terminalised child\") and never revives the child, wakes anyone, or otherwise arranges a consumer; descendant entry %q under owner %q remains durably stored (proven above) but is permanently unacknowledged with no consumer — generation stayed %d, acked=%v", appendRes.MessageID, child.SessionID, rec.Generation, acked)
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
	// Outcome is asserted via the child record load and enqueueErr below,
	// not runTerminalTransition's own return values — mirrors the
	// codebase's own established pattern for this exact situation
	// (turn_test.go::newAL's `//nolint:dogsled` on newTestAgentLoop).
	_, _, _ = al.steering.runTerminalTransition(child.SessionID, //nolint:dogsled
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
