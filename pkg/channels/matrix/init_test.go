//go:build goolm

package matrix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
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

// TestMigrateLegacyMatrixCryptoStore_InterruptedMigration_ResumesAndPreservesData
// is the regression test for the CRITICAL crash-window bug: if the process is
// killed after the first rename (channelDir -> tmpPath) but before the final
// rename (tmpPath -> namespacedPath) completes, channelDir no longer contains
// the legacy flat file on the next boot. The old code's ENTRY check was a
// bare os.Stat(legacyDBFile) failure, which is indistinguishable from a
// genuine fresh install, so it returned silently — permanently orphaning the
// real Olm/Megolm store staged at tmpPath with zero diagnostic trail.
//
// This simulates exactly that crash window: tmpPath exists with realistic
// staged content, legacyDBFile does NOT exist (channelDir was never
// recreated), and the namespaced destination does not exist yet either
// (nothing has used it since the crash). The fix must detect this
// independent of the legacyDBFile check and, since it is unambiguously safe
// to do so (destination still empty), automatically complete the interrupted
// move rather than silently treating it as a fresh install.
func TestMigrateLegacyMatrixCryptoStore_InterruptedMigration_ResumesAndPreservesData(t *testing.T) {
	base := t.TempDir()
	// channelDir is deliberately never (re)created here — this simulates a
	// crash between the first os.Rename(channelDir, tmpPath) and the
	// subsequent os.MkdirAll(channelDir, ...) that would normally recreate it.
	channelDir := filepath.Join(base, "matrix")
	tmpPath := channelDir + ".pre-namespacing-migration-tmp"
	if err := os.MkdirAll(tmpPath, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	stagedFile := filepath.Join(tmpPath, dbName)
	if err := os.WriteFile(stagedFile, []byte("real-olm-crypto-keys"), 0o600); err != nil {
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
		t.Fatalf("expected the interrupted migration to be auto-resumed into %s, got error: %v", migratedFile, err)
	}
	if string(data) != "real-olm-crypto-keys" {
		t.Fatalf("resumed migration data mismatch: got %q, want %q", string(data), "real-olm-crypto-keys")
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("expected the temp staging path to be consumed by the resumed migration, stat err = %v", err)
	}
}

// TestMigrateLegacyMatrixCryptoStore_InterruptedMigration_DestinationAlreadyExists_WarnsWithoutDataLoss
// covers the other half of the crash-window fix: a crash leaves the real
// store stranded at tmpPath, but by the time this runs again, something else
// (e.g. NewMatrixChannel on an earlier boot, since migration never blocks
// channel construction) has already created a fresh, empty store at
// namespacedPath. Auto-resuming here would silently overwrite and destroy
// whatever the fresh store had already accumulated, so the fix must refuse to
// touch either path and instead loudly and unambiguously tell the operator
// that the ORIGINAL store is still recoverable at tmpPath — never silent.
func TestMigrateLegacyMatrixCryptoStore_InterruptedMigration_DestinationAlreadyExists_WarnsWithoutDataLoss(
	t *testing.T,
) {
	base := t.TempDir()
	channelDir := filepath.Join(base, "matrix")
	tmpPath := channelDir + ".pre-namespacing-migration-tmp"
	if err := os.MkdirAll(tmpPath, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	stagedFile := filepath.Join(tmpPath, dbName)
	if err := os.WriteFile(stagedFile, []byte("real-olm-crypto-keys"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	namespacedPath := filepath.Join(channelDir, "matrix")
	if err := os.MkdirAll(namespacedPath, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	freshFile := filepath.Join(namespacedPath, dbName)
	if err := os.WriteFile(freshFile, []byte("fresh-store-already-in-use"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"matrix": {Type: "matrix", Enabled: true},
		},
	}

	// Capture logger output (this repo's standard pattern — see
	// pkg/providers/fallback_test.go) so the fix's loud warning can be
	// asserted directly, not just inferred from filesystem side effects.
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "matrix-migration.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	migrateLegacyMatrixCryptoStore(cfg, channelDir, "matrix", namespacedPath)

	// Neither path may be touched: the fresh store already in use at
	// namespacedPath must not be overwritten, and the stranded original at
	// tmpPath must not be deleted or altered — it is the operator's only
	// remaining recovery path.
	data, err := os.ReadFile(freshFile)
	if err != nil {
		t.Fatalf("expected the fresh namespaced store to remain in place, got error: %v", err)
	}
	if string(data) != "fresh-store-already-in-use" {
		t.Fatalf("fresh namespaced store was overwritten: got %q", string(data))
	}
	staged, err := os.ReadFile(stagedFile)
	if err != nil {
		t.Fatalf("expected the stranded original store to remain recoverable at %s, got error: %v", stagedFile, err)
	}
	if string(staged) != "real-olm-crypto-keys" {
		t.Fatalf("stranded original store was corrupted: got %q", string(staged))
	}

	logged, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logFile, err)
	}
	logStr := string(logged)
	if !strings.Contains(logStr, "interrupted") {
		t.Errorf("log file missing an explanation that migration was interrupted; got:\n%s", logStr)
	}
	if !strings.Contains(logStr, tmpPath) {
		t.Errorf(
			"log file missing the recoverable temp path %q an operator needs to manually recover the store; got:\n%s",
			tmpPath,
			logStr,
		)
	}
}
