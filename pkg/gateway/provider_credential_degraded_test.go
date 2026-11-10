// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// A provider whose credential cannot be resolved must not take the gateway
// down with it.
//
// The incident (2026-08-14): a config.json carrying TWO openrouter entries —
// the seeded one plus an onboarding-created one whose api_key_ref was
// "openrouter_API_KEY" (rest_onboarding.go derives it as body.Provider.Id +
// "_API_KEY") — booted fine while both credentials existed. Delete the second
// credential and the gateway logged "provider credential injection failed" and
// EXITED. One stale line in config.json bricked the whole install: no UI, no
// API, no way to remove the entry short of hand-editing JSON.
//
// The fix must not trade a loud failure for a silent one, which is what these
// tests pin: boot SURVIVES, and the broken provider is unmistakable — an
// ERROR record naming the provider and the credential, and (when it backs the
// default model) a provider that answers every chat turn with the real reason
// instead of an upstream 401.

package gateway

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// captureSlogJSON installs a JSON slog handler over a buffer for the duration of
// the test and returns the buffer. Level Debug so nothing is filtered out and
// a level regression (ERROR silently demoted to WARN/INFO) is still visible to
// the assertions rather than swallowed by the handler.
func captureSlogJSON(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// requireSlogRecord asserts that the captured log contains a record at the
// given level whose msg + attribute VALUES contain every needle. Attribute
// values are matched, not the whole line, so a needle cannot accidentally be
// satisfied by a key name.
func requireSlogRecord(t *testing.T, buf *bytes.Buffer, level string, needles ...string) {
	t.Helper()
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if lv, _ := rec["level"].(string); lv != level {
			continue
		}
		var flat strings.Builder
		for _, v := range rec {
			if s, ok := v.(string); ok {
				flat.WriteString(s)
				flat.WriteString("\n")
			}
		}
		hay := flat.String()
		all := true
		for _, n := range needles {
			if !strings.Contains(hay, n) {
				all = false
				break
			}
		}
		if all {
			return
		}
	}
	t.Fatalf("no %s log record containing %v; captured log:\n%s", level, needles, buf.String())
}

// seedCredential stores one credential in homePath/credentials.json using the
// fixed test master key (which the caller must already have exported).
func seedCredential(t *testing.T, homePath, ref, value string) {
	t.Helper()
	store := credentials.NewStore(filepath.Join(homePath, "credentials.json"))
	if err := credentials.Unlock(store); err != nil {
		t.Fatalf("unlock credential store: %v", err)
	}
	if err := store.Set(ref, value); err != nil {
		t.Fatalf("store.Set(%s): %v", ref, err)
	}
}

// TestGatewayBoot_StaleProviderCredentialDoesNotAbortBoot is the direct
// regression for the incident: one working provider, one whose api_key_ref
// names a credential that is not in the vault. Boot must complete, the healthy
// provider's key must still be injected, and the broken one must be reported
// at ERROR naming BOTH the provider and the credential.
func TestGatewayBoot_StaleProviderCredentialDoesNotAbortBoot(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	const goodRef = "DEGRADED_TEST_OPENROUTER_API_KEY"
	const staleRef = "DEGRADED_TEST_openrouter_API_KEY_STALE"
	// Injection writes into the process env; t.Setenv restores both on cleanup.
	t.Setenv(goodRef, "")
	t.Setenv(staleRef, "")

	seedCredential(t, tmpDir, goodRef, "sk-good-key")
	// staleRef is deliberately never stored — this is the leftover entry.

	configPath := filepath.Join(tmpDir, "config.json")
	writeBootTestFile(t, configPath, `{
		"version": 1,
		"providers": [
			{
				"model_name": "openrouter-auto",
				"model": "openrouter/z-ai/glm-5-turbo",
				"provider": "openrouter",
				"api_base": "https://openrouter.ai/api/v1",
				"api_key_ref": "`+goodRef+`"
			},
			{
				"model_name": "openrouter-onboarding",
				"model": "openrouter/z-ai/glm-5-turbo",
				"provider": "openrouter",
				"api_base": "https://openrouter.ai/api/v1",
				"api_key_ref": "`+staleRef+`"
			}
		],
		"gateway": { "host": "127.0.0.1", "port": 19987 }
	}`)

	logBuf := captureSlogJSON(t)

	cfg, _, _, err := bootCredentials(tmpDir, configPath)
	if err != nil {
		t.Fatalf(
			"boot must SURVIVE a provider whose credential is missing — one stale providers[] entry "+
				"must not brick the install; got: %v",
			err,
		)
	}
	if cfg == nil {
		t.Fatal("bootCredentials returned a nil config on success")
	}

	// The healthy provider is unaffected.
	if got := os.Getenv(goodRef); got != "sk-good-key" {
		t.Errorf("healthy provider's key must still be injected: env %s = %q", goodRef, got)
	}
	// The broken one injected nothing.
	if got := os.Getenv(staleRef); got != "" {
		t.Errorf("missing credential must not be injected: env %s = %q", staleRef, got)
	}

	// Loud, not silent: ERROR naming the provider entry AND the credential.
	requireSlogRecord(t, logBuf, "ERROR", "openrouter", staleRef)
}

// TestGatewayBoot_OnlyBrokenProviderStillBoots covers the worse shape: the
// install's ONLY provider is broken. There is nothing usable left, and the
// gateway must still come up — that is the fresh-install state too (a box with
// no provider configured at all boots fine), and it is the only state from
// which an operator can repair the credential in the UI.
func TestGatewayBoot_OnlyBrokenProviderStillBoots(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	const staleRef = "DEGRADED_TEST_ONLY_PROVIDER_KEY"
	t.Setenv(staleRef, "")

	// Create the store (so it exists and is unlocked) but store nothing in it.
	seedCredential(t, tmpDir, "DEGRADED_TEST_UNRELATED", "x")

	configPath := filepath.Join(tmpDir, "config.json")
	writeBootTestFile(t, configPath, `{
		"version": 1,
		"providers": [
			{
				"model_name": "openrouter-auto",
				"model": "openrouter/z-ai/glm-5-turbo",
				"provider": "openrouter",
				"api_base": "https://openrouter.ai/api/v1",
				"api_key_ref": "`+staleRef+`"
			}
		],
		"gateway": { "host": "127.0.0.1", "port": 19986 }
	}`)

	logBuf := captureSlogJSON(t)

	if _, _, _, err := bootCredentials(tmpDir, configPath); err != nil {
		t.Fatalf("boot must survive with NO usable provider at all; got: %v", err)
	}
	requireSlogRecord(t, logBuf, "ERROR", "openrouter", staleRef)
}

// #1277 founder decision 2026-10-12: the whole store under a wrong master key stays fatal.
// Unlike the old single-corrupted-entry fixture, this seals real entries with
// fixedHexKey first, then selects a genuinely different master key for boot.
func TestGatewayBoot_WrongMasterKeyProviderCredentialIsFatal(t *testing.T) {
	f := newIssue1277Fixture(t)
	t.Setenv("OMNIPUS_MASTER_KEY", strings.Repeat("ff", 32))
	logBuf := captureSlogJSON(t)
	err := bootCredentialsError(f.home, f.configPath)
	if err == nil {
		t.Fatal("boot must fail for a whole-store wrong master key, never degrade it as one missing provider ref")
	}
	if !strings.Contains(err.Error(), "failed authentication") || !strings.Contains(err.Error(), "master key") {
		t.Errorf("fatal error must carry the entry-authentication/master-key cause, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), issue1277ProviderRef) {
		t.Errorf("fatal error must identify the affected provider ref, got %q", err.Error())
	}
	requireSlogRecord(t, logBuf, "ERROR", "qa1277", issue1277ProviderRef)
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, "rest of the system continues") {
			t.Errorf("whole-store failure must not be described as a scoped degradation: %s", line)
		}
	}
}

// TestGatewayBoot_CorruptCredentialsFileProviderInjectionIsFatal covers the
// third failure exit credentials.Store.Get has (the review's D1 evidence
// distinguishes it from ErrWrongKey): loadFileInternal failing to even parse
// credentials.json as JSON. UnlockWithKey never reads the file (it just
// copies the key bytes), so this is NOT caught earlier at the Unlock step —
// it only surfaces when InjectFromConfig calls store.Get for the first ref,
// and it must be just as fatal as ErrWrongKey and ErrStoreLocked.
func TestGatewayBoot_CorruptCredentialsFileProviderInjectionIsFatal(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	const ref = "DEGRADED_TEST_CORRUPT_FILE_PROVIDER_KEY"
	t.Setenv(ref, "")

	// Not valid JSON at all — loadFileInternal's json.Unmarshal fails.
	writeBootTestFile(t, filepath.Join(tmpDir, "credentials.json"), `{not valid json`)

	configPath := filepath.Join(tmpDir, "config.json")
	writeBootTestFile(t, configPath, `{
		"version": 1,
		"providers": [
			{
				"model_name": "openrouter-auto",
				"model": "openrouter/z-ai/glm-5-turbo",
				"provider": "openrouter",
				"api_base": "https://openrouter.ai/api/v1",
				"api_key_ref": "`+ref+`"
			}
		],
		"gateway": { "host": "127.0.0.1", "port": 19984 }
	}`)

	err := bootCredentialsError(tmpDir, configPath)
	if err == nil {
		t.Fatal("bootCredentials must FAIL when credentials.json itself cannot be parsed — store-wide, not scoped")
	}
	if !strings.Contains(err.Error(), "corrupted") {
		t.Errorf("error must surface the store-file-corrupted cause; got: %q", err.Error())
	}
}

// TestProviderCredentialDegraded_GETRowsConfiguredOnly — ADR-068 regression
// row 4 (X-32): with the credential vault locked, GET /api/v1/providers
// reports the vault-read error on CONFIGURED rows only. The seed templates
// (no provider identity, no credential ref) are no longer echoed as
// "disconnected" rows at all (resolution #16), so a locked vault produces
// exactly one row: the configured provider, status=error, with the
// classified remediation message.
func TestProviderCredentialDegraded_GETRowsConfiguredOnly(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// A store that is never unlocked: resolveCredentialRef fails with a
	// non-NotFound error, the "worse than not configured" branch.
	api.credStore = credentials.NewStore(filepath.Join(api.homePath, "credentials.json"))

	const ref = "DEGRADED_TEST_T068_04_LOCKED_KEY"
	t.Setenv(ref, "") // ref configured, nothing injected
	seedTemplateProviders(t, api, &config.ModelConfig{
		Name: "mygw", Provider: "mygw", Model: "mygw/llama", APIKeyRef: ref,
		Models: []string{"mygw/llama"},
	})

	provs := getProviders(t, api)
	if len(provs) != 1 {
		t.Fatalf("locked vault must yield exactly the configured row, no template rows; got %d rows: %+v",
			len(provs), provs)
	}
	row := provs[0]
	if row.Id != "mygw" {
		t.Fatalf("row id = %q, want mygw", row.Id)
	}
	if row.Status != gen.ProviderStatusError {
		t.Errorf("status = %q, want %q (locked vault is worse than 'not configured')",
			row.Status, gen.ProviderStatusError)
	}
	if row.Error == nil || !strings.Contains(*row.Error, "credential vault could not be read") {
		t.Errorf("error must carry the classified vault-read remediation; got %v", row.Error)
	}
}
