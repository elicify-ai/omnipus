package auth

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

func TestNewWeComCommand(t *testing.T) {
	cmd := newWeComCommand()

	require.NotNil(t, cmd)
	assert.Equal(t, "wecom", cmd.Use)
	assert.Equal(t, "Scan a WeCom QR code and configure channels.wecom", cmd.Short)
	assert.NotNil(t, cmd.Flags().Lookup("timeout"))
}

func TestBuildWeComQRGenerateURL(t *testing.T) {
	rawURL, err := buildWeComQRGenerateURL("https://example.com/ai/qc/generate", wecomQRSourceID, 3)
	require.NoError(t, err)

	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)

	assert.Equal(t, wecomQRSourceID, parsed.Query().Get("source"))
	assert.Equal(t, wecomQRSourceID, parsed.Query().Get("sourceID"))
	assert.Equal(t, "3", parsed.Query().Get("plat"))
}

func TestBuildWeComQRCodePageURL(t *testing.T) {
	rawURL, err := buildWeComQRCodePageURL("https://example.com/ai/qc/gen", wecomQRSourceID, "scode-1")
	require.NoError(t, err)

	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)

	assert.Equal(t, wecomQRSourceID, parsed.Query().Get("source"))
	assert.Equal(t, wecomQRSourceID, parsed.Query().Get("sourceID"))
	assert.Equal(t, "scode-1", parsed.Query().Get("scode"))
}

func TestFetchWeComQRCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/generate", r.URL.Path)
		assert.Equal(t, wecomQRSourceID, r.URL.Query().Get("source"))
		assert.Equal(t, wecomQRSourceID, r.URL.Query().Get("sourceID"))
		assert.Equal(t, strconv.Itoa(wecomPlatformCode()), r.URL.Query().Get("plat"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"scode":"scode-1","auth_url":"https://example.com/qr"}}`))
	}))
	defer server.Close()

	opts := normalizeWeComQRFlowOptions(wecomQRFlowOptions{
		HTTPClient:  server.Client(),
		GenerateURL: server.URL + "/generate",
		Writer:      bytes.NewBuffer(nil),
	})

	session, err := fetchWeComQRCode(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, "scode-1", session.SCode)
	assert.Equal(t, "https://example.com/qr", session.AuthURL)
}

func TestPollWeComQRCodeResult(t *testing.T) {
	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		assert.Equal(t, "/query", r.URL.Path)
		assert.Equal(t, "scode-1", r.URL.Query().Get("scode"))
		w.Header().Set("Content-Type", "application/json")
		switch call {
		case 1:
			_, _ = w.Write([]byte(`{"data":{"status":"wait"}}`))
		case 2:
			_, _ = w.Write([]byte(`{"data":{"status":"scaned"}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"status":"success","bot_info":{"botid":"bot-1","secret":"secret-1"}}}`))
		}
	}))
	defer server.Close()

	var output bytes.Buffer
	opts := normalizeWeComQRFlowOptions(wecomQRFlowOptions{
		HTTPClient:   server.Client(),
		QueryURL:     server.URL + "/query",
		PollInterval: time.Millisecond,
		PollTimeout:  time.Second,
		Writer:       &output,
	})

	botInfo, err := pollWeComQRCodeResult(context.Background(), opts, "scode-1")
	require.NoError(t, err)
	assert.Equal(t, "bot-1", botInfo.BotID)
	assert.Equal(t, "secret-1", botInfo.Secret)
	assert.Contains(t, output.String(), "QR code scanned. Confirm the login in WeCom.")
}

func TestApplyWeComAuthResult(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.WeCom.WebSocketURL = ""

	applyWeComAuthResult(cfg, wecomQRBotInfo{
		BotID:  "bot-1",
		Secret: "secret-1",
	})

	assert.True(t, cfg.Channels.WeCom.Enabled)
	assert.Equal(t, "bot-1", cfg.Channels.WeCom.BotID)
	assert.Equal(t, wecomSecretCredRef, cfg.Channels.WeCom.SecretRef)
	assert.Equal(t, wecomDefaultWebSocketURL, cfg.Channels.WeCom.WebSocketURL)
}

func TestAuthWeComCmdWithScanner(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Use a fixed 64-hex-char (256-bit) master key so the credential store can be
	// unlocked without a TTY in the test environment.
	masterKey := "0000000000000000000000000000000000000000000000000000000000000001"
	t.Setenv(credentials.EnvMasterKey, masterKey)
	t.Setenv(config.EnvHome, tmpDir)
	t.Setenv(config.EnvConfig, configPath)

	var output bytes.Buffer
	err := authWeComCmdWithScanner(
		context.Background(),
		&output,
		time.Second,
		func(_ context.Context, opts wecomQRFlowOptions) (wecomQRBotInfo, error) {
			assert.Equal(t, wecomQRSourceID, opts.SourceID)
			return wecomQRBotInfo{
				BotID:  "bot-1",
				Secret: "secret-1",
			}, nil
		},
	)
	require.NoError(t, err)

	cfg, err := config.LoadConfig(internal.GetConfigPath())
	require.NoError(t, err)
	assert.True(t, cfg.Channels.WeCom.Enabled)
	assert.Equal(t, "bot-1", cfg.Channels.WeCom.BotID)
	assert.Equal(t, wecomSecretCredRef, cfg.Channels.WeCom.SecretRef)
	assert.Equal(t, wecomDefaultWebSocketURL, cfg.Channels.WeCom.WebSocketURL)
	assert.Contains(t, output.String(), "WeCom connected.")

	// Verify the secret was stored in the credential store.
	storePath := filepath.Join(tmpDir, "credentials.json")
	store := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(store))
	secret, err := store.Get(wecomSecretCredRef)
	require.NoError(t, err)
	assert.Equal(t, "secret-1", secret)

	// Ensure the secret is not in the config file.
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "secret-1")
}

// TestAuthWeComCmdWithScanner_LockedStore verifies that when the credential store
// cannot be unlocked (OMNIPUS_MASTER_KEY unset and no TTY, and an existing
// credentials.json blocks the auto-generate fallback), the command returns
// an error without modifying config or store.
func TestAuthWeComCmdWithScanner_LockedStore(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Explicitly unset master key and key file so the store cannot be unlocked.
	t.Setenv(credentials.EnvMasterKey, "")
	t.Setenv("OMNIPUS_KEY_FILE", "")
	t.Setenv(config.EnvHome, tmpDir)
	t.Setenv(config.EnvConfig, configPath)

	// Seed a credentials.json so Unlock's auto-generate path (mode 4) does
	// NOT fire — this test pins the locked-existing-store semantic. The
	// fresh-install auto-generate is covered by
	// TestUnlock_AutoGeneratesOnFreshInstall in pkg/credentials.
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "credentials.json"),
		[]byte(`{"version":1,"salt":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","credentials":{}}`),
		0o600,
	))

	var output bytes.Buffer
	err := authWeComCmdWithScanner(
		context.Background(),
		&output,
		time.Second,
		func(_ context.Context, _ wecomQRFlowOptions) (wecomQRBotInfo, error) {
			return wecomQRBotInfo{BotID: "bot-locked", Secret: "secret-locked"}, nil
		},
	)
	require.Error(t, err, "command must fail when credential store is locked")

	// Config file must not have been created.
	_, statErr := os.Stat(configPath)
	assert.True(t, os.IsNotExist(statErr),
		"config.json must not be created when store is locked")
}

// TestAuthWeComCmdWithScanner_Overwrite verifies that running the auth flow twice
// with different secrets overwrites the first cleanly.
func TestAuthWeComCmdWithScanner_Overwrite(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	masterKey := "0000000000000000000000000000000000000000000000000000000000000003"
	t.Setenv(credentials.EnvMasterKey, masterKey)
	t.Setenv(config.EnvHome, tmpDir)
	t.Setenv(config.EnvConfig, configPath)

	runScanner := func(botID, secret string) error {
		return authWeComCmdWithScanner(
			context.Background(),
			bytes.NewBuffer(nil),
			time.Second,
			func(_ context.Context, _ wecomQRFlowOptions) (wecomQRBotInfo, error) {
				return wecomQRBotInfo{BotID: botID, Secret: secret}, nil
			},
		)
	}

	require.NoError(t, runScanner("bot-first", "secret-first"))
	require.NoError(t, runScanner("bot-second", "secret-second"))

	// Store must hold the second secret.
	storePath := filepath.Join(tmpDir, "credentials.json")
	store := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(store))
	stored, err := store.Get(wecomSecretCredRef)
	require.NoError(t, err)
	assert.Equal(t, "secret-second", stored, "second secret must overwrite first")

	// Raw config must not contain either plaintext secret.
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "secret-first")
	assert.NotContains(t, string(raw), "secret-second")
}

// TestAuthWeComCmdWithScanner_SaveConfigFailureRollsBackStore verifies that when
// SaveConfig fails after store.Set succeeds, the credential is removed so that
// config.json is never left with a SecretRef pointing at a non-existent entry.
//
// We simulate a SaveConfig failure by making the config path point to a
// read-only directory, which causes os.Create to fail.
func TestAuthWeComCmdWithScanner_SaveConfigFailureRollsBackStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		// POSIX chmod 0555 does not make a directory non-writable on Windows
		// (Windows uses ACLs, not mode bits). The failure-injection this test
		// relies on only works on Unix-like systems.
		t.Skip("read-only directory injection is POSIX-only; see #108")
	}
	if os.Getuid() == 0 {
		// Root bypasses DAC (Discretionary Access Control), so chmod 0555 on
		// a directory does not prevent writes from root. The failure-injection
		// this test relies on only works for non-privileged users.
		t.Skip("read-only directory injection is ineffective under root; run as non-root")
	}
	tmpDir := t.TempDir()

	// Create a nested directory that we will make read-only so that
	// SaveConfig (which writes to configPath) fails with EACCES.
	roDir := filepath.Join(tmpDir, "ro")
	require.NoError(t, os.MkdirAll(roDir, 0o555))
	configPath := filepath.Join(roDir, "config.json")

	masterKey := "0000000000000000000000000000000000000000000000000000000000000004"
	t.Setenv(credentials.EnvMasterKey, masterKey)
	t.Setenv(config.EnvHome, tmpDir)
	t.Setenv(config.EnvConfig, configPath)

	var output bytes.Buffer
	err := authWeComCmdWithScanner(
		context.Background(),
		&output,
		time.Second,
		func(_ context.Context, _ wecomQRFlowOptions) (wecomQRBotInfo, error) {
			return wecomQRBotInfo{BotID: "bot-ro", Secret: "secret-ro"}, nil
		},
	)
	require.Error(t, err, "command must fail when config cannot be written")

	// The credential must have been rolled back — store.Get must return ErrNotFound.
	storePath := filepath.Join(tmpDir, "credentials.json")
	store := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(store))
	_, getErr := store.Get(wecomSecretCredRef)
	var notFound *credentials.NotFoundError
	assert.ErrorAs(t, getErr, &notFound,
		"store must not contain the secret after SaveConfig rollback")
}

// TestAuthWeComCmdWithScanner_ConcurrentRunsDontWipeEachOther verifies that two
// concurrent auth flows serialized by the advisory flock do not overwrite each
// other's secret in the credential store.  Both runs use different secrets; we
// assert that after both complete the store holds exactly one of the secrets (the
// last one to acquire the lock) and not an empty/corrupt state.
func TestAuthWeComCmdWithScanner_ConcurrentRunsDontWipeEachOther(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	masterKey := "0000000000000000000000000000000000000000000000000000000000000005"
	t.Setenv(credentials.EnvMasterKey, masterKey)
	t.Setenv(config.EnvHome, tmpDir)
	t.Setenv(config.EnvConfig, configPath)

	const secretA = "secret-concurrent-A"
	const secretB = "secret-concurrent-B"

	var wg sync.WaitGroup
	errs := make([]error, 2)

	for i, secret := range []string{secretA, secretB} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = authWeComCmdWithScanner(
				context.Background(),
				bytes.NewBuffer(nil),
				time.Second,
				func(_ context.Context, _ wecomQRFlowOptions) (wecomQRBotInfo, error) {
					return wecomQRBotInfo{BotID: "bot-concurrent", Secret: secret}, nil
				},
			)
		}()
	}
	wg.Wait()

	// At least one run must succeed.
	successCount := 0
	for _, e := range errs {
		if e == nil {
			successCount++
		}
	}
	require.Greater(t, successCount, 0, "at least one concurrent run must succeed")

	// The store must hold a non-empty secret — it must be one of the two secrets
	// written, not an empty or corrupt value.
	storePath := filepath.Join(tmpDir, "credentials.json")
	store := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(store))
	stored, getErr := store.Get(wecomSecretCredRef)
	require.NoError(t, getErr, "store must contain a secret after concurrent runs")
	assert.True(t, stored == secretA || stored == secretB,
		"stored secret must be one of the two written values, got %q", stored)
}
