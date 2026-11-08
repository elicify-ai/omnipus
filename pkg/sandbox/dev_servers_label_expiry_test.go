// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package sandbox — wave-2 PTA-2 pin (fix round 5): the dev store's
// LookupByLabel must refuse an EXPIRED entry that is still in the map.
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: pkg/sandbox/dev_servers.go::LookupByLabel returns
//     nil for a registration past its idle deadline (LastActivity +
//     IdleTimeout) or its hard deadline (CreatedAt + HardTimeout) even though
//     the janitor has not removed the entry yet.
//   - Specification source: ADR-094 spec FR-029 (same sentence as the static
//     store: the label is unresolvable on every token-revocation path,
//     expiry included) + Lookup's documented expiry semantics ("If either has
//     passed the entry is considered expired and nil is returned
//     immediately"). Expected value derived from the spec, never from
//     observed output.
//   - Unit boundary: the REAL registry (NewDevServerRegistry); expired
//     entries constructed DIRECTLY in-package — no clock seam exists
//     (LookupByLabel samples time.Now() internally); PID stays 0 so
//     signalProcess is a no-op and Close() never signals a real process.
//   - Case table:
//     idle-expired-in-map (LastActivity past IdleTimeout, CreatedAt fresh) → nil
//     hard-expired-in-map (CreatedAt past HardTimeout, LastActivity fresh) → nil
//     boundary-inside (idle age IdleTimeout-1s, CreatedAt fresh)           → resolves,
//     LastActivity touched (the documented label-host idle-keeper)
//   - Mutations (run in CHECK, one at a time): delete the idle/hard skip in
//     LookupByLabel → both expired cases die; neutralize only the idle clause
//     → idle case dies; neutralize only the hard clause → hard case dies.
//   - Known gaps: the exact timeout instants (age == timeout) are untestable
//     against a moving wall clock — strict-vs-non-strict there is an
//     equivalent mutant under any real scheduling.
package sandbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// TestDevServerRegistry_LookupByLabel_ExpiredEntryDoesNotResolve pins
// FR-029's expiry half at the dev-store level: a label backed by an
// expired-but-uncleaned registration must NOT resolve — via the idle timer
// or the hard timer — while a label inside both timers still does.
func TestDevServerRegistry_LookupByLabel_ExpiredEntryDoesNotResolve(t *testing.T) {
	r := NewDevServerRegistry()
	t.Cleanup(r.Close)

	now := time.Now()
	insert := func(token, agentID string, createdAt, lastActivity time.Time) {
		t.Helper()
		r.mu.Lock()
		r.entries[token] = &DevServerRegistration{
			AgentID:      agentID,
			Token:        token,
			Port:         1,
			PID:          0, // signalProcess no-ops on PID <= 0
			CreatedAt:    createdAt,
			LastActivity: lastActivity,
		}
		r.mu.Unlock()
	}

	// Idle-expired: activity stale past IdleTimeout, created just now.
	const idleToken = "dev-expiry-pin-idle-token"
	idleLabel := middleware.PreviewLabelForToken(idleToken)
	insert(idleToken, "dev-expiry-pin-idle-agent",
		now, now.Add(-IdleTimeout-time.Minute))

	// Hard-expired: created past HardTimeout, activity fresh.
	const hardToken = "dev-expiry-pin-hard-token"
	hardLabel := middleware.PreviewLabelForToken(hardToken)
	insert(hardToken, "dev-expiry-pin-hard-agent",
		now.Add(-HardTimeout-time.Minute), now)

	// Boundary-inside: idle age IdleTimeout-1s, created just now.
	const insideToken = "dev-expiry-pin-inside-token"
	insideLabel := middleware.PreviewLabelForToken(insideToken)
	insert(insideToken, "dev-expiry-pin-inside-agent",
		now, now.Add(-IdleTimeout+time.Second))

	stillInMap := func(t *testing.T, token string) {
		t.Helper()
		r.mu.Lock()
		_, present := r.entries[token]
		r.mu.Unlock()
		require.True(t, present,
			"fixture binding: the entry must still be in the map (the janitor runs on a 30 s tick)")
	}

	t.Run("idle_expired_entry_still_in_map_does_not_resolve_by_label", func(t *testing.T) {
		stillInMap(t, idleToken)
		assert.Nil(t, r.LookupByLabel(idleLabel),
			"FR-029: a registration past its idle deadline must NOT resolve by label even "+
				"before the janitor removes it — expiry makes the token, and so the label, unresolvable")
	})

	t.Run("hard_expired_entry_still_in_map_does_not_resolve_by_label", func(t *testing.T) {
		stillInMap(t, hardToken)
		assert.Nil(t, r.LookupByLabel(hardLabel),
			"FR-029: a registration past the 4 h hard cap must NOT resolve by label even "+
				"with fresh activity — the hard cap is absolute")
	})

	t.Run("idle_age_one_second_inside_timeout_still_resolves_and_touches", func(t *testing.T) {
		stillInMap(t, insideToken)
		got := r.LookupByLabel(insideLabel)
		require.NotNil(t, got,
			"an entry inside both timers must still resolve — only expiry retires the label")
		assert.Equal(t, "dev-expiry-pin-inside-agent", got.AgentID)
		assert.False(t, got.LastActivity.Before(now),
			"a resolving label-host hit must touch LastActivity (the documented idle keeper — "+
				"a label hit keeps the dev server alive exactly as a token-path hit does)")
	})
}
