package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ADR-068 D14.1 / FR-018 / FR-040 (T068-07): the default model is a
// (provider, model) PAIR persisted at agents.defaults.default_model.
// agents.defaults.model_name and its alias semantics no longer exist;
// GetModelConfig resolves the pair EXACTLY against providers[] — never by a
// row's user-facing alias, never by the model slug alone.

func TestGetModelConfig_ExactPair(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{
			{Name: "glm-via-openrouter", Provider: "openrouter", Model: "z-ai/glm-5.2", APIBase: "https://openrouter.ai/api/v1"},
			{Name: "glm-direct", Provider: "zai", Model: "glm-5.2", APIBase: "https://api.z.ai/v1"},
		},
	}

	got, err := cfg.GetModelConfig("openrouter", "z-ai/glm-5.2")
	if err != nil {
		t.Fatalf("GetModelConfig(openrouter, z-ai/glm-5.2) error = %v", err)
	}
	if got.APIBase != "https://openrouter.ai/api/v1" {
		t.Fatalf("resolved the wrong row: %+v", got)
	}

	got, err = cfg.GetModelConfig("zai", "glm-5.2")
	if err != nil {
		t.Fatalf("GetModelConfig(zai, glm-5.2) error = %v", err)
	}
	if got.APIBase != "https://api.z.ai/v1" {
		t.Fatalf("resolved the wrong row: %+v", got)
	}

	// The pair is exact: the right model under the wrong provider is a miss,
	// not a prefix-stripped or cross-provider hit (ADR-067 exact lookup).
	if _, err := cfg.GetModelConfig("openrouter", "glm-5.2"); err == nil {
		t.Fatal("GetModelConfig(openrouter, glm-5.2) must miss: the openrouter row serves z-ai/glm-5.2, not glm-5.2")
	}
	// The alias no longer resolves anything (CRIT-001).
	if _, err := cfg.GetModelConfig("openrouter", "glm-via-openrouter"); err == nil {
		t.Fatal("GetModelConfig must not resolve a row by its model_name alias")
	}
	if _, err := cfg.GetModelConfig("", "glm-5.2"); err == nil {
		t.Fatal("GetModelConfig with an empty provider must miss a row that names one: the pair is the key, never widened to any provider")
	}
	if _, err := cfg.GetModelConfig("zai", ""); err == nil {
		t.Fatal("GetModelConfig with an empty model must miss")
	}
}

func TestGetModelConfig_PairPrefersUsableSibling(t *testing.T) {
	t.Setenv("T068_07_GOOD_KEY", "sk-good")
	cfg := &Config{
		Providers: []*ModelConfig{
			{Provider: "openai", Model: "gpt-4o", APIKeyRef: "T068_07_MISSING_KEY", APIBase: "https://broken.example"},
			{Provider: "openai", Model: "gpt-4o", APIKeyRef: "T068_07_GOOD_KEY", APIBase: "https://good.example"},
		},
	}
	for i := 0; i < 6; i++ {
		got, err := cfg.GetModelConfig("openai", "gpt-4o")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if got.APIBase != "https://good.example" {
			t.Fatalf("call %d: round-robin handed back the unusable sibling: %+v", i, got)
		}
	}
}

func TestConfig_NoModelNameDefaultKey(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Agents.Defaults.DefaultModel.IsZero() {
		t.Fatalf("fresh install must seed no default model (FR-040); got %+v", cfg.Agents.Defaults.DefaultModel)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	defaults := top["agents"].(map[string]any)["defaults"].(map[string]any)
	if _, ok := defaults["model_name"]; ok {
		t.Fatal("agents.defaults.model_name must not exist in the config schema (CRIT-001)")
	}
	if _, ok := defaults["provider"]; ok {
		t.Fatal("agents.defaults.provider was folded into default_model and must not be serialized on its own")
	}
	dm, ok := defaults["default_model"].(map[string]any)
	if !ok {
		t.Fatalf("agents.defaults.default_model must be an object; got %v", defaults["default_model"])
	}
	if dm["provider"] != "" || dm["model"] != "" {
		t.Fatalf("fresh-install default_model must be the zero pair; got %v", dm)
	}
}

func TestConfig_DefaultModelPairLoadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"version":1,"agents":{"defaults":{"default_model":{"provider":"anthropic","model":"claude-sonnet-4.6"}},"list":[]},"providers":[]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"}
	if cfg.Agents.Defaults.DefaultModel != want {
		t.Fatalf("default_model = %+v, want %+v", cfg.Agents.Defaults.DefaultModel, want)
	}
}

func TestModelConfig_UpdatedAtRoundTrip(t *testing.T) {
	// Absent → omitted (omitempty), so rows never PUT carry no updated_at.
	raw, err := json.Marshal(&ModelConfig{Provider: "openai", Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "updated_at") {
		t.Fatalf("updated_at must be omitted when unset: %s", raw)
	}

	ts := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	raw, err = json.Marshal(&ModelConfig{Provider: "openai", Model: "gpt-4o", UpdatedAt: &ts})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"updated_at":"2026-08-23T10:00:00Z"`) {
		t.Fatalf("updated_at must serialize as RFC 3339 under the updated_at key: %s", raw)
	}
	var back ModelConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.UpdatedAt == nil || !back.UpdatedAt.Equal(ts) {
		t.Fatalf("updated_at did not round-trip: %v", back.UpdatedAt)
	}
}
