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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// writeCorruptedCredentialsFile writes a credentials.json containing exactly
// one entry, credName, whose nonce/ciphertext are random bytes rather than a
// real AES-256-GCM seal produced with fixedHexKey. Decrypting it with ANY key
// fails GCM tag authentication (credentials.ErrWrongKey) — this simulates a
// corrupted store entry / wrong-master-key scenario, which is a fundamentally
// different failure mode than "ref not present in the store"
// (credentials.NotFoundError, covered by TestGatewayBoot_MissingCredentialRefFailsFast
// above). The salt is unused by the OMNIPUS_MASTER_KEY unlock path (it only
// matters for passphrase-derived keys) but is included so the file matches
// the documented on-disk schema (pkg/credentials/store.go's package doc).
func writeCorruptedCredentialsFile(t *testing.T, path, credName string) {
	t.Helper()
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatalf("generate random nonce: %v", err)
	}
	ciphertext := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, ciphertext); err != nil {
		t.Fatalf("generate random ciphertext: %v", err)
	}
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		t.Fatalf("generate random salt: %v", err)
	}
	content := fmt.Sprintf(`{
		"version": 1,
		"salt": %q,
		"credentials": {
			%q: {
				"nonce": %q,
				"ciphertext": %q
			}
		}
	}`,
		base64.StdEncoding.EncodeToString(salt),
		credName,
		base64.StdEncoding.EncodeToString(nonce),
		base64.StdEncoding.EncodeToString(ciphertext),
	)
	writeBootTestFile(t, path, content)
}

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

// TestGatewayBoot_UnsupportedConfigVersionFailsFast verifies that a config.json
// predating the current schema (no v0 migration path exists any more) fails
// boot with a clear "unsupported config version" error rather than silently
// migrating or falling back to defaults.
func TestGatewayBoot_UnsupportedConfigVersionFailsFast(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	configPath := filepath.Join(tmpDir, "config.json")
	writeBootTestFile(t, configPath, `{
		"version": 0,
		"channels": {
			"telegram": {
				"enabled": true,
				"token": "12345:legacy-plaintext"
			}
		},
		"gateway": { "host": "127.0.0.1", "port": 19999 }
	}`)

	_, _, _, err := bootCredentials(tmpDir, configPath) //nolint:dogsled
	if err == nil {
		t.Fatal("bootCredentials must fail for a version:0 config.json (no v0 migration path)")
	}
	if !strings.Contains(err.Error(), "unsupported config version") {
		t.Errorf("bootCredentials error = %q, want it to mention \"unsupported config version\"", err.Error())
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

// TestGatewayBoot_CorruptedCredentialForEnabledChannelFailsFast verifies that
// bootCredentials aborts boot (fatal error, not merely a slog.Warn) when an
// ENABLED channel's credential ref IS present in the store but fails to
// decrypt — a corrupted store entry / wrong-master-key scenario. This is a
// worse failure than a simple NotFoundError (already covered by
// TestGatewayBoot_MissingCredentialRefFailsFast): the operator believes the
// channel is configured correctly, so silently continuing with a Warn would
// let the channel start without a usable credential. This pins the
// enabledRefFromBundleError escalation branch added alongside the
// NotFoundError-on-enabled-channel fatal branch in bootCredentials.
func TestGatewayBoot_CorruptedCredentialForEnabledChannelFailsFast(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	writeCorruptedCredentialsFile(t, credsPath, "TELEGRAM_TOKEN")

	configPath := filepath.Join(tmpDir, "config.json")
	writeBootTestFile(t, configPath, `{
		"version": 1,
		"channels": {
			"telegram": {
				"enabled": true,
				"token_ref": "TELEGRAM_TOKEN"
			}
		},
		"gateway": { "host": "127.0.0.1", "port": 19996 }
	}`)

	_, _, _, err := bootCredentials(tmpDir, configPath) //nolint:dogsled
	if err == nil {
		t.Fatal(
			"bootCredentials must fail when an enabled channel's credential ref fails to decrypt (corrupted store entry)",
		)
	}
	if !strings.Contains(err.Error(), "TELEGRAM_TOKEN") {
		t.Errorf("error must mention the failing ref TELEGRAM_TOKEN; got: %q", err.Error())
	}
	// Must route through the enabledRefFromBundleError escalation branch
	// ("failed to resolve" / "not simply missing"), not the NotFoundError
	// branch ("not found in store — ensure the credential is stored").
	if !strings.Contains(err.Error(), "failed to resolve") {
		t.Errorf(
			"error must indicate the ref failed to resolve (decrypt failure), not merely be missing; got: %q",
			err.Error(),
		)
	}
	if strings.Contains(err.Error(), "not found in store") {
		t.Errorf(
			"error must NOT be attributed to the NotFoundError branch (ref IS present, just undecryptable); got: %q",
			err.Error(),
		)
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
}
