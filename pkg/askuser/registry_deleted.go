// Omnipus — memory-only cleanup after confirmed session deletion (#1343)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package askuser

// ForgetDeletedSession drops the deleted session's entire interaction state.
// This is not ordinary Stop: no successor is promoted, no resume/card/audit
// is emitted, and the deleted store is never read or written. The caller
// invokes it only after successful deletion and after releasing store locks.
func (r *Registry) ForgetDeletedSession(sessionID string) {
	if sessionID == "" {
		return
	}
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	r.mu.Lock()
	var cardIDs []string
	for _, set := range r.entries {
		if set.TranscriptSessionID == sessionID {
			cardIDs = append(cardIDs, set.CardID)
			r.removeLocked(set)
		}
	}
	for _, waiting := range r.queued[sessionID] {
		cardIDs = append(cardIDs, waiting.CardID)
	}
	delete(r.queued, sessionID)
	delete(r.bySession, sessionID)
	r.mu.Unlock()
	for _, cardID := range cardIDs {
		r.stopTimers(cardID)
	}
}
