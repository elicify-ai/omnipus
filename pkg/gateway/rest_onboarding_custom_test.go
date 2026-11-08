package gateway

// rest_onboarding_custom_test.go — WP-1 (#1700) RED tests.
//
// Specification: /workspace/omnipus-coordination/designs-20261029/onboarding-custom-endpoint-design.md
// (architect design note, 2026-11-08). EVERY expected value in this file —
// status codes, the exact M1–M8 message texts, the persisted row shape, the
// 64/65-character id boundary — is derived from that design (§2.2 path table,
// §2.4 checks C1–C8 and the 422, §2.5 messages, §5 R-table). No expectation
// was read off the implementation: these tests were written to fail against
// the pre-change code, and the saved failing-run logs are the RED proof.
//
// What is real here: the whole restAPI handler chain (decode, admission,
// config mutation, credential store, onboarding manager), the real
// security.SSRFChecker (R6) and the real providers.CreateProviderFromConfig
// (R18). What is mocked: only the process edge — the custom endpoint itself
// is a local httptest recording server, and R18's credential injection uses
// the documented env seam (config_models.go: APIKey reads the ref's env var,
// which is what credentials.InjectFromConfig produces at boot).
//
// Request bodies are built as raw JSON maps (not generated Go types), so the
// file compiles against the current wire and every failure below is a
// behavioural assertion failure, not a build error.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// The eight refusal messages, verbatim from the design §2.5. These are the
// oracles; the implementation must converge on them, not vice versa.
const (
	msgM1Fmt = `"%s" is the name of a listed provider. Choose a different name for your custom endpoint, for example "my-%s".`
	msgM2    = `Choose a custom endpoint name of up to 64 characters, without "/", "\" or "..".`
	msgM3    = `The protocol must be "openai-compatible" or "anthropic".`
	msgM4    = `Enter the base URL of your custom endpoint, for example https://llm.example.com/v1.`
	msgM5    = `Choose the protocol your custom endpoint uses: "openai-compatible" or "anthropic".`
	msgM6    = `A region can't be set for a custom endpoint. Remove the region and try again.`
	msgM7    = `Enter the model name your custom endpoint should use.`
	msgM8    = `The provider address points at an internal network, which is not reachable from here. Use the provider's public address.`
)

// --- hermetic recording upstream -------------------------------------------

type recordedRequest struct {
	Method        string
	Path          string
	Authorization string
	Body          []byte
}

// recordingUpstream is the custom endpoint under the test's control: an
// OpenAI-compatible stand-in that records every request it receives and
// answers /models with a public catalog and everything else with the
// configured status/body.
type recordingUpstream struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []recordedRequest
}

func (ru *recordingUpstream) received() []recordedRequest {
	ru.mu.Lock()
	defer ru.mu.Unlock()
	out := make([]recordedRequest, len(ru.requests))
	copy(out, ru.requests)
	return out
}

func newRecordingUpstream(t *testing.T, status int, respBody string) *recordingUpstream {
	t.Helper()
	ru := &recordingUpstream{}
	ru.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ru.mu.Lock()
		ru.requests = append(ru.requests, recordedRequest{
			Method:        r.Method,
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			Body:          body,
		})
		ru.mu.Unlock()
		// A public /models catalog (the OpenRouter pattern the existing
		// fixtures use), so a GET /models can never be mistaken for the key
		// check; every other path gets the configured answer.
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"}]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(ru.srv.Close)
	return ru
}

const okCompletionJSON = `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`

// --- request bodies (raw JSON maps, per the RED brief) ----------------------

// customCompleteBody builds a POST /api/v1/onboarding/complete body as a raw
// JSON map. The defaults are the design R1 happy path; `overrides` replaces
// provider keys (use "" where a distinct empty value is needed) and `omit`
// deletes provider keys so the wire genuinely lacks them (absent, not "").
func customCompleteBody(upstreamBase string, overrides map[string]string, omit ...string) string {
	prov := map[string]any{
		"auth_method": "api_key",
		"id":          "my-azure",
		"api_key":     "k",
		"model":       "gpt-4o-prod",
		"endpoint":    upstreamBase + "/openai/v1",
		"protocol":    "openai-compatible",
	}
	for k, v := range overrides {
		prov[k] = v
	}
	for _, k := range omit {
		delete(prov, k)
	}
	body := map[string]any{
		"provider":    prov,
		"preferences": map[string]any{"name": "Daniel", "tone": "direct", "detail": "brief"},
	}
	out, err := json.Marshal(body)
	if err != nil {
		panic("customCompleteBody: marshal failed: " + err.Error())
	}
	return string(out)
}

// customProbeBody builds the POST /api/v1/onboarding/probe-provider body
// (ProbeProviderRequest wire keys: id, auth, api_key, model, api_base,
// protocol) for the same id, with api_base/protocol omitted when empty.
func customProbeBody(upstreamBase, protocol string) string {
	prov := map[string]any{
		"id":      "my-azure",
		"auth":    "api_key",
		"api_key": "k",
		"model":   "gpt-4o-prod",
	}
	if upstreamBase != "" {
		prov["api_base"] = upstreamBase + "/openai/v1"
	}
	if protocol != "" {
		prov["protocol"] = protocol
	}
	// ProbeProviderRequest is a flat object, not wrapped in "provider".
	out, err := json.Marshal(prov)
	if err != nil {
		panic("customProbeBody: marshal failed: " + err.Error())
	}
	return string(out)
}

// --- scaffolding -------------------------------------------------------------

// newCustomOnboardingAPI mirrors the existing complete-onboarding harness
// (withEdition + fresh install config.json + mustAgentLoop +
// newOnboardingTestAPI) and returns the API plus its home directory, where
// the handler persists config.json. validateInbound flips the
// Gateway.ValidateInbound flag the decode step reads (design R5's second
// Given).
func newCustomOnboardingAPI(t *testing.T, validateInbound bool) (*restAPI, string) {
	t.Helper()
	withEdition(t, config.EditionHosted)
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, ValidateInbound: validateInbound},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return newOnboardingTestAPI(t, tmpDir, al), tmpDir
}

func postComplete(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleCompleteOnboarding(w, signedIn(req))
	return w
}

func decodeResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
		"response must be JSON (body=%s)", w.Body.String())
	return resp
}

// requireErrField asserts the design's fielded refusals: exact status, exact
// error text (design §2.5), and the exact field name.
func requireErrField(t *testing.T, w *httptest.ResponseRecorder, status int, field, wantMsg string) {
	t.Helper()
	require.Equal(t, status, w.Code, "body=%s", w.Body.String())
	resp := decodeResponse(t, w)
	assert.Equal(t, wantMsg, resp["error"], "error message must be the design's exact text")
	assert.Equal(t, field, resp["field"], "refusal must name the field the design names")
}

// requireErrNoField asserts the design's field-less refusals: exact status,
// exact error text, and NO field key on the wire.
func requireErrNoField(t *testing.T, w *httptest.ResponseRecorder, status int, wantMsg string) {
	t.Helper()
	require.Equal(t, status, w.Code, "body=%s", w.Body.String())
	resp := decodeResponse(t, w)
	assert.Equal(t, wantMsg, resp["error"], "error message must be the design's exact text")
	_, hasField := resp["field"]
	assert.False(t, hasField, "this refusal must carry no field key (body=%s)", w.Body.String())
}

// assertNothingStored is the design's "nothing written; onboarding still
// open": no credential under <id>_API_KEY, config.json still carries an empty
// providers list, onboarding not complete.
func assertNothingStored(t *testing.T, api *restAPI, tmpDir, id string) {
	t.Helper()
	_, credErr := api.credStore.Get(id + "_API_KEY")
	require.Error(t, credErr, "a refused request must not write a credential")
	assert.False(t, api.onboardingMgr.IsComplete(), "a refused request must leave onboarding open")
	raw, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	var saved map[string]any
	require.NoError(t, json.Unmarshal(raw, &saved))
	provs, _ := saved["providers"].([]any)
	assert.Empty(t, provs, "a refused request must not persist a provider row (config.json=%s)", string(raw))
}

// savedConfig is what the handler persists into tmpDir/config.json, read back
// through plain JSON so the assertions are on the wire keys the design names.
type savedConfig struct {
	Providers []map[string]any `json:"providers"`
	Agents    struct {
		Defaults struct {
			DefaultModel map[string]any `json:"default_model"`
		} `json:"defaults"`
	} `json:"agents"`
}

func readSavedConfig(t *testing.T, tmpDir string) savedConfig {
	t.Helper()
	var saved savedConfig
	require.NoError(t, json.Unmarshal(mustRawConfig(t, tmpDir), &saved),
		"config.json must parse (content=%s)", string(mustRawConfig(t, tmpDir)))
	return saved
}

func mustRawConfig(t *testing.T, tmpDir string) []byte {
	t.Helper()
	raw, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	return raw
}

// --- R1: a custom row completes setup and is saved as a custom row ----------

// TestOnboardingCompleteCustom_SavesCustomRow — design R1.
//
// BDD: Given a fresh install with onboarding open and the catalog loaded,
// When complete is sent with id "my-azure", endpoint "<srv>/openai/v1",
// protocol "openai-compatible", model "gpt-4o-prod", api_key "k" and the
// endpoint answers the key check 200,
// Then 200; config.json carries exactly the row {provider, model, api_base,
// protocol, custom:true, auth_method, api_key_ref}, agents.defaults.default_model
// is {provider, model}, the key is stored under my-azure_API_KEY, and
// onboarding is complete.
func TestOnboardingCompleteCustom_SavesCustomRow(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, nil))

	require.Equal(t, http.StatusOK, w.Code,
		"a custom endpoint with endpoint+protocol+model+key must complete setup (body=%s)", w.Body.String())

	saved := readSavedConfig(t, tmpDir)
	require.Len(t, saved.Providers, 1, "exactly one provider row must be saved")
	row := saved.Providers[0]
	assert.Equal(t, "my-azure", row["provider"], "row.provider")
	assert.Equal(t, "gpt-4o-prod", row["model"], "row.model")
	assert.Equal(t, ru.srv.URL+"/openai/v1", row["api_base"], "row.api_base must be the endpoint sent")
	assert.Equal(t, "openai-compatible", row["protocol"], "row.protocol")
	assert.Equal(t, true, row["custom"], "row.custom must be true — the row is recognised by the flag, never by a literal id")
	assert.Equal(t, "api_key", row["auth_method"], "row.auth_method")
	assert.Equal(t, "my-azure_API_KEY", row["api_key_ref"], "row.api_key_ref (design: <id>_API_KEY)")

	assert.Equal(t,
		map[string]any{"provider": "my-azure", "model": "gpt-4o-prod"},
		saved.Agents.Defaults.DefaultModel,
		"agents.defaults.default_model must be {provider, model}")

	stored, err := api.credStore.Get("my-azure_API_KEY")
	require.NoError(t, err, "the key must be stored under <id>_API_KEY")
	assert.Equal(t, "k", stored)

	assert.True(t, api.onboardingMgr.IsComplete(), "onboarding must be marked complete")
}

// --- R2: old bodies behave as before -----------------------------------------

// TestOnboardingCompleteCustom_NoProtocolNoEndpoint_Unchanged400 — design R2
// (C0 branch). This body is exactly today's behaviour and MUST stay so after
// the fix, which is why it is written even though it already passes.
//
// BDD: Given a fresh install, When complete is sent with id "my-azure" and
// neither protocol nor endpoint, Then 400 with error exactly
// `unknown provider "my-azure"` and no field key; nothing written; onboarding
// still open.
func TestOnboardingCompleteCustom_NoProtocolNoEndpoint_Unchanged400(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, nil, "protocol", "endpoint"))

	requireErrNoField(t, w, http.StatusBadRequest, `unknown provider "my-azure"`)
	assert.Empty(t, ru.received(), "a refused id must not reach the endpoint")
	assertNothingStored(t, api, tmpDir, "my-azure")
}

// --- R3: missing protocol -----------------------------------------------------

// TestOnboardingCompleteCustom_MissingProtocol_400 — design R3 (check C4).
//
// BDD: Given endpoint is set and protocol absent, When complete is sent,
// Then 400, field "protocol", M5; nothing written.
func TestOnboardingCompleteCustom_MissingProtocol_400(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, nil, "protocol"))

	requireErrField(t, w, http.StatusBadRequest, "protocol", msgM5)
	assert.Empty(t, ru.received(), "a refused request must not reach the endpoint")
	assertNothingStored(t, api, tmpDir, "my-azure")
}

// --- R4: missing base ----------------------------------------------------------

// TestOnboardingCompleteCustom_MissingEndpoint_400 — design R4 (check C3),
// table over the absent and the whitespace-only base (§2.1: trim once, at
// decode).
//
// BDD: When protocol is set and endpoint absent — 400, field "endpoint", M4;
// and again with endpoint "   " — the same refusal; nothing written in
// either case.
func TestOnboardingCompleteCustom_MissingEndpoint_400(t *testing.T) {
	cases := []struct {
		name      string
		omit      []string
		overrides map[string]string
	}{
		{"endpoint_absent", []string{"endpoint"}, nil},
		{"endpoint_whitespace", nil, map[string]string{"endpoint": "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
			api, tmpDir := newCustomOnboardingAPI(t, false)

			w := postComplete(t, api, customCompleteBody(ru.srv.URL, tc.overrides, tc.omit...))

			requireErrField(t, w, http.StatusBadRequest, "endpoint", msgM4)
			assert.Empty(t, ru.received(), "a refused request must not reach the endpoint")
			assertNothingStored(t, api, tmpDir, "my-azure")
		})
	}
}

// --- R5: bad protocol value ----------------------------------------------------

// TestOnboardingCompleteCustom_BadProtocol_400 — design R5 (check C2), table
// over the two Given states of validate_inbound.
//
// BDD: Given validate_inbound off, When protocol "google", Then 400, field
// "protocol", M3. Given validate_inbound on, Then 400 with a schema error
// whose message names OnboardingCompleteRequest.
func TestOnboardingCompleteCustom_BadProtocol_400(t *testing.T) {
	t.Run("validate_inbound_off_field_protocol_m3", func(t *testing.T) {
		ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
		api, tmpDir := newCustomOnboardingAPI(t, false)

		w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"protocol": "google"}))

		requireErrField(t, w, http.StatusBadRequest, "protocol", msgM3)
		assert.Empty(t, ru.received(), "a refused request must not reach the endpoint")
		assertNothingStored(t, api, tmpDir, "my-azure")
	})

	t.Run("validate_inbound_on_schema_error_names_the_request", func(t *testing.T) {
		ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
		api, tmpDir := newCustomOnboardingAPI(t, true)

		w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"protocol": "google"}))

		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		resp := decodeResponse(t, w)
		assert.Contains(t, resp["error"], "OnboardingCompleteRequest",
			"with validate_inbound on the schema error must name the request schema, got %q", resp["error"])
		assertNothingStored(t, api, tmpDir, "my-azure")
	})
}

// --- R6: address guard ----------------------------------------------------------

// TestOnboardingCompleteCustom_SSRFBlocked_422 — design R6 (the custom path's
// address check, design D3: refuse, do not warn-and-continue).
//
// BDD: Given a real SSRFChecker with no allowlist, When the endpoint is the
// loopback test server with a valid protocol, Then 422, no field, M8; the
// test server received NO request; no credential stored; onboarding still
// open.
func TestOnboardingCompleteCustom_SSRFBlocked_422(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)
	api.ssrfChecker = security.NewSSRFChecker(nil)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"endpoint": ru.srv.URL + "/v1"}))

	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"a blocked address on a custom row must be refused, not warned past (body=%s)", w.Body.String())
	resp := decodeResponse(t, w)
	assert.Equal(t, msgM8, resp["error"], "error message must be M8 verbatim")
	_, hasField := resp["field"]
	assert.False(t, hasField, "the 422 carries no field key (body=%s)", w.Body.String())

	assert.Empty(t, ru.received(), "the blocked endpoint must receive no request")
	assertNothingStored(t, api, tmpDir, "my-azure")
}

// --- R8/R9: a listed provider's name is refused as a custom name -------------

// TestOnboardingCompleteCustom_CatalogIdWithProtocol_400 — design R8 (new
// refusal K1): catalog id "openai" plus protocol means the user named their
// custom endpoint after a listed provider.
//
// BDD: When complete is sent with id "openai", endpoint and protocol, Then
// 400, field "id", M1 naming openai/my-openai; nothing written.
func TestOnboardingCompleteCustom_CatalogIdWithProtocol_400(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"id": "openai"}))

	requireErrField(t, w, http.StatusBadRequest, "id", fmt.Sprintf(msgM1Fmt, "openai", "my-openai"))
	assertNothingStored(t, api, tmpDir, "openai")
}

// TestOnboardingCompleteCustom_UnsupportedIdWithProtocol_400 — design R9: the
// listed-but-unsupported "azure" row used as a custom name is refused with M1
// (not the tier's unsupported-deployment-URL text). This is the wizard's
// "azure trap": with protocol present the user demonstrably means Custom
// endpoint, and M1 says so.
//
// BDD: When complete is sent with id "azure", endpoint and protocol, Then
// 400, field "id", M1 naming azure/my-azure; nothing written.
func TestOnboardingCompleteCustom_UnsupportedIdWithProtocol_400(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"id": "azure"}))

	requireErrField(t, w, http.StatusBadRequest, "id", fmt.Sprintf(msgM1Fmt, "azure", "my-azure"))
	assertNothingStored(t, api, tmpDir, "azure")
}

// --- R10: catalog ids unchanged ------------------------------------------------

// TestOnboardingCompleteCustom_CatalogEndpointOverrideUnchanged — design R10's
// second half (new regression guard): a CATALOG id with an endpoint override
// and no protocol keeps today's behaviour exactly — the override is persisted
// as api_base and the row gains neither a custom flag nor a protocol.
//
// BDD: When complete is sent with id "openrouter", an endpoint override and
// no protocol, Then 200 and the saved row's api_base is that endpoint and it
// carries NO custom and NO protocol key.
//
// Hermeticity: the design's scenario names https://eu.example.com/v1; the
// test points the override at the local recording server instead — the same
// convention the existing override tests use (no live egress from CI).
func TestOnboardingCompleteCustom_CatalogEndpointOverrideUnchanged(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)
	override := ru.srv.URL + "/v1"

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{
		"id":       "openrouter",
		"endpoint": override,
	}, "protocol"))

	require.Equal(t, http.StatusOK, w.Code,
		"a catalog id with an endpoint override must keep completing setup (body=%s)", w.Body.String())

	saved := readSavedConfig(t, tmpDir)
	require.Len(t, saved.Providers, 1)
	row := saved.Providers[0]
	assert.Equal(t, "openrouter", row["provider"])
	assert.Equal(t, override, row["api_base"], "the override must be persisted as api_base, unchanged")
	_, hasCustom := row["custom"]
	assert.False(t, hasCustom, "a catalog row must NOT gain a custom key")
	_, hasProtocol := row["protocol"]
	assert.False(t, hasProtocol, "a catalog row must NOT gain a protocol key")
}

// --- R11: empty id (unchanged) ---------------------------------------------------

// TestOnboardingCompleteCustom_EmptyId_400 — design R11. Unchanged behaviour,
// pinned so the new branch cannot swallow it.
//
// BDD: When complete is sent with id "" (endpoint and protocol set), Then 400
// "provider.id is required".
func TestOnboardingCompleteCustom_EmptyId_400(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"id": ""}))

	requireErrNoField(t, w, http.StatusBadRequest, "provider.id is required")
	assertNothingStored(t, api, tmpDir, "my-azure")
}

// --- R12: id shape and the 64/65 boundary ----------------------------------------

// TestOnboardingCompleteCustom_IdShape — design R12 (check C1, the PUT rule).
//
// BDD: ids "../x", "a/b", "a\\b" (each with endpoint+protocol) are refused
// 400, field "id", M2; a 64-character id completes 200; a 65-character id is
// refused 400, field "id", M2.
func TestOnboardingCompleteCustom_IdShape(t *testing.T) {
	badIds := []string{"../x", "a/b", "a\\b"}
	for _, id := range badIds {
		t.Run("bad_shape_"+strings.ReplaceAll(id, "/", "_"), func(t *testing.T) {
			ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
			api, tmpDir := newCustomOnboardingAPI(t, false)

			w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"id": id}))

			requireErrField(t, w, http.StatusBadRequest, "id", msgM2)
			assertNothingStored(t, api, tmpDir, "my-azure")
		})
	}

	t.Run("id_of_64_characters_completes", func(t *testing.T) {
		ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
		api, _ := newCustomOnboardingAPI(t, false)
		id64 := strings.Repeat("a", 64)

		w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"id": id64}))

		require.Equal(t, http.StatusOK, w.Code,
			"a 64-character custom id is allowed (boundary, body=%s)", w.Body.String())
		assert.True(t, api.onboardingMgr.IsComplete())
	})

	t.Run("id_of_65_characters_refused", func(t *testing.T) {
		ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
		api, tmpDir := newCustomOnboardingAPI(t, false)
		id65 := strings.Repeat("a", 65)

		w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"id": id65}))

		requireErrField(t, w, http.StatusBadRequest, "id", msgM2)
		assertNothingStored(t, api, tmpDir, id65)
	})
}

// --- R13: reserved ids (unchanged) -------------------------------------------------

// TestOnboardingCompleteCustom_ReservedId_400 — design R13. Unchanged
// behaviour, pinned: the reserved segment check fires before the custom path.
//
// BDD: When complete is sent with id "catalog" (endpoint+protocol set), Then
// 400 `unknown provider "catalog"`, field "id".
func TestOnboardingCompleteCustom_ReservedId_400(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"id": "catalog"}))

	requireErrField(t, w, http.StatusBadRequest, "id", `unknown provider "catalog"`)
	assertNothingStored(t, api, tmpDir, "catalog")
}

// --- R14: model required -------------------------------------------------------------

// TestOnboardingCompleteCustom_MissingModel_400 — design R14 (check C7):
// without an explicit model the completion default switch would write a
// model the custom endpoint may not serve.
//
// BDD: When the model is absent, Then 400, field "model", M7; nothing
// written.
func TestOnboardingCompleteCustom_MissingModel_400(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, nil, "model"))

	requireErrField(t, w, http.StatusBadRequest, "model", msgM7)
	assert.Empty(t, ru.received(), "a refused request must not reach the endpoint")
	assertNothingStored(t, api, tmpDir, "my-azure")
}

// --- R15: region refused ---------------------------------------------------------------

// TestOnboardingCompleteCustom_Region_400 — design R15 (check C6).
//
// BDD: When region "eu-central-1" is set on a custom body, Then 400, field
// "region", M6; nothing written.
func TestOnboardingCompleteCustom_Region_400(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, map[string]string{"region": "eu-central-1"}))

	requireErrField(t, w, http.StatusBadRequest, "region", msgM6)
	assert.Empty(t, ru.received(), "a refused request must not reach the endpoint")
	assertNothingStored(t, api, tmpDir, "my-azure")
}

// --- R16: the key is checked against the chosen model -----------------------------------

// TestOnboardingCompleteCustom_KeyProbeUsesChosenModel — design R16.
//
// BDD: When the endpoint answers 401 invalid_api_key, Then 400 with the
// validator's invalid-key message and nothing stored. When it answers 200,
// Then the ONE request it received was POST {base}/chat/completions with body
// model "gpt-4o-prod" and header "Authorization: Bearer k" — i.e. the key was
// probed with ProbeModels=[model] (the deployment name), never via a guessed
// /models fallback.
func TestOnboardingCompleteCustom_KeyProbeUsesChosenModel(t *testing.T) {
	t.Run("invalid_key_400_nothing_stored", func(t *testing.T) {
		ru := newRecordingUpstream(t, http.StatusUnauthorized,
			`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`)
		api, tmpDir := newCustomOnboardingAPI(t, false)

		w := postComplete(t, api, customCompleteBody(ru.srv.URL, nil))

		require.Equal(t, http.StatusBadRequest, w.Code,
			"a key the endpoint rejects must not complete setup (body=%s)", w.Body.String())
		resp := decodeResponse(t, w)
		errMsg, _ := resp["error"].(string)
		assert.Contains(t, errMsg, "rejected", "the message must be the validator's invalid-key message, got %q", errMsg)
		assertNothingStored(t, api, tmpDir, "my-azure")
	})

	t.Run("valid_key_probes_the_chosen_model", func(t *testing.T) {
		ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
		api, _ := newCustomOnboardingAPI(t, false)

		w := postComplete(t, api, customCompleteBody(ru.srv.URL, nil))

		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		got := ru.received()
		require.Len(t, got, 1, "the key check must make exactly one request against the endpoint (got %v)", got)
		req := got[0]
		assert.Equal(t, http.MethodPost, req.Method, "the key check must POST")
		assert.True(t, strings.HasSuffix(req.Path, "/chat/completions"),
			"the key check must hit {base}/chat/completions, got path %q", req.Path)
		assert.Equal(t, "Bearer k", req.Authorization, "the key must travel as the Bearer credential")
		var probeBody map[string]any
		require.NoError(t, json.Unmarshal(req.Body, &probeBody), "request body must be JSON")
		assert.Equal(t, "gpt-4o-prod", probeBody["model"],
			"the key must be checked against the chosen model (the deployment name), got %v", probeBody["model"])
	})
}

// --- R17: sign-in cannot create a custom row (unchanged) ---------------------------------

// TestOnboardingCompleteCustom_SignInUnknownId_400 — design R17. Both halves
// are unchanged behaviour, pinned so the custom branch cannot leak into the
// sign-in variant.
//
// BDD: When auth_method "sign_in" with id "my-x" and an endpoint, Then 400
// `unknown provider "my-x"`. When the sign-in body carries protocol, Then 400
// with the strict decode's "field not allowed" refusal.
func TestOnboardingCompleteCustom_SignInUnknownId_400(t *testing.T) {
	t.Run("sign_in_unknown_id_unknown_provider", func(t *testing.T) {
		ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
		api, tmpDir := newCustomOnboardingAPI(t, false)
		body := customCompleteBody(ru.srv.URL, map[string]string{
			"id":          "my-x",
			"auth_method": "sign_in",
		}, "protocol", "api_key")

		w := postComplete(t, api, body)

		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		resp := decodeResponse(t, w)
		assert.Equal(t, `unknown provider "my-x"`, resp["error"])
		assertNothingStored(t, api, tmpDir, "my-x")
	})

	t.Run("sign_in_with_protocol_field_not_allowed", func(t *testing.T) {
		ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
		api, tmpDir := newCustomOnboardingAPI(t, false)
		body := customCompleteBody(ru.srv.URL, map[string]string{
			"id":          "my-x",
			"auth_method": "sign_in",
			"protocol":    "openai-compatible",
		}, "api_key")

		w := postComplete(t, api, body)

		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		resp := decodeResponse(t, w)
		assert.Contains(t, resp["error"], "field not allowed",
			"the strict decode must refuse protocol on the sign_in variant, got %q", resp["error"])
		assertNothingStored(t, api, tmpDir, "my-x")
	})
}

// --- R18: the saved row works at run time --------------------------------------------------

// TestOnboardingCompleteCustom_SavedRowBuildsAtRuntime — design R18
// (integration, handler level): the real proof of R1's row.
//
// BDD: Given the saved config of the R1 flow, When the row is read back and
// built with providers.CreateProviderFromConfig and one chat call is made,
// Then no error and the endpoint receives POST {base}/chat/completions with
// the Bearer key.
func TestOnboardingCompleteCustom_SavedRowBuildsAtRuntime(t *testing.T) {
	ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
	api, tmpDir := newCustomOnboardingAPI(t, false)

	w := postComplete(t, api, customCompleteBody(ru.srv.URL, nil))
	require.Equal(t, http.StatusOK, w.Code, "the custom row must save first (body=%s)", w.Body.String())

	saved := readSavedConfig(t, tmpDir)
	require.Len(t, saved.Providers, 1)

	// The credential-injection seam: at boot credentials.InjectFromConfig
	// exposes the stored key as the ref's environment variable
	// (config.ModelConfig.APIKey reads exactly that). Mirror it for the
	// runtime build.
	t.Setenv("my-azure_API_KEY", "k")

	// Load the SAVED row into the real config type — what the runtime boot
	// does — and build from that, not from a hand-copied struct.
	var loaded struct {
		Providers []config.ModelConfig `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(mustRawConfig(t, tmpDir), &loaded),
		"the saved config must decode into the real config types")
	require.Len(t, loaded.Providers, 1)

	provider, model, err := providers.CreateProviderFromConfig(&loaded.Providers[0])
	require.NoError(t, err, "the saved row must build into a real provider")
	require.Equal(t, "gpt-4o-prod", model, "the bare model id is returned verbatim")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := provider.Chat(ctx,
		[]providers.Message{{Role: "user", Content: "hi"}}, nil, model, nil)
	require.NoError(t, err, "one chat call against the saved custom row must succeed")
	require.NotNil(t, resp)

	got := ru.received()
	require.NotEmpty(t, got, "the endpoint must have received the chat call")
	last := got[len(got)-1]
	assert.Equal(t, http.MethodPost, last.Method)
	assert.True(t, strings.HasSuffix(last.Path, "/chat/completions"),
		"the chat call must hit {base}/chat/completions, got path %q", last.Path)
	assert.Equal(t, "Bearer k", last.Authorization, "the stored key must travel as the Bearer credential")
}

// --- R19: complete and the probe make the same admission decision ------------

// TestOnboardingCompleteCustom_AdmissionMatchesProbe — design R19: the probe
// and complete must agree on accept vs 400 for each id/base/protocol
// combination; complete keeps its field-less 400 for the bare-id case.
func TestOnboardingCompleteCustom_AdmissionMatchesProbe(t *testing.T) {
	cases := []struct {
		name         string
		withBase     bool
		withProtocol bool
		wantStatus   int
	}{
		{"base_and_protocol", true, true, http.StatusOK},
		{"base_only", true, false, http.StatusBadRequest},
		{"protocol_only", false, true, http.StatusBadRequest},
		{"alone", false, false, http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ru := newRecordingUpstream(t, http.StatusOK, okCompletionJSON)
			api, _ := newCustomOnboardingAPI(t, false)
			base := ""
			if tc.withBase {
				base = ru.srv.URL
			}
			protocol := ""
			if tc.withProtocol {
				protocol = "openai-compatible"
			}

			// The probe first — the same id on the same fresh install.
			probeReq := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider",
				strings.NewReader(customProbeBody(base, protocol)))
			probeReq.Header.Set("Content-Type", "application/json")
			probeW := httptest.NewRecorder()
			api.HandleOnboardingProbeProvider(probeW, withFreshInstallConfig(probeReq))
			require.Equal(t, tc.wantStatus, probeW.Code,
				"probe verdict for %s (body=%s)", tc.name, probeW.Body.String())

			// Then complete with the equivalent body.
			omit := []string{}
			if !tc.withProtocol {
				omit = append(omit, "protocol")
			}
			if !tc.withBase {
				omit = append(omit, "endpoint")
			}
			completeW := postComplete(t, api, customCompleteBody(ru.srv.URL, nil, omit...))
			require.Equal(t, tc.wantStatus, completeW.Code,
				"complete verdict for %s (body=%s)", tc.name, completeW.Body.String())

			if tc.name == "alone" {
				// Design R19: complete keeps the OLD field-less 400 here.
				requireErrNoField(t, completeW, http.StatusBadRequest, `unknown provider "my-azure"`)
			}
		})
	}
}
