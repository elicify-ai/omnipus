package agent

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// abandonedWritesSuppressed is incremented each time a write (transcript
// append, frame emit, or cost accumulation) is skipped because the turn has
// been marked abandoned. Exposed via AbandonedWritesSuppressed() for tests
// and operator tooling (omnipus_abandoned_writes_suppressed_total).
var abandonedWritesSuppressed atomic.Int64

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
	// TurnPhaseParked mirrors TurnEndStatusParked (events.go) at the
	// turnState.phase granularity: the turn stopped because a
	// message_parent(question, wait=true) call parked this session in
	// needs_input. Set immediately before runTurn's park early-return so any
	// introspection reading ActiveTurnInfo.Phase mid-unwind sees the real
	// reason rather than "aborted" or a stale "tools".
	TurnPhaseParked TurnPhase = "parked"
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
	// goalDeferredAdjudication is JUDGE-FR-098's deferred-dispatch payload
	// (ADR-084 revision 9 D13, wave E13): checkGoalLoopAfterTurn
	// (goal_loop.go) populates this instead of calling runGoalAdjudication
	// synchronously when a turn resolves a `met` claim. nil means no
	// claim-triggered adjudication is pending. runAgentLoop (loop.go)
	// dispatches it, in a goroutine, strictly AFTER bus.PublishOutbound of
	// this turn's own finalContent — see goalDeferredAdjudicationWork's own
	// doc comment (goal_loop.go) for the full contract.
	goalDeferredAdjudication *goalDeferredAdjudicationWork
}

type turnState struct {
	mu sync.RWMutex

	agent *AgentInstance
	opts  processOptions
	scope turnEventScope

	turnID     string
	agentID    string
	sessionKey string
	// generation is the session's LifecycleRecord.Generation at the moment
	// this turn was registered (ADR-091 I-3 reconstruction /
	// I-6 revival). Zero for a turnState built outside reconstruction (a
	// bare unit-test fixture, or a pre-ADR-091 turn) — requestCancelForGeneration
	// treats a zero-vs-zero match the same as any other match, so a caller
	// that never sets this field (today's non-steered turns) keeps working
	// exactly as before: nothing compares against a real generation.
	generation int

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
	abandoned      atomic.Bool               // true once a controller detaches a stuck turn goroutine
	onCancelFinish func(cancelMethod string) // called exactly once by Finish when cancelFired

	// cancelling was the GATE half of the chain-reaction cancellation fix
	// (ADR-057 FR-024, superseded 2026-08-04): set true by markTurnsCancelling
	// (steering.go) for every turn Interrupt/InterruptSessionHard resolves as
	// a target — the ANCHOR and every currently-known live descendant — as
	// the VERY FIRST thing either function does, before any interrupt signal
	// is actually fired. Pre-ADR-091, the deleted spawnSubTurn walked
	// parentTS's own ancestor chain via parentTurnState, checking THIS flag
	// at every level, before creating a new child; any hit refused the spawn
	// outright (the also-deleted ErrSessionCancelling — zero definitions
	// repo-wide today).
	//
	// ADR-091 fix lane RX-SUBTURN note (comment-only; code unchanged): grep
	// finds markTurnsCancelling still calls cancelling.Store(true) (write
	// side, live), but no call to cancelling.Load() anywhere in the repo —
	// nothing currently reads this flag before minting a new steered child
	// (steer_launcher.go's launchSteered checks the PERSISTED Stop marker on
	// the parent's LifecycleRecord instead, which is a related but distinct
	// mechanism with different timing). Whether that closes the same race
	// this flag existed for is outside a comment-only lane's scope to
	// determine — flagged for the team, not fixed here.
	//
	// This existed because recursion (re-scanning/re-arming for a child that
	// ALREADY registered, or is ALREADY marked as about to via
	// pendingSpawns) fixes the ORDER cancellation reaches existing/imminent
	// descendants but cannot, by itself, stop a BRAND NEW child from being
	// born after cancellation has begun: the child's own context was
	// deliberately NOT derived from the parent's (the deleted spawnSubTurn's
	// childCtx was context.WithTimeout(context.Background(), ...) so a
	// Critical async delegate could outlive its parent's own graceful
	// finish — re-parenting it would have broken that), so Go's ordinary
	// context-cancellation propagation gave no signal here at all. This flag
	// was that signal, checked
	// explicitly at the one place a new child is actually created.
	//
	// Never explicitly cleared: each turnState is a fresh object per turn
	// generation (newTurnState), so there is nothing to reset — a later,
	// unrelated message in the same session constructs a brand-new root
	// turnState (parentTurnState==nil, cancelling's zero value false), which
	// the ancestor walk never even reaches. No TTL, no registry, no
	// possibility of permanently "bricking" a session's ability to delegate.
	cancelling atomic.Bool

	// initialEmptiedSet is the session's WHOLE projection set
	// ((tool_call_id, archive_line) → capped | emptied) as of turn start —
	// the third member of the turn-start restore triple beside
	// initialArchiveLen and initialHistoryLength (ADR-066 FR-020). Captured
	// once in newTurnState and never moved during the turn; restoreSession
	// and HardAbort hand it to RollbackAppended so an aborted turn's
	// emptying is undone together with its archive tail and its Skip
	// advance. nil when the store had nothing projected (or no store).
	initialEmptiedSet memory.ProjectionSet
	// emptiedTranscriptPrev holds, for every transcript tool_call record the
	// D5 pass rewrote during this turn (content_state emptied + projected
	// result), the record's PREVIOUS state — so an abort can put the
	// transcript back in step with the rolled-back window. Guarded by mu.
	emptiedTranscriptPrev []session.ToolCallProjectionUpdate

	// SubTurn support
	depth          int                    // SubTurn depth (0 for root turn)
	parentTurnID   string                 // Parent turn ID (empty for root turn)
	childTurnIDs   []string               // Child turn IDs
	pendingResults chan *tools.ToolResult // Channel for SubTurn results
	concurrencySem chan struct{}          // Semaphore for limiting concurrent SubTurns
	isFinished     atomic.Bool            // Whether this turn has finished
	// finishedByHardAbort distinguishes a parent's hard-abort cascade from a
	// normal Finish(false). Both set isFinished, but only the former makes a
	// child's terminal cancellation an interruption caused by its parent.
	finishedByHardAbort  atomic.Bool
	session              session.SessionStore // Session store reference
	initialHistoryLength int                  // Snapshot of window (GetHistory) length at turn start
	initialArchiveLen    int                  // Snapshot of archive (ReadArchive) line count at turn start — for Skip-preserving rollback

	// injectedRecallSpan is the recall span whose messages are currently
	// present in this turn's in-memory message slice (ADR-066 D5.4,
	// FR-043), compared by IDENTITY against al.activeRecallSpan. It is set
	// by assembleMessages (every from-scratch assembly includes the active
	// span once via BuildMessages) and by the tool-result-site splice
	// (recall_injection.go). The tool-result site never splices a span
	// that is already recorded here, so nothing is doubled; nil means no
	// span is in the slice. injectedRecallAt/injectedRecallLen locate that
	// block — [at, at+len) — so a second recall in the same turn (E20)
	// removes the replaced span before splicing the new one. Both the
	// splice and every reassembly reset the triple; appends only ever land
	// at the end of the slice, so the block stays valid in between.
	injectedRecallSpan *RecallSpan
	injectedRecallAt   int
	injectedRecallLen  int
	// parentSpawnCallID is the ToolCall.ID of the spawn tool call in the parent turn that
	// triggered this sub-turn. Pre-ADR-091 this was set by the deleted spawnSubTurn at
	// child construction (FR-H-003); ADR-091 deleted subturn.go, its only writer, and
	// nothing assigns this field today — confirmed elsewhere in the repo
	// (pkg/gateway/replay.go's "FIX (finding 1, CRITICAL)" doc comment and
	// pkg/gateway/websocket_replay.go's "Finding 1 fix": both call out
	// "turnState.parentSpawnCallID... never assigned" / "had zero real callers" and
	// describe the working replacement, buildPersistedSubagentSpanIndexes/
	// classifyToolCall in replay.go). Every read of ts.parentSpawnCallID in this
	// package (loop_run_turn_tools.go, turn_transcript.go, turn_stream.go) therefore
	// reads the permanent zero value "" today. Flagged for the team rather than
	// changed here (comment-only lane) — related: withSpawnToolCallID (below) still
	// injects a spawn tool call's ID into ctx at each tool dispatch (loop_run_turn_tools.go),
	// but spawnToolCallIDFromContext, the only reader that would turn that into a
	// parentSpawnCallID assignment, has zero callers outside tests — the value is
	// injected and never consumed in production.
	parentSpawnCallID string

	// Additional SubTurn fields
	ctx             context.Context    // Context for this turn
	cancelFunc      context.CancelFunc // Cancel function for this turn's context
	critical        bool               // Whether this SubTurn should continue after parent ends
	parentTurnState *turnState         // Reference to parent turnState
	parentEnded     atomic.Bool        // Whether parent has ended
	closeOnce       sync.Once          // Ensures pendingResults channel is closed once
	finishedChan    chan struct{}      // Closed when turn finishes

	lastUsage *providers.UsageInfo // Last LLM usage info

	// ADR-087 D6.1: the turn-scoped auto-continue accumulator — the
	// load-bearing object for a truncated answer that gets one or more
	// bounded continuation rounds. Guarded by mu like every other field
	// here.
	//
	// continuationAccum is the concatenation of every round's own content
	// produced while runTurn's D4/D6 branch (loop.go's
	// evaluateTruncatedSuccess) has been handling a truncated-with-no-tool-
	// calls response — updated on EVERY entry into that branch, whether or
	// not the round goes on to actually continue. It is the value
	// finalizeStreamer hands the streamer via SetContinuationContent (when
	// hadContinuation() is true) and what D4b annotates as the turn's final
	// content.
	//
	// continuationRounds counts how many times a continuation was actually
	// DISPATCHED (D6.3's bound of 2) — never cleared, so hadContinuation()
	// (continuationRounds > 0) stays true for the rest of the turn once at
	// least one continuation has fired, even after the chain resolves. This
	// is deliberately different from continuationPending (below): the
	// streamer still needs to render the FULL accumulated answer for the
	// whole rest of turn finalization, not just while a round is mid-flight.
	//
	// continuationPending is true only from the moment a continuation is
	// dispatched until the chain resolves — a later round completing
	// normally (clears via resolveContinuation, called both from a
	// non-truncated/tool-calls response and from D4a/D4b's own
	// resolution). preserveTruncatedAccumulator (loop.go, D6.8) gates on
	// THIS field, not on continuationRounds: without the distinction, an
	// unrelated terminal exit many iterations after an already-resolved
	// continuation chain would wrongly re-surface stale partial content.
	continuationAccum   string
	continuationRounds  int
	continuationPending bool

	// currentMessageID is the id #823's live/persisted unification hangs
	// on: the SAME value goes onto this round's live streamed frames
	// (TokenFrame.message_id / DoneFrame.message_id, stamped via
	// turn_stream.go::stampStreamerMessageID) and onto the transcript entry
	// persisted for that SAME round (turn_transcript.go's
	// appendIntermediateAssistantTranscript, via roundMessageIDOrNew). Minted
	// fresh by turn_stream.go::nextRoundMessageID at the top of every LLM
	// call round UNLESS continueSameMessageID says this round continues the
	// PREVIOUS round's message. Guarded by mu like every other field here.
	currentMessageID string

	// continueSameMessageID is a ONE-SHOT flag consumed by
	// nextRoundMessageID (turn_stream.go): true only for the single round
	// immediately following a markContinuationDispatched call, so that round
	// reuses currentMessageID instead of minting a fresh one — the ADR-087
	// D6 auto-continue case, where the model is finishing the SAME answer,
	// not starting a new one.
	//
	// Deliberately DISTINCT from continuationPending, which also covers the
	// D4 "truncated with complete tool calls" carve-out
	// (markContinuationPending): that case executes the tool calls before
	// the next round runs, so the round after it IS a new message even
	// though continuationPending stays true until that round's own
	// evaluateTruncatedSuccess call resolves it. Reusing continuationPending
	// here would wrongly carry the OLD message id across a tool-call
	// boundary. Set by markContinuationDispatched, cleared by
	// resolveContinuation and by nextRoundMessageID's own one-shot consume.
	continueSameMessageID bool

	// truncationReason carries ADR-087 D2's narrow enum ("max_output_tokens"
	// — the only value this package ever writes; "cancelled" is cancel.go's
	// own, unrelated writer) for a turn whose final content was annotated by
	// D4a/D4b or preserved mid-continuation by D6.8. Empty means no
	// truncation annotation is pending for this turn's transcript entry.
	truncationReason string

	// Accumulated turn-level stats across all LLM iterations in this turn.
	// Used to populate the "done" WS frame for the session UI (issue #12).
	turnTokens  int64
	turnCostUSD float64
	// Cache token split accumulated across all LLM iterations in this turn.
	// Populated from UsageInfo.CacheReadTokens / CacheWriteTokens so the
	// transcript entry can carry the full breakdown for SessionStats.ByModel.
	turnCacheRead int
	// turnPromptTokens/turnCompletionTokens carry the provider's input/output
	// split, which turnTokens (a pre-collapsed total) cannot express.
	turnPromptTokens     int
	turnCompletionTokens int
	turnCacheWrite       int

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

	// goalNarrowMisses is ADR-088 D3's bounded-escape counter (the D3
	// amendment, 2026-09-08): the number of CONSECUTIVE LLM requests this
	// turn for which evaluateGoalForcing (loop.go) has offered the narrowed
	// {set_goal[, AskUserQuestion]} first-move door while the base predicate
	// (goalTurnRecordState) still held. It is bumped once per narrowed
	// offering, BEFORE that request's outcome is known — a request that
	// instead finds the record already written (a prior request's set_goal
	// succeeded) or that parks the turn (a genuine AskUserQuestion card)
	// never reaches the bump, because goalTurnRecordState/the turn-ending
	// park short-circuit evaluateGoalForcing first. Once the counter exceeds
	// goalForcingMaxNarrowAttempts, evaluateGoalForcing arms
	// goalNarrowEscaped instead of narrowing that (and every later) request.
	// Zero value is correct: each turnState is fresh per turn generation, so
	// there is nothing to reset between turns.
	goalNarrowMisses int
	// goalNarrowEscaped is true once ADR-088 D3's bounded escape has fired
	// for this turn — evaluateGoalForcing then offers the FULL tool surface
	// for the remainder of the turn even though the base predicate may still
	// hold (a persistently empty record against a model that keeps failing
	// or ignoring the narrowed pair). This is the fix for the real-world
	// defect reproduced 2026-09-08: a /goal set at 11:54:48Z narrowed
	// iteration 1 to {set_goal, AskUserQuestion}; the model's AskUserQuestion
	// call FAILED schema validation ("unexpected property \"recommended\""
	// inside an option); because narrowing used to apply to iteration 1
	// ONLY, iteration 2 got the full tool surface back with the record still
	// empty, and the agent ran ToolSearch/write_file×5/bash/serve_web/
	// browser_navigate for ~17 minutes before finally calling set_goal at
	// 12:12:26Z — the post-turn correction (checkGoalLoopAfterTurn,
	// goal_loop.go) never got a chance to run because the turn never ended.
	// Narrowing now persists across iterations while the predicate holds;
	// this flag is the escape valve so a persistently-failing model cannot
	// wedge the turn in the narrowed pair for its whole MaxIterations
	// budget instead. Never cleared once set.
	goalNarrowEscaped bool

	// Back-reference to the owning AgentLoop, used by Finish's hard-abort
	// cascade over childTurnIDs. Set on exactly ONE path today — the task
	// executor's external-CLI turn
	// (task_executor_run.go::processTaskDirectExternalCLI); the
	// sub-turn path that used to set it is deleted (ADR-091), and a steered
	// session's turn (steer_reconstruct.go::reconstructSteeredTurn) is a
	// standalone turn with no child turns and leaves it nil. Nothing may
	// depend on this field being set: it is nil for every steered turn, which
	// is why the admission slot is released at the dispatch site rather than
	// through here (see turn_exit.go::Finish).
	al *AgentLoop

	// Last streamer used during this turn. Finalized once at turn end
	// to send the "done" frame, preventing premature done signals mid-turn.
	lastStreamer bus.Streamer

	// Transcript recording fields (nil transcriptStore disables recording)
	transcriptSessionID string
	transcriptStore     *session.UnifiedStore

	// pendingFallbackNotes holds this turn's queued fallback-note transcript
	// entries (provider-messages §7.4): queued by
	// queueProviderFallbackNote (once per (session, pair)), written at turn
	// end AFTER the assistant answer entry (MIN-103/FB-2) by
	// writePendingFallbackNotes, whose defer registration in runTurn sits
	// before finalizeStreamer's so LIFO runs it after the streamer's write.
	pendingFallbackNotes []session.TranscriptEntry

	// turnWaitBudget is the D14 per-turn wait budget (10 min total), shared
	// by every fallback-chain call of this turn — a per-iteration budget
	// would multiply the cap by the iteration count. Lazily created by
	// turnWaitBudgetOnce under ts.mu; read by the chain through the ctx
	// carrier (providers.WithWaitBudget).
	turnWaitBudget *providers.WaitBudget

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
	// action. Pre-ADR-091, the deleted spawnSubTurn (pkg/agent/subturn.go,
	// ADR-057 U7) was responsible for OVERWRITING this field on the freshly
	// constructed child by direct assignment — `childTS.routingSessionID =
	// parentTS.routingSessionID`. ADR-091/D2 replaced that direct copy: a
	// steered child's routingSessionID is now DERIVED from the edge's own
	// verified cascade root rather than inherited verbatim —
	// steer_reconstruct.go::reconstructSteeredTurn sets
	// `ts.routingSessionID = session.RoutingSessionID(rec.SteeredBy.RootSessionID)`,
	// where RootSessionID was resolved and persisted at launch time by
	// steer_launcher.go::walkVerifiedRoot. At depth one the two coincide, so
	// this change is invisible in the common case; skipping the assignment
	// would still leave a child's routingSessionID equal to its OWN session
	// id instead of the root's — the exact conflation this field exists to
	// end.
	//
	// CLOSED CONSUMER SET (FR-014): this field MUST NOT be read for any
	// purpose other than routing/interrupt scoping — never as a session
	// store key, transcript write target, ownership predicate, approval-grant
	// key, uploads-directory key, tool-manifest bucket, lifecycle-record
	// field, or audit session_id (those all keep using transcriptSessionID
	// above). Within this file the reads are the role-B predicates FR-015
	// names — GetActiveTurnHookForSession and resolveSessionIDByChannelChat —
	// plus claimAnyTurnForSession, the cancel descendant fallback added
	// post-merge in the same role-B class (see the FR-014 allowlist test,
	// routing_session_id_consumer_set_adr057_test.go, the authority on the
	// exact reader census). ADR-082 D1 deleted this file's third role-B
	// predicate, getActiveRootTurnStateForSession — it existed solely for
	// the now-retired orphan-foreground-turn watchdog.
	// The remaining closed-set readers have all LANDED (U7/U8/U9/U15, this
	// same branch) — do not go looking for unfinished work here: the
	// steering.go role-B predicates (U8), the pre-arm latch keys in
	// cancel_prearm.go (U7/U15 — pre-ADR-091 also read from the since-deleted
	// subturn.go), and the WS payload stamping in loop.go (U9) all read
	// routingSessionID today.
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

	// denialLedger is ADR-058's per-turn tool-denial state (FR-058-09): an
	// aggregate count of every denial response handed to the model in this
	// turn (real or replayed from the quarantine cache), and a map of tools
	// that have already produced a PERMANENT denial and are now
	// short-circuited for the remainder of the turn. Its type and every
	// method that reads/mutates it are defined in tool_denial.go, so this
	// struct gains exactly this one field for the whole ADR-058 change.
	// Guarded by mu above (a sync.RWMutex): recordToolDenial and
	// recordQuarantineReplay mutate it and take Lock(); quarantinedDenialFor
	// only reads it and takes RLock(). Zero value (used 0, quarantined nil)
	// is correct — a fresh turnState (one per turn, via newTurnState) has
	// denied nothing yet, so no counter or quarantine entry ever survives
	// into a new turn or crosses into another session's turnState.
	denialLedger turnDenialLedger

	// browserDeferralLedger is ADR-085's per-turn control-gate deferral
	// state (BROWSER-FR-013/FR-014/FR-017): a plain aggregate count of how
	// many control-gated browser tool calls this turn has been deferred on
	// (BROWSER-FR-013), modelled on denialLedger immediately above but
	// deliberately a SEPARATE counter — the two refusals share this file's
	// tool-dispatch point and nothing else (see pkg/agent/loop.go's
	// shared-file-chain doc: E2's cap counts verifier tool-call denials and
	// refuses at its own ceiling; this counts control-gate deferrals per
	// turn and refuses at three). Its type and every method that reads/
	// mutates it are defined in browser_deferral.go. Guarded by mu above,
	// same discipline as denialLedger. Zero value (used 0) is correct — a
	// fresh turnState (one per turn) has deferred nothing yet, and a
	// delegated child turn gets its OWN turnState and therefore its own
	// independent count (FR-017).
	browserDeferralLedger turnBrowserDeferralLedger

	// mediaRetryDone is the per-turn guard for the RD2 media-downgrade retry
	// (ADR-051 §RD2 / FR-007 / FR-008). When true, the loop's classifier-gated
	// TryMediaDowngrade helper refuses to perform another downgrade — even if
	// a subsequent LLM call in the same turn returns the same media-rejection
	// shape. Hoisted here (was a per-iteration reset in the loop retry block)
	// so a turn can NEVER fire more than one media downgrade-retry, matching
	// the ADR-051 invariant "at most one media rejection → at most one
	// downgrade-retry". Initialized to false (zero value of atomic.Bool).
	mediaRetryDone atomic.Bool

	// imageRetryDone is the per-turn guard for IMAGE-only downgrades. The
	// pass-2 media-downgrade fix split the per-turn guard into image-class
	// and pdf-class, so a PDF rejection in a turn with both media types
	// cannot consume the image-retry budget (and vice versa). Each LLM
	// call may consume at most one downgrade per media class.
	imageRetryDone atomic.Bool

	// outcomeRelabel is the FR-017a relabel-on-success contract. When the
	// outcome-based strip-retry fallback fires AND the subsequent LLM
	// call succeeds, this field is stamped with CodeMediaUnsupported so
	// a later *inconclusive* residual 4xx (CodeUnknown or empty) is
	// labeled as media. A later distinct classified failure (hook abort,
	// session save, rate limit, workspace) keeps its own code — the
	// stamp must not overwrite it, or reload tells the user the model
	// rejected an image. Empty when no outcome-based retry succeeded
	// this turn. Written by the loop call site (loop.go) only; read
	// by persist/emit sites via outcomeRelabelApplies.
	outcomeRelabel LLMErrorCode
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

	// toolCallProgress is G1's turn-scoped liveness signal for an in-flight
	// tool-call argument stream (see protocoltypes.ToolCallProgress's doc
	// comment for the incident it closes). Written from the provider's SSE
	// read goroutine, via the callback loop.go passes to ChatStream, on
	// every argument delta of a live stream — a high-frequency path. Read
	// from a completely different goroutine: a `delegate action=status`
	// poll on another turn (possibly another agent instance) reaching in via
	// AgentLoop.ProgressForSession. atomicToolCallProgress is its
	// own atomics-based type rather than a field guarded by ts.mu above,
	// deliberately: contending on the turn's main RWMutex from a per-delta
	// hot path would slow down every other ts.mu consumer (setLastStreamer,
	// setProviderCancel, ...) purely for the sake of a monitoring signal
	// that only ever needs eventual consistency with the rest of turnState,
	// never linearizability.
	toolCallProgress atomicToolCallProgress

	// toolFailureStreaks tracks, per (tool name + call-arguments) signature,
	// how many times IN A ROW the exact same tool call has come back an
	// error within THIS turn (see tool_failure_circuit_breaker.go). Guarded
	// by mu — same goroutine that drives the tool loop in runTurn, but
	// touched from the same helper methods that already take ts.mu for
	// every other per-turn counter, so it follows that convention rather
	// than assuming single-goroutine access.
	//
	// UAT fix (fix/uat-defects-2026-08-22, Defect 1): a stuck condition
	// (e.g. run_task hitting a saturated dispatch cap, or create_task
	// hitting an unwired store) used to be surfaced back to the model with
	// zero escalation and zero limit — nothing on the dispatch side ever
	// told the model "this is not transient", and nothing stopped the
	// model from retrying the identical call dozens of times, each one a
	// full LLM round trip. This map is this turn's memory of that streak;
	// toolCircuitBroken (below) is set once the streak trips the hard
	// breaker.
	toolFailureStreaks map[string]int
	// toolCircuitBroken records, per signature, the reason a hard breaker
	// tripped (toolCircuitBreakThreshold consecutive identical failures).
	// Once a signature is present here the tool loop refuses to even
	// dispatch that exact call again for the rest of the turn — see
	// loop.go's SEC-26-adjacent circuit-breaker check right before the
	// tool dispatch call.
	toolCircuitBroken map[string]string
	// toolCallHistory is the ordered list of dispatched tool-call signatures
	// this turn (capped at toolCallHistoryCap), scanned by
	// detectOscillation for a repeating short cycle of calls — the loop shape
	// the failure streak cannot see (UAT 2026-09-13 D-23). See
	// tool_failure_circuit_breaker.go.
	toolCallHistory []string

	// toolRepeatSig and toolRepeatRun track the current run of consecutive
	// SUCCESSFUL dispatches of one identical (tool name + arguments)
	// signature, with no other dispatched tool call in between (see
	// tool_failure_circuit_breaker.go). Any different signature or any failure
	// ends the run. toolRepeatStopNotice is set once the run reaches
	// toolRepeatStopThreshold and is consumed at the end of that round, which
	// ends the turn with the notice as its final content. All three are
	// guarded by mu, like the failure-streak fields above.
	toolRepeatSig        string
	toolRepeatRun        int
	toolRepeatStopNotice string
}

// atomicToolCallProgress is the atomics-based store for turnState's live
// tool-call-argument progress (G1). Every field is written independently via
// plain atomic stores from recordToolCallProgress — no cross-field
// invariant is required between them (a reader observing, say, a fresher
// argsBytes than name for one instant is harmless: the next delta corrects
// it, and the worst case is a status poll's snapshot lagging by one delta,
// not a corrupted one). That is what makes plain atomics sufficient here
// where turnState's other cross-goroutine fields use ts.mu or dedicated
// atomic.Bool guards for actual coordination (cancelFired, abandoned, etc.)
// — this state has no such coordination requirement.
type atomicToolCallProgress struct {
	// lastActivityUnixNano is 0 until the first delta arrives, which
	// doubles as the "no progress recorded yet" sentinel read by the
	// snapshot accessor below.
	lastActivityUnixNano atomic.Int64
	argsBytes            atomic.Int64
	totalArgsBytes       atomic.Int64
	reasoningBytes       atomic.Int64
	// name is an atomic.Pointer rather than an atomic.Value: the callback
	// stores a fresh *string on every delta (even once the name has
	// stabilized, since the SSE loop doesn't know that), and
	// atomic.Pointer's zero value is a valid, comparable nil — unlike
	// atomic.Value, which panics if a later Store passes a different
	// concrete type than an earlier one ever stored.
	name atomic.Pointer[string]
}

// recordToolCallProgress is the write side of G1's progress signal, called
// synchronously from the provider's SSE read loop via the
// protocoltypes.OnToolCallProgress callback loop.go passes to ChatStream.
// Five atomic stores, no lock, no I/O, no allocation beyond the one string
// copy for Name — safe to call on every argument or reasoning delta of a live
// stream. A reasoning event stores its empty Name and zero ArgsBytes as-is, so
// the snapshot always describes the kind of delta that arrived last.
// Nil-safe so a callback captured before a turn is fully constructed (should
// never happen, but costs nothing to guard) degrades to a no-op instead of a
// panic.
func (ts *turnState) recordToolCallProgress(p protocoltypes.ToolCallProgress) {
	if ts == nil {
		return
	}
	name := p.Name
	ts.toolCallProgress.name.Store(&name)
	ts.toolCallProgress.argsBytes.Store(int64(p.ArgsBytes))
	ts.toolCallProgress.totalArgsBytes.Store(int64(p.TotalArgsBytes))
	ts.toolCallProgress.reasoningBytes.Store(int64(p.ReasoningBytes))
	// Stamped LAST, deliberately: a concurrent reader that observes a fresh
	// lastActivityUnixNano is guaranteed to also observe the argsBytes/name
	// stores that happened-before it (each is its own atomic op, so there is
	// no single-instruction guarantee across all four, but ordering the
	// timestamp last means a reader can never see "recently active" paired
	// with stale byte counts from a PRIOR delta — the worst residual case is
	// the reverse, a reader catching lastActivity updated but not yet one of
	// the byte counters, which just means it renders one delta stale for a
	// single read, never ahead of reality).
	ts.toolCallProgress.lastActivityUnixNano.Store(time.Now().UnixNano())
}

// clearToolCallProgress drops the recorded tool-argument progress, so a later
// reader sees "nothing being generated" rather than a stale claim.
//
// This MUST be called when an LLM round ends. Without it the signal is a lie
// with the same shape as the bug it was built to fix, only inverted: a worker
// that streamed a 300-byte `bash` argument in two seconds and is now BLOCKED
// for twenty minutes inside that command still renders
//
//	generating tool call "bash" — 300 bytes, last update 3m41s ago
//
// The worker is not generating anything; it is stuck in tool execution. And
// "generating" is precisely the word an orchestrator has been taught by this
// feature to read as "leave it alone". The original defect killed healthy
// workers; this one would suppress the kill a genuinely hung worker needs.
//
// Zeroing lastActivityUnixNano is sufficient: ProgressForSession treats a zero
// timestamp as "no progress recorded" and returns ok=false, which is exactly
// the state we want between rounds. The byte counters are left alone — they
// are unreachable while the timestamp reads zero, and clearing them would
// widen the window in which a concurrent reader sees a torn pair.
func (ts *turnState) clearToolCallProgress() {
	if ts == nil {
		return
	}
	ts.toolCallProgress.lastActivityUnixNano.Store(0)
}

// ToolCallProgress returns the current live tool-call-argument progress
// snapshot for this turn (G1), or the zero value with LastActivity.IsZero()
// true when nothing has been recorded yet — either because this turn never
// reached a streaming tool call, or because it hasn't (the common case for
// most of a turn's lifetime, and for every non-streaming provider path).
// Safe to call from any goroutine at any time, including concurrently with
// recordToolCallProgress. Exported (capital, unlike most turnState methods)
// because AgentLoop.ProgressForSession — the DelegateProgressReader
// implementation delegate.go's action:"status" poll reaches through — lives
// in the same package but is itself called from tools.DelegateTool.
func (ts *turnState) ToolCallProgress() tools.ToolCallProgressSnapshot {
	if ts == nil {
		return tools.ToolCallProgressSnapshot{}
	}
	nanos := ts.toolCallProgress.lastActivityUnixNano.Load()
	if nanos == 0 {
		return tools.ToolCallProgressSnapshot{}
	}
	var name string
	if np := ts.toolCallProgress.name.Load(); np != nil {
		name = *np
	}
	lastActivity := time.Unix(0, nanos)
	return tools.ToolCallProgressSnapshot{
		Name:           name,
		ArgsBytes:      int(ts.toolCallProgress.argsBytes.Load()),
		TotalArgsBytes: int(ts.toolCallProgress.totalArgsBytes.Load()),
		ReasoningBytes: int(ts.toolCallProgress.reasoningBytes.Load()),
		LastActivity:   lastActivity,
		Age:            time.Since(lastActivity),
	}
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

// manifestBucket derives this turn's ADR-071 D3 loaded-tool bucket key —
// manifestBucketKey(agentID, transcriptID, sessionKey) — from the turn's own
// fields, exactly as the writer (the markLoaded closure in loop.go, via
// tools.ToolAgentID/ToolTranscriptSessionID/ToolSessionKey(ctx)) and
// tool_manifest.go's two readers (buildCompressedToolDefs,
// buildToolManifestNote) already do. Every reader of al.loadedTools that has
// a *turnState in scope MUST derive its bucket through this one helper
// instead of re-deriving the three inputs inline — a prior one-off inline
// construction at two of the four reader sites (manifestNoteTokens,
// sentToolSurfaceTokens) silently drifted from this format, which made
// al.sessionLoadedTools always return an empty map at those sites. Nil-safe:
// a nil ts (or nil ts.agent) yields "" agentID, matching the nil-guard the
// two tool_manifest.go readers already had.
func (ts *turnState) manifestBucket() string {
	if ts == nil {
		return ""
	}
	var agentID string
	if ts.agent != nil {
		agentID = ts.agent.ID
	}
	return manifestBucketKey(agentID, ts.opts.TranscriptSessionID, ts.sessionKey)
}

// setOutcomeRelabel stamps the FR-017a outcome-labeller verdict for this
// turn. Called by the loop call site after a successful outcome-based
// strip-retry (the classifier's inconclusive-4xx fallback fired and
// the subsequent LLM call returned a real response). Nil-safe.
func (ts *turnState) setOutcomeRelabel(code LLMErrorCode) {
	if ts == nil {
		return
	}
	ts.outcomeRelabel = code
}

func newTurnState(agent *AgentInstance, opts processOptions, scope turnEventScope) *turnState {
	ts := &turnState{
		agent:        agent,
		opts:         opts,
		scope:        scope,
		turnID:       scope.turnID,
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
	// Bug fix (staticcheck SA5011): agentID used to be set from agent.ID
	// directly in the struct literal above, unconditionally dereferencing
	// agent 16 lines before the "if agent != nil" guard below — a latent nil
	// pointer panic for any future caller that passes a nil agent (today's
	// two call sites happen to already dereference agent.ID themselves before
	// calling newTurnState, so this never fired in practice, but the function
	// otherwise treats a nil agent as tolerable). Guard it the same way the
	// rest of this function treats agent's nilability.
	if agent != nil {
		ts.agentID = agent.ID
	}

	// Bind session store and capture the turn-start restore triple (ADR-066
	// FR-020): initialHistoryLength, initialArchiveLen and initialEmptiedSet.
	// initialArchiveLen is the number of physical lines in the archive at turn
	// start; used by restoreSession and HardAbort to roll back only the messages
	// appended during this turn (Skip-preserving rollback via RollbackAppended).
	// initialEmptiedSet is the projection state at turn start, restored in
	// the same write. All three are captured ONCE, here, and never moved.
	if agent != nil && agent.Sessions != nil {
		ts.session = agent.Sessions
		ts.initialHistoryLength = len(agent.Sessions.GetHistory(opts.SessionKey))
		if entries := agent.Sessions.Projection(opts.SessionKey).Entries; len(entries) > 0 {
			ts.initialEmptiedSet = entries.Clone()
		}
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
	// single-id behavior — AC scenario US-3/AS-1); a delegated child's
	// routingSessionID is then derived from its edge's verified cascade root
	// (steer_reconstruct.go::reconstructSteeredTurn, post-ADR-091/D2 — see
	// routingSessionID's field doc comment above for the full contract,
	// including the pre-ADR-091 spawnSubTurn history).
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
	// reaches a terminal state — see spawnCorrectiveFollowUp,
	// pkg/tools/delegate_followup.go, still live). Pre-ADR-091, the deleted
	// spawnSubTurn deliberately re-Stored the finished childTS under that
	// same key for a further ~935ms after THIS function already ran once
	// during runTurn's own unwind, specifically so the also-deleted
	// IsSubTurnActiveForSpawnCall could still find it (its own "Re-register
	// childTS in activeTurnStates" comment lived in the now-gone subturn.go).
	// That specific re-Store no longer happens (its only caller is deleted);
	// this function's CompareAndDelete-not-bare-Delete discipline is kept
	// for the reuse race described below, which is independent of it. If a new
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
	// the same compare-and-delete-by-identity pattern used everywhere else in
	// this file a map entry can race a concurrent replace.
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
// pkg/tools/delegate_followup.go) is left untouched.
//
// Pre-ADR-091 this was the deleted spawnSubTurn's cleanup seam (subturn.go's
// deferred `clearActiveTurnStateEntry(childID, childTS)`), factored out so
// the invariant was testable in isolation: that defer was otherwise locked
// inside a function whose full execution required a delegation dispatch.
// ADR-091 fix lane RX-SUBTURN note (comment-only; code unchanged): grep
// finds no production caller today — every remaining call site is a
// t.Cleanup helper in a _test.go file.
//
// clearActiveTurn (above) performs the SAME compare-and-delete for the
// parent's own ts.sessionKey plus the cancelPreArm bookkeeping that only
// applies to a finished whole turn — use THIS helper when you only need the
// bare map-entry guard (a deferred child cleanup) and clearActiveTurn when you
// are retiring a turn that ran to completion.
func (al *AgentLoop) clearActiveTurnStateEntry(sessionKey string, ts *turnState) {
	al.activeTurnStates.CompareAndDelete(sessionKey, ts)
}

// registerTurnIfAbsent is ADR-091 I-3/FR-A-013's compare-and-
// set turn registration: it admits ts only if NO turn is currently
// registered under ts.sessionKey, returning true iff THIS call won.
// SessionLauncher.Dispatch calls this (under the record lock, after I-6's
// reserveDispatch) so two concurrent dispatches of the same session resolve
// to exactly one registered turn — the loser learns immediately (a returned
// false) rather than silently overwriting the winner's turnState the way a
// bare registerActiveTurn's unconditional Store would.
//
// Built on sync.Map.LoadOrStore, which is itself the atomic primitive this
// needs: two goroutines racing the same key can never both observe
// loaded==false.
func (al *AgentLoop) registerTurnIfAbsent(ts *turnState) bool {
	_, loaded := al.activeTurnStates.LoadOrStore(ts.sessionKey, ts)
	if loaded {
		return false
	}
	// Mirrors registerActiveTurn's own post-Store step (cancel-prearm race
	// fix): a cancel that arrived for this identity before this admission
	// ran must still be applied now, not lost.
	al.consumePreArmedCancel(ts)
	return true
}

// requestCancelForGeneration is ADR-091 I-6's generation-aware
// cancel primitive: SteerCanceller.CancelSubtree (steer_cancel.go) calls
// this — never al.activeTurnStates directly — so a cancel that carries an
// older generation than the CURRENTLY registered turn's is refused rather
// than firing on the wrong (revived) turn. This is what makes "Stop then
// revive runs the revived generation; revive then Stop stamps and cancels
// the new generation" (I-6) hold: the turn registry itself is the
// tie-breaker, not caller-side ordering.
//
// Returns ok=false with a non-empty reason when: no turn is registered for
// sessionKey (the caller reports this as terminal/not-running — I-6's
// SkippedTerminal), or the registered turn's generation differs from gen
// (I-6's SkippedNewerGeneration). On a match it fires the turn's hard-abort
// cascade (turn_exit.go::requestHardAbort — the same cascade
// InterruptSessionHard dispatches) and returns ok=true; requestHardAbort's
// own first-cancel-wins guard makes a repeat call for an already-cancelled
// turn a safe no-op (ok still true — the generation matched; idempotency is
// requestHardAbort's concern, not this function's).
func (al *AgentLoop) requestCancelForGeneration(sessionKey string, gen int) (ok bool, reason string) {
	ts := al.getActiveTurnState(sessionKey)
	if ts == nil {
		return false, "no active turn registered for this session"
	}
	ts.mu.RLock()
	turnGen := ts.generation
	ts.mu.RUnlock()
	if turnGen != gen {
		return false, "stale generation: cancel targeted a generation the registered turn has moved past"
	}
	ts.requestHardAbort()
	return true, ""
}

func (al *AgentLoop) getActiveTurnState(sessionKey string) *turnState {
	if val, ok := al.activeTurnStates.Load(sessionKey); ok {
		ts, ok := val.(*turnState)
		if !ok {
			logger.ErrorCF("agent", "activeTurnStates: invariant violated — unexpected value type",
				map[string]any{"session_key": sessionKey, "got_type": fmt.Sprintf("%T", val)})
			return nil
		}
		return ts
	}
	return nil
}

// getAnyActiveTurnState returns any active turn state (for backward compatibility)
func (al *AgentLoop) getAnyActiveTurnState() *turnState {
	var firstTS *turnState
	al.activeTurnStates.Range(func(key, value any) bool {
		ts, ok := value.(*turnState)
		if !ok {
			logger.ErrorCF("agent", "activeTurnStates: invariant violated — unexpected value type, skipping entry",
				map[string]any{"key": key, "got_type": fmt.Sprintf("%T", value)})
			return true // keep scanning for a valid entry
		}
		firstTS = ts
		return false // stop after first
	})
	return firstTS
}

func (al *AgentLoop) GetActiveTurn() *ActiveTurnInfo {
	// For backward compatibility, return the first active turn found
	// In the new architecture, there can be multiple concurrent turns
	var firstTS *turnState
	al.activeTurnStates.Range(func(key, value any) bool {
		ts, ok := value.(*turnState)
		if !ok {
			logger.ErrorCF("agent", "activeTurnStates: invariant violated — unexpected value type, skipping entry",
				map[string]any{"key": key, "got_type": fmt.Sprintf("%T", value)})
			return true // keep scanning for a valid entry
		}
		firstTS = ts
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
		ts, ok := value.(*turnState)
		if !ok {
			logger.ErrorCF("agent", "activeTurnStates: invariant violated — unexpected value type, skipping entry",
				map[string]any{"got_type": fmt.Sprintf("%T", value)})
			return true
		}
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
	// MarkAbandoned suppresses later writes after a controller detaches a
	// stuck turn goroutine (gateway cancel or delegation timeout).
	MarkAbandoned()
}

// Compile-time check: *turnState implements TurnCancelHook.
var _ TurnCancelHook = (*turnState)(nil)

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
		ts, ok := value.(*turnState)
		if !ok {
			logger.ErrorCF("agent", "activeTurnStates: invariant violated — unexpected value type, skipping entry",
				map[string]any{"got_type": fmt.Sprintf("%T", value)})
			return true
		}
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

// ProgressForSession implements tools.DelegateProgressReader (G1 fix): it is
// the consumer-side seam `delegate action=status` reaches through to read a
// running native child's LIVE tool-call-argument progress, wired in via
// delegateTool.SetProgressReader at DelegateTool construction (loop.go),
// mirroring the existing steer.SessionLauncher/DelegateAgentRegistry/
// DelegateSessionStore seams this tool already uses to avoid a tools<->agent
// import cycle. (It used to name SubTurnSpawner here; that interface was
// deleted with the sub-turn path and has zero definitions today.)
//
// sessionKey here is expected to be the delegate session id
// (session.LifecycleRecord.SessionID, the id `delegate action=run` returns)
// — the SAME id this file's registerActiveTurn registers the child's own
// turnState under in al.activeTurnStates
// (`al.activeTurnStates.Store(ts.sessionKey, ts)`, where a steered child's
// ts.sessionKey is set to rec.SessionID by
// steer_reconstruct.go::reconstructSteeredTurn). This is a direct Load on that
// existing registry, not a new one: activeTurnStates already exists
// specifically to let cross-goroutine callers reach a live turn by a key
// they hold (GetActiveTurnHookForSession/claimAnyTurnForSession above do the
// same thing for cancellation), so a second, parallel registry would be an
// unjustified duplicate of state that already lives here.
//
// Returns false when no turn is registered under sessionKey (child not yet
// spawned, already finished, or a stale/mismatched id) or when a turn is
// registered but has not yet recorded any tool-call-argument progress (e.g.
// still generating plain text, or on a non-streaming provider path).
func (al *AgentLoop) ProgressForSession(sessionKey string) (tools.ToolCallProgressSnapshot, bool) {
	if al == nil || sessionKey == "" {
		return tools.ToolCallProgressSnapshot{}, false
	}
	val, ok := al.activeTurnStates.Load(sessionKey)
	if !ok {
		return tools.ToolCallProgressSnapshot{}, false
	}
	ts, ok := val.(*turnState)
	if !ok || ts == nil {
		return tools.ToolCallProgressSnapshot{}, false
	}
	// A finished turn is not "generating", whatever it last recorded.
	//
	// activeTurnStates can legitimately hold a COMPLETED turnState: pre-ADR-091,
	// the deleted spawnSubTurn deliberately re-registered the child after runTurn
	// returned, for a persist-retry window of roughly a second. During that
	// window the delegate task was still marked running, so the caller's own
	// status guard did not exclude it. At the poll rate the incident actually
	// exhibited — 75 polls in 46 seconds, roughly one every 600ms — a window
	// that size was hit routinely, not rarely. ADR-091 deleted that re-register
	// call along with spawnSubTurn, so the specific window this guard was
	// written for no longer recurs the same way; kept anyway (comment-only
	// lane, code unchanged) as defensive practice — every other cross-goroutine
	// reader of this registry checks IsAlive, and a completed-but-still-stored
	// turnState remains a reachable state in principle (e.g. clearActiveTurn's
	// CompareAndDelete racing a new generation's registerActiveTurn — see
	// clearActiveTurn's own doc comment).
	if !ts.IsAlive() {
		return tools.ToolCallProgressSnapshot{}, false
	}
	snap := ts.ToolCallProgress()
	if snap.LastActivity.IsZero() {
		return tools.ToolCallProgressSnapshot{}, false
	}
	return snap, true
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
		ts, ok := value.(*turnState)
		if !ok {
			logger.ErrorCF("agent", "activeTurnStates: invariant violated — unexpected value type, skipping entry",
				map[string]any{"got_type": fmt.Sprintf("%T", value)})
			return true
		}
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

// claimAnyTurnForSession scans activeTurnStates for ANY turnState matching
// sessionID (routingSessionID equality — the same role-B cancel predicate
// GetActiveTurnHookForSession/collectDescendantTurnIDs/InterruptSession all
// share post-ADR-057; see the rebase comment in the body) that is still
// alive (IsAlive()) and successfully wins the first-cancel-wins
// ClaimCancel() check.
//
// RequestCancel uses this as a fallback when the SINGLE hook
// GetActiveTurnHookForSession resolved (root-preferring) could not be
// claimed — most commonly because it already fired from an earlier,
// unrelated cancel — while a DIFFERENT, live, never-canceled descendant (a
// background/Critical async delegate is the common case: it shares the
// root's transcriptSessionID but is a wholly separate turnState) still
// shares the session. Without this fallback, RequestCancel's entire
// descendant-cancellation cascade AND its turn_canceled transcript/audit
// write live behind the wasFired gate computed from that ONE resolved hook
// alone, so a claimable-but-never-tried descendant would be silently
// skipped — precisely the bug class 78bddc82 already fixed for
// KillBackgroundSessions (which fires unconditionally, independent of
// wasFired, for exactly this reason), just for the native
// InterruptSession/transcript cascade instead of the background-bash one.
//
// Root-preference does not apply here — unlike GetActiveTurnHookForSession,
// this is a last-resort "is there ANYTHING left to claim" scan, not the
// primary resolution, so the first live, claimable match in sync.Map's
// (unspecified) iteration order is used; InterruptSession's own independent
// Range scan (not this function) is what actually cascades the signal to
// every matching turn regardless of which one was claimed here. Returns nil
// when no turnState matches sessionID, or every match is already finished
// or already claimed.
func (al *AgentLoop) claimAnyTurnForSession(sessionID string) TurnCancelHook {
	// Defense in depth alongside RequestCancel's own sessionID != "" gate:
	// turns with an empty routingSessionID legally exist, and matching them
	// against an empty query would claim an arbitrary unrelated turn.
	// Mirrors resolveSessionIDByChannelChat's empty-sid skip.
	if sessionID == "" {
		return nil
	}
	var claimed *turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts, ok := value.(*turnState)
		if !ok {
			logger.ErrorCF("agent", "activeTurnStates: invariant violated — unexpected value type, skipping entry",
				map[string]any{"got_type": fmt.Sprintf("%T", value)})
			return true
		}
		// ADR-057 merge rebase: release wrote this predicate pre-identity-split,
		// matching transcriptSessionID. Post-D1 a delegated child's
		// transcriptSessionID is its OWN id, so that match can never find the
		// live background/Critical delegate this fallback exists for. The
		// cancel-reachability key is routingSessionID (inherited verbatim down
		// the tree, == the chat root's id) — the same rebase every other
		// role-B cancel predicate received. This adds one reader to
		// routingSessionID's FR-014 reader set, in the same role-B class.
		if ts.routingSessionID != session.RoutingSessionID(sessionID) || !ts.IsAlive() {
			return true
		}
		if ts.ClaimCancel() {
			claimed = ts
			return false // stop — one successful claim is enough
		}
		return true
	})
	if claimed == nil {
		// Explicit nil-interface return: a nil *turnState boxed directly into
		// TurnCancelHook would compare != nil to callers (classic Go
		// interface-nil gotcha), silently defeating the `fallback != nil`
		// check RequestCancel relies on.
		return nil
	}
	return claimed
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

// appendToAccumulator extends ADR-087 D6.1's turn-scoped accumulator with
// one more round's content and returns the running total. Does NOT bump
// continuationRounds/continuationPending — see markContinuationDispatched
// for that; this method only records what was produced, independent of
// whether the caller goes on to actually continue.
func (ts *turnState) appendToAccumulator(content string) string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.continuationAccum += content
	return ts.continuationAccum
}

// continuationAccumulated returns the concatenated answer accumulated so
// far across this turn's ADR-087 D6 continuation rounds.
func (ts *turnState) continuationAccumulated() string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.continuationAccum
}

// markContinuationDispatched records that a D6 continuation round has been
// sent (bumping the D6.3 round bound) and marks the chain unresolved until
// resolveContinuation clears it.
func (ts *turnState) markContinuationDispatched() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.continuationRounds++
	ts.continuationPending = true
	// #823: the NEXT round continues this SAME message — see
	// continueSameMessageID's own doc comment for why markContinuationPending
	// (the sibling D4 carve-out setter) must NOT do this.
	ts.continueSameMessageID = true
}

// markContinuationPending marks the D6 chain unresolved WITHOUT counting a
// dispatched continuation round (ADR-087 D4's "truncated and has complete
// tool calls" carve-out — the tool calls are executed as normal, but the
// truncated finish reason means this round produced no resolving text
// answer, so D6.8 must still be able to rescue the accumulator if the turn
// ends before a later round resolves it). Deliberately does not bump
// continuationRounds — this is not a D6.3-counted auto-continue round.
func (ts *turnState) markContinuationPending() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.continuationPending = true
}

// resolveContinuation clears the D6.8 "unresolved" flag — called when a
// later round completes without needing the truncation branch at all
// (ordinary content or tool calls) and by D4a/D4b once they annotate the
// final content. No-op (idempotent) when nothing is pending.
func (ts *turnState) resolveContinuation() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.continuationPending = false
	// #823: a chain that resolves before continueSameMessageID was ever
	// consumed by nextRoundMessageID (e.g. a later round completed WITHOUT
	// going through the truncation branch at all) must not leave a stale
	// "reuse the old id" flag armed for some unrelated future round.
	ts.continueSameMessageID = false
}

// hadContinuation reports whether this turn has dispatched at least one D6
// continuation round. Unlike continuationUnresolved, this never clears once
// set — finalizeStreamer needs it for the rest of the turn's finalization,
// not just while a round is mid-flight (see the field's own doc comment).
func (ts *turnState) hadContinuation() bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.continuationRounds > 0
}

// continuationUnresolved reports whether a dispatched D6 continuation has
// not yet resolved — the gate preserveTruncatedAccumulator (loop.go, D6.8)
// uses to decide whether a terminal exit needs to rescue a mid-flight
// partial answer.
func (ts *turnState) continuationUnresolved() bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.continuationPending
}

// continuationRoundsSnapshot returns how many D6 continuation rounds this
// turn has dispatched so far.
func (ts *turnState) continuationRoundsSnapshot() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.continuationRounds
}

// setTruncationReason records ADR-087 D2's narrow reason enum for this
// turn's final content annotation ("max_output_tokens" — the only value
// this package writes).
func (ts *turnState) setTruncationReason(reason string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.truncationReason = reason
}

// getTruncationReason returns the pending truncation annotation reason, or
// "" when this turn's final content needs no annotation.
func (ts *turnState) getTruncationReason() string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.truncationReason
}

// noteGoalNarrowAttempt bumps ADR-088 D3's bounded-escape counter
// (goalNarrowMisses, see its doc comment) for one more narrowed first-move
// offering this turn and returns the running total, so the caller
// (evaluateGoalForcing, loop.go) can compare it against
// goalForcingMaxNarrowAttempts.
func (ts *turnState) noteGoalNarrowAttempt() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.goalNarrowMisses++
	return ts.goalNarrowMisses
}

// armGoalNarrowEscape permanently releases ADR-088 D3's narrowed first-move
// door for the rest of this turn (see goalNarrowEscaped's doc comment).
func (ts *turnState) armGoalNarrowEscape() {
	ts.mu.Lock()
	ts.goalNarrowEscaped = true
	ts.mu.Unlock()
}

// goalNarrowIsEscaped reports whether armGoalNarrowEscape has already fired
// this turn.
func (ts *turnState) goalNarrowIsEscaped() bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.goalNarrowEscaped
}

// SetFinalContent records the final assistant response on the turnState so
// finalizeStreamer can pass it through to the streamer's Finalize call.
func (ts *turnState) SetFinalContent(content string) {
	ts.mu.Lock()
	ts.finalContent = content
	ts.mu.Unlock()
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

// TurnID returns the turn's ID string for use outside the agent package.
func (ts *turnState) TurnID() string {
	return ts.turnID
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

// AddTurnIOStats accumulates the prompt/completion split from a single LLM
// iteration. It is a sibling of AddTurnCacheStats and must be called alongside
// AddTurnStats for every LLM call.
//
// AddTurnStats only ever carried Usage.TotalTokens, so the input/output split
// the provider already reports was discarded at that call site. Everything
// downstream then had nothing to record, which is why every session's
// tokens_in read 0 while tokens_out carried the entire volume.
// B4: suppressed when the turn is marked abandoned.
func (ts *turnState) AddTurnIOStats(promptTokens, completionTokens int) {
	if ts.abandoned.Load() {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.turnPromptTokens += promptTokens
	ts.turnCompletionTokens += completionTokens
}

// GetTurnIOStats returns the accumulated prompt/completion split for this turn.
func (ts *turnState) GetTurnIOStats() (promptTokens, completionTokens int) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.turnPromptTokens, ts.turnCompletionTokens
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
// Injected by loop_run_turn_tools.go before tool execution — pre-ADR-091, so the deleted
// spawnSubTurn could read it and set the child turnState.parentSpawnCallID (FR-H-003).
// Today nothing reads it back out in production (spawnToolCallIDFromContext has no
// non-test caller); see parentSpawnCallID's own field doc comment (above) for the
// full finding.
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
