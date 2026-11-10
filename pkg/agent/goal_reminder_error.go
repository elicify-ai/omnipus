package agent

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func withOrdinaryGoalPreTurn(opts processOptions, err error) error {
	if err == nil || opts.goalWake == nil {
		return err
	}
	return markOrdinaryGoalPreTurn(err)
}

// ordinaryGoalPreTurnError marks a goal-wake failure that returned before the
// ordinary turn's own error reporting. Its text is the cause, so settlement
// still names the real operation. Obsolete and held wakes are not this type.
type ordinaryGoalPreTurnError struct{ cause error }

func (e *ordinaryGoalPreTurnError) Error() string {
	if e == nil || e.cause == nil {
		return "goal reminder: automatic work could not start"
	}
	return e.cause.Error()
}

func (e *ordinaryGoalPreTurnError) Unwrap() error { return e.cause }

// ordinaryGoalPreTurnHeld recognizes ordinary refusal/cancellation, not a
// storage or identity-preparation failure. Curated refusals at this boundary
// are the existing in-flight Stop-fence response; no turn/provider has started.
func ordinaryGoalPreTurnHeld(err error) bool {
	if errors.Is(err, errOrdinaryGoalWakeIgnored) || errors.Is(err, ErrPreviousExecutionPending) ||
		errors.Is(err, steer.ErrStaleGeneration) || errors.Is(err, steer.ErrDispatchCancelled) ||
		errors.Is(err, steer.ErrTerminal) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var refusal *curatedTurnError
	return errors.As(err, &refusal)
}

func markOrdinaryGoalPreTurn(err error) error {
	if err == nil || ordinaryGoalPreTurnHeld(err) {
		return err
	}
	var already *ordinaryGoalPreTurnError
	if errors.As(err, &already) {
		return err
	}
	return &ordinaryGoalPreTurnError{cause: err}
}

// goalReminderChatSaveError is the receipt append failure. The text keeps the
// existing operation name so a failed execution still identifies the archive.
type goalReminderChatSaveError struct{ cause error }

func (e *goalReminderChatSaveError) Error() string {
	if e == nil || e.cause == nil {
		return "goal reminder: save chat event"
	}
	return "goal reminder: save chat event: " + e.cause.Error()
}

func (e *goalReminderChatSaveError) Unwrap() error { return e.cause }

const ordinaryGoalPreTurnSaveMessage = "Automatic goal work could not start because its chat event could not be saved. Check disk space and file permissions, then reply to continue."

const ordinaryGoalPreTurnStartMessage = "Automatic goal work could not start. Check the server logs for the cause, then reply to continue."

func (al *AgentLoop) failOrdinaryGoalPreTurn(opts processOptions, err error) (string, error) {
	al.surfaceOrdinaryGoalPreTurnError(opts.TranscriptSessionID, opts.goalWake, err)
	if opts.goalWake != nil {
		al.goalMarkIdleSettling(opts.goalWake.goalID, false)
	}
	return "", err
}

// surfaceOrdinaryGoalPreTurnError shows an unexpected failure that happened
// before the turn's own reporting. The live error does not depend on the chat
// archive; a durable copy is best-effort and may fail when that archive is the
// thing that broke. Routine obsolete or held wakes are not errors.
func (al *AgentLoop) surfaceOrdinaryGoalPreTurnError(sessionID string, wake *ordinaryGoalWake, err error) {
	if err == nil || sessionID == "" || errors.Is(err, errOrdinaryGoalWakeIgnored) {
		return
	}
	message := ordinaryGoalPreTurnStartMessage
	operation := "start"
	var save *goalReminderChatSaveError
	if errors.As(err, &save) {
		message = ordinaryGoalPreTurnSaveMessage
		operation = "save_chat_event"
	}
	goalID, messageID := "", ""
	if wake != nil {
		goalID, messageID = wake.goalID, wake.messageID
	}
	logger.ErrorCF("agent", "automatic goal reminder could not start", map[string]any{
		"session_id": sessionID,
		"goal_id":    goalID,
		"message_id": messageID,
		"operation":  operation,
		"error":      err.Error(),
	})
	if al.audienceFor(context.Background(), steer.BoundaryTypedErrorFrame, sessionID) != steer.AudienceNone {
		al.emitEvent(EventKindError, EventMeta{Source: "goal_loop", SessionKey: sessionID}, ErrorPayload{
			Stage: "goal_reminder_admission", Code: string(CodeUnknown), Message: message, SessionID: sessionID,
		})
	}
	if store := al.goalSessionStoreFor(sessionID); store != nil {
		al.writeGoalSystemTranscript(store, sessionID, "", message)
	}
}
