// Package gateway — Spec-1 BE-fixes tests for issues #351 and #352.
//
// #351 (FR-104): HandleProviders GET reports Connected ONLY when the provider's
// API key resolves to a non-empty credential.
//
// #352 (FR-105/FR-106): gateway.users is removed from RestartGatedKeys (the
// fresh-install restart banner) and hot_reload defaults on.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Issue #352: No spurious restart banner on fresh install ---

// TestRestartGatedKeys_GatewayUsersNotGated verifies that gateway.users is NOT
// in RestartGatedKeys, so adding a user via onboarding does not trigger the
// restart banner.
//
// BDD (US-3 / AC1):
//
//	Given a just-completed onboarding config (wrote gateway.users + a provider)
//	When HandlePendingRestart is called
//	Then no pending-restart diff appears for gateway.users changes.
//
// Traces to: FR-105, US-3/AC1, SC-104, test dataset row: gateway.users change → no banner.
func TestRestartGatedKeys_GatewayUsersNotGated(t *testing.T) {
	// Verify the compile-time invariant: GatewayUsers must not be in the list.
	for _, key := range RestartGatedKeys {
		if key == config.GatewayUsers {
			t.Errorf("config.GatewayUsers must NOT be in RestartGatedKeys (FR-105/US-3): "+
				"gateway.users is hot (auth reads GetConfig() live); adding it caused the "+
				"fresh-install restart banner.\nFound at position in RestartGatedKeys: %v",
				RestartGatedKeys)
		}
	}
}

// TestPendingRestart_FreshOnboardingNoBanner verifies that a config produced by
// onboarding (which writes gateway.users + a provider entry) results in zero
// pending-restart entries when applied and persisted configs start equal.
//
// BDD (US-3 / AC1):
//
//	Given the applied config equals the persisted config for all restart-gated keys
//	AND onboarding writes only gateway.users and providers (neither in RestartGatedKeys)
//	When HandlePendingRestart is called
//	Then the response is an empty array (no banner).
//
// Traces to: FR-105, US-3/AC1, SC-104.
func TestPendingRestart_FreshOnboardingNoBanner(t *testing.T) {
	// Simulate the state after onboarding: config has a provider and gateway.users.
	// Neither is in RestartGatedKeys, so the diff must be empty.
	onboardedCfg := map[string]any{
		"sandbox": map[string]any{"mode": "off"},
		"gateway": map[string]any{
			"port": float64(5000),
			"users": []any{
				map[string]any{
					"username":      "admin",
					"password_hash": "$2a$10$placeholder",
					"token_hash":    "$2a$10$placeholder",
					"role":          "admin",
				},
			},
		},
		"providers": []any{
			map[string]any{
				"model_name":  "claude-sonnet-4-6",
				"provider":    "anthropic",
				"model":       "claude-sonnet-4-6",
				"api_key_ref": "ANTHROPIC_API_KEY",
			},
		},
	}

	// applied = persisted (both are the onboarded config) — no diff expected.
	api := newPendingRestartAPI(t, onboardedCfg, onboardedCfg)

	w := httptest.NewRecorder()
	r := withAdminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/config/pending-restart", nil))
	api.HandlePendingRestart(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())
	assert.Empty(t, diffs,
		"fresh onboarded config must produce zero pending-restart entries (FR-105/US-3/SC-104)")
}

// TestPendingRestart_GatewayPortStillGated verifies that a genuinely restart-required
// key (GatewayPort) still triggers the banner after removing GatewayUsers.
//
// BDD (US-3 / AC3):
//
//	Given hot-reload is on
//	When the gateway port (a key actually in RestartGatedKeys) is changed
//	Then the restart banner shows for that key only.
//
// Traces to: FR-106, US-3/AC3.
func TestPendingRestart_GatewayPortStillGated(t *testing.T) {
	// ADR-044 removed gateway.preview_port entirely (no more auto-derivation
	// from gateway.port to pin against) — the diff is naturally isolated to
	// gateway.port on its own now.
	applied := map[string]any{
		"gateway": map[string]any{"port": float64(5000)},
	}
	persisted := map[string]any{
		"gateway": map[string]any{"port": float64(8080)}, // port changed
	}
	api := newPendingRestartAPI(t, applied, persisted)

	w := httptest.NewRecorder()
	r := withAdminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/config/pending-restart", nil))
	api.HandlePendingRestart(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())
	require.Len(t, diffs, 1, "exactly one gated key changed (gateway.port)")
	assert.Equal(t, "gateway.port", diffs[0].Key,
		"gateway.port must still appear in the diff (genuinely restart-gated)")
}

// TestPendingRestart_SandboxModeStillGated verifies that sandbox.mode (a
// genuinely restart-required key) still triggers the banner.
//
// Traces to: FR-106, US-3/AC3.
func TestPendingRestart_SandboxModeStillGated(t *testing.T) {
	// gateway.port is pinned to the same value on both sides: it is a gated key
	// that lacks omitempty, so the applied config (a full struct) always
	// materializes it. Real config.json is a full serialization too, but the test's
	// persisted map is sparse — without this pin, port 0-vs-absent reads as a second
	// spurious diff. Keeping it equal isolates the assertion to sandbox.mode.
	applied := map[string]any{
		"sandbox": map[string]any{"mode": "off"},
		"gateway": map[string]any{"port": float64(5000)},
	}
	persisted := map[string]any{
		"sandbox": map[string]any{"mode": "enforce"}, // changed
		"gateway": map[string]any{"port": float64(5000)},
	}
	api := newPendingRestartAPI(t, applied, persisted)

	w := httptest.NewRecorder()
	r := withAdminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/config/pending-restart", nil))
	api.HandlePendingRestart(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())
	require.Len(t, diffs, 1, "exactly one gated key changed (sandbox.mode)")
	assert.Equal(t, "sandbox.mode", diffs[0].Key,
		"sandbox.mode must still appear in the diff (genuinely restart-gated)")
}

// TestHotReloadDefault_IsTrue verifies that the HotReload field in the default
// GatewayConfig is true, so fresh installs have hot-reload always on.
//
// BDD (US-3 / AC2):
//
//	Given hot-reload defaults on
//	When a hot-reloadable key changes
//	Then it applies without a restart banner.
//
// Traces to: FR-106, US-3/AC2.
func TestHotReloadDefault_IsTrue(t *testing.T) {
	defaults := config.DefaultConfig()
	assert.True(t, defaults.Gateway.HotReload,
		"gateway.hot_reload must default to true (FR-106) so fresh installs always have hot-reload on")
}
