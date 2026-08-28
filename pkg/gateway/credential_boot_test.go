// Package gateway — credential boot integration tests.
//
// These tests verify that the gateway boot path correctly wires
// credentials.InjectFromConfig so that provider *Ref values are resolved
// into the process environment before any provider factory runs.
//
// Channel credential injection is tested in pkg/credentials/bundle_test.go
// via TestResolveBundle_AllChannelRefs, which exercises the same ref list
// through credentials.ResolveBundle.
//
// Implements: BRD SEC-22 (encrypted credential store) / SEC-23 (deny-by-default).

package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// newUnlockedStore creates a credentials.Store in tmpDir, sets a random
// OMNIPUS_MASTER_KEY, unlocks the store, and returns it.
// The key environment variable is cleaned up when t finishes.
func newUnlockedStore(t *testing.T, tmpDir string) *credentials.Store {
	t.Helper()
	rawKey := make([]byte, 32)
	_, err := rand.Read(rawKey)
	require.NoError(t, err)
	t.Setenv("OMNIPUS_MASTER_KEY", hex.EncodeToString(rawKey))

	store := credentials.NewStore(tmpDir + "/credentials.json")
	require.NoError(t, credentials.Unlock(store))
	return store
}

// TestInjectFromConfig_InjectsProviderKey verifies that InjectFromConfig resolves
// a provider's APIKeyRef from the credential store and injects its value into the
// process environment under the ref name.
//
// BDD: Given a provider with APIKeyRef = "TEST_OPENAI_KEY",
//
//	AND "TEST_OPENAI_KEY" is stored in the credential store with value "sk-boottest",
//	When InjectFromConfig is called,
//	Then os.Getenv("TEST_OPENAI_KEY") == "sk-boottest".
func TestInjectFromConfig_InjectsProviderKey(t *testing.T) {
	tmpDir := t.TempDir()
	store := newUnlockedStore(t, tmpDir)

	const refName = "TEST_BOOT_OPENAI_KEY"
	const apiKeyValue = "sk-boot-test-value"

	// Store the credential.
	require.NoError(t, store.Set(refName, apiKeyValue))

	// Build a minimal config with one provider using APIKeyRef.
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{Name: "openai", APIKeyRef: refName},
		},
	}

	// Ensure the env var is clean before we test injection.
	t.Setenv(refName, "")

	errs := credentials.InjectFromConfig(cfg, store)
	require.Empty(t, errs, "InjectFromConfig must not return errors: %v", errs)

	// The env var must now hold the injected value.
	assert.Equal(t, apiKeyValue, os.Getenv(refName),
		"InjectFromConfig must inject provider API key into process environment")

	// Cleanup: unset the env var so other tests are not affected.
	t.Cleanup(func() { os.Unsetenv(refName) })
}

// TestInjectFromConfig_LockedStoreReturnsError verifies that InjectFromConfig
// returns ErrStoreLocked when the credential store has not been unlocked.
//
// BDD: Given a locked credential store,
//
//	When InjectFromConfig is called,
//	Then errs contains credentials.ErrStoreLocked.
func TestInjectFromConfig_LockedStoreReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	// Do NOT unlock — store remains locked.
	store := credentials.NewStore(tmpDir + "/credentials.json")

	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{Name: "anthropic", APIKeyRef: "ANTHROPIC_API_KEY"},
		},
	}

	errs := credentials.InjectFromConfig(cfg, store)
	require.Len(t, errs, 1, "locked store must produce exactly one error")
	assert.ErrorIs(t, errs[0], credentials.ErrStoreLocked,
		"error must be ErrStoreLocked when store is not unlocked")
}

// TestInjectFromConfig_EmptyRefSkipped verifies that providers with an empty
// APIKeyRef are skipped without error (they use a different auth mechanism or
// are public providers).
func TestInjectFromConfig_EmptyRefSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	store := newUnlockedStore(t, tmpDir)

	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{Name: "local-ollama", APIKeyRef: ""},
		},
	}

	errs := credentials.InjectFromConfig(cfg, store)
	assert.Empty(t, errs, "empty APIKeyRef must be skipped without error")
}
