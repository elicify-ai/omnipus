// delegate_park_test.go: respond to a STOPPED helper. The person-question
// park this file used to exercise (a NeedsInput record answered through a
// parked-record resume path) was removed by ADR-20261004 ("Steering commands:
// no person question"): a helper question parks nothing, and an answer is an
// ordinary steering message. What remains to pin here is the stopped row of
// Correction C1's message/state table: the parent's answer RESUMES the
// stopped helper on the same conversation through the steering sink's
// ReviveStoppedSession — and a refused revive is reported as an error, never
// dressed up as a delivered answer.
//
// (The old "the answer must land before anything dispatches" integrity rule
// moved with the resume mechanics into pkg/agent's ReviveStoppedSession —
// append-before-generation, refuse on append failure — and is pinned there by
// w6_question_crash_recovery_test.go.)

package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// failingReviverSink implements DelegateSteeringSink and steerReviver, with
// the revive failing — the "the resume could not land" half at this boundary.
type failingReviverSink struct {
	gotSessionID   string
	gotInstruction string
	err            error
}

func (f *failingReviverSink) EnqueueSteeringMessage(scope, agentID string, msg providers.Message, correlationID string) (string, error) {
	return "corr_test", nil
}

func (f *failingReviverSink) ReviveStoppedSession(ctx context.Context, sessionID string, by steer.Principal, instruction string) (bool, error) {
	f.gotSessionID = sessionID
	f.gotInstruction = instruction
	return false, f.err
}

// seedStoppedChild persists a native child durably stopped at its current
// generation — the exact shape SteerCanceller leaves behind (stopped state,
// StopNote, no live fence). No NeedsInput record is seeded: nothing writes
// one any more (ADR-20261004 removed the person-question park).
func seedStoppedChild(t *testing.T, lc *session.LifecycleStore, sessionID string) {
	t.Helper()
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: sessionID, Generation: 1, State: session.LifecycleStopped,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID:    "ws-1", AgentID: "worker",
		StopNote: &session.StopNote{
			At: time.Now().UTC(), By: "human:dan", Seq: 1, Cause: session.StopCauseStop,
		},
	}); err != nil {
		t.Fatalf("seed stopped child: %v", err)
	}
}

// TestDelegateTool_Respond_StoppedChild_ResumesSameConversationViaReviver is
// the stopped row of the message/state table: the parent's answer to a
// stopped helper revives the SAME session through the steering sink's
// ReviveStoppedSession, with the answer framed by the correlation id it
// replies to.
func TestDelegateTool_Respond_StoppedChild_ResumesSameConversationViaReviver(t *testing.T) {
	const sessionID = "child-respond-answer-resumes"
	tool, lc, _, _ := newADR053TestTool(t)
	reviver := &fakeReviverSink{}
	tool.SetSteeringSink(reviver)
	seedStoppedChild(t, lc, sessionID)

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": sessionID, "correlation_id": "corr-lands", "text": "yes, ship it",
	})

	if result.IsError {
		t.Fatalf("respond to a stopped helper failed: %s", result.ForLLM)
	}
	revived, gotSessionID, gotInstruction, queued := reviver.snapshot()
	if !revived {
		t.Fatalf("respond to a stopped helper must revive it through ReviveStoppedSession (%d messages went via the plain steering queue instead)", queued)
	}
	if gotSessionID != sessionID {
		t.Errorf("ReviveStoppedSession session = %q, want %q — the answer resumes the SAME conversation", gotSessionID, sessionID)
	}
	// respondAnswerInstruction (delegate_respond.go) frames the answer with the
	// correlation id, so the revived child can tie it to its own open message.
	if gotInstruction != "Answer to your question (correlation_id=corr-lands): yes, ship it" {
		t.Errorf("ReviveStoppedSession instruction = %q, want the correlation-framed answer", gotInstruction)
	}
	// The wire ack (DelegateRespondResponse) is the caller-visible result.
	if !strings.Contains(result.ForLLM, `"acknowledged":true`) {
		t.Errorf("respond ack = %q, want the acknowledged:true DelegateRespondResponse payload", result.ForLLM)
	}
}

// TestDelegateTool_Respond_RefusesVisiblyWhenTheReviveFails is the refusal
// half: when the revive cannot land, respond reports the error — the caller
// must never read a failed resume as a delivered answer.
func TestDelegateTool_Respond_RefusesVisiblyWhenTheReviveFails(t *testing.T) {
	const sessionID = "child-respond-answer-refused"
	tool, lc, _, _ := newADR053TestTool(t)
	sink := &failingReviverSink{err: errors.New("simulated revive failure: transcript append refused")}
	tool.SetSteeringSink(sink)
	seedStoppedChild(t, lc, sessionID)

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": sessionID, "correlation_id": "corr-lost", "text": "yes, ship it",
	})

	if !result.IsError {
		t.Fatalf("respond reported success though the revive failed: %s", result.ForLLM)
	}
	if sink.gotSessionID != sessionID {
		t.Errorf("the refused respond must still have been addressed to the named session, got %q", sink.gotSessionID)
	}
	if !strings.Contains(result.ForLLM, "resume session") {
		t.Errorf("the refusal must name the failed resume step, got: %s", result.ForLLM)
	}
	rec, err := lc.Load(sessionID)
	if err != nil {
		t.Fatalf("Load after refusal: %v", err)
	}
	// A refused revive must leave the record exactly as it was — stopped at
	// the same generation, so a retry addresses the same conversation.
	if rec.State != session.LifecycleStopped || rec.Generation != 1 {
		t.Errorf("state/generation after a refused respond = (%s, %d), want (stopped, 1) — the record must be untouched and retryable",
			rec.State, rec.Generation)
	}
}
