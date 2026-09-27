// Package gateway — ADR-096 save rules the roles PUT must enforce.
//
// Oracles, from docs/internal/specs/web-search-provider-model-spec.md:
//
//   - Save-time table, "Same id as default and as fallback | Rejected".
//     One request that sets both is already rejected. Two requests must not
//     reach the same state: active:true on the provider that is already the
//     stored fallback is HTTP 400 and does not write.
//   - FR-033: usability is judged after the reload, never before it. A PUT
//     that carries api_key and fallback:true therefore stores the key, writes
//     the role, reloads (InjectFromConfig), and only then decides. This
//     matches the default-role rule in the same table: "Rejected only if the
//     key still does not resolve afterwards".
//   - "Fallback points at a provider that is not enabled | Rejected" stays a
//     before-write rejection: enabled is on disk and does not depend on the
//     key becoming live. TestIntegrationPut_FallbackTargetNotUsable_Rejected400
//     pins that row.
package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegrationPut_ActiveOnStoredFallback_Rejected400 pins the save-time
// rule across two requests. The first names Brave as the fallback. The
// second names Brave as the default. That second request is the same id in
// both roles and must be rejected without changing either role.
func TestIntegrationPut_ActiveOnStoredFallback_Rejected400(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	t.Setenv("BRAVE_API_KEY", "k-brave")
	web := rolesFixtureWeb()
	web["brave"] = map[string]any{"enabled": true, "api_key_ref": "BRAVE_API_KEY"}
	writeRolesWebConfig(t, api, web)
	storeWebSearchKeys(t, api, "TAVILY_API_KEY", "BRAVE_API_KEY")
	wireRolesReload(t, cfg, api, nil, nil)

	first := putRoles(t, api, user, "brave", `{"kind":"search","fallback":true}`)
	require.Equal(t, http.StatusOK, first.Code, "body=%s", first.Body.String())
	afterFirst := readRolesWebConfig(t, api)
	require.Equal(t, "tavily", afterFirst["default_provider"])
	require.Equal(t, "brave", afterFirst["fallback_provider"])

	second := putRoles(t, api, user, "brave", `{"kind":"search","active":true}`)
	assert.Equal(t, http.StatusBadRequest, second.Code,
		"save rule: the same id cannot be default and fallback, body=%s", second.Body.String())

	afterSecond := readRolesWebConfig(t, api)
	assert.Equal(t, "tavily", afterSecond["default_provider"],
		"the rejected save must not move the default onto the current fallback")
	assert.Equal(t, "brave", afterSecond["fallback_provider"],
		"the rejected save must not clear or rewrite the stored fallback")
}

// TestIntegrationPut_FallbackWithKey_JudgedAfterReload pins FR-033 for the
// fallback role. Brave is enabled, its key is not in the environment, and
// the PUT carries the key. The reload is what publishes the key. Judging
// usability before that reload rejects a save the spec says to accept.
func TestIntegrationPut_FallbackWithKey_JudgedAfterReload(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	t.Setenv("BRAVE_API_KEY", "")
	web := rolesFixtureWeb()
	web["brave"] = map[string]any{"enabled": true}
	writeRolesWebConfig(t, api, web)
	storeWebSearchKeys(t, api, "TAVILY_API_KEY")
	wireRolesReload(t, cfg, api, func() {
		if os.Getenv("BRAVE_API_KEY") != "" {
			t.Error("reload must run before the handler judges fallback usability")
		}
		// The reload stands in for InjectFromConfig. Cleanup is below;
		// t.Setenv only restores the value captured at the call above.
		os.Setenv("BRAVE_API_KEY", "k-brave")
	}, nil)
	t.Cleanup(func() { os.Unsetenv("BRAVE_API_KEY") })

	w := putRoles(t, api, user, "brave", `{"kind":"search","api_key":"k-brave-raw","fallback":true}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	got := readRolesWebConfig(t, api)
	assert.Equal(t, "brave", got["fallback_provider"], "the fallback role is written")
	assert.Equal(t, "tavily", got["default_provider"], "a fallback save does not move the default")
	assert.Equal(t, "BRAVE_API_KEY", roleSection(t, got, "brave")["api_key_ref"])

	resp := gen.IntegrationProvidersResponse{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.FallbackSearch)
	assert.Equal(t, "brave", *resp.FallbackSearch)
	brave := searchRow(t, resp, "brave")
	require.NotNil(t, brave.Usable)
	assert.True(t, *brave.Usable, "FR-033: the key stored in this request reads ready from post-reload state")
}

// TestIntegrationPut_FallbackKeyUnresolvedAfterReload_400KeepsWrite pins the
// other half of the same rule: when the key still does not resolve after the
// reload, the save is HTTP 400 and the persisted write stays. Rejecting
// before the write is the defect — the key in the body never gets a chance
// to become live.
func TestIntegrationPut_FallbackKeyUnresolvedAfterReload_400KeepsWrite(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	t.Setenv("BRAVE_API_KEY", "")
	web := rolesFixtureWeb()
	web["brave"] = map[string]any{"enabled": true}
	writeRolesWebConfig(t, api, web)
	storeWebSearchKeys(t, api, "TAVILY_API_KEY")
	// Reload swaps config and does not publish the key.
	wireRolesReload(t, cfg, api, nil, nil)

	w := putRoles(t, api, user, "brave", `{"kind":"search","api_key":"k-brave-raw","fallback":true}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "API key",
		"the rejection names the step that failed, as the default-role rule does")

	got := readRolesWebConfig(t, api)
	assert.Equal(t, "brave", got["fallback_provider"],
		"FR-033: the rejection is after the write; the write stays")
	assert.Equal(t, "BRAVE_API_KEY", roleSection(t, got, "brave")["api_key_ref"],
		"the key reference is stored before usability is judged")
}
