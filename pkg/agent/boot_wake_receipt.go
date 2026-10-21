package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// bootWakeHandled checks positive durable receipts, not merely inbox absence.
// A held input stays unacknowledged for the next legitimate turn. An unblocked
// pending input is NOT handled: publication may have failed before the crash.
func (r *SteerBootRecovery) bootWakeHandled(parentID, outcomeID string) (handled, consumed bool, err error) {
	entries, err := r.Inbox.Entries(parentID)
	if err != nil {
		return false, false, err
	}
	var appended, acked bool
	for _, entry := range entries {
		if entry.Kind == session.InboxEntryMessage && entry.Message != nil && messageIDOf(*entry.Message) == outcomeID {
			appended = true
		}
		if entry.Kind == session.InboxEntryAck && slices.Contains(entry.AckedIDs, outcomeID) {
			acked = true
		}
	}
	if appended && acked {
		return true, true, nil
	}
	deliverer, hasRuntime := r.Deliverer.(*SteerUpwardDeliverer)
	if !hasRuntime || deliverer == nil || deliverer.agentLoop == nil {
		// A non-runtime deliverer cannot supply saved-input receipts. Leave
		// delivery to it rather than infer consumption from their absence.
		return false, false, nil
	}
	al := deliverer.agentLoop
	addr, placed, err := r.Sessions.SavedChatInput(parentID, "saved-input-"+outcomeID)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil // real delivery surfaces the unavailable parent
	}
	if err != nil || addr.EntryID == "" {
		return false, false, err
	}
	consumed, err = al.wakeInputConsumed(parentID, outcomeID)
	if err != nil {
		return false, false, err
	}
	if consumed {
		if !appended && !placed {
			return false, false, fmt.Errorf("saved outcome %s has only a consumed marker but its inbox message is missing", outcomeID)
		}
		// Placement is durable positive evidence after inbox compaction too.
		// Do not append an unknown-id ACK for a message compaction removed.
		if appended {
			if err = r.Inbox.Ack(parentID, []string{outcomeID}); err != nil {
				return false, false, err
			}
		}
		if err = r.Sessions.RetireSavedInput(parentID, "saved-input-"+outcomeID); err != nil {
			return false, false, err
		}
		return true, true, nil
	}
	if !appended {
		// A saved input is not a receipt for a missing inbox append. Restore
		// delivery rather than using a Stop/question hold to hide that gap.
		return false, false, nil
	}
	rec, err := r.Lifecycle.Load(parentID)
	if err != nil {
		return false, false, err
	}
	if rec.State == session.LifecycleStopped && rec.StopNote != nil && rec.StopNote.Cause == session.StopCauseRestart {
		// The wake entry, not this receipt check, authenticates PW-D14's
		// fresh-helper restart revival. Never short-circuit that decision.
		return false, false, nil
	}
	blocked, err := al.wakeBlocked(rec)
	if err != nil {
		return false, false, err
	}
	if !blocked {
		blocked, err = al.openQuestionCard(parentID)
	}
	if err != nil || !blocked {
		return false, false, err
	}
	// Archive presence is not pending-index durability. An interrupted save
	// can leave only the original bytes; retry that exact save before holding
	// the input, so the next legitimate turn can discover it. This repairs
	// storage only: no wake, placement or acknowledgement happens here.
	body, found, err := al.savedHelperInput(parentID, outcomeID)
	if err != nil {
		return false, false, err
	}
	if !found {
		return false, false, fmt.Errorf("held outcome %s has no readable saved input", outcomeID)
	}
	if _, err = r.Sessions.SaveChatInput(parentID, "saved-input-"+outcomeID, rec.AgentID, body); err != nil {
		return false, false, fmt.Errorf("held outcome %s pending-input repair: %w", outcomeID, err)
	}
	return true, false, nil
}
