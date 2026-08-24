// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_onboarding_probe_test.go — T068-13 (ADR-068 FR-036 / FR-029 backend
// half, ADR-067 FR-023).
//
// POST /api/v1/onboarding/probe-provider validates its free-string `id`
// against the SERVED CATALOG at runtime — there is no enum and no hand
// pattern (MIN-011). An id the catalog does not carry is admitted only as an
// operator-named custom row, and only when it brings both halves of what it
// takes to reach one (api_base + protocol, FR-035). Everything else is
// `unknown provider "<id>"` on field `id`, with the operator's own input
// echoed back and never a list of accepted ids (CRIT-003) — a list is a map
// of the install for an unauthenticated, pre-onboarding caller.
//
// The other four rules this file pins:
//   - `auth` is required and closed (api_key | sign_in); a catalog row that
//     does not offer the requested method is refused by name (FR-030).
//   - `api_key` is required iff `auth = api_key`, forbidden with sign_in.
//   - any `api_base` passes ssrfChecker.CheckURL BEFORE any outbound call —
//     internal-CIDR base → 422, upstream never contacted (MIN-006).
//   - `model` is used VERBATIM when present (no catalog pre-check: the probe
//     itself is the validation), absent → the provider's first Recommended
//     catalog model; either way the response carries `probed_model` so the
//     SPA can tie the result to the exact pick (FR-029).
package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// probeTestCatalog is the served catalog the probe validates ids against.
//
// `openrouter` carries four Recommended-eligible models so the ordering rule
// (release_date desc, then id asc, capped at 3) and the two disqualifiers
// (no tool calling; window below 128,000) are both exercised.
// `openai-chatgpt` is sign-in only, `zai-coding-plan` is an ordinary api_key
// row with a hyphen in its id.
func probeTestCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	doc := `{
		"schema_version": "2.0.0",
		"version": "v2026.8.23",
		"updated_at": "2026-08-23T06:00:00Z",
		"source": "models.dev@0123456789abcdef0123456789abcdef01234567",
		"default_resize_limits": { "long_edge_px": 7680, "max_bytes": 10485760 },
		"providers": [
			{
				"id": "openrouter", "name": "OpenRouter", "company": "OpenRouter",
				"api": "https://openrouter.ai/api/v1", "protocol": "openai-compatible",
				"env": ["OPENROUTER_API_KEY"], "tier": "standard",
				"auth_methods": ["api_key"],
				"models": [
					{"id": "rec-oldest", "name": "Rec Oldest", "tool_call": true,
					 "context_window": 200000, "max_output_tokens": 8192,
					 "release_date": "2026-01-01",
					 "input_modalities": ["text"], "status": "active"},
					{"id": "rec-newest", "name": "Rec Newest", "tool_call": true,
					 "context_window": 200000, "max_output_tokens": 8192,
					 "release_date": "2026-08-01",
					 "input_modalities": ["text"], "status": "active"},
					{"id": "rec-b-tie", "name": "Rec B Tie", "tool_call": true,
					 "context_window": 128000, "max_output_tokens": 8192,
					 "release_date": "2026-05-01",
					 "input_modalities": ["text"], "status": "active"},
					{"id": "rec-a-tie", "name": "Rec A Tie", "tool_call": true,
					 "context_window": 128000, "max_output_tokens": 8192,
					 "release_date": "2026-05-01",
					 "input_modalities": ["text"], "status": "active"},
					{"id": "no-tools", "name": "No Tools", "tool_call": false,
					 "context_window": 1000000, "max_output_tokens": 8192,
					 "release_date": "2026-08-20",
					 "input_modalities": ["text"], "status": "active"},
					{"id": "small-window", "name": "Small Window", "tool_call": true,
					 "context_window": 127999, "max_output_tokens": 8192,
					 "release_date": "2026-08-20",
					 "input_modalities": ["text"], "status": "active"}
				]
			},
			{
				"id": "openai-chatgpt", "name": "ChatGPT", "company": "OpenAI",
				"api": "https://chatgpt.com/backend-api/codex", "protocol": "openai-compatible",
				"env": [], "tier": "standard",
				"auth_methods": ["sign_in"],
				"models": [
					{"id": "gpt-5.4", "name": "GPT 5.4", "tool_call": true,
					 "context_window": 400000, "max_output_tokens": 8192,
					 "release_date": "2026-07-01",
					 "input_modalities": ["text"], "status": "active"}
				]
			},
			{
				"id": "zai-coding-plan", "name": "Z.ai Coding Plan", "company": "Z.ai",
				"api": "https://api.z.ai/api/coding/paas/v4", "protocol": "anthropic",
				"env": ["ZAI_API_KEY"], "tier": "standard",
				"auth_methods": ["api_key"],
				"models": [
					{"id": "glm-5.2", "name": "GLM 5.2", "tool_call": true,
					 "context_window": 200000, "max_output_tokens": 8192,
					 "release_date": "2026-06-01",
					 "input_modalities": ["text"], "status": "active"}
				]
			}
		]
	}`
	c, err := catalog.NewCatalog([]byte(doc))
	require.NoError(t, err, "probe test catalog must parse")
	return c
}

// probeUpstream is a loopback stand-in for an OpenAI-compatible provider that
// accepts any key. It records every model it was asked to complete with, so a
// test can assert BOTH which model was exercised and how many attempts ran
// (a verbatim `model` must never fall through to a second candidate).
type probeUpstream struct {
	*httptest.Server
	mu     sync.Mutex
	models []string
	hits   int
}

func (p *probeUpstream) completions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.models...)
}

func (p *probeUpstream) requests() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hits
}

// startProbeUpstream serves GET /models and POST /chat/completions. A
// completion for a model whose id contains "not/a-model" answers with the
// upstream's own model-not-found body, so the "the probe is the validation"
// row exercises a real upstream rejection rather than a stubbed one.
func startProbeUpstream(t *testing.T) *probeUpstream {
	t.Helper()
	up := &probeUpstream{}
	up.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up.mu.Lock()
		up.hits++
		up.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = w.Write([]byte(`{"data":[{"id":"live-model"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			var req struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			up.mu.Lock()
			up.models = append(up.models, req.Model)
			up.mu.Unlock()
			if strings.Contains(req.Model, "not/a-model") {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(
					`{"error":{"message":"The model ` + "`not/a-model`" + ` does not exist","code":"model_not_found"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)
	return up
}

// installProbeCatalog makes cat the document this PROCESS serves, which is
// what providers.Admit / CatalogProvider / APIBaseFor read (T067-12 made that
// the one admission gate, shared with the CLI wizard). Without it the probe
// would validate against the embedded snapshot and these fixtures would be
// inert.
func installProbeCatalog(t *testing.T, cat *catalog.Catalog) {
	t.Helper()
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(nil) })
}

// newProbeAPI builds an onboarding-incomplete restAPI serving the probe test
// catalog, with a REAL SSRF checker that allowlists loopback only — so the
// httptest upstream is reachable while 10.0.0.0/8 stays blocked, which is
// exactly the pair the SSRF row needs.
func newProbeAPI(t *testing.T) *restAPI {
	t.Helper()
	api, _ := newAuthMethodOnboardingAPI(t)
	cat := probeTestCatalog(t)
	api.providerCatalog = cat
	installProbeCatalog(t, cat)
	api.ssrfChecker = security.NewSSRFChecker([]string{"127.0.0.1", "::1"})
	return api
}

// TestProbeProviderID_Validation is the outline "Probe provider id
// validation" plus the probe row of "Reserved literals are never provider
// ids", over Dataset "Provider id" rows 1–12.
//
// The sign-in rows that require a signed-in CLI (codex-cli logged in,
// openai-chatgpt with a fresh auth.json) belong to T068-14/T068-16, which
// wire the sign-in probe itself; the sign-in rows that are decided by
// catalog data alone — a provider that does not OFFER sign-in — are here.
func TestProbeProviderID_Validation(t *testing.T) {
	up := startProbeUpstream(t)

	cases := []struct {
		name string
		// body is a %s-format string; the single verb is filled with the
		// upstream URL so a row can point at the loopback provider.
		body string
		// status is the expected HTTP status.
		status int
		// field is the expected ErrorResponse.field ("" → not asserted as a
		// 4xx body at all, i.e. a 200 row).
		field string
		// errMsg, when set, is the exact ErrorResponse.error.
		errMsg string
		// errContains, when set, is a substring of ErrorResponse.error.
		errContains string
		// probedModel, when set, is the expected response probed_model.
		probedModel string
		// wantSuccess is asserted on 200 rows.
		wantSuccess *bool
	}{
		// ── Dataset row 1: empty id ──
		{
			name:   "empty id",
			body:   `{"id":"","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
		},
		// ── Dataset row 2: single char, not in catalog ──
		{
			name:   "single-char id not in catalog",
			body:   `{"id":"a","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
			errMsg: `unknown provider "a"`,
		},
		// ── Dataset row 3: 64 chars — length ok, still unknown ──
		{
			name: "64-char id passes the length cap and is unknown",
			body: fmt.Sprintf(`{"id":%q,"auth":"api_key","api_key":"k"}`,
				strings.Repeat("a", 64)),
			status: http.StatusBadRequest, field: "id",
			errMsg: fmt.Sprintf("unknown provider %q", strings.Repeat("a", 64)),
		},
		// ── Dataset row 4: 65 chars — maxLength ──
		{
			name: "65-char id is over the maxLength",
			body: fmt.Sprintf(`{"id":%q,"auth":"api_key","api_key":"k"}`,
				strings.Repeat("a", 65)),
			status: http.StatusBadRequest, field: "id",
			errContains: "64",
		},
		// ── Dataset row 5: case ──
		{
			name:   "uppercase id is not the catalog id",
			body:   `{"id":"OPENROUTER","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
			errMsg: `unknown provider "OPENROUTER"`,
		},
		// ── Dataset row 6: whitespace ──
		{
			name:   "id with a space",
			body:   `{"id":"open router","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
			errMsg: `unknown provider "open router"`,
		},
		// ── Dataset row 7: path traversal ──
		{
			name:   "path-traversal id",
			body:   `{"id":"../etc","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
			errMsg: `unknown provider "../etc"`,
		},
		// ── Dataset row 8: hyphenated catalog id ──
		{
			name:   "hyphenated catalog id probes",
			body:   `{"id":"zai-coding-plan","auth":"api_key","api_key":"k","api_base":"%s","model":"glm-5.2"}`,
			status: http.StatusOK, probedModel: "glm-5.2",
		},
		// ── Dataset row 9: removed id, generic echo, no list ──
		{
			name:   "removed id antigravity echoes the operator input only",
			body:   `{"id":"antigravity","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
			errMsg: `unknown provider "antigravity"`,
		},
		// ── Dataset row 10: removed alias ──
		{
			name:   "removed alias codexcli",
			body:   `{"id":"codexcli","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
			errMsg: `unknown provider "codexcli"`,
		},
		{
			name:   "retired claude-cli id",
			body:   `{"id":"claude-cli","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
			errMsg: `unknown provider "claude-cli"`,
		},
		// ── Dataset row 11: sign-in-only provider asked for api_key ──
		{
			name:   "sign-in-only provider refuses api_key",
			body:   `{"id":"openai-chatgpt","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "auth",
			errMsg: "provider does not support api_key",
		},
		// ── Dataset row 11b: reserved literals ──
		{
			name:   "reserved literal catalog",
			body:   `{"id":"catalog","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
			errMsg: `unknown provider "catalog"`,
		},
		{
			name:   "reserved literal default-model",
			body:   `{"id":"default-model","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
			errMsg: `unknown provider "default-model"`,
		},
		{
			name:   "reserved literal model-capabilities",
			body:   `{"id":"model-capabilities","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
			errMsg: `unknown provider "model-capabilities"`,
		},
		// ── Dataset row 12: unicode alias is a search term, not an id ──
		{
			name:   "unicode alias is not an id",
			body:   `{"id":"智谱","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
			errMsg: `unknown provider "智谱"`,
		},
		// ── Outline: catalog provider, no model → first Recommended ──
		{
			name:   "catalog provider with no model probes the first Recommended",
			body:   `{"id":"openrouter","auth":"api_key","api_key":"k","api_base":"%s"}`,
			status: http.StatusOK, probedModel: "rec-newest",
		},
		// ── Outline: explicit model is used verbatim ──
		{
			name:   "explicit model is used verbatim",
			body:   `{"id":"openrouter","auth":"api_key","api_key":"k","api_base":"%s","model":"z-ai/glm-5.2"}`,
			status: http.StatusOK, probedModel: "z-ai/glm-5.2",
		},
		// ── Outline: auth sign_in on a key-only provider ──
		{
			name:   "key-only provider refuses sign-in",
			body:   `{"id":"openrouter","auth":"sign_in"}`,
			status: http.StatusBadRequest, field: "auth",
			errMsg: "provider does not support sign-in",
		},
		// ── Outline: auth api_key with no key ──
		{
			name:   "api_key auth without a key",
			body:   `{"id":"openrouter","auth":"api_key"}`,
			status: http.StatusBadRequest, field: "api_key",
		},
		// ── Contract: api_key is forbidden with sign_in ──
		{
			name:   "sign_in auth carrying an api_key",
			body:   `{"id":"openai-chatgpt","auth":"sign_in","api_key":"k"}`,
			status: http.StatusBadRequest, field: "api_key",
		},
		// ── Contract: auth is required and closed ──
		{
			name:   "missing auth",
			body:   `{"id":"openrouter","api_key":"k"}`,
			status: http.StatusBadRequest, field: "auth",
		},
		{
			name:   "off-enum auth",
			body:   `{"id":"openrouter","auth":"oauth","api_key":"k"}`,
			status: http.StatusBadRequest, field: "auth",
		},
		// ── Outline: unknown id with no endpoint ──
		{
			name:   "unknown id with no endpoint",
			body:   `{"id":"not-a-provider","auth":"api_key","api_key":"k"}`,
			status: http.StatusBadRequest, field: "id",
			errMsg: `unknown provider "not-a-provider"`,
		},
		// ── FR-035: an unknown id with a base but no protocol names protocol ──
		{
			name:   "unknown id with an endpoint but no protocol",
			body:   `{"id":"not-a-provider","auth":"api_key","api_key":"k","api_base":"%s"}`,
			status: http.StatusBadRequest, field: "protocol",
		},
		// ── Outline: the custom row (api_base + protocol) probes ──
		{
			name: "custom row with api_base and protocol probes",
			body: `{"id":"not-a-provider","auth":"api_key","api_key":"k",` +
				`"api_base":"%s","protocol":"openai-compatible","model":"custom-model"}`,
			status: http.StatusOK, probedModel: "custom-model",
		},
		// ── Outline: model over the 256 cap ──
		{
			name: "model over the maxLength",
			body: fmt.Sprintf(`{"id":"openrouter","auth":"api_key","api_key":"k","model":%q}`,
				strings.Repeat("m", 257)),
			status: http.StatusBadRequest, field: "model",
			errContains: "256",
		},
		// ── Outline: internal-CIDR api_base → 422, nothing contacted ──
		{
			name:   "internal-CIDR api_base is refused by the SSRF gate",
			body:   `{"id":"openrouter","auth":"api_key","api_key":"k","api_base":"http://10.0.0.5/v1"}`,
			status: http.StatusUnprocessableEntity,
			errMsg: "provider endpoint not allowed (SSRF guard)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newProbeAPI(t)
			body := tc.body
			if strings.Contains(body, "%s") {
				body = fmt.Sprintf(body, up.URL)
			}
			before := up.requests()
			w := postProbe(t, api, body)
			require.Equal(t, tc.status, w.Code, "body=%s", w.Body.String())

			raw := w.Body.String()
			if tc.status == http.StatusOK {
				var resp gen.ProbeProviderResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				if tc.probedModel != "" {
					require.NotNil(t, resp.ProbedModel,
						"a probe that ran must report the model it exercised (FR-036)")
					assert.Equal(t, tc.probedModel, *resp.ProbedModel)
				}
				if tc.wantSuccess != nil {
					assert.Equal(t, *tc.wantSuccess, resp.Success)
				}
				return
			}

			var m map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
			if tc.field != "" {
				assert.Equal(t, tc.field, m["field"], "body=%s", raw)
			}
			if tc.errMsg != "" {
				assert.Equal(t, tc.errMsg, m["error"])
			}
			if tc.errContains != "" {
				assert.Contains(t, fmt.Sprint(m["error"]), tc.errContains)
			}
			// CRIT-003: an unauthenticated pre-onboarding 400 never maps the
			// install. No accepted-id list, no catalog id the caller did not
			// type, in any 4xx body.
			for _, leak := range []string{"openrouter", "zai-coding-plan", "openai-chatgpt"} {
				if strings.Contains(body, `"id":"`+leak+`"`) {
					continue
				}
				assert.NotContains(t, raw, leak,
					"the 400 body must never enumerate provider ids")
			}
			if tc.status == http.StatusUnprocessableEntity {
				assert.Equal(t, before, up.requests(),
					"the SSRF gate must fire before any outbound call")
			}
		})
	}
}

// TestProbeProviderID_VerbatimModelDoesNotFallThrough pins the outline row
// "openrouter | — | not/a-model": an explicit model is exercised exactly
// once. Falling through to the provider's Recommended list on a
// model_not_found would report a DIFFERENT model as working and hand the
// operator a green probe for a model they never picked (FR-029).
func TestProbeProviderID_VerbatimModelDoesNotFallThrough(t *testing.T) {
	up := startProbeUpstream(t)
	api := newProbeAPI(t)

	body := fmt.Sprintf(
		`{"id":"openrouter","auth":"api_key","api_key":"k","api_base":%q,"model":"not/a-model"}`,
		up.URL)
	w := postProbe(t, api, body)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp gen.ProbeProviderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.ProbedModel)
	assert.Equal(t, "not/a-model", *resp.ProbedModel,
		"probed_model must be the operator's pick, never a substitute")
	assert.Equal(t, []string{"not/a-model"}, up.completions(),
		"a verbatim model must be probed once, with no fall-through")
	require.NotNil(t, resp.Validation,
		"an upstream that rejected the model must surface a non-valid outcome")
	assert.NotEqual(t, gen.ProbeProviderResponseValidationOutcome("valid"),
		resp.Validation.Outcome)
}

// TestProbeProviderID_RecommendedOrder pins the fallback pick when no model
// is supplied: Recommended-for-chat eligibility is tool_call AND
// context_window ≥ 128,000 AND status active; the order is release_date desc
// then id asc; at most 3 candidates are ever attempted (FR-030/FR-036).
//
// The upstream rejects every model with model_not_found, so the handler
// walks the whole candidate list and the recorded completions ARE the
// ordered Recommended list.
func TestProbeProviderID_RecommendedOrder(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"live-model"}]}`))
			return
		}
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		asked = append(asked, req.Model)
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model_not_found"}}`))
	}))
	defer up.Close()

	api := newProbeAPI(t)
	body := fmt.Sprintf(`{"id":"openrouter","auth":"api_key","api_key":"k","api_base":%q}`, up.URL)
	w := postProbe(t, api, body)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	mu.Lock()
	got := append([]string(nil), asked...)
	mu.Unlock()

	// rec-newest (2026-08-01) → rec-a-tie / rec-b-tie (2026-05-01, id asc) →
	// rec-oldest is dropped by the 3-attempt cap. `no-tools` (no tool
	// calling) and `small-window` (127,999) are never eligible at all.
	assert.Equal(t, []string{"rec-newest", "rec-a-tie", "rec-b-tie"}, got,
		"Recommended order is release_date desc then id asc, capped at 3")

	var resp gen.ProbeProviderResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.ProbedModel)
	assert.Equal(t, "rec-b-tie", *resp.ProbedModel,
		"probed_model is the candidate actually exercised last, not the first tried")
}

// TestProbeProviderID_NoCatalogAdmitsAnyID guards the degraded install (E7):
// with no catalog DOCUMENT loaded — not merely an id the catalog lacks —
// membership cannot be decided at all, and turning every id into an unknown
// provider would make the probe unusable exactly when the operator most needs
// it. The other rules still apply.
func TestProbeProviderID_NoCatalogAdmitsAnyID(t *testing.T) {
	up := startProbeUpstream(t)
	api := newProbeAPI(t)
	// catalog.New() is a catalog with no document — the E7 state.
	empty := catalog.New()
	api.providerCatalog = empty
	providers.SetCatalog(empty)

	body := fmt.Sprintf(
		`{"id":"whatever","auth":"api_key","api_key":"k","api_base":%q,"model":"m"}`, up.URL)
	w := postProbe(t, api, body)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// …but a reserved literal is never an id, catalog or not.
	w = postProbe(t, api, `{"id":"catalog","auth":"api_key","api_key":"k"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "id", errBody(t, w)["field"])
}
