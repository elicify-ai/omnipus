package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// This marker stays on the internal parallel receipt slice only. It is never
// a provider-message field or a public steering receipt.
const savedWakeCorrelationPrefix = "\x00saved-wake:"

// savedWakeCorrelation carries the saved input's own recipient transcript
// with its outcome id, so a consumer whose turn has no transcript binding (a
// post-turn steering continuation) still addresses the right saved record and
// releases the right claim (R2-2).
func savedWakeCorrelation(transcriptSessionID, outcomeID string) string {
	return savedWakeCorrelationPrefix + outcomeID + "\x00" + transcriptSessionID
}

// parseSavedWakeCorrelation returns the outcome id and the transcript it
// belongs to; fallbackSession is used only when none was carried.
func parseSavedWakeCorrelation(correlationID, fallbackSession string) (outcomeID, sessionID string, ok bool) {
	rest, ok := strings.CutPrefix(correlationID, savedWakeCorrelationPrefix)
	if !ok {
		return "", "", false
	}
	outcomeID, sessionID, _ = strings.Cut(rest, "\x00")
	if sessionID == "" {
		sessionID = fallbackSession
	}
	return outcomeID, sessionID, true
}

// errReservedSteeringCorrelation refuses a caller-supplied steering
// correlation in the engine's saved-wake format (PW-SEC-R3). Only the engine
// mints that form, from a queued wake item; accepted from a caller it would
// name another session's saved input.
var errReservedSteeringCorrelation = errors.New("steering refused: correlation_id uses a reserved internal format")

// callerSteeringCorrelation trims a caller's correlation id and refuses the
// reserved saved-wake form.
func callerSteeringCorrelation(correlationID string) (string, error) {
	correlationID = strings.TrimSpace(correlationID)
	if strings.HasPrefix(correlationID, savedWakeCorrelationPrefix) {
		return "", errReservedSteeringCorrelation
	}
	return correlationID, nil
}

// errSavedWakeForeignRecipient marks a saved-wake correlation whose recipient
// is not the consuming turn's own session. Nothing is placed or released.
var errSavedWakeForeignRecipient = errors.New("saved wake input belongs to another session")

// savedWakeOwnedBy reports whether a consumer bound to consumerSession may act
// on recipient's saved input. A consumer with no transcript binding (a
// post-turn continuation) only ever receives engine-minted correlations from
// its own queue (consumeDequeuedSteeringResult).
func savedWakeOwnedBy(recipient, consumerSession string) bool {
	return consumerSession == "" || recipient == consumerSession
}

var errSavedWakeHeld = errors.New("saved wake input is held by Stop")

// errSavedWakeHeldForQuestion holds a saved input while the session has an
// unanswered question card (PW-D15). The person's correlated answer is the
// continuation that places it.
var errSavedWakeHeldForQuestion = errors.New("saved wake input is held for an unanswered question")

// openQuestionCard reports whether sessionID has an UNANSWERED question card.
// The retained record of an answered or cancelled card stays in the same
// field for history, so presence alone is not "unanswered". A record that
// cannot be read is returned as an error, never treated as a hold.
func (al *AgentLoop) openQuestionCard(sessionID string) (bool, error) {
	store := al.GetSessionStore()
	if store == nil {
		return false, nil
	}
	meta, err := store.GetMeta(sessionID)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return questionCardOpen(sessionID, meta.PendingAskJSON)
}

func questionCardOpen(sessionID, pendingAskJSON string) (bool, error) {
	if strings.TrimSpace(pendingAskJSON) == "" {
		return false, nil
	}
	var set askuser.PendingSet
	if err := json.Unmarshal([]byte(pendingAskJSON), &set); err != nil {
		return false, fmt.Errorf("session %s: question card record is unreadable: %w", sessionID, err)
	}
	return set.Status == askuser.StatusPending, nil
}

// releaseSavedWakeClaims ends the in-process publication claim of every saved
// input in ids that a live consumer is giving back unplaced (held, failed, or
// never attempted). The saved record stays pending, so the next legitimate
// turn's fence places it; a claim left behind would make it look in flight
// for the rest of the process.
func (al *AgentLoop) releaseSavedWakeClaims(sessionID string, correlationIDs []string) {
	for _, id := range correlationIDs {
		if outcomeID, owner, ok := parseSavedWakeCorrelation(id, sessionID); ok && savedWakeOwnedBy(owner, sessionID) {
			al.releaseWakePublication(owner, outcomeID)
		}
	}
}

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
		// PW-D15 at the consumption boundary: a card parked after the
		// producer's check still holds the outcome until the answer.
		open, err := al.openQuestionCard(sessionID)
		if err != nil {
			return false, err
		}
		if open {
			return false, errSavedWakeHeldForQuestion
		}
		consumed, err := al.wakeInputConsumed(sessionID, outcomeID)
		if err != nil {
			return false, err
		}
		if consumed {
			// Already received through another consumer (a poll). It leaves
			// the pending directory; it is not placed again.
			if retireErr := store.RetireSavedInput(sessionID, "saved-input-"+outcomeID); retireErr != nil {
				return false, retireErr
			}
			al.releaseWakePublication(sessionID, outcomeID)
			return false, nil
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
	al.releaseWakePublication(sessionID, outcomeID)
	return !placed, nil
}

// placePendingSavedWakeInputs runs at an ordinary model-consumption fence.
// A parked question has no such turn; its reports remain chat-only until the
// person's correlated answer legitimately continues it.
func (al *AgentLoop) placePendingSavedWakeInputs(ctx context.Context, ts *turnState) error {
	if ts.opts.NoHistory || ts.transcriptSessionID == "" || al.GetSessionStore() == nil {
		return nil
	}
	// The content-free pending directory answers "none" without reading the
	// archive; only pending records' own addresses are read otherwise.
	inputs, err := al.GetSessionStore().PendingChatInputs(ctx, ts.transcriptSessionID)
	if errors.Is(err, fs.ErrNotExist) {
		// SaveChatInput refuses a recipient without metadata, so a session
		// with no metadata cannot hold a saved wake input: nothing to place.
		return nil
	}
	if err != nil || len(inputs) == 0 {
		return err
	}
	open, err := al.openQuestionCard(ts.transcriptSessionID)
	if err != nil {
		return err
	}
	if open {
		// PW-D15: retained until the correlated answer continues the session.
		return nil
	}
	for _, input := range inputs {
		id := strings.TrimPrefix(input.ID, "saved-input-")
		if al.wakePublicationInFlight(ts.transcriptSessionID, id) {
			// Its published wake (idle turn or live-turn join) is the consumer
			// that places it. Placing it here too would let an unrelated turn
			// take the input out from under that wake and a delegate poll.
			continue
		}
		if _, err := al.placeSavedWakeInput(ctx, ts.transcriptSessionID, id, ts.agent.ID); err != nil {
			return err
		}
	}
	return nil
}

func (al *AgentLoop) admitSteeringInput(ts *turnState, msg providers.Message, correlationID string) (bool, error) {
	if outcomeID, sessionID, ok := parseSavedWakeCorrelation(correlationID, ts.transcriptSessionID); ok {
		if !savedWakeOwnedBy(sessionID, ts.transcriptSessionID) {
			return false, fmt.Errorf("%w: turn session %s, recipient %s, outcome %s",
				errSavedWakeForeignRecipient, ts.transcriptSessionID, sessionID, outcomeID)
		}
		ctx := ts.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		include, err := al.placeSavedWakeInput(ctx, sessionID, outcomeID, ts.agent.ID)
		if err != nil {
			// Held (Stop or an unanswered card) or failed before placement: the
			// saved record stays pending for the next legitimate fence, and this
			// consumer gives back its claim instead of stranding it.
			al.releaseWakePublication(sessionID, outcomeID)
			if errors.Is(err, errSavedWakeHeld) || errors.Is(err, errSavedWakeHeldForQuestion) {
				logger.InfoCF("agent", "saved helper input held at the live fence; it stays pending",
					map[string]any{"session_id": sessionID, "outcome_id": outcomeID, "reason": err.Error()})
				return false, nil
			}
		}
		return include, err
	}
	roomRequestID, _ := strings.CutPrefix(correlationID, roomSteerCorrelationPrefix)
	if roomRequestID == correlationID {
		roomRequestID = ""
	}
	_, err := ts.appendWindowMessage(msg, al.inputProducer(ts), roomRequestID)
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

// savedWakeHeldForQuestion is the idle-wake receiver's PW-D15 check: a card
// that became pending after the outcome was published still holds it. The
// saved record stays pending and no outcome-only turn starts; the answer's
// own continuation places it.
func (al *AgentLoop) savedWakeHeldForQuestion(sessionID, messageID string, saved bool) (bool, error) {
	if !saved {
		return false, nil
	}
	open, err := al.openQuestionCard(sessionID)
	if err != nil || !open {
		return false, err
	}
	logger.InfoCF("agent", "helper outcome held for an unanswered question card; no outcome-only turn",
		map[string]any{"session_id": sessionID, "outcome_id": messageID})
	return true, nil
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

// Publication coalescing is process-local, not lifetime state. A receiver
// that finds a poll's consumed marker must release it just like a placing
// consumer; the durable marker remains the duplicate-work guard.
func (al *AgentLoop) releaseWakePublication(sessionID, outcomeID string) {
	if al.asyncNotifier != nil {
		al.asyncNotifier.sessionWakes.Delete(sessionID + "\x00" + outcomeID)
	}
}

// wakePublicationInFlight reports a wake this process published for the
// saved input and whose receiver has not exited yet. After a restart no
// publication is in flight, so the next legitimate turn places the input.
func (al *AgentLoop) wakePublicationInFlight(sessionID, outcomeID string) bool {
	if al.asyncNotifier == nil {
		return false
	}
	_, inFlight := al.asyncNotifier.sessionWakes.Load(sessionID + "\x00" + outcomeID)
	return inFlight
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
