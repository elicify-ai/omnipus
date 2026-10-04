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
	if q.Status == session.QuestionStatusOpen {
		// Reconcile lifecycle vs sidecar (ADR-20260928 D1.5/D1.7): the
		// sidecar OPEN record is appended inside park's Mutate callback
		// before the lifecycle persist, so a failed park leaves an OPEN
		// record whose park never landed. After a restart the record is
		// still readable, and answering it would deliver into a session
		// that never durably parked. The persisted needs_input history
		// line is the evidence the park landed; without it the record is
		// an orphan and the respond is refused visibly. The record itself
		// is left untouched — the refusal is the recovery, not an erase.
		landed, err := dt.t.lifecycle.HasNeedsInputRecord(dt.sessionID, q.AskerGeneration, q.CorrelationID, q.OriginalDeadline)
		if err != nil {
			return false, ErrorResult(fmt.Sprintf("delegate: respond: reconcile pending question: %v", err)).WithError(err), true
		}
		if !landed {
			return false, ErrorResult(fmt.Sprintf(
				"delegate: respond: question %q has no persisted park in the session lifecycle (generation %d); refusing to answer a question whose park never landed",
				dt.correlationID, q.AskerGeneration,
			)), true
		}
		if q.Expired(time.Now()) {
			return false, ErrorResult(fmt.Sprintf(
				"delegate: respond: question %q expired at %s",
				dt.correlationID, q.OriginalDeadline.Format(time.RFC3339Nano),
			)), true
		}
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
//
// Receipt vs effect, in the order resumePendingQuestion runs them
// (ADR-20260928 D1.5): (1) the answer is RESERVED in the sidecar as
// answer_pending_delivery; (2) the answer is RECEIVED — appended to the
// asker's transcript and verified by read-back, which is this tree's
// receipt proof, not a dispatch attempt; (3) Dispatch asks admission to
// resume the same generation; (4) THIS step's lifecycle transition persists
// (the durable resume effect); (5) only after (2) already holds AND (4) has
// persisted does applyPendingQuestion write applied. Applied is therefore
// never written for a queued lifecycle or a dispatch attempt alone, and it
// is never appended inside the Mutate callback: a callback runs before
// Mutate's own persist, so a lifecycle persist failure there would leave
// the sidecar applied while the resume effect never landed — the
// false-applied split. When the Mutate fails, this function returns the
// error visibly, the sidecar keeps answer_pending_delivery with the exact
// reserved answer and its original deadline, and a later explicit retry of
// the same respond re-runs the whole path: commitPendingAnswer treats the
// same answer text as idempotent, the transcript copy keeps its once
// identity, and only a successful Mutate is ever followed by the applied
// record.
func (dt *delegateToolExecuteRespond) finishPendingResume() error {
	if err := dt.t.lifecycle.Mutate(dt.sessionID, func(cur *session.LifecycleRecord) error {
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
		return nil
	}); err != nil {
		return err
	}
	return dt.applyPendingQuestion()
}

// applyPendingQuestion appends the applied record once the receipt (the
// transcript copy, verified by read-back earlier in the respond path) and
// the durable lifecycle resume effect above both hold. It takes the
// per-session lifecycle lock itself (the Mutate has released it;
// QuestionStore methods hold no lock of their own). A concurrent respond
// that reserved the same answer between the two steps still converges here:
// applied is idempotent, and any other status is a visible error, never an
// overwrite.
func (dt *delegateToolExecuteRespond) applyPendingQuestion() error {
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
}

func (dt *delegateToolExecuteRespond) acknowledgedRespond() *ToolResult {
	return NewToolResult("Answer delivered; session resumed.")
}
