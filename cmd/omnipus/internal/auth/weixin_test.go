package auth

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

// newWeixinTestEnv sets up a temp directory with OMNIPUS_HOME and OMNIPUS_CONFIG
// pointing into it, and sets a fixed OMNIPUS_MASTER_KEY so the credential store
// can be unlocked without a TTY.
func newWeixinTestEnv(t *testing.T) (tmpDir, configPath string) {
	t.Helper()
	tmpDir = t.TempDir()
	configPath = filepath.Join(tmpDir, "config.json")

	masterKey := "0000000000000000000000000000000000000000000000000000000000000002"
	t.Setenv(credentials.EnvMasterKey, masterKey)
	t.Setenv(config.EnvHome, tmpDir)
	t.Setenv(config.EnvConfig, configPath)
	return tmpDir, configPath
}

// TestSaveWeixinConfig_Happy verifies the happy path: after saveWeixinConfig,
// the config file has TokenRef = "WEIXIN_TOKEN" and the store holds the token,
// and the raw config bytes do NOT contain the token value.
func TestSaveWeixinConfig_Happy(t *testing.T) {
	tmpDir, configPath := newWeixinTestEnv(t)

	const token = "weixin-tok-happy"
	const baseURL = "https://ilinkai.weixin.qq.com/"

	err := saveWeixinConfig(token, baseURL, "")
	require.NoError(t, err)

	// Config must reference the ref, not the token value.
	cfg, err := config.LoadConfig(internal.GetConfigPath())
	require.NoError(t, err)
	assert.True(t, cfg.Channels.Weixin.Enabled)
	assert.Equal(t, weixinTokenCredRef, cfg.Channels.Weixin.TokenRef)

	// Store must hold the actual token value.
	storePath := filepath.Join(tmpDir, "credentials.json")
	store := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(store))
	stored, err := store.Get(weixinTokenCredRef)
	require.NoError(t, err)
	assert.Equal(t, token, stored)

	// Raw config bytes must NOT contain the plaintext token.
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), token,
		"plaintext token must not appear in config.json")
}

// TestSaveWeixinConfig_LockedStore verifies that when the credential store cannot
// be unlocked and an existing credentials.json blocks the auto-generate fallback
// path, saveWeixinConfig returns an error without modifying config.
func TestSaveWeixinConfig_LockedStore(t *testing.T) {
	tmpDir, configPath := newWeixinTestEnv(t)

	// Unset the master key so the store cannot be unlocked via env/file.
	t.Setenv(credentials.EnvMasterKey, "")
	// Also clear key file path to ensure TTY prompt is not attempted.
	t.Setenv("OMNIPUS_KEY_FILE", "")

	// Seed a credentials.json so Unlock's auto-generate path (mode 4) does
	// NOT fire — this test pins the locked-existing-store semantic, not the
	// fresh-install semantic. Auto-generate is covered by
	// TestUnlock_AutoGeneratesOnFreshInstall in pkg/credentials.
	storePath := filepath.Join(tmpDir, "credentials.json")
	require.NoError(t, os.WriteFile(storePath,
		[]byte(`{"version":1,"salt":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","credentials":{}}`),
		0o600,
	))

	err := saveWeixinConfig("weixin-tok-locked", "https://ilinkai.weixin.qq.com/", "")
	require.Error(t, err, "saveWeixinConfig must fail when credential store is locked")

	// Config file must not have been created.
	_, statErr := os.Stat(configPath)
	assert.True(t, os.IsNotExist(statErr),
		"config.json must not be created when store is locked")
}

// TestSaveWeixinConfig_Overwrite verifies that running saveWeixinConfig twice
// with different tokens results in the second token being stored and the first
// being overwritten cleanly.
func TestSaveWeixinConfig_Overwrite(t *testing.T) {
	tmpDir, _ := newWeixinTestEnv(t)

	const baseURL = "https://ilinkai.weixin.qq.com/"
	const firstToken = "weixin-tok-first"
	const secondToken = "weixin-tok-second"

	require.NoError(t, saveWeixinConfig(firstToken, baseURL, ""))
	require.NoError(t, saveWeixinConfig(secondToken, baseURL, ""))

	// Store must hold the second token.
	storePath := filepath.Join(tmpDir, "credentials.json")
	store := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(store))
	stored, err := store.Get(weixinTokenCredRef)
	require.NoError(t, err)
	assert.Equal(t, secondToken, stored, "second token must overwrite first")

	// Config must still reference the standard ref.
	cfg, err := config.LoadConfig(internal.GetConfigPath())
	require.NoError(t, err)
	assert.Equal(t, weixinTokenCredRef, cfg.Channels.Weixin.TokenRef)
}

// TestSaveWeixinConfig_SaveConfigFailureRollsBackStore verifies that when
// SaveConfig fails after store.Set succeeds, the credential is removed so that
// config.json is never left with a TokenRef pointing at a non-existent entry.
func TestSaveWeixinConfig_SaveConfigFailureRollsBackStore(t *testing.T) {
	if os.Getuid() == 0 {
		// Root bypasses DAC (Discretionary Access Control), so chmod 0555 on
		// a directory does not prevent writes from root. The failure-injection
		// this test relies on only works for non-privileged users.
		t.Skip("read-only directory injection is ineffective under root; run as non-root")
	}
	tmpDir := t.TempDir()

	// Create a read-only directory so that SaveConfig fails with EACCES.
	roDir := filepath.Join(tmpDir, "ro")
	require.NoError(t, os.MkdirAll(roDir, 0o555))
	configPath := filepath.Join(roDir, "config.json")

	masterKey := "0000000000000000000000000000000000000000000000000000000000000006"
	t.Setenv(credentials.EnvMasterKey, masterKey)
	t.Setenv(config.EnvHome, tmpDir)
	t.Setenv(config.EnvConfig, configPath)

	err := saveWeixinConfig("weixin-tok-rollback", "https://ilinkai.weixin.qq.com/", "")
	require.Error(t, err, "saveWeixinConfig must fail when config cannot be written")

	// The credential must have been rolled back.
	storePath := filepath.Join(tmpDir, "credentials.json")
	store := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(store))
	_, getErr := store.Get(weixinTokenCredRef)
	var notFound *credentials.NotFoundError
	assert.ErrorAs(t, getErr, &notFound,
		"store must not contain the token after SaveConfig rollback")
}

// TestSaveWeixinConfig_ConcurrentRunsDontWipeEachOther verifies that two
// concurrent saveWeixinConfig calls serialized by the advisory flock do not
// corrupt each other's credential.
func TestSaveWeixinConfig_ConcurrentRunsDontWipeEachOther(t *testing.T) {
	tmpDir, _ := newWeixinTestEnv(t)
	// Override master key so both goroutines share the same unlocked store.
	masterKey := "0000000000000000000000000000000000000000000000000000000000000007"
	t.Setenv(credentials.EnvMasterKey, masterKey)

	const baseURL = "https://ilinkai.weixin.qq.com/"
	const tokenA = "weixin-concurrent-A"
	const tokenB = "weixin-concurrent-B"

	var wg sync.WaitGroup
	errs := make([]error, 2)

	for i, tok := range []string{tokenA, tokenB} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = saveWeixinConfig(tok, baseURL, "")
		}()
	}
	wg.Wait()

	successCount := 0
	for _, e := range errs {
		if e == nil {
			successCount++
		}
	}
	require.Greater(t, successCount, 0, "at least one concurrent run must succeed")

	storePath := filepath.Join(tmpDir, "credentials.json")
	store := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(store))
	stored, getErr := store.Get(weixinTokenCredRef)
	require.NoError(t, getErr, "store must contain a token after concurrent runs")
	assert.True(t, stored == tokenA || stored == tokenB,
		"stored token must be one of the two written values, got %q", stored)
}
