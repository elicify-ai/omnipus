package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// completeSteeredTurn applies the goal-less completion disposition after a
// real turn exits. Delivery is deliberately first; boot recovery can repair a
// delivered-but-not-terminal record, while terminal-first could lose the only
// copy of the child's result.
func (al *AgentLoop) completeSteeredTurn(ctx context.Context, snapshot *session.LifecycleRecord, result turnResult, runErr error) error {
	if al == nil || snapshot == nil || snapshot.SteeredBy == nil {
		return nil
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return errors.New("steer: complete: lifecycle store is not wired")
	}
	rec, err := lifecycle.Load(snapshot.SessionID)
	if err != nil {
		return fmt.Errorf("steer: complete: load %q: %w", snapshot.SessionID, err)
	}
	if rec.Generation != snapshot.Generation || rec.Terminal() || rec.State == session.LifecycleNeedsInput {
		return nil
	}
	// A goal-bearing session is completed only by the claim/Judge loop.
	if rec.GoalRef != "" {
		return nil
	}

	answer := strings.TrimSpace(result.finalContent)
	outcome, nextState, failureReason := completionDisposition(result, runErr, answer)
	if outcome == "" {
		if answer == "" {
			outcome = steer.OutcomeEmptyAnswer
			nextState = session.LifecycleFailed
			failureReason = "empty_answer: the session produced no final answer"
		} else {
			blocked, blockErr := al.hasRunningOrQueuedDescendant(rec.SessionID)
			if blockErr != nil {
				return blockErr
			}
			if blocked {
				// The answer is already durable in the child's transcript. Keep the
				// lifecycle running until the last executing descendant completes.
				return nil
			}
			outcome = steer.OutcomeFinalAnswer
			nextState = session.LifecycleCompleted
		}
	}

	message, err := al.completionMessage(rec, outcome, answer, failureReason)
	if err != nil {
		return err
	}
	deliverer := al.getUpwardDeliverer()
	if deliverer == nil {
		return errSteerUpwardDelivererNotWired
	}
	if _, err := deliverer.Deliver(ctx, steer.UpwardEvent{
		ChildSessionID: rec.SessionID,
		Outcome:        outcome,
		Message:        message,
	}); err != nil {
		return fmt.Errorf("steer: complete: deliver %q: %w", rec.SessionID, err)
	}

	rec.State = nextState
	rec.NeedsInput = nil
	if nextState == session.LifecycleFailed {
		rec.FailedReason = failureReason
	}
	if err := lifecycle.Persist(rec); err != nil {
		return fmt.Errorf("steer: complete: persist %q: %w", rec.SessionID, err)
	}
	al.completeWaitingAncestors(ctx, rec.SteeringSessionID())
	return nil
}

// finishSteeredGoalTurn sends a goal-bearing child through the same
// session-owned claim/Judge pipeline used by an interactive goal turn. The
// Judge dispatch remains asynchronous, matching runAgentLoop's ordering.
func (al *AgentLoop) finishSteeredGoalTurn(ts *turnState, result *turnResult, runErr error) {
	if al == nil || ts == nil || result == nil || runErr != nil {
		return
	}
	al.checkGoalLoopAfterTurn(context.Background(), ts.agent, ts.opts, result)
	for _, followUp := range result.followUps {
		if err := al.bus.PublishInbound(context.Background(), followUp); err != nil {
			logger.WarnCF("agent", "steer: publish goal follow-up failed",
				map[string]any{"session_id": ts.opts.TranscriptSessionID, "error": err.Error()})
		}
	}
	if result.goalDeferredAdjudication != nil {
		work := result.goalDeferredAdjudication
		go al.dispatchDeferredGoalAdjudication(work)
	}
}

func completionDisposition(result turnResult, runErr error, answer string) (steer.Outcome, session.LifecycleState, string) {
	switch {
	case errors.Is(runErr, context.DeadlineExceeded):
		return steer.OutcomeTimedOut, session.LifecycleTimedOut, "timed_out: the session exceeded its lifetime limit"
	case errors.Is(runErr, context.Canceled), result.status == TurnEndStatusAborted:
		return steer.OutcomeInterrupted, session.LifecycleCancelled, "interrupted: the session was cancelled"
	case runErr != nil:
		return steer.OutcomeFailed, session.LifecycleFailed, "failed: " + runErr.Error()
	case result.turnFailed || result.status == TurnEndStatusError:
		return steer.OutcomeFailed, session.LifecycleFailed, "failed: the turn ended with an error"
	case result.status == TurnEndStatusParked:
		return "", "", ""
	default:
		return "", "", ""
	}
}

func (al *AgentLoop) completionMessage(rec *session.LifecycleRecord, outcome steer.Outcome, answer, failureReason string) (generated.SessionMessage, error) {
	now := time.Now().UTC()
	var message generated.SessionMessage
	if outcome == steer.OutcomeFinalAnswer {
		questions, err := al.parkedQuestions(rec.SessionID)
		if err != nil {
			return message, fmt.Errorf("steer: complete: collect parked questions: %w", err)
		}
		err = message.FromSessionMessageHandback(generated.SessionMessageHandback{
			MessageId:      rec.SessionID,
			SessionId:      rec.SessionID,
			CreatedAt:      now,
			Depth:          1,
			SenderIdentity: rec.AgentID,
			Mode:           generated.SessionMessageHandbackModeFinal,
			ResultSoFar:    answer,
			Artifacts:      []string{},
			OpenQuestions:  questions,
		})
		return message, err
	}
	if failureReason == "" {
		failureReason = string(outcome) + ": the session did not complete"
	}
	err := message.FromSessionMessageError(generated.SessionMessageError{
		MessageId:      rec.SessionID,
		SessionId:      rec.SessionID,
		CreatedAt:      now,
		Depth:          1,
		SenderIdentity: rec.AgentID,
		Fatal:          true,
		Text:           failureReason,
	})
	return message, err
}

func (al *AgentLoop) hasRunningOrQueuedDescendant(parentID string) (bool, error) {
	lifecycle := al.GetSessionLifecycleStore()
	seen := map[string]bool{parentID: true}
	queue := []string{parentID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children, err := lifecycle.List(session.LifecycleFilter{SteeringSessionID: current})
		if err != nil {
			return false, fmt.Errorf("steer: complete: list descendants of %q: %w", current, err)
		}
		for i := range children {
			child := children[i]
			if seen[child.SessionID] {
				continue
			}
			seen[child.SessionID] = true
			if child.State == session.LifecycleQueued || child.State == session.LifecycleRunning {
				return true, nil
			}
			queue = append(queue, child.SessionID)
		}
	}
	return false, nil
}

func (al *AgentLoop) parkedQuestions(ownerID string) ([]string, error) {
	inbox := al.GetMessageInboxStore()
	if inbox == nil {
		return nil, errors.New("message inbox store is not wired")
	}
	messages, cursor, more, err := inbox.Drain(ownerID, "", "", 256)
	if err != nil {
		return nil, err
	}
	for more {
		var page []generated.SessionMessage
		page, cursor, more, err = inbox.Drain(ownerID, "", cursor, 256)
		if err != nil {
			return nil, err
		}
		messages = append(messages, page...)
	}
	questions := make([]string, 0)
	for _, message := range messages {
		kind, err := message.Discriminator()
		if err != nil || kind != "question" {
			continue
		}
		question, err := message.AsSessionMessageQuestion()
		if err == nil && strings.TrimSpace(question.Text) != "" {
			questions = append(questions, question.Text)
		}
	}
	return questions, nil
}

func (al *AgentLoop) completeWaitingAncestors(ctx context.Context, parentID string) {
	for parentID != "" {
		rec, err := al.GetSessionLifecycleStore().Load(parentID)
		if err != nil || rec.SteeredBy == nil || rec.GoalRef != "" || rec.State != session.LifecycleRunning {
			return
		}
		if active := al.getActiveTurnState(parentID); active != nil && active.IsAlive() {
			return
		}
		blocked, err := al.hasRunningOrQueuedDescendant(parentID)
		if err != nil || blocked {
			return
		}
		answer, err := al.lastAssistantAnswer(parentID)
		if err != nil || answer == "" {
			return
		}
		if err := al.completeSteeredTurn(ctx, rec, turnResult{finalContent: answer}, nil); err != nil {
			logger.WarnCF("agent", "steer: complete waiting ancestor failed",
				map[string]any{"session_id": parentID, "error": err.Error()})
		}
		return // completeSteeredTurn recursively continues farther upward.
	}
}

func (al *AgentLoop) lastAssistantAnswer(sessionID string) (string, error) {
	entries, err := al.GetSessionStore().ReadTranscript(sessionID)
	if err != nil {
		return "", err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == "assistant" && strings.TrimSpace(entries[i].Content) != "" {
			return strings.TrimSpace(entries[i].Content), nil
		}
	}
	return "", nil
}
