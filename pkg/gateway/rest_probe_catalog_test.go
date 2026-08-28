// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// ADR-067 T067-12 — key validation and the onboarding probe read the registry
// catalog: the probe model comes from the document (FR-022), `id` is a free
// string validated at runtime against it (FR-023, FR-011), a `tier:
// unsupported` row is refused with the catalog's own reason (FR-019), and an
// id the catalog does not carry is admitted only as an operator-named custom
// row carrying both api_base and protocol (FR-035).
//
// Spec: docs/internal/specs/adr-067-registry-catalog-spec.md — US-9.AC4,
// US-10.AC1–AC4, DS-5 rows 12–17, tests T39 and T40.
//
// Both tests run against the EMBEDDED catalog snapshot, deliberately: it is
// the document a fresh install serves, so "zai is probeable and z-ai is not"
// is asserted about the bytes that actually ship rather than about a fixture
// written to agree with the assertion.

package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// probeCatalogMasterKey is a test-only hex master key, distinct from the
// other suites' so the credential stores cannot cross-contaminate.
const probeCatalogMasterKey = "2122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f40"

// ── the FR-022 rule, re-derived from the spec ───────────────────────────────

// expectedProbeCandidates spells out FR-022 / A-20 against the served
// catalog document — "the first `status: active`, tool-calling,
// text-modality models of that provider in document order, at most three" —
// rather than asking the implementation what it would pick. A snapshot
// refresh that reorders a provider's models moves both sides together; a
// change to the RULE moves only one, and the test fails.
func expectedProbeCandidates(t *testing.T, providerID string) []string {
	t.Helper()
	row, ok := providers.CatalogProvider(providerID)
	require.Truef(t, ok, "the embedded snapshot must carry %q", providerID)
	var out []string
	for _, m := range row.Models {
		if m.Status != catalog.StatusActive || !m.ToolCall {
			continue
		}
		text := false
		for _, mod := range m.InputModalities {
			if mod == catalog.ModalityText {
				text = true
				break
			}
		}
		if !text {
			continue
		}
		out = append(out, m.ID)
		if len(out) == 3 {
			break
		}
	}
	return out
}

// expectedRecommendedCandidates spells out ADR-068 FR-030/FR-036 against the
// served document — Recommended-for-chat is `tool_call` AND `context_window`
// >= 128,000 AND `status: active`, ordered `release_date` descending then id
// ascending, at most three — and is the rule the ONBOARDING PROBE uses to
// pick a model when the caller names none (T068-13).
//
// It is deliberately a SECOND oracle rather than a reuse of
// expectedProbeCandidates: the two endpoints answer different questions.
// POST /providers/{id}/test asks "does this key work at all", so FR-022's
// document order is right there. The onboarding probe asks "does the model I
// am about to make my default work", so it exercises the model the picker
// would have recommended — otherwise a green probe would describe a model the
// operator never sees.
func expectedRecommendedCandidates(t *testing.T, providerID string) []string {
	t.Helper()
	row, ok := providers.CatalogProvider(providerID)
	require.Truef(t, ok, "the embedded snapshot must carry %q", providerID)
	eligible := make([]catalog.Model, 0, len(row.Models))
	for _, m := range row.Models {
		if m.Status != catalog.StatusActive || !m.ToolCall || m.ContextWindow < 128000 {
			continue
		}
		eligible = append(eligible, m)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].ReleaseDate != eligible[j].ReleaseDate {
			return eligible[i].ReleaseDate > eligible[j].ReleaseDate
		}
		return eligible[i].ID < eligible[j].ID
	})
	out := make([]string, 0, 3)
	for _, m := range eligible {
		out = append(out, m.ID)
		if len(out) == 3 {
			break
		}
	}
	return out
}

// ── T39: POST /providers/{id}/test probes from the catalog ──────────────────

// probeRecorder is a stand-in provider endpoint that records every request
// path and every probed model, so a test can assert what was NOT called.
type probeRecorder struct { // not-wire-format: test-local recorder
	mu           sync.Mutex
	modelsGETs   int
	probedModels []string
}

func (p *probeRecorder) snapshot() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.probedModels))
	copy(out, p.probedModels)
	return p.modelsGETs, out
}

// startProbeRecorder serves an OpenAI-compatible endpoint whose
// /chat/completions answer for each model is decided by respond.
func startProbeRecorder(
	t *testing.T,
	respond func(model string, w http.ResponseWriter),
) (*probeRecorder, string) {
	t.Helper()
	rec := &probeRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
			rec.mu.Lock()
			rec.modelsGETs++
			rec.mu.Unlock()
			_, _ = w.Write([]byte(`{"data":[{"id":"should-never-be-used"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			var payload struct { // not-wire-format: decodes the probe's own request
				Model string `json:"model"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &payload)
			rec.mu.Lock()
			rec.probedModels = append(rec.probedModels, payload.Model)
			rec.mu.Unlock()
			respond(payload.Model, w)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return rec, srv.URL
}

// newCatalogProbeTestAPI wires a restAPI whose config.json carries ONE
// provider entry for providerID: an api_key_ref into an unlocked credential
// store, the stub's api_base, and — deliberately — NO configured model, so
// the probe model can only have come from the catalog (FR-022).
func newCatalogProbeTestAPI(t *testing.T, providerID, apiBase string) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_MASTER_KEY", probeCatalogMasterKey)
	tmpDir := t.TempDir()

	cfgJSON := map[string]any{
		"version": 1,
		"agents":  map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": []any{map[string]any{
			"model_name":  providerID,
			"provider":    providerID,
			"api_key_ref": providerID + "_API_KEY",
			"api_base":    apiBase,
		}},
	}
	b, err := json.Marshal(cfgJSON)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", b, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
		Providers: []*config.ModelConfig{
			{Name: providerID, Provider: providerID, APIBase: apiBase},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})

	credStore := credentials.NewStore(tmpDir + "/credentials.json")
	require.NoError(t, credentials.Unlock(credStore))
	require.NoError(t, credStore.Set(providerID+"_API_KEY", "sk-test-key"))

	return &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}
}

// TestRestProviders_Test_ProbeFromCatalog is T39 (US-9.AC4, FR-022):
// POST /providers/{id}/test picks its probe model from the registry catalog,
// makes ZERO `GET /models` calls for a catalog provider, and falls through to
// the next catalog candidate on a model_not_found answer at most three times.
func TestRestProviders_Test_ProbeFromCatalog(t *testing.T) {
	const providerID = "zai"
	candidates := expectedProbeCandidates(t, providerID)
	require.Len(t, candidates, 3,
		"the fixture assumption is that %q offers at least three probe candidates", providerID)

	okBody := func(_ string, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}
	notFound := func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model_not_found","code":404}}`))
	}

	t.Run("one POST, zero GET /models, and the catalog's first candidate", func(t *testing.T) {
		rec, base := startProbeRecorder(t, okBody)
		api := newCatalogProbeTestAPI(t, providerID, base)

		w := doProviderTest(t, api, providerID)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var result gen.OperationResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		assert.True(t, result.Success, "an accepted key must report success=true")

		gets, probed := rec.snapshot()
		assert.Zero(t, gets,
			"FR-022: a catalog provider's key check must not pre-fetch GET /models")
		assert.Equal(t, candidates[:1], probed,
			"FR-022: exactly one completion probe, against the catalog's first candidate")
	})

	t.Run("falls through to the third candidate on model_not_found", func(t *testing.T) {
		rec, base := startProbeRecorder(t, func(model string, w http.ResponseWriter) {
			if model == candidates[2] {
				okBody(model, w)
				return
			}
			notFound(w)
		})
		api := newCatalogProbeTestAPI(t, providerID, base)

		w := doProviderTest(t, api, providerID)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var result gen.OperationResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		assert.True(t, result.Success,
			"a good key must survive two stale catalog candidates")
		assert.Nil(t, result.Validation,
			"the third candidate answered 200, so the outcome is valid and carries no validation object")

		gets, probed := rec.snapshot()
		assert.Zero(t, gets, "FR-022: still no GET /models")
		assert.Equal(t, candidates, probed,
			"F-25: the three catalog candidates must be tried in document order")
	})

	t.Run("bounded at three attempts, then Unreachable", func(t *testing.T) {
		rec, base := startProbeRecorder(t, func(_ string, w http.ResponseWriter) {
			notFound(w)
		})
		api := newCatalogProbeTestAPI(t, providerID, base)

		w := doProviderTest(t, api, providerID)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var result gen.OperationResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.NotNil(t, result.Validation,
			"a non-valid outcome must carry the validation object")
		assert.Equal(t, gen.OperationResultValidationOutcome("unreachable"), result.Validation.Outcome,
			"US-9.AC4: four 404s in a row end as Unreachable, never as a false invalid_key")

		gets, probed := rec.snapshot()
		assert.Zero(t, gets, "FR-022: still no GET /models")
		assert.Len(t, probed, 3,
			"F-25: the fall-through is bounded at three attempts, whatever the candidate count")
	})
}

// ── T40: the onboarding probe's free-string id ──────────────────────────────

// postProbe sends one POST /onboarding/probe-provider at the handler.
func postProbe(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider",
		strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleOnboardingProbeProvider(w, withFreshInstallConfig(r))
	return w
}

// TestOnboarding_Probe_FreeStringID is T40 (US-10.AC1–AC4, FR-011, FR-019,
// FR-023, FR-035; DS-5 rows 12–17): `id` is a free string on the wire and the
// catalog is what decides whether it may be probed.
func TestOnboarding_Probe_FreeStringID(t *testing.T) {
	t.Run("row 12: a catalog id the retired enum never carried is probed", func(t *testing.T) {
		rec, base := startProbeRecorder(t, func(_ string, w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
		})
		api := newOnboardingTestAPI(t, t.TempDir(), nil)

		w := postProbe(t, api,
			`{"id":"zai","auth":"api_key","api_key":"sk-k","api_base":"`+base+`"}`)

		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		var resp gen.ProbeProviderResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success, "US-10.AC1: zai must probe like any other catalog id")

		gets, probed := rec.snapshot()
		assert.Zero(t, gets, "FR-022: the probe path must not pre-fetch GET /models")
		// REPLACED ASSERTION (T068-13): this row pinned FR-022's document
		// order, expectedProbeCandidates(t, "zai")[:1]. ADR-068 FR-036 gives
		// the ONBOARDING probe its own rule — the first Recommended-for-chat
		// model — because that probe is about the model the operator is
		// choosing as their default, not merely about the key. FR-022's order
		// still governs POST /providers/{id}/test, which T39 above asserts.
		// The coverage this row carries is unchanged: the model came from the
		// CATALOG (not a hardcoded table), and exactly one completion ran.
		assert.Equal(t, expectedRecommendedCandidates(t, "zai")[:1], probed,
			"FR-036: the probe model is the first Recommended catalog model")

		require.NotNil(t, resp.Models, "US-9.AC1: the model list is served from the catalog")
		row, ok := providers.CatalogProvider("zai")
		require.True(t, ok)
		require.Len(t, *resp.Models, len(row.Models),
			"every catalog model for the row must be offered, with no outbound call")
	})

	t.Run("row 13: a retired spelling is unknown, with no hint at the live one", func(t *testing.T) {
		api := newOnboardingTestAPI(t, t.TempDir(), nil)

		w := postProbe(t, api, `{"id":"z-ai","auth":"api_key","api_key":"sk-k"}`)

		require.Equal(t, http.StatusBadRequest, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, `unknown provider \"z-ai\"`,
			"US-10.AC2: the message names the id the operator typed")
		assert.NotContains(t, body, "zai\"",
			"SC-010: the response must never suggest the canonical id — that is the alias table returning")
		var resp map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "id", resp["field"], "the SPA must be pointed at the id field")
	})

	t.Run("row 14: a custom row needs both api_base and protocol", func(t *testing.T) {
		_, base := startProbeRecorder(t, func(_ string, w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
		})
		api := newOnboardingTestAPI(t, t.TempDir(), nil)

		w := postProbe(t, api, `{"id":"my-proxy","auth":"api_key","api_key":"sk-k",`+
			`"api_base":"`+base+`","protocol":"openai-compatible"}`)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		var resp gen.ProbeProviderResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success, "US-10.AC3: a complete custom row is probed at its own base")

		w = postProbe(t, api,
			`{"id":"my-proxy","auth":"api_key","api_key":"sk-k","api_base":"`+base+`"}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "protocol is required for a custom row")
		var missingProto map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &missingProto))
		assert.Equal(t, "protocol", missingProto["field"],
			"a base URL without a protocol must point the SPA at the protocol field")

		w = postProbe(t, api,
			`{"id":"my-proxy","auth":"api_key","api_key":"sk-k","protocol":"openai-compatible"}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "api_base is required for a custom row")
		assert.Contains(t, w.Body.String(), `unknown provider \"my-proxy\"`)
	})

	t.Run("row 14b: auth api_key without an api_key", func(t *testing.T) {
		api := newOnboardingTestAPI(t, t.TempDir(), nil)

		w := postProbe(t, api, `{"id":"zai","auth":"api_key"}`)

		require.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["error"], "api_key is required")
		assert.Equal(t, "api_key", resp["field"])
	})

	t.Run("row 15: a cloud-IAM row is refused with the catalog's own reason", func(t *testing.T) {
		api := newOnboardingTestAPI(t, t.TempDir(), nil)

		w := postProbe(t, api, `{"id":"amazon-bedrock","auth":"api_key","api_key":"sk-k"}`)

		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "cloud-iam",
			"US-10.AC4/FR-019: the reason is catalog data, not a Go list")
	})

	t.Run("row 16: an empty id", func(t *testing.T) {
		api := newOnboardingTestAPI(t, t.TempDir(), nil)

		w := postProbe(t, api, `{"id":"","auth":"api_key","api_key":"sk-k"}`)

		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "id is required")
	})

	t.Run("row 17: an id past the 64-character bound", func(t *testing.T) {
		longID := strings.Repeat("a", 65)

		// The runtime gate refuses it on its own — an id that long is not in
		// any catalog and carries no custom-row pair.
		api := newOnboardingTestAPI(t, t.TempDir(), nil)
		w := postProbe(t, api, `{"id":"`+longID+`","auth":"api_key","api_key":"sk-k"}`)
		require.Equal(t, http.StatusBadRequest, w.Code)

		// And with validate_inbound on it never reaches the gate: the schema's
		// maxLength: 64 rejects it first (FR-024, DS-5 row 17).
		validating := newTestRestAPIWithValidation(t)
		w = postProbe(t, validating,
			`{"id":"`+longID+`","auth":"api_key","api_key":"sk-k","api_base":"https://example.invalid/v1",`+
				`"protocol":"openai-compatible"}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["error"], "ProbeProviderRequest",
			"the rejection must come from the schema, not from the catalog lookup")
	})
}
