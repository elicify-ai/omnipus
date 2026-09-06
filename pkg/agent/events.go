package agent

import (
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// EventKind identifies a structured agent-loop event.
//
// MarshalJSON uses a value receiver and UnmarshalJSON uses a pointer receiver
// — the standard Go JSON codec pair. recvcheck is suppressed here because
// MarshalJSON cannot use a pointer receiver without breaking fmt.Stringer
// for value instances (e.g. range-loop variables).
//
//nolint:recvcheck
type EventKind uint8

const (
	// EventKindTurnStart is emitted when a turn begins processing.
	EventKindTurnStart EventKind = iota
	// EventKindTurnEnd is emitted when a turn finishes, successfully or with an error.
	EventKindTurnEnd
	// EventKindLLMRequest is emitted before a provider chat request is made.
	EventKindLLMRequest
	// EventKindLLMDelta is emitted when a streaming provider yields a partial delta.
	EventKindLLMDelta
	// EventKindLLMResponse is emitted after a provider chat response is received.
	EventKindLLMResponse
	// EventKindLLMRetry is emitted when an LLM request is retried.
	EventKindLLMRetry
	// EventKindContextCompress is emitted when session history is forcibly compressed.
	EventKindContextCompress
	// EventKindToolExecStart is emitted immediately before a tool executes.
	EventKindToolExecStart
	// EventKindToolExecEnd is emitted immediately after a tool finishes executing.
	EventKindToolExecEnd
	// EventKindToolExecSkipped is emitted when a queued tool call is skipped.
	EventKindToolExecSkipped
	// EventKindSteeringInjected is emitted when queued steering is injected into context.
	EventKindSteeringInjected
	// EventKindFollowUpQueued is emitted when an async tool queues a follow-up system message.
	EventKindFollowUpQueued
	// EventKindInterruptReceived is emitted when a soft interrupt message is accepted.
	EventKindInterruptReceived
	// EventKindSubTurnSpawn is emitted when a sub-turn is spawned.
	EventKindSubTurnSpawn
	// EventKindSubTurnEnd is emitted when a sub-turn finishes.
	EventKindSubTurnEnd
	// EventKindSubTurnResultDelivered is emitted when a sub-turn result is delivered.
	EventKindSubTurnResultDelivered
	// EventKindSubTurnOrphan is emitted when a sub-turn result cannot be delivered.
	EventKindSubTurnOrphan
	// EventKindError is emitted when a turn encounters an execution error.
	EventKindError
	// EventKindTurnTimeout is emitted when a turn exceeds its configured timeout.
	EventKindTurnTimeout
	// EventKindEmptyResponseRetry is emitted when the LLM returns an empty response and a retry is attempted.
	EventKindEmptyResponseRetry
	// EventKindCompactionRetry is emitted when context compaction is triggered due to a timeout.
	EventKindCompactionRetry
	// EventKindBackgroundProcessKill is emitted when a background process is force-killed after exceeding its timeout.
	EventKindBackgroundProcessKill
	// EventKindRateLimit is emitted when an agent LLM or tool call is denied by a rate limit (SEC-26).
	EventKindRateLimit
	// EventKindWhatsAppPairing is emitted when the WhatsApp native channel produces
	// a linked-device pairing update (QR code or status) to surface in the SPA (#283).
	EventKindWhatsAppPairing
	// EventKindNotification is emitted when a user-facing notification is raised
	// (e.g. a scheduled run failed). Delivered live only to the recipient user's
	// WebSocket connections (#264).
	EventKindNotification
	// EventKindTaskStatusChanged is emitted when a workflow task transitions
	// status (queued→running→completed/failed). The WS forwarder turns it into a
	// task_status_changed frame so the SPA can invalidate its tasks cache.
	EventKindTaskStatusChanged
	// EventKindPlanStatusChanged is emitted when a Plan's state, plan_phase,
	// progress, or paused_reason changes (ADR-049 D4/D7, spec Part B R3). The
	// WS forwarder turns it into a plan_status frame, broadcast to every
	// connection (a Plan is workspace-scoped, not tied to one chat session —
	// mirrors EventKindTaskStatusChanged's broadcast, not EventKindNotification's
	// per-recipient filter).
	EventKindPlanStatusChanged
	// EventKindGoalStatusChanged is emitted whenever a session's `/goal` loop
	// state changes (set, round advance, met, bound reached, cleared —
	// ADR-049 D6/D7, spec Part B US-8). The WS forwarder turns it into a
	// goal_status frame, broadcast to every connection (mirrors
	// EventKindPlanStatusChanged/EventKindTaskStatusChanged's broadcast — the
	// SPA filters by session_id client-side).
	EventKindGoalStatusChanged
	// EventKindLoopStatusChanged is emitted whenever a session's `/loop` state
	// changes (set, run fired, run-cap reached, stop — ADR-049 D6/D7, spec
	// Part B US-9). The WS forwarder turns it into a loop_status frame,
	// broadcast to every connection.
	EventKindLoopStatusChanged
	// EventKindTaskRunStatus is emitted when a per-task-execution TaskRun
	// record (ADR-050, docs/internal/specs/task-run-history-spec.md §3.8 —
	// additive alongside Task.status/EventKindTaskStatusChanged) opens or
	// closes. A recurring occurrence's queued→in_progress→done transition
	// does not move Task.status between distinct values the calendar reads
	// (RD2), so the WS forwarder turns THIS event into a task_run_status
	// frame the calendar's per-occurrence chip can key off instead.
	EventKindTaskRunStatus
	// EventKindToolResultProjection is emitted when the D5 emptying pass
	// (ADR-066 FR-022, pkg/agent/empty_in_place.go) replaces an already
	// delivered tool result's content with the recall mark in the model's
	// window. The WS forwarder turns it into a tool_result_projection frame
	// (generated.ToolResultProjectionFrame) so the SPA can re-render the
	// matching tool call; on reload the same state is read from
	// ToolCall.content_state on the transcript.
	EventKindToolResultProjection

	eventKindCount
)

// Compile-time assertion: eventKindNames must have exactly eventKindCount entries.
var _ [eventKindCount]string = eventKindNames

var eventKindNames = [...]string{
	"turn_start",
	"turn_end",
	"llm_request",
	"llm_delta",
	"llm_response",
	"llm_retry",
	"context_compress",
	"tool_exec_start",
	"tool_exec_end",
	"tool_exec_skipped",
	"steering_injected",
	"follow_up_queued",
	"interrupt_received",
	"subturn_spawn",
	"subturn_end",
	"subturn_result_delivered",
	"subturn_orphan",
	"error",
	"turn_timeout",
	"empty_response_retry",
	"compaction_retry",
	"background_process_kill",
	"rate_limit",
	"whatsapp_pairing",
	"notification",
	"task_status_changed",
	"plan_status_changed",
	"goal_status_changed",
	"loop_status_changed",
	"task_run_status",
	"tool_result_projection",
}

// String returns the stable string form of an EventKind.
func (k EventKind) String() string {
	if k >= eventKindCount {
		return fmt.Sprintf("event_kind(%d)", k)
	}
	return eventKindNames[k]
}

// MarshalJSON emits the canonical string form of an EventKind so that
// subprocess hooks (and any other JSON consumer) see e.g. "tool_exec_start"
// rather than the underlying uint8 index 8. Without this, the wire payload
// for hook.event notifications was an integer that subprocess authors had
// to map manually — and the index would silently shift if a new EventKind
// was added in the middle of the enum.
//
// Regression guard for #164.
func (k EventKind) MarshalJSON() ([]byte, error) {
	return []byte(`"` + k.String() + `"`), nil
}

// UnmarshalJSON parses the canonical string form back into an EventKind.
// Round-trips with MarshalJSON. Returns an error for unknown names so a
// typo or stale schema gets surfaced instead of silently mapping to
// EventKindTurnStart (the zero value).
func (k *EventKind) UnmarshalJSON(data []byte) error {
	// Strip surrounding quotes.
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return fmt.Errorf("EventKind: expected JSON string, got %s", data)
	}
	name := string(data[1 : len(data)-1])
	for i, n := range eventKindNames {
		if n == name {
			*k = EventKind(i)
			return nil
		}
	}
	return fmt.Errorf("EventKind: unknown name %q", name)
}

// Event is the structured envelope broadcast by the agent EventBus.
type Event struct {
	Kind    EventKind `json:"Kind"`
	Time    time.Time `json:"Time"`
	Meta    EventMeta `json:"Meta"`
	Payload any       `json:"Payload"`
}

// EventMeta contains correlation fields shared by all agent-loop events.
type EventMeta struct {
	AgentID      string `json:"AgentID"`
	TurnID       string `json:"TurnID"`
	ParentTurnID string `json:"ParentTurnID"`
	SessionKey   string `json:"SessionKey"`
	Iteration    int    `json:"Iteration"`
	TracePath    string `json:"TracePath"`
	Source       string `json:"Source"`
}

// TurnEndStatus describes the terminal state of a turn.
type TurnEndStatus string

const (
	// TurnEndStatusCompleted indicates the turn finished normally.
	TurnEndStatusCompleted TurnEndStatus = "completed"
	// TurnEndStatusError indicates the turn ended because of an error.
	TurnEndStatusError TurnEndStatus = "error"
	// TurnEndStatusAborted indicates the turn was hard-aborted and rolled back.
	TurnEndStatusAborted TurnEndStatus = "aborted"
	// TurnEndStatusParked indicates the turn stopped because a tool call
	// (message_parent kind=question wait=true) parked the calling session's
	// own durable LifecycleRecord in needs_input (ADR-053 §5.1), awaiting a
	// parent response via `delegate respond`. Deliberately distinct from
	// Aborted: there is no rollback and no error — the turn's history up to
	// and including the parking tool call's own recorded result is left
	// intact (runTurn returns immediately after finishing that bookkeeping),
	// so a later `respond` + steering-queue resume continues from exactly
	// this point rather than replaying or discarding anything. See
	// pkg/agent/loop.go's runTurn tool-execution loop (the
	// toolResult.ParksTurn check, tools.ToolResult's doc comment) — the sole
	// producer of this status — for the full defect this closes (C2/ADR-057
	// UAT 2026-08-03): before it existed, a successful park still left the
	// in-memory turn loop blind to its own session's lifecycle transition,
	// so the turn kept running additional LLM iterations/tool calls past the
	// park, eventually overwriting the durable needs_input record before any
	// `respond` could ever reach it.
	TurnEndStatusParked TurnEndStatus = "parked"
)

// TurnStartPayload describes the start of a turn.
type TurnStartPayload struct {
	Channel     string
	ChatID      string
	UserMessage string
	MediaCount  int
	// IsRoot is true when this turn has no parent (parentTurnID == "").
	// The WS forwarder uses it to reset its root-turn-ended latch so that
	// spans spawned by a NEW root turn are not spuriously armed at
	// registration (#605); only a root turn's start may reset the latch,
	// otherwise a child's own turn-start would reopen the arming hole for
	// later-arriving sibling spawn events.
	IsRoot bool
}

// TurnEndPayload describes the completion of a turn.
type TurnEndPayload struct {
	Status          TurnEndStatus
	Iterations      int
	Duration        time.Duration
	FinalContentLen int
	// ChatID is the chat session this turn belongs to.
	// Populated so the WS watchdog can scope orphan detection to the correct connection.
	ChatID string
	// SessionID is the transcript-store session ID for this turn.
	// Carried end-to-end so the WS forwarder can avoid the sessionIDs reverse-lookup.
	SessionID string
	// IsRoot is true when this turn has no parent (parentTurnID == "").
	// The orphan watchdog only arms on root turn-end to avoid spurious interrupts
	// from sibling sub-turn completions.
	IsRoot bool
}

// LLMRequestPayload describes an outbound LLM request.
type LLMRequestPayload struct {
	Model         string
	MessagesCount int
	ToolsCount    int
	MaxTokens     int
	Temperature   float64
}

// LLMResponsePayload describes an inbound LLM response.
type LLMResponsePayload struct {
	ContentLen   int
	ToolCalls    int
	HasReasoning bool
}

// LLMDeltaPayload describes a streamed LLM delta.
type LLMDeltaPayload struct {
	ContentDeltaLen   int
	ReasoningDeltaLen int
}

// LLMRetryPayload describes a retry of an LLM request.
type LLMRetryPayload struct {
	Attempt    int
	MaxRetries int
	Reason     string
	Error      string
	Backoff    time.Duration
}

// ContextCompressReason identifies why emergency compression ran.
type ContextCompressReason string

const (
	// ContextCompressReasonProactive indicates compression before the first LLM call.
	ContextCompressReasonProactive ContextCompressReason = "proactive_budget"
	// ContextCompressReasonRetry indicates compression during context-error retry handling.
	ContextCompressReasonRetry ContextCompressReason = "llm_retry"
)

// ContextCompressPayload describes a forced history compression.
type ContextCompressPayload struct {
	Reason            ContextCompressReason
	DroppedMessages   int
	RemainingMessages int
}

// ToolExecStartPayload describes a tool execution request.
//
// tool_call_start is class (a) per the ADR-057 W5 audit (FR-089, BDD-16): a
// child turn genuinely emits it, so the wire frame carries both ids. See
// SessionID and ProducingSessionID below for which is which.
type ToolExecStartPayload struct {
	ToolCallID session.ToolCallID
	ChatID     string
	// SessionID is the transcript-store session ID for this turn.
	//
	// ADR-057 FR-012 (W5/U23): the WS forwarder (pkg/gateway/websocket.go,
	// U11) stamps the outbound tool_call_start frame's wire `session_id`
	// straight from this field, so it MUST hold the ROUTING session id —
	// the id inherited verbatim from the root of the delegation subtree
	// (session.RoutingSessionID's contract) — not necessarily this turn's
	// own store-backed session when the call fires several delegation
	// levels deep. Emitting code (turn.go, U3/U9) is responsible for
	// sourcing it from the emitting turnState's routing identity. The
	// turn's own real session, when it differs, belongs in
	// ProducingSessionID below.
	SessionID string
	Tool      string
	Arguments map[string]any
	// ParentSpawnCallID is non-empty when this tool call fires inside a sub-turn.
	// It equals the parent spawn tool call's ToolCall.ID (FR-H-002).
	// The WebSocket forwarder propagates this as parent_call_id on outbound frames (FR-H-005).
	ParentSpawnCallID session.ToolCallID
	// AgentID is the agent executing this tool call.
	// FR-I-008: live tool_call_start frames must carry agent_id to match replay frame parity.
	AgentID string
	// ProducingSessionID is the real, store-backed session that actually
	// executed this tool call (ADR-057 FR-013, W5d, owned by U23) — the
	// child's own session.SessionID when the call fires inside a delegated
	// sub-turn, distinct from SessionID's routing key above. Left as the
	// zero value when this turn IS the routing session (producing ==
	// routing), so the WS forwarder can implement FR-013's "present iff it
	// differs from session_id" rule with a plain non-empty-and-unequal
	// check before stamping the wire's optional producing_session_id
	// (generated.ToolCallStartFrame.ProducingSessionId). Populated by the
	// emitting turnState (U3/U9) with its own transcriptSessionID — never by
	// this file, which defines the shape only.
	ProducingSessionID session.SessionID
}

// ToolExecEndPayload describes the outcome of a tool execution.
//
// tool_call_result is class (a) per the ADR-057 W5 audit (FR-089, BDD-16): a
// child turn genuinely emits it, so the wire frame carries both ids. See
// SessionID and ProducingSessionID below for which is which.
type ToolExecEndPayload struct {
	ToolCallID session.ToolCallID
	ChatID     string
	// SessionID is the transcript-store session ID for this turn.
	//
	// ADR-057 FR-012 (W5/U23): the WS forwarder (pkg/gateway/websocket.go,
	// U11) stamps the outbound tool_call_result frame's wire `session_id`
	// straight from this field, so it MUST hold the ROUTING session id — see
	// ToolExecStartPayload.SessionID's doc comment for the full rationale,
	// which applies identically here.
	SessionID  string
	Tool       string
	Duration   time.Duration
	ForLLMLen  int
	ForUserLen int
	IsError    bool
	Async      bool
	// Result is the tool's ForLLM content, forwarded to the browser via WebSocket
	// so rich tool UIs (e.g., browser screenshot preview) can render the result.
	Result string
	// ParentSpawnCallID is non-empty when this tool call fires inside a sub-turn.
	// It equals the parent spawn tool call's ToolCall.ID (FR-H-002).
	// The WebSocket forwarder propagates this as parent_call_id on outbound frames (FR-H-005).
	ParentSpawnCallID session.ToolCallID
	// AgentID is the agent executing this tool call.
	// FR-I-008: live tool_call_result frames must carry agent_id to match replay frame parity.
	AgentID string
	// ProducingSessionID is the real, store-backed session that actually
	// executed this tool call (ADR-057 FR-013, W5d, owned by U23) — the
	// child's own session.SessionID when the call fires inside a delegated
	// sub-turn, distinct from SessionID's routing key above. See
	// ToolExecStartPayload.ProducingSessionID's doc comment for the full
	// "present iff it differs" contract, which applies identically here.
	ProducingSessionID session.SessionID
}

// ToolExecSkippedPayload describes a skipped tool call.
type ToolExecSkippedPayload struct {
	Tool   string
	Reason string
}

// SteeringInjectedPayload describes steering messages appended before the next LLM call.
type SteeringInjectedPayload struct {
	Count           int
	TotalContentLen int
}

// FollowUpQueuedPayload describes an async follow-up queued back into the inbound bus.
type FollowUpQueuedPayload struct {
	SourceTool string
	Channel    string
	ChatID     string
	ContentLen int
}

type InterruptKind string

const (
	InterruptKindSteering InterruptKind = "steering"
	InterruptKindGraceful InterruptKind = "graceful"
	InterruptKindHard     InterruptKind = "hard_abort"
)

// InterruptReceivedPayload describes accepted turn-control input.
type InterruptReceivedPayload struct {
	Kind       InterruptKind
	Role       string
	ContentLen int
	QueueDepth int
	HintLen    int
}

// SubTurnStatus describes the terminal state of a sub-turn.
// Using a named type prevents accidental use of arbitrary strings at call sites.
// JSON marshaling is identical to a plain string.
type SubTurnStatus string

const (
	// SubTurnStatusSuccess indicates the sub-turn completed normally.
	SubTurnStatusSuccess SubTurnStatus = "success"
	// SubTurnStatusError indicates the sub-turn ended with an error.
	SubTurnStatusError SubTurnStatus = "error"
	// SubTurnStatusCancelled indicates THIS sub-turn's own cancel was
	// explicitly claimed — i.e. RequestCancel targeted the sub-turn
	// directly (childTS.cancelFired == true when its context was
	// canceled), not merely inherited via a parent's hard-abort cascade.
	// Reachable, if narrow (FIX 4, 7-reviewer-gate follow-up on the Wave 3
	// fix pass — see spawnSubTurn's cleanup defer, pkg/agent/subturn.go):
	// a Critical:true sub-turn survives a graceful parent finish by design
	// (SubTurnConfig.Critical) and keeps running under its own session ID;
	// a later RequestCancel against that same session (GetActiveTurnHookForSession's
	// fallback match, pkg/agent/turn.go) can find and cancel the sub-turn
	// itself once its parent has already finished. Distinct from
	// SubTurnStatusInterrupted below, which covers the cascade case
	// (childTS.cancelFired stays false there — the parent's Finish(true)
	// cascades via Finish(true) directly on children, bypassing
	// ClaimCancel entirely).
	//
	//nolint:misspell // wire value "cancelled" matches frontend TS union in src/store/chat.ts, src/lib/ws.ts
	SubTurnStatusCancelled SubTurnStatus = "cancelled"
	// SubTurnStatusInterrupted indicates the sub-turn was interrupted by its
	// parent's hard-abort cascade (the common case — see spawnSubTurn's
	// cleanup defer for the childCtx.Err()==context.Canceled check). The
	// wire contract's SubagentEndFrame.reason field (surfaced from
	// SubTurnEndPayload.Reason) is populated only for this status.
	SubTurnStatusInterrupted SubTurnStatus = "interrupted"
	// SubTurnStatusTimeout indicates the sub-turn exceeded its configured
	// timeout. Deliberately routed to SubTurnStatusError in practice, not
	// this value — spawnSubTurn's cleanup defer distinguishes an external
	// cancel (context.Canceled) from every other error case, including a
	// genuine context.DeadlineExceeded from the sub-turn's own Timeout
	// config expiring, which falls through to SubTurnStatusError (a real
	// failure, not a cancellation, from the sub-turn's own point of view).
	// This value remains declared for wire-contract completeness (the
	// SPA's SUBAGENT_END_STATUSES validation set already includes it) and
	// as a documented, intentional design choice, not an oversight.
	SubTurnStatusTimeout SubTurnStatus = "timeout"
	// SubTurnStatusParked indicates the sub-turn stopped because a
	// message_parent(kind="question", wait=true) call parked its own
	// session in needs_input (ADR-057 UAT defect C2 fix) — mirrors
	// TurnEndStatusParked (this file, above), which spawnSubTurn's
	// endStatus switch (pkg/agent/subturn.go) checks lastTurnStatus against
	// to set this value. Named identically to TurnEndStatusParked's wire
	// value ("parked") end-to-end — turn status, this SubTurnStatus, and
	// the SubagentEndFrame.status wire enum all use the same literal — so
	// no per-layer translation is needed. Deliberately NOT named
	// "needs_input" to match the durable session-lifecycle state
	// (session.LifecycleNeedsInput): that state is long-lived and outlives
	// THIS span (a later `delegate respond` runs a fresh sub-turn with its
	// own new span_id, not a continuation of this one), whereas this value
	// describes only this one-shot span's own terminal outcome. Not an
	// error, not a success, not a cancellation, not a timeout — the SPA
	// must render it as a distinct "paused" state, not fall through to any
	// of those (see src/lib/toolStatusConfig.tsx's getSpanStatusDot).
	SubTurnStatusParked SubTurnStatus = "parked"
)

// SubTurnSpawnPayload describes the creation of a child turn.
// FR-H-004: carries span_id, parent_call_id, task_label, agent_id for the WS forwarder.
type SubTurnSpawnPayload struct {
	AgentID      string
	Label        string
	ParentTurnID string
	// SpanID is "span_" + ParentSpawnCallID (deterministic, derivable from persisted data).
	SpanID string
	// ParentSpawnCallID is the ToolCall.ID of the spawn tool call that triggered this sub-turn.
	// This is the correlation anchor for the subagent span.
	ParentSpawnCallID session.ToolCallID
	// TaskLabel is the human-readable label for the sub-turn task (from spawn tool's label param).
	TaskLabel string
	// ChatID is needed so the WS forwarder can route this event to the right connection.
	ChatID string
	// SessionID is the ROUTING session id (ADR-057 FR-011/FR-017), NOT this
	// child turn's own transcript session.
	//
	// FROZEN CONTRACT (ADR-057 Rule 7, this field owned by U23 — do not
	// "tidy" it to the child): sourced from the PARENT's turnState — today
	// parentTS.transcriptSessionID at pkg/agent/subturn.go:1183 (U7); once
	// U3's turn.go role split (W4) lands, that becomes
	// parentTS.routingSessionID, still parent-scoped. subagent_start is
	// class (b) per the W5 audit (FR-089, BDD-98): emitted by the PARENT
	// about the child, so producing_session_id would always equal this
	// field and is therefore always absent (FR-013's "iff it differs") —
	// no ProducingSessionID sibling exists on this payload for that reason.
	// The child's own identity already rides this same payload as Label
	// (set to childID at the spawn call site) and SpanID/ParentSpawnCallID.
	// Repointing SessionID to the child here would split a delegation's
	// span from its own steps in the SPA's frame bucketing
	// (src/store/chat.ts, grill #2 finding C-2) on the live connection, on
	// the FIRST delegation — not merely after a reload.
	SessionID string
}

// SubTurnEndPayload describes the completion of a child turn.
// FR-H-004: carries span_id, status, duration_ms for the WS forwarder.
type SubTurnEndPayload struct {
	AgentID string
	Status  SubTurnStatus
	// SpanID is "span_" + ParentSpawnCallID, matching the corresponding SubTurnSpawnPayload.
	SpanID string
	// ParentSpawnCallID is the ToolCall.ID of the spawn tool call that triggered this sub-turn.
	ParentSpawnCallID session.ToolCallID
	// DurationMS is the wall-clock duration of the sub-turn in milliseconds.
	DurationMS int64
	// ChatID is needed so the WS forwarder can route this event to the right connection.
	ChatID string
	// SessionID is the ROUTING session id (ADR-057 FR-011/FR-017), NOT this
	// child turn's own transcript session — sourced from the PARENT's
	// turnState, today parentTS.transcriptSessionID at
	// pkg/agent/subturn.go:1424 (U7). See SubTurnSpawnPayload.SessionID's
	// doc comment for the full frozen-contract rationale (ADR-057 Rule 7,
	// owned by U23), which applies identically here: subagent_end is class
	// (b) (FR-089, BDD-98), so producing_session_id would always equal this
	// field and is therefore always absent — do not repoint to the child.
	SessionID string
	// Reason is populated ONLY when Status == SubTurnStatusInterrupted (FIX 4,
	// 7-reviewer-gate follow-up on the Wave 3 fix pass), mirroring the wire
	// contract's SubagentEndFrame.reason field ("why the sub-turn was
	//nolint:misspell // documents the literal wire enum value, matches frontend TS union
	// interrupted by the parent" — parent_timeout | parent_cancelled |
	// parent_done_early | unknown). spawnSubTurn's cleanup defer sets this
	// from the cheapest honest signal available at that point
	// (parentTS.cancelFired) — see its doc comment for the deliberate
	// coarseness (this does NOT yet distinguish a live user cancel from a
	// scheduled run's deadline force-abort, both of which reach
	// parentTS.cancelFired via the same RequestCancel path; a finer split
	// would require threading the canceller identity through turnState,
	// out of scope for this fix). Empty for every other Status value.
	Reason string
}

// SubTurnResultDeliveredPayload describes delivery of a sub-turn result.
type SubTurnResultDeliveredPayload struct {
	TargetChannel string
	TargetChatID  string
	ContentLen    int
}

// SubTurnOrphanPayload describes a sub-turn result that could not be delivered.
type SubTurnOrphanPayload struct {
	ParentTurnID string
	ChildTurnID  string
	Reason       string
}

// ErrorPayload describes an execution error inside the agent loop.
//
// ProviderError (ADR-051 §RD5 CRIT-001) carries the structured
// *ProviderError — status + body + wrapped error — so the classifier at
// the two choke points (appendErrorTranscript write + WS-forwarder
// EventKindError live) sees real provider data instead of a stringified
// message. Optional: non-provider error paths (hook aborts, internal
// model_switch failures, rate-limit denials) leave it nil and the
// classifier falls back to substring matching on Message.
//
// Detail is computed live at the forwarder (NEVER persisted); only the
// WS path surfaces it, behind Verbose Chat (operator Q2).
type ErrorPayload struct {
	Stage         string
	Code          string
	Message       string
	ProviderError *ProviderError
	// ChatID is needed so the WS event forwarder can route this event to the
	// originating connection via matchesChatID.
	ChatID string
	// SessionID is the routing session id (ADR-057). matchesEvent falls back
	// on this so a second tab or a reload attached to the same session still
	// receives the typed error. ChatID alone dies when ServeHTTP mints a new
	// webchat: uuid per connection. Not a wire-frame field — ErrorFrame is
	// .strict() and already has its own session_id.
	SessionID string
}

// TurnTimeoutPayload describes a turn that exceeded its configured timeout.
type TurnTimeoutPayload struct {
	TimeoutSeconds int
	Compacted      bool
	Retried        bool
}

// EmptyResponseRetryPayload describes a retry triggered by an empty LLM response.
type EmptyResponseRetryPayload struct {
	Attempt    int
	MaxRetries int
}

// CompactionRetryPayload describes context compaction triggered during a timeout recovery.
type CompactionRetryPayload struct {
	DroppedMessages   int
	RemainingMessages int
}

// BackgroundProcessKillPayload describes a background process that was force-killed.
type BackgroundProcessKillPayload struct {
	PID             int
	MaxSeconds      int
	TerminatedClean bool
}

// RateLimitPayload describes a rate-limit denial for an LLM or tool call (SEC-26).
// ChatID routes the frame to the originating connection. SessionID is the
// fallback so a second tab or a reload attached to the same session still
// receives the denial after ServeHTTP mints a new webchat: uuid.
type RateLimitPayload struct {
	Scope             string  `json:"scope"`
	Resource          string  `json:"resource"` // "llm_call" or "tool_call"
	PolicyRule        string  `json:"policy_rule"`
	RetryAfterSeconds float64 `json:"retry_after_seconds"`
	AgentID           string  `json:"agent_id,omitempty"`
	ChatID            string  `json:"chat_id,omitempty"`
	// SessionID is the routing session id (ADR-057). matchesEvent falls
	// back on this so a second tab or a reload still receives the
	// dedicated rate_limit frame. ChatID alone dies when ServeHTTP mints
	// a new webchat: uuid per connection. Not a new wire field —
	// RateLimitFrame already has session_id.
	SessionID string `json:"session_id,omitempty"`
	Tool      string `json:"tool,omitempty"`
}

// NotificationAdminBroadcast is the sentinel Recipient value used when a
// notification could not be routed to a specific user and must reach every
// connected client instead (single-user model — no role distinction; W-7
// fallback). The constant name is kept for historical/call-site continuity.
const NotificationAdminBroadcast = "*admin*"

// NotificationPayload carries a user-facing notification for the live WS push
// (#264). It is delivered ONLY to connections whose userID equals Recipient
// (or, when Recipient == NotificationAdminBroadcast, to every connected
// client — single-user model, no role distinction).
type NotificationPayload struct {
	// Recipient is the username the notification is for, or
	// NotificationAdminBroadcast to fan out to every connected client.
	Recipient        string `json:"recipient"`
	ID               string `json:"id"`
	NotificationType string `json:"notification_type"`
	Title            string `json:"title"`
	Body             string `json:"body,omitempty"`
	Severity         string `json:"severity"`
	Read             bool   `json:"read"`
	CreatedAtMs      int64  `json:"created_at_ms"`
	ScheduleID       string `json:"schedule_id,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
}

// WhatsAppPairingPayload carries a WhatsApp native/QR linked-device pairing
// update for the SPA (#283). QR is populated only when
// Status == channels.PairingStatusCode.
type WhatsAppPairingPayload struct {
	ChannelID string                 `json:"channel_id"`
	Status    channels.PairingStatus `json:"status"`
	QR        string                 `json:"qr,omitempty"`
	Message   string                 `json:"message,omitempty"`
}

// TaskStatusChangedPayload carries a workflow task status transition for the
// SPA. The WS forwarder turns this into a task_status_changed frame so the SPA
// invalidates its tasks TanStack Query cache. SessionID is the task's session
// (falls back to "task:<id>" when no session has been created yet so the
// contract-required session_id field is always populated).
type TaskStatusChangedPayload struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id,omitempty"`
}

// PlanStatusChangedPayload carries a Plan status/phase/progress transition for
// the SPA (ADR-049 D4/D7, spec Part B R3). The WS forwarder turns this into a
// plan_status frame (generated.PlanStatusFrame, canonical type literal
// "plan_status" per Round-1 Grill Reconciliation R3). Not session-scoped — a
// Plan is a standalone workspace-scoped entity. Emitted via
// AgentLoop.EmitPlanStatusChanged, which pkg/plan's Store.OnChange hook calls
// after every successful Create/Update so both the plan engine's internal
// mutations (dispatch/judge-round/idle-sweep/pause-resume) and the gateway's
// REST-driven mutations (approve/stop/edit) emit through the same path.
type PlanStatusChangedPayload struct {
	PlanID       string  `json:"plan_id"`
	State        string  `json:"state"`
	PlanPhase    string  `json:"plan_phase"`
	Progress     float64 `json:"progress"`
	PausedReason string  `json:"paused_reason,omitempty"`
}

// GoalStatusChangedPayload carries a session's `/goal` loop status for the
// SPA (ADR-049 D6/D7, spec Part B US-8). The WS forwarder turns this into a
// goal_status frame (generated.GoalStatusFrame). Session-scoped — SessionID
// is the transcript session the goal belongs to.
type GoalStatusChangedPayload struct {
	SessionID string `json:"session_id"`
	// GoalID is the stable per-generation goal identifier (ADR-053 R§8.11,
	// UAT S3 fix) — see session.SessionMeta.GoalID's doc comment. Empty for a
	// legacy pre-upgrade goal that never had one minted; the WS forwarder
	// (pkg/gateway/websocket.go) omits GoalStatusFrame.GoalId in that case
	// (it is OPTIONAL on the wire).
	GoalID       string `json:"goal_id,omitempty"`
	Condition    string `json:"condition"`
	Round        int    `json:"round"`
	MaxRounds    int    `json:"max_rounds"`
	LatestReason string `json:"latest_reason"`
	ActiveLoops  int    `json:"active_loops"`
	Cap          int    `json:"cap"`
	State        string `json:"state"`
	// Criteria (ADR-074 D5.2 / judgment-first FR-011) is the compiled
	// acceptance-criteria breakdown behind Condition, carried on the
	// `queued` (pending-confirm) emission so the SPA's confirmation card
	// itemizes exactly what will run (commands verbatim). Empty on
	// round/lifecycle pushes. The WS forwarder maps it onto the wire
	// frame's optional `criteria` field.
	Criteria []task.AcceptanceCriterion `json:"criteria,omitempty"`
}

// LoopStatusChangedPayload carries a session's `/loop` status for the SPA
// (ADR-049 D6/D7, spec Part B US-9). The WS forwarder turns this into a
// loop_status frame (generated.LoopStatusFrame). Session-scoped.
type LoopStatusChangedPayload struct {
	SessionID string `json:"session_id"`
	Mode      string `json:"mode"`
	Run       int    `json:"run"`
	MaxRuns   int    `json:"max_runs"`
	NextDelay *int   `json:"next_delay,omitempty"`
	State     string `json:"state"`
}

// TaskRunStatusPayload carries a per-execution TaskRun open/close transition
// (ADR-050 §3.8, docs/internal/specs/task-run-history-spec.md §3.8) for the
// SPA. The WS forwarder turns this into a task_run_status frame
// (generated.TaskRunStatusFrame) so the calendar's per-occurrence chip can
// update live without a full occurrences refetch — additive alongside
// TaskStatusChangedPayload, never a replacement for it (RD2: Task.status
// keeps its exact existing behavior and event).
//
// OccurrenceMs mirrors task.TaskRun.OccurrenceMs's own nullability: nil for
// an ad-hoc/once/manual run, non-nil for the RRULE instant a recurring fire
// realizes. Status is one of task.StatusInProgress/StatusDone/StatusFailed/
// task.StatusSkipped (task.IsValidRunStatus) — the narrower 4-state TaskRun
// vocabulary, not the full 7-state Task one. StatusSkipped is emitted by
// TaskTriggerScheduler (task_trigger.go's RunScheduled, via the same
// TaskExecutor.emitRunStatus function OpenRun/CloseRun already use) when the
// overlap guard records a skipped-occurrence run, not just by OpenRun/
// CloseRun's in_progress/done/failed transitions.
type TaskRunStatusPayload struct {
	TaskID       string `json:"task_id"`
	RunID        string `json:"run_id"`
	OccurrenceMs *int64 `json:"occurrence_ms,omitempty"`
	Status       string `json:"status"`
}

// ToolResultProjectionPayload is EventKindToolResultProjection's payload
// (ADR-066 D5 / FR-022). SessionID and ProducingSessionID follow the
// ToolExecEndPayload contract exactly (routing id on the wire's session_id;
// the child's own session only when it differs — u9ToolExecSessionIDs).
type ToolResultProjectionPayload struct {
	ChatID             string
	SessionID          string
	ProducingSessionID session.SessionID
	ToolCallID         session.ToolCallID
	// ArchiveLine is the zero-based archive line of the projected result;
	// with ToolCallID it is the projection-state key (FR-019).
	ArchiveLine int
	// ContentState is "emptied" (the only state the pass produces today;
	// the wire enum also admits "capped").
	ContentState string
	// Mark is the recall mark now standing in for the content.
	Mark string
	// AgentID is the agent whose window was emptied.
	AgentID string
}
