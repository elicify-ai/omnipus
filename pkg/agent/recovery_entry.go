package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
		history := ts.agent.Sessions.GetHistory(ts.contextKey())
		if len(findOrphanedToolCalls(history)) > 0 {
			return nil, fmt.Errorf("reconciliation_required: recovery execution identity is unavailable")
		}
		return history, nil
	}
	rec, err := lc.Load(id)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return ts.agent.Sessions.GetHistory(ts.contextKey()), nil
	}
	if err != nil {
		return nil, err
	}
	return al.recoverSessionToolCalls(ctx, ts.agent, rec, ts.contextKey())
}

func (al *AgentLoop) recoveryInterruptedResult(ts *turnState, cause error) (turnResult, error) {
	ts.markInterrupted(session.InterruptionReconciliationRequired)
	slog.Warn("recovery: automatic continuation refused", "session_id", ts.transcriptSessionID, "error", cause)
	text := recoveryInterruptionNotice(ts.transcriptSessionID)
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
	slog.Warn("recovery: helper continuation refused", "session_id", rec.SessionID, "error", err)
	commitErr := al.reportSteeredSessionTerminalUpward(ctx, rec.SessionID, rec.Generation, session.LifecycleInterrupted, steer.OutcomeReconciliationRequired, recoveryInterruptionNotice(rec.SessionID))
	fresh, loadErr := al.GetSessionLifecycleStore().Load(rec.SessionID)
	if loadErr != nil || fresh.State != session.LifecycleInterrupted || fresh.Generation != rec.Generation ||
		fresh.ExecutionID == nil || rec.ExecutionID == nil || *fresh.ExecutionID != *rec.ExecutionID {
		return errors.Join(err, commitErr, loadErr)
	}
	// A failed publication can follow a successful terminal commit. Still save
	// the local report and leave the committed parent notice pending for replay.
	if reportErr := al.ensureRecoveryInterruptionReport(fresh); reportErr != nil {
		return errors.Join(err, commitErr, reportErr)
	}
	return fmt.Errorf("%w: %w", errRecoveryReconciliationHeld, errors.Join(err, commitErr))
}

func (al *AgentLoop) ensureRecoveryInterruptionReport(rec *session.LifecycleRecord) error {
	if rec.ExecutionID == nil || al.GetSessionStore() == nil {
		return fmt.Errorf("recovery: interruption report identity or store is missing")
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("reconciliation-report:"+rec.SessionID+":"+rec.ExecutionID.RunID)).String()
	entry := session.TranscriptEntry{ID: id, Type: session.EntryTypeSystem, Role: "system",
		Content: recoveryInterruptionNotice(rec.SessionID), Timestamp: rec.UpdatedAt,
		AgentID: rec.AgentID, Status: "interrupted", InterruptionReason: string(session.InterruptionReconciliationRequired)}
	if _, err := al.GetSessionStore().AppendTranscriptOnce(rec.SessionID, entry); err != nil {
		return err
	}
	return markRecoveryReported(rec.SessionID)
}

var errRecoveryReconciliationHeld = errors.New("recovery: unresolved effects reported; execution is Interrupted")
