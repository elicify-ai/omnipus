package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestPairingStop_CompletedTurnStopStillEndsThePairing is Train 2 gate r1
// CR-T2-03: connect_channel starts the pairing, returns and the turn
// completes (lifecycle Completed). A later Stop of that chat has no live turn
// to cancel, so it never reaches RequestCancel; it must still end the
// in-flight pairing with a correlated stopped frame (BDD-8.8, FR-081).
func TestPairingStop_CompletedTurnStopStillEndsThePairing(t *testing.T) {
	f := newPairingStopFixture(t)
	lifecycle := f.al.GetSessionLifecycleStore()
	require.NotNil(t, lifecycle, "instrument: the real loop wires a lifecycle store")
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID: f.mainID, Generation: 1, State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: "omnipus",
	}))

	f.stopChat(f.mainID)

	select {
	case <-f.fake.stopCalled:
	case <-time.After(10 * time.Second):
		t.Fatal("a Stop after the turn completed never reached StopPairing (CR-T2-03)")
	}
	payload := f.awaitPairingEvent("stopped")
	require.Equal(t, f.requestID, payload.RequestID)
	require.Equal(t, f.mainID, payload.SessionID)
}
