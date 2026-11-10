package providers

// effort_red_codex_recap_test.go — RED test for the ChatGPT-login recap
// output-limit rule.
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: S-89 (also the S-71 ChatGPT-login row's body half), TD-2,
// FR-070, S-80's output-cap adapter table row for Codex Responses.
//
// RED reason: buildCodexParams reads only prompt_cache_key today — a
// configured effort is never placed in the request and (as today) no
// output-token limit is sent.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestT_C5_S89_CodexRecapCarriesEffortAndNoOutputLimit pins the serialised
// Codex Responses shape for a recap request with a configured effort and a
// configured output cap: the effort is carried (reasoning.effort) and NO
// output-token limit field appears.
// Traces to: S-89, S-71 ChatGPT-login row, S-80 adapter table (Codex row),
// TD-2, FR-070.
func TestT_C5_S89_CodexRecapCarriesEffortAndNoOutputLimit(t *testing.T) {
	opts := map[string]any{
		"max_tokens":       4096, // recap_max_output_tokens — must NOT reach this route (TD-2)
		"reasoning_effort": "high",
	}
	params := buildCodexParams(nil, nil, "gpt-6.1-sol", opts, false)
	raw, err := json.Marshal(params)
	require.NoError(t, err, "params must marshal (the SDK serialises this body)")

	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))

	effort, present := bodyFindPath(body, "reasoning.effort")
	require.True(t, present, "the Codex body carries reasoning.effort (S-89)")
	assert.Equal(t, "high", effort)

	for _, banned := range []string{"max_tokens", "max_output_tokens", "max_completion_tokens"} {
		_, present := body[banned]
		assert.False(t, present, "%s must be absent on the ChatGPT-login route (S-89, TD-2)", banned)
	}
}

// TestT_C5_S89_CodexRecapDefaultOff pins the S-71 ChatGPT-login row: with no
// recap effort configured the default Off (none) is sent as reasoning.effort
// none, and still no output limit.
// Traces to: S-71 ChatGPT-login row, S-89, FR-070.
func TestT_C5_S89_CodexRecapDefaultOff(t *testing.T) {
	opts := map[string]any{
		"max_tokens":       2048,
		"reasoning_effort": "none",
	}
	params := buildCodexParams(nil, nil, "gpt-6.1-sol", opts, false)
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	effort, present := bodyFindPath(body, "reasoning.effort")
	require.True(t, present, "default Off reaches the Codex route as reasoning.effort none (S-71)")
	assert.Equal(t, "none", effort)
	_, hasMaxTokens := body["max_tokens"]
	assert.False(t, hasMaxTokens, "no output-token limit on ChatGPT login (S-71, S-89)")
}

// bodyFindPath looks up a dotted path in a decoded JSON object.
func bodyFindPath(body map[string]any, path string) (any, bool) {
	var cur any = body
	start := 0
	for {
		idx := -1
		for i := start; i < len(path); i++ {
			if path[i] == '.' {
				idx = i
				break
			}
		}
		var key string
		if idx < 0 {
			key = path[start:]
		} else {
			key = path[start:idx]
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[key]
		if !ok {
			return nil, false
		}
		if idx < 0 {
			return cur, true
		}
		start = idx + 1
	}
}

// S-17/S-19: this route has one Responses builder, not a second stream builder.
func TestT_C5_S17_S19_CodexRequestMatrix(t *testing.T) {
	for _, effort := range []string{"high", "provider_default", ""} {
		t.Run(effort, func(t *testing.T) {
			params := buildCodexParams([]Message{{Role: "user", Content: "codex wire"}}, nil, "gpt-6.1-sol", map[string]any{"reasoning_effort": effort, "max_tokens": 4096}, false)
			raw, err := json.Marshal(params)
			require.NoError(t, err)
			var body map[string]any
			require.NoError(t, json.Unmarshal(raw, &body))
			if effort == "high" {
				assert.Equal(t, map[string]any{"effort": "high"}, body["reasoning"])
			} else {
				assert.NotContains(t, body, "reasoning")
			}
			for _, key := range []string{"reasoning_effort", "thinking", "output_config", "max_tokens", "max_output_tokens", "max_completion_tokens"} {
				assert.NotContains(t, body, key)
			}
		})
	}
}
