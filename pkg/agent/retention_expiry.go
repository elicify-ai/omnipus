package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// DispatchRetentionNotices uses the ordinary saved-input/wake path. Neither a
// live frame nor queue publication acknowledges the durable notice outbox.
func (al *AgentLoop) DispatchRetentionNotices(ctx context.Context, emit func(session.RetentionNotice) error) error {
	store := al.GetSessionStore()
	if store == nil {
		return nil
	}
	notices, failures := store.PendingRetentionNotices(ctx, "")
	for _, notice := range notices {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		if notice.ChatPresent && emit != nil {
			if err := emit(notice); err != nil {
				failures = errors.Join(failures, fmt.Errorf("retention notice %s: live publication: %w", notice.ReceiptID, err))
				continue
			}
		}
		if _, err := al.WakeSession(ctx, notice.SessionID, "creator_notice", notice.OutcomeID, notice.Body); err != nil {
			failures = errors.Join(failures, fmt.Errorf("retention notice %s: parent wake: %w", notice.ReceiptID, err))
		}
	}
	return failures
}

// finishExpiredSavedWake retires only the addressed, positively expired original.
// It does not claim delivery, alter child work state, or revive a stopped parent.
func (al *AgentLoop) finishExpiredSavedWake(ctx context.Context, sessionID, outcomeID string, cause error) (bool, error) {
	var expired *session.SavedInputExpiry
	if !errors.As(cause, &expired) || expired.SessionID != sessionID || expired.InputID != "saved-input-"+outcomeID {
		return false, nil
	}
	al.releaseWakePublication(sessionID, outcomeID)
	err := al.ackConsumedSteeringWake(steeringWake{messageID: outcomeID, transcriptSessionID: sessionID})
	if store := al.GetSessionStore(); store != nil {
		err = errors.Join(err, store.RecoverSavedInputLifecycle(ctx, sessionID))
	}
	if err != nil {
		return true, errors.Join(cause, err)
	}
	return true, nil
}

func (al *AgentLoop) finishKnownSavedWake(ctx context.Context, wake steeringWake) (bool, error) {
	if al.GetSessionStore() == nil {
		return false, nil
	}
	consumed, err := al.wakeInputConsumed(wake.transcriptSessionID, wake.messageID)
	if expired, finishErr := al.finishExpiredSavedWake(ctx, wake.transcriptSessionID, wake.messageID, err); expired {
		return true, finishErr
	}
	if err != nil {
		return false, err
	}
	if consumed {
		al.releaseWakePublication(wake.transcriptSessionID, wake.messageID)
		if _, err := al.writeSteeringConsumedMarker(wake); err != nil {
			return true, err
		}
		return true, al.ackConsumedSteeringWake(wake)
	}
	return false, nil
}

// savedFinalDisposition is non-consuming for valid messages, including an
// ancestor's authorized reads. Only a known expired original is acknowledged.
func (al *AgentLoop) savedFinalDisposition(parentID, messageID string) error {
	store := al.GetSessionStore()
	if store == nil {
		return fmt.Errorf("saved final disposition: session store unavailable")
	}
	_, err := store.SavedInputConsumed(parentID, "saved-input-"+messageID)
	if errors.Is(err, session.ErrSavedInputExpired) {
		_, finishErr := al.finishExpiredSavedWake(context.Background(), parentID, messageID, err)
		return errors.Join(err, finishErr)
	}
	return err
}
