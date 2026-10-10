// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package steer

import (
	"context"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// LaunchRequest is SessionLauncher.Launch's input (I-2).
type LaunchRequest struct {
	// SteeringSessionID is the caller's session; empty for a human-,
	// schedule- or plan-created task (no edge; an ordinary_root).
	SteeringSessionID string
	// TargetAgentID is the agent profile to run.
	TargetAgentID string
	// Label is an optional short title; Task text is the fallback. Both
	// empty is refused with ErrTitleRequired before any write (Q15).
	Label string
	// Task is the first user message.
	Task string
	// Origin is the record-level discriminator (I-1) — the front door
	// (delegate/task/...) and its originating tool-call id.
	Origin Origin
	// WorkspaceID, Owner are set ONLY for an ordinary-root launch with no
	// steering session (a scheduled or human-created task): taken from the
	// task record. For a steered launch they are inherited from the
	// steering session and these inputs must be empty.
	WorkspaceID string
	Owner       string
	// PlanID gives an ordinary-root task launch durable plan ownership.
	// Empty means OwnerScopeHuman.
	PlanID string
	// Goal is optional — criteria plus a Definition of Done, in the same
	// shape create_task validates (pkg/tools/task.go::validateRequest).
	// Nil means no goal (founder decision, round 3).
	Goal *GoalSpec
	// Limits, ToolExclusions are as I-1's SteeredBy fields.
	Limits         Limits
	ToolExclusions []string
	// RequestedSkill is delegate.run's optional `requested_skill` (ADR-072
	// D9, spec FR-050/FR-053/FR-054): "a hard request, not a hint". Empty
	// means no skill was requested. When non-empty, Launch resolves it
	// against the TARGET agent's own grant — never the caller's — and
	// refuses the launch outright (no session created) when the target is
	// not granted it or the slug does not resolve at all, rather than
	// silently proceeding without it.
	RequestedSkill string
	// ContextReferences and ContextNotes are delegate.run's curated context
	// snapshot (`snapshot.references` / `snapshot.notes`, R§8.5): the
	// parent-named artifact references and free-text notes the child is
	// meant to start with. Already validated against the snapshot caps by
	// the delegate tool; Launch seeds them into the child's first message,
	// after Task. Both empty means no snapshot.
	ContextReferences []string
	ContextNotes      string
}

// LaunchResult is SessionLauncher.Launch's output (I-2).
type LaunchResult struct {
	SessionID string
	// Generation is 1 at launch.
	Generation int
	// Is3P is the runtime classification Launch PERSISTED on the child's
	// lifecycle record (true: external CLI, false: native). Callers that report
	// the child's runtime (delegate's is_3p) project this value; they never
	// re-resolve it, because a later executor change would make a second read
	// describe a different runtime from the child that was actually created.
	Is3P bool
}

// DispatchState is DispatchResult.State's enum (I-2) — the authoritative
// admission decision, decided atomically under the admission lock inside
// Dispatch.
type DispatchState string

const (
	// DispatchRunning means Dispatch registered and ran the first turn.
	DispatchRunning DispatchState = "running"
	// DispatchQueued means the effective concurrency cap was reached; the
	// record is queued and the admission loop starts it in launch order as
	// slots free. Nothing blocks and nothing is refused for the cap
	// (founder decision, round 8).
	DispatchQueued DispatchState = "queued"
)

// DispatchResult is SessionLauncher.Dispatch's output (I-2) — the
// authoritative admission decision (R06).
type DispatchResult struct {
	State DispatchState
	// ConcurrencyLimit is the effective running-turn cap used for this
	// decision. It is populated when State == DispatchQueued so the caller can
	// explain why it queued.
	ConcurrencyLimit int
	// QueuePosition is 1-based when State == DispatchQueued, else 0.
	QueuePosition int
	// Generation is the generation the turn was (or will be) registered in.
	Generation int
}

// Audience is AudienceResolver's answer to "who is this session's
// audience?" (I-5): a human user, its steering session, or nobody. A
// boundary that resolves AudienceNone logs a diagnostic and does not
// publish.
type Audience string

const (
	AudienceUser            Audience = "user"
	AudienceSteeringSession Audience = "steering_session"
	AudienceNone            Audience = "none"
)

// Class is RecordClassifier's answer (I-8) — one of the six classes a
// lifecycle record (plus the session's own metadata) can resolve to.
type Class string

const (
	ClassOrdinaryRoot   Class = "ordinary_root"
	ClassSteered        Class = "steered"
	ClassDamagedChild   Class = "damaged_child"
	ClassLegacyDelegate Class = "legacy_delegate"
	ClassUnreadable     Class = "unreadable"
	ClassInvalidEdge    Class = "invalid_edge"
)

// Runnable reports whether a session classified c may be dispatched — I-3
// reconstruction refuses anything but ClassSteered or ClassOrdinaryRoot.
func (c Class) Runnable() bool {
	return c == ClassSteered || c == ClassOrdinaryRoot
}

// Outcome is UpwardEvent's turn-outcome discriminator (I-5's "Turn outcome"
// table column) — what a child's turn concluded with, before it is mapped
// onto the SessionMessage kind the inbox already stores.
type Outcome string

const (
	OutcomeFinalAnswer     Outcome = "final_answer"
	OutcomeEmptyAnswer     Outcome = "empty_answer"
	OutcomeParkedQuestion  Outcome = "parked_question"
	OutcomeInterrupted     Outcome = "interrupted"
	OutcomeTimedOut        Outcome = "timed_out"
	OutcomeFailed          Outcome = "failed"
	OutcomeWaitingChildren Outcome = "waiting_for_children"
	OutcomeProgress        Outcome = "progress"
	OutcomeCheckpoint      Outcome = "checkpoint"
	OutcomeBlocker         Outcome = "blocker"
	OutcomeLifecycleNotice Outcome = "lifecycle_notice"
	OutcomeGoalVerdict     Outcome = "goal_verdict"
)

// UpwardEvent is UpwardDeliverer.Deliver's input (I-5). The event IS an
// existing SessionMessage — there is no second vocabulary; every Outcome
// maps onto a kind the inbox already stores and the SPA already renders.
type UpwardEvent struct {
	ChildSessionID string
	// Generation pins a terminal event to the lifecycle generation that its
	// producer validated. Zero means the deliverer uses the currently loaded
	// generation, preserving existing non-completion producers.
	Generation int
	Outcome    Outcome
	Message    generated.SessionMessage

	// SuppressWake stores the entry without waking the recipient (the
	// delivery still runs its frames, and the outcome reports
	// DeliveryStoredNotWoken). Zero value = the class's own wake-eligibility,
	// so every existing producer is unchanged. Set ONLY where a wake would
	// double-re-enter the parent for one logical event: the session-goal
	// "met" verdict (founder Q1=A, #984 follow-up) — the completion handback
	// that follows it is the one wake worth the parent's turn, the verdict
	// stays visible through its side-panel entry, and the entry is
	// acknowledged at hand-back time (pkg/agent goal path) so boot recovery
	// never re-wakes it. Never set it on a message the parent must ACT on —
	// questions, blockers and errors keep their wakes.
	SuppressWake bool

	// RecipientSessionID, when set, addresses the report to that session's inbox
	// instead of the child's own steering edge (session-core U6, FR-020: a task
	// run notifies each captured recipient). The child needs no steering edge for
	// this; a recipient that is not the child's real parent receives the inbox
	// entry and the wake only — never the side-panel subagent frames. Empty keeps
	// every existing producer unchanged.
	RecipientSessionID string
}

// DeliveryOutcome is Delivery.Outcome's enum (I-5).
type DeliveryOutcome string

const (
	// DeliveryWoke means the recipient's inbox entry was written and the
	// recipient woken.
	DeliveryWoke DeliveryOutcome = "woke"
	// DeliveryQueuedIntoLiveTurn means the recipient already has a live
	// turn; the wake was enqueued as a steering message into it — no
	// second turn.
	DeliveryQueuedIntoLiveTurn DeliveryOutcome = "queued_into_live_turn"
	// DeliveryStoredNotWoken means the entry was durably stored but the
	// recipient was NOT woken. It is the outcome for every non-wake path:
	// a recipient carrying a Stop marker for its current generation (it is
	// acknowledged at revival), a message kind that is not wake-eligible
	// (progress/checkpoint/non-fatal error), a missing recipient record, an
	// already-acked deterministic duplicate, a wake that failed, and a
	// process with no async notifier wired — see
	// pkg/agent/steer_audience.go::SteerUpwardDeliverer.Deliver for the
	// complete list.
	//
	// ADR-091 fix lane RX-OUTCOME: there used to be a fourth value here,
	// DeliverySuppressed, for "the wake was throttled away by the debounce
	// or the hourly cap". It had ZERO producers and ZERO consumers
	// repo-wide and was DELETED rather than implemented (founder decision).
	// It could not be produced even in principle: a wake-eligible kind
	// bypasses the debounce entirely (async_notifier.go::WakeParentAlways
	// never consults allowWake), and a non-wake-eligible kind never reaches
	// a wake at all — it returns stored_not_woken above. The spec
	// (docs/internal/specs/adr-091-wp-b-audience-delivery-spec.md) was
	// corrected to describe that behaviour instead of promising this value.
	// Do NOT reintroduce it as an alias or a shim.
	DeliveryStoredNotWoken DeliveryOutcome = "stored_not_woken"
)

// Delivery is UpwardDeliverer.Deliver's output (I-5). MessageID is the
// inbox entry's id and is the delivery identity that travels in the wake
// (WakeInput.MessageID). Terminal entries have deterministic ids
// (`<child>:<gen>:final`), so the same outcome recreated after a crash is
// the same entry.
type Delivery struct {
	MessageID string
	Outcome   DeliveryOutcome
}

// WakeInput is the inbox entry that woke a session (I-3) — nil for a first
// run or a human message. Generation is the recipient's generation at the
// time the entry was written; reconstruction refuses a wake whose
// Generation is older than the record's current one (ErrStaleGeneration).
type WakeInput struct {
	MessageID string
	// Kind is the SessionMessage kind the entry carries (e.g. "handback",
	// "question", "blocker", "error", "goal_status") — restated as a plain
	// string here rather than importing the generated union type, since
	// WakeInput only ever compares this against a known set of literals.
	Kind       string
	Generation int
}

// UnreachableSession names one session a cascade could not reach, and why
// (I-6 CancelReport.Unreachable).
type UnreachableSession struct {
	ID     string
	Reason string
	// HelperTreeUnlisted marks an entry that says nothing about ID's own Stop:
	// ID itself was stopped, but its durable helper tree could not be listed,
	// so helpers under it may still be running. Summaries count ID as stopped
	// and report the unlisted tree separately.
	HelperTreeUnlisted bool
}

// CancelReport is Canceller.CancelSubtree's output (I-6). Partial walks are
// reported, never silent.
type CancelReport struct {
	Reached     []string
	Unreachable []UnreachableSession
	// SkippedNewerGeneration lists sessions whose cancel targeted a
	// generation a concurrent revival had already moved past.
	SkippedNewerGeneration []string
	// Superseded is the subset of SkippedNewerGeneration whose accepted
	// selection was no longer current when its effect ran: it already
	// landed, or a newer explicit action (Resume, completion) replaced it
	// (ADR-20260928 D2/D5). The Stop's effect was a no-op for them.
	Superseded []string
	// SkippedTerminal lists terminal descendants a Stop wrote nothing for
	// (terminal records are immutable).
	SkippedTerminal []string
}

// Boundary names one of the places a human could see something
// published. Each boundary calls the injected
// AudienceResolver, then BoundaryObserver.Observe, and nothing else
// decides.
type Boundary string

const (
	BoundarySyncToolText             Boundary = "sync_tool_text"
	BoundaryAsyncToolFeedback        Boundary = "async_tool_feedback"
	BoundaryFinalReply               Boundary = "final_reply"
	BoundaryMedia                    Boundary = "media"
	BoundaryRetryNotice              Boundary = "retry_notice"
	BoundaryWebchatStreaming         Boundary = "webchat_streaming"
	BoundaryExternalChannelStreaming Boundary = "external_channel_streaming"
	BoundaryAgentRequestedMessage    Boundary = "agent_requested_message"
	BoundaryTaskResultNotification   Boundary = "task_result_notification"
	BoundaryTypedErrorFrame          Boundary = "typed_error_frame"
	BoundaryQuestionCard             Boundary = "question_card"
)

// Boundaries is the complete, ordered inventory of reachable publication
// boundaries — the set every containment test and control iterates over.
var Boundaries = []Boundary{
	BoundarySyncToolText,
	BoundaryAsyncToolFeedback,
	BoundaryFinalReply,
	BoundaryMedia,
	BoundaryRetryNotice,
	BoundaryWebchatStreaming,
	BoundaryExternalChannelStreaming,
	BoundaryAgentRequestedMessage,
	BoundaryTaskResultNotification,
	BoundaryTypedErrorFrame,
	BoundaryQuestionCard,
}

// BootHook is the function Deps.BootHook names — the same function
// pkg/gateway/gateway_boot.go runs at process boot (I-7's Reboot() hook
// runs the identical function against a fixture's reopened stores).
type BootHook func(ctx context.Context) error

// Deps bundles the I-7 fixture dependencies: every steer interface
// implementation plus the two real stores and the boot hook, all injected
// at construction so pkg/agent/testutil's DelegationTree fixture (and
// gateway_boot.go's own wiring) build from one shape.
type Deps struct {
	Launcher       SessionLauncher
	Canceller      Canceller
	Deliverer      UpwardDeliverer
	Classifier     RecordClassifier
	LifecycleStore *session.LifecycleStore
	SessionStore   *session.UnifiedStore
	// BootEpoch is the one store this process minted at gateway boot.
	// Readers use Current. Mint stays the gateway's single call.
	BootEpoch *session.BootEpochStore
	BootHook  BootHook
}
