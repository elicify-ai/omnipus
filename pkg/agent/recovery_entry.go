package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/google/uuid"
)

func (al *AgentLoop) prepareTurnRecovery(ctx context.Context, ts *turnState) ([]providers.Message, error) {
	id := ts.transcriptSessionID
	if id == "" {
		id = ts.sessionKey
	}
	lc := al.GetSessionLifecycleStore()
	if lc == nil {
		history := ts.agent.Sessions.GetHistory(ts.sessionKey)
		if len(findOrphanedToolCalls(history)) > 0 {
			return nil, fmt.Errorf("reconciliation_required: recovery execution identity is unavailable")
		}
		return history, nil
	}
	rec, err := lc.Load(id)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return ts.agent.Sessions.GetHistory(ts.sessionKey), nil
	}
	if err != nil {
		return nil, err
	}
	return al.recoverSessionToolCalls(ctx, ts.agent, rec, ts.sessionKey)
}

func (al *AgentLoop) recoveryInterruptedResult(ts *turnState, cause error) (turnResult, error) {
	ts.markInterrupted(session.InterruptionReconciliationRequired)
	text := "reconciliation_required: " + cause.Error()
	ts.setFinalContent(text)
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return turnResult{}, fmt.Errorf("recovery: interruption report store is missing")
	}
	entry := session.TranscriptEntry{ID: uuid.NewString(), Role: "assistant", Content: text, Timestamp: time.Now().UTC(), AgentID: ts.agentID, TurnID: ts.turnID, Status: "interrupted", InterruptionReason: string(session.InterruptionReconciliationRequired)}
	if _, err := ts.transcriptStore.AppendTranscriptOnce(ts.transcriptSessionID, entry); err != nil {
		return turnResult{}, err
	}
	if err := markRecoveryReported(ts.transcriptSessionID); err != nil {
		return turnResult{}, err
	}
	return turnResult{finalContent: text, interruptionReason: session.InterruptionReconciliationRequired, turnID: ts.turnID}, nil
}

func (al *AgentLoop) reconcileBootHelper(ctx context.Context, rec *session.LifecycleRecord) error {
	inst, ok := al.GetRegistry().GetAgent(rec.AgentID)
	if !ok {
		return fmt.Errorf("tool recovery: helper agent is unavailable")
	}
	_, err := al.recoverSessionToolCalls(ctx, inst, rec, rec.SessionID)
	if err == nil {
		return nil
	}
	if held, readErr := loadRecoveryHold(rec.SessionID); readErr != nil || len(held.Occurrences) == 0 {
		return errors.Join(err, readErr)
	}
	// The selected old execution owns the same completion/outbox commit as a real turn.
	if commitErr := al.reportSteeredSessionTerminalUpward(ctx, rec.SessionID, rec.Generation, session.LifecycleInterrupted, steer.OutcomeReconciliationRequired, err.Error()); commitErr != nil {
		return errors.Join(err, commitErr)
	}
	if rec.ExecutionID == nil {
		return fmt.Errorf("recovery: producing execution is missing")
	}
	entry := session.TranscriptEntry{ID: "reconciliation-required:" + rec.ExecutionID.RunID,
		Type: session.EntryTypeSystem, Role: "system", Content: err.Error(), Timestamp: time.Now().UTC(),
		AgentID: rec.AgentID, Status: "interrupted", InterruptionReason: string(session.InterruptionReconciliationRequired)}
	if _, appendErr := al.GetSessionStore().AppendTranscriptOnce(rec.SessionID, entry); appendErr != nil {
		return errors.Join(err, appendErr)
	}
	if reportErr := markRecoveryReported(rec.SessionID); reportErr != nil {
		return errors.Join(err, reportErr)
	}
	return fmt.Errorf("%w: %v", errRecoveryReconciliationHeld, err)
}

var errRecoveryReconciliationHeld = errors.New("recovery: unresolved effects reported; execution is Interrupted")
