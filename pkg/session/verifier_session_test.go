// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the verifier session primitives (ADR-052 FR-036, US-13
// Acceptance 1+6):
//   - SessionTypeVerifier constant + IsValidSessionType
//   - NewVerifierSession: stamps type/owner + round-trips through GetMeta
//     and ListSessions
//
// Traces to: pkg/session/unified.go — SessionTypeVerifier, NewVerifierSession.

package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionTypeVerifier_IsValid asserts that the new verifier type passes
// IsValidSessionType and that its string value equals "verifier" (the wire
// enum value fixed by contracts/components/schemas/Session.yaml — the SPA's
// generated zod schema must match this exact literal).
func TestSessionTypeVerifier_IsValid(t *testing.T) {
	assert.True(t, IsValidSessionType(SessionTypeVerifier), "verifier must be valid")
	assert.Equal(t, "verifier", string(SessionTypeVerifier))
	// Regression: every pre-existing type must remain valid.
	assert.True(t, IsValidSessionType(SessionTypeChat))
	assert.True(t, IsValidSessionType(SessionTypeTask))
	assert.True(t, IsValidSessionType(SessionTypeChannel))
	assert.True(t, IsValidSessionType(SessionTypeScheduled))
	assert.True(t, IsValidSessionType(SessionTypeHeartbeat))
	// Bogus type must still be rejected.
	assert.False(t, IsValidSessionType(UnifiedSessionType("bogus")))
}

// TestNewVerifierSession_StampsTypeAndOwner verifies that a freshly created
// verifier session is stamped Type=SessionTypeVerifier and owned by the
// supplied verifier agent id (e.g. the Judge System Agent), matching
// NewHeartbeatSession/NewScheduledSession's AgentID/AgentIDs/ActiveAgentID
// stamping convention.
func TestNewVerifierSession_StampsTypeAndOwner(t *testing.T) {
	store := newTestStore(t)

	const judgeID = "judge"

	meta, err := store.NewVerifierSession(judgeID)
	require.NoError(t, err)
	require.NotNil(t, meta)

	assert.Equal(t, SessionTypeVerifier, meta.Type, "type must be verifier")
	assert.Equal(t, judgeID, meta.AgentID, "AgentID must be the verifier agent")
	assert.Equal(t, judgeID, meta.ActiveAgentID, "ActiveAgentID must be the verifier agent")
	assert.Equal(t, []string{judgeID}, meta.AgentIDs, "AgentIDs must be [judgeID]")
	assert.NotEmpty(t, meta.ID, "session must have a non-empty ID")
}

// TestNewVerifierSession_RoundTrips verifies that a session created by
// NewVerifierSession survives a GetMeta round-trip with its type intact —
// the persisted meta.json must carry "verifier", not silently coerce to
// "chat" or drop the field.
func TestNewVerifierSession_RoundTrips(t *testing.T) {
	store := newTestStore(t)

	created, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	got, err := store.GetMeta(created.ID)
	require.NoError(t, err)

	assert.Equal(t, created.ID, got.ID, "ID must survive round-trip")
	assert.Equal(t, SessionTypeVerifier, got.Type, "type must be verifier after round-trip")
	assert.Equal(t, "judge", got.AgentID, "AgentID must survive round-trip")
}

// TestNewVerifierSession_DistinctIDs verifies that two calls to
// NewVerifierSession produce two distinct sessions — ADR-052 mandates a
// FRESH session per adjudication (never reused across calls, R2-24), so
// this is a "New", not "GetOrCreate", primitive.
func TestNewVerifierSession_DistinctIDs(t *testing.T) {
	store := newTestStore(t)

	a, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	b, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	assert.NotEqual(t, a.ID, b.ID, "each verifier adjudication gets a distinct session")
}

// TestNewVerifierSession_ListSessions verifies that a verifier session
// appears in the raw UnifiedStore.ListSessions with its type intact. Per
// ADR-052/FR-036, default EXCLUSION from the wire response is a gateway
// (HTTP-layer) concern (pkg/gateway/rest.go listSessions), not a
// pkg/session concern — ListSessions itself must return everything
// unfiltered so the gateway (and the usage/cost aggregator, which
// deliberately applies no type filter — SC-014) can see it.
func TestNewVerifierSession_ListSessions(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	all, err := store.ListSessions()
	require.NoError(t, err)
	require.Len(t, all, 1, "ListSessions must NOT filter out verifier sessions — that is a gateway-layer concern")

	assert.Equal(t, meta.ID, all[0].ID)
	assert.Equal(t, SessionTypeVerifier, all[0].Type)
}

// TestNewVerifierSession_CoexistsWithOtherTypes verifies ListSessions
// returns a mix of verifier and non-verifier sessions unfiltered, so a
// downstream consumer (gateway or usage aggregator) can apply its own
// policy rather than pkg/session silently dropping anything.
func TestNewVerifierSession_CoexistsWithOtherTypes(t *testing.T) {
	store := newTestStore(t)

	chatMeta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	verifierMeta, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	all, err := store.ListSessions()
	require.NoError(t, err)
	require.Len(t, all, 2)

	byID := make(map[string]*UnifiedMeta, len(all))
	for _, m := range all {
		byID[m.ID] = m
	}
	require.Contains(t, byID, chatMeta.ID)
	require.Contains(t, byID, verifierMeta.ID)
	assert.Equal(t, SessionTypeChat, byID[chatMeta.ID].Type)
	assert.Equal(t, SessionTypeVerifier, byID[verifierMeta.ID].Type)
}
