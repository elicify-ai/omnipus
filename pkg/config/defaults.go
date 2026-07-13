// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg"
)

// DefaultConfig returns the default configuration for Omnipus.
func DefaultConfig() *Config {
	// Determine the base path for the workspace via the single mandatory
	// home-dir helper (OmnipusHomeDir, pkg/config/home.go) — every subsystem
	// MUST resolve $OMNIPUS_HOME / ~/.omnipus / the secure-temp-dir fallback
	// through that one helper so behavior (relative-path resolution, 0700
	// temp-dir fallback when UserHomeDir fails) stays consistent everywhere
	// instead of being reimplemented ad hoc. In the common case (OMNIPUS_HOME
	// unset, a real $HOME) this produces the same
	// filepath.Join(userHome, pkg.DefaultOmnipusHome) result as before.
	homePath := OmnipusHomeDir()
	workspacePath := filepath.Join(homePath, pkg.WorkspaceName)

	return &Config{
		Version: CurrentVersion,
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Workspace:                 workspacePath,
				RestrictToWorkspace:       true,
				Provider:                  "",
				MaxTokens:                 32768,
				Temperature:               nil, // nil means use provider default
				MaxToolIterations:         200,
				SummarizeMessageThreshold: 20,
				SummarizeTokenPercent:     75,
				SteeringMode:              "one-at-a-time",
				ToolFeedback: ToolFeedbackConfig{
					Enabled:       false,
					MaxArgsLength: 300,
				},
				SplitOnMarker:  false,
				TimeoutSeconds: 0, // disabled; OpenRouter queue delays make fixed timeouts unreliable
			},
		},
		Bindings: []AgentBinding{},
		Session: SessionConfig{
			DMScope: "per-channel-peer",
		},
		// Channels starts as an empty map; no default instances are pre-seeded
		// (greenfield — FR-2.9). Channels are added via REST PUT /api/v1/channels/{id}/configure.
		Channels: map[string]ChannelInstanceConfig{},
		Hooks: HooksConfig{
			Enabled: true,
			Defaults: HookDefaultsConfig{
				ObserverTimeoutMS:    500,
				InterceptorTimeoutMS: 5000,
				ApprovalTimeoutMS:    60000,
			},
		},
		Providers: []*ModelConfig{
			// ============================================
			// Add your API key to the model you want to use
			// ============================================

			// Zhipu AI (智谱) - https://open.bigmodel.cn/usercenter/apikeys
			{
				ModelName: "glm-4.7",
				Model:     "zhipu/glm-4.7",
				APIBase:   "https://open.bigmodel.cn/api/paas/v4",
			},

			// OpenAI - https://platform.openai.com/api-keys
			{
				ModelName: "gpt-5.4",
				Model:     "openai/gpt-5.4",
				APIBase:   "https://api.openai.com/v1",
			},

			// Anthropic Claude - https://console.anthropic.com/settings/keys
			{
				ModelName: "claude-sonnet-4.6",
				Model:     "anthropic/claude-sonnet-4.6",
				APIBase:   "https://api.anthropic.com/v1",
			},

			// DeepSeek - https://platform.deepseek.com/
			{
				ModelName: "deepseek-chat",
				Model:     "deepseek/deepseek-chat",
				APIBase:   "https://api.deepseek.com/v1",
			},

			// Google Gemini - https://ai.google.dev/
			{
				ModelName: "gemini-2.0-flash",
				Model:     "gemini/gemini-2.0-flash-exp",
				APIBase:   "https://generativelanguage.googleapis.com/v1beta",
			},

			// Qwen (通义千问) - https://dashscope.console.aliyun.com/apiKey
			{
				ModelName: "qwen-plus",
				Model:     "qwen/qwen-plus",
				APIBase:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
			},

			// Moonshot (月之暗面) - https://platform.moonshot.cn/console/api-keys
			{
				ModelName: "moonshot-v1-8k",
				Model:     "moonshot/moonshot-v1-8k",
				APIBase:   "https://api.moonshot.cn/v1",
			},

			// Groq - https://console.groq.com/keys
			{
				ModelName: "llama-3.3-70b",
				Model:     "groq/llama-3.3-70b-versatile",
				APIBase:   "https://api.groq.com/openai/v1",
			},

			// OpenRouter (100+ models) - https://openrouter.ai/keys
			{
				ModelName: "openrouter-auto",
				Model:     "openrouter/auto",
				APIBase:   "https://openrouter.ai/api/v1",
			},
			{
				ModelName: "openrouter-gpt-5.4",
				Model:     "openrouter/openai/gpt-5.4",
				APIBase:   "https://openrouter.ai/api/v1",
			},

			// NVIDIA - https://build.nvidia.com/
			{
				ModelName: "nemotron-4-340b",
				Model:     "nvidia/nemotron-4-340b-instruct",
				APIBase:   "https://integrate.api.nvidia.com/v1",
			},

			// Cerebras - https://inference.cerebras.ai/
			{
				ModelName: "cerebras-llama-3.3-70b",
				Model:     "cerebras/llama-3.3-70b",
				APIBase:   "https://api.cerebras.ai/v1",
			},

			// Vivgrid - https://vivgrid.com
			{
				ModelName: "vivgrid-auto",
				Model:     "vivgrid/auto",
				APIBase:   "https://api.vivgrid.com/v1",
			},

			// Volcengine (火山引擎) - https://console.volcengine.com/ark
			{
				ModelName: "ark-code-latest",
				Model:     "volcengine/ark-code-latest",
				APIBase:   "https://ark.cn-beijing.volces.com/api/v3",
			},
			{
				ModelName: "doubao-pro",
				Model:     "volcengine/doubao-pro-32k",
				APIBase:   "https://ark.cn-beijing.volces.com/api/v3",
			},

			// ShengsuanYun (神算云)
			{
				ModelName: "deepseek-v3",
				Model:     "shengsuanyun/deepseek-v3",
				APIBase:   "https://api.shengsuanyun.com/v1",
			},

			// Antigravity (Google Cloud Code Assist) - OAuth only
			{
				ModelName:  "gemini-flash",
				Model:      "antigravity/gemini-3-flash",
				AuthMethod: "oauth",
			},

			// Ollama (local) - https://ollama.com
			{
				ModelName: "llama3",
				Model:     "ollama/llama3",
				APIBase:   "http://localhost:11434/v1",
			},

			// Mistral AI - https://console.mistral.ai/api-keys
			{
				ModelName: "mistral-small",
				Model:     "mistral/mistral-small-latest",
				APIBase:   "https://api.mistral.ai/v1",
			},

			// Avian - https://avian.io
			{
				ModelName: "deepseek-v3.2",
				Model:     "avian/deepseek/deepseek-v3.2",
				APIBase:   "https://api.avian.io/v1",
			},
			{
				ModelName: "kimi-k2.5",
				Model:     "avian/moonshotai/kimi-k2.5",
				APIBase:   "https://api.avian.io/v1",
			},

			// Minimax - https://api.minimax.io/
			{
				ModelName: "MiniMax-M2.5",
				Model:     "minimax/MiniMax-M2.5",
				APIBase:   "https://api.minimax.io/v1",
				ExtraBody: map[string]any{"reasoning_split": true},
			},

			// LongCat - https://longcat.chat/platform
			{
				ModelName: "LongCat-Flash-Thinking",
				Model:     "longcat/LongCat-Flash-Thinking",
				APIBase:   "https://api.longcat.chat/openai",
			},

			// ModelScope (魔搭社区) - https://modelscope.cn/my/tokens
			{
				ModelName: "modelscope-qwen",
				Model:     "modelscope/Qwen/Qwen3-235B-A22B-Instruct-2507",
				APIBase:   "https://api-inference.modelscope.cn/v1",
			},

			// VLLM (local) - http://localhost:8000
			{
				ModelName: "local-model",
				Model:     "vllm/custom-model",
				APIBase:   "http://localhost:8000/v1",
			},

			// Azure OpenAI - https://portal.azure.com
			// model_name is a user-friendly alias; the model field's path after "azure/" is your deployment name
			{
				ModelName: "azure-gpt5",
				Model:     "azure/my-gpt5-deployment",
				APIBase:   "https://your-resource.openai.azure.com",
			},
		},
		Gateway: GatewayConfig{
			Host: "127.0.0.1",
			Port: 5000,
			// FR-106: hot_reload defaults on so config changes take effect without a
			// restart on fresh installs. Operators who explicitly set hot_reload:false
			// in their config.json retain the old behavior (JSON value wins over default).
			HotReload: true,
			LogLevel:  "warn",
		},
		Sandbox: OmnipusSandboxConfig{
			// Seeded, fully-enumerated GLOBAL CEILING for a fresh install: every
			// static builtin tool defaults to "allow" except irreversible
			// delete_*/remove_* actions, which ask for confirmation. This is a
			// ceiling, not a grant — the runtime filter resolves global x agent
			// as most-restrictive-wins (pkg/agent/instance.go:agentToolsCfgToPolicy;
			// "a global deny always blocks"), so a global "allow" here can never
			// loosen an agent's own, independently-seeded policy
			// (pkg/coreagent/core.go's per-agent tools.builtin.policies, which
			// stays deny-by-default least-privilege per role). An operator/agent
			// policy MAY be set stricter than this ceiling (e.g. deny a delete_*
			// tool this map asks for) but never looser (e.g. allow one) — matching
			// the same one-line rule config.ValidateToolPolicyCoverage enforces
			// structurally: no default-policy fallback, only explicit, literal
			// entries (CLAUDE.md hard constraint 6). This map is a genuine
			// configuration value, not resolution-code logic — visible in
			// config.json's sandbox.tool_policies and editable at any time via
			// Settings -> Security -> Tool Policies or PUT /api/v1/security/tool-policies,
			// exactly like any operator-set entry.
			//
			// Every entry below mirrors pkg/coreagent/core.go's allStaticToolNames
			// literal-for-literal (77 tools: 31 general + 11 browser + 35 sysagent) —
			// pkg/config cannot import pkg/coreagent (coreagent already imports
			// config, so the reverse would cycle), so this list is a second,
			// independent hardcoded literal. A drift between the two is caught
			// loudly at boot by the same coverage validator, not silently ignored.
			ToolPolicies: map[string]string{
				// --- General builtin tools ---
				"bash":                "allow",
				"read_file":           "allow",
				"write_file":          "allow",
				"list_directory":      "allow",
				"edit_file":           "allow",
				"append_file":         "allow",
				"search_web":          "allow",
				"fetch_url":           "allow",
				"send_message":        "allow",
				"hand_off":            "allow",
				"return_to_default":   "allow",
				"send_file":           "allow",
				"find_skills":         "allow",
				"install_skill":       "allow",
				"delegate":            "allow",
				"list_tasks":          "allow",
				"create_task":         "allow",
				"update_task":         "allow",
				"delete_task":         "ask", // irreversible delete
				"list_agents":         "allow",
				"remember":            "allow",
				"recall_memory":       "allow",
				"run_retrospective":   "allow",
				"recall_conversation": "allow",
				"serve_web":           "allow",
				"set_todos":           "allow",
				"read_inbox":          "allow",
				"search_email":        "allow",
				"read_message":        "allow",
				"send_email":          "allow",
				"reply":               "allow",
				"load_tool":           "allow",

				// --- Browser automation tools ---
				"browser_navigate":   "allow",
				"browser_click":      "allow",
				"browser_type":       "allow",
				"browser_screenshot": "allow",
				"browser_get_text":   "allow",
				"browser_wait":       "allow",
				"browser_evaluate":   "allow",
				// ADR-041 D3 — tab-management tools.
				"browser_list_tabs":  "allow",
				"browser_switch_tab": "allow",
				"browser_close_tab":  "allow",
				"browser_open_tab":   "allow",

				// --- Sysagent management tools ---
				"navigate":                 "allow",
				"create_workspace":         "allow",
				"update_workspace":         "allow",
				"delete_workspace":         "ask", // irreversible delete
				"list_workspaces":          "allow",
				"get_workspace":            "allow",
				"read_agent_metadata":      "allow",
				"write_agent_metadata":     "allow",
				"configure_provider":       "allow",
				"list_providers":           "allow",
				"test_provider":            "allow",
				"list_models":              "allow",
				"run_doctor":               "allow",
				"get_usage":                "allow",
				"add_mcp_server":           "allow",
				"remove_mcp_server":        "ask", // irreversible delete
				"list_mcp_servers":         "allow",
				"create_skill":             "allow",
				"edit_skill":               "allow",
				"create_task_in_workspace": "allow",
				"update_task_in_workspace": "allow",
				"delete_task_in_workspace": "ask", // irreversible delete
				"list_tasks_in_workspace":  "allow",
				"remove_skill":             "ask", // irreversible delete
				"list_skills":              "allow",
				"enable_channel":           "allow",
				"configure_channel":        "allow",
				"disable_channel":          "allow", // reversible, not a delete
				"list_channels":            "allow",
				"test_channel":             "allow",
				"get_config":               "allow",
				"set_config":               "allow",
				"create_agent":             "allow",
				"update_agent":             "allow",
				"delete_agent":             "ask", // irreversible delete
			},
		},
		Tools: ToolsConfig{
			FilterSensitiveData: true,
			FilterMinLength:     8,
			RunInWorkspace: RunInWorkspaceConfig{
				WarmupTimeoutSeconds: 60,
			},
			MediaCleanup: MediaCleanupConfig{
				ToolConfig: ToolConfig{
					Enabled: true,
				},
				MaxAge:   30,
				Interval: 5,
			},
			Web: WebToolsConfig{
				ToolConfig: ToolConfig{
					Enabled: true,
				},
				PreferNative:    true,
				Proxy:           "",
				FetchLimitBytes: 10 * 1024 * 1024, // 10MB by default
				Format:          "plaintext",
				Brave: BraveConfig{
					Enabled:    false,
					MaxResults: 5,
				},
				Tavily: TavilyConfig{
					Enabled:    false,
					MaxResults: 5,
				},
				DuckDuckGo: DuckDuckGoConfig{
					Enabled:    true,
					MaxResults: 5,
				},
				Perplexity: PerplexityConfig{
					Enabled:    false,
					MaxResults: 5,
				},
				SearXNG: SearXNGConfig{
					Enabled:    false,
					BaseURL:    "",
					MaxResults: 5,
				},
				GLMSearch: GLMSearchConfig{
					Enabled:      false,
					BaseURL:      "https://open.bigmodel.cn/api/paas/v4/web_search",
					SearchEngine: "search_std",
					MaxResults:   5,
				},
				BaiduSearch: BaiduSearchConfig{
					Enabled:    false,
					BaseURL:    "https://qianfan.baidubce.com/v2/ai_search/web_search",
					MaxResults: 10,
				},
			},
			Cron: CronToolsConfig{
				ToolConfig: ToolConfig{
					Enabled: true,
				},
				ExecTimeoutMinutes: 5,
				AllowCommand:       true,
			},
			Exec: ExecConfig{
				ToolConfig: ToolConfig{
					Enabled: true,
				},
			},
			// Browser automation is a standard built-in tool — enabled by default
			// like exec/web/cron. Headless on by default for server use;
			// browser_evaluate stays opt-in (EvaluateEnabled=false) per SEC-04/SEC-06.
			Browser: BrowserToolConfig{
				ToolConfig: ToolConfig{
					Enabled: true,
				},
				Headless: true,
				// ADR-038: the live interactive browser panel and take-control
				// are both on by default — operators can disable either via
				// config (LiveViewEnabled=false drops the second listener
				// entirely; TakeControlEnabled=false keeps it watch-only).
				LiveViewEnabled:    true,
				TakeControlEnabled: true,
			},
			Skills: SkillsToolsConfig{
				ToolConfig: ToolConfig{
					Enabled: true,
				},
				Marketplaces: []MarketplaceConfig{
					{
						Name:    "clawhub",
						Type:    MarketplaceTypeClawHub,
						Enabled: true,
						BaseURL: "https://clawhub.ai",
					},
				},
				MaxConcurrentSearches: 2,
				SearchCache: SearchCacheConfig{
					MaxSize:    50,
					TTLSeconds: 300,
				},
			},
			SendFile: ToolConfig{
				Enabled: true,
			},
			MCP: MCPConfig{
				ToolConfig: ToolConfig{
					Enabled: false,
				},
				Discovery: ToolDiscoveryConfig{
					Enabled:          false,
					TTL:              5,
					MaxSearchResults: 5,
					UseBM25:          true,
					UseRegex:         false,
				},
				Servers: map[string]MCPServerConfig{},
			},
			AppendFile: ToolConfig{
				Enabled: true,
			},
			EditFile: ToolConfig{
				Enabled: true,
			},
			FindSkills: ToolConfig{
				Enabled: true,
			},
			InstallSkill: ToolConfig{
				Enabled: true,
			},
			ListDir: ToolConfig{
				Enabled: true,
			},
			Message: ToolConfig{
				Enabled: true,
			},
			ReadFile: ReadFileToolConfig{
				Enabled:         true,
				MaxReadFileSize: 64 * 1024, // 64KB
			},
			WebFetch: ToolConfig{
				Enabled: true,
			},
			WriteFile: ToolConfig{
				Enabled: true,
			},
			TaskList: ToolConfig{
				Enabled: true,
			},
			TaskCreate: ToolConfig{
				Enabled: true,
			},
			TaskUpdate: ToolConfig{
				Enabled: true,
			},
			Manifest: ManifestConfig{
				Compressed: true,
			},
		},
		Schedules: SchedulesConfig{
			MaxConcurrentRuns: DefaultSchedulesMaxConcurrentRuns,
			RunTimeoutSeconds: DefaultSchedulesRunTimeoutSeconds,
			RetryBackoffMs:    append([]int64(nil), DefaultSchedulesRetryBackoffMs...),
		},
		Devices: DevicesConfig{
			Enabled:    false,
			MonitorUSB: true,
		},
		Voice: VoiceConfig{
			ModelName:         "",
			EchoTranscription: false,
		},
		BuildInfo: BuildInfo{
			Version:   Version,
			GitCommit: GitCommit,
			BuildTime: BuildTime,
			GoVersion: GoVersion,
		},
	}
}
