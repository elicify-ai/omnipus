// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestGetModelConfig_Found(t *testing.T) {
	cfg := &Config{
		Version: CurrentVersion,
		Providers: []*ModelConfig{
			{Provider: "openai", Model: "gpt-4o", APIKeyRef: "TEST_KEY_1"},
			{Provider: "anthropic", Model: "claude", APIKeyRef: "TEST_KEY_2"},
		},
	}

	result, err := cfg.GetModelConfig("openai", "gpt-4o")
	if err != nil {
		t.Fatalf("GetModelConfig() error = %v", err)
	}
	if result.APIKeyRef != "TEST_KEY_1" {
		t.Errorf("APIKeyRef = %q, want %q", result.APIKeyRef, "TEST_KEY_1")
	}
}

func TestGetModelConfig_NotFound(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{
			{Provider: "openai", Model: "gpt-4o", APIKeyRef: "TEST_KEY_1"},
		},
	}

	_, err := cfg.GetModelConfig("openai", "nonexistent")
	if err == nil {
		t.Fatal("GetModelConfig() expected error for nonexistent model")
	}
}

func TestGetModelConfig_EmptyList(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{},
	}

	_, err := cfg.GetModelConfig("openai", "any-model")
	if err == nil {
		t.Fatal("GetModelConfig() expected error for empty model list")
	}
}

func TestGetModelConfig_RoundRobin(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{
			{Provider: "openai", Model: "gpt-4o", APIBase: "https://lb-1.example", APIKeyRef: "TEST_KEY_1"},
			{Provider: "openai", Model: "gpt-4o", APIBase: "https://lb-2.example", APIKeyRef: "TEST_KEY_2"},
			{Provider: "openai", Model: "gpt-4o", APIBase: "https://lb-3.example", APIKeyRef: "TEST_KEY_3"},
		},
	}

	// Test round-robin distribution
	results := make(map[string]int)
	for range 30 {
		result, err := cfg.GetModelConfig("openai", "gpt-4o")
		if err != nil {
			t.Fatalf("GetModelConfig() error = %v", err)
		}
		results[result.APIBase]++
	}

	// Each model should appear roughly 10 times (30 calls / 3 models)
	for model, count := range results {
		if count < 5 || count > 15 {
			t.Errorf("Model %s appeared %d times, expected ~10", model, count)
		}
	}
}

func TestGetModelConfig_RoundRobinStartsFromFirstMatch(t *testing.T) {
	rrCounter.Store(0)

	cfg := &Config{
		Providers: []*ModelConfig{
			{Provider: "openai", Model: "gpt-4o", APIBase: "https://lb-1.example", APIKeyRef: "TEST_KEY_1"},
			{Provider: "openai", Model: "gpt-4o", APIBase: "https://lb-2.example", APIKeyRef: "TEST_KEY_2"},
			{Provider: "openai", Model: "gpt-4o", APIBase: "https://lb-3.example", APIKeyRef: "TEST_KEY_3"},
		},
	}

	wantOrder := []string{
		"https://lb-1.example",
		"https://lb-2.example",
		"https://lb-3.example",
		"https://lb-1.example",
		"https://lb-2.example",
	}

	for i, want := range wantOrder {
		result, err := cfg.GetModelConfig("openai", "gpt-4o")
		if err != nil {
			t.Fatalf("GetModelConfig() call %d error = %v", i, err)
		}
		if result.APIBase != want {
			t.Fatalf("GetModelConfig() call %d api_base = %q, want %q", i, result.APIBase, want)
		}
	}
}

// TestGetModelConfig_SkipsUnusableCredentialInFavorOfWorkingSibling is the D3
// review regression (2026-08-15): several providers[] entries may share one
// (provider, model) pair for load balancing, round-robinned by GetModelConfig. Before
// this fix, an entry whose api_key_ref never resolved stayed in the
// candidate pool on equal footing with a working sibling — the round-robin
// counter has no key-awareness, so it could hand the broken entry back to
// CreateProviderFromConfig, which happily builds a provider with an empty API
// key and produces a bare upstream 401 naming neither the provider nor the
// credential. This was unreachable before gateway.go's reportInjectionErrors
// started degrading (rather than aborting) on an unresolvable provider ref —
// boot used to abort outright, so a config carrying a broken load-balanced
// sibling never got to run. The degrade-not-abort fix made it reachable for
// the first time.
func TestGetModelConfig_SkipsUnusableCredentialInFavorOfWorkingSibling(t *testing.T) {
	rrCounter.Store(0)

	const goodRef = "MODELCFG_TEST_LB_GOOD_KEY"
	const badRef = "MODELCFG_TEST_LB_BAD_KEY"
	t.Setenv(goodRef, "sk-good")
	t.Setenv(badRef, "") // present but never resolved — simulates a failed injection

	cfg := &Config{
		Providers: []*ModelConfig{
			{Provider: "openai", Model: "gpt-4o", APIBase: "https://lb-bad.example", APIKeyRef: badRef},
			{Provider: "openai", Model: "gpt-4o", APIBase: "https://lb-good.example", APIKeyRef: goodRef},
		},
	}

	for i := range 20 {
		result, err := cfg.GetModelConfig("openai", "gpt-4o")
		if err != nil {
			t.Fatalf("GetModelConfig() call %d error = %v", i, err)
		}
		if result.APIBase != "https://lb-good.example" {
			t.Fatalf(
				"call %d returned %q — round-robin must never select an entry whose credential "+
					"never resolved when a working sibling exists; a bare upstream 401 naming neither "+
					"the provider nor the credential would result",
				i, result.APIBase,
			)
		}
	}
}

// TestGetModelConfig_AllUnusableFallsBackToUnfiltered pins the fallback: when
// NONE of the siblings sharing a (provider, model) pair are usable, GetModelConfig must
// still return one of them rather than reporting "model not found" — the
// caller (gateway.go's defaultModelCredentialBlocked) is responsible for
// reporting the model as blocked, and needs a real ModelConfig (for its
// APIKeyRef) to name the credential in that message.
func TestGetModelConfig_AllUnusableFallsBackToUnfiltered(t *testing.T) {
	rrCounter.Store(0)

	const ref1 = "MODELCFG_TEST_ALL_BAD_1"
	const ref2 = "MODELCFG_TEST_ALL_BAD_2"
	t.Setenv(ref1, "")
	t.Setenv(ref2, "")

	cfg := &Config{
		Providers: []*ModelConfig{
			{Provider: "openai", Model: "gpt-4o", APIBase: "https://lb-1.example", APIKeyRef: ref1},
			{Provider: "openai", Model: "gpt-4o", APIBase: "https://lb-2.example", APIKeyRef: ref2},
		},
	}

	for i := range 10 {
		result, err := cfg.GetModelConfig("openai", "gpt-4o")
		if err != nil {
			t.Fatalf("GetModelConfig() call %d error = %v — must still resolve when all siblings are unusable", i, err)
		}
		if result == nil {
			t.Fatalf("GetModelConfig() call %d returned a nil result", i)
		}
	}
}

func TestGetModelConfig_Concurrent(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{
			{Provider: "openai", Model: "gpt-4o", APIBase: "https://lb-1.example", APIKeyRef: "TEST_KEY_1"},
			{Provider: "openai", Model: "gpt-4o", APIBase: "https://lb-2.example", APIKeyRef: "TEST_KEY_2"},
		},
	}

	const goroutines = 100
	const iterations = 10

	var wg sync.WaitGroup
	errors := make(chan error, goroutines*iterations)

	for range goroutines {
		wg.Go(func() {
			for range iterations {
				_, err := cfg.GetModelConfig("openai", "gpt-4o")
				if err != nil {
					errors <- err
				}
			}
		})
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("Concurrent GetModelConfig() error: %v", err)
	}
}

func TestModelConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  ModelConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: ModelConfig{
				ModelName: "test",
				Model:     "openai/gpt-4o",
			},
			wantErr: false,
		},
		{
			name: "missing model_name",
			config: ModelConfig{
				Model: "openai/gpt-4o",
			},
			wantErr: true,
		},
		{
			name: "missing model",
			config: ModelConfig{
				ModelName: "test",
			},
			wantErr: true,
		},
		{
			name:    "empty config",
			config:  ModelConfig{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_ValidateProviders(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
		errMsg  string // partial error message to check
	}{
		{
			name: "valid list",
			config: &Config{
				Providers: []*ModelConfig{
					{ModelName: "test1", Model: "openai/gpt-4o"},
					{ModelName: "test2", Model: "anthropic/claude"},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid entry",
			config: &Config{
				Providers: []*ModelConfig{
					{ModelName: "test1", Model: "openai/gpt-4o"},
					{ModelName: "", Model: "anthropic/claude"}, // missing model_name
				},
			},
			wantErr: true,
			errMsg:  "model_name is required",
		},
		{
			name: "empty list",
			config: &Config{
				Providers: []*ModelConfig{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.ValidateProviders()
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateProviders() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateProviders() error = %v, want error containing %q", err, tt.errMsg)
				}
			}
		})
	}
}

func TestModelConfig_RequestTimeoutParsing(t *testing.T) {
	jsonData := `{
		"model_name": "slow-local",
		"model": "openai/local-model",
		"api_base": "http://localhost:11434/v1",
		"request_timeout": 300
	}`

	var cfg ModelConfig
	if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if cfg.RequestTimeout != 300 {
		t.Fatalf("RequestTimeout = %d, want 300", cfg.RequestTimeout)
	}
}

func TestModelConfig_RequestTimeoutDefaultZeroValue(t *testing.T) {
	jsonData := `{
		"model_name": "default-timeout",
		"model": "openai/gpt-4o",
		"api_key": "test-key"
	}`

	var cfg ModelConfig
	if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if cfg.RequestTimeout != 0 {
		t.Fatalf("RequestTimeout = %d, want 0", cfg.RequestTimeout)
	}
}
