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
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
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

	// cancelling is the GATE half of the chain-reaction cancellation fix
	// (ADR-057 FR-024, superseded 2026-08-04): set true by markTurnsCancelling
	// (steering.go) for every turn Interrupt/InterruptSessionHard resolves as
	// a target — the ANCHOR and every currently-known live descendant — as
	// the VERY FIRST thing either function does, before any interrupt signal
	// is actually fired. spawnSubTurn (subturn.go) walks parentTS's own
	// ancestor chain via parentTurnState, checking THIS flag at every level,
	// before creating a new child; any hit refuses the spawn outright
	// (ErrSessionCancelling).
	//
	// This exists because recursion (re-scanning/re-arming for a child that
	// ALREADY registered, or is ALREADY marked as about to via
	// pendingSpawns) fixes the ORDER cancellation reaches existing/imminent
	// descendants but cannot, by itself, stop a BRAND NEW child from being
	// born after cancellation has begun: the child's own context is
	// deliberately NOT derived from the parent's (spawnSubTurn's childCtx is
	// context.WithTimeout(context.Background(), ...) so a Critical async
	// delegate can outlive its parent's own graceful finish — re-parenting it
	// would break that), so Go's ordinary context-cancellation propagation
	// gives no signal here at all. This flag is that signal, checked
	// explicitly at the one place a new child is actually created.
	//
	// Never explicitly cleared: each turnState is a fresh object per turn
	// generation (newTurnState), so there is nothing to reset — a later,
	// unrelated message in the same session constructs a brand-new root
	// turnState (parentTurnState==nil, cancelling's zero value false), which
	// the ancestor walk never even reaches. No TTL, no registry, no
	// possibility of permanently "bricking" a session's ability to delegate.
	cancelling atomic.Bool

	restorePointHistory []providers.Message
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
	// returns (its own deferred Finish call), but spawnSubTurn's cleanup
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
	// above). Within this file the reads are the three role-B predicates
	// FR-015 names — GetActiveTurnHookForSession,
	// resolveSessionIDByChannelChat and getActiveRootTurnStateForSession —
	// plus claimAnyTurnForSession, the cancel descendant fallback added
	// post-merge in the same role-B class (see the FR-014 allowlist test,
	// routing_session_id_consumer_set_adr057_test.go, the authority on the
	// exact reader census).
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
// Four atomic stores, no lock, no I/O, no allocation beyond the one string
// copy for Name — safe to call on every argument delta of a live stream.
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

// outcomeRelabelApplies reports whether FR-017a's media-retry stamp should
// override this persist/emit. Residual 4xx stays CodeUnknown so the stamp
// can label that inconclusive trigger as media after a successful
// strip-retry. A later distinct classified failure must keep its own code
// — otherwise reload tells the user the model rejected an image.
func outcomeRelabelApplies(current, relabel LLMErrorCode) bool {
	return relabel != "" && (current == "" || current == CodeUnknown)
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

// ProgressForSession implements tools.DelegateProgressReader (G1 fix): it is
// the consumer-side seam `delegate action=status` reaches through to read a
// running native child's LIVE tool-call-argument progress, wired in via
// delegateTool.SetProgressReader at DelegateTool construction (loop.go),
// mirroring the existing SubTurnSpawner/DelegateAgentRegistry/
// DelegateSessionStore seams this tool already uses to avoid a tools<->agent
// import cycle.
//
// sessionKey here is expected to be a DelegateTaskState.DelegateSessionID —
// the SAME id spawnSubTurn (subturn.go) registers the child's own turnState
// under in al.activeTurnStates (`al.activeTurnStates.Store(childID, childTS)`
// where childID := cfg.DelegateSessionID). This is a direct Load on that
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
	// activeTurnStates can legitimately hold a COMPLETED turnState: spawnSubTurn
	// deliberately re-registers the child after runTurn returns, for a persist-
	// retry window of roughly a second. During that window the delegate task is
	// still marked running, so the caller's own status guard does not exclude
	// it. At the poll rate the incident actually exhibited — 75 polls in 46
	// seconds, roughly one every 600ms — a window that size is hit routinely,
	// not rarely. Every other cross-goroutine reader of this registry checks
	// IsAlive; this one must too.
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
		ts := value.(*turnState)
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

// streamerIOStatsSetter is an optional interface a Streamer may implement to
// receive the provider's input/output token split, and the cache split,
// before Finalize is called.
//
// It exists because streamerStatsSetter carries only a COLLAPSED total, and
// the streamer builds its own TranscriptEntry. Without this, a streamed turn —
// which is every ordinary webchat turn — wrote an entry with no split, so
// session stats fell back to booking the whole total as output and tokens_in
// stayed 0. That is the exact defect the split was added to fix; wiring it
// only into the non-streaming path fixed headless runs and left the flagship
// chat surface reporting the same wrong numbers.
type streamerIOStatsSetter interface {
	SetTurnIOStats(promptTokens, completionTokens, cacheReadTokens, cacheWriteTokens int)
}

// streamerFailedSetter is an optional interface a Streamer may implement to
// receive the turn-failed flag before Finalize is called. When implemented,
// finalizeStreamer calls SetTurnFailed(true) whenever the turn ended via the
// engine's error/limit fallback — (1) empty response after retries (engine
// defaultResponse sentinel), (2) tool-iteration limit, (3) generic
// empty-content exhaustion that resolved to the defaultResponse sentinel
// (excluding caller-supplied success DefaultResponse strings such as the
// heartbeat path's "Background task completed."), or (4) any return path that
// left turnStatus == TurnEndStatusError, including the LLM-error early returns.
// The done frame carries DoneStats.TurnFailed=true. CLI/automation clients
// read this field to exit non-zero on a failed turn.
type streamerFailedSetter interface {
	SetTurnFailed(failed bool)
}

// markTurnFailed records that this turn did NOT end in a real, successful model
// response. It is called from four sites in loop.go: (1) empty-response-after-
// retry, (2) tool-iteration limit, (3) generic empty-content exhaustion when the
// caller's DefaultResponse equals the engine's own error sentinel (never when a
// success message is supplied), and (4) runTurn's turn-end defer, whenever
// turnStatus == TurnEndStatusError — the catch-all that covers every error
// return path, including the LLM-error early return that a provider refusal
// takes. See that defer's own comment for why the trigger is exactly
// TurnEndStatusError and not emptiness, and why its LIFO registration position
// relative to `defer ts.finalizeStreamer(ctx)` is load-bearing.
//
// The flag is read by finalizeStreamer and forwarded to the streamer's
// SetTurnFailed before Finalize so it can set DoneStats.TurnFailed on the done
// WS frame. Idempotent: sites (1)-(3) and (4) can both fire for the same turn.
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
	promptTokens := ts.turnPromptTokens
	completionTokens := ts.turnCompletionTokens
	cacheRead := ts.turnCacheRead
	cacheWrite := ts.turnCacheWrite
	ts.lastStreamer = nil
	ts.mu.Unlock()
	if s != nil {
		if setter, ok := s.(streamerStatsSetter); ok {
			setter.SetTurnStats(tokens, cost, duration)
		}
		if iosetter, ok := s.(streamerIOStatsSetter); ok {
			iosetter.SetTurnIOStats(promptTokens, completionTokens, cacheRead, cacheWrite)
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

func (ts *turnState) captureRestorePoint(history []providers.Message) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.restorePointHistory = append([]providers.Message(nil), history...)
}

func (ts *turnState) recordPersistedMessage(msg providers.Message) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.persistedMessages = append(ts.persistedMessages, msg)
}

func (ts *turnState) refreshRestorePointFromSession(agent *AgentInstance) {
	history := agent.Sessions.GetHistory(ts.sessionKey)

	ts.mu.RLock()
	persisted := append([]providers.Message(nil), ts.persistedMessages...)
	ts.mu.RUnlock()

	if matched := matchingTurnMessageTail(history, persisted); matched > 0 {
		history = append([]providers.Message(nil), history[:len(history)-matched]...)
	}

	ts.captureRestorePoint(history)
}

func (ts *turnState) restoreSession(agent *AgentInstance) error {
	ts.mu.RLock()
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
	//
	// The third member of the turn-start triple (ADR-066 FR-020) is the
	// projection set as of turn start. Until T066-12 captures it in
	// newTurnState (turnState.initialEmptiedSet), nil is passed: no result
	// is ever emptied before T066-12 lands, so "nothing emptied at turn
	// start" is exact, and the store keeps pre-turn capped entries on its own.
	agent.Sessions.RollbackAppended(ts.sessionKey, targetLen, targetSkip, nil)

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
	turnPromptTokens, turnCompletionTokens := ts.GetTurnIOStats()
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
		PromptTokens:     turnPromptTokens,
		CompletionTokens: turnCompletionTokens,
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
// human-readable description; `pe` is the optional structured provider
// error (status/body) — when present, the classifier runs here (write
// choke point, ADR-051 §RD5) so raw provider text never lands on disk.
//
// Used by recordRateLimitDenial and the LLM-call-error paths in loop.go to
// satisfy FR-001 (rate_limit → transcript) and FR-002 (provider error →
// transcript) of docs/internal/specs/phase-1-chat-model-and-errors.md, plus
// the translation-at-write invariant of ADR-051 §RD5 (FR-007/008).
//
// Rate-limit skip (ADR-051 §RD5 MAJ-001/004): when kind==EventKindRateLimit
// the caller-supplied message is already friendly (rate_limit: policyRule
// (retry after Ns)) — passing pe=nil here lets the classifier recognize
// it via the message substring and emit CodeRateLimited, but the caller
// message is preserved as-is. Either way, raw provider text never reaches
// the JSONL.
//
// Silently no-ops when the turn has been abandoned or when no transcript
// store is wired (matches appendAssistantTranscript's failure semantics — a
// failed transcript write must NOT abort the in-flight turn). Debug-level
// logging is emitted when the no-op fires so an operator can trace why a
// transcript entry was suppressed.
// trustedInternalStages is the set of stage+kind tuples that the write
// choke point MUST NOT sanitize. Callers of appendErrorTranscript for
// these stages produce curated, generic copy that is the actionable
// signal a user/operator needs to see; sanitizing them would clobber
// the operator-friendly shape. Provider-originated text NEVER enters
// these paths (the prior call site is an internal hook, abort, or
// synthetic-deny source).
type internalStage struct{ stage, kind string }

var trustedInternalStageSet = map[internalStage]struct{}{
	{"rate_limit", "rate_limit"}:   {},
	{"model_switch", "error"}:      {},
	{"before_llm", "error"}:        {},
	{"after_llm", "error"}:         {},
	{"llm_call", "error"}:          {},
	{"llm_retry_backoff", "error"}: {},
	{"turn_loop", "error"}:         {},
	// ADR-058 §10.A3: FR-084 (the retired synthetic-error-floor feature) was
	// deleted in full — no producer calls appendErrorTranscript with that
	// stage name anymore, so a trust-set entry for it does not belong here.
	// FIX 6: hookAbortError (loop.go) is the SOLE producer of hook-abort
	// transcript entries, and it ALWAYS calls appendErrorTranscript with the
	// literal stage "hooks" — regardless of which HookInterceptor stage
	// (before_llm/after_llm/before_tool/after_tool) actually triggered the
	// abort; that more specific stage name only flows into the error
	// MESSAGE text and the live EventPayload.Stage ("hook."+stage), never
	// into this appendErrorTranscript call. So "hooks" is the one entry
	// that actually matters here: without it, hookAbortError's
	// decision.Reason (caller-curated text from a HookInterceptor — before_
	// tool/after_tool share the exact same ToolInterceptor/HookManager
	// plumbing and provenance as before_llm/after_llm, already trusted
	// above) gets re-run through the classifier, and any hook reason that
	// happens to contain a pinned substring (e.g. "safety", "rate limit")
	// is silently replaced with generic boilerplate — even though the SAME
	// reason survives byte-for-byte in the CodeUnknown/no-providerErr
	// fallback below for reasons that don't happen to match a substring.
	// "before_tool"/"after_tool" are added too — unlike "hooks" above, these
	// ARE reachable today, via a DIFFERENT call site than hookAbortError:
	// abortTurn (loop.go) passes its `stage` argument straight through to
	// appendErrorTranscript verbatim (no hardcoded "hooks" collapse), and is
	// itself called as al.abortTurn(ts, "before_tool", decision.Reason) /
	// al.abortTurn(ts, "after_tool", decision.Reason) on a HookActionHardAbort
	// decision (loop.go's before_tool and after_tool HookInterceptor call
	// sites). decision.Reason is the same caller-curated
	// HookInterceptor/HookManager text as before_llm/after_llm/"hooks" above,
	// so it needs the identical trusted-stage protection from re-classification.
	{"hooks", "error"}:       {},
	{"before_tool", "error"}: {},
	{"after_tool", "error"}:  {},
	// Workspace-membership refusals (runTurn after EventKindTurnStart).
	// The caller already ran TranslateTurnError and passed the catalogue
	// sentence. Without this entry the write choke point re-classifies
	// that sentence as unknown, and replay looks up the unknown line
	// ("we can't tell why") — the live fix vanishing on reload.
	{"workspace", "error"}:    {},
	{"external_cli", "error"}: {},
}

func isTrustedInternalStage(stage, kind string) bool {
	_, ok := trustedInternalStageSet[internalStage{stage: stage, kind: kind}]
	return ok
}

func (ts *turnState) appendErrorTranscript(kind, stage, message string, pe ...*ProviderError) {
	ts.writeErrorTranscript(kind, stage, message, "", pe...)
}

// appendClassifiedError persists an error the caller already classified.
// Replay looks up the bubble by ErrorCode, not Content. Re-running the
// catalogue sentence through TranslateLLMError stamps unknown — the live
// fix then vanishes on reload. Pass the live LLMError instead.
func (ts *turnState) appendClassifiedError(kind, stage string, llm LLMError) {
	ts.writeErrorTranscript(kind, stage, llm.Message, llm.Code)
}

func (ts *turnState) writeErrorTranscript(kind, stage, message string, code LLMErrorCode, pe ...*ProviderError) {
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

	// ADR-051 §RD5 write choke point: translate the message via the shared
	// classifier so raw provider text never persists. The classifier reads
	// pe.Status/pe.Body when present (nil-safe — see classifyByProviderError);
	// pe nil means "this is not a provider error" (e.g. internal model_switch
	// failures, hook aborts, rate-limit denials) and the classifier falls
	// back to substring matching on the caller-supplied message.
	var providerErr *ProviderError
	if len(pe) > 0 {
		providerErr = pe[0]
	}
	// Trusted internal-stages bypass (ADR-051 §RD5 IMPORTANT 1):
	// the caller has produced a curated, generic message for these stages
	// that does NOT carry raw provider text. Sanitizing them would clobber
	// the actionable signal the operator relies on (e.g. hook abort reason,
	// synthetic-error-floor count, model-switch friendly guidance). The
	// classifier still stamps a typed code on the entry for replay routing,
	// but the user-visible text is the caller-provided copy verbatim.
	llm := TranslateLLMError(providerErr, message)

	// Prefer the caller's already-classified code. Catalogue sentences do
	// not contain the classifier substrings, so a second TranslateLLMError
	// would stamp unknown and reload would say we cannot tell why.
	if code != "" {
		llm.Code = code
		llm.Retryable = isRetryable(code)
		if isTrustedInternalStage(stage, kind) {
			llm.Message = message
		} else {
			llm.Message = defaultUserMessage(code)
		}
	}
	// Belt for the one uncoded producer that is unambiguous: an internal
	// SEC-26 denial written as kind=rate_limit, stage=rate_limit. Live is
	// EventKindRateLimit; replay looks up by ErrorCode.
	if code == "" && stage == "rate_limit" && kind == EventKindRateLimit.String() {
		llm.Code = CodeRateLimited
		llm.Message = message
		llm.Retryable = isRetryable(CodeRateLimited)
	}

	// FR-017a: label an *inconclusive* residual 4xx after a successful
	// strip-retry. Do not overwrite a later distinct classified code.
	if outcomeRelabelApplies(llm.Code, ts.outcomeRelabel) {
		llm.Code = ts.outcomeRelabel
		llm.Message = defaultUserMessage(ts.outcomeRelabel)
	}

	written := message
	if !isTrustedInternalStage(stage, kind) {
		// Friendly short-circuit for rate-limit messages whose caller-supplied
		// copy is already generic and safe (rate_limit: policyRule (retry
		// after Ns)); translation reuses it. This is the ADR-051 §RD5
		// MAJ-001/004 carve-out — the classifier still RECOGNIZES rate-limit
		// shape, but the emitted Content is the caller-provided message
		// verbatim (no double translate, no model-name leak from MAJ-003).
		if llm.Code == CodeRateLimited {
			written = message
		} else if llm.Code != CodeUnknown || providerErr != nil {
			written = llm.Message
		}
	}

	agentID := ts.resolveActiveAgentID()
	entry := session.TranscriptEntry{
		ID:             uuid.New().String(),
		Type:           session.EntryTypeSystem,
		AgentID:        agentID,
		Content:        written,
		Timestamp:      time.Now().UTC(),
		ErrorCode:      string(llm.Code),
		ErrorRetryable: llm.Retryable,
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
	// repeated Finish calls (e.g. runTurn's own deferred Finish call running
	// after an explicit Finish(true) from a hard abort) cannot invoke it a
	// second time (FR-15).
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
