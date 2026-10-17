package rooms

import (
	"context"
	"errors"
	"fmt"
)

// settlementRecord is a failed-turn outcome awaiting its delivery-state write.
// It never authorizes model execution.
type settlementRecord struct { // not-wire-format: private repair journal, never exposed at the gateway boundary
	RequestID string `json:"request_id"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
}
type settlementKey struct{ roomID, requestID string }

// ConsumeEnded returns permission to discard ONLY after the actual outcome is
// durable. An answer with unfinished mirror work retains its live reply route.
func (s *Service) ConsumeEnded(roomID, requestID string, turnErr error) (discardCapture bool, completionErr error) {
	if turnErr == nil {
		return false, nil
	}
	intent := settlementRecord{RequestID: requestID, State: StateFailed, Reason: ReasonTurnFailed}
	if errors.Is(turnErr, context.Canceled) {
		intent.State, intent.Reason = StateInterrupted, ""
	}
	return s.completeConsume(roomID, intent)
}

func (s *Service) completeConsume(roomID string, intent settlementRecord) (bool, error) {
	r, err := s.loadRoom(roomID)
	if err != nil {
		if StatusOf(err) == 404 {
			return true, nil
		}
		return false, errors.Join(err, s.rememberSettlement(roomID, intent))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	discard, err := s.applySettlement(r, intent)
	if err != nil {
		return false, errors.Join(err, s.rememberSettlement(roomID, intent))
	}
	s.pendingSettlements.Delete(settlementKey{roomID, intent.RequestID})
	return discard, nil
}

// The caller holds the room lock (or is loading it). Working ownership is
// forgotten only after the terminal state is durable, never after a void log.
func (s *Service) applySettlement(r *room, intent settlementRecord) (bool, error) {
	d := r.deliveries[intent.RequestID]
	if r.deleted || d == nil {
		return true, nil
	}
	if r.rec.PendingReleases[intent.RequestID] {
		return false, nil
	} // never handed over
	posted, unmirrored := false, false
	for _, entry := range r.entries {
		if entry.AnswersRequest == intent.RequestID {
			posted = true
			unmirrored = unmirrored || !r.mirrored[entry.Entry.Id]
		}
	}
	if posted {
		if d.State != StateAnswered {
			if err := s.setState(r, d, StateAnswered, ""); err != nil {
				return false, err
			}
		}
		s.forgetWorking(d.SessionID, d.RequestID)
		return !unmirrored, nil
	}
	if !terminal(d.State) && d.Consumed {
		if err := s.setState(r, d, intent.State, intent.Reason); err != nil {
			return false, err
		}
	}
	if !terminal(d.State) {
		return false, nil
	}
	s.forgetWorking(d.SessionID, d.RequestID)
	return true, nil
}

// This independent journal also works when the room record cannot be read.
// The in-memory copy survives a failed intent append until a later live read;
// durability across process loss still requires at least one successful write.
func (s *Service) rememberSettlement(roomID string, intent settlementRecord) error {
	s.pendingSettlements.Store(settlementKey{roomID, intent.RequestID}, intent)
	if err := s.journal.Append(roomID, StreamSettlements, &intent); err != nil {
		return storageError("The turn ended, but its pending group outcome could not be recorded", err)
	}
	return nil
}

// Reads repair only bookkeeping, never re-admit or execute the old input. A
// persistent failure refuses the read instead of presenting healthy working.
func (s *Service) repairSettlements(r *room) error {
	var repairErr error
	s.pendingSettlements.Range(func(key, value any) bool {
		k, ok := key.(settlementKey)
		if !ok || k.roomID != r.rec.ID {
			return true
		}
		intent, ok := value.(settlementRecord)
		if !ok {
			repairErr = errors.New("invalid pending room settlement")
			return false
		}
		discard, err := s.applySettlement(r, intent)
		if err != nil {
			repairErr = err
			return false
		}
		if d := r.deliveries[intent.RequestID]; discard && d != nil && d.SessionID != "" {
			if err := s.deps.Discard(d.SessionID, d.RequestID); err != nil {
				repairErr = storageError("The turn outcome was saved, but its completed request could not be retired", err)
				return false
			}
		}
		s.pendingSettlements.Delete(k)
		return true
	})
	return repairErr
}

func (s *Service) settlementPending(r *room, requestID string) bool {
	_, pending := s.pendingSettlements.Load(settlementKey{r.rec.ID, requestID})
	return pending
}

func validateSettlement(intent settlementRecord) error {
	if intent.RequestID == "" {
		return fmt.Errorf("room settlement has no request id")
	}
	switch intent.State {
	case StateFailed, StateInterrupted:
		return nil
	}
	return fmt.Errorf("room settlement has invalid state %q", intent.State)
}

func answerNeedsMirror(r *room, requestID string) bool {
	for _, entry := range r.entries {
		if entry.AnswersRequest == requestID && !r.mirrored[entry.Entry.Id] {
			return true
		}
	}
	return false
}
