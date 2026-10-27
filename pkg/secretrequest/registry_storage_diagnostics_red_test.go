package secretrequest_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"syscall"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SF-05 typed-cause control: identical arbitrary Error strings must produce
// DIFFERENT safe categories by inspecting standard typed/unwrap causes only.
// Actual open/commit/state/recovery I/O faults are separate rows, not replaced
// by this store-opener edge control.
func TestWU4RegistryStoreOpenTypedCausesDiagnosedWithoutRawErrorEcho(t *testing.T) {
	cases := []struct {
		category string
		cause    error
	}{
		{category: "permission_denied", cause: os.ErrPermission},
		{category: "store_locked", cause: credentials.ErrStoreLocked},
		{category: "disk_full", cause: syscall.ENOSPC},
		{category: "invalid_metadata", cause: &json.SyntaxError{Offset: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.category, func(t *testing.T) {
			c := newRegistryFaultCapture(t)
			var openFault error
			opts := c.options(c.f.sessions, c.f.store)
			opts.Credentials = func() (*credentials.Store, error) {
				if openFault != nil {
					return nil, openFault
				}
				return c.f.store, nil
			}
			c.f.reg = secretrequest.NewRegistry(opts)
			c.f.reg.SetSink(c.notify)
			witness, before := registryDiagnosticWitness(t, c)
			pending := c.f.pending()
			openFault = registryOpaqueCauseError{cause: tc.cause}
			require.Equal(t, registryDiagnosticRawText, openFault.Error(), "all cases must have identical, non-classifying raw text")
			require.ErrorIs(t, openFault, tc.cause, "instrument must retain the typed cause despite opaque text")
			out, err := c.f.reg.Submit(pending.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": registryDiagnosticValue}))
			requireMatrixFailure(t, err, http.StatusInternalServerError, "")
			assertRegistryKnownPending(t, c, pending, out, err)
			assertRegistryDiagnostic(t, c.logs(t), pending.Card.RequestId, pending.Card.SessionId, tc.category, "open")
			assertRegistryNoNewStorage(t, c, pending, witness, before)
			c.assertValueFree(t, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
			openFault = nil
			registryDiagnosticRepairSubmit(t, c, pending)
			t.Logf("typed store-open cause %s checked without raw-error parsing; real explicit submit succeeds after repair", tc.category)
		})
	}
}

// This is a REAL locked Store.CommitCard boundary, not a callback returning a
// preselected failure. The healthy witness proves existing ciphertext survives.
func TestWU4RegistryLockedCommitDiagnosesCauseAndKeepsPending(t *testing.T) {
	c := newRegistryFaultCapture(t)
	witness, before := registryDiagnosticWitness(t, c)
	pending := c.f.pending()
	c.f.store.Close()
	_, _, probeErr := c.f.store.CardReceipt(pending.Card.RequestId)
	require.ErrorIs(t, probeErr, credentials.ErrStoreLocked, "instrument: store is actually locked")
	out, err := c.f.reg.Submit(pending.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": registryDiagnosticValue}))
	requireMatrixFailure(t, err, http.StatusInternalServerError, "")
	assertRegistryKnownPending(t, c, pending, out, err)
	assertRegistryDiagnostic(t, c.logs(t), pending.Card.RequestId, pending.Card.SessionId, "store_locked", "commit")
	afterFailure, readErr := os.ReadFile(c.f.store.Path())
	require.NoError(t, readErr)
	assert.Equal(t, before, afterFailure)
	registryUnlockAfterFault(t, c.f.store)
	assertRegistryNoNewStorage(t, c, pending, witness, before)
	registryDiagnosticRepairSubmit(t, c, pending)
}

// The public opener executes real credential-file probing and key installation.
// A directory at the original file path forces an actual read error, including
// on root-run CI; no ineffective chmod or timing-based race is used.
func TestWU4RegistryStoreOpenIOFaultDiagnosesContextAndKeepsPending(t *testing.T) {
	c := newRegistryFaultCapture(t)
	opts := c.options(c.f.sessions, c.f.store)
	opts.Credentials = func() (*credentials.Store, error) {
		store := credentials.NewStore(c.f.store.Path())
		t.Cleanup(store.Close)
		if err := store.UnlockWithKey(bytes.Repeat([]byte{0x44}, 32)); err != nil {
			return nil, err
		}
		return store, nil
	}
	c.f.reg = secretrequest.NewRegistry(opts)
	c.f.reg.SetSink(c.notify)
	witness, before := registryDiagnosticWitness(t, c)
	pending := c.f.pending()
	repair := obstructRegistryCredentialFile(t, c)
	probe := credentials.NewStore(c.f.store.Path())
	t.Cleanup(probe.Close)
	probeErr := probe.UnlockWithKey(bytes.Repeat([]byte{0x44}, 32))
	var ioCause *os.PathError
	require.ErrorAs(t, probeErr, &ioCause, "instrument: real store open must fail at the I/O boundary")
	out, err := c.f.reg.Submit(pending.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": registryDiagnosticValue}))
	requireMatrixFailure(t, err, http.StatusInternalServerError, "")
	assertRegistryKnownPending(t, c, pending, out, err)
	assertRegistryIODiagnostic(t, c.logs(t), pending.Card.RequestId, pending.Card.SessionId, "open")
	repair()
	assertRegistryNoNewStorage(t, c, pending, witness, before)
	registryDiagnosticRepairSubmit(t, c, pending)
}

func obstructRegistryCredentialFile(t *testing.T, c *registryFaultCapture) func() {
	t.Helper()
	path := c.f.store.Path()
	preserved := path + ".diagnostic-cut-preserved"
	require.NoError(t, os.Rename(path, preserved))
	require.NoError(t, os.Mkdir(path, 0o700))
	active := true
	repair := func() {
		if active {
			require.NoError(t, os.Remove(path), "remove only the empty test obstruction")
			require.NoError(t, os.Rename(preserved, path), "restore original ciphertext/bindings/receipts intact")
			active = false
		}
	}
	t.Cleanup(repair)
	return repair
}

// Standard error matching is part of the instrument contract, not a production
// type oracle: a raw message cannot distinguish these underlying causes.
func TestWU4RegistryOpaqueCauseControlRetainsTypeWithoutSecretTextParsing(t *testing.T) {
	permission := registryOpaqueCauseError{cause: os.ErrPermission}
	locked := registryOpaqueCauseError{cause: credentials.ErrStoreLocked}
	require.Equal(t, permission.Error(), locked.Error())
	require.True(t, errors.Is(permission, os.ErrPermission))
	require.False(t, errors.Is(permission, credentials.ErrStoreLocked))
	require.True(t, errors.Is(locked, credentials.ErrStoreLocked))
	require.False(t, errors.Is(locked, os.ErrPermission))
}
