// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package agent — wave-2 PTA-2 pin (fix round 5): the static store's
// LookupByLabel must refuse an EXPIRED entry that is still in the map.
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: pkg/agent/served_subdirs.go::LookupByLabel
//     returns nil for a registration whose Deadline has passed even though
//     the entry has not been janitor-cleaned yet (the entry is still in the
//     map when the lookup runs).
//   - Specification source: ADR-094 spec FR-029 ("the label becomes
//     unresolvable on EVERY path that makes the token unresolvable: expiry
//     (janitor purgeExpired/sweepExpired), …") — the expired-in-map entry is
//     the window between deadline and janitor sweep. Expected value derived
//     from the spec + the store's own Lookup expiry semantics ("an
//     expired-but-not-yet-janitor-cleaned entry is treated as missing"),
//     never from observed output.
//   - Unit boundary: the REAL registry (NewServedSubdirs); the expired entry
//     is constructed DIRECTLY in-package — there is no clock seam
//     (LookupByLabel samples time.Now() internally), and preview_
//     label_lifecycle_red_test.go's header already named exactly this gap:
//     "janitor expiry … not drivable without a clock seam; they land with
//     GREEN's store-side unit tests if the seam exists". This is that
//     store-side test; the in-package constructor is the seam substitute.
//   - Case table:
//     expired-in-map (Deadline past)      → nil            (FR-029, the pin)
//     boundary-inside (Deadline now+2s)   → resolves       (min-side boundary)
//     registered live entry               → resolves       (positive control —
//     kills a mutant that breaks the
//     lookup instead of the expiry check)
//   - Mutations (run in CHECK, one at a time): delete the
//     `now.After(entry.Deadline)` skip in LookupByLabel → expired case dies;
//     break the label match → live controls die.
//   - Known gaps: the exact deadline instant (Deadline == now) is untestable
//     against a moving wall clock; strict-vs-non-strict at that instant is
//     an equivalent mutant under any real scheduling.
package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// TestServedSubdirs_LookupByLabel_ExpiredEntryDoesNotResolve pins FR-029's
// expiry half at the store level: a label backed by an expired-but-uncleaned
// registration must NOT resolve, while labels backed by live registrations
// still do.
func TestServedSubdirs_LookupByLabel_ExpiredEntryDoesNotResolve(t *testing.T) {
	s := NewServedSubdirs()
	t.Cleanup(s.Stop)

	// The expired entry: constructed directly under the registry lock — the
	// dispatch-authorized seam substitute (no clock seam exists; LookupByLabel
	// calls time.Now() internally). Deadline is clearly past; the janitor's
	// 30 s tick cannot fire inside this test, so the entry is still in the map
	// at lookup time — asserted as the precondition below.
	const expiredToken = "expiry-pin-expired-token"
	expiredLabel := middleware.PreviewLabelForToken(expiredToken)
	s.mu.Lock()
	s.byToken[expiredToken] = &ServedEntry{
		AgentID:     "expiry-pin-expired-agent",
		AbsDir:      t.TempDir(),
		Deadline:    time.Now().Add(-time.Minute),
		FirstIssued: time.Now().Add(-2 * time.Minute),
	}
	s.mu.Unlock()

	// Boundary-inside entry: Deadline 2 s in the future — must still resolve.
	const insideToken = "expiry-pin-inside-token"
	insideLabel := middleware.PreviewLabelForToken(insideToken)
	s.mu.Lock()
	s.byToken[insideToken] = &ServedEntry{
		AgentID:     "expiry-pin-inside-agent",
		AbsDir:      t.TempDir(),
		Deadline:    time.Now().Add(2 * time.Second),
		FirstIssued: time.Now(),
	}
	s.mu.Unlock()

	// Positive control: an ordinary live registration.
	liveToken, _, err := s.Register("expiry-pin-live-agent", t.TempDir(), time.Hour)
	require.NoError(t, err)
	liveLabel := middleware.PreviewLabelForToken(liveToken)

	t.Run("expired_entry_still_in_map_does_not_resolve_by_label", func(t *testing.T) {
		// Precondition: the entry is genuinely still in the map — the lookup's
		// nil must come from the expiry check, not from a missing entry.
		s.mu.RLock()
		_, present := s.byToken[expiredToken]
		s.mu.RUnlock()
		require.True(t, present,
			"fixture binding: the expired entry must still be in the map (the janitor runs on a 30 s tick)")

		assert.Nil(t, s.LookupByLabel(expiredLabel),
			"FR-029: an expired-but-not-yet-janitor-cleaned registration must NOT resolve by label — "+
				"expiry makes the token unresolvable, so the label is unresolvable too")
	})

	t.Run("deadline_two_seconds_out_still_resolves", func(t *testing.T) {
		got := s.LookupByLabel(insideLabel)
		require.NotNil(t, got,
			"an entry inside its deadline must still resolve — only expiry retires the label")
		assert.Equal(t, "expiry-pin-inside-agent", got.AgentID)
	})

	t.Run("registered_live_entry_resolves_control", func(t *testing.T) {
		got := s.LookupByLabel(liveLabel)
		require.NotNil(t, got,
			"fixture binding: the label lookup itself must work — the nil above is caused by expiry alone")
		assert.Equal(t, "expiry-pin-live-agent", got.AgentID)
	})
}
