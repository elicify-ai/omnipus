// Omnipus — durable per-session question-card FIFO (#1329)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package askuser

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// pendingState extends the existing stored object without nesting its head:
// legacy PendingSet readers still see the active (or last terminal) card.
// Waiting sets are storage-only; they never enter the active-card indices or
// the CardSink until promotion.
type pendingState struct { // not-wire-format: internal session-store record, never emitted in a gateway or SPA response
	PendingSet
	QueuedSets []*PendingSet `json:"queued_sets,omitempty"`
}

func clonePendingSets(sets []*PendingSet) []*PendingSet {
	if len(sets) == 0 {
		return nil
	}
	out := make([]*PendingSet, len(sets))
	for i, set := range sets {
		out[i] = set.Clone()
	}
	return out
}

func routingSessionKey(set *PendingSet) string {
	if set.RoutingSessionKey != "" {
		return set.RoutingSessionKey
	}
	return set.TranscriptSessionID
}

// pendingCountLocked includes hidden waiting sets in the existing global cap.
func (r *Registry) pendingCountLocked() int {
	count := len(r.entries)
	for _, waiting := range r.queued {
		count += len(waiting)
	}
	return count
}

func (r *Registry) cardSessionLocked(cardID string) (string, bool) {
	if set, ok := r.entries[cardID]; ok {
		return set.TranscriptSessionID, true
	}
	for sessionID, waiting := range r.queued {
		for _, set := range waiting {
			if set.CardID == cardID {
				return sessionID, true
			}
		}
	}
	return "", false
}

// Reserve the routing namespace for waiting sets too, so another session
// cannot take a queued head's routing key before that head is promoted.
func (r *Registry) routingConflictLocked(set *PendingSet) bool {
	key := routingSessionKey(set)
	if id, ok := r.byRouting[key]; ok && r.entries[id].TranscriptSessionID != set.TranscriptSessionID {
		return true
	}
	for sessionID, waiting := range r.queued {
		if sessionID == set.TranscriptSessionID {
			continue
		}
		for _, queued := range waiting {
			if routingSessionKey(queued) == key {
				return true
			}
		}
	}
	return false
}

// admit serializes recovery, admission and publication. The store write
// precedes every index change: failed admission cannot be reported as accepted
// or overwrite work admitted concurrently with an answer or cancellation.
func (r *Registry) admit(set *PendingSet) error {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()

	// Boot recovery is asynchronous. A first call arriving before its sweep
	// must restore the durable head and queue, not replace them with this set.
	if r.meta != nil {
		if err := r.rearmSessionLocked(set.TranscriptSessionID); err != nil {
			return fmt.Errorf("askuser: CreatePending: recover pending state: %w", err)
		}
	}

	r.mu.Lock()
	if sessionID, exists := r.cardSessionLocked(set.CardID); exists {
		r.mu.Unlock()
		if sessionID != set.TranscriptSessionID {
			return ErrSessionMismatch
		}
		return nil // exact CardID retry, not workspace/content deduplication
	}
	if r.routingConflictLocked(set) {
		r.mu.Unlock()
		return ErrSessionMismatch
	}
	if r.maxPending > 0 && r.pendingCountLocked() >= r.maxPending {
		r.mu.Unlock()
		return ErrSaturated
	}
	active := r.entries[r.bySession[set.TranscriptSessionID]].Clone()
	waiting := clonePendingSets(r.queued[set.TranscriptSessionID])
	r.mu.Unlock()

	if active != nil {
		waiting = append(waiting, set.Clone())
		if err := r.persist(active, waiting); err != nil {
			return fmt.Errorf("askuser: CreatePending: queue persist failed: %w", err)
		}
		r.mu.Lock()
		r.queued[set.TranscriptSessionID] = waiting
		r.mu.Unlock()
		slog.Info("askuser: card queued", "card_id", set.CardID,
			"session_id", set.TranscriptSessionID, "queue_depth", len(waiting))
		return nil
	}

	if err := r.persist(set, nil); err != nil {
		return fmt.Errorf("askuser: CreatePending: persist failed: %w", err)
	}
	r.mu.Lock()
	r.entries[set.CardID] = set.Clone()
	r.byRouting[routingSessionKey(set)] = set.CardID
	r.bySession[set.TranscriptSessionID] = set.CardID
	r.mu.Unlock()
	r.emitPending(set)
	return nil
}

// emitPending is called under transitionMu, never mu. Its timer callback
// acquires transitionMu too, so even an elapsed timer cannot emit a terminal
// frame before this card's initial pending frame.
func (r *Registry) emitPending(set *PendingSet) {
	r.armTimers(set)
	defaultSafeCount := 0
	for i := range set.Questions {
		if set.Questions[i].DefaultSafe {
			defaultSafeCount++
		}
	}
	slog.Info("askuser: card created", "card_id", set.CardID,
		"question_count", len(set.Questions), "default_safe_count", defaultSafeCount)
	r.sink.EmitCard(set.Clone())
}

// finishPending commits consumption and promotion in ONE durable record.
// The caller holds transitionMu and supplies a private terminal snapshot.
// No timer, active index, queued set or frame changes if that write fails.
func (r *Registry) finishPending(consumed *PendingSet) error {
	r.mu.Lock()
	waiting := clonePendingSets(r.queued[consumed.TranscriptSessionID])
	r.mu.Unlock()

	storedHead := consumed
	var next *PendingSet
	if len(waiting) > 0 {
		next = waiting[0]
		waiting = waiting[1:]
		next.Status = StatusPending
		next.CreatedAt = r.now().UTC()
		next.AutoResolved = nil
		next.Answers = nil
		storedHead = next
	}
	if err := r.persist(storedHead, waiting); err != nil {
		return fmt.Errorf("askuser: resolve: persist failed: %w", err)
	}

	r.mu.Lock()
	r.removeLocked(consumed)
	delete(r.queued, consumed.TranscriptSessionID)
	if next != nil {
		r.entries[next.CardID] = next.Clone()
		r.byRouting[routingSessionKey(next)] = next.CardID
		r.bySession[next.TranscriptSessionID] = next.CardID
		if len(waiting) > 0 {
			r.queued[next.TranscriptSessionID] = waiting
		}
	}
	r.mu.Unlock()

	r.stopTimers(consumed.CardID)
	r.sink.EmitCard(consumed.Clone())
	if next != nil {
		r.emitPending(next)
	}
	return nil
}

// submitPending is the transactional half of Submit. Resume dispatch is
// deliberately outside this helper and its operation lock.
func (r *Registry) submitPending(cardID, sessionID, user string, answers []SubmittedAnswer) (*PendingSet, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	r.mu.Lock()
	set, ok := r.entries[cardID]
	if !ok {
		r.mu.Unlock()
		return nil, ErrNoPending
	}
	if set.TranscriptSessionID != sessionID {
		r.mu.Unlock()
		return nil, ErrSessionMismatch
	}
	if set.Owner != "" && user != "" && user != set.Owner {
		r.mu.Unlock()
		return nil, ErrNotOwner
	}
	if set.Status != StatusPending {
		r.mu.Unlock()
		return nil, ErrAlreadyResolved
	}
	consumed := set.Clone()
	r.mu.Unlock()
	final, err := validateSubmission(consumed.Questions, answers)
	if err != nil {
		return nil, err
	}
	consumed.Status = StatusAnswered
	consumed.Answers = final
	if err := r.finishPending(consumed); err != nil {
		return nil, err
	}
	return consumed, nil
}

// cancelPending is called under transitionMu by either cancellation path.
func (r *Registry) cancelPending(cardID, sessionID string) (*PendingSet, error) {
	r.mu.Lock()
	set, ok := r.entries[cardID]
	if !ok {
		r.mu.Unlock()
		return nil, ErrNoPending
	}
	if sessionID != "" && set.TranscriptSessionID != sessionID {
		r.mu.Unlock()
		return nil, ErrSessionMismatch
	}
	consumed := set.Clone()
	r.mu.Unlock()
	consumed.Status = StatusCancelled
	consumed.Answers = nil
	if err := r.finishPending(consumed); err != nil {
		return nil, err
	}
	return consumed, nil
}

// rearmSessionLocked restores the complete durable FIFO under transitionMu.
// Already-admitted sessions are not reloaded over more recent live state.
func (r *Registry) rearmSessionLocked(sessionID string) error {
	r.mu.Lock()
	_, alreadyLive := r.bySession[sessionID]
	r.mu.Unlock()
	if alreadyLive {
		return nil
	}
	if r.meta == nil {
		return fmt.Errorf("askuser: RearmSession: no meta store wired")
	}
	meta, err := r.meta.GetMeta(sessionID)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("askuser: RearmSession: missing session %s", sessionID)
	}
	if meta.PendingAskJSON == "" {
		return nil
	}
	var state pendingState
	if err := json.Unmarshal([]byte(meta.PendingAskJSON), &state); err != nil {
		return fmt.Errorf("askuser: RearmSession: corrupt pending_ask on %s: %w", sessionID, err)
	}
	if state.Status != StatusPending {
		if len(state.QueuedSets) == 0 && (state.Status == StatusAnswered || state.Status == StatusCancelled) {
			return nil
		}
		return fmt.Errorf("askuser: RearmSession: invalid pending state on %s", sessionID)
	}

	r.mu.Lock()
	if err := r.validateRecoveredLocked(sessionID, &state); err != nil {
		r.mu.Unlock()
		return err
	}
	head := state.Clone()
	r.entries[head.CardID] = head.Clone()
	r.byRouting[routingSessionKey(head)] = head.CardID
	r.bySession[sessionID] = head.CardID
	if len(state.QueuedSets) > 0 {
		r.queued[sessionID] = clonePendingSets(state.QueuedSets)
	}
	r.mu.Unlock()

	// Accepted durable work is restored even if a newly lowered admission cap
	// is already exceeded. New admissions remain blocked until space clears.
	r.armTimers(head)
	r.sink.EmitCard(head.Clone())
	return nil
}

func (r *Registry) validateRecoveredLocked(sessionID string, state *pendingState) error {
	seen := make(map[string]bool, 1+len(state.QueuedSets))
	sets := append([]*PendingSet{&state.PendingSet}, state.QueuedSets...)
	for _, set := range sets {
		if set == nil || set.CardID == "" || set.TranscriptSessionID != sessionID || set.Status != StatusPending {
			return fmt.Errorf("askuser: RearmSession: invalid queued question set on %s", sessionID)
		}
		if err := ValidateQuestions(set.Questions); err != nil {
			return fmt.Errorf("askuser: RearmSession: invalid questions on %s: %w", sessionID, err)
		}
		if seen[set.CardID] {
			return fmt.Errorf("askuser: RearmSession: duplicate card id on %s", sessionID)
		}
		seen[set.CardID] = true
		if _, exists := r.cardSessionLocked(set.CardID); exists || r.routingConflictLocked(set) {
			return fmt.Errorf("askuser: RearmSession: conflicting pending state on %s: %w", sessionID, ErrSessionMismatch)
		}
	}
	return nil
}
