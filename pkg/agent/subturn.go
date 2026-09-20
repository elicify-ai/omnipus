package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
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
	// ErrDelegationTargetUnresolved is returned when SubTurnConfig.TargetAgentID
	// names an agent that is not in the registry — deleted, renamed, or its
	// entities/agents/<id>.json record failed to load (ADR-054 D7/§9). The
	// sub-turn is aborted rather than silently falling back to the parent's
	// own identity/tool policy (see spawnSubTurn's execSource resolution).
	ErrDelegationTargetUnresolved = errors.New("subturn: delegation target agent not found")
	// ErrSessionCancelling is returned by spawnSubTurn when parentTS or any
	// of its ancestors (walked via parentTurnState) has already been marked
	// cancelling (turnState.cancelling, set by markTurnsCancelling —
	// steering.go — the instant Interrupt/InterruptSessionHard resolves it
	// as a cancel target). This is the GATE half of the chain-reaction
	// supersession of ADR-057 FR-024: it closes, by construction, the window
	// where a brand-new delegate spawn is dispatched WHILE its own parent's
	// (or an ancestor's) cancellation is already underway — a child born in
	// that window would otherwise have to be caught after the fact by
	// recursion (the fresh re-scan / pendingSpawn-latch machinery in
	// cancel.go), which can only reach a spawn that has ALREADY registered
	// or is ALREADY known to be imminent, never one that has not been
	// attempted at all. Refusing outright (rather than creating the child
	// and immediately tearing it down) means no session/workspace/transcript
	// state is ever created for a delegation that was never going to be
	// allowed to run.
	ErrSessionCancelling = errors.New("subturn: refused — session is being cancelled")
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
		maxConcurrent, _ = al.cfg.Performance.EffectiveMaxParallelAgents()
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
	}
}

// subTurnRuntimeConfig holds the effective runtime configuration for SubTurn execution.
type subTurnRuntimeConfig struct {
	maxDepth           int
	maxConcurrent      int
	concurrencyTimeout time.Duration
	defaultTimeout     time.Duration
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
//	result, err := spawner.SpawnSubTurn(ctx, cfg) // spawner is a tools.SubTurnSpawner
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
//	result, err := spawner.SpawnSubTurn(ctx, cfg) // spawner is a tools.SubTurnSpawner
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

	// DelegateSessionID, when non-empty, is the ADR-053 durable session_id
	// (S2) the caller (pkg/tools/delegate.go's executeRun) already minted
	// and persisted a `queued` LifecycleRecord under BEFORE calling this
	// spawner — spawnSubTurn reuses it verbatim as childID/sessionKey (see
	// the childID assignment) rather than generating a fresh counter-based
	// one, so a caller-issued session_id is always the child's real
	// steering-queue scope. Empty means "let spawnSubTurn generate one" —
	// the pre-ADR-053 default for any caller that does not set this.
	DelegateSessionID string

	// IsResume marks this dispatch as a WARM RESUME of an existing,
	// store-backed session — native `delegate follow_up`'s own use case —
	// rather than a brand-new session mint. DelegateSessionID in this case
	// names a session that already exists on disk (the terminal session
	// being resumed for its next generation): spawnSubTurn verifies it still
	// exists (GetMeta) instead of calling CreateSessionWithID, which would
	// ALWAYS collide with FR-096's create-path collision guard — a resume is
	// not a create and must never be routed through that primitive. false
	// (the default) is every other caller's existing create-path behavior,
	// unchanged; the ParentSessionID edge is also left untouched on a
	// resume (see spawnSubTurn's own comment at the call site).
	IsResume bool

	// ContextSnapshot carries the DISCRETIONARY portion of the ADR-053 D1
	// curated context snapshot (R§8.5) — parent-named artifact references
	// (not contents) plus optional parent-authored notes. Deny-by-default:
	// nothing beyond this + the MANDATORY core (SystemPrompt/task prompt +
	// ActualSystemPrompt/target identity, both already carried by this
	// struct's existing fields — assembled server-side via execSource per
	// ADR-032, never from the parent's own transcript/credentials/sibling
	// context) reaches the child. nil/zero-value means "no discretionary
	// snapshot" (an empty, valid snapshot). Validate with
	// ValidateContextSnapshot BEFORE spawning — spawnSubTurn does not
	// itself enforce the cap; the caller (pkg/tools/delegate.go's
	// executeRun) MUST call ValidateContextSnapshot and reject the
	// `delegate.run` call on error rather than let an over-cap snapshot
	// through.
	ContextSnapshot *ContextSnapshot

	// RequestedSkill is the ADR-072 D9 "request" mechanism (spec FR-050..056)
	// — the tools-side mirror of tools.SubTurnConfig.RequestedSkill, which
	// AgentLoopSpawner.SpawnSubTurn converts this from 1:1 (the same
	// tools<->agent duplication ContextSnapshot/SubTurnConfig already
	// document). An optional skill slug the parent names; spawnSubTurn
	// resolves it against execSource's (the CHILD's) OWN ContextBuilder —
	// see resolveRequestedSkillForChild — never the parent's, before any
	// dispatch/session work below. Empty means "no requested skill".
	RequestedSkill string

	// Can be extended with temperature, topP, etc.
}

// ContextSnapshot is the DISCRETIONARY portion of the ADR-053 D1 curated
// context snapshot (R§8.5) a parent may attach to a `delegate.run` call.
// The MANDATORY core (task prompt + compiled criteria + engine-injected
// target identity) is assembled server-side and is EXEMPT from
// snapshot_max_bytes (m4) — it is never represented by this type, which
// covers ONLY the parent-named references + optional notes.
type ContextSnapshot struct {
	// References are parent-named artifact path/ref strings (NOT contents)
	// visible to the child. Never the parent's own transcript, credentials,
	// or sibling context (D1 deny-by-default).
	References []string
	// Notes are optional parent-authored free text, counted against
	// snapshotMaxBytes alongside References.
	Notes string
}

// ADR-053 §Contract Surface / R§8.5 defaults for the curated context
// snapshot's discretionary-portion caps. Overridable via
// SubTurnConfig.SnapshotMaxBytes/SnapshotMaxRefs-style config plumbing in a
// later wave (config is outside this wave's write-set) — ValidateContextSnapshot
// accepts explicit overrides so a caller with real config values never has
// to touch these constants.
const (
	defaultSnapshotMaxBytes = 8 * 1024 // 8 KiB, per ADR §Contract Surface
	defaultSnapshotMaxRefs  = 50
)

// ErrSnapshotOverCap is returned by ValidateContextSnapshot when the
// DISCRETIONARY portion (references + notes) exceeds its byte or count cap.
// The MANDATORY core (task prompt + criteria + identity) is NEVER subject
// to this check (m4) — only what this function is handed.
var ErrSnapshotOverCap = errors.New("agent: curated context snapshot exceeds the discretionary cap")

// ValidateContextSnapshot enforces R§8.5's deny-by-default, hard-capped
// curated context snapshot on the DISCRETIONARY portion only
// (snap.References + snap.Notes). maxBytes/maxRefs <= 0 fall back to the
// ADR §Contract Surface defaults (8 KiB / 50 refs). A nil snap is always
// valid (no discretionary content — an empty snapshot never trips the
// cap). Returns a wrapped ErrSnapshotOverCap naming exactly which cap was
// exceeded so the caller can render a "narrow the snapshot" tool error
// (never silently truncate, per R§8.5/FR-124).
func ValidateContextSnapshot(snap *ContextSnapshot, maxBytes, maxRefs int) error {
	if snap == nil {
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = defaultSnapshotMaxBytes
	}
	if maxRefs <= 0 {
		maxRefs = defaultSnapshotMaxRefs
	}
	if len(snap.References) > maxRefs {
		return fmt.Errorf("%w: %d references exceeds snapshot_max_refs (%d) — narrow the snapshot",
			ErrSnapshotOverCap, len(snap.References), maxRefs)
	}
	total := len(snap.Notes)
	for _, ref := range snap.References {
		total += len(ref)
	}
	if total > maxBytes {
		return fmt.Errorf("%w: %d bytes exceeds snapshot_max_bytes (%d) — narrow the snapshot",
			ErrSnapshotOverCap, total, maxBytes)
	}
	return nil
}

// renderContextSnapshot renders the DISCRETIONARY portion of a curated
// context snapshot as plain text woven into the child's task prompt. Empty
// for a nil snapshot or a snapshot with no references/notes — the common
// case, so a call with no snapshot produces byte-for-byte the same task
// text as before this field existed.
func renderContextSnapshot(snap *ContextSnapshot) string {
	if snap == nil || (len(snap.References) == 0 && strings.TrimSpace(snap.Notes) == "") {
		return ""
	}
	var b strings.Builder
	if len(snap.References) > 0 {
		b.WriteString("References:\n")
		for _, ref := range snap.References {
			b.WriteString("- ")
			b.WriteString(ref)
			b.WriteString("\n")
		}
	}
	if strings.TrimSpace(snap.Notes) != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Notes: ")
		b.WriteString(snap.Notes)
	}
	return b.String()
}

// ====================== Context Keys ======================
type agentLoopKeyType struct{}

var agentLoopKey = agentLoopKeyType{}

// WithAgentLoop injects AgentLoop into context for tool access
func WithAgentLoop(ctx context.Context, al *AgentLoop) context.Context {
	return context.WithValue(ctx, agentLoopKey, al)
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
		MaxTokens:          cfg.MaxTokens,
		Async:              cfg.Async,
		Critical:           cfg.Critical,
		Timeout:            cfg.Timeout,
		MaxContextRunes:    cfg.MaxContextRunes,
		TaskLabel:          cfg.TaskLabel,
		ResolvedMaxDepth:   cfg.ResolvedMaxDepth,
		DelegateSessionID:  cfg.DelegateSessionID,
		IsResume:           cfg.IsResume,
		RequestedSkill:     cfg.RequestedSkill,
	}
	if cfg.ContextSnapshot != nil {
		agentCfg.ContextSnapshot = &ContextSnapshot{
			References: cfg.ContextSnapshot.References,
			Notes:      cfg.ContextSnapshot.Notes,
		}
	}

	return spawnSubTurn(ctx, s.al, parentTS, agentCfg)
}

// NewSubTurnSpawner creates a SubTurnSpawner for the given AgentLoop.
func NewSubTurnSpawner(al *AgentLoop) *AgentLoopSpawner {
	return &AgentLoopSpawner{al: al}
}

// MarkPendingDelegateSpawn implements tools.DelegateSpawnMarker. DelegateTool
// calls this (via the interface, wired automatically by SetSpawner's type
// assertion — see delegate.go's SetSpawner) synchronously, on the delegating
// parent's own tool-execution goroutine, immediately before dispatching the
// goroutine that will call SpawnSubTurn — never after. This is what makes it
// safe for spawnSubTurn's own cleanup (below) to assume "a marker was set
// implies a spawn attempt is genuinely in flight for this identity": there is
// no caller that marks without following through.
//
// See pendingSpawns' field doc comment on the cancelPreArm struct
// (cancel_prearm.go) for the full mark/clear/TTL contract this closes —
// turnImminentForIdentity's only OTHER evidence source (al.sessionWorkers) is
// structurally blind to a delegate sub-turn, which is dispatched straight to
// al.runTurn from a bare goroutine and never touches the inbound-message
// dispatch loop that populates sessionWorkers.
func (s *AgentLoopSpawner) MarkPendingDelegateSpawn(sessionID, channel, chatID string) {
	if s == nil || s.al == nil || s.al.cancelPreArm == nil {
		return
	}
	keys := pendingSpawnKeys(sessionID, channel, chatID)
	if len(keys) == 0 {
		return
	}
	s.al.cancelPreArm.markPendingSpawn(time.Now(), keys...)
}

// requestedSkillOutcome is the closed set of outcomes
// resolveRequestedSkillForChild can report for a `delegate.run`
// requested_skill slug (ADR-072 D9, spec FR-050..056).
type requestedSkillOutcome int

const (
	// requestedSkillUnresolvable is the zero value deliberately, mirroring
	// pkg/tools/skill.go's SkillLoadNotFound: an unwired or misbehaving
	// ContextBuilder fails toward "nothing exists", never toward "granted"
	// or "installed-but-denied" — either of which would leak information a
	// missing/nil builder cannot actually back up.
	requestedSkillUnresolvable requestedSkillOutcome = iota
	// requestedSkillDenied: the slug exists on some shelf visible to the
	// child, but the child is not granted it.
	requestedSkillDenied
	// requestedSkillGranted: the child may load the slug — the resolved
	// canonical slug is returned alongside this outcome.
	requestedSkillGranted
)

// spawnSubTurnState carries the shared state of spawnSubTurn across its stages.
type spawnSubTurnState struct {
	al                          *AgentLoop
	parentTS                    *turnState
	cfg                         SubTurnConfig
	pendingSpawnKeysForThisCall []string
	registeredForCancel         bool
	rtCfg                       subTurnRuntimeConfig
	childCtx                    context.Context
	cancel                      context.CancelFunc
	childID                     string
	parentSpawnCallID           string
	emitSpanEvents              bool
	spanID                      string
	execSource                  *AgentInstance
	canonicalRequestedSkill     string
	ephemeralStore              session.SessionStore
	agent                       AgentInstance
	sharedStore                 *session.UnifiedStore
	opts                        processOptions
	childTS                     *turnState
}

// spawnSubTurnSetupState carries the shared state of spawnSubTurn across its stages.
type spawnSubTurnSetupState struct {
	ctx              context.Context
	st               *spawnSubTurnState
	timeout          time.Duration
	forceCancelAt    time.Time
	subTurnStartedAt time.Time
	dispatchKind     runner.DispatchKind
	dispatchErr      error
}

// spawnSubTurnExecutionState carries the shared state of spawnSubTurn across its stages.
type spawnSubTurnExecutionState struct {
	ss                *spawnSubTurnSetupState
	semAcquired       bool
	lastTurnStatus    TurnEndStatus
	forceCancelFired  bool
	detachedOnTimeout bool
	detachedPhysical  bool
	forceCancel       *subTurnForceCancel
	turnRes           turnResult
	turnErr           error
}

func spawnSubTurn(
	ctx context.Context,
	al *AgentLoop,
	parentTS *turnState,
	cfg SubTurnConfig,
) (result *tools.ToolResult, err error) {
	ex := &spawnSubTurnExecutionState{}

	ex.ss = &spawnSubTurnSetupState{ctx: ctx}

	ex.ss.st = &spawnSubTurnState{al: al, parentTS: parentTS, cfg: cfg}

	// Delegate-spawn pending-marker cleanup (cancel_prearm.go's pendingSpawns
	// / DelegateSpawnMarker seam): pkg/tools/delegate.go's executeAsync may
	// have called MarkPendingDelegateSpawn for parentTS's own identity
	// (routingSessionID, channel, chatID — inherited verbatim by the
	// child below, so the SAME keys apply regardless of whether this call
	// ever reaches child construction) immediately before dispatching the
	// goroutine that reached this function. That marker's ONLY job is to
	// make turnImminentForIdentity report "imminent" for the brief window
	// before the child registers below; once this function exits — whether
	// by registering the child (the common case) or by returning early
	// (depth limit, concurrency timeout, invalid config, unresolved
	// delegation target, or a panic) — the marker must not outlive it, or
	// it would make turnImminentForIdentity report true forever for an
	// identity with no turn actually coming, reopening the
	// TestPreArmedCancel_FinishedSession_DoesNotArmOrPoisonNextTurn hazard
	// from the delegate-spawn side instead of the message-dispatch side.
	//
	// registeredForCancel flips true the instant the child is registered
	// (al.registerActiveTurn below) — at that point the child's own
	// turnState is real, discoverable evidence
	// (GetActiveTurnHookForSession/sessionTurnsStillAlive) and the marker is
	// cleared explicitly, right there, rather than left for this defer.
	// This defer is the catch-all for every OTHER exit — every early
	// `return` between here and that point, and a panic anywhere in
	// between (this defer is registered first, so it is still live — Go
	// defers registered before a panic still run during that panic's
	// unwind — regardless of whether a LATER defer, e.g. the panic-recovery
	// one further down, ever got registered at all).
	// ADR-057 FR-016 (W4 subturn half): this is one of the three DIRECT reads
	// re-based onto routingSessionID (the other two are cancel_prearm.go:354,
	// :355, U15's file, this same wave) — routingSessionID is inherited
	// verbatim by the child a few lines below (childTS.routingSessionID =
	// parentTS.routingSessionID), so the SAME keys this call computes are the
	// ones a subsequently-registered child turn would be checked against
	// on the pre-arm-latch side. Was parentTS.transcriptSessionID, which
	// under D1 is now the PARENT's own real session id (potentially a
	// delegated child's own id for a nested spawn) rather than the routing
	// identity a chat-wide Stop click resolves against.
	ex.ss.st.pendingSpawnKeysForThisCall = pendingSpawnKeys(string(ex.ss.st.parentTS.routingSessionID), ex.ss.st.parentTS.channel, ex.ss.st.parentTS.chatID) // u19:pre-arm
	ex.ss.st.registeredForCancel = false
	if ex.ss.st.al.cancelPreArm != nil && len(ex.ss.st.pendingSpawnKeysForThisCall) > 0 {
		defer func() {
			if !ex.ss.st.registeredForCancel {
				ex.ss.st.al.cancelPreArm.clearPendingSpawn(ex.ss.st.pendingSpawnKeysForThisCall...)
			}
		}()
	}

	if r0, stop, r1 := ex.ss.rejectCancellingAncestor(); stop {
		return r0, r1
	}

	// 0. Acquire concurrency semaphore FIRST to ensure it's released even if early validation fails.
	// Blocks if parent already has maxConcurrentSubTurns running, with a timeout to prevent indefinite blocking.
	// Also respects context cancellation so we don't block forever if parent is aborted.
	// Native execution transfers the slot to the physical runTurn goroutine;
	// its defer releases on actual exit, before spawnSubTurn's result-delivery
	// cleanup can block. Early failures and external dispatch release explicitly.

	if ex.ss.st.parentTS.concurrencySem != nil {
		// Create a timeout context for semaphore acquisition.
		//
		// Critical sub-turns (async/background delegation via DelegateTool's
		// executeAsync, which always sets Critical:true) are designed to
		// outlive the parent turn — the child runs on an INDEPENDENT
		// context.Background()-derived childCtx built further below (line
		// ~473), and runTurn receives that childCtx, never the parent ctx.
		// Deriving the acquire timeout from ctx in that case races the parent
		// turn's own completion: a background delegation's goroutine is
		// frequently scheduled only after the parent turn has already ended
		// and canceled its ctx, so the acquire's select would pick the
		// already-done timeout channel and abort the spawn with ctx.Err() —
		// silently dropping a delegation that was supposed to keep running.
		// Base the timeout on context.Background() for Critical spawns so only
		// a genuine concurrencyTimeout exhaustion bounds the acquire.
		semTimeoutBase := ex.ss.ctx
		if ex.ss.st.cfg.Critical {
			semTimeoutBase = context.Background()
		}
		timeoutCtx, cancel := context.WithTimeout(semTimeoutBase, ex.ss.st.rtCfg.concurrencyTimeout)
		defer cancel()

		select {
		case ex.ss.st.parentTS.concurrencySem <- struct{}{}:
			ex.semAcquired = true
			defer func() {
				if ex.semAcquired {
					<-ex.ss.st.parentTS.concurrencySem
				}
			}()
		case <-timeoutCtx.Done():
			// A Critical acquire can no longer be aborted by parent
			// cancellation (its base is context.Background()), so a done
			// timeout here is a genuine concurrencyTimeout exhaustion. Only
			// the non-Critical path can still surface a parent cancellation.
			if !ex.ss.st.cfg.Critical && ex.ss.ctx.Err() != nil {
				return nil, ex.ss.ctx.Err()
			}
			// Otherwise it's our timeout
			return nil, fmt.Errorf("%w: all %d slots occupied for %v",
				ErrConcurrencyTimeout, ex.ss.st.rtCfg.maxConcurrent, ex.ss.st.rtCfg.concurrencyTimeout)
		}
	}

	if r0, stop, r1 := ex.ss.validateAndCreateContext(); stop {
		return r0, r1
	}
	defer ex.ss.st.cancel()

	if r0, stop, r1 := ex.ss.resolveDelegateIdentity(); stop {
		return r0, r1
	}

	ex.ss.st.buildDelegateAgent()

	if r0, stop, r1 := ex.ss.createChildSession(); stop {
		return r0, r1
	}

	ex.ss.st.configureChildTurn()
	// CompareAndDelete via clearActiveTurnStateEntry, not a bare Delete. A
	// native `follow_up` warm-resume reuses childID VERBATIM for its next
	// generation once this generation's LifecycleRecord reaches a terminal
	// state (see spawnCorrectiveFollowUp's doc comment, pkg/tools/delegate.go)
	// — and this generation's own tail (the re-Store below plus the cleanup
	// defer's up-to-~935ms updateToolCallStatusWithRetry backoff) can still be
	// in flight when that happens. If follow_up's registerActiveTurn(childTS2)
	// lands in that window, a bare `Delete(childID)` here would unconditionally
	// erase whatever is CURRENTLY stored under childID when this defer finally
	// fires — which by then is the NEW generation's live, running turnState,
	// not this (finished) one. That silently makes the new generation
	// unreachable to GetActiveTurnHookForSession/Interrupt/
	// sessionTurnsStillAlive for the rest of its life: no cancel (graceful,
	// hard, or detach) can ever find it again, and it runs unchecked until its
	// own MaxIterations ceiling. clearActiveTurnStateEntry only removes the
	// entry if it is STILL this exact childTS, so a since-registered newer
	// generation is left untouched — the same compare-and-delete-by-identity
	// pattern clearActiveTurn uses for the parent's own ts.sessionKey.
	defer ex.clearActiveTurnAfterCoordinator()

	ex.ss.st.publishChildSpawn()

	// lastTurnStatus mirrors the effective outcome selected by
	// executeNativeChildTurn — the real runTurn outcome, or a synthesized
	// Aborted outcome when a cancellation-ignoring child is detached. The
	// cleanup defer below cannot read that later-selected outcome directly.
	// M4 (2026-08-04, UAT): pkg/agent/loop.go's
	// abortTurn Case 1 (a tool-call-time hard interrupt/cancel) deliberately
	// returns turnResult{status: TurnEndStatusAborted} with a NIL error (see
	// abortTurn's own doc comment: a clean, user-initiated stop, not a
	// failure) — so the endStatus switch below, which used to branch solely
	// on `err != nil`, fell through every case for that nil-error abort and
	// reported endStatus=Success for a genuinely killed child (live UAT:
	// chat-wide Stop killing a child blocked in a bash tool call reported
	// success). Declaring this here, in the same top-level scope as the
	// defer literal below, is what makes it a valid closure upvalue.

	// forceCancelFired is true when THIS sub-turn was stopped by its own time
	// limit (armSubTurnForceCancel, UAT A-17) rather than by completing, failing
	// on its own, or being cancelled by a user/parent. Declared here, beside
	// lastTurnStatus and for the same reason (a closure upvalue the cleanup
	// defer below must be able to read). executeNativeChildTurn assigns it
	// only after either disarming the timer or observing the timer's completed
	// callback/detach path, so cleanup always sees a final answer.

	// 7. Defer cleanup: deliver result (for async), emit End event, and recover from panics
	defer func() {
		if r := recover(); r != nil {
			// Include childID in the error message so support can correlate
			// a user-visible "subturn panicked" back to the logged stack trace.
			err = fmt.Errorf("subturn %q panicked: %v", ex.ss.st.childID, r)
			result = nil
			slog.Error("subturn: panic recovered",
				"child_id", ex.ss.st.childID,
				"parent_id", ex.ss.st.parentTS.turnID,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(debug.Stack()),
			)
		}

		// Result Delivery Strategy (Async vs Sync)
		if ex.ss.st.cfg.Async {
			deliverSubTurnResult(ex.ss.st.al, ex.ss.st.parentTS, ex.ss.st.childID, result)
		}

		// W1-12: only emit span end event when parentSpawnCallID was non-empty.
		if ex.ss.st.emitSpanEvents {
			endStatus, endReason := ex.classifySubTurnEnd(err)

			// Finding F (A-I4 round 5): mirror endStatus onto the returned/
			// delivered result's Interrupted flag so BOTH delivery paths agree
			// with the exact terminal status the live subagent_end frame
			// (endStatus, just computed above) reports:
			//   - Synchronous delegation: `result` here IS the value
			//     spawnSubTurn returns to its caller (DelegateTool.executeSync,
			//     pkg/tools/delegate.go), which pkg/agent/loop.go's tool-call-
			//     transcript persistence (tcStatus derivation) reads to decide
			//     what status a session reload will show for this span.
			//   - Asynchronous delegation: `result` is delivered via
			//     deliverSubTurnResult below; its own tc.Status correction
			//     (updateToolCallStatusWithRetry, right below) already writes
			//     endStatus onto the persisted record directly, so this is
			//     redundant-but-harmless there — asyncCallback (loop.go) never
			//     reads result.Interrupted.
			// Without this, a canceled SYNCHRONOUS delegate's result only ever
			// carried a generic non-nil err — indistinguishable, once folded
			// into IsError=true/tcStatus="error", from a genuine failure — so
			// reload showed "failed" for the very same span live correctly
			// labeled "interrupted (parent canceled)".
			if result != nil && (endStatus == SubTurnStatusInterrupted || endStatus == SubTurnStatusCancelled) {
				result.Interrupted = true
			}

			subTurnDurationMS := time.Since(ex.ss.subTurnStartedAt).Milliseconds()
			slog.Debug("subagent_end",
				"span_id", ex.ss.st.spanID,
				"parent_call_id", ex.ss.st.parentSpawnCallID,
				"agent_id", ex.ss.st.childTS.agentID,
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
			//
			// W4: alongside status/duration, also persist the sub-turn's own
			// OUTPUT onto the same record — result.ForLLM on success (which
			// already carries a "SubTurn failed: <err>" / "SubTurn dispatch
			// rejected: <err>" message on the error paths above, since those
			// set result.ForLLM to the error text directly), falling back to
			// err.Error() for the rare case result is nil (e.g. the panic-
			// recover branch above). Without this, the persisted delegate
			// tool_call carried a terminal status but an empty `result` even
			// when the sub-turn completed successfully — reload showed no
			// trace of what the delegate actually produced, unlike the live
			// WS stream (SubTurnEndPayload).
			var toolCallResult map[string]any
			switch {
			case result != nil && result.ForLLM != "":
				toolCallResult = map[string]any{"text": result.ForLLM}
			case err != nil:
				toolCallResult = map[string]any{"text": err.Error()}
			}
			// Flag the persisted result as an error whenever the sub-turn
			// failed — keyed off err != nil OR result.IsError. Some failure
			// paths (e.g. the dispatch-reject at ~L1149) set result.Err/ForLLM
			// but leave IsError false, so keying off IsError alone would persist
			// a failed delegate as if it had completed cleanly. Always carry
			// explanatory text alongside the error flag.
			if err != nil || (result != nil && result.IsError) {
				if toolCallResult == nil {
					toolCallResult = map[string]any{"text": "sub-turn reported an error"}
				}
				toolCallResult["error"] = true
			}
			if ex.ss.st.parentTS.transcriptStore != nil && ex.ss.st.parentTS.transcriptSessionID != "" {
				found, updateErr := updateToolCallStatusWithRetry(
					ex.ss.st.parentTS.transcriptStore,
					ex.ss.st.parentTS.transcriptSessionID,
					session.ToolCallID(ex.ss.st.parentSpawnCallID),
					string(endStatus),
					subTurnDurationMS,
					ex.ss.st.cfg.Async,
					toolCallResult,
				)
				switch {
				case updateErr != nil:
					slog.Warn("subturn: failed to persist real end status/duration onto spawn tool call",
						"session_id", ex.ss.st.parentTS.transcriptSessionID,
						"parent_spawn_call_id", ex.ss.st.parentSpawnCallID,
						"error", updateErr,
					)
				case ex.ss.st.cfg.Async && !found:
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
						"session_id", ex.ss.st.parentTS.transcriptSessionID,
						"parent_spawn_call_id", ex.ss.st.parentSpawnCallID,
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
			ex.ss.st.childTS.subTurnRecordPersisted.Store(true)

			ex.ss.st.al.emitEvent(EventKindSubTurnEnd,
				ex.ss.st.childTS.eventMeta("spawnSubTurn", "subturn.end"),
				SubTurnEndPayload{
					AgentID:           ex.ss.st.childTS.agentID,
					Status:            endStatus,
					SpanID:            ex.ss.st.spanID,
					ParentSpawnCallID: session.ToolCallID(ex.ss.st.parentSpawnCallID),
					DurationMS:        subTurnDurationMS,
					ChatID:            ex.ss.st.parentTS.chatID,
					// ADR-057 FR-017/W21c: pinned to the PARENT's
					// routingSessionID (SubTurnEndPayload.SessionID's own
					// doc comment, U23, events.go) — was
					// parentTS.transcriptSessionID before U3's role split
					// landed. Do NOT repoint to the child's own id.
					SessionID: string(ex.ss.st.parentTS.routingSessionID), // u19:ws-stamping
					Reason:    endReason,
				},
			)
			// The end event is now queued for every subscriber (EventBus.Emit
			// delivers on this goroutine, with a bounded blocking retry for this
			// must-not-drop kind), so only now may the span stop counting as
			// active — see markSubTurnSpanOpen (steering.go).
			ex.ss.st.al.markSubTurnSpanEnded(ex.ss.st.parentSpawnCallID)
		}

		// ADR-057 FR-033/W10d (US-6 AS-4): the child-turn-terminal CloseSession
		// call site — verified absent from the tree before this change (the
		// only non-test callers were websocket.go:1038 "explicit",
		// loop.go:1048/:1064 "idle", session_end.go:865 "bootstrap"). Runs
		// unconditionally, regardless of cfg.Async/emitSpanEvents/panic, since
		// it is bounded per-session store cleanup (grant set, loadedTools
		// bucket, metaCache entry — U17b's session_end.go) tied to the
		// CHILD's own session lifetime, not to span/event emission. Without
		// this call, a delegated child's inherited grants (FR-031 above)
		// never expire when the child ends, and its metaCache entry leaks for
		// the process lifetime of every ever-delegated child.
		ex.closeChildSessionAfterCoordinator()
	}()

	// 8. Execute the sub-turn. The executor on the resolved DELEGATE's config
	//    decides HOW: native → the Omnipus agent loop (default, unchanged);
	//    external-cli → an external CLI runner driven directly in the
	//    delegate's own workspace directory (ADR-032 relaxes the original
	//    Spec-4 FR-5.3 worktree isolation for external CLIs). remote-a2a
	//    (reserved) and unknown kinds fail the sub-turn cleanly.
	//    dispatchKind/dispatchErr were already resolved above (from the
	//    correct DELEGATE identity, before the AgentInstance was built).
	if ex.ss.dispatchErr != nil {
		err = ex.ss.dispatchErr
		result = &tools.ToolResult{
			Err:    ex.ss.dispatchErr,
			ForLLM: fmt.Sprintf("SubTurn dispatch rejected: %v", ex.ss.dispatchErr),
		}
		// Release the semaphore before returning (mirrors the post-runTurn release).
		if ex.semAcquired {
			<-ex.ss.st.parentTS.concurrencySem
			ex.semAcquired = false
		}
		return result, err
	}

	// UAT A-17: arm the time limit as a force-cancel for BOTH dispatch kinds.
	// See forceCancelAt's comment (step 4) and armSubTurnForceCancel's doc
	// comment for why a context deadline alone does not stop a child.
	ex.forceCancel = armSubTurnForceCancel(ex.ss.st.childTS, time.Until(ex.ss.forceCancelAt))

	if ex.ss.dispatchKind == runner.DispatchKindExternalCLI {
		// External-cli dispatch: compose the same (soul, task) pair the
		// native path uses. An empty soul yields task-only input (a
		// soul-less custom agent — a seeded worker's compiled prompt is
		// non-empty as of the RC-6 fix — gets no persona text if there is
		// none). The composed string is what the external CLI sees as its
		// prompt, mirroring the native system+user split.
		externalInput := composeDelegateInput(ex.ss.st.al, ex.ss.st.cfg.SystemPrompt, ex.ss.st.cfg.ActualSystemPrompt, ex.ss.st.cfg.TargetAgentID)
		extResult, extErr := runExternalCLISubTurn(ex.ss.st.childCtx, ex.ss.st.al, ex.ss.st.childTS, externalInput, ex.ss.timeout)
		// runExternalCLISubTurn registers one cancel func as both
		// turnCancel and providerCancel, and every driver binds the OS child
		// to it, so the force-cancel's requestHardAbort kills the CLI process.
		// A run that nonetheless SUCCEEDED as the timer fired keeps its result.
		ex.forceCancelFired = ex.forceCancel.disarm() && extErr != nil
		if ex.semAcquired {
			<-ex.ss.st.parentTS.concurrencySem
			ex.semAcquired = false
		}
		result = extResult
		err = extErr
		if ex.forceCancelFired {
			result, err = subTurnTimedOutResult(ex.ss.st.al, ex.ss.st.childTS, ex.ss.timeout, extErr, false)
		}
		return result, err
	}

	ex.executeNativeChildTurn()

	// Convert turnResult to tools.ToolResult
	if ex.forceCancelFired {
		result, err = subTurnTimedOutResult(
			ex.ss.st.al, ex.ss.st.childTS, ex.ss.timeout, ex.turnErr, ex.detachedOnTimeout,
		)
		return result, err
	}
	if ex.turnErr != nil {
		err = ex.turnErr
		// IsError is set explicitly (rather than left at its zero value, as
		// before) so this result is self-describing regardless of which
		// caller inspects it — see the cleanup defer above, which may
		// additionally mark this same result Interrupted for a
		// parent-cancellation case; IsError stays true either way, matching
		// the OUTER tool_call_result frame's existing (unchanged by that
		// fix) always-"error"-on-any-non-nil-err behavior for a synchronous
		// delegate call.
		result = &tools.ToolResult{
			Err:     ex.turnErr,
			ForLLM:  fmt.Sprintf("SubTurn failed: %v", ex.turnErr),
			IsError: true,
		}
	} else {
		result = &tools.ToolResult{
			ForLLM:  ex.turnRes.finalContent,
			ForUser: ex.turnRes.finalContent,
		}
		// C2 (2026-08-04): surface the park onto the ToolResult the caller
		// (pkg/tools/delegate.go's executeSync/executeAsync, for the
		// synchronous and asynchronous delegation paths respectively) sees.
		// KNOWN GAP, reported rather than fixed here (outside this file's
		// scope): as of this change, neither executeSync's nor executeAsync's
		// own post-dispatch switch (delegate.go, ~L2026 and ~L1837) checks
		// this field yet — both still fall through to their `default` case
		// and unconditionally call transitionLifecycle(..., LifecycleCompleted),
		// overwriting the needs_input state message_parent.go's parkNeedsInput
		// (and this fix) correctly left in place. A parked child dispatched
		// via the real `delegate` tool therefore still reproduces the "session
		// ... is not parked" respond() failure today, from a DIFFERENT cause
		// than the one this file's fix closes (the turn loop no longer keeps
		// running past the park, but delegate.go's own bookkeeping still
		// stomps the record afterward). The exact fix: add a
		// `case result.ParksTurn:` branch (checked before `default`) in both
		// switches that skips transitionLifecycle entirely — message_parent.go
		// already correctly parked the record; it must not be touched again.
		if ex.turnRes.status == TurnEndStatusParked {
			result.ParksTurn = true
		}
	}

	return result, err
}

func (ex *spawnSubTurnExecutionState) clearActiveTurnAfterCoordinator() {
	if ex.detachedPhysical {
		return
	}
	ex.ss.st.al.clearActiveTurnStateEntry(ex.ss.st.childID, ex.ss.st.childTS)
}

func (ex *spawnSubTurnExecutionState) closeChildSessionAfterCoordinator() {
	if ex.detachedPhysical {
		return
	}
	ex.ss.st.al.CloseSession(ex.ss.st.childID, "delegate_terminal")
}

// classifySubTurnEnd keeps provider failures distinct from cancellation.
// childCtx cannot be used after runTurn returns because Finish cancels it on
// every exit, including ordinary errors such as an exhausted provider 429.
func (ex *spawnSubTurnExecutionState) classifySubTurnEnd(err error) (SubTurnStatus, string) {
	parentCancelled := ex.ss.st.parentTS.cancelFired.Load() ||
		ex.ss.st.parentTS.hardAbortRequested() || ex.ss.st.parentTS.finishedByHardAbort.Load()
	turnCancelled := errors.Is(ex.turnErr, context.Canceled) ||
		ex.lastTurnStatus == TurnEndStatusAborted
	interruptReason := func() string {
		if ex.ss.st.parentTS.cancelFired.Load() {
			return "parent_cancelled" //nolint:misspell // wire value, frontend TS union
		}
		return "unknown"
	}

	switch {
	case err != nil && ex.forceCancelFired:
		// Delegation-owned timeouts are failures on the subturn wire, not
		// user cancellations, including the detached timeout variant.
		return SubTurnStatusError, ""
	case err != nil && turnCancelled && ex.ss.st.childTS.cancelFired.Load():
		return SubTurnStatusCancelled, ""
	case err != nil && parentCancelled:
		return SubTurnStatusInterrupted, interruptReason()
	case err != nil && turnCancelled:
		// A direct hard abort may bypass ClaimCancel and leave cancelFired
		// false; the actual aborted result is still a child cancellation.
		return SubTurnStatusCancelled, ""
	case err != nil:
		return SubTurnStatusError, ""
	case ex.lastTurnStatus == TurnEndStatusAborted && parentCancelled:
		return SubTurnStatusInterrupted, interruptReason()
	case ex.lastTurnStatus == TurnEndStatusAborted:
		return SubTurnStatusCancelled, ""
	case ex.lastTurnStatus == TurnEndStatusParked:
		return SubTurnStatusParked, ""
	default:
		return SubTurnStatusSuccess, ""
	}
}

// executeNativeChildTurn executes native dispatch, records its status, and releases the concurrency slot.
func (ex *spawnSubTurnExecutionState) executeNativeChildTurn() {
	// Native tool execution is cooperative: most tools return when their
	// context is cancelled, but a faulty or third-party tool can ignore it.
	// Run the turn behind a result channel so the delegation time limit can
	// detach such a goroutine after the same grace used by user cancellation.
	type nativeTurnOutcome struct {
		result     turnResult
		err        error
		panicValue any
	}
	outcomeCh := make(chan nativeTurnOutcome, 1)
	// Once native execution starts, the concurrency slot belongs to the
	// physical child goroutine, not the coordinator waiting for its result.
	// A detached cancellation-ignoring tool is still consuming resources and
	// must keep its slot until runTurn actually exits; otherwise repeated stuck
	// children can bypass MaxConcurrent by timing out one after another.
	childOwnsSemaphore := ex.semAcquired
	if childOwnsSemaphore {
		ex.semAcquired = false
	}
	rootLease := rootDelegationLeaseFromContext(ex.ss.ctx)
	childOwnsRootLease := rootLease.transferToPhysicalChild()
	go func() {
		outcome := nativeTurnOutcome{}
		defer func() {
			if childOwnsSemaphore {
				<-ex.ss.st.parentTS.concurrencySem
			}
			if childOwnsRootLease {
				rootLease.releaseSlot()
			}
			if recovered := recover(); recovered != nil {
				outcome.panicValue = recovered
				stack := debug.Stack()
				slog.Error("subturn: child goroutine panic captured",
					"child_id", ex.ss.st.childID,
					"parent_id", ex.ss.st.parentTS.turnID,
					"panic", fmt.Sprintf("%v", recovered),
					"stack", string(stack),
				)
			}
			outcomeCh <- outcome
		}()
		outcome.result, outcome.err = ex.ss.st.al.runTurn(ex.ss.st.childCtx, ex.ss.st.childTS)
	}()
	acceptOutcome := func(outcome nativeTurnOutcome) {
		if outcome.panicValue != nil {
			// Re-panic on spawnSubTurn's goroutine so its established recovery
			// path still owns async delivery, lifecycle cleanup, and span-end
			// emission. A panic after detachment remains contained above.
			panic(outcome.panicValue)
		}
		ex.turnRes, ex.turnErr = outcome.result, outcome.err
	}
	startDetachedReaper := func() {
		ex.detachedPhysical = true
		go func() {
			// runTurn owns the active-turn entry until it physically exits.
			// Drain its eventual outcome here so session-scoped cleanup follows
			// that real lifetime instead of the coordinator's logical detach.
			<-outcomeCh
			ex.ss.st.al.CloseSession(ex.ss.st.childID, "delegate_terminal")
		}()
	}
	detachAfterIgnoredAbort := func(timeoutOwned bool) {
		ex.ss.st.childTS.MarkAbandoned()
		ex.turnRes = turnResult{status: TurnEndStatusAborted}
		if timeoutOwned {
			ex.turnErr = context.DeadlineExceeded
			ex.forceCancelFired = true
			ex.detachedOnTimeout = true
		} else {
			ex.turnErr = context.Canceled
		}
		startDetachedReaper()
	}

	detachDelay := cancelDetachDelay
	select {
	case outcome := <-outcomeCh:
		acceptOutcome(outcome)
		// UAT A-17: settle the force-cancel before ANYTHING reads this
		// outcome. A child that completed or parked as the timer fired keeps
		// its real result.
		ex.forceCancelFired = ex.forceCancel.disarm() &&
			(ex.turnErr != nil || ex.turnRes.status == TurnEndStatusAborted)
	case <-ex.forceCancel.done:
		timeoutOwned := ex.forceCancel.fired.Load()
		timer := time.NewTimer(detachDelay)
		select {
		case outcome := <-outcomeCh:
			if !timer.Stop() {
				<-timer.C
			}
			acceptOutcome(outcome)
			if timeoutOwned {
				ex.forceCancelFired = outcome.err != nil || outcome.result.status == TurnEndStatusAborted
			}
		case <-timer.C:
			// The hard abort did not make the turn return. Mark it abandoned so
			// any eventual zombie writes are suppressed. The timeout reporter has
			// one controller-owned terminal-write path that remains available
			// after abandonment, so replay still records why the child stopped.
			detachAfterIgnoredAbort(timeoutOwned)
			slog.Error("subturn: hard-aborted child ignored cancellation — detached after grace",
				"child_id", ex.ss.st.childID,
				"agent_id", ex.ss.st.childTS.agentID,
				"time_limit", ex.ss.timeout,
				"detach_grace", detachDelay,
				"time_limit_owned_abort", timeoutOwned,
			)
		}
	}
	// M4/C2 (2026-08-04): mirror the real terminal status into the
	// pre-declared upvalue the cleanup defer above reads — see
	// lastTurnStatus's own doc comment for why a direct reference from that
	// closure is not possible.
	ex.lastTurnStatus = ex.turnRes.status

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
	if !ex.detachedPhysical {
		ex.ss.st.al.activeTurnStates.Store(ex.ss.st.childID, ex.ss.st.childTS)
	}
}

// rejectCancellingAncestor rejects spawning beneath a cancelling turn and loads the runtime configuration.
func (ss *spawnSubTurnSetupState) rejectCancellingAncestor() (*tools.ToolResult, bool, error) {
	// -0.5. Cancellation gate (chain-reaction supersession of ADR-057
	// FR-024 — the GATE half, see turnState.cancelling's doc comment, turn.go,
	// and ErrSessionCancelling's doc comment, above, for the full rationale).
	// Walk parentTS's own ancestor chain via parentTurnState, checking
	// whether parentTS itself or ANY ancestor has already been marked
	// cancelling by markTurnsCancelling (steering.go, called from
	// Interrupt/InterruptSessionHard the instant either resolves that turn
	// as a cancel target). A hit means a Stop (or a `delegate action=cancel`)
	// targeting this turn or an ancestor of it is already underway — refuse
	// the spawn outright rather than create a child that would immediately
	// need to be torn down, or that recursion would have to notice and chase
	// down after the fact. Checked BEFORE the concurrency semaphore
	// acquisition below so a doomed spawn never occupies a slot at all.
	//
	// This walk terminates: parentTurnState is nil for a root turn (turn.go's
	// own field doc comment), and every child's parentTurnState is set to a
	// SPECIFIC, already-constructed parent turnState at spawn time (below,
	// `childTS.parentTurnState = parentTS`) — never to itself or to a turn
	// constructed later — so the chain is a strictly finite, acyclic list
	// bounded by the actual (already depth-limited) delegation tree, not
	// something this check could loop on by itself.
	for p := ss.st.parentTS; p != nil; p = p.parentTurnState {
		if p.cancelling.Load() {
			logger.WarnCF("subturn", "Refusing to spawn — parent or an ancestor is already being cancelled", map[string]any{
				"parent_turn_id":     ss.st.parentTS.turnID,
				"cancelling_turn_id": p.turnID,
			})
			return nil, true, ErrSessionCancelling
		}
	}

	// Get effective SubTurn configuration
	ss.st.rtCfg = ss.st.al.getSubTurnConfig()
	return nil, false, nil
}

// validateAndCreateContext validates the request and creates the independent child context.
func (ss *spawnSubTurnSetupState) validateAndCreateContext() (*tools.ToolResult, bool, error) {
	// 1. Depth limit check. cfg.ResolvedMaxDepth, when set, is the effective
	// cap the delegation-graph gate (enforceEdgeModeAndDepth) already
	// authorized THIS specific call against — it takes precedence over
	// rtCfg.maxDepth's own global-only default so an explicit per-edge Depth
	// is never silently overridden by this backstop (#477, FR-D9/FR-D10).
	effectiveMaxDepth := ss.st.rtCfg.maxDepth
	if ss.st.cfg.ResolvedMaxDepth != nil {
		effectiveMaxDepth = *ss.st.cfg.ResolvedMaxDepth
	}
	if ss.st.parentTS.depth >= effectiveMaxDepth {
		logger.WarnCF("subturn", "Depth limit exceeded", map[string]any{
			"parent_id": ss.st.parentTS.turnID,
			"depth":     ss.st.parentTS.depth,
			"max_depth": effectiveMaxDepth,
		})
		return nil, true, ErrDepthLimitExceeded
	}

	// 2. Config validation
	if ss.st.cfg.Model == "" {
		return nil, true, ErrInvalidSubTurnConfig
	}

	// 2b. ADR-053 D1/R§8.5: weave the curated context snapshot's
	// DISCRETIONARY portion (parent-named references + notes — already
	// cap-validated by the caller via ValidateContextSnapshot, e.g.
	// pkg/tools/delegate.go's executeRun) into the SAME task text every
	// dispatch kind reads (cfg.SystemPrompt becomes the child's first user
	// message on the native path and composeDelegateInput's task text on
	// the external-cli path below) — one composition point covers BOTH,
	// so a snapshot reaches the child regardless of native/3P dispatch.
	// The MANDATORY core (task prompt + criteria + target identity) is
	// untouched by this — it is already cfg.SystemPrompt/ActualSystemPrompt
	// themselves, assembled by the caller, never subject to the
	// snapshot_max_bytes cap (m4).
	if snapshotText := renderContextSnapshot(ss.st.cfg.ContextSnapshot); snapshotText != "" {
		ss.st.cfg.SystemPrompt = ss.st.cfg.SystemPrompt + "\n\n---\nContext (parent-provided, read-only references):\n" + snapshotText
	}

	// 3. Determine timeout for child SubTurn
	ss.timeout = ss.st.cfg.Timeout
	if ss.timeout <= 0 {
		ss.timeout = ss.st.rtCfg.defaultTimeout
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
	//
	// UAT A-17: the time limit itself is NOT this context's deadline any more.
	// It is enforced by the force-cancel timer armed just before dispatch
	// (armSubTurnForceCancel, below), which hard-aborts the child at
	// forceCancelAt exactly the way a hard cancel does. A bare context
	// deadline could not do that: runTurn's tool loop stops between queued
	// tool calls only on turnState.hardAbortRequested(), which a deadline never
	// sets, so a child whose limit expired inside one tool call went on to run
	// the next queued call (a file write) after its caller had already been
	// told the delegation failed. childCtx keeps a deadline
	// subTurnForceCancelBackstop later as a pure safety net for the one window
	// the timer cannot reach (the limit expiring before runTurn has registered
	// its cancel funcs).
	ss.forceCancelAt = time.Now().Add(ss.timeout)
	ss.st.childCtx, ss.st.cancel = context.WithTimeout(context.Background(), ss.timeout+subTurnForceCancelBackstop)
	return nil, false, nil
}

// resolveDelegateIdentity resolves the child identity and dispatch kind.
func (ss *spawnSubTurnSetupState) resolveDelegateIdentity() (*tools.ToolResult, bool, error) {
	// ADR-053 S2/D1: when the caller (pkg/tools/delegate.go's executeRun)
	// already minted a durable session_id BEFORE dispatch (so it could
	// persist the initial `queued` LifecycleRecord and hand the id back to
	// the caller synchronously), reuse that EXACT value as childID rather
	// than generating a fresh counter-based one. childID becomes
	// childTS.sessionKey below, which is also the steering-queue scope key
	// (steering.go) — this alignment is what lets delegate.go's steer/
	// respond/cancel/peek actions address a child purely by the durable
	// session_id it returned from `run`, with no separate id-mapping table.
	ss.st.childID = ss.st.cfg.DelegateSessionID
	if ss.st.childID == "" {
		ss.st.childID = ss.st.al.generateSubTurnID()
	}

	// FR-H-003: Extract the parent spawn tool call's ID from context. This was injected
	// by loop.go via withSpawnToolCallID before calling ExecuteWithContext on the spawn tool.
	// It becomes the parentSpawnCallID for the child turn, enabling correlation of all
	// child tool calls back to their originating spawn call on the wire.
	ss.st.parentSpawnCallID = spawnToolCallIDFromContext(ss.ctx)

	// W1-12: guard against degenerate input (empty parentSpawnCallID). When the
	// spawn tool call ID was not injected into context, "span_" + "" == "span_"
	// which collides across sub-turns and corrupts the SubagentBlock. We still run
	// the sub-turn, but emit no subagent_start / subagent_end WS frames so the
	// tool call renders as a flat ToolCallBadge instead of an unrooted span.
	ss.st.emitSpanEvents = ss.st.parentSpawnCallID != ""
	if !ss.st.emitSpanEvents {
		slog.Warn("subturn: empty parent_spawn_call_id — skipping span lifecycle emission",
			"child_id", ss.st.childID,
			"parent_turn_id", ss.st.parentTS.turnID,
		)
	}

	// Compute span_id deterministically (FR-H-004): "span_" + parentSpawnCallID.
	ss.st.spanID = "span_" + ss.st.parentSpawnCallID

	// subTurnStartedAt records the wall-clock start for duration_ms in SubTurnEndPayload.
	ss.subTurnStartedAt = time.Now()

	// Get the agent instance from parent, falling back to the default agent.
	// Wrap it in a shallow copy that uses an ephemeral (in-memory only) session store
	// so that child turns never pollute or persist to the parent's session history.
	baseAgent := ss.st.parentTS.agent
	if baseAgent == nil {
		baseAgent = ss.st.al.registry.GetDefaultAgent()
	}
	if baseAgent == nil {
		return nil, true, errors.New("parent turnState has no agent instance")
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
	//
	// ADR-054 D7/§9 (REVISED — the earlier "best-effort fall back to
	// baseAgent" posture is withdrawn): a named target that does not resolve
	// — deleted, renamed since delegation was configured, or its
	// entities/agents/<id>.json record failed to load — must ABORT the
	// sub-turn, never substitute the parent's identity/tool policy. Falling
	// back to baseAgent here was exactly the bug D7 named: execSource would
	// then be the PARENT, and StoreToolPolicy(execSource.LoadToolPolicy())
	// below would run the child with the PARENT's tool policy — inverting
	// the entire reason to delegate to a distinct, possibly more-restricted
	// worker. Self-delegation (cfg.TargetAgentID == "") is unaffected and
	// still trivially uses baseAgent as its own source.
	var targetAgent *AgentInstance
	if ss.st.cfg.TargetAgentID != "" {
		if t, ok := ss.st.al.registry.GetAgent(ss.st.cfg.TargetAgentID); ok && t != nil {
			targetAgent = t
		} else {
			slog.Warn(
				"subturn: target agent not found in registry; aborting sub-turn "+
					"(ADR-054 D7 — never falls back to the parent's identity/tool policy)",
				"target_agent_id",
				ss.st.cfg.TargetAgentID,
				"parent_id",
				ss.st.parentTS.turnID,
			)
			return nil, true, fmt.Errorf("%w: %q", ErrDelegationTargetUnresolved, ss.st.cfg.TargetAgentID)
		}
	}
	ss.st.execSource = baseAgent
	if targetAgent != nil {
		ss.st.execSource = targetAgent
	}

	// ADR-072 D9 / spec FR-050..056: requested_skill is resolved against
	// execSource — the CHILD's own ContextBuilder, never the parent's — and
	// checked here, before any further dispatch/session/context work below,
	// so a denial or an unresolvable slug aborts the sub-turn cleanly at
	// dispatch, before the child's first model call (FR-053), exactly like
	// the depth-limit and target-unresolved checks immediately above this
	// one. A granted slug is recorded in canonicalRequestedSkill and
	// appended to the child's opts.ForcedSkills once opts exists below
	// (mirrors applyExplicitSkillCommand's own one-shot activation, so the
	// child's first turn begins with it loaded per FR-052/FR-056).

	if requested := strings.TrimSpace(ss.st.cfg.RequestedSkill); requested != "" {
		canonical, outcome := resolveRequestedSkillForChild(ss.st.execSource.ContextBuilder, requested)
		switch outcome {
		case requestedSkillDenied:
			return nil, true, fmt.Errorf("%w: agent %q, skill %q", tools.ErrRequestedSkillDenied, ss.st.execSource.ID, requested)
		case requestedSkillUnresolvable:
			return nil, true, fmt.Errorf("%w: skill %q", tools.ErrRequestedSkillNotFound, requested)
		case requestedSkillGranted:
			ss.st.canonicalRequestedSkill = canonical
		}
	}

	// Decide dispatch kind from the resolved DELEGATE's own executor config
	// (not the parent's) — see the comment above. Resolved here, ahead of the
	// AgentInstance build below, so the external-cli-only field overrides can
	// be applied precisely when needed and left untouched for native dispatch.
	// dispatchErr is consulted at the original dispatch site (step 8 below);
	// resolving it early does not change when the sub-turn actually fails —
	// only when the DECISION is computed.
	ss.dispatchKind, ss.dispatchErr = runner.ResolveDispatch(executorConfigOf(ss.st.execSource))
	return nil, false, nil
}

// createChildSession creates or resumes the child's durable session and records its task.
func (ss *spawnSubTurnSetupState) createChildSession() (*tools.ToolResult, bool, error) {
	// ADR-057 US-2/D1 (W1 agent half, FR-005/FR-006/FR-008/FR-009/FR-010):
	// mint a REAL, store-backed session under the EXACT childID computed
	// above — the child no longer shares the parent's transcript.jsonl.
	// Minted into the SAME shared *session.UnifiedStore the delegate tool
	// holds (pkg/agent/loop.go:1727-1728's sharedStore/al.GetSessionStore()),
	// or ChildCount/drill-down/cascade-cancel-by-lineage would all read an
	// empty store for this child. CreateSessionWithID (pkg/session/
	// unified_api.go) also copies the parent's Owner verbatim (FR-006)
	// under its own read-then-release two-step protocol (FR-082) and
	// refuses a childID that already exists on disk (FR-096) — both a nil
	// store (degraded boot, see loop.go:609-620) and a parent id that does
	// not name a real session surface here as a loud, non-nil error rather
	// than a silent delegation-without-a-real-session, which is exactly the
	// success-shaped failure this whole migration exists to close (see this
	// spec's governing note). SessionTypeDelegate is FR-008's "subordinate"
	// value.
	ss.st.sharedStore = ss.st.al.GetSessionStore()
	if ss.st.sharedStore == nil {
		return nil, true, fmt.Errorf("subturn: no shared session store wired — cannot mint a real session for delegated child %q", ss.st.childID)
	}
	if ss.st.cfg.IsResume {
		// Warm resume (native `delegate follow_up` on a terminal session —
		// see SubTurnConfig.IsResume's doc comment): childID already names a
		// REAL, store-backed session, minted by that session's very first
		// generation. Routing a resume through CreateSessionWithID would
		// ALWAYS collide with FR-096's own create-path collision guard
		// (BDD-107, doc comment below) — that guard exists to refuse a
		// create over an already-existing directory, and a resume is
		// definitionally not a create; it must never be sent through that
		// primitive (doing so was the exact regression this branch fixes —
		// every `follow_up` on a terminal session failed this guard 100% of
		// the time, since the directory it is "creating" is the very one it
		// means to resume). Verify the session genuinely still exists
		// instead, so a vanished/corrupted session on disk still surfaces as
		// a real, non-nil error here rather than silently "resuming" into
		// nothing.
		if active := ss.st.al.getActiveTurnState(ss.st.childID); active != nil && active.IsAlive() {
			return nil, true, fmt.Errorf(
				"subturn: resume child session %q: prior generation is still physically running",
				ss.st.childID,
			)
		}
		if _, getErr := ss.st.sharedStore.GetMeta(ss.st.childID); getErr != nil {
			return nil, true, fmt.Errorf("subturn: resume child session %q: %w", ss.st.childID, getErr)
		}
		// No SetMeta here: this is not a new parent->child edge. childID's
		// ParentSessionID was already stamped by the session's FIRST
		// generation (the create branch below) and must not be re-derived
		// from THIS caller — the follow_up caller is not necessarily the
		// agent that originally spawned the session (spawnCorrectiveFollowUp's
		// own doc comment on ParentAgentID makes the identical point for the
		// lifecycle record; the same non-re-parenting rule applies here to
		// the session store's own parent edge).
	} else {
		if _, createErr := ss.st.sharedStore.CreateSessionWithID(
			ss.st.childID,
			ss.st.parentTS.transcriptSessionID,
			session.SessionTypeDelegate,
			ss.st.parentTS.channel,
			ss.st.agent.ID,
		); createErr != nil {
			return nil, true, fmt.Errorf("subturn: create child session %q: %w", ss.st.childID, createErr)
		}
		// FR-008 (the parent->child edge itself): CreateSessionWithID mints the
		// session but never persists ParentSessionID (grep -c ParentSessionID
		// pkg/session/unified_api.go == 0) — SetMeta is the sole writer of that
		// field and is also what wires the FR-097 in-memory parent index
		// (u5WriteIdentityLocked, unified_meta_files.go), which is what makes
		// ChildCount(parentID) non-zero and the whole nested-session hierarchy
		// (sidebar/search tree, drill-down, durable-walk Stop, cascade delete)
		// resolvable at all. Skipping this call leaves meta.json's
		// ParentSessionID at its zero value with a green build and no compiler
		// or runtime signal — the exact silent-success shape this migration
		// exists to end. Applies to a genuine create only — a resume (above)
		// keeps its ORIGINAL edge untouched.
		childParentSessionID := ss.st.parentTS.transcriptSessionID
		if setMetaErr := ss.st.sharedStore.SetMeta(ss.st.childID, session.MetaPatch{ParentSessionID: &childParentSessionID}); setMetaErr != nil {
			return nil, true, fmt.Errorf("subturn: stamp parent edge for child %q: %w", ss.st.childID, setMetaErr)
		}
	}

	// RC-5b (ADR-057 UAT root-cause fix): persist the delegated task text to
	// the CHILD's OWN durable transcript as a role:"user" entry. The task
	// text reaches the LLM as opts.UserMessage below (cfg.SystemPrompt), but
	// that only ever lands in the ephemeral, in-memory ephemeralStore
	// (agent.Sessions above) — deliberate, so a child turn's history never
	// pollutes or persists to the delegate's real session (see
	// newEphemeralSession's call site above). Meanwhile the durable
	// transcript only ever received user-role entries from channel ingress,
	// scheduled/heartbeat runs, and the websocket handler — a delegated
	// sub-turn passes through none of those, so a delegated child's own
	// transcript.jsonl never recorded what it was asked to do (observed
	// live: 11 workers in a UAT session with zero user messages, making it
	// impossible to audit what any of them were told). Mirrors the shape
	// used by pkg/agent/loop.go's channelNeedsTranscript writer (runTurn),
	// scoped to the child's own session id (childID) rather than a channel
	// session, and does NOT touch parentTS's session history — the point is
	// only that the child's own durable transcript records its own task.
	// Fires on every generation, including a follow_up resume, since each
	// generation's cfg.SystemPrompt is a genuinely new instruction to record.
	//
	// Must write through sharedStore, NOT parentTS.transcriptStore: childID
	// was minted a few lines above into sharedStore (al.GetSessionStore()) —
	// that is the only store that has ever heard of it. parentTS.transcriptStore
	// is whatever al.ResolveSessionStore(parentSessionID) (or, for a
	// task-executor-triggered run, al.GetAgentStore(agentID) directly — see
	// processTaskDirect/processTaskDirectExternalCLI in loop.go) resolved for
	// the PARENT session, which for an old/legacy session is a per-agent
	// store distinct from sharedStore. Writing through parentTS.transcriptStore
	// in that case handed AppendTranscriptStrict a childID it had never seen,
	// so the strict "refuse an unknown session" contract (see
	// AppendTranscriptStrict's own doc comment) silently swallowed the write
	// (WARN-logged, not propagated) for exactly the legacy-session deployments
	// this fix was meant to help most.
	if strings.TrimSpace(ss.st.cfg.SystemPrompt) != "" {
		taskEntry := session.TranscriptEntry{
			ID:        fmt.Sprintf("user-%d", time.Now().UnixNano()),
			Role:      "user",
			AgentID:   ss.st.agent.ID,
			Content:   ss.st.cfg.SystemPrompt,
			Timestamp: time.Now().UTC(),
		}
		if taskErr := ss.st.sharedStore.AppendTranscriptStrict(ss.st.childID, taskEntry); taskErr != nil {
			logger.WarnCF("subturn", "could not record delegated task to child transcript",
				map[string]any{"child_id": ss.st.childID, "error": taskErr.Error()})
		}
	}

	ss.st.prepareProcessOptions()
	return nil, false, nil
}

// buildDelegateAgent builds the delegated agent from the resolved target identity.
func (st *spawnSubTurnState) buildDelegateAgent() {
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
	st.execSource.mu.RLock()
	execModel := st.execSource.Model
	execProvider := st.execSource.Provider
	execCandidates := st.execSource.Candidates
	execThinkingLevel := st.execSource.ThinkingLevel
	// ADR-066 D2: the window travels with the quad — it is resolved from
	// the TARGET's own (provider, model), so the child sees the same
	// window its provider/model would give it, never the parent's.
	execContextWindow := st.execSource.ContextWindow
	execWindowSource := st.execSource.WindowSource
	execWindowClamped := st.execSource.WindowClamped
	execWindowExempt := st.execSource.WindowExempt
	execWindowUnknown := st.execSource.WindowUnknown
	execProviderPool := st.execSource.providerPool.Load()
	st.execSource.mu.RUnlock()

	st.ephemeralStore = newEphemeralSession(nil)
	// Build a new AgentInstance from execSource's fields to avoid copying the
	// mutex. Sessions is the one deliberate exception — always a fresh
	// ephemeral (in-memory only) store, so child turns never pollute or
	// persist to the source agent's real session history. Tools is set below
	// (needs the delegate/switch_agent exclusion, not a plain copy); toolPolicy
	// and providerPool are unexported atomic fields a struct literal cannot
	// copy at all, also set below.
	st.agent = AgentInstance{
		ID:              st.execSource.ID,
		Name:            st.execSource.Name,
		Model:           execModel,
		Fallbacks:       st.execSource.Fallbacks,
		FallbackModels:  st.execSource.FallbackModels,
		Home:            st.execSource.Home,
		MaxIterations:   st.execSource.MaxIterations,
		MaxTokens:       st.execSource.MaxTokens,
		Temperature:     st.execSource.Temperature,
		ThinkingLevel:   execThinkingLevel,
		ContextWindow:   execContextWindow,
		WindowSource:    execWindowSource,
		WindowClamped:   execWindowClamped,
		WindowExempt:    execWindowExempt,
		WindowUnknown:   execWindowUnknown,
		Provider:        execProvider,
		Sessions:        st.ephemeralStore,
		ContextBuilder:  st.execSource.ContextBuilder,
		Subagents:       st.execSource.Subagents,
		SkillsFilter:    st.execSource.SkillsFilter,
		Candidates:      execCandidates,
		TimeoutSeconds:  st.execSource.TimeoutSeconds,
		Router:          st.execSource.Router,
		LightCandidates: st.execSource.LightCandidates,
		LightProvider:   st.execSource.LightProvider,
		AgentType:       st.execSource.AgentType,
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
		st.agent.StoreProviderPool(*execProviderPool)
	}
	// LoadToolPolicy()/StoreToolPolicy() — same "unexported atomic field, struct
	// literal can't copy it" situation as providerPool. Left unset, every tool
	// fails closed to deny (resolveEffectivePolicyWith: no entry on either
	// side -> deny) — this was the second half of the original identity bug
	// (see below).
	st.agent.StoreToolPolicy(st.execSource.LoadToolPolicy())
	// ID/ContextBuilder — the first half of the original identity bug, still
	// worth naming explicitly: ContextBuilder.BuildSystemPrompt() resolves
	// the compiled soul via cb.agentID, so reusing the PARENT's ContextBuilder
	// (the pre-fix behavior) meant a delegate literally answered as the
	// parent — observed live: delegating to Worker (an intentionally
	// soul-less agent) returned Jim's own compiled persona verbatim. This
	// also caused the tool-policy split-brain: tools.WithAgentID(turnCtx,
	// ts.agent.ID) (loop.go) threads agent.ID into the tool-execution
	// context, and ToolSearch's canLoad resolver looks up "the calling agent"
	// by that ID via a fresh al.registry.GetAgent(...) call — so an unswapped
	// ID meant canLoad checked the PARENT's real, registry-backed policy
	// while the final FilterToolsByPolicy call (loop.go) read the child's own
	// (nil, deny-all) toolPolicy above — two different verdicts for the same
	// tool, observed live as an infinite ToolSearch retry loop.

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
	// ADR-057 FR-031 (W10a): the retired single-key Inherit(sessionID,
	// parentAgentID, childAgentID) used ONE session id for both the source
	// lookup and the destination write — correct only while parent and
	// child shared a session id. Under D1 every delegated child owns its
	// OWN real session (childID), so the two-key InheritFrom is required:
	// source = the PARENT's own session id + the PARENT's agent id (where
	// its grants actually live); destination = the CHILD's OWN session id
	// (childID) + the child's agent id. This field intentionally stays
	// parentTS.transcriptSessionID, NOT parentTS.routingSessionID — grant
	// inheritance is not in FR-014's closed routingSessionID consumer set
	// (WS payload stamping, the role-B predicates, pre-arm keys), and
	// transcriptSessionID is exactly "the parent's own real session id"
	// (its own childID when the parent is itself a delegated child).
	st.al.ApprovalGrants().InheritFrom(st.parentTS.transcriptSessionID, st.parentTS.agentID, st.childID, st.agent.ID)

	// FR-H-006 REVERSAL: "delegate" is NO LONGER excluded from the child's
	// registry. Note: distinct from the identity-swap
	// ToolSearch bug documented just above (ID/ContextBuilder, ~line 663) —
	// that one was wrong AGENT IDENTITY (an unswapped childTS.agentID made
	// canLoad resolve the PARENT's policy instead of the child's own); this
	// one is an INCOMPLETE TOOL SET for a correctly-identified agent (the
	// child's identity was already right, but "delegate" was unconditionally
	// missing from its registry regardless of identity or policy). Both
	// happen to manifest through the same ToolSearch fabricated-success
	// symptom described below, but the two are independent bugs with
	// independent fixes — do not conflate them when debugging a future
	// ToolSearch report. The original FR-H-006 rationale ("one level
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
	// unified `ToolSearch` infra tool (ScopeCore, lazily loaded — see
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
	// authorized. "switch_agent" remains excluded (ADR-071 D4 renamed
	// hand_off + return_to_default to this one tool): a nested sub-turn
	// hijacking the ACTIVE parent session's agent is a distinct, still-valid
	// concern (session takeover) unrelated to task-delegation chain depth,
	// and is not governed by the depth-cap/trust-graph system at all.
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
	if st.execSource.Tools != nil {
		// Known residual gap (not fixed here, documented only): unlike
		// "delegate" above, "switch_agent" (ADR-071 D4 renamed hand_off +
		// return_to_default to this one tool — the defect's mechanism is
		// unaffected by the rename, see below) is unconditionally excluded
		// from EVERY child sub-turn's registry, and the SAME ToolSearch
		// fabricated-success-then-permission_denied bug just cured for
		// "delegate" is still live for "switch_agent" — canLoad/markLoaded
		// (pkg/tools/tools_tool.go) resolve the caller via
		// al.registry.GetAgent(callerID), the PERSISTENT top-level agent,
		// not this ephemeral child's own registry, so ToolSearch can still
		// report a fabricated success for "switch_agent" here even though it
		// is structurally absent from agent.Tools. Root-caused but out of
		// scope for this fix (tools_tool.go is a larger, separate change) —
		// this is a wrong-REGISTRY bug (caller resolution), not a wrong-NAME
		// bug, so it survives the D1 (load_tool->ToolSearch) and D4
		// (hand_off/return_to_default->switch_agent) renames identically.
		st.agent.Tools = st.execSource.Tools.CloneExcept(tools.ExcludedSwitchAgent)
		// Log the constructed registry so operators can debug "my subagent has no tools" issues.
		slog.Info("subturn: child registry constructed",
			"excluded", []string{string(tools.ExcludedSwitchAgent)},
			"remaining_count", st.agent.Tools.Count(),
			"child_id", st.childID,
		)
	}
}

// prepareProcessOptions prepares the child turn's process options without reordering identity fields.
func (st *spawnSubTurnState) prepareProcessOptions() {
	// Create processOptions for the child turn.
	// ADR-057 FR-007/FR-009: TranscriptSessionID is now the child's OWN
	// session id (childID) — every delegated child writes its own
	// transcript, never the parent's. Cascade-cancel reachability no
	// longer depends on a SHARED transcriptSessionID matching inside the
	// (now-retired) InterruptSession entry point, which is what this
	// comment used to describe — it is carried instead by
	// routingSessionID, inherited verbatim from the parent immediately
	// below (FR-011) and consumed by the collapsed Interrupt(id, scope,
	// hint) entry point (FR-041) via the role-B predicates (FR-015).
	//
	// Soul composition (RC-8 fix): the system role is the DELEGATE's own soul
	// (config.AgentConfig.Soul or the compiled coreagent.GetPrompt), and the
	// task becomes the first user message. For the NATIVE dispatch path (this
	// branch), that composition happens for free through childTS.agent's own
	// ContextBuilder — agent.ContextBuilder above (~line 950) is copied
	// verbatim from execSource (the resolved delegate, or baseAgent for
	// self-delegation) per ADR-032's "no inheritance from the parent" rule,
	// and ContextBuilder.BuildSystemPrompt/BuildMessages resolve the soul
	// from that builder's OWN agentID when the child turn actually runs —
	// see pkg/agent/context.go's compiled-prompt / SOUL.md / getIdentity()
	// branches. There used to be a SECOND, parallel soul-resolution path
	// here — an opts.SystemPromptOverride field computed via
	// resolveDelegateSoul(al, cfg.TargetAgentID) — but ContextBuilder.
	// BuildMessages (context.go) has no override parameter and never read
	// it, and newTurnState (turn.go) never touched it either: it was dead
	// from the day it was written, verified by grepping for every read of
	// processOptions.SystemPromptOverride outside its own declaration and
	// this one write site — none exist. Deleted rather than wired, since the
	// live ContextBuilder path already does this correctly (see
	// TestSpawnSubTurn_NativeDispatch_AdoptsFullTargetIdentityIncludingModel
	// in subturn_target_identity_test.go, and
	// TestSpawnSubTurn_NativeDispatch_SystemPromptComesFromTargetContextBuilder
	// in subturn_rc8_dead_override_test.go for the regression coverage that
	// this deletion did not change native persona resolution).
	//
	// The EXTERNAL-CLI dispatch path (below, ~line 1774) composes the soul
	// independently via composeDelegateInput(al, cfg.SystemPrompt,
	// cfg.ActualSystemPrompt, cfg.TargetAgentID) — that call site resolves
	// cfg.ActualSystemPrompt / cfg.TargetAgentID directly from cfg, not from
	// any field on this opts literal, so it is entirely unaffected by this
	// deletion. A worker with an EMPTY soul still runs with an EMPTY system
	// role there, NOT the legacy "You are a subagent" string.
	st.opts = processOptions{
		SessionKey:              st.childID,
		Channel:                 st.parentTS.channel,
		ChatID:                  st.parentTS.chatID,
		SenderID:                st.parentTS.opts.SenderID,
		SenderDisplayName:       st.parentTS.opts.SenderDisplayName,
		UserMessage:             st.cfg.SystemPrompt, // Task description becomes the first user message
		Media:                   nil,
		InitialSteeringMessages: st.cfg.InitialMessages,
		DefaultResponse:         "",
		SendResponse:            false,
		// ADR-057 FR-007: NoHistory MUST NOT be set for a delegated child —
		// was `true` here ("SubTurns don't use session history"). Left at
		// its zero value (false) so the child goes through the same
		// history load/save path as any other turn, against its own
		// ephemeral in-memory store (agent.Sessions above) — a separate
		// concept from the transcript.jsonl persistence TranscriptSessionID/
		// TranscriptStore below govern.
		SkipInitialSteeringPoll: true,
		TranscriptSessionID:     st.childID, // FR-007: the child's OWN session id, not the parent's
		// Must be sharedStore, NOT parentTS.transcriptStore: childID was
		// minted into sharedStore (al.GetSessionStore()) above, and
		// parentTS.transcriptStore can be a different store instance (e.g. a
		// task-executor-triggered run's al.GetAgentStore(agentID) legacy
		// per-agent store — see loop.go's processTaskDirect/
		// processTaskDirectExternalCLI, or any parent session
		// al.ResolveSessionStore fell back on). The RC-5b task-entry write a
		// few lines above this struct was fixed for the identical reason;
		// this field governs every OTHER transcript write for the child's
		// own turn (assistant messages, tool calls, etc. — turn.go's
		// appendToolCallTranscript/recordAssistantMessage-style writers all
		// key off opts.TranscriptStore) and would silently fail the exact
		// same way if left pointed at the wrong store.
		TranscriptStore: st.sharedStore,
		// FIX 1 (re-review): WorkspaceID inherits from the PARENT turn, not
		// execSource (the resolved delegate). This is deliberately NOT covered
		// by ADR-032's "no inheritance from the parent" rule (see that ADR's
		// note in CLAUDE.md): the "Workspace" ADR-032 protects is the
		// AgentInstance Home field — the per-agent directory-path identity
		// field (renamed "agent home" by ADR-046) — sourced from execSource a
		// few lines above via CloneExcept/the execSource-snapshot copy, and
		// via the SEPARATE, identity-keyed CoreTeam reroot in runTurn
		// (resolveTurnWorkDirOrRefuse, keyed off ts.agent.ID == execSource.ID
		// for the child). processOptions.WorkspaceID is a different concept
		// entirely — the Spec-1 multi-agent Workspace *room* a turn is
		// running inside (FR-7.1 memory routing / bus.OutboundMediaMessage
		// delivery) — and it is turn/session-scoped, not agent-scoped: every
		// other field in this same struct literal that carries that same
		// kind of context (Channel, ChatID, SenderID, SenderDisplayName,
		// TranscriptSessionID, TranscriptStore) is already sourced from
		// parentTS, not execSource, because a delegated child is still
		// answering within the PARENT's conversation/room, just running as a
		// different agent identity. Leaving WorkspaceID as the sole
		// session-context field NOT inherited was the actual bug: it silently
		// degraded bus.OutboundMediaMessage.WorkspaceID (loop.go's tool-media
		// delivery block) to the private/global room for every delegated
		// child that produces media inside a workspace-bound session, and
		// (via FindForAgentPreferring's tie-break, loop.go's "Filesystem
		// re-rooting" comment) removed a genuine tie-breaking signal for a
		// child agent that belongs to more than one workspace's CoreTeam.
		WorkspaceID: st.parentTS.opts.WorkspaceID,
		// ADR-075 FR-032 / issue #659: AutoDenyAsk is INHERITED from the
		// parent turn. It was not, and that was the defect.
		//
		// AutoDenyAsk means "there is no operator on this run, so an
		// `ask`-policy tool must be denied rather than queued for an approval
		// nobody can answer". It is set true only for headless/scheduled runs
		// (ProcessScheduled). A delegated child of such a run is just as
		// unattended as its parent — there is no second operator who appeared
		// because the work was delegated — but the child's processOptions were
		// built without the flag, so its first `ask`-policy tool issued an
		// approval request into a run with nobody watching and the turn
		// blocked until its deadline.
		//
		// D1 is what makes this urgent rather than tidy: under ADR-075 a
		// delegated sub-turn browses its workspace's SIGNED-IN browser, and
		// D2.9 seeds browser_upload_file as `ask` for every agent. Without
		// this line the first delegated sub-turn to reach it hangs.
		//
		// INHERITED, NOT FORCED ON. Setting it unconditionally for every
		// delegated child would silently convert an INTERACTIVE user's
		// delegation into blanket denials — an operator IS attached to that
		// run, and the approval prompt is exactly what they expect. The
		// property the requirement names is "no operator attached", and the
		// parent's flag is what records that.
		AutoDenyAsk: st.parentTS.opts.AutoDenyAsk,
	}
}

// configureChildTurn configures and registers the child turn while preserving routing identity assignment order.
func (st *spawnSubTurnState) configureChildTurn() {
	// ADR-072 D9 / FR-052/FR-056: a granted requested_skill is appended to
	// the child's ForcedSkills — the SAME one-shot, per-turn field the human
	// "/<skill>" slash command populates (applyExplicitSkillCommand, loop.go)
	// — so the child's first turn begins with it already loaded, exactly as
	// a slash-command activation would. canonicalRequestedSkill is empty
	// whenever cfg.RequestedSkill was empty (the ordinary, unaffected case).
	if st.canonicalRequestedSkill != "" {
		st.opts.ForcedSkills = append(st.opts.ForcedSkills, st.canonicalRequestedSkill)
	}

	// Create event scope for the child turn
	scope := st.al.newTurnEventScope(st.agent.ID, st.childID)

	// Create child turnState using the new API
	st.childTS = newTurnState(&st.agent, st.opts, scope)
	// ADR-057 FR-011 (W4 subturn half): OVERWRITE the routing id
	// newTurnState just defaulted to this child's OWN session id (correct
	// only for a root turn) with the PARENT's routingSessionID, inherited
	// verbatim through the whole delegation subtree — see
	// turnState.routingSessionID's doc comment (turn.go) for the full
	// contract this closes. Skipping this overwrite silently leaves a
	// child's routing/interrupt-scope key equal to its own session id
	// instead of the root's, and a chat-wide Stop stops reaching it.
	st.childTS.routingSessionID = st.parentTS.routingSessionID // u19:inheritance

	// Set SubTurn-specific fields
	st.childTS.cancelFunc = st.cancel
	st.childTS.critical = st.cfg.Critical
	st.childTS.depth = st.parentTS.depth + 1
	st.childTS.parentTurnID = st.parentTS.turnID
	st.childTS.parentTurnState = st.parentTS
	st.childTS.pendingResults = make(chan *tools.ToolResult, 16)
	st.childTS.concurrencySem = make(chan struct{}, st.rtCfg.maxConcurrent)
	st.childTS.al = st.al                  // back-ref for hard abort cascade
	st.childTS.session = st.ephemeralStore // same store as agent.Sessions
	// FR-H-003: set parentSpawnCallID so all ToolExec* events emitted by this child turn
	// carry the parent spawn's ToolCall.ID as ParentSpawnCallID.
	st.childTS.parentSpawnCallID = st.parentSpawnCallID

	// IMPORTANT: Put childTS into childCtx so that code inside runTurn can retrieve it
	st.childCtx = withTurnState(st.childCtx, st.childTS)
	st.childCtx = WithAgentLoop(st.childCtx, st.al) // Propagate AgentLoop to child turn
	// ADR-053 S2/D1: expose the child's own durable session_id (== childID
	// above) to its OWN tool calls (message_parent.go reads this via
	// tools.ToolDelegateSessionID) — historically distinct from the
	// transcript session id (tools.ToolTranscriptSessionID), which this
	// context also carries via runTurn below. Under ADR-057 FR-007 the two
	// have converged for a delegated child: TranscriptSessionID is now
	// childID as well (see the processOptions construction above), so both
	// context values name the same real session; ToolDelegateSessionID is
	// kept as its own carrier rather than removed, since callers resolve
	// delegation identity through it independently of transcript wiring.
	st.childCtx = tools.WithDelegateSessionID(st.childCtx, st.childID)

	st.childTS.ctx = st.childCtx

	// Register child turn state so GetAllActiveTurns/Subagents can find it.
	//
	// registerActiveTurn (turn.go), not a bare activeTurnStates.Store: a
	// cancel that arrived for this session BEFORE the child existed at all
	// (RequestCancel found nothing yet — e.g. the delegating parent's own
	// turn already finished, the common case for `delegate async=true`,
	// since DelegateTool.executeAsync dispatches this whole call on a fresh
	// goroutine and returns an immediate ack) arms a pre-registration cancel
	// latch (cancel_prearm.go) instead of silently no-op'ing. Only
	// registerActiveTurn calls consumePreArmedCancel, so this MUST be the
	// registration path — a bare Store here left that latch unconsumed at
	// the earliest (and safest) possible moment, relying entirely on
	// al.runTurn's OWN later internal registerActiveTurn call (loop.go) to
	// pick it up instead. That still usually worked, but only by accident of
	// timing: it pushed the latch's already-bounded 5s TTL window out across
	// every step runTurn does first (workspace-dir resolution, citation
	// tracker setup, ...) for no reason, and under real load (slow disk,
	// contended CPU) that widened window is exactly what let a genuine Stop
	// click's latch expire unconsumed — the turn then ran to completion with
	// no cancellation and no turn_canceled transcript entry (e2e T24a
	// regression, tests/e2e/cancel-cross-channel.spec.ts:665). Registering
	// here consumes the latch (if any) the INSTANT the child becomes
	// reachable, before any of that later setup work — see
	// TestRepro_SpawnSubTurn_RawStoreBypassesPreArmedCancel for a
	// deterministic proof this closes, and TestRepro_AsyncDelegateCancel_
	// ArmsBeforeChildRegisters for the full end-to-end cascade.
	//
	// Safe to call unconditionally: consumePreArmedCancel is an exactly-once,
	// map-delete-guarded no-op when no latch is armed, so a turn spawned with
	// no pending cancel behaves identically to the old bare Store.
	st.al.registerActiveTurn(st.childTS)
}

// publishChildSpawn publishes the registered child and its spawn event.
func (st *spawnSubTurnState) publishChildSpawn() {
	// The child is now real, discoverable evidence of its own (findable via
	// GetActiveTurnHookForSession/sessionTurnsStillAlive by routingSessionID,
	// re-based from transcriptSessionID by U3's role split, ADR-057 FR-015) —
	// the pending-spawn marker's whole job was to stand in for that evidence
	// during the window that just closed. Clear it explicitly, right here,
	// rather than leaving it for this function's own early-return defer
	// (above): that defer only fires when registeredForCancel is still
	// false, so setting it true and clearing now are the same operation
	// from two angles — mark the marker "no longer needed" and remove it in
	// the same breath, at the earliest point that is true.
	st.registeredForCancel = true
	st.al.cancelPreArm.clearPendingSpawn(st.pendingSpawnKeysForThisCall...)

	// 5. Establish parent-child relationship (thread-safe)
	st.parentTS.mu.Lock()
	st.parentTS.childTurnIDs = append(st.parentTS.childTurnIDs, st.childID)
	st.parentTS.mu.Unlock()

	// 6. Emit Spawn event (FR-H-004: carries SpanID, ParentSpawnCallID, TaskLabel, ChatID, AgentID)
	// task label: prefer cfg.TaskLabel if set (from spawn tool's label arg); else use the first
	// 60 runes of SystemPrompt as a fallback so the WS frame always has something human-readable.
	taskLabel := st.cfg.TaskLabel
	if taskLabel == "" {
		runes := []rune(st.cfg.SystemPrompt)
		if len(runes) > 60 {
			taskLabel = string(runes[:60])
		} else {
			taskLabel = st.cfg.SystemPrompt
		}
	}
	// W1-12: only emit span lifecycle events when parentSpawnCallID is non-empty.
	if st.emitSpanEvents {
		// The span counts as active from before its spawn event until after its
		// end event (the cleanup defer below) — markSubTurnSpanOpen's doc
		// comment (steering.go) explains why the turn registry alone cannot say.
		st.al.markSubTurnSpanOpen(st.parentSpawnCallID)
		slog.Debug("subagent_start",
			"span_id", st.spanID,
			"parent_call_id", st.parentSpawnCallID,
			"agent_id", st.childTS.agentID,
		)
		st.al.emitEvent(EventKindSubTurnSpawn,
			st.childTS.eventMeta("spawnSubTurn", "subturn.spawn"),
			SubTurnSpawnPayload{
				AgentID:           st.childTS.agentID,
				Label:             st.childID,
				ParentTurnID:      st.parentTS.turnID,
				SpanID:            st.spanID,
				ParentSpawnCallID: session.ToolCallID(st.parentSpawnCallID),
				TaskLabel:         taskLabel,
				ChatID:            st.parentTS.chatID,
				// ADR-057 FR-017/W21c: pinned to the PARENT's routingSessionID
				// (SubTurnSpawnPayload.SessionID's own doc comment, U23,
				// events.go, is the frozen contract this satisfies) — was
				// parentTS.transcriptSessionID before U3's role split landed.
				// Do NOT repoint to the child's own id: the child's id already
				// rides this same payload as Label.
				SessionID: string(st.parentTS.routingSessionID), // u19:ws-stamping
			},
		)
	}
}

// subTurnForceCancelBackstop is how far past a sub-turn's time limit childCtx's
// own deadline sits (UAT A-17). The limit itself is enforced by
// armSubTurnForceCancel; this backstop only bounds the rare window in which
// the limit expires before runTurn / runExternalCLISubTurn has registered the
// cancel funcs requestHardAbort fires. The hard-abort flag is still set in
// that window, so no tool call can start — only an in-flight model call is
// left for this deadline to end.
const subTurnForceCancelBackstop = 5 * time.Second

// ====================== Other Types ======================

// ephemeralSessionStore is an in-memory session.SessionStore used by SubTurns.
// It does not persist to disk and auto-truncates history to maxEphemeralHistorySize.
type ephemeralSessionStore struct {
	mu      sync.Mutex
	history []providers.Message
	// projection / hydrated: in-memory projection state (ADR-066 FR-019);
	// lives and dies with the sub-turn, never persisted.
	//
	// projection is keyed ABSOLUTELY — by (tool_call_id, dropped + relative
	// index) — while every caller addresses lines RELATIVELY, by the index
	// ReadArchive reports today. See dropped.
	projection memory.ProjectionSet
	hydrated   bool
	// dropped counts the messages removed from the FRONT of history, by
	// TruncateHistory and by the maxEphemeralHistorySize ring.
	//
	// memory.ProjectionKey.ArchiveLine is documented as "the zero-based
	// archive line", and every producer derives it from a ReadArchive index
	// (tool_result_admit.go: line = len(read) - 1). memory.JSONLStore honours
	// the implied invariant — its archive is append-only and TruncateHistory
	// only advances Skip, so an index never moves. This store truncates from
	// the front, which shifts every surviving message's index down while the
	// recorded keys stay put: after one trim (or one ring wrap) every capped
	// or emptied sub-turn result silently re-inflated to its FULL content on
	// the next assembly — so the retry after a provider context overflow sent
	// MORE than the attempt that had just overflowed.
	//
	// dropped restores the invariant without changing the interface: writes
	// are translated to absolute (+dropped) and reads back to relative
	// (−dropped), so a key addresses the same MESSAGE for as long as that
	// message is in the ring.
	dropped int
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

func (e *ephemeralSessionStore) SetHistory(_ string, history []providers.Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = make([]providers.Message, len(history))
	copy(e.history, history)
	// A wholesale replacement invalidates every recorded line, exactly as
	// memory.JSONLStore.SetHistory clears meta.Projection.
	e.projection = nil
	e.dropped = 0
	e.truncateLocked()
}

func (e *ephemeralSessionStore) TruncateHistory(_ string, keepLast int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if keepLast <= 0 {
		e.dropped += len(e.history)
		e.history = nil
		return
	}

	if keepLast >= len(e.history) {
		return
	}
	e.dropped += len(e.history) - keepLast
	e.history = e.history[len(e.history)-keepLast:]
}

func (e *ephemeralSessionStore) Save(_ string) error { return nil }

func (e *ephemeralSessionStore) Close() error { return nil }

// RollbackAppended truncates the in-memory history to its first
// targetArchiveLen messages, discarding anything appended after that point,
// and drops in-memory projection entries whose archive_line ≥ targetArchiveLen.
// targetSkip is accepted for interface compatibility but has no effect: the
// ephemeral backend is a bounded in-memory ring with no Skip/archive split —
// there is no eviction cursor to restore. emptiedSet is likewise a no-op
// here (ADR-066 FR-020): the ephemeral store keeps its projection state in
// memory for the lifetime of one sub-turn and is discarded with it, so
// there is no turn-start restore point to return to.
//
// Note: rollback is best-effort when the ephemeral ring has wrapped (i.e. a
// sub-turn appended >maxEphemeralHistorySize messages and the ring discarded
// the oldest). In that case targetArchiveLen no longer maps to the same
// messages that were at the head before the ring wrapped, so the logical
// pre-turn state cannot be perfectly restored. This is low-probability
// (sub-turns are short) and the ephemeral store has no persistent archive.
//
// Satisfies session.SessionStore (used by hard-abort turn rollback).
func (e *ephemeralSessionStore) RollbackAppended(_ string, targetArchiveLen, _ int, _ memory.ProjectionSet) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if targetArchiveLen < 0 {
		targetArchiveLen = 0
	}
	if targetArchiveLen < len(e.history) {
		e.history = e.history[:targetArchiveLen]
	}
	// targetArchiveLen is a RELATIVE length; the keys are absolute.
	for k := range e.projection {
		if k.ArchiveLine-e.dropped >= targetArchiveLen {
			delete(e.projection, k)
		}
	}
}

// Projection implements session.SessionStore — the in-memory projection
// state of this sub-turn (never persisted; FR-019 store half).
// The returned keys are RELATIVE to the current ring contents — the same
// index space ReadArchive reports and archiveLineResolver resolves into —
// so a key keeps addressing its own message across front-truncations. An
// entry whose message has been dropped out of the ring is omitted.
func (e *ephemeralSessionStore) Projection(_ string) memory.ProjectionMeta {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(memory.ProjectionSet, len(e.projection))
	for k, v := range e.projection {
		rel := k.ArchiveLine - e.dropped
		if rel < 0 {
			continue // the message is no longer in the ring
		}
		out[memory.ProjectionKey{ToolCallID: k.ToolCallID, ArchiveLine: rel}] = v
	}
	return memory.ProjectionMeta{Entries: out, Hydrated: e.hydrated}
}

// SetProjectionState implements session.SessionStore (in-memory). The caller
// addresses the line relatively (len(ReadArchive)-1); it is stored absolutely
// so a later front-truncation cannot re-point it at a different message.
func (e *ephemeralSessionStore) SetProjectionState(_ string, pk memory.ProjectionKey, state memory.ProjectionState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.projection == nil {
		e.projection = memory.ProjectionSet{}
	}
	e.projection[memory.ProjectionKey{ToolCallID: pk.ToolCallID, ArchiveLine: pk.ArchiveLine + e.dropped}] = state
}

// MarkHydrated implements session.SessionStore (in-memory).
func (e *ephemeralSessionStore) MarkHydrated(_ string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hydrated = true
}

func (e *ephemeralSessionStore) truncateLocked() {
	if len(e.history) > maxEphemeralHistorySize {
		e.dropped += len(e.history) - maxEphemeralHistorySize
		e.history = e.history[len(e.history)-maxEphemeralHistorySize:]
	}
}
