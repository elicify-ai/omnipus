package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-057 U16 (W9b) — Background shells: SessionManager.KillAllForSessions,
// the general "cascade over a set of owner ids" primitive FR-027 requires.
//
// FR-027: "ProcessSession.OwnerSessionID MUST be stamped from the child's own
// id, and KillBackgroundSessions MUST cascade over the descendant set."
// Before ADR-057, a delegated child shared the root chat session's
// transcript id, so a single exact-match KillAllForSession(rootID) reached
// the child's background shells too. ADR-057 stamps each child's
// OwnerSessionID from its OWN distinct session id (see shell.go's
// runBackground / OwnerSessionID's doc comment in session.go), so that same
// single match against only the root id would no longer reach a child's
// shells at all — silently orphaning them as detached processes on a
// chat-level Stop. KillAllForSessions is the fix: it matches against a SET
// of owner ids (the resolved descendant set — root id plus every live
// descendant's own id), a superset of KillAllForSession's single-id form.
//
// This file covers the matching/no-op boundary of that primitive with
// synthetic (fake) PIDs, following the existing established convention in
// session_kill_all_for_session_test.go (fakeUnusedPIDBase — a PID value well
// outside any real range this sandbox allocates, so killProcessGroup's
// underlying syscall reliably no-ops via ESRCH-as-success): these assertions
// are about which candidates the SET MATCHES, not about whether a real OS
// process actually dies, so a fake PID is the right (fast, deterministic,
// cross-platform) tool for this half of the coverage.
//
// The complementary half — proving a REAL background process is actually
// terminated by the cascade while an unrelated sibling's real process
// survives (the orphaned-process failure mode this unit exists to close,
// BDD-28) — lives in session_adr057_unix_test.go, which needs POSIX PID
// liveness checks and is therefore build-tagged `!windows`.
//
// Per binding rule 4 (every negative/zero-count/exclusion gate must first
// prove its search is live), every "must not be touched" assertion below is
// preceded by a positive assertion that the untouched session was actually
// registered and running in the first place.

// TestSessionManager_KillAllForSessions_MatchesAnyIDInTheSet proves the core
// cascade behavior: a call naming MULTIPLE owner ids (the descendant set)
// kills every running session owned by ANY of them, while a session owned by
// an id NOT in the set is left untouched — the multi-id generalization of
// TestSessionManager_KillAllForSession's single-id "does not affect other
// owners" regression test.
func TestSessionManager_KillAllForSessions_MatchesAnyIDInTheSet(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	rootOwned := &ProcessSession{
		ID:             "m1-root-owned",
		OwnerSessionID: "root-x",
		Status:         "running",
		PID:            fakeUnusedPIDBase + 501,
		StartTime:      time.Now().Unix(),
	}
	childOwned := &ProcessSession{
		ID:             "m2-child-owned",
		OwnerSessionID: "child-x",
		Status:         "running",
		PID:            fakeUnusedPIDBase + 502,
		StartTime:      time.Now().Unix(),
	}
	siblingOwned := &ProcessSession{
		ID:             "m3-sibling-owned",
		OwnerSessionID: "sibling-x",
		Status:         "running",
		PID:            fakeUnusedPIDBase + 503,
		StartTime:      time.Now().Unix(),
	}
	sm.Add(rootOwned)
	sm.Add(childOwned)
	sm.Add(siblingOwned)

	// Positive lower bound: all three candidates exist and are running
	// before the cascade runs.
	for _, id := range []string{rootOwned.ID, childOwned.ID, siblingOwned.ID} {
		got, err := sm.Get(id)
		require.NoError(t, err)
		require.Equal(t, "running", got.GetStatus(), "precondition: %s must start running", id)
	}

	// The descendant set is {root-x, child-x} — deliberately excludes
	// sibling-x, mirroring BDD-28's "kills a child's background shell but
	// not a sibling's" scenario at the SessionManager level.
	killed, failed := sm.KillAllForSessions([]string{"root-x", "child-x"})
	require.Equal(t, 2, killed, "both sessions owned by an id in the set must be killed")
	require.Equal(t, 0, failed)

	gotRoot, err := sm.Get(rootOwned.ID)
	require.NoError(t, err)
	assert.Equal(t, "canceled", gotRoot.GetStatus())

	gotChild, err := sm.Get(childOwned.ID)
	require.NoError(t, err)
	assert.Equal(t, "canceled", gotChild.GetStatus())

	gotSibling, err := sm.Get(siblingOwned.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", gotSibling.GetStatus(),
		"a session owned by an id NOT in the set must be left untouched")
}

// TestSessionManager_KillAllForSessions_EmptyOrNonMatchingSetsAreNoOps covers
// the boundary the plural primitive must get right without ever touching an
// unrelated running session: nil, empty, all-empty-string, and
// non-matching sets are all no-ops.
func TestSessionManager_KillAllForSessions_EmptyOrNonMatchingSetsAreNoOps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sessionIDs []string
	}{
		{name: "nil set", sessionIDs: nil},
		{name: "empty set", sessionIDs: []string{}},
		{name: "set of only empty strings", sessionIDs: []string{"", ""}},
		{name: "set with ids that match nothing", sessionIDs: []string{"no-such-owner-1", "no-such-owner-2"}},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sm := NewSessionManager()
			sess := &ProcessSession{
				ID:             "unrelated-session",
				OwnerSessionID: "owner-real",
				Status:         "running",
				PID:            fakeUnusedPIDBase + 600 + i,
				StartTime:      time.Now().Unix(),
			}
			sm.Add(sess)

			// Positive lower bound: the candidate genuinely exists and is
			// running before the no-op/empty-set case is asserted — a search
			// that never found a real registered session would otherwise
			// pass this "left untouched" check vacuously.
			before, err := sm.Get(sess.ID)
			require.NoError(t, err)
			require.Equal(t, "running", before.GetStatus(), "precondition: candidate must start running")

			killed, failed := sm.KillAllForSessions(tt.sessionIDs)
			assert.Equal(t, 0, killed, "case %q must kill nothing", tt.name)
			assert.Equal(t, 0, failed, "case %q must fail nothing", tt.name)

			after, err := sm.Get(sess.ID)
			require.NoError(t, err)
			assert.Equal(t, "running", after.GetStatus(),
				"case %q must never touch an unrelated running session", tt.name)
		})
	}
}

// TestSessionManager_KillAllForSession_StillDelegatesToPluralForm is a thin
// regression guard for the KillAllForSession -> KillAllForSessions refactor:
// the single-id convenience form must still kill exactly what it always has,
// now that its body is a one-element-slice call into the plural primitive.
// The broader single-id behavior (log content, failure counting, counter
// increments) remains covered in session_kill_all_for_session_test.go; this
// only pins that the delegation itself did not silently change behavior.
func TestSessionManager_KillAllForSession_StillDelegatesToPluralForm(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	owned := &ProcessSession{
		ID:             "single-id-owned",
		OwnerSessionID: "owner-single",
		Status:         "running",
		PID:            fakeUnusedPIDBase + 700,
		StartTime:      time.Now().Unix(),
	}
	unrelated := &ProcessSession{
		ID:             "single-id-unrelated",
		OwnerSessionID: "owner-other",
		Status:         "running",
		PID:            fakeUnusedPIDBase + 701,
		StartTime:      time.Now().Unix(),
	}
	sm.Add(owned)
	sm.Add(unrelated)

	killed, failed := sm.KillAllForSession("owner-single")
	require.Equal(t, 1, killed)
	require.Equal(t, 0, failed)

	gotOwned, err := sm.Get(owned.ID)
	require.NoError(t, err)
	assert.Equal(t, "canceled", gotOwned.GetStatus())

	gotUnrelated, err := sm.Get(unrelated.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", gotUnrelated.GetStatus())
}
