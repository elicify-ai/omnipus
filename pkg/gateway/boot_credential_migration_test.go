// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Boot-level tests for the one-time credential store migration: an install
// upgraded from the pre-name-binding release must boot and inject every
// credential without re-entry, and a store whose migration is refused must
// stop boot with an error that names the entry to fix.

package gateway

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// writePreUpgradeStore writes credentials.json exactly as the pre-name-binding
// release did: "version": 1, every entry sealed with a nil AAD under
// fixedHexKey. corrupt names entries whose ciphertext gets one bit flipped.
func writePreUpgradeStore(t *testing.T, path string, values map[string]string, corrupt ...string) {
	t.Helper()
	key, err := hex.DecodeString(fixedHexKey)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		Nonce      string `json:"nonce"`
		Ciphertext string `json:"ciphertext"`
	}
	creds := map[string]entry{}
	for name, v := range values {
		nonce := make([]byte, 12)
		if _, rErr := io.ReadFull(rand.Reader, nonce); rErr != nil {
			t.Fatal(rErr)
		}
		ct := gcm.Seal(nil, nonce, []byte(v), nil)
		for _, c := range corrupt {
			if c == name {
				ct[0] ^= 0x01
			}
		}
		creds[name] = entry{base64.StdEncoding.EncodeToString(nonce), base64.StdEncoding.EncodeToString(ct)}
	}
	salt := make([]byte, 32)
	if _, rErr := io.ReadFull(rand.Reader, salt); rErr != nil {
		t.Fatal(rErr)
	}
	raw, err := json.Marshal(map[string]any{
		"version":     1,
		"salt":        base64.StdEncoding.EncodeToString(salt),
		"credentials": creds,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeBootTestFile(t, path, string(raw))
}

const migrationBootConfig = `{
	"version": 1,
	"providers": [
		{
			"model_name": "openai-main",
			"model": "openai/gpt-4o",
			"provider": "openai",
			"api_base": "https://api.openai.com/v1",
			"api_key_ref": "MIGRATION_TEST_openai_API_KEY"
		}
	],
	"gateway": { "host": "127.0.0.1", "port": 19984 }
}`

// TestGatewayBoot_PreUpgradeStoreMigratesAndBoots is the reported failure:
// before the migration this boot stopped with "credential store unreadable for
// a configured ref ... entry openai_API_KEY failed authentication".
func TestGatewayBoot_PreUpgradeStoreMigratesAndBoots(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)
	const ref = "MIGRATION_TEST_openai_API_KEY"
	t.Setenv(ref, "")

	credsPath := filepath.Join(tmpDir, "credentials.json")
	writePreUpgradeStore(t, credsPath, map[string]string{
		ref:                          "sk-openai-from-v0.1.0",
		"MIGRATION_TEST_UNRELATED":   "other-value",
		"channel_telegram_bot_token": "123:abc",
	})
	configPath := filepath.Join(tmpDir, "config.json")
	writeBootTestFile(t, configPath, migrationBootConfig)

	logBuf := captureSlogJSON(t)
	_, _, store, err := bootCredentials(tmpDir, configPath)
	if err != nil {
		t.Fatalf("an install upgraded from the pre-name-binding release must boot without re-entering keys; got: %v", err)
	}
	if got := os.Getenv(ref); got != "sk-openai-from-v0.1.0" {
		t.Errorf("the migrated provider key must be injected: env %s = %q", ref, got)
	}
	if v, gerr := store.Get("MIGRATION_TEST_UNRELATED"); gerr != nil || v != "other-value" {
		t.Errorf("every migrated entry must read back: got %q, %v", v, gerr)
	}
	requireSlogRecord(t, logBuf, "INFO", "credentials.store_migrated")

	// Second boot: already migrated, nothing re-runs.
	before, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err2 := bootCredentials(tmpDir, configPath); err2 != nil {
		t.Fatalf("second boot: %v", err2)
	}
	after, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a second boot must not rewrite an already-migrated store")
	}
}

// TestGatewayBoot_RefusedMigrationStopsBootNamingTheEntry pins the decision
// for requirement 3: a pre-upgrade store with an entry that fails even the
// legacy read STOPS boot (same fatal class as a wrong master key), names the
// entry, and leaves credentials.json untouched.
func TestGatewayBoot_RefusedMigrationStopsBootNamingTheEntry(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)
	const ref = "MIGRATION_TEST_openai_API_KEY"
	t.Setenv(ref, "")

	credsPath := filepath.Join(tmpDir, "credentials.json")
	writePreUpgradeStore(t, credsPath, map[string]string{
		ref:                     "sk-openai-from-v0.1.0",
		"MIGRATION_TEST_BROKEN": "broken-value",
	}, "MIGRATION_TEST_BROKEN")
	before, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(tmpDir, "config.json")
	writeBootTestFile(t, configPath, migrationBootConfig)

	err = bootCredentialsError(tmpDir, configPath)
	if err == nil {
		t.Fatal("a refused migration must stop boot")
	}
	if !strings.Contains(err.Error(), "MIGRATION_TEST_BROKEN") {
		t.Errorf("the error must name the entry to fix; got: %v", err)
	}
	if strings.Contains(err.Error(), "broken-value") || strings.Contains(err.Error(), "sk-openai") {
		t.Errorf("no secret value may appear in the error; got: %v", err)
	}
	var authErr *credentials.EntryAuthError
	if !errors.As(err, &authErr) || !errors.Is(err, credentials.ErrWrongKey) {
		t.Errorf("the refusal must keep the EntryAuthError / ErrWrongKey classification; got: %v", err)
	}
	after, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a refused migration must leave credentials.json byte-identical")
	}
	if os.Getenv(ref) != "" {
		t.Error("nothing may be injected when the migration is refused")
	}
}
