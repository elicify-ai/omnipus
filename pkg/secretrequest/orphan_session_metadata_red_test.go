package secretrequest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// The constructor can omit an unreadable session from its cached listing.
// A missing listing row is NOT proof that its receipt is an orphan: recovery
// must fail safely rather than sweep recoverable values' commit evidence.
func TestSecretRecovery_UnreadableCoreSessionMetadataCannotAuthorizeReceiptSweep(t *testing.T) {
	f := newMatrixEnv(t)
	orphan := commitOrphanProbe(t, f, "FAKE-CORE-META-ORPHAN")
	control := commitOrphanProbe(t, f, "FAKE-CORE-META-CONTROL")
	broken := commitOrphanProbe(t, f, "FAKE-CORE-META-UNKNOWN")
	require.NoError(t, f.sessions.WriteSecretRequestState(orphan.Card.SessionId, []byte("[]")))
	bindings, err := f.store.CardSecretMetadata()
	require.NoError(t, err)
	path := filepath.Join(f.sessions.BaseDir(), broken.Card.SessionId, "meta.json")
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Mkdir(path, 0o700)) // Real I/O fault, not root-sensitive permission simulation.
	freshSessions, err := session.NewUnifiedStore(f.sessions.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, freshSessions.Close()) })
	f.sessions = freshSessions // A real post-restart metadata cache, not the old readable cache.
	attemptsBefore := len(f.attempts)
	boot := secretrequest.NewRegistry(f.options())
	recoverErr := boot.Recover()
	require.Error(t, recoverErr, "an unreadable session omitted by the listing must block successful orphan reconciliation")
	for _, rec := range []secretrequest.Record{orphan, control, broken} {
		receipt, exists, readErr := f.store.CardReceipt(rec.Card.RequestId)
		require.NoError(t, readErr)
		require.True(t, exists, "incomplete core metadata must preserve all potentially recoverable receipts")
		require.Equal(t, rec.Card.ReferenceNames, receipt.References)
	}
	require.Len(t, f.attempts, attemptsBefore)
	requireOrphanValuesUnchanged(t, f, map[string]string{orphan.Card.ReferenceNames[0]: "FAKE-CORE-META-ORPHAN", control.Card.ReferenceNames[0]: "FAKE-CORE-META-CONTROL", broken.Card.ReferenceNames[0]: "FAKE-CORE-META-UNKNOWN"}, bindings)
}
