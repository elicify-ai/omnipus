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
	"context"
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
	requireSlogRecord(t, logBuf, "ERROR", "openrouter-onboarding", staleRef)
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
	requireSlogRecord(t, logBuf, "ERROR", "openrouter-auto", staleRef)
}

// TestCreateStartupProvider_BlockedDefaultModelNamesTheCredential pins the
// second honesty surface. Once boot survives, the factory would happily build
// an HTTP provider with an EMPTY api key (api_base alone satisfies it), and the
// operator's first message would come back as a bare upstream 401 naming
// neither the provider nor the credential. Instead every turn must answer with
// the real cause.
func TestCreateStartupProvider_BlockedDefaultModelNamesTheCredential(t *testing.T) {
	const ref = "DEGRADED_TEST_BLOCKED_DEFAULT_KEY"
	t.Setenv(ref, "") // ref configured, credential never resolved

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{ModelName: "openrouter-auto"},
		},
		Providers: []*config.ModelConfig{{
			ModelName: "openrouter-auto",
			Model:     "openrouter/z-ai/glm-5-turbo",
			Provider:  "openrouter",
			APIBase:   "https://openrouter.ai/api/v1",
			APIKeyRef: ref,
		}},
	}

	p, _, err := createStartupProvider(cfg, false)
	if err != nil {
		t.Fatalf("createStartupProvider must not fail when the default model's credential is missing: %v", err)
	}
	if _, ok := p.(*startupBlockedProvider); !ok {
		t.Fatalf(
			"expected a startupBlockedProvider for a default model with an unresolvable credential, got %T "+
				"— an HTTP provider with an empty key would 401 with no mention of the real cause",
			p,
		)
	}
	_, chatErr := p.Chat(context.Background(), nil, nil, "", nil)
	if chatErr == nil {
		t.Fatal("a blocked provider must fail every chat turn")
	}
	if !strings.Contains(chatErr.Error(), ref) || !strings.Contains(chatErr.Error(), "openrouter-auto") {
		t.Errorf(
			"the chat error must name the model and the missing credential so the operator can act; got: %q",
			chatErr.Error(),
		)
	}
}

// TestCreateStartupProvider_ResolvedCredentialIsNotBlocked is the control: the
// same config with the credential present must build the real provider. Without
// it, the test above would still pass if createStartupProvider blocked
// unconditionally.
func TestCreateStartupProvider_ResolvedCredentialIsNotBlocked(t *testing.T) {
	const ref = "DEGRADED_TEST_RESOLVED_DEFAULT_KEY"
	t.Setenv(ref, "sk-resolved")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{ModelName: "openrouter-auto"},
		},
		Providers: []*config.ModelConfig{{
			ModelName: "openrouter-auto",
			Model:     "openrouter/z-ai/glm-5-turbo",
			Provider:  "openrouter",
			APIBase:   "https://openrouter.ai/api/v1",
			APIKeyRef: ref,
		}},
	}

	p, _, err := createStartupProvider(cfg, false)
	if err != nil {
		t.Fatalf("createStartupProvider: %v", err)
	}
	if _, blocked := p.(*startupBlockedProvider); blocked {
		t.Fatal("a provider whose credential resolves must NOT be blocked")
	}
}

// TestCreateStartupProvider_LoadBalancedSiblingKeepsModelUsable guards the
// multi-entry case: several providers[] entries may share one model_name for
// load balancing (config.GetModelConfig round-robins over them). One broken
// sibling must not disable a model that still has a working entry.
func TestCreateStartupProvider_LoadBalancedSiblingKeepsModelUsable(t *testing.T) {
	const goodRef = "DEGRADED_TEST_LB_GOOD_KEY"
	const badRef = "DEGRADED_TEST_LB_BAD_KEY"
	t.Setenv(goodRef, "sk-good")
	t.Setenv(badRef, "")

	entry := func(ref string) *config.ModelConfig {
		return &config.ModelConfig{
			ModelName: "openrouter-auto",
			Model:     "openrouter/z-ai/glm-5-turbo",
			Provider:  "openrouter",
			APIBase:   "https://openrouter.ai/api/v1",
			APIKeyRef: ref,
		}
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{ModelName: "openrouter-auto"},
		},
		Providers: []*config.ModelConfig{entry(badRef), entry(goodRef)},
	}

	p, _, err := createStartupProvider(cfg, false)
	if err != nil {
		t.Fatalf("createStartupProvider: %v", err)
	}
	if _, blocked := p.(*startupBlockedProvider); blocked {
		t.Fatal("a model with at least one usable load-balanced entry must not be blocked")
	}
}

// TestGatewayBoot_WrongMasterKeyProviderCredentialIsFatal is the D1 review
// regression (2026-08-15): a *credentials.CredentialRefError is NOT, by
// itself, a safe signal to degrade the referencing provider and keep booting.
// When the ref IS present in the store but fails to decrypt — a wrong or
// rotated master key, or a corrupted store entry — credentials.Store.Get
// returns credentials.ErrWrongKey, not a *credentials.NotFoundError.
// UnlockWithKey performs no verification against the stored data, so a stale
// master.key unlocks cleanly (IsLocked() is false) and EVERY credential in
// the store is equally unreadable — this is store-wide, exactly like the
// bare ErrStoreLocked case, even though InjectFromConfig's per-ref loop
// attributes the failure to just the one provider it happened to be
// resolving. Before this fix, reportInjectionErrors degraded ANY
// *CredentialRefError regardless of its cause, so this scenario booted
// successfully with a single ERROR log line — "BOOT SURVIVED a wrong master
// key" per the review's live evidence.
func TestGatewayBoot_WrongMasterKeyProviderCredentialIsFatal(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	const ref = "DEGRADED_TEST_WRONGKEY_PROVIDER_KEY"
	t.Setenv(ref, "")

	credsPath := filepath.Join(tmpDir, "credentials.json")
	// writeCorruptedCredentialsFile (boot_order_test.go) seeds an entry whose
	// nonce/ciphertext are random bytes, not a real seal under fixedHexKey —
	// GCM tag authentication fails regardless of which key decrypts it,
	// simulating both "wrong master key" and "corrupted store entry" at once
	// (Store.Get cannot and does not distinguish the two; both are
	// ErrWrongKey).
	writeCorruptedCredentialsFile(t, credsPath, ref)

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
		"gateway": { "host": "127.0.0.1", "port": 19985 }
	}`)

	logBuf := captureSlogJSON(t)

	_, _, _, err := bootCredentials(tmpDir, configPath) //nolint:dogsled
	if err == nil {
		t.Fatal(
			"bootCredentials must FAIL when a provider's credential ref is present in the store but " +
				"fails to decrypt (wrong master key / corrupted entry) — this is store-wide, not scoped " +
				"to one provider, and must not be degraded like a simple missing ref",
		)
	}
	// The underlying credentials.ErrWrongKey text must survive into the
	// returned error so the operator sees the real cause, not a generic
	// "injection failed".
	if !strings.Contains(err.Error(), "wrong master key") {
		t.Errorf("error must surface the ErrWrongKey cause (\"wrong master key\"); got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), ref) {
		t.Errorf("error must mention the failing ref %s; got: %q", ref, err.Error())
	}

	// Loud at ERROR, naming the provider and the ref, and explicitly NOT
	// worded as the scoped "credential unusable ... rest of the system
	// continues" message the NotFoundError-degrade branch emits — that
	// message would be a lie here.
	requireSlogRecord(t, logBuf, "ERROR", "openrouter-auto", ref)
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, "rest of the system continues") {
			t.Errorf(
				"a store-wide decrypt failure must not be logged with the scoped-degrade wording; got line: %s",
				line,
			)
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

	_, _, _, err := bootCredentials(tmpDir, configPath) //nolint:dogsled
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
		ModelName: "mygw", Provider: "mygw", Model: "mygw/llama", APIKeyRef: ref,
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
