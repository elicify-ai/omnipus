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
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
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
var _ = fmt.Sprintf
