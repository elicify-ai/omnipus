package secretrequest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Full inventory must fail safely before orphan pruning. A real healthy
// submitted witness is made an orphan by PUBLIC session deletion; corrupting a
// different session must not turn the unknown inventory into permission to prune.
func TestWU4RegistryRecoveryMetadataFaultsDiagnoseUnknownWithoutPruningReceipts(t *testing.T) {
	for _, fault := range []string{"unreadable_file", "malformed_json", "invalid_binding"} {
		t.Run(fault, func(t *testing.T) {
			c := newRegistryFaultCapture(t)
			witness, before := registryDiagnosticWitness(t, c)
			pending := c.f.pending()
			require.NoError(t, c.f.sessions.DeleteSession(witness.Card.SessionId))
			path := filepath.Join(c.f.sessions.BaseDir(), pending.Card.SessionId, "secret_requests.json")
			original, err := os.ReadFile(path)
			require.NoError(t, err)
			var repair func()
			if fault == "unreadable_file" {
				repair = blockRegistryStatePublication(t, c, pending.Card.SessionId)
			} else {
				broken := []byte(`[{"untrusted_raw_error":"` + registryDiagnosticRawText + `",`)
				if fault == "invalid_binding" {
					rec := pending
					rec.Recipients = nil // card still has its field: real invalid persisted binding.
					rec.ReasonCode = registryDiagnosticRawText
					broken, err = json.Marshal([]secretrequest.Record{rec})
					require.NoError(t, err)
				}
				require.NoError(t, os.WriteFile(path, broken, 0o600))
				repair = func() { require.NoError(t, c.f.sessions.WriteSecretRequestState(pending.Card.SessionId, original)) }
				t.Cleanup(repair)
			}
			boot := c.fresh(t)
			recoveryErr := boot.Recover()
			require.Error(t, recoveryErr, "a real unreadable/invalid inventory must not be called recovered")
			requireValueFree(t, "real recovery error", map[string]any{"error": recoveryErr.Error()}, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
			assertRegistryRepairGuidance(t, recoveryErr.Error())
			if fault == "unreadable_file" {
				var pathCause *os.PathError
				require.ErrorAs(t, recoveryErr, &pathCause, "typed I/O cause stays available without raw text parsing")
				assertRegistryIODiagnostic(t, c.logs(t), "", pending.Card.SessionId, "recover")
			} else {
				requestID := ""
				if fault == "invalid_binding" {
					requestID = pending.Card.RequestId // decoded, allocated identity is available in this case.
				}
				assertRegistryDiagnostic(t, c.logs(t), requestID, pending.Card.SessionId, "invalid_metadata", "recover")
			}
			assertRegistryUnavailableNotInventedState(t, boot, pending.Card.RequestId)
			_, exists, receiptErr := c.f.store.CardReceipt(witness.Card.RequestId)
			require.NoError(t, receiptErr)
			assert.True(t, exists, "unknown inventory cannot justify pruning even a real orphan's receipt")
			afterFailure, readErr := os.ReadFile(c.f.store.Path())
			require.NoError(t, readErr)
			assert.Equal(t, before, afterFailure, "failed recovery must not alter ciphertext/bindings/receipts")
			assert.Empty(t, c.auditTransitions(t, pending.Card.RequestId, "submitted", "interrupted", "recovered_submitted", "recovered-submitted"), "no invented transition before reliable recovery")
			c.assertValueFree(t, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
			repair()
			nextBoot := c.fresh(t)
			require.NoError(t, nextBoot.Recover())
			recovered, readErr := nextBoot.Get(pending.Card.RequestId, matrixOwner)
			require.NoError(t, readErr)
			require.Equal(t, "interrupted", recovered.Card.State, "repaired pending metadata still has no receipt, so nothing was stored")
			require.Empty(t, recovered.Card.ReferenceNames)
			_, exists, receiptErr = c.f.store.CardReceipt(witness.Card.RequestId)
			require.NoError(t, receiptErr)
			require.False(t, exists, "only a complete repaired inventory authorizes orphan receipt cleanup")
			stored, readErr := c.f.store.Get(witness.Card.ReferenceNames[0])
			require.NoError(t, readErr)
			require.Equal(t, registryDiagnosticOld, stored, "cleanup never deletes the actual encrypted value")
			transcript, readErr := c.f.sessions.ReadTranscript(pending.Card.SessionId)
			require.NoError(t, readErr)
			require.Empty(t, transcript)
			c.assertValueFree(t, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
		})
	}
}

func TestWU4RegistryRecoveryLockedStoreDiagnosesKnownRequestWithoutInventingState(t *testing.T) {
	c := newRegistryFaultCapture(t)
	witness, before := registryDiagnosticWitness(t, c)
	pending := c.f.pending()
	freshSessions, err := session.NewUnifiedStore(c.f.sessions.BaseDir())
	require.NoError(t, err)
	freshSessions.SetStatsFlushInterval(100 * 365 * 24 * time.Hour)
	t.Cleanup(func() { require.NoError(t, freshSessions.Close()) })
	locked := credentials.NewStore(c.f.store.Path()) // A REAL store, deliberately not unlocked.
	t.Cleanup(locked.Close)
	_, _, probeErr := locked.CardReceipt(pending.Card.RequestId)
	require.ErrorIs(t, probeErr, credentials.ErrStoreLocked)
	boot := secretrequest.NewRegistry(c.options(freshSessions, locked))
	recoveryErr := boot.Recover()
	require.ErrorIs(t, recoveryErr, credentials.ErrStoreLocked, "standard typed cause must survive recovery")
	requireValueFree(t, "real locked-store recovery error", map[string]any{"error": recoveryErr.Error()}, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
	assertRegistryRepairGuidance(t, recoveryErr.Error())
	assertRegistryDiagnostic(t, c.logs(t), pending.Card.RequestId, pending.Card.SessionId, "store_locked", "recover")
	assertRegistryUnavailableNotInventedState(t, boot, pending.Card.RequestId)
	afterFailure, err := os.ReadFile(c.f.store.Path())
	require.NoError(t, err)
	assert.Equal(t, before, afterFailure)
	_, exists, err := c.f.store.CardReceipt(witness.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists)
	nextBoot := c.fresh(t)
	require.NoError(t, nextBoot.Recover())
	recovered, err := nextBoot.Get(pending.Card.RequestId, matrixOwner)
	require.NoError(t, err)
	require.Equal(t, "interrupted", recovered.Card.State)
	require.Empty(t, recovered.Card.ReferenceNames)
	c.assertValueFree(t, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
}
