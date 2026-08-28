// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// ADR-067 T067-10 — the REST half: GET /providers/catalog (FR-017), the
// configured-only providers list with catalog-fed models (FR-020, FR-029),
// the PUT admission vocabulary (FR-019, FR-035) and the /health catalog
// state (FR-037).
//
// Spec: docs/internal/specs/adr-067-registry-catalog-spec.md — US-7.AC1–AC6,
// US-8.AC2/AC3/AC4, US-9.AC1/AC3, E2, E7, E9, E10, DS-5 rows 1–9, and the
// "HTTP" / "HTTP caching" Machine-Verifiable blocks.

package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/health"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ── fixtures ────────────────────────────────────────────────────────────────

// catalogDocJSON renders a 2.0.0 document whose updated_at is `age` old.
// Two cloud rows (one popular with models, one with ZERO models — E2), one
// cloud-IAM unsupported row (US-8.AC2), and the two local rows FR-020's
// live half needs. Provider and model names carry non-ASCII text so E9's
// byte-for-byte preservation is observable on the wire.
func catalogDocJSON(age time.Duration) []byte {
	updated := time.Now().UTC().Add(-age).Format(time.RFC3339)
	return []byte(fmt.Sprintf(`{
		"schema_version": "2.0.0",
		"version": "v2026.8.23",
		"updated_at": %q,
		"source": "models.dev@0123456789abcdef0123456789abcdef01234567",
		"default_resize_limits": { "long_edge_px": 7680, "max_bytes": 10485760 },
		"providers": [
			{
				"id": "skyprov",
				"name": "Skyprov — 天空",
				"company": "Skyprov",
				"api": "https://api.skyprov.example/v1",
				"protocol": "openai-compatible",
				"tier": "popular",
				"auth_methods": ["api_key"],
				"models": [
					{"id": "sky-large", "name": "Sky Large — 大", "tool_call": true,
					 "context_window": 200000, "max_output_tokens": 8192,
					 "input_modalities": ["text"], "status": "active"},
					{"id": "sky-small", "name": "Sky Small", "tool_call": true,
					 "context_window": 32000, "max_output_tokens": 4096,
					 "input_modalities": ["text"], "status": "active"}
				]
			},
			{
				"id": "emptyprov",
				"name": "Empty Prov",
				"company": "Empty Prov",
				"api": "https://api.emptyprov.example/v1",
				"protocol": "openai-compatible",
				"tier": "standard",
				"auth_methods": ["api_key"],
				"models": []
			},
			{
				"id": "amazon-bedrock",
				"name": "Amazon Bedrock",
				"company": "Amazon",
				"api": "",
				"tier": "unsupported",
				"unsupported_reason": "cloud-iam",
				"auth_methods": ["api_key"],
				"models": []
			},
			{
				"id": "ollama",
				"name": "Ollama",
				"company": "Ollama",
				"api": "http://localhost:11434/v1",
				"protocol": "ollama",
				"tier": "standard",
				"auth_methods": ["api_key"],
				"models": []
			},
			{
				"id": "lmstudio",
				"name": "LM Studio",
				"company": "LM Studio",
				"api": "http://127.0.0.1:1234/v1",
				"protocol": "openai-compatible",
				"tier": "standard",
				"auth_methods": ["api_key"],
				"models": []
			}
		]
	}`, updated))
}

// freshCatalog returns a catalog serving a document produced one day ago.
func freshCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.NewCatalog(catalogDocJSON(24 * time.Hour))
	require.NoError(t, err, "the T067-10 fixture document must parse")
	return c
}

// getProvidersCatalog issues one GET /api/v1/providers/catalog straight at
// the handler (auth is the route's middleware, exercised separately).
func getProvidersCatalog(api *restAPI, ifNoneMatch string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/catalog", nil)
	if ifNoneMatch != "" {
		r.Header.Set("If-None-Match", ifNoneMatch)
	}
	api.HandleProvidersCatalog(w, r)
	return w
}

// ── T34 — GET /providers/catalog: 200 / 401 / 304 / 503 ─────────────────────

// TestRestProvidersCatalog_GET covers DS-5 rows 1–4 and US-7.AC1–AC4.
func TestRestProvidersCatalog_GET(t *testing.T) {
	t.Run("row 1: authenticated GET returns the full document plus the envelope", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)

		w := getProvidersCatalog(api, "")
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var got gen.ProvidersCatalog
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got),
			"the body must validate against the generated ProvidersCatalog type (US-7.AC5)")
		assert.Equal(t, gen.ProvidersCatalogSchemaVersion("2.0.0"), got.SchemaVersion)
		assert.Equal(t, "v2026.8.23", got.Version)
		assert.Equal(t, "models.dev@0123456789abcdef0123456789abcdef01234567", got.Source,
			"the document's own free-text source is passed through, never the served_from marker")
		assert.Equal(t, gen.ProvidersCatalogServedFrom("embedded"), got.ServedFrom)
		assert.False(t, got.Stale, "a document one day old is not stale")
		require.Len(t, got.Providers, 5)

		byID := map[string]gen.CatalogProvider{}
		for _, p := range got.Providers {
			byID[p.Id] = p
		}
		sky := byID["skyprov"]
		assert.Equal(t, "Skyprov — 天空", sky.Name,
			"E9: unicode in a provider name survives load and GET byte-for-byte")
		require.Len(t, sky.Models, 2)
		assert.Equal(t, "sky-large", sky.Models[0].Id)
		assert.Equal(t, "Sky Large — 大", sky.Models[0].Name)
		assert.Equal(t, gen.CatalogProviderTier("popular"), sky.Tier)

		bedrock := byID["amazon-bedrock"]
		assert.Equal(t, gen.CatalogProviderTier("unsupported"), bedrock.Tier)
		require.NotNil(t, bedrock.UnsupportedReason)
		assert.Equal(t, gen.CatalogProviderUnsupportedReason("cloud-iam"), *bedrock.UnsupportedReason)

		require.NotNil(t, byID["emptyprov"].Models)
		assert.Empty(t, byID["emptyprov"].Models,
			"E2: a provider whose models were all retired is still listed, with an empty models array")

		assert.Equal(t, gen.CatalogProviderLocality("local"), byID["ollama"].Locality,
			"FR-039: locality is derived on load and served on the envelope")
		assert.Equal(t, gen.CatalogProviderLocality("cloud"), byID["skyprov"].Locality)
	})

	t.Run("row 1: the body is served from the cached pair, never re-marshalled", func(t *testing.T) {
		// SC-011: the handler writes bytes the catalog serialised at apply
		// time. Two reads must hand back the SAME backing array — if the
		// handler (or Served) rebuilt the document per request, these
		// would be distinct allocations.
		cat := freshCatalog(t)
		first, ok := cat.Served()
		require.True(t, ok)
		second, ok := cat.Served()
		require.True(t, ok)
		require.NotEmpty(t, first.Body)
		assert.Same(t, &first.Body[0], &second.Body[0],
			"the served body must be a cached slice, not a per-request marshal")
		assert.Equal(t, first.ETag, second.ETag)
	})

	t.Run("row 2: the FR-050 pre-auth window still serves the catalog to an anonymous caller", func(t *testing.T) {
		// RELEASE BLOCKER regression (UAT, 2026-08): before this fix the
		// route was registered under withAuth unconditionally, so a FRESH
		// INSTALL — onboarding incomplete, no users, no
		// OMNIPUS_BEARER_TOKEN, i.e. no admin account exists yet to
		// authenticate as — 401'd here. The onboarding wizard's provider
		// picker (src/routes/onboarding.tsx, providersCatalogQueryOptions)
		// calls exactly this route to render its ~200 providers; the SPA
		// treats a confirmed 401 as session expiry and force-logs-out
		// (src/routes/-app-auth.test.ts), so a brand-new install redirected
		// straight to /#/login and onboarding was unreachable. This test
		// exercises the real middleware chain (withOptionalAuth +
		// requireAuthOutsideOnboarding), not the handler alone, so a
		// regression back to withAuth fails it.
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)

		handler := api.withOptionalAuth(api.HandleProvidersCatalog)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/catalog", nil)
		ctx := context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
		handler(w, r.WithContext(ctx))

		require.Equal(t, http.StatusOK, w.Code,
			"a fresh install with no admin account yet must still be able to render the onboarding "+
				"provider picker; body=%s", w.Body.String())
		var got gen.ProvidersCatalog
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		assert.NotEmpty(t, got.Providers, "the picker needs a non-empty provider list to render")
		assert.Contains(t, w.Body.String(), "skyprov")
	})

	t.Run("row 2b: an unauthenticated request is 401 once onboarding is complete", func(t *testing.T) {
		// The other half of FR-050: the window must close the instant an
		// admin account could exist. The gate is requireAuthOutsideOnboarding
		// (the same FR-050 shape GET /providers uses), not an unconditional
		// 401 — see the C1 comment on that branch (rest.go) — but outside
		// the window the effect is identical to the pre-fix unconditional
		// gate.
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		require.NoError(t, api.onboardingMgr.CompleteOnboarding())
		require.True(t, api.onboardingMgr.IsComplete())

		handler := api.withOptionalAuth(api.HandleProvidersCatalog)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/catalog", nil)
		ctx := context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
		handler(w, r.WithContext(ctx))

		assert.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
		assert.NotContains(t, w.Body.String(), "skyprov",
			"an unauthenticated caller must not see the document once onboarding is complete")
	})

	t.Run("row 2c: an authenticated caller still gets the catalog once onboarding is complete", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		require.NoError(t, api.onboardingMgr.CompleteOnboarding())

		handler := api.withOptionalAuth(api.HandleProvidersCatalog)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/catalog", nil)
		ctx := context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
		ctx = context.WithValue(ctx, UserContextKey{}, &config.UserConfig{Username: "admin"})
		handler(w, r.WithContext(ctx))

		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		assert.Contains(t, w.Body.String(), "skyprov",
			"the Settings screen must still be able to read the catalog once onboarded")
	})

	t.Run("row 3: If-None-Match with the current ETag is 304 with no body", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)

		first := getProvidersCatalog(api, "")
		require.Equal(t, http.StatusOK, first.Code)
		etag := first.Header().Get("ETag")
		require.NotEmpty(t, etag)

		second := getProvidersCatalog(api, etag)
		assert.Equal(t, http.StatusNotModified, second.Code)
		assert.Empty(t, second.Body.Bytes(), "a 304 carries no body")
		assert.Equal(t, etag, second.Header().Get("ETag"),
			"the validator is repeated on the 304 so the client can keep caching")
	})

	t.Run("row 4: no catalog at all is 503 with a typed error, never an empty 200", func(t *testing.T) {
		for name, api := range map[string]*restAPI{
			"catalog never installed": func() *restAPI {
				a := newTestRestAPIWithHome(t)
				a.providerCatalog = nil
				return a
			}(),
			"E7: catalog installed but no document loaded": func() *restAPI {
				a := newTestRestAPIWithHome(t)
				a.providerCatalog = catalog.New()
				return a
			}(),
		} {
			t.Run(name, func(t *testing.T) {
				w := getProvidersCatalog(api, "")
				require.Equal(t, http.StatusServiceUnavailable, w.Code, "body=%s", w.Body.String())
				var body gen.ErrorResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				assert.Equal(t, "provider catalog unavailable", body.Error,
					"the Machine-Verifiable HTTP block pins this exact string")
			})
		}
	})

	t.Run("the retired refresh-models route no longer dispatches", func(t *testing.T) {
		// A-CONTRACT: POST /providers/{id}/refresh-models never existed in
		// openapi.yaml; the entitlement endpoint replaces it. The branch
		// and its handler are gone, so the dispatcher must not answer it —
		// neither with the old 200 nor with a 404 that would read as
		// "this provider is not configured".
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		seedTemplateProviders(t, api, &config.ModelConfig{
			Provider: "my-proxy", Model: "llama", Custom: true,
			APIBase: "https://my-proxy.example.com/v1", Protocol: "openai-compatible",
			Models: []string{"llama"},
		})
		for _, id := range []string{"my-proxy", "ghost"} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/providers/"+id+"/refresh-models", nil)
			api.HandleProviders(w, isolateRateLimit(t, r))
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code,
				"refresh-models must be gone for %q; body=%s", id, w.Body.String())
		}
	})

	t.Run("a non-GET verb on the catalog route is 405", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/providers/catalog", nil)
		api.HandleProvidersCatalog(w, r)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

// TestProvidersCatalog_RealMux_FreshInstallCanReachTheCatalog is the
// end-to-end proof for the UAT-reported release blocker: "fresh install
// cannot onboard". Reproduced live: GET /api/v1/providers/catalog 401'd on a
// clean $OMNIPUS_HOME with onboarding_complete=false, so the onboarding
// wizard's provider picker (src/routes/onboarding.tsx) could never render,
// the SPA read the 401 as session expiry and force-logged-out
// (src/routes/-app-auth.test.ts), and the fresh install was stuck at
// /#/login for an account that never existed.
//
// Unlike rows 2/2b/2c above (which call the handler behind a hand-picked
// middleware wrapper), this test exercises the PRODUCTION
// registerAdditionalEndpoints route table through a real *http.ServeMux —
// the exact call gateway.go makes at startup (see testMuxRegistrar,
// routes_admin_test.go). It therefore fails on EITHER half of a regression:
// reverting the registration line back to withAuth, or reverting the
// internal requireAuthOutsideOnboarding gate.
func TestProvidersCatalog_RealMux_FreshInstallCanReachTheCatalog(t *testing.T) {
	getCatalogViaRealMux := func(t *testing.T, api *restAPI) *httptest.ResponseRecorder {
		t.Helper()
		mux := http.NewServeMux()
		api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/providers/catalog", nil)
		ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req.WithContext(ctx))
		return w
	}

	t.Run("fresh install: onboarding incomplete, no admin account yet — anonymous GET is 200 with providers", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)

		w := getCatalogViaRealMux(t, api)

		require.Equal(t, http.StatusOK, w.Code,
			"a fresh install's onboarding provider picker must reach the real "+
				"production route; body=%s", w.Body.String())
		var got gen.ProvidersCatalog
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		assert.NotEmpty(t, got.Providers, "the picker needs a non-empty provider list to render")
	})

	t.Run("onboarded instance: anonymous GET is 401", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		require.NoError(t, api.onboardingMgr.CompleteOnboarding())

		w := getCatalogViaRealMux(t, api)

		assert.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
		assert.NotContains(t, w.Body.String(), "skyprov")
	})
}

// ── T34c — ETag shape, atomic pair, staleness, /health ──────────────────────

// TestRestProvidersCatalog_ETagAtomicAndStale pins the "HTTP caching"
// Machine-Verifiable block, the atomic bytes+ETag swap (F-29/F-22), the
// FR-037 staleness horizon and the /health projection.
func TestRestProvidersCatalog_ETagAtomicAndStale(t *testing.T) {
	t.Run("the ETag is a quoted strong SHA-256 and Cache-Control is fixed", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)

		w := getProvidersCatalog(api, "")
		require.Equal(t, http.StatusOK, w.Code)
		etag := w.Header().Get("ETag")
		require.Len(t, etag, 66, "a quoted 64-hex-digit SHA-256: got %q", etag)
		assert.True(t, strings.HasPrefix(etag, `"`) && strings.HasSuffix(etag, `"`),
			"the validator must be quoted: %q", etag)
		assert.NotContains(t, etag, "W/", "the validator must be STRONG, never weak")
		inner := strings.Trim(etag, `"`)
		assert.Regexp(t, "^[0-9a-f]{64}$", inner, "the validator is the hex SHA-256 of the served bytes")
		assert.Equal(t, "private, max-age=0, must-revalidate", w.Header().Get("Cache-Control"))
		assert.Empty(t, w.Header().Get("Vary"), "there is no content negotiation on this route")
	})

	t.Run("a weak or unquoted If-None-Match is not a match (200)", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)

		strong := getProvidersCatalog(api, "").Header().Get("ETag")
		require.NotEmpty(t, strong)

		for _, candidate := range []string{
			"W/" + strong,             // weak validator
			strings.Trim(strong, `"`), // unquoted
			`"deadbeef"`,              // a different document
			strong + `, "other"`,      // a list — no RFC 7232 list parsing here
		} {
			w := getProvidersCatalog(api, candidate)
			assert.Equal(t, http.StatusOK, w.Code,
				"If-None-Match %q must NOT match the strong validator", candidate)
			assert.NotEmpty(t, w.Body.Bytes(), "a 200 carries the document")
		}
	})

	t.Run("bytes and ETag are one pair under a concurrent apply", func(t *testing.T) {
		// E10: a refresh landing mid-session changes the ETag. What must
		// never happen is a reader seeing document A's bytes under
		// document B's validator — the hash of the body a caller got must
		// always be the validator it got with it.
		cat := freshCatalog(t)
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = cat

		var stop atomic.Bool
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; !stop.Load(); i++ {
				// Alternate two documents that differ in updated_at, so
				// each apply produces a genuinely different body + ETag.
				age := time.Duration(24+(i%2)) * time.Hour
				_ = cat.Apply(catalogDocJSON(age))
			}
		}()

		seen := map[string]struct{}{}
		for i := 0; i < 200; i++ {
			w := getProvidersCatalog(api, "")
			require.Equal(t, http.StatusOK, w.Code)
			etag := w.Header().Get("ETag")
			seen[etag] = struct{}{}
			// The 304 path is the observable consequence: replaying the
			// validator we just received against the SAME bytes must be a
			// match, which can only hold if the pair was never mixed.
			assert.Equal(t, etagOf(w.Body.Bytes()), etag,
				"served bytes and ETag must belong to the same apply")
		}
		stop.Store(true)
		wg.Wait()
		assert.GreaterOrEqual(t, len(seen), 1)
	})

	t.Run("stale is false at 13 days and true at 15 (FR-017 14-day horizon)", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			age       time.Duration
			wantStale bool
		}{
			{"13 days — inside the horizon", 13 * 24 * time.Hour, false},
			{"15 days — past the horizon", 15 * 24 * time.Hour, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cat, err := catalog.NewCatalog(catalogDocJSON(tc.age))
				require.NoError(t, err)
				api := newTestRestAPIWithHome(t)
				api.providerCatalog = cat

				w := getProvidersCatalog(api, "")
				require.Equal(t, http.StatusOK, w.Code)
				var got gen.ProvidersCatalog
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
				assert.Equal(t, tc.wantStale, got.Stale)
			})
		}
	})

	t.Run("/health reports the catalog degraded with the reason when stale", func(t *testing.T) {
		staleCat, err := catalog.NewCatalog(catalogDocJSON(15 * 24 * time.Hour))
		require.NoError(t, err)

		degraded, reason := catalogHealthState(staleCat)
		require.True(t, degraded, "a 15-day-old document degrades the catalog (FR-037)")
		assert.Contains(t, reason, "stale", "the reason must name the condition: %q", reason)

		body := healthBody(t, staleCat)
		catalogInfo, ok := body["catalog"].(map[string]any)
		require.True(t, ok, "/health must carry a catalog object; body=%v", body)
		assert.Equal(t, true, catalogInfo["degraded"])
		assert.NotEmpty(t, catalogInfo["reason"])
		assert.Equal(t, "ok", body["status"],
			"a stale catalog is an accuracy problem, not an availability one — /health stays 200/ok")
	})

	t.Run("/health reports the catalog degraded when no document is loaded", func(t *testing.T) {
		degraded, reason := catalogHealthState(catalog.New())
		assert.True(t, degraded)
		assert.NotEmpty(t, reason)

		body := healthBody(t, catalog.New())
		catalogInfo, ok := body["catalog"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, true, catalogInfo["degraded"])
	})

	t.Run("/health reports the catalog healthy when the document is fresh", func(t *testing.T) {
		degraded, reason := catalogHealthState(freshCatalog(t))
		assert.False(t, degraded)
		assert.Empty(t, reason)

		body := healthBody(t, freshCatalog(t))
		catalogInfo, ok := body["catalog"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, false, catalogInfo["degraded"])
		assert.NotContains(t, catalogInfo, "reason")
	})
}

// healthBody drives a real health.Server carrying the catalog hook and
// returns the decoded /health body.
func healthBody(t *testing.T, cat *catalog.Catalog) map[string]any {
	t.Helper()
	hs := health.NewServer("127.0.0.1", 0)
	hs.SetCatalogStateFunc(func() (bool, string) { return catalogHealthState(cat) })
	mux := http.NewServeMux()
	hs.RegisterOnMux(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body=%s", w.Body.String())
	return body
}

// ── T34b — GET /providers is configurations only (DS-5 rows 4b/4c) ──────────

// TestRestProviders_GET_ConfiguredOnly covers US-7.AC6 with a full-size
// catalog behind it: the picker lists the catalog, the providers list
// lists configurations, and the two never blend.
func TestRestProviders_GET_ConfiguredOnly(t *testing.T) {
	t.Run("row 4b: two configured providers against a 5-provider catalog → exactly two rows", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		t.Setenv("T067_10_SKY_KEY", "sk-sky")

		seedTemplateProviders(t, api,
			&config.ModelConfig{Provider: "skyprov", Model: "sky-large", APIKeyRef: "T067_10_SKY_KEY"},
			&config.ModelConfig{Provider: "emptyprov", Model: "nothing", APIBase: "https://api.emptyprov.example/v1"},
		)

		provs := getProviders(t, api)
		require.Len(t, provs, 2,
			"only the operator's own configurations are rows — the catalog's other providers are not; got %+v", provs)
		ids := []string{provs[0].Id, provs[1].Id}
		assert.ElementsMatch(t, []string{"skyprov", "emptyprov"}, ids)
	})

	t.Run("row 4c: no configured provider → [] , never a template row", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		seedTemplateProviders(t, api)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
		api.HandleProviders(w, isolateRateLimit(t, r))
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, "[]", trimJSON(w.Body.Bytes()))
	})

	t.Run("a configured row carries its catalog identity on the wire", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		seedTemplateProviders(t, api,
			&config.ModelConfig{Provider: "skyprov", Model: "sky-large", APIBase: "https://api.skyprov.example/v1"})

		provs := getProviders(t, api)
		require.Len(t, provs, 1)
		row := provs[0]
		require.NotNil(t, row.Protocol)
		assert.Equal(t, gen.ProviderProtocol("openai-compatible"), *row.Protocol)
		require.NotNil(t, row.Locality)
		assert.Equal(t, gen.ProviderLocality("cloud"), *row.Locality)
		require.NotNil(t, row.Company)
		assert.Equal(t, "Skyprov", *row.Company)
		assert.Nil(t, row.Custom, "a catalog id is not a custom row")
	})

	t.Run("a custom row is not an unknown provider", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		seedTemplateProviders(t, api, &config.ModelConfig{
			Provider: "my-proxy", Model: "llama", Custom: true,
			APIBase: "https://my-proxy.example.com/v1", Protocol: "openai-compatible",
			Models: []string{"llama", "mixtral"},
		})

		provs := getProviders(t, api)
		require.Len(t, provs, 1)
		row := provs[0]
		assert.NotEqual(t, gen.ProviderStatusUnknownProvider, row.Status,
			"X-13: the check is on the custom flag, not on membership of the catalog")
		require.NotNil(t, row.Custom)
		assert.True(t, *row.Custom)
		assert.Equal(t, []string{"llama", "mixtral"}, row.Models,
			"a custom row's catalogue is the operator's own slug list")
	})
}

// ── T36 — the offline model list (US-9.AC1, SC-003) ────────────────────────

// TestRestProviders_OfflineModelList covers DS-5 row 9: a cloud provider's
// models come from the catalog and NO outbound request is made.
func TestRestProviders_OfflineModelList(t *testing.T) {
	var outbound atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outbound.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"live-model-that-must-not-appear"}]}`))
	}))
	defer upstream.Close()

	api := newTestRestAPIWithHome(t)
	api.providerCatalog = freshCatalog(t)
	t.Setenv("T067_10_OFFLINE_KEY", "sk-live")
	// The row's api_base points at the counting server: if the handler
	// still listed a CLOUD provider live, the counter would move. It is
	// the only way the code could reach the network on this path, so a
	// zero here is a real proof rather than an absence of evidence.
	seedTemplateProviders(t, api, &config.ModelConfig{
		Provider: "skyprov", Model: "sky-large",
		APIKeyRef: "T067_10_OFFLINE_KEY", APIBase: upstream.URL,
	})

	provs := getProviders(t, api)
	require.Len(t, provs, 1)
	assert.Equal(t, []string{"sky-large", "sky-small"}, provs[0].Models,
		"US-9.AC1: a cloud provider's models come from the catalog, in document order")
	assert.Equal(t, int64(0), outbound.Load(),
		"SC-003: listing a cloud provider's models makes NO outbound request")
	assert.Nil(t, provs[0].Warning)

	t.Run("E2: a catalog provider with zero models lists nothing and warns nothing", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		seedTemplateProviders(t, api, &config.ModelConfig{
			Provider: "emptyprov", Model: "none", APIBase: "https://api.emptyprov.example/v1",
		})
		provs := getProviders(t, api)
		require.Len(t, provs, 1)
		require.NotNil(t, provs[0].Models)
		assert.Empty(t, provs[0].Models)
		assert.Nil(t, provs[0].Warning)
	})

	t.Run("an unknown id lists nothing at all (S67 Q4)", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		seedTemplateProviders(t, api, &config.ModelConfig{
			Provider: "nope", Model: "x", APIBase: "https://nope.example/v1",
			Models: []string{"slug-the-operator-typed"},
		})
		provs := getProviders(t, api)
		require.Len(t, provs, 1)
		assert.Equal(t, gen.ProviderStatusUnknownProvider, provs[0].Status)
		require.NotNil(t, provs[0].Models)
		assert.Empty(t, provs[0].Models,
			"an unknown provider's models are [] even when the row carries operator slugs")
	})
}

// ── T38 — local endpoints are listed live (US-9.AC3, DS-5 row 9's local half)

// TestRestProviders_Ollama_Live proves FR-020's local half: the live
// listing is the source, per protocol.
func TestRestProviders_Ollama_Live(t *testing.T) {
	t.Run("ollama is listed from /api/tags", func(t *testing.T) {
		var path atomic.Value
		ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path.Store(r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3:latest"},{"name":"qwen3:8b"}]}`))
		}))
		defer ollama.Close()

		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		seedTemplateProviders(t, api, &config.ModelConfig{
			Provider: "ollama", Model: "llama3:latest", APIBase: ollama.URL + "/v1",
			Models: []string{"a-stale-slug-the-live-list-must-replace"},
		})

		provs := getProviders(t, api)
		require.Len(t, provs, 1)
		assert.Equal(t, "/api/tags", path.Load(),
			"the ollama protocol lists from /api/tags, not /v1/models")
		assert.Equal(t, []string{"llama3:latest", "qwen3:8b"}, provs[0].Models,
			"US-9.AC3: the live listing is the source and the catalog is not consulted for the list")
		require.NotNil(t, provs[0].Locality)
		assert.Equal(t, gen.ProviderLocality("local"), *provs[0].Locality)
	})

	t.Run("a non-ollama local endpoint is listed from /v1/models", func(t *testing.T) {
		var path atomic.Value
		lmstudio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path.Store(r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen3-8b"}]}`))
		}))
		defer lmstudio.Close()

		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		seedTemplateProviders(t, api, &config.ModelConfig{
			Provider: "lmstudio", Model: "qwen3-8b", APIBase: lmstudio.URL + "/v1",
		})

		provs := getProviders(t, api)
		require.Len(t, provs, 1)
		assert.Equal(t, "/v1/models", path.Load())
		assert.Equal(t, []string{"qwen3-8b"}, provs[0].Models)
	})

	t.Run("an unreachable local endpoint warns and falls back to the operator's slugs", func(t *testing.T) {
		down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer down.Close()

		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		seedTemplateProviders(t, api, &config.ModelConfig{
			Provider: "ollama", Model: "llama3:latest", APIBase: down.URL + "/v1",
			Models: []string{"llama3:latest"},
		})

		provs := getProviders(t, api)
		require.Len(t, provs, 1)
		require.NotNil(t, provs[0].Warning)
		assert.Contains(t, *provs[0].Warning, "could not fetch upstream model list")
		assert.Equal(t, []string{"llama3:latest"}, provs[0].Models,
			"a failed live listing must not wipe the row — the operator's slugs stand in")
	})
}

// ── T35 — PUT admission vocabulary (DS-5 rows 5–8, US-8.AC2/AC3/AC4) ───────

// TestRestProviders_PUT_Unknown_CloudIAM_Custom pins the exact error
// vocabulary of the Machine-Verifiable HTTP block.
func TestRestProviders_PUT_Unknown_CloudIAM_Custom(t *testing.T) {
	newAPI := func(t *testing.T, rows ...map[string]any) *restAPI {
		t.Helper()
		api := newTestRestAPIWithHome(t)
		api.providerCatalog = freshCatalog(t)
		seedProviderConfig(t, api, rows...)
		return api
	}

	t.Run("row 8: an unknown id is 400 with the unknown-provider vocabulary", func(t *testing.T) {
		api := newAPI(t)
		w := doPutProvider(t, api, "nope", `{"api_key":"sk-x"}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, `unknown provider "nope"`, errorOf(t, w))
	})

	t.Run("row 5: a cloud-IAM provider is 400 with the catalog's own reason", func(t *testing.T) {
		api := newAPI(t)
		w := doPutProvider(t, api, "amazon-bedrock", `{"api_key":"sk-x"}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, `provider "amazon-bedrock" is unsupported: cloud-iam`, errorOf(t, w))
	})

	t.Run("row 7: a custom id without api_base is 400 unknown", func(t *testing.T) {
		api := newAPI(t)
		w := doPutProvider(t, api, "my-proxy", `{"api_key":"sk-x","protocol":"openai-compatible"}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, `unknown provider "my-proxy"`, errorOf(t, w))
	})

	t.Run("US-8.AC4: a custom id without a protocol is 400", func(t *testing.T) {
		api := newAPI(t)
		w := doPutProvider(t, api, "my-proxy", `{"api_key":"sk-x","api_base":"https://my-proxy.example.com/v1"}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, `unknown provider "my-proxy"`, errorOf(t, w))
	})

	t.Run("a custom id with a protocol no base URL can describe is 400", func(t *testing.T) {
		api := newAPI(t)
		w := doPutProvider(t, api, "my-proxy",
			`{"api_key":"sk-x","api_base":"https://my-proxy.example.com/v1","protocol":"ollama"}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, `unknown provider "my-proxy"`, errorOf(t, w))
	})

	t.Run("US-8.AC4: a custom id with api_base and a permitted protocol is accepted as custom:true", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, "/models") {
				_, _ = w.Write([]byte(`{"data":[{"id":"proxy-model"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
		}))
		defer upstream.Close()

		t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
		api := newAPI(t)

		body := fmt.Sprintf(
			`{"api_key":"sk-x","api_base":%q,"protocol":"openai-compatible","model":"proxy-model"}`,
			upstream.URL)
		w := doPutProvider(t, api, "my-proxy", body)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var got gen.Provider
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		assert.Equal(t, "my-proxy", got.Id)
		require.NotNil(t, got.Custom)
		assert.True(t, *got.Custom, "FR-035: an accepted non-catalog id becomes a custom row")
		require.NotNil(t, got.Protocol)
		assert.Equal(t, gen.ProviderProtocol("openai-compatible"), *got.Protocol)

		// The flag and both identity halves must be PERSISTED — every
		// later check reads them, not the literal id (X-13).
		persisted := persistedProviderRow(t, api, "my-proxy")
		assert.Equal(t, true, persisted["custom"])
		assert.Equal(t, upstream.URL, persisted["api_base"])
		assert.Equal(t, "openai-compatible", persisted["protocol"])
	})

	t.Run("row 6: a standard-tier catalog provider is accepted with no probe requirement", func(t *testing.T) {
		// No api_key in the body → no key-validation probe at all
		// (US-8.AC3: a standard-tier provider is reachable through
		// protocol dispatch without one), so this stays hermetic.
		api := newAPI(t, map[string]any{
			"provider": "emptyprov", "model": "old", "api_key_ref": "T067_10_EXISTING",
		})
		w := doPutProvider(t, api, "emptyprov", `{"model":"new-model"}`)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var got gen.Provider
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		assert.Equal(t, "emptyprov", got.Id)
		assert.Nil(t, got.Custom, "a catalog id is never marked custom")

		persisted := persistedProviderRow(t, api, "emptyprov")
		assert.Equal(t, "new-model", persisted["model"])
		assert.NotContains(t, persisted, "custom")
	})

	t.Run("with no catalog loaded (E7) admission classifies nothing", func(t *testing.T) {
		// A gateway that booted without a catalog cannot tell a real id
		// from a typo. Refusing every write would make a bad snapshot
		// unrecoverable through the UI, so the gate stands down.
		custom, err := providerAdmission(catalog.New(), "anything", "", "")
		assert.NoError(t, err)
		assert.False(t, custom)
	})
}

// errorOf decodes the ErrorResponse.error field from a recorder.
func errorOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body=%s", w.Body.String())
	return body.Error
}

// persistedProviderRow reads config.json back off disk and returns the
// providers[] entry for id — proving what was WRITTEN, not what the
// response echoed.
func persistedProviderRow(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	list, _ := m["providers"].([]any)
	for _, item := range list {
		e, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if e["provider"] == id {
			return e
		}
	}
	t.Fatalf("no persisted providers[] entry for %q in %s", id, string(raw))
	return nil
}

// etagOf recomputes the quoted strong validator for a body, so a test can
// assert that the ETag a caller received really belongs to the bytes it
// received with it.
func etagOf(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// ── BenchmarkProvidersCatalogGET — SC-011: no per-request marshal ───────────

func BenchmarkProvidersCatalogGET(b *testing.B) {
	cat, err := catalog.NewCatalog(catalogDocJSON(24 * time.Hour))
	if err != nil {
		b.Fatalf("fixture must parse: %v", err)
	}
	api := &restAPI{providerCatalog: cat}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/catalog", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		api.HandleProvidersCatalog(w, r)
		if w.Code != http.StatusOK {
			b.Fatalf("status = %d", w.Code)
		}
	}
}
