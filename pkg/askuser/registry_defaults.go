// Omnipus — transactional default-safe resolution over the question FIFO
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package askuser

import (
	"fmt"
	"time"
)

const defaultSafeRetryDelay = time.Second

// defaultSafeResolution carries a consumed card only after a complete automatic
// submission. Its zero value is a successful no-op or partial resolution, not a
// storage failure and not a reason to dispatch a resume.
type defaultSafeResolution struct {
	consumed *PendingSet
}

// applyDefaultSafeSet runs under transitionMu and returns a terminal set in its
// result only when all questions were automatically answered. Partial resolution
// preserves the complete queue in the same write. Store failures leave the
// original active set untouched and return to the timer's retry path.
func (r *Registry) applyDefaultSafeSet(cardID string) (defaultSafeResolution, error) {
	r.mu.Lock()
	current, ok := r.entries[cardID]
	if !ok || current.Status != StatusPending {
		r.mu.Unlock()
		return defaultSafeResolution{}, nil
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
		return defaultSafeResolution{}, nil
	}

	serverSubmit := allDefaultSafe(set.Questions) && len(set.AutoResolved) == len(set.Questions)
	if serverSubmit {
		set.Status = StatusAnswered
		set.Answers = buildAllDefaultAnswers(set.Questions)
		if err := r.finishPending(set); err != nil {
			return defaultSafeResolution{}, err
		}
	} else {
		if err := r.persist(set, waiting); err != nil {
			return defaultSafeResolution{}, fmt.Errorf("askuser: auto-default: persist failed: %w", err)
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
		return defaultSafeResolution{consumed: set}, nil
	}
	return defaultSafeResolution{}, nil
}
