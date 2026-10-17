package secretrequest_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/require"
)

// Chief ruling 2026-10-16 / FR-087: recovery reconciles orphan RECEIPTS,
// never secret values/versions/bindings. No expiry or per-request delete API.
func commitOrphanProbe(t *testing.T, f *matrixEnv, value string) secretrequest.Record {
	t.Helper()
	pending := f.pending()
	rec, err := f.reg.Submit(pending.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": value}))
	require.NoError(t, err)
	require.Equal(t, "submitted", rec.Card.State)
	require.Len(t, rec.Card.ReferenceNames, 1)
	receipt, exists, err := f.store.CardReceipt(rec.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists, "instrument: this is an actual orphanable durable receipt")
	require.Equal(t, rec.Card.ReferenceNames, receipt.References)
	return rec
}

func requireOrphanValuesUnchanged(t *testing.T, f *matrixEnv, expected map[string]string, bindings map[string]credentials.CardSecret) {
	t.Helper()
	for ref, value := range expected {
		stored, readErr := f.store.Get(ref)
		require.NoError(t, readErr, "orphan receipt pruning must never delete encrypted values")
		require.Equal(t, value, stored)
	}
	names, listErr := f.store.List()
	require.NoError(t, listErr)
	require.Len(t, names, len(expected), "no extra/deleted value version")
	actual, metadataErr := f.store.CardSecretMetadata()
	require.NoError(t, metadataErr)
	require.Equal(t, bindings, actual, "recipient binding and every version survive receipt-only cleanup")
}

func TestSecretRecovery_OrphanReceiptsPrunedWithoutDeletingValuesOrBindings(t *testing.T) {
	for _, cut := range []string{"terminal_metadata_pruned", "acting_session_deleted"} {
		t.Run(cut, func(t *testing.T) {
			f := newMatrixEnv(t)
			orphan := commitOrphanProbe(t, f, "FAKE-ORPHAN-SECRET-VALUE")
			control := commitOrphanProbe(t, f, "FAKE-LOADED-CONTROL-VALUE")
			bindings, err := f.store.CardSecretMetadata()
			require.NoError(t, err)
			expected := map[string]string{orphan.Card.ReferenceNames[0]: "FAKE-ORPHAN-SECRET-VALUE", control.Card.ReferenceNames[0]: "FAKE-LOADED-CONTROL-VALUE"}
			if cut == "terminal_metadata_pruned" {
				// Crash cut: the terminal-state prune was published, but the
				// credential-side receipt prune never ran. Do not prune values.
				require.NoError(t, f.sessions.WriteSecretRequestState(orphan.Card.SessionId, []byte("[]")))
			} else {
				require.NoError(t, f.sessions.DeleteSession(orphan.Card.SessionId))
			}
			_, presentBefore, receiptErr := f.store.CardReceipt(orphan.Card.RequestId)
			require.NoError(t, receiptErr)
			require.True(t, presentBefore, "negative-control premise: orphan receipt really survives the cut")
			attemptsBefore := len(f.attempts)
			boot := secretrequest.NewRegistry(f.options())
			require.NoError(t, boot.Recover())
			_, exists, receiptErr := f.store.CardReceipt(orphan.Card.RequestId)
			require.NoError(t, receiptErr)
			require.False(t, exists, "ONE recovery must remove an orphan receipt after %s, without deleting its value", cut)
			loaded, loadedExists, controlErr := f.store.CardReceipt(control.Card.RequestId)
			require.NoError(t, controlErr)
			require.True(t, loadedExists, "every successfully loaded request keeps its receipt")
			require.Equal(t, control.Card.ReferenceNames, loaded.References)
			requireOrphanValuesUnchanged(t, f, expected, bindings)
			_, getErr := boot.Get(orphan.Card.RequestId, matrixOwner)
			requireMatrixFailure(t, getErr, http.StatusNotFound, "")
			snapshot := boot.Snapshot(matrixOwner, "")
			require.Len(t, snapshot, 1)
			require.Equal(t, control.Card, snapshot[0], "orphan must never be resurrected into a state list")
			require.NoError(t, boot.RetryResumes())
			require.Len(t, f.attempts, attemptsBefore+1)
			require.Equal(t, control.Card.RequestId, f.attempts[attemptsBefore].Card.RequestId, "only the loaded control may resume")
			beforeSecond, readErr := os.ReadFile(f.store.Path())
			require.NoError(t, readErr)
			require.NoError(t, boot.Recover())
			require.NoError(t, boot.RetryResumes())
			afterSecond, readErr := os.ReadFile(f.store.Path())
			require.NoError(t, readErr)
			require.Equal(t, beforeSecond, afterSecond, "second Recover is a no-op, not a second sweep/write")
			require.Len(t, f.attempts, attemptsBefore+1)
			requireOrphanValuesUnchanged(t, f, expected, bindings)
		})
	}
}

// Incomplete metadata is UNKNOWN, not proof of an orphan. A failed recovery
// must not prune receipts that a later repaired scan could recover.
func TestSecretRecovery_UnreadableMetadataPreservesAllReceiptsAndValues(t *testing.T) {
	f := newMatrixEnv(t)
	orphan := commitOrphanProbe(t, f, "FAKE-ORPHAN-READ-FAILURE")
	control := commitOrphanProbe(t, f, "FAKE-READABLE-CONTROL")
	broken := commitOrphanProbe(t, f, "FAKE-UNREADABLE-REQUEST")
	require.NoError(t, f.sessions.WriteSecretRequestState(orphan.Card.SessionId, []byte("[]")))
	path := filepath.Join(f.sessions.BaseDir(), broken.Card.SessionId, "secret_requests.json")
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Mkdir(path, 0o700)) // Deterministic I/O fault, works even when CI runs as root.
	bindings, err := f.store.CardSecretMetadata()
	require.NoError(t, err)
	expected := map[string]string{orphan.Card.ReferenceNames[0]: "FAKE-ORPHAN-READ-FAILURE", control.Card.ReferenceNames[0]: "FAKE-READABLE-CONTROL", broken.Card.ReferenceNames[0]: "FAKE-UNREADABLE-REQUEST"}
	attemptsBefore := len(f.attempts)
	boot := secretrequest.NewRegistry(f.options())
	recoverErr := boot.Recover()
	require.Error(t, recoverErr, "an incomplete scan must not be called successful recovery")
	var pathErr *os.PathError
	require.ErrorAs(t, recoverErr, &pathErr)
	for _, rec := range []secretrequest.Record{orphan, control, broken} {
		receipt, exists, readErr := f.store.CardReceipt(rec.Card.RequestId)
		require.NoError(t, readErr)
		require.True(t, exists, "read failure must preserve every potentially recoverable receipt")
		require.Equal(t, rec.Card.ReferenceNames, receipt.References)
	}
	require.Len(t, f.attempts, attemptsBefore, "failed recovery may not dispatch a continuation")
	requireOrphanValuesUnchanged(t, f, expected, bindings)
}
