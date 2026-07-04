package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/media"
	"github.com/dapicom-ai/omnipus/pkg/memory"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/routing"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// AgentInstance represents a fully configured agent with its own workspace,
// session manager, context builder, and tool registry.
type AgentInstance struct {
	// mu protects Model, Provider, Candidates, and ThinkingLevel which may be
	// written by SwitchModel while runTurn reads them concurrently.
	mu sync.RWMutex

	ID        string
	Name      string
	Model     string
	Fallbacks []string
	// FallbackModels is the provider-aware fallback chain (FR-005 / FR-007).
	// Each entry carries its own Provider so a fallback can route through a
	// different provider than the primary — when set, Fallbacks (the legacy
	// []string field) is ignored at candidate-resolution time. The wire format
	// is [{model, provider}] (FR-005); legacy [string] entries are normalized
	// at config-load time via config.NormalizeFallbacks, so by the time this
	// slice reaches the agent every entry carries a populated Provider (or an
	// empty one if no configured provider matched).
	FallbackModels            []config.FallbackModel
	Workspace                 string
	MaxIterations             int
	MaxTokens                 int
	Temperature               float64
	ThinkingLevel             ThinkingLevel
	ContextWindow             int
	SummarizeMessageThreshold int
	SummarizeTokenPercent     int
	Provider                  providers.LLMProvider
	// providerPool is the atomic-pointer form of ProviderPool. The pool is
	// keyed by provider name and holds one LLMProvider instance per distinct
	// provider referenced by Candidates (or by the light tier). The fallback
	// chain looks up the right provider here so a fallback declared with
	// Provider="anthropic" actually uses the anthropic credentials at LLM-call
	// time — not the primary's openrouter credentials (FR-007).
	//
	// Eagerly built by buildProviderPool at agent construction (see
	// NewAgentInstance) — NOT lazily populated at lookup time. Reads in
	// GetProviderForCandidate and writes via StoreProviderPool (model switch)
	// are race-free because both go through the atomic.Pointer. Even with
	// the atomic load/store, ApplyAgentModel holds agent.mu around the
	// Model + Provider + Candidates + ProviderPool flip so an in-flight turn
	// never sees a torn (newModel, oldPool) state — the lock ensures the
	// tuple of fields swaps as one unit.
	providerPool   atomic.Pointer[map[string]providers.LLMProvider]
	Sessions       session.SessionStore
	ContextBuilder *ContextBuilder
	Tools          *tools.ToolRegistry
	Subagents      *config.SubagentsConfig
	// DelegationPolicy is the canonical unified delegation policy for this agent.
	// When non-nil, it takes precedence over Subagents.AllowAgents in CanSpawnSubagent.
	// Nil means fall back to legacy Subagents.AllowAgents check.
	DelegationPolicy *config.DelegationPolicy
	SkillsFilter     []string
	Candidates       []providers.FallbackCandidate

	// TimeoutSeconds is the per-turn hard timeout. 0 = disabled.
	// Populated from AgentDefaults.TimeoutSeconds; per-agent override if available.
	TimeoutSeconds int

	// AgentType is the resolved type string ("core", "custom", "system") used by
	// FilterToolsByPolicy at LLM-call assembly time (FR-003, FR-041). Set once at
	// construction; never mutated after creation.
	AgentType string

	// IsRoutingDefault records whether this agent was marked Default=true in
	// its AgentConfig at construction time. Used by GetDefaultAgent to make the
	// per-agent routing-default flag the single canonical source of truth (F3),
	// so SPA WebSocket chat and channel routing both converge on the same agent.
	// Read-only after construction.
	IsRoutingDefault bool

	// toolPolicy holds the per-agent tool policy snapshot used by
	// FilterToolsByPolicy at LLM-call assembly time (FR-003, FR-020, FR-041).
	// Populated at construction from config.AgentConfig.Tools and the global
	// sandbox policies (agentToolsCfgToPolicy). A global tool-policy PUT rebuilds
	// this instance via TriggerReload → NewAgentRegistry (not an in-place swap),
	// so the fresh snapshot carries the new GlobalPolicies. The turn assembly
	// Load()s the pointer on each tool call so a stale policy is never seen
	// mid-turn. The zero value (nil pointer) defaults to allow-all.
	toolPolicy atomic.Pointer[tools.ToolPolicyCfg]

	// Router is non-nil when model routing is configured and the light model
	// was successfully resolved. It scores each incoming message and decides
	// whether to route to LightCandidates or stay with Candidates.
	Router *routing.Router
	// LightCandidates holds the resolved provider candidates for the light model.
	// Pre-computed at agent creation to avoid repeated model_list lookups at runtime.
	LightCandidates []providers.FallbackCandidate
	// LightProvider is the concrete provider instance for the configured light model.
	// It is only used when routing selects the light tier for a turn.
	LightProvider providers.LLMProvider
}

// NewAgentInstance creates an agent instance from config.
func NewAgentInstance(
	agentCfg *config.AgentConfig,
	defaults *config.AgentDefaults,
	cfg *config.Config,
	provider providers.LLMProvider,
) *AgentInstance {
	workspace := resolveAgentWorkspace(agentCfg, defaults)
	// H11: escalate MkdirAll failure to Error — a missing workspace is serious.
	if mkErr := os.MkdirAll(workspace, 0o755); mkErr != nil {
		logger.ErrorCF("agent", "Failed to create agent workspace directory",
			map[string]any{"workspace": workspace, "error": mkErr.Error()})
	}

	model := resolveAgentModel(agentCfg, defaults)
	fallbacks := resolveAgentFallbacks(agentCfg, defaults)
	fallbackModels := resolveAgentFallbackModels(agentCfg, defaults)

	restrict := defaults.RestrictToWorkspace
	readRestrict := restrict && !defaults.AllowReadOutsideWorkspace

	// Compile path whitelist patterns from config.
	allowReadPaths := buildAllowReadPatterns(cfg)
	allowWritePaths := compilePatterns(cfg.Tools.AllowWritePaths)

	toolsRegistry := tools.NewToolRegistry()

	// All file-system and exec tools register unconditionally. Policy
	// (allow / ask / deny) decides whether an agent can actually invoke them.
	maxReadFileSize := cfg.Tools.ReadFile.MaxReadFileSize
	toolsRegistry.Register(tools.NewReadFileTool(workspace, readRestrict, maxReadFileSize, allowReadPaths))
	toolsRegistry.Register(tools.NewWriteFileTool(workspace, restrict, allowWritePaths))
	toolsRegistry.Register(tools.NewListDirTool(workspace, readRestrict, allowReadPaths))

	execTool, err := tools.NewExecToolWithConfig(workspace, restrict, cfg, allowReadPaths)
	if err != nil {
		logger.ErrorCF("agent", "Failed to initialize exec tool; continuing without exec",
			map[string]any{"error": err.Error()})
	} else {
		toolsRegistry.Register(execTool)
	}

	toolsRegistry.Register(tools.NewEditFileTool(workspace, restrict, allowWritePaths))
	toolsRegistry.Register(tools.NewAppendFileTool(workspace, restrict, allowWritePaths))

	// Resolve agentID early so the session store can tag sessions with the correct owner.
	agentID := routing.DefaultAgentID
	agentName := ""
	var subagents *config.SubagentsConfig
	var skillsFilter []string

	var delegationPolicy *config.DelegationPolicy
	if agentCfg != nil {
		agentID = routing.NormalizeAgentID(agentCfg.ID)
		agentName = agentCfg.Name
		subagents = agentCfg.Subagents
		skillsFilter = agentCfg.Skills
		delegationPolicy = agentCfg.DelegationPolicy
		// Emit startup WARN for inert delegation policy fields (accept_from, budget).
		delegationPolicy.WarnIfInertFieldsSet(agentCfg.ID)
	}

	sessionsDir := filepath.Join(workspace, "sessions")
	sessions := initSessionStore(sessionsDir, agentID, omnipusHome())

	mcpDiscoveryActive := cfg.Tools.MCP.Enabled && cfg.Tools.MCP.Discovery.Enabled
	contextBuilder := NewContextBuilder(workspace).
		WithToolDiscovery(
			mcpDiscoveryActive && cfg.Tools.MCP.Discovery.UseBM25,
			mcpDiscoveryActive && cfg.Tools.MCP.Discovery.UseRegex,
		).
		WithSplitOnMarker(cfg.Agents.Defaults.SplitOnMarker)

	if agentCfg != nil {
		contextBuilder.WithAgentInfo(agentID, agentName)
		// FR-9.4: install the per-agent skill allowlist so it is enforced at
		// skill-resolution time (default-DENY). agentCfg.Skills is nil when the
		// agent declares no allowlist → unrestricted; a non-nil list restricts
		// resolution and progressive disclosure to exactly those skills.
		contextBuilder.WithSkillAllowlist(agentCfg.Skills)
	}

	// Memory tools (FR-016, FR-017): register remember, recall_memory, and
	// run_retrospective for all agents except the main gateway agent.
	// recall_conversation is registered separately (loop.go) as it is session-scoped.
	// Subagents DO receive these tools — they are not in the ExcludedSpawn/
	// ExcludedSubagent/ExcludedHandoff lists so CloneExcept leaves them intact.
	// Note: there is no "omnipus-system" runtime agent (CLAUDE.md). The check
	// below is intentionally main-only.
	if agentID != "main" {
		memAdapter := NewMemoryStoreAdapter(contextBuilder.Memory())
		toolsRegistry.Register(tools.NewRememberTool(memAdapter, nil))
		toolsRegistry.Register(tools.NewRecallMemoryTool(memAdapter))
		toolsRegistry.Register(tools.NewRetrospectiveTool(memAdapter, nil))
	}

	// Per-turn tool-round cap: per-agent override wins, then the global
	// default, then 200. The per-agent field was persisted+displayed but never
	// applied before 2026-07-03 (P0) — the runtime silently ran everything on
	// the old emergency fallback of 20.
	maxIter := 0
	if agentCfg != nil {
		maxIter = agentCfg.MaxToolIterations
	}
	if maxIter <= 0 {
		maxIter = defaults.MaxToolIterations
	}
	if maxIter <= 0 {
		maxIter = 200
	}

	maxTokens := defaults.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	contextWindow := defaults.ContextWindow
	if contextWindow == 0 {
		// Default heuristic: 4x the output token limit.
		// Most models have context windows well above their output limits
		// (e.g., GPT-4o 128k ctx / 16k out, Claude 200k ctx / 8k out).
		// 4x is a conservative lower bound that avoids premature
		// summarization while remaining safe — the reactive
		// windowTrim (context paging) handles any overshoot.
		contextWindow = maxTokens * 4
	}

	temperature := 0.7
	if defaults.Temperature != nil {
		temperature = *defaults.Temperature
	}

	var thinkingLevelStr string
	if mc, err := cfg.GetModelConfig(model); err == nil {
		thinkingLevelStr = mc.ThinkingLevel
	}
	thinkingLevel := parseThinkingLevel(thinkingLevelStr)

	summarizeMessageThreshold := defaults.SummarizeMessageThreshold
	if summarizeMessageThreshold == 0 {
		summarizeMessageThreshold = 20
	}

	summarizeTokenPercent := defaults.SummarizeTokenPercent
	if summarizeTokenPercent == 0 {
		summarizeTokenPercent = 75
	}

	// Resolve fallback candidates. Prefer the provider-aware resolver when
	// AgentConfig.FallbackModels (or a derived equivalent) is populated so a
	// fallback can route through its own provider (FR-007). Falls back to the
	// legacy resolver when only the historical Fallbacks []string is set —
	// preserves the original behavior for configs that have not migrated to
	// the new wire shape.
	// O3 two-field model: when the agent pins an explicit primary provider, the
	// primary candidate routes through it directly (never inferred). Empty
	// provider preserves the pre-O3 selection exactly.
	primaryProvider := resolveAgentPrimaryProvider(agentCfg)
	candidates := resolveAgentCandidatesWithPrimaryProvider(
		cfg, defaults.Provider, model, primaryProvider, fallbackModels, fallbacks)

	// Pre-build the provider pool for every distinct provider referenced by
	// the resolved candidate chain. FR-007 requires each fallback to use its
	// own credentials at LLM-call time; building the pool here at
	// construction (vs. lazily inside the fallback hot path) keeps the
	// per-turn hot path allocation-free and surfaces credential / API-base
	// config errors at startup instead of mid-conversation.
	providerPool := buildProviderPool(cfg, candidates)

	// Model routing setup: pre-resolve light model candidates at creation time
	// to avoid repeated model_list lookups on every incoming message.
	var router *routing.Router
	var lightCandidates []providers.FallbackCandidate
	var lightProvider providers.LLMProvider
	if rc := defaults.Routing; rc != nil && rc.Enabled && rc.LightModel != "" {
		resolved := resolveModelCandidates(cfg, defaults.Provider, rc.LightModel, nil)
		if len(resolved) > 0 {
			lightModelCfg, err := resolvedModelConfig(cfg, rc.LightModel, workspace)
			if err != nil {
				logger.WarnCF("agent", "Routing light model config invalid; routing disabled",
					map[string]any{"light_model": rc.LightModel, "agent_id": agentID, "error": err.Error()})
			} else {
				lp, _, err := providers.CreateProviderFromConfig(lightModelCfg)
				if err != nil {
					logger.WarnCF("agent", "Routing light model provider init failed; routing disabled",
						map[string]any{"light_model": rc.LightModel, "agent_id": agentID, "error": err.Error()})
				} else {
					router = routing.New(routing.RouterConfig{
						LightModel: rc.LightModel,
						Threshold:  rc.Threshold,
					})
					lightCandidates = resolved
					lightProvider = lp
				}
			}
		} else {
			logger.WarnCF("agent", "Routing light model not found; routing disabled",
				map[string]any{"light_model": rc.LightModel, "agent_id": agentID})
		}
	}

	// Per-turn timeout from agent defaults (0 = disabled).
	// Clamp negative values to 0 (disabled) — a negative timeout is meaningless
	// and would cause context.WithTimeout to fire immediately.
	timeoutSeconds := defaults.TimeoutSeconds
	if timeoutSeconds < 0 {
		timeoutSeconds = 0
	}

	// Derive agent type and policy snapshot for LLM-call-time tool filtering (FR-003, FR-041).
	// agentType uses the config-stored Type when present; falls back to "custom" for
	// unrecognized types. The registry may upgrade this to "core" via SetAgentType()
	// for runtime-seeded agents (FR-045).
	resolvedAgentType := "custom"
	if agentCfg != nil {
		switch agentCfg.Type {
		case config.AgentTypeCore, config.AgentTypeSystem, config.AgentTypeWorker:
			resolvedAgentType = string(agentCfg.Type)
		case config.AgentTypeCustom:
			resolvedAgentType = "custom"
		}
	}
	// A worker is never the routing default — it is not a chat target. Even if a
	// tampered config marked a worker Default=true, refuse to carry that into the
	// runtime so GetDefaultAgent can never select it (defense in depth; the
	// config-load repair and the PUT handler also reject it).
	isRoutingDefault := agentCfg != nil && agentCfg.Default && !agentCfg.IsWorker()
	inst := &AgentInstance{
		ID:                        agentID,
		Name:                      agentName,
		Model:                     model,
		Fallbacks:                 fallbacks,
		FallbackModels:            fallbackModels,
		Workspace:                 workspace,
		MaxIterations:             maxIter,
		MaxTokens:                 maxTokens,
		Temperature:               temperature,
		ThinkingLevel:             thinkingLevel,
		ContextWindow:             contextWindow,
		SummarizeMessageThreshold: summarizeMessageThreshold,
		SummarizeTokenPercent:     summarizeTokenPercent,
		Provider:                  provider,
		Sessions:                  sessions,
		ContextBuilder:            contextBuilder,
		Tools:                     toolsRegistry,
		Subagents:                 subagents,
		SkillsFilter:              skillsFilter,
		Candidates:                candidates,
		Router:                    router,
		LightCandidates:           lightCandidates,
		LightProvider:             lightProvider,
		TimeoutSeconds:            timeoutSeconds,
		AgentType:                 resolvedAgentType,
		IsRoutingDefault:          isRoutingDefault,
		DelegationPolicy:          delegationPolicy,
	}
	// Publish the eagerly-built pool. StoreProviderPool uses the atomic
	// pointer; calling it here (vs. direct field assignment) keeps the
	// publish path identical to the model-switch path in ApplyAgentModel.
	inst.StoreProviderPool(providerPool)
	// O7: thread the global sandbox tool policies into the runtime policy
	// snapshot so FilterToolsByPolicy enforces global × agent merge at call
	// time. Build the policy even when the agent has no per-agent tools
	// config, because a global deny must still apply to that agent.
	var agentToolsCfg *config.AgentToolsCfg
	if agentCfg != nil {
		agentToolsCfg = agentCfg.Tools
	}
	inst.toolPolicy.Store(agentToolsCfgToPolicy(cfg, agentToolsCfg))
	return inst
}

// LoadToolPolicy returns the current tool policy snapshot for this agent.
// Returns nil when no policy has been stored (defaults to allow-all at call sites).
// Safe for concurrent access (atomic load, FR-020).
func (a *AgentInstance) LoadToolPolicy() *tools.ToolPolicyCfg {
	return a.toolPolicy.Load()
}

// StoreToolPolicy atomically replaces the agent's tool policy (FR-020).
// Passing nil resets to allow-all. Safe for concurrent access with ongoing
// turn assembly (atomic pointer swap).
//
// Note: production does NOT hot-swap policy via this setter — a global
// tool-policy PUT goes through TriggerReload → NewAgentRegistry, which rebuilds
// each instance with a fresh snapshot from agentToolsCfgToPolicy (see
// rest_tool_policies.go::putToolPolicies). This setter exists for that atomic
// guarantee and is exercised by the turn-recheck / reload-race tests, which use
// it to simulate a mid-turn policy swap.
func (a *AgentInstance) StoreToolPolicy(p *tools.ToolPolicyCfg) {
	a.toolPolicy.Store(p)
}

// GetProviderForCandidate returns the LLMProvider instance that should service
// the given fallback candidate (FR-007). When a candidate explicitly pins a
// Provider via the new [{model, provider}] wire shape, the fallback chain
// MUST use that provider's credentials — not the primary's.
//
// Lookup order:
//  1. ProviderPool by provider name (pre-populated at agent construction
//     time for every distinct provider referenced by Candidates /
//     LightCandidates).
//  2. Fall back to the agent's primary provider when the candidate has no
//     Provider pinned (legacy []string wire shape) or the pool entry is
//     missing (operator pre-build failure — log + degrade gracefully).
//
// This is the runtime half of the FR-007 contract: candidate chain carries
// the explicit provider (set in resolveModelCandidatesFromList) AND the
// fallback iteration routes through the matching provider instance here.
//
// Safe for concurrent access with StoreProviderPool (model switch). Both
// sides go through the atomic pointer, so concurrent map read+write is
// impossible by construction. ApplyAgentModel additionally holds agent.mu
// around the full Model+Provider+Candidates+ProviderPool flip so an
// in-flight turn never observes a torn (newModel, oldPool) state — see
// loop.go::ApplyAgentModel.
func (a *AgentInstance) GetProviderForCandidate(candidate providers.FallbackCandidate) providers.LLMProvider {
	if a == nil {
		return nil
	}
	pinned := strings.TrimSpace(candidate.Provider)
	if pinned == "" {
		// Legacy fallback path: every candidate uses the primary's provider.
		// Preserve pre-FR-007 behavior.
		return a.Provider
	}
	if pool := a.providerPool.Load(); pool != nil {
		if p, ok := (*pool)[pinned]; ok && p != nil {
			return p
		}
	}
	logger.WarnCF("agent", "GetProviderForCandidate: no pre-built provider for pinned name; falling back to primary",
		map[string]any{
			"agent_id": a.ID,
			"pinned":   pinned,
		})
	return a.Provider
}

// StoreProviderPool atomically replaces the agent's provider pool. Called by
// NewAgentInstance to publish the eagerly-built initial pool, and by
// ApplyAgentModel on model switch to ensure the new primary's provider is in
// the pool (the old primary is closed only after ApplyAgentModel finishes
// its Provider swap on the instance). Safe for concurrent access with
// in-flight turn assembly — readers in GetProviderForCandidate Load() the
// same atomic pointer and never observe a torn map state.
//
// The atomic pointer only protects the pointer swap; callers that need the
// new pool to be coherent with Model+Provider+Candidates (e.g. ApplyAgentModel)
// MUST additionally hold agent.mu around the full flip so an in-flight turn
// never reads (newModel, oldPool) — see loop.go::ApplyAgentModel.
func (a *AgentInstance) StoreProviderPool(pool map[string]providers.LLMProvider) {
	// Defensive copy: a caller that mutates the map after StoreProviderPool
	// would race with readers. Copy the keys+values so the published map is
	// owned by the agent instance from this point on.
	var publish *map[string]providers.LLMProvider
	if pool != nil {
		buf := make(map[string]providers.LLMProvider, len(pool))
		for k, v := range pool {
			buf[k] = v
		}
		publish = &buf
	}
	a.providerPool.Store(publish)
}

// buildProviderPool pre-builds an LLMProvider for every distinct provider
// name referenced by the agent's candidate chain (primary + fallbacks).
// Returns nil when the candidate chain has no explicit providers (every
// candidate routes through the agent primary — no pool needed).
//
// Build failures are non-fatal: a missing entry for a pinned provider name
// degrades to "use the primary's provider" via GetProviderForCandidate's
// fallback path. We log at WARN so operators can see the cause of a
// degraded fallback path at startup.
func buildProviderPool(cfg *config.Config, candidates []providers.FallbackCandidate) map[string]providers.LLMProvider {
	if cfg == nil || len(candidates) == 0 {
		return nil
	}
	// Collect distinct provider names. Empty Provider names share the
	// primary's provider via the legacy path — no pool entry needed.
	seen := make(map[string]struct{})
	for _, c := range candidates {
		name := strings.TrimSpace(c.Provider)
		if name == "" {
			continue
		}
		seen[name] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	pool := make(map[string]providers.LLMProvider, len(seen))
	for name := range seen {
		mc, err := findModelConfigForProvider(cfg, name)
		if err != nil {
			logger.WarnCF("agent", "buildProviderPool: no ModelConfig for candidate provider; pool entry skipped",
				map[string]any{"provider": name, "error": err.Error()})
			continue
		}
		p, _, err := providers.CreateProviderFromConfig(mc)
		if err != nil {
			logger.WarnCF("agent", "buildProviderPool: CreateProviderFromConfig failed; pool entry skipped",
				map[string]any{"provider": name, "error": err.Error()})
			continue
		}
		pool[name] = p
	}

	// Defensive fallback: if a candidate's Provider (which may be a vendor
	// namespace like "zai" or "z-ai" leaked from re-parsing the slash in
	// mc.Model) doesn't match any configured provider, scan cfg.Providers
	// for a passthrough entry whose Model matches the candidate's Model.
	// This is the safety net behind the resolver fix — if any future
	// change reintroduces the seeded-agent bug, the agent still routes
	// through the openrouter entry that has the matching model.
	// The original candidate's name is preserved as the pool key so the
	// agent's GetProviderForCandidate lookup still works.
	for _, c := range candidates {
		name := strings.TrimSpace(c.Provider)
		if name == "" {
			continue
		}
		if _, ok := pool[name]; ok {
			continue
		}
		if mc := findPassthroughForModel(cfg, c.Model); mc != nil {
			p, _, err := providers.CreateProviderFromConfig(mc)
			if err != nil {
				logger.WarnCF(
					"agent",
					"buildProviderPool: passthrough fallback CreateProviderFromConfig failed; pool entry skipped",
					map[string]any{"provider": name, "model": c.Model, "error": err.Error()},
				)
				continue
			}
			pool[name] = p
			logger.InfoCF(
				"agent",
				"buildProviderPool: candidate provider not configured; routed through passthrough entry with matching model",
				map[string]any{
					"candidate_provider":   name,
					"candidate_model":      c.Model,
					"passthrough_provider": mc.Provider,
				},
			)
		}
	}

	if len(pool) == 0 {
		return nil
	}
	return pool
}

// findPassthroughForModel scans cfg.Providers for a passthrough entry
// (openrouter, vivgrid) whose Model field equals or contains the given
// model string. Returns nil if no passthrough carries the model. Used as a
// defensive fallback in buildProviderPool when a candidate's Provider
// (e.g. a vendor namespace) doesn't match any configured provider entry.
func findPassthroughForModel(cfg *config.Config, model string) *config.ModelConfig {
	if cfg == nil || strings.TrimSpace(model) == "" {
		return nil
	}
	for _, mc := range cfg.Providers {
		if mc == nil {
			continue
		}
		if !providers.IsPassthroughProvider(strings.TrimSpace(mc.Provider), mc.APIBase) {
			continue
		}
		// Exact match — e.g. mc.Model = "z-ai/glm-5.2", model = "z-ai/glm-5.2".
		if strings.EqualFold(strings.TrimSpace(mc.Model), model) {
			clone := *mc
			return &clone
		}
		// Suffix match — one openrouter entry may have a wildcard Model like
		// "openrouter/*" or a base model that covers many variants. The
		// candidate's Model is the full "vendor/model" form, the entry's
		// Model is the canonical form.
		if strings.EqualFold(strings.TrimSpace(mc.ModelName), model) {
			clone := *mc
			return &clone
		}
	}
	return nil
}

// findModelConfigForProvider returns a clone of the ModelConfig whose
// Provider field matches the given provider name. Used by buildProviderPool
// at agent construction to locate the right ModelConfig for a candidate's
// pinned provider.
func findModelConfigForProvider(cfg *config.Config, providerName string) (*config.ModelConfig, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return nil, fmt.Errorf("provider name is required")
	}
	for _, mc := range cfg.Providers {
		if mc == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(mc.Provider), providerName) {
			clone := *mc
			return &clone, nil
		}
	}
	return nil, fmt.Errorf("provider %q not found in configured providers", providerName)
}

// agentToolsCfgToPolicy converts config.AgentToolsCfg to tools.ToolPolicyCfg for
// use at LLM-call assembly time (FR-003, FR-041). Reads from cfg.Builtin which
// holds the per-tool DefaultPolicy and Policies map.
//
// O7 (WS-G): the global sandbox tool policies (sandbox.tool_policies +
// sandbox.default_tool_policy) MUST be threaded in alongside the per-agent
// policy so that runtime FilterToolsByPolicy enforces most-restrictive-wins
// (deny > ask > allow). Without GlobalPolicies populated, an admin's global
// deny showed enforced in the REST view but did NOT block the tool at call
// time. The full config is required to source those globals; a nil config
// degrades to per-agent-only (test/legacy construction paths).
// godModeAvailable is the process-level god-mode AVAILABILITY gate (O14). It is
// set once at boot by SetAllowGodMode to ((--allow-god-mode OR
// config.Sandbox.GodModeAllowed) AND sandbox.GodModeAvailable) — the gateway
// boot path (pkg/gateway/gateway.go's resolveAllowGodMode) is the single place
// that combines the CLI flag and the config-persisted authorization grant
// before calling SetAllowGodMode, so this atomic always reflects one coherent
// decision. The runtime ON/OFF state lives in config.Sandbox.GodMode; god mode
// is only ACTIVE when both are true. Using a package atomic keeps the free
// function agentToolsCfgToPolicy (which has no loop receiver) able to consult
// availability without threading the boot flag through every construction
// path.
//
// This is frozen for the process lifetime: a config.Sandbox.GodModeAllowed
// grant written AFTER boot (e.g. via the Settings UI toggle) does not flip
// this atomic until the process restarts and re-reads config — see
// pkg/gateway/rest_god_mode.go's restart_required response field.
var godModeAvailable atomic.Bool

// setGodModeAvailable publishes the boot-time availability decision. Called by
// SetAllowGodMode; safe for concurrent use.
func setGodModeAvailable(v bool) { godModeAvailable.Store(v) }

// GodModeActive reports whether the global god-mode override is currently in
// effect: the runtime switch (cfg.Sandbox.GodMode) is on AND god mode is
// available in this build/boot. This is the single source of truth consulted by
// every override site (tool policy + sandbox profile) so the decision cannot
// drift between layers.
func GodModeActive(globalCfg *config.Config) bool {
	return globalCfg != nil && globalCfg.Sandbox.GodMode && godModeAvailable.Load()
}

func agentToolsCfgToPolicy(globalCfg *config.Config, cfg *config.AgentToolsCfg) *tools.ToolPolicyCfg {
	out := &tools.ToolPolicyCfg{DefaultPolicy: "allow"}
	// O14 god-mode: when the global switch is active, floor every tool's
	// effective policy at "allow" (no prompts, no deny) and skip the admin-ask
	// fence. Set the flag and return early — the per-agent and global policy
	// maps are deliberately left unpopulated so the override is total and
	// non-destructive (the on-disk policies are untouched and restored verbatim
	// the moment god mode is switched off, because this snapshot is rebuilt from
	// config on every TriggerReload).
	if GodModeActive(globalCfg) {
		out.GodMode = true
		return out
	}
	if cfg != nil {
		dp := string(cfg.Builtin.DefaultPolicy)
		if dp == "" {
			dp = "allow"
		}
		policies := make(map[string]string, len(cfg.Builtin.Policies))
		for k, v := range cfg.Builtin.Policies {
			policies[k] = string(v)
		}
		out.DefaultPolicy = dp
		out.Policies = policies
	}
	// Thread global sandbox tool policies so the runtime filter enforces
	// global × agent most-restrictive-wins (O7). FilterToolsByPolicy applies
	// GlobalPolicies before agent policies; a global deny always blocks.
	if globalCfg != nil {
		if len(globalCfg.Sandbox.ToolPolicies) > 0 {
			gp := make(map[string]string, len(globalCfg.Sandbox.ToolPolicies))
			for k, v := range globalCfg.Sandbox.ToolPolicies {
				gp[k] = v
			}
			out.GlobalPolicies = gp
		}
		out.GlobalDefaultPolicy = globalCfg.Sandbox.DefaultToolPolicy
	}
	return out
}

// SetAgentType updates the resolved agent type. Called by the registry to upgrade
// runtime-seeded core agents (e.g., Ava, Main) that may not have Type set in config.
func (a *AgentInstance) SetAgentType(agentType string) {
	a.AgentType = agentType
}

// IsWorker reports whether this instance is a sub-agent worker (the delegation-only
// labor tier). A worker is never a chat target and must never be resolved as the
// routing default. Single predicate so worker checks don't hand-roll the type-string
// comparison (config.AgentTypeWorker) at each call site.
func (a *AgentInstance) IsWorker() bool {
	return a.AgentType == string(config.AgentTypeWorker)
}

// ResolveAgentWorkspace exports resolveAgentWorkspace (below) for cross-
// package use, mirroring the "exported wrapper for cross-package test/tooling
// use" pattern already established in pkg/agent/runner/buildargs_crosspkg.go
// for that package's own unexported internals. Added so pkg/gateway's
// executor-smoke-test endpoint (POST /api/v1/agents/executor-smoke-test) can
// resolve EXACTLY the same workspace path a real subagent_3p delegation would
// run in (see external_dispatch.go, which calls resolveAgentWorkspace
// in-package via NewAgentInstance) when the smoke test names a real, saved
// agent_id — instead of always running in a disposable ephemeral scratch
// directory that carries none of the agent's real project files or
// AGENTS.md/CLAUDE.md/opencode.json context. Pure path computation: unlike
// NewAgentInstance's own call site, this does NOT create the directory —
// callers that need the directory to exist must os.MkdirAll it themselves,
// same as every other resolveAgentWorkspace caller does.
func ResolveAgentWorkspace(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	return resolveAgentWorkspace(agentCfg, defaults)
}

// resolveAgentWorkspace determines the workspace directory for an agent.
func resolveAgentWorkspace(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	if agentCfg != nil && strings.TrimSpace(agentCfg.Workspace) != "" {
		return expandHome(strings.TrimSpace(agentCfg.Workspace))
	}
	// Use the configured default workspace (respects OMNIPUS_HOME) for the
	// legacy "main" agent identity. The agentCfg.Default flag means "this agent
	// is the routing default for inbound messages" — it does NOT mean "share the
	// workspace". A routing-default agent (e.g. Mia with Default=true) must still
	// get its own per-agent workspace (FUNC-11). Only the literal "main" sentinel
	// identity falls back to the shared default workspace.
	if agentCfg == nil || agentCfg.ID == "" || routing.NormalizeAgentID(agentCfg.ID) == routing.DefaultAgentID {
		return expandHome(defaults.Workspace)
	}
	// Per-agent isolated workspace (FUNC-11). Each custom agent gets its own
	// directory under $OMNIPUS_HOME/agents/{id}/ (or ~/.omnipus/agents/{id}/ when
	// OMNIPUS_HOME is unset), matching the REST API convention.
	// H4: validate that the resolved path is actually under the agents base
	// directory after cleaning, to guard against path traversal via crafted agent IDs.
	safeBase := filepath.Join(omnipusHome(), "agents")
	// Strip any path separators or ".." from the agent ID.
	sanitizedID := filepath.Base(filepath.Clean(agentCfg.ID))
	if sanitizedID == "." || sanitizedID == ".." || sanitizedID == "" {
		// The agent ID sanitized to an unusable value. Use a UUID-based directory
		// name to avoid colliding with the reserved "main" workspace that
		// routing.NormalizeAgentID would return for empty/dot inputs.
		fallbackID := "agent-" + uuid.New().String()
		logger.WarnCF("agent", "Suspicious agent ID after sanitization; using UUID fallback workspace",
			map[string]any{"original_id": agentCfg.ID, "sanitized": sanitizedID, "fallback_id": fallbackID})
		return filepath.Join(safeBase, fallbackID)
	}
	resolved := filepath.Join(safeBase, sanitizedID)
	if !strings.HasPrefix(filepath.Clean(resolved), safeBase) {
		logger.WarnCF("agent", "Agent workspace path escapes base directory; using fallback",
			map[string]any{"agent_id": agentCfg.ID, "resolved": resolved})
		return filepath.Join(
			expandHome(defaults.Workspace),
			"..",
			"workspace-"+routing.NormalizeAgentID(agentCfg.ID),
		)
	}
	return resolved
}

// resolveAgentModel resolves the primary model for an agent.
func resolveAgentModel(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	if agentCfg != nil && agentCfg.Model != nil && strings.TrimSpace(agentCfg.Model.Primary) != "" {
		return strings.TrimSpace(agentCfg.Model.Primary)
	}
	return defaults.GetModelName()
}

// resolveAgentPrimaryProvider returns the agent's EXPLICIT primary-model provider
// (O3 two-field model). Empty string means "no explicit provider — resolve via
// the default/passthrough path". Only honored when the agent actually sets a
// primary model; a defaults-derived model uses the default provider.
func resolveAgentPrimaryProvider(agentCfg *config.AgentConfig) string {
	if agentCfg != nil && agentCfg.Model != nil && strings.TrimSpace(agentCfg.Model.Primary) != "" {
		return strings.TrimSpace(agentCfg.Model.Provider)
	}
	return ""
}

// resolveAgentFallbacks resolves the fallback models for an agent.
func resolveAgentFallbacks(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) []string {
	if agentCfg != nil && agentCfg.Model != nil && agentCfg.Model.Fallbacks != nil {
		return agentCfg.Model.Fallbacks
	}
	return defaults.ModelFallbacks
}

// resolveAgentFallbackModels resolves the provider-aware fallback chain for
// an agent. It prefers the modern AgentConfig.FallbackModels (FR-005) when
// populated and falls back to deriving one from the legacy sources in order:
//  1. agentCfg.Model.Fallbacks (the legacy []string on the agent's model block)
//  2. defaults.ModelFallbacks (the agent-defaults model_fallbacks list)
//
// Each returned entry is a config.FallbackModel with Provider populated by
// config.NormalizeFallbacks (which has already run at config-load time for
// the AgentConfig path). The defaults path is normalized here so the
// downstream resolveModelCandidatesFromList can treat every entry uniformly.
func resolveAgentFallbackModels(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) []config.FallbackModel {
	if agentCfg != nil && len(agentCfg.FallbackModels) > 0 {
		// Already normalized at config load — Provider is populated for every
		// entry that matched a configured provider or passthrough.
		return agentCfg.FallbackModels
	}
	var legacy []string
	switch {
	case agentCfg != nil && agentCfg.Model != nil && len(agentCfg.Model.Fallbacks) > 0:
		legacy = agentCfg.Model.Fallbacks
	case len(defaults.ModelFallbacks) > 0:
		legacy = defaults.ModelFallbacks
	default:
		return nil
	}
	out := make([]config.FallbackModel, len(legacy))
	for i, s := range legacy {
		out[i] = config.FallbackModel{Model: s}
	}
	return out
}

func compilePatterns(patterns []string) []*regexp.Regexp {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			logger.WarnCF("agent", "invalid path pattern, skipping", map[string]any{"pattern": p, "error": err.Error()})
			continue
		}
		compiled = append(compiled, re)
	}
	return compiled
}

func buildAllowReadPatterns(cfg *config.Config) []*regexp.Regexp {
	var configured []string
	if cfg != nil {
		configured = cfg.Tools.AllowReadPaths
	}

	compiled := compilePatterns(configured)
	mediaDirPattern := regexp.MustCompile(mediaTempDirPattern())
	for _, pattern := range compiled {
		if pattern.String() == mediaDirPattern.String() {
			return compiled
		}
	}

	return append(compiled, mediaDirPattern)
}

func mediaTempDirPattern() string {
	sep := regexp.QuoteMeta(string(os.PathSeparator))
	return "^" + regexp.QuoteMeta(filepath.Clean(media.TempDir())) + "(?:" + sep + "|$)"
}

// Close releases resources held by the agent's session store.
func (a *AgentInstance) Close() error {
	if a.Sessions != nil {
		return a.Sessions.Close()
	}
	return nil
}

// initSessionStore creates the unified session store for an agent.
// homePath is the ~/.omnipus/ root so that uploads cascade-delete on
// DeleteSession finds the correct <homePath>/uploads/<sessionID> path
// regardless of how deep the store's baseDir is in the directory tree (N-B fix).
// Falls back to the JSONL backend if the unified store cannot be initialized.
func initSessionStore(dir, agentID, homePath string) session.SessionStore {
	us, err := session.NewUnifiedStoreWithHome(dir, homePath)
	if err != nil {
		logger.ErrorCF("agent", "UnifiedStore init failed; falling back to JSONL backend",
			map[string]any{"dir": dir, "error": err.Error()})
		store, storeErr := memory.NewJSONLStore(dir)
		if storeErr != nil {
			logger.ErrorCF("agent", "JSONL store fallback also failed; using SessionManager",
				map[string]any{"error": storeErr.Error()})
			return session.NewSessionManager(dir)
		}
		if n, merr := memory.MigrateFromJSON(context.Background(), dir, store); merr != nil {
			logger.ErrorCF("agent", "Memory migration failed; falling back to SessionManager",
				map[string]any{"error": merr.Error()})
			store.Close()
			return session.NewSessionManager(dir)
		} else if n > 0 {
			logger.InfoCF("agent", "Memory migrated to JSONL", map[string]any{"sessions_migrated": n})
		}
		return session.NewJSONLBackend(store)
	}
	return us
}

// omnipusHome returns the Omnipus data directory.
// Priority: $OMNIPUS_HOME > ~/.omnipus.
// Falls back to /tmp/.omnipus when both os.UserHomeDir and OMNIPUS_HOME are unavailable.
func omnipusHome() string {
	if h := os.Getenv("OMNIPUS_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/.omnipus"
	}
	return filepath.Join(home, ".omnipus")
}

func expandHome(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		// When OMNIPUS_HOME is set, expand ~ to the parent of OMNIPUS_HOME so
		// that paths like ~/.omnipus/sessions resolve under OMNIPUS_HOME.
		// When OMNIPUS_HOME is not set, use os.UserHomeDir() for byte-identical
		// behavior to the pre-OMNIPUS_HOME code.
		var home string
		if h := os.Getenv("OMNIPUS_HOME"); h != "" {
			// OMNIPUS_HOME is already the .omnipus directory; its parent is the
			// effective ~ for paths of the form ~/.omnipus/...
			home = filepath.Dir(h)
		} else {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				logger.WarnCF("agent", "UserHomeDir failed in expandHome; attempting Getwd fallback",
					map[string]any{"path": path, "error": err.Error()})
				// M20: prefer an absolute path fallback — "." is relative and ambiguous.
				if wd, wdErr := os.Getwd(); wdErr == nil {
					home = wd
				} else {
					// Both UserHomeDir and Getwd failed; use /tmp as a last resort.
					logger.WarnCF("agent", "Getwd also failed in expandHome; using /tmp fallback",
						map[string]any{"path": path, "error": wdErr.Error()})
					home = os.TempDir()
				}
			}
		}
		if len(path) > 1 && (path[1] == '/' || path[1] == filepath.Separator) {
			return home + path[1:]
		}
		return home
	}
	return path
}
