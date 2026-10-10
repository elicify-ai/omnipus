// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// attention.go: the store side of a main's attention mark (session-core U11,
// FR-047, C-ATTENTION). One small object in the main's meta.json, two
// operations: the outcome raise (inside the transcript append) and the seen ack.

package session

import (
	"fmt"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// persisted is the identity-group form: nil (key omitted) for the zero mark.
func (a AttentionMark) persisted() *AttentionMark {
	if a == (AttentionMark{}) {
		return nil
	}
	return &a
}

// value is the in-memory form of a persisted (possibly absent) mark.
func (a *AttentionMark) value() AttentionMark {
	if a == nil {
		return AttentionMark{}
	}
	return *a
}

// raisesAttention reports whether entry is a saved goal outcome that lights a
// main: a goal that finished (met), failed (rounds_exhausted) or ended for
// another reason. A goal the user stopped never does (founder rule).
func raisesAttention(entry TranscriptEntry) bool {
	if entry.SystemSubtype != SystemSubtypeGoalOutcome || entry.GoalOutcome == nil {
		return false
	}
	switch entry.GoalOutcome.Ending {
	case generated.GoalOutcomeEndingMet,
		generated.GoalOutcomeEndingRoundsExhausted,
		generated.GoalOutcomeEndingOther:
		return true
	}
	return false
}

// raiseAttentionLocked advances meta.Attention.OutcomeOrder for an attention
// outcome and persists meta.json BEFORE the caller appends the entry: if the
// append then fails the main shows a dot with no matching line (cleared on the
// next open), whereas the reverse order could leave an outcome saved with no
// dot, a false "off" that never clears. Caller holds the session shard. Only a
// main carries the mark.
func (us *UnifiedStore) raiseAttentionLocked(sessionID string, meta *UnifiedMeta, entry TranscriptEntry) error {
	if meta.Type != SessionTypeMain || !raisesAttention(entry) {
		return nil
	}
	next := meta.Attention
	order := entry.Timestamp.UnixMilli()
	if floor := next.OutcomeOrder + 1; order < floor {
		order = floor
	}
	next.OutcomeOrder = order
	meta.Attention = next
	if err := us.u5WriteIdentityLocked(sessionID, meta); err != nil {
		return fmt.Errorf("unified_store: record attention outcome order: %w", err)
	}
	return nil
}

// AckAttentionSeen advances the one shared seen mark of a main to
// max(seen, min(n, outcomeOrder)) and writes it synchronously, returning the new
// mark. It never reads "now", never recaptures a newer bound, and never lowers
// the mark. A negative n is refused.
func (us *UnifiedStore) AckAttentionSeen(sessionID string, n int64) (int64, error) {
	if err := validateSessionID(sessionID); err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("unified_store: attention ack %d is negative", n)
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return 0, err
	}
	if meta.Type != SessionTypeMain {
		return 0, fmt.Errorf("unified_store: session %q is not a main; it has no attention mark", sessionID)
	}
	mark := meta.Attention
	target := min(n, mark.OutcomeOrder)
	if target <= mark.SeenOrder {
		return mark.SeenOrder, nil
	}
	mark.SeenOrder = target
	meta.Attention = mark
	if err := us.u5WriteIdentityLocked(sessionID, meta); err != nil {
		return 0, fmt.Errorf("unified_store: write attention seen mark: %w", err)
	}
	return mark.SeenOrder, nil
}
