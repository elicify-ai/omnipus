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
			{ModelName: "test-model", Model: "openai/gpt-4o", APIKeyRef: "TEST_KEY_1"},
			{ModelName: "other-model", Model: "anthropic/claude", APIKeyRef: "TEST_KEY_2"},
		},
	}

	result, err := cfg.GetModelConfig("test-model")
	if err != nil {
		t.Fatalf("GetModelConfig() error = %v", err)
	}
	if result.Model != "openai/gpt-4o" {
		t.Errorf("Model = %q, want %q", result.Model, "openai/gpt-4o")
	}
}

func TestGetModelConfig_NotFound(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{
			{ModelName: "test-model", Model: "openai/gpt-4o", APIKeyRef: "TEST_KEY_1"},
		},
	}

	_, err := cfg.GetModelConfig("nonexistent")
	if err == nil {
		t.Fatal("GetModelConfig() expected error for nonexistent model")
	}
}

func TestGetModelConfig_EmptyList(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{},
	}

	_, err := cfg.GetModelConfig("any-model")
	if err == nil {
		t.Fatal("GetModelConfig() expected error for empty model list")
	}
}

func TestGetModelConfig_RoundRobin(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{
			{ModelName: "lb-model", Model: "openai/gpt-4o-1", APIKeyRef: "TEST_KEY_1"},
			{ModelName: "lb-model", Model: "openai/gpt-4o-2", APIKeyRef: "TEST_KEY_2"},
			{ModelName: "lb-model", Model: "openai/gpt-4o-3", APIKeyRef: "TEST_KEY_3"},
		},
	}

	// Test round-robin distribution
	results := make(map[string]int)
	for range 30 {
		result, err := cfg.GetModelConfig("lb-model")
		if err != nil {
			t.Fatalf("GetModelConfig() error = %v", err)
		}
		results[result.Model]++
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
			{ModelName: "lb-model", Model: "openai/gpt-4o-1", APIKeyRef: "TEST_KEY_1"},
			{ModelName: "lb-model", Model: "openai/gpt-4o-2", APIKeyRef: "TEST_KEY_2"},
			{ModelName: "lb-model", Model: "openai/gpt-4o-3", APIKeyRef: "TEST_KEY_3"},
		},
	}

	wantOrder := []string{
		"openai/gpt-4o-1",
		"openai/gpt-4o-2",
		"openai/gpt-4o-3",
		"openai/gpt-4o-1",
		"openai/gpt-4o-2",
	}

	for i, want := range wantOrder {
		result, err := cfg.GetModelConfig("lb-model")
		if err != nil {
			t.Fatalf("GetModelConfig() call %d error = %v", i, err)
		}
		if result.Model != want {
			t.Fatalf("GetModelConfig() call %d model = %q, want %q", i, result.Model, want)
		}
	}
}

func TestGetModelConfig_Concurrent(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{
			{ModelName: "concurrent-model", Model: "openai/gpt-4o-1", APIKeyRef: "TEST_KEY_1"},
			{ModelName: "concurrent-model", Model: "openai/gpt-4o-2", APIKeyRef: "TEST_KEY_2"},
		},
	}

	const goroutines = 100
	const iterations = 10

	var wg sync.WaitGroup
	errors := make(chan error, goroutines*iterations)

	for range goroutines {
		wg.Go(func() {
			for range iterations {
				_, err := cfg.GetModelConfig("concurrent-model")
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
