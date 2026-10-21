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

// startUnowned admits an ordinary (Connectors) enable, commits it and
// activates it: an unowned attempt, started with its own token.
func startUnowned(t *testing.T, s *PairingAttempts) PairingAttempt {
	t.Helper()
	admitted, err := s.Admit(testInstance, PairingAdmissionEnable, nil)
	require.NoError(t, err)
	require.True(t, s.MarkReady(testInstance, admitted.AttemptID, "rev-1"))
	active, err := s.Activate(testInstance, s.TakeReady(testInstance, "rev-1"), noCancel)
	require.NoError(t, err)
	return active
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

	connectors := startUnowned(t, s)
	require.Nil(t, connectors.Owner, "a Connectors start is unowned")
	_, err := s.Admit(testInstance, PairingAdmissionRetry, ownerA)
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
	_, err = s.ActivateRepair(testInstance, noCancel)
	require.ErrorIs(t, err, ErrPairingStopping, "no successor starts while the client is being torn down")
	_, err = s.Admit(testInstance, PairingAdmissionEnable, nil)
	require.ErrorIs(t, err, ErrPairingConflict, "nor is one admitted")
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
	require.ErrorIs(t, err, ErrPairingDeleting, "admission is closed while the connector is deleted")
	_, err = s.Admit(testInstance, PairingAdmissionEnable, nil)
	require.ErrorIs(t, err, ErrPairingDeleting, "an ordinary enable is refused too (gate r4 R4-A02)")
	_, err = s.ActivateRepair(testInstance, noCancel)
	require.ErrorIs(t, err, ErrPairingDeleting, "and automatic re-pairing")
	s.EndDelete(testInstance, true)
	_, ok := s.Latest(testInstance)
	require.False(t, ok, "a deleted connector keeps no attempts")
	_, err = s.Admit(testInstance, PairingAdmissionRetry, ownerA)
	require.ErrorIs(t, err, ErrPairingConflict, "nothing is retryable on a recreated connector")
}

// TestPairingAttempts_APairingStartsOnlyWithItsOwnToken is gate r4 R4-A01 /
// R4-A02 / T2-SF-13 (M2, M14): a start without its own ready token is
// refused — it can neither mint a fallback unowned attempt nor replace a
// chat's pending or ready reservation; automatic re-pairing is an admission
// that refuses a reservation too, and a delete closes token starts.
func TestPairingAttempts_APairingStartsOnlyWithItsOwnToken(t *testing.T) {
	s := NewPairingAttempts()
	_, err := s.Activate(testInstance, "", noCancel)
	require.ErrorIs(t, err, ErrPairingConflict, "no token, no pairing")
	_, ok := s.Latest(testInstance)
	require.False(t, ok, "and nothing was minted")

	_, err = s.Admit(testInstance, PairingAdmissionRetry, ownerA)
	require.ErrorIs(t, err, ErrPairingConflict, "instrument: nothing to retry")
	pending, err := s.Admit(testInstance, PairingAdmissionConnect, ownerA)
	require.NoError(t, err)
	_, err = s.ActivateRepair(testInstance, noCancel)
	require.ErrorIs(t, err, ErrPairingConflict, "re-pairing never replaces a pending reservation")
	_, err = s.Admit(testInstance, PairingAdmissionEnable, nil)
	require.ErrorIs(t, err, ErrPairingConflict, "nor does an ordinary enable")
	require.True(t, s.MarkReady(testInstance, pending.AttemptID, "rev-1"))
	_, err = s.ActivateRepair(testInstance, noCancel)
	require.ErrorIs(t, err, ErrPairingConflict, "nor a ready one")
	_, err = s.Activate(testInstance, "pair_someone_else", noCancel)
	require.ErrorIs(t, err, ErrPairingConflict, "a start consumes only its own token")
	latest, _ := s.Latest(testInstance)
	require.Equal(t, pending.AttemptID, latest.AttemptID)
	require.Equal(t, PairingPhaseReady, latest.Phase, "the winner's reservation is intact")
	require.Equal(t, *ownerA, *latest.Owner)
	require.True(t, s.IsReady(testInstance, pending.AttemptID))

	s.BeginDelete(testInstance)
	_, err = s.Activate(testInstance, pending.AttemptID, noCancel)
	require.ErrorIs(t, err, ErrPairingDeleting, "a delete in progress refuses even a token start")
	require.False(t, s.IsReady(testInstance, pending.AttemptID), "the delete revoked the reservation")
}

// TestPairingAttempts_StartFailureIsRetryableByItsChat is gate r4 T2-SF-16: a
// pairing whose start failed belongs to the same chat, which may Retry it; a
// different chat may not.
func TestPairingAttempts_StartFailureIsRetryableByItsChat(t *testing.T) {
	s := NewPairingAttempts()
	admitted, err := s.Admit(testInstance, PairingAdmissionConnect, ownerA)
	require.NoError(t, err)
	require.True(t, s.MarkReady(testInstance, admitted.AttemptID, "rev-1"))
	require.True(t, s.Release(testInstance, admitted.AttemptID, PairingOutcomeStartFailed))
	_, err = s.Admit(testInstance, PairingAdmissionRetry, ownerB)
	require.ErrorIs(t, err, ErrPairingConflict, "another chat cannot retry it")
	retry, err := s.Admit(testInstance, PairingAdmissionRetry, ownerA)
	require.NoError(t, err, "its own chat can")
	require.NotEqual(t, admitted.AttemptID, retry.AttemptID)
}

// TestPairingAttempts_FailedTeardownIsRetriedOnItsOwnHandle is gate r4
// T2-SF-14 (M7): a failed teardown stays stopping, is selected again by a
// later Stop of its chat, is retried on that attempt's own handle, and ends
// stopped once the client is disposed.
func TestPairingAttempts_FailedTeardownIsRetriedOnItsOwnHandle(t *testing.T) {
	s := NewPairingAttempts()
	calls := 0
	admitted, err := s.Admit(testInstance, PairingAdmissionConnect, ownerA)
	require.NoError(t, err)
	require.True(t, s.MarkReady(testInstance, admitted.AttemptID, "rev-1"))
	_, err = s.Activate(testInstance, admitted.AttemptID, func(string) func(context.Context) error {
		return func(context.Context) error { calls++; return nil }
	})
	require.NoError(t, err)
	stop, ok := s.BeginStop(s.OwnedBy("chat-a")[0])
	require.True(t, ok)
	_, ok = s.FinishStop(testInstance, stop.Attempt.AttemptID, context.DeadlineExceeded)
	require.False(t, ok, "a failed teardown is not reported stopped")

	again := s.OwnedBy("chat-a")
	require.Len(t, again, 1, "the stuck attempt can be selected again")
	retried, ok := s.BeginStop(again[0])
	require.True(t, ok)
	require.Equal(t, admitted.AttemptID, retried.Attempt.AttemptID)
	require.NoError(t, retried.Cancel(context.Background()))
	require.Equal(t, 1, calls, "the retry runs that attempt's own handle")
	_, ok = s.BeginStop(again[0])
	require.False(t, ok, "a retry in progress is not selected twice")
	done, ok := s.FinishStop(testInstance, admitted.AttemptID, nil)
	require.True(t, ok)
	require.Equal(t, PairingOutcomeStopped, done.Outcome)
	require.Empty(t, s.OwnedBy("chat-a"))
}

// TestPairingAttempts_AStaleReadyAttemptIsClosedByTheNextAdmission is gate r5
// R5-A05's other half: a ready attempt whose committed configuration was
// replaced can never start; the next admission at the current revision
// interrupts it (reported), while a ready attempt at the current revision
// still refuses that admission.
func TestPairingAttempts_AStaleReadyAttemptIsClosedByTheNextAdmission(t *testing.T) {
	s := NewPairingAttempts()
	admitted, err := s.Admit(testInstance, PairingAdmissionConnect, ownerA)
	require.NoError(t, err)
	require.True(t, s.MarkReady(testInstance, admitted.AttemptID, "rev-old"))
	_, stale := s.InterruptStaleReady(testInstance, "rev-old")
	require.False(t, stale, "a ready attempt at the current revision is not stale")
	_, err = s.Admit(testInstance, PairingAdmissionEnable, nil)
	require.ErrorIs(t, err, ErrPairingConflict)

	rec, stale := s.InterruptStaleReady(testInstance, "rev-new")
	require.True(t, stale)
	require.Equal(t, admitted.AttemptID, rec.AttemptID)
	require.Equal(t, PairingOutcomeInterrupted, rec.Outcome)
	_, err = s.Admit(testInstance, PairingAdmissionEnable, nil)
	require.NoError(t, err, "the stale attempt no longer blocks a new one")
}
