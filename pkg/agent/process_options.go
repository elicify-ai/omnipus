// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// processOptions is the per-turn option carrier for runAgentLoop.
// Split verbatim out of loop.go (2026-09-27, message_parent
// delegate-session-id wiring): loop.go is pinned at its exact line count
// in scripts/budgets/files.txt (shrink-only), so new fields are added here
// rather than grown into the pinned file — "do not add to one, extract
// first" (founder size-budget ruling).

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey        string // Session identifier for history/context
	Channel           string // Target channel for tool execution
	ChatID            string // Target chat ID for tool execution
	SenderID          string // Current sender ID for dynamic context
	SenderDisplayName string // Current sender display name for dynamic context
	// UserID is the authenticated gateway principal that initiated this turn,
	// threaded from the WS connection (websocket.go wc.userID) via the dedicated
	// bus.InboundMessage.GatewayUserID carrier (FR-017). It is stamped onto
	// turn-scoped audit.Entry.User so CLI runs (principal "cli") and admin browser
	// sessions are attributable. Empty for channel-originated turns (the platform
	// sender in Sender.Username is not a gateway principal and is never read here)
	// and unauthenticated env-token / dev-bypass paths — never guessed.
	UserID                  string              // Authenticated gateway principal (FR-017)
	UserMessage             string              // User message content (may include prefix)
	ForcedSkills            []string            // Skills explicitly requested for this message
	Media                   []string            // media:// refs from inbound message
	InitialSteeringMessages []providers.Message // Steering messages from refactor/agent
	// InitialSteeringCorrelationIDs (issue #870) is the parallel correlation-id
	// slice for InitialSteeringMessages — index i's id belongs to message i,
	// "" where none. Carried separately rather than folded into
	// providers.Message: that struct is the literal LLM provider request
	// wire shape, never a receipt-bookkeeping carrier.
	InitialSteeringCorrelationIDs []string
	DefaultResponse               string                // Response when LLM returns empty
	SendResponse                  bool                  // Whether to send response via bus
	SuppressToolFeedback          bool                  // Whether to suppress inline tool call and result feedback
	NoHistory                     bool                  // If true, don't load session history (for heartbeat)
	SkipInitialSteeringPoll       bool                  // If true, skip the steering poll at loop start (used by Continue)
	TranscriptSessionID           string                // Session ID for transcript tool call recording (empty = disabled)
	TranscriptStore               *session.UnifiedStore // Store for transcript tool call recording (nil = disabled)
	// OriginKind identifies the durable execution origin when this turn does
	// not have a lifecycle record to supply it. The zero value is an ordinary
	// interactive turn. Publication policy resolves the record first.
	OriginKind session.OriginKind

	// WorkspaceID is the Spec-1 Workspace identifier for this turn.
	// When set, the memory store uses the shared workspace room
	// ($OMNIPUS_HOME/workspaces/<id>/.omnipus/) for memories scoped to "shared".
	// Empty means no workspace is associated (private room only).
	WorkspaceID string

	// AutoDenyAsk, when true, makes every `ask`-policy tool call auto-DENIED
	// without ever requesting human approval (issue #264, FR-009). Scheduled
	// runs are headless — there is no operator to approve, so blocking on an
	// approval prompt would stall the run forever. Only ProcessScheduled sets
	// this; interactive paths leave it false so `ask` keeps prompting.
	AutoDenyAsk bool

	// Metadata carries the inbound message metadata (bus.InboundMessage.Metadata)
	// through to the turn flow. The agent loop reads Metadata["model_name"] to
	// detect a per-thread model switch (FR-011) and apply switch-time
	// compress before the next LLM call.
	Metadata map[string]string

	// InitialDelegationDepth seeds the root turnState depth for a task run. A
	// task created from within another task run carries a non-zero generation
	// (task.Task.DelegationDepth); processTaskDirect seeds it here so the
	// per-workspace delegation-graph edge's depth gate (currentDelegationDepth)
	// trips on onward await/background delegation even though a task run
	// otherwise starts a fresh turn at depth 0. Interactive/chat turns leave
	// this 0.
	InitialDelegationDepth int

	// IsTaskRun marks a turn as a native task-dispatch run (set by
	// processTaskDirect's runAgentLoop call; false for interactive chat,
	// heartbeat, and the external-CLI task path, which never reaches
	// assembleMessages at all). assembleMessages reads it to decide whether to
	// append a terse TASK_STATUS/TASK_SUMMARY marker reminder to the
	// breadcrumb block (review B3): a task's marker instruction lives only in
	// its first user turn (buildPrompt, task_executor.go), and windowTrim
	// (ADR-028) can evict that turn on a long, tool-heavy task run — this flag
	// is what lets the reminder re-surface exactly when (and only when) that
	// eviction has actually happened, piggybacking on the breadcrumb's own
	// eviction-survives-everything delivery mechanism rather than adding a
	// second, parallel injection path.
	IsTaskRun bool

	// RunningTaskID names the unified task this turn is executing (founder
	// decision 2026-09-14: last-activity on task cards). processTaskDirect
	// sets it from the tools.WithRunningTaskID context the task executor
	// already stamps on the run; AgentLoop.TaskLiveLastActivity matches on
	// it to expose the turn's live progress stamp for exactly this task.
	// Empty for every non-task turn.
	RunningTaskID string

	// UserInitiated threads bus.InboundMessage.UserInitiated into the turn
	// (ADR-049 Gap #8/r2, spec Part B FR-075/SD-B6/R6) — see that field's doc
	// comment for the fail-closed origin contract. handleCommand reads this
	// (never msg.UserInitiated directly, for the same "read the dedicated
	// processOptions carrier, not the raw inbound field" discipline
	// UserID/gatewayPrincipal already establishes) to decide whether /goal
	// and /loop action or pass through inert as ordinary text. Every
	// processOptions literal NOT built from userInitiated(msg) — ProcessScheduled,
	// processTaskDirect, processTaskDirectExternalCLI, processSystemMessage —
	// leaves this at its zero value (false), which is the correct fail-closed
	// answer for every one of those non-user origins. Pre-ADR-091, the
	// deleted spawnSubTurn did too, for a delegated child. ADR-091 fix lane
	// RX-SUBTURN finding (comment-only; code unchanged): today's replacement
	// entry point, steer_reconstruct.go::reconstructSteeredTurn, instead sets
	// `UserInitiated: wake == nil` — TRUE for a steered session's first turn
	// (delegate child, task child, etc.), by its own doc comment's
	// deliberate design ("mark it as user-originated for the session-owned
	// goal loop"). Whether that is an intentional broadening of this field's
	// fail-closed contract for a launched child, or an unreviewed departure
	// from the invariant this comment states, needs a team check — flagged,
	// not resolved here.
	UserInitiated bool

	// SteeredSessionID is the session's own LifecycleRecord.SessionID when
	// this turn belongs to a steered/delegated session, "" otherwise — the
	// delegate-session-id context carrier's source (registerTurnContext
	// stamps it via tools.WithDelegateSessionID). Set only by
	// steer_reconstruct.go::reconstructSteeredTurn, gated on rec.SteeredBy
	// != nil (ADR-093 D3's standing-root test); every other turn — root
	// chat, heartbeat, scheduled, task — leaves it "" and stays unstamped.
	SteeredSessionID string
}
