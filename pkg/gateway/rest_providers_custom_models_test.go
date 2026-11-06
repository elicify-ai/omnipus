package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRestProviders_CustomModels_FullIdentityUpdateRoundTrip is #1639's handler
// positive control, not a demand that a models-only custom PUT bypass admission.
// Oracles: ADR-067 FR-035 / US-8.AC4 and ProviderUpdateRequest's api_base,
// protocol, models and existing-key retention contract. The frontend regression
// asserts that the editor actually supplies this full request.
func TestRestProviders_CustomModels_FullIdentityUpdateRoundTrip(t *testing.T) {
	var probes atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			probes.Add(1)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			// A custom gateway without a model-list endpoint: the operator's
			// stored slugs, not an upstream catalogue, must survive the GET.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"model listing not supported"}`))
		default:
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKeyValidation)
	api := newTestRestAPIWithHome(t)
	api.providerCatalog = freshCatalog(t)
	// Start empty and install the existing synchronous reload seam. The row
	// itself is added through the real PUT, not seeded as a catalog exception.
	seedProviderConfig(t, api)
	store := credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(store), "unlock the isolated test credential store")
	api.credStore = store

	const id = "localtest"
	key := "test-only-custom-provider-key"
	base := upstream.URL + "/v1"
	protocol := gen.ProviderUpdateRequestProtocolOpenaiCompatible
	probeModel := "test/model-a"
	createBody, err := json.Marshal(gen.ProviderUpdateRequest{
		ApiKey: &key, ApiBase: &base, Protocol: &protocol, Model: &probeModel,
	})
	require.NoError(t, err)
	created := doPutProvider(t, api, id, string(createBody))
	require.Equal(t, http.StatusOK, created.Code, "normal custom creation: %s", created.Body.String())
	require.Equal(t, int32(1), probes.Load(), "normal creation must really validate the key")
	initial := persistedProviderRow(t, api, id)
	ref, ok := initial["api_key_ref"].(string)
	require.True(t, ok, "normal creation must persist an encrypted-key reference")
	require.NotEmpty(t, ref, "normal creation must not keep a plaintext-only key")

	// ADR-067's request contract requires the pair again. Omit api_key to keep
	// the real stored key; provide models to replace the empty manual catalogue.
	slugs := []string{"test/model-a", "test/model-b"}
	updateBody, err := json.Marshal(gen.ProviderUpdateRequest{
		ApiBase: &base, Protocol: &protocol, Models: &slugs,
	})
	require.NoError(t, err)
	updated := doPutProvider(t, api, id, string(updateBody))
	require.Equal(t, http.StatusOK, updated.Code, "existing custom model save: %s", updated.Body.String())

	var response gen.Provider
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &response))
	assert.Equal(t, id, response.Id)
	assert.Equal(t, slugs, response.Models, "PUT must return the saved manual catalogue")
	require.NotNil(t, response.Custom)
	assert.True(t, *response.Custom, "the arbitrary id remains a custom row")
	require.NotNil(t, response.Protocol)
	assert.Equal(t, gen.ProviderProtocolOpenaiCompatible, *response.Protocol)

	persisted := persistedProviderRow(t, api, id)
	assert.Equal(t, []any{"test/model-a", "test/model-b"}, persisted["models"], "slugs must reach config.json")
	assert.Equal(t, base, persisted["api_base"], "model editing must retain the base")
	assert.Equal(t, "openai-compatible", persisted["protocol"])
	assert.Equal(t, true, persisted["custom"])
	assert.Equal(t, ref, persisted["api_key_ref"], "a model edit must not replace the encrypted-key reference")
	retainedKey, err := store.Get(ref)
	require.NoError(t, err, "the existing encrypted key must remain resolvable")
	assert.Equal(t, key, retainedKey)

	listed := getProviders(t, api)
	require.Len(t, listed, 1, "the normal creation and update must not create duplicate provider rows")
	assert.Equal(t, id, listed[0].Id)
	assert.Equal(t, slugs, listed[0].Models, "GET must list the stored slugs for the endpoint without /models")
	require.NotNil(t, listed[0].Custom)
	assert.True(t, *listed[0].Custom)
	require.NotNil(t, listed[0].Protocol)
	assert.Equal(t, gen.ProviderProtocolOpenaiCompatible, *listed[0].Protocol)
	assert.Equal(t, int32(1), probes.Load(), "a model edit without a key must not run another billable key probe")
}
