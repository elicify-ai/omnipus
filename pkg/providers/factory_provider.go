// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	anthropicmessages "github.com/elicify-ai/omnipus/pkg/providers/anthropic_messages"
	"github.com/elicify-ai/omnipus/pkg/providers/azure"
	"github.com/elicify-ai/omnipus/pkg/providers/bedrock"
)

// createClaudeAuthProvider creates a Claude provider using OAuth credentials from auth store.
func createClaudeAuthProvider() (LLMProvider, error) {
	cred, err := getCredential("anthropic")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf(
			"no credentials for anthropic. Run: omnipus credentials set ANTHROPIC_API_KEY <your-key>",
		)
	}
	return NewClaudeProviderWithTokenSource(cred.AccessToken, createClaudeTokenSource()), nil
}

// createCodexAuthProvider creates a Codex provider using OAuth credentials from auth store.
func createCodexAuthProvider() (LLMProvider, error) {
	cred, err := getCredential("openai")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf(
			"no credentials for openai. Run: omnipus credentials set OPENAI_API_KEY <your-key>",
		)
	}
	return NewCodexProviderWithTokenSource(
		cred.AccessToken,
		cred.AccountID,
		createCodexTokenSource(),
	), nil
}

// ExtractProtocol extracts the protocol prefix and model identifier from a model string.
// If no prefix is specified, it defaults to "openai".
// Examples:
//   - "openai/gpt-4o" -> ("openai", "gpt-4o")
//   - "anthropic/claude-sonnet-4.6" -> ("anthropic", "claude-sonnet-4.6")
//   - "gpt-4o" -> ("openai", "gpt-4o")  // default protocol
func ExtractProtocol(model string) (protocol, modelID string) {
	model = strings.TrimSpace(model)
	protocol, modelID, found := strings.Cut(model, "/")
	if !found {
		return "openai", model
	}
	return protocol, modelID
}

// CreateProviderFromConfig creates a provider based on the ModelConfig.
// It uses the protocol prefix in the Model field to determine which provider to create.
// Supported protocol families include OpenAI-compatible prefixes (e.g., openai, openrouter, groq, gemini),
// Azure OpenAI, Amazon Bedrock, Anthropic (including messages), and various CLI/compatibility shims.
// See the switch on protocol in this function for the authoritative list.
// Returns the provider, the model ID (without protocol prefix), and any error.
func CreateProviderFromConfig(cfg *config.ModelConfig) (LLMProvider, string, error) {
	if cfg == nil {
		return nil, "", fmt.Errorf("config is nil")
	}

	if cfg.Model == "" {
		return nil, "", fmt.Errorf("model is required")
	}

	var protocol, modelID string
	if cfg.Provider != "" {
		protocol = cfg.Provider
		modelID = cfg.Model
	} else {
		protocol, modelID = ExtractProtocol(cfg.Model)
	}

	switch protocol {
	case "openai":
		// OpenAI with OAuth/token auth (Codex-style)
		if cfg.AuthMethod == "oauth" || cfg.AuthMethod == "token" {
			provider, err := createCodexAuthProvider()
			if err != nil {
				return nil, "", err
			}
			return provider, modelID, nil
		}
		// OpenAI with API key
		if cfg.APIKey() == "" && cfg.APIBase == "" {
			return nil, "", fmt.Errorf(
				"api_key or api_base is required for HTTP-based protocol %q",
				protocol,
			)
		}
		apiBase := cfg.APIBase
		if apiBase == "" {
			apiBase = GetDefaultAPIBase(protocol)
		}
		p, err := NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
			cfg.APIKey(),
			apiBase,
			cfg.Proxy,
			cfg.MaxTokensField,
			cfg.RequestTimeout,
			cfg.ExtraBody,
		)
		if err != nil {
			return nil, "", err
		}
		return p, modelID, nil

	case "azure", "azure-openai":
		// Azure OpenAI uses deployment-based URLs, api-key header auth,
		// and always sends max_completion_tokens.
		if cfg.APIKey() == "" {
			return nil, "", fmt.Errorf("api_key is required for azure protocol")
		}
		if cfg.APIBase == "" {
			return nil, "", fmt.Errorf(
				"api_base is required for azure protocol (e.g., https://your-resource.openai.azure.com)",
			)
		}
		p, err := azure.NewProviderWithTimeout(
			cfg.APIKey(),
			cfg.APIBase,
			cfg.Proxy,
			cfg.RequestTimeout,
		)
		if err != nil {
			return nil, "", err
		}
		return p, modelID, nil

	case "bedrock":
		// AWS Bedrock uses AWS SDK credentials (env vars, profiles, IAM roles, etc.)
		// api_base can be:
		//   - A full endpoint URL: https://bedrock-runtime.us-east-1.amazonaws.com
		//   - A region name: us-east-1 (AWS SDK resolves endpoint automatically)
		var opts []bedrock.Option
		if cfg.APIBase != "" {
			if !strings.Contains(cfg.APIBase, "://") {
				// Treat as region: let AWS SDK resolve the correct endpoint
				// (supports all AWS partitions: aws, aws-cn, aws-us-gov, etc.)
				opts = append(opts, bedrock.WithRegion(cfg.APIBase))
			} else {
				// Full endpoint URL provided (for custom endpoints or testing)
				opts = append(opts, bedrock.WithBaseEndpoint(cfg.APIBase))
			}
		}
		// Use a separate timeout for AWS config loading (credential resolution can block)
		initTimeout := 30 * time.Second
		if cfg.RequestTimeout > 0 {
			reqTimeout := time.Duration(cfg.RequestTimeout) * time.Second
			// Set request timeout for API calls
			opts = append(opts, bedrock.WithRequestTimeout(reqTimeout))
			// Ensure init timeout is at least as large as request timeout
			if reqTimeout > initTimeout {
				initTimeout = reqTimeout
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
		defer cancel()
		// Note: AWS_PROFILE env var is automatically used by AWS SDK
		provider, err := bedrock.NewProvider(ctx, opts...)
		if err != nil {
			return nil, "", fmt.Errorf("creating bedrock provider: %w", err)
		}
		return provider, modelID, nil

	case "openrouter":
		// OpenRouter: inject temperature=0 and seed=42 for deterministic responses
		// unless the caller has explicitly overridden them in ExtraBody.
		if cfg.APIKey() == "" && cfg.APIBase == "" {
			return nil, "", fmt.Errorf(
				"api_key or api_base is required for HTTP-based protocol %q",
				protocol,
			)
		}
		apiBase := cfg.APIBase
		if apiBase == "" {
			apiBase = GetDefaultAPIBase(protocol)
		}
		extraBody := map[string]any{
			"temperature": float64(0),
			"seed":        42,
		}
		for k, v := range cfg.ExtraBody {
			extraBody[k] = v
		}
		p, err := NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
			cfg.APIKey(),
			apiBase,
			cfg.Proxy,
			cfg.MaxTokensField,
			cfg.RequestTimeout,
			extraBody,
		)
		if err != nil {
			return nil, "", err
		}
		return p, modelID, nil

	case "litellm",
		"groq",
		"zhipu",
		"z-ai",
		"z.ai",
		"zai",
		"z-ai-coding",
		"glm-coding",
		"zhipu-coding",
		"gemini",
		"google",
		"nvidia",
		"ollama",
		"moonshot",
		"moonshot-cn",
		"shengsuanyun",
		"deepseek",
		"cerebras",
		"vivgrid",
		"volcengine",
		"vllm",
		"qwen",
		"qwen-intl",
		"qwen-international",
		"dashscope-intl",
		"qwen-us",
		"dashscope-us",
		"mistral",
		"avian",
		"longcat",
		"modelscope",
		"novita",
		"coding-plan",
		"alibaba-coding",
		"qwen-coding",
		"mimo":
		// All other OpenAI-compatible HTTP providers
		if cfg.APIKey() == "" && cfg.APIBase == "" {
			return nil, "", fmt.Errorf(
				"api_key or api_base is required for HTTP-based protocol %q",
				protocol,
			)
		}
		apiBase := cfg.APIBase
		if apiBase == "" {
			apiBase = GetDefaultAPIBase(protocol)
		}
		p, err := NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
			cfg.APIKey(),
			apiBase,
			cfg.Proxy,
			cfg.MaxTokensField,
			cfg.RequestTimeout,
			cfg.ExtraBody,
		)
		if err != nil {
			return nil, "", err
		}
		return p, modelID, nil

	case "minimax", "minimax-cn":
		// Minimax requires reasoning_split: true in the request body
		if cfg.APIKey() == "" && cfg.APIBase == "" {
			return nil, "", fmt.Errorf(
				"api_key or api_base is required for HTTP-based protocol %q",
				protocol,
			)
		}
		apiBase := cfg.APIBase
		if apiBase == "" {
			apiBase = GetDefaultAPIBase(protocol)
		}
		extraBody := cfg.ExtraBody
		if extraBody == nil {
			extraBody = make(map[string]any)
		}
		if _, ok := extraBody["reasoning_split"]; !ok {
			extraBody["reasoning_split"] = true
		}
		p, err := NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
			cfg.APIKey(),
			apiBase,
			cfg.Proxy,
			cfg.MaxTokensField,
			cfg.RequestTimeout,
			extraBody,
		)
		if err != nil {
			return nil, "", err
		}
		return p, modelID, nil

	case "anthropic":
		if cfg.AuthMethod == "oauth" || cfg.AuthMethod == "token" {
			// Use OAuth credentials from auth store
			provider, err := createClaudeAuthProvider()
			if err != nil {
				return nil, "", err
			}
			return provider, modelID, nil
		}
		// Use API key with HTTP API
		apiBase := cfg.APIBase
		if apiBase == "" {
			apiBase = "https://api.anthropic.com/v1"
		}
		if cfg.APIKey() == "" {
			return nil, "", fmt.Errorf(
				"api_key is required for anthropic protocol (model: %s)",
				cfg.Model,
			)
		}
		p, err := NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
			cfg.APIKey(),
			apiBase,
			cfg.Proxy,
			cfg.MaxTokensField,
			cfg.RequestTimeout,
			cfg.ExtraBody,
		)
		if err != nil {
			return nil, "", err
		}
		return p, modelID, nil

	case "anthropic-messages":
		// Anthropic Messages API with native format (HTTP-based, no SDK)
		apiBase := cfg.APIBase
		if apiBase == "" {
			apiBase = "https://api.anthropic.com/v1"
		}
		if cfg.APIKey() == "" {
			return nil, "", fmt.Errorf(
				"api_key is required for anthropic-messages protocol (model: %s)",
				cfg.Model,
			)
		}
		return anthropicmessages.NewProviderWithTimeout(
			cfg.APIKey(),
			apiBase,
			cfg.RequestTimeout,
		), modelID, nil

	case "coding-plan-anthropic", "alibaba-coding-anthropic",
		"z-ai-anthropic", "zhipu-anthropic",
		"moonshot-anthropic", "moonshot-cn-anthropic",
		"minimax-anthropic", "minimax-cn-anthropic",
		"deepseek-anthropic":
		// Anthropic-compatible (Messages API) providers — Alibaba Coding Plan plus
		// the Chinese vendors that expose a Claude-compatible endpoint alongside
		// their OpenAI one. All speak the native Anthropic Messages format.
		apiBase := cfg.APIBase
		if apiBase == "" {
			apiBase = GetDefaultAPIBase(protocol)
		}
		if cfg.APIKey() == "" {
			return nil, "", fmt.Errorf(
				"api_key is required for %q protocol (model: %s)",
				protocol,
				cfg.Model,
			)
		}
		return anthropicmessages.NewProviderWithTimeout(
			cfg.APIKey(),
			apiBase,
			cfg.RequestTimeout,
		), modelID, nil

	case "claude-cli", "claudecli":
		workspace := cfg.Home
		if workspace == "" {
			workspace = "."
		}
		return NewClaudeCliProvider(workspace), modelID, nil

	case "codex-cli", "codexcli":
		workspace := cfg.Home
		if workspace == "" {
			workspace = "."
		}
		return NewCodexCliProvider(workspace), modelID, nil

	default:
		return nil, "", fmt.Errorf("unknown protocol %q in model %q", protocol, cfg.Model)
	}
}

// knownProtocols mirrors the switch in CreateProviderFromConfig. Keep in sync
// when adding or removing a protocol case above. Used by IsKnownProtocol for
// early validation in REST/CLI paths that must reject unknown protocols before
// persisting them to config.json.
var knownProtocols = map[string]bool{
	"openai":                   true,
	"azure":                    true,
	"azure-openai":             true,
	"bedrock":                  true,
	"litellm":                  true,
	"openrouter":               true,
	"groq":                     true,
	"zhipu":                    true,
	"z-ai":                     true,
	"z.ai":                     true,
	"zai":                      true,
	"z-ai-coding":              true,
	"glm-coding":               true,
	"zhipu-coding":             true,
	"z-ai-anthropic":           true,
	"zhipu-anthropic":          true,
	"moonshot-anthropic":       true,
	"moonshot-cn-anthropic":    true,
	"minimax-anthropic":        true,
	"minimax-cn-anthropic":     true,
	"deepseek-anthropic":       true,
	"gemini":                   true,
	"google":                   true,
	"nvidia":                   true,
	"ollama":                   true,
	"moonshot":                 true,
	"moonshot-cn":              true,
	"shengsuanyun":             true,
	"deepseek":                 true,
	"cerebras":                 true,
	"vivgrid":                  true,
	"volcengine":               true,
	"vllm":                     true,
	"qwen":                     true,
	"qwen-intl":                true,
	"qwen-international":       true,
	"dashscope-intl":           true,
	"qwen-us":                  true,
	"dashscope-us":             true,
	"mistral":                  true,
	"avian":                    true,
	"longcat":                  true,
	"modelscope":               true,
	"novita":                   true,
	"coding-plan":              true,
	"alibaba-coding":           true,
	"qwen-coding":              true,
	"mimo":                     true,
	"minimax":                  true,
	"minimax-cn":               true,
	"anthropic":                true,
	"anthropic-messages":       true,
	"coding-plan-anthropic":    true,
	"alibaba-coding-anthropic": true,
	"claude-cli":               true,
	"claudecli":                true,
	"codex-cli":                true,
	"codexcli":                 true,
}

// IsKnownProtocol reports whether the given protocol name is recognized by
// CreateProviderFromConfig. Empty strings return false. Used for early
// validation in REST/CLI paths that reject bad configs before they reach the
// credential store or config.json.
func IsKnownProtocol(protocol string) bool {
	return knownProtocols[protocol]
}

// AllKnownProtocols returns the sorted slice of all protocol ids in
// knownProtocols. This is the reflective accessor used by the catalog
// drift-guard test (TestCatalog_DriftGuard_NewProtocolUntriagedFails) so that
// adding a new id to knownProtocols without triaging it into the catalog OR
// catalogExcluded causes CI to fail immediately — no hand-copied allKnown list
// to forget to update.
func AllKnownProtocols() []string {
	out := make([]string, 0, len(knownProtocols))
	for id := range knownProtocols {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// GetDefaultAPIBase returns the default API base URL for a given protocol.
func GetDefaultAPIBase(protocol string) string {
	switch protocol {
	case "openai":
		return "https://api.openai.com/v1"
	case "anthropic", "anthropic-messages":
		// The runtime factory hardcodes this base too; resolving it here lets the
		// onboarding model-list probe work (it resolves via GetDefaultAPIBase).
		return "https://api.anthropic.com/v1"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "litellm":
		return "http://localhost:4000/v1"
	case "novita":
		return "https://api.novita.ai/openai"
	case "groq":
		return "https://api.groq.com/openai/v1"
	case "zhipu":
		return "https://open.bigmodel.cn/api/paas/v4"
	case "z-ai", "z.ai", "zai":
		// International Z.ai (GLM) endpoint — distinct from the China-mainland
		// Zhipu host above. The onboarding "Z.ai (GLM)" option sends id "z-ai".
		return "https://api.z.ai/api/paas/v4"
	case "z-ai-coding", "glm-coding":
		// GLM Coding Plan (subscription) endpoint — OpenAI-compatible. Billed
		// against the Coding Plan, NOT the pay-per-token api/paas/v4 above (a
		// Coding Plan key gets 1113 "insufficient balance" on the pay-per-token
		// host). Distinct base so subscription users can actually run.
		return "https://api.z.ai/api/coding/paas/v4"
	case "zhipu-coding":
		// GLM Coding Plan, China-mainland host (OpenAI-compatible).
		return "https://open.bigmodel.cn/api/coding/paas/v4"
	// ── Anthropic-compatible (Messages API) endpoints — Chinese providers that
	// expose a Claude-compatible endpoint alongside their OpenAI one. Base ends
	// in /anthropic/v1; the anthropic_messages provider appends "messages".
	case "z-ai-anthropic":
		return "https://api.z.ai/api/anthropic/v1"
	case "zhipu-anthropic":
		return "https://open.bigmodel.cn/api/anthropic/v1"
	case "moonshot-anthropic":
		return "https://api.moonshot.ai/anthropic/v1"
	case "moonshot-cn-anthropic":
		return "https://api.moonshot.cn/anthropic/v1"
	case "minimax-anthropic":
		return "https://api.minimax.io/anthropic/v1"
	case "minimax-cn-anthropic":
		return "https://api.minimaxi.com/anthropic/v1"
	case "deepseek-anthropic":
		return "https://api.deepseek.com/anthropic/v1"
	case "gemini", "google":
		return "https://generativelanguage.googleapis.com/v1beta/openai"
	case "nvidia":
		return "https://integrate.api.nvidia.com/v1"
	case "ollama":
		return "http://localhost:11434/v1"
	case "moonshot":
		// International Moonshot (Kimi) endpoint.
		return "https://api.moonshot.ai/v1"
	case "moonshot-cn":
		// China-mainland Moonshot (Kimi) platform — separate account/key from intl.
		return "https://api.moonshot.cn/v1"
	case "shengsuanyun":
		return "https://router.shengsuanyun.com/api/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "cerebras":
		return "https://api.cerebras.ai/v1"
	case "vivgrid":
		return "https://api.vivgrid.com/v1"
	case "volcengine":
		return "https://ark.cn-beijing.volces.com/api/v3"
	case "qwen":
		return "https://dashscope.aliyuncs.com/compatible-mode/v1"
	case "qwen-intl", "qwen-international", "dashscope-intl":
		return "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
	case "qwen-us", "dashscope-us":
		return "https://dashscope-us.aliyuncs.com/compatible-mode/v1"
	case "coding-plan", "alibaba-coding", "qwen-coding":
		return "https://coding-intl.dashscope.aliyuncs.com/v1"
	case "coding-plan-anthropic", "alibaba-coding-anthropic":
		return "https://coding-intl.dashscope.aliyuncs.com/apps/anthropic"
	case "vllm":
		return "http://localhost:8000/v1"
	case "mistral":
		return "https://api.mistral.ai/v1"
	case "avian":
		return "https://api.avian.io/v1"
	case "minimax":
		// International MiniMax endpoint.
		return "https://api.minimax.io/v1"
	case "minimax-cn":
		// China-mainland MiniMax platform — separate account/key from intl.
		return "https://api.minimaxi.com/v1"
	case "longcat":
		return "https://api.longcat.chat/openai"
	case "modelscope":
		return "https://api-inference.modelscope.cn/v1"
	case "mimo":
		return "https://api.xiaomimimo.com/v1"
	default:
		return ""
	}
}
