// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package secretrequest

import (
	"errors"
	"sort"
)

// PendingSessionIDs returns, once each and sorted, the sessions that hold a
// pending secret card — the attention source of FR-091 / BDD-10.2. It never
// returns a card, a value or a recipient. A registry whose saved state could
// not be recovered answers with an error, so the caller reports attention as
// unknown instead of a false "nothing pending".
func (r *Registry) PendingSessionIDs() ([]string, error) {
	if r == nil {
		return nil, errors.New("secret request registry is not wired")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.initErr != nil {
		return nil, r.initErr
	}
	seen := make(map[string]struct{})
	for _, rec := range r.entries {
		if rec.Card.State == "pending" && rec.Card.SessionId != "" {
			seen[rec.Card.SessionId] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
