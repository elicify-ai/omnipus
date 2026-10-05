package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func (rc *agentLoopRequestCancel) prepareSelectedStopDisposition() (CancelOutcome, error, bool) {
	if !rc.scope.TurnOnly || rc.scope.Generation == 0 {
		return CancelOutcome{}, nil, false
	}
	d, current, err := rc.al.retainSelectedStop(rc.ctx, rc.sessionID, rc.scope.Generation, rc.hooks.OnStopSettled)
	if err != nil {
		return CancelOutcome{}, err, true
	}
	if !current {
		return CancelOutcome{SkippedNewerGeneration: true}, nil, true
	}
	if d == nil || rc.al.activeTurnForCancel(rc.sessionID, rc.scope) == nil {
		rc.al.cancelPendingAskForScope(rc.sessionID)
		if rc.hooks.CancelPendingApprovals != nil {
			rc.hooks.CancelPendingApprovals(rc.sessionID, "session canceled")
		}
		// A retained outer producer will settle; a missing owner was proven
		// disposed and settled synchronously. Neither is an unbound latch.
		return CancelOutcome{Fired: true}, nil, true
	}
	return CancelOutcome{}, nil, false
}

// reportStopSettlementFailure uses existing nonfatal error/event/message
// transports. It never commits a failed outcome, final id, or resume.
func (al *AgentLoop) reportStopSettlementFailure(selected session.StopSelection, failure error, emitFrame bool) error {
	logger.ErrorCF("agent", "selected Stop settlement failed",
		map[string]any{"session_id": selected.SessionID, "control_id": selected.Effect.ControlID, "error": failure.Error()})
	text := fmt.Sprintf("Stop for session %s could not finish; required storage or notice publication failed. Retry after storage is repaired.", selected.SessionID)
	if emitFrame {
		al.emitEvent(EventKindError, EventMeta{SessionKey: selected.SessionID, TracePath: "stop.settlement"}, ErrorPayload{
			Stage: "stop", Code: "stop_pending", Message: text, SessionID: selected.SessionID,
		})
	}
	rec, readErr := al.GetSessionLifecycleStore().Load(selected.SessionID)
	if readErr != nil {
		return readErr
	}
	// Only the selected execution can send this runtime error through its
	// current direct-parent edge. Obsolete effects do not notify as newer work.
	if rec.ExecutionID != nil && !claimForStopEffect(selected.SessionID, selected.Effect).matches(rec) {
		return nil
	}
	var errs []error
	if rec.SteeredBy != nil {
		errs = append(errs, al.deliverSteeredNotice(context.Background(), rec, steer.OutcomeLifecycleNotice, "", text))
	} else if rec.OriginChannel != "" && rec.OriginChannel != "webchat" && rec.OriginChatID != "" {
		errs = append(errs, al.bus.PublishOutbound(context.Background(), bus.OutboundMessage{
			Channel: rec.OriginChannel, ChatID: rec.OriginChatID, SessionID: rec.SessionID, Content: text,
		}))
	}
	return errors.Join(errs...)
}
