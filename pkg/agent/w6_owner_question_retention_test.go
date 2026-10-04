// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// W6 RED pack 2 — ADR-20260928 D1.1, D1.4, D1.7, D1.8, T8, T14, T25.
//
// The owner_required retention cases below are behavioral: a real
// message_parent park, production StopTurns (single and stop-all), a fresh
// store open, then the parent's respond. Since the steering-commands
// amendment's Correction C2 removed the 24-hour question expiry with no
// replacement, the former expiry case is behavioral too: it now pins that a
// question's age never closes it nor fails its helper
// (TestW6QuestionExpiry_Retired_AgeNeverClosesAQuestion). The provenance and
// withdrawal cases still name seams that are not in this tree; they fail as
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

// The original-deadline record's behaviour at boot is pinned behaviorally by
// the question boot pack in w6_question_expiry_boot_test.go, over the durable
// pending-question record (pkg/session/question_record.go QuestionStore):
// since the steering-commands amendment's Correction C2 removed the 24-hour
// expiry with no replacement,
// TestW6QuestionExpiryBoot_AgedOwnerRequired_SurvivesBootUnchanged,
// TestW6QuestionExpiryBoot_StoppedAsker and
// TestW6QuestionExpiryBoot_AgedSelfOK_SurvivesBootUnchanged pin that an aged
// question neither expires nor fails its helper, and
// TestW6QuestionExpiryBoot_NotExpiredOwnerRequired_SurvivesBootUnchanged is
// the fresh-park control.

// TestW6OwnerAnswer_ProvenanceWhoWhenWhichOnce_CompileBlocked is not a
// behavioral RED. D1.4's signed-in owner answer (direct child and relay
// root), including wrong principal, forged Role=user, client-chosen id,
// before-shown ordinal and duplicate use, needs a persisted authenticated
// per-entry writer. This tree does not have one. session.Owner is not used
// as a substitute.
func TestW6OwnerAnswer_ProvenanceWhoWhenWhichOnce_CompileBlocked(t *testing.T) {
	t.Fatal("COMPILE-BLOCKED: no per-entry trusted provenance writer (server message id, authenticated principal, append ordinal) — required by ADR D1.4 Who/When/Which/Once, T1 and T14. pkg/gateway/websocket_chat.go::persistUserMessage logs a non-first append failure and continues; TranscriptEntry has no append ordinal; GatewayUserID is not durable. Not a behavioral RED.")
}

// TestW6QuestionExpiry_Retired_AgeNeverClosesAQuestion replaces the former
// TestW6QuestionExpiry_OwnerUnreachableAndAnswerTimeout_CompileBlocked
// placeholder: the steering-commands amendment (Correction C2) REMOVED the
// 24-hour question expiry with no replacement, so the outcomes that
// placeholder demanded (owner_unreachable/answer_timeout failures, relay
// superseded, stale refusal) are retired, not missing. What remains true at
// the store level — and is the exact negation of the retired rule — is
// pinned here: a question whose original deadline is long past is still
// OPEN and ANSWERABLE (session.PendingQuestion.Answerable reads the status,
// never the clock; the recorded deadline is data nothing acts on), and its
// helper keeps its needs_input record. The consumer-level half (boot leaves
// the aged park untouched) is pinned by w6_question_expiry_boot_test.go.
func TestW6QuestionExpiry_Retired_AgeNeverClosesAQuestion(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-owner-required-retired-expiry")
	parent := u1LaunchChild(t, al, root, "w6-retired-expiry-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-retired-expiry-child")

	correlationID := "w6-retired-expiry-owner-required"
	parkClock := time.Now().Add(-48 * time.Hour)
	deadline, generation := w6qeParkQuestion(t, al, child.SessionID, correlationID,
		session.QuestionAuthorityOwnerRequired, parkClock)

	lc, _ := w6qeReopen(t, al)

	// The sidecar is genuinely AGED past its original deadline...
	q := w6qeMustQuestion(t, lc, child.SessionID)
	if !q.Expired(time.Now()) {
		t.Fatalf("setup: the sidecar is not aged past its original deadline %s — the no-expiry rule would be untested", deadline.Format(time.RFC3339Nano))
	}
	// ...and STILL open and answerable: age alone never closes or
	// unanswers a question (Correction C2 — the expiry that did is retired).
	if q.Status != session.QuestionStatusOpen || !q.Answerable() {
		t.Fatalf("aged question = (status %s, answerable %t), want open and answerable — C2: a helper question does not expire", q.Status, q.Answerable())
	}
	// The helper keeps its park: needs_input, same correlation, same
	// recorded deadline, same generation — nothing failed or unparked it.
	rec := w6qeMustLoad(t, lc, child.SessionID)
	if rec.State != session.LifecycleNeedsInput || rec.NeedsInput == nil ||
		rec.NeedsInput.CorrelationID != correlationID || !rec.NeedsInput.TTLDeadline.Equal(deadline) {
		t.Fatalf("aged helper record = (%s, needs_input %v), want needs_input with corr %q at %s — nothing about the question's age touches the helper",
			rec.State, rec.NeedsInput, correlationID, deadline.Format(time.RFC3339Nano))
	}
	if rec.Generation != generation {
		t.Fatalf("helper generation = %d, want %d — an aged question must not dispatch a run or mint a generation", rec.Generation, generation)
	}
}

// TestW6OwnerQuestion_WithdrawByRedirectOrResume_CompileBlocked is not a
// behavioral RED. Delegate has no redirect or resume action, so an
// unanswered owner_required withdrawal cannot record the audit entry or the
// factual unanswered note, and a later stale answer cannot be refused.
func TestW6OwnerQuestion_WithdrawByRedirectOrResume_CompileBlocked(t *testing.T) {
	t.Fatal("COMPILE-BLOCKED: delegate has no redirect or resume action — required by ADR D1.7/T25. Missing outcomes: question and relays superseded, pkg/audit entry, runtime fact containing \"without an answer from the owner\", and a later owner answer refused stale without being consumed. Not a behavioral RED.")
}
