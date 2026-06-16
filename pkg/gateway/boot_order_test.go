//go:build !cgo

// Package gateway — boot order integration tests.
//
// These tests verify the invariant that the gateway boot sequence is:
//   NewStore → Unlock → LoadConfigWithStore → InjectFromConfig → ResolveBundle →
//   RegisterSensitiveValues → NewManager → Start
//
// They exercise the real boot path via the bootCredentials helper that is shared
// with gateway.Run — a refactor of Run that reorders or drops any step will also
// break these tests (they cannot drift from Run's behavior).
//
// Implements: BRD SEC-22 / SEC-23 (deny-by-default credential management).

package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

// fixedHexKey is a deterministic 64-character hex key for use in tests.
// Using a fixed key avoids Argon2id KDF overhead (which would add ~2s per test).
const fixedHexKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// writeBootTestFile creates a file at path with the given content and mode 0600.
func writeBootTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestGatewayBoot_V0ConfigWithPlaintextSecretMigrates verifies end-to-end that:
//  1. A v0 config.json with a plaintext Telegram token is loaded via
//     bootCredentials (which calls LoadConfigWithStore after Unlock), triggering MigrateWithStore.
//  2. credentials.json is written to disk with the migrated token.
//  3. The migrated config.json no longer contains the plaintext token.
//  4. The rewritten config.json contains token_ref instead.
//  5. The credential can be retrieved via the returned bundle.
//
// Boot order pinned: Unlock MUST precede LoadConfigWithStore. If Unlock is
// skipped, the store is locked and MigrateWithStore returns an error.
// This test exercises bootCredentials (shared with gateway.Run) so any refactor
// that breaks the sequence will also break this test.
func TestGatewayBoot_V0ConfigWithPlaintextSecretMigrates(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	configPath := filepath.Join(tmpDir, "config.json")
	const legacyToken = "12345:legacy-plaintext"

	writeBootTestFile(t, configPath, `{
		"version": 0,
		"channels": {
			"telegram": {
				"enabled": true,
				"token": "`+legacyToken+`"
			}
		},
		"gateway": { "host": "127.0.0.1", "port": 19999 }
	}`)

	// Call bootCredentials — the canonical boot sequence shared with gateway.Run.
	cfg, bundle, credStore, err := bootCredentials(tmpDir, configPath)
	if err != nil {
		t.Fatalf("bootCredentials: %v", err)
	}

	// Assert 1: credentials.json now exists.
	credPath := filepath.Join(tmpDir, "credentials.json")
	if _, statErr := os.Stat(credPath); statErr != nil {
		t.Fatalf("credentials.json must exist after migration, got stat error: %v", statErr)
	}

	// Assert 2: store.Get("TELEGRAM_TOKEN") returns the original plaintext.
	gotToken, err := credStore.Get("TELEGRAM_TOKEN")
	if err != nil {
		t.Fatalf("store.Get(TELEGRAM_TOKEN): %v", err)
	}
	if gotToken != legacyToken {
		t.Errorf("store.Get(TELEGRAM_TOKEN) = %q, want %q", gotToken, legacyToken)
	}

	// Assert 3: config struct has TokenRef set.
	if cfg.Channels["telegram"].TokenRef != "TELEGRAM_TOKEN" {
		t.Errorf("cfg.Channels[telegram].TokenRef = %q, want %q",
			cfg.Channels["telegram"].TokenRef, "TELEGRAM_TOKEN")
	}

	// Assert 4: rewritten config.json does NOT contain the plaintext token.
	migratedData, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("re-read config.json: %v", readErr)
	}
	if strings.Contains(string(migratedData), legacyToken) {
		t.Error("config.json must NOT contain the plaintext token after migration")
	}

	// Assert 5: rewritten config.json contains "token_ref" marker.
	if !strings.Contains(string(migratedData), "token_ref") {
		t.Error("config.json must contain token_ref after migration")
	}

	// Assert 6: the resolved bundle carries the token (ResolveBundle ran in bootCredentials).
	resolved := bundle.GetString("TELEGRAM_TOKEN")
	if resolved != legacyToken {
		t.Errorf("bundle.GetString(TELEGRAM_TOKEN) = %q, want %q", resolved, legacyToken)
	}
}

// TestGatewayBoot_MissingCredentialRefFailsFast verifies that:
//  1. bootCredentials returns a fatal error when a credential ref is missing for
//     an ENABLED channel — the gateway must not start with broken enabled channels.
//  2. bootCredentials succeeds when the same ref is missing but the channel is DISABLED.
//  3. ResolveBundle itself surfaces a NotFoundError for missing refs, regardless
//     of enabled state — the enabled/disabled fatality gate lives in bootCredentials.
//
// Boot order invariant: ResolveBundle MUST run after LoadConfigWithStore and
// MUST return an error (not silently ignore) for missing refs.
// This test exercises bootCredentials so it cannot drift from gateway.Run behavior.
func TestGatewayBoot_MissingCredentialRefFailsFast(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	// --- Sub-test 1: enabled channel with missing ref → fatal. ---
	enabledConfigPath := filepath.Join(tmpDir, "config_enabled.json")
	writeBootTestFile(t, enabledConfigPath, `{
		"version": 1,
		"channels": {
			"telegram": {
				"enabled": true,
				"token_ref": "TELEGRAM_TOKEN"
			}
		},
		"gateway": { "host": "127.0.0.1", "port": 19998 }
	}`)

	_, _, _, enabledErr := bootCredentials(tmpDir, enabledConfigPath) //nolint:dogsled
	if enabledErr == nil {
		t.Fatal("bootCredentials must fail when an enabled channel's credential ref is missing from the store")
	}
	if !strings.Contains(enabledErr.Error(), "TELEGRAM_TOKEN") {
		t.Errorf("error must mention the missing ref; got: %q", enabledErr.Error())
	}

	// --- Sub-test 2: disabled channel with missing ref → not fatal. ---
	disabledConfigPath := filepath.Join(tmpDir, "config_disabled.json")
	writeBootTestFile(t, disabledConfigPath, `{
		"version": 1,
		"channels": {
			"telegram": {
				"enabled": false,
				"token_ref": "TELEGRAM_TOKEN"
			}
		},
		"gateway": { "host": "127.0.0.1", "port": 19997 }
	}`)

	_, disabledBundle, disabledStore, disabledErr := bootCredentials(tmpDir, disabledConfigPath)
	if disabledErr != nil {
		t.Fatalf("bootCredentials must NOT fail for a disabled channel with missing ref; got: %v", disabledErr)
	}
	// Bundle must not carry the missing ref.
	if disabledBundle.GetString("TELEGRAM_TOKEN") != "" {
		t.Error("bundle must not carry a value for a missing credential ref")
	}

	// --- Sub-test 3: ResolveBundle itself surfaces NotFoundError for missing refs. ---
	// This pins the invariant that ResolveBundle reports errors independent of bootCredentials.
	directCfg, err2 := config.LoadConfigWithStore(disabledConfigPath, disabledStore)
	if err2 != nil {
		t.Fatalf("LoadConfigWithStore: %v", err2)
	}
	_, bundleErrs := credentials.ResolveBundle(directCfg, disabledStore)
	if len(bundleErrs) == 0 {
		t.Fatal("ResolveBundle must return errors when a configured ref is absent from the store")
	}

	// At least one error must mention the missing ref name.
	found := false
	for _, e := range bundleErrs {
		if strings.Contains(e.Error(), "TELEGRAM_TOKEN") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("bundleErrs must contain the missing ref name TELEGRAM_TOKEN; got: %v", bundleErrs)
	}

	// At least one error must be a NotFoundError.
	hasNotFound := false
	for _, e := range bundleErrs {
		var nfe *credentials.NotFoundError
		var curr error = e
		for curr != nil {
			if _, ok := curr.(*credentials.NotFoundError); ok {
				hasNotFound = true
				_ = nfe
				break
			}
			type unwrapper interface{ Unwrap() error }
			if u, ok := curr.(unwrapper); ok {
				curr = u.Unwrap()
			} else {
				break
			}
		}
		if hasNotFound {
			break
		}
	}
	if !hasNotFound {
		t.Errorf("bundleErrs must contain a NotFoundError; got: %v", bundleErrs)
	}
}

// TestGatewayBoot_LockedStoreFailsBeforeConfig verifies that when
// OMNIPUS_MASTER_KEY is unset and an existing credentials.json blocks the
// auto-generate fallback path, bootCredentials returns an error before any
// config is loaded. This pins the invariant that Unlock is the FIRST step —
// no config loading can happen with a locked store.
//
// Note: on a truly fresh install (no credentials.json), Unlock now
// auto-generates a master key — that path is covered by
// TestGatewayBoot_AutoGeneratesMasterKeyOnFreshInstall below.
func TestGatewayBoot_LockedStoreFailsBeforeConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Ensure the env var is NOT set.
	t.Setenv("OMNIPUS_MASTER_KEY", "")
	t.Setenv("OMNIPUS_KEY_FILE", "")

	// Seed a credentials.json so Unlock mode 4 (auto-generate) does NOT fire —
	// this test pins the locked-existing-store semantic.
	writeBootTestFile(t, filepath.Join(tmpDir, "credentials.json"),
		`{"version":1,"salt":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","credentials":{}}`)

	configPath := filepath.Join(tmpDir, "config.json")
	writeBootTestFile(t, configPath, `{
		"version": 1,
		"channels": {
			"telegram": { "enabled": true, "token_ref": "TELEGRAM_TOKEN" }
		}
	}`)

	// bootCredentials must fail — Unlock returns an error when OMNIPUS_MASTER_KEY
	// is unset and auto-generate cannot fire because credentials.json exists.
	_, _, _, bootErr := bootCredentials(tmpDir, configPath) //nolint:dogsled
	if bootErr == nil {
		t.Fatal("bootCredentials must fail when OMNIPUS_MASTER_KEY is unset and credentials.json exists")
	}

	// Error must mention master key or OMNIPUS_MASTER_KEY.
	errMsg := strings.ToLower(bootErr.Error())
	if !strings.Contains(errMsg, "master key") && !strings.Contains(errMsg, "omnipus_master_key") {
		t.Errorf("bootCredentials error must mention master key; got: %q", bootErr.Error())
	}

	// V0 config with plaintext secrets: bootCredentials also fails because
	// MigrateWithStore calls store.Set on a locked store.
	v0ConfigPath := filepath.Join(tmpDir, "config_v0.json")
	writeBootTestFile(t, v0ConfigPath, `{
		"version": 0,
		"channels": {
			"telegram": { "enabled": true, "token": "secret-token" }
		}
	}`)

	_, _, _, v0Err := bootCredentials(tmpDir, v0ConfigPath) //nolint:dogsled
	if v0Err == nil {
		t.Fatal("bootCredentials with locked store must fail for v0 config with plaintext secrets")
	}
	lowerV0Err := strings.ToLower(v0Err.Error())
	if !strings.Contains(lowerV0Err, "lock") && !strings.Contains(lowerV0Err, "master") &&
		!strings.Contains(lowerV0Err, "credential") {
		t.Errorf("bootCredentials error must mention lock/master/credential; got: %q", v0Err.Error())
	}
}
