package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRestProviders_CustomModels_FullIdentityUpdateRoundTrip is #1639's handler
// positive control: explicitly resending both identity fields is still allowed
// under accepted architect ruling B (2026-11-06). It does not cover the models-only
// bug; R1 below demands that omitted fields count from an eligible stored custom
// row. Oracles: ruling B's existing-test table and ADR-067's identity/model/key
// retention contracts, not a requirement that the client resend the pair.
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

	// Explicitly supplying the pair remains valid under ruling B. Omit api_key
	// to keep the stored key; R1 separately covers an omitted identity pair.
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

// The remaining oracles are accepted architect ruling B, behaviour rules 1–5
// (2026-11-06, #1639). Production admission/persistence is never mocked or edited.
const customModelsProviderID = "localtest"

type customModelsFixture struct {
	api    *restAPI
	base   string
	key    string
	ref    string
	probes *atomic.Int32
}

func customModelsRequestJSON(t *testing.T, request gen.ProviderUpdateRequest) string {
	t.Helper()
	raw, err := json.Marshal(request)
	require.NoError(t, err, "marshal generated provider request")
	return string(raw)
}

// newStoredCustomModelsFixture creates the row through the real PUT, so R1/R3/R5
// start from an actually configured endpoint with a real encrypted key.
func newStoredCustomModelsFixture(t *testing.T) customModelsFixture {
	t.Helper()
	probes := new(atomic.Int32)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			probes.Add(1)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"model listing not supported"}`))
		default:
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKeyValidation)
	api := newTestRestAPIWithHome(t)
	api.providerCatalog = freshCatalog(t)
	_, catalogID := api.providerCatalog.Provider(customModelsProviderID)
	require.False(t, catalogID, "the fixture must exercise a non-catalog id")
	seedProviderConfig(t, api)
	api.credStore = credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(api.credStore))
	key, base := "test-only-custom-provider-key", upstream.URL+"/v1"
	protocol := gen.ProviderUpdateRequestProtocolOpenaiCompatible
	model, models := "test/original", []string{"test/original"}
	created := doPutProvider(t, api, customModelsProviderID, customModelsRequestJSON(t, gen.ProviderUpdateRequest{
		ApiKey: &key, ApiBase: &base, Protocol: &protocol, Model: &model, Models: &models,
	}))
	require.Equal(t, http.StatusOK, created.Code, "normal custom creation: %s", created.Body.String())
	require.Equal(t, int32(1), probes.Load(), "fixture creation must validate the real key once")
	initial := persistedProviderRow(t, api, customModelsProviderID)
	ref, ok := initial["api_key_ref"].(string)
	require.True(t, ok, "creation must persist the encrypted-key reference")
	require.NotEmpty(t, ref)
	return customModelsFixture{api: api, base: base, key: key, ref: ref, probes: probes}
}

type customModelsFileState struct {
	body   []byte
	info   os.FileInfo
	exists bool
}

func snapshotCustomModelsFile(t *testing.T, path string) customModelsFileState {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return customModelsFileState{}
	}
	require.NoError(t, err, "read test-owned file %s", path)
	info, err := os.Stat(path)
	require.NoError(t, err)
	return customModelsFileState{body: body, info: info, exists: true}
}

func assertCustomModelsFileUnchanged(t *testing.T, path string, before customModelsFileState) {
	t.Helper()
	after := snapshotCustomModelsFile(t, path)
	assert.Equal(t, before.exists, after.exists, "a refused/no-key update must not create/delete %s", path)
	assert.Equal(t, before.body, after.body, "a refused/no-key update must not change bytes in %s", path)
	if before.exists && after.exists {
		assert.True(t, os.SameFile(before.info, after.info), "must not atomically rewrite %s", path)
		assert.Equal(t, before.info.ModTime(), after.info.ModTime(), "must not write %s in place", path)
	}
}

func assertCustomModelsStored(t *testing.T, f customModelsFixture, base, protocol string, models []string) {
	t.Helper()
	stored := persistedProviderRow(t, f.api, customModelsProviderID)
	assert.Equal(t, base, stored["api_base"], "stored base must be the expected original/explicit value")
	assert.Equal(t, protocol, stored["protocol"], "stored protocol must be the expected original/explicit value")
	assert.Equal(t, true, stored["custom"], "model edits must not declassify the custom row")
	assert.Equal(t, f.ref, stored["api_key_ref"], "editing models/identity must not rotate the key")
	wantModels := make([]any, len(models))
	for i, model := range models {
		wantModels[i] = model
	}
	assert.Equal(t, wantModels, stored["models"], "the exact manual catalogue must be persisted")
	key, err := f.api.credStore.Get(f.ref)
	require.NoError(t, err, "the existing encrypted key must still resolve")
	assert.Equal(t, f.key, key, "the existing secret must remain unchanged")
	listed := getProviders(t, f.api)
	require.Len(t, listed, 1, "updates must not create duplicate rows")
	assert.Equal(t, customModelsProviderID, listed[0].Id)
	assert.Equal(t, models, listed[0].Models, "GET must expose the actual persisted model list")
	require.NotNil(t, listed[0].Custom)
	assert.True(t, *listed[0].Custom)
	require.NotNil(t, listed[0].Protocol)
	assert.Equal(t, gen.ProviderProtocol(protocol), *listed[0].Protocol)
	assert.Equal(t, int32(1), f.probes.Load(), "no second key probe without a new key")
}

func assertCustomModelsRefusal(t *testing.T, w *httptest.ResponseRecorder, field string) {
	t.Helper()
	assert.Equal(t, http.StatusBadRequest, w.Code, "refused update body=%s", w.Body.String())
	var response gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, gen.ErrorResponse{
		Error: fmt.Sprintf("unknown provider %q", customModelsProviderID), Field: &field,
	}, response, "normal admission vocabulary and attributable field must be preserved")
}

func TestRestProviders_CustomModels_R1ModelsOnlyRetainsIdentity(t *testing.T) {
	f := newStoredCustomModelsFixture(t)
	credentialsBefore := snapshotCustomModelsFile(t, f.api.credentialsStorePath())
	models := []string{"test/model-a", "test/model-b"}
	w := doPutProvider(t, f.api, customModelsProviderID, customModelsRequestJSON(t, gen.ProviderUpdateRequest{Models: &models}))
	require.Equal(t, http.StatusOK, w.Code, "R1: models-only PUT on an existing custom row must succeed: %s", w.Body.String())
	var response gen.Provider
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, customModelsProviderID, response.Id)
	assert.Equal(t, models, response.Models, "R1: PUT returns exactly the saved models")
	require.NotNil(t, response.Custom)
	assert.True(t, *response.Custom)
	require.NotNil(t, response.Protocol)
	assert.Equal(t, gen.ProviderProtocolOpenaiCompatible, *response.Protocol)
	assertCustomModelsStored(t, f, f.base, "openai-compatible", models)
	assertCustomModelsFileUnchanged(t, f.api.credentialsStorePath(), credentialsBefore)
}

func TestRestProviders_CustomModels_R2FirstSaveRequiresIdentity(t *testing.T) {
	key, protocol := "test-only-first-key", gen.ProviderUpdateRequestProtocolOpenaiCompatible
	models := []string{"test/model-a"}
	for _, name := range []string{"models_only", "key_only", "models_and_protocol", "key_and_protocol", "models_and_base", "key_and_base"} {
		t.Run(name, func(t *testing.T) {
			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamCalls.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer upstream.Close()
			base := upstream.URL + "/v1"
			request := gen.ProviderUpdateRequest{}
			if strings.HasPrefix(name, "models") {
				request.Models = &models
			} else {
				request.ApiKey = &key
			}
			field := "id"
			if strings.HasSuffix(name, "_protocol") {
				request.Protocol = &protocol
			}
			if strings.HasSuffix(name, "_base") {
				request.ApiBase = &base
				field = "protocol"
			}
			t.Setenv("OMNIPUS_MASTER_KEY", testMasterKeyValidation)
			api := newTestRestAPIWithHome(t)
			api.providerCatalog = freshCatalog(t)
			seedProviderConfig(t, api)
			api.credStore = credentials.NewStore(api.credentialsStorePath())
			require.NoError(t, credentials.Unlock(api.credStore))
			configBefore := snapshotCustomModelsFile(t, api.configPath())
			credentialsBefore := snapshotCustomModelsFile(t, api.credentialsStorePath())
			w := doPutProvider(t, api, customModelsProviderID, customModelsRequestJSON(t, request))
			assertCustomModelsRefusal(t, w, field)
			assertCustomModelsFileUnchanged(t, api.configPath(), configBefore)
			assertCustomModelsFileUnchanged(t, api.credentialsStorePath(), credentialsBefore)
			assert.Empty(t, api.agentLoop.GetConfig().Providers, "R2: no row may be created by a refused first save")
			assert.Equal(t, int32(0), upstreamCalls.Load(), "R2: refuse before any key probe")
		})
	}
}

func TestRestProviders_CustomModels_R3PartialIdentity(t *testing.T) {
	t.Run("invalid_protocol_only_is_a_protocol_error", func(t *testing.T) {
		f := newStoredCustomModelsFixture(t)
		configBefore := snapshotCustomModelsFile(t, f.api.configPath())
		credentialsBefore := snapshotCustomModelsFile(t, f.api.credentialsStorePath())
		invalid := gen.ProviderUpdateRequestProtocol("google")
		w := doPutProvider(t, f.api, customModelsProviderID, customModelsRequestJSON(t, gen.ProviderUpdateRequest{Protocol: &invalid}))
		assertCustomModelsRefusal(t, w, "protocol")
		assertCustomModelsFileUnchanged(t, f.api.configPath(), configBefore)
		assertCustomModelsFileUnchanged(t, f.api.credentialsStorePath(), credentialsBefore)
		assert.Equal(t, int32(1), f.probes.Load(), "R3: rejected protocol must not probe the key")
	})
	t.Run("valid_protocol_only_keeps_stored_base", func(t *testing.T) {
		// Ruling B's partial-identity rule combines the supplied valid protocol
		// with the stored base; CHECK F3 / Round 4 explicitly require this case.
		f := newStoredCustomModelsFixture(t)
		credentialsBefore := snapshotCustomModelsFile(t, f.api.credentialsStorePath())
		protocol := gen.ProviderUpdateRequestProtocolAnthropic
		w := doPutProvider(t, f.api, customModelsProviderID, customModelsRequestJSON(t, gen.ProviderUpdateRequest{Protocol: &protocol}))
		require.Equal(t, http.StatusOK, w.Code, "R3: valid protocol-only update keeps the stored base: %s", w.Body.String())
		var response gen.Provider
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.NotNil(t, response.Protocol)
		assert.Equal(t, gen.ProviderProtocolAnthropic, *response.Protocol, "R3: PUT must expose the explicitly changed protocol")
		assertCustomModelsStored(t, f, f.base, "anthropic", []string{"test/original"})
		assertCustomModelsFileUnchanged(t, f.api.credentialsStorePath(), credentialsBefore)
	})
	t.Run("base_only_keeps_stored_protocol", func(t *testing.T) {
		f := newStoredCustomModelsFixture(t)
		credentialsBefore := snapshotCustomModelsFile(t, f.api.credentialsStorePath())
		// Same test-owned host, a different path: no real network even if probed.
		newBase := f.base + "/changed"
		w := doPutProvider(t, f.api, customModelsProviderID, customModelsRequestJSON(t, gen.ProviderUpdateRequest{ApiBase: &newBase}))
		require.Equal(t, http.StatusOK, w.Code, "R3: base-only update keeps the stored protocol: %s", w.Body.String())
		var response gen.Provider
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.NotNil(t, response.Protocol)
		assert.Equal(t, gen.ProviderProtocolOpenaiCompatible, *response.Protocol)
		assertCustomModelsStored(t, f, newBase, "openai-compatible", []string{"test/original"})
		assertCustomModelsFileUnchanged(t, f.api.credentialsStorePath(), credentialsBefore)
	})
	t.Run("explicit_empty_identity_counts_as_absent", func(t *testing.T) {
		f := newStoredCustomModelsFixture(t)
		credentialsBefore := snapshotCustomModelsFile(t, f.api.credentialsStorePath())
		emptyBase, emptyProtocol := "", gen.ProviderUpdateRequestProtocol("")
		models := []string{"test/model-a"}
		w := doPutProvider(t, f.api, customModelsProviderID, customModelsRequestJSON(t, gen.ProviderUpdateRequest{
			ApiBase: &emptyBase, Protocol: &emptyProtocol, Models: &models,
		}))
		require.Equal(t, http.StatusOK, w.Code, "R3: empty identity fields count as omitted: %s", w.Body.String())
		assertCustomModelsStored(t, f, f.base, "openai-compatible", models)
		assertCustomModelsFileUnchanged(t, f.api.credentialsStorePath(), credentialsBefore)
	})
}

func TestRestProviders_CustomModels_R4IneligibleStoredRowsHaveNoFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		custom   bool
		base     string
		protocol string
	}{
		{name: "not_custom", custom: false, base: "https://stored.example/v1", protocol: "openai-compatible"},
		{name: "custom_missing_base", custom: true, protocol: "openai-compatible"},
		{name: "custom_blank_base", custom: true, base: " \t", protocol: "openai-compatible"},
		{name: "custom_missing_protocol", custom: true, base: "https://stored.example/v1"},
		{name: "custom_invalid_protocol", custom: true, base: "https://stored.example/v1", protocol: "google"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			api.providerCatalog = freshCatalog(t)
			row := map[string]any{"provider": customModelsProviderID, "model": "test/original", "custom": tc.custom}
			if tc.base != "" {
				row["api_base"] = tc.base
			}
			if tc.protocol != "" {
				row["protocol"] = tc.protocol
			}
			seedProviderConfig(t, api, row)
			configBefore := snapshotCustomModelsFile(t, api.configPath())
			credentialsBefore := snapshotCustomModelsFile(t, api.credentialsStorePath())
			models := []string{"test/model-a"}
			w := doPutProvider(t, api, customModelsProviderID, customModelsRequestJSON(t, gen.ProviderUpdateRequest{Models: &models}))
			assertCustomModelsRefusal(t, w, "id")
			assertCustomModelsFileUnchanged(t, api.configPath(), configBefore)
			assertCustomModelsFileUnchanged(t, api.credentialsStorePath(), credentialsBefore)
		})
	}
}

func deleteStoredCustomModelsFixture(t *testing.T, f customModelsFixture) {
	t.Helper()
	deleted := doProviderDelete(t, f.api, customModelsProviderID, "", nil, true)
	require.Equal(t, http.StatusOK, deleted.Code, "real DELETE must complete before the held PUT writes: %s", deleted.Body.String())
	var response gen.ProviderDeleteResponse
	require.NoError(t, json.Unmarshal(deleted.Body.Bytes(), &response))
	require.True(t, response.Deleted, "DELETE fixture must actually remove the provider")
	require.Empty(t, diskProviderIDs(t, f.api.homePath), "DELETE must remove the persisted row")
	require.Empty(t, getProviders(t, f.api), "DELETE must remove the live row")
	keys, err := f.api.credStore.List()
	require.NoError(t, err)
	require.Empty(t, keys, "DELETE must remove the custom key, not leave a seeded illusion")
}

func TestRestProviders_CustomModels_R5ConcurrentDelete(t *testing.T) {
	t.Run("real_delete_fixture_control", func(t *testing.T) {
		// Exercise the downstream delete fixture even while fallback admission is
		// missing. A passing control is not a passing concurrent-save guard.
		deleteStoredCustomModelsFixture(t, newStoredCustomModelsFixture(t))
	})
	t.Run("fallback_admitted_save_must_not_recreate_deleted_row", func(t *testing.T) {
		f := newStoredCustomModelsFixture(t)
		models := []string{"test/model-a"}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/providers/"+customModelsProviderID,
			strings.NewReader(customModelsRequestJSON(t, gen.ProviderUpdateRequest{Models: &models})))
		r.Header.Set("Content-Type", "application/json")
		r = isolateRateLimit(t, r)
		// Existing production stage seam: the same real stages and order as
		// putProviderHTTP. No fake admitted state, goroutine timing or sleep.
		p, admitted := f.api.providerPutAdmit(w, r, customModelsProviderID)
		require.True(t, admitted, "R5 prerequisite: real models-only admission must succeed before deterministic DELETE; status=%d body=%s", w.Code, w.Body.String())
		require.True(t, f.api.providerPutResolveExisting(p), "R5: resolve before DELETE: %s", w.Body.String())
		require.True(t, f.api.providerPutValidateKey(p), "R5: no-key validation before DELETE: %s", w.Body.String())
		require.True(t, f.api.providerPutStoreKeyAndModels(p), "R5: prepare models before DELETE: %s", w.Body.String())
		deleteStoredCustomModelsFixture(t, f)
		configAfterDelete := snapshotCustomModelsFile(t, f.api.configPath())
		credentialsAfterDelete := snapshotCustomModelsFile(t, f.api.credentialsStorePath())
		persisted := f.api.providerPutPersist(p)
		if persisted {
			// Complete the real handler sequence to observe the actual HTTP result
			// of an erroneous successful write, rather than the recorder default.
			f.api.providerPutRespond(p)
		}
		assert.False(t, persisted, "R5: a fallback-admitted save must refuse after its row was removed")
		assert.Equal(t, http.StatusConflict, w.Code, "R5: removed-during-save is 409, not recreate or generic failure: %s", w.Body.String())
		var response gen.ErrorResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		assert.Equal(t, "provider was removed or changed; reload", response.Error, "R5: actionable conflict reason")
		assertCustomModelsFileUnchanged(t, f.api.configPath(), configAfterDelete)
		assertCustomModelsFileUnchanged(t, f.api.credentialsStorePath(), credentialsAfterDelete)
		assert.Empty(t, diskProviderIDs(t, f.api.homePath), "R5: no row may be resurrected")
		assert.Empty(t, getProviders(t, f.api), "R5: no resurrected row may be listed")
		assert.Equal(t, int32(1), f.probes.Load(), "R5: no key probe for the held model save")
	})
}
