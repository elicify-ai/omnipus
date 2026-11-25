package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Oracle: #1968 spec §4.3's literal numbers (research 2026-11-22), not
// defaults read from the implementation. Equal researched model rows are
// represented by prefixes. Vertex Claude and gateway '~' aliases were in the
// read-only researched seed and retain their original field values (R5).
func TestPDFLimitsConfig_ShippedExactlySpec43(t *testing.T) {
	var raw struct {
		Default   map[string]any   `yaml:"default"`
		Overrides []map[string]any `yaml:"overrides"`
	}
	require.NoError(t, yaml.Unmarshal(shippedPDFLimitsYAML, &raw))
	assertPDFSeedJSON(t, `{"max_file_bytes":50000000,"max_total_file_bytes":50000000,"max_pages_per_file":100,"max_documents_per_request":5}`, raw.Default)
	type key struct{ provider, model string }
	want := make(map[key]string)
	// Provider ids are exact; provider patterns are deliberately not supported.
	for _, p := range []string{"openai", "azure", "azure-cognitive-services", "zai", "zhipuai", "zai-coding-plan", "zhipuai-coding-plan"} {
		want[key{p, ""}] = `{"max_file_bytes":50000000,"max_total_file_bytes":50000000,"max_pages_per_file":100,"max_documents_per_request":5,"max_pages_per_request":100}`
	}
	want[key{"anthropic", ""}] = `{"max_request_bytes":128000000,"max_pages_per_request":100}`
	want[key{"amazon-bedrock", ""}] = `{"max_file_bytes":4500000,"max_documents_per_message":5,"max_request_bytes":20000000}`
	want[key{"google-vertex-anthropic", ""}] = `{"max_file_bytes":20000000,"max_total_file_bytes":20000000,"max_request_bytes":30000000,"max_pages_per_request":100}`
	want[key{"google", ""}] = `{"max_pages_per_file":1000,"max_pages_per_request":1000,"max_request_bytes":100000000}`
	want[key{"google-vertex", ""}] = `{"max_file_bytes":52428800,"max_pages_per_file":1000}`
	want[key{"perplexity-agent", ""}] = `{"max_total_file_bytes":52428800,"max_documents_per_request":30,"max_request_bytes":75497472}`
	for _, p := range []string{"xai", "meta", "deepseek", "moonshotai", "moonshotai-cn", "kimi-code-plan-cn", "kimi-code-plan-global", "kimi-for-coding"} {
		want[key{p, ""}] = `{"inline_pdf":false}`
	}
	for _, m := range []string{"anthropic.claude-*", "in.anthropic.claude-*"} {
		want[key{"amazon-bedrock", m}] = `{"max_pages_per_request":100}`
	}
	for _, p := range []string{"azure", "azure-cognitive-services"} {
		want[key{p, "claude-*"}] = `{"max_file_bytes":20000000,"max_total_file_bytes":20000000,"max_request_bytes":32000000}`
	}
	want[key{"google-vertex", "claude-*"}] = `{"max_file_bytes":20000000,"max_total_file_bytes":20000000,"max_request_bytes":30000000,"max_pages_per_request":100}`
	for _, m := range []string{"anthropic/*", "~anthropic/*"} {
		want[key{"openrouter", m}] = `{"max_request_bytes":128000000,"max_pages_per_request":100}`
	}
	for _, m := range []string{"x-ai/*", "~x-ai/*", "meta-llama/*"} {
		want[key{"openrouter", m}] = `{"inline_pdf":false}`
	}
	require.Len(t, raw.Overrides, len(want), "no extra/missing shipped research rows")
	seen := make(map[key]bool)
	for _, row := range raw.Overrides {
		p, ok := row["provider"].(string)
		require.True(t, ok)
		m, _ := row["model"].(string)
		k := key{p, m}
		expected, ok := want[k]
		require.True(t, ok, "unexpected shipped row %v", k)
		require.False(t, seen[k], "duplicate shipped row %v", k)
		seen[k] = true
		delete(row, "provider")
		delete(row, "model")
		t.Run(p+"/"+m, func(t *testing.T) { assertPDFSeedJSON(t, expected, row) })
	}

	// The assumption is documentation, so inspect YAML comments rather than
	// claiming this source-text assertion proves runtime behavior.
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(shippedPDFLimitsYAML, &doc))
	marked := make(map[string]bool)
	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			provider := ""
			for i := 0; i < len(n.Content); i += 2 {
				if n.Content[i].Value == "provider" {
					provider = n.Content[i+1].Value
				}
			}
			if provider != "" {
				for i := 0; i < len(n.Content); i += 2 {
					if n.Content[i].Value == "max_pages_per_request" && n.Content[i+1].LineComment == "# [ASSUMED]" {
						marked[provider] = true
					}
				}
			}
		}
		for _, child := range n.Content {
			walk(child)
		}
	}
	walk(&doc)
	assert.Equal(t, map[string]bool{"openai": true, "azure": true, "azure-cognitive-services": true, "zai": true, "zhipuai": true, "zai-coding-plan": true, "zhipuai-coding-plan": true}, marked)
}

func assertPDFSeedJSON(t *testing.T, expected string, row map[string]any) {
	t.Helper()
	actual, err := json.Marshal(row)
	require.NoError(t, err)
	assert.JSONEq(t, expected, string(actual))
}

func TestPDFLimitsConfig_ShippedFamilyBehavior(t *testing.T) {
	// Tuple order: file bytes, total file bytes, request bytes, pages per file,
	// pages per request, documents per wire message, documents per request.
	// Zero in this expected tuple means absent, not explicitly unlimited.
	for _, tc := range []struct {
		provider, model string
		want            [7]int64
		inlineFalse     bool
	}{
		{"unknown", "m", [7]int64{50000000, 50000000, 0, 100, 0, 0, 5}, false},
		{"openai", "gpt", [7]int64{50000000, 50000000, 0, 100, 100, 0, 5}, false},
		{"anthropic", "claude", [7]int64{50000000, 50000000, 128000000, 100, 100, 0, 5}, false},
		{"amazon-bedrock", "other", [7]int64{4500000, 50000000, 20000000, 100, 0, 5, 5}, false},
		{"amazon-bedrock", "anthropic.claude-opus-5-5", [7]int64{4500000, 50000000, 20000000, 100, 100, 5, 5}, false},
		{"amazon-bedrock", "in.anthropic.claude-sonnet-5", [7]int64{4500000, 50000000, 20000000, 100, 100, 5, 5}, false},
		{"azure", "claude-opus-5-5", [7]int64{20000000, 20000000, 32000000, 100, 100, 0, 5}, false},
		{"azure-cognitive-services", "claude-sonnet-5", [7]int64{20000000, 20000000, 32000000, 100, 100, 0, 5}, false},
		{"google-vertex", "gemini", [7]int64{52428800, 50000000, 0, 1000, 0, 0, 5}, false},
		{"google-vertex", "claude-sonnet-5@default", [7]int64{20000000, 20000000, 30000000, 1000, 100, 0, 5}, false},
		{"google-vertex-anthropic", "claude", [7]int64{20000000, 20000000, 30000000, 100, 100, 0, 5}, false},
		{"google", "gemini", [7]int64{50000000, 50000000, 100000000, 1000, 1000, 0, 5}, false},
		{"perplexity-agent", "m", [7]int64{50000000, 52428800, 75497472, 100, 0, 0, 30}, false},
		{"openrouter", "anthropic/claude-opus-5", [7]int64{50000000, 50000000, 128000000, 100, 100, 0, 5}, false},
		{"openrouter", "~anthropic/claude-opus-latest", [7]int64{50000000, 50000000, 128000000, 100, 100, 0, 5}, false},
		{"openrouter", "x-ai/grok", [7]int64{50000000, 50000000, 0, 100, 0, 0, 5}, true},
		{"openrouter", "~x-ai/grok-latest", [7]int64{50000000, 50000000, 0, 100, 0, 0, 5}, true},
		{"openrouter", "meta-llama/llama", [7]int64{50000000, 50000000, 0, 100, 0, 0, 5}, true},
		{"xai", "grok", [7]int64{50000000, 50000000, 0, 100, 0, 0, 5}, true},
	} {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			limits := (&Config{}).ResolvePDFLimits(tc.provider, tc.model)
			for i, get := range pdfLimitsGetters() {
				got, stated := get(limits)
				assert.Equal(t, tc.want[i], got, pdfLimitIntegerNames()[i])
				assert.Equal(t, tc.want[i] > 0, stated, pdfLimitIntegerNames()[i])
			}
			inline, stated := limits.InlinePDF()
			assert.False(t, inline, "no shipped row explicitly states true")
			assert.Equal(t, tc.inlineFalse, stated)
		})
	}
}
