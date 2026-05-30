package agent

import (
	"context"
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

	ID                        string
	Name                      string
	Model                     string
	Fallbacks                 []string
	Workspace                 string
	MaxIterations             int
	MaxTokens                 int
	Temperature               float64
	ThinkingLevel             ThinkingLevel
	ContextWindow             int
	SummarizeMessageThreshold int
	SummarizeTokenPercent     int
	Provider                  providers.LLMProvider
	Sessions                  session.SessionStore
	ContextBuilder            *ContextBuilder
	Tools                     *tools.ToolRegistry
	Subagents                 *config.SubagentsConfig
	SkillsFilter              []string
	Candidates                []providers.FallbackCandidate

	// TimeoutSeconds is the per-turn hard timeout. 0 = disabled.
	// Populated from AgentDefaults.TimeoutSeconds; per-agent override if available.
	TimeoutSeconds int

	// AgentType is the resolved type string ("core", "custom", "system") used by
	// FilterToolsByPolicy at LLM-call assembly time (FR-003, FR-041). Set once at
	// construction; never mutated after creation.
	AgentType string

	// toolPolicy holds the per-agent tool policy snapshot used by
	// FilterToolsByPolicy at LLM-call assembly time (FR-003, FR-020, FR-041).
	// Populated at construction from config.AgentConfig.Tools.
	// Config PUT swaps the pointer via StoreToolPolicy; the turn assembly Load()s
	// it on each call to ensure stale policies are never seen mid-turn.
	// The zero value (nil pointer) defaults to allow-all.
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

	if agentCfg != nil {
		agentID = routing.NormalizeAgentID(agentCfg.ID)
		agentName = agentCfg.Name
		subagents = agentCfg.Subagents
		skillsFilter = agentCfg.Skills
	}

	sessionsDir := filepath.Join(workspace, "sessions")
	sessions := initSessionStore(sessionsDir, agentID)

	mcpDiscoveryActive := cfg.Tools.MCP.Enabled && cfg.Tools.MCP.Discovery.Enabled
	contextBuilder := NewContextBuilder(workspace).
		WithToolDiscovery(
			mcpDiscoveryActive && cfg.Tools.MCP.Discovery.UseBM25,
			mcpDiscoveryActive && cfg.Tools.MCP.Discovery.UseRegex,
		).
		WithSplitOnMarker(cfg.Agents.Defaults.SplitOnMarker)

	if agentCfg != nil {
		contextBuilder.WithAgentInfo(agentID, agentName)
	}

	// Memory tools (FR-016, FR-017): register remember, recall_memory, and
	// retrospective for all agents except the main gateway agent.
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

	maxIter := defaults.MaxToolIterations
	if maxIter == 0 {
		maxIter = 20
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
		// forceCompression handles any overshoot.
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

	// Resolve fallback candidates
	candidates := resolveModelCandidates(cfg, defaults.Provider, model, fallbacks)

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
		case config.AgentTypeCore, config.AgentTypeSystem:
			resolvedAgentType = string(agentCfg.Type)
		case config.AgentTypeCustom:
			resolvedAgentType = "custom"
		}
	}
	inst := &AgentInstance{
		ID:                        agentID,
		Name:                      agentName,
		Model:                     model,
		Fallbacks:                 fallbacks,
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
	}
	if agentCfg != nil && agentCfg.Tools != nil {
		inst.toolPolicy.Store(agentToolsCfgToPolicy(agentCfg.Tools))
	}
	return inst
}

// LoadToolPolicy returns the current tool policy snapshot for this agent.
// Returns nil when no policy has been stored (defaults to allow-all at call sites).
// Safe for concurrent access (atomic load, FR-020).
func (a *AgentInstance) LoadToolPolicy() *tools.ToolPolicyCfg {
	return a.toolPolicy.Load()
}

// StoreToolPolicy atomically replaces the agent's tool policy (FR-020).
// Called by ReloadProviderAndConfig on config PUT to propagate the new policy
// without rebuilding the agent registry. Passing nil resets to allow-all.
// Safe for concurrent access with ongoing turn assembly.
func (a *AgentInstance) StoreToolPolicy(p *tools.ToolPolicyCfg) {
	a.toolPolicy.Store(p)
}

// agentToolsCfgToPolicy converts config.AgentToolsCfg to tools.ToolPolicyCfg for
// use at LLM-call assembly time (FR-003, FR-041). Reads from cfg.Builtin which
// holds the per-tool DefaultPolicy and Policies map.
func agentToolsCfgToPolicy(cfg *config.AgentToolsCfg) *tools.ToolPolicyCfg {
	if cfg == nil {
		return &tools.ToolPolicyCfg{DefaultPolicy: "allow"}
	}
	dp := string(cfg.Builtin.DefaultPolicy)
	if dp == "" {
		dp = "allow"
	}
	policies := make(map[string]string, len(cfg.Builtin.Policies))
	for k, v := range cfg.Builtin.Policies {
		policies[k] = string(v)
	}
	return &tools.ToolPolicyCfg{
		DefaultPolicy: dp,
		Policies:      policies,
	}
}

// SetAgentType updates the resolved agent type. Called by the registry to upgrade
// runtime-seeded core agents (e.g., Ava, Main) that may not have Type set in config.
func (a *AgentInstance) SetAgentType(agentType string) {
	a.AgentType = agentType
}

// resolveAgentWorkspace determines the workspace directory for an agent.
func resolveAgentWorkspace(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	if agentCfg != nil && strings.TrimSpace(agentCfg.Workspace) != "" {
		return expandHome(strings.TrimSpace(agentCfg.Workspace))
	}
	// Use the configured default workspace (respects OMNIPUS_HOME)
	if agentCfg == nil || agentCfg.Default || agentCfg.ID == "" || routing.NormalizeAgentID(agentCfg.ID) == "main" {
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

// resolveAgentFallbacks resolves the fallback models for an agent.
func resolveAgentFallbacks(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) []string {
	if agentCfg != nil && agentCfg.Model != nil && agentCfg.Model.Fallbacks != nil {
		return agentCfg.Model.Fallbacks
	}
	return defaults.ModelFallbacks
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
// Falls back to the JSONL backend if the unified store cannot be initialized.
func initSessionStore(dir, agentID string) session.SessionStore {
	us, err := session.NewUnifiedStore(dir)
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
