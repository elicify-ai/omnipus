// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// W6, rewritten for ADR-20261004 ("Steering commands: no person question",
// locked decisions 2-7). The owner_required retention pack this file used to
// pin is gone with the person-question pause: a helper question is an
// ORDINARY message — no wait, no authority tag, no owner-only answer path,
// no expiry. What survives — and what this file now pins, behaviorally, over
// REAL stores — is the durability half of the old guarantee, plus the answer
// path that replaced the refused owner guidance:
//
//   - the question message is durable in the parent's inbox across a single
//     Stop, across Stop all, and across a fresh store reopen;
//   - the parent's answer (delegate respond) to the stopped helper revives
//     the SAME conversation through the real wired steering sink, and only
//     the child — the parent stays stopped under Stop all;
//   - respond never launches a session (the revive is the steering sink's).
//
// An aged question staying open, and not failing its helper, is pinned in
// w6_question_expiry_boot_test.go. There is no owner-only answer and no
// 24-hour expiry.

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const w6OwnerCorrelation = "w6-owner-required-staging"

// TestW6Question_SurvivesSingleStopAndFreshStore keeps one helper question
// across a single Stop and a fresh store open, then revives the asker with
// the parent's answer.
func TestW6Question_SurvivesSingleStopAndFreshStore(t *testing.T) {
	w6AssertQuestionSurvivesStopAndAnswerRevives(t, false)
}

// TestW6Question_SurvivesStopAllAndFreshStore keeps the same question across
// Stop all — and proves the parent's answer resumes ONLY the child while the
// parent stays stopped.
func TestW6Question_SurvivesStopAllAndFreshStore(t *testing.T) {
	w6AssertQuestionSurvivesStopAndAnswerRevives(t, true)
}

func w6AssertQuestionSurvivesStopAndAnswerRevives(t *testing.T, stopAll bool) {
	t.Helper()
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-owner-required")
	parent := u1LaunchChild(t, al, root, "w6-owner-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-owner-child")
	w6AskQuestion(t, al, parent.SessionID, child.SessionID)

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}
	w6StopScope(t, al, parent.SessionID, child.SessionID, by, stopAll)

	freshLC, freshInbox := w6ReopenMessagingStores(t, al)
	stoppedChild := w6MustLoad(t, freshLC, child.SessionID)
	stoppedParent := w6MustLoad(t, freshLC, parent.SessionID)
	// Durability: the ordinary question survives the stop(s) and the fresh
	// reopen in the parent's inbox.
	msgs, _, _, err := freshInbox.Drain(parent.SessionID, child.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain parent inbox after reopen: %v", err)
	}
	foundQuestion := false
	for _, msg := range msgs {
		if kind, kindErr := msg.Discriminator(); kindErr == nil && kind == "question" {
			if q, qerr := msg.AsSessionMessageQuestion(); qerr == nil && q.CorrelationId == w6OwnerCorrelation {
				foundQuestion = true
			}
		}
	}
	if !foundQuestion {
		t.Fatalf("the child's question %s did not survive stop_all=%t + fresh store reopen in the parent's inbox (%d messages)",
			w6OwnerCorrelation, stopAll, len(msgs))
	}
	if stoppedChild.State != session.LifecycleStopped || stoppedChild.Generation == 0 {
		t.Fatalf("precondition: the child must be stopped before the answer: state=%s generation=%d",
			stoppedChild.State, stoppedChild.Generation)
	}

	delegate, launches := w6ParentRespondTool(t, al, freshLC, freshInbox)
	parentCtx := tools.WithTranscriptSessionID(context.Background(), parent.SessionID)
	result := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, w6OwnerCorrelation, "approved"))
	if result.IsError {
		t.Fatalf("the parent's answer to a stopped helper must revive it (no owner gate, no guidance refusal), got: %s", result.ForLLM)
	}
	if len(launches.calls) != 0 {
		t.Fatalf("respond must revive through the steering sink, never launch/dispatch a session: %+v", launches.calls)
	}
	afterChild := w6MustLoad(t, freshLC, child.SessionID)
	if afterChild.State == session.LifecycleStopped {
		t.Errorf("the answer must revive the stopped child, state still %s", afterChild.State)
	}
	if afterChild.Generation != stoppedChild.Generation+1 {
		t.Errorf("revived child generation = %d, want %d — the resume continues the SAME conversation as a new generation",
			afterChild.Generation, stoppedChild.Generation+1)
	}
	afterParent := w6MustLoad(t, freshLC, parent.SessionID)
	if afterParent.State != session.LifecycleStopped || afterParent.Generation != stoppedParent.Generation {
		t.Errorf("only the child resumes: parent = (%s, %d), want still stopped at %d",
			afterParent.State, afterParent.Generation, stoppedParent.Generation)
	}
}

// w6AskQuestion sends the child's question to its parent through the
// production message_parent tool and pins the ordinary-message rule at the
// record level: no park, no NeedsInput, no authority state anywhere.
func w6AskQuestion(t *testing.T, al *AgentLoop, parentID, childID string) {
	t.Helper()
	tool := tools.NewMessageParentTool(al.getUpwardDeliverer(), al.GetSessionLifecycleStore())
	tool.SetSessionMessagingEnabled(func() bool { return true })
	ctx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), childID), childID)
	result := tool.Execute(ctx, map[string]any{
		"kind": "question", "text": "may I use the staging key?", "correlation_id": w6OwnerCorrelation,
	})
	if result.IsError {
		t.Fatalf("message_parent question: %s", result.ForLLM)
	}
	rec := w6MustLoad(t, al.GetSessionLifecycleStore(), childID)
	if rec.State == session.LifecycleNeedsInput || rec.NeedsInput != nil {
		t.Fatalf("an ordinary question must not park the helper: state=%s needs_input=%v", rec.State, rec.NeedsInput)
	}
	msgs, _, _, err := al.GetMessageInboxStore().Drain(parentID, childID, "", 10)
	if err != nil {
		t.Fatalf("Drain parent inbox: %v", err)
	}
	for _, msg := range msgs {
		kind, kindErr := msg.Discriminator()
		if kindErr != nil || kind != "question" {
			continue
		}
		if question, qerr := msg.AsSessionMessageQuestion(); qerr == nil && question.CorrelationId == w6OwnerCorrelation {
			return // present, with its correlation id, and carrying no authority field (the generated type has none)
		}
	}
	t.Fatalf("parent inbox has no question %s", w6OwnerCorrelation)
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
