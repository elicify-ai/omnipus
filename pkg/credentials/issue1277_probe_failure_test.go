package credentials

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// #1277 chief round-3 ruling, 2026-10-14, requirement 4: a store-format
// probe error must be returned immediately, not deferred to a Get that a
// reference-free installation will never make. These are current/malformed
// files, not legacy migration acceptance fixtures. Keys/values are synthetic.
func issue1277ProbeStore(t *testing.T) (*Store, []byte) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	store := NewStore(filepath.Join(home, "credentials.json"))
	t.Cleanup(store.Close)
	// A valid AES-256 key has exactly 32 bytes; no derivation or real secret.
	return store, bytes.Repeat([]byte{0x31}, 32)
}

func issue1277RunMigrationProbe(store *Store, key []byte) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.migrateLegacyLocked(key, key, nil)
}

func TestIssue1277_MigrationProbeMalformedFileReturnsSyntaxError(t *testing.T) {
	store, key := issue1277ProbeStore(t)
	original := []byte(`{not-json`)
	require.NoError(t, os.WriteFile(store.Path(), original, 0o600))
	_, present, probeErr := store.readStoreVersionFile()
	require.True(t, present)
	var originalSyntax *json.SyntaxError
	require.ErrorAs(t, probeErr, &originalSyntax, "instrument: the real format probe sees malformed JSON")
	migrated, err := issue1277RunMigrationProbe(store, key)
	var syntax *json.SyntaxError
	require.ErrorAs(t, err, &syntax, "round-3 F2: migrateLegacyLocked must surface its probe error, not return nil")
	require.Contains(t, err.Error(), "store")
	require.Contains(t, err.Error(), "corrupt")
	require.False(t, migrated)
	require.True(t, store.IsLocked())
	after, readErr := os.ReadFile(store.Path())
	require.NoError(t, readErr)
	require.Equal(t, original, after)
}

func TestIssue1277_MigrationProbeReadFailureReturnsPathError(t *testing.T) {
	store, key := issue1277ProbeStore(t)
	// Directory-as-file fails for the actual reader, including under root;
	// chmod-based unreadability would not reliably exercise an I/O failure.
	require.NoError(t, os.Mkdir(store.Path(), 0o700))
	_, present, probeErr := store.readStoreVersionFile()
	require.True(t, present)
	var originalPathError *os.PathError
	require.ErrorAs(t, probeErr, &originalPathError, "instrument: the real store-format read fails")
	require.Equal(t, store.Path(), originalPathError.Path)
	migrated, err := issue1277RunMigrationProbe(store, key)
	var pathError *os.PathError
	require.ErrorAs(t, err, &pathError, "round-3 F2: migration must not swallow an actual filesystem read error")
	require.Equal(t, store.Path(), pathError.Path)
	require.Contains(t, err.Error(), "store")
	require.False(t, migrated)
	require.True(t, store.IsLocked())
	info, statErr := os.Stat(store.Path())
	require.NoError(t, statErr)
	require.True(t, info.IsDir(), "refusal must not replace the unreadable path")
}

func TestIssue1277_UnlockMalformedStorePropagatesProbeErrorAndStaysLocked(t *testing.T) {
	store, key := issue1277ProbeStore(t)
	original := []byte(`{not-json`)
	require.NoError(t, os.WriteFile(store.Path(), original, 0o600))
	err := store.UnlockWithKey(key)
	var syntax *json.SyntaxError
	require.ErrorAs(t, err, &syntax, "round-3 F2: the probe error must reach the public unlock caller")
	require.Contains(t, err.Error(), "store")
	require.Contains(t, err.Error(), "corrupt")
	require.True(t, store.IsLocked(), "a refused unlock must not install its key")
	after, readErr := os.ReadFile(store.Path())
	require.NoError(t, readErr)
	require.Equal(t, original, after)
}

func TestIssue1277_MigrationProbeCurrentStoreIsSuccessfulNoOp(t *testing.T) {
	store, key := issue1277ProbeStore(t)
	require.NoError(t, store.UnlockWithKey(key))
	require.NoError(t, store.Set("QA1277_PROBE_HEALTHY", "qa1277-probe-synthetic-value"))
	before, readErr := os.ReadFile(store.Path())
	require.NoError(t, readErr)
	migrated, err := issue1277RunMigrationProbe(store, key)
	require.NoError(t, err, "valid current-format store is the intentional no-migration path")
	require.False(t, migrated)
	after, readErr := os.ReadFile(store.Path())
	require.NoError(t, readErr)
	require.Equal(t, before, after)
	value, getErr := store.Get("QA1277_PROBE_HEALTHY")
	require.NoError(t, getErr)
	require.Equal(t, "qa1277-probe-synthetic-value", value)
}
