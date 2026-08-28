// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_entitlement_test.go — TDD row 23 for ADR-068 T068-17.
//
// The ADR-068 half of "Check with my account": the wire-visible annotation
// shape the SPA greys rows from, the process cache's key derivation and its
// three-way eviction contract (DELETE and a key-changing PUT evict; an
// `updated_at`-only PUT does not), and FR-010 step 2b — a provider DELETE
// leaves no `ContextSettings.model_overrides[]` row for the id behind
// (cross-spec Q3), which nothing else covers.
//
// Spec: docs/internal/specs/adr-068-providers-ux-spec.md — FR-031 (X-03,
// X-21), FR-010 steps 2b/3b, US-7 scenarios "Check with my account greys
// unavailable models" and "Check with my account upstream failure".
//
// The per-PROTOCOL depth (which path each protocol dials, which header
// carries the key, the 409/422 refusals) is ADR-067's T37 next door in
// rest_providers_entitlement_test.go and is deliberately not repeated here;
// this file drives the same real handler through the same real dispatcher
// and asserts what ADR-068 owns.
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// entitlementS68CatalogJSON is a valid 2.0.0 document whose openai row
// carries exactly the US-7 scenario's catalog models A, B and C. The
// hosted `api` is an https public host (FR-033 rejects a hosted row
// pointed at loopback); the stub URL rides on the CONFIG row's api_base,
// which is what resolveProviderRow prefers.
func entitlementS68CatalogJSON() []byte {
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
					{"id": "A", "name": "Model A", "tool_call": true,
					 "context_window": 128000, "max_output_tokens": 8192,
					 "input_modalities": ["text"], "status": "active"},
					{"id": "B", "name": "Model B", "tool_call": true,
					 "context_window": 64000, "max_output_tokens": 4096,
					 "input_modalities": ["text"], "status": "active"},
					{"id": "C", "name": "Model C", "tool_call": true,
					 "context_window": 32000, "max_output_tokens": 4096,
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
					 "input_modalities": ["text"], "status": "active"}
				]
			}
		]
	}`)
}

// newEntitlementS68API wires the provider-delete harness (config.json on
// disk, unlocked credential store, audit logger) to the scenario catalog
// and returns the home dir too — step 2b is only observable in the config
// that was actually PERSISTED.
func newEntitlementS68API(
	t *testing.T, overrides []config.ContextModelOverride, rows ...*config.ModelConfig,
) (*restAPI, string) {
	t.Helper()
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "unrelated", Model: "unrelated-model"}, rows)
	cfg.Context = config.DefaultContextSettings()
	cfg.Context.ModelOverrides = overrides
	api, tmpDir, _ := newProviderDeleteAPI(t, cfg)
	cat := catalog.Boot(context.Background(), entitlementS68CatalogJSON(),
		&stubCatalogPuller{data: entitlementS68CatalogJSON()}, nil, nil)
	require.NotNil(t, cat.Document(), "the T068-17 fixture document must parse")
	api.providerCatalog = cat
	registerEntitlementCacheInvalidation(cat, api)
	return api, tmpDir
}

// diskModelOverrides reads context.model_overrides back off disk as
// (provider, model) pairs, in persisted order.
func diskModelOverrides(t *testing.T, tmpDir string) [][2]string {
	t.Helper()
	m := diskConfig(t, tmpDir)
	ctxSettings, ok := m["context"].(map[string]any)
	require.True(t, ok, "config.json must carry the context section: %v", m["context"])
	list, ok := ctxSettings["model_overrides"].([]any)
	require.True(t, ok, "context.model_overrides must survive as a list: %v", ctxSettings)
	out := make([][2]string, 0, len(list))
	for _, item := range list {
		row, isMap := item.(map[string]any)
		require.True(t, isMap, "every override row is an object: %v", item)
		provider, _ := row["provider"].(string)
		model, _ := row["model"].(string)
		out = append(out, [2]string{provider, model})
	}
	return out
}

// TestEntitlement_IntersectsAndCaches is TDD row 23.
func TestEntitlement_IntersectsAndCaches(t *testing.T) {
	// ── US-7 scenario: "Check with my account greys unavailable models" ──
	t.Run("annotates the catalog, makes one upstream call, and the second click is cached", func(t *testing.T) {
		const ref = "openai_API_KEY"
		const secret = "sk-t068-17"
		t.Setenv(ref, secret)
		// The key reaches A and C but not B, and reports a Z the catalog
		// has never heard of.
		stub := newEntitlementStub(t, "A", "C", "Z")
		api, _ := newEntitlementS68API(t, nil,
			entitlementRow("openai", "openai-compatible", stub.srv.URL, ref, false))

		w := postEntitlement(t, api, "openai")
		first := entitlementBody(t, w)

		assert.False(t, first.Cached, "the first click is a live check")
		assert.Equal(t, 1, stub.calls(), "exactly one upstream request was made")
		require.Equal(t, []gen.EntitlementModel{
			{Id: "A", Entitled: true, Limits: gen.EntitlementModelLimitsKnown},
			{Id: "B", Entitled: false, Limits: gen.EntitlementModelLimitsKnown},
			{Id: "C", Entitled: true, Limits: gen.EntitlementModelLimitsKnown},
			{Id: "Z", Entitled: true, Limits: gen.EntitlementModelLimitsUnknown},
		}, first.Models,
			"the catalog rows come first in document order, then the models only the key reported")
		assert.False(t, first.CheckedAt.IsZero(), "checked_at carries the time of the live call")
		assert.NotContains(t, w.Body.String(), secret,
			"the operator's key never appears in the response")

		second := entitlementBody(t, postEntitlement(t, api, "openai"))
		assert.True(t, second.Cached, "a second click returns cached: true")
		assert.Equal(t, first.Models, second.Models, "the cached answer is the same answer")
		assert.Equal(t, first.CheckedAt.UTC(), second.CheckedAt.UTC(),
			"a cache hit repeats the ORIGINAL checked_at — the fact is as old as it is")
	})

	// ── X-03: the key derivation, and what is NOT in it ─────────────────
	t.Run("the entry is keyed on SHA-256(providerID + \":\" + ref name), never the secret", func(t *testing.T) {
		const ref = "openai_API_KEY"
		const secret = "sk-never-hashed"
		t.Setenv(ref, secret)
		stub := newEntitlementStub(t, "A")
		api, _ := newEntitlementS68API(t, nil,
			entitlementRow("openai", "openai-compatible", stub.srv.URL, ref, false))

		require.False(t, entitlementBody(t, postEntitlement(t, api, "openai")).Cached)

		want := sha256.Sum256([]byte("openai:" + ref))
		wantKey := hex.EncodeToString(want[:])
		_, ok := api.entitlements.get(wantKey)
		assert.True(t, ok, "the entry lives under SHA-256(providerID + \":\" + credentialRefName)")

		secretKeyed := sha256.Sum256([]byte("openai:" + secret))
		_, bySecret := api.entitlements.get(hex.EncodeToString(secretKeyed[:]))
		assert.False(t, bySecret, "hashing the secret value instead of the ref name is the bug X-03 forbids")

		api.entitlements.mu.Lock()
		keys := make([]string, 0, len(api.entitlements.entries))
		for k := range api.entitlements.entries {
			keys = append(keys, k)
		}
		api.entitlements.mu.Unlock()
		assert.Equal(t, []string{wantKey}, keys, "one check, one entry, one key")
	})

	// ── FR-010 steps 3b and 2b: DELETE evicts AND prunes ────────────────
	t.Run("a provider DELETE leaves no cache entry and no model_overrides row for the id", func(t *testing.T) {
		const ref = "openai_API_KEY"
		t.Setenv(ref, "sk-delete")
		stub := newEntitlementStub(t, "A", "C")
		api, home := newEntitlementS68API(t,
			[]config.ContextModelOverride{
				{Provider: "openai", Model: "A", ContextWindow: 32768},
				{Provider: "anthropic", Model: "claude-x", ContextWindow: 120000},
				{Provider: "openai", Model: "C", ContextWindow: 16384},
			},
			entitlementRow("openai", "openai-compatible", stub.srv.URL, ref, false),
			entitlementRow("anthropic", "anthropic", stub.srv.URL+"/v1", "anthropic_API_KEY", false))

		require.False(t, entitlementBody(t, postEntitlement(t, api, "openai")).Cached)
		key := entitlementCacheKey("openai", ref)
		_, primed := api.entitlements.get(key)
		require.True(t, primed, "the first check must populate the cache")
		require.Len(t, diskModelOverrides(t, home), 3, "the fixture seeds three overrides")

		w := doProviderDelete(t, api, "openai", "", nil, true)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		_, survived := api.entitlements.get(key)
		assert.False(t, survived,
			"FR-010 step 3b: the removed provider's entitlement answer must not outlive the row")
		assert.Equal(t, [][2]string{{"anthropic", "claude-x"}}, diskModelOverrides(t, home),
			"FR-010 step 2b (cross-spec Q3): every model_overrides row for the id goes, and only those")
		assert.NotContains(t, diskProviderIDs(t, home), "openai",
			"the provider row itself is gone — the pruning is not a substitute for step 2")
	})

	// ── X-21: which PUT evicts, and which deliberately does not ─────────
	t.Run("a key-changing PUT evicts; a PUT that only bumps updated_at does not", func(t *testing.T) {
		// The credential ref name is the one the PUT itself writes
		// (`<id>_API_KEY`), so the derived key is IDENTICAL before and
		// after the rotation: only the eviction can turn the next check
		// into a miss. Keyed on any other ref name this sub-test would
		// still pass with the eviction deleted.
		const ref = "openai_API_KEY"
		t.Setenv(ref, "sk-before")
		stub := newEntitlementStub(t, "A", "C")
		api, _ := newEntitlementS68API(t, nil,
			entitlementRow("openai", "openai-compatible", stub.srv.URL, ref, false))
		key := entitlementCacheKey("openai", ref)

		require.False(t, entitlementBody(t, postEntitlement(t, api, "openai")).Cached)
		require.Equal(t, 1, stub.calls())

		putProviderForEntitlement(t, api, "openai", `{"models":["A","B","C"]}`)
		_, keptOnModelEdit := api.entitlements.get(key)
		assert.True(t, keptOnModelEdit,
			"a PUT that changes no key is not an eviction — the answer is still true of the same key")
		assert.True(t, entitlementBody(t, postEntitlement(t, api, "openai")).Cached)
		assert.Equal(t, 1, stub.calls(), "and it costs no upstream request")

		putProviderForEntitlement(t, api, "openai", `{"api_key":"sk-after"}`)
		require.Equal(t, ref, api.configuredProviderRow("openai").APIKeyRef,
			"the rotation must keep the ref NAME stable, or this sub-test proves nothing")
		_, survivedRotation := api.entitlements.get(key)
		assert.False(t, survivedRotation, "a key-changing PUT drops the entry")
		assert.False(t, entitlementBody(t, postEntitlement(t, api, "openai")).Cached,
			"what a different key can reach is a different fact")
		assert.Equal(t, 2, stub.calls(), "so the next check dials upstream again")
	})

	// ── US-7 scenario: "Check with my account upstream failure" ─────────
	t.Run("an upstream 429 answers S67's 502 body and greys nothing", func(t *testing.T) {
		const ref = "openai_API_KEY"
		t.Setenv(ref, "sk-429")
		stub := newEntitlementStub(t, "A", "C")
		stub.setStatus(http.StatusTooManyRequests)
		api, _ := newEntitlementS68API(t, nil,
			entitlementRow("openai", "openai-compatible", stub.srv.URL, ref, false))

		w := postEntitlement(t, api, "openai")

		require.Equal(t, http.StatusBadGateway, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, "could not fetch upstream model list: status 429",
			entitlementErrorBody(t, w).Error,
			"the inline warning renders this exact upstream fact")
		assert.NotContains(t, w.Body.String(), `"models"`,
			"a failed check annotates nothing — the SPA keeps the catalog list unchanged")
		_, cached := api.entitlements.get(entitlementCacheKey("openai", ref))
		assert.False(t, cached, "a failure is never cached")
	})
}
