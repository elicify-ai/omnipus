package agent

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
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

// transcriptWriteFailures is incremented each time one of this file's four
// transcript writers (appendToolCallTranscript, appendIntermediateAssistantTranscript,
// appendAssistantTranscript, appendErrorTranscript) calls
// UnifiedStore.AppendTranscriptStrict against a session id that does not
// resolve to a real, store-backed session (ADR-057 FR-001/FR-002, W3;
// BDD-03). Before ADR-057, AppendTranscript silently minted an orphan
// session directory for exactly this case and returned nil, so a lost
// transcript write was indistinguishable from a successful one; the four
// call sites already logged a WARN on error, but nothing counted it. This is
// a DIFFERENT failure than transcriptSuppressedErrors (which fires when no
// transcript store is wired at all, i.e. ts.transcriptStore == nil ||
// ts.transcriptSessionID == "") — this counter fires only when the store IS
// wired and the call actually reaches it, but the session id it names does
// not exist.
var transcriptWriteFailures atomic.Uint64

// TranscriptWriteFailures returns the current value of the
// transcript-write-failure counter (ADR-057 FR-001/FR-002). Used by tests
// and operator tooling.
func TranscriptWriteFailures() uint64 {
	return transcriptWriteFailures.Load()
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
	// providerCancel is fired by the graceful cascade (Interrupt — ADR-057
	// FR-041 collapsed the retired InterruptSession into it) to abort the
	// in-flight LLM/provider call immediately; turnCancel is fired by the
	// hard-abort cascade (InterruptSessionHard/requestHardAbort, still live
	// under that name with a mandatory InterruptScope) to tear down the
	// whole turn. For a NATIVE turn these are two genuinely
	// distinct cancel funcs (turnCtx's cancel is a superset of the
	// provider-call's own). For an EXTERNAL-CLI sub-turn
	// (pkg/agent/external_dispatch.go's runExternalCLISubTurn) both slots are
	// set to the exact SAME cancel func — the runner exposes no distinct
	// graceful-stop primitive, so canceling either slot cancels the one
	// runCtx the external CLI's OS child is bound to
	// (exec.CommandContext(runCtx, ...)), killing the process outright either
	// way. That makes firing providerCancel alone already terminal for an
	// external-CLI turn — there is no softer "graceful" stage for this kind
	// of turn to fall back through.
	providerCancel context.CancelFunc
	turnCancel     context.CancelFunc

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
	depth          int                    // SubTurn depth (0 for root turn)
	parentTurnID   string                 // Parent turn ID (empty for root turn)
	childTurnIDs   []string               // Child turn IDs
	pendingResults chan *tools.ToolResult // Channel for SubTurn results
	concurrencySem chan struct{}          // Semaphore for limiting concurrent SubTurns
	isFinished     atomic.Bool            // Whether this turn has finished
	// subTurnRecordPersisted is true once this sub-turn's OWN spawning
	// "delegate"/"spawn" tool-call record (on the PARENT's transcript) has
	// been corrected with the real terminal status/duration — or it has been
	// determined that no correction attempt was needed/possible (no
	// transcript store, no session ID). isFinished flips the instant runTurn
	// returns (its own `defer ts.Finish(false)`), but spawnSubTurn's cleanup
	// defer — which performs the correction via updateToolCallStatusWithRetry,
	// up to ~935ms of retry backoff for async delegation — only runs AFTER
	// that point, once spawnSubTurn itself returns. A reload/replay landing
	// in that window previously read isFinished==true as "safe to trust the
	// persisted record" and served the still-stale async placeholder ack
	// (Status="success", DurationMS≈0) as genuine. IsSubTurnActiveForSpawnCall
	// treats "finished but not yet persisted" as still active so callers
	// withhold a terminal frame/replay snapshot until BOTH are true. Zero
	// value (false) is correct for turns with no parentSpawnCallID too — they
	// are never matched by IsSubTurnActiveForSpawnCall, which requires a
	// non-empty parentSpawnCallID equality match first.
	subTurnRecordPersisted atomic.Bool
	session                session.SessionStore // Session store reference
	initialHistoryLength   int                  // Snapshot of window (GetHistory) length at turn start
	initialArchiveLen      int                  // Snapshot of archive (ReadArchive) line count at turn start — for Skip-preserving rollback
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

	// routingSessionID is ADR-057's D2 identity split (FR-011): the id that
	// answers "which open chat does this turn belong to", inherited VERBATIM
	// through an entire delegation subtree — for a grandchild it equals the
	// ROOT's own session id, not its immediate parent's transcriptSessionID.
	// It is the routing/interrupt-scope key; transcriptSessionID (above)
	// remains the id that answers "which store-backed session does this
	// turn's own state live under" — the two questions used to be answered
	// by the same field, which is exactly what let a delegated child's own
	// transcript writes and its parent's cancel/interrupt scope silently
	// diverge the moment the child got a real, distinct session (D1).
	//
	// Set by newTurnState below to this turn's OWN transcriptSessionID,
	// which is the correct value for every root turn (FR-011: "for a root
	// turn it MUST equal the turn's own session id") and requires no caller
	// action. spawnSubTurn (pkg/agent/subturn.go, ADR-057 U7) is responsible
	// for OVERWRITING this field on the freshly constructed child —
	// `childTS.routingSessionID = parentTS.routingSessionID` — immediately
	// after its own `newTurnState(...)` call, mirroring the existing
	// `childTS.parentTurnState = parentTS` same-package direct-field-set
	// pattern a few lines below that same call (both fields are unexported
	// but pkg/agent is one package, so no accessor is needed). Skipping that
	// overwrite would silently leave a child's routingSessionID equal to its
	// OWN session id instead of the root's — the exact conflation this field
	// exists to end.
	//
	// CLOSED CONSUMER SET (FR-014): this field MUST NOT be read for any
	// purpose other than routing/interrupt scoping — never as a session
	// store key, transcript write target, ownership predicate, approval-grant
	// key, uploads-directory key, tool-manifest bucket, lifecycle-record
	// field, or audit session_id (those all keep using transcriptSessionID
	// above). Within this file the only reads are the three role-B
	// predicates FR-015 names: GetActiveTurnHookForSession,
	// resolveSessionIDByChannelChat and getActiveRootTurnStateForSession.
	// The remaining closed-set readers have all LANDED (U7/U8/U9/U15, this
	// same branch) — do not go looking for unfinished work here: the
	// steering.go role-B predicates (U8), the pre-arm latch keys in
	// subturn.go/cancel_prearm.go (U7/U15), and the WS payload stamping in
	// loop.go (U9) all read routingSessionID today.
	routingSessionID session.RoutingSessionID

	// askPendingToolCalls holds the tool-call IDs for which a "pending"
	// approval placeholder has been written to the transcript by
	// recordAskPendingToolCall (approval_transcript.go), and not yet settled.
	//
	// Its only job is to let appendToolCallTranscript settle that placeholder
	// IN PLACE instead of appending a second entry with the same tool-call ID
	// (which renders as a duplicate card on replay — the defect
	// external_dispatch.go's S1 note records for its own flow). Membership is
	// the cheap pre-check that keeps the read-modify-rewrite off the hot path:
	// a tool call that never went through the ask gate is never in this map, so
	// it takes the plain append with no extra file I/O.
	//
	// sync.Map rather than a plain map under ts.mu: the settle can arrive from
	// the async tool callback goroutine as well as the synchronous loop.
	askPendingToolCalls sync.Map // session.ToolCallID -> struct{}

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

	// Bind session store and capture initial history/archive lengths for rollback.
	// initialArchiveLen is the number of physical lines in the archive at turn
	// start; used by restoreSession and HardAbort to roll back only the messages
	// appended during this turn (Skip-preserving rollback via RollbackAppended).
	if agent != nil && agent.Sessions != nil {
		ts.session = agent.Sessions
		ts.initialHistoryLength = len(agent.Sessions.GetHistory(opts.SessionKey))
		if archived, err := agent.Sessions.ReadArchive(context.Background(), opts.SessionKey); err == nil {
			ts.initialArchiveLen = len(archived)
		} else {
			// ReadArchive failed (new session, missing file, transient I/O error).
			// Use math.MaxInt so that any subsequent RollbackAppended call is a
			// guaranteed no-op (RollbackAppended treats target >= Count as no-op).
			// Falling back to initialHistoryLength (the post-Skip window length,
			// which is SMALLER than the true archive) would cause a rollback to
			// truncate the archive to fewer lines than it already has — silently
			// destroying evicted turns (SC-001 violation).
			logger.WarnCF("agent", "newTurnState: ReadArchive failed; rollback will be no-op for this turn",
				map[string]any{"session_key": opts.SessionKey, "error": err.Error()})
			ts.initialArchiveLen = math.MaxInt
		}
	}

	// Bind transcript store for persisting tool calls
	ts.transcriptSessionID = opts.TranscriptSessionID
	ts.transcriptStore = opts.TranscriptStore

	// ADR-057 FR-011: default routingSessionID to this turn's own session
	// id. Correct as-is for every root turn (byte-identical to today's
	// single-id behavior — AC scenario US-3/AS-1); spawnSubTurn overwrites
	// it post-construction for a delegated child — see routingSessionID's
	// field doc comment above for the full contract.
	ts.routingSessionID = session.RoutingSessionID(ts.transcriptSessionID)

	return ts
}

func (al *AgentLoop) registerActiveTurn(ts *turnState) {
	al.activeTurnStates.Store(ts.sessionKey, ts)
	// Cancel-prearm race fix (pkg/agent/cancel_prearm.go): a cancel that
	// arrived for this turn's identity BEFORE this Store ran (RequestCancel
	// found no active turn and armed a latch instead of silently no-op'ing)
	// must be applied now, at the earliest possible moment the turn is
	// reachable, rather than being lost. Synchronous and unconditional —
	// consumePreArmedCancel is a fast no-op when no latch is armed.
	al.consumePreArmedCancel(ts)
}

func (al *AgentLoop) clearActiveTurn(ts *turnState) {
	// CompareAndDelete, not a bare Delete. A sessionKey CAN be reused by a
	// later, unrelated turn while this one's own cleanup is still unwinding
	// (the concrete case: a native `delegate follow_up` warm-resume reuses
	// its childID verbatim once the prior generation's LifecycleRecord
	// reaches a terminal state — see spawnCorrectiveFollowUp, pkg/tools/
	// delegate.go — and spawnSubTurn deliberately re-Stores the finished
	// childTS under that same key for a further ~935ms after THIS function
	// already ran once during runTurn's own unwind, specifically so
	// IsSubTurnActiveForSpawnCall can still find it — see subturn.go's
	// "Re-register childTS in activeTurnStates" comment). If a new
	// generation's registerActiveTurn lands in that window, a bare
	// Delete(ts.sessionKey) here would unconditionally erase whichever
	// turnState is CURRENTLY stored under that key — which may by then be the
	// new generation's own live, running turnState, not this one. That turn
	// then becomes permanently unreachable to GetActiveTurnHookForSession/
	// Interrupt/InterruptSessionHard/sessionTurnsStillAlive: no Stop
	// click (graceful, hard-abort, or detach) can ever find it again, and it
	// runs unchecked until its own MaxIterations ceiling. CompareAndDelete
	// only removes the entry if it is STILL this exact ts, so a
	// since-registered newer turn sharing the same key is left untouched —
	// mirrors the identical guard orphan_watch.go already uses for
	// al.orphanWatches (fireOrphanForegroundTurnWatch's CompareAndDelete).
	al.activeTurnStates.CompareAndDelete(ts.sessionKey, ts)
	// Design-flaw fix (cancel_prearm.go, turnImminentForIdentity): record
	// that a turn JUST cleared for this identity so a still-true
	// sessionWorker.inTurn (session_worker.go's processTurn stays "in turn"
	// through its own post-clear tail — steering-drain check, typing-stop
	// notify, response-guard/panic-recover defers) is not misread as
	// evidence a DIFFERENT, new turn is imminent for the next
	// armCancelOrFindActiveTurn call that finds nothing registered. See
	// turnSettleGrace's doc comment for the full rationale. Keyed on both
	// identity forms (session id and (channel, chatID)) exactly like
	// consumePreArmedCancel's own preArmKeysForTurn lookup, so either a Web
	// SPA/Tier A session-id cancel or a Tier B channel/chatID cancel sees
	// the same suppression. No-op when al.cancelPreArm is nil (bare
	// turnState-only unit tests that never went through NewAgentLoop).
	al.cancelPreArm.markSettled(time.Now(), preArmKeysForTurn(ts)...)
}

// clearActiveTurnStateEntry performs the compare-and-delete of a turnState
// entry registered under sessionKey: the entry is removed ONLY if it is still
// the given ts, so a newer turnState reusing the same key (a native
// `delegate follow_up` warm-resume — see spawnCorrectiveFollowUp,
// pkg/tools/delegate.go) is left untouched.
//
// This is the spawnSubTurn-side cleanup seam (subturn.go's deferred
// `clearActiveTurnStateEntry(childID, childTS)`), factored out so the
// invariant is testable in isolation: spawnSubTurn's defer is otherwise locked
// inside a function whose full execution requires a delegation dispatch.
// clearActiveTurn (above) performs the SAME compare-and-delete for the
// parent's own ts.sessionKey plus the cancelPreArm bookkeeping that only
// applies to a finished whole turn — use THIS helper when you only need the
// bare map-entry guard (a deferred child cleanup) and clearActiveTurn when you
// are retiring a turn that ran to completion. Mirrors the identical guard
// orphan_watch.go uses for al.orphanWatches (fireOrphanForegroundTurnWatch's
// CompareAndDelete).
func (al *AgentLoop) clearActiveTurnStateEntry(sessionKey string, ts *turnState) {
	al.activeTurnStates.CompareAndDelete(sessionKey, ts)
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
// belonging to the given ROUTING session ID, or nil if none is active. Used
// by handleCancel to atomically claim the turn and register the post-cancel
// callback (FR-10, FR-11, FR-15).
//
// ADR-057 FR-015 (role-B predicate, one of the seven): rebased from
// transcriptSessionID onto routingSessionID. Before D1, a delegated child's
// transcriptSessionID equaled its parent's verbatim (no real session of its
// own), so matching on transcriptSessionID and matching on the routing key
// were indistinguishable. Once a child owns its own real, distinct
// transcriptSessionID, matching on that field here would silently stop
// finding it from a Stop click on the chat's own (routing) session id — the
// exact regression User Story 5 ("A Stop reaches the whole subtree") exists
// to prevent. sessionID stays a plain string (this function's external
// callers, e.g. cancel.go/cancel_prearm.go, are outside this unit's file
// ownership and are not retyped here); the explicit string() conversion at
// the comparison below is where the routing-typed field meets that
// still-string boundary.
//
// H1: When multiple turns share the same routingSessionID (a root turn plus
// one or more sub-turns — always true pre-D1, and still true post-D1 since
// routingSessionID is inherited verbatim through the whole subtree), the
// root turn (depth==0 / parentTurnID=="") is preferred so the cancel handler
// targets the outermost scope. The first match in the sync.Map iteration is
// returned only as a last-resort fallback (defensive; should not occur in
// normal operation).
func (al *AgentLoop) GetActiveTurnHookForSession(sessionID string) TurnCancelHook {
	var rootMatch *turnState
	var anyMatch *turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts := value.(*turnState)
		if string(ts.routingSessionID) != sessionID {
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

// resolveSessionIDByChannelChat walks activeTurnStates for the turnState
// matching (channel, chatID) — preferring the root turn (depth==0 /
// parentTurnID=="") exactly like GetActiveTurnHookForSession — and returns
// its ROUTING session ID, or "" when no active turn currently matches.
//
// ADR-057 FR-015 (role-B predicate, one of the seven): the match itself is
// keyed on (channel, chatID), unaffected by the identity split, but the
// RETURN VALUE is rebased from transcriptSessionID onto routingSessionID.
// This matters precisely when the only match is a non-root descendant (the
// root already finished and cleared from activeTurnStates, a live
// Critical/background delegate remains): returning the descendant's OWN
// (post-D1, real and distinct) transcriptSessionID would hand callers an id
// that GetActiveTurnHookForSession — itself rebased onto routingSessionID —
// can no longer find, silently breaking the two-function chain
// cancel.go/cancel_prearm.go build on top of this one. Returning the
// descendant's routingSessionID instead (which, inherited verbatim, equals
// the root's own id) keeps that chain working.
//
// Shared by RequestCancel's Tier B resolution (cancel.go, a channel carrying
// no SessionID of its own) and armCancelOrFindActiveTurn's re-check
// (cancel_prearm.go) so both use the identical predicate. This is precisely
// the lookup that fails — returns "" — in the pre-registration cancel race:
// a Tier B cancel arriving before any turn exists has no active turnState to
// walk yet, which is why cancel_prearm.go's fallback latch key is
// (channel, chatID) rather than a session id in that case.
func (al *AgentLoop) resolveSessionIDByChannelChat(channel, chatID string) string {
	var rootTS *turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts := value.(*turnState)
		ts.mu.RLock()
		ch := ts.channel
		cid := ts.chatID
		sid := ts.routingSessionID
		depth := ts.depth
		parentID := ts.parentTurnID
		ts.mu.RUnlock()
		if ch == channel && cid == chatID && sid != "" {
			// Prefer the root turn (depth==0 / parentTurnID=="").
			if depth == 0 || parentID == "" {
				rootTS = ts
				return false // stop
			}
			if rootTS == nil {
				rootTS = ts
			}
		}
		return true
	})
	if rootTS != nil {
		return string(rootTS.routingSessionID)
	}
	return ""
}

// getActiveRootTurnStateForSession returns the ROOT turnState (depth==0 /
// parentTurnID=="") matching sessionID's ROUTING session ID, or nil when no
// root turn is currently active for the session — INCLUDING when the only
// resolvable match is a non-root descendant. Unlike
// GetActiveTurnHookForSession (which falls back to ANY match, root-preferring
// but not root-EXCLUSIVE, as a defensive last resort for other callers), this
// NEVER returns a delegate sub-turn.
//
// ADR-057 FR-015 (role-B predicate, one of the seven): rebased from
// transcriptSessionID onto routingSessionID for the same reason as
// GetActiveTurnHookForSession's identical rebase — see that function's doc
// comment. The depth==0/parentTurnID=="" filter below already excludes every
// descendant regardless of which id field feeds it, so for THIS function the
// rebase changes no currently-observable input/output pair; it exists so
// this predicate stays keyed on the same closed-set field as its six
// siblings (FR-014) rather than reintroducing a transcriptSessionID
// comparison that would silently diverge the moment any of them depends on
// this one matching a genuinely-distinct-id descendant in the future.
//
// Used exclusively by the orphan-foreground-turn watchdog (ADR-045,
// pkg/agent/orphan_watch.go) to answer "is there still a genuine foreground
// turn to reap" without ever mistaking a surviving Critical/background
// delegate — whose parent root has already finished and been cleared from
// activeTurnStates via clearActiveTurn (loop.go) — for one. Reusing
// GetActiveTurnHookForSession's anyMatch fallback for that decision was the
// root cause of MA-1: it would resolve the delegate as "the turn to reap",
// and handing that to RequestCancel would trigger RequestCancel's
// session-wide escalation against the exact turn ADR-045 exists to protect.
func (al *AgentLoop) getActiveRootTurnStateForSession(sessionID string) *turnState {
	var root *turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts := value.(*turnState)
		if string(ts.routingSessionID) != sessionID {
			return true
		}
		if ts.depth == 0 || ts.parentTurnID == "" {
			root = ts
			return false
		}
		return true
	})
	return root
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

// stampStreamerProducerAgentID stamps the TRUE per-turn producer (ts.agent.ID)
// onto a freshly-obtained streamer, before any token can flow through it.
//
// FIX 5a: without this, a streaming-capable streamer's own "active session
// agent" guess (computed by the channel Manager/WSHandler at GetStreamer
// time, from session metadata) leaks into both the live TokenFrame.AgentId
// and the streamer's own Finalize transcript entry. That guess is correct
// for an ordinary turn, but wrong for a background/delegated sub-turn: per
// ADR-032 (no inheritance from the parent), the delegate runs as its own
// identity, never the parent's, so the session's "active" (parent) agent and
// this specific turn's real producer (ts.agent.ID) can legitimately differ.
//
// Uses a type-assertion to an inline interface so bus.Streamer needs no new
// method — non-webchat streamers (telegram, wecom, sse) are untouched; only
// wsStreamer implements SetProducerAgentID.
func (ts *turnState) stampStreamerProducerAgentID(streamer bus.Streamer) {
	if pas, ok := streamer.(interface{ SetProducerAgentID(agentID string) }); ok && ts.agent != nil {
		pas.SetProducerAgentID(ts.agent.ID)
	}
}

// stampStreamerTurnID stamps this turn's own ID onto a freshly-obtained
// streamer, before any token can flow through it. Mirrors
// stampStreamerProducerAgentID exactly.
//
// FIX 5c/1: without this, the assistant transcript entry Finalize writes
// carries no TurnID at all — confirmed via live verification to break BOTH
// the frontend's turn_canceled -> assistant-message replay correlation
// (chatTurnCanceledNoMatch fires on every reload after a mid-stream cancel)
// and MarkLastEntryTruncated's own turn-scoped backward-walk matching
// (pkg/session/unified.go), silently disabling the Truncated flag for every
// real cancel.
//
// Uses a type-assertion to an inline interface so bus.Streamer needs no new
// method — non-webchat streamers (telegram, wecom, sse) are untouched; only
// wsStreamer implements SetTurnID.
func (ts *turnState) stampStreamerTurnID(streamer bus.Streamer) {
	if tid, ok := streamer.(interface{ SetTurnID(turnID string) }); ok {
		tid.SetTurnID(ts.turnID)
	}
}

// stampStreamerParentSpawnCallID stamps this turn's parentSpawnCallID (empty
// for a root/non-delegated turn) onto a freshly-obtained streamer, before any
// token can flow through it. Mirrors stampStreamerTurnID exactly.
//
// A delegated child sub-turn streams through the SAME wsStreamer/wsConn
// machinery as any other turn (it shares its parent's chatID —
// spawnSubTurn's opts.ChatID: parentTS.chatID), so the assistant-text entry
// wsStreamer.Finalize persists must carry the same ParentSpawnCallID
// correlation that appendIntermediateAssistantTranscript/
// appendAssistantTranscript already stamp for the child's non-streaming
// writes — otherwise a delegate's OWN final streamed response (the common
// case: multi-step delegations stream their last round) would round-trip
// through Finalize with no way for pkg/gateway/replay.go to tell it apart
// from a genuine top-level parent message. See
// session.TranscriptEntry.ParentSpawnCallID's doc comment for the full
// root-cause writeup.
//
// Uses a type-assertion to an inline interface so bus.Streamer needs no new
// method — non-webchat streamers (telegram, wecom, sse) are untouched; only
// wsStreamer implements SetParentSpawnCallID.
func (ts *turnState) stampStreamerParentSpawnCallID(streamer bus.Streamer) {
	if pid, ok := streamer.(interface {
		SetParentSpawnCallID(parentSpawnCallID string)
	}); ok {
		pid.SetParentSpawnCallID(ts.parentSpawnCallID)
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

// streamOwnershipReleaser is an optional interface a Streamer may implement
// to release a live-stream ownership claim (see gateway's
// WSHandler.streamOwners doc comment) without performing the rest of
// Finalize's work — sending the done frame, persisting the transcript
// entry. finalizeStreamer's B4 abandoned-turn early return deliberately
// skips the full Finalize call so a stuck goroutine cannot send a spurious
// done signal to the frontend, but it must still relinquish any live-stream
// ownership claim the streamer holds: without this, a background delegate
// that became the live owner for a chatID and was later MarkAbandoned()'d
// by cancel.go's PHASE C left that chatID permanently shadowed — no other
// release path runs for an abandoned turn (Finalize never fires; there's no
// TTL or sweep on the gateway side either). Confirmed as a critical,
// unanimous finding across a 7-reviewer gate.
type streamOwnershipReleaser interface {
	ReleaseStreamOwnership()
}

func (ts *turnState) finalizeStreamer(ctx context.Context) {
	// B4: if the turn has been abandoned, suppress the final "done" frame so
	// a stuck goroutine cannot send a spurious done signal to the frontend.
	if ts.abandoned.Load() {
		abandonedWritesSuppressed.Add(1)
		ts.mu.Lock()
		s := ts.lastStreamer
		ts.lastStreamer = nil
		ts.mu.Unlock()
		// Even though the full Finalize (done frame + transcript write) is
		// skipped above, this streamer may already hold a live-stream
		// ownership claim for its chatID — claimed on its first Update()
		// call, before abandonment occurred. Release it here so a later,
		// unrelated turn on the same chatID is not shadowed forever.
		if s != nil {
			if releaser, ok := s.(streamOwnershipReleaser); ok {
				releaser.ReleaseStreamOwnership()
			}
		}
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
	summary := ts.restorePointSummary
	targetLen := ts.initialArchiveLen
	initialHistLen := ts.initialHistoryLength
	ts.mu.RUnlock()

	// Compute the Skip value at turn start.
	// targetSkip = initialArchiveLen - initialHistoryLength derives the Skip
	// cursor that was in effect before this turn began. If windowTrim advanced
	// Skip mid-turn and the turn is now aborting, restoring Skip to this value
	// ensures GetHistory returns exactly the pre-turn live window (SC-001, SC-010).
	// Guard: when initialArchiveLen == math.MaxInt (ReadArchive failed at turn
	// start, rollback is a no-op), pass targetSkip=0 — it is irrelevant because
	// targetLen=MaxInt means RollbackAppended exits early before touching Skip.
	targetSkip := 0
	if targetLen != math.MaxInt {
		targetSkip = targetLen - initialHistLen
		if targetSkip < 0 {
			targetSkip = 0
		}
	}

	// Rollback: truncate the archive back to the line count captured at turn
	// start AND restore meta.Skip to its turn-start value so mid-turn evictions
	// are undone. SetHistory is explicitly NOT used here — it would overwrite
	// the entire JSONL file and reset Skip=0, permanently deleting any evicted
	// turns that preceded this turn (CRITICAL 1, path 2).
	agent.Sessions.RollbackAppended(ts.sessionKey, targetLen, targetSkip)
	agent.Sessions.SetSummary(ts.sessionKey, summary)

	// M4 mirror: verify the rollback actually took effect. RollbackAppended is
	// fire-and-forget (no error return). Re-read the archive and confirm the
	// length dropped to <= targetLen. Skip verification when targetLen is
	// math.MaxInt (ReadArchive failed at turn start — rollback was a no-op by
	// design, so there is nothing meaningful to verify).
	if targetLen != math.MaxInt {
		if postArchive, readErr := agent.Sessions.ReadArchive(context.Background(), ts.sessionKey); readErr == nil {
			if len(postArchive) > targetLen {
				logger.ErrorCF("agent", "restoreSession: rollback did not shrink archive to target",
					map[string]any{
						"session_key": ts.sessionKey,
						"target":      targetLen,
						"after":       len(postArchive),
					})
				return fmt.Errorf(
					"restoreSession: RollbackAppended did not take effect (archive len %d > target %d)",
					len(postArchive),
					targetLen,
				)
			}
		}
		// If ReadArchive itself fails here, we can't verify — fall through and
		// let Save() persist whatever state the backend is in. The caller
		// (interrupt handler) logs the restoreSession error if we return one.
	}

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

// warnAbandonedTranscriptWrite emits the ADR-057 FR-003/BDD-04 WARN record
// for the ts.abandoned write-suppression branch shared by all four
// transcript writers below, naming the session id and the suppression
// reason. `[grill C-2]` The abandonedWritesSuppressed counter already exists
// and already increments at each of these four sites (and three more
// outside this file) — this call adds only the previously-missing log
// record; the counter's own existing behavior is untouched by this change.
func (ts *turnState) warnAbandonedTranscriptWrite(writer string) {
	logger.WarnCF("agent", "transcript write suppressed: turn marked abandoned",
		map[string]any{"session_id": ts.transcriptSessionID, "writer": writer, "reason": "abandoned"})
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
		ts.warnAbandonedTranscriptWrite("appendToolCallTranscript")
		return
	}
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return
	}

	// Approval-gate settle (approval_transcript.go): when this call previously
	// wrote a "pending" placeholder because it blocked on human approval,
	// REPLACE that entry rather than appending a second one with the same ID.
	// Only ever true for ask-policy calls that actually reached the approver,
	// so every other tool call skips straight to the append below.
	//
	// The placeholder itself arrives here with Status "pending" and must not
	// try to replace itself, hence the status guard. A failed replacement falls
	// through to the append — a duplicate entry is a far better outcome than a
	// lost record of what the tool did.
	if tc.Status != toolCallStatusPending {
		if _, hadPending := ts.askPendingToolCalls.LoadAndDelete(tc.ID); hadPending {
			if replaceToolCallInTranscript(ts, tc.ID, toolCallStatusPending, tc) {
				return
			}
		}
	}

	agentID := ts.resolveActiveAgentID()
	entry := session.TranscriptEntry{
		ID:        string(tc.ID),
		Type:      session.EntryTypeToolCall,
		AgentID:   agentID,
		Timestamp: time.Now().UTC(),
		ToolCalls: []session.ToolCall{tc},
	}
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		transcriptWriteFailures.Add(1)
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
		ts.warnAbandonedTranscriptWrite("appendIntermediateAssistantTranscript")
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
		// TurnID: without this, neither MarkLastEntryTruncated's own
		// turn-scoped backward-walk (H2 fix — it matches on e.TurnID ==
		// turnID) nor the frontend's turn_canceled -> assistant-message
		// replay correlation can ever match a REAL entry; both were only
		// ever exercised by tests that hand-seed TurnID directly. See
		// appendAssistantTranscript's identical fix for the full rationale.
		TurnID: ts.turnID,
		// ParentSpawnCallID: non-empty only when ts is a CHILD delegation
		// sub-turn (spawnSubTurn stamps childTS.parentSpawnCallID before any
		// turn processing runs). Lets pkg/gateway/replay.go withhold this
		// entry from top-level replay, matching live rendering — see
		// session.TranscriptEntry.ParentSpawnCallID's doc comment for the
		// full root-cause writeup (live/reload bubble-count divergence on
		// multi-step delegation).
		ParentSpawnCallID: ts.parentSpawnCallID,
		// Tokens and Cost are intentionally 0 — the turn total is attributed to
		// the final assistant entry only. See appendAssistantTranscript.
	}
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		transcriptWriteFailures.Add(1)
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
		ts.warnAbandonedTranscriptWrite("appendAssistantTranscript")
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
		ID:      uuid.New().String(),
		Role:    "assistant",
		AgentID: agentID,
		Content: content,
		// TurnID stamps this entry with its own producing turn. This is
		// THE fix for the confirmed live-verification bug: before this,
		// TurnID was set on the turn_canceled entry (cancel.go) but NEVER on
		// the assistant entry it describes, so (1) the frontend's
		// turn_canceled -> assistant-message replay correlation could never
		// match a real entry (chatTurnCanceledNoMatch always fired on
		// reload), and (2) MarkLastEntryTruncated's own turn-scoped
		// backward-walk (which requires e.TurnID == turnID) could never
		// match a real entry either, silently disabling the Truncated flag
		// for every real cancel. Both were only ever exercised by tests that
		// hand-seed TurnID directly on the entry.
		TurnID:           ts.turnID,
		Timestamp:        time.Now().UTC(),
		Tokens:           int(turnTokens),
		Cost:             turnCost,
		Model:            model,
		CacheReadTokens:  turnCacheRead,
		CacheWriteTokens: turnCacheWrite,
		// ParentSpawnCallID: see appendIntermediateAssistantTranscript's
		// identical stamp for the full rationale — non-empty only for a
		// child delegation sub-turn's own final-turn text.
		ParentSpawnCallID: ts.parentSpawnCallID,
	}
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		transcriptWriteFailures.Add(1)
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
		ts.warnAbandonedTranscriptWrite("appendErrorTranscript")
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
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		transcriptWriteFailures.Add(1)
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
