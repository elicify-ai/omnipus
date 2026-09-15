// config_agents_test.go: tests for agent configuration: the AgentConfig types and their helpers

package config

import (
	"testing"
)

// --- moved from config.go tests 2026-09-15 ---

// TestNormalizeFallbacks_EmptyAndPassThrough covers the trivial cases of
// NormalizeFallbacks: nil in → nil out; already-resolved entries pass
// through unchanged.
func TestNormalizeFallbacks_EmptyAndPassThrough(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{
			{
				Name:     "glm-5.2",
				Model:    "z-ai/glm-5.2",
				Provider: "openrouter",
				APIBase:  "https://openrouter.ai/api/v1",
			},
		},
	}
	// Pre-normalized form passes through unchanged.
	already := []FallbackModel{{Model: "gpt-4o", Provider: "openai"}}
	out := NormalizeFallbacks(cfg, already)
	if len(out) != 1 || out[0].Model != "gpt-4o" || out[0].Provider != "openai" {
		t.Errorf("pre-normalized pass-through = %+v", out)
	}

	// Empty → empty.
	if got := NormalizeFallbacks(cfg, nil); len(got) != 0 {
		t.Errorf("empty input = %+v, want empty", got)
	}
}
