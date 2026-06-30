package agent

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// abandonedWritesSuppressed is incremented each time a write (transcript
// append, frame emit, or cost accumulation) is skipped because the turn has
// been marked abandoned. Exposed via AbandonedWritesSuppressed() for tests
// and operator tooling (omnipus_abandoned_writes_suppressed_total).
var abandonedWritesSuppressed atomic.Int64

// transcriptSuppressedErrors is incremented each time appendErrorTranscript
// is called with no transcript store wired (e.g. boot misconfig where
// transcriptStore is nil). A persistent non-zero value is a strong signal
// that error events are vanishing from the on-disk transcript; surfaces
// the otherwise-silent "errors-but-no-record" gap (W4-18). DEBUG logs of
// the no-op are promoted to WARN to keep this signal visible in production.
var transcriptSuppressedErrors atomic.Uint64

// TranscriptSuppressedErrors returns the current value of the
// transcript-suppressed-error counter. Used by tests and operator tooling.
func TranscriptSuppressedErrors() uint64 {
	return transcriptSuppressedErrors.Load()
}

// AbandonedWritesSuppressed returns the current value of the
// omnipus_abandoned_writes_suppressed_total counter.
func AbandonedWritesSuppressed() int64 {
	return abandonedWritesSuppressed.Load()
}

type TurnPhase string

const (
	TurnPhaseSetup      TurnPhase = "setup"
	TurnPhaseRunning    TurnPhase = "running"
	TurnPhaseTools      TurnPhase = "tools"
	TurnPhaseFinalizing TurnPhase = "finalizing"
	TurnPhaseCompleted  TurnPhase = "completed"
	TurnPhaseAborted    TurnPhase = "aborted"
)

type ActiveTurnInfo struct {
	TurnID       string
	AgentID      string
	SessionKey   string
	Channel      string
	ChatID       string
	UserMessage  string
	Phase        TurnPhase
	Iteration    int
	StartedAt    time.Time
	Depth        int
	ParentTurnID string
	ChildTurnIDs []string
}

type turnResult struct {
	finalContent string
	status       TurnEndStatus
	followUps    []bus.InboundMessage
	// turnFailed mirrors turnState.turnFailed so callers of runTurn can observe
	// whether the turn ended via the engine's error/limit fallback without holding
	// a reference to the turnState.  Populated by runTurn before it returns.
	turnFailed bool
}

type turnState struct {
	mu sync.RWMutex

	agent *AgentInstance
	opts  processOptions
	scope turnEventScope

	turnID     string
	agentID    string
	sessionKey string

	channel     string
	chatID      string
	userMessage string
	// userID is the authenticated gateway principal that initiated this turn
	// (FR-017), threaded from processOptions.UserID (← InboundMessage.GatewayUserID
	// ← websocket.go wc.userID). Stamped onto turn-scoped audit entries via
	// auditUser() so CLI runs (principal "cli") and admin sessions are
	// attributable. Empty for channel-originated and unauthenticated turns
	// (Sender.Username, the platform handle, is never read for this).
	userID string
	media  []string

	phase        TurnPhase
	iteration    int
	startedAt    time.Time
	finalContent string

	followUps []bus.InboundMessage

	gracefulInterrupt     bool
	gracefulInterruptHint string
	gracefulTerminalUsed  bool
	hardAbort             bool
	providerCancel        context.CancelFunc
	turnCancel            context.CancelFunc

	// Cancel dedup / callback fields (FR-10, FR-11, FR-15).
	// cancelMu guards cancelFired to make the first-cancel-wins check atomic.
	cancelMu       sync.Mutex
	cancelFired    atomic.Bool               // true once handleCancel has claimed this turn
	abandoned      atomic.Bool               // true once the stuck-watchdog gives up on the goroutine
	onCancelFinish func(cancelMethod string) // called exactly once by Finish when cancelFired

	restorePointHistory []providers.Message
	restorePointSummary string
	persistedMessages   []providers.Message

	// SubTurn support
	depth                int                    // SubTurn depth (0 for root turn)
	parentTurnID         string                 // Parent turn ID (empty for root turn)
	childTurnIDs         []string               // Child turn IDs
	pendingResults       chan *tools.ToolResult // Channel for SubTurn results
	concurrencySem       chan struct{}          // Semaphore for limiting concurrent SubTurns
	isFinished           atomic.Bool            // Whether this turn has finished
	session              session.SessionStore   // Session store reference
	initialHistoryLength int                    // Snapshot of history length at turn start
	// parentSpawnCallID is the ToolCall.ID of the spawn tool call in the parent turn that
	// triggered this sub-turn. Set by spawnSubTurn at child construction (FR-H-003).
	// Empty for root turns. Used to populate ParentSpawnCallID on ToolExec* payloads
	// emitted by this child turn, enabling the WS forwarder to tag frames with parent_call_id.
	parentSpawnCallID string

	// Additional SubTurn fields
	ctx             context.Context    // Context for this turn
	cancelFunc      context.CancelFunc // Cancel function for this turn's context
	critical        bool               // Whether this SubTurn should continue after parent ends
	parentTurnState *turnState         // Reference to parent turnState
	parentEnded     atomic.Bool        // Whether parent has ended
	closeOnce       sync.Once          // Ensures pendingResults channel is closed once
	finishedChan    chan struct{}      // Closed when turn finishes

	// Token budget tracking
	tokenBudget      *atomic.Int64        // Shared token budget counter
	lastFinishReason string               // Last LLM finish_reason
	lastUsage        *providers.UsageInfo // Last LLM usage info

	// Accumulated turn-level stats across all LLM iterations in this turn.
	// Used to populate the "done" WS frame for the session UI (issue #12).
	turnTokens  int64
	turnCostUSD float64
	// Cache token split accumulated across all LLM iterations in this turn.
	// Populated from UsageInfo.CacheReadTokens / CacheWriteTokens so the
	// transcript entry can carry the full breakdown for SessionStats.ByModel.
	turnCacheRead  int
	turnCacheWrite int

	// turnFailed is set to true when the turn ended via the engine's error/limit
	// fallback rather than a real model response.  Three conditions trigger it:
	//   1. The LLM returned an empty response after all retries and the engine
	//      substituted the package-level defaultResponse sentinel.
	//   2. The tool-iteration limit (MaxIterations) was reached without a final
	//      response.
	//   3. The generic empty-content exhaustion path (finalContent=="" at the
	//      bottom of runTurn) resolved to the defaultResponse sentinel — but NOT
	//      when the caller supplied a custom success DefaultResponse (e.g.
	//      "Background task completed." on the heartbeat/system path).
	// Threaded into the DoneStats.TurnFailed field on the done frame so
	// CLI/automation clients can detect failure without parsing message content.
	turnFailed bool

	// Back-reference to the owning AgentLoop (set for SubTurns only, used for hard abort cascade)
	al *AgentLoop

	// Last streamer used during this turn. Finalized once at turn end
	// to send the "done" frame, preventing premature done signals mid-turn.
	lastStreamer bus.Streamer

	// Transcript recording fields (nil transcriptStore disables recording)
	transcriptSessionID string
	transcriptStore     *session.UnifiedStore

	// activeAgentResolver, when non-nil, returns the runtime-current active
	// agent for this session's transcript. It is set at turn construction for
	// webchat turns (where sessionActiveAgent tracks post-handoff overrides).
	// appendToolCallTranscript calls it to tag each entry with the agent that
	// is currently active rather than the one that started the turn — so
	// tool_call entries produced after a handoff (same turn, new active agent)
	// carry the correct agent_id in the transcript.
	activeAgentResolver func() string

	// syntheticErrorCount tracks consecutive synthetic-deny tool results within
	// this turn. When it reaches the configured floor (FR-084,
	// gateway.turn_synthetic_error_floor, default 8), the turn is aborted with
	// a system message {type: "turn_aborted", reason: "synthetic_error_loop"}.
	// The counter resets per turn (initialized to zero here).
	syntheticErrorCount int

	// consecutiveToolFailures counts consecutive execution failures on
	// provisioning-prone tools (exec, workspace_shell, workspace_shell_bg).
	// Resets on any success or non-provisioning tool call. Used by
	// recordToolOutcome to inject a one-time guidance note when the threshold
	// is hit (FR-CBC-001). The counter accumulates across all tool executions
	// within a turn, including multiple tool calls batched in a single model
	// response.
	consecutiveToolFailures int

	// lastProducedModel is the model string that produced the most recent
	// assistant message in this turn. Set after each successful LLM call in
	// loop.go (and external_dispatch.go for CLI providers). The transcript
	// write sites read this to stamp the per-turn Model field on every
	// assistant entry (FR-013).
	//
	// NOTE: written and read on the same goroutine as the LLM call sequence
	// (no cross-goroutine access); no synchronization needed. For the
	// streaming path, setLastProducedModel is called by the streamer wrapper
	// after each chat completes.
	lastProducedModel string
}

// setLastProducedModel stamps the model that produced the most recent
// successful LLM call. Used by transcript writes to attribute the response
// to the correct model (FR-013).
func (ts *turnState) setLastProducedModel(model string) {
	if ts == nil {
		return
	}
	ts.lastProducedModel = model
}

// auditUser returns the authenticated gateway principal for this turn, used to
// stamp audit.Entry.User (FR-017). Returns "" for a nil turnState or a turn
// with no authenticated principal (channel-originated, env-token, dev-bypass) —
// callers leave Entry.User empty in that case rather than guessing.
func (ts *turnState) auditUser() string {
	if ts == nil {
		return ""
	}
	return ts.userID
}

// consecutiveShellFailureLimit is the number of consecutive provisioning-prone
// tool failures (exec, workspace_shell, workspace_shell_bg) that triggers a
// one-time guidance injection into the tool result visible to the model
// (FR-CBC-001). The guidance fires ONCE — at exactly the threshold — to avoid
// prompt spam; subsequent failures above the threshold are silent
// (MaxIterations remains the hard ceiling). Value of 3 matches empirical
// runaway patterns (typically 3 apt/npm-install attempts before the model
// escalates or loop burns budget).
const consecutiveShellFailureLimit = 3

// shellBreakerGuidance is the guidance injected into the tool result content
// when consecutiveShellFailureLimit consecutive provisioning-prone failures are
// detected. It is a constant so the unit test can assert on the exact text.
const shellBreakerGuidance = "\n\n[SYSTEM NOTE] You've had several consecutive shell-command failures. This sandbox blocks installing software (apt/snap/npm are denied by design). Stop trying to install or run external commands to work around it — use the built-in tools (e.g. fetch_url) or tell the user the capability is unavailable in this environment."

// provisioningToolNames is the single source of truth for which tool names are
// considered provisioning-prone for circuit-breaker purposes (FR-CBC-001).
// All three shell execution tools are included: exec (direct subprocess),
// workspace_shell (interactive foreground shell), and workspace_shell_bg
// (background shell). Adding a tool here is sufficient to make the breaker
// track it — no other changes needed.
var provisioningToolNames = map[string]struct{}{
	"exec":               {},
	"workspace_shell":    {},
	"workspace_shell_bg": {},
}

// isProvisioningTool reports whether the named tool is considered
// provisioning-prone for circuit-breaker purposes. The check is a map lookup
// against provisioningToolNames — the single source of truth.
func isProvisioningTool(toolName string) bool {
	_, ok := provisioningToolNames[toolName]
	return ok
}

// recordToolOutcome updates the consecutive-failure counter and returns a
// guidance string (inject=true) the first time the limit is reached. The
// caller must append the guidance to the content sent to the model so the
// LLM sees it in the same tool-result turn that triggered the breach.
//
// The counter advances once per tool execution (including multiple tool calls
// batched in one model response). The per-call shell tool result already
// carries sandbox guidance when the kernel blocks the call; this breaker is a
// backstop — a model receiving the 3rd failure may therefore see both the
// tool's own sandbox guidance and this note (same advice, harmless).
//
// Semantics:
//   - Non-provisioning tool OR success: counter resets to 0 and ("", false)
//     is returned (a success on any provisioning tool also resets).
//   - Provisioning-tool failure: counter increments; all provisioning tools
//     are treated identically so alternating exec/workspace_shell/
//     workspace_shell_bg still accumulates toward the limit.
//   - At exactly consecutiveShellFailureLimit: returns (guidance, true) —
//     fires ONCE.
//   - Above the limit: returns ("", false) — no spam.
func (ts *turnState) recordToolOutcome(toolName string, isError bool) (guidance string, inject bool) {
	if !isError || !isProvisioningTool(toolName) {
		// Success on any tool, OR failure on a non-provisioning tool: reset.
		ts.consecutiveToolFailures = 0
		return "", false
	}
	// Provisioning-tool failure: increment (treat all provisioning tools the
	// same — alternating exec/workspace_shell/workspace_shell_bg still
	// accumulates toward limit).
	ts.consecutiveToolFailures++

	if ts.consecutiveToolFailures == consecutiveShellFailureLimit {
		return shellBreakerGuidance, true
	}
	return "", false
}

func newTurnState(agent *AgentInstance, opts processOptions, scope turnEventScope) *turnState {
	ts := &turnState{
		agent:        agent,
		opts:         opts,
		scope:        scope,
		turnID:       scope.turnID,
		agentID:      agent.ID,
		sessionKey:   opts.SessionKey,
		channel:      opts.Channel,
		chatID:       opts.ChatID,
		userMessage:  opts.UserMessage,
		userID:       opts.UserID,
		media:        append([]string(nil), opts.Media...),
		phase:        TurnPhaseSetup,
		startedAt:    time.Now(),
		finishedChan: make(chan struct{}),
	}

	// Bind session store and capture initial history length for rollback logic
	if agent != nil && agent.Sessions != nil {
		ts.session = agent.Sessions
		ts.initialHistoryLength = len(agent.Sessions.GetHistory(opts.SessionKey))
	}

	// Bind transcript store for persisting tool calls
	ts.transcriptSessionID = opts.TranscriptSessionID
	ts.transcriptStore = opts.TranscriptStore

	return ts
}

func (al *AgentLoop) registerActiveTurn(ts *turnState) {
	al.activeTurnStates.Store(ts.sessionKey, ts)
}

func (al *AgentLoop) clearActiveTurn(ts *turnState) {
	al.activeTurnStates.Delete(ts.sessionKey)
}

func (al *AgentLoop) getActiveTurnState(sessionKey string) *turnState {
	if val, ok := al.activeTurnStates.Load(sessionKey); ok {
		return val.(*turnState)
	}
	return nil
}

// getAnyActiveTurnState returns any active turn state (for backward compatibility)
func (al *AgentLoop) getAnyActiveTurnState() *turnState {
	var firstTS *turnState
	al.activeTurnStates.Range(func(key, value any) bool {
		firstTS = value.(*turnState)
		return false // stop after first
	})
	return firstTS
}

func (al *AgentLoop) GetActiveTurn() *ActiveTurnInfo {
	// For backward compatibility, return the first active turn found
	// In the new architecture, there can be multiple concurrent turns
	var firstTS *turnState
	al.activeTurnStates.Range(func(key, value any) bool {
		firstTS = value.(*turnState)
		return false // stop after first
	})
	if firstTS == nil {
		return nil
	}
	info := firstTS.snapshot()
	return &info
}

// GetActiveAgentIDs returns the IDs of all agents that currently have an active turn.
// Used by the REST API to report real-time agent status.
func (al *AgentLoop) GetActiveAgentIDs() []string {
	seen := make(map[string]struct{})
	al.activeTurnStates.Range(func(_, value any) bool {
		ts := value.(*turnState)
		ts.mu.RLock()
		id := ts.agentID
		ts.mu.RUnlock()
		if id != "" {
			seen[id] = struct{}{}
		}
		return true
	})
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids
}

// TurnCancelHook is the exported interface that the gateway's cancel handler
// uses to interact with an active turn. It exposes only the methods needed
// for the two-stage cancel timer, preventing gateway code from touching
// unexported turnState fields.
type TurnCancelHook interface {
	// IsAlive returns true while the turn has not yet finished.
	IsAlive() bool
	// TurnID returns the turn's unique identifier.
	TurnID() string
	// SetOnCancelFinish registers a callback invoked by Finish() when the turn
	// exits after a cancel. Receives "graceful" or "hard".
	SetOnCancelFinish(fn func(cancelMethod string))
	// ClaimCancel performs the atomic first-cancel-wins check. Returns true
	// if this call is the first to claim the cancel (i.e. cancelFired was false
	// and has now been set to true). Returns false if already canceled.
	ClaimCancel() bool
	// MarkAbandoned sets the abandoned flag so the gateway can stop tracking
	// a stuck goroutine (FR-19, FR-20, FR-21).
	MarkAbandoned()
}

// Compile-time check: *turnState implements TurnCancelHook.
var _ TurnCancelHook = (*turnState)(nil)

// ClaimCancel performs the atomic first-cancel-wins check under cancelMu.
// Returns true if this call successfully set cancelFired from false→true.
func (ts *turnState) ClaimCancel() bool {
	ts.cancelMu.Lock()
	defer ts.cancelMu.Unlock()
	if ts.cancelFired.Load() {
		return false
	}
	ts.cancelFired.Store(true)
	return true
}

// MarkAbandoned sets the abandoned flag. Called by the stuck-watchdog timer
// when the turn goroutine has not exited 5s after the hard-abort signal (FR-21).
func (ts *turnState) MarkAbandoned() {
	ts.abandoned.Store(true)
}

// GetActiveTurnHookForSession returns a TurnCancelHook for the active turn
// belonging to the given transcript session ID, or nil if none is active.
// Used by handleCancel to atomically claim the turn and register the
// post-cancel callback (FR-10, FR-11, FR-15).
//
// H1: When multiple turns share the same transcriptSessionID (a root turn
// plus one or more sub-turns), the root turn (depth==0 / parentTurnID=="")
// is preferred so the cancel handler targets the outermost scope. The first
// match in the sync.Map iteration is returned only as a last-resort fallback
// (defensive; should not occur in normal operation).
func (al *AgentLoop) GetActiveTurnHookForSession(sessionID string) TurnCancelHook {
	var rootMatch *turnState
	var anyMatch *turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts := value.(*turnState)
		if ts.transcriptSessionID != sessionID {
			return true
		}
		if anyMatch == nil {
			anyMatch = ts
		}
		if ts.depth == 0 || ts.parentTurnID == "" {
			rootMatch = ts
			return false // stop — root found
		}
		return true
	})
	if rootMatch != nil {
		return rootMatch
	}
	if anyMatch != nil {
		return anyMatch
	}
	return nil
}

func (al *AgentLoop) GetActiveTurnBySession(sessionKey string) *ActiveTurnInfo {
	ts := al.getActiveTurnState(sessionKey)
	if ts == nil {
		return nil
	}
	info := ts.snapshot()
	return &info
}

func (ts *turnState) snapshot() ActiveTurnInfo {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	return ActiveTurnInfo{
		TurnID:       ts.turnID,
		AgentID:      ts.agentID,
		SessionKey:   ts.sessionKey,
		Channel:      ts.channel,
		ChatID:       ts.chatID,
		UserMessage:  ts.userMessage,
		Phase:        ts.phase,
		Iteration:    ts.iteration,
		StartedAt:    ts.startedAt,
		Depth:        ts.depth,
		ParentTurnID: ts.parentTurnID,
		ChildTurnIDs: append([]string(nil), ts.childTurnIDs...),
	}
}

func (ts *turnState) setPhase(phase TurnPhase) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.phase = phase
}

func (ts *turnState) setIteration(iteration int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.iteration = iteration
}

func (ts *turnState) currentIteration() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.iteration
}

func (ts *turnState) setFinalContent(content string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.finalContent = content
}

func (ts *turnState) finalContentLen() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.finalContent)
}

func (ts *turnState) setTurnCancel(cancel context.CancelFunc) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.turnCancel = cancel
}

func (ts *turnState) setProviderCancel(cancel context.CancelFunc) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.providerCancel = cancel
}

func (ts *turnState) setLastStreamer(s bus.Streamer) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.lastStreamer = s
}

// markLastStreamerProducedModel stamps the model that produced the response
// on the active streamer. The streamer's Finalize writes the assistant
// transcript entry directly (bypassing appendAssistantTranscript); we push
// the model there so the entry carries the per-turn Model field (FR-013).
//
// Uses a type-assertion to an inline interface so bus.Streamer needs no new
// method — non-streaming-transcript streamers (telegram, wecom, sse, manager)
// are untouched; only wsStreamer implements SetProducedModel.
func (ts *turnState) markLastStreamerProducedModel(model string) {
	ts.mu.RLock()
	s := ts.lastStreamer
	ts.mu.RUnlock()
	if pm, ok := s.(interface{ SetProducedModel(model string) }); ok && s != nil {
		pm.SetProducedModel(model)
	}
}

// markLastStreamerTranscriptPersisted tells the active streamer that the agent
// loop has already written this round's narration to the transcript (via
// appendIntermediateAssistantTranscript), so the streamer's own Finalize must
// not write it again. Only the streamer that ends up finalized (the last one)
// matters; marking superseded streamers is harmless (they are never finalized).
//
// Uses a type-assertion to an inline interface so bus.Streamer needs no new
// method — non-streaming-transcript impls (telegram, wecom, sse, manager) are
// untouched; only wsStreamer implements SuppressTranscriptWrite.
func (ts *turnState) markLastStreamerTranscriptPersisted() {
	ts.mu.RLock()
	s := ts.lastStreamer
	ts.mu.RUnlock()
	if sup, ok := s.(interface{ SuppressTranscriptWrite() }); ok {
		sup.SuppressTranscriptWrite()
	}
}

// streamerStatsSetter is an optional interface a Streamer may implement to
// receive turn-end stats (tokens, cost, duration) before Finalize is called.
// The ws streamer uses this to populate the "done" frame so the chat UI shows
// real token counts and cost instead of zeros (issue #12).
type streamerStatsSetter interface {
	SetTurnStats(tokens int64, costUSD float64, duration time.Duration)
}

// streamerFailedSetter is an optional interface a Streamer may implement to
// receive the turn-failed flag before Finalize is called. When implemented,
// finalizeStreamer calls SetTurnFailed(true) whenever the turn ended via the
// engine's error/limit fallback — (1) empty response after retries (engine
// defaultResponse sentinel), (2) tool-iteration limit, or (3) generic
// empty-content exhaustion that resolved to the defaultResponse sentinel
// (excluding caller-supplied success DefaultResponse strings such as the
// heartbeat path's "Background task completed.").
// The done frame carries DoneStats.TurnFailed=true. CLI/automation clients
// read this field to exit non-zero on a failed turn.
type streamerFailedSetter interface {
	SetTurnFailed(failed bool)
}

// markTurnFailed records that this turn ended via the engine's error/limit
// fallback rather than a real model response.  It is called from three sites
// in loop.go: (1) empty-response-after-retry, (2) tool-iteration limit, and
// (3) generic empty-content exhaustion when the caller's DefaultResponse equals
// the engine's own error sentinel (never when a success message is supplied).
// The flag is read by finalizeStreamer and forwarded to the streamer's
// SetTurnFailed before Finalize so it can set DoneStats.TurnFailed on the done
// WS frame.
func (ts *turnState) markTurnFailed() {
	ts.mu.Lock()
	ts.turnFailed = true
	ts.mu.Unlock()
}

// SetFinalContent records the final assistant response on the turnState so
// finalizeStreamer can pass it through to the streamer's Finalize call.
func (ts *turnState) SetFinalContent(content string) {
	ts.mu.Lock()
	ts.finalContent = content
	ts.mu.Unlock()
}

func (ts *turnState) finalizeStreamer(ctx context.Context) {
	// B4: if the turn has been abandoned, suppress the final "done" frame so
	// a stuck goroutine cannot send a spurious done signal to the frontend.
	if ts.abandoned.Load() {
		abandonedWritesSuppressed.Add(1)
		ts.mu.Lock()
		ts.lastStreamer = nil
		ts.mu.Unlock()
		return
	}
	ts.mu.Lock()
	s := ts.lastStreamer
	tokens := ts.turnTokens
	cost := ts.turnCostUSD
	duration := time.Since(ts.startedAt)
	finalContent := ts.finalContent
	failed := ts.turnFailed
	ts.lastStreamer = nil
	ts.mu.Unlock()
	if s != nil {
		if setter, ok := s.(streamerStatsSetter); ok {
			setter.SetTurnStats(tokens, cost, duration)
		}
		if fsetter, ok := s.(streamerFailedSetter); ok {
			fsetter.SetTurnFailed(failed)
		}
		// Pass finalContent so the streamer can persist the assistant message
		// even when its own accumulated-from-token buffer is empty — happens
		// when the WS client disconnects mid-stream and every Update() call
		// silently failed against a closed sendCh, leaving the streamer
		// without any tokens to record.
		if err := s.Finalize(ctx, finalContent); err != nil {
			logger.WarnCF("agent", "Turn-end streaming finalize error", map[string]any{"error": err.Error()})
		}
	}
}

func (ts *turnState) clearProviderCancel(_ context.CancelFunc) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.providerCancel = nil
}

func (ts *turnState) requestGracefulInterrupt(hint string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.hardAbort {
		return false
	}
	ts.gracefulInterrupt = true
	ts.gracefulInterruptHint = hint
	return true
}

func (ts *turnState) gracefulInterruptRequested() (bool, string) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.gracefulInterrupt && !ts.gracefulTerminalUsed, ts.gracefulInterruptHint
}

func (ts *turnState) markGracefulTerminalUsed() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.gracefulTerminalUsed = true
}

func (ts *turnState) requestHardAbort() bool {
	ts.mu.Lock()
	if ts.hardAbort {
		ts.mu.Unlock()
		return false
	}
	ts.hardAbort = true
	turnCancel := ts.turnCancel
	providerCancel := ts.providerCancel
	ts.mu.Unlock()

	if providerCancel != nil {
		providerCancel()
	}
	if turnCancel != nil {
		turnCancel()
	}
	return true
}

func (ts *turnState) hardAbortRequested() bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.hardAbort
}

func (ts *turnState) eventMeta(source, tracePath string) EventMeta {
	snap := ts.snapshot()
	return EventMeta{
		AgentID:    snap.AgentID,
		TurnID:     snap.TurnID,
		SessionKey: snap.SessionKey,
		Iteration:  snap.Iteration,
		Source:     source,
		TracePath:  tracePath,
	}
}

func (ts *turnState) captureRestorePoint(history []providers.Message, summary string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.restorePointHistory = append([]providers.Message(nil), history...)
	ts.restorePointSummary = summary
}

func (ts *turnState) recordPersistedMessage(msg providers.Message) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.persistedMessages = append(ts.persistedMessages, msg)
}

func (ts *turnState) refreshRestorePointFromSession(agent *AgentInstance) {
	history := agent.Sessions.GetHistory(ts.sessionKey)
	summary := agent.Sessions.GetSummary(ts.sessionKey)

	ts.mu.RLock()
	persisted := append([]providers.Message(nil), ts.persistedMessages...)
	ts.mu.RUnlock()

	if matched := matchingTurnMessageTail(history, persisted); matched > 0 {
		history = append([]providers.Message(nil), history[:len(history)-matched]...)
	}

	ts.captureRestorePoint(history, summary)
}

func (ts *turnState) restoreSession(agent *AgentInstance) error {
	ts.mu.RLock()
	history := append([]providers.Message(nil), ts.restorePointHistory...)
	summary := ts.restorePointSummary
	ts.mu.RUnlock()

	agent.Sessions.SetHistory(ts.sessionKey, history)
	agent.Sessions.SetSummary(ts.sessionKey, summary)
	return agent.Sessions.Save(ts.sessionKey)
}

func matchingTurnMessageTail(history, persisted []providers.Message) int {
	maxMatch := min(len(history), len(persisted))
	for size := maxMatch; size > 0; size-- {
		if reflect.DeepEqual(history[len(history)-size:], persisted[len(persisted)-size:]) {
			return size
		}
	}
	return 0
}

func (ts *turnState) interruptHintMessage() providers.Message {
	_, hint := ts.gracefulInterruptRequested()
	content := "Interrupt requested. Stop scheduling tools and provide a short final summary."
	if hint != "" {
		content += "\n\nInterrupt hint: " + hint
	}
	return providers.Message{
		Role:    "user",
		Content: content,
	}
}

// appendToolCallTranscript records a tool call to the session transcript.
// It is a no-op when no transcript store or session ID is configured, or when
// the turn has been marked abandoned (B4: suppresses writes from stuck goroutines).
//
// Bug 1 fix: the AgentID on the entry reflects the runtime-current active agent
// (via activeAgentResolver) rather than the turn's starting agent. This ensures
// that tool_call entries produced after a handoff carry the correct agent_id —
// the new active agent — instead of the one that initiated the turn.
func (ts *turnState) appendToolCallTranscript(tc session.ToolCall) {
	if ts.abandoned.Load() {
		abandonedWritesSuppressed.Add(1)
		return
	}
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return
	}
	agentID := ts.resolveActiveAgentID()
	entry := session.TranscriptEntry{
		ID:        string(tc.ID),
		Type:      session.EntryTypeToolCall,
		AgentID:   agentID,
		Timestamp: time.Now().UTC(),
		ToolCalls: []session.ToolCall{tc},
	}
	if err := ts.transcriptStore.AppendTranscript(ts.transcriptSessionID, entry); err != nil {
		logger.WarnCF("agent", "could not record tool call to transcript",
			map[string]any{"session_id": ts.transcriptSessionID, "tool": tc.Tool, "error": err.Error()})
	}
}

// resolveActiveAgentID returns the runtime-current active agent ID for this
// turn's session. When activeAgentResolver is set (webchat sessions), it
// reflects post-handoff switches that may have occurred during the turn.
// Falls back to the turn's starting agent ID for all other sessions.
//
// Use this — not ts.agentID — in any event payload or log field that should
// attribute work to the agent that is currently active in the session.
func (ts *turnState) resolveActiveAgentID() string {
	if ts.activeAgentResolver != nil {
		if id := ts.activeAgentResolver(); id != "" {
			return id
		}
	}
	return ts.agentID
}

// appendIntermediateAssistantTranscript persists an assistant text segment that
// immediately precedes a round of tool calls within a single turn. It is called
// once per tool-call iteration when the LLM emits both narration text AND tool
// calls in the same response — the text must be recorded BEFORE the tool_call
// entries so the transcript faithfully reflects the interleaved order the user
// saw live.
//
// Tokens and cost are always 0 for intermediate entries to avoid double-counting:
// the turn total is attributed to the final assistant entry written by either
// wsStreamer.Finalize (streaming path) or appendAssistantTranscript (non-streaming).
//
// Bug #416 fix: without this, only the last text segment reached the transcript.
//
// producedModel is the model string that emitted THIS segment. When the
// caller is a streaming path or a sub-agent, the per-message model differs
// from ts.lastProducedModel (a single slot which the parent's LLM call may
// have just overwritten). Pass "" to fall back to ts.lastProducedModel
// for callers that don't have a per-message producer.
func (ts *turnState) appendIntermediateAssistantTranscript(content string, producedModel ...string) {
	if ts.abandoned.Load() {
		abandonedWritesSuppressed.Add(1)
		return
	}
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" || content == "" {
		return
	}
	agentID := ts.resolveActiveAgentID()
	model := ts.lastProducedModel
	if len(producedModel) > 0 && producedModel[0] != "" {
		model = producedModel[0]
	}
	entry := session.TranscriptEntry{
		ID:        uuid.New().String(),
		Role:      "assistant",
		AgentID:   agentID,
		Content:   content,
		Timestamp: time.Now().UTC(),
		Model:     model,
		// Tokens and Cost are intentionally 0 — the turn total is attributed to
		// the final assistant entry only. See appendAssistantTranscript.
	}
	if err := ts.transcriptStore.AppendTranscript(ts.transcriptSessionID, entry); err != nil {
		logger.WarnCF("agent", "could not record intermediate assistant message to transcript",
			map[string]any{"session_id": ts.transcriptSessionID, "error": err.Error()})
	}
}

// appendAssistantTranscript persists a completed assistant text response to the
// session transcript. It is called on the non-streaming path (no wsStreamer) so
// that replay can reconstruct the full conversation even after a WS disconnect.
//
// Bug 3 fix: the wsStreamer.Finalize path already handles streaming responses.
// For non-streaming turns (WS disconnected or channel that never streams), this
// ensures the assistant content reaches transcript.jsonl.
//
// producedModel is the model string that emitted THIS response. Pass ""
// to fall back to ts.lastProducedModel.
func (ts *turnState) appendAssistantTranscript(content string, producedModel ...string) {
	if ts.abandoned.Load() {
		abandonedWritesSuppressed.Add(1)
		return
	}
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" || content == "" {
		return
	}
	agentID := ts.resolveActiveAgentID()
	model := ts.lastProducedModel
	if len(producedModel) > 0 && producedModel[0] != "" {
		model = producedModel[0]
	}
	// Populate token/cost from accumulated turn stats so scheduled and
	// non-websocket turns record real usage, mirroring the wsStreamer.Finalize
	// path (#411). GetTurnStats is safe to call here — the turn is finishing.
	turnTokens, turnCost := ts.GetTurnStats()
	turnCacheRead, turnCacheWrite := ts.GetTurnCacheStats()
	entry := session.TranscriptEntry{
		ID:               uuid.New().String(),
		Role:             "assistant",
		AgentID:          agentID,
		Content:          content,
		Timestamp:        time.Now().UTC(),
		Tokens:           int(turnTokens),
		Cost:             turnCost,
		Model:            model,
		CacheReadTokens:  turnCacheRead,
		CacheWriteTokens: turnCacheWrite,
	}
	if err := ts.transcriptStore.AppendTranscript(ts.transcriptSessionID, entry); err != nil {
		logger.WarnCF("agent", "could not record assistant message to transcript",
			map[string]any{"session_id": ts.transcriptSessionID, "error": err.Error()})
	}
}

// appendErrorTranscript writes a system entry to the JSONL transcript so a
// later session replay can render the error after a page reload. The
// `kind` parameter is the EventKind label that triggered the write
// ("error" for a provider error, "rate_limit" for a rate-limit denial);
// `stage` is the loop stage ("runTurn", "hooks", etc.); `message` is the
// human-readable description.
//
// Used by recordRateLimitDenial and the LLM-call-error paths in loop.go to
// satisfy FR-001 (rate_limit → transcript) and FR-002 (provider error →
// transcript) of docs/internal/specs/phase-1-chat-model-and-errors.md.
//
// Silently no-ops when the turn has been abandoned or when no transcript
// store is wired (matches appendAssistantTranscript's failure semantics — a
// failed transcript write must NOT abort the in-flight turn). Debug-level
// logging is emitted when the no-op fires so an operator can trace why a
// transcript entry was suppressed.
func (ts *turnState) appendErrorTranscript(kind, stage, message string) {
	if ts == nil {
		return
	}
	if ts.abandoned.Load() {
		abandonedWritesSuppressed.Add(1)
		return
	}
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		transcriptSuppressedErrors.Add(1)
		logger.WarnCF(
			"agent",
			"appendErrorTranscript: suppressed (no transcript store wired) — error event will NOT appear in replay",
			map[string]any{
				"event_kind":  kind,
				"stage":       stage,
				"message_len": len(message),
			},
		)
		return
	}
	agentID := ts.resolveActiveAgentID()
	entry := session.TranscriptEntry{
		ID:        uuid.New().String(),
		Type:      session.EntryTypeSystem,
		AgentID:   agentID,
		Content:   message,
		Timestamp: time.Now().UTC(),
		// Status="error" lets the replay path distinguish error entries from
		// informational system entries (e.g. compaction summaries) without
		// parsing the free-text Content.
		Status: "error",
	}
	if err := ts.transcriptStore.AppendTranscript(ts.transcriptSessionID, entry); err != nil {
		logger.WarnCF("agent", "could not record error to transcript",
			map[string]any{
				"session_id": ts.transcriptSessionID,
				"event_kind": kind,
				"stage":      stage,
				"error":      err.Error(),
			})
	}
}

// SubTurn-related methods

// Finish marks the turn as finished and closes the pendingResults channel.
//
// When cancelFired is true (i.e. handleCancel claimed this turn), Finish
// invokes the onCancelFinish callback exactly once with the cancel method
// ("hard" when isHardAbort, "graceful" otherwise). The callback is called
// from within the goroutine that finishes the turn, so it must not block or
// call back into the agent loop with any locks held.
func (ts *turnState) Finish(isHardAbort bool) {
	ts.isFinished.Store(true)

	// Ensure finishedChan exists before entering closeOnce.Do.
	// Finished() is the single point of lazy creation; calling it here
	// guarantees that Finish() and all Finished() callers share the
	// exact same channel instance, eliminating the race where closeOnce.Do
	// used to create a second channel that Finished() would never return.
	ch := ts.Finished()

	// Signal completion exactly once.
	//
	// pendingResults is intentionally NOT closed here. Closing it while
	// deliverSubTurnResult may hold a local copy of the channel reference and be
	// mid-select-send creates an unavoidable runtime-level race between
	// closechan() and chansend() that the race detector flags even when the
	// channel field itself is zeroed under a mutex. Instead we rely on the
	// finishedChan close as the sole stop signal; all consumers of pendingResults
	// use non-blocking select+default (loop.go) or select+Finished() (subturn.go)
	// so they never block waiting for a close. The channel is garbage-collected
	// once all references drop after the turn is finished.
	ts.closeOnce.Do(func() {
		if ch != nil {
			close(ch)
		}
	})

	// If this is a graceful finish (not hard abort), signal to children
	if !isHardAbort && ts.parentTurnState == nil {
		// This is a root turn finishing gracefully
		ts.parentEnded.Store(true)
	}

	// Cancel the turn context
	if ts.cancelFunc != nil {
		ts.cancelFunc()
	}

	// Hard abort cascades to all child turns
	if isHardAbort && ts.al != nil {
		ts.mu.RLock()
		children := append([]string(nil), ts.childTurnIDs...)
		ts.mu.RUnlock()
		for _, childID := range children {
			if val, ok := ts.al.activeTurnStates.Load(childID); ok {
				val.(*turnState).Finish(true)
			}
		}
	}

	// If handleCancel claimed this turn, fire the post-cancel callback exactly
	// once. Swap the callback to nil under the write-lock so that concurrent or
	// repeated Finish calls (e.g. the defer Finish(false) after a hard-abort
	// Finish(true)) cannot invoke it a second time (FR-15).
	if ts.cancelFired.Load() {
		ts.mu.Lock()
		cb := ts.onCancelFinish
		ts.onCancelFinish = nil
		ts.mu.Unlock()
		if cb != nil {
			method := "graceful"
			if isHardAbort {
				method = "hard"
			}
			cb(method)
		}
	}
}

// Finished returns a channel that is closed when the turn finishes.
// finishedChan is always initialized by newTurnState for production turns.
// For ad-hoc test struct literals that skip newTurnState, we lazily create
// the channel here under mu.Lock so that Finish() and all Finished() callers
// always share the same channel instance. The lazy-creation path is the
// single authoritative creator; Finish() calls Finished() before closing,
// ensuring no second channel can be created after the close.
func (ts *turnState) Finished() chan struct{} {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.finishedChan == nil {
		ts.finishedChan = make(chan struct{})
	}
	return ts.finishedChan
}

// IsParentEnded checks if the parent turn has ended
func (ts *turnState) IsParentEnded() bool {
	if ts.parentTurnState == nil {
		return false
	}
	return ts.parentTurnState.isFinished.Load()
}

// IsAlive returns true when the turn has not yet finished. This is the
// complement of isFinished and is used by the cancel watchdog timers in
// handleCancel to decide whether to escalate to the next stage.
func (ts *turnState) IsAlive() bool {
	return !ts.isFinished.Load()
}

// TurnID returns the turn's ID string for use outside the agent package.
func (ts *turnState) TurnID() string {
	return ts.turnID
}

// SetOnCancelFinish registers a callback that Finish() will invoke exactly
// once when cancelFired==true and the turn exits. The callback receives the
// cancel method ("graceful" or "hard"). Calling this more than once replaces
// the previous callback; it must be called before handleCancel calls Finish
// (i.e. before the timers fire).
func (ts *turnState) SetOnCancelFinish(fn func(cancelMethod string)) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.onCancelFinish = fn
}

// GetLastFinishReason returns the last LLM finish_reason
func (ts *turnState) GetLastFinishReason() string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.lastFinishReason
}

// SetLastFinishReason sets the last LLM finish_reason
func (ts *turnState) SetLastFinishReason(reason string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.lastFinishReason = reason
}

// GetLastUsage returns the last LLM usage info
func (ts *turnState) GetLastUsage() *providers.UsageInfo {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.lastUsage
}

// SetLastUsage sets the last LLM usage info
func (ts *turnState) SetLastUsage(usage *providers.UsageInfo) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.lastUsage = usage
}

// AddTurnStats accumulates per-iteration token counts and cost so the
// turn-end "done" frame can surface the full cost of the turn to the UI.
// Safe to call multiple times per turn (once per LLM iteration).
// B4: suppressed when the turn is marked abandoned so stuck goroutines cannot
// inflate cost counters after the operator-visible 5s detach point.
func (ts *turnState) AddTurnStats(tokens int64, costUSD float64) {
	if ts.abandoned.Load() {
		abandonedWritesSuppressed.Add(1)
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.turnTokens += tokens
	ts.turnCostUSD += costUSD
}

// AddTurnCacheStats accumulates cache token counts from a single LLM iteration.
// Must be called alongside AddTurnStats for each LLM call that reports cache usage.
// B4: suppressed when the turn is marked abandoned.
func (ts *turnState) AddTurnCacheStats(cacheRead, cacheWrite int) {
	if ts.abandoned.Load() {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.turnCacheRead += cacheRead
	ts.turnCacheWrite += cacheWrite
}

// GetTurnStats returns the accumulated turn stats.
func (ts *turnState) GetTurnStats() (tokens int64, costUSD float64) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.turnTokens, ts.turnCostUSD
}

// GetTurnCacheStats returns the accumulated cache token split for this turn.
func (ts *turnState) GetTurnCacheStats() (cacheRead, cacheWrite int) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.turnCacheRead, ts.turnCacheWrite
}

// Context helper functions for SubTurn

type turnStateKeyType struct{}

var turnStateKey = turnStateKeyType{}

func withTurnState(ctx context.Context, ts *turnState) context.Context {
	return context.WithValue(ctx, turnStateKey, ts)
}

func turnStateFromContext(ctx context.Context) *turnState {
	ts, _ := ctx.Value(turnStateKey).(*turnState)
	return ts
}

// TurnStateFromContext retrieves turnState from context (exported for tools)
func TurnStateFromContext(ctx context.Context) *turnState {
	return turnStateFromContext(ctx)
}

// spawnToolCallIDKeyType is the context key for the current spawn tool call's ToolCall.ID.
// Injected by loop.go before tool execution so that spawnSubTurn can read it and set
// the child turnState.parentSpawnCallID (FR-H-003).
type spawnToolCallIDKeyType struct{}

var spawnToolCallIDKey = spawnToolCallIDKeyType{}

// withSpawnToolCallID injects the spawn tool call's ToolCall.ID into the context.
// Called by loop.go at each tool dispatch so tools can correlate their execution
// to the parent spawn call.
func withSpawnToolCallID(ctx context.Context, toolCallID string) context.Context {
	return context.WithValue(ctx, spawnToolCallIDKey, toolCallID)
}

// spawnToolCallIDFromContext retrieves the spawn tool call ID from context.
// Returns empty string if not set (i.e., not inside a spawn tool execution).
func spawnToolCallIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(spawnToolCallIDKey).(string)
	return id
}
