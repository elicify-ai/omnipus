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
				Home:                  workspacePath,
				RestrictToWorkspace:   true,
				Provider:              "",
				MaxTokens:             32768,
				Temperature:           nil, // nil means use provider default
				MaxToolIterations:     200,
				SummarizeTokenPercent: 75,
				SteeringMode:          "one-at-a-time",
				// Concurrency-gate consolidation (2026-08-04, commit
				// 536b7340's follow-up fix): SubTurn.MaxConcurrent is
				// deliberately left UNSET (Go zero value) rather than seeded.
				// A fresh install previously seeded this to a fixed 16
				// (ADR-057 FR-095 / grill #2 M2-1) so getSubTurnConfig's and
				// ResolveRootDelegationCap's `if maxConcurrent <= 0` fallback
				// branch (pkg/agent/subturn.go, pkg/agent/admission.go) would
				// never fire — that reasoning depended on
				// Performance.EffectiveMaxParallelAgents() ALSO being
				// hard-clamped to 16 at the time. 536b7340 removed that
				// ceiling, so a fixed 16 seed here would become a SECOND,
				// independent concurrency cap silently disagreeing with the
				// operator's own max_parallel_agents setting — leaving this
				// field at zero makes Performance.EffectiveMaxParallelAgents()
				// the single, central authority both fallback branches
				// resolve to, with no seeded value to drift out of sync. See
				// SubTurnConfig.MaxConcurrent's doc comment (config.go).
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
			// ADR-057 FR-067 (grill m-5, operator decision 2): seeded
			// explicitly so a fresh install's config.json is
			// self-documenting; EffectiveStatsFlushInterval re-applies the
			// same default for any zeroed-out value.
			StatsFlushInterval: duration(DefaultSessionStatsFlushInterval),
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
			// Read+execute-only toolchain directories. Seeded as install-time
			// DATA (an operator can edit or empty it in config.json), not a
			// fallback branch in the binary. loadConfig unmarshals the
			// operator's JSON over DefaultConfig(), so this seed reaches
			// EXISTING installs whose config.json predates the key, and is
			// fully replaced — including to empty — when the key is present.
			AllowedExecPaths: DefaultAllowedExecPaths(),

			// ADR-062: reads and program execution are OPEN by default; writes
			// are confined exactly as before, and Omnipus's own secrets
			// (master.key, credentials.json, config.json, cli.token, entities/)
			// stay unreachable to sandboxed children on both macOS and Linux.
			//
			// This is the default for EVERY install, upgrading ones included
			// (operator decision, 2026-08-12). loadConfig unmarshals the
			// operator's JSON over DefaultConfig(), so a config.json with no
			// filesystem_model key picks this up on the next boot — a real
			// posture change on upgrade, and an intended one. The confined model
			// does not work in practice: the set of paths a working toolchain
			// reads cannot be enumerated in advance, so leaving existing installs
			// on it means the bug stays unfixed for precisely the people already
			// running the product. Ships with a release note.
			//
			// An operator who wants the old behaviour sets
			// filesystem_model: "confined" explicitly, and that choice is
			// honoured — the seed is data, never a fallback branch.
			FilesystemModel: string(FilesystemModelOpen),
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
			// literal-for-literal (the full static catalog; the pkg/gateway
			// catalog-sync test is authoritative for the count, not this comment) —
			// pkg/config cannot import pkg/coreagent (coreagent already imports
			// config, so the reverse would cycle), so this list is a second,
			// independent hardcoded literal. A drift between the two is caught
			// loudly at boot by the same coverage validator, not silently ignored.
			ToolPolicies: map[string]string{
				// --- General builtin tools ---
				"bash":           "allow",
				"read_file":      "allow",
				"write_file":     "allow",
				"list_directory": "allow",
				"edit_file":      "allow",
				"append_file":    "allow",
				"library_list":   "allow",
				"library_read":   "allow",
				// The operator approves each grant; see ADR-063 FR-7.2.
				"request_mount":       "ask",
				"search_web":          "allow",
				"fetch_url":           "allow",
				"send_message":        "allow",
				"hand_off":            "allow",
				"return_to_default":   "allow",
				"send_file":           "allow",
				"find_skills":         "allow",
				"install_skill":       "allow",
				"delegate":            "allow",
				"message_parent":      "allow",
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
				"navigate":             "allow",
				"create_workspace":     "allow",
				"update_workspace":     "allow",
				"delete_workspace":     "ask", // irreversible delete
				"list_workspaces":      "allow",
				"get_workspace":        "allow",
				"read_agent_metadata":  "allow",
				"write_agent_metadata": "allow",
				"configure_provider":   "allow",
				"list_providers":       "allow",
				"test_provider":        "allow",
				"list_models":          "allow",
				"run_doctor":           "allow",
				"get_usage":            "allow",
				// add_mcp_server is DENIED in the seeded default because an MCP
				// server definition is a program the gateway launches, and the
				// launched process is not confined by the sandbox. An agent that
				// can add one has escaped the cage through the front door:
				// config.json is in the ADR-062 secret set precisely so an agent
				// cannot write an MCP server entry with write_file, and this tool
				// wrote the same setting through the API.
				//
				// This matches the competitor threat model — Claude Code does not
				// sandbox MCP server processes either, and defends the boundary by
				// making .mcp.json unwritable by the agent. The control is "an
				// agent must not be able to ADD a server", not "a server must be
				// caged".
				//
				// "deny" rather than "ask": the approval modal does render the full
				// argument JSON, so the command is visible — but it is a generic,
				// scrollable dump with no dedicated command preview (that special
				// case exists only for `bash`), and the decision is turn-scoped
				// while the effect is permanent and applies at every subsequent
				// boot. An operator who has just asked for an MCP server set up
				// cannot tell that request apart from one injected by a page the
				// agent read.
				//
				// This is seeded DATA, not a code branch (CLAUDE.md constraint 6):
				// an operator who wants an agent to install MCP servers changes
				// this entry to "ask" or "allow" on their own install, in Settings
				// or config.json, and keeps that power.
				"add_mcp_server": "deny",
				// remove_mcp_server stays "ask", deliberately asymmetric: removing
				// a server narrows capability rather than widening it, destroys no
				// data, and is recoverable by re-adding the entry. The server name
				// the modal shows IS the whole decision surface for a removal,
				// unlike an add, where the decision surface is a command line.
				"remove_mcp_server": "ask",
				// list_mcp_servers is read-only and reports name/transport/enabled/
				// command/url only — never args or env — so it leaks no credential.
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

				// --- ADR-052 (autonomous agent plan execution) planning/
				// verifier tools --- Ceiling is "allow" for the three
				// plan-execution tools — never absent, never deny.
				//
				// It was seeded "ask" (the spec's literal FR-005/FR-027/DS-6
				// Test-2 seed matrix value) until 2026-07-28, when that turned
				// out to be the THIRD instance of the same landed defect the
				// inspect_session note below and the ADR-055 note further down
				// each record: the runtime global x agent merge is
				// strictest-wins, deny > ask > allow, applied whenever BOTH
				// sides have an entry
				// (pkg/tools/compositor.go:resolveEffectivePolicyWith). An
				// "ask" ceiling here therefore OVERRULED Jim's own seeded
				// "allow" (pkg/coreagent/core.go's IDJim case) and resolved
				// "ask" for him too — making FR-005/R2-06's "Jim is the ONLY
				// seeded agent granted unprompted plan-execution" dead on
				// every install, in a build where the seed data still read
				// exactly as the spec required.
				//
				// Observed cost before the fix: a 300 s stall per call.
				// run_task raised an approval nobody was there to answer, the
				// turn blocked on the default timeout in
				// pkg/gateway/approvals.go, and the tool never executed at all.
				//
				// Raising the ceiling grants these tools to NOBODY by itself —
				// it only raises the level an agent's own policy may be granted
				// UP TO. Every seeded agent except Jim carries an explicit
				// per-agent "ask" for all three (pkg/coreagent/core.go's
				// coreAgentSeed), the Judge carries an explicit "deny"
				// (systemAgentSeed, DS-6), and the Worker's sparse
				// tightenGlobalCeiling map carries its own explicit entries.
				// All of those still win under strictest-wins, so the only
				// resolution this change moves is Jim's, from "ask" to the
				// "allow" he was always seeded. This mirrors exactly what
				// inspect_session (below) and ADR-055's plan_correct/stop_plan
				// already do, for the same reason.
				//
				// Regression coverage:
				// pkg/coreagent/tool_policy_effective_resolution_test.go
				// resolves the seeds end-to-end through the real resolver
				// (tools.ResolveEffectivePolicy), so a future ceiling
				// tightening that silently overrules a seeded per-agent
				// "allow" — for these three tools OR any other, see that
				// file's bug-class test — fails the build rather than
				// shipping.
				// inspect_session is verifier-role-only (fix-wave finding #2,
				// architect F2 half 1). The runtime global x agent merge is
				// strictest-wins (deny > ask > allow,
				// pkg/tools/compositor.go:resolveEffectivePolicyWith), so a
				// ceiling "deny" here would have OVERRULED the Judge's own
				// seeded "allow" and resolved the Judge to deny — exactly
				// the landed defect this seed inverts. The ceiling therefore
				// seeds "allow" (raising the CEILING an agent's own policy
				// can be granted UP TO, same as every other non-destructive
				// tool — it does not, by itself, grant the tool to anyone);
				// EVERY seeded non-Judge agent carries an explicit per-agent
				// "deny" for inspect_session (pkg/coreagent/core.go's
				// coreAgentSeed/systemAgentSeed — denyAllThenOverride's
				// fully-enumerated deny-by-default already covers every
				// core/subagent-tier agent; the Worker's sparse
				// tightenGlobalCeiling map now carries an explicit override
				// too, since it would otherwise silently inherit this
				// ceiling's "allow"), so the strictest-wins merge still
				// resolves deny for everyone except the Judge, whose own
				// "allow" now merges cleanly against an "allow" ceiling.
				// Custom/unlisted agents are NOT deny-backfilled for this
				// tool — the coverage repair only fills gaps, and this
				// ceiling entry means inspect_session is never a gap — so a
				// custom agent with no override resolves "allow" at the
				// policy layer. Their real protection is the engine-set,
				// fail-closed verifier-session scope lock
				// (tools.VerifierSessionScopeAllows): a turn without the
				// scope is refused every session id regardless of policy.
				"create_plan":     "allow",
				"execute_plan":    "allow",
				"run_task":        "allow",
				"inspect_session": "allow",

				// --- ADR-055 (PlanSupervisor) supervision/containment ---
				// Both ceilings are "allow". Two independent reasons, both
				// recorded because either alone breaks the feature:
				//
				// 1. plan_correct: an "ask" or "deny" ceiling would OVERRULE
				//    PlanSupervisor's own seeded "allow" under the
				//    strictest-wins global x agent merge
				//    (pkg/tools/compositor.go:resolveEffectivePolicyWith) and
				//    the correction loop would be dead on every install —
				//    exactly the landed defect the inspect_session note above
				//    describes, on the very next tool. Raising the ceiling
				//    grants the tool to nobody by itself: every seeded agent
				//    except PlanSupervisor carries an explicit per-agent
				//    "deny" (pkg/coreagent's denyAllThenOverride), and the
				//    REAL control against an agent with no per-agent entry
				//    (e.g. one persisted before this tool name existed, which
				//    would inherit this "allow") is the engine's exact-identity
				//    gate on the correction path, not the policy layer.
				//
				// 2. stop_plan: an "ask" ceiling would silently defeat the
				//    FR-006b seeding rule. pkg/coreagent seeds stop_plan
				//    alongside execute_plan at the same per-agent value, so
				//    Jim gets "allow" — and an "ask" ceiling here would merge
				//    that back down to "ask", making the plan owner stopping
				//    their OWN plan depend on a human answering a prompt.
				//    (Until 2026-07-28 execute_plan's own ceiling was "ask"
				//    and this note cited that asymmetry as deliberate; it was
				//    in fact the same defect, and execute_plan's ceiling has
				//    since been raised to "allow" too — see the ADR-052 note
				//    above. The reasoning for stop_plan is unchanged and was
				//    always correct; only the contrast it drew is gone.)
				"plan_correct": "allow",
				"stop_plan":    "allow",

				// --- ADR-056 (list_jobs) background-job roster ---
				// "allow", for three reasons, the third of which is the one
				// that would actually break something:
				//
				// 1. It mutates nothing. list_jobs is strictly read-only, is
				//    fail-closed on an unresolvable caller identity (it refuses
				//    rather than returning the whole installation's roster),
				//    scopes every row to the calling principal, and bounds its
				//    own output. There is no destructive action for an "ask" to
				//    stand in front of.
				// 2. An "ask" ceiling would put a human prompt in front of an
				//    agent reading its OWN work roster, on every call — the
				//    highest-frequency, lowest-consequence call in the planning
				//    surface.
				// 3. Under the strictest-wins global x agent merge
				//    (pkg/tools/compositor.go:resolveEffectivePolicyWith) an
				//    "ask" ceiling would drag every per-agent "allow" down to
				//    "ask" — including Jim's. Jim's stop_plan resolves "allow"
				//    precisely so a runaway plan can be contained with no human
				//    in the loop, and stop_plan takes a PLAN ID. Gating the one
				//    tool that produces that id behind a prompt hands the human
				//    dependency straight back and makes the asymmetric stop_plan
				//    ceiling directly above pointless. Same defect shape as
				//    inspect_session and plan_correct, third time.
				//
				// As always, raising the ceiling grants the tool to nobody by
				// itself: the four base agents carry an explicit per-agent
				// "allow" and every other seeded agent an explicit "deny"
				// (pkg/coreagent/core.go's ROSTER VISIBILITY seed rule,
				// including the Worker's sparse-map deny — an absent key there
				// would inherit this "allow").
				"list_jobs": "allow",

				// --- ADR-068 D15.3 (FR-070) knowledge-base tools ---
				// All six seeded "allow" at the ceiling, read tier and the
				// three writes alike — superseding ADR-067 D17's nine (see
				// pkg/coreagent/core.go's allStaticToolNames for the
				// retirement). As everywhere else in this map, "allow" here
				// grants the tools to NOBODY by itself — it only raises the
				// level an agent's own policy may be granted up to, and
				// every seeded agent carries an explicit per-agent entry
				// (pkg/coreagent/core.go's coreAgentSeed: allow on all six
				// for Jim, allow-read + ask-write for Ava/Mia/Ray, explicit
				// deny on all six everywhere else including the Worker's
				// sparse map, where an absent key would silently INHERIT
				// this allow).
				//
				// An "ask" ceiling on the write three (knowledge_edit,
				// knowledge_restructure, knowledge_configure) would be the
				// same landed defect recorded four times above
				// (inspect_session, the ADR-052 three, plan_correct/
				// stop_plan, list_jobs): the runtime global x agent merge is
				// strictest-wins (pkg/tools/compositor.go:
				// resolveEffectivePolicyWith), so a stricter ceiling here
				// would drag Jim's deliberately-seeded "allow" back down to
				// "ask" and make his one intentional exception (argued in
				// his own coreAgentSeed case: he already holds unprompted
				// bash, so an ask-gate on these three would gate nothing
				// real for him) dead on every install while the seed data
				// still read exactly as intended.
				//
				// The real containment for these tools is not the ceiling:
				// it is the per-agent seed, the workspace-mount scoping the
				// read tier enforces itself, and the per-mutation audit
				// event (ADR-068's FR-090, carried forward from ADR-067
				// D19).
				// Read tier.
				"knowledge_describe": "allow",
				"knowledge_find":     "allow",
				"knowledge_read":     "allow",
				// Writes.
				"knowledge_edit":        "allow",
				"knowledge_restructure": "allow",
				"knowledge_configure":   "allow",
			},
		},
		// Planning holds the Planning & Goals epic's global loop bounds
		// (ADR-049 D7, spec Part A §G). Populated explicitly here (rather than
		// left zero and default-applied only at boot) so a fresh install's
		// config.json is self-documenting; validateBootConfig still applies
		// the same defaults for any field an operator zeroes out later.
		Planning: PlanningConfig{
			TaskMaxAttempts:      DefaultTaskMaxAttempts,
			GoalMaxRounds:        DefaultGoalMaxRounds,
			PlanJudgeMaxRounds:   DefaultPlanJudgeMaxRounds,
			LoopMaxRuns:          DefaultLoopMaxRuns,
			IdleExpiryDays:       DefaultIdleExpiryDays,
			GlobalActiveLoopCap:  DefaultGlobalActiveLoopCap,
			CheckTimeoutSeconds:  DefaultCheckTimeoutSeconds,
			VerifierWindowTokens: DefaultVerifierWindowTokens,
		},
		// SessionMessaging holds the ADR-053 §8 session-control-plane
		// operability config (FR-195's 21 keys). Seeded explicitly so a fresh
		// install's config.json is self-documenting and the plane is LIVE by
		// default (enabled=true). validateBootConfig re-applies the same
		// defaults for any field an operator zeroes out later.
		SessionMessaging: SessionMessagingConfig{
			// Fix-wave finding #4: these three are now *bool (nil = unset) —
			// see SessionMessagingConfig's own doc comment. boolPtr wraps the
			// named bool constants below since a constant cannot be addressed
			// directly (&DefaultSessionMessagingEnabled does not compile).
			Enabled:             boolPtr(DefaultSessionMessagingEnabled),
			WakeEnabled:         boolPtr(DefaultSessionMessagingWakeEnabled),
			AdjudicationEnabled: boolPtr(DefaultSessionMessagingAdjudicationEnabled),

			ChildSendRatePerMinute: DefaultSMChildSendRatePerMinute,
			ChildSendBody:          DefaultSMChildSendBodyBytes,
			ChildSendDepth:         DefaultSMChildSendMaxDepth,

			InboxUnackedMax:     DefaultSMInboxUnackedMax,
			InboxPerTypeCeiling: DefaultSMInboxPerTypeCeiling,

			SteerRatePerMinute: DefaultSMSteerRatePerMinute,
			SteerBody:          DefaultSMSteerBodyBytes,

			CancelGrace:   duration(DefaultSMCancelGrace),
			NeedsInputTTL: duration(DefaultSMNeedsInputTTL),

			WakeDebounce:   duration(DefaultSMWakeDebounce),
			WakeMaxPerHour: DefaultSMWakeMaxPerHour,

			IdleQuietWindow: duration(DefaultSMIdleQuietWindow),

			AttemptsMax:    DefaultSMAttemptsMax,
			JudgeRoundsMax: DefaultSMJudgeRoundsMax,

			MessageRetention:     DefaultSMMessageRetention,
			AuditRetention:       DefaultSMAuditRetention,
			UndeliveredRetention: DefaultSMUndeliveredRetention,
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
				// ADR-047: WebRTC media (audio+video) is on by default as a
				// progressive enhancement over the always-on JPEG fallback;
				// operators can disable it (WebRTCEnabled=false) to force
				// every viewer onto the JPEG tier. Google's public STUN
				// server is the default so ICE works out of the box; set
				// WebRTCStunServer="" for host-candidates-only.
				WebRTCEnabled:    true,
				WebRTCStunServer: "stun:stun.l.google.com:19302",
				// ADR-052 D2: default false — operators keep $PATH Chrome as the
				// winning source by default (operator autonomy preserved), BUT
				// ONLY when they have also opted into TrustPathChrome (the
				// SEC-ADR052-002 toggle below). With TrustPathChrome=false
				// (the default), $PATH Chrome is recorded but refused — see
				// BrowserToolConfig.PreferPackaged's doc comment for the full
				// interaction. Fleets that want the pinned package Chrome to
				// outrank $PATH for reproducibility flip BOTH fields to true
				// (post-onboarding, in config.json).
				PreferPackaged: false,
				// ADR-052 SEC-ADR052-002: default false — the resolver records a
				// $PATH Chrome but refuses to launch it (falls through to the
				// package Chrome + emits WARN-BROWSER-007). Operators with a
				// deliberate custom Chrome opt in with true.
				TrustPathChrome: false,
				// ADR-048 condition 1/Option A: default TRUE — WebRTC capture
				// requires the agent's session to share Chrome's default
				// browser context (see CaptureSharedContext's doc comment on
				// BrowserToolConfig for the full isolation warning). Operators
				// who need real cross-agent cookie isolation can set this
				// false; the JPEG browser_screencast fallback keeps working
				// either way.
				CaptureSharedContext: true,
				// Launch the shared Chrome during boot rather than on the
				// first browser tool call. Default TRUE: the lazy cold start
				// is expensive (ADR-042: ~30-60s on a fresh install) and
				// lands on a user-facing interaction, including the WebRTC
				// offer path where it must fit inside the browser
				// WebSocket's 60s read deadline. Best-effort — a warm-up
				// failure is logged and the lazy path still works.
				WarmAtBoot: true,
				// Warm the first TAB too, not just the Chrome process. Default
				// TRUE and cheap: a warmed Chrome with zero renderers still made
				// the first panel open build a browsing context + tab on demand
				// (measured 1.0-2.2s of a ~9.5s first open). A tab parked on the
				// static start page costs one idle renderer.
				WarmTabAtBoot: true,
				// Warm the WebRTC capture pipeline as well. Default TRUE: the
				// encoder page + ingest + negotiation are the largest remaining
				// share of a first open (1.7-6.7s measured) and the part that
				// fails first under load. Unlike the tab, this one costs
				// continuous CPU, so it stops itself after WarmCaptureIdleSec
				// with no viewer — see WarmCaptureAtBoot's doc comment.
				WarmCaptureAtBoot: true,
				// Conservative: 5 minutes covers "restart, then open the panel"
				// without leaving an unattended host encoding video forever.
				WarmCaptureIdleSec: 300,
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
