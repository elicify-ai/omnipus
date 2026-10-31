package promptcache_test

// effort_red_usage_parse_test.go — RED tests for the reasoning-token parse
// into UsageInfo (FR-024): completion_tokens_details.reasoning_tokens on
// OpenAI-compatible providers and output_tokens_details.reasoning_tokens on
// Responses.
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: S-43, S-44, D7 rows 1-6, FR-024, T-U1.
//
// RED reason: NormalizeUsage parses the documented token counters but has no
// reasoning-token output today, so every assertion on the parsed
// reasoning_tokens JSON member fails; the absent-count pin (row 5) passes at
// baseline by design.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/promptcache"
)

func normalizeJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	u, err := promptcache.NormalizeUsage("openrouter", []byte(raw))
	require.NoError(t, err, "the documented usage object parses")
	out, err := json.Marshal(u)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))
	return m
}

// TestT_U1_S43_NormalizeUsageParsesReasoningTokens walks D7 rows 1-6 at the
// parse seam: the reasoning count is carried as its own JSON member (never
// folded into completion or total tokens), reported zero stays present, an
// over-report is capped at the output count, and an absent count leaves the
// member absent.
// Traces to: S-43, S-44, D7 rows 1-6, FR-024.
func TestT_U1_S43_NormalizeUsageParsesReasoningTokens(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    any    // expected reasoning_tokens JSON value; nil = key must be absent
		wantOut any    // expected completion_tokens value
	}{
		{
			name:    "D7 row 1: reported 120 carried, output and total unchanged",
			raw:     `{"prompt_tokens":10,"completion_tokens":180,"total_tokens":190,"completion_tokens_details":{"reasoning_tokens":120}}`,
			want:    float64(120),
			wantOut: float64(180),
		},
		{
			name:    "D7 row 2: reported zero stays present as zero",
			raw:     `{"prompt_tokens":10,"completion_tokens":180,"total_tokens":190,"completion_tokens_details":{"reasoning_tokens":0}}`,
			want:    float64(0),
			wantOut: float64(180),
		},
		{
			name:    "D7 row 3: reported 180 equal to output",
			raw:     `{"prompt_tokens":10,"completion_tokens":180,"total_tokens":190,"completion_tokens_details":{"reasoning_tokens":180}}`,
			want:    float64(180),
			wantOut: float64(180),
		},
		{
			name:    "D7 row 4: over-report capped at the output count",
			raw:     `{"prompt_tokens":10,"completion_tokens":180,"total_tokens":190,"completion_tokens_details":{"reasoning_tokens":181}}`,
			want:    float64(180),
			wantOut: float64(180),
		},
		{
			name:    "D7 row 5: absent count leaves the member absent (never 0, never null)",
			raw:     `{"prompt_tokens":10,"completion_tokens":180,"total_tokens":190}`,
			want:    nil,
			wantOut: float64(180),
		},
		{
			name:    "Responses variant: output_tokens_details.reasoning_tokens",
			raw:     `{"prompt_tokens":10,"completion_tokens":180,"total_tokens":190,"output_tokens_details":{"reasoning_tokens":120}}`,
			want:    float64(120),
			wantOut: float64(180),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := normalizeJSON(t, tc.raw)
			got, present := m["reasoning_tokens"]
			if tc.want == nil {
				assert.False(t, present, "an absent count must not appear (never 0, never null) (D7 row 5) — PIN")
			} else {
				require.True(t, present, "the parsed usage carries reasoning_tokens (S-43/FR-024) — RED: no reasoning member exists")
				assert.Equal(t, tc.want, got)
			}
			assert.Equal(t, tc.wantOut, m["completion_tokens"], "output tokens unchanged by the reasoning parse (SC-011)")
			assert.Equal(t, float64(190), m["total_tokens"], "totals unchanged (SC-011)")
		})
	}
}

// TestT_U1_S43_ParseUsagePointer pinS FR-024's pointer contract through
// ParseUsage: a report with reasoning details returns a usage whose JSON
// carries the count; a null/empty raw returns nil usage (absent stays
// absent).
// Traces to: S-43, S-44, FR-024.
func TestT_U1_S43_ParseUsagePointer(t *testing.T) {
	u, err := promptcache.ParseUsage("openrouter", []byte(`{"completion_tokens":180,"total_tokens":190,"completion_tokens_details":{"reasoning_tokens":120}}`))
	require.NoError(t, err)
	require.NotNil(t, u)
	raw, err := json.Marshal(u)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	assert.Equal(t, float64(120), m["reasoning_tokens"], "ParseUsage carries the reasoning count — RED")

	nilU, err := promptcache.ParseUsage("openrouter", []byte(`null`))
	require.NoError(t, err)
	assert.Nil(t, nilU, "a missing usage object is distinct from a known zero split — PIN")
}
