package secretrequest_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/secretrequest"
)

func unconfirmed(t *testing.T, reg *secretrequest.Registry, sessionID, requestID string) bool {
	t.Helper()
	for _, card := range reg.Snapshot(matrixOwner, sessionID) {
		if card.RequestId == requestID {
			return card.ContinuationUnconfirmed != nil && *card.ContinuationUnconfirmed
		}
	}
	t.Fatalf("instrument: request %s is not in the snapshot", requestID)
	return false
}

// TestContinuationUnconfirmed_SnapshotCarriesTheDeliveryFact is Train 2 gate
// r1 M13 (SFH T2-SF-02; architect ruling arch-m13-ruling.md): a committed
// submission whose continuation was not delivered must be visible to a fresh
// tab (the snapshot), survive a restart, and clear — durably — once a retry
// delivers it. An explicit Stop clears it too.
func TestContinuationUnconfirmed_SnapshotCarriesTheDeliveryFact(t *testing.T) {
	f := newMatrixEnv(t)
	sid := f.newSession("omnipus", "")
	rec := f.create(sid, "omnipus", providerInput())
	f.resumeErr = errors.New("continuation transport is down")
	out, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
	require.Error(t, err, "instrument: the continuation dispatch failed")
	require.Equal(t, "submitted", out.Card.State)
	require.True(t, unconfirmed(t, f.reg, sid, rec.Card.RequestId), "a failed continuation is flagged on the snapshot")

	// Boot order (WSHandler.wireSecretRequests): Recover, then RetryResumes.
	restarted := secretrequest.NewRegistry(f.options())
	require.NoError(t, restarted.Recover())
	require.True(t, unconfirmed(t, restarted, sid, rec.Card.RequestId), "the flag survives a restart")
	require.Error(t, restarted.RetryResumes(), "instrument: the boot retry still fails")
	require.True(t, unconfirmed(t, restarted, sid, rec.Card.RequestId), "a failed boot retry keeps it")

	f.resumeErr = nil
	require.NoError(t, restarted.RetryResumes())
	require.False(t, unconfirmed(t, restarted, sid, rec.Card.RequestId), "a delivered retry clears the flag")
	again := secretrequest.NewRegistry(f.options())
	require.NoError(t, again.Recover())
	require.False(t, unconfirmed(t, again, sid, rec.Card.RequestId), "the clear is persisted")
}

func TestContinuationUnconfirmed_StopClearsIt(t *testing.T) {
	f := newMatrixEnv(t)
	sid := f.newSession("omnipus", "")
	rec := f.create(sid, "omnipus", providerInput())
	f.resumeErr = errors.New("continuation transport is down")
	_, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
	require.Error(t, err)
	require.True(t, unconfirmed(t, f.reg, sid, rec.Card.RequestId))
	require.NoError(t, f.reg.Stop(sid))
	require.False(t, unconfirmed(t, f.reg, sid, rec.Card.RequestId), "a deliberate Stop is not a failed delivery")
}
