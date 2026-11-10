package catalog

// effort_red_served_reasoning_test.go — RED tests for the served reasoning
// catalog (T-K1): S-45 (the served objects carry levels and switch, filtered
// by the encoder table) and S-46 (the vendored catalog carries reasoning
// data).
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: S-45, S-46, FR-037a, FR-049, SC-008, US-8 AS1-3, T-K1.
//
// RED reason: the catalog parse (ParseDocument) and the served builder
// (buildServed/providerToJSON/modelJSON) carry no reasoning member at all, so
// a document whose model rows carry upstream reasoning objects serves NONE of
// them: rows (a)-(e) of the S-45 matrix come out absent instead of served,
// and the vendored file counts zero reasoning rows. The absence row (f) and
// the banned-keys check are PINs: absent stays absent and no budget,
// controls_published or reasoning_route key exists today — they must still
// hold at GREEN (FR-037a: no budget encoder; revision 1's reasoning_route is
// withdrawn).

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// servedReasoningDoc returns a valid 2.0.0 document carrying the S-45 matrix
// rows: (a) levels+switch on OpenRouter; (b) switch-only on OpenRouter;
// (c) budget-only; (d) reasoning-flagged with no control; (e) levels plus an
// upstream switch on a generic OpenAI-compatible provider whose route cannot
// encode the switch; (f) no reasoning data.
func servedReasoningDoc(t *testing.T) []byte {
	t.Helper()
	doc := map[string]any{
		"schema_version": "2.0.0",
		"version":        "v2026.10.1",
		"updated_at":     "2026-10-30T06:00:00Z",
		"source":         "red-test",
		"default_resize_limits": map[string]any{
			"long_edge_px": 7680,
			"max_bytes":    10485760,
		},
		"providers": []map[string]any{
			{
				"id":           "openrouter",
				"name":         "OpenRouter",
				"company":      "OpenRouter",
				"api":          "https://openrouter.ai/api/v1",
				"protocol":     "openai-compatible",
				"protocols":    []map[string]any{{"protocol": "openai-compatible", "api": "https://openrouter.ai/api/v1"}},
				"tier":         "popular",
				"auth_methods": []string{"api_key"},
				"aliases":      []string{},
				"models": []map[string]any{
					{
						"id": "deepseek-v4.1-flash", "name": "DeepSeek V4.1 Flash",
						"context_window": 128000, "max_output_tokens": 16384,
						"input_modalities": []string{"text"}, "tool_call": true, "status": "active",
						"reasoning": map[string]any{"levels": []string{"low", "medium", "high"}, "switch": true},
					},
					{
						"id": "minimax-m3", "name": "MiniMax M3",
						"context_window": 200000, "max_output_tokens": 65536,
						"input_modalities": []string{"text"}, "tool_call": true, "status": "active",
						"reasoning": map[string]any{"levels": []any{}, "switch": true},
					},
					{
						"id": "budget-only-m", "name": "Budget Only",
						"context_window": 32000, "max_output_tokens": 8192,
						"input_modalities": []string{"text"}, "tool_call": true, "status": "active",
						"reasoning": map[string]any{"budget": true},
					},
					{
						"id": "flagged-m", "name": "Flagged No Control",
						"context_window": 32000, "max_output_tokens": 8192,
						"input_modalities": []string{"text"}, "tool_call": true, "status": "active",
						"reasoning": map[string]any{},
					},
					{
						"id": "no-reasoning-m", "name": "No Reasoning Data",
						"context_window": 32000, "max_output_tokens": 8192,
						"input_modalities": []string{"text"}, "tool_call": true, "status": "active",
					},
				},
			},
			{
				"id":           "example-compat",
				"name":         "Example Compat",
				"company":      "Example",
				"api":          "https://example.test/api/v1",
				"protocol":     "openai-compatible",
				"protocols":    []map[string]any{{"protocol": "openai-compatible", "api": "https://example.test/api/v1"}},
				"tier":         "standard",
				"auth_methods": []string{"api_key"},
				"aliases":      []string{},
				"models": []map[string]any{
					{
						"id": "generic-levels-m", "name": "Generic Levels",
						"context_window": 64000, "max_output_tokens": 8192,
						"input_modalities": []string{"text"}, "tool_call": true, "status": "active",
						"reasoning": map[string]any{"levels": []string{"low", "high"}, "switch": true},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	return raw
}

// servedModelReasoning finds a provider's model row in the served body and
// returns its decoded reasoning member (nil when the key is absent).
func servedModelReasoning(t *testing.T, body []byte, providerID, modelID string) (map[string]any, bool) {
	t.Helper()
	var env struct {
		Providers []struct {
			ID     string `json:"id"`
			Models []struct {
				ID        string          `json:"id"`
				Reasoning json.RawMessage `json:"reasoning"`
			} `json:"models"`
		} `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(body, &env))
	for _, p := range env.Providers {
		if p.ID != providerID {
			continue
		}
		for _, m := range p.Models {
			if m.ID != modelID {
				continue
			}
			if len(m.Reasoning) == 0 {
				return nil, false
			}
			var r map[string]any
			require.NoError(t, json.Unmarshal(m.Reasoning, &r))
			return r, true
		}
	}
	t.Fatalf("served body has no row for %s/%s", providerID, modelID)
	return nil, false
}

// TestT_K1_S45_ServedReasoningFilteredByEncoderTable pins the S-45 matrix:
// the served catalog carries levels and switch, filtered by the encoder
// table, with the upstream switch removed on routes that cannot encode it,
// muted objects for budget-only and flagged-no-control rows, level order as
// published, schema_version unchanged, and no budget, controls_published or
// reasoning_route key anywhere.
// Traces to: S-45, FR-037a, FR-049, US-8 AS1-2, T-K1.
func TestT_K1_S45_ServedReasoningFilteredByEncoderTable(t *testing.T) {
	c := New()
	require.NoError(t, c.Apply(servedReasoningDoc(t)))
	s, ok := c.Served()
	require.True(t, ok)

	rows := []struct {
		name, provider, model string
		present               bool
		want                  map[string]any
	}{
		{"a", "openrouter", "deepseek-v4.1-flash", true, map[string]any{"levels": []any{"low", "medium", "high"}, "switch": true}},
		{"b", "openrouter", "minimax-m3", true, map[string]any{"levels": []any{}, "switch": true}},
		{"c", "openrouter", "budget-only-m", true, map[string]any{"levels": []any{}, "switch": false}},
		{"d", "openrouter", "flagged-m", true, map[string]any{"levels": []any{}, "switch": false}},
		{"e", "example-compat", "generic-levels-m", true, map[string]any{"levels": []any{"low", "high"}, "switch": false}},
		{"f", "openrouter", "no-reasoning-m", false, nil},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got, present := servedModelReasoning(t, s.Body, row.provider, row.model)
			assert.Equal(t, row.present, present)
			assert.Equal(t, row.want, got, "exact whole served object: order, non-null [], filtered switch and no unknown keys")
		})
	}

	// schema_version unchanged; the banned keys appear nowhere.
	var envRaw map[string]any
	require.NoError(t, json.Unmarshal(s.Body, &envRaw))
	assert.Equal(t, "2.0.0", envRaw["schema_version"], "schema_version unchanged (S-45)")
	banned := []string{"budget", "controls_published", "reasoning_route"}
	for _, key := range banned {
		assert.NotContains(t, string(s.Body), `"`+key+`"`,
			"no %q key appears anywhere in the served body (S-45/FR-037a)", key)
	}
}

// TestT_K1_S46_VendoredCatalogCarriesReasoning pins SC-008/S-46 on the
// committed catalog: more than zero model rows carry a reasoning object, the
// openai-chatgpt row's copied objects are among them, and OpenRouter's
// deepseek/deepseek-v4.1-flash carries levels none, low, high, max while
// minimax/minimax-m3 carries the switch.
// Traces to: S-46, SC-008, US-8 AS3, T-K1.
func TestT_K1_S46_VendoredCatalogCarriesReasoning(t *testing.T) {
	raw, err := os.ReadFile("data/providers_catalog.json")
	require.NoError(t, err)
	var doc struct {
		Providers []struct {
			ID     string `json:"id"`
			Models []struct {
				ID        string          `json:"id"`
				Reasoning json.RawMessage `json:"reasoning"`
			} `json:"models"`
		} `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	count := 0
	chatgptHas := false
	var deepseekLevels []string
	minimaxSwitch := false
	for _, p := range doc.Providers {
		for _, m := range p.Models {
			if len(m.Reasoning) > 0 && string(m.Reasoning) != "null" {
				count++
				if p.ID == "openai-chatgpt" {
					chatgptHas = true
				}
			}
			if p.ID == "openrouter" && m.ID == "deepseek/deepseek-v4.1-flash" {
				// At RED the row carries no reasoning member at all; leave the
				// levels unset so the failure lands on the assertion below.
				var r struct {
					Levels []string `json:"levels"`
					Switch bool     `json:"switch"`
				}
				if len(m.Reasoning) > 0 {
					require.NoError(t, json.Unmarshal(m.Reasoning, &r))
					deepseekLevels = r.Levels
				}
			}
			if p.ID == "openrouter" && m.ID == "minimax/minimax-m3" {
				var r struct {
					Switch bool `json:"switch"`
				}
				if len(m.Reasoning) > 0 {
					require.NoError(t, json.Unmarshal(m.Reasoning, &r))
					minimaxSwitch = r.Switch
				}
			}
		}
	}
	assert.Greater(t, count, 0,
		"the vendored catalog carries reasoning data (S-46/SC-008) — RED: zero reasoning objects in the committed file")
	assert.True(t, chatgptHas,
		"the openai-chatgpt row's copied reasoning objects are included (S-46)")
	assert.Equal(t, []string{"none", "low", "high", "max"}, deepseekLevels,
		"openrouter deepseek/deepseek-v4.1-flash carries levels none, low, high, max (S-46/SC-008); got %v", deepseekLevels)
	assert.True(t, minimaxSwitch,
		"openrouter minimax/minimax-m3 carries the switch (S-46/SC-008)")
}
