// Package programstore provides read-only assertions for S1 test fixtures.
package programstore

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// State includes private selection/options and known receipts as well as values.
// Receipts have no public enumeration API: callers must supply every receipt ID
// their fixture creates, including pending card IDs whose absence is significant.
type State struct {
	Values      map[string]string
	Inventory   []credentials.CardInventoryEntry
	Metadata    map[string]credentials.CardSecret
	Generations map[string]int64
	Receipts    map[string]*credentials.CardReceipt
}

func Snapshot(t testing.TB, s *credentials.Store, receiptIDs ...string) State {
	t.Helper()
	names, err := s.List()
	require.NoError(t, err)
	out := State{Values: map[string]string{}, Generations: map[string]int64{}, Receipts: map[string]*credentials.CardReceipt{}}
	for _, name := range names {
		value, getErr := s.Get(name)
		require.NoError(t, getErr, "snapshot decrypts %s", name)
		out.Values[name] = value
		if gen, ok := s.ProgramEntryGeneration(name); ok {
			out.Generations[name] = gen
		}
	}
	out.Inventory, err = s.CardInventory()
	require.NoError(t, err)
	out.Metadata, err = s.CardSecretMetadata()
	require.NoError(t, err)
	for _, id := range receiptIDs {
		r, found, err := s.CardReceipt(id)
		require.NoError(t, err, "snapshot reads receipt %s", id)
		if found {
			out.Receipts[id] = &r
		} else {
			out.Receipts[id] = nil
		}
	}
	return out
}

// SeedCanary makes unchanged-state assertions watch a real selected private
// value, recipient metadata and historical receipt, not an empty store.
func SeedCanary(t testing.TB, s *credentials.Store) string {
	t.Helper()
	const id = "req-program-gate-canary"
	r, err := s.CommitCard(id, "sess-program-gate-canary", []credentials.CardValue{{
		BaseName: "PG_CANARY",
		Recipient: credentials.CardRecipient{Kind: credentials.KindAgentPrivate,
			WorkspaceID: "sales", AgentID: "dev", Field: "PG_CANARY"},
		Value: "canary_not_a_program_token",
	}}, time.Now())
	require.NoError(t, err)
	require.Len(t, r.References, 1)
	state := Snapshot(t, s, id)
	require.Equal(t, "canary_not_a_program_token", state.Values[r.References[0]])
	require.Equal(t, credentials.KindAgentPrivate, state.Metadata[r.References[0]].Recipient.Kind)
	require.NotNil(t, state.Receipts[id])
	selected, err := s.PrivateEnvironment("sales", "dev", false)
	require.NoError(t, err)
	require.Contains(t, selected, credentials.PrivateValue{Name: "PG_CANARY", Value: "canary_not_a_program_token"})
	return id
}

// RequireOwned refuses a no-op CommitProgramCredential as a valid Given.
func RequireOwned(t testing.TB, s *credentials.Store, ref, value, requestID, sessionID string) State {
	t.Helper()
	state := Snapshot(t, s, requestID)
	if state.Values[ref] != value || state.Metadata[ref].Recipient.Kind != credentials.KindProgram || state.Generations[ref] != 1 {
		t.Fatalf("BLOCKED: CommitProgramCredential did not publish the required value, recipient kind program and generation 1 for %s — required by I-3/FR-048/S-128/S-129", ref)
	}
	r := state.Receipts[requestID]
	if r == nil {
		t.Fatalf("BLOCKED: CommitProgramCredential did not publish its bound receipt %s in the same write — required by I-3", requestID)
	}
	require.Equal(t, requestID, r.RequestID)
	require.Equal(t, sessionID, r.SessionID)
	require.Equal(t, []string{ref}, r.References)
	require.False(t, r.SubmittedAt.IsZero(), "the receipt has a commit time")
	return state
}
