package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// This marker stays on the internal parallel receipt slice only. It is never
// a provider-message field or a public steering receipt.
const savedWakeCorrelationPrefix = "\x00saved-wake:"

var errSavedWakeHeld = errors.New("saved wake input is held by Stop")

func (al *AgentLoop) placeSavedWakeInput(ctx context.Context, sessionID, outcomeID, agentID string) (bool, error) {
	store := al.GetSessionStore()
	addr, placed, err := store.SavedChatInput(sessionID, "saved-input-"+outcomeID)
	if err != nil {
		return false, err
	}
	if addr.EntryID == "" {
		return false, nil
	}
	if !placed {
		lifecycle := al.GetSessionLifecycleStore()
		lock := lifecycle.Lock(sessionID)
		lock.Lock()
		defer lock.Unlock()
		rec, err := lifecycle.LoadLocked(sessionID)
		if err != nil {
			return false, err
		}
		intents, err := lifecycle.UnfinishedStopIntentsLocked(sessionID)
		if err != nil {
			return false, err
		}
		if lifecycleInFlightStopFence(rec) || rec.State == session.LifecycleStopped || acceptedStopSelectsExecution(rec, intents) {
			return false, errSavedWakeHeld
		}
		consumed, err := al.wakeInputConsumed(sessionID, outcomeID)
		if err != nil || consumed {
			return false, err
		}
		if _, _, err := store.PlaceSavedInput(ctx, sessionID, addr); err != nil {
			if !errors.Is(err, session.ErrAlreadyConsumed) {
				return false, fmt.Errorf("place saved wake %q: %w", outcomeID, err)
			}
			placed = true
		}
	}
	wake := steeringWake{messageID: outcomeID, transcriptSessionID: sessionID, agentID: agentID}
	_, markerErr := al.writeSteeringConsumedMarker(wake)
	if markerErr != nil {
		return false, markerErr
	}
	if err := al.ackConsumedSteeringWake(wake); err != nil {
		return false, err
	}
	if al.asyncNotifier != nil {
		al.asyncNotifier.sessionWakes.Delete(sessionID + "\x00" + outcomeID)
	}
	return !placed, nil
}

// placePendingSavedWakeInputs runs at an ordinary model-consumption fence.
// A parked question has no such turn; its reports remain chat-only until the
// person's correlated answer legitimately continues it.
func (al *AgentLoop) placePendingSavedWakeInputs(ctx context.Context, ts *turnState) error {
	if ts.opts.NoHistory || ts.transcriptSessionID == "" || al.GetSessionStore() == nil {
		return nil
	}
	meta, err := al.GetSessionStore().GetMeta(ts.transcriptSessionID)
	if err != nil {
		return err
	}
	if meta.PendingAskJSON != "" {
		return nil
	}
	inputs, err := al.GetSessionStore().PendingChatInputs(ctx, ts.transcriptSessionID)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		id := strings.TrimPrefix(input.ID, "saved-input-")
		if _, err := al.placeSavedWakeInput(ctx, ts.transcriptSessionID, id, ts.agent.ID); err != nil {
			return err
		}
	}
	return nil
}

func (al *AgentLoop) admitSteeringInput(ts *turnState, msg providers.Message, correlationID string) (bool, error) {
	if strings.HasPrefix(correlationID, savedWakeCorrelationPrefix) {
		ctx := ts.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		include, err := al.placeSavedWakeInput(ctx, ts.transcriptSessionID,
			strings.TrimPrefix(correlationID, savedWakeCorrelationPrefix), ts.agent.ID)
		if errors.Is(err, errSavedWakeHeld) {
			return false, nil
		}
		return include, err
	}
	_, err := ts.appendWindowMessage(msg, al.inputProducer(ts))
	return err == nil, err
}

func (al *AgentLoop) prepareSavedSystemWake(ctx context.Context, sessionID, messageID, agentID string) (saved, consumed bool, err error) {
	addr, placed, err := al.GetSessionStore().SavedChatInput(sessionID, "saved-input-"+messageID)
	if err != nil {
		return false, false, err
	}
	if placed {
		// Placement, not a prior bus publish, is the durable crash boundary.
		_, err := al.placeSavedWakeInput(ctx, sessionID, messageID, agentID)
		return true, true, err
	}
	return addr.EntryID != "", false, nil
}

func (al *AgentLoop) placeSavedWakeAtEntry(ctx context.Context, ts *turnState, messageID string, saved bool) error {
	if !saved {
		return nil
	}
	if _, err := al.placeSavedWakeInput(ctx, ts.transcriptSessionID, messageID, ts.agent.ID); err != nil {
		return err
	}
	ts.opts.InputAlreadyArchived = true
	return nil
}

func visibleSteeringReceiptIDs(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		if !strings.HasPrefix(id, savedWakeCorrelationPrefix) {
			out[i] = id
		}
	}
	return out
}
