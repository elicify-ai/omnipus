//go:build goolm

package matrix

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestMigrateLegacyMatrixCryptoStore_SingleInstance verifies the core fix for
// Finding 2: a pre-namespacing single-instance deployment (legacy store
// written directly at channelDir, e.g. "<workspace>/matrix/store.db") has its
// crypto store moved into the new instance-namespaced directory rather than
// silently orphaned, so E2E room history stays decryptable after upgrade.
func TestMigrateLegacyMatrixCryptoStore_SingleInstance(t *testing.T) {
	base := t.TempDir()
	channelDir := filepath.Join(base, "matrix")
	if err := os.MkdirAll(channelDir, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	legacyFile := filepath.Join(channelDir, dbName)
	if err := os.WriteFile(legacyFile, []byte("legacy-olm-store"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	namespacedPath := filepath.Join(channelDir, "matrix")
	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"matrix": {Type: "matrix", Enabled: true},
		},
	}

	migrateLegacyMatrixCryptoStore(cfg, channelDir, "matrix", namespacedPath)

	migratedFile := filepath.Join(namespacedPath, dbName)
	data, err := os.ReadFile(migratedFile)
	if err != nil {
		t.Fatalf("expected migrated store at %s, got error: %v", migratedFile, err)
	}
	if string(data) != "legacy-olm-store" {
		t.Fatalf("migrated store content mismatch: got %q", string(data))
	}
	if _, err := os.Stat(legacyFile); !os.IsNotExist(err) {
		t.Fatalf("expected legacy flat file to be gone after migration, stat err = %v", err)
	}
}

// TestMigrateLegacyMatrixCryptoStore_MultipleInstances_SkipsAmbiguousCase
// verifies the safety guard: when more than one Matrix instance is enabled,
// there is no way to know which instance's keys are in the shared legacy
// file, so migration must be skipped (not guessed) and the legacy store left
// exactly where it was.
func TestMigrateLegacyMatrixCryptoStore_MultipleInstances_SkipsAmbiguousCase(t *testing.T) {
	base := t.TempDir()
	channelDir := filepath.Join(base, "matrix")
	if err := os.MkdirAll(channelDir, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	legacyFile := filepath.Join(channelDir, dbName)
	if err := os.WriteFile(legacyFile, []byte("legacy-olm-store"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	namespacedPath := filepath.Join(channelDir, "matrix.eu")
	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"matrix.eu": {Type: "matrix", Enabled: true},
			"matrix.us": {Type: "matrix", Enabled: true},
		},
	}

	migrateLegacyMatrixCryptoStore(cfg, channelDir, "matrix.eu", namespacedPath)

	if _, err := os.Stat(legacyFile); err != nil {
		t.Fatalf("expected legacy file to remain untouched, stat err = %v", err)
	}
	if _, err := os.Stat(namespacedPath); !os.IsNotExist(err) {
		t.Fatalf("expected no namespaced destination to be created in the ambiguous case, stat err = %v", err)
	}
}

// TestMigrateLegacyMatrixCryptoStore_NoLegacyStore_NoOp verifies a fresh
// install (no legacy flat store on disk) is a silent no-op — no directories
// created, nothing migrated.
func TestMigrateLegacyMatrixCryptoStore_NoLegacyStore_NoOp(t *testing.T) {
	base := t.TempDir()
	channelDir := filepath.Join(base, "matrix") // deliberately never created
	namespacedPath := filepath.Join(channelDir, "matrix")
	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"matrix": {Type: "matrix", Enabled: true},
		},
	}

	migrateLegacyMatrixCryptoStore(cfg, channelDir, "matrix", namespacedPath)

	if _, err := os.Stat(channelDir); !os.IsNotExist(err) {
		t.Fatalf("expected no directory to be created on a fresh install, stat err = %v", err)
	}
}

// TestMigrateLegacyMatrixCryptoStore_DestinationExists_NeverOverwrites
// verifies the "already migrated" case is idempotent: if the namespaced
// destination already exists (e.g. a prior successful migration, or an
// instance that was always namespaced), the legacy file must be left alone
// and the destination must never be overwritten.
func TestMigrateLegacyMatrixCryptoStore_DestinationExists_NeverOverwrites(t *testing.T) {
	base := t.TempDir()
	channelDir := filepath.Join(base, "matrix")
	if err := os.MkdirAll(channelDir, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	legacyFile := filepath.Join(channelDir, dbName)
	if err := os.WriteFile(legacyFile, []byte("legacy-olm-store"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	namespacedPath := filepath.Join(channelDir, "matrix")
	if err := os.MkdirAll(namespacedPath, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	existingFile := filepath.Join(namespacedPath, dbName)
	if err := os.WriteFile(existingFile, []byte("already-namespaced-store"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"matrix": {Type: "matrix", Enabled: true},
		},
	}

	migrateLegacyMatrixCryptoStore(cfg, channelDir, "matrix", namespacedPath)

	data, err := os.ReadFile(existingFile)
	if err != nil {
		t.Fatalf("expected existing namespaced store to remain, got error: %v", err)
	}
	if string(data) != "already-namespaced-store" {
		t.Fatalf("existing namespaced store was overwritten: got %q", string(data))
	}
	if _, err := os.Stat(legacyFile); err != nil {
		t.Fatalf("expected legacy file to remain untouched since destination already existed, stat err = %v", err)
	}
}
