package channels

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// CHECK R7-F1; DESIGN §3 Stop freezes the exact handle. This durable detector
// follows the auditor's post-BeginStop/pre-invocation retirement schedule.
// It is not T6 and makes no native late-client disposal claim.
func TestPairingStop_SelectedHandleSurvivesMetadataRetirement(t *testing.T) {
	store := NewPairingAttempts()
	owner := &PairingOwner{SessionID: "pinned-chat", RequestID: "pinned-request"}
	a, err := store.Admit("whatsapp.pinned", PairingAdmissionConnect, owner)
	require.NoError(t, err)
	require.True(t, store.MarkReady(a.Instance, a.AttemptID, "captured-write"))
	calls := 0
	active, err := store.Activate(a.Instance, a.AttemptID, func(id string) func(context.Context) error {
		require.Equal(t, a.AttemptID, id)
		return func(context.Context) error { calls++; return nil }
	})
	require.NoError(t, err)
	selected, ok := store.BeginStop(active)
	require.True(t, ok)
	require.NotNil(t, selected.Cancel)

	store.BeginDelete(a.Instance)
	store.EndDelete(a.Instance, true)
	_, current := store.Latest(a.Instance)
	require.False(t, current, "instrument: latest-by-name cannot resolve the retired resource")
	retained, exists := store.Attempt(a.Instance, a.AttemptID)
	require.True(t, exists)
	require.Equal(t, PairingPhaseStopping, retained.Phase)
	require.Equal(t, owner, retained.Owner)

	require.NoError(t, selected.Cancel(context.Background()))
	require.Equal(t, 1, calls, "the selected captured callback must run once despite metadata retirement")
	done, finished := store.FinishStop(a.Instance, a.AttemptID, nil)
	require.True(t, finished)
	require.Equal(t, PairingOutcomeStopped, done.Outcome)
	require.Equal(t, owner, done.Owner)
}
