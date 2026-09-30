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
	"errors"
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
	// above. The generation advances synchronously, but its detached turn
	// may already have completed by the time we read the record; either
	// running or terminal at startGeneration+1 is a valid observation.
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.Generation != startGeneration+1 {
		t.Fatalf("BLOCKED: round-3 spec item 4 (a held post-finish steer immediately revives the child to a new generation right after the terminal commit) is not implemented — child record generation=%d terminal=%v, want generation=%d (running or already terminal)", rec.Generation, rec.Terminal(), startGeneration+1)
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
// The terminal-write hook captures the generation-g final in the parent's
// durable inbox and the descendant entry in the child's UNACKED Drain
// before a replay can revive and acknowledge it. A wake follows S1's
// post-finish steer contract: the original final is unchanged, then the
// child revives to g+1 and its real turn consumes (marks and acknowledges)
// the descendant entry. The revived turn must finish with no wake queued
// or unacknowledged. At HEAD, processFinishingItems only logs and drops
// the in-memory wake, so this test must fail at that latter consumer check.
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

	// The hook fires BEFORE the generation-g terminal write and AFTER its
	// final was delivered. Observe both inboxes here: a replay may already
	// have acknowledged the descendant by the time completion returns.
	// Only the first terminal write injects a wake; g+1's own write must
	// not manufacture a second wake and an unbounded revival chain.
	type commitObservation struct {
		enqueueErr    error
		parent        []session.InboxEntry
		parentErr     error
		descendant    []generated.SessionMessage
		descendantErr error
	}
	var hookMu sync.Mutex
	var hookCalls int
	var observed commitObservation
	completeStateWriteTestHook = func(sessionID string) {
		hookMu.Lock()
		hookCalls++
		first := hookCalls == 1
		hookMu.Unlock()
		if !first {
			return
		}
		enqueueErr := al.EnqueueSteeringWake(
			sessionID, testDefaultAgentID, sessionID,
			appendRes.MessageID,
			providers.Message{Role: "user", Content: "ROUND3-S3-DESCENDANT-WAKE"},
		)
		parent, parentErr := inbox.Entries(rootID)
		descendant, _, _, descendantErr := inbox.Drain(child.SessionID, descendantSessionID, "", 10)
		hookMu.Lock()
		observed = commitObservation{enqueueErr, parent, parentErr, descendant, descendantErr}
		hookMu.Unlock()
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	if err := al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "pre-wake answer"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn: %v", err)
	}
	hookMu.Lock()
	firstHookCalls := hookCalls
	commit := observed
	hookMu.Unlock()
	if firstHookCalls < 1 {
		t.Fatal("completeStateWriteTestHook never observed the generation-g terminal write")
	}
	if commit.enqueueErr != nil {
		t.Fatalf("BLOCKED: round-3 spec item 2/S3 (a descendant's wake arriving during the child's own terminal delivery must be ACCEPTED, never refused) — EnqueueSteeringWake returned %v", commit.enqueueErr)
	}
	if commit.parentErr != nil || commit.descendantErr != nil {
		t.Fatalf("inspect inboxes before terminal write: parent=%v descendant=%v", commit.parentErr, commit.descendantErr)
	}

	// Assert the generation-g final's deterministic identity and unchanged
	// answer while the hand-off is paused, then confirm it remains durable
	// after the successful terminal write. Never inspect the child's state
	// here: the g+1 replay is free to have started already.
	wantGenFinalID := fmt.Sprintf("%s:%d:final", child.SessionID, startGeneration)
	assertGenFinal := func(entries []session.InboxEntry) {
		t.Helper()
		var found int
		for _, entry := range entries {
			if entry.Kind != session.InboxEntryMessage || entry.Message == nil || messageIDOf(*entry.Message) != wantGenFinalID {
				continue
			}
			found++
			final, decodeErr := entry.Message.AsSessionMessageHandback()
			if decodeErr != nil {
				t.Fatalf("generation %d final %q is not a handback: %v", startGeneration, wantGenFinalID, decodeErr)
			}
			if final.ResultSoFar != "pre-wake answer" {
				t.Fatalf("generation %d final %q changed: got %q, want %q", startGeneration, wantGenFinalID, final.ResultSoFar, "pre-wake answer")
			}
		}
		if found != 1 {
			t.Fatalf("generation %d final %q durable entries = %d, want exactly 1 unchanged handback", startGeneration, wantGenFinalID, found)
		}
	}
	assertGenFinal(commit.parent)
	if len(commit.descendant) != 1 || messageIDOf(commit.descendant[0]) != appendRes.MessageID {
		t.Fatalf("descendant durable UNACKED entries during terminal write = %d, want the one real entry %q", len(commit.descendant), appendRes.MessageID)
	}
	stored, decodeErr := commit.descendant[0].AsSessionMessageHandback()
	if decodeErr != nil || stored.ResultSoFar != "ROUND3-S3-DESCENDANT-WAKE" {
		t.Fatalf("durable descendant entry content = %q (decode err %v), want %q", stored.ResultSoFar, decodeErr, "ROUND3-S3-DESCENDANT-WAKE")
	}
	parentAfterCommit, parentErr := inbox.Entries(rootID)
	if parentErr != nil {
		t.Fatalf("Entries(parent) after terminal commit: %v", parentErr)
	}
	assertGenFinal(parentAfterCommit)

	// The real wake consumer must revive to exactly g+1, write its consumed
	// marker, acknowledge the durable entry, and finish its turn. Neither a
	// bare revival nor an ACK without a turn is sufficient. The drain joins
	// admitted turns; the bounded poll checks the durable end state.
	al.drainSteeredTurns(10 * time.Second)
	transcriptStore := al.ResolveSessionStore(child.SessionID)
	if transcriptStore == nil {
		t.Fatal("ResolveSessionStore(child): no transcript store for the wake consumer")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
		if err != nil {
			t.Fatalf("Load(child) while waiting for wake consumption: %v", err)
		}
		acked, ackedErr := deliverEntryIsAcked(inbox, child.SessionID, descendantSessionID, appendRes.MessageID)
		if ackedErr != nil {
			t.Fatalf("deliverEntryIsAcked(descendant): %v", ackedErr)
		}
		unacked, _, more, drainErr := inbox.Drain(child.SessionID, "", "", 10)
		if drainErr != nil {
			t.Fatalf("Drain(child) while waiting for wake consumption: %v", drainErr)
		}
		transcript, readErr := transcriptStore.ReadTranscript(child.SessionID)
		if readErr != nil {
			t.Fatalf("ReadTranscript(child) while waiting for wake consumption: %v", readErr)
		}
		consumed := false
		for _, entry := range transcript {
			if entry.ID == "consumed-"+appendRes.MessageID && entry.Content == "consumed "+appendRes.MessageID {
				consumed = true
				break
			}
		}
		queued := al.pendingSteeringCountForScope(child.SessionID)
		if rec.Generation == startGeneration+1 && rec.Terminal() && consumed && acked && len(unacked) == 0 && !more && queued == 0 {
			if rec.State != session.LifecycleCompleted {
				t.Fatalf("revived wake consumer ended in state %q, want completed at generation %d", rec.State, startGeneration+1)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("BLOCKED: round-3 spec S3 post-finish durable wake was not consumed by a revived generation — child generation=%d state=%q (want terminal g+1=%d), consumed marker=%v, descendant acked=%v, child unacked=%d more=%v, queued=%d; at HEAD processFinishingItems only logs/drops the wake", rec.Generation, rec.State, startGeneration+1, consumed, acked, len(unacked), more, queued)
		}
		time.Sleep(10 * time.Millisecond)
	}
	hookMu.Lock()
	totalHookCalls := hookCalls
	hookMu.Unlock()
	if totalHookCalls != 2 {
		t.Fatalf("completeStateWriteTestHook calls = %d, want exactly 2 (one for generation %d and one for revived generation %d)", totalHookCalls, startGeneration, startGeneration+1)
	}
}

// TestSteeredTurnDrain1020Round3_StopRaceDuringWakeDeliveryNeverStrandsAcceptedWake
// covers S4 with the real Stop cascade, durable descendant message and
// completeSteeredTurn's production finishing-items callback. A successful
// wake enqueue must end at a real consumer, not merely leave the main queue.
func TestSteeredTurnDrain1020Round3_StopRaceDuringWakeDeliveryNeverStrandsAcceptedWake(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, rootID, "round3-s4-stop-race")
	inbox := al.GetMessageInboxStore()

	const descendantID = "round3-s4-grandchild"
	const wakeText = "ROUND3-S4-DURABLE-WAKE"
	var message generated.SessionMessage
	if err := message.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId:      descendantID + ":1:final",
		SessionId:      descendantID,
		CreatedAt:      time.Now().UTC(),
		Depth:          2,
		SenderIdentity: testDefaultAgentID,
		Mode:           generated.SessionMessageHandbackModeFinal,
		ResultSoFar:    wakeText,
		Artifacts:      []string{},
		OpenQuestions:  []string{},
	}); err != nil {
		t.Fatalf("FromSessionMessageHandback(descendant): %v", err)
	}
	// The real upward writer appends before enqueuing its wake. A fabricated
	// message ID would let an accepted wake vanish without a recoverable entry.
	appended, err := inbox.Append(child.SessionID, message)
	if err != nil {
		t.Fatalf("Append(child inbox): %v", err)
	}
	unacked, _, _, err := inbox.Drain(child.SessionID, descendantID, "", 10)
	if err != nil || len(unacked) != 1 || messageIDOf(unacked[0]) != appended.MessageID {
		t.Fatalf("SETUP: durable unacknowledged descendant entry = %+v, err=%v; want the appended ID %q", unacked, err, appended.MessageID)
	}

	var hookMu sync.Mutex
	var hookCalls int
	var stopErr, stopReadErr, enqueueErr error
	var stopState session.LifecycleState
	var stopReport steer.CancelReport
	completeStateWriteTestHook = func(sessionID string) {
		hookMu.Lock()
		hookCalls++
		first := hookCalls == 1
		hookMu.Unlock()
		if !first {
			return
		}
		// The finishing mark is installed: Stop lands via the real cascade
		// while completion is between durable delivery and its terminal write.
		stopReport, stopErr = al.steerCanceller().CancelSubtree(context.Background(), sessionID,
			steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"})
		stopped, readErr := al.GetSessionLifecycleStore().Load(sessionID)
		stopReadErr = readErr
		if readErr == nil {
			stopState = stopped.State
		}
		// Resume the pending upward delivery while the finishing mark is open.
		enqueueErr = al.EnqueueSteeringWake(sessionID, testDefaultAgentID, sessionID,
			appended.MessageID, providers.Message{Role: "user", Content: wakeText})
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	completionErr := al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "before Stop"}, nil)
	al.drainSteeredTurns(10 * time.Second)
	hookMu.Lock()
	calls := hookCalls
	hookMu.Unlock()
	if calls < 1 || stopErr != nil || stopReadErr != nil || stopState != session.LifecycleCancelled || len(stopReport.Unreachable) != 0 {
		t.Fatalf("SETUP: real Stop must terminalise the child in the finishing window: calls=%d state=%q stopErr=%v readErr=%v unreachable=%+v completionErr=%v",
			calls, stopState, stopErr, stopReadErr, stopReport.Unreachable, completionErr)
	}
	entries, err := inbox.Entries(child.SessionID)
	if err != nil {
		t.Fatalf("Entries(child inbox): %v", err)
	}
	found := 0
	for _, entry := range entries {
		if entry.Kind == session.InboxEntryMessage && entry.Message != nil && messageIDOf(*entry.Message) == appended.MessageID {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("durable descendant entry count after Stop = %d, want exactly 1 for %q", found, appended.MessageID)
	}
	if enqueueErr != nil {
		// Explicit refusal preserves the entry for recovery; it must not
		// claim success or acknowledge a message nobody consumed.
		if !errors.Is(enqueueErr, errSteeringScopeClosed) {
			t.Fatalf("wake was refused for an unexpected reason: %v", enqueueErr)
		}
		pending, _, _, drainErr := inbox.Drain(child.SessionID, descendantID, "", 10)
		if drainErr != nil || len(pending) != 1 || messageIDOf(pending[0]) != appended.MessageID {
			t.Fatalf("refused wake must remain durably unacknowledged for recovery: entries=%+v err=%v, want %q", pending, drainErr, appended.MessageID)
		}
		return
	}

	acked, err := deliverEntryIsAcked(inbox, child.SessionID, descendantID, appended.MessageID)
	if err != nil {
		t.Fatalf("deliverEntryIsAcked(descendant): %v", err)
	}
	transcript, err := al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(child): %v", err)
	}
	consumed := 0
	for _, entry := range transcript {
		if entry.ID == "consumed-"+appended.MessageID && entry.Content == "consumed "+appended.MessageID {
			consumed++
		}
	}
	pending := al.pendingSteeringCountForScope(child.SessionID)
	if !acked || consumed != 1 || pending != 0 {
		t.Fatalf("accepted Stop-race wake %q was not delivered to a real consumer: durable=%d acked=%v consumed markers=%d pending=%d completionErr=%v; want one durable entry, ACK, one consumed marker and no stranded queue item (round-3 S4)",
			appended.MessageID, found, acked, consumed, pending, completionErr)
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
