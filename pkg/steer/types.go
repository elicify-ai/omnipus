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
}

// LaunchResult is SessionLauncher.Launch's output (I-2).
type LaunchResult struct {
	SessionID string
	// Generation is 1 at launch.
	Generation int
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
	Outcome        Outcome
	Message        generated.SessionMessage
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
	// DeliveryStoredNotWoken means the entry was stored but the recipient
	// itself carries a Stop marker; it is acknowledged at revival.
	DeliveryStoredNotWoken DeliveryOutcome = "stored_not_woken"
	// DeliverySuppressed is possible only for progress/checkpoint and
	// non-fatal error kinds — never for a wake-eligible kind.
	DeliverySuppressed DeliveryOutcome = "suppressed"
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
}

// CancelReport is Canceller.CancelSubtree's output (I-6). Partial walks are
// reported, never silent.
type CancelReport struct {
	Reached     []string
	Unreachable []UnreachableSession
	// SkippedNewerGeneration lists sessions whose cancel targeted a
	// generation a concurrent revival had already moved past.
	SkippedNewerGeneration []string
	// SkippedTerminal lists terminal descendants a Stop wrote nothing for
	// (terminal records are immutable).
	SkippedTerminal []string
}

// Boundary names one of the places a human could see something
// published (landing order §6). Each boundary calls the injected
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
// at construction so WP-G's DelegationTree fixture (and gateway_boot.go's
// own wiring) build from one shape.
type Deps struct {
	Launcher       SessionLauncher
	Canceller      Canceller
	Deliverer      UpwardDeliverer
	Classifier     RecordClassifier
	LifecycleStore *session.LifecycleStore
	SessionStore   *session.UnifiedStore
	BootHook       BootHook
}
