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

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
	FallbackModels []config.FallbackModel
	Home           string
	MaxIterations  int
	MaxTokens      int
	// configuredMaxTokens is the CONFIGURED max_tokens, before the FR-005b
	// window clamp. MaxTokens is the clamped value the turn actually uses.
	//
	// The two must stay separate: clampMaxTokensForWindow only ever LOWERS,
	// so feeding the already-clamped MaxTokens back into it on every model
	// switch made the value monotonically decreasing for the lifetime of the
	// process. Switching to an 8k-window model and back to a 200k one left
	// the agent capped at the small model's window/4 forever — silently, with
	// no log line and no way back short of a restart. Every re-clamp reads
	// this field, never MaxTokens.
	configuredMaxTokens int
	Temperature         float64
	ThinkingLevel       ThinkingLevel
	// ContextWindow is the effective window resolved by the ADR-066 D2
	// ladder (ResolveWindow) at construction and on every model switch. 0
	// when the provider is exempt (WindowExempt) or the window is unknown
	// (WindowUnknown). Written under mu by ApplyAgentModel; read through
	// windowSnapshot on the turn path so a switch can never tear it.
	ContextWindow int
	// WindowSource / WindowClamped / WindowExempt / WindowUnknown are the
	// rest of the resolution (see WindowResolution); the gateway projects
	// them as context_window_source / context_window_clamped.
	WindowSource  WindowSource
	WindowClamped bool
	WindowExempt  bool
	WindowUnknown bool
	// needsProvider / needsProviderID are ADR-067 FR-016's per-agent degrade:
	// the PRIMARY candidate pins a provider id that is neither a catalog id
	// nor a constructible custom row. runTurn refuses such a turn FIRST in
	// the pre-turn gate with LLMError code needs_provider and makes zero
	// upstream requests; the gateway projects the same state independently
	// as Agent.degraded_reason. needsProviderID is the operator's own
	// spelling of the id, carried so the WARN can name it (and nothing else
	// — SC-010). Written under mu at construction and on every model switch;
	// read through needsProviderSnapshot on the turn path so a concurrent
	// ApplyAgentModel can never be observed torn.
	needsProvider   bool
	needsProviderID string
	Provider        providers.LLMProvider
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
	SkillsFilter   []string
	Candidates     []providers.FallbackCandidate

	// TimeoutSeconds is the per-turn hard timeout. 0 = disabled.
	// Populated from AgentDefaults.TimeoutSeconds; per-agent override if available.
	TimeoutSeconds int

	// AgentType is the resolved type string ("core", "custom", "system") used by
	// FilterToolsByPolicy at LLM-call assembly time (FR-003, FR-041). Set once at
	// construction; never mutated after creation.
	AgentType string

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
	workspace := resolveAgentHome(agentCfg, defaults)
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
	// library_list / library_read (D3, library-spec): scoped facades over
	// this agent's own workspace .library/ dual-write directory, where chat
	// uploads land (D-1) so file tools can actually reach them (ADR-046).
	// Registered unconditionally for every agent, exactly like their
	// read_file/list_directory siblings above — tool POLICY (allow/ask/
	// deny), not conditional registration, decides who can actually invoke
	// them (CLAUDE.md Constraint #6).
	toolsRegistry.Register(tools.NewLibraryListTool(workspace, readRestrict, allowReadPaths))
	toolsRegistry.Register(tools.NewLibraryReadTool(workspace, readRestrict, maxReadFileSize, allowReadPaths))

	execTool, err := tools.NewExecToolWithConfig(workspace, restrict, cfg, allowReadPaths)
	if err != nil {
		logger.ErrorCF("agent", "Failed to initialize exec tool; continuing without exec",
			map[string]any{"error": err.Error()})
	} else {
		toolsRegistry.Register(execTool)
	}

	toolsRegistry.Register(tools.NewEditFileTool(workspace, restrict, allowWritePaths))
	toolsRegistry.Register(tools.NewAppendFileTool(workspace, restrict, allowWritePaths))

	// request_mount (ADR-063 FR-7.2): the agent asks the operator for write
	// access to a real folder. Registered unconditionally like every sibling
	// above — its protection is its tool POLICY (seeded "ask", so every call
	// goes through the approval modal), never conditional registration.
	//
	// It was previously present ONLY in the metadata catalog, so it appeared
	// in /api/v1/tools and in Settings while being absent from every agent's
	// execution registry: no agent could call it, and it could not be granted
	// or revoked per agent, because GET /agents/{id}/tools lists this registry.
	toolsRegistry.Register(tools.NewRequestMountTool(config.OmnipusHomeDir()))

	// list_mounts (ADR-068 §4) registers here for exactly the reason spelled
	// out above: a mount tool that lives only in the metadata catalog is
	// visible in Settings and callable by nobody. It is the read-only half of
	// the pair — an agent that can request a mount must also be able to see
	// which mounts it already has, or it re-requests folders it was already
	// granted (ADR-068 §2.2).
	toolsRegistry.Register(tools.NewListMountsTool(config.OmnipusHomeDir()))

	// Resolve agentID early so the session store can tag sessions with the correct owner.
	// Empty until an agentCfg supplies one: the "main" sentinel used to stand in
	// here, which is how it ended up stamped on sessions, transcripts, tasks and
	// plans as an owner that no agent record ever matched.
	agentID := ""
	agentName := ""
	var subagents *config.SubagentsConfig
	var skillsFilter []string

	if agentCfg != nil {
		agentID = routing.NormalizeAgentID(agentCfg.ID)
		agentName = agentCfg.Name
		subagents = agentCfg.Subagents
		skillsFilter = agentCfg.Skills
	}

	sessionsDir := filepath.Join(workspace, "sessions")
	sessions := initSessionStore(sessionsDir, agentID, omnipusHome())

	// WithToolDiscovery gates the "Tool Discovery" prompt section on the
	// 3-tier tool-manifest system (cfg.Tools.Manifest.Compressed, default ON)
	// — NOT on the unrelated MCP-discovery config (cfg.Tools.MCP.Discovery.*,
	// default OFF), which this used to (wrongly) gate on, per finding 1 / GH
	// #657: a default install has the manifest system active but MCP
	// discovery off, so the old gate rendered no discovery guidance at all.
	contextBuilder := NewContextBuilder(workspace).
		WithToolDiscovery(cfg.Tools.Manifest.Compressed).
		WithSplitOnMarker(cfg.Agents.Defaults.SplitOnMarker)

	if agentCfg != nil {
		contextBuilder.WithAgentInfo(agentID, agentName)
		// FR-9.4: install the per-agent skill allowlist so it is enforced at
		// skill-resolution time (default-DENY). agentCfg.Skills is nil when the
		// agent declares no allowlist → unrestricted; a non-nil list restricts
		// resolution and progressive disclosure to exactly those skills.
		contextBuilder.WithSkillAllowlist(agentCfg.Skills)
		// ADR-052 FR-039: per-agent memory gate (nil = enabled). The Judge
		// verifier runs memoryless for impartial, reproducible verdicts —
		// its seed leaves MemoryEnabled=false; every other agent defaults on.
		contextBuilder.WithMemoryEnabled(agentCfg.MemoryEnabledEffective())
	}

	// Memory tools (FR-016, FR-017): register remember, recall_memory and
	// run_retrospective for EVERY agent.
	// recall_conversation is registered separately (loop.go) as it is session-scoped.
	// Subagents DO receive these tools — they are not in the ExcludedDelegate/
	// ExcludedHandoff lists so CloneExcept leaves them intact.
	//
	// This used to be gated on `agentID != "main"`, excluding the sentinel. That
	// was a hardcoded identity check, not a capability one: it left the default
	// agent on un-starred installs unable to remember anything, with no operator
	// control to change it, because the per-agent permissions screen lists the
	// execution registry. The gate went with the sentinel — whether an agent may
	// remember is its tool policy, like every other tool.
	memAdapter := NewMemoryStoreAdapter(contextBuilder.Memory())
	toolsRegistry.Register(tools.NewRememberTool(memAdapter, nil))
	toolsRegistry.Register(tools.NewRecallMemoryTool(memAdapter))
	toolsRegistry.Register(tools.NewRetrospectiveTool(memAdapter, nil))

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

	temperature := 0.7
	if defaults.Temperature != nil {
		temperature = *defaults.Temperature
	}

	var thinkingLevelStr string
	if mc, err := cfg.FindModelConfigBySlug(model); err == nil {
		thinkingLevelStr = mc.ThinkingLevel
	}
	thinkingLevel := parseThinkingLevel(thinkingLevelStr)

	// Resolve fallback candidates. Prefer the provider-aware resolver when
	// AgentConfig.FallbackModels (or a derived equivalent) is populated so a
	// fallback can route through its own provider (FR-007). Falls back to the
	// legacy resolver when only the historical Fallbacks []string is set —
	// preserves the original behavior for configs that have not migrated to
	// the new wire shape.
	// O3 two-field model: when the agent pins an explicit primary provider, the
	// primary candidate routes through it directly (never inferred). Empty
	// provider preserves the pre-O3 selection exactly.
	primaryProvider := resolveAgentPrimaryProvider(agentCfg, defaults)
	candidates := resolveAgentCandidatesWithPrimaryProvider(
		cfg, defaults.DefaultModel.Provider, model, primaryProvider, fallbackModels, fallbacks)

	// Pre-build the provider pool for every distinct provider referenced by
	// the resolved candidate chain. FR-007 requires each fallback to use its
	// own credentials at LLM-call time; building the pool here at
	// construction (vs. lazily inside the fallback hot path) keeps the
	// per-turn hot path allocation-free and surfaces credential / API-base
	// config errors at startup instead of mid-conversation.
	poolBuild := buildProviderPool(cfg, candidates, agentID)

	// ADR-066 D2: the context window comes from the ONE resolver, keyed by
	// the primary candidate's (provider, model) pair and this agent's id so
	// the per-agent override applies. There is no heuristic fallback: an
	// exempt provider gets 0 and skips every budget check; a local endpoint
	// nobody can size gets 0 and is refused at turn start (D3).
	windowProvider, windowModel := primaryWindowPair(candidates, defaults.DefaultModel.Provider, model)
	window := ResolveWindow(cfg, windowProvider, windowModel, agentID)
	contextWindow := window.Window
	// FR-005b: max_tokens must leave a positive budget B for the window.
	// Keep the configured value: every later re-clamp (a model switch) must
	// start from it, never from the already-clamped result.
	configuredMaxTokens := maxTokens
	maxTokens = clampMaxTokensForWindow(contextWindow, maxTokens, model)

	// Model routing setup: pre-resolve light model candidates at creation time
	// to avoid repeated model_list lookups on every incoming message.
	var router *routing.Router
	var lightCandidates []providers.FallbackCandidate
	var lightProvider providers.LLMProvider
	if rc := defaults.Routing; rc != nil && rc.Enabled && rc.LightModel != "" {
		resolved := resolveModelCandidates(cfg, defaults.DefaultModel.Provider, rc.LightModel, nil)
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
	// NOTE (ADR-054 D6.4): this constructor used to also derive an
	// IsRoutingDefault bool from agentCfg.Default here and store it on the
	// instance. That field is removed: splitting agents into independent
	// per-entity files means two concurrent agent writes could each set
	// Default=true with no shared lock to serialize them (each delta is
	// individually valid, the composition is not — ADR-054 D6 rule 4). The
	// single "default agent" pointer now lives exclusively in the settings
	// singleton (config.Agents.Defaults.DefaultAgentID, already existed) and
	// is consulted directly by AgentRegistry.GetDefaultAgent /
	// RouteResolver.resolveDefaultAgentID — see those functions' doc
	// comments for the current (3-priority) ladder.
	inst := &AgentInstance{
		ID:                  agentID,
		Name:                agentName,
		Model:               model,
		Fallbacks:           fallbacks,
		FallbackModels:      fallbackModels,
		Home:                workspace,
		MaxIterations:       maxIter,
		MaxTokens:           maxTokens,
		configuredMaxTokens: configuredMaxTokens,
		Temperature:         temperature,
		ThinkingLevel:       thinkingLevel,
		ContextWindow:       contextWindow,
		WindowSource:        window.Source,
		WindowClamped:       window.Clamped,
		WindowExempt:        window.Exempt,
		WindowUnknown:       window.Unknown,
		needsProvider:       poolBuild.primaryUnknown,
		needsProviderID:     poolBuild.primaryProvider,
		Provider:            provider,
		Sessions:            sessions,
		ContextBuilder:      contextBuilder,
		Tools:               toolsRegistry,
		Subagents:           subagents,
		SkillsFilter:        skillsFilter,
		Candidates:          candidates,
		Router:              router,
		LightCandidates:     lightCandidates,
		LightProvider:       lightProvider,
		TimeoutSeconds:      timeoutSeconds,
		AgentType:           resolvedAgentType,
	}
	// Publish the eagerly-built pool. StoreProviderPool uses the atomic
	// pointer; calling it here (vs. direct field assignment) keeps the
	// publish path identical to the model-switch path in ApplyAgentModel.
	inst.StoreProviderPool(poolBuild.pool)
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

// snapshotForExternalDispatch returns a private copy of a suitable for handing
// to a long-running dispatch that executes OUTSIDE of a.mu's protection
// window (e.g. an external-CLI run, which can take minutes) — a itself may be
// the LIVE registry instance, and SwitchModel/ApplyAgentModel can concurrently
// rewrite its Model/Provider/Candidates/ThinkingLevel (+ providerPool) tuple
// while the dispatched run is in flight (see mu's doc comment above).
//
// FIX 1 (7-reviewer gate, data race): processTaskDirectExternalCLI used to
// pass the live *AgentInstance straight into runExternalCLISubTurn, which
// reads agent.Model unlocked (transcript attribution + RunOptions.Model) —
// a read/write race with SwitchModel. This mirrors spawnSubTurn's existing
// execSource-snapshot pattern (subturn.go ~603-662, which the native
// delegation path already relies on for the identical reason): take a SINGLE
// RLock and copy the whole mutex-protected quad together (never a per-field
// read spread across separate lock acquisitions, which could observe a torn
// combination), then build a brand-new AgentInstance value via struct
// literal — NOT `*a` dereference-assignment, which would copy the mutex and
// the atomic providerPool/toolPolicy fields themselves and trip `go vet`'s
// copylocks check. Every other field is shared by value/pointer from the
// same source, matching what the live instance held at snapshot time.
//
// providerPool and toolPolicy are copied via their own atomic accessors
// (Load then Store into the new instance) so the pool snapshot stays paired
// with the Candidates it was built for, exactly as spawnSubTurn's comment
// there explains.
func (a *AgentInstance) snapshotForExternalDispatch() *AgentInstance {
	a.mu.RLock()
	model := a.Model
	provider := a.Provider
	candidates := a.Candidates
	thinkingLevel := a.ThinkingLevel
	contextWindow := a.ContextWindow
	windowSource := a.WindowSource
	windowClamped := a.WindowClamped
	windowExempt := a.WindowExempt
	windowUnknown := a.WindowUnknown
	needsProvider := a.needsProvider
	needsProviderID := a.needsProviderID
	pool := a.providerPool.Load()
	a.mu.RUnlock()

	out := &AgentInstance{
		ID:              a.ID,
		Name:            a.Name,
		Model:           model,
		Fallbacks:       a.Fallbacks,
		FallbackModels:  a.FallbackModels,
		Home:            a.Home,
		MaxIterations:   a.MaxIterations,
		MaxTokens:       a.MaxTokens,
		Temperature:     a.Temperature,
		ThinkingLevel:   thinkingLevel,
		ContextWindow:   contextWindow,
		WindowSource:    windowSource,
		WindowClamped:   windowClamped,
		WindowExempt:    windowExempt,
		WindowUnknown:   windowUnknown,
		needsProvider:   needsProvider,
		needsProviderID: needsProviderID,
		Provider:        provider,
		Sessions:        a.Sessions,
		ContextBuilder:  a.ContextBuilder,
		Tools:           a.Tools,
		Subagents:       a.Subagents,
		SkillsFilter:    a.SkillsFilter,
		Candidates:      candidates,
		TimeoutSeconds:  a.TimeoutSeconds,
		AgentType:       a.AgentType,
		Router:          a.Router,
		LightCandidates: a.LightCandidates,
		LightProvider:   a.LightProvider,
	}
	if pool != nil {
		out.StoreProviderPool(*pool)
	}
	out.StoreToolPolicy(a.LoadToolPolicy())
	return out
}

// providerPoolBuild is everything buildProviderPool decided for one agent:
// the pool itself, plus whether the PRIMARY candidate pins a provider id that
// is UNKNOWN (ADR-067 FR-016). An unknown primary is the agent's
// `needs_provider` degrade — it refuses every turn with zero upstream
// requests and the gateway projects it as Agent.degraded_reason. An unknown
// FALLBACK is not: it is dropped from the pool and the agent runs on the
// rest (DS-8 rows 3 and 6).
type providerPoolBuild struct {
	pool map[string]providers.LLMProvider
	// primaryUnknown and primaryProvider name the FR-016 degrade. The id is
	// carried so the refusal can name the operator's own spelling — and only
	// that, never a canonical alternative (SC-010).
	primaryUnknown  bool
	primaryProvider string
}

// buildProviderPool pre-builds an LLMProvider for every distinct provider
// name referenced by the agent's candidate chain (primary + fallbacks).
// Returns a nil pool when the candidate chain has no explicit providers
// (every candidate routes through the agent primary — no pool needed).
//
// Build failures are non-fatal: a missing entry for a pinned provider name
// degrades to "use the primary's provider" via GetProviderForCandidate's
// fallback path. We log at WARN so operators can see the cause of a
// degraded fallback path at startup.
//
// ADR-067 FR-016 splits one of those failures out of the generic skip. A
// candidate whose provider id is UNKNOWN — neither a catalog id nor a
// constructible custom row (providers.IsUnknownProviderID) — is never
// constructed and never rescued through the passthrough net: rescuing it
// would be an alias by another name, and aliases are what the greenfield
// rule deleted. Instead:
//
//   - unknown PRIMARY  → primaryUnknown; the agent is `needs_provider` and
//     runTurn refuses before any provider call.
//   - unknown FALLBACK → dropped from the pool with exactly ONE WARN naming
//     the agent and the provider; the agent runs on the remaining pool.
//
// agentID is the agent the pool belongs to; it appears in those WARNs so an
// operator can tell WHICH agent lost a fallback. It may be empty for a
// one-off pool that belongs to no single agent (the session-recap chain).
func buildProviderPool(
	cfg *config.Config,
	candidates []providers.FallbackCandidate,
	agentID string,
) providerPoolBuild {
	if cfg == nil || len(candidates) == 0 {
		return providerPoolBuild{}
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
		return providerPoolBuild{}
	}
	// unknown holds the FR-016 classification for every distinct candidate
	// provider id. It is computed BEFORE any construction so a decision that
	// belongs to the catalog is never inferred from a client-build failure
	// (a missing credential is not an unknown provider).
	unknown := make(map[string]struct{})
	pool := make(map[string]providers.LLMProvider, len(seen))
	for name := range seen {
		if providers.IsUnknownProviderID(cfg, name) {
			unknown[name] = struct{}{}
			continue
		}
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
		if _, bad := unknown[name]; bad {
			// FR-016: an UNKNOWN id is never rescued through a passthrough.
			// The net exists for a vendor NAMESPACE that leaked out of a
			// slash-separated model id (the id is a real catalog provider,
			// just not a configured row); routing an id the catalog has
			// never heard of through someone else's credentials would be an
			// alias, which is exactly what the greenfield rule removed.
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

	// FR-016 verdict. The primary candidate is candidates[0] — the pinned
	// (provider, model) pair when the agent (or agents.defaults.default_model)
	// names one, and the resolver's own first choice otherwise.
	primary := strings.TrimSpace(candidateProvider(candidates))
	_, primaryUnknown := unknown[primary]
	primaryUnknown = primaryUnknown && primary != ""

	// One WARN per unknown FALLBACK-only provider, naming the agent and the
	// provider — the operator's own spelling, never a canonical alternative.
	// The unknown PRIMARY gets its own single WARN below; it is a different
	// event (the agent stops running) and must not be reported as a dropped
	// fallback.
	for name := range unknown {
		if name == primary {
			continue
		}
		logger.WarnCF("agent",
			"Unknown provider named by a fallback; dropped from the agent's provider pool",
			map[string]any{"agent_id": agentID, "provider": name})
	}
	if primaryUnknown {
		logger.WarnCF("agent",
			"Agent's primary provider is unknown; the agent needs a provider and will refuse turns",
			map[string]any{"agent_id": agentID, "provider": primary})
	}

	if len(pool) == 0 {
		pool = nil
	}
	return providerPoolBuild{
		pool:            pool,
		primaryUnknown:  primaryUnknown,
		primaryProvider: primary,
	}
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
		// ADR-067 FR-036: EXACT after TrimSpace, never case-folded. The
		// previous strings.EqualFold made "ZAI" resolve the "zai" row, which
		// is the one shape a retired spelling could still be resurrected
		// through after the greenfield rename paths were deleted. An
		// entity whose provider differs from the config only by case is
		// UNKNOWN, and the agent bound to it degrades (DS-8 row 4).
		if strings.TrimSpace(mc.Provider) == providerName {
			clone := *mc
			return &clone, nil
		}
	}
	return nil, fmt.Errorf("provider %q not found in configured providers", providerName)
}

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

// agentToolsCfgToPolicy builds the *tools.ToolPolicyCfg the runtime filter
// resolves against. There is no default-policy fallback (CLAUDE.md hard
// constraint 6): every static builtin tool must have an explicit, literal
// entry in cfg.Builtin.Policies and/or globalCfg.Sandbox.ToolPolicies for
// every agent — coverage is enforced structurally by
// config.ValidateToolPolicyCoverage at boot and at every agent write, not by
// a hardcoded "allow" here.
func agentToolsCfgToPolicy(globalCfg *config.Config, cfg *config.AgentToolsCfg) *tools.ToolPolicyCfg {
	out := &tools.ToolPolicyCfg{}
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
		// cfg.Builtin.Policies is already map[string]config.ToolPolicy (typed at
		// the config layer) — a direct copy, no string round-trip needed now
		// that tools.ToolPolicyCfg.Policies is typed too.
		policies := make(map[string]config.ToolPolicy, len(cfg.Builtin.Policies))
		for k, v := range cfg.Builtin.Policies {
			policies[k] = v
		}
		out.Policies = policies
	}
	// Thread global sandbox tool policies so the runtime filter enforces
	// global × agent most-restrictive-wins (O7). FilterToolsByPolicy applies
	// GlobalPolicies before agent policies; a global deny always blocks.
	//
	// globalCfg.Sandbox.ToolPolicies is raw config data (map[string]string) —
	// its values are validated at the write boundary (rest_tool_policies.go's
	// putToolPolicies rejects anything outside allow/ask/deny before
	// persisting), so this is a trusting conversion, matching this codebase's
	// existing pattern of trusting validated config rather than re-validating
	// everywhere.
	if globalCfg != nil && len(globalCfg.Sandbox.ToolPolicies) > 0 {
		gp := make(map[string]config.ToolPolicy, len(globalCfg.Sandbox.ToolPolicies))
		for k, v := range globalCfg.Sandbox.ToolPolicies {
			gp[k] = config.ToolPolicy(v)
		}
		out.GlobalPolicies = gp
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

// IsSystem reports whether this is a System Agent.
//
// IsChatTarget mirrors config.AgentConfig.IsChatTarget so the registry and
// pkg/routing can apply the SAME eligibility rule when resolving a default
// agent. They used to differ: routing excluded system agents and the registry
// did not, so on a config containing one the two could name different agents
// for the same install — a disagreement neither side could see.
func (a *AgentInstance) IsSystem() bool {
	return a.AgentType == string(config.AgentTypeSystem)
}

// IsChatTarget reports whether this agent may be resolved as a chat target.
func (a *AgentInstance) IsChatTarget() bool {
	return !a.IsWorker() && !a.IsSystem()
}

// ResolveAgentHome exports resolveAgentHome (below) for cross-
// package use, mirroring the "exported wrapper for cross-package test/tooling
// use" pattern already established in pkg/agent/runner/buildargs_crosspkg.go
// for that package's own unexported internals. Added so pkg/gateway's
// executor-smoke-test endpoint (POST /api/v1/agents/executor-smoke-test) can
// resolve EXACTLY the same directory a real subagent_3p delegation would
// run in (see external_dispatch.go, which calls resolveAgentHome
// in-package via NewAgentInstance) when the smoke test names a real, saved
// agent_id — instead of always running in a disposable ephemeral scratch
// directory that carries none of the agent's real project files or
// AGENTS.md/CLAUDE.md/opencode.json context. Pure path computation: unlike
// NewAgentInstance's own call site, this does NOT create the directory —
// callers that need the directory to exist must os.MkdirAll it themselves,
// same as every other resolveAgentHome caller does.
//
// Naming note: "workspace" here means AgentConfig.Home (this field was
// named "Workspace" before the ADR-046 "agent home" rename), a plain
// per-agent directory-path field — NOT pkg/workspace.Workspace, Omnipus's
// separate multi-agent Workspace feature (CoreTeam, REST-CRUD, delegation
// graph, $OMNIPUS_HOME/workspaces/{id}/). No AgentConfig field binds an
// agent to one of those, and real subagent_3p dispatch (ADR-032) never
// consults that package at all. The two concepts share an English word by
// historical accident, not by design — do not conflate them.
func ResolveAgentHome(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	return resolveAgentHome(agentCfg, defaults)
}

// resolveAgentHome determines the workspace directory for an agent.
func resolveAgentHome(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	if agentCfg != nil && strings.TrimSpace(agentCfg.Home) != "" {
		return expandHome(strings.TrimSpace(agentCfg.Home))
	}
	// Use the configured default workspace (respects OMNIPUS_HOME) for the
	// unidentified agent. The agentCfg.Default flag means "this agent is the
	// routing default for inbound messages" — it does NOT mean "share the
	// workspace". A routing-default agent (e.g. Mia with Default=true) must
	// still get its own per-agent workspace (FUNC-11). Only a caller with no
	// agent identity at all falls back to the shared default workspace; the
	// "main" sentinel that used to land here has been removed.
	if agentCfg == nil || agentCfg.ID == "" {
		return expandHome(defaults.Home)
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
		// The agent ID sanitized to an unusable value. Use a UUID-based
		// directory name so this degenerate case still gets a unique,
		// collision-free workspace. (routing.NormalizeAgentID used to return
		// the "main" sentinel for empty/dot inputs, which made a collision
		// with the sentinel's own workspace a real risk here; with the
		// sentinel retired, NormalizeAgentID("") returns empty string and
		// there is no reserved name left to collide with — the UUID fallback
		// remains simply for uniqueness.)
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
			expandHome(defaults.Home),
			"..",
			"workspace-"+routing.NormalizeAgentID(agentCfg.ID),
		)
	}
	return resolved
}

// resolveAgentModel resolves the primary model for an agent: its own
// model.primary, else the model half of agents.defaults.default_model
// (ADR-068 D14.1). Empty when neither is set.
func resolveAgentModel(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	if agentCfg != nil && agentCfg.Model != nil && strings.TrimSpace(agentCfg.Model.Primary) != "" {
		return strings.TrimSpace(agentCfg.Model.Primary)
	}
	if defaults == nil || defaults.DefaultModel.IsZero() {
		return ""
	}
	return strings.TrimSpace(defaults.DefaultModel.Model)
}

// resolveAgentPrimaryProvider returns the provider the agent's primary model is
// PINNED to (O3 two-field model). When the agent sets its own primary model,
// this is its explicit model.provider — empty means "no explicit provider,
// resolve via the passthrough path". When the agent runs on the default model
// it is the provider half of agents.defaults.default_model: the default is an
// exact pair (ADR-068 D14.1), so it is pinned exactly like an explicit
// per-agent provider and never inferred from a configured passthrough.
func resolveAgentPrimaryProvider(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	if agentCfg != nil && agentCfg.Model != nil && strings.TrimSpace(agentCfg.Model.Primary) != "" {
		return strings.TrimSpace(agentCfg.Model.Provider)
	}
	if defaults == nil || defaults.DefaultModel.IsZero() {
		return ""
	}
	return strings.TrimSpace(defaults.DefaultModel.Provider)
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

// primaryWindowPair is the (provider, model) pair ResolveWindow is keyed by:
// the primary candidate's, which already carries the configured provider
// that owns the credentials and the bare model id the catalog indexes.
// With no resolvable candidate it falls back to the defaults' provider and
// the raw model string so the ladder can still answer.
func primaryWindowPair(candidates []providers.FallbackCandidate, defaultProvider, model string) (string, string) {
	if len(candidates) > 0 {
		return candidates[0].Provider, candidates[0].Model
	}
	return strings.TrimSpace(defaultProvider), strings.TrimSpace(model)
}

// primaryModelPair is the (provider, model) pair the presentation gate and
// the resize-budget lookup resolve against for this turn — the SAME pair
// primaryWindowPair hands ResolveWindow, read under the same lock, so the
// media path and the context-window path can never disagree about which
// row of the catalog this agent is. A model id alone is not a catalog key
// (ADR-067 FR-003): "z-ai/glm-5.2" under openrouter and "glm-5.2" under
// zai are different rows with different modalities.
//
// With no resolved candidates the provider is unknown (""), which resolves
// as a miss — the documented optimistic posture, never a guess.
func (a *AgentInstance) primaryModelPair() (provider, model string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.Candidates) > 0 {
		return a.Candidates[0].Provider, a.Candidates[0].Model
	}
	return "", a.Model
}

// candidateProvider is primaryModelPair's counterpart for a candidate slice
// already resolved for this turn (the light-tier switch resolves its own).
// An empty slice yields "" — a catalog miss, not a guess.
func candidateProvider(candidates []providers.FallbackCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].Provider
}

// windowSnapshot reads the resolved window under the instance mutex so a
// concurrent ApplyAgentModel (which rewrites ContextWindow together with
// Model / Provider / Candidates) can never be observed torn.
func (a *AgentInstance) windowSnapshot() (window int, exempt, unknown bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ContextWindow, a.WindowExempt, a.WindowUnknown
}

// needsProviderSnapshot reads the ADR-067 FR-016 degrade under the instance
// mutex, for the same reason windowSnapshot does: ApplyAgentModel rewrites it
// inside the Model / Provider / Candidates / pool flip, and a turn must never
// pair the new model with the old verdict. Returns the operator's own
// spelling of the offending id alongside the flag so the refusal can name it.
func (a *AgentInstance) needsProviderSnapshot() (needs bool, providerID string) {
	if a == nil {
		return false, ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.needsProvider, a.needsProviderID
}

// needsModelSnapshot reports ADR-068 FR-014's derived `needs_model` for this
// live instance, read under the instance mutex for the same reason its two
// siblings are: ApplyAgentModel rewrites Model together with Candidates and
// the needsProvider verdict, and a turn must never pair one half of that flip
// with the other.
//
// The predicate is FR-014's, expressed in the terms the instance actually
// has:
//
//   - Model empty — neither the agent's own `model.primary` nor
//     `agents.defaults.default_model` named one, so there is nothing to call.
//   - needsProvider — the model it does name routes through a provider id
//     that is not configured (ADR-067 FR-016). This half is why the gate
//     ORDER is load-bearing rather than decorative: such an agent satisfies
//     BOTH pre-turn predicates, and SC-013 requires the turn to end with
//     `needs_provider`, never `model_unassigned`.
//
// It is deliberately NOT "the model failed to resolve to a configured row".
// An agent running on an injected provider with an unrecognised slug is a
// turn-time failure (the provider answers, or it does not), not a
// configuration state the operator can see and fix in the agent's settings.
func (a *AgentInstance) needsModelSnapshot() bool {
	if a == nil {
		return true
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return strings.TrimSpace(a.Model) == "" || a.needsProvider
}

// budgetChecksExempt reports whether every budget check — pre-turn trim,
// mid-turn, timeout recovery, model switch — is skipped for this agent
// (ADR-066 FR-005: a subprocess-CLI provider manages its own context).
func (a *AgentInstance) budgetChecksExempt() bool {
	_, exempt, _ := a.windowSnapshot()
	return exempt
}

// applyWindowResolution writes a fresh resolution onto the instance. The
// caller holds a.mu (ApplyAgentModel's tuple flip).
func (a *AgentInstance) applyWindowResolutionLocked(r WindowResolution) {
	a.ContextWindow = r.Window
	a.WindowSource = r.Source
	a.WindowClamped = r.Clamped
	a.WindowExempt = r.Exempt
	a.WindowUnknown = r.Unknown
}

// configuredMaxTokensLocked returns the configured (pre-clamp) max_tokens,
// falling back to the current field for an instance built by a test that
// never set it. agent.mu must be held.
func (a *AgentInstance) configuredMaxTokensLocked() int {
	if a.configuredMaxTokens > 0 {
		return a.configuredMaxTokens
	}
	return a.MaxTokens
}
