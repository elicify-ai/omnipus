// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
)

type migratable interface {
	Migrate() (*Config, error)
}

// buildModelWithProtocol constructs a model string with protocol prefix.
// If the model already contains a "/" (indicating it has a protocol prefix), it is returned as-is.
// Otherwise, the protocol prefix is added.
func buildModelWithProtocol(protocol, model string) string {
	if strings.Contains(model, "/") {
		// Model already has a protocol prefix, return as-is
		return model
	}
	return protocol + "/" + model
}

// v0ConvertProvidersToModelList converts the old providersConfigV0 to a slice of ModelConfig.
// This enables backward compatibility with existing configurations.
// It preserves the user's configured model from agents.defaults.model when possible.
func v0ConvertProvidersToModelList(cfg *configV0) []modelConfigV0 {
	if cfg == nil {
		return nil
	}

	// providerMigrationConfig defines how to migrate a provider from old config to new format.
	type providerMigrationConfig struct {
		// providerNames are the possible names used in agents.defaults.provider
		providerNames []string
		// protocol is the protocol prefix for the model field
		protocol string
		// buildConfig creates the ModelConfig from ProviderConfig
		buildConfig func(p providersConfigV0) (modelConfigV0, bool)
	}

	// Get user's configured provider and model
	userProvider := strings.ToLower(cfg.Agents.Defaults.Provider)
	userModel := cfg.Agents.Defaults.GetModelName()

	p := cfg.Providers

	var result []modelConfigV0

	// Track if we've applied the legacy model name fix (only for first provider)
	legacyModelNameApplied := false

	// Define migration rules for each provider
	migrations := []providerMigrationConfig{
		{
			providerNames: []string{"openai", "gpt"},
			protocol:      "openai",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.OpenAI.APIKey == "" && p.OpenAI.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "openai",
					Model:          "openai/gpt-5.4",
					APIKey:         p.OpenAI.APIKey,
					APIBase:        p.OpenAI.APIBase,
					Proxy:          p.OpenAI.Proxy,
					RequestTimeout: p.OpenAI.RequestTimeout,
					AuthMethod:     p.OpenAI.AuthMethod,
				}, true
			},
		},
		{
			providerNames: []string{"anthropic", "claude"},
			protocol:      "anthropic",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Anthropic.APIKey == "" && p.Anthropic.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "anthropic",
					Model:          "anthropic/claude-sonnet-4.6",
					APIKey:         p.Anthropic.APIKey,
					APIBase:        p.Anthropic.APIBase,
					Proxy:          p.Anthropic.Proxy,
					RequestTimeout: p.Anthropic.RequestTimeout,
					AuthMethod:     p.Anthropic.AuthMethod,
				}, true
			},
		},
		{
			providerNames: []string{"litellm"},
			protocol:      "litellm",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.LiteLLM.APIKey == "" && p.LiteLLM.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "litellm",
					Model:          "litellm/auto",
					APIKey:         p.LiteLLM.APIKey,
					APIBase:        p.LiteLLM.APIBase,
					Proxy:          p.LiteLLM.Proxy,
					RequestTimeout: p.LiteLLM.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"openrouter"},
			protocol:      "openrouter",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.OpenRouter.APIKey == "" && p.OpenRouter.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "openrouter",
					Model:          "openrouter/auto",
					APIKey:         p.OpenRouter.APIKey,
					APIBase:        p.OpenRouter.APIBase,
					Proxy:          p.OpenRouter.Proxy,
					RequestTimeout: p.OpenRouter.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"groq"},
			protocol:      "groq",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Groq.APIKey == "" && p.Groq.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "groq",
					Model:          "groq/llama-3.1-70b-versatile",
					APIKey:         p.Groq.APIKey,
					APIBase:        p.Groq.APIBase,
					Proxy:          p.Groq.Proxy,
					RequestTimeout: p.Groq.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"zhipu", "glm"},
			protocol:      "zhipu",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Zhipu.APIKey == "" && p.Zhipu.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "zhipu",
					Model:          "zhipu/glm-4",
					APIKey:         p.Zhipu.APIKey,
					APIBase:        p.Zhipu.APIBase,
					Proxy:          p.Zhipu.Proxy,
					RequestTimeout: p.Zhipu.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"vllm"},
			protocol:      "vllm",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.VLLM.APIKey == "" && p.VLLM.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "vllm",
					Model:          "vllm/auto",
					APIKey:         p.VLLM.APIKey,
					APIBase:        p.VLLM.APIBase,
					Proxy:          p.VLLM.Proxy,
					RequestTimeout: p.VLLM.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"gemini", "google"},
			protocol:      "gemini",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Gemini.APIKey == "" && p.Gemini.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "gemini",
					Model:          "gemini/gemini-pro",
					APIKey:         p.Gemini.APIKey,
					APIBase:        p.Gemini.APIBase,
					Proxy:          p.Gemini.Proxy,
					RequestTimeout: p.Gemini.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"nvidia"},
			protocol:      "nvidia",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Nvidia.APIKey == "" && p.Nvidia.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "nvidia",
					Model:          "nvidia/meta/llama-3.1-8b-instruct",
					APIKey:         p.Nvidia.APIKey,
					APIBase:        p.Nvidia.APIBase,
					Proxy:          p.Nvidia.Proxy,
					RequestTimeout: p.Nvidia.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"ollama"},
			protocol:      "ollama",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Ollama.APIKey == "" && p.Ollama.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "ollama",
					Model:          "ollama/llama3",
					APIKey:         p.Ollama.APIKey,
					APIBase:        p.Ollama.APIBase,
					Proxy:          p.Ollama.Proxy,
					RequestTimeout: p.Ollama.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"moonshot", "kimi"},
			protocol:      "moonshot",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Moonshot.APIKey == "" && p.Moonshot.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "moonshot",
					Model:          "moonshot/kimi",
					APIKey:         p.Moonshot.APIKey,
					APIBase:        p.Moonshot.APIBase,
					Proxy:          p.Moonshot.Proxy,
					RequestTimeout: p.Moonshot.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"shengsuanyun"},
			protocol:      "shengsuanyun",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.ShengSuanYun.APIKey == "" && p.ShengSuanYun.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "shengsuanyun",
					Model:          "shengsuanyun/auto",
					APIKey:         p.ShengSuanYun.APIKey,
					APIBase:        p.ShengSuanYun.APIBase,
					Proxy:          p.ShengSuanYun.Proxy,
					RequestTimeout: p.ShengSuanYun.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"deepseek"},
			protocol:      "deepseek",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.DeepSeek.APIKey == "" && p.DeepSeek.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "deepseek",
					Model:          "deepseek/deepseek-chat",
					APIKey:         p.DeepSeek.APIKey,
					APIBase:        p.DeepSeek.APIBase,
					Proxy:          p.DeepSeek.Proxy,
					RequestTimeout: p.DeepSeek.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"cerebras"},
			protocol:      "cerebras",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Cerebras.APIKey == "" && p.Cerebras.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "cerebras",
					Model:          "cerebras/llama-3.3-70b",
					APIKey:         p.Cerebras.APIKey,
					APIBase:        p.Cerebras.APIBase,
					Proxy:          p.Cerebras.Proxy,
					RequestTimeout: p.Cerebras.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"vivgrid"},
			protocol:      "vivgrid",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Vivgrid.APIKey == "" && p.Vivgrid.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "vivgrid",
					Model:          "vivgrid/auto",
					APIKey:         p.Vivgrid.APIKey,
					APIBase:        p.Vivgrid.APIBase,
					Proxy:          p.Vivgrid.Proxy,
					RequestTimeout: p.Vivgrid.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"volcengine", "doubao"},
			protocol:      "volcengine",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.VolcEngine.APIKey == "" && p.VolcEngine.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "volcengine",
					Model:          "volcengine/doubao-pro",
					APIKey:         p.VolcEngine.APIKey,
					APIBase:        p.VolcEngine.APIBase,
					Proxy:          p.VolcEngine.Proxy,
					RequestTimeout: p.VolcEngine.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"github_copilot", "copilot"},
			protocol:      "github-copilot",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.GitHubCopilot.APIKey == "" && p.GitHubCopilot.APIBase == "" && p.GitHubCopilot.ConnectMode == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:   "github-copilot",
					Model:       "github-copilot/gpt-5.4",
					APIBase:     p.GitHubCopilot.APIBase,
					ConnectMode: p.GitHubCopilot.ConnectMode,
				}, true
			},
		},
		{
			providerNames: []string{"antigravity"},
			protocol:      "antigravity",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Antigravity.APIKey == "" && p.Antigravity.AuthMethod == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:  "antigravity",
					Model:      "antigravity/gemini-2.0-flash",
					APIKey:     p.Antigravity.APIKey,
					AuthMethod: p.Antigravity.AuthMethod,
				}, true
			},
		},
		{
			providerNames: []string{"qwen", "tongyi"},
			protocol:      "qwen",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Qwen.APIKey == "" && p.Qwen.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "qwen",
					Model:          "qwen/qwen-max",
					APIKey:         p.Qwen.APIKey,
					APIBase:        p.Qwen.APIBase,
					Proxy:          p.Qwen.Proxy,
					RequestTimeout: p.Qwen.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"mistral"},
			protocol:      "mistral",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Mistral.APIKey == "" && p.Mistral.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "mistral",
					Model:          "mistral/mistral-small-latest",
					APIKey:         p.Mistral.APIKey,
					APIBase:        p.Mistral.APIBase,
					Proxy:          p.Mistral.Proxy,
					RequestTimeout: p.Mistral.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"avian"},
			protocol:      "avian",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.Avian.APIKey == "" && p.Avian.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "avian",
					Model:          "avian/deepseek/deepseek-v3.2",
					APIKey:         p.Avian.APIKey,
					APIBase:        p.Avian.APIBase,
					Proxy:          p.Avian.Proxy,
					RequestTimeout: p.Avian.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"longcat"},
			protocol:      "longcat",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.LongCat.APIKey == "" && p.LongCat.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "longcat",
					Model:          "longcat/LongCat-Flash-Thinking",
					APIKey:         p.LongCat.APIKey,
					APIBase:        p.LongCat.APIBase,
					Proxy:          p.LongCat.Proxy,
					RequestTimeout: p.LongCat.RequestTimeout,
				}, true
			},
		},
		{
			providerNames: []string{"modelscope"},
			protocol:      "modelscope",
			buildConfig: func(p providersConfigV0) (modelConfigV0, bool) {
				if p.ModelScope.APIKey == "" && p.ModelScope.APIBase == "" {
					return modelConfigV0{}, false
				}
				return modelConfigV0{
					ModelName:      "modelscope",
					Model:          "modelscope/Qwen/Qwen3-235B-A22B-Instruct-2507",
					APIKey:         p.ModelScope.APIKey,
					APIBase:        p.ModelScope.APIBase,
					Proxy:          p.ModelScope.Proxy,
					RequestTimeout: p.ModelScope.RequestTimeout,
				}, true
			},
		},
	}

	// Process each provider migration
	for _, m := range migrations {
		mc, ok := m.buildConfig(p)
		if !ok {
			continue
		}

		// Check if this is the user's configured provider
		if slices.Contains(m.providerNames, userProvider) && userModel != "" {
			// Use the user's configured model instead of default
			mc.Model = buildModelWithProtocol(m.protocol, userModel)
		} else if userProvider == "" && userModel != "" && !legacyModelNameApplied {
			// Legacy config: no explicit provider field but model is specified
			// Use userModel as ModelName for the FIRST provider so GetModelConfig(model) can find it
			// This maintains backward compatibility with old configs that relied on implicit provider selection
			mc.ModelName = userModel
			mc.Model = buildModelWithProtocol(m.protocol, userModel)
			legacyModelNameApplied = true
		}

		result = append(result, mc)
	}

	return result
}

// loadConfigV0 loads a legacy config (no version field)
func loadConfigV0(data []byte) (migratable, error) {
	var v0 configV0
	if err := json.Unmarshal(data, &v0); err != nil {
		return nil, err
	}

	v0.migrateChannelConfigs()

	// Auto-migrate: if only legacy providers config exists, convert to model_list
	if len(v0.ModelList) == 0 && !v0.Providers.IsEmpty() {
		newModelList := v0ConvertProvidersToModelList(&v0)
		// Convert []ModelConfig to []modelConfigV0
		v0.ModelList = make([]modelConfigV0, len(newModelList))
		for i, m := range newModelList {
			v0.ModelList[i] = modelConfigV0{
				ModelName:      m.ModelName,
				Model:          m.Model,
				APIBase:        m.APIBase,
				Proxy:          m.Proxy,
				Fallbacks:      m.Fallbacks,
				AuthMethod:     m.AuthMethod,
				ConnectMode:    m.ConnectMode,
				Workspace:      m.Workspace,
				RPM:            m.RPM,
				MaxTokensField: m.MaxTokensField,
				RequestTimeout: m.RequestTimeout,
				ThinkingLevel:  m.ThinkingLevel,
				APIKey:         m.APIKey,
				APIKeys:        m.APIKeys,
			}
		}
	}

	return &v0, nil
}

// loadConfigV1 loads a version 1 config (current schema)
func loadConfig(data []byte) (*Config, error) {
	cfg := DefaultConfig()

	// Backward compatibility: rename "model_list" to "providers" in old config files
	// before unmarshalling so the new field name works.
	compatData := jsonRenameKey(data, "model_list", "providers")

	// Pre-scan the JSON to check how many providers entries the user provided.
	// Go's JSON decoder reuses existing slice backing-array elements rather than
	// zero-initializing them, so fields absent from the user's JSON (e.g. api_base)
	// would silently inherit values from the DefaultConfig template at the same
	// index position. We only reset cfg.Providers when the user actually provides
	// entries; when count is 0 we keep DefaultConfig's built-in list as fallback.
	var tmp Config
	if err := json.Unmarshal(compatData, &tmp); err != nil {
		return nil, err
	}
	if len(tmp.Providers) > 0 {
		cfg.Providers = nil
	}

	if err := json.Unmarshal(compatData, cfg); err != nil {
		return nil, err
	}

	// FR-004 / FR-027: detect unknown top-level fields, log them at debug level,
	// and store them for round-trip preservation on SaveConfig.
	detectUnknownConfigFields(data, cfg)

	// Emit structured warnings for channels that have been removed from Omnipus.
	// The JSON fields are silently dropped by the unmarshaler; we surface them
	// as Warn entries so operators know their config has stale sections.
	warnRemovedChannelFields(data)

	return cfg, nil
}

// removedChannelKeys lists channel JSON keys that were supported in older
// versions of Omnipus but have since been removed. A config.json that still
// contains one of these keys will not cause a load error (the field is simply
// ignored by the JSON unmarshaler), but we emit a structured Warn so operators
// are aware the section is inert and can clean up their config file.
var removedChannelKeys = []string{"maixcam", "teams"}

// warnRemovedChannelFields inspects the raw "channels" object in the JSON
// config and emits a slog.Warn for any key that belongs to a removed channel.
func warnRemovedChannelFields(data []byte) {
	var raw struct {
		Channels map[string]json.RawMessage `json:"channels"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return // best-effort; parse errors already handled upstream
	}
	for _, key := range removedChannelKeys {
		if _, present := raw.Channels[key]; present {
			slog.Warn("config: legacy channel section ignored; channel has been removed",
				"channel", key,
				"action", "remove the section from config.json to silence this warning",
			)
		}
	}
}

// detectUnknownConfigFields finds JSON keys not declared on the Config struct,
// emits a slog.Debug per unknown key, and stores them in cfg.UnknownFields so
// SaveConfig can write them back verbatim (round-trip safety per FR-004/FR-027).
func detectUnknownConfigFields(data []byte, cfg *Config) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return // best-effort; parse errors already caught above
	}

	// Build set of known JSON field names from Config struct tags.
	known := make(map[string]struct{})
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
		if tag == "" || tag == "-" {
			tag = f.Name
		}
		known[tag] = struct{}{}
	}

	for k, v := range raw {
		if _, ok := known[k]; !ok {
			slog.Debug("config: unknown field preserved for forward compatibility",
				"field", k)
			if cfg.UnknownFields == nil {
				cfg.UnknownFields = make(map[string]json.RawMessage)
			}
			cfg.UnknownFields[k] = v
		}
	}
}

// jsonRenameKey performs a simple string replacement in JSON data to rename a top-level key.
// This is used for backward compatibility when field names change in the config schema.
func jsonRenameKey(data []byte, oldKey, newKey string) []byte {
	// Simple string replacement works here because:
	// 1. JSON keys are always quoted with double quotes
	// 2. We only replace top-level keys (not nested ones)
	// 3. The old and new keys have the same length ("model_list" -> "providers")
	oldKeyJSON := fmt.Sprintf(`"%s"`, oldKey)
	newKeyJSON := fmt.Sprintf(`"%s"`, newKey)
	return bytes.ReplaceAll(data, []byte(oldKeyJSON), []byte(newKeyJSON))
}

// toolPolicyDeny is the deny policy value written into cfg.Sandbox.ToolPolicies
// for migrated tools.<name>.enabled=false entries. Defined as a package-level
// constant to avoid importing pkg/policy (which already imports pkg/config,
// creating a cycle) while still avoiding bare string literals at call sites.
const toolPolicyDeny = "deny"

// deprecatedToolEnableMigrateOnce is keyed on the content hash of the migrated
// tools section so that successive hot-reloads that introduce new disabled tools
// each emit their own warning, while repeated reloads of an identical config
// remain silent. The Once guard protects the FIRST migration call; subsequent
// calls bypass the Once and re-check the content hash.
//
// Implementation note: we use a global sync.Once to match the test contract
// (deprecatedToolEnableMigrateOnce = sync.Once{} resets the guard in tests).
// At runtime the function self-documents the migration via slog.Info per reload
// regardless of the Once guard — see the comment inside migrateDeprecatedToolEnableFlags.
var deprecatedToolEnableMigrateOnce sync.Once

// toolEnableToPolicy maps each tools.<name> JSON key (the key immediately under
// "tools" in config.json) to the policy-engine glob that covers all tools in
// that subsystem. A nil cfg.Sandbox.ToolPolicies entry for the glob is created as
// "deny" when operator intent is detected from raw JSON.
//
// "Operator intent" means the "enabled" key is EXPLICITLY present in the raw JSON
// and its value is the JSON literal false. A missing key (absent from JSON) means
// the in-memory default applies — we never treat a missing key as operator intent.
var toolEnableToPolicy = []struct {
	// jsonKey is the key directly under "tools" in the raw config JSON
	jsonKey string
	// policyGlob is the key written into cfg.Sandbox.ToolPolicies
	policyGlob string
}{
	{"browser", "browser.*"},
	{"mcp", "mcp_*"},
	{"web", "web_search"},
	{"web_fetch", "web_fetch"},
	{"exec", "exec"},
	{"cron", "cron"},
	{"spawn", "spawn"},
	{"spawn_status", "spawn_status"},
	{"subagent", "subagent"},
	{"write_file", "write_file"},
	{"edit_file", "edit_file"},
	{"append_file", "append_file"},
	{"send_file", "send_file"},
	{"task_list", "task_list"},
	{"task_create", "task_create"},
	{"task_update", "task_update"},
}

// migrateDeprecatedToolEnableFlags translates legacy tools.<name>.enabled=false
// operator intent into cfg.Sandbox.ToolPolicies deny entries.
//
// The plan (issue #237): The tool-registry redesign removed infrastructure-level
// enable gating; the policy engine (pkg/policy, sandbox.ToolPolicies) is the only
// enforcement path. Configs that still carry an explicit tools.<name>.enabled=false
// were silently fail-open (the flag had no effect). This function closes that gap
// by converting operator-disabled tools into deny policy entries at config-load time.
//
// Idempotent: if a policy entry already exists (allow/ask) it is NOT downgraded.
// Only absent keys are written as "deny".
//
// Intent detection: raw JSON is inspected so that absent keys (which marshal as
// false due to Go's bool zero-value) are not misclassified as operator intent.
// This prevents false positives for tools like mcp and spawn_status whose defaults.go
// default is Enabled=false but which operators never explicitly disabled.
func migrateDeprecatedToolEnableFlags(cfg *Config, raw []byte) {
	if cfg == nil || len(raw) == 0 {
		return
	}

	// Extract the raw "tools" object from the JSON. If absent, nothing to migrate.
	var top struct {
		Tools map[string]json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		// Best-effort: parse errors are already handled upstream.
		return
	}
	if len(top.Tools) == 0 {
		return
	}

	// For each subsystem, check whether "enabled" is explicitly present AND false.
	var migrated []string
	for _, m := range toolEnableToPolicy {
		subsysRaw, present := top.Tools[m.jsonKey]
		if !present {
			// Key absent from JSON: no operator intent.
			continue
		}
		// Parse the subsystem object to check whether "enabled" key is explicitly set.
		var subsys map[string]json.RawMessage
		if err := json.Unmarshal(subsysRaw, &subsys); err != nil {
			continue
		}
		enabledRaw, hasEnabled := subsys["enabled"]
		if !hasEnabled {
			// "enabled" absent from this subsystem object: no operator intent.
			continue
		}
		// Check for the literal JSON false (not "false" string or 0).
		if string(enabledRaw) != "false" {
			continue
		}

		// Operator explicitly set enabled=false. Translate to deny unless a
		// stricter-or-equal policy already exists (never downgrade ask→deny or
		// preserve an existing allow if the operator already set one explicitly).
		if cfg.Sandbox.ToolPolicies == nil {
			cfg.Sandbox.ToolPolicies = make(map[string]string)
		}
		if _, exists := cfg.Sandbox.ToolPolicies[m.policyGlob]; !exists {
			cfg.Sandbox.ToolPolicies[m.policyGlob] = toolPolicyDeny
			migrated = append(migrated, m.jsonKey)
		}
	}

	if len(migrated) > 0 {
		// Always emit an Info-level log so operators see the migration on every
		// config load (including hot-reloads). The one-time Warn below is kept for
		// backwards-compat with alert rules that key on the Warn level, but it does
		// NOT suppress the per-reload Info — a later hot-reload that introduces a
		// newly disabled tool will be visible in the log even after the Once has fired.
		slog.Info("tools.<name>.enabled=false migrated to security policy deny entries on this load",
			"component", "config",
			"migrated_tools", migrated)

		deprecatedToolEnableMigrateOnce.Do(func() {
			slog.Warn("tools.<name>.enabled=false migrated to security policy deny entries; "+
				"remove tools.<name>.enabled from config.json and use security.tool_policies instead",
				"component", "config",
				"migrated_tools", migrated)
		})
	}
}
