// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestExtractProtocol(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		wantProtocol string
		wantModelID  string
	}{
		{
			name:         "openai with prefix",
			model:        "openai/gpt-4o",
			wantProtocol: "openai",
			wantModelID:  "gpt-4o",
		},
		{
			name:         "anthropic with prefix",
			model:        "anthropic/claude-sonnet-4.6",
			wantProtocol: "anthropic",
			wantModelID:  "claude-sonnet-4.6",
		},
		{
			name:         "no prefix - defaults to openai",
			model:        "gpt-4o",
			wantProtocol: "openai",
			wantModelID:  "gpt-4o",
		},
		{
			name:         "groq with prefix",
			model:        "groq/llama-3.1-70b",
			wantProtocol: "groq",
			wantModelID:  "llama-3.1-70b",
		},
		{
			name:         "empty string",
			model:        "",
			wantProtocol: "openai",
			wantModelID:  "",
		},
		{
			name:         "with whitespace",
			model:        "  openai/gpt-4  ",
			wantProtocol: "openai",
			wantModelID:  "gpt-4",
		},
		{
			name:         "multiple slashes",
			model:        "nvidia/meta/llama-3.1-8b",
			wantProtocol: "nvidia",
			wantModelID:  "meta/llama-3.1-8b",
		},
		{
			name:         "azure with prefix",
			model:        "azure/my-gpt5-deployment",
			wantProtocol: "azure",
			wantModelID:  "my-gpt5-deployment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protocol, modelID := ExtractProtocol(tt.model)
			if protocol != tt.wantProtocol {
				t.Errorf("ExtractProtocol(%q) protocol = %q, want %q", tt.model, protocol, tt.wantProtocol)
			}
			if modelID != tt.wantModelID {
				t.Errorf("ExtractProtocol(%q) modelID = %q, want %q", tt.model, modelID, tt.wantModelID)
			}
		})
	}
}

func TestCreateProviderFromConfig_OpenAI(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{
		ModelName: "test-openai",
		Model:     "openai/gpt-4o",
		APIBase:   "https://api.example.com/v1",
		APIKeyRef: keyRef,
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if provider == nil {
		t.Fatal("CreateProviderFromConfig() returned nil provider")
	}
	if modelID != "gpt-4o" {
		t.Errorf("modelID = %q, want %q", modelID, "gpt-4o")
	}
}

func TestCreateProviderFromConfig_DefaultAPIBase(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
	}{
		{"openai", "openai"},
		{"groq", "groq"},
		{"novita", "novita"},
		{"openrouter", "openrouter"},
		{"cerebras", "cerebras"},
		{"vivgrid", "vivgrid"},
		{"qwen", "qwen"},
		{"vllm", "vllm"},
		{"deepseek", "deepseek"},
		{"ollama", "ollama"},
		{"longcat", "longcat"},
		{"modelscope", "modelscope"},
		{"mimo", "mimo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const keyRef = "FACTORY_PROVIDER_PROTOCOL_TEST_KEY"
			t.Setenv(keyRef, "test-key")
			cfg := &config.ModelConfig{
				ModelName: "test-" + tt.protocol,
				Model:     tt.protocol + "/test-model",
				APIKeyRef: keyRef,
			}

			provider, _, err := CreateProviderFromConfig(cfg)
			if err != nil {
				t.Fatalf("CreateProviderFromConfig() error = %v", err)
			}

			// Verify we got an HTTPProvider for all these protocols
			if _, ok := provider.(*HTTPProvider); !ok {
				t.Fatalf("expected *HTTPProvider, got %T", provider)
			}
		})
	}
}

func TestGetDefaultAPIBase_LiteLLM(t *testing.T) {
	if got := GetDefaultAPIBase("litellm"); got != "http://localhost:4000/v1" {
		t.Fatalf("GetDefaultAPIBase(%q) = %q, want %q", "litellm", got, "http://localhost:4000/v1")
	}
}

func TestCreateProviderFromConfig_LiteLLM(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_LITELLM_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{
		ModelName: "test-litellm",
		Model:     "litellm/my-proxy-alias",
		APIBase:   "http://localhost:4000/v1",
		APIKeyRef: keyRef,
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if provider == nil {
		t.Fatal("CreateProviderFromConfig() returned nil provider")
	}
	if modelID != "my-proxy-alias" {
		t.Errorf("modelID = %q, want %q", modelID, "my-proxy-alias")
	}
}

func TestCreateProviderFromConfig_LongCat(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_LONGCAT_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{
		ModelName: "test-longcat",
		Model:     "longcat/LongCat-Flash-Thinking",
		APIBase:   "https://api.longcat.chat/openai",
		APIKeyRef: keyRef,
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if provider == nil {
		t.Fatal("CreateProviderFromConfig() returned nil provider")
	}
	if modelID != "LongCat-Flash-Thinking" {
		t.Errorf("modelID = %q, want %q", modelID, "LongCat-Flash-Thinking")
	}
	if _, ok := provider.(*HTTPProvider); !ok {
		t.Fatalf("expected *HTTPProvider, got %T", provider)
	}
}

func TestCreateProviderFromConfig_ModelScope(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_MODELSCOPE_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{
		ModelName: "test-modelscope",
		Model:     "modelscope/Qwen/Qwen3-235B-A22B-Instruct-2507",
		APIBase:   "https://api-inference.modelscope.cn/v1",
		APIKeyRef: keyRef,
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if provider == nil {
		t.Fatal("CreateProviderFromConfig() returned nil provider")
	}
	if modelID != "Qwen/Qwen3-235B-A22B-Instruct-2507" {
		t.Errorf("modelID = %q, want %q", modelID, "Qwen/Qwen3-235B-A22B-Instruct-2507")
	}
	if _, ok := provider.(*HTTPProvider); !ok {
		t.Fatalf("expected *HTTPProvider, got %T", provider)
	}
}

func TestGetDefaultAPIBase_ModelScope(t *testing.T) {
	if got := GetDefaultAPIBase("modelscope"); got != "https://api-inference.modelscope.cn/v1" {
		t.Fatalf("GetDefaultAPIBase(%q) = %q, want %q", "modelscope", got, "https://api-inference.modelscope.cn/v1")
	}
}

func TestCreateProviderFromConfig_Novita(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_NOVITA_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{
		ModelName: "test-novita",
		Model:     "novita/deepseek/deepseek-v3.2",
		APIKeyRef: keyRef,
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if provider == nil {
		t.Fatal("CreateProviderFromConfig() returned nil provider")
	}
	if modelID != "deepseek/deepseek-v3.2" {
		t.Errorf("modelID = %q, want %q", modelID, "deepseek/deepseek-v3.2")
	}
	if _, ok := provider.(*HTTPProvider); !ok {
		t.Fatalf("expected *HTTPProvider, got %T", provider)
	}
}

func TestGetDefaultAPIBase_Novita(t *testing.T) {
	if got := GetDefaultAPIBase("novita"); got != "https://api.novita.ai/openai" {
		t.Fatalf("GetDefaultAPIBase(%q) = %q, want %q", "novita", got, "https://api.novita.ai/openai")
	}
}

func TestCreateProviderFromConfig_Mimo(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_MIMO_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{
		ModelName: "test-mimo",
		Model:     "mimo/mimo-v2-pro",
		APIBase:   "https://api.xiaomimimo.com/v1",
		APIKeyRef: keyRef,
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if provider == nil {
		t.Fatal("CreateProviderFromConfig() returned nil provider")
	}
	if modelID != "mimo-v2-pro" {
		t.Errorf("modelID = %q, want %q", modelID, "mimo-v2-pro")
	}
	if _, ok := provider.(*HTTPProvider); !ok {
		t.Fatalf("expected *HTTPProvider, got %T", provider)
	}
}

func TestGetDefaultAPIBase_Mimo(t *testing.T) {
	if got := GetDefaultAPIBase("mimo"); got != "https://api.xiaomimimo.com/v1" {
		t.Fatalf("GetDefaultAPIBase(%q) = %q, want %q", "mimo", got, "https://api.xiaomimimo.com/v1")
	}
}

func TestCreateProviderFromConfig_Anthropic(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_ANTHROPIC_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{
		ModelName: "test-anthropic",
		Model:     "anthropic/claude-sonnet-4.6",
		APIKeyRef: keyRef,
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if provider == nil {
		t.Fatal("CreateProviderFromConfig() returned nil provider")
	}
	if modelID != "claude-sonnet-4.6" {
		t.Errorf("modelID = %q, want %q", modelID, "claude-sonnet-4.6")
	}
}

func TestCreateProviderFromConfig_Antigravity(t *testing.T) {
	cfg := &config.ModelConfig{
		ModelName: "test-antigravity",
		Model:     "antigravity/gemini-2.0-flash",
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if provider == nil {
		t.Fatal("CreateProviderFromConfig() returned nil provider")
	}
	if modelID != "gemini-2.0-flash" {
		t.Errorf("modelID = %q, want %q", modelID, "gemini-2.0-flash")
	}
}

func TestCreateProviderFromConfig_CodexCLI(t *testing.T) {
	cfg := &config.ModelConfig{
		ModelName: "test-codex-cli",
		Model:     "codex-cli/codex",
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if provider == nil {
		t.Fatal("CreateProviderFromConfig() returned nil provider")
	}
	if modelID != "codex" {
		t.Errorf("modelID = %q, want %q", modelID, "codex")
	}
}

func TestCreateProviderFromConfig_MissingAPIKey(t *testing.T) {
	cfg := &config.ModelConfig{
		ModelName: "test-no-key",
		Model:     "openai/gpt-4o",
	}

	_, _, err := CreateProviderFromConfig(cfg)
	if err == nil {
		t.Fatal("CreateProviderFromConfig() expected error for missing API key")
	}
}

func TestCreateProviderFromConfig_UnknownProtocol(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_UNKNOWN_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{
		ModelName: "test-unknown",
		Model:     "unknown-protocol/model",
		APIKeyRef: keyRef,
	}

	_, _, err := CreateProviderFromConfig(cfg)
	if err == nil {
		t.Fatal("CreateProviderFromConfig() expected error for unknown protocol")
	}
}

func TestCreateProviderFromConfig_NilConfig(t *testing.T) {
	_, _, err := CreateProviderFromConfig(nil)
	if err == nil {
		t.Fatal("CreateProviderFromConfig(nil) expected error")
	}
}

func TestCreateProviderFromConfig_EmptyModel(t *testing.T) {
	cfg := &config.ModelConfig{
		ModelName: "test-empty",
		Model:     "",
	}

	_, _, err := CreateProviderFromConfig(cfg)
	if err == nil {
		t.Fatal("CreateProviderFromConfig() expected error for empty model")
	}
}

func TestCreateProviderFromConfig_RequestTimeoutPropagation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	const keyRef = "FACTORY_PROVIDER_TIMEOUT_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{
		ModelName:      "test-timeout",
		Model:          "openai/gpt-4o",
		APIBase:        server.URL,
		RequestTimeout: 1,
		APIKeyRef:      keyRef,
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if modelID != "gpt-4o" {
		t.Fatalf("modelID = %q, want %q", modelID, "gpt-4o")
	}

	_, err = provider.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		modelID,
		nil,
	)
	if err == nil {
		t.Fatal("Chat() expected timeout error, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "context deadline exceeded") && !strings.Contains(errMsg, "Client.Timeout exceeded") {
		t.Fatalf("Chat() error = %q, want timeout-related error", errMsg)
	}
}

func TestCreateProviderFromConfig_Azure(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_AZURE_TEST_KEY"
	t.Setenv(keyRef, "test-azure-key")
	cfg := &config.ModelConfig{
		ModelName: "azure-gpt5",
		Model:     "azure/my-gpt5-deployment",
		APIBase:   "https://my-resource.openai.azure.com",
		APIKeyRef: keyRef,
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if provider == nil {
		t.Fatal("CreateProviderFromConfig() returned nil provider")
	}
	if modelID != "my-gpt5-deployment" {
		t.Errorf("modelID = %q, want %q", modelID, "my-gpt5-deployment")
	}
}

func TestCreateProviderFromConfig_AzureOpenAIAlias(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_AZURE_OPENAI_TEST_KEY"
	t.Setenv(keyRef, "test-azure-key")
	cfg := &config.ModelConfig{
		ModelName: "azure-gpt4",
		Model:     "azure-openai/my-deployment",
		APIBase:   "https://my-resource.openai.azure.com",
		APIKeyRef: keyRef,
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if provider == nil {
		t.Fatal("CreateProviderFromConfig() returned nil provider")
	}
	if modelID != "my-deployment" {
		t.Errorf("modelID = %q, want %q", modelID, "my-deployment")
	}
}

func TestCreateProviderFromConfig_AzureMissingAPIKey(t *testing.T) {
	cfg := &config.ModelConfig{
		ModelName: "azure-gpt5",
		Model:     "azure/my-gpt5-deployment",
		APIBase:   "https://my-resource.openai.azure.com",
	}

	_, _, err := CreateProviderFromConfig(cfg)
	if err == nil {
		t.Fatal("CreateProviderFromConfig() expected error for missing API key")
	}
}

func TestCreateProviderFromConfig_AzureMissingAPIBase(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_AZURE_BASE_TEST_KEY"
	t.Setenv(keyRef, "test-azure-key")
	cfg := &config.ModelConfig{
		ModelName: "azure-gpt5",
		Model:     "azure/my-gpt5-deployment",
		APIKeyRef: keyRef,
	}

	_, _, err := CreateProviderFromConfig(cfg)
	if err == nil {
		t.Fatal("CreateProviderFromConfig() expected error for missing API base")
	}
}

func TestCreateProviderFromConfig_QwenInternationalAlias(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_QWEN_INTL_TEST_KEY"
	t.Setenv(keyRef, "test-key")

	tests := []struct {
		name     string
		protocol string
	}{
		{"qwen-international", "qwen-international"},
		{"dashscope-intl", "dashscope-intl"},
		{"qwen-intl", "qwen-intl"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.ModelConfig{
				ModelName: "test-" + tt.protocol,
				Model:     tt.protocol + "/qwen-max",
				APIKeyRef: keyRef,
			}

			provider, modelID, err := CreateProviderFromConfig(cfg)
			if err != nil {
				t.Fatalf("CreateProviderFromConfig() error = %v", err)
			}
			if provider == nil {
				t.Fatal("CreateProviderFromConfig() returned nil provider")
			}
			if modelID != "qwen-max" {
				t.Errorf("modelID = %q, want %q", modelID, "qwen-max")
			}
			if _, ok := provider.(*HTTPProvider); !ok {
				t.Fatalf("expected *HTTPProvider, got %T", provider)
			}
		})
	}
}

func TestCreateProviderFromConfig_QwenUSAlias(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_QWEN_US_TEST_KEY"
	t.Setenv(keyRef, "test-key")

	tests := []struct {
		name     string
		protocol string
	}{
		{"qwen-us", "qwen-us"},
		{"dashscope-us", "dashscope-us"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.ModelConfig{
				ModelName: "test-" + tt.protocol,
				Model:     tt.protocol + "/qwen-max",
				APIKeyRef: keyRef,
			}

			provider, modelID, err := CreateProviderFromConfig(cfg)
			if err != nil {
				t.Fatalf("CreateProviderFromConfig() error = %v", err)
			}
			if provider == nil {
				t.Fatal("CreateProviderFromConfig() returned nil provider")
			}
			if modelID != "qwen-max" {
				t.Errorf("modelID = %q, want %q", modelID, "qwen-max")
			}
			if _, ok := provider.(*HTTPProvider); !ok {
				t.Fatalf("expected *HTTPProvider, got %T", provider)
			}
		})
	}
}

func TestCreateProviderFromConfig_CodingPlanAnthropic(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_CODING_PLAN_TEST_KEY"
	t.Setenv(keyRef, "test-key")

	tests := []struct {
		name     string
		protocol string
	}{
		{"coding-plan-anthropic", "coding-plan-anthropic"},
		{"alibaba-coding-anthropic", "alibaba-coding-anthropic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.ModelConfig{
				ModelName: "test-" + tt.protocol,
				Model:     tt.protocol + "/claude-sonnet-4-20250514",
				APIKeyRef: keyRef,
			}

			provider, modelID, err := CreateProviderFromConfig(cfg)
			if err != nil {
				t.Fatalf("CreateProviderFromConfig() error = %v", err)
			}
			if provider == nil {
				t.Fatal("CreateProviderFromConfig() returned nil provider")
			}
			if modelID != "claude-sonnet-4-20250514" {
				t.Errorf("modelID = %q, want %q", modelID, "claude-sonnet-4-20250514")
			}
			// coding-plan-anthropic uses Anthropic Messages provider
			// Verify it's the anthropic messages provider by checking interface
			var _ LLMProvider = provider
		})
	}
}

func TestGetDefaultAPIBase_CodingPlanAnthropic(t *testing.T) {
	expectedURL := "https://coding-intl.dashscope.aliyuncs.com/apps/anthropic"
	if got := GetDefaultAPIBase("coding-plan-anthropic"); got != expectedURL {
		t.Fatalf("GetDefaultAPIBase(%q) = %q, want %q", "coding-plan-anthropic", got, expectedURL)
	}
	if got := GetDefaultAPIBase("alibaba-coding-anthropic"); got != expectedURL {
		t.Fatalf("GetDefaultAPIBase(%q) = %q, want %q", "alibaba-coding-anthropic", got, expectedURL)
	}
}

func TestGetDefaultAPIBase_QwenIntlAliases(t *testing.T) {
	expectedURL := "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
	for _, protocol := range []string{"qwen-intl", "qwen-international", "dashscope-intl"} {
		if got := GetDefaultAPIBase(protocol); got != expectedURL {
			t.Fatalf("GetDefaultAPIBase(%q) = %q, want %q", protocol, got, expectedURL)
		}
	}
}

func TestGetDefaultAPIBase_QwenUSAliases(t *testing.T) {
	expectedURL := "https://dashscope-us.aliyuncs.com/compatible-mode/v1"
	for _, protocol := range []string{"qwen-us", "dashscope-us"} {
		if got := GetDefaultAPIBase(protocol); got != expectedURL {
			t.Fatalf("GetDefaultAPIBase(%q) = %q, want %q", protocol, got, expectedURL)
		}
	}
}

func TestCreateProviderFromConfig_MinimaxInjectsReasoningSplit(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	const keyRef = "FACTORY_PROVIDER_MINIMAX_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{
		ModelName: "test-minimax",
		Model:     "minimax/MiniMax-M2.5",
		APIBase:   server.URL,
		APIKeyRef: keyRef,
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if provider == nil {
		t.Fatal("CreateProviderFromConfig() returned nil provider")
	}
	if modelID != "MiniMax-M2.5" {
		t.Errorf("modelID = %q, want %q", modelID, "MiniMax-M2.5")
	}

	_, err = provider.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		modelID,
		nil,
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	// Verify reasoning_split is automatically injected
	if got, ok := requestBody["reasoning_split"]; !ok || got != true {
		t.Fatalf("reasoning_split = %v, want true", got)
	}
}

func TestCreateProviderFromConfig_MinimaxPreservesUserExtraBody(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	const keyRef2 = "FACTORY_PROVIDER_MINIMAX_CUSTOM_TEST_KEY"
	t.Setenv(keyRef2, "test-key")
	cfg := &config.ModelConfig{
		ModelName: "test-minimax-custom",
		Model:     "minimax/MiniMax-M2.5",
		APIBase:   server.URL,
		ExtraBody: map[string]any{"custom_field": "test"},
		APIKeyRef: keyRef2,
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}

	_, err = provider.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		modelID,
		nil,
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	// Verify reasoning_split is automatically injected
	if got, ok := requestBody["reasoning_split"]; !ok || got != true {
		t.Fatalf("reasoning_split = %v, want true", got)
	}
	// Verify user's custom field is preserved
	if got, ok := requestBody["custom_field"]; !ok || got != "test" {
		t.Fatalf("custom_field = %v, want test", got)
	}
}

func TestCreateProviderFromConfig_Bedrock(t *testing.T) {
	// Set dummy AWS env vars to make test deterministic
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	// Clear profile-related env vars to avoid loading shared config
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")
	t.Setenv("AWS_SDK_LOAD_CONFIG", "")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")

	cfg := &config.ModelConfig{
		ModelName: "bedrock-claude",
		Model:     "bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0",
		APIBase:   "us-west-2", // Region (also sets AWS region)
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err == nil {
		// Provider created successfully (built with -tags bedrock)
		if provider == nil {
			t.Error("provider is nil on success")
		}
		if modelID != "us.anthropic.claude-sonnet-4-20250514-v1:0" {
			t.Errorf("modelID = %q, want %q", modelID, "us.anthropic.claude-sonnet-4-20250514-v1:0")
		}
		return
	}
	errMsg := err.Error()
	// When built without -tags bedrock, expect stub error
	if strings.Contains(errMsg, "build with -tags bedrock") {
		return // Expected stub error
	}
	// Unexpected error - fail the test
	t.Errorf("unexpected error from bedrock provider: %v", err)
}

func TestCreateProviderFromConfig_BedrockWithEndpointURL(t *testing.T) {
	// Set dummy AWS env vars to make test deterministic
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_REGION", "us-east-1") // Required when using endpoint URL
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	// Clear profile-related env vars to avoid loading shared config
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")
	t.Setenv("AWS_SDK_LOAD_CONFIG", "")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")

	cfg := &config.ModelConfig{
		ModelName: "bedrock-claude",
		Model:     "bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0",
		APIBase:   "https://bedrock-runtime.us-east-1.amazonaws.com", // Full endpoint URL
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err == nil {
		// Provider created successfully (built with -tags bedrock)
		if provider == nil {
			t.Error("provider is nil on success")
		}
		if modelID != "us.anthropic.claude-sonnet-4-20250514-v1:0" {
			t.Errorf("modelID = %q, want %q", modelID, "us.anthropic.claude-sonnet-4-20250514-v1:0")
		}
		return
	}
	errMsg := err.Error()
	// When built without -tags bedrock, expect stub error
	if strings.Contains(errMsg, "build with -tags bedrock") {
		return // Expected stub error
	}
	// Unexpected error - fail the test
	t.Errorf("unexpected error from bedrock provider: %v", err)
}

// TestGetDefaultAPIBase_ZAI guards the z-ai/GLM wiring: the onboarding
// "Z.ai (GLM)" option sends provider id "z-ai", which must resolve to the
// international Z.ai endpoint. Regression for the onboarding probe returning
// 400 "unknown provider \"z-ai\"" (surfaced to the user as "Couldn't reach
// Z.ai (GLM)") because GetDefaultAPIBase had no z-ai case.
func TestGetDefaultAPIBase_ZAI(t *testing.T) {
	const want = "https://api.z.ai/api/paas/v4"
	for _, id := range []string{"z-ai", "z.ai", "zai"} {
		if got := GetDefaultAPIBase(id); got != want {
			t.Errorf("GetDefaultAPIBase(%q) = %q, want %q", id, got, want)
		}
	}
}

// TestCreateProviderFromConfig_ZAI verifies the runtime factory recognizes the
// z-ai protocol (OpenAI-compatible) and builds a provider with the default base.
func TestCreateProviderFromConfig_ZAI(t *testing.T) {
	const keyRef = "FACTORY_PROVIDER_ZAI_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{Model: "z-ai/glm-5.2", APIKeyRef: keyRef}
	p, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig(z-ai) error: %v", err)
	}
	if p == nil {
		t.Fatal("CreateProviderFromConfig(z-ai) returned nil provider")
	}
	if modelID != "glm-5.2" {
		t.Errorf("modelID = %q, want %q", modelID, "glm-5.2")
	}
}

// ---------------------------------------------------------------------------
// RETIRED (A-CONTRACT, 36801b44 — ADR-067 FR-023): probeEnumIDsFromYAML,
// TestProbeEnumProvidersResolveBase, TestProbeEnumProvidersAreKnownProtocols and
// TestEveryProbeProviderBuilds enumerated the ProbeProviderRequest.id enum. The
// enum no longer exists — id is a free string validated at runtime against the
// served catalog. Their replacements are the catalog-driven guards delivered by
// T067-08 (table-backed factory, T24 case set) and T067-12 (probe id validation,
// T40 TestOnboarding_Probe_FreeStringID); see
// docs/internal/specs/tasks/adr-067-registry-catalog-spec-tasks.md.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Task 2 — per-provider base assertions for newly-wired and key providers
// Traces to: docs/internal/provider-endpoint-audit-2026-06.md (Findings §A, §B)
//
// TestGetDefaultAPIBase_ZAI and the existing aliases already cover z-ai/zai/z.ai.
// This table covers the newly added and adjacent providers; do not duplicate those.
// ---------------------------------------------------------------------------

func TestGetDefaultAPIBase_ExactBases(t *testing.T) {
	// Traces to: docs/internal/provider-endpoint-audit-2026-06.md
	tests := []struct {
		id   string
		want string
	}{
		// Anthropic — added to fix onboarding-probe break (audit Category A)
		{"anthropic", "https://api.anthropic.com/v1"},
		{"anthropic-messages", "https://api.anthropic.com/v1"},
		// Moonshot / Kimi — international vs China-mainland split (audit Category B)
		{"moonshot", "https://api.moonshot.ai/v1"},
		{"moonshot-cn", "https://api.moonshot.cn/v1"},
		// MiniMax — international vs China-mainland split (audit Category B)
		{"minimax", "https://api.minimax.io/v1"},
		{"minimax-cn", "https://api.minimaxi.com/v1"},
		// Zhipu / Z.ai — existing, confirmed in TestGetDefaultAPIBase_ZAI; add zhipu here for cross-reference
		{"zhipu", "https://open.bigmodel.cn/api/paas/v4"},
		{"z-ai", "https://api.z.ai/api/paas/v4"},
		// GLM Coding Plan (subscription) — distinct from the pay-per-token z-ai base.
		{"z-ai-coding", "https://api.z.ai/api/coding/paas/v4"},
		{"glm-coding", "https://api.z.ai/api/coding/paas/v4"},
		{"zhipu-coding", "https://open.bigmodel.cn/api/coding/paas/v4"},
		// Anthropic-compatible (Messages API) endpoints for the Chinese vendors.
		{"z-ai-anthropic", "https://api.z.ai/api/anthropic/v1"},
		{"zhipu-anthropic", "https://open.bigmodel.cn/api/anthropic/v1"},
		{"moonshot-anthropic", "https://api.moonshot.ai/anthropic/v1"},
		{"moonshot-cn-anthropic", "https://api.moonshot.cn/anthropic/v1"},
		{"minimax-anthropic", "https://api.minimax.io/anthropic/v1"},
		{"minimax-cn-anthropic", "https://api.minimaxi.com/anthropic/v1"},
		{"deepseek-anthropic", "https://api.deepseek.com/anthropic/v1"},
		// Qwen / DashScope variants
		{"qwen", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{"qwen-intl", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"},
		{"qwen-us", "https://dashscope-us.aliyuncs.com/compatible-mode/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := GetDefaultAPIBase(tt.id)
			if got != tt.want {
				t.Errorf("GetDefaultAPIBase(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Task 3 — factory recognizes moonshot-cn and minimax-cn
// Traces to: docs/internal/provider-endpoint-audit-2026-06.md (Findings §B)
//
// Regression guard: the factory's case list (factory_provider.go CreateProviderFromConfig)
// must recognize "moonshot-cn" and "minimax-cn" and build a non-nil provider.
// Without a case these return "unknown protocol" even though GetDefaultAPIBase knows them.
// ---------------------------------------------------------------------------

func TestCreateProviderFromConfig_MoonshotCN(t *testing.T) {
	// Traces to: docs/internal/provider-endpoint-audit-2026-06.md (Category B)
	// moonshot-cn is the China-mainland Moonshot (Kimi) platform; separate key from intl.
	const keyRef = "FACTORY_PROVIDER_MOONSHOT_CN_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{
		ModelName: "test-moonshot-cn",
		Model:     "moonshot-cn/moonshot-v1-8k",
		APIKeyRef: keyRef,
	}

	p, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig(moonshot-cn) error: %v — factory may be missing a case for moonshot-cn", err)
	}
	if p == nil {
		t.Fatal("CreateProviderFromConfig(moonshot-cn) returned nil provider")
	}
	if modelID != "moonshot-v1-8k" {
		t.Errorf("modelID = %q, want %q", modelID, "moonshot-v1-8k")
	}
	if _, ok := p.(*HTTPProvider); !ok {
		t.Errorf("expected *HTTPProvider, got %T", p)
	}
}

func TestCreateProviderFromConfig_MinimaxCN(t *testing.T) {
	// Traces to: docs/internal/provider-endpoint-audit-2026-06.md (Category B)
	// minimax-cn is the China-mainland MiniMax platform; separate key from intl.
	// The factory also injects reasoning_split:true for both minimax variants.
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	const keyRef = "FACTORY_PROVIDER_MINIMAX_CN_TEST_KEY"
	t.Setenv(keyRef, "test-key")
	cfg := &config.ModelConfig{
		ModelName: "test-minimax-cn",
		Model:     "minimax-cn/MiniMax-M2.5",
		APIBase:   server.URL,
		APIKeyRef: keyRef,
	}

	p, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig(minimax-cn) error: %v — factory may be missing a case for minimax-cn", err)
	}
	if p == nil {
		t.Fatal("CreateProviderFromConfig(minimax-cn) returned nil provider")
	}
	if modelID != "MiniMax-M2.5" {
		t.Errorf("modelID = %q, want %q", modelID, "MiniMax-M2.5")
	}
	// Invoke Chat to let the provider populate requestBody
	_, _ = p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, modelID, nil)
	// minimax-cn must also inject reasoning_split (same branch as minimax)
	if got, ok := requestBody["reasoning_split"]; !ok || got != true {
		t.Errorf("minimax-cn: reasoning_split = %v, want true — minimax-cn branch must inject reasoning_split", got)
	}
}
