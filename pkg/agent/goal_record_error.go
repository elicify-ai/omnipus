// goal_record_error.go makes an unreadable goal state visible at asynchronous
// boundaries that cannot return the read error to their caller.

package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// goalRecordReadError distinguishes a goal-store read refusal from a provider
// failure, so a task run pauses rather than spending an execution attempt.
// Unwrap preserves the concrete filesystem cause for callers.
type goalRecordReadError struct {
	cause error
}

func (err *goalRecordReadError) Error() string { return err.cause.Error() }
func (err *goalRecordReadError) Unwrap() error { return err.cause }

// reportGoalReadError accompanies a conservative refusal, never a goal-less
// fallback. Keep the cause in the session transcript as well as the live error
// surface: an unattended operation must remain explainable after reconnect.
func (al *AgentLoop) reportGoalReadError(sessionID, operation string, err error) string {
	message := fmt.Sprintf("Goal state could not be read; %s deferred: %v", operation, err)
	logger.ErrorCF("agent", "goal_read_error: "+message,
		map[string]any{"component": "goal", "session_id": sessionID, "operation": operation, "error": err.Error()})
	if store := al.goalSessionStoreFor(sessionID); store != nil {
		al.writeGoalSystemTranscript(store, sessionID, "", message)
	}
	if al.audienceFor(context.Background(), steer.BoundaryTypedErrorFrame, sessionID) != steer.AudienceNone {
		al.emitEvent(EventKindError, EventMeta{Source: "goal_loop", SessionKey: sessionID}, ErrorPayload{
			Stage: "goal_record_read", Code: string(CodeUnknown), Message: message, SessionID: sessionID,
		})
	}
	return message
}

// refuseUnreadGoalState stops before the provider call, rather than offer an
// unrestricted tool surface based on an unread goal predicate.
func (rq *agentLoopRunTurnRequest) refuseUnreadGoalState(err error) agentLoopRunTurnRequestFlow {
	message := rq.ri.rf.rt.al.reportGoalReadError(rq.ri.rf.rt.ts.opts.TranscriptSessionID, "provider request", err)
	rq.ri.turnStatus = TurnEndStatusError
	rq.ret0 = turnResult{status: TurnEndStatusError, finalContent: message}
	rq.ret1 = fmt.Errorf("goal: provider request deferred: %w", err)
	return agentLoopRunTurnRequestReturn
}
