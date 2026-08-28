package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestFallbackModels_LegacyString_NormalizedToObject exercises the full
// load-time normalization for the legacy `["model-slug"]` form. After
// unmarshal + NormalizeFallbacks, the result is the new
// `[{model, provider}]` form with the provider resolved via the same
// passthrough lookup the chat-side model resolver uses (Q2 / FR-006).
//
// Traces to: spec §11 Dataset 2 / §12 TDD rows 7 + 9.
func TestFallbackModels_LegacyString_NormalizedToObject(t *testing.T) {
	// OpenRouter is a passthrough provider — the resolver routes any slug
	// through it. So "glm-5-turbo" with openrouter configured normalizes to
	// {Model: "glm-5-turbo", Provider: "openrouter"}.
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

	jsonData := `{
		"id": "ava",
		"name": "Ava",
		"fallback_models": ["glm-5-turbo"]
	}`

	var agent AgentConfig
	if err := json.Unmarshal([]byte(jsonData), &agent); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Apply the load-time normalization (FR-006 — caller's responsibility).
	agent.FallbackModels = NormalizeFallbacks(cfg, agent.FallbackModels)

	if len(agent.FallbackModels) != 1 {
		t.Fatalf("len(FallbackModels) = %d, want 1", len(agent.FallbackModels))
	}
	fb := agent.FallbackModels[0]
	if fb.Model != "glm-5-turbo" {
		t.Errorf("FallbackModels[0].Model = %q, want glm-5-turbo", fb.Model)
	}
	if fb.Provider != "openrouter" {
		t.Errorf("FallbackModels[0].Provider = %q, want openrouter (passthrough lookup)", fb.Provider)
	}
}

// TestFallbackModels_NewObjectUnchanged_SurvivesNormalize ensures the new
// `[{model, provider}]` form is preserved through NormalizeFallbacks
// (FR-005 / FR-006 — already-resolved entries pass through).
//
// Traces to: spec §11 Dataset 2 row 4 / §12 TDD row 8.
func TestFallbackModels_NewObjectUnchanged_SurvivesNormalize(t *testing.T) {
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

	jsonData := `{
		"id": "ava",
		"name": "Ava",
		"fallback_models": [{"model": "claude-sonnet-4.6", "provider": "anthropic"}]
	}`

	var agent AgentConfig
	if err := json.Unmarshal([]byte(jsonData), &agent); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	agent.FallbackModels = NormalizeFallbacks(cfg, agent.FallbackModels)

	if len(agent.FallbackModels) != 1 {
		t.Fatalf("len(FallbackModels) = %d, want 1", len(agent.FallbackModels))
	}
	fb := agent.FallbackModels[0]
	if fb.Model != "claude-sonnet-4.6" {
		t.Errorf("FallbackModels[0].Model = %q, want claude-sonnet-4.6", fb.Model)
	}
	if fb.Provider != "anthropic" {
		t.Errorf("FallbackModels[0].Provider = %q, want anthropic (preserved)", fb.Provider)
	}
}

// TestFallbackModels_BothFormsOrdered_NormalizedInPlace accepts a mix of
// legacy strings and new objects in the same array. Order is preserved
// (legacy first, then new) — FR-006 (Dataset 2 row 5).
//
// Traces to: spec §11 Dataset 2 row 5 / §12 TDD row 9.
func TestFallbackModels_BothFormsOrdered_NormalizedInPlace(t *testing.T) {
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

	jsonData := `{
		"id": "ava",
		"name": "Ava",
		"fallback_models": [
			"glm-5-turbo",
			{"model": "claude-sonnet-4.6", "provider": "anthropic"}
		]
	}`

	var agent AgentConfig
	if err := json.Unmarshal([]byte(jsonData), &agent); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	agent.FallbackModels = NormalizeFallbacks(cfg, agent.FallbackModels)

	if len(agent.FallbackModels) != 2 {
		t.Fatalf("len(FallbackModels) = %d, want 2", len(agent.FallbackModels))
	}

	// First: legacy string → resolved via openrouter passthrough.
	if agent.FallbackModels[0].Model != "glm-5-turbo" {
		t.Errorf("FallbackModels[0].Model = %q, want glm-5-turbo", agent.FallbackModels[0].Model)
	}
	if agent.FallbackModels[0].Provider != "openrouter" {
		t.Errorf("FallbackModels[0].Provider = %q, want openrouter", agent.FallbackModels[0].Provider)
	}

	// Second: object unchanged.
	if agent.FallbackModels[1].Model != "claude-sonnet-4.6" {
		t.Errorf("FallbackModels[1].Model = %q, want claude-sonnet-4.6", agent.FallbackModels[1].Model)
	}
	if agent.FallbackModels[1].Provider != "anthropic" {
		t.Errorf("FallbackModels[1].Provider = %q, want anthropic", agent.FallbackModels[1].Provider)
	}
}

// TestFallbackModels_MarshalWritesObjectForm guarantees the wire format is
// always the new object form. After unmarshal+marshal, both legacy and new
// inputs become objects. This is what the SPA AgentProfile.tsx editor and
// the openapi.yaml schema expect.
func TestFallbackModels_MarshalWritesObjectForm(t *testing.T) {
	agent := AgentConfig{
		ID: "ava",
		FallbackModels: FallbackModelSlice{
			{Model: "glm-5-turbo", Provider: "openrouter"},
			{Model: "claude-sonnet-4.6", Provider: "anthropic"},
		},
	}
	data, err := json.Marshal(agent)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `"fallback_models"`) {
		t.Errorf("output missing fallback_models: %s", got)
	}
	if !strings.Contains(got, `"model":"glm-5-turbo"`) {
		t.Errorf("output missing model field for glm-5-turbo: %s", got)
	}
	if !strings.Contains(got, `"provider":"anthropic"`) {
		t.Errorf("output missing provider field for anthropic: %s", got)
	}
}

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

// TestFallbackModels_UnmarshalEmptyArray verifies that an empty array
// (FR-005 absence case) decodes cleanly to an empty slice — no fallback.
func TestFallbackModels_UnmarshalEmptyArray(t *testing.T) {
	jsonData := `{"id":"ava","fallback_models":[]}`
	var agent AgentConfig
	if err := json.Unmarshal([]byte(jsonData), &agent); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(agent.FallbackModels) != 0 {
		t.Errorf("len(FallbackModels) = %d, want 0", len(agent.FallbackModels))
	}
}

// TestFallbackModels_UnmarshalAbsentField — the field is optional; absence
// must not cause an error and must yield a nil/empty slice.
func TestFallbackModels_UnmarshalAbsentField(t *testing.T) {
	jsonData := `{"id":"ava","name":"Ava"}`
	var agent AgentConfig
	if err := json.Unmarshal([]byte(jsonData), &agent); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(agent.FallbackModels) != 0 {
		t.Errorf("len(FallbackModels) = %d, want 0 when field absent", len(agent.FallbackModels))
	}
}

// TestFallbackModels_UnmarshalExactMatchProvider resolves a legacy string
// whose slug exactly matches a configured provider's Model field.
// Resolution must use that provider, NOT a passthrough. The display-alias
// rung is gone with ModelConfig.ModelName (ADR-067 X-25), so the ONLY thing
// a bare slug can match is what the row actually serves.
//
// Traces to: Dataset 2 row 3 ("claude-sonnet-4.6" with anthropic provider).
func TestFallbackModels_UnmarshalExactMatchProvider(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{
			{
				Name:     "claude-sonnet-4.6",
				Model:    "claude-sonnet-4.6",
				Provider: "anthropic",
			},
		},
	}
	jsonData := `{"id":"ava","fallback_models":["claude-sonnet-4.6"]}`
	var agent AgentConfig
	if err := json.Unmarshal([]byte(jsonData), &agent); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	agent.FallbackModels = NormalizeFallbacks(cfg, agent.FallbackModels)

	if len(agent.FallbackModels) != 1 {
		t.Fatalf("len = %d, want 1", len(agent.FallbackModels))
	}
	if agent.FallbackModels[0].Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic (exact match, not passthrough)", agent.FallbackModels[0].Provider)
	}
}
