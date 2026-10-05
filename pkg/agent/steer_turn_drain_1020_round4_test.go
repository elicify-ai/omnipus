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
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// R1 preserves the exact prefix and non-replacement oracle on a genuine
// post-commit next-round fixture, never on input accepted before the commit.
func TestSteeredTurnDrain1020Round4_PostFinishRevivalHandBackPrefixesLateInstruction(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	publication := installGoalCommitGate(t, al)
	child, provider := r1AdmitChild(t, al, rootID, "round4-late-steer-prefix", "pre-late answer", "post-late answer")
	provider.open(0)
	select {
	case <-publication.event:
	case <-time.After(5 * time.Second):
		t.Fatal("final did not reach real post-commit publication")
	}
	committed := rootReopenedRecord(t, al, child.SessionID)
	if committed.State != session.LifecycleCompleted || committed.FinalDelivery == nil {
		t.Fatalf("post-commit premise missing: state=%q outbox=%+v", committed.State, committed.FinalDelivery)
	}
	if _, err := al.EnqueueSteeringMessage(child.SessionID, testDefaultAgentID,
		providers.Message{Role: "user", Content: "ROUND4-LATE-STEER-FOR-PREFIX"}, "round4-prefix-control"); err != nil {
		t.Fatalf("post-commit late steer: %v", err)
	}
	publication.open()
	r1AwaitProvider(t, provider, 1)
	rec := rootReopenedRecord(t, al, child.SessionID)
	if rec.Generation != child.Generation+1 || rec.State != session.LifecycleRunning || rec.Terminal() {
		t.Fatalf("post-commit revival G=%d state=%q terminal=%v, want G=%d running", rec.Generation, rec.State, rec.Terminal(), child.Generation+1)
	}
	assertSteerRevivalInput1020(t, provider.Requests()[1:], []string{"ROUND4-LATE-STEER-FOR-PREFIX"})
	provider.open(1)
	joinGoalFixtureRuns(t, al)
	r1AssertFinals(t, al, child, map[string]string{
		fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation):   "pre-late answer",
		fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation+1): "Follow-up after a late instruction: post-late answer",
	})
	if al.consumePostFinishRevival(child.SessionID, child.Generation+1) {
		t.Error("real G+1 final did not clear the post-finish prefix stamp")
	}
	if got := al.pendingSteeringCountForScope(child.SessionID); got != 0 {
		t.Errorf("stranded late prefix input=%d, want zero", got)
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
	// D2 repairs the execution fixture; R1 retains this no-input prefix control.
	child = admitSteeringRepairExecution(t, al, child.SessionID, child.Generation)

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

// R1: a publication failure does not uncommit G. Bound a real outbox retry
// pass and separately admit two next rounds; conserve every accepted instruction
// and keep each failed publication owed, never silently abandoned.
func TestSteeredTurnDrain1020Round4_PersistentDeliveryFailureIsBoundedAndLoud(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-round4-persistent")
	child, provider := r1AdmitChild(t, al, parentID, "round4-persistent-real-owner", "seed answer", "round two answer", "round three answer")
	logPath := filepath.Join(t.TempDir(), "round4-persistent-publication.jsonl")
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)
	deliveries := 0
	failing := &round3FailingDeliverer{onDeliver: func() error { deliveries++; return nil }}
	al.SetSteerAudienceDeps(al.getSteerAudienceResolver(), nil, failing)
	provider.openAll()
	joinGoalFixtureRuns(t, al)
	accepted := []string{"ROUND4-PERSISTENT-LATE-STEER-1", "ROUND4-PERSISTENT-LATE-STEER-2"}
	for _, text := range accepted {
		resumed, err := al.ReviveStoppedSession(context.Background(), child.SessionID,
			steer.Principal{Kind: steer.PrincipalKindHuman, ID: "round4-parent"}, text)
		if err != nil || !resumed {
			t.Fatalf("independent next-round admission %q: resumed=%v error=%v", text, resumed, err)
		}
		joinGoalFixtureRuns(t, al)
	}
	if deliveries != 3 || len(provider.Requests()) != 3 {
		t.Fatalf("bounded initial+two independent rounds: deliveries=%d provider calls=%d, want exactly three each", deliveries, len(provider.Requests()))
	}
	assertSteerRevivalInput1020(t, provider.Requests()[1:], accepted)
	current := rootReopenedRecord(t, al, child.SessionID)
	if current.Generation != child.Generation+2 || current.State != session.LifecycleCompleted {
		t.Fatalf("failed publication undid a committed next round: generation=%d state=%q", current.Generation, current.State)
	}
	pending, err := al.GetSessionLifecycleStore().ListPendingFinalDeliveries()
	if err != nil || len(pending) != 3 {
		t.Fatalf("owed committed finals=%d error=%v, want all three generations", len(pending), err)
	}
	for _, item := range pending {
		if item.SessionID != child.SessionID || !item.Pending() || item.Progress.InboxAppended || len(item.Commit.Payload) == 0 {
			t.Fatalf("failed publication lost payload or falsely acknowledged it: %+v", item)
		}
	}
	var notices []string
	recovery := &SteerBootRecovery{Lifecycle: al.GetSessionLifecycleStore(), Sessions: al.GetSessionStore(), Inbox: al.GetMessageInboxStore(),
		Deliverer: failing, OperatorNotice: func(text string) { notices = append(notices, text) }}
	if retryErr := recovery.runFinalDeliveryPass(context.Background()); retryErr != nil {
		t.Fatalf("delivery-only retry pass: %v", retryErr)
	}
	// One pass visits the three owed commits once; it must not spin or start
	// additional model work. Each owed final must have its own visible failure.
	if deliveries != 6 || len(provider.Requests()) != 3 {
		t.Fatalf("retry was unbounded or ran compute: deliveries=%d provider calls=%d, want 6/3", deliveries, len(provider.Requests()))
	}
	if len(notices) != len(pending) {
		t.Fatalf("publication failure notices=%d, want one per owed committed final (%d): %q", len(notices), len(pending), notices)
	}
	for _, item := range pending {
		visible := 0
		for _, notice := range notices {
			if strings.Contains(notice, item.Commit.MessageID+" ") && strings.Contains(notice, "simulated terminal delivery failure") {
				visible++
			}
		}
		if visible != 1 {
			t.Errorf("owed final %q has %d failure notices, want exactly one", item.Commit.MessageID, visible)
		}
	}
	after := rootReopenedRecord(t, al, child.SessionID)
	if !reflect.DeepEqual(after, current) {
		t.Errorf("delivery-only retry rewrote the committed current lifecycle: before=%+v after=%+v", current, after)
	}
	afterPending, err := al.GetSessionLifecycleStore().ListPendingFinalDeliveries()
	// D2 promises every immutable commit, not scan enumeration order.
	// Canonicalize only this unordered collection; compare every commit,
	// payload byte, progress fact, revision and retirement field exactly.
	// Input delivery ordering above remains a separate, ordered oracle.
	sort.Slice(pending, func(i, j int) bool { return pending[i].Generation < pending[j].Generation })
	sort.Slice(afterPending, func(i, j int) bool { return afterPending[i].Generation < afterPending[j].Generation })
	if err != nil || !reflect.DeepEqual(afterPending, pending) {
		t.Errorf("retry silently lost/acknowledged owed finals: before=%+v after=%+v error=%v", pending, afterPending, err)
	}
	if count := al.pendingSteeringCountForScope(child.SessionID); count != 0 {
		t.Errorf("stranded accepted input=%d, want zero", count)
	}
	logger.DisableFileLogging()
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(publication log): %v", err)
	}
	loudFailures := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if decodeErr := json.Unmarshal([]byte(line), &event); decodeErr != nil {
			t.Fatalf("decode publication log: %v", decodeErr)
		}
		if event["message"] == "steer: complete turn failed" && event["session_id"] == child.SessionID && strings.Contains(fmt.Sprint(event["error"]), "simulated terminal delivery failure") {
			loudFailures++
		}
	}
	if loudFailures != 3 {
		t.Errorf("loud failed-commit publication reports=%d, want exactly three", loudFailures)
	}
	entries, err := al.GetSessionStore().ReadTranscript(child.SessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(child): %v", err)
	}
	for _, text := range accepted {
		injections := 0
		for _, e := range entries {
			if e.Role == "user" && e.Content == text {
				injections++
			}
		}
		if injections != 1 {
			t.Errorf("durable instruction %q injections=%d, want one", text, injections)
		}
	}
	for _, e := range entries {
		if e.Status == "error" && strings.Contains(e.Content, "queued follow-up message could not be processed") {
			t.Errorf("delivered next-round input falsely reported abandoned: %q", e.Content)
		}
	}
}
