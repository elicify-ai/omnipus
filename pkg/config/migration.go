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
var removedChannelKeys = []string{"maixcam", "teams", "onebot"}

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

// restoreSkillDiscoveryDefaults repairs configs that persisted the skill-
// discovery tool defaults as disabled/empty.
//
// Background (the bug): loadConfig starts from DefaultConfig() (which has
// find_skills/install_skill enabled and ClawHub {enabled:true,
// base_url:"https://clawhub.ai"}) then unmarshals the on-disk JSON over it. A
// config.json that was written from a partially-populated or zero-valued struct
// carries the JSON literals `"enabled": false` / `"base_url": ""` for these
// fields, which OVERWRITE the in-memory defaults at unmarshal time. The result:
// the agent loop builds its skills RegistryManager from a ClawHub config with
// Enabled=false, NewRegistryManagerFromConfig skips it, and the agent's
// find_skills/install_skill tools reach ZERO registries — while the UI works
// because it constructs the ClawHub client directly (gateway.go), defaulting
// the URL and ignoring the enabled flag.
//
// This restores the DefaultConfig() values ONLY when the corresponding key is
// ABSENT from the raw JSON (no operator intent): a missing key means "apply
// the default", an explicitly-present key (e.g. `"enabled": false`) is honored as operator
// intent and left untouched. This way an operator who deliberately disabled
// ClawHub or the skill tools keeps that setting, while every legacy/onboarded
// config that merely never wrote these fields (or wrote zero values for them
// before they had defaults) is healed back to the working default.
//
// Empty BaseURL is always healed to the default URL even when the key is
// present, because an empty base_url is never a meaningful operator choice and
// the UI already treats empty-as-default (NewClawHubRegistry). This keeps
// agent↔UI parity: both reach https://clawhub.ai when no URL is configured.
//
// Operates on the unified Marketplaces list (FR-10.1). The clawhub entry is
// located by Type=="clawhub" (or Name=="clawhub"); if no such entry exists, one
// is appended with the defaults. Operator intent for the clawhub entry's
// "enabled" flag is detected in the raw JSON across BOTH shapes: the new
// "marketplaces" list shape and the legacy "registries.clawhub" singleton
// shape (which migrateMarketplaces has already converted into the list by the
// time this runs).
func restoreSkillDiscoveryDefaults(cfg *Config, raw []byte) {
	if cfg == nil {
		return
	}
	defaults := DefaultConfig()

	// Parse the raw "tools" object so we can tell "key absent" (apply default)
	// from "key present" (honor operator intent). Best-effort: on any parse
	// failure we fall back to absence semantics, which is the safe, healing
	// direction for this fix.
	var top struct {
		Tools map[string]json.RawMessage `json:"tools"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &top)
	}

	// jsonKeyPresent reports whether nested key path exists in the raw tools
	// object. Each segment must resolve to a JSON object except the final one,
	// whose mere presence is what we test.
	jsonKeyPresent := func(path ...string) bool {
		if len(path) == 0 {
			return false
		}
		cur := top.Tools
		for i, seg := range path {
			rawVal, ok := cur[seg]
			if !ok {
				return false
			}
			if i == len(path)-1 {
				return true
			}
			var next map[string]json.RawMessage
			if err := json.Unmarshal(rawVal, &next); err != nil {
				return false
			}
			cur = next
		}
		return true
	}

	// find_skills.enabled — restore default (true) unless the operator wrote
	// an explicit find_skills.enabled value.
	if !jsonKeyPresent("find_skills", "enabled") {
		cfg.Tools.FindSkills.Enabled = defaults.Tools.FindSkills.Enabled
	}
	// install_skill.enabled — same treatment.
	if !jsonKeyPresent("install_skill", "enabled") {
		cfg.Tools.InstallSkill.Enabled = defaults.Tools.InstallSkill.Enabled
	}

	// ClawHub marketplace entry: heal Enabled (when no operator intent) and
	// BaseURL (always, when empty). Intent is detected across both shapes.
	clawHubEnabledExplicit, clawHubEnabledValue := clawHubEnabledIntent(top.Tools)
	if idx := findClawHubMarketplace(cfg); idx >= 0 {
		// Entry present — heal in place.
		if !clawHubEnabledExplicit {
			cfg.Tools.Skills.Marketplaces[idx].Enabled = defaults.Tools.Skills.Marketplaces[0].Enabled
		}
		if cfg.Tools.Skills.Marketplaces[idx].BaseURL == "" {
			cfg.Tools.Skills.Marketplaces[idx].BaseURL = defaults.Tools.Skills.Marketplaces[0].BaseURL
		}
		// Ensure the entry is typed as clawhub (defensive: a hand-edited entry
		// missing Type would otherwise be skipped by NewRegistryManagerFromConfig).
		if cfg.Tools.Skills.Marketplaces[idx].Type == "" {
			cfg.Tools.Skills.Marketplaces[idx].Type = "clawhub"
		}
		if cfg.Tools.Skills.Marketplaces[idx].Name == "" {
			cfg.Tools.Skills.Marketplaces[idx].Name = "clawhub"
		}
	} else {
		// No clawhub entry at all — append one. Enabled follows intent
		// detection (default true when no explicit disable); BaseURL is the
		// default.
		enabled := defaults.Tools.Skills.Marketplaces[0].Enabled
		if clawHubEnabledExplicit {
			enabled = clawHubEnabledValue
		}
		entry := MarketplaceConfig{
			Name:    "clawhub",
			Type:    MarketplaceTypeClawHub,
			Enabled: enabled,
			BaseURL: defaults.Tools.Skills.Marketplaces[0].BaseURL,
		}
		cfg.Tools.Skills.Marketplaces = append(cfg.Tools.Skills.Marketplaces, entry)
	}
	_ = clawHubEnabledValue // (read above via the tuple return)
}

// findClawHubMarketplace returns the index of the first marketplace entry
// identified as ClawHub (Type=="clawhub", or Name=="clawhub" when Type is
// empty), or -1 when none is present.
func findClawHubMarketplace(cfg *Config) int {
	for i, m := range cfg.Tools.Skills.Marketplaces {
		if m.Type == "clawhub" || (m.Type == "" && m.Name == "clawhub") {
			return i
		}
	}
	return -1
}

// clawHubEnabledIntent reports whether the raw tools JSON carries an explicit
// "enabled" value for the ClawHub marketplace, and if so, its value. It probes
// BOTH shapes:
//   - New shape: tools.skills.marketplaces[] → find entry where type=="clawhub"
//     (or name=="clawhub") and check for an "enabled" key.
//   - Legacy shape: tools.skills.registries.clawhub.enabled.
//
// The legacy probe runs even when the new-shape list is present, so a config
// in transition (both keys written) is handled conservatively: an explicit
// legacy enabled=false is honored.
func clawHubEnabledIntent(tools map[string]json.RawMessage) (explicit bool, value bool) {
	// Legacy singleton shape: tools.skills.registries.clawhub.enabled
	skillsRaw, ok := tools["skills"]
	if ok {
		var skills struct {
			Registries   json.RawMessage `json:"registries"`
			Marketplaces json.RawMessage `json:"marketplaces"`
		}
		if json.Unmarshal(skillsRaw, &skills) == nil {
			// Legacy path.
			if len(skills.Registries) > 0 {
				var reg struct {
					ClawHub json.RawMessage `json:"clawhub"`
				}
				if json.Unmarshal(skills.Registries, &reg) == nil && len(reg.ClawHub) > 0 {
					var ch struct {
						Enabled *bool `json:"enabled"`
					}
					if json.Unmarshal(reg.ClawHub, &ch) == nil && ch.Enabled != nil {
						return true, *ch.Enabled
					}
				}
			}
			// New list shape: tools.skills.marketplaces[]
			if len(skills.Marketplaces) > 0 {
				var list []map[string]json.RawMessage
				if json.Unmarshal(skills.Marketplaces, &list) == nil {
					for _, entry := range list {
						typeRaw, hasType := entry["type"]
						nameRaw, hasName := entry["name"]
						isClawHub := false
						if hasType {
							var t string
							if json.Unmarshal(typeRaw, &t) == nil && t == "clawhub" {
								isClawHub = true
							}
						}
						if !isClawHub && hasName {
							var n string
							if json.Unmarshal(nameRaw, &n) == nil && n == "clawhub" {
								isClawHub = true
							}
						}
						if !isClawHub {
							continue
						}
						enabledRaw, hasEnabled := entry["enabled"]
						if !hasEnabled {
							continue
						}
						var b bool
						if json.Unmarshal(enabledRaw, &b) == nil {
							return true, b
						}
					}
				}
			}
		}
	}
	return false, false
}

// migrateMarketplaces converts the pre-FR-10.1 skills config shape (a typed
// ClawHub singleton under tools.skills.registries plus a separate
// tools.skills.github block) into the unified tools.skills.marketplaces list.
//
// Idempotency is keyed off the RAW JSON, not the in-memory cfg: loadConfig
// starts from DefaultConfig() which pre-seeds Marketplaces with a clawhub
// entry, so a "len > 0" guard would short-circuit and never migrate old-shape
// configs. Instead:
//   - If the raw config carries a "marketplaces" key under tools.skills → it is
//     already new-shape (unmarshal populated cfg correctly) → no-op.
//   - Else if it carries legacy "registries" and/or "github" keys → build the
//     list from those keys, OVERWRITING any default-seeded entries so operator
//     values (enabled, base_url, paths, token_ref, proxy) are honored.
//   - Else (no skills section at all) → leave cfg as-is (DefaultConfig's
//     clawhub entry stands; restoreSkillDiscoveryDefaults will heal it).
func migrateMarketplaces(cfg *Config, raw []byte) {
	if cfg == nil || len(raw) == 0 {
		return
	}

	// v0 configs already build the Marketplaces list via configV0.Migrate() /
	// ToSkillsToolsConfig; re-parsing the v0 raw shape here would only lose
	// information (v0 github stored a plaintext token, dropped during Migrate).
	// Skip migration for v0 — only v1 configs (old-shape or new-shape) need it.
	var ver struct {
		Version int `json:"version"`
	}
	if json.Unmarshal(raw, &ver) == nil && ver.Version == 0 {
		return
	}

	var top struct {
		Tools map[string]json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return
	}
	skillsRaw, ok := top.Tools["skills"]
	if !ok {
		return
	}

	var skills struct {
		Marketplaces json.RawMessage `json:"marketplaces"`
		Registries   json.RawMessage `json:"registries"`
		Github       json.RawMessage `json:"github"`
	}
	if err := json.Unmarshal(skillsRaw, &skills); err != nil {
		return
	}

	// Already new-shape on disk → unmarshal populated cfg correctly; nothing to do.
	if len(skills.Marketplaces) > 0 {
		return
	}

	// No legacy keys either → leave DefaultConfig's clawhub entry in place.
	if len(skills.Registries) == 0 && len(skills.Github) == 0 {
		return
	}

	marketplaces := make([]MarketplaceConfig, 0, 2)

	// ClawHub singleton → clawhub marketplace entry.
	if len(skills.Registries) > 0 {
		var reg struct {
			ClawHub json.RawMessage `json:"clawhub"`
		}
		if json.Unmarshal(skills.Registries, &reg) == nil && len(reg.ClawHub) > 0 {
			var ch struct {
				Enabled         bool   `json:"enabled"`
				BaseURL         string `json:"base_url"`
				AuthTokenRef    string `json:"auth_token_ref"`
				SearchPath      string `json:"search_path"`
				SkillsPath      string `json:"skills_path"`
				DownloadPath    string `json:"download_path"`
				Timeout         int    `json:"timeout"`
				MaxZipSize      int    `json:"max_zip_size"`
				MaxResponseSize int    `json:"max_response_size"`
			}
			if json.Unmarshal(reg.ClawHub, &ch) == nil {
				marketplaces = append(marketplaces, MarketplaceConfig{
					Name:            "clawhub",
					Type:            MarketplaceTypeClawHub,
					Enabled:         ch.Enabled,
					BaseURL:         ch.BaseURL,
					AuthTokenRef:    ch.AuthTokenRef,
					SearchPath:      ch.SearchPath,
					SkillsPath:      ch.SkillsPath,
					DownloadPath:    ch.DownloadPath,
					Timeout:         ch.Timeout,
					MaxZipSize:      ch.MaxZipSize,
					MaxResponseSize: ch.MaxResponseSize,
				})
			}
		}
	}

	// Separate github block → github marketplace entry (only when configured).
	if len(skills.Github) > 0 {
		var gh struct {
			TokenRef string `json:"token_ref"`
			Proxy    string `json:"proxy"`
		}
		if json.Unmarshal(skills.Github, &gh) == nil && (gh.TokenRef != "" || gh.Proxy != "") {
			marketplaces = append(marketplaces, MarketplaceConfig{
				Name:     "github",
				Type:     MarketplaceTypeGitHub,
				Enabled:  true,
				TokenRef: gh.TokenRef,
				Proxy:    gh.Proxy,
			})
		}
	}

	if len(marketplaces) > 0 {
		cfg.Tools.Skills.Marketplaces = marketplaces
	}
}
