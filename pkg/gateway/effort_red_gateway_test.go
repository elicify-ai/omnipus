// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// effort_red_gateway_test.go — RED pack, train T7 (effort levels), pkg/gateway half.
//
// Spec: designs-20261029/effort-levels-spec-r10.md (scenarios S-15, S-21, S-50,
// S-55, S-56, S-58, S-59, S-60, S-61, S-62, S-75, S-79, S-80, S-81, S-94, S-95;
// dataset D5; test rows T-R2/T-W1).
//
// Every assertion on a not-yet-existing field is JSON-level: the real wire or
// disk bytes are decoded into map[string]any and the key is asserted there.
// No production type is touched, no field is invented in test code.
//
// RED tests fail today for the documented missing-feature reason (the effort
// key is refused or dropped by today's strict/non-strict decodes, the value
// is lost on persist, or the refusal does not exist). PIN tests pin behaviour
// the spec requires to KEEP working after the feature lands.
//
// No test in this file calls a real LLM: providers are the package's
// restMockProvider stub or the loopback httptest fakes the existing gateway
// fixtures use (startFakeProviderUpstream for the onboarding key probe).
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// --- shared helpers (prefixed egw to avoid collisions in the package) ---

// egwPutAgentField sends PUT /api/v1/agents/test-agent with a raw JSON body,
// revision auto-filled from the entity store.
func egwPutAgentField(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleAgents(w, r)
	return w
}

// egwGetAgentRaw GETs one agent and returns the raw response JSON as a map.
func egwGetAgentRaw(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+id, nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET /api/v1/agents/%s must answer 200; body=%s", id, w.Body.String())
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m), "body=%s", w.Body.String())
	return m
}

// egwAgentEntityPath is the persisted entity JSON for the fixture agent
// (ADR-054: agents persist under $OMNIPUS_HOME/entities/agents/<id>.json).
func egwAgentEntityPath(api *restAPI, id string) string {
	return filepath.Join(api.homePath, "entities", "agents", id+".json")
}

// egwReadAgentEntity reads the persisted entity JSON as a raw map.
func egwReadAgentEntity(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(egwAgentEntityPath(api, id))
	require.NoError(t, err, "agent entity file must exist")
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

// egwWriteAgentEntity writes the entity JSON back (used to hand-seed the
// stored effort form the feature must persist and keep).
func egwWriteAgentEntity(t *testing.T, api *restAPI, id string, m map[string]any) {
	t.Helper()
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(egwAgentEntityPath(api, id), raw, 0o600))
}

// --- S-50 ---

// TestT_GW_AgentPrimaryEffortSavedAndReadBack is S-50: PUT
// /api/v1/agents/{id} accepts a per-agent primary-model effort and the value
// survives a read back through GET and the saved agent JSON.
//
// Traces to: effort-levels-spec-r10.md S-50
//
// RED today: gen.AgentUpdateRequest has no reasoning_effort field and the
// PUT's raw-body shape check (validateAgentUpdateShape, DisallowUnknownFields)
// refuses the whole update with 400 "invalid agent update: json: unknown
// field \"reasoning_effort\"" — the spec requires the PUT to accept and
// persist the effort, so neither the GET readback nor the persisted agent
// JSON can carry the value.
func TestT_GW_AgentPrimaryEffortSavedAndReadBack(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := egwPutAgentField(t, api, `{"reasoning_effort":"high"}`)
	require.Equal(t, http.StatusOK, w.Code,
		"RED S-50: the PUT must accept a per-agent primary-model effort; today the strict shape check "+
			"refuses the unknown field (got %d, body=%s)", w.Code, w.Body.String())

	// Differentiation: a control field in the same body must round-trip, so a
	// failure below is about the effort key, never about the write path.
	w2 := egwPutAgentField(t, api, `{"reasoning_effort":"high","description":"probe"}`)
	require.Equal(t, http.StatusOK, w2.Code, "body=%s", w2.Body.String())
	got := egwGetAgentRaw(t, api, "test-agent")
	require.NotNil(t, got["description"], "control: description must round-trip")

	require.Equal(t, "high", got["reasoning_effort"],
		"RED S-50: GET must return the stored primary-model effort — today the PUT body's "+
			"reasoning_effort is dropped by the non-strict decode (got %v)", got["reasoning_effort"])

	stored := egwReadAgentEntity(t, api, "test-agent")
	require.Equal(t, "high", stored["reasoning_effort"],
		"RED S-50: the saved agent JSON must contain the reasoning_effort key (got %v)", stored["reasoning_effort"])
}

// --- S-56 ---

// TestT_GW_AgentEffortSaveValidationRows is S-56's REST rows: an unoffered
// level is stored as written (the turn maps it later), while an
// over-32-character value is refused with HTTP 400 and nothing is saved.
//
// Traces to: effort-levels-spec-r10.md S-56
//
// RED today: the unoffered level cannot even be sent — the strict shape check
// refuses the whole PUT with 400 (unknown field). The 33-character row is
// PIN-by-status: today it is ALSO refused 400, but by the unknown-field shape
// check, not the handler's effort length check the spec names; the status
// must stay 400 once reasoning_effort is a declared field.
func TestT_GW_AgentEffortSaveValidationRows(t *testing.T) {
	api := buildExecutorTestAPI(t)

	t.Run("unoffered level stored as written", func(t *testing.T) {
		// The fixture agent's model offers no catalog levels at all, which is
		// exactly S-56's premise: REST saves never check a stored effort
		// against the catalog (S-55); the value is kept as written.
		w := egwPutAgentField(t, api, `{"reasoning_effort":"max"}`)
		require.Equal(t, http.StatusOK, w.Code,
			"RED S-56: an unoffered level must be stored as written; today the strict shape check "+
				"refuses the whole PUT (got %d, body=%s)", w.Code, w.Body.String())
		got := egwGetAgentRaw(t, api, "test-agent")
		require.Equal(t, "max", got["reasoning_effort"],
			"RED S-56: an unoffered level must be stored as written (got %v)", got["reasoning_effort"])
	})

	t.Run("over-32-character value refused 400, nothing saved", func(t *testing.T) {
		// PIN-by-status: refused 400 today via the unknown-field shape check;
		// the spec's own effort length check must keep the 400.
		long := strings.Repeat("x", 33)
		body, err := json.Marshal(map[string]any{"reasoning_effort": long})
		require.NoError(t, err)
		w := egwPutAgentField(t, api, string(body))
		require.Equal(t, http.StatusBadRequest, w.Code,
			"RED S-56: a 33-character effort must be refused with HTTP 400; today it is silently "+
				"dropped and the PUT answers 200 (got %d, body=%s)", w.Code, w.Body.String())

		// Nothing saved: the earlier "max" (if stored) or no value remains —
		// in either case the 33-character string must not appear anywhere.
		got := egwGetAgentRaw(t, api, "test-agent")
		require.NotEqual(t, long, got["reasoning_effort"],
			"the over-length value must never be stored")
	})
}

// --- S-55 ---

// TestT_GW_AgentNameOnlyEditKeepsStoredEffort is S-55: a REST save that does
// not touch the model never re-checks the stored effort against the catalog —
// a display-name-only edit keeps the stored level exactly as written.
//
// Traces to: effort-levels-spec-r10.md S-55
//
// RED today: the hand-seeded stored level does not survive the save because
// the typed entity decode drops the unknown key and the persist writes the
// typed copy back.
func TestT_GW_AgentNameOnlyEditKeepsStoredEffort(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Hand-seed the stored effort in the persisted entity JSON — the exact
	// disk form the feature must keep. No catalog is configured, so no level
	// here is "offered": S-55's premise is that this must not matter.
	stored := egwReadAgentEntity(t, api, "test-agent")
	stored["reasoning_effort"] = "max"
	egwWriteAgentEntity(t, api, "test-agent", stored)

	w := egwPutAgentField(t, api, `{"name":"Renamed Agent"}`)
	require.Equal(t, http.StatusOK, w.Code,
		"S-55: a name-only edit must save successfully; body=%s", w.Body.String())

	after := egwReadAgentEntity(t, api, "test-agent")
	require.Equal(t, "max", after["reasoning_effort"],
		"RED S-55: the stored level must be kept as written across a name-only save (got %v)",
		after["reasoning_effort"])
	require.Equal(t, "Renamed Agent", after["name"], "control: the name edit itself must persist")
}

// --- S-58 ---

// TestT_GW_AgentModelChangeWithoutEffortMappedOrBlocked is S-58's REST half:
// a model change that arrives WITHOUT reasoning_effort is either mapped to a
// compatible value (model with a `none` → Off kept/saved) or refused with the
// spec sentence when no Off exists on the target (levels low/medium/high, no
// switch).
//
// Traces to: effort-levels-spec-r10.md S-58
//
// RED today: the handler has no effort mapping or refusal at all — both
// shapes answer 200 and persist the model change with no effort anywhere.
func TestT_GW_AgentModelChangeWithoutEffortMappedOrBlocked(t *testing.T) {
	for _, target := range []string{"m-none", "m-levels"} {
		t.Run(target, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			old := egwReadAgentEntity(t, api, "test-agent")
			old["provider"] = "openrouter"
			old["model"] = "m-old"
			old["reasoning_effort"] = "off"
			egwWriteAgentEntity(t, api, "test-agent", old)
			cfgPath := filepath.Join(api.homePath, "config.json")
			raw, err := os.ReadFile(cfgPath)
			require.NoError(t, err)
			var disk map[string]any
			require.NoError(t, json.Unmarshal(raw, &disk))
			disk["providers"] = []any{map[string]any{"provider": "openrouter", "model": "m-old", "api_key": "test", "api_base": "http://127.0.0.1:1"}, map[string]any{"provider": "openrouter", "model": target, "api_key": "test", "api_base": "http://127.0.0.1:1"}}
			raw, err = json.Marshal(disk)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(cfgPath, raw, 0600))
			cat := egwEffortCatalog(t)
			previous := providers.ProviderCatalog()
			providers.SetCatalog(cat)
			t.Cleanup(func() { providers.SetCatalog(previous) })
			api.providerCatalog = cat
			before, err := os.ReadFile(egwAgentEntityPath(api, "test-agent"))
			require.NoError(t, err)
			w := egwPutAgentField(t, api, `{"provider":"openrouter","model":"`+target+`"}`)
			if target == "m-none" {
				require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
				after := egwGetAgentRaw(t, api, "test-agent")
				assert.Equal(t, "m-none", after["model"])
				assert.Equal(t, "off", after["reasoning_effort"])
			} else {
				require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
				var response map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
				require.Equal(t, "m-levels can't turn reasoning off. Send reasoning_effort with one of: provider_default, low, medium, high.", response["error"])
				after, err := os.ReadFile(egwAgentEntityPath(api, "test-agent"))
				require.NoError(t, err)
				assert.Equal(t, before, after, "refused pair/value is byte-identical")
			}
		})
	}
}

// --- S-59 ---

// TestT_GW_DefaultModelEffortStored is S-59: the default model stores its
// effort — PUT /api/v1/providers/default-model accepts the pair AND the
// level, the GET readback carries it, and the persisted config.json's
// agents.defaults.default_model contains the reasoning_effort key.
//
// Traces to: effort-levels-spec-r10.md S-59
//
// RED today: gen.DefaultModelUpdateRequest has no reasoning_effort and the
// PUT decodes with DisallowUnknownFields, so the request is refused with 400
// and no effort exists anywhere.
func TestT_GW_DefaultModelEffortStored(t *testing.T) {
	rec := &recordingLLMProvider{}
	rows := []*config.ModelConfig{
		{Name: "p1", Provider: "p1", Model: "m1", APIBase: "https://a.example/v1", Protocol: "openai-compatible"},
		{Name: "p2", Provider: "p2", Model: "m2", APIBase: "https://b.example/v1", Protocol: "openai-compatible"},
	}
	api, tmpDir, _ := newDefaultModelAPI(t,
		config.DefaultModel{Provider: "p1", Model: "m1"}, rows, rec)

	w := doDefaultModel(api, http.MethodPut,
		`{"provider":"p2","model":"m2","reasoning_effort":"high"}`)
	require.Equal(t, http.StatusOK, w.Code,
		"RED S-59: the default-model PUT must accept pair+effort together; today the strict decode "+
			"refuses the unknown key (got %d, body=%s)", w.Code, w.Body.String())

	g := doDefaultModel(api, http.MethodGet, "")
	require.Equal(t, http.StatusOK, g.Code, "body=%s", g.Body.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(g.Body.Bytes(), &got))
	require.Equal(t, "high", got["reasoning_effort"],
		"RED S-59: the default-model GET must carry reasoning_effort (got %v)", got["reasoning_effort"])

	raw, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	var cfgRaw map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfgRaw))
	agentsRaw, _ := cfgRaw["agents"].(map[string]any)
	require.NotNil(t, agentsRaw)
	defaults, _ := agentsRaw["defaults"].(map[string]any)
	require.NotNil(t, defaults)
	dm, _ := defaults["default_model"].(map[string]any)
	require.NotNil(t, dm, "the default model pair must persist")
	require.Equal(t, "high", dm["reasoning_effort"],
		"RED S-59: config.json agents.defaults.default_model must carry the reasoning_effort key (got %v)", dm)
}

// --- S-62 ---

// TestT_GW_ProviderDeleteNewDefaultCarriesEffort is S-62: removing the
// default-backing provider with a replacement stores the replacement's effort
// through new_default.
//
// Traces to: effort-levels-spec-r10.md S-62
//
// RED today: ProviderDeleteRequest.new_default decodes strictly and
// DefaultModelUpdateRequest has no reasoning_effort, so the request is
// refused with 400 and the default never changes.
func TestT_GW_ProviderDeleteNewDefaultCarriesEffort(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "openrouter", Model: "z-ai/glm-5"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
	)
	api, tmpDir, _ := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test"))

	w := doProviderDelete(t, api, "openrouter",
		`{"new_default":{"provider":"anthropic","model":"claude-sonnet-4.6","reasoning_effort":"low"}}`, nil, true)
	require.Equal(t, http.StatusOK, w.Code,
		"RED S-62: provider removal with a replacement+effort must succeed; today the strict decode "+
			"refuses the effort key (got %d, body=%s)", w.Code, w.Body.String())

	dm := egwDiskDefaultModel(t, tmpDir)
	require.NotNil(t, dm, "the replacement must become the default model")
	require.Equal(t, "low", dm["reasoning_effort"],
		"RED S-62: agents.defaults.default_model must carry the replacement's effort (got %v)", dm)
}

// egwDiskDefaultModel reads agents.defaults.default_model from config.json.
func egwDiskDefaultModel(t *testing.T, tmpDir string) map[string]any {
	t.Helper()
	cfgRaw := diskConfig(t, tmpDir)
	agentsRaw, _ := cfgRaw["agents"].(map[string]any)
	if agentsRaw == nil {
		return nil
	}
	defaults, _ := agentsRaw["defaults"].(map[string]any)
	if defaults == nil {
		return nil
	}
	dm, _ := defaults["default_model"].(map[string]any)
	return dm
}

// --- S-61 ---

// TestT_GW_OnboardingEffortStoredOnVariants is S-61's positive rows: the real
// POST /onboarding/complete accepts provider.reasoning_effort on BOTH
// declared variants (api_key and sign_in) and stores the pair and the effort
// TOGETHER in agents.defaults.default_model in the completion's single
// configuration write — never on the shared provider row.
//
// Traces to: effort-levels-spec-r10.md S-61
//
// RED today: both variants decode strictly (decodeOnboardingCompleteBody,
// DisallowUnknownFields) and neither generated variant type has
// reasoning_effort, so each completion is refused with 400 and nothing is
// stored.
func TestT_GW_OnboardingEffortStoredOnVariants(t *testing.T) {
	t.Run("api_key variant", func(t *testing.T) {
		withEdition(t, config.EditionHosted)
		api, tmpDir := newAuthMethodOnboardingAPI(t)
		upstream := startFakeProviderUpstream(t)
		body := withProviderEndpoint(
			`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-red-key","model":"gpt-4o","reasoning_effort":"high"},`+
				`"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`,
			upstream,
		)

		w := postOnboardingComplete(api, body)
		require.Equal(t, http.StatusOK, w.Code,
			"RED S-61: the api_key variant must accept reasoning_effort on the declared variant; "+
				"today the strict variant decode refuses it (got %d, body=%s)", w.Code, w.Body.String())

		cfgRaw, _ := readOnboardedConfig(t, tmpDir)
		agentsRaw, _ := cfgRaw["agents"].(map[string]any)
		require.NotNil(t, agentsRaw)
		defaults, _ := agentsRaw["defaults"].(map[string]any)
		require.NotNil(t, defaults)
		dm, _ := defaults["default_model"].(map[string]any)
		require.NotNil(t, dm, "the completion must persist the default model")
		require.Equal(t, "high", dm["reasoning_effort"],
			"RED S-61: agents.defaults.default_model.reasoning_effort must be stored (got %v)", dm)

		// The effort is NOT persisted on the shared provider row.
		row := providerRow(t, cfgRaw, "openai", "gpt-4o")
		_, hasEffort := row["reasoning_effort"]
		require.False(t, hasEffort,
			"the effort must not be persisted on the provider row: %v", row)
	})

	t.Run("sign_in variant", func(t *testing.T) {
		withEdition(t, config.EditionHosted)
		api, tmpDir := newAuthMethodOnboardingAPI(t)
		body := `{"provider":{"auth_method":"sign_in","id":"openai-chatgpt","model":"gpt-5","reasoning_effort":"high"},` +
			`"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`

		w := postOnboardingComplete(api, body)
		require.Equal(t, http.StatusOK, w.Code,
			"RED S-61: the sign_in variant must accept reasoning_effort too (r9: effort belongs on BOTH "+
				"declared variants); today the strict variant decode refuses it (got %d, body=%s)",
			w.Code, w.Body.String())

		dm := egwDiskDefaultModel(t, tmpDir)
		require.NotNil(t, dm, "the completion must persist the default model")
		require.Equal(t, "high", dm["reasoning_effort"],
			"RED S-61: the sign-in completion must store the effort with the pair (got %v)", dm)
	})

	t.Run("no reasoning_effort key stores Provider default", func(t *testing.T) {
		// PIN: without the key the stored effort is Provider default (absent
		// key) — today's behaviour the feature must keep.
		withEdition(t, config.EditionHosted)
		api, tmpDir := newAuthMethodOnboardingAPI(t)
		upstream := startFakeProviderUpstream(t)
		body := withProviderEndpoint(
			`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-red-key","model":"gpt-4o"},`+
				`"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`,
			upstream,
		)
		w := postOnboardingComplete(api, body)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		dm := egwDiskDefaultModel(t, tmpDir)
		require.NotNil(t, dm)
		_, hasEffort := dm["reasoning_effort"]
		require.False(t, hasEffort,
			"PIN S-61: without the key the stored effort is Provider default — the key must stay absent: %v", dm)
	})
}

// TestT_GW_OnboardingInvalidPlacementsRefused is S-61's refusal contract
// (r9): the strict-decode refusals cover exactly the invalid placements, plus
// S-94's onboarding rows for null / non-string / over-length effort — each is
// HTTP 400, names the offending field, and nothing is persisted.
//
// Traces to: effort-levels-spec-r10.md S-61, S-94
//
// PIN today: every row below already refuses with 400 and persists nothing,
// because the wrapper and both variants decode with DisallowUnknownFields.
// These rows exist to keep those refusals intact once reasoning_effort IS
// declared on the variants (r9: refusing validly placed effort is itself a
// defect).
func TestT_GW_OnboardingInvalidPlacementsRefused(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		expectField string // the offending field the refusal must name
	}{
		{
			"effort at the wrapper top level (outside either variant)",
			`{"reasoning_effort":"high","provider":{"auth_method":"api_key","id":"openai","api_key":"sk-x","model":"gpt-4o"},"preferences":{"name":"D","tone":"direct","detail":"brief"}}`,
			"reasoning_effort",
		},
		{
			"api_key inside the sign-in variant",
			`{"provider":{"auth_method":"sign_in","id":"openai-chatgpt","model":"gpt-5","api_key":"sk-x"},"preferences":{"name":"D","tone":"direct","detail":"brief"}}`,
			"api_key",
		},
		{
			"device_code inside the api_key variant",
			`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-x","model":"gpt-4o","device_code":"dc"},"preferences":{"name":"D","tone":"direct","detail":"brief"}}`,
			"device_code",
		},
		{
			"null effort on the api_key variant (S-94)",
			`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-x","model":"gpt-4o","reasoning_effort":null},"preferences":{"name":"D","tone":"direct","detail":"brief"}}`,
			"reasoning_effort",
		},
		{
			"numeric effort on the api_key variant (S-94)",
			`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-x","model":"gpt-4o","reasoning_effort":7},"preferences":{"name":"D","tone":"direct","detail":"brief"}}`,
			"reasoning_effort",
		},
		{
			"33-character effort on the api_key variant (S-94)",
			`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-x","model":"gpt-4o","reasoning_effort":"` + strings.Repeat("x", 33) + `"},"preferences":{"name":"D","tone":"direct","detail":"brief"}}`,
			"reasoning_effort",
		},
	}
	for _, validation := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("validate=%v/%s", validation, tc.name), func(t *testing.T) {
				withEdition(t, config.EditionHosted)
				api, tmpDir := newAuthMethodOnboardingAPI(t)
				api.agentLoop.GetConfig().Gateway.ValidateInbound = validation

				w := postOnboardingComplete(api, tc.body)
				require.Equal(t, http.StatusBadRequest, w.Code,
					"the invalid placement must be refused with HTTP 400 (got %d, body=%s)", w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), tc.expectField,
					"the refusal must name the offending field; body=%s", w.Body.String())

				// Nothing persisted: no default model, no provider row, no admin
				// change — the refusal happens before the transaction commits.
				dm := egwDiskDefaultModel(t, tmpDir)
				require.Nil(t, dm, "nothing may persist from a refused completion (got %v)", dm)
				cfgRaw := diskConfig(t, tmpDir)
				rows, _ := cfgRaw["providers"].([]any)
				require.Empty(t, rows, "no provider row may persist from a refused completion: %v", rows)
				if !validation && tc.expectField == "api_key" {
					var response map[string]any
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
					assert.Equal(t, `field not allowed on provider auth_method "sign_in": json: unknown field "api_key" — see the OnboardingProviderSignIn schema`, response["error"])
				}
			})
		}
	}
}

// --- S-60 memory fixture ---

// egwNewMemoryAPI builds the Memory-settings harness the existing
// rest_memory_settings_test.go uses: a v1 config.json on disk (so the PUT's
// read-modify-write cycle preserves agents.defaults) plus a persisted
// test-agent entity. validateInbound mirrors gateway.validate_inbound.
func egwNewMemoryAPI(t *testing.T, validateInbound bool) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	cfgJSON := `{"version":1,"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096}}}`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, ValidateInbound: validateInbound},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "test-agent", Name: "Test Agent", Type: config.AgentTypeCustom}},
		},
	}
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return &restAPI{agentLoop: al, homePath: tmpDir}
}

// egwMemoryPut sends PUT /api/v1/settings/memory with a raw JSON body.
func egwMemoryPut(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/memory", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleMemorySettings(w, r)
	return w
}

// egwMemoryGetRaw GETs /api/v1/settings/memory and returns the raw JSON map
// (JSON-level: keys the generated MemorySettings type lacks are still visible).
func egwMemoryGetRaw(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/settings/memory", nil)
	api.HandleMemorySettings(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET memory settings must answer 200; body=%s", w.Body.String())
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m), "body=%s", w.Body.String())
	return m
}

// --- S-60 ---

// TestT_GW_RecapEffortAndProviderStored is S-60's REST storage half: the
// Memory settings write accepts recap_provider and recap_reasoning_effort
// (default Off, here set high) and a recap fallback's reasoning_effort, and
// all of them survive a read back through GET and into config.json.
//
// Traces to: effort-levels-spec-r10.md S-60
//
// RED today: gen.MemorySettings and gen.FallbackModel have no such fields and
// the PUT decodes non-strict, so every effort/provider key is silently
// dropped; the #1291 recap_provider key is dropped the same way.
func TestT_GW_RecapEffortAndProviderStored(t *testing.T) {
	api := egwNewMemoryAPI(t, false)

	body := `{"recap_model":"deepseek/deepseek-v4.1-flash","recap_provider":"openrouter",` +
		`"recap_reasoning_effort":"high",` +
		`"recap_fallback_models":[{"provider":"openrouter","model":"minimax/minimax-m3","reasoning_effort":"off"}]}`
	w := egwMemoryPut(t, api, body)
	require.Equal(t, http.StatusOK, w.Code,
		"the Memory settings PUT must accept the recap slots; body=%s", w.Body.String())

	got := egwMemoryGetRaw(t, api)
	require.Equal(t, "high", got["recap_reasoning_effort"],
		"RED S-60: GET must carry recap_reasoning_effort (got %v)", got["recap_reasoning_effort"])
	require.Equal(t, "openrouter", got["recap_provider"],
		"RED S-60: the recap model must be stored with recap_provider (#1291) (got %v)", got["recap_provider"])

	fbs, _ := got["recap_fallback_models"].([]any)
	require.NotEmpty(t, fbs, "recap_fallback_models must round-trip")
	fb0, _ := fbs[0].(map[string]any)
	require.Equal(t, "off", fb0["reasoning_effort"],
		"RED S-60: a recap fallback carries its own reasoning_effort through FallbackModel (got %v)", fb0)

	// The stored config carries the keys too.
	defaults := egwDefaultsOf(t, diskConfig(t, api.homePath))
	require.Equal(t, "high", defaults["recap_reasoning_effort"],
		"RED S-60: config.json agents.defaults must carry recap_reasoning_effort (got %v)", defaults["recap_reasoning_effort"])
}

// egwDefaultsOf extracts agents.defaults from a raw config map.
func egwDefaultsOf(t *testing.T, cfgRaw map[string]any) map[string]any {
	t.Helper()
	agentsRaw, _ := cfgRaw["agents"].(map[string]any)
	require.NotNil(t, agentsRaw, "config.json must have agents")
	defaults, _ := agentsRaw["defaults"].(map[string]any)
	require.NotNil(t, defaults, "config.json must have agents.defaults")
	return defaults
}

// --- S-79 / S-80 / S-81 / S-94 (memory half) ---

// egwRecapLimitRow is one ranged recap-limit key with the spec's bounds
// (effort-levels-spec-r10.md, contract MemorySettings row + L2 table).
type egwRecapLimitRow struct {
	key        string
	min        int
	max        int
	setValue   int // the S-80 example value the stored-write row uses
	shippedDef int // the shipped default (S-79)
}

// egwRecapLimitRows lists the TEN new recap-limit keys (FD-20/FD-23) with
// their spec bounds, S-80 example values and shipped defaults. The two
// pre-existing ranged keys (idle_timeout_minutes 1-1440,
// bootstrap_recap_max_per_minute 1-60) are exercised in the bounds rows below.
func egwRecapLimitRows() []egwRecapLimitRow {
	return []egwRecapLimitRow{
		{"recap_max_output_tokens", 512, 16384, 4096, 2048},
		{"recap_input_tokens", 500, 32000, 500, 2000},
		{"recap_summary_words", 50, 500, 100, 150},
		{"recap_list_items", 1, 10, 3, 5},
		{"recap_fallback_context_chars", 200, 8000, 300, 1500},
		{"recap_timeout_seconds", 15, 300, 15, 60},
		{"recap_stream_retries", 0, 5, 0, 2},
		{"recap_retry_pause_ms", 100, 5000, 200, 500},
		{"recap_chars_per_token", 2, 8, 3, 4},
		{"bootstrap_recap_min_spacing_seconds", 1, 60, 20, 1},
	}
}

// TestT_GW_RecapLimitsStoredAndBounded covers:
//   - S-80: each recap limit, set through Settings/Memory REST, is stored and
//     read back (and recap_stream_retries 0 is stored as 0, never the
//     default 2);
//   - S-79: with none of the ten keys set, GET reports the shipped defaults;
//   - S-81: min/max are saved; min-1 and max+1 are refused with HTTP 400,
//     body "<key> must be between <min> and <max>", and nothing is saved;
//   - S-94 (memory half): recap_reasoning_effort null is refused.
//
// Traces to: effort-levels-spec-r10.md S-79, S-80, S-81, S-94
//
// No LLM is involved: the endpoint only validates and persists config.
//
// RED today: the ten keys do not exist on the generated request/response
// types and the PUT decodes non-strict — every key is silently dropped
// (storage rows fail), no defaults are reported (S-79 fails), and the
// out-of-range refusals do not exist (bounds rows fail with 200).
func TestT_GW_RecapLimitsStoredAndBounded(t *testing.T) {
	rows := egwRecapLimitRows()

	t.Run("S80_each_set_limit_is_stored_and_read_back", func(t *testing.T) {
		api := egwNewMemoryAPI(t, false)
		for _, row := range rows {
			body, err := json.Marshal(map[string]any{row.key: row.setValue})
			require.NoError(t, err)
			w := egwMemoryPut(t, api, string(body))
			require.Equal(t, http.StatusOK, w.Code,
				"%s=%d: the PUT must be accepted; body=%s", row.key, row.setValue, w.Body.String())
			got := egwMemoryGetRaw(t, api)
			require.EqualValues(t, row.setValue, got[row.key],
				"RED S-80: %s=%d must be stored and read back (got %v)", row.key, row.setValue, got[row.key])
		}

		// The stored config carries the keys under agents.defaults.
		defaults := egwDefaultsOf(t, diskConfig(t, api.homePath))
		for _, row := range rows {
			require.EqualValues(t, row.setValue, defaults[row.key],
				"RED S-80: config.json agents.defaults.%s must carry the set value (got %v)",
				row.key, defaults[row.key])
		}
	})

	t.Run("S80_recap_stream_retries_0_is_stored_as_0", func(t *testing.T) {
		api := egwNewMemoryAPI(t, false)
		w := egwMemoryPut(t, api, `{"recap_stream_retries":0}`)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		got := egwMemoryGetRaw(t, api)
		v, present := got["recap_stream_retries"]
		require.True(t, present, "RED S-80: recap_stream_retries must be present")
		require.EqualValues(t, 0, v,
			"RED S-80: recap_stream_retries 0 must be stored and used as 0, never replaced by the default 2 (got %v)", v)
	})

	t.Run("S79_defaults_when_no_key_is_set", func(t *testing.T) {
		api := egwNewMemoryAPI(t, false)
		got := egwMemoryGetRaw(t, api)
		for _, row := range rows {
			require.EqualValues(t, row.shippedDef, got[row.key],
				"RED S-79: %s must default to %d in the GET response (got %v)",
				row.key, row.shippedDef, got[row.key])
		}
	})

	t.Run("S81_min_and_max_saved_min_minus_1_and_max_plus_1_refused", func(t *testing.T) {
		api := egwNewMemoryAPI(t, false)
		twelve := append(rows[:len(rows):len(rows)],
			egwRecapLimitRow{"idle_timeout_minutes", 1, 1440, 0, 0},
			egwRecapLimitRow{"bootstrap_recap_max_per_minute", 1, 60, 0, 0},
		)
		for _, row := range twelve {
			// min saved.
			body, _ := json.Marshal(map[string]any{row.key: row.min})
			w := egwMemoryPut(t, api, string(body))
			require.Equal(t, http.StatusOK, w.Code, "%s min: body=%s", row.key, w.Body.String())
			require.EqualValues(t, row.min, egwMemoryGetRaw(t, api)[row.key],
				"RED S-81: %s min=%d must be saved (got %v)", row.key, row.min, egwMemoryGetRaw(t, api)[row.key])

			// max saved.
			body, _ = json.Marshal(map[string]any{row.key: row.max})
			w = egwMemoryPut(t, api, string(body))
			require.Equal(t, http.StatusOK, w.Code, "%s max: body=%s", row.key, w.Body.String())
			require.EqualValues(t, row.max, egwMemoryGetRaw(t, api)[row.key],
				"RED S-81: %s max=%d must be saved (got %v)", row.key, row.max, egwMemoryGetRaw(t, api)[row.key])

			// min-1 refused, nothing saved.
			body, _ = json.Marshal(map[string]any{row.key: row.min - 1})
			w = egwMemoryPut(t, api, string(body))
			require.Equal(t, http.StatusBadRequest, w.Code,
				"RED S-81: %s=%d (min-1) must be refused with HTTP 400; today it is dropped with 200 (got %d, body=%s)",
				row.key, row.min-1, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), fmt.Sprintf("%s must be between %d and %d", row.key, row.min, row.max),
				"with gateway.validate_inbound off the refusal body is the handler's exact sentence; body=%s", w.Body.String())
			require.EqualValues(t, row.max, egwMemoryGetRaw(t, api)[row.key],
				"nothing may be saved from the refused min-1 write")

			// max+1 refused, nothing saved.
			body, _ = json.Marshal(map[string]any{row.key: row.max + 1})
			w = egwMemoryPut(t, api, string(body))
			require.Equal(t, http.StatusBadRequest, w.Code,
				"RED S-81: %s=%d (max+1) must be refused with HTTP 400; today it is dropped with 200 (got %d, body=%s)",
				row.key, row.max+1, w.Code, w.Body.String())
			require.EqualValues(t, row.max, egwMemoryGetRaw(t, api)[row.key],
				"nothing may be saved from the refused max+1 write")
		}
	})

	t.Run("S81_validation_on_refuses_out_of_range_with_key_named", func(t *testing.T) {
		// With gateway.validate_inbound ON the refusal is the inbound
		// validator's schema error — the test asserts status, that the key is
		// named and nothing was saved, not the validator's wording (S-81).
		//
		// PREMISE MISMATCH (reported): putMemorySettings validates against the
		// mirror name "UpdateMemorySettingsJSONRequestBody", which the
		// embedded inbound mirror (pkg/gateway/inboundschemas/) does not carry
		// — it only ships MemorySettings.yaml — so validate-on answers 500
		// "inbound schema unavailable" today instead of a 400 naming the key.
		// A missing schema is a loud BLOCKED failure, not a validation RED.
		// Once available, require the specified 400 and unchanged GET default,
		// whether implementation fixes the mirror or uses its existing name.
		api := egwNewMemoryAPI(t, true)
		body, _ := json.Marshal(map[string]any{"recap_summary_words": 501})
		w := egwMemoryPut(t, api, string(body))
		requireEffortMemorySchema(t, w)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"validation on must refuse an out-of-range value with 400 (got %d, body=%s)", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "recap_summary_words",
			"the refusal must name the key; body=%s", w.Body.String())
		got := egwMemoryGetRaw(t, api)
		require.EqualValues(t, 150, got["recap_summary_words"], "refusal preserves the shipped GET default, it does not remove the field")
	})

	t.Run("S94_memory_null_effort_refused_and_nothing_saved", func(t *testing.T) {
		api := egwNewMemoryAPI(t, false)
		// Store a valid value first (the S-60 premise).
		w := egwMemoryPut(t, api, `{"recap_reasoning_effort":"high"}`)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		wNull := egwMemoryPut(t, api, `{"recap_reasoning_effort":null}`)
		require.Equal(t, http.StatusBadRequest, wNull.Code,
			"RED S-94: PUT /settings/memory with recap_reasoning_effort null must be refused with HTTP 400; "+
				"today the unknown key is dropped and the PUT answers 200 (got %d, body=%s)",
			wNull.Code, wNull.Body.String())
		got := egwMemoryGetRaw(t, api)
		require.Equal(t, "high", got["recap_reasoning_effort"],
			"the stored value stays high after the refusal (got %v)", got["recap_reasoning_effort"])
	})
}

// --- S-94 (agent REST half) ---

// TestT_GW_AgentNullEffortRefused is S-94's agent REST rows: PUT agent update
// with reasoning_effort null (and with a null effort inside
// fallback_models[0]) is refused with HTTP 400 and the stored value stays
// high. Omitting the key keeps high; "" clears to Not set.
//
// Traces to: effort-levels-spec-r10.md S-94
//
// Today: the null rows are PIN-by-status — the strict shape check refuses any
// reasoning_effort key (null included) with 400, which matches the required
// status, though the wording is the unknown-field error, not the spec's
// "must be a string; send \"\" to clear it". The "" row is RED: the spec
// requires an empty string to CLEAR the stored effort to Not set, and today
// the same shape check refuses it with 400.
func TestT_GW_AgentNullEffortRefused(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Given: stored primary effort high (hand-seeded disk form).
	stored := egwReadAgentEntity(t, api, "test-agent")
	stored["reasoning_effort"] = "high"
	egwWriteAgentEntity(t, api, "test-agent", stored)

	t.Run("primary null refused 400, stored stays high", func(t *testing.T) {
		w := egwPutAgentField(t, api, `{"reasoning_effort":null}`)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"RED S-94: PUT with reasoning_effort null must be refused with HTTP 400; today the null is "+
				"treated as absence and the PUT answers 200 (got %d, body=%s)", w.Code, w.Body.String())
		var payload map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		require.Equal(t, `reasoning_effort must be a string; send "" to clear it`, payload["error"], "S-94 validation-off refusal must identify null, not an unknown effort field")
		after := egwReadAgentEntity(t, api, "test-agent")
		require.Equal(t, "high", after["reasoning_effort"],
			"the stored value stays high after the refusal (got %v)", after["reasoning_effort"])
	})

	t.Run("fallback_models null effort refused 400", func(t *testing.T) {
		w := egwPutAgentField(t, api,
			`{"fallback_models":[{"provider":"openrouter","model":"m1","reasoning_effort":null}]}`)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"RED S-94: a null effort inside fallback_models[0] must be refused with HTTP 400; today the "+
				"whole fallback row decodes without the key and the PUT answers 200 (got %d, body=%s)",
			w.Code, w.Body.String())
		var payload map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		require.Equal(t, `reasoning_effort must be a string; send "" to clear it`, payload["error"], "S-94 nested null gets the same validation-off refusal")
	})

	t.Run("empty string clears to Not set", func(t *testing.T) {
		w := egwPutAgentField(t, api, `{"reasoning_effort":""}`)
		require.Equal(t, http.StatusOK, w.Code,
			"RED S-94: an empty string must clear the stored effort to Not set; today the strict shape check "+
				"refuses the key outright (got %d, body=%s)", w.Code, w.Body.String())
		after := egwReadAgentEntity(t, api, "test-agent")
		v, present := after["reasoning_effort"]
		require.True(t, !present || v == "",
			"RED S-94: an empty string must clear the stored effort to Not set (absent or empty; got %v, present=%v)", v, present)
	})
}

// --- S-15 / S-75 / D5: chat WS frame metadata ---

// TestT_GW_ChatFrameEffortMetadataBoundaries is D5 rows 6-7 (S-15/S-75):
// metadata.provider and metadata.reasoning_effort are typed strings with
// 64/32-character limits. An over-length or wrongly-typed value is dropped on
// the existing error-frame path whether or not gateway.validate_inbound is
// on, causing no provider call and no conversation-setting change; the
// boundary values themselves are accepted and reach the bus.
//
// Traces to: effort-levels-spec-r10.md D5 rows 6-7, S-15, S-75
//
// RED today: the handler reads only metadata.model_name and never validates
// provider/reasoning_effort, so every refused row below is silently accepted
// and published to the bus (a provider call would follow).
func TestT_GW_ChatFrameEffortMetadataBoundaries(t *testing.T) {
	frameWith := func(provider, effort string, effortJSON string) string {
		meta := `"model_name":"test-model"`
		if provider != "" {
			meta += `,"provider":"` + provider + `"`
		}
		if effort != "" {
			meta += `,"reasoning_effort":` + effortJSON
		}
		return `{"type":"message","content":"hi","agent_id":"mia","metadata":{` + meta + `}}`
	}

	newEnv := func(t *testing.T, validateInbound bool) (*WSHandler, *bus.MessageBus, *httptest.Server) {
		handler, msgBus, al := newTestWSHandler(t)
		t.Cleanup(handler.Wait)
		al.GetConfig().Gateway.ValidateInbound = validateInbound
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		return handler, msgBus, srv
	}

	boundary := func(t *testing.T, validateInbound bool) {
		t.Run("provider_65_chars_refused_on_error_frame_path", func(t *testing.T) {
			handler, msgBus, srv := newEnv(t, validateInbound)
			_ = handler
			conn := dialTestWS(t, srv)
			t.Cleanup(func() { _ = conn.Close() })
			sendWSAuthFrameDevMode(t, conn)

			errs := wu0aSendAndCollect(t, conn, frameWith(strings.Repeat("p", 65), "", ""))
			require.NotEmpty(t, errs,
				"RED D5 row 6: a 65-character metadata.provider must be refused on the error-frame path "+
					"independently of gateway.validate_inbound (validation on=%v); today it is silently accepted (errs=%v)",
				validateInbound, errs)
			// No provider call and no conversation-setting change: nothing was
			// published to the bus.
			select {
			case m := <-msgBus.InboundChan():
				t.Fatalf("a refused frame must cause no provider call: an inbound message was published (%+v)", m)
			default:
			}
		})

		t.Run("effort_33_chars_refused_on_error_frame_path", func(t *testing.T) {
			_, msgBus, srv := newEnv(t, validateInbound)
			conn := dialTestWS(t, srv)
			t.Cleanup(func() { _ = conn.Close() })
			sendWSAuthFrameDevMode(t, conn)

			long := strings.Repeat("e", 33)
			errs := wu0aSendAndCollect(t, conn, frameWith("", long, `"`+long+`"`))
			require.NotEmpty(t, errs,
				"RED D5 row 7: a 33-character metadata.reasoning_effort must be refused on the error-frame path "+
					"independently of gateway.validate_inbound (validation on=%v); today it is silently accepted (errs=%v)",
				validateInbound, errs)
			select {
			case m := <-msgBus.InboundChan():
				t.Fatalf("a refused frame must cause no provider call: an inbound message was published (%+v)", m)
			default:
			}
		})

		t.Run("non_string_effort_refused", func(t *testing.T) {
			_, msgBus, srv := newEnv(t, validateInbound)
			conn := dialTestWS(t, srv)
			t.Cleanup(func() { _ = conn.Close() })
			sendWSAuthFrameDevMode(t, conn)

			errs := wu0aSendAndCollect(t, conn, frameWith("", "7", `7`))
			require.NotEmpty(t, errs,
				"RED: a non-string metadata.reasoning_effort must be refused on the error-frame path "+
					"(validation on=%v); today it is silently accepted (errs=%v)", validateInbound, errs)
			select {
			case m := <-msgBus.InboundChan():
				t.Fatalf("a refused frame must cause no provider call: an inbound message was published (%+v)", m)
			default:
			}
		})

		t.Run("boundary_values_accepted_and_published", func(t *testing.T) {
			// PIN: 64-character provider and 32-character effort are ACCEPTED
			// (D5 rows 6-7 lower bounds) and the turn is dispatched normally.
			_, msgBus, srv := newEnv(t, validateInbound)
			conn := dialTestWS(t, srv)
			t.Cleanup(func() { _ = conn.Close() })
			sendWSAuthFrameDevMode(t, conn)

			errs := wu0aSendAndCollect(t, conn,
				frameWith(strings.Repeat("p", 64), strings.Repeat("e", 32), `"`+strings.Repeat("e", 32)+`"`))
			require.Empty(t, errs,
				"PIN D5 rows 6-7: the boundary values themselves must be accepted (errs=%v)", errs)
			awaitInboundMessage(t, msgBus, "boundary-values accepted frame must reach the bus")
		})
	}

	t.Run("validation_off", func(t *testing.T) { boundary(t, false) })
	t.Run("validation_on", func(t *testing.T) { boundary(t, true) })
}

// --- S-15 / S-75: the turn-completion frame ---

// TestT_GW_TurnCompletionFrameCarriesEffortMetadata is the S-15/S-75 live
// turn half: the done frame the gateway writes to the WebSocket carries the
// turn's effort record (stats.effort with provider/model/chosen/sent/reason)
// and the display-only turn reasoning sum (stats.reasoning_tokens).
//
// Traces to: effort-levels-spec-r10.md S-15, S-75, contract DoneStats row
//
// Method: capture what the gateway actually writes to the WS conn — bind a
// real wsConn through the attach path, finalize a real streamer, and decode
// the done frame bytes into map[string]any (JSON-level: the generated
// DoneStats type has neither field today).
//
// RED today: stats carries only tokens/cost/duration/session_tokens_total —
// no effort, no reasoning_tokens.
func TestT_GW_TurnCompletionFrameCarriesEffortMetadata(t *testing.T) {
	done := egwRealEffortDone(t)
	stats, ok := done["stats"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"chosen": "high", "sent": "high", "provider": "openrouter", "model": "m-levels"}, stats["effort"], "actual configured High belongs to the actual serving pair")
	assert.Equal(t, float64(120), stats["reasoning_tokens"], "real usage reports 120, not absence or fabricated zero")
}

// --- S-95 / S-21 / T-R2: history transports ---

// egwT7HistoryEnv builds one agent loop with BOTH the REST api and the WS
// handler bound to it, mirroring newTestWSHandler's config shape.
type egwT7HistoryEnv struct {
	api     *restAPI
	handler *WSHandler
	home    string
}

func egwNewHistoryEnv(t *testing.T) *egwT7HistoryEnv {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()
	// Pin OMNIPUS_HOME to THIS temp dir before mustAgentLoop, and point the
	// workspace at home/workspace: the shared session store opens at
	// filepath.Dir(AgentHomeBasePath()) (pkg/agent sessionsHomeFor), so with
	// the workspace at home/workspace the transcript partitions land under
	// home/sessions/ — the tree egwPatchEntryInPartition walks. With the
	// workspace at home itself the archive lands OUTSIDE it.
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(home, "workspace"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: home}},
		},
	}
	seedAgentEntities(t, home, cfg.Agents.List)
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &egwT7HistoryEnv{
		api:     &restAPI{agentLoop: al, homePath: home},
		handler: newWSHandler(msgBus, al, ""),
		home:    home,
	}
}

// TestT_GW_HistoryTransportsCarryEffortRecord is S-95/T-R2's backend half
// (with S-21's reload rows): both history transports — the WebSocket attach's
// ReplayMessageFrame and the REST messages endpoint's Message — must carry the
// persisted effort record and the turn reasoning snapshot, with identical
// values, and never a per-call reasoning_tokens member.
//
// Traces to: effort-levels-spec-r10.md S-95, S-21, T-R2 (R3-MAJ-002)
//
// CHECK-01: real archive bytes contain metadata before either reader opens.
// The saved snapshot is not reconstructed from the selected page.
func TestT_GW_HistoryTransportsCarryEffortRecord(t *testing.T) {
	env := egwNewHistoryEnv(t)
	sid := egwSeedHistoryMetadata(t, env)
	effortRecord := map[string]any{"chosen": "medium", "sent": "low", "reason": "mapped", "provider": "PB", "model": "B"}
	meta := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: sid}}
	t.Run("ws_attach_delivers_entries", func(t *testing.T) {
		wc := &wsConn{sendCh: make(chan []byte, 128), doneCh: make(chan struct{})}
		env.handler.mu.Lock()
		env.handler.sessions["chat-egw-hist"] = wc
		env.handler.mu.Unlock()
		env.handler.handleAttachSession(context.Background(), "chat-egw-hist", sid, nil, wc)
		got := map[string]map[string]any{}
		deadline := time.After(10 * time.Second)
		for {
			select {
			case raw := <-wc.sendCh:
				var f map[string]any
				require.NoError(t, json.Unmarshal(raw, &f))
				if f["type"] == "replay_message" {
					if id, ok := f["id"].(string); ok {
						got[id] = f
					}
				}
				if f["type"] == "done" {
					egwAssertHistoryMetadata(t, got)
					return
				}
			case <-deadline:
				t.Fatal("replay did not finish")
			}
		}
	})

	t.Run("replay_frame_contract_accepts_effort_fields", func(t *testing.T) {
		// The wire form the feature must emit, validated against the SAME
		// component schema the replay projection is held to
		// (ReplayMessageFrame.yaml, additionalProperties: false — an unknown
		// field on the wire is a contract violation).
		frame := map[string]any{
			"type": "replay_message", "session_id": meta.ID,
			"id": "entry-egw-a", "role": "assistant", "agent_id": "mia",
			"content": "answer with effort", "model": "B", "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"effort":                effortRecord,
			"turn_reasoning_tokens": 120,
		}
		raw, err := json.Marshal(frame)
		require.NoError(t, err)

		schema := loadReplayMessageFrameSchema(t)
		var inst any
		require.NoError(t, json.Unmarshal(raw, &inst))
		require.NoError(t, schema.Validate(inst),
			"RED S-95 (R3-MAJ-002): ReplayMessageFrame.yaml must accept effort and turn_reasoning_tokens — "+
				"today the closed schema refuses both, so the feature's wire form cannot be delivered")

		// Control: the same frame without the new fields stays valid.
		delete(frame, "effort")
		delete(frame, "turn_reasoning_tokens")
		raw, err = json.Marshal(frame)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &inst))
		require.NoError(t, schema.Validate(inst), "control: the bare frame form must stay schema-valid")
	})

	t.Run("rest_messages_delivers_entries", func(t *testing.T) {
		w := pagingRequest(env.api, sid, "limit=20&min_rows=1")
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		var page struct {
			Entries    []map[string]any `json:"entries"`
			NextBefore *string          `json:"next_before"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
		require.NotNil(t, page.NextBefore, "real latest-page cut must exclude earlier contributors")
		byID := map[string]map[string]any{}
		for _, entry := range page.Entries {
			id, _ := entry["id"].(string)
			byID[id] = entry
		}
		egwAssertHistoryMetadata(t, byID)
		// Repeat the exact persisted snapshot through a refresh, not an accumulated
		// UI sum. Both reads use the same canonical source.
		refresh := pagingRequest(env.api, sid, "limit=20&min_rows=1")
		require.Equal(t, w.Body.String(), refresh.Body.String())
	})

	t.Run("rest_message_contract_accepts_effort_fields", func(t *testing.T) {
		// The wire form the feature must serve, validated through the
		// gateway's own Message validator — the exact check historyWireMessage
		// applies before serving an entry, whose failure degrades the answer
		// to "This saved message could not be displayed." (S-95: the embedded
		// Message schema copy must accept both fields).
		message := map[string]any{
			"id": "entry-egw-a", "role": "assistant", "agent_id": "mia",
			"content": "answer with effort", "model": "B", "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"effort":                effortRecord,
			"turn_reasoning_tokens": 120,
		}
		raw, err := json.Marshal(message)
		require.NoError(t, err)
		msg, serverErr := validateBodyAgainstSchema("Message", raw)
		require.False(t, serverErr, "the Message schema itself must be available")
		require.Empty(t, msg,
			"RED S-95 (R3-MAJ-002): Message.yaml must accept effort and turn_reasoning_tokens — today the "+
				"closed schema refuses both, and a generated-field mismatch would degrade every answer into "+
				"the placeholder (validation message: %s)", msg)

		// Control: the same message without the new fields stays valid.
		delete(message, "effort")
		delete(message, "turn_reasoning_tokens")
		raw, err = json.Marshal(message)
		require.NoError(t, err)
		msg, _ = validateBodyAgainstSchema("Message", raw)
		require.Empty(t, msg, "control: the bare message form must stay schema-valid (got %s)", msg)
	})
}

// --- contract anchors ---

// TestT_GW_ContractSchemasDeclareEffortKeys pins the contract-first step
// (Constraint #8, 5-step process step 1) for the effort feature's frame and
// message schemas: DoneStats declares effort + reasoning_tokens;
// ReplayMessageFrame and Message declare effort + turn_reasoning_tokens.
//
// Traces to: effort-levels-spec-r10.md contract L1 rows (DoneStats,
// ReplayMessageFrame, REST Message), S-15, S-75, S-95
//
// RED today: none of the four keys exists in the contract schemas yet.
func TestT_GW_ContractSchemasDeclareEffortKeys(t *testing.T) {
	thisFile := gatewayTestCallerFile(t)
	schemasDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "contracts", "components", "schemas")

	readProps := func(name string) map[string]any {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(schemasDir, name))
		require.NoError(t, err, "%s must be readable", name)
		var v any
		require.NoError(t, yaml.Unmarshal(data, &v))
		doc, ok := jsonifyYAML(v).(map[string]any)
		require.True(t, ok, "%s must decode to a mapping", name)
		props, ok := doc["properties"].(map[string]any)
		require.True(t, ok, "%s must declare properties", name)
		return props
	}

	doneStats := readProps("DoneStats.yaml")
	require.Contains(t, doneStats, "effort",
		"RED: DoneStats.yaml must declare optional effort (the EffortRecord)")
	require.Contains(t, doneStats, "reasoning_tokens",
		"RED: DoneStats.yaml must declare reasoning_tokens (the display-only turn sum)")

	replay := readProps("ReplayMessageFrame.yaml")
	require.Contains(t, replay, "effort",
		"RED: ReplayMessageFrame.yaml must declare effort (per-entry history)")
	require.Contains(t, replay, "turn_reasoning_tokens",
		"RED: ReplayMessageFrame.yaml must declare turn_reasoning_tokens")

	message := readProps("Message.yaml")
	require.Contains(t, message, "effort",
		"RED: Message.yaml must declare effort (both history transports, R3-MAJ-002)")
	require.Contains(t, message, "turn_reasoning_tokens",
		"RED: Message.yaml must declare turn_reasoning_tokens")
}
