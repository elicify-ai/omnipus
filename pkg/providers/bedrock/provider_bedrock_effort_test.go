// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/thinking-reasoning-spec.md — D32 adapter coverage
// matrix (Amazon Bedrock row) and Section 16 test 43.
//
// The Bedrock Converse adapter translates llmOpts["reasoning_effort"] (WP-G's
// C5 output, pkg/agent/loop_run_turn.go::prepareLLMRequest) into
// model-family-shaped additionalModelRequestFields exactly:
//
//   - Anthropic-family Bedrock models (catalog ids prefixed "anthropic.",
//     e.g. anthropic.claude-opus-4-6-v1): a "thinking" object of exactly
//     {"type": "adaptive", "display": "summarized"} plus
//     "output_config.effort" set to the level — the current Anthropic
//     request shape (adaptive thinking + output_config.effort; SDK
//     v1.48.0 ThinkingConfigAdaptiveParam) passed through as model-native
//     fields, per AWS's Converse additionalModelRequestFields. The spec's
//     ban (D6, AS8) is a user-facing raw budget_tokens input, not this
//     wire-level thinking type — spec MIN-005.
//   - Nova-family models (catalog ids prefixed "amazon.", e.g.
//     amazon.nova-lite-v1:0): a "reasoningConfig" object whose
//     "maxReasoningEffort" is the level.
//
// The unset token "default", an absent option, and a stale level no longer
// routable omit the ENTIRE additionalModelRequestFields key from the wire
// JSON — the whole key, never an empty-object placeholder (D9), and a stale
// level degrades to the provider default instead of blocking the turn (T1 /
// SC-007). The builder must never panic on an arbitrary stored string.
//
// The two positive rows use DIFFERENT named levels ("high" vs "low") so a
// hardcoded single-level mapping dies on one family or the other.
//
// RED for the adaptive-shape fix: the adapter still sent the legacy
// {"type": "enabled", "budget_tokens": N} thinking object, so the
// Anthropic-family positive rows fail on exact-shape equality.
package bedrock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// converseRequestBody drives one real Provider.Chat against an httptest
// server (the same boundary TestChat_RequestResponseAndBearerAuthentication
// uses) and decodes the raw Converse request body into a map — assertions pin
// AWS's wire keys, not the Go struct's field names.
func converseRequestBody(t *testing.T, model string, options map[string]any) map[string]any {
	t.Helper()
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding converse request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}`))
	}))
	t.Cleanup(server.Close)

	p, err := NewProvider("fake-bedrock-key", WithBaseEndpoint(server.URL))
	require.NoError(t, err)
	_, err = p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, model, options)
	require.NoError(t, err)
	return gotBody
}

// TestConverseRequest_EffortAdditionalModelRequestFields is spec test 43:
// table-driven Anthropic + Nova cases assert the exact
// additionalModelRequestFields shape for a named level and complete omission
// for Default (and, per T1, for an absent/stale effort).
func TestConverseRequest_EffortAdditionalModelRequestFields(t *testing.T) {
	// Model ids are real catalog rows (pkg/providers/catalog/data/
	// providers_catalog.json), not invented shapes.
	const anthropicModel = "anthropic.claude-opus-4-6-v1"
	const novaModel = "amazon.nova-lite-v1:0"

	cases := []struct {
		name    string
		model   string
		options map[string]any
		family  string // "anthropic" | "nova" — the shape to assert
		level   string // "" = the whole additionalModelRequestFields key must be absent
	}{
		{
			name:    "Anthropic family: high carries thinking plus output_config.effort",
			model:   anthropicModel,
			options: map[string]any{"reasoning_effort": "high"},
			family:  "anthropic",
			level:   "high",
		},
		{
			name:    "Anthropic family: absent effort omits additionalModelRequestFields entirely",
			model:   anthropicModel,
			options: map[string]any{},
			level:   "",
		},
		{
			name:    "Anthropic family: Default omits additionalModelRequestFields entirely",
			model:   anthropicModel,
			options: map[string]any{"reasoning_effort": "default"},
			level:   "",
		},
		{
			name:    "Anthropic family: stale level degrades to omission and does not panic",
			model:   anthropicModel,
			options: map[string]any{"reasoning_effort": "ultra"},
			level:   "",
		},
		{
			name:    "Nova family: low carries reasoningConfig.maxReasoningEffort",
			model:   novaModel,
			options: map[string]any{"reasoning_effort": "low"},
			family:  "nova",
			level:   "low",
		},
		{
			name:    "Nova family: absent effort omits additionalModelRequestFields entirely",
			model:   novaModel,
			options: map[string]any{},
			level:   "",
		},
		{
			name:    "Nova family: Default omits additionalModelRequestFields entirely",
			model:   novaModel,
			options: map[string]any{"reasoning_effort": "default"},
			level:   "",
		},
		{
			name:    "Nova family: stale level degrades to omission and does not panic",
			model:   novaModel,
			options: map[string]any{"reasoning_effort": "ultra"},
			level:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := converseRequestBody(t, tc.model, tc.options)

			if tc.level == "" {
				require.NotContains(t, body, "additionalModelRequestFields",
					"absent/Default/stale effort must omit the WHOLE additionalModelRequestFields key (D9/T1) — never an empty object")
				return
			}

			require.Contains(t, body, "additionalModelRequestFields",
				"a selected level must send additionalModelRequestFields")
			fields, ok := body["additionalModelRequestFields"].(map[string]any)
			require.True(t, ok, "additionalModelRequestFields must be a JSON object, got %T", body["additionalModelRequestFields"])

			switch tc.family {
			case "anthropic":
				thinking, ok := fields["thinking"].(map[string]any)
				require.True(t, ok, "Anthropic-family models must carry thinking in additionalModelRequestFields (D32), got %T", fields["thinking"])
				assert.Equal(t, map[string]any{"type": "adaptive", "display": "summarized"}, thinking,
					"thinking must be exactly {type: adaptive, display: summarized} (D32/D18, spec MIN-005) — no budget_tokens, no other keys")
				outputConfig, ok := fields["output_config"].(map[string]any)
				require.True(t, ok, "output_config must be a JSON object, got %T", fields["output_config"])
				assert.Equal(t, map[string]any{"effort": tc.level}, outputConfig,
					"output_config must be exactly {effort: <level>}")
			case "nova":
				reasoningConfig, ok := fields["reasoningConfig"].(map[string]any)
				require.True(t, ok, "Nova models must carry reasoningConfig in additionalModelRequestFields (D32), got %T", fields["reasoningConfig"])
				assert.Equal(t, tc.level, reasoningConfig["maxReasoningEffort"],
					"reasoningConfig.maxReasoningEffort must carry the selected level exactly")
			default:
				t.Fatalf("unknown family %q in test table", tc.family)
			}
		})
	}
}
