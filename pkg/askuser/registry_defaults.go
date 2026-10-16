// Omnipus — transactional default-safe resolution over the question FIFO
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package askuser

import (
	"fmt"
	"time"
)

const defaultSafeRetryDelay = time.Second

// applyDefaultSafeSet runs under transitionMu and returns a terminal set
// only when all questions were automatically answered. Partial resolution
// preserves the complete queue in the same write. Store failures leave the
// original active set untouched and return to the timer's retry path.
func (r *Registry) applyDefaultSafeSet(cardID string) (*PendingSet, error) {
	r.mu.Lock()
	current, ok := r.entries[cardID]
	if !ok || current.Status != StatusPending {
		r.mu.Unlock()
		return nil, nil
	}
	set := current.Clone()
	waiting := clonePendingSets(r.queued[set.TranscriptSessionID])
	r.mu.Unlock()

	if set.AutoResolved == nil {
		set.AutoResolved = make(map[string]time.Time)
	}
	now := r.now().UTC()
	type autoResolution struct{ header, label string }
	var resolved []autoResolution
	for _, question := range set.Questions {
		if !question.DefaultSafe {
			continue
		}
		if _, done := set.AutoResolved[question.Header]; done {
			continue
		}
		set.AutoResolved[question.Header] = now
		resolved = append(resolved, autoResolution{question.Header, question.Recommended})
	}
	if len(resolved) == 0 {
		return nil, nil
	}

	serverSubmit := allDefaultSafe(set.Questions) && len(set.AutoResolved) == len(set.Questions)
	if serverSubmit {
		set.Status = StatusAnswered
		set.Answers = buildAllDefaultAnswers(set.Questions)
		if err := r.finishPending(set); err != nil {
			return nil, err
		}
	} else {
		if err := r.persist(set, waiting); err != nil {
			return nil, fmt.Errorf("askuser: auto-default: persist failed: %w", err)
		}
		r.mu.Lock()
		r.entries[cardID] = set.Clone()
		r.mu.Unlock()
		r.stopTimers(cardID)
		r.sink.EmitCard(set.Clone())
	}

	if r.audit != nil {
		for _, resolution := range resolved {
			r.audit.RecordAutoDefault(cardID, set.TranscriptSessionID, resolution.header, resolution.label)
		}
	}
	if serverSubmit {
		return set, nil
	}
	return nil, nil
}
