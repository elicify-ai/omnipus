package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// ADR-036 / docs/internal/specs/agent-delegation-spec.md — `delegate` is the
// single, unified delegation tool. It replaces the formerly-separate `spawn`,
// `run_subagent`, and `check_spawn_status` tools with one tool and one schema.
//
// FR-D2 (the bug this merge exists to fix): before this merge, `spawn` ran the
// child turn directly in a goroutine, entirely bypassing the legacy
// SubagentManager.tasks map that `check_spawn_status` read from —
// checking on a spawn-created task always reported "no subagents have been
// spawned yet." DelegateTool grew its own `tasks`/`sessionIndex` maps as the
// FR-D2 fix's SINGLE state store, keyed by a task_id the tool itself minted.
//
// ADR-091 superseded that store: the launcher migration (delegate_run.go's
// executeRun/launchAndDispatch) moved dispatch onto steer.SessionLauncher —
// the one session-launch primitive every session type now shares — which
// writes only the durable session.LifecycleRecord, never the legacy tasks
// map. `action:"status"` (and every other parent-side action) now reads
// that same durable record by session_id; the tasks/sessionIndex maps and
// their task_id addressing were deleted with the last caller that wrote
// them, closing the FR-D2 problem this comment used to describe a
// different way (a second, disconnected store) permanently rather than
// reopening it.

// ContextSnapshot is the discretionary portion of the ADR-053 curated
// context snapshot: parent-named artifact references, not contents, plus
// optional notes.
type ContextSnapshot struct {
	References []string
	Notes      string
}

// delegateSessionIDCtxKey is the context key carrying a child turn's own
// ADR-053 durable session_id (distinct from the shared transcript session
// id — pkg/tools.ToolTranscriptSessionID). Defined here (not
// pkg/tools/base.go, outside this wave's write-set) following the exact
// same WithX/ToolX accessor-pair convention every other per-turn context
// carrier in this package already uses.
type delegateSessionIDCtxKey struct{}

// WithDelegateSessionID returns a child context carrying the durable
// ADR-053 session_id for the turn currently executing. Carried on the child's
// own turn context, so a child's OWN tool calls (message_parent, and any
// future session-aware tool) can resolve their own durable identity without
// conflating it with the shared transcript session id.
func WithDelegateSessionID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, delegateSessionIDCtxKey{}, id)
}

// ToolDelegateSessionID extracts the durable ADR-053 session_id from ctx,
// or "" if unset (a root/non-delegated turn).
func ToolDelegateSessionID(ctx context.Context) string {
	v, _ := ctx.Value(delegateSessionIDCtxKey{}).(string)
	return v
}

// DelegateAgentRegistry is a minimal interface for resolving whether a
// delegation target dispatches natively (inside the Omnipus agent loop) or
// via an external CLI runner (subagent_3p: claude-code/codex/opencode).
// DelegateTool consults this at task-creation time (W2) to decide whether a
// background task is eligible for a live in-flight transcript snapshot
// under action:"status" — see DelegateTaskState.Is3P's doc comment.
//
// Satisfied by *agent.AgentRegistry; defined as an interface here (mirroring
// AgentRegistryReader in handoff.go) to avoid an import cycle
// (tools -> agent -> tools).
type DelegateAgentRegistry interface {
	// IsExternalCLI reports whether agentID resolves to dispatch kind
	// "external-cli" (subagent_3p). Returns false (native) for an unknown or
	// empty agentID.
	IsExternalCLI(agentID string) bool
}

// DelegateSessionStore is the subset of *session.UnifiedStore DelegateTool
// needs to read back a running native task's own transcript entries for
// action:"status" (W2). Defined as an interface (mirroring
// HandoffSessionStore in handoff.go) to decouple from the concrete store
// type.
type DelegateSessionStore interface {
	// ReadTranscript returns all transcript entries for the session.
	ReadTranscript(sessionID string) ([]session.TranscriptEntry, error)
}

// ToolCallProgressSnapshot is DelegateProgressReader's read-side value type
// (G1 fix): a point-in-time view of a running native delegate's live
// tool-call-argument stream, as recorded turn-side by
// agent.turnState.recordToolCallProgress. It deliberately carries no
// argument CONTENT — see protocoltypes.ToolCallProgress's own doc comment,
// which this mirrors on the read side of the tools<->agent boundary — only
// enough to answer "is this still making forward progress, and on what?".
type ToolCallProgressSnapshot struct {
	// Name is the tool being called, once the stream has revealed it. May be
	// empty for the first few deltas of a call.
	Name string
	// ArgsBytes is the byte count accumulated so far for the tool call that
	// produced the most recent delta. Zero when the most recent delta was
	// reasoning rather than a tool-call argument.
	ArgsBytes int
	// TotalArgsBytes is the byte count accumulated across every tool call in
	// the current LLM response so far (may exceed ArgsBytes when more than
	// one tool call is in flight in the same response).
	TotalArgsBytes int
	// ReasoningBytes is the reasoning ("thinking") byte count streamed so far
	// in the current LLM response. A count only — the reasoning text is never
	// recorded (see protocoltypes.ToolCallProgress).
	ReasoningBytes int
	// LastActivity is the wall-clock time of the most recent recorded delta.
	// Zero when no progress has ever been recorded for the turn.
	LastActivity time.Time
	// Age is time.Since(LastActivity), computed once at snapshot time so a
	// caller renders a stable value even if it takes a moment to format the
	// response.
	Age time.Duration
}

// DelegateProgressReader is the seam action:"status" (G1 fix) reads a
// running native delegate's LIVE tool-call-argument progress through —
// distinct from DelegateSessionStore.ReadTranscript above, which only ever
// sees data already flushed to the PERSISTED transcript at full-LLM-round
// completion. A model spending tens of seconds streaming a large tool-call
// argument (a multi-kilobyte SVG body, a long file write) produces nothing
// on that persisted path until the round finishes — recentActivityLines
// alone is blind to precisely the window a status poll most needs
// visibility into, which is what let an orchestrator conclude a
// still-working child had hung and kill it mid-write (see
// protocoltypes.ToolCallProgress's doc comment for the full incident).
//
// Implemented by *agent.AgentLoop (ToolCallProgressForSession, turn.go) and
// wired via SetProgressReader at DelegateTool construction time (loop.go),
// mirroring every other tools<->agent seam this tool already has
// (the steer.SessionLauncher seam, DelegateAgentRegistry, DelegateSessionStore) to avoid a
// tools<->agent import cycle: pkg/agent already imports pkg/tools, so the
// dependency can only run tools->agent as an interface, never the reverse as
// a concrete type.
type DelegateProgressReader interface {
	// ProgressForSession returns the live progress snapshot for the turn
	// registered under sessionKey — expected to be a
	// DelegateTaskState.DelegateSessionID — and false when no turn is
	// registered under that key, or one is but has not yet recorded any
	// tool-call-argument progress.
	ProgressForSession(sessionKey string) (ToolCallProgressSnapshot, bool)
}

// DelegateTool is the unified delegation tool (FR-D1). Any agent — including
// the main/orchestrating agent — uses this exact tool; access is governed
// solely by the delegation-policy gate (trust set, modes, depth), never by
// tool-registration role restriction (FR-D4).
type DelegateTool struct {
	BaseTool

	launcher     steer.SessionLauncher
	defaultModel string
	maxTokens    int
	temperature  float64

	// getAgentRegistry, when set, resolves the live agent registry used to
	// classify a delegation target as native or external-CLI at
	// task-creation time (W2). Called at task-creation time (not
	// construction time) so hot reloads are reflected automatically,
	// mirroring NewHandoffTool's getRegistry closure pattern. A nil/unset
	// resolver leaves every task's Is3P at its zero value (false — treated
	// as native), matching the pre-W2 behavior for anyone who doesn't wire
	// it (e.g. this file's existing unit tests).
	getAgentRegistry func() DelegateAgentRegistry
	// sessionStore, when set, is read from by action:"status" (W2) to build
	// a running native task's recent-activity snapshot. A nil/unset store
	// degrades gracefully — status falls back to the prompt-only summary it
	// already returned before this feature.
	sessionStore DelegateSessionStore

	// progressReader, when set via SetProgressReader, is read from by
	// action:"status" (G1 fix) to report a running native task's LIVE
	// tool-call-argument progress — see DelegateProgressReader's doc
	// comment for why this is a separate seam from sessionStore above
	// (persisted transcript vs. live in-memory turn state). A nil/unset
	// reader degrades gracefully — status falls back to sessionStore's
	// persisted-transcript snapshot alone, matching pre-G1 behavior.
	progressReader DelegateProgressReader

	// sessionManager, when set via SetSessionManager, is the shared
	// *SessionManager (pkg/tools/session.go — same package, no interface
	// indirection needed) executeCancel uses to kill a cancelled child's
	// OWN background bash/exec shells (ADR-057 FR-028/BDD-29): "delegate
	// action=cancel MUST kill that child's background shells (today no such
	// call exists on that path)". This is deliberately independent of, and
	// does not replace, U15's RequestCancel/Stop-button cascade
	// (pkg/agent/cancel.go's resolveBackgroundKillSessionIDs loop over
	// hooks.KillBackgroundSessions) — that path fires on a chat-wide Stop
	// click and walks the FULL descendant subtree; this one fires on a
	// delegate(action="cancel") tool call targeting exactly one child
	// session (BDD-29's scope is "that child's" shells, not its subtree,
	// matching ScopeSelfOnly's own single-target semantics — see
	// SetCancelHooks' doc comment). Reuses the SAME KillAllForSessions
	// primitive U16 exposes rather than re-deriving a second, parallel
	// descendant walk. A nil sessionManager (SetSessionManager never
	// called) is a silent no-op, matching every other optional capability
	// this tool accepts via a setter.
	sessionManager *SessionManager

	// ownershipWalkMaxDepth bounds the ancestor-chain walk
	// verifyCallerOwnsSession performs (FR-039/BDD-43) — see
	// SetOwnershipWalkMaxDepth and defaultOwnershipWalkMaxDepth.
	ownershipWalkMaxDepth int

	mu sync.Mutex

	// delegationDenyBackground applies the full delegation-policy gate
	// (FR-6.2: trust set + mode("background") + depth) for async=true calls.
	// This is the ONLY gate for the background mode (ADR-037 retired the
	// legacy trust-only allowlistCheck fallback — it was only ever consulted
	// when this was nil, which never happens in production wiring).
	delegationDenyBackground func(ctx context.Context, targetAgentID string) *DelegationDenial

	// --- ADR-053 §5.1 corrected delegate action set (run|status|inbox|
	// inbox_ack|steer|respond|cancel|follow_up|peek) ---

	// lifecycle is the durable S2 session-lifecycle store (pkg/session).
	// Required for every action beyond legacy run/status.
	lifecycle MessageParentLifecycleStore
	// inbox is the durable S3 child->parent message inbox (D16, pkg/session).
	inbox DelegateInboxStore
	// steering delivers a parent->child steer/respond into the child's
	// steering-queue scope (generalizes pkg/agent/steering.go's existing
	// mechanism — see DelegateSteeringSink's doc comment).
	steering DelegateSteeringSink
	// cancelSoft/cancelHard hold two-argument closures over AgentLoop's
	// collapsed ADR-057 W13 entry points, Interrupt/InterruptSessionHard
	// (pkg/agent/steering.go — each now takes a mandatory, explicit
	// InterruptScope), wired in pkg/agent/session_messaging_wire.go as
	// `func(sessionKey, hint string) ([]string, error) { return
	// al.Interrupt(sessionKey, ScopeSelfOnly, hint) }` (soft) and the
	// InterruptSessionHard analogue (hard) — both pinned to ScopeSelfOnly,
	// never ScopeSubtree, matching this field's own load-bearing point
	// below: a direct activeTurnStates.Load(sessionKey) targeting exactly
	// ONE delegation, not a subtree sweep. (Pre-W13 these wrapped the now-
	// retired two-argument InterruptBySessionKey/InterruptBySessionKeyHard
	// directly — the field TYPE here never changed, only what it's wired
	// to.) Injected to avoid a tools<->agent import cycle, matching
	// every other AgentLoop capability this tool already consumes via a
	// setter (SetSpawner, etc.). Returns the canceled turn's ID as a
	// single-element descendants slice on a hit, nil descendants on a miss
	// (target already terminated) — executeCancel uses that miss signal to
	// detect a TOCTOU window.
	cancelSoft func(sessionKey, hint string) ([]string, error)
	cancelHard func(sessionKey, hint string) ([]string, error)
	// cancelGrace is the cooperative-stop grace window before the hard
	// RequestCancel backstop fires (session_messaging.cancel_grace,
	// FR-195). Defaults to defaultCancelGrace.
	cancelGrace time.Duration

	// sessionMessagingEnabled, when set via SetSessionMessagingEnabled, is the
	// live-read FR-196 kill switch (session_messaging.enabled) for the SYNC
	// session-messaging-plane actions (inbox/inbox_ack/steer/respond/cancel/
	// follow_up/peek). The async consumer honors the same switch per event;
	// without this guard those actions bypassed it (calling
	// EnqueueSteeringMessage / inbox directly).
	//
	// sessionMessagingWired tracks whether SetSessionMessagingEnabled was
	// EVER called, so an unwired tool fails CLOSED on the kill switch rather
	// than fail-open (silent-failure hunter #12 — fix B.5). The FR-196
	// kill switch is a security boundary; an unwired production tool is a
	// configuration bug, not a permission grant.
	sessionMessagingEnabled func() bool
	sessionMessagingWired   atomic.Bool

	// requireParentAgentID, when set via SetRequireParentAgentID, is the
	// live-read reader for tools.delegate.require_parent_agent_id
	// (R2-MAJ-015) — the operator kill switch for the FR-015 fail-closed
	// parent-agent-id guard in Execute's lifecycle-mint block.
	//
	// It is a func() bool, NOT a captured bool, for two independent reasons:
	//
	//  1. Live reads. An operator flipping the key must take effect without a
	//     restart, exactly like sessionMessagingEnabled above. That matters
	//     more here than almost anywhere else: the guard's failure mode is
	//     "every delegate call in the install errors", and needing a restart
	//     to escape it defeats the point of shipping an escape hatch.
	//  2. Late binding. Gateway boot assigns several of this tool's
	//     dependencies AFTER the wiring pass that constructs it runs, so a
	//     dependency read eagerly at wiring time can be nil (or stale)
	//     forever while registration still looks perfectly correct. Resolving
	//     through the closure on every call sidesteps the ordering question
	//     entirely rather than depending on getting it right.
	//
	// UNWIRED (nil) resolves to TRUE — the fail-closed posture, matching
	// config.DelegateToolConfig.EffectiveRequireParentAgentID's own default
	// for an unset key. Deliberately NOT the sessionMessagingWired treatment:
	// there, unwired and "wired to false" must be distinguishable because
	// fail-closed is the SAFE end of that switch and an unwired tool must not
	// be granted the plane. Here the safe end and the unwired default are the
	// SAME value (true = keep refusing), so an extra wired flag would carry
	// no information — any path that reaches this resolver without a wired
	// closure gets the strict guard, which is the correct answer.
	requireParentAgentID func() bool

	snapshotMaxBytes int
	snapshotMaxRefs  int

	// steerRateMu/steerRateWindows back the steer/respond rate cap (ADR-053
	// §Contract Surface "Caps": 6/min, 16 KiB — session_messaging.steer_rate/
	// steer_body), keyed by target session_id. Mirrors
	// session.MessageInboxStore's own in-memory sliding-window rate-limiter
	// pattern exactly, kept local to this tool rather than shared so a
	// steer-rate breach never touches the durable inbox store's own state.
	steerRateMu      sync.Mutex
	steerRateWindows map[string][]time.Time
	steerRatePerMin  int
	steerBodyBytes   int

	// now is overridable for deterministic tests.
	now func() time.Time
}

// SetSessionLauncher installs ADR-091's one session-launch primitive. The
// delegate run front refuses to launch while this dependency is absent.
func (t *DelegateTool) SetSessionLauncher(launcher steer.SessionLauncher) {
	t.launcher = launcher
}

// Compile-time check: DelegateTool implements AsyncExecutor.
var _ AsyncExecutor = (*DelegateTool)(nil)

// Compile-time check: DelegateTool implements JobSessionResolver (#583).
var _ JobSessionResolver = (*DelegateTool)(nil)

// NewDelegateTool constructs a DelegateTool. defaultModel/maxTokens/temperature
// mirror the values the retired SubagentManager used to carry for its callers
// (agent.Model / agent.MaxTokens / agent.Temperature at the call site).
func NewDelegateTool(defaultModel string, maxTokens int, temperature float64) *DelegateTool {
	return &DelegateTool{
		defaultModel:     defaultModel,
		maxTokens:        maxTokens,
		temperature:      temperature,
		cancelGrace:      defaultCancelGrace,
		now:              time.Now,
		steerRateWindows: make(map[string][]time.Time),
		steerRatePerMin:  session.DefaultSteerRatePerMinute,
		steerBodyBytes:   session.DefaultSteerBodyBytes,
	}
}

// SetLifecycleStore installs the durable S2 session-lifecycle store.
// Required for inbox/inbox_ack/steer/respond/cancel/follow_up/peek — those
// actions return a clear "not configured" error when this is unset.
func (t *DelegateTool) SetLifecycleStore(store MessageParentLifecycleStore) {
	t.lifecycle = store
}

// SetMessageInbox installs the durable S3 child->parent message inbox.
func (t *DelegateTool) SetMessageInbox(inbox DelegateInboxStore) {
	t.inbox = inbox
}

// SetSteeringSink installs the parent->child steer/respond delivery
// mechanism (generalizes pkg/agent/steering.go's existing queue).
func (t *DelegateTool) SetSteeringSink(sink DelegateSteeringSink) {
	t.steering = sink
}

// SetSessionMessagingEnabled installs the live FR-196 kill-switch reader for
// the SYNC session-messaging-plane actions (arch-M2 review): when the returned
// bool is false, inbox/inbox_ack/steer/respond/cancel/follow_up/peek fail
// closed with a clear "plane disabled" error instead of bypassing the kill
// switch the async consumer already honors. Wired live (re-reads config per
// call) by wireSessionMessagingForAgent. run/status are delegation-spawn/query
// actions and are intentionally NOT gated.
func (t *DelegateTool) SetSessionMessagingEnabled(fn func() bool) {
	t.sessionMessagingEnabled = fn
	// Mark the tool as wired regardless of whether fn is nil — once the
	// gateway has explicitly installed a reader (even one that always
	// returns false), the wiring has been acknowledged and we honor the
	// closure's verdict rather than falling through to the fail-closed
	// default below.
	t.sessionMessagingWired.Store(true)
}

// sessionMessagingPlaneEnabled reports whether the session-messaging plane is
// live for the SYNC action surface. An UNWIRED tool (SetSessionMessagingEnabled
// never called — e.g. a bare unit test that did not configure the kill switch)
// fails CLOSED, matching the FR-196 security boundary's "no silent default"
// posture (silent-failure hunter #12 — fix B.5). The wired-but-nil case is
// the explicit "always disabled" sentinel the gateway uses to ship a build-
// time kill.
func (t *DelegateTool) sessionMessagingPlaneEnabled() bool {
	if !t.sessionMessagingWired.Load() {
		// Unwired = fail closed (no silent fail-open on a security boundary).
		return false
	}
	if t.sessionMessagingEnabled == nil {
		// Wired with a nil closure = explicit "always disabled" sentinel.
		return false
	}
	return t.sessionMessagingEnabled()
}

// SetRequireParentAgentID installs the live reader for
// tools.delegate.require_parent_agent_id (R2-MAJ-015) — the operator kill
// switch for the FR-015 fail-closed parent-agent-id guard. See the
// requireParentAgentID field doc for why this is a closure and not a bool.
//
// The caller is expected to pass a closure that resolves the key through
// config.DelegateToolConfig.EffectiveRequireParentAgentID, e.g.
//
//	tool.SetRequireParentAgentID(func() bool {
//	    return al.GetConfig().Tools.Delegate.EffectiveRequireParentAgentID()
//	})
//
// Passing nil restores the unwired default (true / strict), so this is safe
// to call unconditionally from a re-runnable wiring pass.
func (t *DelegateTool) SetRequireParentAgentID(fn func() bool) {
	t.requireParentAgentID = fn
}

// parentAgentIDRequired resolves the FR-015 guard's strictness for this call.
// An unwired tool resolves TRUE (strict) — see the requireParentAgentID field
// doc for why this one does not need the sessionMessagingWired treatment.
func (t *DelegateTool) parentAgentIDRequired() bool {
	if t.requireParentAgentID == nil {
		return true
	}
	return t.requireParentAgentID()
}

// isSessionMessagingAction reports whether a delegate action touches the
// session-messaging plane (the FR-196 kill-switch surface). run/status spawn /
// query delegation and are intentionally NOT gated. Kept as a helper so the
// guard and its action set have one source of truth.
func isSessionMessagingAction(action string) bool {
	switch action {
	case "inbox", "inbox_ack", "steer", "respond", "cancel", "follow_up", "peek":
		return true
	}
	return false
}

// SetCancelHooks installs the soft (cooperative) and hard (RequestCancel
// backstop) cancel functions. ADR-057 W13 collapsed the four legacy
// interrupt entry points (InterruptSession, InterruptSessionHard,
// InterruptBySessionKey, InterruptBySessionKeyHard) into two —
// AgentLoop.Interrupt and AgentLoop.InterruptSessionHard
// (pkg/agent/steering.go) — each now taking a mandatory, explicit
// InterruptScope. The canonical wiring, in
// pkg/agent/session_messaging_wire.go, is a pair of two-argument closures
// pinned to ScopeSelfOnly: `func(sessionKey, hint string) ([]string, error)
// { return al.Interrupt(sessionKey, ScopeSelfOnly, hint) }` (soft) and the
// InterruptSessionHard analogue (hard) — NEVER ScopeSubtree, and never a
// closure over the OLD, now-retired InterruptBySessionKey(Hard) pair
// (still named here only for historical contrast). ScopeSubtree would
// widen a single targeted cancel into a whole-subtree sweep, exactly the
// dual-namespace-style bug this hook's own WARNING below exists to keep
// closed (see pkg/agent/session_messaging_wire_adr057_test.go's
// TestSetCancelHooks_ScopeSelfOnlyNotSubtree) — a future "fixing
// consistency" edit swapping in ScopeSubtree here would silently
// reintroduce it, unless a scope-aware regression test catches it, since
// the compiler cannot: soft/hard keep the same
// func(string, string) ([]string, error) signature regardless of which
// scope the wiring closure captures.
//
// WARNING — the hook MUST be invoked with the delegate's sessionKey
// (== delegateSessionID, the caller-facing id this tool returns from run and
// accepts on every subsequent cancel/steer/respond/peek), NEVER the parent
// chat's transcriptSessionID/routingSessionID. The two id spaces are
// deliberately distinct for a delegated sub-turn (see
// turnState.routingSessionID's own doc comment, pkg/agent/turn.go — the
// ROUTING id, not the transcript id, is what a chat-wide Stop cascades via)
// — sessionKey is the unique per-delegation address, unrelated to either.
// executeCancel passes its session_id argument here verbatim — that
// argument IS the delegateSessionID by contract.
func (t *DelegateTool) SetCancelHooks(
	soft func(sessionKey, hint string) ([]string, error),
	hard func(sessionKey, hint string) ([]string, error),
) {
	t.cancelSoft = soft
	t.cancelHard = hard
}

// SetCancelGrace overrides the default cooperative-stop grace window
// (session_messaging.cancel_grace, FR-195).
func (t *DelegateTool) SetCancelGrace(d time.Duration) {
	if d > 0 {
		t.cancelGrace = d
	}
}

// SetSnapshotCaps overrides the curated context snapshot's discretionary-
// portion caps (session_messaging config — snapshot_max_bytes/
// snapshot_max_refs, R§8.5). Zero/negative values fall back to the ADR
// §Contract Surface defaults.
func (t *DelegateTool) SetSnapshotCaps(maxBytes, maxRefs int) {
	t.snapshotMaxBytes = maxBytes
	t.snapshotMaxRefs = maxRefs
}

// SetClock overrides the tool's time source for deterministic tests.
func (t *DelegateTool) SetClock(now func() time.Time) {
	if now != nil {
		t.now = now
	}
}

// SetSteerCaps overrides the steer/respond rate (per-minute) and body
// (bytes) caps (session_messaging.steer_rate/steer_body, FR-195).
// Zero/negative values fall back to the ADR §Contract Surface defaults.
func (t *DelegateTool) SetSteerCaps(ratePerMinute, bodyBytes int) {
	t.steerRatePerMin = ratePerMinute
	t.steerBodyBytes = bodyBytes
}

// DelegateSteeringSink lands a parent->child steer/respond message in the
// child's steering-queue scope at its next tool boundary. Satisfied by
// *agent.AgentLoop (via its EnqueueSteeringMessage wrapper — see
// pkg/agent/steering.go); defined as an interface here to avoid a
// tools<->agent import cycle.
type DelegateSteeringSink interface {
	EnqueueSteeringMessage(scope, agentID string, msg providers.Message) error
}

// defaultCancelGrace is the cooperative-stop grace window before the hard
// RequestCancel backstop fires when SetCancelGrace is never called
// (session_messaging.cancel_grace default, FR-195).
const defaultCancelGrace = 5 * time.Second

// SetAgentRegistry installs the live agent-registry lookup (W2) DelegateTool
// uses at task-creation time to classify a delegation target as native or
// external-CLI (DelegateTaskState.Is3P). getRegistry is called at
// task-creation time, not construction time, so hot reloads are reflected
// automatically — see the getAgentRegistry field doc.
func (t *DelegateTool) SetAgentRegistry(getRegistry func() DelegateAgentRegistry) {
	t.getAgentRegistry = getRegistry
}

// SetSessionStore installs the transcript store DelegateTool reads from to
// build a running native task's recent-activity snapshot under
// action:"status" (W2). See the sessionStore field doc.
func (t *DelegateTool) SetSessionStore(store DelegateSessionStore) {
	t.sessionStore = store
}

// SetProgressReader installs the DelegateProgressReader action:"status"
// (G1 fix) reads a running native task's live tool-call-argument progress
// from. See the progressReader field and DelegateProgressReader's doc
// comments for what this adds over sessionStore above. A nil reader (never
// called) leaves action:"status" behaving exactly as before this fix.
func (t *DelegateTool) SetProgressReader(reader DelegateProgressReader) {
	t.progressReader = reader
}

// SetSessionManager installs the shared *SessionManager executeCancel uses
// to kill a cancelled child's own background shells (FR-028/BDD-29). See
// the sessionManager field doc. A nil sessionManager (never called) leaves
// action="cancel" behaving exactly as before this fix — a silent no-op on
// this specific side effect, matching every other optional capability.
func (t *DelegateTool) SetSessionManager(sm *SessionManager) {
	t.sessionManager = sm
}

// defaultOwnershipWalkMaxDepth bounds the ancestor-chain walk
// verifyCallerOwnsSession performs (FR-039/BDD-43) when
// SetOwnershipWalkMaxDepth is never called. pkg/tools cannot reference
// pkg/agent's own safety-backstop delegation-depth default directly, so this
// is a same-valued, independently-declared constant.
const defaultOwnershipWalkMaxDepth = 3

// SetOwnershipWalkMaxDepth overrides the ancestor-chain walk's depth bound
// (FR-039). Zero/negative values fall back to defaultOwnershipWalkMaxDepth.
//
// Production wiring resolves performance.max_delegation_depth through the
// shared effective-depth function before calling this setter. The ownership
// walk and delegation authorization therefore use the same bound; n<=0 keeps
// the local safety default for isolated callers.
func (t *DelegateTool) SetOwnershipWalkMaxDepth(n int) {
	if n > 0 {
		t.ownershipWalkMaxDepth = n
	}
}

func (t *DelegateTool) ownershipMaxDepth() int {
	if t.ownershipWalkMaxDepth > 0 {
		return t.ownershipWalkMaxDepth
	}
	return defaultOwnershipWalkMaxDepth
}

// SetDelegationDenyCheckerBackground installs the full delegation-policy gate
// (FR-6.2: trust set + mode("background") + depth) applied when async=true.
// Mirrors the pre-merge SpawnTool.SetDelegationDenyChecker exactly.
func (t *DelegateTool) SetDelegationDenyCheckerBackground(
	check func(ctx context.Context, targetAgentID string) *DelegationDenial,
) {
	t.delegationDenyBackground = check
}

func (t *DelegateTool) Name() string {
	return "delegate"
}

func (t *DelegateTool) Description() string {
	return "Delegate a task to a subagent, and control/monitor it afterward. " +
		"Choosing between these: delegate hands work to another agent now and returns immediately — " +
		"use it when you need the result inside this conversation. create_task files work as a card " +
		"on the board that runs on its own and is judged against its goal — use it for work that " +
		"outlives this conversation or that someone should see. A plan is for long-running, complex " +
		"implementations and higher-level planning: several tasks with an order and dependencies " +
		"between them, and an agent working on one of those tasks can itself delegate further. If the " +
		"work is a single lookup or one action you can do yourself, just do it — starting a child " +
		"costs time and one of a limited number of concurrent slots. " +
		"For a goal with two or more independent parts meant to run in parallel (for example several " +
		"files or deliverables written by different agents), prefer a plan over several parallel run " +
		"calls: load create_plan and execute_plan with ToolSearch (if your policy allows them). A plan's " +
		"members declare write_sets that plan-lint checks for overlap before anything runs, and the whole " +
		"plan is judged against one Definition of Done and can be stopped as a unit; parallel delegate " +
		"calls get no overlap check. Delegate directly for a single self-contained piece of work. " +
		"action=\"run\" (default) launches a session. It returns at once with the child's session_id " +
		"and whether it is running or queued (with its place in line). You get a message when the " +
		"child finishes, asks a question, or hits a problem. Check on it with delegate status, " +
		"redirect it with delegate steer, stop it with delegate cancel. A delegation is " +
		"force-cancelled after timeout_seconds (default 300s / 5 min) if it has not finished by then. " +
		"action=\"status\" checks on a previously-delegated session by its session_id — the only way " +
		"to address a child; use list_jobs to see everything you have outstanding. " +
		"action=\"inbox\" drains messages the child has pushed back to you (progress/" +
		"checkpoint/artifact/blocker/question/handback); action=\"inbox_ack\" acknowledges " +
		"them. action=\"steer\" injects an instruction at the child's next tool boundary " +
		"(NOT available for a delegation running on an external CLI, subagent_3p: " +
		"claude-code/codex/opencode — use respond or follow_up instead); " +
		"action=\"respond\" answers a child's open question by correlation_id — " +
		"always available for a delegation you started. " +
		"action=\"cancel\" stops a child (cooperatively by default; hard=true bypasses " +
		"the grace window). " +
		"action=\"follow_up\" warm-resumes a finished child with additional instructions. " +
		"action=\"peek\" reads a child's latest checkpoint/progress without side effects. " +
		"Optionally provide agent_id to target a specific agent from your delegation " +
		"allowlist; omit it to run a generic subagent under your own agent."
}

func (t *DelegateTool) Scope() ToolScope { return ScopeCore }

func (t *DelegateTool) Category() ToolCategory { return CategoryDelegation }

// delegateCriterionItemSchema is the per-item schema shared by the "goal"
// parameter's criteria/dod arrays below — the exact object shape
// parseDelegateCriterion (delegate_goal.go) accepts. It is a NARROWER subset
// of create_task/create_plan's own criteria/dod item schema (task.go/plan.go):
// delegate's goal only accepts kind "prose" or "check" — there is no
// "behavior" kind here, unlike the task/plan tools' criteria/dod, so it is
// not mirrored byte-for-byte, only shape-for-shape on the fields delegate's
// own parser actually reads.
func delegateCriterionItemSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The criterion statement (required).",
			},
			"kind": map[string]any{
				"type": "string",
				"enum": []string{"prose", "check"},
				"description": "prose: a free-text statement judged when the child's work is checked. " +
					"check: a shell command run to verify it. Optional — inferred from the payload (a " +
					"check payload => check, otherwise prose); an explicit kind mismatching its payload " +
					"is rejected.",
			},
			"check": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":            map[string]any{"type": "string", "description": "Shell command to run"},
					"expected_exit_code": map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
				},
				"description": "Required when kind is \"check\"; must be omitted for \"prose\".",
			},
			"judgment": map[string]any{
				"type":        "string",
				"enum":        []string{"boolean", "quantitative", "artifact"},
				"description": "How this criterion is scored. Optional, defaults to boolean.",
			},
		},
		"required": []string{"text"},
	}
}

func (t *DelegateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type": "string",
				"description": "The task for the subagent to complete. Required when action is \"run\" (the " +
					"default). DEPRECATED alias for \"text\" under action=\"follow_up\" — \"text\" wins when " +
					"both are present.",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Optional short label for the task (for display)",
			},
			"agent_id": map[string]any{
				"type": "string",
				"description": "Optional: the id of a specific agent to delegate to (must be in your " +
					"delegation allowlist). Omit to run a generic subagent under your own agent.",
			},
			"action": map[string]any{
				"type": "string",
				"enum": []string{"run", "status", "inbox", "inbox_ack", "steer", "respond", "cancel", "follow_up", "peek"},
				"description": "\"run\" (default) delegates a new task. \"status\" checks progress. \"inbox\" " +
					"drains child->parent messages. \"inbox_ack\" acknowledges them. \"steer\" injects an " +
					"instruction. \"respond\" answers an open question. \"cancel\" stops a child. " +
					"\"follow_up\" warm-resumes a finished child. \"peek\" reads latest checkpoint/progress.",
			},
			"session_id": map[string]any{
				"type": "string",
				"description": "The durable child session to target — the only way to address a " +
					"child. Required for status/inbox/inbox_ack/steer/respond/cancel/follow_up/peek.",
			},
			"goal": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"criteria": map[string]any{
						"type":        "array",
						"items":       delegateCriterionItemSchema(),
						"description": "Outcome-specific checks for this delegation.",
					},
					"dod": map[string]any{
						"type":        "array",
						"items":       delegateCriterionItemSchema(),
						"description": "Generic standing quality gates for this delegation (same shape as criteria).",
					},
				},
				"description": "Optional (action=\"run\" only): must be an object with a criteria and/or " +
					"dod array — a plain string is not accepted — and at least one item across the two " +
					"once the object is given. One sentence stating what 'done' means for this child, " +
					"and how you would check it. Set one for multi-step work or work you must verify " +
					"before relying on it; leave it off for a quick lookup or a single action.",
			},
			"snapshot": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"references": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Parent-named artifact path/ref strings (not contents) visible to the child.",
					},
					"notes": map[string]any{
						"type":        "string",
						"description": "Optional parent-authored notes.",
					},
				},
				"description": "Optional (action=\"run\" only): the DISCRETIONARY portion of the curated " +
					"context snapshot (deny-by-default, hard-capped). Over-cap is rejected, never truncated.",
			},
			"requested_skill": map[string]any{
				"type": "string",
				"description": "Optional (action=\"run\" only): the exact slug of a skill you want the " +
					"delegation target to start its first turn with already loaded. This is a hard request, " +
					"not a hint — resolved against the TARGET's own grant, never yours: if the target is " +
					"granted it, its first turn begins with the skill loaded and the result names it; if " +
					"the target is not granted it, or the slug does not resolve to any installed skill, the " +
					"whole delegation call fails instead of silently proceeding without it. Merely mentioning " +
					"a skill by name inside \"task\" does not have this effect — it is only a hopeful hint " +
					"the target's own judgement may or may not act on.",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Optional (action=\"run\" only): max seconds before this delegation is force-cancelled. 0 = default (5 min).",
			},
			"critical": map[string]any{
				"type":        "boolean",
				"description": "Optional (action=\"run\" only): continue running after the parent finishes gracefully.",
			},
			"message_ids": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Required for action=\"inbox_ack\": the message_ids to acknowledge.",
			},
			"since_cursor": map[string]any{
				"type":        "string",
				"description": "Optional (action=\"inbox\" only): opaque cursor — return only messages after this point.",
			},
			"max": map[string]any{
				"type":        "integer",
				"description": "Optional (action=\"inbox\" only): maximum messages to return.",
			},
			"text": map[string]any{
				"type": "string",
				"description": "Required for action=\"steer\"/\"respond\"/\"follow_up\": the instruction/answer/" +
					"new-instruction text (for follow_up, \"task\" is accepted as a deprecated alias).",
			},
			"correlation_id": map[string]any{
				"type":        "string",
				"description": "Required for action=\"respond\" (optional for \"steer\"): the open question this answers.",
			},
			"hard": map[string]any{
				"type": "boolean",
				"description": "Optional (action=\"cancel\" only, default false): false is a cooperative soft " +
					"cancel with grace; true bypasses the grace window immediately.",
			},
		},
		// Nothing is unconditionally required at the schema level — requiredness
		// is action-dependent and is enforced at runtime, mirroring ExecTool's
		// action-dispatch pattern.
		"required": []string{},
	}
}

func (t *DelegateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	return t.execute(ctx, args, nil)
}

// ExecuteAsync implements AsyncExecutor. The callback is passed through as a
// call parameter — never stored on the DelegateTool instance.
func (t *DelegateTool) ExecuteAsync(
	ctx context.Context,
	args map[string]any,
	cb AsyncCallback,
) *ToolResult {
	return t.execute(ctx, args, cb)
}

func (t *DelegateTool) execute(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	action, _ := args["action"].(string)
	if rawAction, present := args["action"]; present && rawAction != nil {
		if _, ok := rawAction.(string); !ok {
			return ErrorResult("action must be a string")
		}
	}
	if action == "" {
		action = "run"
	}

	// arch-M2 (Phase-2 review): FR-196 kill switch on the SYNC tool path. The
	// async consumer honors session_messaging.enabled per event; these seven
	// session-messaging-plane actions used to bypass it. run/status are
	// delegation spawn/query and are NOT gated.
	if isSessionMessagingAction(action) && !t.sessionMessagingPlaneEnabled() {
		return ErrorResult("delegate." + action + ": the session-messaging plane is disabled (session_messaging.enabled = false)")
	}

	switch action {
	case "run":
		return t.executeRun(ctx, args, cb)
	case "status":
		return t.executeStatus(ctx, args)
	case "inbox":
		return t.executeInbox(ctx, args)
	case "inbox_ack":
		return t.executeInboxAck(ctx, args)
	case "steer":
		return t.executeSteer(ctx, args)
	case "respond":
		return t.executeRespond(ctx, args, cb)
	case "cancel":
		return t.executeCancel(ctx, args)
	case "follow_up":
		return t.executeFollowUp(ctx, args, cb)
	case "peek":
		return t.executePeek(ctx, args)
	default:
		return ErrorResult(fmt.Sprintf(
			"invalid action %q: must be one of run, status, inbox, inbox_ack, steer, respond, cancel, follow_up, peek",
			action,
		))
	}
}

// ====================== ADR-053 §5.1: inbox/inbox_ack/steer/respond/cancel/follow_up/peek ======================

// DelegateInboxStore is the PARENT-side subset of *session.MessageInboxStore
// the delegate tool needs: draining/acking a child's messages, reading the
// per-child unacked ceiling count, and a side-effect-free peek. Distinct
// from message_parent.go's MessageParentInboxStore (the CHILD-side
// Append-only view) — least privilege per tool, each only gets the methods
// it actually calls.
type DelegateInboxStore interface {
	Drain(ownerKey, childSessionID, sinceCursor string, maxMessages int) ([]generated.SessionMessage, string, bool, error)
	Ack(ownerKey string, messageIDs []string) error
	// AckDetailed is Ack's richer sibling (M1, UAT 2026-08): it performs the
	// identical acknowledgement but additionally reports which requested
	// message_ids matched a real message ever appended under ownerKey
	// (Acknowledged) versus which did not (Unknown) — see
	// session.AckResult's doc comment. executeInboxAck uses this instead of
	// Ack so its reported count is truthful: a caller passing a wholly
	// fabricated message id used to get back "Acknowledged 1 message(s)."
	// regardless, silently drifting any reconciliation against that count.
	AckDetailed(ownerKey string, messageIDs []string) (*session.AckResult, error)
	UnackedCount(ownerKey, childSessionID string) (int, error)
	Peek(ownerKey, childSessionID string) (*session.PeekSnapshot, error)
}

// requiredStringArg extracts a required, non-blank string argument, or a
// descriptive error naming the missing/invalid field.
func requiredStringArg(args map[string]any, key string) (string, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return "", fmt.Errorf("%s is required", key)
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("%s is required and must be a non-empty string", key)
	}
	return s, nil
}

// callerOwnerKey resolves the CALLING agent's own durable inbox key — the
// same ToolTranscriptSessionID(ctx) value that was captured as the child's
// SteeringSessionID at `run` time (D16). Every parent-side action
// (inbox/inbox_ack/steer/respond/cancel/follow_up/peek) uses this exact
// resolution so a caller can only ever address inboxes/sessions it itself
// spawned.
func callerOwnerKey(ctx context.Context) string {
	return strings.TrimSpace(ToolTranscriptSessionID(ctx))
}

type delegatePrincipalContextKey struct{}

// WithDelegatePrincipal carries an already-authenticated human principal from
// the gateway into a delegate steering action. Tools never manufacture human
// identity: callers that do not supply this value are evaluated as agents.
func WithDelegatePrincipal(ctx context.Context, principal steer.Principal) context.Context {
	return context.WithValue(ctx, delegatePrincipalContextKey{}, principal)
}

func delegateHumanPrincipal(ctx context.Context) (steer.Principal, bool) {
	principal, ok := ctx.Value(delegatePrincipalContextKey{}).(steer.Principal)
	if !ok || principal.Kind != steer.PrincipalKindHuman || strings.TrimSpace(principal.ID) == "" {
		return steer.Principal{}, false
	}
	return principal, true
}

// verifyCallerOwnsSession (ADR-057 W12/FR-039/FR-040) rejects a gated
// delegate action whose caller is not an ANCESTOR of rec — a direct parent,
// grandparent, and so on up to the configured max delegation depth
// (SetOwnershipWalkMaxDepth) — in the SteeringSessionID chain (defense in
// depth: a session_id alone is guessable/loggable; ownership must also
// match at the handler).
//
// Pre-ADR-057, a plain `caller == rec.SteeringSessionID()` equality check was
// correct because SteeringSessionID was shared across an entire subtree (a
// parent's key was literally re-inherited down every generation) — which
// ALSO meant it accidentally permitted sibling/cousin reach (FR-040's
// "MUST be removed": any two sessions sharing the SAME parent — or the same
// distant ancestor — carried the identical SteeringSessionID value and thus
// passed the equality check against EACH OTHER's records, not just their
// real parent's). U13's SteeringSessionID redefinition (pkg/session/lifecycle.go's
// own doc comment: "names its DIRECT parent only — it is NOT re-inherited
// down the chain") already closed that leak by construction — a sibling's
// target now carries the immediate parent's key, never the caller's own —
// but it also silently broke the LEGITIMATE root-over-subtree case
// (BDD-42): a chat A that spawned child B, which spawned grandchild D, can
// no longer reach D via one-hop equality, because D's SteeringSessionID
// names B, not A. This walk restores that reach without reopening the
// sibling/cousin one: it climbs ONE hop per iteration (rec's own
// SteeringSessionID is depth 1, its parent's SteeringSessionID is depth 2,
// …), matching each hop against the caller, and stops — rejecting — the
// moment it either exhausts the depth bound (BDD-43) or reaches a link with
// no further LifecycleRecord to load (the root chat has none of its own,
// which is exactly the terminal, no-match case; a Load failure is never
// treated as an ownership match).
func (t *DelegateTool) verifyCallerOwnsSession(ctx context.Context, rec *session.LifecycleRecord) error {
	_, err := t.verifyCallerPrincipal(ctx, rec)
	return err
}

// verifyCallerPrincipal proves steering authority and returns the identity
// that must accompany the resulting action. An authenticated human is global
// steering authority. An agent must be the target's direct or transitive
// steering ancestor, walked exclusively through the durable SteeredBy edge.
func (t *DelegateTool) verifyCallerPrincipal(ctx context.Context, rec *session.LifecycleRecord) (steer.Principal, error) {
	if principal, ok := delegateHumanPrincipal(ctx); ok {
		return principal, nil
	}
	caller := callerOwnerKey(ctx)
	if caller == "" {
		return steer.Principal{}, fmt.Errorf("session %s is not steered by the calling principal", rec.SessionID)
	}
	ancestor := ""
	if rec.SteeredBy != nil {
		ancestor = strings.TrimSpace(rec.SteeredBy.SteeringSessionID)
	}
	maxDepth := t.ownershipMaxDepth()
	for depth := 0; depth < maxDepth; depth++ {
		if ancestor == "" {
			break
		}
		if ancestor == caller {
			return steer.Principal{Kind: steer.PrincipalKindAgent, ID: caller}, nil
		}
		if t.lifecycle == nil {
			break
		}
		parentRec, err := t.lifecycle.Load(ancestor)
		if err != nil {
			if !errors.Is(err, session.ErrLifecycleNotFound) {
				// A genuine I/O error (truncated/corrupt .jsonl, a
				// disk-full partial write, permissions) is NOT the same
				// signal as the expected not-found case below — collapsing
				// both into silent chain-end previously meant a corrupt
				// record was logged NOWHERE, and the operator debugging the
				// resulting "session X is not owned by the calling session"
				// error had no way to distinguish it from a real
				// cross-tenant attempt. Both still fail closed identically
				// (this walk's fail-closed posture is intentional and MUST
				// NOT weaken) — this only adds diagnosability for the case
				// that deserves it.
				slog.Warn("delegate: verifyCallerOwnsSession: ancestor lifecycle record failed to load (denying ownership, fail-closed)",
					"ancestor_session_id", ancestor, "depth", depth, "target_session_id", rec.SessionID, "error", err)
			}
			// No further lifecycle record to climb — either the expected
			// not-found case (ancestor names the root chat, which, being a
			// plain chat session never itself a delegated child, has no
			// LifecycleRecord of its own) or the logged I/O error above.
			// The chain ends here with no match either way — a Load
			// failure of ANY kind is never treated as an ownership match.
			break
		}
		if parentRec.SteeredBy == nil {
			break
		}
		ancestor = strings.TrimSpace(parentRec.SteeredBy.SteeringSessionID)
	}
	return steer.Principal{}, fmt.Errorf("session %s is not steered by the calling principal", rec.SessionID)
}
