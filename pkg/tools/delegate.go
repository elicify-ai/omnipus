package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/google/uuid"
)

// ADR-036 / docs/internal/specs/agent-delegation-spec.md — `delegate` is the
// single, unified delegation tool. It replaces the formerly-separate `spawn`
// (async/background), `run_subagent` (sync/await), and `check_spawn_status`
// tools with one tool, one schema, and one piece of task-status state.
//
// FR-D2 (the bug this merge exists to fix): before this merge, `spawn` called
// SubTurnSpawner.SpawnSubTurn directly in a goroutine, entirely bypassing the
// legacy SubagentManager.tasks map that `check_spawn_status` read from —
// checking on a spawn-created task always reported "no subagents have been
// spawned yet." DelegateTool's own `tasks` map is now the SINGLE state store
// both the async path writes to and `action: "status"` reads from — no
// second, disconnected data structure exists.

// SubTurnSpawner is an interface for spawning sub-turns.
// This avoids circular dependency between tools and agent packages.
type SubTurnSpawner interface {
	SpawnSubTurn(ctx context.Context, cfg SubTurnConfig) (*ToolResult, error)
}

// SubTurnConfig holds configuration for spawning a sub-turn. This is the
// shared underlying primitive DelegateTool's async and sync paths both call
// (Async is the only differentiator) — unchanged in shape from the
// pre-merge spawn/run_subagent split, since pkg/agent/subturn.go converts
// this field-by-field into its own agent.SubTurnConfig.
type SubTurnConfig struct {
	Model              string
	Tools              []Tool
	SystemPrompt       string
	MaxTokens          int
	Temperature        float64
	Async              bool          // true for background delegation, false for await (blocking) delegation
	Critical           bool          // continue running after parent finishes gracefully
	Timeout            time.Duration // 0 = use default (5 minutes)
	MaxContextRunes    int           // 0 = auto, -1 = no limit, >0 = explicit limit
	ActualSystemPrompt string
	// TargetAgentID, when non-empty, is the configured agent the sub-turn is
	// delegating TO (e.g., a worker). When set, subturn.go resolves the
	// delegate's soul (AgentConfig.Soul or, for seeded base agents, the
	// compiled coreagent.GetPrompt) and uses it as the ActualSystemPrompt so
	// the child turn runs with system=soul + user=task, uniformly across the
	// native and external-cli executors. Empty means "delegate the parent's
	// own agent" — the parent's own soul applies.
	TargetAgentID      string
	InitialMessages    []providers.Message
	InitialTokenBudget *atomic.Int64 // Shared token budget for team members; nil if no budget
	// TaskLabel is the optional human-readable label for the sub-turn task (FR-H-004).
	// Populated from delegate's "label" argument. Used in the subagent_start WS frame.
	TaskLabel string
	// ResolvedMaxDepth, when non-nil, is the effective onward-delegation depth
	// cap the delegation-policy gate already authorized this specific call
	// against (the tighter of a matched delegation-graph edge's own Depth and
	// the global SubTurn.MaxDepth ceiling). When set, the spawn-time depth
	// check uses this value instead of independently re-deriving a possibly
	// different default, so an explicit per-edge Depth is never silently
	// overridden (#477). nil means "no override — use the spawner's own
	// default depth resolution."
	ResolvedMaxDepth *int

	// ContextSnapshot carries the DISCRETIONARY portion of the ADR-053 D1
	// curated context snapshot (R§8.5) — see ContextSnapshot's own doc
	// comment. nil means no discretionary snapshot. Mirrors (and is
	// converted 1:1 into) agent.SubTurnConfig.ContextSnapshot by
	// AgentLoopSpawner.SpawnSubTurn — the same tools<->agent type-doubling
	// this whole struct already exists to work around (see this type's own
	// doc comment above).
	ContextSnapshot *ContextSnapshot

	// DelegateSessionID, when non-empty, is the ADR-053 durable session_id
	// (S2) DelegateTool minted and persisted a `queued` LifecycleRecord
	// under BEFORE calling Spawn — see agent.SubTurnConfig.DelegateSessionID
	// (the sibling field this converts into) for why reusing this exact
	// value as the child's turn/steering-queue identity matters.
	DelegateSessionID string

	// IsResume marks this dispatch as a WARM RESUME of an existing session
	// (native `delegate follow_up` on a terminal session) rather than a
	// brand-new mint — see agent.SubTurnConfig.IsResume (the sibling field
	// this converts into) for the full rationale. false for every other
	// caller (delegate.run, team, evaluator-optimizer, ...), unchanged.
	IsResume bool
}

// ContextSnapshot is the tools-side mirror of agent.ContextSnapshot (ADR-053
// D1/R§8.5's curated context snapshot, discretionary portion only — parent-
// named artifact references, not contents, plus optional notes). Kept as a
// separate type from agent.ContextSnapshot for the identical reason
// SubTurnConfig itself is duplicated across the two packages: avoiding a
// tools<->agent import cycle (agent already imports tools).
type ContextSnapshot struct {
	References []string
	Notes      string
}

// ErrSnapshotOverCap is returned by ValidateContextSnapshot when the
// DISCRETIONARY portion (references + notes) exceeds its byte or count cap.
// The MANDATORY core (task prompt + criteria + identity) is NEVER subject
// to this check (m4) — only what this function is handed.
var ErrSnapshotOverCap = errors.New("tools: curated context snapshot exceeds the discretionary cap")

// defaultSnapshotMaxBytes/defaultSnapshotMaxRefs mirror
// agent.defaultSnapshotMaxBytes/defaultSnapshotMaxRefs (ADR §Contract
// Surface: 8 KiB / 50 refs) — duplicated here for the same
// tools<->agent-cycle reason as ContextSnapshot itself.
const (
	defaultSnapshotMaxBytes = 8 * 1024
	defaultSnapshotMaxRefs  = 50
)

// ValidateContextSnapshot enforces R§8.5's deny-by-default, hard-capped
// curated context snapshot on the DISCRETIONARY portion only. See
// agent.ValidateContextSnapshot (the byte-for-byte identical sibling
// function on the agent-package side) for the full contract.
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

// DelegateTaskState is the single source of truth for a background
// (async=true) delegated task's status — written by DelegateTool's own async
// path and read by action:"status" (FR-D2). It replaces the legacy,
// disconnected SubagentTask/SubagentManager.tasks pair.
type DelegateTaskState struct {
	ID            string
	Task          string
	Label         string
	AgentID       string
	OriginChannel string
	OriginChatID  string
	Status        string // running | completed | failed | canceled
	Result        string
	Created       int64

	// SessionID (ADR-057 W21b — RE-POINTED, deliberately, not left as a
	// silent byproduct of FR-007 landing elsewhere in this change set) is
	// the DELEGATING PARENT's own transcript session id at task-creation
	// time — captured from ToolTranscriptSessionID(ctx) inside the caller's
	// own tool-execution context, i.e. the caller's OWN durable session id
	// (pkg/agent/subturn.go's TranscriptSessionID: childID, post-FR-007),
	// NOT the spawned child's. Retained for display/back-compat only
	// (delegateFormatTask does not currently render it, and no other
	// consumer in this package reads it). Pre-ADR-057, this field doubled
	// as "the session a running native task's activity snapshot is read
	// from", because a delegated child used to write its OWN narration into
	// its PARENT's shared transcript — that assumption broke silently the
	// moment FR-007 gave every child its own real session, and
	// recentActivityLines has been re-pointed at DelegateSessionID instead
	// (FR-043; see that field's own doc comment). Empty when no transcript
	// session context was available at creation time (e.g. a direct
	// programmatic Execute call, as in most of this file's tests).
	SessionID string
	// SpawnCallID is this delegate tool call's own ID — the value a spawned
	// child sub-turn's transcript entries carry back as
	// session.TranscriptEntry.ParentSpawnCallID (see that field's doc
	// comment and pkg/agent/subturn.go's parentSpawnCallID). Captured at
	// task-creation time from ToolCallID(ctx). Used to filter SessionID's
	// transcript down to just this task's own activity.
	SpawnCallID string
	// Is3P is true when this task's target agent dispatches via an external
	// CLI runner (subagent_3p: claude-code/codex/opencode — see
	// runner.DispatchKindExternalCLI) rather than natively inside the
	// Omnipus agent loop. Resolved ONCE at task-creation time via
	// DelegateAgentRegistry.IsExternalCLI, so a registry/config change
	// mid-flight cannot flip a task's own snapshot eligibility
	// inconsistently. By design (operator-confirmed scope for W2),
	// external-CLI dispatch is treated as batch/report-on-completion for
	// action:"status" purposes even though runExternalCLISubTurn's own
	// narration DOES land in the same ParentSpawnCallID-tagged transcript
	// entries a native task's does (see recordExternalToolCall /
	// pkg/agent/external_dispatch.go's appendIntermediateAssistantTranscript
	// calls) — a running Is3P task's action:"status" never attempts a live
	// transcript snapshot regardless, and instead renders a fixed
	// no-live-progress note.
	Is3P bool

	// DelegateSessionID is the ADR-053 durable session_id (S2) this task's
	// child was spawned under — distinct from SessionID above. status/
	// inbox/steer/respond/cancel/follow_up/peek all address a child by THIS
	// id, and (ADR-057 FR-043) so does recentActivityLines: post-FR-007 a
	// delegated child writes its OWN transcript into its OWN session
	// (DelegateSessionID), never into SessionID (the delegating PARENT's
	// own transcript id at dispatch time — see SessionID's own doc comment
	// above), so reading SessionID back for a running task's activity
	// snapshot silently found nothing the moment FR-007 landed elsewhere in
	// this change set. Fixed here; DelegateSessionID is the only correct
	// key for that read.
	DelegateSessionID string

	// LastStatusRead is the UnixMilli timestamp of this task's most recent
	// action:"status" read (ADR-057 FR-045/FR-087, BDD-52) — stamped by
	// getTaskCopy/listTaskCopies on every read, and initialized to the
	// task's own Created time at registration so a never-polled task still
	// ages from a real timestamp rather than from the zero value (which
	// would read as 1970 and make it immediately eligible for eviction).
	// evictStaleTasksLocked uses this, not Created, to decide whether a
	// terminal task has gone stale long enough to reclaim — a task still
	// being actively polled must never be evicted out from under a caller
	// mid-conversation.
	LastStatusRead int64
}

// delegateSessionIDCtxKey is the context key carrying a child turn's own
// ADR-053 durable session_id (distinct from the shared transcript session
// id — pkg/tools.ToolTranscriptSessionID). Defined here (not
// pkg/tools/base.go, outside this wave's write-set) following the exact
// same WithX/ToolX accessor-pair convention every other per-turn context
// carrier in this package already uses.
type delegateSessionIDCtxKey struct{}

// WithDelegateSessionID returns a child context carrying the durable
// ADR-053 session_id for the turn currently executing. Set by
// pkg/agent/subturn.go's spawnSubTurn on the child's own turn context, so a
// child's OWN tool calls (message_parent, and any future session-aware
// tool) can resolve their own durable identity without conflating it with
// the shared transcript session id.
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
	// produced the most recent delta.
	ArgsBytes int
	// TotalArgsBytes is the byte count accumulated across every tool call in
	// the current LLM response so far (may exceed ArgsBytes when more than
	// one tool call is in flight in the same response).
	TotalArgsBytes int
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
// (SubTurnSpawner, DelegateAgentRegistry, DelegateSessionStore) to avoid a
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

	spawner      SubTurnSpawner
	defaultModel string
	maxTokens    int
	temperature  float64

	// spawnMarker, when non-nil, is the DelegateSpawnMarker seam executeAsync
	// calls (synchronously, before dispatching the async spawn goroutine) to
	// record that a delegate spawn is genuinely imminent for the delegating
	// parent's own identity. Wired automatically by SetSpawner via a type
	// assertion against the concrete spawner passed in — see SetSpawner's
	// doc comment. nil is a silent no-op (no marker recorded), matching
	// every other optional capability on this tool.
	spawnMarker DelegateSpawnMarker

	// asyncWG tracks the detached goroutines executeAsync launches. Background
	// delegation is deliberately fire-and-forget for the CALLER (the parent
	// turn moves on immediately — see executeAsync's Critical:true comment),
	// but the goroutine keeps writing to the lifecycle store after the caller
	// returns. Anything that tears down the stores those writes target must be
	// able to wait for them first; WaitForAsyncTasks is that seam.
	asyncWG sync.WaitGroup

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

	mu     sync.Mutex
	tasks  map[string]*DelegateTaskState
	nextID int
	// sessionIndex maps a DelegateSessionID (ADR-053 durable id) back to its
	// legacy taskID (t.tasks' key), so status/inbox/etc. can resolve either
	// the legacy task_id or the new session_id to the same DelegateTaskState.
	sessionIndex map[string]string
	// taskRetentionCap/taskRetentionTTL bound t.tasks/t.sessionIndex
	// (FR-045/FR-087, BDD-52) — see SetTaskRetentionPolicy,
	// defaultDelegateTaskRetentionCap and defaultDelegateTaskTTL.
	taskRetentionCap int
	taskRetentionTTL time.Duration

	// delegationDenyBackground applies the full delegation-policy gate
	// (FR-6.2: trust set + mode("background") + depth) for async=true calls.
	// This is the ONLY gate for the background mode (ADR-037 retired the
	// legacy trust-only allowlistCheck fallback — it was only ever consulted
	// when this was nil, which never happens in production wiring).
	delegationDenyBackground func(ctx context.Context, targetAgentID string) *DelegationDenial
	// delegationDenyAwait applies the full delegation-policy gate (FR-6.2:
	// trust set + mode("await") + depth) for async=false calls. This is the
	// ONLY gate for the await mode (ADR-037 retired the legacy trust-only
	// delegateChecker fallback — same reasoning as delegationDenyBackground).
	delegationDenyAwait func(ctx context.Context, targetAgentID string) *DelegationDenial

	// delegationDepthResolver, when non-nil, resolves the effective onward-
	// delegation depth cap for a specific target — the SAME cap the deny
	// checker above already authorized this call against. Returns nil for "no
	// override" (fall back to the spawner's own default depth resolution) or
	// a pointer to the resolved cap. Threaded into SubTurnConfig.ResolvedMaxDepth
	// so the spawn-time depth check never independently re-derives a different
	// number than the one this gate already authorized (#477). Field name and
	// setter name are pinned — do not rename (relied on by pkg/agent/loop.go).
	delegationDepthResolver func(ctx context.Context, targetAgentID string) *int

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
		tasks:            make(map[string]*DelegateTaskState),
		sessionIndex:     make(map[string]string),
		nextID:           1,
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
// pkg/agent/steering.go); defined as an interface here (mirroring
// SubTurnSpawner above) to avoid a tools<->agent import cycle.
type DelegateSteeringSink interface {
	EnqueueSteeringMessage(scope, agentID string, msg providers.Message) error
}

// DelegateSpawnMarker lets DelegateTool record — synchronously, on the
// dispatching goroutine, BEFORE the goroutine that will actually spawn the
// child sub-turn is even launched — that a delegate spawn is genuinely about
// to happen for a given identity (sessionID, or the (channel, chatID) Tier B
// fallback form). This exists to close a real gap: a Stop click's
// RequestCancel decides whether to arm a pre-registration cancel latch via
// turnImminentForIdentity (pkg/agent/cancel_prearm.go), whose ONLY
// production evidence source is al.sessionWorkers — populated exclusively
// by the top-level inbound-message dispatch loop. A delegate sub-turn NEVER
// goes through that loop (executeAsync below dispatches straight to
// SpawnSubTurn on a bare goroutine), so without this marker, a Stop landing
// between "the delegating parent's own turn finished" and "the child has
// registered" finds no active turn AND no dispatcher evidence, and the
// cancel is silently lost — precisely the bug this closes.
//
// Satisfied by *agent.AgentLoopSpawner (pkg/agent/subturn.go), the SAME
// concrete type already passed to SetSpawner as a SubTurnSpawner — see
// SetSpawner's own doc comment for how the two interfaces are wired
// together from that one call. Defined as a SEPARATE interface (not folded
// into SubTurnSpawner itself) so a test-only SubTurnSpawner mock (this
// package's own tests construct several) is never forced to implement a
// marker method it has no use for; DelegateTool treats an unwired marker
// (nil) as a silent no-op, matching every other optional capability this
// tool already accepts via a setter (cancelSoft/cancelHard,
// sessionMessagingEnabled, etc.).
//
// Deliberately has NO Clear method: clearing the marker is entirely
// pkg/agent's own responsibility (spawnSubTurn clears it the instant the
// child registers, or on any early return that never reaches registration —
// see subturn.go's pendingSpawnKeysForThisCall/registeredForCancel), which
// never needs to cross the tools<->agent boundary at all.
type DelegateSpawnMarker interface {
	MarkPendingDelegateSpawn(sessionID, channel, chatID string)
}

// defaultCancelGrace is the cooperative-stop grace window before the hard
// RequestCancel backstop fires when SetCancelGrace is never called
// (session_messaging.cancel_grace default, FR-195).
const defaultCancelGrace = 5 * time.Second

// WaitForAsyncTasks blocks until every in-flight background (async=true)
// delegation goroutine has finished writing its terminal lifecycle state.
//
// Background delegation is fire-and-forget for the CALLER by design, so the
// goroutine outlives the Execute call that started it and keeps writing to the
// lifecycle store afterwards. Any caller that is about to tear down the
// storage those writes target MUST wait here first, or the writes race the
// teardown. Tests rooted at t.TempDir() are the primary case (the temp dir is
// removed the moment the test body returns); a graceful-shutdown path that
// swaps stores would be another.
//
// This does NOT cancel anything — it only waits. Cancellation is the caller's
// ctx, which the goroutine already honors.
func (t *DelegateTool) WaitForAsyncTasks() {
	t.asyncWG.Wait()
}

// SetSpawner sets the SubTurnSpawner used for both async and sync delegation.
//
// If spawner ALSO implements DelegateSpawnMarker (as *agent.AgentLoopSpawner
// does, in the real production wiring — pkg/agent/subturn.go), it is
// automatically installed as this tool's pending-spawn marker too, via a
// plain interface type assertion. This is a deliberate "one setter wires
// both capabilities" choice, not an oversight: production has exactly one
// real SubTurnSpawner implementation and it always supports marking, so a
// second SetSpawnMarker call at every wiring site would be pure
// boilerplate; a test-only SubTurnSpawner mock that does NOT implement
// DelegateSpawnMarker simply leaves t.spawnMarker nil (the type assertion's
// ok is false), which is the correct, harmless "no marker configured"
// behavior for a test that never exercises this path. Calling SetSpawner
// again with a spawner that does NOT implement the marker interface clears
// any previously-wired marker rather than leaving a stale one from an
// earlier call — this setter is the single source of truth for both
// fields, never a partial update.
func (t *DelegateTool) SetSpawner(spawner SubTurnSpawner) {
	t.spawner = spawner
	marker, _ := spawner.(DelegateSpawnMarker)
	t.spawnMarker = marker
}

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
// pkg/agent's own safety-backstop delegation-depth default
// (defaultMaxSubTurnDepth, currently 3) directly — that package boundary
// already exists for every other AgentLoop capability this tool consumes
// via a setter (see delegationDepthResolver) — so this is a same-valued,
// independently-declared constant, not a shared symbol.
const defaultOwnershipWalkMaxDepth = 3

// SetOwnershipWalkMaxDepth overrides the ancestor-chain walk's depth bound
// (FR-039). Zero/negative values fall back to defaultOwnershipWalkMaxDepth.
//
// PRODUCTION WIRING GAP (flagged, not fixed, by this comment): unlike
// delegationDepthResolver (SetDelegationDepthResolver, wired in
// pkg/agent/loop.go alongside the deny-checker setters for this same
// delegateTool), nothing in the production call graph calls this setter —
// its only callers repo-wide are this package's own tests. Onward-
// delegation depth is fully operator-configurable
// (cfg.Agents.Defaults.SubTurn.MaxDepth — pkg/agent/delegation_depth.go's
// buildDelegationDepthResolver reads this exact same field as its
// globalDepthCap), but this walk's bound stays hardcoded at
// defaultOwnershipWalkMaxDepth (3) regardless of that config. An operator
// who raises max_depth beyond 3 gets cancel/steer/peek/respond/follow_up
// ownership errors on a legitimate deeper descendant that are
// indistinguishable from a real cross-tenant attempt. The fix is a
// one-line call in pkg/agent/loop.go, right after the existing
// SetDelegationDepthResolver wiring for this same delegateTool
// (currently ~line 1787, inside registerSharedTools):
//
//	delegateTool.SetOwnershipWalkMaxDepth(cfg.Agents.Defaults.SubTurn.MaxDepth)
//
// (n<=0 already no-ops back to today's default via this setter, so that
// call is safe unconditionally — an unset config leaves current behavior
// unchanged.) Not made here: pkg/agent/loop.go is outside this file's
// ownership for this change.
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

// defaultDelegateTaskRetentionCap/defaultDelegateTaskTTL bound
// t.tasks/t.sessionIndex (FR-045/FR-087, BDD-52) when
// SetTaskRetentionPolicy is never called: an install that runs many
// delegations over a long uptime must not grow these maps without bound.
const (
	defaultDelegateTaskRetentionCap = 1000
	defaultDelegateTaskTTL          = time.Hour
)

// SetTaskRetentionPolicy overrides the retention bound (C, FR-087) and TTL
// (T, FR-045) governing t.tasks/t.sessionIndex eviction. Zero/negative
// values fall back to the defaults above.
//
// Parameter named retentionCap, not cap: the predeclared built-in `cap()`
// must stay callable unshadowed inside this function's own body (and any
// future edit to it) — golangci-lint's predeclared check flags a parameter
// sharing that name.
func (t *DelegateTool) SetTaskRetentionPolicy(retentionCap int, ttl time.Duration) {
	if retentionCap > 0 {
		t.taskRetentionCap = retentionCap
	}
	if ttl > 0 {
		t.taskRetentionTTL = ttl
	}
}

// taskCap returns the configured retention bound (C, FR-087) —
// evictStaleTasksLocked's second pass enforces it.
func (t *DelegateTool) taskCap() int {
	if t.taskRetentionCap > 0 {
		return t.taskRetentionCap
	}
	return defaultDelegateTaskRetentionCap
}

func (t *DelegateTool) taskTTL() time.Duration {
	if t.taskRetentionTTL > 0 {
		return t.taskRetentionTTL
	}
	return defaultDelegateTaskTTL
}

// isTerminalDelegateStatus reports whether status is one of the three
// terminal DelegateTaskState.Status values eviction is scoped to
// (FR-045/FR-087) — a "running" task is never evicted regardless of age.
func isTerminalDelegateStatus(status string) bool {
	switch status {
	case "completed", "failed", "canceled":
		return true
	}
	return false
}

// evictStaleTasksLocked removes terminal DelegateTaskState entries whose
// last action:"status" read (getTaskCopy stamps LastStatusRead on a
// targeted single-task read; a never-polled task ages from its own Created
// time) is older than the configured TTL (FR-045), keeping
// t.tasks/t.sessionIndex bounded (FR-087, BDD-52) without evicting a task
// still within its TTL window (BDD-52's "But" clause, test #93) — which
// would otherwise break a caller's next action:"status" poll for it.
// Callers MUST already hold t.mu. Runs as part of the tool's own
// bookkeeping (every new task registration in executeAsync/executeSync) —
// FR-045 requires no external caller/ticker, and this satisfies it without
// adding a goroutine to manage.
//
// Second pass — FR-087's cap (C), previously dead code: a fleet of terminal
// tasks that are all still individually within their own TTL window (e.g. a
// caller polling every one of them faster than TTL elapses) would otherwise
// grow t.tasks/t.sessionIndex without bound regardless of the configured
// retention cap, since the TTL sweep above is the ONLY mechanism that ran
// before this fix (taskCap had no other reference in the repo). When the
// map is still over taskCap() after the TTL sweep, evict the
// LEAST-RECENTLY-READ terminal tasks first until at/under cap — the same
// "actively polled survives" ordering as the TTL sweep (a task with a
// fresh LastStatusRead is evicted last, so an in-progress poll loop is
// never starved out from under the caller). Running tasks are NEVER
// evicted by either mechanism (isTerminalDelegateStatus), so the cap is a
// best-effort bound when running tasks alone already exceed it.
func (t *DelegateTool) evictStaleTasksLocked() {
	cutoff := t.now().Add(-t.taskTTL())
	for id, st := range t.tasks {
		if !isTerminalDelegateStatus(st.Status) {
			continue
		}
		if time.UnixMilli(st.LastStatusRead).After(cutoff) {
			continue
		}
		delete(t.tasks, id)
		if st.DelegateSessionID != "" {
			delete(t.sessionIndex, st.DelegateSessionID)
		}
	}

	limit := t.taskCap()
	if len(t.tasks) <= limit {
		return
	}
	type terminalAge struct {
		id   string
		read int64
	}
	terminal := make([]terminalAge, 0, len(t.tasks))
	for id, st := range t.tasks {
		if isTerminalDelegateStatus(st.Status) {
			terminal = append(terminal, terminalAge{id: id, read: st.LastStatusRead})
		}
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].read < terminal[j].read })
	excess := len(t.tasks) - limit
	for i := 0; i < excess && i < len(terminal); i++ {
		id := terminal[i].id
		if st, ok := t.tasks[id]; ok && st.DelegateSessionID != "" {
			delete(t.sessionIndex, st.DelegateSessionID)
		}
		delete(t.tasks, id)
	}
}

// SetDelegationDenyCheckerBackground installs the full delegation-policy gate
// (FR-6.2: trust set + mode("background") + depth) applied when async=true.
// Mirrors the pre-merge SpawnTool.SetDelegationDenyChecker exactly.
func (t *DelegateTool) SetDelegationDenyCheckerBackground(
	check func(ctx context.Context, targetAgentID string) *DelegationDenial,
) {
	t.delegationDenyBackground = check
}

// SetDelegationDenyCheckerAwait installs the full delegation-policy gate
// (FR-6.2: trust set + mode("await") + depth) applied when async=false.
// Mirrors the pre-merge SubagentTool.SetDelegationDenyChecker exactly.
func (t *DelegateTool) SetDelegationDenyCheckerAwait(
	check func(ctx context.Context, targetAgentID string) *DelegationDenial,
) {
	t.delegationDenyAwait = check
}

// SetDelegationDepthResolver installs the effective-depth-cap resolver (#477).
// See the delegationDepthResolver field doc. Name pinned — relied on by
// pkg/agent/loop.go's registration wiring.
func (t *DelegateTool) SetDelegationDepthResolver(resolve func(ctx context.Context, targetAgentID string) *int) {
	t.delegationDepthResolver = resolve
}

func (t *DelegateTool) Name() string {
	return "delegate"
}

func (t *DelegateTool) Description() string {
	return "Delegate a task to a subagent, and control/monitor it afterward. " +
		"action=\"run\" (default) delegates a new task — by default in the background " +
		"(async=true), returning immediately with a task_id/session_id; set async=false to " +
		"block and receive the result inline. A delegation is force-cancelled after " +
		"timeout_seconds (default 300s / 5 min) if it has not finished by then. " +
		"action=\"status\" checks on a previously-delegated task/session; with no " +
		"task_id/session_id given, it lists all tasks currently visible to you instead — " +
		"this is the tool's discovery affordance for what you have outstanding. " +
		"action=\"inbox\" drains messages the child has pushed back to you (progress/" +
		"checkpoint/artifact/blocker/question/handback); action=\"inbox_ack\" acknowledges " +
		"them. action=\"steer\" injects an instruction at the child's next tool boundary; " +
		"action=\"respond\" answers a child's open question by correlation_id — both are " +
		"always available for a delegation you started. " +
		"action=\"cancel\" stops a child (cooperatively by default; hard=true bypasses " +
		"the grace window). " +
		"action=\"follow_up\" warm-resumes a finished child with additional instructions. " +
		"action=\"peek\" reads a child's latest checkpoint/progress without side effects. " +
		"Optionally provide agent_id to target a specific agent from your delegation " +
		"allowlist; omit it to run a generic subagent under your own agent."
}

func (t *DelegateTool) Scope() ToolScope       { return ScopeCore }
func (t *DelegateTool) Category() ToolCategory { return CategoryDelegation }

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
			"async": map[string]any{
				"type": "boolean",
				"description": "Whether to run in the background (true, the default) and return immediately " +
					"with a task_id, or block until the delegated turn completes (false) and return its " +
					"result inline.",
			},
			"action": map[string]any{
				"type": "string",
				"enum": []string{"run", "status", "inbox", "inbox_ack", "steer", "respond", "cancel", "follow_up", "peek"},
				"description": "\"run\" (default) delegates a new task. \"status\" checks progress. \"inbox\" " +
					"drains child->parent messages. \"inbox_ack\" acknowledges them. \"steer\" injects an " +
					"instruction. \"respond\" answers an open question. \"cancel\" stops a child. " +
					"\"follow_up\" warm-resumes a finished child. \"peek\" reads latest checkpoint/progress.",
			},
			"task_id": map[string]any{
				"type": "string",
				"description": "The task_id to check (e.g. \"delegate-1\"), used with action=\"status\". " +
					"When omitted under action=\"status\", all visible tasks are listed instead. DEPRECATED " +
					"alias for session_id — session_id wins when both are present.",
			},
			"session_id": map[string]any{
				"type": "string",
				"description": "The durable child session to target. Required for status/inbox/inbox_ack/" +
					"steer/respond/cancel/follow_up/peek.",
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
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Optional (action=\"run\" only): max seconds before this delegation is force-cancelled. 0 = default (5 min).",
			},
			"critical": map[string]any{
				"type":        "boolean",
				"description": "Optional (action=\"run\" only): continue running after the parent finishes gracefully.",
			},
			"allow_blocking_question": map[string]any{
				"type": "boolean",
				"description": "Optional (action=\"run\" with wait/async=false only): permit a bounded human-" +
					"routed wait on a child question instead of the default rejection.",
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

func (t *DelegateTool) executeRun(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	task, ok := args["task"].(string)
	if !ok || strings.TrimSpace(task) == "" {
		return ErrorResult("task is required and must be a non-empty string")
	}

	label, _ := args["label"].(string)

	// agent_id is OPTIONAL (omit it to run a generic subagent under the
	// caller's own agent), but when the caller DOES supply the key, it must
	// not be blank — an empty string used to be silently accepted and
	// treated identically to "omitted", spawning a generic/default subagent
	// instead of the (presumably named) target the caller intended. Mirrors
	// the "task is required and must be a non-empty string" / shell.go's
	// "command is required and must be a non-empty string" validation style,
	// adapted for an optional field: only PRESENT-but-blank is rejected.
	var agentID string
	if rawAgentID, present := args["agent_id"]; present && rawAgentID != nil {
		s, ok := rawAgentID.(string)
		if !ok {
			return ErrorResult("agent_id must be a string")
		}
		if strings.TrimSpace(s) == "" {
			return ErrorResult("agent_id must be a non-empty string when provided; omit it to run a generic subagent")
		}
		agentID = s
	}

	async := true
	if rawAsync, present := args["async"]; present && rawAsync != nil {
		b, ok := rawAsync.(bool)
		if !ok {
			return ErrorResult("async must be a boolean")
		}
		async = b
	}

	// timeout_seconds was documented in the schema but never actually read
	// anywhere — every delegated sub-turn silently used the hardcoded
	// defaultSubTurnTimeout (5 minutes, pkg/agent/subturn.go) regardless of
	// what the caller requested. 0/absent means "no override — use the
	// spawner's own default", matching the schema's "0 = default (5 min)"
	// wording; a nonzero value is bounds-checked and threaded into
	// SubTurnConfig.Timeout below.
	timeout, timeoutErr := resolveDelegateTimeoutSeconds(args)
	if timeoutErr != nil {
		return ErrorResult(timeoutErr.Error())
	}

	// R§8.5 curated context snapshot — deny-by-default, hard-capped
	// discretionary portion. Rejected here (never silently truncated) if
	// over cap.
	var snap *ContextSnapshot
	if raw, present := args["snapshot"]; present && raw != nil {
		rawMap, ok := raw.(map[string]any)
		if !ok {
			return ErrorResult("snapshot must be an object")
		}
		snap = &ContextSnapshot{}
		if refsRaw, present := rawMap["references"]; present && refsRaw != nil {
			refsAny, ok := refsRaw.([]any)
			if !ok {
				return ErrorResult("snapshot.references must be an array of strings")
			}
			for _, r := range refsAny {
				s, ok := r.(string)
				if !ok {
					return ErrorResult("snapshot.references must be an array of strings")
				}
				snap.References = append(snap.References, s)
			}
		}
		if notesRaw, present := rawMap["notes"]; present && notesRaw != nil {
			s, ok := notesRaw.(string)
			if !ok {
				return ErrorResult("snapshot.notes must be a string")
			}
			snap.Notes = s
		}
	}
	if err := ValidateContextSnapshot(snap, t.snapshotMaxBytes, t.snapshotMaxRefs); err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}

	// Delegation policy gate (FR-6.2): trust set + mode + depth, mode selected
	// by the async flag ("background" vs "await") — applied identically
	// regardless of async value (FR-D3). ADR-037: this is now the ONLY gate —
	// the legacy trust-only allowlistCheck/delegateChecker fallbacks (consulted
	// only when these were nil, which never happened in production) are
	// retired.
	//
	// FAIL CLOSED, not open, when no checker is wired: an unwired deny-checker
	// is a configuration error, never a permission grant. This is unreachable
	// in today's production wiring — pkg/agent/loop.go's registerSharedTools
	// unconditionally calls SetDelegationDenyCheckerBackground/Await for every
	// agent — but removing the legacy fallback (which was itself deny-by-
	// default: config.IsDelegationAllowed/CanSpawnSubagent both returned false
	// on an unset policy) must not also remove the safety net for the NEXT
	// wiring bug: a new agent-construction path, a v0.3 plugin-system entry
	// point, or a refactor slip that forgets to call the setter. Do NOT
	// "simplify" this back to fail-open — CLAUDE.md Hard Constraint #6 exists
	// precisely to forbid a silent runtime default here.
	if async {
		if t.delegationDenyBackground != nil {
			if denial := t.delegationDenyBackground(ctx, agentID); denial != nil {
				return DelegationDeniedResult("delegate", denial)
			}
		} else {
			slog.Error("delegate: no background delegation-deny checker installed — denying by default",
				"agent_id", agentID)
			return DelegationDeniedResult("delegate", &DelegationDenial{
				Reason:        "delegation is not configured for this agent (no policy gate installed) — denying by default",
				Policy:        DenyTrustSet,
				TargetAgentID: agentID,
			})
		}
	} else {
		if t.delegationDenyAwait != nil {
			if denial := t.delegationDenyAwait(ctx, agentID); denial != nil {
				return DelegationDeniedResult("delegate", denial)
			}
		} else {
			slog.Error("delegate: no await delegation-deny checker installed — denying by default",
				"agent_id", agentID)
			return DelegationDeniedResult("delegate", &DelegationDenial{
				Reason:        "delegation is not configured for this agent (no policy gate installed) — denying by default",
				Policy:        DenyTrustSet,
				TargetAgentID: agentID,
			})
		}
	}

	// #477: resolve the effective depth cap the gate above just authorized
	// this call against, so the spawner's own depth check does not
	// independently re-derive a different (possibly stricter) default.
	var resolvedMaxDepth *int
	if t.delegationDepthResolver != nil {
		resolvedMaxDepth = t.delegationDepthResolver(ctx, agentID)
	}

	// ADR-053 S2 — mint the child's own durable session_id (distinct from
	// the shared transcript session id, D1) and persist its initial
	// `queued` lifecycle record BEFORE dispatch, so a crash between here and
	// the goroutine/spawn call below still leaves a queryable record (the
	// boot sweep — another wave — will reconcile it to failed(interrupted)).
	delegateSessionID := uuid.NewString()
	parentDurableKey := strings.TrimSpace(ToolTranscriptSessionID(ctx))
	is3P := false
	if agentID != "" && t.getAgentRegistry != nil {
		if reg := t.getAgentRegistry(); reg != nil {
			is3P = reg.IsExternalCLI(agentID)
		}
	}
	ownerScopeKind := session.OwnerScopeHuman
	ownerScopeID := ""
	if parentDelegateID := strings.TrimSpace(ToolDelegateSessionID(ctx)); parentDelegateID != "" {
		ownerScopeKind = session.OwnerScopeParentSession
		ownerScopeID = parentDelegateID
	}
	if t.lifecycle != nil {
		// FR-015 — fail closed on an unresolvable parent. ToolAgentID returns
		// "" for BOTH a missing context key AND a wrong-typed value (it is a
		// comma-ok type assertion with the error discarded), so an empty
		// value here means the DELEGATING agent's identity could not be
		// resolved at all. A record minted without it is permanently
		// unattributable: ParentAgentID is the only parent linkage, and no
		// other field can stand in for it (ParentDurableKey post-ADR-057 (D1)
		// names only the DIRECT parent — one hop, never re-inherited down the
		// chain, see pkg/session/lifecycle.go's ParentDurableKey doc comment —
		// so it cannot stand in for ParentAgentID either; OwnerScopeID is ""
		// for a top-level delegation, and AgentID is the child's). Such a
		// session could never be returned to its parent by list_jobs. Refuse
		// the mint — and therefore the whole delegation — rather than persist
		// an orphan. Note the mint is deliberately still skipped entirely
		// when no lifecycle store is configured: with no store there is no
		// record to orphan (see the else branch below — FR-021/BDD-20 refuse
		// the delegation outright in that case instead).
		//
		// R2-MAJ-015 — tools.delegate.require_parent_agent_id is the operator
		// kill switch for exactly that refusal. It exists because the guard's
		// blast radius is the whole install: a wiring regression anywhere
		// upstream of ToolAgentID turns EVERY delegate call into this error,
		// and without a lever the only remedy is a code change. Resolving the
		// key to false downgrades the refusal to a log-at-Error and mints
		// with an empty ParentAgentID — knowingly degraded attribution, an
		// explicit operator choice, never the default (unset resolves to
		// true) and never silent: the Error line below fires on EVERY such
		// mint, not once, so a forgotten kill switch keeps announcing the
		// orphan records it is creating.
		parentAgentID := strings.TrimSpace(ToolAgentID(ctx))
		if parentAgentID == "" {
			if t.parentAgentIDRequired() {
				slog.Error("delegate: refusing to mint an unattributable lifecycle record — no parent agent id in context",
					"delegate_session_id", delegateSessionID,
					"target_agent_id", agentID,
					"parent_durable_key", parentDurableKey)
				return ErrorResult("delegate: cannot resolve the delegating agent's identity — " +
					"refusing to start a delegated session that could never be traced back to its parent")
			}
			slog.Error("delegate: minting an unattributable lifecycle record with an empty parent agent id — "+
				"the FR-015 guard is disabled by tools.delegate.require_parent_agent_id=false; "+
				"this session cannot be traced back to its parent and will never be returned to it by list_jobs",
				"delegate_session_id", delegateSessionID,
				"target_agent_id", agentID,
				"parent_durable_key", parentDurableKey)
		}
		rec := &session.LifecycleRecord{
			SessionID:        delegateSessionID,
			Generation:       0,
			State:            session.LifecycleQueued,
			OwnerScopeKind:   ownerScopeKind,
			OwnerScopeID:     ownerScopeID,
			ParentAgentID:    parentAgentID,
			ParentDurableKey: parentDurableKey,
			OriginChannel:    ToolChannel(ctx),
			OriginChatID:     ToolChatID(ctx),
			WorkspaceID:      ToolWorkspaceID(ctx),
			AgentID:          agentID,
			Is3P:             is3P,
		}
		if err := t.lifecycle.Persist(rec); err != nil {
			return ErrorResult(fmt.Sprintf("delegate: failed to persist durable session record: %v", err)).WithError(err)
		}
	} else {
		// FR-021/BDD-20 (W7a) — fail CLOSED, not silently degraded, when no
		// durable lifecycle store is wired at all. Before this fix, the whole
		// `if t.lifecycle != nil { ... }` block above (mint + persist) was
		// simply skipped and execution fell straight through to
		// executeAsync/executeSync below — spawning a real child sub-turn
		// with NO durable record, no ParentDurableKey edge for the ancestor
		// walk (W12) or the boot sweep (W6/FR-078) to ever find, and a
		// success-shaped AsyncResult/inline result returned to the caller as
		// if nothing were wrong. That is precisely the silent-degradation
		// posture Hard Constraint #6 and this file's own FR-015 guard above
		// forbid, and BDD-20 pins the correct behavior: an operator-visible
		// refusal, no child session created, no success payload returned.
		// Mirrors the FR-015 refusal's shape (slog.Error + ErrorResult)
		// immediately above rather than introducing a second error style.
		slog.Error("delegate: refusing delegation — no durable lifecycle store configured",
			"delegate_session_id", delegateSessionID,
			"target_agent_id", agentID,
			"parent_durable_key", parentDurableKey)
		return ErrorResult("delegate: cannot start a delegated session — no durable lifecycle store is " +
			"configured (operator misconfiguration); refusing rather than spawning an untracked, " +
			"unrecoverable session")
	}

	if async {
		// isResume: false — executeRun always mints a BRAND-NEW
		// delegateSessionID (generation 0) just above; this is a genuine
		// create, never a resume. Native `follow_up`'s warm resume goes
		// through spawnCorrectiveFollowUp's own executeAsync call instead.
		return t.executeAsync(ctx, task, label, agentID, resolvedMaxDepth, delegateSessionID, timeout, snap, false, cb)
	}
	return t.executeSync(ctx, task, label, agentID, resolvedMaxDepth, delegateSessionID, timeout, snap)
}

// minDelegateTimeoutSeconds/maxDelegateTimeoutSeconds bound a caller-supplied
// timeout_seconds override. Mirrors pkg/tools/shell.go's
// minTimeoutSeconds/maxTimeoutSeconds bounds-checking style/values for
// consistency across the tool surface.
const (
	minDelegateTimeoutSeconds = 1
	maxDelegateTimeoutSeconds = 3600
)

// resolveDelegateTimeoutSeconds parses args["timeout_seconds"] for
// action="run". Absent/nil/explicit 0 all resolve to 0 (time.Duration zero
// value), meaning "no override — use the spawner's own default
// (defaultSubTurnTimeout)", matching the schema's documented "0 = default
// (5 min)". A nonzero value is bounds-checked against
// [minDelegateTimeoutSeconds, maxDelegateTimeoutSeconds] and REJECTED —
// never silently clamped or ignored — when out of range, mirroring
// shell.go's resolveTimeoutSeconds.
func resolveDelegateTimeoutSeconds(args map[string]any) (time.Duration, error) {
	raw, present := args["timeout_seconds"]
	if !present || raw == nil {
		return 0, nil
	}
	var v int64
	switch n := raw.(type) {
	case float64:
		v = int64(n)
	case int:
		v = int64(n)
	case int32:
		v = int64(n)
	case int64:
		v = n
	default:
		return 0, fmt.Errorf("timeout_seconds must be a number")
	}
	if v == 0 {
		return 0, nil
	}
	if v < minDelegateTimeoutSeconds || v > maxDelegateTimeoutSeconds {
		return 0, fmt.Errorf(
			"timeout_seconds must be between %d and %d when non-zero (got %d); 0 means use the default",
			minDelegateTimeoutSeconds, maxDelegateTimeoutSeconds, v,
		)
	}
	return time.Duration(v) * time.Second, nil
}

// transitionLifecycle is a small helper that atomically transitions
// sessionID's durable record to the given terminal/non-terminal state +
// optional failedReason, preserving every other field. Errors are logged,
// not propagated — a durable-record write failure must never fail (or mask
// the outcome of) the underlying delegation itself.
//
// NOTE ON LOCKING (Correctness-MAJOR-3, honesty template): this delegates
// to session.TransitionSession (the single dual-store mediator, Defect #28)
// with a nil UnifiedStore — a delegate/subturn session has no chat-transcript
// meta.json at all (UnifiedStore.NewSession is never called for a child turn
// — see pkg/agent/subturn.go), so there is nothing to mirror onto. The
// mediator's atomic LifecycleStore.Mutate (the RMW primitive that holds the
// per-session striped lock across tail→fn→write) replaces the hand-rolled
// Mutate call this helper used to make directly. The prior Load+Persist pair
// was a non-atomic RMW: two concurrent transitions on the same session_id
// (the cancel-vs-complete race, S4 INV-3) raced — the loser either overwrote
// the winner's terminal record or was rejected by the immutable-terminal
// guard non-atomically. Under the mediator's Mutate the two serialize: the
// first writer lands its terminal state, the second sees that terminal tail
// under the lock and persistLocked rejects its same-generation write with
// ErrLifecycleTerminalImmutable (logged here, harmless — the record is already
// terminally correct). Callers MUST NOT already hold Lock(sessionID):
// sync.Mutex is not reentrant, and Mutate takes the lock ONCE internally.
// The sibling comment in message_parent.go parkNeedsInput mirrors this one.
func (t *DelegateTool) transitionLifecycle(sessionID string, state session.LifecycleState, failedReason string) {
	if t.lifecycle == nil || sessionID == "" {
		return
	}
	// nil UnifiedStore: delegate/subturn sessions have no chat-transcript meta
	// (see the doc comment above) — the mediator skips the mirror. t.lifecycle
	// (MessageParentLifecycleStore) satisfies session.LifecycleMutator, so no
	// type assertion is needed.
	if err := session.TransitionSession(t.lifecycle, nil, sessionID, state, failedReason); err != nil {
		slog.Warn("delegate: transitionLifecycle: dual-store transition failed", "session_id", sessionID, "state", state, "error", err)
	}
}

// killChildBackgroundShells kills sessionID's own background bash/exec
// shells AND every one of its durable descendants' (ADR-057 D8/R-13,
// FR-028/BDD-29 — see the sessionManager field doc for the full rationale)
// via the same KillAllForSessions primitive U16 exposes.
//
// [D8-CASCADE, 2026-08-04] Before this fix, this call reached ONLY sessionID's
// own shells — never a descendant's — because KillAllForSessions was called
// with the single-element []string{sessionID} regardless of whether
// sessionID had any children. The UAT gap-closure report (2026-08-03) proved
// this live against a real jim->ray->worker chain: hard-cancelling ray left
// worker's detached background HTTP server (a genuine descendant, owned by
// worker's own distinct session id per ProcessSession.OwnerSessionID's doc
// comment) serving for minutes afterward. This mirrors the identical fix
// pkg/agent/cancel.go's RequestCancel already applies to the chat-wide Stop
// path (resolveBackgroundKillSessionIDs) — a per-delegation cancel must
// reach the same descendant set a chat-wide Stop does.
//
// Returns (killed, failed, walkIncomplete). killed/failed were previously
// the only return values; executeCancel folds failed into what it tells the
// user. walkIncomplete is true when collectCancelDescendantSessionIDs hit a
// t.lifecycle.List failure partway through the walk — in that case
// killed/failed reflect only the PARTIAL subtree actually reached, and
// executeCancel MUST surface that rather than reporting a clean success (a
// caller reading "0 failed" must not conclude "the whole subtree was swept
// clean" when this is true). A nil sessionManager (SetSessionManager never
// called) is a silent no-op ((0, 0, false)), matching every other optional
// capability this tool accepts via a setter.
func (t *DelegateTool) killChildBackgroundShells(sessionID string) (killed, failed int, walkIncomplete bool) {
	if t.sessionManager == nil || sessionID == "" {
		return 0, 0, false
	}
	descendants, walkErr := t.collectCancelDescendantSessionIDs(sessionID)
	if walkErr != nil {
		walkIncomplete = true
		slog.Warn("delegate: cancel: descendant walk failed partway through — the background-shell kill "+
			"cascade below is INCOMPLETE; some descendants' background bash/exec work may be left running undetected",
			"session_id", sessionID, "error", walkErr)
	}
	ids := append([]string{sessionID}, descendants...)
	killed, failed = t.sessionManager.KillAllForSessions(ids)
	switch {
	case failed > 0:
		// A REAL kill failure (KillAllForSessions already excludes the
		// benign lost-the-race case from this count) deserves Warn, not
		// the same Info level a clean kill gets — this is the actionable
		// signal executeCancel's own result message is now built from.
		slog.Warn("delegate: cancel: failed to kill some background shells for cancelled child or its descendants",
			"session_id", sessionID, "descendant_count", len(descendants), "killed", killed, "failed", failed)
	case killed > 0:
		slog.Info("delegate: cancel: killed background shells for cancelled child and/or its descendants",
			"session_id", sessionID, "descendant_count", len(descendants), "killed", killed)
	}
	return killed, failed, walkIncomplete
}

// collectCancelDescendantSessionIDs performs a breadth-first walk of the
// durable ParentDurableKey edge (pkg/session/lifecycle.go) starting at
// rootSessionID and returns every reachable descendant's own session id
// (rootSessionID itself is never included).
//
// This mirrors agent.CollectDescendantSessionIDs (pkg/agent/cancel.go)
// byte-for-byte in walk semantics and error contract, duplicated here rather
// than called directly because pkg/tools cannot import pkg/agent (pkg/agent
// already imports pkg/tools — see AgentLoopSpawner/SubTurnConfig — so the
// dependency can only run that direction). This is the same class of
// cross-package duplication cancel.go's own CollectDescendantSessionIDs doc
// comment describes for the (now-hoisted) pkg/gateway/websocket.go copy.
//
// Returns (nil, nil) when t.lifecycle is nil or rootSessionID is empty — not
// an error, the documented degrade-gracefully path for a DelegateTool that
// never had SetLifecycleStore called (killChildBackgroundShells's caller
// already checks t.sessionManager != nil before reaching here, but this
// function is defensive on its own terms too).
//
// Returns a non-nil error when ANY t.lifecycle.List call in the walk fails
// partway through — the returned slice is still the PARTIAL set discovered
// before the failure, never silently reported as "this node has no
// children." Callers MUST treat a non-nil error as a truncated view of the
// true descendant set, never as clean success with fewer descendants than
// expected (D8-CASCADE, mirroring FIX-5's identical Defect-2 fix in
// agent.CollectDescendantSessionIDs).
func (t *DelegateTool) collectCancelDescendantSessionIDs(rootSessionID string) ([]string, error) {
	if t.lifecycle == nil || rootSessionID == "" {
		return nil, nil
	}
	visited := map[string]struct{}{rootSessionID: {}}
	queue := []string{rootSessionID}
	var descendants []string
	var walkErrs []error
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		children, err := t.lifecycle.List(session.LifecycleFilter{ParentDurableKey: id})
		if err != nil {
			// This branch of the tree is now UNREACHABLE for this walk —
			// recorded (not just logged) so the caller can distinguish this
			// from "id has no children".
			walkErrs = append(walkErrs, fmt.Errorf("list children of %q: %w", id, err))
			continue
		}
		for _, rec := range children {
			if _, seen := visited[rec.SessionID]; seen {
				continue
			}
			visited[rec.SessionID] = struct{}{}
			descendants = append(descendants, rec.SessionID)
			queue = append(queue, rec.SessionID)
		}
	}
	if len(walkErrs) > 0 {
		return descendants, fmt.Errorf("descendant walk incomplete for root %q: %d branch(es) failed to list children: %w",
			rootSessionID, len(walkErrs), errors.Join(walkErrs...))
	}
	return descendants, nil
}

// executeAsync runs the background (async=true) delegation path. It records
// the task's state in t.tasks BEFORE launching the sub-turn goroutine and
// updates that SAME record on completion — the fix for FR-D2: action:"status"
// reads from this exact map, so a real, live status is always available.
func (t *DelegateTool) executeAsync(
	ctx context.Context,
	task, label, agentID string,
	resolvedMaxDepth *int,
	delegateSessionID string,
	timeout time.Duration,
	snap *ContextSnapshot,
	isResume bool,
	cb AsyncCallback,
) *ToolResult {
	if t.spawner == nil {
		return ErrorResult("delegate: no sub-turn spawner configured")
	}

	channel := ToolChannel(ctx)
	chatID := ToolChatID(ctx)
	// W2: capture, at task-creation time, the exact correlation anchors a
	// spawned child sub-turn's transcript entries will carry back —
	// SessionID mirrors TranscriptSessionID: parentTS.transcriptSessionID
	// (pkg/agent/subturn.go), and SpawnCallID mirrors the delegate tool
	// call's own ID (the value spawnToolCallIDFromContext captures on the
	// agent-package side to set childTS.parentSpawnCallID). Reading both
	// from THIS SAME ctx guarantees they match what the child will actually
	// use, without any agent-package coupling.
	sessionID := ToolTranscriptSessionID(ctx)
	spawnCallID := ToolCallID(ctx)
	is3P := false
	if agentID != "" && t.getAgentRegistry != nil {
		if reg := t.getAgentRegistry(); reg != nil {
			is3P = reg.IsExternalCLI(agentID)
		}
	}

	t.mu.Lock()
	// FR-045: eviction runs as part of the tool's own bookkeeping — every
	// new task registration — never a separate goroutine/ticker.
	t.evictStaleTasksLocked()
	taskID := fmt.Sprintf("delegate-%d", t.nextID)
	t.nextID++
	t.tasks[taskID] = &DelegateTaskState{
		ID:                taskID,
		Task:              task,
		Label:             label,
		AgentID:           agentID,
		OriginChannel:     channel,
		OriginChatID:      chatID,
		Status:            "running",
		Created:           time.Now().UnixMilli(),
		SessionID:         sessionID,
		SpawnCallID:       spawnCallID,
		Is3P:              is3P,
		DelegateSessionID: delegateSessionID,
		LastStatusRead:    t.now().UnixMilli(),
	}
	if delegateSessionID != "" {
		t.sessionIndex[delegateSessionID] = taskID
	}
	t.mu.Unlock()

	t.transitionLifecycle(delegateSessionID, session.LifecycleRunning, "")

	// The task is the first USER message; the delegate's soul (worker /
	// configured agent) is resolved inside spawnSubTurn and used as the
	// system role. delegate does not pre-inject any persona — a configured
	// delegate exposes its own soul and a soul-less worker runs with an empty
	// system role (worker souls are OPTIONAL by design). The label, when set,
	// is preserved as the task label for the WS subTurn_start frame.
	//
	// Critical: true is REQUIRED here, not optional. Background delegation's
	// entire premise is "the parent moves on; tell me later" — the parent
	// turn routinely finishes (its own follow-up LLM call after receiving
	// this async ack, then Finish(false)) in well under the time it takes
	// the delegate to run even one tool call. Without Critical:true, the
	// child sub-turn's own loop (pkg/agent/loop.go's "Parent turn ended"
	// check, evaluated early in each iteration, before the next LLM call)
	// treats !ts.critical && ts.IsParentEnded() as a signal to exit
	// gracefully — silently discarding the delegate's real answer for any
	// task needing more than a single LLM turn (i.e.
	// any task that calls a tool before its final answer). The delegate's
	// pre-tool-call narration survives (persisted per-iteration), but the
	// synthesized final answer is never produced at all: spawnSubTurn's
	// result comes back with ForLLM/ForUser == "", and asyncCallback's
	// `content == "" { return }` guard (pkg/agent/loop.go) then silently
	// drops it — no error, no notification, nothing delivered to the user,
	// live or on reload. Critical:true lets the child keep running past the
	// parent's own finish (it still delivers as an "orphan" on the
	// now-moot pendingResults channel — see deliverSubTurnResult — but its
	// REAL delivery path, this same cb -> AsyncNotifier.Notify chain, is
	// unaffected by parent lifecycle and fires correctly once the child
	// actually finishes). See SubTurnConfig.Critical's doc comment.
	//
	// Pending-spawn marker (delegate-spawn cancel race fix): recorded HERE,
	// synchronously on THIS (the delegating parent's own tool-execution)
	// goroutine, as the LAST thing that happens before the goroutine below
	// is dispatched — not inside the goroutine itself, and not any earlier
	// than this point. Marking any earlier (e.g. before the t.tasks
	// bookkeeping above) would risk recording a marker for a spawn that
	// this function then aborts before ever reaching the goroutine — there
	// is no such abort path between here and the `go func` below, so this
	// is also the LATEST point that still guarantees "marked implies a
	// spawn attempt is genuinely in flight," which is exactly the
	// invariant AgentLoopSpawner.MarkPendingDelegateSpawn's own doc comment
	// (pkg/agent/subturn.go) requires of every caller. sessionID/channel/
	// chatID are the delegating PARENT turn's own identity (captured above
	// from this same ctx) — the SAME identity a Stop click's
	// CancelScope carries (CancelScope.SessionID for the web SPA/CLI/Tier A
	// path, or (Channel, ChatID) for Tier B). channel/chatID are inherited
	// verbatim by the spawned child at any delegation depth; sessionID's
	// match instead relies on ROUTING identity being what's inherited
	// verbatim (turn.go's own doc comment: "routingSessionID is inherited
	// verbatim through the whole subtree"), NOT transcriptSessionID — post-
	// ADR-057 D1 each child gets its OWN distinct transcriptSessionID
	// rather than copying the parent's (this comment used to claim
	// "processOptions construction copies parentTS.transcriptSessionID …
	// onto the child," which was true pre-D1 and is false now). So
	// spawnSubTurn's own cleanup clears the exact keys marked here. A nil
	// t.spawnMarker (SetSpawner was never called with a marker-capable
	// spawner — e.g. this package's own unit tests) makes this call a
	// silent no-op, unchanged from before this fix.
	if t.spawnMarker != nil {
		t.spawnMarker.MarkPendingDelegateSpawn(sessionID, channel, chatID)
	}
	t.asyncWG.Add(1)
	go func() {
		defer t.asyncWG.Done()
		result, err := t.spawner.SpawnSubTurn(ctx, SubTurnConfig{
			Model:             t.defaultModel,
			Tools:             nil, // Will inherit from parent via context
			SystemPrompt:      task,
			TargetAgentID:     agentID,
			MaxTokens:         t.maxTokens,
			Temperature:       t.temperature,
			Async:             true,
			Critical:          true,
			Timeout:           timeout,
			TaskLabel:         label,
			ResolvedMaxDepth:  resolvedMaxDepth,
			ContextSnapshot:   snap,
			DelegateSessionID: delegateSessionID,
			IsResume:          isResume,
		})

		var lifecycleState session.LifecycleState
		var lifecycleFailedReason string
		// parked: the child called message_parent(wait=true) and is waiting on
		// the parent's respond(), NOT finished. Its turn stopped deliberately,
		// so err is nil and the result is neither Interrupted nor IsError —
		// exactly the shape that otherwise falls into `default` below and gets
		// stamped LifecycleCompleted, overwriting the needs_input state
		// parkNeedsInput just wrote. That overwrite is what made respond()
		// fail closed with "session is not parked" in the ADR-057 UAT even
		// once the turn loop itself was fixed to stop.
		parked := false

		t.mu.Lock()
		if state, ok := t.tasks[taskID]; ok {
			switch {
			case err != nil && ctx.Err() != nil:
				state.Status = "canceled"
				state.Result = "Task canceled during execution"
				lifecycleState, lifecycleFailedReason = session.LifecycleCancelled, "stopped_by_user"
			case err != nil:
				state.Status = "failed"
				state.Result = fmt.Sprintf("Error: %v", err)
				lifecycleState, lifecycleFailedReason = session.LifecycleFailed, "error"
			case result != nil && result.ParksTurn:
				parked = true
				state.Status = "needs_input"
				state.Result = result.ForLLM
			default:
				state.Status = "completed"
				if result != nil {
					state.Result = result.ForLLM
				}
				lifecycleState = session.LifecycleCompleted
			}
		}
		t.mu.Unlock()

		// Skip the transition entirely when parked — parkNeedsInput already
		// owns this record's state, and any write here would clobber it.
		if !parked {
			t.transitionLifecycle(delegateSessionID, lifecycleState, lifecycleFailedReason)
		}

		switch {
		case err != nil:
			// Kill the silent swallow: a spawn that dies before starting
			// (e.g. a `follow_up` resume whose target session vanished, or
			// any other SpawnSubTurn failure) must be operator-visible on
			// its OWN, unconditionally — never dependent on whatever `cb`
			// happens to do with the result downstream (a live channel that
			// may already be gone, an AsyncNotifier publish that lands
			// somewhere other than where an operator is watching, etc.).
			// This is the ONE line that fires every single time this
			// goroutine's spawn attempt fails, regardless of whether cb is
			// nil, so `grep -i "async subturn spawn failed" gateway.log`
			// always finds it.
			slog.Error("delegate: async subturn spawn failed",
				"session_id", delegateSessionID,
				"task_id", taskID,
				"agent_id", agentID,
				"is_resume", isResume,
				"error", err)
			result = ErrorResult(fmt.Sprintf("Delegate failed: %v", err)).WithError(err)
		case result != nil:
			// Finding B (A-I4 round 4, live-verified): mirror executeSync's
			// own wrapping below — the raw spawner result's ForUser field
			// (spawnSubTurn / pkg/agent/subturn.go sets it to
			// turnRes.finalContent, the CHILD's own unwrapped, first-person
			// final text) must never reach pkg/agent/loop.go's asyncCallback
			// unmodified. asyncCallback unconditionally does
			// `if !result.Silent && result.ForUser != "" { PublishOutbound(...
			// Content: result.ForUser ...) }` — a DIRECT, immediate publish
			// with no relation to the wsStreamer/shadow-stream machinery at
			// all (confirmed via a live background delegation: the leaked
			// bubble appeared even with no cancellation involved, the moment
			// the child's own final answer happened to require no wrapping —
			// e.g. a policy-denied task explaining itself in its own voice).
			// That silently turns the delegate's own raw narration into a
			// second, unattributed top-level chat bubble the instant the
			// parent's own turn has already ended — the common case for
			// background delegation (Critical:true's own doc comment above:
			// "the parent turn routinely finishes ... in well under the time
			// it takes the delegate to run even one tool call"). This is
			// exactly the content class the design intends to keep hidden,
			// matching the already-correct sync/await case (executeSync
			// below never independently publishes anything — its result only
			// ever becomes a normal tool_call_result) and
			// pkg/gateway/replay.go's ParentSpawnCallID skip. The LLM-facing
			// AsyncNotifier continuation turn (still fed the wrapped ForLLM
			// content below) already informs the user, in the DELEGATOR's
			// own voice, that the delegation finished and what it found —
			// clearing ForUser here removes the duplicate, unattributed raw
			// dump without losing any user-facing information.
			labelStr := label
			if labelStr == "" {
				labelStr = "(unnamed)"
			}
			// A parked child is NOT finished — it is waiting on this
			// delegator's own respond(). Saying "completed" would tell the
			// delegator's next turn the opposite of what the lifecycle
			// record says (needs_input), which is how an orchestrator ends
			// up believing work is done and never answering the question.
			// ParksTurn must also survive this rebuild: dropping it here
			// would silently kill the signal for every downstream reader.
			headline := "Subagent task completed"
			if result.ParksTurn {
				headline = "Subagent task is PAUSED awaiting your answer (respond to it to continue)"
			}
			result = &ToolResult{
				ForLLM:    fmt.Sprintf("%s:\nLabel: %s\nResult: %s", headline, labelStr, result.ForLLM),
				IsError:   result.IsError,
				ParksTurn: result.ParksTurn,
				Async:     true,
			}
		}

		// Call callback if provided
		if cb != nil {
			cb(ctx, result)
		} else if err != nil {
			slog.Error("delegate: subturn failed with no callback", "error", err)
		}
	}()

	// NOTE: "(task_id: %s)" must stay its own parenthesized clause, ending in
	// the FIRST ")" after the id — pkg/tools/delegate_test.go's extractTaskID
	// helper scans for "task_id: " and stops at the next ")"/"\n", so a
	// session_id appended INSIDE the same parens would corrupt every test
	// using that helper (regression: existing one-shot delegate.run compat).
	msg := fmt.Sprintf("Delegated task for: %s (task_id: %s)", task, taskID)
	if label != "" {
		msg = fmt.Sprintf("Delegated task '%s' for: %s (task_id: %s)", label, task, taskID)
	}
	msg += fmt.Sprintf(" (session_id: %s)", delegateSessionID)
	msg += fmt.Sprintf(
		" — running in background; check progress with delegate(action=\"status\", session_id=%q), "+
			"or inbox/steer/respond/cancel/follow_up/peek using the same session_id.", delegateSessionID,
	)
	return AsyncResult(msg)
}

// executeSync runs the await (async=false) delegation path: it blocks until
// the delegated turn completes and returns the result inline.
func (t *DelegateTool) executeSync(
	ctx context.Context,
	task, label, agentID string,
	resolvedMaxDepth *int,
	delegateSessionID string,
	timeout time.Duration,
	snap *ContextSnapshot,
) *ToolResult {
	if t.spawner == nil {
		return ErrorResult("delegate: no sub-turn spawner configured").WithError(fmt.Errorf("spawner not set"))
	}

	// ADR-057 FR-044/BDD-49: executeSync now registers a DelegateTaskState
	// too — previously only executeAsync did, so a completed/failed
	// synchronous (await) delegation left action:"status" with nothing to
	// find for it at all (not "empty", genuinely absent), and
	// recentActivityLines could never build a snapshot for a task that
	// action:"status" could not even resolve. Mirrors executeAsync's own
	// registration exactly (same fields, same eviction call).
	channel := ToolChannel(ctx)
	chatID := ToolChatID(ctx)
	sessionID := ToolTranscriptSessionID(ctx)
	spawnCallID := ToolCallID(ctx)
	is3P := false
	if agentID != "" && t.getAgentRegistry != nil {
		if reg := t.getAgentRegistry(); reg != nil {
			is3P = reg.IsExternalCLI(agentID)
		}
	}

	t.mu.Lock()
	t.evictStaleTasksLocked()
	taskID := fmt.Sprintf("delegate-%d", t.nextID)
	t.nextID++
	t.tasks[taskID] = &DelegateTaskState{
		ID:                taskID,
		Task:              task,
		Label:             label,
		AgentID:           agentID,
		OriginChannel:     channel,
		OriginChatID:      chatID,
		Status:            "running",
		Created:           time.Now().UnixMilli(),
		SessionID:         sessionID,
		SpawnCallID:       spawnCallID,
		Is3P:              is3P,
		DelegateSessionID: delegateSessionID,
		LastStatusRead:    t.now().UnixMilli(),
	}
	if delegateSessionID != "" {
		t.sessionIndex[delegateSessionID] = taskID
	}
	t.mu.Unlock()

	t.transitionLifecycle(delegateSessionID, session.LifecycleRunning, "")

	result, err := t.spawner.SpawnSubTurn(ctx, SubTurnConfig{
		Model:             t.defaultModel,
		Tools:             nil, // Will inherit from parent via context
		SystemPrompt:      task,
		TargetAgentID:     agentID, // "" → parent's own soul; non-empty → named agent's soul
		TaskLabel:         label,
		MaxTokens:         t.maxTokens,
		Temperature:       t.temperature,
		Async:             false,
		Timeout:           timeout,
		ResolvedMaxDepth:  resolvedMaxDepth,
		ContextSnapshot:   snap,
		DelegateSessionID: delegateSessionID,
	})
	// Finding F (A-I4 round 5): only take the generic "Delegate execution
	// failed" shortcut for a genuine dispatch failure — result == nil (e.g.
	// a panic spawnSubTurn's own recover() deliberately nils result for) or
	// a real, non-interrupted error. A parent-cancellation interruption
	// (result.Interrupted, set by spawnSubTurn's cleanup defer using the
	// SAME classification the live subagent_end frame already reports —
	// see ToolResult.Interrupted's doc comment) still returns a non-nil err
	// here (the child's context WAS canceled), but must fall through to the
	// normal formatting below so result.Interrupted survives onto the
	// result this function returns — which pkg/agent/loop.go's tool-call-
	// transcript persistence reads to decide whether a session reload shows
	// "interrupted" (matching live) or "failed" (the bug this closes).
	if result == nil || (err != nil && !result.Interrupted) {
		t.transitionLifecycle(delegateSessionID, session.LifecycleFailed, "error")
		t.finalizeSyncTask(taskID, "failed", fmt.Sprintf("Error: %v", err))
		return ErrorResult(fmt.Sprintf("Delegate execution failed: %v", err)).WithError(err)
	}

	switch {
	case result.Interrupted:
		t.transitionLifecycle(delegateSessionID, session.LifecycleCancelled, "stopped_by_user")
		t.finalizeSyncTask(taskID, "canceled", "Task canceled during execution")
	case result.IsError:
		t.transitionLifecycle(delegateSessionID, session.LifecycleFailed, "error")
		t.finalizeSyncTask(taskID, "failed", result.ForLLM)
	case result.ParksTurn:
		// Parked on message_parent(wait=true): waiting for the parent's
		// respond(), not finished. Deliberately transitions nothing —
		// parkNeedsInput already wrote needs_input, and writing
		// LifecycleCompleted here would clobber it and make respond() fail
		// closed with "session is not parked" (the ADR-057 UAT symptom).
		// The in-memory task status IS updated (finalizeSyncTask is a plain
		// setter despite the name), so `delegate status` reports needs_input
		// rather than a stale "running" — matching executeAsync's parked case.
		t.finalizeSyncTask(taskID, "needs_input", result.ForLLM)
	default:
		t.transitionLifecycle(delegateSessionID, session.LifecycleCompleted, "")
		t.finalizeSyncTask(taskID, "completed", result.ForLLM)
	}

	// Format result for display
	userContent := result.ForLLM
	if result.ForUser != "" {
		userContent = result.ForUser
	}
	maxUserLen := 500
	if len(userContent) > maxUserLen {
		userContent = userContent[:maxUserLen] + "..."
	}

	labelStr := label
	if labelStr == "" {
		labelStr = "(unnamed)"
	}
	// Same truthfulness rule as executeAsync's rebuild: a parked child is
	// waiting on this delegator's respond(), not finished. Telling the
	// delegator's next turn "completed" contradicts the needs_input record
	// and is how an unanswered question turns into a permanently stuck child.
	llmHeadline := "Subagent task completed"
	if result.ParksTurn {
		llmHeadline = "Subagent task is PAUSED awaiting your answer (respond to it to continue)"
	}
	llmContent := fmt.Sprintf("%s:\nLabel: %s\nResult: %s",
		llmHeadline, labelStr, result.ForLLM)

	return &ToolResult{
		ForLLM:      llmContent,
		ForUser:     userContent,
		Silent:      false,
		IsError:     result.IsError,
		Interrupted: result.Interrupted,
		// Carry the park signal across this rebuild. Dropping it would leave
		// the delegator's OWN turn loop unaware that its child parked — the
		// same class of silently-lost signal this defect started as.
		ParksTurn: result.ParksTurn,
		Async:     false,
	}
}

// finalizeSyncTask records executeSync's terminal outcome onto the
// DelegateTaskState registered at the top of executeSync (FR-044), mirroring
// the status/Result assignment executeAsync's own completion goroutine makes.
// A no-op if taskID was never registered (defensive; cannot happen given
// executeSync always registers before this is called).
func (t *DelegateTool) finalizeSyncTask(taskID, status, resultText string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if st, ok := t.tasks[taskID]; ok {
		st.Status = status
		st.Result = resultText
	}
}

// ResolvableSessionIDs implements tools.JobSessionResolver (#583): it reports
// which of the given delegate session ids (ADR-053 durable session_id, the
// same id space as DelegateTaskState.DelegateSessionID/t.sessionIndex's keys)
// are still resolvable in THIS process's in-memory delegate index — the
// signal list_jobs uses to decide whether a subagent row's session_id can
// actually be acted on right now (status/inbox/steer/respond/cancel/
// follow_up/peek) rather than failing on use. A durable LifecycleRecord can
// survive a process restart while this in-memory index does not; such a
// session is correctly reported unresolvable here (FR-011).
//
// Single lock acquisition for the WHOLE batch (FR-028, matching the
// JobSessionResolver interface's own doc comment): the underlying index is
// guarded by t.mu, the same mutex every delegate status/inbox/steer/respond/
// cancel call already takes, so resolving one id per row would put a
// read-only visibility tool in contention with the live dispatch path.
func (t *DelegateTool) ResolvableSessionIDs(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range ids {
		_, ok := t.sessionIndex[id]
		out[id] = ok
	}
	return out
}

// ResolvableLabels implements tools.JobLabelResolver (UAT M3, 2026-08-03):
// for each delegate session id given, it reports the caller-supplied `label`
// argument (delegate(..., label:"...")) recorded in THIS process's
// in-memory task-state index at dispatch time — see JobLabelResolver's own
// doc comment (pkg/tools/list_jobs_sources.go) for why no durable field
// exists to read instead (session.LifecycleRecord carries no Label at all;
// DelegateTaskState.Label plus a one-shot subagent_start WS payload are the
// only places a custom label ever lives).
//
// Mirrors ResolvableSessionIDs exactly: a single t.mu acquisition for the
// WHOLE batch (FR-028 — the same contract JobSessionResolver's own doc
// comment documents), so this read-only visibility call never contends with
// the live dispatch path over the same lock.
//
// A session id absent from t.sessionIndex, or whose task has no Label set,
// is simply omitted from the returned map — never an error; list_jobs falls
// back to the row's already-resolved agent display name for that case (see
// JobLabelResolver's doc comment).
func (t *DelegateTool) ResolvableLabels(ids []string) map[string]string {
	out := make(map[string]string, len(ids))
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range ids {
		taskID, ok := t.sessionIndex[id]
		if !ok {
			continue
		}
		task, ok := t.tasks[taskID]
		if !ok || task.Label == "" {
			continue
		}
		out[id] = task.Label
	}
	return out
}

// delegateTaskVisibleToCaller decides whether the caller identified by
// (callerSessionID, callerChannel, callerChatID) may see/act on task via
// action:"status" (C3, UAT 2026-07-31).
//
// pkg/gateway/websocket.go:615 mints a brand-new chatID ("webchat:" +
// uuid.New().String()) on EVERY WebSocket connection — a page refresh, a
// network blip, or any client that opens one connection per message all
// rotate it. Worse, for webchat specifically Channel is a fixed literal
// ("webchat", websocket.go:1707) shared by every webchat conversation, so
// chatID was the ONLY thing giving that channel any per-conversation
// isolation at all — and it is exactly what a reconnect breaks. The result
// (pre-fix): a status lookup for a task dispatched in a prior turn, on the
// SAME durable conversation, reported "No subagent found" the instant the
// browser reconnected, even though the task was very much alive (peek/
// list_jobs, which key off the durable session id, kept reporting it
// correctly the whole time).
//
// The durable identity that DOES survive a reconnect is the ADR-053 session
// id: the client resends the SAME session_id on every reconnect
// (websocket.go's `sessionID` local only ever gets a NEW session minted when
// the frame carries none at all — see websocket.go:1489 vs :1563), and
// DelegateTaskState.SessionID already captures it at dispatch time (this
// file's executeAsync, via ToolTranscriptSessionID(ctx) — the parent
// turn's own durable transcript session id).
//
// So: when BOTH the caller and the task carry a durable session id, that id
// is the authoritative scope check — it survives the caller's chatID
// rotating out from under it, and (being an unguessable, per-conversation
// identifier, unlike the shared "webchat" channel literal) is at least as
// strong an isolation boundary as the legacy channel/chatID pair for the
// cross-conversation case. When either side lacks a durable session id (a
// direct programmatic Execute call, a task registered before any transcript
// session was bound, or a non-webchat channel that never threads one
// through), this falls back to the pre-existing channel+chatID comparison
// unchanged — preserving check_spawn_status's original scoping exactly for
// every caller this fix does not need to touch.
func delegateTaskVisibleToCaller(callerSessionID, callerChannel, callerChatID string, task *DelegateTaskState) bool {
	if callerSessionID != "" && task.SessionID != "" {
		return callerSessionID == task.SessionID
	}
	if callerChannel != "" && task.OriginChannel != "" && task.OriginChannel != callerChannel {
		return false
	}
	if callerChatID != "" && task.OriginChatID != "" && task.OriginChatID != callerChatID {
		return false
	}
	return true
}

// executeStatus implements action:"status". It resolves against the exact
// same t.tasks map the async path writes to (FR-D2) and preserves
// check_spawn_status's channel/chatID scoping exactly: a lookup or listing is
// restricted to tasks that originated from the SAME conversation, and all
// tasks are listed only when no channel/chat context is injected at all
// (e.g. direct programmatic Execute calls). C3 (UAT 2026-07-31): the scope
// check now prefers the durable ADR-053 session id (delegateTaskVisibleToCaller)
// whenever both sides have one, since that identity survives a WebSocket
// reconnect where callerChatID does not — see that function's doc comment
// for the full rationale.
func (t *DelegateTool) executeStatus(ctx context.Context, args map[string]any) *ToolResult {
	callerChannel := ToolChannel(ctx)
	callerChatID := ToolChatID(ctx)
	callerSessionID := ToolTranscriptSessionID(ctx)

	var taskID string
	if rawTaskID, ok := args["task_id"]; ok && rawTaskID != nil {
		taskIDStr, ok := rawTaskID.(string)
		if !ok {
			return ErrorResult("task_id must be a string")
		}
		taskID = strings.TrimSpace(taskIDStr)
	}
	// session_id (ADR-053) wins when both are present (DelegateStatusAction's
	// documented precedence); task_id survives as a deprecated alias.
	if rawSessionID, ok := args["session_id"]; ok && rawSessionID != nil {
		sessionIDStr, ok := rawSessionID.(string)
		if !ok {
			return ErrorResult("session_id must be a string")
		}
		if sid := strings.TrimSpace(sessionIDStr); sid != "" {
			t.mu.Lock()
			resolved, found := t.sessionIndex[sid]
			t.mu.Unlock()
			if !found {
				return ErrorResult(fmt.Sprintf("No subagent found with session ID: %s", sid))
			}
			taskID = resolved
		}
	}

	if taskID != "" {
		taskCopy, ok := t.getTaskCopy(taskID)
		if !ok {
			// Genuine absence: log distinctly from the scope-mismatch branch
			// below so an operator can tell the two apart, even though the
			// caller-visible message is identical for both (UAT 2026-07-31 —
			// the two paths were previously indistinguishable to anyone
			// debugging a "status went blind" report).
			slog.Debug("delegate: status lookup — task not found", "task_id", taskID)
			return ErrorResult(fmt.Sprintf("No subagent found with task ID: %s", taskID))
		}

		// Restrict lookup to tasks visible to this conversation — see
		// delegateTaskVisibleToCaller's doc comment (C3 fix: the durable
		// session id takes priority over the legacy channel/chatID pair
		// whenever both sides have one, since only the session id survives a
		// WebSocket reconnect).
		if !delegateTaskVisibleToCaller(callerSessionID, callerChannel, callerChatID, &taskCopy) {
			// Deliberately the SAME caller-visible "not found" message a
			// genuine miss returns above — never confirm to an untrusted
			// caller that a task exists in a DIFFERENT conversation — but
			// logged distinctly for diagnosability.
			slog.Debug("delegate: status lookup — task exists but is not visible to this caller (scope mismatch)",
				"task_id", taskID,
				"caller_session_id", callerSessionID,
				"task_session_id", taskCopy.SessionID,
				"caller_channel", callerChannel,
				"task_channel", taskCopy.OriginChannel,
			)
			return ErrorResult(fmt.Sprintf("No subagent found with task ID: %s", taskID))
		}

		return NewToolResult(delegateFormatTask(&taskCopy, t.delegateStatusExtra(&taskCopy)))
	}

	origTasks := t.listTaskCopies()
	if len(origTasks) == 0 {
		return NewToolResult("No subagents have been spawned yet.")
	}

	taskList := make([]*DelegateTaskState, 0, len(origTasks))
	for i := range origTasks {
		cpy := &origTasks[i]

		// Filter to tasks visible to the current conversation only — see
		// delegateTaskVisibleToCaller's doc comment.
		if !delegateTaskVisibleToCaller(callerSessionID, callerChannel, callerChatID, cpy) {
			continue
		}

		taskList = append(taskList, cpy)
	}

	if len(taskList) == 0 {
		return NewToolResult("No subagents found for this conversation.")
	}

	// Order by creation time (ascending) so spawning order is preserved.
	// Fall back to ID string for tasks created in the same millisecond.
	sort.Slice(taskList, func(i, j int) bool {
		if taskList[i].Created != taskList[j].Created {
			return taskList[i].Created < taskList[j].Created
		}
		return taskList[i].ID < taskList[j].ID
	})

	counts := map[string]int{}
	for _, task := range taskList {
		counts[task.Status]++
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Subagent status report (%d total):\n", len(taskList))
	for _, status := range []string{"running", "completed", "failed", "canceled"} {
		if n := counts[status]; n > 0 {
			label := strings.ToUpper(status[:1]) + status[1:] + ":"
			fmt.Fprintf(&sb, "  %-10s %d\n", label, n)
		}
	}
	sb.WriteString("\n")

	for _, task := range taskList {
		sb.WriteString(delegateFormatTask(task, t.delegateStatusExtra(task)))
		sb.WriteString("\n\n")
	}

	return NewToolResult(strings.TrimRight(sb.String(), "\n"))
}

// getTaskCopy returns a copy of the task with the given ID, taken under the
// lock, so the caller receives a consistent snapshot with no data race.
// ADR-057 FR-045: this is action:"status"'s single-task read path, so it
// also stamps LastStatusRead — resetting the eviction clock on the task's
// own stored record (not just the returned copy) so a task under active
// polling is never reclaimed by evictStaleTasksLocked out from under the
// caller (BDD-52's "But" clause, test #93).
func (t *DelegateTool) getTaskCopy(taskID string) (DelegateTaskState, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	task, ok := t.tasks[taskID]
	if !ok {
		return DelegateTaskState{}, false
	}
	task.LastStatusRead = t.now().UnixMilli()
	return *task, true
}

// listTaskCopies returns value copies of all tasks, taken under the lock, so
// callers receive consistent snapshots with no data race. ADR-057 FR-045:
// this backs action:"status"'s list-all-tasks path (no task_id/session_id
// given).
//
// Deliberately does NOT stamp LastStatusRead, unlike getTaskCopy. getTaskCopy
// is a targeted lookup by task_id/session_id — a genuine "I am actively
// polling THIS task" signal that legitimately resets its own eviction clock
// (BDD-52's "But" clause). A bare list-all read is not scoped to any one
// task the caller is following; it previously stamped EVERY task in the
// entire map (including other conversations' — the channel/chatID filter
// executeStatus applies happens AFTER this call returns) on every plain
// action:"status" call with no target, refreshing the eviction clock on the
// whole fleet and starving evictStaleTasksLocked (and the taskCap it now
// also enforces) indefinitely regardless of the configured retention
// policy. Copies are returned exactly as stored.
func (t *DelegateTool) listTaskCopies() []DelegateTaskState {
	t.mu.Lock()
	defer t.mu.Unlock()

	copies := make([]DelegateTaskState, 0, len(t.tasks))
	for _, task := range t.tasks {
		copies = append(copies, *task)
	}
	return copies
}

// delegateFormatTask renders a single DelegateTaskState as a human-readable
// block. extra, when non-empty, is appended as a trailing "\n"+extra section
// — used by action:"status" (W2) to attach either a running native task's
// recent transcript activity or a running external-CLI task's
// no-live-progress note (see delegateStatusExtra). Pass "" for a task with
// nothing to add (a non-running task, or a running native task with no
// activity captured yet) — this keeps the function's output identical to
// its pre-W2 shape for those cases.
func delegateFormatTask(task *DelegateTaskState, extra string) string {
	var sb strings.Builder

	header := fmt.Sprintf("[%s] status=%s", task.ID, task.Status)
	if task.Label != "" {
		header += fmt.Sprintf("  label=%q", task.Label)
	}
	if task.AgentID != "" {
		header += fmt.Sprintf("  agent=%s", task.AgentID)
	}
	if task.Created > 0 {
		created := time.UnixMilli(task.Created).UTC().Format("2006-01-02 15:04:05 UTC")
		header += fmt.Sprintf("  created=%s", created)
	}
	sb.WriteString(header)

	if task.Task != "" {
		fmt.Fprintf(&sb, "\n  task:   %s", task.Task)
	}
	if task.Result != "" {
		result := task.Result
		const maxResultLen = 300
		runes := []rune(result)
		if len(runes) > maxResultLen {
			result = string(runes[:maxResultLen]) + "…"
		}
		fmt.Fprintf(&sb, "\n  result: %s", result)
	}
	if extra != "" {
		sb.WriteString("\n")
		sb.WriteString(extra)
	}

	return sb.String()
}

// maxStatusActivityLines caps how many of a running native task's most
// recent transcript entries action:"status" surfaces (W2). Fixed at ~5 per
// spec — enough to convey what the delegate is currently doing without
// flooding the calling LLM's context on every poll.
const maxStatusActivityLines = 5

// maxStatusActivityLineRunes caps each surfaced activity line's length (W2).
const maxStatusActivityLineRunes = 120

// delegate3PStatusNote is the fixed action:"status" annotation for a running
// external-CLI (subagent_3p) task — see DelegateTaskState.Is3P's doc comment
// for why no live snapshot is attempted for these.
const delegate3PStatusNote = "  note:   external agent — no live progress; results on completion"

// delegateStatusExtra computes the action:"status" trailing annotation for
// task (W2). Only a "running" task gets anything:
//   - a native task gets up to maxStatusActivityLines of its own recent
//     transcript activity (recentActivityLines), or "" if the child sub-turn
//     hasn't written anything yet;
//   - an external-CLI (Is3P) task gets the fixed delegate3PStatusNote instead
//     of any attempted snapshot (batch/report-on-completion by design).
//
// Every non-running task (completed/failed/canceled) returns "" — its
// Result field already carries the final answer, so nothing is added.
func (t *DelegateTool) delegateStatusExtra(task *DelegateTaskState) string {
	if task.Status != "running" {
		return ""
	}
	if task.Is3P {
		return delegate3PStatusNote
	}

	var sb strings.Builder

	// G1 fix: check LIVE tool-call-argument progress first, before falling
	// back to the persisted-transcript snapshot below. A model mid-stream on
	// a large tool-call argument writes NOTHING to the persisted transcript
	// until its LLM round completes (see DelegateProgressReader's doc
	// comment) — this is precisely the window recentActivityLines alone
	// cannot see, and precisely the window that got a genuinely-working
	// child killed as "hung" in production.
	if t.progressReader != nil {
		if snap, ok := t.progressReader.ProgressForSession(task.DelegateSessionID); ok {
			sb.WriteString(formatToolCallProgressLine(snap))
		}
	}

	// ADR-057 FR-043: read the child's OWN durable session (DelegateSessionID),
	// not task.SessionID (the delegating PARENT's own transcript id — see
	// its doc comment). Post-FR-007 a delegated child writes its own
	// narration into its own session, so reading task.SessionID here always
	// found nothing the moment FR-007 landed elsewhere in this change set —
	// BDD-49/BDD-50 (a sync or async delegation's status snapshot must be
	// non-empty) were silently broken until this re-point.
	lines := t.recentActivityLines(task.DelegateSessionID, task.SpawnCallID, maxStatusActivityLines)
	if len(lines) == 0 {
		return sb.String()
	}
	if sb.Len() > 0 {
		sb.WriteString("\n")
	}
	sb.WriteString("  recent activity:")
	for _, line := range lines {
		sb.WriteString("\n    - ")
		sb.WriteString(line)
	}
	return sb.String()
}

// maxToolCallProgressStaleness caps how long ago a recorded tool-call
// progress update may be while still rendering as "generating" rather than
// "stale" (G1 fix). Generous on purpose: the underlying callback only fires
// on argument GROWTH (see protocoltypes.OnToolCallProgress's doc comment),
// so a model that paused between deltas — a slow provider round-trip,
// network jitter — should not read as hung just because its LAST delta was
// a while ago. It exists only so a turnState whose progress was never
// cleared (a crash mid-stream, rather than a clean turn end) does not
// masquerade as live forever; delegateStatusExtra only ever renders this
// note for a task whose Status is still "running" in the first place.
const maxToolCallProgressStaleness = 5 * time.Minute

// formatToolCallProgressLine renders a DelegateProgressReader snapshot as
// the leading line of action:"status"'s extra section (G1 fix) — the signal
// that lets a caller tell "still generating a large tool-call argument"
// apart from "hung", which is the whole point of this feature. See
// delegateStatusExtra's call site and DelegateProgressReader's doc comment
// for the incident this closes.
func formatToolCallProgressLine(snap ToolCallProgressSnapshot) string {
	name := snap.Name
	if name == "" {
		name = "(name pending)"
	}
	verb := "generating"
	if snap.Age > maxToolCallProgressStaleness {
		verb = "stale — no update recently, may have stalled while generating"
	}
	if snap.TotalArgsBytes > snap.ArgsBytes {
		return fmt.Sprintf("  progress: %s tool call %q — %d bytes (%d bytes total this round), last update %s ago",
			verb, name, snap.ArgsBytes, snap.TotalArgsBytes, snap.Age.Round(time.Second))
	}
	return fmt.Sprintf("  progress: %s tool call %q — %d bytes, last update %s ago",
		verb, name, snap.ArgsBytes, snap.Age.Round(time.Second))
}

// recentActivityLines reads back up to max of the most recent transcript
// entries a running NATIVE delegated sub-turn has written into its OWN
// durable session (ADR-057 FR-043 — sessionID here is the child's
// DelegateSessionID, not the shared parent transcript id; see
// delegateStatusExtra's call site), filtered to just this task's own
// activity via session.TranscriptEntry.ParentSpawnCallID == spawnCallID —
// the delegate tool call's own ID, which subturn.go stamps onto every
// intermediate/final assistant-text entry the child sub-turn produces (see
// ParentSpawnCallID's doc comment). This is a pure READ of data the child
// sub-turn already persists as a side effect of running — no new storage or
// write path is introduced by W2.
//
// Returns nil (never an error) when no session store is wired, sessionID or
// spawnCallID is empty, the transcript can't be read, or nothing has been
// written yet — delegateStatusExtra treats all of these as "no snapshot
// available" and falls back to the prompt-only summary that predates this
// feature. The last of those cases — a clean read that simply found no
// matching entries yet — is logged (ADR-057 FR-043/BDD-51): a genuinely
// empty activity path must leave a trace an operator can find, not degrade
// silently into the exact same "nothing available" shape as an unwired
// store or a transcript-read error.
func (t *DelegateTool) recentActivityLines(sessionID, spawnCallID string, maxLines int) []string {
	if t.sessionStore == nil || sessionID == "" || spawnCallID == "" {
		return nil
	}
	entries, err := t.sessionStore.ReadTranscript(sessionID)
	if err != nil {
		slog.Warn("delegate: status snapshot: failed to read transcript",
			"session_id", sessionID, "error", err)
		return nil
	}

	var lines []string
	for _, e := range entries {
		if e.ParentSpawnCallID != spawnCallID {
			continue
		}
		content := strings.TrimSpace(e.Content)
		if content == "" {
			continue
		}
		runes := []rune(content)
		if len(runes) > maxStatusActivityLineRunes {
			content = string(runes[:maxStatusActivityLineRunes]) + "…"
		}
		lines = append(lines, content)
	}
	if len(lines) == 0 {
		slog.Info("delegate: status snapshot: no recent activity found for this task yet",
			"session_id", sessionID, "spawn_call_id", spawnCallID)
		return nil
	}
	// Entries are in chronological (append) order — keep only the most
	// recent `maxLines`, preserving chronological order within that window.
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
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

// checkSteerCaps enforces the steer/respond body-size cap (16 KiB default)
// and per-target-session rate cap (6/min default) — ADR-053 §Contract
// Surface "Caps". Returns a clear, typed error naming the exceeded cap;
// callers surface it as a tool error (never-silent-drop applies to
// parent->child delivery too — a rejected steer must be visible to the
// PARENT, not silently dropped).
func (t *DelegateTool) checkSteerCaps(sessionID, text string) error {
	bodyCap := t.steerBodyBytes
	if bodyCap <= 0 {
		bodyCap = session.DefaultSteerBodyBytes
	}
	if len(text) > bodyCap {
		return fmt.Errorf("steer/respond body (%d bytes) exceeds the %d byte cap", len(text), bodyCap)
	}

	limit := t.steerRatePerMin
	if limit <= 0 {
		limit = session.DefaultSteerRatePerMinute
	}
	now := t.now()
	cutoff := now.Add(-1 * time.Minute)

	t.steerRateMu.Lock()
	defer t.steerRateMu.Unlock()

	// LOW-4 (14-reviewer sign-off): opportunistic full-map eviction, mirroring
	// cancel_prearm.go::markPendingSpawn's own pattern. The per-session logic
	// below always ends by storing THIS session's own entry back non-empty —
	// either the rate-limited kept window (>= limit, so never empty) or
	// kept+now (always >= 1) — so a session's own key never self-deletes,
	// even long after that session is terminal and will never call
	// steer/respond again. Left unaddressed, every distinct session_id ever
	// steered/responded-to accumulates a permanent entry in this map for the
	// life of the process. Sweep every OTHER session's window for entries
	// older than the rate window on each call and drop any that are now
	// fully empty, bounding the map to roughly the sessions actively
	// steered within the last minute.
	for sid, ts := range t.steerRateWindows {
		if sid == sessionID {
			continue // handled by the per-session logic below
		}
		live := ts[:0]
		for _, t2 := range ts {
			if t2.After(cutoff) {
				live = append(live, t2)
			}
		}
		if len(live) == 0 {
			delete(t.steerRateWindows, sid)
		} else {
			t.steerRateWindows[sid] = live
		}
	}

	window := t.steerRateWindows[sessionID]
	kept := window[:0]
	for _, ts := range window {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= limit {
		t.steerRateWindows[sessionID] = kept
		return fmt.Errorf("steer/respond rate exceeded (%d/min) for session %s", limit, sessionID)
	}
	t.steerRateWindows[sessionID] = append(kept, now)
	return nil
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
// ParentDurableKey at `run` time (D16). Every parent-side action
// (inbox/inbox_ack/steer/respond/cancel/follow_up/peek) uses this exact
// resolution so a caller can only ever address inboxes/sessions it itself
// spawned.
func callerOwnerKey(ctx context.Context) string {
	return strings.TrimSpace(ToolTranscriptSessionID(ctx))
}

// verifyCallerOwnsSession (ADR-057 W12/FR-039/FR-040) rejects a gated
// delegate action whose caller is not an ANCESTOR of rec — a direct parent,
// grandparent, and so on up to the configured max delegation depth
// (SetOwnershipWalkMaxDepth) — in the ParentDurableKey chain (defense in
// depth: a session_id alone is guessable/loggable; ownership must also
// match at the handler).
//
// Pre-ADR-057, a plain `caller == rec.ParentDurableKey` equality check was
// correct because ParentDurableKey was shared across an entire subtree (a
// parent's key was literally re-inherited down every generation) — which
// ALSO meant it accidentally permitted sibling/cousin reach (FR-040's
// "MUST be removed": any two sessions sharing the SAME parent — or the same
// distant ancestor — carried the identical ParentDurableKey value and thus
// passed the equality check against EACH OTHER's records, not just their
// real parent's). U13's ParentDurableKey redefinition (pkg/session/lifecycle.go's
// own doc comment: "names its DIRECT parent only — it is NOT re-inherited
// down the chain") already closed that leak by construction — a sibling's
// target now carries the immediate parent's key, never the caller's own —
// but it also silently broke the LEGITIMATE root-over-subtree case
// (BDD-42): a chat A that spawned child B, which spawned grandchild D, can
// no longer reach D via one-hop equality, because D's ParentDurableKey
// names B, not A. This walk restores that reach without reopening the
// sibling/cousin one: it climbs ONE hop per iteration (rec's own
// ParentDurableKey is depth 1, its parent's ParentDurableKey is depth 2,
// …), matching each hop against the caller, and stops — rejecting — the
// moment it either exhausts the depth bound (BDD-43) or reaches a link with
// no further LifecycleRecord to load (the root chat has none of its own,
// which is exactly the terminal, no-match case; a Load failure is never
// treated as an ownership match).
func (t *DelegateTool) verifyCallerOwnsSession(ctx context.Context, rec *session.LifecycleRecord) error {
	caller := callerOwnerKey(ctx)
	if caller == "" {
		return fmt.Errorf("session %s is not owned by the calling session", rec.SessionID)
	}
	ancestor := strings.TrimSpace(rec.ParentDurableKey)
	maxDepth := t.ownershipMaxDepth()
	for depth := 0; depth < maxDepth; depth++ {
		if ancestor == "" {
			break
		}
		if ancestor == caller {
			return nil
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
		ancestor = strings.TrimSpace(parentRec.ParentDurableKey)
	}
	return fmt.Errorf("session %s is not owned by the calling session", rec.SessionID)
}

func (t *DelegateTool) executeInbox(ctx context.Context, args map[string]any) *ToolResult {
	if t.inbox == nil {
		return ErrorResult("delegate: no message inbox configured")
	}
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	ownerKey := callerOwnerKey(ctx)
	if ownerKey == "" {
		return ErrorResult("delegate: no session context available to resolve the inbox owner key")
	}
	// FAIL-CLOSED (Security-MAJOR-1): caller-ownership verification is
	// MANDATORY — a Load error (not-found, corrupt tail, I/O) MUST NOT let
	// the inbox read fall through against whatever session_id the caller
	// named (a cross-tenant read gated on an induced read error — peek/inbox
	// would leak the victim's lifecycle state + persisted messages).
	// Previously the ownership check only ran when Load SUCCEEDED, so any
	// Load error skipped it and the inbox was drained regardless. Now deny
	// when the lifecycle store is unconfigured OR when Load errors, mirroring
	// executeCancel/executeSteer/executeRespond/executeFollowUp's posture
	// (the rest of ADR-053's fail-closed contract — see delegate.go:1808).
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox: %v", lerr))
	}
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox: %v", verr))
	}

	sinceCursor, _ := args["since_cursor"].(string)
	maxMessages := 0
	if raw, present := args["max"]; present && raw != nil {
		n, cerr := toIntArg(raw)
		if cerr != nil {
			return ErrorResult("max must be an integer")
		}
		maxMessages = n
	}

	// MEDIUM-2 (14-reviewer sign-off): key the Drain by rec.ParentDurableKey
	// (the target session's own DIRECT parent — the key its messages were
	// actually Appended under), NOT the calling ownerKey. verifyCallerOwnsSession
	// above already grants an authorized ANCESTOR (grandparent, etc., up to
	// SetOwnershipWalkMaxDepth — FR-039) reach into a descendant's inbox, but
	// a message is always stored under the child's own direct parent's key
	// (message_parent.go's Append call), never the calling ancestor's. Keying
	// this read by ownerKey silently returned an empty inbox for exactly the
	// authorized-ancestor case FR-039 exists to permit — the ownerKey
	// variable above and its own presence check remain (a caller must still
	// have SOME resolvable session identity to reach this far at all), but
	// the store key must be the target's own ParentDurableKey. executeRespond
	// already uses this correct key (see its own Drain call).
	msgs, nextCursor, hasMore, derr := t.inbox.Drain(rec.ParentDurableKey, sessionID, sinceCursor, maxMessages)
	if derr != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox: %v", derr)).WithError(derr)
	}

	resp := generated.DelegateInboxResponse{Messages: msgs, HasMore: hasMore}
	if nextCursor != "" {
		resp.NextCursor = &nextCursor
	}
	payload, merr := json.Marshal(resp)
	if merr != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox: encode response: %v", merr))
	}
	return NewToolResult(string(payload))
}

func (t *DelegateTool) executeInboxAck(ctx context.Context, args map[string]any) *ToolResult {
	if t.inbox == nil {
		return ErrorResult("delegate: no message inbox configured")
	}
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	ids, serr := stringSliceArg(args, "message_ids")
	if serr != nil {
		return ErrorResult(serr.Error())
	}
	if len(ids) == 0 {
		return ErrorResult("message_ids is required and must be a non-empty array of strings")
	}
	ownerKey := callerOwnerKey(ctx)
	if ownerKey == "" {
		return ErrorResult("delegate: no session context available to resolve the inbox owner key")
	}
	// HIGH (nested-delegation message leak, 2026-08): the READ path
	// (executeInbox/executePeek) was re-keyed to rec.ParentDurableKey but the
	// ACK path was left on the calling ownerKey, so read and ack disagreed
	// for every caller that is not the target's DIRECT parent. Because
	// verifyCallerOwnsSession deliberately permits an ANCESTOR (FR-039) —
	// whose key is by definition NOT rec.ParentDurableKey — an A -> B -> C
	// chain let A drain C's question successfully and then ack it against
	// A's OWN inbox file: every id came back Unknown, nothing was actually
	// acknowledged, the messages were redelivered on every subsequent drain,
	// and they permanently consumed C's InboxUnackedMax budget until C's
	// message_parent sends started failing outright (the ceiling is enforced
	// in pkg/session/message_inbox.go::MessageInboxStore.Append against the
	// child's own owner key, which only an ack under THAT key can relieve).
	// The ack must therefore be keyed exactly like the read: by the target
	// session's own direct parent, the key its messages were actually
	// Appended under.
	//
	// Loading the record also closes a second gap this action had: keying by
	// rec.ParentDurableKey without an ownership check would let any caller
	// ack messages in an inbox it does not own, so the same MANDATORY,
	// fail-closed verification executeInbox performs is applied here (a Load
	// error denies — it never falls through to whatever session_id the
	// caller named).
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox_ack: %v", lerr))
	}
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox_ack: %v", verr))
	}

	result, err := t.inbox.AckDetailed(rec.ParentDurableKey, ids)
	if err != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox_ack: %v", err)).WithError(err)
	}
	// M1 (UAT 2026-08): report the TRUTHFUL acknowledged count — a wholly
	// fabricated or already-unknown message_id must never inflate it — and
	// surface any unknown ids explicitly rather than silently folding them
	// into "success," so a caller reconciling its inbox against this count
	// notices the drift instead of trusting a number that doesn't match what
	// actually happened.
	msg := fmt.Sprintf("Acknowledged %d message(s).", len(result.Acknowledged))
	if len(result.Unknown) > 0 {
		msg += fmt.Sprintf(" %d message ID(s) were not recognized and could not be acknowledged: %s.",
			len(result.Unknown), strings.Join(result.Unknown, ", "))
	}
	return NewToolResult(msg)
}

func (t *DelegateTool) executeSteer(ctx context.Context, args map[string]any) *ToolResult {
	if t.steering == nil {
		return ErrorResult("delegate: no steering sink configured")
	}
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	text, err := requiredStringArg(args, "text")
	if err != nil {
		return ErrorResult(err.Error())
	}

	// ADR-057 W12: ownership is verified via a plain Load BEFORE the Mutate
	// below, deliberately OUTSIDE the atomic closure — unlike the terminal
	// check (see the TOCTOU comment below), ownership cannot race: a
	// session's ParentDurableKey is stamped once at mint time and carried
	// forward unchanged even across follow_up generations (see
	// spawnCorrectiveFollowUp's whole-struct-copy comment), so a Load taken
	// a moment before Mutate observes the exact same value Mutate itself
	// would. Verifying it here, rather than inside the closure below, is
	// not just style: the ownership walk (verifyCallerOwnsSession, FR-039)
	// climbs the ParentDurableKey chain via t.lifecycle.Load(ancestor) for
	// every hop beyond the direct parent, and pkg/session/lifecycle_lock.go's
	// striped lock is only 64-wide — an ancestor whose id happens to hash to
	// the SAME shard as sessionID would deadlock against Mutate's
	// already-held, non-reentrant per-shard sync.Mutex if the walk ran
	// inside that closure instead.
	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", lerr))
	}
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", verr))
	}

	// TOCTOU race guard: a plain Load() followed by a branch on
	// rec.Terminal() was a check-then-act race against the concurrent
	// atomic terminal transition in pkg/agent/task_executor.go
	// (session.TransitionSession) — if that transition landed in the window
	// between this Load and EnqueueSteeringMessage below, a stale
	// non-terminal snapshot passed the check and the steering message got
	// queued for a session with no live consumer left to read it (orphaned
	// permanently — pkg/agent/steering.go's queue has no liveness check).
	// The sibling action executeRespond already avoids this by routing
	// through LifecycleStore.Mutate, the atomic read-modify-write primitive
	// that holds the per-session striped lock across the whole read+decide
	// (pkg/session/lifecycle.go's own docs: "a naked Load+Persist races a
	// concurrent transition on the same session_id"). Doing the same here —
	// the terminal check evaluated INSIDE the Mutate closure, under the SAME
	// lock the terminal-transition writer uses — closes the window; a
	// transition landing just before we take the lock is now guaranteed
	// visible, and one landing just after must wait for us to release it.
	// This performs no actual field mutation on the non-error path (steer
	// delivery is a separate side channel, not a lifecycle field) — Mutate
	// persisting an unchanged copy of the tail record is a harmless,
	// deliberate byproduct of reusing the RMW primitive purely for its
	// locking guarantee. Ownership is deliberately NOT re-checked here — see
	// the comment above for why a stale ownership read cannot happen.
	if merr := t.lifecycle.Mutate(sessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return session.ErrLifecycleNotFound
		}
		if cur.Terminal() {
			return fmt.Errorf("session %s is terminal (%s) and cannot be steered", sessionID, cur.State)
		}
		rec = cur
		return nil
	}); merr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", merr))
	}

	if cerr := t.checkSteerCaps(sessionID, text); cerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", cerr)).WithError(cerr)
	}

	if serr := t.steering.EnqueueSteeringMessage(sessionID, rec.AgentID, providers.Message{Role: "user", Content: text}); serr != nil {
		return ErrorResult(fmt.Sprintf("delegate: steer: %v", serr)).WithError(serr)
	}
	return NewToolResult(fmt.Sprintf(
		"Steering message queued for session %s; it will apply at the child's next tool boundary.", sessionID,
	))
}

func (t *DelegateTool) executeRespond(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	// HIGH-1 (14-reviewer sign-off): a parked child's turn has already ENDED
	// (TurnEndStatusParked, via message_parent(wait=true)) — respond must
	// actually RESUME it, not merely unpark the lifecycle record and hope a
	// live consumer is still around to read the steering queue (it never is
	// — see the redispatch comment on the native path below). That resume
	// goes through the same spawner-backed isResume machinery follow_up
	// uses, so a missing spawner must be rejected up front, before touching
	// anything, exactly like executeFollowUp's own posture (checked before
	// any argument parsing) — never as a failure discovered mid-flow after
	// some other state has already changed.
	if t.spawner == nil {
		return ErrorResult("delegate: respond: no sub-turn spawner configured to resume the session")
	}
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	correlationID, err := requiredStringArg(args, "correlation_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	text, err := requiredStringArg(args, "text")
	if err != nil {
		return ErrorResult(err.Error())
	}

	// rec is loaded UNLOCKED here only for the fast-path pre-checks
	// (ownership, needs_input/correlation match, authority via inbox Drain).
	// The actual state transition is atomic — see the Mutate call below,
	// which re-verifies state + correlation UNDER the per-session striped
	// lock (Correctness-MAJOR-3) so a concurrent respond/cancel cannot
	// double-apply. Do NOT wrap this Load in a manual Lock(); the transition
	// uses Mutate, which takes the lock once internally.
	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", lerr))
	}
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", verr))
	}

	if rec.State != session.LifecycleNeedsInput || rec.NeedsInput == nil || rec.NeedsInput.CorrelationID != correlationID {
		return ErrorResult(fmt.Sprintf(
			"delegate: respond: session %s is not parked on correlation_id %q", sessionID, correlationID,
		))
	}
	if cerr := t.checkSteerCaps(sessionID, text); cerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", cerr)).WithError(cerr)
	}

	// R§8.2/FR-132: reject a respond targeting an owner_required question.
	// PHASE-1 SCOPING: this reads the original question's CHILD-AUTHORED
	// authority tag directly from the inbox — the runtime content-based
	// upgrade heuristic (deriveQuestionAuthority, FR-139) is Group F,
	// Phase 2; this wave implements only the mandatory fail-closed DEFAULT
	// (an omitted tag already resolved to owner_required at
	// message_parent.go's Append time, FR-131), which IS enforced here.
	//
	// FAIL-CLOSED (MAJOR-2): the authority check is a POSITIVE confirmation
	// that the target question is safe to answer, not a negative scan that
	// silently passes when the question can't be inspected. Previously a
	// Drain error OR a question excluded from the Drain result (ACKED by an
	// earlier inbox_ack) let the check silently pass — letting a parent
	// answer an owner_required question. Now every condition that prevents
	// the target question from being positively inspected DENIES the
	// respond:
	//   1. inbox not configured → deny (can't verify authority)
	//   2. Drain errors → deny (can't verify authority)
	//   3. target question absent from the Drain (acked / never existed)
	//      → deny (can't verify authority — R§8.2's fail-closed default)
	// Only when the question IS found in the drain AND its authority is not
	// owner_required does the respond proceed. Resolving the question
	// independent of ack state would need a new inbox-store primitive
	// (outside this wave's write-set); fail-closed-on-absent is the safe
	// posture until that lands.
	if t.inbox == nil {
		return ErrorResult("delegate: respond: no message inbox configured to verify question authority")
	}
	msgs, _, _, derr := t.inbox.Drain(rec.ParentDurableKey, sessionID, "", 0)
	if derr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: %v", derr)).WithError(derr)
	}
	questionVerified := false
	for _, m := range msgs {
		kind, kerr := m.Discriminator()
		if kerr != nil || kind != "question" {
			continue
		}
		q, qerr := m.AsSessionMessageQuestion()
		if qerr != nil || q.CorrelationId != correlationID {
			continue
		}
		if q.Authority != nil && string(*q.Authority) == "owner_required" {
			return ErrorResult(fmt.Sprintf(
				"delegate: respond: question %q requires owner/human authority and cannot be "+
					"answered by a parent directly (R§8.2)", correlationID,
			))
		}
		questionVerified = true
		break
	}
	if !questionVerified {
		return ErrorResult(fmt.Sprintf(
			"delegate: respond: question %q could not be verified in the inbox (it may be acked or absent) — "+
				"denying by default to enforce owner_required authority (R§8.2)", correlationID,
		))
	}

	// HIGH-1 ordering fix (14-reviewer sign-off): the un-park lifecycle
	// transition used to commit BEFORE delivery was even attempted — an
	// enqueue failure then left the record flipped away from needs_input
	// (NeedsInput cleared) with NO way to retry respond(), since the
	// correlation_id match it re-checks was already erased. Delivery is now
	// attempted FIRST, while the session is still safely parked; the
	// lifecycle is flipped only once delivery has genuinely succeeded, so a
	// failure at any point below leaves the session exactly as parked as it
	// was, ready for the caller to retry.
	//
	// Correctness-MAJOR-2 (3P respond state): a 3P respond spawns a NEW
	// corrective session (spawnCorrectiveFollowUp) and never warm-resumes the
	// original. Flipping the ORIGINAL to `running` would leave a record with
	// no live runtime turn, which status would falsely report as `running`
	// and the Phase-2 boot sweep would re-classify `failed(interrupted)`,
	// corrupting the terminal record. Instead the original is marked terminal
	// `cancelled` — recording via FailedReason that it was superseded by the
	// corrective re-dispatch. (FailedReason is the record's free-text "why
	// this ended" field; using it for a cancelled-via-supersession is more
	// informative than leaving the cancellation unexplained, and adding a
	// dedicated `superseded` state would be a wire-type change outside this
	// wave's scope.) The native path keeps the original `running` transition.
	nextState := session.LifecycleRunning
	var failedReason string
	if rec.Is3P {
		nextState = session.LifecycleCancelled
		failedReason = "superseded by corrective re-dispatch (3P respond)"
	}

	if rec.Is3P {
		// D5: 3P respond spawns a NEW corrective session — never an
		// in-place warm resume (external CLIs have no such primitive).
		// spawnCorrectiveFollowUp Persists the new generation under a
		// DIFFERENT session_id and dispatches it; it never touches THIS
		// (the original) record. Dispatch it FIRST — a failure here (e.g. a
		// Persist I/O error, or the inner executeAsync call) never marks the
		// original as superseded, so it stays parked and retryable.
		dispatch := t.spawnCorrectiveFollowUp(ctx, sessionID, rec,
			fmt.Sprintf("Answer to your question (correlation_id=%s): %s", correlationID, text), cb)
		if dispatch.IsError {
			return dispatch
		}
		// The corrective successor is confirmed dispatched — only now mark
		// the ORIGINAL terminal (superseded by the successor).
		if merr := t.lifecycle.Mutate(sessionID, func(cur *session.LifecycleRecord) error {
			if cur == nil {
				return session.ErrLifecycleNotFound
			}
			if cur.State != session.LifecycleNeedsInput || cur.NeedsInput == nil || cur.NeedsInput.CorrelationID != correlationID {
				return fmt.Errorf("session %s is not parked on correlation_id %q", sessionID, correlationID)
			}
			cur.State = nextState
			cur.NeedsInput = nil
			cur.FailedReason = failedReason
			return nil
		}); merr != nil {
			// The corrective successor is already running by this point —
			// returning an error here would misleadingly tell the caller
			// "respond failed" when the answer was in fact delivered. Log it
			// instead; the original record is a display/bookkeeping nicety at
			// this stage, not the source of truth for whether the answer landed.
			slog.Warn("delegate: respond: 3P corrective successor dispatched but the original could not be marked superseded",
				"session_id", sessionID, "error", merr)
		}
		return dispatch
	}

	// Native: the parked child's turn has already ENDED (TurnEndStatusParked)
	// — the steering queue below has no live consumer, and even a freshly
	// redispatched turn's OWN first iteration does not drain it
	// (SkipInitialSteeringPoll, set unconditionally for every subturn spawn —
	// see pkg/agent/subturn.go). EnqueueSteeringMessage is kept for any rare
	// case where the turn is somehow still alive to read it, but the actual,
	// guaranteed delivery mechanism is the redispatch below, which reuses the
	// SAME isResume spawn machinery `delegate follow_up` uses
	// (spawnCorrectiveFollowUp's native branch): the answer text becomes the
	// resumed turn's own first message (processOptions.UserMessage), loaded
	// against the child's existing, on-disk history.
	//
	// Enqueue is attempted FIRST, before any lifecycle mutation — an enqueue
	// failure returns immediately with the record still untouched (still
	// parked, retryable).
	if t.steering == nil {
		return ErrorResult("delegate: respond: no steering sink configured to deliver the answer")
	}
	if serr := t.steering.EnqueueSteeringMessage(sessionID, rec.AgentID, providers.Message{Role: "user", Content: text}); serr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: failed to deliver answer: %v", serr)).WithError(serr)
	}

	// Atomic claim: re-verify state + correlation UNDER the lock
	// (Correctness-MAJOR-3) so a concurrent respond/cancel on this same
	// session cannot double-apply. This MUST run BEFORE the redispatch below,
	// not after: executeAsync's own internal transitionLifecycle call
	// unconditionally (no correlation re-check) forces the record to
	// `running` the instant dispatch begins, so a Mutate placed after that
	// call would always observe a record no longer `needs_input` — even on
	// the single-caller happy path — and spuriously reject it. Placing the
	// claim here instead, immediately before a redispatch that cannot fail
	// synchronously (t.spawner == nil was already rejected at the top of this
	// function, before any side effect), delivers the same net effect the
	// ordering fix requires: the record is only ever flipped once delivery
	// (the enqueue above) has already succeeded, and never in a way an
	// enqueue failure could leave half-applied.
	if merr := t.lifecycle.Mutate(sessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return session.ErrLifecycleNotFound
		}
		if cur.State != session.LifecycleNeedsInput || cur.NeedsInput == nil || cur.NeedsInput.CorrelationID != correlationID {
			return fmt.Errorf("session %s is not parked on correlation_id %q", sessionID, correlationID)
		}
		cur.State = nextState
		cur.NeedsInput = nil
		cur.FailedReason = failedReason
		return nil
	}); merr != nil {
		return ErrorResult(fmt.Sprintf("delegate: respond: failed to resume session: %v", merr)).WithError(merr)
	}

	label := ""
	t.mu.Lock()
	if taskID, ok := t.sessionIndex[sessionID]; ok {
		if st, ok := t.tasks[taskID]; ok {
			label = st.Label
		}
	}
	t.mu.Unlock()

	dispatch := t.executeAsync(ctx,
		fmt.Sprintf("Answer to your question (correlation_id=%s): %s", correlationID, text),
		label, rec.AgentID, nil, sessionID, 0, nil, true, cb)
	if dispatch.IsError {
		return dispatch
	}

	resp := generated.DelegateRespondResponse{Acknowledged: true}
	payload, merr := json.Marshal(resp)
	if merr != nil {
		return NewToolResult("Answer delivered; session resumed.")
	}
	return NewToolResult(string(payload))
}

// cancelBackgroundShellWarnings renders the "could not be killed" /
// "descendant walk incomplete" warning sentences shared by every
// executeCancel outcome message — INCLUDING the TOCTOU "nothing to cancel"
// branch (MEDIUM-3, 14-reviewer sign-off): a background-shell kill failure or
// an incomplete descendant walk is real, caller-relevant information
// regardless of whether the turn-level cancel itself found anything left to
// cancel. Before this fix, killChildBackgroundShells' own warnings were
// computed unconditionally but appended ONLY to the two success-message
// branches below — the "terminated between the terminal check and the
// cancel hook" branch discarded them outright, so a caller could be told
// "nothing to cancel" while a background shell it just tried to kill was, in
// fact, left running with no warning at all.
func cancelBackgroundShellWarnings(killFailed int, walkIncomplete bool) string {
	var warnings string
	if killFailed > 0 {
		warnings += fmt.Sprintf(
			" WARNING: %d of that session's background shell(s) could not be killed and may still be running.",
			killFailed,
		)
	}
	if walkIncomplete {
		warnings += " WARNING: the descendant walk for this session's background shells failed partway through — " +
			"some of its descendants may not have been reached at all and their background shells could still be running."
	}
	return warnings
}

func (t *DelegateTool) executeCancel(ctx context.Context, args map[string]any) *ToolResult {
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	hard := false
	if raw, present := args["hard"]; present && raw != nil {
		b, ok := raw.(bool)
		if !ok {
			return ErrorResult("hard must be a boolean")
		}
		hard = b
	}
	// FAIL-CLOSED (MAJOR-1): caller-ownership verification is MANDATORY —
	// a Load error (not-found, corrupt tail, I/O) MUST NOT let the cancel
	// fall through against whatever session_id the caller named (a cross-
	// tenant DoS gated on an induced read error). Previously the ownership
	// check only ran when Load SUCCEEDED, so any Load error skipped it and
	// cancelHard/cancelSoft proceeded regardless. Now deny the cancel when
	// the lifecycle store is unconfigured OR when Load errors, mirroring
	// executeSteer/executeRespond/executeFollowUp's posture (the rest of
	// ADR-053's fail-closed contract).
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: cancel: %v", lerr))
	}
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: cancel: %v", verr))
	}

	// Cancelling an already-terminal session doesn't corrupt any state
	// (cancelSoft/cancelHard against a session with no live turn are
	// harmless no-ops). #588 (N9) required this to NOT reuse the
	// success-shaped "cooperatively cancelled" / "hard-cancelled
	// immediately" message — that wording would misleadingly claim an
	// action was taken when nothing happened. The rec.Terminal() check
	// below is a plain check-then-return, not a check-then-act race: a
	// terminal session never leaves that state (L-3 immutable-terminal
	// invariant), so there's nothing for a concurrent writer to race here.
	//
	// RC-3 (UAT amplification-loop fix, 2026-08): #588's OWN requirement
	// was narrower than the original implementation of it — it bars reusing
	// the success-cancel WORDING, it does not require this to be a tool-call
	// FAILURE. Reporting IsError:true here made an orchestrating agent read
	// routine cleanup (a worker session finishing before the parent's
	// cancel call landed) as breakage: in one real UAT session 20 of 28
	// cancel calls hit this branch, and the caller re-issued cancels and
	// re-spawned workers in a loop instead of treating "already done" as
	// success. SessionManager.KillAll (pkg/tools/session.go:635-637)
	// already treats an already-terminal candidate as a silent no-op —
	// this brings cancel in line with that precedent. The response is
	// still a SUCCESS with wording distinct from "cooperatively
	// cancelled"/"hard-cancelled" so it can never be mistaken for "I just
	// cancelled something" — do not re-fix this back to ErrorResult; that
	// would resurrect the RC-3 amplification loop while only restoring a
	// stricter reading of #588 than #588 itself required.
	//
	// There is, however, a TOCTOU window BETWEEN this check and the
	// cancelSoft/cancelHard call below: a non-terminal session can terminate
	// in that gap, in which case Interrupt/InterruptSessionHard (ScopeSelfOnly)
	// finds no live turnState and returns (nil descendants, nil error) — a documented
	// no-op. The pre-fix code discarded the descendants return and STILL
	// reported the success-shaped message, so a cancel that landed nothing
	// looked identical to one that actually interrupted. The cancelSoft/
	// cancelHard calls below now capture descendants and, when
	// len(descendants)==0 and cerr==nil, return the same idempotent-no-op
	// shape as this pre-dispatch check UNLESS a real background-shell kill
	// failure or an incomplete descendant walk was also detected — that
	// failure signal is a genuine partial failure, not a clean no-op, and
	// signoff14's MEDIUM-3 fix (delegate_signoff14_test.go) requires it to
	// still reach the caller as an actionable error rather than being
	// silently downgraded to success by this fix. See the len(descendants)==0
	// branches below for that split.
	if rec.Terminal() {
		return NewToolResult(fmt.Sprintf(
			"Session %s is already terminal (%s) — no action needed.", sessionID, rec.State,
		))
	}

	// ADR-057 FR-028/BDD-29/D8/R-13: delegate action="cancel" now also kills
	// that child's OWN background shells AND every durable descendant's
	// (D8-CASCADE) — before this fix, it reached only the single named
	// session, silently leaking a grandchild's background work (see
	// killChildBackgroundShells's own doc comment). Fired unconditionally of
	// hard/soft and BEFORE the turn-level escalation below, mirroring
	// RequestCancel's own "decoupled from the active-turn gate" design
	// (pkg/agent/cancel.go): a background shell is an OS process, not a
	// steerable LLM turn, so there is no reason to make it wait through the
	// cooperative grace window that exists for the turn.
	//
	// killFailed and walkIncomplete are carried into both success messages
	// below: this call previously reported the same unconditional
	// "cancelled"/"hard-cancelled" success text regardless of whether a
	// background shell for this session (or a descendant) actually died, or
	// whether the descendant walk itself broke down partway through — a real
	// kill failure or an incomplete walk was visible only in a log line,
	// never to the caller. A partial cascade must not read as a clean
	// success (binding requirement): walkIncomplete forces that WARNING into
	// the response even when killFailed is 0, since a walk that broke down
	// may have left descendants entirely unvisited (0 attempted, not 0
	// failed). The turn-level cancel outcome (hard/soft, checked separately
	// just below) and the shell-kill outcome are distinct facts; a caller
	// needs both to know what actually happened.
	_, killFailed, walkIncomplete := t.killChildBackgroundShells(sessionID)

	if hard {
		if t.cancelHard == nil {
			return ErrorResult("delegate: no hard-cancel hook configured")
		}
		descendants, cerr := t.cancelHard(sessionID, "delegate cancel(hard=true)")
		if cerr != nil {
			return ErrorResult(fmt.Sprintf("delegate: cancel: %v", cerr)).WithError(cerr)
		}
		if len(descendants) == 0 {
			// RC-3: a clean TOCTOU miss (no real kill failure, no incomplete
			// walk) is an idempotent no-op, same as the pre-dispatch
			// rec.Terminal() branch above — see that branch's comment for
			// the full rationale. A real background-shell kill failure or
			// an incomplete descendant walk is NOT a clean no-op though:
			// signoff14's MEDIUM-3 fix requires that failure signal to
			// still surface as an actionable error, so this branch keeps
			// the original error shape whenever killFailed>0 or
			// walkIncomplete (see delegate_signoff14_test.go's
			// TestDelegateTool_Cancel_NothingToCancel_StillSurfacesShellKillWarnings).
			if killFailed > 0 || walkIncomplete {
				return ErrorResult(fmt.Sprintf(
					"delegate: cancel: session %s terminated between the terminal check and the cancel hook — nothing to cancel",
					sessionID,
				) + cancelBackgroundShellWarnings(killFailed, walkIncomplete))
			}
			return NewToolResult(fmt.Sprintf(
				"Session %s terminated between the terminal check and the cancel hook — no action needed.",
				sessionID,
			) + cancelBackgroundShellWarnings(killFailed, walkIncomplete))
		}
		t.transitionLifecycle(sessionID, session.LifecycleCancelled, "stopped_by_user")
		msg := fmt.Sprintf("Session %s hard-cancelled immediately.", sessionID)
		msg += cancelBackgroundShellWarnings(killFailed, walkIncomplete)
		return NewToolResult(msg)
	}

	if t.cancelSoft == nil {
		return ErrorResult("delegate: no soft-cancel hook configured")
	}
	softDescendants, cerr := t.cancelSoft(sessionID, "delegate cancel(hard=false)")
	if cerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: cancel: %v", cerr)).WithError(cerr)
	}
	if len(softDescendants) == 0 {
		// RC-3: mirrors the hard-path branch above — a clean TOCTOU miss is
		// an idempotent no-op; a real kill failure or incomplete walk keeps
		// the original error shape (signoff14's MEDIUM-3 requirement).
		if killFailed > 0 || walkIncomplete {
			return ErrorResult(fmt.Sprintf(
				"delegate: cancel: session %s terminated between the terminal check and the cancel hook — nothing to cancel",
				sessionID,
			) + cancelBackgroundShellWarnings(killFailed, walkIncomplete))
		}
		return NewToolResult(fmt.Sprintf(
			"Session %s terminated between the terminal check and the cancel hook — no action needed.",
			sessionID,
		) + cancelBackgroundShellWarnings(killFailed, walkIncomplete))
	}

	// cancel(soft) = soft cooperative stop + a hard RequestCancel backstop
	// after the grace window, mirroring Interrupt/InterruptSessionHard's
	// existing two-phase escalation (steering.go, ScopeSelfOnly). The backstop only fires
	// if the session has NOT already reached a terminal state within grace.
	// (Comments-MINOR-3: the prior `// FR-...` prefix was a placeholder —
	// cancel is not a numbered FR; see ADR-053 R§Cancel/restart for the
	// two-phase prose this implements.)
	if t.cancelHard != nil {
		grace := t.cancelGrace
		go func() {
			time.Sleep(grace)
			if t.lifecycle != nil {
				if rec, lerr := t.lifecycle.Load(sessionID); lerr == nil && rec.Terminal() {
					return // cooperative stop already landed — no backstop needed
				}
			}
			// The descendants return closes the same TOCTOU window the
			// synchronous path above guards: if the session terminated
			// between the terminal check and this hard-cancel call,
			// cancelHard returns (nil, nil) and there is nothing left to
			// transition — skip transitionLifecycle rather than stamping a
			// redundant LifecycleCancelled onto an already-terminal record.
			backstopDescendants, cerr := t.cancelHard(sessionID, "delegate cancel(hard=false): grace elapsed")
			if cerr != nil {
				slog.Warn("delegate: cancel: hard-cancel backstop failed", "session_id", sessionID, "error", cerr)
				return
			}
			if len(backstopDescendants) == 0 {
				return
			}
			t.transitionLifecycle(sessionID, session.LifecycleCancelled, "stopped_by_user")
		}()
	}

	msg := fmt.Sprintf(
		"Session %s cooperatively cancelled; a checkpoint flush is expected within %s, "+
			"after which a hard cancel backstop fires if it has not stopped on its own.",
		sessionID, t.cancelGrace,
	)
	msg += cancelBackgroundShellWarnings(killFailed, walkIncomplete)
	return NewToolResult(msg)
}

func (t *DelegateTool) executeFollowUp(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	if t.spawner == nil {
		return ErrorResult("delegate: no sub-turn spawner configured")
	}
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}

	// follow_up's own schema documents "text" as the instruction field
	// (grouped with steer/respond everywhere the schema/description mentions
	// them together — e.g. session_id's own description lists "steer/
	// respond/cancel/follow_up/peek" as one family), but this action used to
	// read ONLY args["task"], silently falling back to a generic "Continue
	// the previous task." placeholder whenever a caller passed "text" per
	// the documented sibling-action convention — dropping the caller's real
	// instruction with no error at all. "task" survives as a deprecated
	// back-compat alias (text wins when both are present); an instruction
	// that is blank after trimming both is now a validation error, never a
	// silent placeholder substitution.
	instruction, ierr := followUpInstructionArg(args)
	if ierr != nil {
		return ErrorResult(ierr.Error())
	}

	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: follow_up: %v", lerr))
	}
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: follow_up: %v", verr))
	}
	// NOTE: this is a naked Load+Terminal check, NOT the LifecycleStore.Mutate
	// RMW that executeSteer/executeRespond use to close their check-then-act
	// TOCTOU window. The polarity is inverted here: follow_up RESUMES a
	// terminal session (executeAsync re-queues it as a new generation), so
	// the gate is `!rec.Terminal() -> reject`, and the immutable-terminal
	// invariant (L-3) means a session that is terminal at this Load STAYS
	// terminal — there is no concurrent transition that can flip it back to
	// non-terminal under us and race the branch. spawnCorrectiveFollowUp
	// below performs its OWN Persist under the lifecycle store's per-session
	// striped lock to mint the new generation; this read only decides whether
	// to enter that path. Do not "fix" this toward Mutate to match steer —
	// it would be a no-op lock acquisition on an immutable predicate.
	if !rec.Terminal() {
		return ErrorResult(fmt.Sprintf(
			"delegate: follow_up: session %s is not terminal (state=%s) — follow_up only resumes a finished session",
			sessionID, rec.State,
		))
	}

	return t.spawnCorrectiveFollowUp(ctx, sessionID, rec, instruction, cb)
}

// followUpInstructionArg resolves the new instruction for action="follow_up":
// "text" is the documented field, "task" is a deprecated back-compat alias
// consulted only when "text" is absent/blank. Returns an error naming the
// missing field when neither yields a non-blank string — no silent
// placeholder substitution.
func followUpInstructionArg(args map[string]any) (string, error) {
	if rawText, present := args["text"]; present && rawText != nil {
		s, ok := rawText.(string)
		if !ok {
			return "", fmt.Errorf("text must be a string")
		}
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			return trimmed, nil
		}
	}
	if rawTask, present := args["task"]; present && rawTask != nil {
		s, ok := rawTask.(string)
		if !ok {
			return "", fmt.Errorf("task must be a string")
		}
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			return trimmed, nil
		}
	}
	return "", fmt.Errorf(
		`text is required and must be a non-empty string for action="follow_up" (the new instruction to resume the session with; "task" is accepted as a deprecated alias)`,
	)
}

// spawnCorrectiveFollowUp is the shared mechanics behind `follow_up` (native
// and 3P) and a 3P `respond` (D5 — a 3P child never warm-resumes; every
// continuation is a NEW corrective session carrying the prior context).
// Native follow_up reuses sessionID verbatim (warm resume, same session, new
// generation — see agent.spawnSubTurn's childID-reuse mechanism); 3P mints a
// NEW session_id (cold respawn), linked back via ResumedFrom.
func (t *DelegateTool) spawnCorrectiveFollowUp(
	ctx context.Context,
	sessionID string,
	rec *session.LifecycleRecord,
	instructions string,
	cb AsyncCallback,
) *ToolResult {
	newSessionID := sessionID
	if rec.Is3P {
		newSessionID = uuid.NewString()
	}

	label := ""
	t.mu.Lock()
	if taskID, ok := t.sessionIndex[sessionID]; ok {
		if st, ok := t.tasks[taskID]; ok {
			label = st.Label
			instructions = fmt.Sprintf("Original task: %s\n\n%s", st.Task, instructions)
		}
	}
	t.mu.Unlock()

	// The whole-struct copy is load-bearing for FR-034: ParentAgentID (and
	// ParentDurableKey/OriginChannel/OriginChatID with it) MUST be carried
	// forward onto every generation mint. It is deliberately CARRIED FORWARD
	// from the prior generation rather than re-sourced from ToolAgentID(ctx)
	// — the follow_up caller is not necessarily the agent that originally
	// spawned the session, and re-sourcing would silently re-parent it. Do
	// not replace this copy with field-by-field construction.
	newRec := *rec
	newRec.SessionID = newSessionID
	newRec.Generation = rec.Generation + 1
	newRec.ResumedFrom = sessionID
	newRec.State = session.LifecycleQueued
	newRec.FailedReason = ""
	newRec.NeedsInput = nil
	if err := t.lifecycle.Persist(&newRec); err != nil {
		return ErrorResult(fmt.Sprintf("delegate: follow_up: failed to persist new generation: %v", err)).WithError(err)
	}

	// timeout: 0 (use the spawner's default) — a follow_up resume does not
	// carry forward any original timeout_seconds override from the prior
	// generation; the timeout_seconds thread-through is scoped to the
	// initial `run` dispatch only.
	//
	// isResume: !rec.Is3P — mirrors newSessionID's own native/3P branch
	// above. Native follow_up reuses sessionID VERBATIM (newSessionID ==
	// sessionID), so the spawner must treat this as a WARM RESUME of that
	// already-existing session rather than attempt to create it — routing it
	// through the ordinary create path would always collide with FR-096's
	// collision guard (BDD-107), since the directory it would be "creating"
	// is the very one being resumed. A 3P respawn mints a genuinely NEW
	// session_id (newSessionID != sessionID), so it is a real create like any
	// other dispatch and must NOT set IsResume.
	return t.executeAsync(ctx, instructions, label, rec.AgentID, nil, newSessionID, 0, nil, !rec.Is3P, cb)
}

func (t *DelegateTool) executePeek(ctx context.Context, args map[string]any) *ToolResult {
	if t.inbox == nil {
		return ErrorResult("delegate: no message inbox configured")
	}
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	ownerKey := callerOwnerKey(ctx)
	if ownerKey == "" {
		return ErrorResult("delegate: no session context available to resolve the inbox owner key")
	}

	// FAIL-CLOSED (Security-MAJOR-1): caller-ownership verification is
	// MANDATORY — same posture as executeInbox above. The prior `if lerr == nil`
	// pattern skipped ownership verification on ANY Load error, so peek
	// leaked the victim's lifecycle state (info disclosure) whenever Load
	// errored. Now deny when the lifecycle store is unconfigured OR when Load
	// errors OR when ownership mismatches.
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: peek: %v", lerr))
	}
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: peek: %v", verr))
	}
	state := string(rec.State)

	// MEDIUM-2 (14-reviewer sign-off): key the Peek by rec.ParentDurableKey,
	// not the calling ownerKey — see executeInbox's identical fix above for
	// the full rationale (FR-039 grants an authorized ancestor reach beyond
	// the direct parent, but messages are always stored under the target's
	// own direct parent's key).
	snap, perr := t.inbox.Peek(rec.ParentDurableKey, sessionID)
	if perr != nil {
		return ErrorResult(fmt.Sprintf("delegate: peek: %v", perr)).WithError(perr)
	}

	resp := generated.DelegatePeekResponse{
		SessionId: sessionID,
		State:     generated.DelegatePeekResponseState(state),
	}
	if snap.HasCheckpoint {
		summary := snap.LatestCheckpointSummary
		resp.LatestCheckpointSummary = &summary
	}
	if snap.HasProgress {
		text := snap.LatestProgressText
		resp.LatestProgressText = &text
		resp.LatestProgressPct = snap.LatestProgressPct
	}
	payload, merr := json.Marshal(resp)
	if merr != nil {
		return NewToolResult(fmt.Sprintf("session %s: state=%s", sessionID, state))
	}
	return NewToolResult(string(payload))
}
