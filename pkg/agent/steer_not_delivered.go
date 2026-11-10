// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// steer_not_delivered.go: the recorder behind a refused helper report
// (FR-013 / BDD-04.3, #1211 D2). When the parent's inbox refuses a child's
// report at a cap, the report is NOT saved; this file makes that loss visible
// to the parent on every read path: LifecycleRecord.NotDelivered (load-bearing,
// read by delegate inbox/status) and a best-effort server-authored
// subagent_message frame of kind not_delivered (read by the Activity panel).
// Refused content is never stored anywhere.

package agent

import (
	"fmt"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// notDeliveredFrameKind is the SubagentMessageFrame kind for a refused report.
const notDeliveredFrameKind = "not_delivered"

// notDeliveredReasonPhrase is the human-readable reason in the frame text.
func notDeliveredReasonPhrase(reason generated.DelegateNotDeliveredSummaryLastReason) string {
	switch reason {
	case generated.DelegateNotDeliveredSummaryLastReasonRateLimited:
		return "the report rate limit was reached"
	case generated.DelegateNotDeliveredSummaryLastReasonBodyTooLarge:
		return "the report was too large"
	case generated.DelegateNotDeliveredSummaryLastReasonQuestionBlockerCeiling:
		return "too many questions or blockers are still open"
	case generated.DelegateNotDeliveredSummaryLastReasonUnackedCap:
		return "the parent inbox is full"
	}
	return string(reason)
}

// recordNotDelivered handles a failed Append in publishUpward. For anything
// other than one of the four cap refusals it returns appendErr unchanged
// (wrapped as the deliverer always does). For a cap refusal it records the
// summary on the child's lifecycle record, emits the best-effort frame, and
// returns the refusal still matching errors.Is; when the record write fails the
// returned error also says the parent could not be told.
func (al *AgentLoop) recordNotDelivered(
	lifecycle *session.LifecycleStore,
	ownerKey string,
	childRec *session.LifecycleRecord,
	kind string,
	appendErr error,
) error {
	wrapped := fmt.Errorf("steer: deliver: append: %w", appendErr)
	reason, isCap := session.InboxRefusalReason(appendErr)
	if !isCap {
		return wrapped
	}
	now := time.Now().UTC()
	if recErr := lifecycle.Mutate(childRec.SessionID, func(rec *session.LifecycleRecord) error {
		if rec == nil {
			return session.ErrLifecycleNotFound
		}
		next := generated.DelegateNotDeliveredSummary{}
		if rec.NotDelivered != nil {
			next = *rec.NotDelivered
		}
		next.Count++
		next.LastReason = reason
		next.LastKind = kind
		next.LastAt = now
		rec.NotDelivered = &next
		return nil
	}); recErr != nil {
		logger.ErrorCF("agent", "steer: a refused report could not be recorded on the child; the parent is not notified",
			map[string]any{"session_id": childRec.SessionID, "reason": string(reason), "error": recErr.Error()})
		return fmt.Errorf("steer: deliver: append: %w; %s: %w", appendErr, session.ParentNotNotifiedMarker, recErr)
	}
	al.deliverSubagentMessageFrame(ownerKey, childRec, notDeliveredFrameKind,
		fmt.Sprintf("%s report not delivered: %s", kind, notDeliveredReasonPhrase(reason)), nil, false)
	return wrapped
}
