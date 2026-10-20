package secretrequest_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/secretrequest"
)

// TestRetryResumes_FailedSessionCheckFlagsTheContinuation is Train 2 gate r2
// T2-SF-02: a submitted request recovered after a restart carries no
// continuation flag. If the boot retry cannot even read whether the session
// was stopped, the continuation is unconfirmed: a fresh client must see the
// warning, never an ordinary "Saved". Repairing the check delivers it once and
// clears the flag; a stopped session is still never flagged.
func TestRetryResumes_FailedSessionCheckFlagsTheContinuation(t *testing.T) {
	f := newMatrixEnv(t)
	sid := f.newSession("omnipus", "")
	rec := f.create(sid, "omnipus", providerInput())
	_, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
	require.NoError(t, err)
	require.False(t, unconfirmed(t, f.reg, sid, rec.Card.RequestId), "instrument: a delivered submission is not flagged")

	checkErr := errors.New("session lifecycle unreadable")
	opts := f.options()
	stoppedCheck := opts.Stopped
	opts.Stopped = func(id string) (bool, error) {
		if checkErr != nil {
			return false, checkErr
		}
		return stoppedCheck(id)
	}
	restarted := secretrequest.NewRegistry(opts)
	require.NoError(t, restarted.Recover())
	require.False(t, unconfirmed(t, restarted, sid, rec.Card.RequestId), "instrument: recovery alone carries no flag")
	require.Error(t, restarted.RetryResumes(), "instrument: the session check fails")
	require.True(t, unconfirmed(t, restarted, sid, rec.Card.RequestId),
		"a failed session check leaves the continuation unconfirmed — the client must warn")

	attempts := len(f.attempts)
	checkErr = nil
	require.NoError(t, restarted.RetryResumes())
	require.Equal(t, attempts+1, len(f.attempts), "the repaired retry delivers the continuation exactly once")
	require.False(t, unconfirmed(t, restarted, sid, rec.Card.RequestId), "a delivered retry clears the flag")

	stoppedSid := f.newSession("omnipus", "")
	stoppedRec := f.create(stoppedSid, "omnipus", providerInput())
	_, err = f.reg.Submit(stoppedRec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
	require.NoError(t, err)
	f.stopped[stoppedSid] = true
	again := secretrequest.NewRegistry(f.options())
	require.NoError(t, again.Recover())
	require.NoError(t, again.RetryResumes())
	require.False(t, unconfirmed(t, again, stoppedSid, stoppedRec.Card.RequestId), "a stopped session's veto is never a warning")
}
