// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_providers_entitlement_test.go — T37 for ADR-067 T067-11.
//
// POST /api/v1/providers/{id}/entitlement (FR-021, FR-037's invalidation
// half): the per-protocol listing call, the catalog intersection, the
// process-lifetime cache keyed on the credential REF NAME, and the three
// evictions (provider DELETE, key-changing PUT, catalog refresh).
//
// Spec: docs/internal/specs/adr-067-registry-catalog-spec.md — US-9.AC2,
// E10, DS-5 rows 10, 11, 11b, 11c, and the "Scenario Outline (AP):
// entitlement per protocol" / "Scenario (AP): entitlement intersects and
// caches" / "Scenario (EP): entitlement without a key" blocks.
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ── fixtures ────────────────────────────────────────────────────────────────

// entitlementStub is a recording upstream. It counts LISTING calls only —
// a POST is the PUT path's key-validation probe, which must never be
// mistaken for the one listing call FR-021 allows.
type entitlementStub struct {
	mu        sync.Mutex
	listCalls int
	paths     []string
	headers   []http.Header
	status    int      // when non-zero, every listing answers this status
	ids       []string // model ids the listing reports

	srv *httptest.Server
}

func newEntitlementStub(t *testing.T, ids ...string) *entitlementStub {
	t.Helper()
	s := &entitlementStub{ids: ids}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// The PUT branch's completion probe (providers.ValidateKey).
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
			return
		}
		s.mu.Lock()
		s.listCalls++
		s.paths = append(s.paths, r.URL.Path)
		s.headers = append(s.headers, r.Header.Clone())
		status := s.status
		ids := append([]string(nil), s.ids...)
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/api/tags") {
			rows := make([]string, 0, len(ids))
			for _, id := range ids {
				rows = append(rows, fmt.Sprintf(`{"name":%q}`, id))
			}
			_, _ = w.Write([]byte(`{"models":[` + strings.Join(rows, ",") + `]}`))
			return
		}
		rows := make([]string, 0, len(ids))
		for _, id := range ids {
			rows = append(rows, fmt.Sprintf(`{"id":%q}`, id))
		}
		_, _ = w.Write([]byte(`{"data":[` + strings.Join(rows, ",") + `]}`))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *entitlementStub) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCalls
}

func (s *entitlementStub) lastPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.paths) == 0 {
		return ""
	}
	return s.paths[len(s.paths)-1]
}

func (s *entitlementStub) lastHeader(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.headers) == 0 {
		return ""
	}
	return s.headers[len(s.headers)-1].Get(key)
}

func (s *entitlementStub) setStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = code
}

// entitlementCatalogJSON is a valid 2.0.0 document carrying one row per
// protocol the endpoint dispatches on, plus the `cli` row that must be
// refused. Every hosted `api` is https on a public host — FR-033 rejects a
// document that points a hosted row at loopback — so the stub URL rides on
// each CONFIG row's api_base instead (a regional host / self-hosted
// gateway, which is exactly what resolveProviderRow prefers).
func entitlementCatalogJSON() []byte {
	return []byte(`{
		"schema_version": "2.0.0",
		"version": "v2026.8.23",
		"updated_at": "2026-08-22T00:00:00Z",
		"source": "models.dev@0123456789abcdef0123456789abcdef01234567",
		"default_resize_limits": { "long_edge_px": 7680, "max_bytes": 10485760 },
		"providers": [
			{
				"id": "openai", "name": "OpenAI", "company": "OpenAI",
				"api": "https://api.openai.example/v1",
				"protocol": "openai-compatible", "tier": "popular",
				"auth_methods": ["api_key"],
				"models": [
					{"id": "gpt-a", "name": "GPT A", "tool_call": true,
					 "context_window": 128000, "max_output_tokens": 8192,
					 "input_modalities": ["text"], "status": "active"},
					{"id": "gpt-b", "name": "GPT B", "tool_call": true,
					 "context_window": 64000, "max_output_tokens": 4096,
					 "input_modalities": ["text"], "status": "active"}
				]
			},
			{
				"id": "google", "name": "Google", "company": "Google",
				"api": "https://generativelanguage.example/v1beta",
				"protocol": "google", "tier": "popular",
				"auth_methods": ["api_key"],
				"models": [
					{"id": "gem-a", "name": "Gemini A", "tool_call": true,
					 "context_window": 1000000, "max_output_tokens": 8192,
					 "input_modalities": ["text"], "status": "active"}
				]
			},
			{
				"id": "anthropic", "name": "Anthropic", "company": "Anthropic",
				"api": "https://api.anthropic.example/v1",
				"protocol": "anthropic", "tier": "popular",
				"auth_methods": ["api_key"],
				"models": [
					{"id": "claude-x", "name": "Claude X", "tool_call": true,
					 "context_window": 200000, "max_output_tokens": 8192,
					 "input_modalities": ["text"], "status": "active"},
					{"id": "claude-y", "name": "Claude Y", "tool_call": true,
					 "context_window": 200000, "max_output_tokens": 8192,
					 "input_modalities": ["text"], "status": "active"}
				]
			},
			{
				"id": "ollama", "name": "Ollama", "company": "Ollama",
				"api": "http://localhost:11434/v1",
				"protocol": "ollama", "tier": "standard",
				"auth_methods": ["api_key"],
				"models": []
			},
			{
				"id": "codex-cli", "name": "Codex CLI", "company": "OpenAI",
				"api": "", "protocol": "cli", "cli_kind": "codex",
				"tier": "standard", "auth_methods": ["sign_in"],
				"models": []
			}
		]
	}`)
}

// stubCatalogPuller hands Refresh a fixed document so a real refresh
// transaction (and therefore the real OnRefreshApplied hook) can run in a
// unit test. Re-applying the SAME version is a permitted no-op re-apply,
// which still fires the hooks — exactly the E10 condition under test.
type stubCatalogPuller struct{ data []byte }

func (p *stubCatalogPuller) Pull(context.Context) ([]byte, error) { return p.data, nil }
func (p *stubCatalogPuller) LastPullDegraded() (bool, error)      { return false, nil }

// entitlementRow builds a configured provider row pointed at a stub.
func entitlementRow(id, protocol, apiBase, ref string, custom bool) *config.ModelConfig {
	return &config.ModelConfig{
		Name: id, Provider: id, Model: "probe-model",
		APIKeyRef: ref, APIBase: apiBase, Protocol: protocol, Custom: custom,
	}
}

// newEntitlementAPI wires the delete-test harness (config.json on disk,
// unlocked credential store, audit logger) to a booted catalog with a
// puller, and registers the SAME refresh-invalidation hook gateway.go
// registers at boot.
func newEntitlementAPI(t *testing.T, rows ...*config.ModelConfig) *restAPI {
	t.Helper()
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "unrelated", Model: "unrelated-model"}, rows)
	api, _, _ := newProviderDeleteAPI(t, cfg)
	cat := catalog.Boot(context.Background(), entitlementCatalogJSON(),
		&stubCatalogPuller{data: entitlementCatalogJSON()}, nil, nil)
	require.NotNil(t, cat.Document(), "the T067-11 fixture document must parse")
	api.providerCatalog = cat
	registerEntitlementCacheInvalidation(cat, api)
	return api
}

// postEntitlement drives the real HandleProviders dispatcher.
func postEntitlement(t *testing.T, api *restAPI, id string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/providers/"+id+"/entitlement", nil)
	ctx := context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	ctx = context.WithValue(ctx, UserContextKey{}, &config.UserConfig{Username: "admin"})
	w := httptest.NewRecorder()
	api.HandleProviders(w, isolateRateLimit(t, r.WithContext(ctx)))
	return w
}

// entitlementBody decodes a 200 into the generated wire type.
func entitlementBody(t *testing.T, w *httptest.ResponseRecorder) gen.EntitlementResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got gen.EntitlementResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got), "body=%s", w.Body.String())
	return got
}

// entitlementByID indexes a response's rows.
func entitlementByID(rows []gen.EntitlementModel) map[string]gen.EntitlementModel {
	out := make(map[string]gen.EntitlementModel, len(rows))
	for _, m := range rows {
		out[m.Id] = m
	}
	return out
}

// errorBody decodes an ErrorResponse envelope.
func entitlementErrorBody(t *testing.T, w *httptest.ResponseRecorder) gen.ErrorResponse {
	t.Helper()
	var body gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body=%s", w.Body.String())
	return body
}

// ── T37 ─────────────────────────────────────────────────────────────────────

// TestRestProviders_Entitlement_PerProtocol is T37: the six-row protocol
// outline, the intersect-and-cache behaviour, the three evictions, the 422
// and the 502.
func TestRestProviders_Entitlement_PerProtocol(t *testing.T) {
	// ── the outline (US-9.AC2, DS-5 rows 10 and 11b) ────────────────────
	t.Run("outline: one listing call per protocol; cli and custom rows are 409", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			id         string
			protocol   string
			custom     bool
			apiBaseSfx string // appended to the stub root
			wantStatus int
			wantPath   string
			wantCalls  int
		}{
			{
				name: "openai — openai-compatible → GET /models Bearer",
				id:   "openai", protocol: "openai-compatible",
				wantStatus: http.StatusOK, wantPath: "/models", wantCalls: 1,
			},
			{
				name: "google → GET /models Bearer",
				id:   "google", protocol: "google",
				wantStatus: http.StatusOK, wantPath: "/models", wantCalls: 1,
			},
			{
				name: "anthropic → GET /v1/models with x-api-key + anthropic-version",
				id:   "anthropic", protocol: "anthropic", apiBaseSfx: "/v1",
				wantStatus: http.StatusOK, wantPath: "/v1/models", wantCalls: 1,
			},
			{
				name: "ollama → GET /api/tags",
				id:   "ollama", protocol: "ollama", apiBaseSfx: "/v1",
				wantStatus: http.StatusOK, wantPath: "/api/tags", wantCalls: 1,
			},
			{
				name: "codex-cli — protocol cli → 409, nothing dialled",
				id:   "codex-cli", protocol: "cli",
				wantStatus: http.StatusConflict, wantCalls: 0,
			},
			{
				name: "my-proxy — a custom row → 409, nothing dialled",
				id:   "my-proxy", protocol: "openai-compatible", custom: true,
				wantStatus: http.StatusConflict, wantCalls: 0,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				const ref = "T067_11_KEY"
				const secret = "sk-entitlement-secret"
				t.Setenv(ref, secret)
				stub := newEntitlementStub(t, "gpt-a", "gem-a", "claude-x", "llama3")
				api := newEntitlementAPI(t, entitlementRow(
					tc.id, tc.protocol, stub.srv.URL+tc.apiBaseSfx, ref, tc.custom))

				w := postEntitlement(t, api, tc.id)

				require.Equal(t, tc.wantStatus, w.Code, "body=%s", w.Body.String())
				assert.Equal(t, tc.wantCalls, stub.calls(),
					"FR-021 allows exactly one listing call per uncached check")
				if tc.wantStatus == http.StatusConflict {
					assert.Equal(t, "entitlement not supported for this protocol",
						entitlementErrorBody(t, w).Error,
						"DS-5 row 11b pins this exact string")
					return
				}
				assert.Equal(t, tc.wantPath, stub.lastPath(),
					"the protocol decides the listing path")
				got := entitlementBody(t, w)
				assert.False(t, got.Cached, "the first check is never served from the cache")
				assert.NotEmpty(t, got.Models)
				assert.False(t, got.CheckedAt.IsZero(), "checked_at carries the time of the live call")
				assert.NotContains(t, w.Body.String(), secret,
					"the response must never echo the operator's key")

				switch catalog.Protocol(tc.protocol) {
				case catalog.ProtocolAnthropic:
					assert.Equal(t, secret, stub.lastHeader("X-Api-Key"),
						"the anthropic listing authenticates with x-api-key")
					assert.NotEmpty(t, stub.lastHeader("Anthropic-Version"),
						"the anthropic listing must carry anthropic-version")
					assert.Empty(t, stub.lastHeader("Authorization"),
						"the anthropic listing is not a Bearer call")
				case catalog.ProtocolOllama:
					// /api/tags is unauthenticated by design.
				default:
					assert.Equal(t, "Bearer "+secret, stub.lastHeader("Authorization"),
						"openai-compatible and google list with a Bearer key")
				}
			})
		}
	})

	// ── intersect + cache (US-9.AC2, DS-5 row 10, E10) ──────────────────
	t.Run("intersects the catalog, caches for the process, and a refresh evicts", func(t *testing.T) {
		const ref = "T067_11_ANTHROPIC_KEY"
		t.Setenv(ref, "sk-anthropic")
		stub := newEntitlementStub(t, "claude-x", "brand-new-model")
		api := newEntitlementAPI(t,
			entitlementRow("anthropic", "anthropic", stub.srv.URL+"/v1", ref, false))

		first := entitlementBody(t, postEntitlement(t, api, "anthropic"))
		assert.False(t, first.Cached)
		require.Equal(t, 1, stub.calls())

		rows := entitlementByID(first.Models)
		require.Len(t, first.Models, 3, "two catalog models plus the one extra: %+v", first.Models)
		assert.True(t, rows["claude-x"].Entitled, "the listing returned claude-x")
		assert.Equal(t, gen.EntitlementModelLimitsKnown, rows["claude-x"].Limits)
		assert.False(t, rows["claude-y"].Entitled, "the listing did NOT return claude-y")
		assert.Equal(t, gen.EntitlementModelLimitsKnown, rows["claude-y"].Limits,
			"a catalog model always has known limits, entitled or not")
		assert.True(t, rows["brand-new-model"].Entitled)
		assert.Equal(t, gen.EntitlementModelLimitsUnknown, rows["brand-new-model"].Limits,
			"a model the catalog lacks carries limits: unknown")

		second := entitlementBody(t, postEntitlement(t, api, "anthropic"))
		assert.True(t, second.Cached, "the second check is served from the process cache")
		assert.Equal(t, first.CheckedAt.UTC(), second.CheckedAt.UTC(),
			"a cached result keeps the ORIGINAL checked_at")
		assert.Equal(t, first.Models, second.Models)
		assert.Equal(t, 1, stub.calls(), "a cache hit makes no upstream call")

		// E10 / FR-037: a catalog refresh invalidates the cache.
		require.NoError(t, api.providerCatalog.Refresh(context.Background()))
		third := entitlementBody(t, postEntitlement(t, api, "anthropic"))
		assert.False(t, third.Cached, "the refresh evicted the entry")
		assert.Equal(t, 2, stub.calls(), "the check after a refresh dials upstream again")
	})

	// ── eviction on DELETE (ADR-068 FR-010 step 3b) ─────────────────────
	t.Run("provider DELETE evicts the entry", func(t *testing.T) {
		const ref = "T067_11_DELETE_KEY"
		t.Setenv(ref, "sk-delete")
		stub := newEntitlementStub(t, "gpt-a")
		row := entitlementRow("openai", "openai-compatible", stub.srv.URL, ref, false)
		api := newEntitlementAPI(t, row)

		require.False(t, entitlementBody(t, postEntitlement(t, api, "openai")).Cached)
		key := entitlementCacheKey("openai", ref)
		_, cached := api.entitlements.get(key)
		require.True(t, cached, "the first check must populate the cache")

		w := doProviderDelete(t, api, "openai", "", nil, true)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		_, stillCached := api.entitlements.get(key)
		assert.False(t, stillCached,
			"FR-010 step 3b: the deleted provider's entitlement entry must not survive")
	})

	// ── eviction on a key-changing PUT, and NOT on an updated_at-only PUT ──
	t.Run("a key-changing PUT evicts; an updated_at-only PUT does not", func(t *testing.T) {
		// The row is keyed on the SAME credential ref name the PUT writes
		// (`<id>_API_KEY`), so the cache key is identical before and after
		// the rotation: the only thing that can turn the second check into
		// a miss is the eviction itself. With a different ref name this
		// test would pass even with the eviction deleted — the key would
		// have changed underneath it.
		const ref = "openai_API_KEY"
		t.Setenv(ref, "sk-put")
		stub := newEntitlementStub(t, "gpt-a", "gpt-b")
		api := newEntitlementAPI(t,
			entitlementRow("openai", "openai-compatible", stub.srv.URL, ref, false))
		primed := entitlementCacheKey("openai", ref)

		require.False(t, entitlementBody(t, postEntitlement(t, api, "openai")).Cached)
		require.Equal(t, 1, stub.calls())
		_, ok := api.entitlements.get(primed)
		require.True(t, ok, "the first check must populate the cache under the stable key")

		// A PUT carrying no api_key only re-stamps updated_at (and here the
		// model list) — the key is untouched, so the cache must survive.
		putProviderForEntitlement(t, api, "openai", `{"models":["gpt-a"]}`)
		afterModelOnly := entitlementBody(t, postEntitlement(t, api, "openai"))
		assert.True(t, afterModelOnly.Cached,
			"unasked Q5: a PUT that only bumps updated_at is NOT an eviction")
		assert.Equal(t, 1, stub.calls())

		// A PUT that changes the key must evict: the new key's entitlement
		// is a different fact.
		putProviderForEntitlement(t, api, "openai", `{"api_key":"sk-rotated"}`)
		require.Equal(t, ref, api.configuredProviderRow("openai").APIKeyRef,
			"the rotation must keep the ref NAME stable, or this test proves nothing")
		_, survived := api.entitlements.get(primed)
		assert.False(t, survived, "the key-changing PUT must drop the entry itself")
		afterKeyChange := entitlementBody(t, postEntitlement(t, api, "openai"))
		assert.False(t, afterKeyChange.Cached, "a key-changing PUT evicts the entry")
		assert.Equal(t, 2, stub.calls(), "the check after a key change dials upstream again")
	})

	// ── 422: no resolvable key (DS-5 row 11) ────────────────────────────
	t.Run("an unresolvable credential ref is 422 and dials nothing", func(t *testing.T) {
		stub := newEntitlementStub(t, "claude-x")
		api := newEntitlementAPI(t, entitlementRow(
			"anthropic", "anthropic", stub.srv.URL+"/v1", "T067_11_MISSING_REF", false))

		w := postEntitlement(t, api, "anthropic")

		require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, "the configured credential reference no longer exists — re-enter the API key.",
			entitlementErrorBody(t, w).Error,
			"the 422 speaks the describeCredentialResolutionError vocabulary")
		assert.Equal(t, 0, stub.calls(), "no key means no outbound call at all")
	})

	// ── 502: upstream non-2xx, nothing cached (DS-5 row 11c, X-12) ──────
	t.Run("an upstream non-2xx is 502 and caches nothing", func(t *testing.T) {
		const ref = "T067_11_502_KEY"
		t.Setenv(ref, "sk-502")
		stub := newEntitlementStub(t, "gpt-a")
		stub.setStatus(http.StatusTooManyRequests)
		api := newEntitlementAPI(t,
			entitlementRow("openai", "openai-compatible", stub.srv.URL, ref, false))

		w := postEntitlement(t, api, "openai")

		require.Equal(t, http.StatusBadGateway, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, "could not fetch upstream model list: status 429",
			entitlementErrorBody(t, w).Error, "X-12 pins this exact string")
		_, cached := api.entitlements.get(entitlementCacheKey("openai", ref))
		assert.False(t, cached, "a failed check must cache nothing")

		// And the failure is not sticky: the next call dials again.
		require.Equal(t, http.StatusBadGateway, postEntitlement(t, api, "openai").Code)
		assert.Equal(t, 2, stub.calls())
	})

	// ── guards ──────────────────────────────────────────────────────────
	t.Run("an unconfigured provider is 404", func(t *testing.T) {
		api := newEntitlementAPI(t)
		w := postEntitlement(t, api, "openai")
		assert.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
	})
}

// TestEntitlementCacheKey pins the FR-021 key derivation: the ref NAME is
// hashed, never the secret, and the two inputs are joined by a colon.
func TestEntitlementCacheKey(t *testing.T) {
	want := sha256.Sum256([]byte("openai:OPENAI_API_KEY"))
	got := entitlementCacheKey("openai", "OPENAI_API_KEY")
	assert.Equal(t, hex.EncodeToString(want[:]), got,
		"the key is SHA-256(providerID + \":\" + credentialRefName)")
	assert.Len(t, got, 64)
	assert.NotEqual(t, got, entitlementCacheKey("anthropic", "OPENAI_API_KEY"))
	assert.NotEqual(t, got, entitlementCacheKey("openai", "OPENAI_API_KEY_2"))
}

// TestEntitlementCacheEviction covers the cache type itself: evictProvider
// removes every entry for one provider and leaves the others alone; clear
// empties it.
func TestEntitlementCacheEviction(t *testing.T) {
	var c entitlementCache
	c.put(entitlementCacheKey("openai", "A"), entitlementCacheEntry{providerID: "openai"})
	c.put(entitlementCacheKey("openai", "B"), entitlementCacheEntry{providerID: "openai"})
	c.put(entitlementCacheKey("anthropic", "A"), entitlementCacheEntry{providerID: "anthropic"})

	c.evictProvider("openai")
	_, a := c.get(entitlementCacheKey("openai", "A"))
	_, b := c.get(entitlementCacheKey("openai", "B"))
	_, other := c.get(entitlementCacheKey("anthropic", "A"))
	assert.False(t, a, "every entry for the provider goes, whatever ref it was keyed on")
	assert.False(t, b)
	assert.True(t, other, "another provider's entry is untouched")

	c.clear()
	_, afterClear := c.get(entitlementCacheKey("anthropic", "A"))
	assert.False(t, afterClear)
}

// putProviderForEntitlement drives PUT /api/v1/providers/{id} through the
// real dispatcher with no user in context (pre-onboarding posture), so the
// re-auth consent gate does not apply.
func putProviderForEntitlement(t *testing.T, api *restAPI, id, body string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/providers/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	w := httptest.NewRecorder()
	api.HandleProviders(w, isolateRateLimit(t, r.WithContext(ctx)))
	require.Equal(t, http.StatusOK, w.Code, "PUT body=%s", w.Body.String())
}
