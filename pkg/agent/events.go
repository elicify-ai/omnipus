package agent

import (
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/session"
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
	// EventKindSessionSummarize is emitted when asynchronous summarization completes.
	EventKindSessionSummarize
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
	"session_summarize",
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
)

// TurnStartPayload describes the start of a turn.
type TurnStartPayload struct {
	Channel     string
	ChatID      string
	UserMessage string
	MediaCount  int
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

// SessionSummarizePayload describes a completed async session summarization.
type SessionSummarizePayload struct {
	SummarizedMessages int
	KeptMessages       int
	SummaryLen         int
	OmittedOversized   bool
}

// ToolExecStartPayload describes a tool execution request.
type ToolExecStartPayload struct {
	ToolCallID session.ToolCallID
	ChatID     string
	// SessionID is the transcript-store session ID for this turn.
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
}

// ToolExecEndPayload describes the outcome of a tool execution.
type ToolExecEndPayload struct {
	ToolCallID session.ToolCallID
	ChatID     string
	// SessionID is the transcript-store session ID for this turn.
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
	// SubTurnStatusCancelled indicates the sub-turn was explicitly canceled by the user.
	//
	//nolint:misspell // wire value "cancelled" matches frontend TS union in src/store/chat.ts, src/lib/ws.ts
	SubTurnStatusCancelled SubTurnStatus = "cancelled"
	// SubTurnStatusInterrupted indicates the sub-turn was interrupted by its parent.
	SubTurnStatusInterrupted SubTurnStatus = "interrupted"
	// SubTurnStatusTimeout indicates the sub-turn exceeded its configured timeout.
	SubTurnStatusTimeout SubTurnStatus = "timeout"
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
	// SessionID is the transcript-store session ID for this turn.
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
	// SessionID is the transcript-store session ID for this turn.
	SessionID string
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
type ErrorPayload struct {
	Stage   string
	Message string
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
// ChatID is required so the WebSocket event forwarder can route the frame to
// the correct connection via matchesChatID — a rate-limit denial is meaningless
// without the chat context it applies to.
type RateLimitPayload struct {
	Scope             string  `json:"scope"`
	Resource          string  `json:"resource"` // "llm_call" or "tool_call"
	PolicyRule        string  `json:"policy_rule"`
	RetryAfterSeconds float64 `json:"retry_after_seconds"`
	AgentID           string  `json:"agent_id,omitempty"`
	ChatID            string  `json:"chat_id,omitempty"`
	Tool              string  `json:"tool,omitempty"`
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
