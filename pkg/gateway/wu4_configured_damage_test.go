package gateway

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	wu4P0ARef          = "WU4_P0A_CONFIGURED_DAMAGED"
	wu4P0AValue        = "wu4-p0a-original-configured-value"
	wu4P0AMailRef      = "WU4_P0A_HEALTHY_MAILBOX"
	wu4P0AMailValue    = "wu4-p0a-healthy-mailbox-password"
	wu4P0ABadNonce     = "p0a-nonce11" // 11 bytes: one below GCM's 12-byte nonce.
	wu4P0AMalformed    = "WU4_P0A_NONCE_NOT_BASE64%%%"
	wu4P0AProviderName = "wu4-p0a-damaged-provider"
)

// Oracle: founder P0-a brief, 2026-10-17. Error implementation types are not
// asserted: only continued service, a clear scoped error and no leaked material.
func newWU4P0AConfiguredFixture(t *testing.T, consumer string) (*issue1277Fixture, string) {
	t.Helper()
	f := newIssue1277Fixture(t)
	t.Cleanup(f.store.Close)
	t.Setenv(wu4P0ARef, "")
	t.Setenv(wu4P0AMailRef, "")
	require.NoError(t, f.store.Set(issue1277BrokenRef, wu4P0AValue))
	require.NoError(t, f.store.Set(wu4P0AMailRef, wu4P0AMailValue))
	f.cfg.Mailboxes = config.MailboxesConfig{
		"wu4-p0a-agent": {"healthy-workspace": {
			Enabled: true, WorkspaceID: "healthy-workspace", PasswordRef: wu4P0AMailRef,
		}},
	}
	ref := wu4P0ARef
	switch consumer {
	case "provider":
		f.cfg.Providers = append(f.cfg.Providers, &config.ModelConfig{
			Provider: wu4P0AProviderName, Model: "health-probe", Models: []string{"health-probe"},
			Custom: true, Protocol: "openai-compatible", APIBase: f.cfg.Providers[0].APIBase, APIKeyRef: ref,
		})
	case "mailbox":
		f.cfg.Mailboxes["wu4-p0a-agent"]["damaged-workspace"] = config.MailboxConfig{
			Enabled: true, WorkspaceID: "damaged-workspace", PasswordRef: ref,
		}
	case "channel":
		ref = issue1277BrokenRef
	default:
		t.Fatalf("unknown P0-a consumer %q", consumer)
	}
	require.NoError(t, f.store.Set(ref, wu4P0AValue))
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	return f, ref
}

func wu4P0ARequireReadableSiblings(t *testing.T, store *credentials.Store) {
	t.Helper()
	for ref, want := range map[string]string{
		issue1277ProviderRef:         issue1277ProviderValue,
		issue1277HealthyRef:          "qa1277-healthy-secret",
		"QA1277_HEALTHY_LINE_ACCESS": issue1277HealthyAccess,
		issue1277BrokenAccessRef:     "qa1277-broken-access",
		wu4P0AMailRef:                wu4P0AMailValue,
	} {
		got, err := store.Get(ref)
		require.NoError(t, err, "P0-a: healthy sibling %s must remain readable", ref)
		require.Equal(t, want, got, "P0-a: healthy sibling %s must retain its exact value", ref)
	}
}

// Fault injection stays at the file boundary. Preserve the real ciphertext and
// every sibling; collect encoded material so leakage assertions watch real data.
func wu4P0ADamageNonce(t *testing.T, f *issue1277Fixture, ref, consumer string) ([]byte, []string) {
	t.Helper()
	path := filepath.Join(f.home, "credentials.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &document))
	var entries map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document["credentials"], &entries))
	require.Contains(t, entries, ref, "instrument: damage must target a stored configured entry")
	material := make([]string, 0, 7+2*len(entries)+3)
	material = append(material, wu4P0AValue, wu4P0AMailValue, issue1277ProviderValue,
		"qa1277-healthy-secret", issue1277HealthyAccess, "qa1277-broken-access", fixedHexKey)
	for _, entry := range entries {
		for _, field := range []string{"nonce", "ciphertext"} {
			var value string
			require.NoError(t, json.Unmarshal(entry[field], &value))
			require.NotEmpty(t, value, "instrument: real sealed %s must be present", field)
			material = append(material, value)
		}
	}
	nonce := base64.StdEncoding.EncodeToString([]byte(wu4P0ABadNonce))
	if consumer == "channel" {
		nonce = wu4P0AMalformed // Exercises a malformed configured channel entry, not a missing ref.
	}
	material = append(material, nonce, wu4P0ABadNonce, wu4P0AMalformed)
	entries[ref]["nonce"], err = json.Marshal(nonce)
	require.NoError(t, err)
	document["credentials"], err = json.Marshal(entries)
	require.NoError(t, err)
	raw, err = json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return raw, material
}

func wu4P0ARequireBundle(t *testing.T, cfg *config.Config, store *credentials.Store, ref string) []error {
	t.Helper()
	bundle, errs := credentials.ResolveBundle(cfg, store)
	require.NotContains(t, bundle, credentials.SecretRef(ref), "damaged material must not acquire a plaintext fallback")
	require.Len(t, errs, 1, "only the configured damaged ref may fail resolution")
	require.ErrorContains(t, errs[0], ref, "the scoped diagnostic must identify the configured ref")
	for sibling, want := range map[string]string{
		issue1277ProviderRef: issue1277ProviderValue, issue1277HealthyRef: "qa1277-healthy-secret",
		wu4P0AMailRef: wu4P0AMailValue,
	} {
		require.Equal(t, want, bundle.GetString(sibling), "healthy sibling %s must remain usable in the bundle", sibling)
	}
	return errs
}

func wu4P0ARequireConfiguredConsumer(t *testing.T, runtime issue1277RuntimeState, consumer, ref, boundary string) []string {
	t.Helper()
	var diagnostics []string
	switch consumer {
	case "provider":
		issue1277RequireProviderError(t, runtime.api, wu4P0AProviderName, ref)
	case "mailbox":
		mb := runtime.api.agentLoop.GetConfig().Mailboxes["wu4-p0a-agent"]["damaged-workspace"]
		require.True(t, mb.Enabled, "do not hide a damaged mailbox by disabling persisted intent")
		require.Equal(t, ref, mb.PasswordRef)
	case "channel":
		// REST refresh swaps config/redaction, not the channel manager. Boot
		// and reload are the channel-reconstruction boundaries in this pack.
		if boundary != "refresh" {
			issue1277RequireDegradedChannel(t, runtime.api, runtime.services.ChannelManager)
		}
	}
	for _, row := range getProviders(t, runtime.api) {
		if row.Error != nil {
			diagnostics = append(diagnostics, *row.Error)
		}
	}
	for _, row := range issue1277ChannelRows(t, runtime.api) {
		if row.DegradedReason != nil {
			diagnostics = append(diagnostics, *row.DegradedReason)
		}
	}
	return diagnostics
}

func wu4P0ARequireScopedDamage(t *testing.T, consumer, boundary string) {
	t.Helper()
	previousLevel := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	t.Cleanup(func() { logger.SetLevel(previousLevel) })
	f, ref := newWU4P0AConfiguredFixture(t, consumer)
	// Positive instrument: identical configured entries and real runtime work
	// before tampering; a broken fixture must not masquerade as the required RED.
	cfg, bundle, store := f.boot(t)
	t.Cleanup(store.Close)
	runtime := f.startRuntimeState(t, cfg, bundle)
	healthyValue, err := store.Get(ref)
	require.NoError(t, err)
	require.Equal(t, wu4P0AValue, healthyValue)
	issue1277RequireHealthyPeer(t, f, runtime.api, runtime.services.ChannelManager)
	wu4P0ARequireReadableSiblings(t, store)

	logBuf := captureSlogJSON(t)
	logPath := filepath.Join(f.home, "p0a-native.log")
	require.NoError(t, logger.EnableFileLogging(logPath))
	t.Cleanup(logger.DisableFileLogging)
	before, material := wu4P0ADamageNonce(t, f, ref, consumer)
	var diagnostics []string
	defer func() {
		// Run even on an early fatal regression; inspect the actual returned
		// error as well as logs, without fixing its private type or wording.
		nativeLog, logErr := os.ReadFile(logPath)
		assert.NoError(t, logErr)
		assert.Contains(t, string(nativeLog), "saving config", "instrument: native log capture must receive the real save")
		text := logBuf.String() + string(nativeLog) + "\n" + strings.Join(diagnostics, "\n")
		for _, secret := range material {
			assert.NotContains(t, text, secret, "P0-a: logs/errors must not disclose credential or nonce material")
		}
		after, readErr := os.ReadFile(filepath.Join(f.home, "credentials.json"))
		assert.NoError(t, readErr)
		assert.Equal(t, before, after, "handling scoped damage must not rewrite the credential file")
	}()
	var value string
	require.NotPanics(t, func() { value, err = store.Get(ref) }, "a malformed configured entry must return an error, not panic")
	require.Empty(t, value, "the damaged entry must return no plaintext")
	require.ErrorContains(t, err, ref)
	diagnostics = append(diagnostics, err.Error())
	wu4P0ARequireReadableSiblings(t, store)

	f.cfg.Gateway.LogLevel = "debug" // A successful update must actually apply an unrelated setting.
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	require.NotPanics(t, func() {
		switch boundary {
		case "boot":
			var bootStore *credentials.Store
			cfg, bundle, bootStore, err = bootCredentials(f.home, f.configPath)
			if bootStore != nil {
				t.Cleanup(bootStore.Close)
			}
		case "reload":
			var candidate *config.Config
			candidate, err = config.LoadConfigWithStore(f.configPath, store)
			if err == nil {
				err = executeReload(t.Context(), runtime.api.agentLoop, candidate, &runtime.provider,
					runtime.services, runtime.bus, false)
			}
		case "refresh":
			err = runtime.api.refreshConfigAndRewireServices(f.configPath)
		default:
			t.Fatalf("unknown P0-a boundary %q", boundary)
		}
	}, "P0-a: %s must not panic for a damaged configured %s", boundary, consumer)
	if err != nil {
		diagnostics = append(diagnostics, err.Error())
	}
	require.NoError(t, err, "P0-a: %s must continue with a scoped error for the damaged configured %s", boundary, consumer)
	if boundary == "boot" {
		require.NotNil(t, cfg)
		require.NotContains(t, bundle, credentials.SecretRef(ref))
		runtime = f.startRuntimeState(t, cfg, bundle)
	}
	liveCfg := runtime.api.agentLoop.GetConfig()
	require.Equal(t, "debug", liveCfg.Gateway.LogLevel, "the unrelated setting must apply, not silently roll back")
	for _, resolutionErr := range wu4P0ARequireBundle(t, liveCfg, store, ref) {
		diagnostics = append(diagnostics, resolutionErr.Error())
	}
	requireSlogRecord(t, logBuf, "ERROR", ref)
	diagnostics = append(diagnostics, wu4P0ARequireConfiguredConsumer(t, runtime, consumer, ref, boundary)...)
	wu4P0ARequireReadableSiblings(t, store)
	issue1277RequireHealthyPeer(t, f, runtime.api, runtime.services.ChannelManager)
}

func TestWU4P0AConfiguredDamageDegradesOnlyItsConsumer(t *testing.T) {
	for _, consumer := range []string{"provider", "mailbox", "channel"} {
		t.Run(consumer, func(t *testing.T) {
			for _, boundary := range []string{"boot", "reload", "refresh"} {
				t.Run(boundary, func(t *testing.T) { wu4P0ARequireScopedDamage(t, consumer, boundary) })
			}
		})
	}
}

func TestWU4P0AHealthyConfiguredConsumersApplyAllBoundaries(t *testing.T) {
	for _, consumer := range []string{"provider", "mailbox", "channel"} {
		t.Run(consumer, func(t *testing.T) {
			f, ref := newWU4P0AConfiguredFixture(t, consumer)
			cfg, bundle, store := f.boot(t)
			t.Cleanup(store.Close)
			runtime := f.startRuntimeState(t, cfg, bundle)
			require.Equal(t, wu4P0AValue, bundle.GetString(ref))
			f.cfg.Gateway.LogLevel = "debug"
			require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
			require.NoError(t, runtime.api.refreshConfigAndRewireServices(f.configPath))
			require.Equal(t, "debug", runtime.api.agentLoop.GetConfig().Gateway.LogLevel)
			f.cfg.Gateway.LogLevel = "info"
			require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
			candidate, err := config.LoadConfigWithStore(f.configPath, store)
			require.NoError(t, err)
			require.NoError(t, executeReload(t.Context(), runtime.api.agentLoop, candidate, &runtime.provider,
				runtime.services, runtime.bus, false))
			require.Equal(t, "info", runtime.api.agentLoop.GetConfig().Gateway.LogLevel)
			require.Equal(t, wu4P0AValue, runtime.services.bundle.GetString(ref))
			require.Empty(t, runtime.services.ChannelManager.FailedChannels())
			wu4P0ARequireReadableSiblings(t, store)
			issue1277RequireHealthyPeer(t, f, runtime.api, runtime.services.ChannelManager)
		})
	}
}
