// Package programstore provides read-only assertions for S1 test fixtures.
package programstore

import (
	"testing"

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
		value, err := s.Get(name)
		require.NoError(t, err, "snapshot decrypts %s", name)
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
