package gateway

import (
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// A local provider row (Ollama, vLLM, a custom row on a loopback or private
// host) is loopback by definition. In core with sandbox.ssrf on, the new
// provider address checks must leave it alone: only the guard-active (hosted)
// path refuses a local target.

// TestProviderPlantCore_SSRFOn_LocalRowStillListsModels: GET /providers still
// fetches a local row's live model list when sandbox.ssrf is on in core.
func TestProviderPlantCore_SSRFOn_LocalRowStillListsModels(t *testing.T) {
	env := newPlantEnv(t, config.EditionCore, false)
	env.api.ssrfChecker = security.NewSSRFChecker(nil)
	canary := startPlantCanary(t)
	seedProviderRow(t, env, map[string]any{
		"provider": "localbox", "model": "m1", "custom": true,
		"protocol": "openai-compatible", "api_base": canary.URL + "/v1",
	})

	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodGet, "/api/v1/providers", "", false), "198.51.100.180")

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Positive(t, canary.hits.Load(), "the local row's model list must still be fetched")
}

// TestProviderPlantCore_SSRFOn_KeylessSaveOfLoopbackBaseForLocalRow: a keyless
// PUT that points an existing local row at a loopback base keeps working in
// core with sandbox.ssrf on.
func TestProviderPlantCore_SSRFOn_KeylessSaveOfLoopbackBaseForLocalRow(t *testing.T) {
	env := newPlantEnv(t, config.EditionCore, false)
	env.api.ssrfChecker = security.NewSSRFChecker(nil)
	canary := startPlantCanary(t)
	seedProviderRow(t, env, map[string]any{
		"provider": "localbox", "model": "m1", "custom": true,
		"protocol": "openai-compatible", "api_base": "http://127.0.0.1:11434/v1",
	})
	body := fmt.Sprintf(`{"api_base":%q,"protocol":"openai-compatible"}`, canary.URL+"/v1")

	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPut, "/api/v1/providers/localbox", body, false), "198.51.100.181")

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	rows := diskProviderRows(t, env)
	require.Len(t, rows, 1)
	assert.Equal(t, canary.URL+"/v1", rows[0]["api_base"])
}

// TestProviderPlantCore_SSRFOn_KeylessSaveOfCloudRowToPrivateAddressRefused:
// the keyless check still applies to a row that is not local. A public host
// name that resolves to a private address is not local (locality looks at the
// literal host), so the save is refused and the stored base stays.
func TestProviderPlantCore_SSRFOn_KeylessSaveOfCloudRowToPrivateAddressRefused(t *testing.T) {
	env := newPlantEnv(t, config.EditionCore, false)
	checker := security.NewSSRFChecker(nil)
	checker.SetResolver(&recordingResolver{ip: net.ParseIP("10.0.0.7")})
	env.api.ssrfChecker = checker
	seedProviderRow(t, env, map[string]any{
		"provider": "mybox", "model": "m1", "custom": true,
		"protocol": "openai-compatible", "api_base": "https://mybox.example.com/v1",
	})
	body := `{"api_base":"https://planter.example.net/v1","protocol":"openai-compatible"}`

	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPut, "/api/v1/providers/mybox", body, false), "198.51.100.182")

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body=%s", w.Body.String())
	rows := diskProviderRows(t, env)
	require.Len(t, rows, 1)
	assert.Equal(t, "https://mybox.example.com/v1", rows[0]["api_base"], "the stored base must be unchanged")
}

// TestProviderPlantCore_SSRFOn_CatalogRowCannotClaimLocalByProtocol: locality
// of a catalog row comes from the catalog, never from the caller's protocol.
// A cloud row that claims protocol=ollama must not skip the address check,
// and a protocol the catalog row does not offer is refused before any write.
func TestProviderPlantCore_SSRFOn_CatalogRowCannotClaimLocalByProtocol(t *testing.T) {
	env := newPlantEnv(t, config.EditionCore, false)
	checker := security.NewSSRFChecker(nil)
	checker.SetResolver(&recordingResolver{ip: net.ParseIP("10.0.0.7")})
	env.api.ssrfChecker = checker
	seedProviderRow(t, env, map[string]any{
		"provider": "plantco", "model": "plant-model",
		"api_base": "https://api.plantco.example/v1",
	})
	body := `{"api_base":"https://planter.example.net/v1","protocol":"ollama"}`

	w := serveViaRealMux(t, env.api, env.cfg,
		plantRequest(http.MethodPut, "/api/v1/providers/plantco", body, false), "198.51.100.183")

	require.GreaterOrEqual(t, w.Code, 400, "body=%s", w.Body.String())
	require.Less(t, w.Code, 500, "body=%s", w.Body.String())
	rows := diskProviderRows(t, env)
	require.Len(t, rows, 1)
	assert.Equal(t, "https://api.plantco.example/v1", rows[0]["api_base"], "nothing may be persisted")
}
