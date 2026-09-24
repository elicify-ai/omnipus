// delegate_park_test.go: respond/resume behaviour of a parked delegation.

package tools

import (
	"context"
	"fmt"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// seedParkedChildAwaitingAnswer persists a native child parked on
// correlationID and seeds the self_ok question respond() re-verifies in the
// parent's inbox, so the call reaches resumeNative rather than being turned
// away by an earlier gate.
func seedParkedChildAwaitingAnswer(t *testing.T, lc *session.LifecycleStore, inbox *session.MessageInboxStore, sessionID, correlationID string) {
	t.Helper()
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: sessionID, Generation: 1, State: session.LifecycleNeedsInput,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID:    "ws-1", AgentID: "worker",
		NeedsInput: &session.NeedsInput{CorrelationID: correlationID, TTLDeadline: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("seed parked child: %v", err)
	}
	if _, err := inbox.Append("parent-1", questionMsgForDelegateTest(t, sessionID, "q-"+correlationID,
		correlationID, generated.SessionMessageQuestionAuthoritySelfOk)); err != nil {
		t.Fatalf("seed self_ok question: %v", err)
	}
}

// TestDelegateTool_Respond_RefusesResumeWhenTheAnswerCannotLand is ADR-091
// fix lane RX-OUTCOME's exit proof for delegate_park.go::resumeNative.
//
// resumeNative called appendFollowUpInstruction and DISCARDED its error, then
// dispatched regardless — the identical defect the sibling lane already fixed
// in delegate_followup.go::executeFollowUp, with the identical consequence:
// agent/steer_reconstruct.go::reconstructSteeredTurn rebuilds the resumed turn
// from the last `user` transcript entry, so a dropped answer makes the child
// re-run the instruction it was working on BEFORE it asked its question and
// report THAT answer upward as if it were the reply.
func TestDelegateTool_Respond_RefusesResumeWhenTheAnswerCannotLand(t *testing.T) {
	const sessionID = "child-respond-answer-lost"
	tool, lc, inbox, _ := newADR053TestTool(t)
	launcher := &followUpLauncher{dispatch: steer.DispatchResult{State: steer.DispatchRunning, Generation: 1}}
	tool.SetSessionLauncher(launcher)
	tool.SetSessionStore(&followUpHistoryStore{appendErr: fmt.Errorf("transcript append failed: disk full")})
	seedParkedChildAwaitingAnswer(t, lc, inbox, sessionID, "corr-lost")

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": sessionID, "correlation_id": "corr-lost", "text": "yes, ship it",
	})

	if launcher.sessionID != "" {
		t.Fatalf("Dispatch was called for %q even though the answer never landed — the child will re-run its PREVIOUS instruction and report that answer upward as the reply to this response",
			launcher.sessionID)
	}
	if !result.IsError {
		t.Fatalf("respond reported success though the answer never landed: %s", result.ForLLM)
	}
	rec, err := lc.Load(sessionID)
	if err != nil {
		t.Fatalf("Load after refusal: %v", err)
	}
	// Landed failed, not left running: the Mutate inside resumeNative has
	// already cleared NeedsInput, so the correlation id respond() re-checks
	// is gone and this call can never be retried. A `running` record with no
	// live turn would strand the child AND block its parent's own completion
	// for ever (agent/steer_completion.go::hasRunningOrQueuedDescendant).
	if rec.State != session.LifecycleFailed {
		t.Errorf("state after a refused respond = %q, want %q — a record left running with no live turn strands the child and blocks its parent for ever",
			rec.State, session.LifecycleFailed)
	}
	if rec.FailedReason == "" {
		t.Error("a refused respond must leave a non-empty FailedReason so a later delegate(status)/peek poll can discover why")
	}
}

// TestDelegateTool_Respond_DispatchesWhenTheAnswerLands is the positive half:
// the refusal above must not have been bought by breaking the happy path.
// The answer has to be written where the rebuilt turn actually reads it —
// the TRANSCRIPT — and only then may Dispatch run.
func TestDelegateTool_Respond_DispatchesWhenTheAnswerLands(t *testing.T) {
	const sessionID = "child-respond-answer-lands"
	tool, lc, inbox, _ := newADR053TestTool(t)
	launcher := &followUpLauncher{dispatch: steer.DispatchResult{State: steer.DispatchRunning, Generation: 1}}
	tool.SetSessionLauncher(launcher)
	store := &followUpHistoryStore{}
	tool.SetSessionStore(store)
	seedParkedChildAwaitingAnswer(t, lc, inbox, sessionID, "corr-lands")

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": sessionID, "correlation_id": "corr-lands", "text": "yes, ship it",
	})

	if result.IsError {
		t.Fatalf("respond failed on the happy path: %s", result.ForLLM)
	}
	if launcher.sessionID != sessionID {
		t.Fatalf("Dispatch session id = %q, want %q — the parked turn must actually be resumed", launcher.sessionID, sessionID)
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	var landed bool
	for _, entry := range entries {
		if entry.Role == "user" && entry.Content == "Answer to your question (correlation_id=corr-lands): yes, ship it" {
			landed = true
		}
	}
	if !landed {
		t.Fatalf("the answer never reached the transcript the rebuilt turn reads; entries = %#v", entries)
	}
	rec, err := lc.Load(sessionID)
	if err != nil {
		t.Fatalf("Load after respond: %v", err)
	}
	if rec.State != session.LifecycleRunning {
		t.Errorf("state after a successful respond = %q, want %q", rec.State, session.LifecycleRunning)
	}
}
