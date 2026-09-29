package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// schedulePostFinishWakes hands each accepted wake to a tracked consumer, not
// just an operator log. The parent already has the descendant's durable
// message; check its identity before losing the finishing-window copy.
func (al *AgentLoop) schedulePostFinishWakes(rec *session.LifecycleRecord, wakes []steeringQueueItem) error {
	if len(wakes) == 0 {
		return nil
	}
	inbox := al.GetMessageInboxStore()
	if inbox == nil {
		return errors.New("steer: post-finish wake: durable inbox is not wired")
	}
	entries, err := inbox.Entries(rec.SessionID)
	if err != nil {
		return fmt.Errorf("steer: post-finish wake: inspect inbox %q: %w", rec.SessionID, err)
	}
	var pending []steeringQueueItem
	var invalid []error
	for _, item := range wakes {
		id := item.wake.messageID
		if id == "" || item.wake.transcriptSessionID != rec.SessionID {
			invalid = append(invalid, fmt.Errorf("steer: post-finish wake %q: invalid inbox owner %q (want %q)",
				id, item.wake.transcriptSessionID, rec.SessionID))
			continue
		}
		found := false
		for _, entry := range entries {
			if entry.Kind == session.InboxEntryMessage && entry.Message != nil && messageIDOf(*entry.Message) == id {
				found = true
				break
			}
		}
		if !found {
			invalid = append(invalid, fmt.Errorf("steer: post-finish wake %q: no durable inbox entry under %q", id, rec.SessionID))
			continue
		}
		acked, ackErr := deliverEntryIsAcked(inbox, rec.SessionID, "", id)
		if ackErr != nil {
			invalid = append(invalid, fmt.Errorf("steer: post-finish wake %q: inspect acknowledgement: %w", id, ackErr))
			continue
		}
		if !acked {
			pending = append(pending, item)
		}
	}
	if len(pending) > 0 {
		al.goSteeredTurn(func() {
			for _, item := range pending {
				if wakeErr := al.replayPostFinishWake(rec.SessionID, item.wake.messageID); wakeErr != nil {
					text := fmt.Sprintf("Descendant wake %s could not be consumed: %v", item.wake.messageID, wakeErr)
					logger.ErrorCF("agent", "steer: post-finish durable wake replay failed",
						map[string]any{"session_id": rec.SessionID, "message_id": item.wake.messageID, "error": wakeErr.Error()})
					al.deliverSubagentMessage(rec.SteeringSessionID(), rec, "error", text, nil)
				}
			}
		})
		logger.InfoCF("agent", "steer: post-finish durable inbox replay scheduled",
			map[string]any{"session_id": rec.SessionID, "pending_wakes": len(pending)})
	}
	return errors.Join(invalid...)
}

// replayPostFinishWake uses the message in the durable inbox, not the
// discarded in-memory copy. A terminal recipient must be revived before the
// existing wake consumer can admit a turn and acknowledge the message ID.
func (al *AgentLoop) replayPostFinishWake(sessionID, messageID string) error {
	inbox := al.GetMessageInboxStore()
	lifecycle := al.GetSessionLifecycleStore()
	if inbox == nil || lifecycle == nil {
		return errors.New("steer: post-finish wake: messaging stores are not wired")
	}
	entries, err := inbox.Entries(sessionID)
	if err != nil {
		return fmt.Errorf("steer: post-finish wake: inspect inbox %q: %w", sessionID, err)
	}
	var durable *generated.SessionMessage
	for _, entry := range entries {
		if entry.Kind == session.InboxEntryMessage && entry.Message != nil && messageIDOf(*entry.Message) == messageID {
			durable = entry.Message
			break
		}
	}
	if durable == nil {
		return fmt.Errorf("steer: post-finish wake %q: durable inbox entry missing under %q", messageID, sessionID)
	}
	acked, err := deliverEntryIsAcked(inbox, sessionID, "", messageID)
	if err != nil {
		return fmt.Errorf("steer: post-finish wake %q: inspect acknowledgement: %w", messageID, err)
	}
	if acked {
		return nil
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return fmt.Errorf("steer: post-finish wake: load recipient %q: %w", sessionID, err)
	}
	if rec.Stopped() {
		return fmt.Errorf("steer: post-finish wake %q: recipient %q was stopped; entry remains unacknowledged", messageID, sessionID)
	}
	if rec.Terminal() {
		if _, err := al.steerCanceller().Revive(context.Background(), sessionID,
			steer.Principal{Kind: steer.PrincipalKindAgent, ID: rec.AgentID}); err != nil {
			return fmt.Errorf("steer: post-finish wake: revive recipient %q: %w", sessionID, err)
		}
		al.clearRevivalFailure(sessionID)
		al.resetUnifiedMetaStatusActive(sessionID)
		rec, err = lifecycle.Load(sessionID)
		if err != nil {
			return fmt.Errorf("steer: post-finish wake: reload recipient %q: %w", sessionID, err)
		}
	}
	content := deliverySummary(*durable)
	if ts := al.getActiveTurnState(sessionID); ts != nil && ts.IsAlive() {
		if err := al.EnqueueSteeringWake(sessionID, rec.AgentID, sessionID, messageID,
			providers.Message{Role: "user", Content: content}); err == nil {
			return nil
		} else if !errors.Is(err, errSteeringScopeClosed) {
			return fmt.Errorf("steer: post-finish wake: enqueue live turn: %w", err)
		}
	}
	message := bus.InboundMessage{
		Channel:                  "system",
		AsyncOriginAgentID:       rec.AgentID,
		AsyncTranscriptSessionID: sessionID,
		Content:                  content,
		Metadata: map[string]string{
			"steer_message_id": messageID,
			"steer_generation": strconv.Itoa(rec.Generation),
		},
	}
	_, err = al.processSteeredSystemWake(context.Background(), message)
	return err
}
