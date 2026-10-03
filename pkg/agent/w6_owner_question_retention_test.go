// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// W6 RED pack 2 — ADR-20260928 D1.1, D1.4, D1.7, D1.8, T8, T14, T25.
//
// The owner_required retention cases below are behavioral: a real
// message_parent park, production StopTurns (single and stop-all), a fresh
// store open, then the parent's respond. The provenance, expiry and
// withdrawal cases name seams that are not in this tree. They fail as
// COMPILE-BLOCKED on purpose. They are not behavioral RED, and they do not
// invent an owner from session.Owner.

import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	w6OwnerCorrelation = "w6-owner-required-staging"
	// w6OwnerRequiredGuidance is D1.1's respond result for an owner_required
	// question: non-error, and the question stays open.
	w6OwnerRequiredGuidance = "This question needs the owner. Use action=escalate to pass it up, or redirect/RESUME the child without an owner answer."
)

// TestW6OwnerRequiredQuestion_SurvivesSingleStopAndFreshStore keeps one
// owner_required question across a single Stop and a fresh store open.
func TestW6OwnerRequiredQuestion_SurvivesSingleStopAndFreshStore(t *testing.T) {
	w6AssertOwnerQuestionSurvivesStop(t, false)
}

// TestW6OwnerRequiredQuestion_SurvivesStopAllAndFreshStore keeps the same
// question across Stop all. Repeated Stop all must not reset the deadline;
// this case checks the first Stop all leaves the original deadline.
func TestW6OwnerRequiredQuestion_SurvivesStopAllAndFreshStore(t *testing.T) {
	w6AssertOwnerQuestionSurvivesStop(t, true)
}

func w6AssertOwnerQuestionSurvivesStop(t *testing.T, stopAll bool) {
	t.Helper()
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-owner-required")
	parent := u1LaunchChild(t, al, root, "w6-owner-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-owner-child")
	deadline, generation := w6ParkOwnerRequiredQuestion(t, al, parent.SessionID, child.SessionID)

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}
	w6StopScope(t, al, parent.SessionID, child.SessionID, by, stopAll)

	freshLC, freshInbox := w6ReopenMessagingStores(t, al)
	stoppedChild := w6MustLoad(t, freshLC, child.SessionID)
	stoppedParent := w6MustLoad(t, freshLC, parent.SessionID)

	delegate, resumes := w6ParentRespondTool(t, al, freshLC, freshInbox)
	parentCtx := tools.WithTranscriptSessionID(context.Background(), parent.SessionID)
	result := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, w6OwnerCorrelation, "approved"))
	afterChild := w6MustLoad(t, freshLC, child.SessionID)
	afterParent := w6MustLoad(t, freshLC, parent.SessionID)
	untouched := w6OwnerQuestionUntouched(result, resumes, afterChild, generation)

	parentStopped := !stopAll || (stoppedParent.State == session.LifecycleStopped && afterParent.State == session.LifecycleStopped)
	if stoppedChild.State != session.LifecycleStopped || stoppedChild.Generation != generation ||
		!untouched || !parentStopped || afterParent.Generation != stoppedParent.Generation {
		t.Fatalf("owner_required respond changed the stopped child or was not the owner guidance (ADR D1.1/D1.7): "+
			"stop_all=%t child_state=%s parent_state=%s after_child=%s after_parent=%s generation=%d "+
			"needs_input=%v respond_error=%v respond=%q dispatches=%d want_guidance=%q parked_deadline=%s",
			stopAll, stoppedChild.State, stoppedParent.State, afterChild.State, afterParent.State, stoppedChild.Generation,
			stoppedChild.NeedsInput, result.IsError, result.ForLLM, len(resumes.calls),
			w6OwnerRequiredGuidance, deadline.Format(time.RFC3339Nano))
	}
}

func w6ParkOwnerRequiredQuestion(t *testing.T, al *AgentLoop, parentID, childID string) (time.Time, int) {
	t.Helper()
	tool := tools.NewMessageParentTool(al.getUpwardDeliverer(), al.GetSessionLifecycleStore())
	tool.SetSessionMessagingEnabled(func() bool { return true })
	before := time.Now()
	ctx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), childID), childID)
	result := tool.Execute(ctx, map[string]any{
		"kind": "question", "text": "may I use the staging key?", "wait": true,
		"authority": "owner_required", "correlation_id": w6OwnerCorrelation,
	})
	after := time.Now()
	if result.IsError {
		t.Fatalf("message_parent owner_required question: %s", result.ForLLM)
	}
	rec := w6MustLoad(t, al.GetSessionLifecycleStore(), childID)
	if rec.State != session.LifecycleNeedsInput || rec.NeedsInput == nil || rec.NeedsInput.CorrelationID != w6OwnerCorrelation {
		t.Fatalf("park did not record owner question: state=%s needs_input=%v", rec.State, rec.NeedsInput)
	}
	earliest := before.Add(session.DefaultNeedsInputTTL)
	latest := after.Add(session.DefaultNeedsInputTTL)
	if rec.NeedsInput.TTLDeadline.Before(earliest) || rec.NeedsInput.TTLDeadline.After(latest) {
		t.Fatalf("parked TTLDeadline = %s, want the original 24h deadline", rec.NeedsInput.TTLDeadline.Format(time.RFC3339Nano))
	}
	w6AssertInboxAuthority(t, al, parentID, childID)
	return rec.NeedsInput.TTLDeadline, rec.Generation
}

func w6AssertInboxAuthority(t *testing.T, al *AgentLoop, parentID, childID string) {
	t.Helper()
	msgs, _, _, err := al.GetMessageInboxStore().Drain(parentID, childID, "", 10)
	if err != nil {
		t.Fatalf("Drain parent inbox: %v", err)
	}
	for _, msg := range msgs {
		kind, kindErr := msg.Discriminator()
		if kindErr != nil || kind != "question" {
			continue
		}
		question, qerr := msg.AsSessionMessageQuestion()
		if qerr != nil || question.CorrelationId != w6OwnerCorrelation {
			continue
		}
		if question.Authority != nil && string(*question.Authority) == string(generated.SessionMessageQuestionAuthorityOwnerRequired) {
			return
		}
		t.Fatalf("inbox authority = %v, want owner_required", question.Authority)
	}
	t.Fatalf("parent inbox has no owner_required question %s", w6OwnerCorrelation)
}

func w6StopScope(t *testing.T, al *AgentLoop, parentID, childID string, by steer.Principal, stopAll bool) {
	t.Helper()
	canceller := NewSteerCanceller(al.GetSessionLifecycleStore())
	if stopAll {
		if _, err := canceller.StopTurns(context.Background(), parentID, by, true, al.SteerGenerationCancel); err != nil {
			t.Fatalf("StopTurns(stop all): %v", err)
		}
		return
	}
	if _, err := canceller.StopTurns(context.Background(), childID, by, false, al.SteerGenerationCancel); err != nil {
		t.Fatalf("StopTurns(child): %v", err)
	}
}

func w6OwnerQuestionUntouched(result *tools.ToolResult, resumes *w6ResumeRecorder, child *session.LifecycleRecord, generation int) bool {
	if result == nil || result.IsError || !strings.Contains(result.ForLLM, w6OwnerRequiredGuidance) || len(resumes.calls) != 0 {
		return false
	}
	return child.State == session.LifecycleStopped && child.Generation == generation
}

// TestW6QuestionDeadline_SeparateRecordRead_CompileBlocked is not a
// behavioral RED. D1.7 keeps the original TTLDeadline in a pending-question
// record separate from NeedsInput. This tree has no reader for that record
// and no expiry sweeper, so the deadline cannot be checked without pinning
// the stopped lifecycle tail.
func TestW6QuestionDeadline_SeparateRecordRead_CompileBlocked(t *testing.T) {
	t.Fatal("COMPILE-BLOCKED: no durable pending-question reader and no question-expiry sweeper — required by ADR D1.7/D1.8 so the original TTLDeadline can be read after a fresh store open. The stopped lifecycle tail is not that record. Not a behavioral RED.")
}

// TestW6OwnerAnswer_ProvenanceWhoWhenWhichOnce_CompileBlocked is not a
// behavioral RED. D1.4's signed-in owner answer (direct child and relay
// root), including wrong principal, forged Role=user, client-chosen id,
// before-shown ordinal and duplicate use, needs a persisted authenticated
// per-entry writer. This tree does not have one. session.Owner is not used
// as a substitute.
func TestW6OwnerAnswer_ProvenanceWhoWhenWhichOnce_CompileBlocked(t *testing.T) {
	t.Fatal("COMPILE-BLOCKED: no per-entry trusted provenance writer (server message id, authenticated principal, append ordinal) — required by ADR D1.4 Who/When/Which/Once, T1 and T14. pkg/gateway/websocket_chat.go::persistUserMessage logs a non-first append failure and continues; TranscriptEntry has no append ordinal; GatewayUserID is not durable. Not a behavioral RED.")
}

// TestW6QuestionExpiry_OwnerUnreachableAndAnswerTimeout_CompileBlocked is
// not a behavioral RED. No question-expiry sweeper or injectable expiry
// clock exists, so just-before/at/after, owner_unreachable text, fatal
// upward notice, relay superseded, self_ok answer_timeout, stale post-expiry
// refusal and idempotent retry cannot be driven. The existing
// TestU1GoalStaysActiveAcrossEveryStopPath question-expiry subtest is
// unchanged.
func TestW6QuestionExpiry_OwnerUnreachableAndAnswerTimeout_CompileBlocked(t *testing.T) {
	t.Fatal("COMPILE-BLOCKED: no question-expiry sweeper — required by ADR D1.8/T8. Missing outcomes: at or after the original deadline failed(owner_unreachable) text \"owner could not be reached\" plus a fatal direct-parent notice and relay superseded; self_ok failed(answer_timeout); a late owner answer refused stale without consuming the message; repeat expiry is idempotent. Just-before must still be answerable. Not a behavioral RED.")
}

// TestW6OwnerQuestion_WithdrawByRedirectOrResume_CompileBlocked is not a
// behavioral RED. Delegate has no redirect or resume action, so an
// unanswered owner_required withdrawal cannot record the audit entry or the
// factual unanswered note, and a later stale answer cannot be refused.
func TestW6OwnerQuestion_WithdrawByRedirectOrResume_CompileBlocked(t *testing.T) {
	t.Fatal("COMPILE-BLOCKED: delegate has no redirect or resume action — required by ADR D1.7/T25. Missing outcomes: question and relays superseded, pkg/audit entry, runtime fact containing \"without an answer from the owner\", and a later owner answer refused stale without being consumed. Not a behavioral RED.")
}
