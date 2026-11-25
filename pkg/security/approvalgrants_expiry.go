// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security

// Skills & Tools C-26, Dataset D17 row 9: when a timed address allowance
// ends during a running command, the command's next new connection is
// refused and the command is not killed. The lookups already ignore an ended
// entry; the egress proxy, however, keeps the hosts pushed onto a live token.
// SweepEndedNetwork finds the ended network allowances on the store's own
// clock so the caller can revoke the agent's live egress tokens.

import (
	"sort"
	"time"
)

// SweepEndedNetwork removes every network allowance (named address or the
// usual ports to any address) whose end has passed and returns the live
// egress tokens of the agents that lost one, sorted. An allowance with no
// end is never swept.
func (s *ApprovalGrantStore) SweepEndedNetwork() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ended := map[string]bool{}
	for id, a := range s.agents {
		for h, end := range a.hosts {
			if s.timedAndEnded(end) {
				delete(a.hosts, h)
				ended[id] = true
			}
		}
		if a.hasNetwork && s.timedAndEnded(a.networkAny) {
			a.hasNetwork, a.networkAny = false, 0
			ended[id] = true
		}
	}
	var out []string
	for key, tok := range s.networkTokens {
		if ended[key.agentID] && tok != "" {
			out = append(out, tok)
		}
	}
	sort.Strings(out)
	return out
}

// timedAndEnded reports a timed entry whose end has passed. Caller holds
// s.mu.
func (s *ApprovalGrantStore) timedAndEnded(end time.Duration) bool {
	return end != noEnd && !s.live(end)
}
