// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// withAdminCtx returns a request with an authenticated *config.UserConfig
// injected into context. Separate from the withAdminRole helper in
// rest_tool_policies_test.go to avoid a duplicate declaration (both live in
// package gateway). Under the single-user model there is no role to
// inject — the handlers this feeds (e.g. HandlePendingRestart) are called
// directly in these tests, bypassing the RequireNotBypass gate entirely, so
// no config snapshot is needed either.
func withAdminCtx(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), ctxkey.UserContextKey{}, &config.UserConfig{Username: "admin"})
	return r.WithContext(ctx)
}

// bootSnapshot loads the config at configPath the SAME way boot does
// (LoadConfig + ApplyWarmupTimeoutDefault), returning the resulting
// *config.Config to use as appliedConfig. This mirrors production:
// appliedConfig is the boot config WITH computed defaults applied, not the
// raw on-disk bytes. Centralizing it here guarantees the test's applied side
// is normalised identically to the handler's persisted side, so a
// clean-install (applied == persisted) yields an empty diff even for keys
// that only get a value via boot-time defaults (session.dm_scope).
// gateway.preview_enabled (ADR-044) needs no boot-time normalization here —
// Config.IsPreviewEnabled() resolves the nil-vs-false default live, on every
// read, so there is nothing to mirror at snapshot time.
func bootSnapshot(t *testing.T, configPath string) *config.Config {
	t.Helper()
	cfg, err := config.LoadConfig(configPath)
	require.NoError(t, err)
	cfg.Tools.ApplyWarmupTimeoutDefault()
	return cfg
}

// writeConfig marshals m and writes it to configPath. It injects
// version=CurrentVersion when absent so LoadConfig takes the side-effect-free
// `case CurrentVersion` branch (no v0 migration / makeBackup / deferred
// SaveConfig), matching how a post-boot on-disk config.json is shaped.
func writeConfig(t *testing.T, configPath string, m map[string]any) {
	t.Helper()
	if _, ok := m["version"]; !ok {
		withVersion := make(map[string]any, len(m)+1)
		for k, v := range m {
			withVersion[k] = v
		}
		withVersion["version"] = float64(config.CurrentVersion)
		m = withVersion
	}
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, raw, 0o600))
}

// newPendingRestartAPI builds a restAPI for pending-restart tests.
//
//   - persisted is written to the real config.json the handler reads.
//   - applied is the boot-time config map; it is normalised through the SAME
//     boot pipeline (bootSnapshot) to produce appliedConfig, exactly as
//     production does. A nil applied yields a nil appliedConfig (legacy callers
//     that pass nil to exercise method/role guards).
//
// Because both the applied and persisted sides now flow through bootSnapshot /
// the handler's identical normalisation, a clean install (applied == persisted)
// produces an empty diff, while a genuine post-boot edit on the persisted side
// still surfaces.
func newPendingRestartAPI(t *testing.T, applied, persisted map[string]any) *restAPI {
	t.Helper()
	tmpDir := t.TempDir()
	configPath := tmpDir + "/config.json"

	// Normalise the applied side through the boot pipeline (when provided).
	var appliedCfg *config.Config
	if applied != nil {
		appliedDir := t.TempDir()
		appliedPath := appliedDir + "/config.json"
		writeConfig(t, appliedPath, applied)
		appliedCfg = bootSnapshot(t, appliedPath)
	}

	// The persisted map is what the handler reads from disk.
	writeConfig(t, configPath, persisted)

	api := newTestRestAPIWithHome(t)
	api.homePath = tmpDir
	api.appliedConfig = appliedCfg
	return api
}

// decodeDiffs unmarshals the response body into a slice of pendingRestartEntry.
func decodeDiffs(t *testing.T, body []byte) []pendingRestartEntry {
	t.Helper()
	var diffs []pendingRestartEntry
	require.NoError(t, json.Unmarshal(body, &diffs))
	return diffs
}

// callPendingRestart drives the handler with an admin context and returns the
// recorder.
func callPendingRestart(t *testing.T, api *restAPI) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := withAdminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/config/pending-restart", nil))
	api.HandlePendingRestart(w, r)
	return w
}

// TestHandlePendingRestart_CleanInstallNoPhantomDiff is the core regression
// guard (FIX A): a fresh-install config.json that contains ONLY the gateway
// host/port and omits session.dm_scope must produce an EMPTY diff. Before the
// fix, the persisted side was the raw on-disk JSON (no defaults) while
// appliedConfig had dm_scope defaulted, so that field surfaced as a phantom
// "value → null" change.
func TestHandlePendingRestart_CleanInstallNoPhantomDiff(t *testing.T) {
	// Both applied (boot) and persisted (on-disk) are the SAME clean-install
	// config: only gateway host/port, with NO session.dm_scope (it gets a
	// computed default at boot but is absent on disk). The handler must
	// normalise the persisted side identically, so the diff is empty.
	clean := map[string]any{
		"version": float64(config.CurrentVersion),
		"gateway": map[string]any{
			"host": "127.0.0.1",
			"port": float64(5000),
		},
	}
	api := newPendingRestartAPI(t, clean, clean)

	w := callPendingRestart(t, api)
	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())

	// The whole point: empty diff on a clean install.
	assert.Empty(t, diffs, "clean install must yield no pending-restart diff; got %+v", diffs)

	// Belt-and-suspenders: the defaulted key must specifically NOT appear.
	for _, d := range diffs {
		assert.NotEqual(t, string(config.SessionDMScope), d.Key,
			"session.dm_scope must not appear as a phantom diff")
	}
}

// TestHandlePendingRestart_GenuineChangeStillShows verifies that a real
// post-boot edit to a restart-gated key (gateway.port 5000 → 8080 written to
// disk) STILL surfaces in the diff after the clean-install normalization fix.
func TestHandlePendingRestart_GenuineChangeStillShows(t *testing.T) {
	applied := map[string]any{
		"version": float64(config.CurrentVersion),
		"gateway": map[string]any{
			"host": "127.0.0.1",
			"port": float64(5000),
		},
	}
	// Persisted (on-disk) has gateway.port edited to 8080 after boot.
	persisted := map[string]any{
		"version": float64(config.CurrentVersion),
		"gateway": map[string]any{
			"host": "127.0.0.1",
			"port": float64(8080),
		},
	}
	api := newPendingRestartAPI(t, applied, persisted)

	w := callPendingRestart(t, api)
	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())

	var found *pendingRestartEntry
	for i := range diffs {
		if diffs[i].Key == string(config.GatewayPort) {
			found = &diffs[i]
		}
	}
	require.NotNil(t, found, "gateway.port change must appear in diff; got %+v", diffs)
	assert.EqualValues(t, 8080, found.PersistedValue, "persisted (disk) value is the edited port")
	assert.EqualValues(t, 5000, found.AppliedValue, "applied (boot) value is the original port")
}

// TestHandlePendingRestart_GodModeKeysExcluded verifies that god-mode's two
// config keys are DELIBERATELY absent from the generic pending-restart diff,
// even when persisted differs from the boot-applied snapshot. god_mode /
// god_mode_allowed are the only restart-gated keys that can ALSO apply live
// (any boot where god mode was already available), and this endpoint diffs a
// boot-frozen appliedConfig that is never refreshed for a live toggle —
// listing them would make the generic "restart to apply" banner falsely claim
// the kernel sandbox is inactive right after a live enable. The dedicated
// GodModeControl surface (POST restart_required → GatewayRestartModal, plus
// the availability-driven toggle note) owns this signal accurately instead.
func TestHandlePendingRestart_GodModeKeysExcluded(t *testing.T) {
	applied := map[string]any{
		"version": float64(config.CurrentVersion),
		"gateway": map[string]any{
			"host": "127.0.0.1",
			"port": float64(5000),
		},
		"sandbox": map[string]any{},
	}
	// Persisted (on-disk) has both god-mode keys set after a UI enable —
	// the maximally-different case that WOULD show if the keys were gated.
	persisted := map[string]any{
		"version": float64(config.CurrentVersion),
		"gateway": map[string]any{
			"host": "127.0.0.1",
			"port": float64(5000),
		},
		"sandbox": map[string]any{
			"god_mode":         true,
			"god_mode_allowed": true,
		},
	}
	api := newPendingRestartAPI(t, applied, persisted)

	w := callPendingRestart(t, api)
	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())

	for i := range diffs {
		assert.NotEqual(t, string(config.SandboxGodMode), diffs[i].Key,
			"sandbox.god_mode must NOT appear in the generic pending-restart diff; got %+v", diffs)
		assert.NotEqual(t, string(config.SandboxGodModeAllowed), diffs[i].Key,
			"sandbox.god_mode_allowed must NOT appear in the generic pending-restart diff; got %+v", diffs)
	}
}

// TestHandlePendingRestart_HostChangeStillShows verifies that a real post-boot
// edit to gateway.host (the bind address; "Bind address" in the UI) surfaces in
// the diff — gateway.host is restart-gated like gateway.port because changing it
// re-binds the listener, which can only happen safely on restart.
func TestHandlePendingRestart_HostChangeStillShows(t *testing.T) {
	applied := map[string]any{
		"version": float64(config.CurrentVersion),
		"gateway": map[string]any{
			"host": "127.0.0.1",
			"port": float64(5000),
		},
	}
	// Persisted (on-disk) has gateway.host edited to 0.0.0.0 after boot.
	persisted := map[string]any{
		"version": float64(config.CurrentVersion),
		"gateway": map[string]any{
			"host": "0.0.0.0",
			"port": float64(5000),
		},
	}
	api := newPendingRestartAPI(t, applied, persisted)

	w := callPendingRestart(t, api)
	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())

	var found *pendingRestartEntry
	for i := range diffs {
		if diffs[i].Key == string(config.GatewayHost) {
			found = &diffs[i]
		}
	}
	require.NotNil(t, found, "gateway.host change must appear in diff; got %+v", diffs)
	assert.Equal(t, "0.0.0.0", found.PersistedValue, "persisted (disk) value is the edited host")
	assert.Equal(t, "127.0.0.1", found.AppliedValue, "applied (boot) value is the original host")
}

// TestHandlePendingRestart_HostUnchangedNoPhantomDiff verifies that when the
// bind host is the same on disk and at boot (the normal case — host is always
// explicitly set), no phantom gateway.host diff is produced.
func TestHandlePendingRestart_HostUnchangedNoPhantomDiff(t *testing.T) {
	clean := map[string]any{
		"version": float64(config.CurrentVersion),
		"gateway": map[string]any{
			"host": "127.0.0.1",
			"port": float64(5000),
		},
	}
	api := newPendingRestartAPI(t, clean, clean)

	w := callPendingRestart(t, api)
	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())

	for _, d := range diffs {
		assert.NotEqual(t, string(config.GatewayHost), d.Key,
			"gateway.host must not appear when the bind host is unchanged; got %+v", diffs)
	}
}

// TestHandlePendingRestart_DMScopeNeverPhantom is a focused guard: even when the
// boot config explicitly sets dm_scope to the default value AND the disk omits
// it, no diff appears. (Both sides normalize to "per-channel-peer".)
func TestHandlePendingRestart_DMScopeStableAcrossExplicitDefault(t *testing.T) {
	// Applied (boot) config explicitly carries the defaulted value.
	applied := map[string]any{
		"version": float64(config.CurrentVersion),
		"session": map[string]any{"dm_scope": "per-channel-peer"},
		"gateway": map[string]any{
			"host": "127.0.0.1",
			"port": float64(5000),
		},
	}
	// Persisted (on-disk) drops dm_scope — it should re-default to the same
	// value, producing no diff.
	persisted := map[string]any{
		"version": float64(config.CurrentVersion),
		"gateway": map[string]any{
			"host": "127.0.0.1",
			"port": float64(5000),
		},
	}
	api := newPendingRestartAPI(t, applied, persisted)

	w := callPendingRestart(t, api)
	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())
	assert.Empty(t, diffs, "default-valued dm_scope must not diff; got %+v", diffs)
}

// TestHandlePendingRestart_ListsQueuedChanges verifies that a post-boot change
// to a restart-gated key (sandbox.mode off → enforce on disk) surfaces as a diff.
func TestHandlePendingRestart_ListsQueuedChanges(t *testing.T) {
	applied := map[string]any{
		"version": float64(config.CurrentVersion),
		"sandbox": map[string]any{"mode": "off"},
		"gateway": map[string]any{"host": "127.0.0.1", "port": float64(5000)},
	}
	persisted := map[string]any{
		"version": float64(config.CurrentVersion),
		"sandbox": map[string]any{"mode": "enforce"},
		"gateway": map[string]any{"host": "127.0.0.1", "port": float64(5000)},
	}
	api := newPendingRestartAPI(t, applied, persisted)

	w := callPendingRestart(t, api)
	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())

	var found *pendingRestartEntry
	for i := range diffs {
		if diffs[i].Key == "sandbox.mode" {
			found = &diffs[i]
		}
	}
	require.NotNil(t, found, "sandbox.mode change must appear; got %+v", diffs)
	assert.Equal(t, "enforce", found.PersistedValue)
	assert.Equal(t, "off", found.AppliedValue)
}

// TestHandlePendingRestart_EmptyAfterApply verifies that when the on-disk config
// is unchanged since boot, the response is an empty array (not null).
func TestHandlePendingRestart_EmptyAfterApply(t *testing.T) {
	same := map[string]any{
		"version": float64(config.CurrentVersion),
		"sandbox": map[string]any{"mode": "enforce", "enabled": true},
		"gateway": map[string]any{"host": "127.0.0.1", "port": float64(8080)},
	}
	api := newPendingRestartAPI(t, same, same)

	w := callPendingRestart(t, api)
	require.Equal(t, http.StatusOK, w.Code)
	// Must be "[]" (empty array), not "null".
	body := w.Body.String()
	assert.Contains(t, body, "[", "body must be a JSON array, not null")
	diffs := decodeDiffs(t, w.Body.Bytes())
	assert.Empty(t, diffs, "no diff when on-disk config is unchanged since boot")
}

// TestHandlePendingRestart_SetThenRevertClearsDiff verifies the diff-based
// semantics: if a key was changed to Y and then changed back to X (the applied
// value) before restart, the diff returns []. Modeled by leaving the on-disk
// config identical to boot (net-zero edit).
func TestHandlePendingRestart_SetThenRevertClearsDiff(t *testing.T) {
	// Applied had mode="off"; persisted was changed to "enforce" then reverted to
	// "off" before restart — so persisted == applied now.
	cfg := map[string]any{
		"version": float64(config.CurrentVersion),
		"sandbox": map[string]any{"mode": "off"},
		"gateway": map[string]any{"host": "127.0.0.1", "port": float64(5000)},
	}
	api := newPendingRestartAPI(t, cfg, cfg)

	w := callPendingRestart(t, api)
	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())
	assert.Empty(t, diffs, "reverted change must not appear in diff")
}

// TestHandlePendingRestart_HotReloadKeyNotInDiff verifies that hot-reload keys
// (e.g. sandbox.prompt_injection_level) are excluded from the diff even when
// their values differ between applied and persisted.
func TestHandlePendingRestart_HotReloadKeyNotInDiff(t *testing.T) {
	applied := map[string]any{
		"version": float64(config.CurrentVersion),
		"sandbox": map[string]any{
			"mode":                   "enforce",
			"prompt_injection_level": "medium",
		},
		"gateway": map[string]any{"host": "127.0.0.1", "port": float64(5000)},
	}
	// Persisted flips only the hot-reload key.
	persisted := map[string]any{
		"version": float64(config.CurrentVersion),
		"sandbox": map[string]any{
			"mode":                   "enforce",
			"prompt_injection_level": "high",
		},
		"gateway": map[string]any{"host": "127.0.0.1", "port": float64(5000)},
	}
	api := newPendingRestartAPI(t, applied, persisted)

	w := callPendingRestart(t, api)
	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())
	for _, d := range diffs {
		assert.NotEqual(t, "sandbox.prompt_injection_level", d.Key,
			"hot-reload key must never appear in pending-restart diff")
	}
	assert.Empty(t, diffs, "only the hot-reload key changed; diff must be empty")
}

// TestHandlePendingRestart_MethodNotAllowed verifies that POST and PUT return 405.
func TestHandlePendingRestart_MethodNotAllowed(t *testing.T) {
	api := newPendingRestartAPI(t, nil, map[string]any{"version": float64(config.CurrentVersion)})

	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := withAdminCtx(httptest.NewRequest(method, "/api/v1/config/pending-restart", nil))
			api.HandlePendingRestart(w, r)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}

// TestRefreshConfigHotAppliesLogLevel verifies that a config save which changes
// gateway.log_level is hot-applied immediately by refreshConfigAndRewireServices
// (the config-reload path), rather than waiting for a manual restart. log_level
// is intentionally NOT restart-gated — applying it live is the fix.
//
// newTestRestAPIWithHome wires an agentLoop with credStore == nil, so this
// exercises the early-return (no-store) branch of refreshConfigAndRewireServices.
func TestRefreshConfigHotAppliesLogLevel(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Force a known starting level distinct from the one we will load.
	logger.SetLevel(logger.WARN)
	require.Equal(t, logger.WARN, logger.GetLevel())

	// Write a config.json (at the API's homePath) whose log_level is "debug".
	cfgPath := api.configPath()
	writeConfig(t, cfgPath, map[string]any{
		"gateway": map[string]any{
			"host":      "127.0.0.1",
			"port":      float64(8080),
			"log_level": "debug",
		},
	})

	require.NoError(t, api.refreshConfigAndRewireServices(cfgPath))

	assert.Equal(t, logger.DEBUG, logger.GetLevel(),
		"refreshConfigAndRewireServices must hot-apply gateway.log_level")

	// Restore the default level so the global logger state does not leak to
	// other tests in the package.
	t.Cleanup(func() { logger.SetLevel(logger.INFO) })
}

// TestRestartGatedKeys_KeepsPublicURL_DropsPreview is the ADR-044 (preview-on-
// main-listener) pinning test for US-6/FR-007/FR-014: the four deleted preview
// keys (gateway.preview_port/preview_host/preview_origin/preview_listener_enabled)
// must be gone from RestartGatedKeys, while gateway.public_url MUST remain —
// it drives the boot-frozen CORS/CSP/WS-origin fences (CanonicalGatewayOrigin),
// so unlike preview_enabled it still requires a restart to take effect.
func TestRestartGatedKeys_KeepsPublicURL_DropsPreview(t *testing.T) {
	removedPreviewKeys := []string{
		"gateway.preview_port",
		"gateway.preview_host",
		"gateway.preview_origin",
		"gateway.preview_listener_enabled",
	}

	keys := make(map[string]bool, len(RestartGatedKeys))
	for _, k := range RestartGatedKeys {
		keys[string(k)] = true
	}

	for _, removed := range removedPreviewKeys {
		assert.False(t, keys[removed],
			"RestartGatedKeys must not contain the removed preview key %q; got %+v", removed, RestartGatedKeys)
	}

	assert.True(t, keys[string(config.GatewayPublicURL)],
		"RestartGatedKeys must still contain gateway.public_url (drives boot-frozen origin fences); got %+v",
		RestartGatedKeys)

	// gateway.preview_enabled (the new live-read toggle) must also be absent —
	// it is explicitly NOT restart-gated (ADR-044, FR-007).
	assert.False(t, keys[string(config.GatewayPreviewEnabled)],
		"RestartGatedKeys must not contain gateway.preview_enabled — it is read live, not restart-gated; got %+v",
		RestartGatedKeys)
}

// TestGetAtPath_DottedPath verifies that getAtPath returns the correct value
// for a two-segment dotted path.
func TestGetAtPath_DottedPath(t *testing.T) {
	m := map[string]any{
		"gateway": map[string]any{
			"port": float64(5000),
		},
	}
	val := getAtPath(m, "gateway.port")
	assert.Equal(t, float64(5000), val)
}

// TestGetAtPath_MissingSegment verifies that getAtPath returns nil without
// panicking when a path segment is absent.
func TestGetAtPath_MissingSegment(t *testing.T) {
	m := map[string]any{
		"gateway": map[string]any{},
	}
	val := getAtPath(m, "gateway.port")
	assert.Nil(t, val, "missing leaf must return nil")

	val2 := getAtPath(m, "sandbox.mode")
	assert.Nil(t, val2, "missing root segment must return nil")
}

// TestRestartGatedKeys_NoChannelKeys_GetAtPathSplitsBlindly is a trip-wire, not
// a behavioural test. getAtPath walks a dotted config path with a plain
// strings.SplitN(dotted, ".", 2), which is correct for every key currently in
// RestartGatedKeys and wrong for any "channels.*" key: a channel instance id is
// itself built as <type>.<slug> (rest.go's createChannelInstance), so
// "channels.telegram.one.enabled" would be walked as channel "telegram", field
// "one" — a lookup that silently misses and reports no drift, meaning the
// operator is never told a restart is needed.
//
// pkg/sysagent/tools/config.go solved this for the settings tool by coalescing
// the instance key against cfg.Channels before splitting. getAtPath was left
// alone deliberately: it is unreachable for channel keys today, and an
// unreachable fix is untestable dead weight. This test fails the moment that
// stops being true, so whoever adds the first channels.* gated key is told to
// teach getAtPath the same grammar rather than discovering the miss in the
// field.
func TestRestartGatedKeys_NoChannelKeys_GetAtPathSplitsBlindly(t *testing.T) {
	for _, k := range RestartGatedKeys {
		if strings.HasPrefix(string(k), "channels.") {
			t.Fatalf("RestartGatedKeys contains %q: getAtPath splits dotted paths blindly and "+
				"cannot address a <type>.<slug> channel instance id. Teach getAtPath the instance-key "+
				"grammar (see pkg/sysagent/tools/config.go::configKeySegments) before gating a channels.* key.", k)
		}
	}
}
