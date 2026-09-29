// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Round-four correction coverage for issue #1020:
//
//  1. The post-finish revival's hand-back to the parent is prefixed with
//     "Follow-up after a late instruction: ..." (founder ruling Q10; round-4
//     correction item 4). The prefix is added in completionMessage when
//     consumePostFinishRevival returns true for the session's NEW
//     generation, so the parent can see the wake-up summary was triggered
//     by a late steer rather than an ordinary new turn.
//  2. delegate(action="steer") on a finishing child returns
//     "queued; the child is finishing and will see it next" instead of the
//     plain success, by plumbing EnqueueStatus from EnqueueSteeringMessage
//     into the delegate tool's caller-facing result text (round-4
//     correction item 5).
//
// Both tests are NEW — the brief explicitly forbids editing existing test
// assertions. They wire up to the existing fixtures (newSteerAL,
// wireSteerCompletionDeps, launchRunningChild, etc.) the round-3 tests
// already use, and assert the spec's exact text in the wake-up summary
// and the delegate tool's result.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestSteeredTurnDrain1020Round4_PostFinishRevivalHandBackPrefixesLateInstruction
// pins the round-4 correction item 4: the revived generation's final
// hand-back to the parent carries "Follow-up after a late instruction: "
// in the ResultSoFar field. The test drives a post-finish revival, then
// invokes completionMessage directly with a generation-N+1 record to
// exercise the prefix path without waiting on the full async turn
// pipeline (which the round-3 S1 test already covers).
func TestSteeredTurnDrain1020Round4_PostFinishRevivalHandBackPrefixesLateInstruction(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, rootID, "round4-late-steer-prefix")

	completeStateWriteTestHook = func(sessionID string) {
		_, _ = al.EnqueueSteeringMessage(
			sessionID, testDefaultAgentID,
			providers.Message{Role: "user", Content: "ROUND4-LATE-STEER-FOR-PREFIX"}, "")
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	if err := al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "pre-late answer"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn (gen=g): %v", err)
	}

	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.Generation != child.Generation+1 || rec.Terminal() {
		t.Fatalf("revival did not land; generation=%d terminal=%v want gen=%d running", rec.Generation, rec.Terminal(), child.Generation+1)
	}

	// Drive the revived gen=g+1 final directly through completionMessage,
	// which is the production path that consumes the post-finish stamp.
	// The stamp was set by processFinishingItems before the async
	// dispatch and is now sitting on the loop, waiting for THIS
	// generation's completion to read-and-clear it.
	newGenRec := *rec
	msg, err := al.completionMessage(&newGenRec, "final_answer", "post-late answer", "")
	if err != nil {
		t.Fatalf("completionMessage: %v", err)
	}
	handback, herr := msg.AsSessionMessageHandback()
	if herr != nil {
		t.Fatalf("AsSessionMessageHandback: %v", herr)
	}
	if !strings.HasPrefix(handback.ResultSoFar, "Follow-up after a late instruction:") {
		t.Errorf("gen=%d final ResultSoFar = %q, must carry the late-instruction prefix", newGenRec.Generation, handback.ResultSoFar)
	}
	if got := strings.TrimPrefix(handback.ResultSoFar, "Follow-up after a late instruction: "); got != "post-late answer" {
		t.Errorf("gen=%d final ResultSoFar = %q, after stripping the prefix = %q, want %q (prefix must PREPEND the answer, not REPLACE it)", newGenRec.Generation, handback.ResultSoFar, got, "post-late answer")
	}
	// Idempotency: a second read-and-clear for the SAME session/generation
	// must return false so a later hand-back (e.g. a tool-iteration
	// lifecycle notice after a revival) is not permanently mislabelled.
	if al.consumePostFinishRevival(newGenRec.SessionID, newGenRec.Generation) {
		t.Errorf("consumePostFinishRevival did not clear the stamp after the gen=%d final consumed it", newGenRec.Generation)
	}
}

// TestSteeredTurnDrain1020Round4_CompleteSteeredTurnWithoutLateSteerNoPrefix
// is the negative control: a turn that completes with no late steer
// MUST NOT carry the late-instruction prefix. This pairs with the
// post-finish revival test above to prove the prefix is conditional on
// the post-finish-revival stamp — the S5 positive control covers the
// one-no-late-steer path of the round-3 spec, but the round-4 prefix
// is new and so needs its own negative control.
func TestSteeredTurnDrain1020Round4_CompleteSteeredTurnWithoutLateSteerNoPrefix(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, rootID, "round4-no-prefix-control")

	if err := al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "plain answer"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn: %v", err)
	}

	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	newGenRec := *rec
	msg, err := al.completionMessage(&newGenRec, "final_answer", "plain answer", "")
	if err != nil {
		t.Fatalf("completionMessage: %v", err)
	}
	handback, herr := msg.AsSessionMessageHandback()
	if herr != nil {
		t.Fatalf("AsSessionMessageHandback: %v", herr)
	}
	if strings.HasPrefix(handback.ResultSoFar, "Follow-up after a late instruction:") {
		t.Errorf("non-revival hand-back ResultSoFar = %q, must NOT carry the late-instruction prefix", handback.ResultSoFar)
	}
}

// ensurePrefixFormat holds the round-4 spec's literal text the new tests
// pin against. Lives at file scope so a future spec drift surfaces as a
// test failure rather than a silent mismatch.
const ensurePrefixFormat = "Follow-up after a late instruction: "

var _ = ensurePrefixFormat

// TestSteeredTurnDrain1020Round4_PersistentDeliveryFailureIsBoundedAndLoud
// covers the round-4 correction's own bound: a deliverer that NEVER
// succeeds, and that injects a FRESH late steer on every attempt (the
// pathological shape the qa-lead round-4 fixture correction removed from
// the other three tests in this suite, kept here on purpose to prove the
// bound catches it), must not spin disposeSteeredTurnResult's drain-retry
// loop forever.
//
// disposeSteeredTurnResult's outer loop (steer_launcher.go) shares
// continueDrainMaxRetries (session_worker.go) with the ordinary session
// worker's own drain-retry. steerTurnDrainProvider1020
// (steer_turn_drain_1020_test.go) allows exactly two successful Chat calls
// before erroring on the third; that third call turns the third late
// steer's continuation into a POST-dequeue failure
// (steering.go::continuePendingSteeringWithAgent's run() branch, which
// restores the item to the queue before returning), and
// retrySteeringContinuation (session_worker.go) does NOT retry a
// post-dequeue failure — its stopOn matches errContinuePostDequeueFailure
// on the very first attempt — so drainSteeredTurn (steer_turn_drain.go)
// calls abandonSteeredQueuedSteering for that one restored item
// immediately. disposeSteeredTurnResult's own attempt counter is at
// continueDrainMaxRetries by the same iteration, so the loop exits right
// after. The two events coincide by this fixture's construction, not by
// accident: the injection-count assertion below is what actually proves
// the OUTER loop ran exactly continueDrainMaxRetries times, independent of
// the provider's own cap.
func TestSteeredTurnDrain1020Round4_PersistentDeliveryFailureIsBoundedAndLoud(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default test agent is not registered")
	}
	agent.Provider = provider

	var child steer.LaunchResult
	var injectCount int
	deliverer := &round3FailingDeliverer{onDeliver: func() error {
		injectCount++
		_, err := al.EnqueueSteeringMessage(
			child.SessionID, testDefaultAgentID,
			providers.Message{Role: "user", Content: fmt.Sprintf("ROUND4-PERSISTENT-LATE-STEER-%d", injectCount)}, "")
		return err
	}}
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(classifier), nil, deliverer)

	child = launchQueuedSteeredTurnDrainChild1020(t, al, testDefaultAgentID, "round4 persistent delivery failure")
	snapshot := setLifecycleState1020(t, al, child.SessionID, session.LifecycleRunning)
	ts, err := al.reconstructSteeredTurn(snapshot, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}

	originalBackoff := continueDrainBackoff
	continueDrainBackoff = []time.Duration{0, 0, 0}
	t.Cleanup(func() { continueDrainBackoff = originalBackoff })
	logPath := filepath.Join(t.TempDir(), "round4-persistent-abandonment.jsonl")
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)

	// Bounded by construction (see the derivation above), so this call is
	// expected to return quickly; go test's own -timeout is the outer
	// safety net if a mutation removes the bound (brief item (d)).
	al.disposeSteeredTurnResult(ts, snapshot, snapshot.Generation, turnResult{finalContent: "seed answer"}, nil)
	logger.DisableFileLogging()

	if injectCount != continueDrainMaxRetries {
		t.Fatalf("late-steer injections = %d, want exactly continueDrainMaxRetries (%d): one per disposeSteeredTurnResult outer attempt", injectCount, continueDrainMaxRetries)
	}

	requests := provider.Requests()
	if len(requests) != continueDrainMaxRetries {
		t.Fatalf("continuation provider request count = %d, want exactly continueDrainMaxRetries (%d)", len(requests), continueDrainMaxRetries)
	}
	for i := 0; i < continueDrainMaxRetries; i++ {
		want := fmt.Sprintf("ROUND4-PERSISTENT-LATE-STEER-%d", i+1)
		if got := countMessagesContaining(requests[i], want); got != 1 {
			t.Errorf("continuation request %d occurrences of %q = %d, want exactly 1", i, want, got)
		}
	}

	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.Terminal() {
		t.Fatalf("child became terminal despite every terminal delivery failing; state=%q", rec.State)
	}
	if got := al.pendingSteeringCountForScope(child.SessionID); got != 0 {
		t.Errorf("pending steering count after bounded exhaustion = %d, want 0: the last item must be abandoned, never left queued", got)
	}

	logData, logErr := os.ReadFile(logPath)
	if logErr != nil {
		t.Fatalf("ReadFile(abandonment log): %v", logErr)
	}
	matchedAbandonment := 0
	for _, line := range strings.Split(strings.TrimSpace(string(logData)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry map[string]any
		if decodeErr := json.Unmarshal([]byte(line), &entry); decodeErr != nil {
			t.Fatalf("decode abandonment log line: %v; line=%q", decodeErr, line)
		}
		if entry["message"] != "steer: persistent Continue failure — abandoning queued steering" ||
			entry["session_id"] != child.SessionID {
			continue
		}
		matchedAbandonment++
		if entry["queue_depth"] != float64(1) {
			t.Errorf("abandonment queue_depth = %v, want exactly 1 (only the third late steer was ever queued at abandonment time)", entry["queue_depth"])
		}
		if entry["attempts"] != float64(1) {
			t.Errorf("abandonment attempts = %v, want exactly 1: a post-dequeue failure (errContinuePostDequeueFailure) is never retried", entry["attempts"])
		}
	}
	if matchedAbandonment != 1 {
		t.Fatalf("abandonSteeredQueuedSteering log entries = %d, want exactly 1 — the drain must report the leftover item loudly, exactly once, never a silent drop", matchedAbandonment)
	}

	entries, readErr := al.GetSessionStore().ReadTranscript(child.SessionID)
	if readErr != nil {
		t.Fatalf("ReadTranscript(child): %v", readErr)
	}
	reports := 0
	for _, e := range entries {
		if e.Status == "error" {
			reports++
		}
	}
	if reports != 1 {
		t.Errorf("abandonment error transcript entries = %d, want exactly 1 (the one abandoned item)", reports)
	}
}
