package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ====================== Config & Constants ======================
const (
	// Default values for SubTurn configuration (used when config is not set or is zero)
	defaultMaxSubTurnDepth       = 3
	defaultMaxConcurrentSubTurns = 5
	defaultConcurrencyTimeout    = 30 * time.Second
	defaultSubTurnTimeout        = 5 * time.Minute
	// maxEphemeralHistorySize limits the number of messages stored in ephemeral sessions.
	// This prevents memory accumulation in long-running sub-turns.
	maxEphemeralHistorySize = 50
)

var (
	ErrDepthLimitExceeded   = errors.New("sub-turn depth limit exceeded")
	ErrInvalidSubTurnConfig = errors.New("invalid sub-turn config")
	ErrConcurrencyTimeout   = errors.New("timeout waiting for concurrency slot")
)

// getSubTurnConfig returns the effective SubTurn configuration with defaults applied.
// When SubTurn.MaxConcurrent is not explicitly set (≤ 0), it falls back to the
// resolved MaxParallelAgents value so the in-turn fan-out cap tracks the same
// global ceiling as the async workflow dispatch path (FR-6.6).
func (al *AgentLoop) getSubTurnConfig() subTurnRuntimeConfig {
	cfg := al.cfg.Agents.Defaults.SubTurn

	// #477 / FR-D9: resolve via the SAME shared function enforceEdgeModeAndDepth
	// and wireDelegationInjectors use, so this backstop's "nothing configured"
	// default and the delegation graph's own gate are never computed
	// independently. edgeDepth is nil here — this is the GLOBAL-only fallback;
	// a specific delegation call's own per-edge override (when one applies)
	// arrives separately via SubTurnConfig.ResolvedMaxDepth (see spawnSubTurn).
	maxDepth := resolveEffectiveDelegationDepth(nil, cfg.MaxDepth)

	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		// Fall back to MaxParallelAgents so that the synchronous spawn/subagent
		// fan-out is capped by the same knob as the async task dispatch path.
		maxConcurrent = al.cfg.Performance.EffectiveMaxParallelAgents()
	}

	concurrencyTimeout := time.Duration(cfg.ConcurrencyTimeoutSec) * time.Second
	if concurrencyTimeout <= 0 {
		concurrencyTimeout = defaultConcurrencyTimeout
	}

	defaultTimeout := time.Duration(cfg.DefaultTimeoutMinutes) * time.Minute
	if defaultTimeout <= 0 {
		defaultTimeout = defaultSubTurnTimeout
	}

	return subTurnRuntimeConfig{
		maxDepth:           maxDepth,
		maxConcurrent:      maxConcurrent,
		concurrencyTimeout: concurrencyTimeout,
		defaultTimeout:     defaultTimeout,
		defaultTokenBudget: cfg.DefaultTokenBudget,
	}
}

// subTurnRuntimeConfig holds the effective runtime configuration for SubTurn execution.
type subTurnRuntimeConfig struct {
	maxDepth           int
	maxConcurrent      int
	concurrencyTimeout time.Duration
	defaultTimeout     time.Duration
	defaultTokenBudget int
}

// ====================== SubTurn Config ======================

// SubTurnConfig configures the execution of a child sub-turn.
//
// Usage Examples:
//
// Synchronous sub-turn (Async=false):
//
//	cfg := SubTurnConfig{
//	    Model: "gpt-4o-mini",
//	    SystemPrompt: "Analyze this code",
//	    Async: false,  // Result returned immediately
//	}
//	result, err := SpawnSubTurn(ctx, cfg)
//	// Use result directly here
//	processResult(result)
//
// Asynchronous sub-turn (Async=true):
//
//	cfg := SubTurnConfig{
//	    Model: "gpt-4o-mini",
//	    SystemPrompt: "Background analysis",
//	    Async: true,  // Result delivered to channel
//	}
//	result, err := SpawnSubTurn(ctx, cfg)
//	// Result also available in parent's pendingResults channel
//	// Parent turn will poll and process it in a later iteration
type SubTurnConfig struct {
	Model        string
	Tools        []tools.Tool
	SystemPrompt string
	MaxTokens    int

	// Async controls the result delivery mechanism:
	//
	// When Async = false (synchronous sub-turn):
	//   - The caller blocks until the sub-turn completes
	//   - The result is ONLY returned via the function return value
	//   - The result is NOT delivered to the parent's pendingResults channel
	//   - This prevents double delivery: caller gets result immediately, no need for channel
	//   - Use case: When the caller needs the result immediately to continue execution
	//   - Example: A tool that needs to process the sub-turn result before returning
	//
	// When Async = true (asynchronous sub-turn):
	//   - The sub-turn runs in the background (still blocks the caller, but semantically async)
	//   - The result is delivered to the parent's pendingResults channel
	//   - The result is ALSO returned via the function return value (for consistency)
	//   - The parent turn can poll pendingResults in later iterations to process results
	//   - Use case: Fire-and-forget operations, or when results are processed in batches
	//   - Example: Spawning multiple sub-turns in parallel and collecting results later
	//
	// IMPORTANT: The Async flag does NOT make the call non-blocking. It only controls
	// whether the result is delivered via the channel. For true non-blocking execution,
	// the caller must spawn the sub-turn in a separate goroutine.
	Async bool

	// Critical indicates this SubTurn's result is important and should continue
	// running even after the parent turn finishes gracefully.
	//
	// When parent finishes gracefully (Finish(false)):
	//   - Critical=true: SubTurn continues running, delivers result as orphan
	//   - Critical=false: SubTurn exits gracefully without error
	//
	// When parent finishes with hard abort (Finish(true)):
	//   - All SubTurns are canceled regardless of Critical flag
	Critical bool

	// Timeout is the maximum duration for this SubTurn.
	// If the SubTurn runs longer than this, it will be canceled.
	// Default is 5 minutes (defaultSubTurnTimeout) if not specified.
	Timeout time.Duration

	// MaxContextRunes limits the context size (in runes) passed to the SubTurn.
	// This prevents context window overflow by truncating message history before LLM calls.
	//
	// Values:
	//   0  = Auto-calculate based on model's ContextWindow * 0.75 (default, recommended)
	//   -1 = No limit (disable soft truncation, rely only on hard context errors)
	//   >0 = Use specified rune limit
	//
	// The soft limit acts as a first line of defense before hitting the provider's
	// hard context window limit. When exceeded, older messages are intelligently
	// truncated while preserving system messages and recent context.
	MaxContextRunes int

	// ActualSystemPrompt is injected as the true 'system' role message for the childAgent.
	// The legacy SystemPrompt field is actually used as the first 'user' message (task description).
	ActualSystemPrompt string

	// TargetAgentID, when non-empty, is the configured agent the sub-turn is
	// delegating TO (e.g., a worker). When set, spawnSubTurn resolves the
	// delegate's soul (config.AgentConfig.Soul or, for seeded base/worker
	// agents, the compiled coreagent.GetPrompt) and uses it as the true
	// system role, so the child turn runs with system=soul + user=task,
	// uniformly across the native and external-cli executors. Empty means
	// "delegate the parent's own agent" — the parent's own soul applies.
	TargetAgentID string

	// InitialMessages preloads the ephemeral session history before the agent loop starts.
	// Used by evaluator-optimizer patterns to pass the full worker context across multiple iterations.
	InitialMessages []providers.Message

	// InitialTokenBudget is a shared atomic counter for tracking remaining tokens.
	// If set, the SubTurn will inherit this budget and deduct tokens after each LLM call.
	// If nil, the SubTurn will inherit the parent's tokenBudget (if any).
	// Used by team tool to enforce token limits across all team members.
	InitialTokenBudget *atomic.Int64

	// TaskLabel is the optional human-readable label for the sub-turn task.
	// Populated by the spawn tool from its "label" argument (FR-H-004).
	// Used in SubTurnSpawnPayload.TaskLabel for the WS subagent_start frame.
	TaskLabel string

	// ResolvedMaxDepth, when non-nil, is the effective onward-delegation depth
	// cap the delegation-graph gate (enforceEdgeModeAndDepth, via
	// buildDelegationDepthResolver) already authorized THIS specific delegation
	// call against — resolved from the matched edge's own Depth and the global
	// SubTurn.MaxDepth ceiling via the shared resolveEffectiveDelegationDepth
	// function. When set, spawnSubTurn's own depth check uses this value
	// INSTEAD of independently re-deriving one from getSubTurnConfig's
	// global-only default, so an explicit per-edge Depth is never silently
	// overridden by the backstop's own default (#477, FR-D9/FR-D10). nil means
	// "no override" — e.g. self-delegation or an untargeted call, where no
	// single edge's Depth uniquely applies — and spawnSubTurn falls back to
	// getSubTurnConfig's own (shared-function) resolution.
	ResolvedMaxDepth *int

	// Can be extended with temperature, topP, etc.
}

// ====================== Context Keys ======================
type agentLoopKeyType struct{}

var agentLoopKey = agentLoopKeyType{}

// WithAgentLoop injects AgentLoop into context for tool access
func WithAgentLoop(ctx context.Context, al *AgentLoop) context.Context {
	return context.WithValue(ctx, agentLoopKey, al)
}

// AgentLoopFromContext retrieves AgentLoop from context
func AgentLoopFromContext(ctx context.Context) *AgentLoop {
	al, _ := ctx.Value(agentLoopKey).(*AgentLoop)
	return al
}

// ====================== Helper Functions ======================

// resolveDelegateSoul returns the soul (system-prompt text) of the configured
// agent identified by agentID, or "" when the agent has no soul / is unknown.
//
// Resolution order:
//  1. The compiled core prompt (coreagent.GetPrompt) — for seeded base agents
//     and the worker. The worker prompt is intentionally empty today (a
//     worker's soul is OPTIONAL), so a worker with no SOUL.md resolves to "".
//  2. The agent's on-disk SOUL.md content — for custom agents and operators
//     who have placed a SOUL.md in the worker workspace.
//
// An unknown agentID resolves to "" so an unresolved target never falls back
// to the legacy generic "You are a subagent" string. The sub-turn's true
// system role is then empty for that delegate, and the task is the only
// user-facing input.
//
// Used by spawnSubTurn and runExternalCLISubTurn to compose the
// (soul, task) prompt pair uniformly across the native and external-cli
// executors (worker property-model correction: soul is OPTIONAL and the
// composition is identical for both).
func resolveDelegateSoul(al *AgentLoop, agentID string) string {
	if al == nil || agentID == "" {
		return ""
	}
	// 1. Compiled base/worker prompt.
	if compiled := coreagent.GetPrompt(agentID); compiled != "" {
		return compiled
	}
	// 2. On-disk SOUL.md for the agent's workspace. We need the agent's
	//    config to resolve the workspace, then read <workspace>/SOUL.md.
	cfg := al.GetConfig()
	if cfg == nil {
		return ""
	}
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID != agentID {
			continue
		}
		ac := cfg.Agents.List[i]
		ws := ac.Workspace
		if ws == "" {
			ws = cfg.Agents.Defaults.Workspace
		}
		if ws == "" {
			return ""
		}
		// SOUL.md is the operator's optional persona text for a custom agent.
		// Read with os; an empty/missing file is a valid worker (soul-less) state.
		path := filepath.Join(ws, "SOUL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
	return ""
}

// composeDelegateInput builds the prompt string the external-cli runner sees
// as its input, matching the native path's (system, user) split: the soul is
// prepended (when present) and the task follows. When the soul is empty — the
// worker case where the soul is OPTIONAL — the input is the task alone, with
// no persona text and no legacy "You are a subagent" wrapper.
//
// An explicit ActualSystemPrompt from the caller (legacy / future callers)
// takes precedence over resolveDelegateSoul so the dispatch site is a single
// composition point.
func composeDelegateInput(al *AgentLoop, task, actualSystem, targetAgentID string) string {
	// Prefer the explicitly-supplied system prompt when present.
	soul := strings.TrimSpace(actualSystem)
	if soul == "" && targetAgentID != "" {
		soul = strings.TrimSpace(resolveDelegateSoul(al, targetAgentID))
	}
	if soul == "" {
		return task
	}
	// Same shape as the native path: the soul is the system context, the
	// task is the user input. Keep it as a single string for the external
	// CLI; the child transcript records it as one user message.
	return fmt.Sprintf("## System\n\n%s\n\n## Task\n\n%s", soul, task)
}

func (al *AgentLoop) generateSubTurnID() string {
	return fmt.Sprintf("subturn-%d", al.subTurnCounter.Add(1))
}

// ====================== Core Function: spawnSubTurn ======================

// AgentLoopSpawner implements tools.SubTurnSpawner interface.
// This allows tools to spawn sub-turns without circular dependency.
type AgentLoopSpawner struct {
	al *AgentLoop
}

// SpawnSubTurn implements tools.SubTurnSpawner interface.
func (s *AgentLoopSpawner) SpawnSubTurn(
	ctx context.Context,
	cfg tools.SubTurnConfig,
) (*tools.ToolResult, error) {
	parentTS := turnStateFromContext(ctx)
	if parentTS == nil {
		return nil, errors.New(
			"parent turnState not found in context - cannot spawn sub-turn outside of a turn",
		)
	}

	// Convert tools.SubTurnConfig to agent.SubTurnConfig
	agentCfg := SubTurnConfig{
		Model:              cfg.Model,
		Tools:              cfg.Tools,
		SystemPrompt:       cfg.SystemPrompt,
		ActualSystemPrompt: cfg.ActualSystemPrompt,
		TargetAgentID:      cfg.TargetAgentID,
		InitialMessages:    cfg.InitialMessages,
		InitialTokenBudget: cfg.InitialTokenBudget,
		MaxTokens:          cfg.MaxTokens,
		Async:              cfg.Async,
		Critical:           cfg.Critical,
		Timeout:            cfg.Timeout,
		MaxContextRunes:    cfg.MaxContextRunes,
		TaskLabel:          cfg.TaskLabel,
		ResolvedMaxDepth:   cfg.ResolvedMaxDepth,
	}

	return spawnSubTurn(ctx, s.al, parentTS, agentCfg)
}

// NewSubTurnSpawner creates a SubTurnSpawner for the given AgentLoop.
func NewSubTurnSpawner(al *AgentLoop) *AgentLoopSpawner {
	return &AgentLoopSpawner{al: al}
}

// SpawnSubTurn is the exported entry point for tools to spawn sub-turns.
// It retrieves AgentLoop and parent turnState from context and delegates to spawnSubTurn.
func SpawnSubTurn(ctx context.Context, cfg SubTurnConfig) (*tools.ToolResult, error) {
	al := AgentLoopFromContext(ctx)
	if al == nil {
		return nil, errors.New(
			"AgentLoop not found in context - ensure context is properly initialized",
		)
	}

	parentTS := turnStateFromContext(ctx)
	if parentTS == nil {
		return nil, errors.New(
			"parent turnState not found in context - cannot spawn sub-turn outside of a turn",
		)
	}

	return spawnSubTurn(ctx, al, parentTS, cfg)
}

func spawnSubTurn(
	ctx context.Context,
	al *AgentLoop,
	parentTS *turnState,
	cfg SubTurnConfig,
) (result *tools.ToolResult, err error) {
	// Get effective SubTurn configuration
	rtCfg := al.getSubTurnConfig()

	// 0. Acquire concurrency semaphore FIRST to ensure it's released even if early validation fails.
	// Blocks if parent already has maxConcurrentSubTurns running, with a timeout to prevent indefinite blocking.
	// Also respects context cancellation so we don't block forever if parent is aborted.
	// NOTE: The semaphore is released immediately after runTurn completes (not in a defer) to
	// ensure it is freed before the cleanup phase (async result delivery), which may block on
	// a full pendingResults channel. Holding the semaphore through cleanup would allow the
	// parent's goroutine to be blocked waiting for a semaphore slot while child turns are
	// blocked delivering results — a deadlock.
	var semAcquired bool
	if parentTS.concurrencySem != nil {
		// Create a timeout context for semaphore acquisition
		timeoutCtx, cancel := context.WithTimeout(ctx, rtCfg.concurrencyTimeout)
		defer cancel()

		select {
		case parentTS.concurrencySem <- struct{}{}:
			semAcquired = true
			defer func() {
				if semAcquired {
					<-parentTS.concurrencySem
				}
			}()
		case <-timeoutCtx.Done():
			// Check parent context first - if it was canceled, propagate that error
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Otherwise it's our timeout
			return nil, fmt.Errorf("%w: all %d slots occupied for %v",
				ErrConcurrencyTimeout, rtCfg.maxConcurrent, rtCfg.concurrencyTimeout)
		}
	}

	// 1. Depth limit check. cfg.ResolvedMaxDepth, when set, is the effective
	// cap the delegation-graph gate (enforceEdgeModeAndDepth) already
	// authorized THIS specific call against — it takes precedence over
	// rtCfg.maxDepth's own global-only default so an explicit per-edge Depth
	// is never silently overridden by this backstop (#477, FR-D9/FR-D10).
	effectiveMaxDepth := rtCfg.maxDepth
	if cfg.ResolvedMaxDepth != nil {
		effectiveMaxDepth = *cfg.ResolvedMaxDepth
	}
	if parentTS.depth >= effectiveMaxDepth {
		logger.WarnCF("subturn", "Depth limit exceeded", map[string]any{
			"parent_id": parentTS.turnID,
			"depth":     parentTS.depth,
			"max_depth": effectiveMaxDepth,
		})
		return nil, ErrDepthLimitExceeded
	}

	// 2. Config validation
	if cfg.Model == "" {
		return nil, ErrInvalidSubTurnConfig
	}

	// 3. Determine timeout for child SubTurn
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = rtCfg.defaultTimeout
	}

	// 4. Create INDEPENDENT child context (not derived from parent ctx).
	// This allows the child to continue running after parent finishes gracefully.
	// The child has its own timeout for self-protection.
	//
	// Design note: the child context is intentionally not derived from ctx so
	// that Critical sub-turns survive a graceful per-turn parent cancellation.
	// Process-level shutdown is handled by AgentLoop.Stop() and the
	// activeRequests WaitGroup rather than context propagation here; the child
	// timeout (defaultSubTurnTimeout, typically 5 minutes) acts as a safety
	// ceiling that prevents runaway sub-turns from blocking clean shutdown.
	childCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	childID := al.generateSubTurnID()

	// FR-H-003: Extract the parent spawn tool call's ID from context. This was injected
	// by loop.go via withSpawnToolCallID before calling ExecuteWithContext on the spawn tool.
	// It becomes the parentSpawnCallID for the child turn, enabling correlation of all
	// child tool calls back to their originating spawn call on the wire.
	parentSpawnCallID := spawnToolCallIDFromContext(ctx)

	// W1-12: guard against degenerate input (empty parentSpawnCallID). When the
	// spawn tool call ID was not injected into context, "span_" + "" == "span_"
	// which collides across sub-turns and corrupts the SubagentBlock. We still run
	// the sub-turn, but emit no subagent_start / subagent_end WS frames so the
	// tool call renders as a flat ToolCallBadge instead of an unrooted span.
	emitSpanEvents := parentSpawnCallID != ""
	if !emitSpanEvents {
		slog.Warn("subturn: empty parent_spawn_call_id — skipping span lifecycle emission",
			"child_id", childID,
			"parent_turn_id", parentTS.turnID,
		)
	}

	// Compute span_id deterministically (FR-H-004): "span_" + parentSpawnCallID.
	spanID := "span_" + parentSpawnCallID

	// subTurnStartedAt records the wall-clock start for duration_ms in SubTurnEndPayload.
	subTurnStartedAt := time.Now()

	// Get the agent instance from parent, falling back to the default agent.
	// Wrap it in a shallow copy that uses an ephemeral (in-memory only) session store
	// so that child turns never pollute or persist to the parent's session history.
	baseAgent := parentTS.agent
	if baseAgent == nil {
		baseAgent = al.registry.GetDefaultAgent()
	}
	if baseAgent == nil {
		return nil, errors.New("parent turnState has no agent instance")
	}

	// ADR-032 / no-inheritance identity fix: resolve the actual DELEGATE named
	// by TargetAgentID from the registry. A delegated sub-turn runs as that
	// agent's own real instance — dispatch kind, workspace, model/provider,
	// tools, tool policy, and every other agent-level setting all come from
	// the resolved target, never from baseAgent (the PARENT doing the
	// delegating). The parent contributes only the task prompt and the fact
	// that delegating to this target was authorized (the workspace
	// delegation-graph gate, enforced before spawnSubTurn is reached) — see
	// the execSource construction below for the full field list this covers.
	// Resolution is best-effort: an unresolvable target (deleted/renamed
	// since delegation was configured, or self-delegation where
	// TargetAgentID=="") falls back to baseAgent, so a sub-turn is never
	// silently dropped — self-delegation trivially satisfies "inherit
	// nothing from the parent" by being its own source.
	var targetAgent *AgentInstance
	// FIX 3 (silent failure F1 / arch #5): when TargetAgentID is set but does
	// not resolve (deleted/renamed delegate), the fallback below to the
	// parent's own config must not be SILENT — the caller/LLM and the
	// session/audit trail both need to learn a different agent ran than the
	// one that was asked for. targetAgentUnresolved is only set in this
	// branch, never for benign self-delegation (cfg.TargetAgentID == "").
	var targetAgentUnresolved bool
	var targetAgentFallbackWarning string
	if cfg.TargetAgentID != "" {
		if t, ok := al.registry.GetAgent(cfg.TargetAgentID); ok && t != nil {
			targetAgent = t
		} else {
			targetAgentUnresolved = true
			targetAgentFallbackWarning = fmt.Sprintf(
				"[delegation warning: target agent %q was not found; ran with the parent's own configuration instead] ",
				cfg.TargetAgentID)
			slog.Warn(
				"subturn: target agent not found in registry; dispatch falls back to parent's own executor config",
				"target_agent_id",
				cfg.TargetAgentID,
				"parent_id",
				parentTS.turnID,
			)
		}
	}
	execSource := baseAgent
	if targetAgent != nil {
		execSource = targetAgent
	}
	// Decide dispatch kind from the resolved DELEGATE's own executor config
	// (not the parent's) — see the comment above. Resolved here, ahead of the
	// AgentInstance build below, so the external-cli-only field overrides can
	// be applied precisely when needed and left untouched for native dispatch.
	// dispatchErr is consulted at the original dispatch site (step 8 below);
	// resolving it early does not change when the sub-turn actually fails —
	// only when the DECISION is computed.
	dispatchKind, dispatchErr := runner.ResolveDispatch(executorConfigOf(execSource))

	// Operator-confirmed design principle: a delegated sub-turn inherits
	// NOTHING from the parent — delegating to an agent means running that
	// agent's own real instance. The parent's only contribution is the task
	// prompt (SubTurnConfig.SystemPrompt, used as the child's first user
	// message) and the fact that delegation to this target was authorized at
	// all (the workspace delegation-graph gate, enforced before spawnSubTurn
	// is ever reached). Every agent-level setting below — including
	// Model/Provider/Candidates, previously deliberately left as the
	// PARENT's — comes from execSource (the resolved delegate when
	// TargetAgentID matched a real registry agent, else baseAgent itself for
	// self-delegation, where "inherit nothing from the parent" trivially
	// means "use its own settings").
	//
	// execSource.Model/Provider/Candidates/ThinkingLevel are the four fields
	// protected by execSource.mu (AgentInstance.mu's doc comment,
	// instance.go:27-30) — concurrently written by SwitchModel/ApplyAgentModel
	// (loop.go ~3418-3435, which takes mu.Lock() around the full tuple flip
	// AND the providerPool swap — its own comment there states this makes
	// "(Model, Provider, Candidates, ProviderPool) a single coherent swap
	// from any reader's perspective") while a turn referencing this same live
	// registry AgentInstance is in flight. A single RLock-snapshot-RUnlock
	// here (one lock acquisition, not one per field) avoids both a race and a
	// torn read across the quad, mirroring the existing sibling read-sites
	// (loop.go:5249-5255, 5570-5572, 8554-8556). providerPool.Load() is
	// captured in this SAME window — it is a separate atomic field, not one
	// of the four mu names, but ApplyAgentModel's writer-side lock covers it
	// too, so reading it outside this RLock would reopen the exact
	// torn-snapshot window the lock exists to close: a concurrent
	// ApplyAgentModel between RUnlock and a later, separate Load() could pair
	// this snapshot's (now-stale) Candidates with a providerPool already
	// rebuilt for a NEW candidate set, which GetProviderForCandidate would
	// silently mismatch. Verified deadlock-safe: runTurn's tool-dispatch loop
	// (loop.go ~6900, which reaches here via the spawn/subagent tools) does
	// NOT hold this lock across the ExecuteWithContext call that eventually
	// invokes spawnSubTurn, so this RLock cannot contend with a lock already
	// held by our own call stack.
	execSource.mu.RLock()
	execModel := execSource.Model
	execProvider := execSource.Provider
	execCandidates := execSource.Candidates
	execThinkingLevel := execSource.ThinkingLevel
	execProviderPool := execSource.providerPool.Load()
	execSource.mu.RUnlock()

	ephemeralStore := newEphemeralSession(nil)
	// Build a new AgentInstance from execSource's fields to avoid copying the
	// mutex. Sessions is the one deliberate exception — always a fresh
	// ephemeral (in-memory only) store, so child turns never pollute or
	// persist to the source agent's real session history. Tools is set below
	// (needs the delegate/hand_off exclusion, not a plain copy); toolPolicy
	// and providerPool are unexported atomic fields a struct literal cannot
	// copy at all, also set below.
	agent := AgentInstance{
		ID:                        execSource.ID,
		Name:                      execSource.Name,
		Model:                     execModel,
		Fallbacks:                 execSource.Fallbacks,
		FallbackModels:            execSource.FallbackModels,
		Workspace:                 execSource.Workspace,
		MaxIterations:             execSource.MaxIterations,
		MaxTokens:                 execSource.MaxTokens,
		Temperature:               execSource.Temperature,
		ThinkingLevel:             execThinkingLevel,
		ContextWindow:             execSource.ContextWindow,
		SummarizeMessageThreshold: execSource.SummarizeMessageThreshold,
		SummarizeTokenPercent:     execSource.SummarizeTokenPercent,
		Provider:                  execProvider,
		Sessions:                  ephemeralStore,
		ContextBuilder:            execSource.ContextBuilder,
		Subagents:                 execSource.Subagents,
		SkillsFilter:              execSource.SkillsFilter,
		Candidates:                execCandidates,
		TimeoutSeconds:            execSource.TimeoutSeconds,
		Router:                    execSource.Router,
		LightCandidates:           execSource.LightCandidates,
		LightProvider:             execSource.LightProvider,
		AgentType:                 execSource.AgentType,
		IsRoutingDefault:          execSource.IsRoutingDefault,
	}
	// providerPool is tied to the SAME Candidates it was built for — now that
	// Candidates is execSource's own (above), the pool must match, or
	// GetProviderForCandidate would silently miss every FR-007
	// provider-pinned fallback candidate and fall back to the primary
	// provider. execProviderPool was captured inside the RLock above,
	// alongside Candidates — NOT re-Load()'d here — so the pairing is
	// guaranteed consistent even if ApplyAgentModel runs concurrently between
	// this point and the snapshot above.
	if execProviderPool != nil {
		agent.StoreProviderPool(*execProviderPool)
	}
	// LoadToolPolicy()/StoreToolPolicy() — same "unexported atomic field, struct
	// literal can't copy it" situation as providerPool. Left unset, every tool
	// fails closed to deny (resolveEffectivePolicyWith: no entry on either
	// side -> deny) — this was the second half of the original identity bug
	// (see below).
	agent.StoreToolPolicy(execSource.LoadToolPolicy())
	// ID/ContextBuilder — the first half of the original identity bug, still
	// worth naming explicitly: ContextBuilder.BuildSystemPrompt() resolves
	// the compiled soul via cb.agentID, so reusing the PARENT's ContextBuilder
	// (the pre-fix behavior) meant a delegate literally answered as the
	// parent — observed live: delegating to Worker (an intentionally
	// soul-less agent) returned Jim's own compiled persona verbatim. This
	// also caused the tool-policy split-brain: tools.WithAgentID(turnCtx,
	// ts.agent.ID) (loop.go) threads agent.ID into the tool-execution
	// context, and load_tool's canLoad resolver looks up "the calling agent"
	// by that ID via a fresh al.registry.GetAgent(...) call — so an unswapped
	// ID meant canLoad checked the PARENT's real, registry-backed policy
	// while the final FilterToolsByPolicy call (loop.go) read the child's own
	// (nil, deny-all) toolPolicy above — two different verdicts for the same
	// tool, observed live as an infinite load_tool retry loop.

	// Tool-approval grant inheritance (consent boundary — delegation): the
	// child sub-turn inherits every "Always Allow" grant the PARENT has
	// accumulated in this session, so a tool the parent already
	// always-allowed does not re-prompt when the delegate (spawn /
	// run_subagent — both funnel through this one spawnSubTurn) calls it.
	// Copy-at-spawn semantics (ApprovalGrantStore.Inherit): a snapshot of the
	// parent's grants at this moment, not a live link.
	//
	// parentTS.agentID is the identity under which the PARENT's own tool
	// calls are scoped (turnState.eventMeta/snapshot -> ToolApprovalRequest.
	// Meta.AgentID); agent.ID (== execSource.ID above) is the identity THIS
	// child turn will use for its own tool-approval requests (see
	// newTurnState(&agent, ...) below, which sets childTS.agentID = agent.
	// ID). Keying the inherit call on the same variable the child will
	// actually be looked up under keeps this correct whether execSource is
	// baseAgent (self-delegation — a harmless same-key union) or a resolved
	// target (agent.ID == targetAgent.ID, the real delegate).
	al.ApprovalGrants().Inherit(parentTS.transcriptSessionID, parentTS.agentID, agent.ID)

	// FR-H-006 REVERSAL (live UAT, 2026-07-12): "delegate" is NO LONGER excluded
	// from the child's registry. Note: distinct from the identity-swap
	// load_tool bug documented just above (ID/ContextBuilder, ~line 663) —
	// that one was wrong AGENT IDENTITY (an unswapped childTS.agentID made
	// canLoad resolve the PARENT's policy instead of the child's own); this
	// one is an INCOMPLETE TOOL SET for a correctly-identified agent (the
	// child's identity was already right, but "delegate" was unconditionally
	// missing from its registry regardless of identity or policy). Both
	// happen to manifest through the same load_tool fabricated-success
	// symptom described below, but the two are independent bugs with
	// independent fixes — do not conflate them when debugging a future
	// load_tool report. The original FR-H-006 rationale ("one level
	// only for general subagents", owner decision 2026-04-20) predates the
	// per-edge depth-cap + trust-graph delegation system that now exists
	// (workspace.DelegationEdge.Depth, config.SubTurn.MaxDepth,
	// resolveEffectiveDelegationDepth/enforceEdgeModeAndDepth — see
	// delegation_depth.go and loop.go's buildDelegationDenyCheckerForDelegate),
	// which ALREADY supports and correctly enforces multi-hop chains up to a
	// configurable depth (default defaultMaxSubTurnDepth == 3) gated by the
	// per-workspace trust graph. Excluding "delegate" from the registry was a
	// blunt, unconditional, registry-level block layered UNDERNEATH that real
	// gate — it did not just enforce "one level only", it made ANY grandchild
	// delegation structurally impossible regardless of an explicit, wired,
	// "unrestricted" trust edge, and it failed in a confusing way: the
	// unified `load_tool` infra tool (ScopeCore, lazily loaded — see
	// pkg/tools/tools_tool.go) reports a fabricated LOAD SUCCESS for
	// "delegate" inside a child sub-turn (its canLoad/markLoaded closures
	// resolve the caller's agent via al.registry.GetAgent(callerID), i.e. the
	// PERSISTENT top-level agent instance, not this ephemeral child's own
	// execSource.Tools clone), while the child's own ts.agent.Tools —
	// consulted by loop.go's per-iteration filterTimePolicyMap / FR-079
	// resolveToolPolicyAtExec TOCTOU re-check — never actually gained the
	// tool. The LLM would then be told "delegate" loaded fine and immediately
	// hit `{"error":"permission_denied", ...}` on the very next call, without
	// ever reaching DelegateTool.Execute's real trust-set/mode/depth gate
	// (delegationDenyBackground/Await, SetDelegationDepthResolver) at all —
	// blocking every multi-hop chain (e.g. jim -> ray -> planner ->
	// {explorer|researcher}) even when every edge in the chain was explicitly
	// authorized. "hand_off" remains excluded: a nested sub-turn hijacking the
	// ACTIVE parent session's agent is a distinct, still-valid concern
	// (session takeover) unrelated to task-delegation chain depth, and is not
	// governed by the depth-cap/trust-graph system at all.
	//
	// Sourced from execSource (the resolved delegate, or baseAgent for
	// self-delegation) — NOT unconditionally baseAgent. Workspace-scoped
	// tools (read_file/write_file/edit_file/bash/...) bind their root
	// directory ONCE, at NewAgentInstance construction time
	// (tools.NewReadFileTool(workspace, ...), tools.NewExecToolWithConfig
	// (workspace, ...)) — CloneExcept is a shallow filter that copies the
	// SAME underlying tool pointers, it does not rebind them. Cloning from
	// baseAgent.Tools regardless of delegation target would leave a native
	// delegate's actual file/bash sandbox boundary silently pinned to the
	// PARENT's workspace even after the identity/ContextBuilder swap above
	// says "you are the target" — a declared-vs-enforced mismatch and a real
	// data-boundary gap between parent and delegate workspaces. Cloning from
	// execSource.Tools instead reuses the TARGET's own already-correctly
	// -workspace-bound tool objects, the same pattern already used for
	// ContextBuilder.
	if execSource.Tools != nil {
		// Known residual gap (not fixed here, documented only): unlike
		// "delegate" above, "hand_off" is unconditionally excluded from
		// EVERY child sub-turn's registry, and the SAME load_tool
		// fabricated-success-then-permission_denied bug just cured for
		// "delegate" is still live for "hand_off" — canLoad/markLoaded
		// (pkg/tools/tools_tool.go) resolve the caller via
		// al.registry.GetAgent(callerID), the PERSISTENT top-level agent,
		// not this ephemeral child's own registry, so load_tool can still
		// report a fabricated success for "hand_off" here even though it is
		// structurally absent from agent.Tools. Root-caused but out of
		// scope for this fix (tools_tool.go is a larger, separate change).
		agent.Tools = execSource.Tools.CloneExcept(tools.ExcludedHandoff)
		// Log the constructed registry so operators can debug "my subagent has no tools" issues.
		slog.Info("subturn: child registry constructed",
			"excluded", []string{"hand_off"},
			"remaining_count", agent.Tools.Count(),
			"child_id", childID,
		)
	}

	// Create processOptions for the child turn.
	// FR-6a: inherit TranscriptSessionID from parent so that cascade cancel in
	// InterruptSession can match this sub-turn via ts.transcriptSessionID == sessionID.
	// Without this, every sub-turn has transcriptSessionID == "" and the cascade
	// matches only the parent turn (the load-bearing bug fixed here).
	//
	// Soul composition: the system role is the DELEGATE's own soul
	// (config.AgentConfig.Soul or the compiled coreagent.GetPrompt), and the
	// task becomes the first user message. When the spawn did not name a
	// target agent (TargetAgentID == ""), the parent's own soul applies via
	// the empty override — the loop's standard identity builder will pick up
	// the parent's SOUL.md / compiled prompt as before. An explicit
	// ActualSystemPrompt from the caller (legacy / future callers) takes
	// precedence; otherwise we resolve the delegate's soul so a worker with
	// an EMPTY soul runs with an EMPTY system role, NOT the legacy
	// "You are a subagent" string.
	systemOverride := cfg.ActualSystemPrompt
	if systemOverride == "" {
		if cfg.TargetAgentID != "" {
			systemOverride = resolveDelegateSoul(al, cfg.TargetAgentID)
		}
	}
	opts := processOptions{
		SessionKey:              childID,
		Channel:                 parentTS.channel,
		ChatID:                  parentTS.chatID,
		SenderID:                parentTS.opts.SenderID,
		SenderDisplayName:       parentTS.opts.SenderDisplayName,
		UserMessage:             cfg.SystemPrompt, // Task description becomes the first user message
		SystemPromptOverride:    systemOverride,
		Media:                   nil,
		InitialSteeringMessages: cfg.InitialMessages,
		DefaultResponse:         "",
		EnableSummary:           false,
		SendResponse:            false,
		NoHistory:               true, // SubTurns don't use session history
		SkipInitialSteeringPoll: true,
		TranscriptSessionID:     parentTS.transcriptSessionID,
		TranscriptStore:         parentTS.transcriptStore,
	}

	// Create event scope for the child turn
	scope := al.newTurnEventScope(agent.ID, childID)

	// Create child turnState using the new API
	childTS := newTurnState(&agent, opts, scope)

	// Set SubTurn-specific fields
	childTS.cancelFunc = cancel
	childTS.critical = cfg.Critical
	childTS.depth = parentTS.depth + 1
	childTS.parentTurnID = parentTS.turnID
	childTS.parentTurnState = parentTS
	childTS.pendingResults = make(chan *tools.ToolResult, 16)
	childTS.concurrencySem = make(chan struct{}, rtCfg.maxConcurrent)
	childTS.al = al                  // back-ref for hard abort cascade
	childTS.session = ephemeralStore // same store as agent.Sessions
	// FR-H-003: set parentSpawnCallID so all ToolExec* events emitted by this child turn
	// carry the parent spawn's ToolCall.ID as ParentSpawnCallID.
	childTS.parentSpawnCallID = parentSpawnCallID

	// FIX 3 (silent failure F1 / arch #5): surface the target-resolution
	// fallback in the session/audit trail — not just the process-log
	// slog.Warn above — using the same EventKindError + appendErrorTranscript
	// pair the LLM-call-error and rate-limit paths already use (loop.go
	// ~965-991, ~6188-6200), so a session replay after page reload still
	// shows the anomaly. The ToolResult.ForLLM prefix (surfacing it to the
	// delegating caller/LLM) is applied uniformly for every return path in
	// the cleanup defer below.
	if targetAgentUnresolved {
		al.emitEvent(
			EventKindError,
			childTS.eventMeta("spawnSubTurn", "subturn.delegation_fallback"),
			ErrorPayload{
				Stage:   "subturn_delegation",
				Message: strings.TrimSpace(targetAgentFallbackWarning),
			},
		)
		childTS.appendErrorTranscript(
			EventKindError.String(), "spawnSubTurn", strings.TrimSpace(targetAgentFallbackWarning),
		)
	}

	// Token budget initialization/inheritance
	// If InitialTokenBudget is explicitly provided (e.g., by team tool), use it.
	// Otherwise, inherit from parent's tokenBudget (for nested SubTurns).
	if cfg.InitialTokenBudget != nil {
		childTS.tokenBudget = cfg.InitialTokenBudget
	} else if parentTS.tokenBudget != nil {
		childTS.tokenBudget = parentTS.tokenBudget
	} else if rtCfg.defaultTokenBudget > 0 {
		// Apply default token budget from config if no budget is set
		budget := &atomic.Int64{}
		budget.Store(int64(rtCfg.defaultTokenBudget))
		childTS.tokenBudget = budget
	}

	// IMPORTANT: Put childTS into childCtx so that code inside runTurn can retrieve it
	childCtx = withTurnState(childCtx, childTS)
	childCtx = WithAgentLoop(childCtx, al) // Propagate AgentLoop to child turn

	childTS.ctx = childCtx

	// Register child turn state so GetAllActiveTurns/Subagents can find it
	al.activeTurnStates.Store(childID, childTS)
	defer al.activeTurnStates.Delete(childID)

	// 5. Establish parent-child relationship (thread-safe)
	parentTS.mu.Lock()
	parentTS.childTurnIDs = append(parentTS.childTurnIDs, childID)
	parentTS.mu.Unlock()

	// 6. Emit Spawn event (FR-H-004: carries SpanID, ParentSpawnCallID, TaskLabel, ChatID, AgentID)
	// task label: prefer cfg.TaskLabel if set (from spawn tool's label arg); else use the first
	// 60 runes of SystemPrompt as a fallback so the WS frame always has something human-readable.
	taskLabel := cfg.TaskLabel
	if taskLabel == "" {
		runes := []rune(cfg.SystemPrompt)
		if len(runes) > 60 {
			taskLabel = string(runes[:60])
		} else {
			taskLabel = cfg.SystemPrompt
		}
	}
	// W1-12: only emit span lifecycle events when parentSpawnCallID is non-empty.
	if emitSpanEvents {
		slog.Debug("subagent_start",
			"span_id", spanID,
			"parent_call_id", parentSpawnCallID,
			"agent_id", childTS.agentID,
		)
		al.emitEvent(EventKindSubTurnSpawn,
			childTS.eventMeta("spawnSubTurn", "subturn.spawn"),
			SubTurnSpawnPayload{
				AgentID:           childTS.agentID,
				Label:             childID,
				ParentTurnID:      parentTS.turnID,
				SpanID:            spanID,
				ParentSpawnCallID: session.ToolCallID(parentSpawnCallID),
				TaskLabel:         taskLabel,
				ChatID:            parentTS.chatID,
				SessionID:         parentTS.transcriptSessionID,
			},
		)
	}

	// 7. Defer cleanup: deliver result (for async), emit End event, and recover from panics
	defer func() {
		if r := recover(); r != nil {
			// Include childID in the error message so support can correlate
			// a user-visible "subturn panicked" back to the logged stack trace.
			err = fmt.Errorf("subturn %q panicked: %v", childID, r)
			result = nil
			slog.Error("subturn: panic recovered",
				"child_id", childID,
				"parent_id", parentTS.turnID,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(debug.Stack()),
			)
		}

		// FIX 3 (silent failure F1 / arch #5): prefix the delegation-fallback
		// warning onto ForLLM for every return path (dispatch-reject, external-cli,
		// native, and the async-delivered copy below) — one insertion point so the
		// caller/LLM sees it regardless of which branch produced the result.
		if targetAgentUnresolved && result != nil {
			result.ForLLM = targetAgentFallbackWarning + result.ForLLM
		}

		// Result Delivery Strategy (Async vs Sync)
		if cfg.Async {
			deliverSubTurnResult(al, parentTS, childID, result)
		}

		// W1-12: only emit span end event when parentSpawnCallID was non-empty.
		if emitSpanEvents {
			endStatus := SubTurnStatusSuccess
			var endReason string
			switch {
			case err != nil && errors.Is(childCtx.Err(), context.Canceled) && childTS.cancelFired.Load():
				// FIX 4 (7-reviewer-gate follow-up on FIX 5): this SPECIFIC
				// sub-turn's own ClaimCancel was claimed — i.e. RequestCancel
				// targeted THIS turn directly, not the parent. Reachable, if
				// narrow: GetActiveTurnHookForSession's fallback (turn.go)
				// resolves to a sub-turn when its session's ROOT turn has
				// already finished but a Critical:true sub-turn (which
				// survives a graceful parent finish by design — see
				// SubTurnConfig.Critical's doc comment) is still running on
				// that same session, and a later RequestCancel against that
				// session finds and cancels the sub-turn itself. Distinct
				// from — and takes priority over — the more common cascade
				// case below (childTS.cancelFired stays false there because
				// the parent's Finish(true) calls childTS.Finish(true)
				// directly, bypassing ClaimCancel entirely). No `reason` is
				// set: the wire contract documents reason as meaningful only
				// "when status is interrupted", and this is a genuine direct
				// cancel, not a parent-caused interruption.
				endStatus = SubTurnStatusCancelled
			case err != nil && errors.Is(childCtx.Err(), context.Canceled):
				// FIX 5: the child's own context was explicitly canceled —
				// reached via the parent's hard-abort cascade
				// (turnState.Finish(true) calling childTS.cancelFunc(),
				// which IS childCtx's own cancel func, assigned at
				// childTS.cancelFunc = cancel above). A user-canceling the
				// parent turn while this async delegate was in flight must
				// read back on replay as "interrupted" — not "error", which
				// is indistinguishable from a genuine failure sitting right
				// next to the parent's own correctly-labeled
				// "(interrupted)" entry. A genuine timeout
				// (context.DeadlineExceeded, the sub-turn's own Timeout
				// config expiring rather than an external cancel) falls
				// through to SubTurnStatusError below — that IS a real
				// failure, not a cancellation.
				endStatus = SubTurnStatusInterrupted
				// FIX 4: populate the wire contract's SubagentEndFrame.reason
				// (frontend already renders it — src/components/chat/SubagentBlock.tsx
				// — but no Go path ever set it before this fix, so it silently
				// rendered without the "(reason)" detail chip for every
				// interrupted span). parentTS.cancelFired is the cheapest
				// honest signal available here: it is true whenever the
				// PARENT's own ClaimCancel was claimed, which covers both a
				// live user cancel (web SPA/CLI/Tier A/B -> RequestCancel)
				// AND pkg/gateway/schedules.go's watchDeadline force-abort on
				// a scheduled run's deadline (it too calls RequestCancel,
				// with CancelCanceller{UserID:"scheduler",Channel:"cron"}) —
				//nolint:misspell // documents the literal wire enum value, matches frontend TS union
				// both are honestly summarized as "parent_cancelled" here.
				// Distinguishing "the parent was explicitly canceled" from
				// "the parent hit its own deadline" precisely would require
				// threading the canceller identity through turnState, which
				// is a bigger change than this fix's scope; "unknown" is the
				// honest fallback for any other, rarer trigger (e.g. a hook
				// decision's hard-abort) where parentTS.cancelFired never
				// got set at all.
				if parentTS.cancelFired.Load() {
					endReason = "parent_cancelled" //nolint:misspell // wire value, frontend TS union
				} else {
					endReason = "unknown"
				}
			case err != nil:
				endStatus = SubTurnStatusError
			}
			subTurnDurationMS := time.Since(subTurnStartedAt).Milliseconds()
			slog.Debug("subagent_end",
				"span_id", spanID,
				"parent_call_id", parentSpawnCallID,
				"agent_id", childTS.agentID,
			)

			// Wave 3 fix 5b: persist the sub-turn's REAL terminal status/duration
			// onto the spawning "delegate" tool call's own persisted
			// session.ToolCall record. For async delegation (DelegateTool.
			// executeAsync, pkg/tools/delegate.go) that record was already
			// written moments after spawnSubTurn started, carrying a
			// placeholder ack (Status="success", DurationMS≈0, from
			// tools.AsyncResult) — this corrects it to the real value so a
			// session reload (pkg/gateway/replay.go) shows the same
			// status/duration the live WS stream showed, instead of
			// emitNestedToolCalls re-deriving a different, incompatible
			// aggregate from child tool calls (which flips to "error" on any
			// single denied child tool, even when the sub-turn itself
			// completed successfully). No-op for synchronous delegation — see
			// UpdateToolCallStatus's doc comment for why.
			//
			// FIX 4: for ASYNC delegation specifically, "not found on the
			// first attempt" is not necessarily a permanent no-op — it can be
			// a genuine happens-before race: DelegateTool.executeAsync
			// launches this sub-turn in a goroutine and returns to the
			// parent immediately, while the parent only writes ITS OWN
			// placeholder ack record after further processing (hooks, media,
			// events) back in its own call stack. A fast-failing dispatch
			// can reach this defer before that write lands.
			// updateToolCallStatusWithRetry retries briefly (bounded, ~1s
			// total) ONLY when cfg.Async is true, establishing real
			// happens-before ordering instead of silently leaving the
			// placeholder permanent.
			if parentTS.transcriptStore != nil && parentTS.transcriptSessionID != "" {
				found, updateErr := updateToolCallStatusWithRetry(
					parentTS.transcriptStore,
					parentTS.transcriptSessionID,
					session.ToolCallID(parentSpawnCallID),
					string(endStatus),
					subTurnDurationMS,
					cfg.Async,
				)
				switch {
				case updateErr != nil:
					slog.Warn("subturn: failed to persist real end status/duration onto spawn tool call",
						"session_id", parentTS.transcriptSessionID,
						"parent_spawn_call_id", parentSpawnCallID,
						"error", updateErr,
					)
				case cfg.Async && !found:
					// FIX 3 (7-reviewer-gate follow-up): updateErr == nil AND
					// found == false means the retry budget (~935ms across 6
					// attempts) was exhausted without ever locating the
					// placeholder record — a real, named scenario in
					// updateToolCallStatusWithRetry's own doc comment (the
					// parent's hooks/media/event processing taking longer
					// than the retry budget). Without this branch, that
					// outcome was silently undiagnosable: no error, so no
					// log — the delegate's transcript entry permanently
					// keeps the stale placeholder ack (success/0ms) with
					// zero trace of why. This is exactly the "reload
					// silently disagrees with live" failure class this
					// whole fix pass exists to close.
					slog.Warn("subturn: gave up waiting for spawn tool call's placeholder record after "+
						"exhausting retry budget; reload will show the stale placeholder ack instead of "+
						"the real terminal status",
						"session_id", parentTS.transcriptSessionID,
						"parent_spawn_call_id", parentSpawnCallID,
						"resolved_status", endStatus,
					)
				}
			}

			// The correction attempt (above) is now genuinely done — either it
			// succeeded, it exhausted its retry budget (logged above), or there
			// was nothing to correct (nil store/empty session ID). Only NOW is
			// it safe for IsSubTurnActiveForSpawnCall to let a reload/replay
			// trust this turn's persisted terminal record; see
			// turnState.subTurnRecordPersisted's doc comment (turn.go).
			childTS.subTurnRecordPersisted.Store(true)

			al.emitEvent(EventKindSubTurnEnd,
				childTS.eventMeta("spawnSubTurn", "subturn.end"),
				SubTurnEndPayload{
					AgentID:           childTS.agentID,
					Status:            endStatus,
					SpanID:            spanID,
					ParentSpawnCallID: session.ToolCallID(parentSpawnCallID),
					DurationMS:        subTurnDurationMS,
					ChatID:            parentTS.chatID,
					SessionID:         parentTS.transcriptSessionID,
					Reason:            endReason,
				},
			)
		}
	}()

	// 8. Execute the sub-turn. The executor on the resolved DELEGATE's config
	//    decides HOW: native → the Omnipus agent loop (default, unchanged);
	//    external-cli → an external CLI runner driven directly in the
	//    delegate's own workspace directory (ADR-032 relaxes the original
	//    Spec-4 FR-5.3 worktree isolation for external CLIs). remote-a2a
	//    (reserved) and unknown kinds fail the sub-turn cleanly.
	//    dispatchKind/dispatchErr were already resolved above (from the
	//    correct DELEGATE identity, before the AgentInstance was built).
	if dispatchErr != nil {
		err = dispatchErr
		result = &tools.ToolResult{
			Err:    dispatchErr,
			ForLLM: fmt.Sprintf("SubTurn dispatch rejected: %v", dispatchErr),
		}
		// Release the semaphore before returning (mirrors the post-runTurn release).
		if semAcquired {
			<-parentTS.concurrencySem
			semAcquired = false
		}
		return result, err
	}

	if dispatchKind == runner.DispatchKindExternalCLI {
		// External-cli dispatch: compose the same (soul, task) pair the
		// native path uses. An empty soul yields task-only input (the
		// worker's soul is OPTIONAL — the external CLI gets no persona text
		// if there is none). The composed string is what the external CLI
		// sees as its prompt, mirroring the native system+user split.
		externalInput := composeDelegateInput(al, cfg.SystemPrompt, cfg.ActualSystemPrompt, cfg.TargetAgentID)
		extResult, extErr := runExternalCLISubTurn(childCtx, al, childTS, externalInput, timeout)
		if semAcquired {
			<-parentTS.concurrencySem
			semAcquired = false
		}
		result = extResult
		err = extErr
		return result, err
	}

	// Native path (default, existing behavior — unchanged).
	turnRes, turnErr := al.runTurn(childCtx, childTS)

	// Re-register childTS in activeTurnStates: runTurn's OWN internal defer
	// chain (loop.go) already deleted it — al.clearActiveTurn(ts) (keyed by
	// ts.sessionKey, which equals childID here) runs as part of runTurn's
	// unwind BEFORE ts.Finish(false) even sets isFinished=true, deleting the
	// exact map entry IsSubTurnActiveForSpawnCall's Range scan depends on to
	// find this turn at all. Without this re-Store, the entry would be gone
	// from activeTurnStates for the ENTIRE remainder of this function —
	// including the persist-retry window below (up to ~935ms) — so
	// IsSubTurnActiveForSpawnCall could never report "active" for THIS span
	// again no matter what subTurnRecordPersisted says, defeating that fix
	// entirely (Range finds nothing to check IsAlive()/subTurnRecordPersisted
	// against in the first place). Re-storing under the SAME key (childID)
	// used at construction (see "Register child turn state" above) makes the
	// span findable again for exactly as long as the cleanup defer below
	// needs it; the pre-existing `defer al.activeTurnStates.Delete(childID)`
	// (registered earlier, so it runs AFTER the cleanup defer per LIFO order)
	// removes it for real once that defer — including the persistence
	// correction — has fully completed.
	al.activeTurnStates.Store(childID, childTS)

	// Release the concurrency semaphore immediately after runTurn completes,
	// before the cleanup defer runs. This prevents a deadlock where:
	// - All semaphore slots are held by sub-turns in their cleanup phase
	// - Cleanup blocks on a full pendingResults channel
	// - The parent goroutine is blocked waiting for a semaphore slot
	// - The parent cannot consume pendingResults because it is blocked on the semaphore
	if semAcquired {
		<-parentTS.concurrencySem
		semAcquired = false // prevent the defer from double-releasing
	}

	// Convert turnResult to tools.ToolResult
	if turnErr != nil {
		err = turnErr
		result = &tools.ToolResult{
			Err:    turnErr,
			ForLLM: fmt.Sprintf("SubTurn failed: %v", turnErr),
		}
	} else {
		result = &tools.ToolResult{
			ForLLM:  turnRes.finalContent,
			ForUser: turnRes.finalContent,
		}
	}

	return result, err
}

// updateToolCallStatusRetryDelays is the bounded backoff schedule
// updateToolCallStatusWithRetry uses when retrying for async delegation.
// Total budget: ~935ms across 6 retries (after the first, immediate
// attempt) — comfortably covering the parent's own tool-result
// post-processing (hooks/media/events) between ExecuteWithContext returning
// and ts.appendToolCallTranscript actually persisting the placeholder ack,
// without meaningfully delaying anything user-visible (this all runs inside
// spawnSubTurn's cleanup defer, on the child sub-turn's OWN background
// goroutine — never blocking the parent's turn).
var updateToolCallStatusRetryDelays = []time.Duration{
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
}

// updateToolCallStatusWithRetry calls store.UpdateToolCallStatus, retrying
// with updateToolCallStatusRetryDelays's bounded backoff when async is true
// and the first attempt finds no matching record yet (found=false, err=nil).
//
// FIX 4 (confirmed independently by silent-failure-hunter, architect, and
// code-reviewer): DelegateTool.executeAsync (pkg/tools/delegate.go) launches
// the child sub-turn in a goroutine and returns to the parent turn
// immediately; the PARENT writes this SAME tool call's own placeholder ack
// record only after further processing (hooks, media, events) in its own
// call stack, back in pkg/agent/loop.go's tool-execution loop. When the
// child's dispatch fails fast (e.g. a depth-limit or target-resolution
// rejection), spawnSubTurn's cleanup defer — this function's only caller —
// can reach UpdateToolCallStatus BEFORE the parent's placeholder write
// lands, and UpdateToolCallStatus's "not found" case was previously
// (incorrectly, for this specific caller) treated as a terminal, expected
// no-op. Retrying establishes REAL happens-before ordering without
// requiring pkg/tools (which deliberately has no transcript-store access —
// SubTurnSpawner/AsyncExecutor exist specifically to avoid an agent<->tools
// import cycle) to change at all.
//
// For SYNCHRONOUS delegation (async=false), found=false on the first
// attempt is the documented, PERMANENT, expected outcome — the caller
// (DelegateTool.executeSync, via the tool-execution loop) will append the
// record itself moments later, once spawnSubTurn returns with the real
// result. Retrying in that case would only waste the full backoff budget on
// every synchronous delegation for no benefit, so this makes a single,
// no-retry attempt when async is false.
func updateToolCallStatusWithRetry(
	store *session.UnifiedStore,
	sessionID string,
	toolCallID session.ToolCallID,
	status string,
	durationMS int64,
	async bool,
) (found bool, err error) {
	found, err = store.UpdateToolCallStatus(sessionID, toolCallID, status, durationMS)
	if err != nil || found || !async {
		return found, err
	}
	for _, delay := range updateToolCallStatusRetryDelays {
		time.Sleep(delay)
		found, err = store.UpdateToolCallStatus(sessionID, toolCallID, status, durationMS)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// ====================== Result Delivery ======================

// deliverSubTurnResult delivers a sub-turn result to the parent turn's pendingResults channel.
//
// IMPORTANT: This function is ONLY called for asynchronous sub-turns (Async=true).
// For synchronous sub-turns (Async=false), results are returned directly via the function
// return value to avoid double delivery.
//
// Delivery behavior:
//   - If parent turn is still running: attempts to deliver to pendingResults channel
//   - If channel is full: emits SubTurnOrphanResultEvent (result is lost from channel but tracked)
//   - If parent turn has finished: emits SubTurnOrphanResultEvent (late arrival)
//
// Thread safety:
//   - Reads parent state under lock, then releases lock before channel send
//   - Small race window exists but is acceptable (worst case: result becomes orphan)
//
// Event emissions:
//   - SubTurnResultDeliveredEvent: successful delivery to channel
//   - SubTurnOrphanResultEvent: delivery failed (parent finished or channel full)
func deliverSubTurnResult(al *AgentLoop, parentTS *turnState, childID string, result *tools.ToolResult) {
	// Let GC clean up the pendingResults channel; parent Finish will no longer close it.
	// We use defer/recover to catch any unlikely channel panics if it were ever closed.
	defer func() {
		if r := recover(); r != nil {
			logger.WarnCF("subturn", "recovered panic sending to pendingResults", map[string]any{
				"parent_id": parentTS.turnID,
				"child_id":  childID,
				"recover":   r,
			})
			if result != nil && al != nil {
				al.emitEvent(EventKindSubTurnOrphan,
					parentTS.eventMeta("deliverSubTurnResult", "subturn.orphan"),
					SubTurnOrphanPayload{ParentTurnID: parentTS.turnID, ChildTurnID: childID, Reason: "panic"},
				)
			}
		}
	}()
	parentTS.mu.Lock()
	isFinished := parentTS.isFinished.Load()
	resultChan := parentTS.pendingResults
	parentTS.mu.Unlock()

	// If parent turn has already finished, treat this as an orphan result
	if isFinished || resultChan == nil {
		if result != nil && al != nil {
			al.emitEvent(EventKindSubTurnOrphan,
				parentTS.eventMeta("deliverSubTurnResult", "subturn.orphan"),
				SubTurnOrphanPayload{ParentTurnID: parentTS.turnID, ChildTurnID: childID, Reason: "parent_finished"},
			)
		}
		return
	}

	// Parent Turn is still running → attempt to deliver result
	// We use a select statement with parentTS.Finished() to ensure that if the
	// parent turn finishes while we are waiting to send the result (e.g. channel
	// is full), we don't leak this goroutine by blocking forever.
	select {
	case resultChan <- result:
		// Successfully delivered
		if al != nil {
			al.emitEvent(EventKindSubTurnResultDelivered,
				parentTS.eventMeta("deliverSubTurnResult", "subturn.result_delivered"),
				SubTurnResultDeliveredPayload{ContentLen: len(result.ForLLM)},
			)
		}
	case <-parentTS.Finished():
		// Parent finished while we were waiting to deliver.
		// The result cannot be delivered to the LLM, so it becomes an orphan.
		logger.WarnCF("subturn", "parent finished before result could be delivered", map[string]any{
			"parent_id": parentTS.turnID,
			"child_id":  childID,
		})
		if result != nil && al != nil {
			al.emitEvent(
				EventKindSubTurnOrphan,
				parentTS.eventMeta("deliverSubTurnResult", "subturn.orphan"),
				SubTurnOrphanPayload{
					ParentTurnID: parentTS.turnID,
					ChildTurnID:  childID,
					Reason:       "parent_finished_waiting",
				},
			)
		}
	}
}

// ====================== Other Types ======================

// ephemeralSessionStore is an in-memory session.SessionStore used by SubTurns.
// It does not persist to disk and auto-truncates history to maxEphemeralHistorySize.
type ephemeralSessionStore struct {
	mu      sync.Mutex
	history []providers.Message
	summary string
}

// newEphemeralSession returns a session.SessionStore backed by an in-memory
// ephemeralSessionStore. It is typed as session.SessionStore directly
// (rather than a separate locally-declared interface) because
// *ephemeralSessionStore's method set already matches session.SessionStore
// exactly (see the "Satisfies session.SessionStore" notes on ReadArchive and
// RollbackAppended below) — a second, parallel interface declaration here
// would just be an unreviewed duplicate of pkg/session's own contract (and
// one that independently tripped the interfacebloat lint at the same 11
// methods; see pkg/session/session_store.go for the SessionReader/
// SessionWriter split of the interface this duplicated).
func newEphemeralSession(initial []providers.Message) session.SessionStore {
	s := &ephemeralSessionStore{}
	if len(initial) > 0 {
		s.history = append(s.history, initial...)
	}
	return s
}

func (e *ephemeralSessionStore) AddMessage(_, role, content string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = append(e.history, providers.Message{Role: role, Content: content})
	e.truncateLocked()
}

func (e *ephemeralSessionStore) AddFullMessage(_ string, msg providers.Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = append(e.history, msg)
	e.truncateLocked()
}

func (e *ephemeralSessionStore) GetHistory(_ string) []providers.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]providers.Message, len(e.history))
	copy(out, e.history)
	return out
}

// ReadArchive returns the ephemeral in-memory history as ArchivedMessage
// values with TS=0. This backend is a bounded in-memory ring (capacity
// maxEphemeralHistorySize): it keeps NO per-line timestamps and NEVER
// evicts turns to disk. Because there is no Skip-based windowing and no
// append-only JSONL archive, ReadArchive == GetHistory — it returns the
// complete in-memory slice; there is no separate line-0 archive that
// recall or breadcrumb logic can dip into for additional evicted turns.
// Satisfies the session.SessionStore interface (FR-016).
func (e *ephemeralSessionStore) ReadArchive(_ context.Context, _ string) ([]memory.ArchivedMessage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]memory.ArchivedMessage, len(e.history))
	for i, m := range e.history {
		out[i] = memory.ArchivedMessage{Message: m}
	}
	return out, nil
}

func (e *ephemeralSessionStore) GetSummary(_ string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.summary
}

func (e *ephemeralSessionStore) SetSummary(_, summary string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.summary = summary
}

func (e *ephemeralSessionStore) SetHistory(_ string, history []providers.Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = make([]providers.Message, len(history))
	copy(e.history, history)
	e.truncateLocked()
}

func (e *ephemeralSessionStore) TruncateHistory(_ string, keepLast int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if keepLast <= 0 {
		e.history = nil
		return
	}

	if keepLast >= len(e.history) {
		return
	}
	e.history = e.history[len(e.history)-keepLast:]
}

func (e *ephemeralSessionStore) Save(_ string) error { return nil }
func (e *ephemeralSessionStore) Close() error        { return nil }

// RollbackAppended truncates the in-memory history to its first
// targetArchiveLen messages, discarding anything appended after that point.
// targetSkip is accepted for interface compatibility but has no effect: the
// ephemeral backend is a bounded in-memory ring with no Skip/archive split —
// there is no eviction cursor to restore.
//
// Note: rollback is best-effort when the ephemeral ring has wrapped (i.e. a
// sub-turn appended >maxEphemeralHistorySize messages and the ring discarded
// the oldest). In that case targetArchiveLen no longer maps to the same
// messages that were at the head before the ring wrapped, so the logical
// pre-turn state cannot be perfectly restored. This is low-probability
// (sub-turns are short) and the ephemeral store has no persistent archive.
//
// Satisfies session.SessionStore (used by hard-abort turn rollback).
func (e *ephemeralSessionStore) RollbackAppended(_ string, targetArchiveLen, _ int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if targetArchiveLen < 0 {
		targetArchiveLen = 0
	}
	if targetArchiveLen < len(e.history) {
		e.history = e.history[:targetArchiveLen]
	}
}

func (e *ephemeralSessionStore) truncateLocked() {
	if len(e.history) > maxEphemeralHistorySize {
		e.history = e.history[len(e.history)-maxEphemeralHistorySize:]
	}
}
