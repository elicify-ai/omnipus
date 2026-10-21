package channels

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testInstance = "whatsapp"

var (
	ownerA = &PairingOwner{SessionID: "chat-a", RequestID: "req-a"}
	ownerB = &PairingOwner{SessionID: "chat-b", RequestID: "req-b"}
)

func noCancel(string) func(context.Context) error {
	return func(context.Context) error { return nil }
}

// startOwned admits owner's connect, commits it and activates it.
func startOwned(t *testing.T, s *PairingAttempts, owner *PairingOwner) PairingAttempt {
	t.Helper()
	admitted, err := s.Admit(testInstance, PairingAdmissionConnect, owner)
	require.NoError(t, err)
	require.True(t, s.MarkReady(testInstance, admitted.AttemptID, "rev-1"))
	active, err := s.Activate(testInstance, s.TakeReady(testInstance, "rev-1"), noCancel)
	require.NoError(t, err)
	require.Equal(t, admitted.AttemptID, active.AttemptID, "a start consumes exactly its admitted attempt")
	return active
}

// TestPairingAttempts_RetryIsAlwaysANewAttemptOfTheEndedOwner pins M1, M3, M4:
// only the owner of the latest ended attempt may Retry, and its Retry is a new
// attempt id, never the old one.
func TestPairingAttempts_RetryIsAlwaysANewAttemptOfTheEndedOwner(t *testing.T) {
	s := NewPairingAttempts()
	a := startOwned(t, s, ownerA)
	_, err := s.Admit(testInstance, PairingAdmissionRetry, ownerA)
	require.ErrorIs(t, err, ErrPairingConflict, "an active attempt is not claimable, even by its owner")
	_, ok := s.Complete(testInstance, a.AttemptID, PairingOutcomeTimeout)
	require.True(t, ok)

	_, err = s.Admit(testInstance, PairingAdmissionRetry, ownerB)
	require.ErrorIs(t, err, ErrPairingConflict, "another chat cannot retry A's pairing")
	retry, err := s.Admit(testInstance, PairingAdmissionRetry, ownerA)
	require.NoError(t, err)
	require.NotEqual(t, a.AttemptID, retry.AttemptID, "a Retry is a new attempt")
	require.Equal(t, *ownerA, *retry.Owner)
	_, err = s.Admit(testInstance, PairingAdmissionRetry, ownerA)
	require.ErrorIs(t, err, ErrPairingConflict, "the pending Retry is not claimable again")
}

// TestPairingAttempts_ANewerUnownedAttemptEndsOlderEligibility is gate r3
// T2-R3-S1 / CR-T2-R3-02 (M3): after A's pairing ended, a newer attempt from
// Connectors — active, and then ended — leaves A nothing to Retry.
func TestPairingAttempts_ANewerUnownedAttemptEndsOlderEligibility(t *testing.T) {
	s := NewPairingAttempts()
	a := startOwned(t, s, ownerA)
	_, _ = s.Complete(testInstance, a.AttemptID, PairingOutcomeTimeout)

	connectors, err := s.Activate(testInstance, "", noCancel)
	require.NoError(t, err)
	require.Nil(t, connectors.Owner, "a Connectors start is unowned")
	_, err = s.Admit(testInstance, PairingAdmissionRetry, ownerA)
	require.ErrorIs(t, err, ErrPairingConflict, "an old Retry cannot adopt a running unowned pairing")

	_, _ = s.Complete(testInstance, connectors.AttemptID, PairingOutcomeTimeout)
	_, err = s.Admit(testInstance, PairingAdmissionRetry, ownerA)
	require.ErrorIs(t, err, ErrPairingConflict, "a later unowned timeout does not restore an older chat")
}

// TestPairingAttempts_ConcurrentAdmissionsHaveOneWinner is gate r3 T2 /
// CHECK-r3 R3-F2 (M2): the first admission is held inside the critical section
// between its check and its publication while competing admissions run. With
// the lock, exactly one wins and the winner's owner is intact.
func TestPairingAttempts_ConcurrentAdmissionsHaveOneWinner(t *testing.T) {
	for name, second := range map[string]struct {
		kind  PairingAdmission
		owner *PairingOwner
	}{
		"retry vs connect":    {PairingAdmissionConnect, ownerB},
		"duplicate retries":   {PairingAdmissionRetry, ownerA},
		"retry vs connectors": {PairingAdmissionEnable, nil},
	} {
		t.Run(name, func(t *testing.T) {
			s := NewPairingAttempts()
			a := startOwned(t, s, ownerA)
			_, _ = s.Complete(testInstance, a.AttemptID, PairingOutcomeTimeout)

			inside := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			s.afterCheck = func() {
				once.Do(func() {
					close(inside)
					<-release
				})
			}
			type result struct {
				rec PairingAttempt
				err error
			}
			first := make(chan result, 1)
			go func() {
				rec, err := s.Admit(testInstance, PairingAdmissionRetry, ownerA)
				first <- result{rec, err}
			}()
			<-inside
			secondDone := make(chan result, 1)
			go func() {
				rec, err := s.Admit(testInstance, second.kind, second.owner)
				secondDone <- result{rec, err}
			}()
			select {
			case r := <-secondDone:
				// Only possible when the check and publication are not one
				// critical section: the competitor saw the old state.
				close(release)
				<-first
				t.Fatalf("a competing admission completed while the first was inside its check-and-publish step (err=%v)", r.err)
			case <-time.After(300 * time.Millisecond):
			}
			close(release)
			r1 := <-first
			r2 := <-secondDone
			require.NoError(t, r1.err, "the first admission wins")
			require.ErrorIs(t, r2.err, ErrPairingConflict, "the competitor loses")
			latest, ok := s.Latest(testInstance)
			require.True(t, ok)
			require.Equal(t, r1.rec.AttemptID, latest.AttemptID)
			require.Equal(t, *ownerA, *latest.Owner, "the winner's owner is not torn")
		})
	}
}

// TestPairingAttempts_StopIsPinnedToItsAttempt is gate r3 T2-R3-S2 (M6, M8):
// a Stop selection captured for attempt A is a no-op on a successor — even one
// with A's own session and request ids — and an old attempt's events never
// change the successor.
func TestPairingAttempts_StopIsPinnedToItsAttempt(t *testing.T) {
	s := NewPairingAttempts()
	a := startOwned(t, s, ownerA)
	selected := s.OwnedBy("chat-a")
	require.Len(t, selected, 1)
	require.Equal(t, a.AttemptID, selected[0].AttemptID)

	_, _ = s.Complete(testInstance, a.AttemptID, PairingOutcomeTimeout)
	retry, err := s.Admit(testInstance, PairingAdmissionRetry, ownerA)
	require.NoError(t, err)
	require.True(t, s.MarkReady(testInstance, retry.AttemptID, "rev-1"))
	successor, err := s.Activate(testInstance, s.TakeReady(testInstance, "rev-1"), noCancel)
	require.NoError(t, err)
	require.Equal(t, *ownerA, *successor.Owner, "the successor has the same session and request ids")

	_, ok := s.BeginStop(selected[0])
	require.False(t, ok, "a selection of attempt A cannot stop its successor")
	_, ok = s.Complete(testInstance, a.AttemptID, PairingOutcomeError)
	require.False(t, ok, "a late event of attempt A cannot end the successor")
	require.True(t, s.IsCurrent(testInstance, successor.AttemptID), "the successor is untouched")

	fresh := s.OwnedBy("chat-a")
	require.Len(t, fresh, 1)
	stop, ok := s.BeginStop(fresh[0])
	require.True(t, ok, "control: a selection of the current attempt stops it")
	require.NotNil(t, stop.Cancel)
	_, err = s.Activate(testInstance, "", noCancel)
	require.ErrorIs(t, err, ErrPairingStopping, "no successor starts while the client is being torn down")
	done, ok := s.FinishStop(testInstance, successor.AttemptID, nil)
	require.True(t, ok)
	require.Equal(t, PairingOutcomeStopped, done.Outcome)
}

// TestPairingAttempts_FailedTeardownStaysStopping pins M7: a teardown that did
// not finish is not reported stopped and keeps excluding a successor.
func TestPairingAttempts_FailedTeardownStaysStopping(t *testing.T) {
	s := NewPairingAttempts()
	a := startOwned(t, s, ownerA)
	stop, ok := s.BeginStop(s.OwnedBy("chat-a")[0])
	require.True(t, ok)
	_, ok = s.FinishStop(testInstance, stop.Attempt.AttemptID, context.DeadlineExceeded)
	require.False(t, ok)
	phase, _ := s.Phase(testInstance, a.AttemptID)
	require.Equal(t, PairingPhaseStopping, phase)
}

// TestPairingAttempts_RestartInterruptedIsRetryableByAnyChat is the founder's
// restart rule: a pairing a restart cut off is unowned and interrupted, and a
// chat Retry starts a new attempt of its own.
func TestPairingAttempts_RestartInterruptedIsRetryableByAnyChat(t *testing.T) {
	s := NewPairingAttempts()
	cut := s.RecordRestartInterrupted(testInstance)
	require.Nil(t, cut.Owner)
	require.Equal(t, PairingOutcomeInterrupted, cut.Outcome)
	retry, err := s.Admit(testInstance, PairingAdmissionRetry, ownerB)
	require.NoError(t, err)
	require.Equal(t, *ownerB, *retry.Owner)
	require.NotEqual(t, cut.AttemptID, retry.AttemptID)
}

// TestPairingAttempts_ReleaseAndReadyOnlyMoveTheirOwnAttempt pins M8/M10/M14:
// MarkReady and Release act only on the named pending/ready attempt, a
// released owned attempt stays retryable, and a consumed one is not released.
func TestPairingAttempts_ReleaseAndReadyOnlyMoveTheirOwnAttempt(t *testing.T) {
	s := NewPairingAttempts()
	admitted, err := s.Admit(testInstance, PairingAdmissionConnect, ownerA)
	require.NoError(t, err)
	require.False(t, s.MarkReady(testInstance, "pair_other", "rev-1"))
	require.True(t, s.Release(testInstance, admitted.AttemptID, PairingOutcomeInterrupted))
	_, err = s.Admit(testInstance, PairingAdmissionRetry, ownerA)
	require.NoError(t, err, "a released owned admission stays retryable")

	b := NewPairingAttempts()
	active := startOwned(t, b, ownerA)
	require.False(t, b.Release(testInstance, active.AttemptID, PairingOutcomeInterrupted), "a started attempt is not released")
}

// TestPairingAttempts_DeleteClosesAdmissionAndForgets pins M12.
func TestPairingAttempts_DeleteClosesAdmissionAndForgets(t *testing.T) {
	s := NewPairingAttempts()
	a := startOwned(t, s, ownerA)
	_, _ = s.Complete(testInstance, a.AttemptID, PairingOutcomeTimeout)
	s.BeginDelete(testInstance)
	_, err := s.Admit(testInstance, PairingAdmissionRetry, ownerA)
	require.ErrorIs(t, err, ErrPairingConflict, "admission is closed while the connector is deleted")
	s.EndDelete(testInstance, true)
	_, ok := s.Latest(testInstance)
	require.False(t, ok, "a deleted connector keeps no attempts")
	_, err = s.Admit(testInstance, PairingAdmissionRetry, ownerA)
	require.ErrorIs(t, err, ErrPairingConflict, "nothing is retryable on a recreated connector")
}
