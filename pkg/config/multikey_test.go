package config

import (
	"testing"
)

// Multi-key failover via APIKeys was removed in favor of credential-store-backed
// APIKeyRef. These tests verify ModelConfig.APIKey() resolves correctly from env
// and that IsVirtual() is false for an ordinary, non-expanded model.

func TestModelConfig_APIKey_ResolvesFromRef(t *testing.T) {
	const keyRef = "MULTIKEY_TEST_SINGLE_KEY"
	t.Setenv(keyRef, "single-key")

	model := &ModelConfig{
		Name:      "gpt-4",
		Model:     "openai/gpt-4o",
		APIKeyRef: keyRef,
	}

	if model.Name != "gpt-4" {
		t.Errorf("expected model_name 'gpt-4', got %q", model.Name)
	}
	if model.APIKey() != "single-key" {
		t.Errorf("expected api_key 'single-key', got %q", model.APIKey())
	}
	if len(model.Fallbacks) != 0 {
		t.Errorf("expected no fallbacks, got %v", model.Fallbacks)
	}
}

func TestModelConfig_APIKey_MultipleModels(t *testing.T) {
	const key1Ref = "MULTIKEY_TEST_KEY_1"
	const key2Ref = "MULTIKEY_TEST_KEY_2"
	const key3Ref = "MULTIKEY_TEST_KEY_3"
	t.Setenv(key1Ref, "key1")
	t.Setenv(key2Ref, "key2")
	t.Setenv(key3Ref, "key3")

	models := []*ModelConfig{
		{Name: "glm-4.7-a", Model: "zhipu/glm-4.7", APIBase: "https://api.example.com", APIKeyRef: key1Ref},
		{Name: "glm-4.7-b", Model: "zhipu/glm-4.7", APIBase: "https://api.example.com", APIKeyRef: key2Ref},
		{Name: "glm-4.7-c", Model: "zhipu/glm-4.7", APIBase: "https://api.example.com", APIKeyRef: key3Ref},
	}

	if models[0].APIKey() != "key1" {
		t.Errorf("models[0].APIKey() = %q, want %q", models[0].APIKey(), "key1")
	}
	if models[1].APIKey() != "key2" {
		t.Errorf("models[1].APIKey() = %q, want %q", models[1].APIKey(), "key2")
	}
	if models[2].APIKey() != "key3" {
		t.Errorf("models[2].APIKey() = %q, want %q", models[2].APIKey(), "key3")
	}
}

func TestModelConfig_FieldsSurvivedConstruction(t *testing.T) {
	const keyRef = "MULTIKEY_TEST_PRESERVE_KEY"
	t.Setenv(keyRef, "key0")

	model := &ModelConfig{
		Name:           "gpt-4",
		Model:          "openai/gpt-4o",
		APIBase:        "https://api.example.com",
		Proxy:          "http://proxy:8080",
		RPM:            60,
		MaxTokensField: "max_completion_tokens",
		RequestTimeout: 30,
		ThinkingLevel:  "high",
		APIKeyRef:      keyRef,
	}

	if model.Name != "gpt-4" {
		t.Errorf("expected model_name preserved, got %q", model.Name)
	}
	if model.Model != "openai/gpt-4o" {
		t.Errorf("expected model preserved, got %q", model.Model)
	}
	if model.APIBase != "https://api.example.com" {
		t.Errorf("expected api_base preserved, got %q", model.APIBase)
	}
	if model.Proxy != "http://proxy:8080" {
		t.Errorf("expected proxy preserved, got %q", model.Proxy)
	}
	if model.RPM != 60 {
		t.Errorf("expected rpm preserved, got %d", model.RPM)
	}
	if model.MaxTokensField != "max_completion_tokens" {
		t.Errorf("expected max_tokens_field preserved, got %q", model.MaxTokensField)
	}
	if model.RequestTimeout != 30 {
		t.Errorf("expected request_timeout preserved, got %d", model.RequestTimeout)
	}
	if model.ThinkingLevel != "high" {
		t.Errorf("expected thinking_level preserved, got %q", model.ThinkingLevel)
	}
	if model.APIKeyRef != keyRef {
		t.Errorf("expected api_key_ref preserved, got %q", model.APIKeyRef)
	}
}

func TestModelConfig_IsVirtualFlag(t *testing.T) {
	const keyRef = "MULTIKEY_TEST_VIRTUAL_KEY"
	t.Setenv(keyRef, "key1")

	model := &ModelConfig{
		Name:      "gpt-4",
		Model:     "openai/gpt-4o",
		APIKeyRef: keyRef,
	}

	if model.isVirtual {
		t.Errorf("ordinary model should not be virtual")
	}
	if model.IsVirtual() {
		t.Errorf("IsVirtual() should return false for non-virtual model")
	}
}

func TestMergeAPIKeys(t *testing.T) {
	tests := []struct {
		name     string
		apiKey   string
		apiKeys  []string
		expected []string
	}{
		{
			name:     "both empty",
			apiKey:   "",
			apiKeys:  nil,
			expected: nil,
		},
		{
			name:     "only ApiKey",
			apiKey:   "key1",
			apiKeys:  nil,
			expected: []string{"key1"},
		},
		{
			name:     "only ApiKeys",
			apiKey:   "",
			apiKeys:  []string{"key1", "key2"},
			expected: []string{"key1", "key2"},
		},
		{
			name:     "both with overlap",
			apiKey:   "key1",
			apiKeys:  []string{"key1", "key2", "key3"},
			expected: []string{"key1", "key2", "key3"},
		},
		{
			name:     "with whitespace",
			apiKey:   "  key1  ",
			apiKeys:  []string{"  key2  ", "  key1  "},
			expected: []string{"key1", "key2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeAPIKeys(tt.apiKey, tt.apiKeys)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d keys, got %d", len(tt.expected), len(result))
			}
			for i, k := range result {
				if k != tt.expected[i] {
					t.Errorf("expected key[%d] = %q, got %q", i, tt.expected[i], k)
				}
			}
		})
	}
}
