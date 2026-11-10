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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/datamodel"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// writeCorruptedCredentialsFile writes a credentials.json containing exactly
// one entry, credName, whose nonce/ciphertext are random bytes rather than a
// real AES-256-GCM seal produced with fixedHexKey. Decrypting it with ANY key
// fails GCM tag authentication (credentials.ErrWrongKey) — this simulates a
// corrupted store entry / wrong-master-key scenario, which is a fundamentally
// different failure mode than "ref not present in the store"
// (credentials.NotFoundError, covered by TestGatewayBoot_MissingChannelCredentialDegradesWithoutAborting
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
	// "version": 2 is the current, name-bound store format. This fixture models
	// a CURRENT store with one undecryptable entry; at "version": 1 the file
	// would instead be a pre-upgrade store and exercise the one-time migration
	// (covered by boot_credential_migration_test.go), not the read path.
	content := fmt.Sprintf(`{
		"version": 2,
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

func bootCredentialsError(homePath, configPath string) error {
	cfg, bundle, store, err := bootCredentials(homePath, configPath)
	_ = cfg
	_ = bundle
	_ = store
	return err
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

	err := bootCredentialsError(tmpDir, configPath)
	if err == nil {
		t.Fatal("bootCredentials must fail for a version:0 config.json (no v0 migration path)")
	}
	if !strings.Contains(err.Error(), "unsupported config version") {
		t.Errorf("bootCredentials error = %q, want it to mention \"unsupported config version\"", err.Error())
	}
}

// #1277 founder decision 2026-10-12: missing enabled-channel secrets degrade only that integration.
func TestGatewayBoot_MissingChannelCredentialDegradesWithoutAborting(t *testing.T) {
	f := newIssue1277Fixture(t)
	cfg, bundle, store := f.boot(t)
	if bundle.GetString(issue1277BrokenRef) != "" {
		t.Fatal("missing credential must not be present in the boot bundle")
	}
	// Resolution remains loud, independent of the boot fatal-vs-degrade gate.
	_, errs := credentials.ResolveBundle(cfg, store)
	missing := 0
	for _, err := range errs {
		var nf *credentials.NotFoundError
		if errors.As(err, &nf) && nf.Name == issue1277BrokenRef {
			missing++
		}
	}
	if missing != 1 {
		t.Fatalf("resolver must report exactly one NotFoundError naming %q, got %v", issue1277BrokenRef, errs)
	}
	api, svc, _, _ := f.startRuntime(t, cfg, bundle)
	issue1277RequireDegradedChannel(t, api, svc.ChannelManager)
	issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)
}

// #1277 founder decision 2026-10-12: one unreadable secret degrades its channel, not the gateway.
func TestGatewayBoot_UnreadableChannelCredentialDegradesWithoutAborting(t *testing.T) {
	f := newIssue1277Fixture(t)
	if err := f.store.Set(issue1277BrokenRef, "qa1277-before-tamper"); err != nil {
		t.Fatal(err)
	}
	issue1277TamperSingleEntry(t, f, issue1277BrokenRef)
	if value, err := f.store.Get(issue1277ProviderRef); err != nil || value != issue1277ProviderValue {
		t.Fatalf("positive control: the store key and healthy provider must still resolve, err=%v", err)
	}
	cfg, bundle, _ := f.boot(t)
	api, svc, _, _ := f.startRuntime(t, cfg, bundle)
	issue1277RequireDegradedChannel(t, api, svc.ChannelManager)
	issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)
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
	bootErr := bootCredentialsError(tmpDir, configPath)
	if bootErr == nil {
		t.Fatal("bootCredentials must fail when OMNIPUS_MASTER_KEY is unset and credentials.json exists")
	}

	// Error must mention master key or OMNIPUS_MASTER_KEY.
	errMsg := strings.ToLower(bootErr.Error())
	if !strings.Contains(errMsg, "master key") && !strings.Contains(errMsg, "omnipus_master_key") {
		t.Errorf("bootCredentials error must mention master key; got: %q", bootErr.Error())
	}
}

// TestBootOrder_DataModelInitLogsReachGatewayLog is the FIX 1 revert-proof:
// it calls bootLoggingAndDataModel — the exact function RunContextWithOptions
// calls for the logging-setup/datamodel.Init slice of its boot preamble — and
// asserts that datamodel.Init's own real first-run slog.Info line ("first-run
// setup complete — default config written") lands in gateway.log.
//
// Unlike slog_bridge_wiring_test.go (which proves the bridge mechanism in
// isolation with a synthetic slog.Warn stand-in), this test exercises the
// actual early-boot subsystem call whose lost visibility was the reported
// bug: two independent reviewers (plus a third verifying pass) found that
// datamodel.Init used to run BEFORE installSlogBridge/logger.EnableFileLogging
// existed, so this exact line was permanently lost on every fresh install —
// invisible in gateway.log, and (since it also preceded logger.InitPanic's
// stderr redirect) not even captured in gateway_panic.log.
//
// Revert-proof: reverting bootLoggingAndDataModel to call datamodel.Init
// before logger.EnableFileLogging/installSlogBridge (the pre-fix order)
// makes this test fail — at the time datamodel.Init logs, slog.Default()
// would still be whatever it was before the bridge install, so the line
// would never reach the file this test reads back.
func TestBootOrder_DataModelInitLogsReachGatewayLog(t *testing.T) {
	homePath := t.TempDir()

	prevDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevDefault) })
	prevLevel := logger.GetLevel()
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})
	logger.SetLevel(logger.INFO)

	if err := bootLoggingAndDataModel(homePath); err != nil {
		t.Fatalf("bootLoggingAndDataModel: %v", err)
	}

	// Second proof within the same test: datamodel.Init's config.json must
	// actually have been written — proving Init itself really ran (not just
	// that some unrelated log line happens to match below).
	if _, statErr := os.Stat(filepath.Join(homePath, "config.json")); statErr != nil {
		t.Errorf("datamodel.Init must have written config.json: %v", statErr)
	}

	logFilePath := filepath.Join(homePath, logPath, logFile)
	data, err := os.ReadFile(logFilePath)
	if err != nil {
		t.Fatalf("reading gateway.log: %v", err)
	}
	logged := string(data)

	if !strings.Contains(logged, "first-run setup complete") {
		t.Errorf("datamodel.Init's first-run slog.Info line must reach gateway.log; got:\n%s", logged)
	}
	if !strings.Contains(logged, "default config written") {
		t.Errorf("datamodel.Init's first-run slog.Info fields must survive as structured fields; got:\n%s", logged)
	}
}

// TestBootOrder_OldOrderWouldLoseDataModelInitLogs is the companion negative
// proof: it replicates the PRE-FIX inline order (datamodel.Init called
// before logger.EnableFileLogging/installSlogBridge exist) directly against
// the real datamodel.Init function, and confirms its first-run log line does
// NOT reach gateway.log in that order — the exact bug FIX 1 closes. It does
// not call bootLoggingAndDataModel (which now runs the corrected order); it
// exists so the "why this order matters" claim in bootLoggingAndDataModel's
// doc comment is verified directly rather than only asserted in prose.
func TestBootOrder_OldOrderWouldLoseDataModelInitLogs(t *testing.T) {
	homePath := t.TempDir()

	prevDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevDefault) })
	// Stand-in for "whatever slog.Default() is before installSlogBridge is
	// ever called" — same reasoning as slog_bridge_wiring_test.go's own
	// negative test: a real, un-redirected stdlib default writes to
	// os.Stderr, which cannot be captured as a file assertion in-process.
	slog.SetDefault(slog.New(slog.NewTextHandler(discardWriter{}, nil)))

	// Pre-fix order: datamodel.Init runs first, with no logging wired up yet.
	if err := datamodel.Init(homePath); err != nil {
		t.Fatalf("datamodel.Init: %v", err)
	}

	// ...THEN logging gets wired up, exactly like the reverted gateway.go
	// code did.
	logFilePath := filepath.Join(homePath, logPath, logFile)
	if err := logger.EnableFileLogging(logFilePath); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)
	installSlogBridge()

	data, err := os.ReadFile(logFilePath)
	if err != nil {
		t.Fatalf("reading gateway.log: %v", err)
	}
	if strings.Contains(string(data), "first-run setup complete") {
		t.Error("datamodel.Init's first-run log line must NOT reach gateway.log when " +
			"Init runs before the bridge is installed — if it does, this negative control is broken")
	}
}

// withEditionForBootTest sets the package-level config.Edition for the
// duration of a test and restores it afterward, mirroring the withEdition
// save/restore helper in pkg/config/edition_test.go (that helper is
// unexported and lives in package config, so this is its gateway-package
// equivalent, operating on the same exported config.Edition var).
func withEditionForBootTest(t *testing.T, e string) {
	t.Helper()
	prev := config.Edition
	config.Edition = e
	t.Cleanup(func() { config.Edition = prev })
}

// TestGatewayBoot_EditionMisbuildRefusesToStart pins the ADR-0010 boot
// tripwire added to bootCredentials: a core-stamped binary whose config.json
// has a platform trust anchor configured (security.platform_auth.issuer) is a
// hosted or desktop build that lost its edition stamp, and must never boot —
// serving it would expose the password login on what is supposed to be a
// platform-only instance. A plain core build (no issuer configured) must
// still boot normally.
func TestGatewayBoot_EditionMisbuildRefusesToStart(t *testing.T) {
	withEditionForBootTest(t, config.EditionCore)

	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	// --- Sub-test 1: core edition + configured platform issuer → fatal. ---
	misbuiltConfigPath := filepath.Join(tmpDir, "config_misbuilt.json")
	writeBootTestFile(t, misbuiltConfigPath, `{
		"version": 1,
		"security": {
			"platform_auth": {
				"issuer": "https://platform.example"
			}
		},
		"gateway": { "host": "127.0.0.1", "port": 19995 }
	}`)

	err := bootCredentialsError(tmpDir, misbuiltConfigPath)
	if err == nil {
		t.Fatal("bootCredentials must refuse to start a core-stamped binary with a platform trust anchor configured")
	}
	if !strings.Contains(err.Error(), "refusing to start") {
		t.Errorf("error must surface the EditionMisbuild refusal; got: %q", err.Error())
	}

	// --- Sub-test 2: core edition, no platform issuer configured → boots normally. ---
	plainConfigPath := filepath.Join(tmpDir, "config_plain.json")
	writeBootTestFile(t, plainConfigPath, `{
		"version": 1,
		"gateway": { "host": "127.0.0.1", "port": 19994 }
	}`)

	if err := bootCredentialsError(tmpDir, plainConfigPath); err != nil {
		t.Fatalf("bootCredentials must NOT fail for a plain core build with no platform trust anchor; got: %v", err)
	}
}
