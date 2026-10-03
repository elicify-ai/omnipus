// delegate_question.go: durable pending question separate from NeedsInput (ADR D1.7).

package tools

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// ownerRequiredRespondGuidance is the parent respond result for an
// owner_required question. It is not an authenticated owner answer.
const ownerRequiredRespondGuidance = "This question needs the owner. Use action=escalate to pass it up, or redirect/RESUME the child without an owner answer."

func questionStoreFromLifecycle(lc MessageParentLifecycleStore) (*session.QuestionStore, error) {
	if lc == nil {
		return nil, fmt.Errorf("no lifecycle store configured for the pending-question record")
	}
	type directory interface{ Dir() string }
	d, ok := lc.(directory)
	if !ok || strings.TrimSpace(d.Dir()) == "" {
		return nil, fmt.Errorf("lifecycle store has no directory for the pending-question record")
	}
	return session.NewQuestionStore(session.PendingQuestionDir(d.Dir())), nil
}

// appendPendingQuestion writes the open question under the lifecycle lock the
// caller already holds (park's Mutate). A missing directory is a visible error.
func (t *MessageParentTool) appendPendingQuestion(cur *session.LifecycleRecord, correlationID, authority string, deadline time.Time) error {
	if cur == nil {
		return session.ErrLifecycleNotFound
	}
	store, err := questionStoreFromLifecycle(t.lifecycle)
	if err != nil {
		return fmt.Errorf("record pending question: %w", err)
	}
	if authority != session.QuestionAuthoritySelfOK {
		authority = session.QuestionAuthorityOwnerRequired
	}
	return store.Append(session.PendingQuestion{
		CorrelationID:    correlationID,
		AskerSessionID:   cur.SessionID,
		AskerGeneration:  cur.Generation,
		Authority:        authority,
		OriginalDeadline: deadline.UTC(),
		Status:           session.QuestionStatusOpen,
	})
}

// acceptStoppedPendingQuestion accepts a respond whose lifecycle park was
// cleared by Stop, when the separate question record is still answerable.
// It does not write. accepted is false when this is not that case.
func (dt *delegateToolExecuteRespond) acceptStoppedPendingQuestion() (accepted bool, result *ToolResult, stop bool) {
	if dt.rec == nil || dt.rec.State != session.LifecycleStopped {
		return false, nil, false
	}
	store, err := questionStoreFromLifecycle(dt.t.lifecycle)
	if err != nil {
		return false, ErrorResult(fmt.Sprintf("delegate: respond: %v", err)).WithError(err), true
	}
	q, err := store.Load(dt.sessionID)
	if err != nil {
		if errors.Is(err, session.ErrPendingQuestionNotFound) {
			return false, nil, false
		}
		return false, ErrorResult(fmt.Sprintf("delegate: respond: %v", err)).WithError(err), true
	}
	if q.CorrelationID != dt.correlationID || q.AskerSessionID != dt.sessionID || !q.Answerable() {
		return false, nil, false
	}
	if q.AskerGeneration != dt.rec.Generation {
		return false, ErrorResult(fmt.Sprintf(
			"delegate: respond: question %q belongs to generation %d, session is generation %d",
			dt.correlationID, q.AskerGeneration, dt.rec.Generation,
		)), true
	}
	if q.Status == session.QuestionStatusOpen && q.Expired(time.Now()) {
		return false, ErrorResult(fmt.Sprintf(
			"delegate: respond: question %q expired at %s",
			dt.correlationID, q.OriginalDeadline.Format(time.RFC3339Nano),
		)), true
	}
	dt.pending = q
	return true, nil, false
}

func (dt *delegateToolExecuteRespond) pendingQuestionAuthorityResult() (*ToolResult, bool) {
	if dt.pending == nil {
		return nil, false
	}
	switch dt.pending.Authority {
	case session.QuestionAuthorityOwnerRequired:
		return NewToolResult(ownerRequiredRespondGuidance), true
	case session.QuestionAuthoritySelfOK:
		return nil, false
	default:
		return ErrorResult(fmt.Sprintf(
			"delegate: respond: question %q has unknown authority %q",
			dt.correlationID, dt.pending.Authority,
		)), true
	}
}

func pendingAnswerInstruction(correlationID, text string) string {
	return fmt.Sprintf("Answer to your question (correlation_id=%s): %s", correlationID, text)
}

// resumePendingQuestion delivers a self_ok answer to a stopped asker and asks
// Dispatch to resume that same generation. Admission owns the lifecycle state:
// this path only queues a session that is still stopped after Dispatch returns,
// and it never writes failed, completed, or needs_input to satisfy a caller.
func (dt *delegateToolExecuteRespond) resumePendingQuestion() *ToolResult {
	instruction := pendingAnswerInstruction(dt.correlationID, dt.text)
	if err := dt.commitPendingAnswer(instruction); err != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", err)).WithError(err)
	}
	if err := dt.recordPendingAnswer(instruction); err != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: answer pending delivery: %v", err)).WithError(err)
	}
	if _, err := dt.t.launcher.Dispatch(dt.ctx, dt.sessionID, dt.rec.Generation); err != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: answer pending delivery: dispatch: %v", err)).WithError(err)
	}
	if err := dt.finishPendingResume(); err != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: answer pending delivery: %v", err)).WithError(err)
	}
	return dt.acknowledgedRespond()
}

func (dt *delegateToolExecuteRespond) commitPendingAnswer(instruction string) error {
	mu := dt.t.lifecycle.Lock(dt.sessionID)
	mu.Lock()
	defer mu.Unlock()
	store, err := questionStoreFromLifecycle(dt.t.lifecycle)
	if err != nil {
		return err
	}
	q, err := store.Load(dt.sessionID)
	if err != nil {
		return err
	}
	if q.CorrelationID != dt.correlationID || q.AskerGeneration != dt.rec.Generation {
		return fmt.Errorf("session %s is not parked on correlation_id %q", dt.sessionID, dt.correlationID)
	}
	switch q.Status {
	case session.QuestionStatusOpen:
		if q.Expired(time.Now()) {
			return fmt.Errorf("question %q expired at %s", dt.correlationID, q.OriginalDeadline.Format(time.RFC3339Nano))
		}
		q.Status = session.QuestionStatusAnswerPendingDelivery
		q.AnswerText = instruction
		return store.Append(*q)
	case session.QuestionStatusAnswerPendingDelivery:
		if q.AnswerText != instruction {
			return fmt.Errorf("question %q already has a different pending answer", dt.correlationID)
		}
		return nil
	default:
		return fmt.Errorf("question %q is %s", dt.correlationID, q.Status)
	}
}

func (dt *delegateToolExecuteRespond) recordPendingAnswer(instruction string) error {
	has, err := dt.transcriptHasInstruction(instruction)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	if err := dt.t.appendFollowUpInstruction(dt.sessionID, instruction); err != nil {
		return err
	}
	has, err = dt.transcriptHasInstruction(instruction)
	if err != nil {
		return err
	}
	if !has {
		return fmt.Errorf("answer was not recorded in the transcript for %q", dt.sessionID)
	}
	return nil
}

func (dt *delegateToolExecuteRespond) transcriptHasInstruction(instruction string) (bool, error) {
	if dt.t.sessionStore == nil {
		return false, fmt.Errorf("no session store configured to record the answer")
	}
	entries, err := dt.t.sessionStore.ReadTranscript(dt.sessionID)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Role == "user" && entry.Content == instruction {
			return true, nil
		}
	}
	return false, nil
}

// finishPendingResume closes the question and, if Dispatch did not admit the
// session, queues it for admission. It does not mint a generation.
func (dt *delegateToolExecuteRespond) finishPendingResume() error {
	return dt.t.lifecycle.Mutate(dt.sessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return session.ErrLifecycleNotFound
		}
		if cur.Generation != dt.rec.Generation {
			return fmt.Errorf("session %s generation changed during respond", dt.sessionID)
		}
		switch cur.State {
		case session.LifecycleStopped:
			cur.State = session.LifecycleQueued
			cur.NeedsInput = nil
			cur.FailedReason = ""
			if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
				cur.Stop = nil
			}
		case session.LifecycleQueued, session.LifecycleRunning:
		default:
			return fmt.Errorf("session %s cannot resume from state %s", dt.sessionID, cur.State)
		}
		store, err := questionStoreFromLifecycle(dt.t.lifecycle)
		if err != nil {
			return err
		}
		q, err := store.Load(dt.sessionID)
		if err != nil {
			return err
		}
		if q.CorrelationID != dt.correlationID {
			return fmt.Errorf("session %s question changed during respond", dt.sessionID)
		}
		if q.Status == session.QuestionStatusApplied {
			return nil
		}
		if q.Status != session.QuestionStatusAnswerPendingDelivery {
			return fmt.Errorf("question %q is %s", dt.correlationID, q.Status)
		}
		q.Status = session.QuestionStatusApplied
		return store.Append(*q)
	})
}

func (dt *delegateToolExecuteRespond) acknowledgedRespond() *ToolResult {
	return NewToolResult("Answer delivered; session resumed.")
}
