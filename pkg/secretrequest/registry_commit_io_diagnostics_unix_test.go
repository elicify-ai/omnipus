//go:build darwin || linux

package secretrequest_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This cut covers the real POSIX commit-lock filesystem boundary. Windows has
// no sidecar flock/open at all; its real locked-Store commit row is portable.
// A directory obstruction remains effective when Linux CI runs as root.
func TestWU4RegistryCommitIOFaultDiagnosesStageAndPreservesCiphertext(t *testing.T) {
	c := newRegistryFaultCapture(t)
	witness, before := registryDiagnosticWitness(t, c)
	pending := c.f.pending()
	lockPath := fileutil.SidecarLockPath(c.f.store.Path())
	preserved := lockPath + ".diagnostic-cut-preserved"
	require.NoError(t, os.Rename(lockPath, preserved))
	require.NoError(t, os.Mkdir(lockPath, 0o700))
	active := true
	repair := func() {
		if active {
			require.NoError(t, os.Remove(lockPath))
			require.NoError(t, os.Rename(preserved, lockPath))
			active = false
		}
	}
	t.Cleanup(repair)
	// Readable values and receipt prove the fault is in COMMIT, not store-open.
	value, err := c.f.store.Get(witness.Card.ReferenceNames[0])
	require.NoError(t, err)
	require.Equal(t, registryDiagnosticOld, value)
	_, exists, err := c.f.store.CardReceipt(witness.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists)
	out, err := c.f.reg.Submit(pending.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": registryDiagnosticValue}))
	requireMatrixFailure(t, err, http.StatusInternalServerError, "")
	assertRegistryKnownPending(t, c, pending, out, err)
	assertRegistryIODiagnostic(t, c.logs(t), pending.Card.RequestId, pending.Card.SessionId, "commit")
	assertRegistryNoNewStorage(t, c, pending, witness, before)
	c.assertValueFree(t, registryDiagnosticValue, registryDiagnosticOld, registryDiagnosticRawText)
	repair()
	registryDiagnosticRepairSubmit(t, c, pending)
	assert.Len(t, c.f.attempts, 2, "one normal witness and one explicit repaired submit; no automatic plaintext retry")
}
