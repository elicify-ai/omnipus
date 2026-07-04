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

	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/memory"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/tools"
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

	// ADR-032 / external-agent identity fix: resolve the actual DELEGATE named
	// by TargetAgentID from the registry. Previously the dispatch-kind
	// decision (native vs external-cli) and — for external-cli — the run's
	// workspace/model/turn-cap/executor config always came from baseAgent
	// (the PARENT doing the delegating), never the target. That meant (a) an
	// external-cli worker could silently run natively whenever the PARENT's
	// own Subagents.Executor was unset/native, and (b) an external-cli run
	// always executed in the PARENT's workspace with the PARENT's model.
	// Resolution is best-effort: an unresolvable target (deleted/renamed
	// since delegation was configured, or self-delegation where
	// TargetAgentID=="") falls back to baseAgent, so a sub-turn is never
	// silently dropped and native dispatch stays byte-for-byte unchanged for
	// that case.
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
			slog.Warn("subturn: target agent not found in registry; dispatch falls back to parent's own executor config",
				"target_agent_id", cfg.TargetAgentID, "parent_id", parentTS.turnID)
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

	// FIX 2 (data race): baseAgent.Model is one of the four fields
	// (Model/Provider/Candidates/ThinkingLevel) protected by baseAgent.mu
	// (see AgentInstance.mu's doc comment, instance.go:27-30) — it can be
	// concurrently written by SwitchModel/ApplyAgentModel (loop.go
	// ~3479-3496, which takes mu.Lock() around the full tuple flip) while a
	// turn referencing this same live registry AgentInstance is in flight.
	// Reading it unlocked would race. A brief RLock-snapshot-RUnlock here
	// mirrors the existing sibling read-sites (loop.go:5249-5255,
	// 5570-5572, 8554-8556) rather than nesting a lock inside the struct
	// literal below. Verified deadlock-safe: runTurn's tool-dispatch loop
	// (loop.go ~6900, which reaches here via the spawn/subagent tools) does
	// NOT hold baseAgent.mu across the ExecuteWithContext call that
	// eventually invokes spawnSubTurn, so this RLock cannot contend with a
	// lock already held by our own call stack.
	baseAgent.mu.RLock()
	baseAgentModel := baseAgent.Model
	baseAgent.mu.RUnlock()

	ephemeralStore := newEphemeralSession(nil)
	// Build a new AgentInstance from baseAgent fields to avoid copying the mutex.
	agent := AgentInstance{
		ID:                        baseAgent.ID,
		Name:                      baseAgent.Name,
		Model:                     baseAgentModel,
		Fallbacks:                 baseAgent.Fallbacks,
		Workspace:                 baseAgent.Workspace,
		MaxIterations:             baseAgent.MaxIterations,
		MaxTokens:                 baseAgent.MaxTokens,
		Temperature:               baseAgent.Temperature,
		ThinkingLevel:             baseAgent.ThinkingLevel,
		ContextWindow:             baseAgent.ContextWindow,
		SummarizeMessageThreshold: baseAgent.SummarizeMessageThreshold,
		SummarizeTokenPercent:     baseAgent.SummarizeTokenPercent,
		Provider:                  baseAgent.Provider,
		Sessions:                  ephemeralStore,
		ContextBuilder:            baseAgent.ContextBuilder,
		Tools:                     baseAgent.Tools,
		Subagents:                 baseAgent.Subagents,
		SkillsFilter:              baseAgent.SkillsFilter,
		Candidates:                baseAgent.Candidates,
		TimeoutSeconds:            baseAgent.TimeoutSeconds,
		Router:                    baseAgent.Router,
		LightCandidates:           baseAgent.LightCandidates,
		LightProvider:             baseAgent.LightProvider,
	}
	// External-cli dispatch runs as a SEPARATE process outside Omnipus's own
	// LLM-call machinery — runExternalCLISubTurn never consults
	// Candidates/Provider/ProviderPool — so it is safe to source the run
	// identity (workspace, model, turn cap, executor config) from the
	// resolved TARGET here. Native dispatch is deliberately left untouched
	// (still 100% baseAgent-sourced) to avoid a Model/Candidates/Provider
	// mismatch in the native LLM-call resolution path, which would be a
	// larger refactor outside this fix's scope.
	if dispatchKind == runner.DispatchKindExternalCLI && targetAgent != nil {
		// FIX 2 (data race): targetAgent is a LIVE registry pointer too — its
		// Model is protected by targetAgent.mu the same as baseAgent.mu above.
		// Snapshot under a brief RLock before assigning.
		targetAgent.mu.RLock()
		targetModel := targetAgent.Model
		targetAgent.mu.RUnlock()

		// FIX 1 (correctness, HIGH — wrong audit attribution): ID/Name must
		// also come from the TARGET, not just Workspace/Model/MaxIterations/
		// Subagents. Without this, childTS.agentID (set from agent.ID by
		// newTurnState, turn.go:252) stays the PARENT's ID even though the
		// run executes with the target's workspace/model — the human
		// tool-approval broadcast (PolicyApprovalReq.AgentID), the transcript
		// AgentID, and the SubTurnSpawn/End WS payloads would all attribute
		// the delegated run to the wrong agent.
		agent.ID = targetAgent.ID
		agent.Name = targetAgent.Name
		agent.Workspace = targetAgent.Workspace
		agent.Model = targetModel
		agent.MaxIterations = targetAgent.MaxIterations
		agent.Subagents = targetAgent.Subagents
	}

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
	// Meta.AgentID); agent.ID (finalized above) is the identity THIS child
	// turn will use for its own tool-approval requests (see
	// newTurnState(&agent, ...) below, which sets childTS.agentID = agent.
	// ID). Keying the inherit call on the same variable the child will
	// actually be looked up under keeps this correct for both native
	// dispatch (agent.ID == baseAgent.ID — a harmless same-key union) and
	// external-CLI dispatch (agent.ID == targetAgent.ID, the real delegate).
	al.ApprovalGrants().Inherit(parentTS.transcriptSessionID, parentTS.agentID, agent.ID)

	// FR-H-006: exclude delegation-adjacent tools from the child's registry so
	// it cannot recursively delegate to a grandchild or hand off. Registry-
	// level filter — the tools are absent, not refused at execute time. One
	// level only (owner decision 2026-04-20).
	//
	// ADR-036 (2026-07-04) merged the former spawn (async delegation) and
	// run_subagent (sync delegation) tools into one `delegate` tool, so what
	// used to be two excluded names (ExcludedSpawn, ExcludedSubagent) is now
	// one (ExcludedDelegate) — the "one level only" invariant itself is
	// UNCHANGED by that merge, just re-expressed against the single tool name:
	//   - delegate: the unified delegation tool (async AND sync modes)
	//   - handoff:  agent switch
	if baseAgent.Tools != nil {
		agent.Tools = baseAgent.Tools.CloneExcept(tools.ExcludedDelegate, tools.ExcludedHandoff)
		// Log the constructed registry so operators can debug "my subagent has no tools" issues.
		slog.Info("subturn: child registry constructed",
			"excluded", []string{"delegate", "hand_off"},
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
			if err != nil {
				endStatus = SubTurnStatusError
			}
			subTurnDurationMS := time.Since(subTurnStartedAt).Milliseconds()
			slog.Debug("subagent_end",
				"span_id", spanID,
				"parent_call_id", parentSpawnCallID,
				"agent_id", childTS.agentID,
			)
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

func newEphemeralSession(initial []providers.Message) ephemeralSessionStoreIface {
	s := &ephemeralSessionStore{}
	if len(initial) > 0 {
		s.history = append(s.history, initial...)
	}
	return s
}

// ephemeralSessionStoreIface is satisfied by *ephemeralSessionStore.
// Declared so newEphemeralSession can return a typed interface.
type ephemeralSessionStoreIface interface {
	AddMessage(sessionKey, role, content string)
	AddFullMessage(sessionKey string, msg providers.Message)
	GetHistory(key string) []providers.Message
	GetSummary(key string) string
	SetSummary(key, summary string)
	SetHistory(key string, history []providers.Message)
	TruncateHistory(key string, keepLast int)
	ReadArchive(ctx context.Context, key string) ([]memory.ArchivedMessage, error)
	RollbackAppended(key string, targetArchiveLen, targetSkip int)
	Save(key string) error
	Close() error
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
