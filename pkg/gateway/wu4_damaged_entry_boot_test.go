package gateway

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

const wu4DamagedRef = "WU4_DAMAGED_ENTRY"

// wu4DamageEntry gives name a wrong-length nonce in the store file.
func wu4DamageEntry(t *testing.T, path, name string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var sf map[string]any
	require.NoError(t, json.Unmarshal(raw, &sf))
	creds, ok := sf["credentials"].(map[string]any)
	require.True(t, ok)
	entry, ok := creds[name].(map[string]any)
	require.True(t, ok, "fixture: %s present", name)
	entry["nonce"] = base64.StdEncoding.EncodeToString(make([]byte, 8))
	out, err := json.Marshal(sf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, out, 0o600))
}

// Founder 2026-10-15 (WU-4): one damaged credential entry is a visible,
// scoped failure. Boot still starts (the store and its other entries are
// readable) instead of refusing as a whole-store failure; the sibling entry
// still resolves.
func TestWU4BootContinuesWithOneDamagedEntry(t *testing.T) {
	f := newIssue1277R3Fixture(t, false)
	require.NoError(t, f.store.Set(wu4DamagedRef, "wu4-damaged-value"))
	wu4DamageEntry(t, filepath.Join(f.home, "credentials.json"), wu4DamagedRef)

	health := f.store.HealthCheck()
	var damaged *credentials.DamagedEntriesError
	require.True(t, errors.As(health, &damaged), "control: the store reports the damaged entry: %v", health)

	cfg, _, store, err := bootCredentials(f.home, f.configPath)
	require.NoError(t, err, "a single damaged entry must not refuse boot")
	require.NotNil(t, cfg)
	require.NotNil(t, store)
	t.Cleanup(store.Close)
	value, err := store.Get(issue1277R3Ref)
	require.NoError(t, err, "the sibling entry stays readable")
	require.Equal(t, issue1277R3Value, value)
}
