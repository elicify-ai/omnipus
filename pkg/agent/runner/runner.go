// Package runner defines the ExternalAgentRunner interface — the bidirectional,
// consent-routed, resumable contract for driving external CLI agent processes
// (Claude Code, Codex, opencode) from inside Omnipus.
//
// Architecture (Spec-4, FR-5.1, M-1, M-2):
//
//   - Events flow OUT from the external process to Omnipus via the Events() channel.
//   - Control flows IN from Omnipus to the process via Decide, Cancel, and Input.
//   - Permission requests (PermissionRequestEvent) route to the consent/policy layer;
//     deny-by-default when no handler is registered (FR-5.1, US-2 edge).
//   - Runs are resumable via a stable RunID (FR-5.1, US-3).
//   - The interface is the INVERSE direction of hook_process (child emits unsolicited
//     events; Omnipus answers) — correlation reusable, direction new (M-1).
//
// U2 wires the real CLI drivers. This package ships the interface shape + a fake
// driver for tests.
package runner

import (
	"context"
	"time"
)

// EventKind is a typed string discriminator for RunEvent.
type EventKind string

const (
	// EventKindOutput is a text output chunk from the external agent.
	EventKindOutput EventKind = "output"
	// EventKindToolCall is a tool invocation emitted by the external agent.
	EventKindToolCall EventKind = "tool-call"
	// EventKindDiff is a file-diff chunk emitted by the external agent.
	EventKindDiff EventKind = "diff"
	// EventKindPermissionRequest is a permission prompt the external agent raised.
	// It MUST be routed to the consent layer; deny-by-default when no handler wired.
	EventKindPermissionRequest EventKind = "permission-request"
	// EventKindEnd signals that the run completed successfully.
	EventKindEnd EventKind = "end"
	// EventKindError signals that the run failed. The run is terminated after this event.
	EventKindError EventKind = "error"
	// EventKindToolResult signals that an external tool call produced a result.
	EventKindToolResult EventKind = "tool-result"
)

// RunEvent is a single structured event emitted by an external agent run.
// Exactly one of Output, ToolCall, Diff, PermissionRequest, or Err will be non-zero,
// determined by Kind.
type RunEvent struct {
	// Kind discriminates the event type.
	Kind EventKind
	// RunID is the stable identifier of the run that emitted this event.
	RunID string
	// Timestamp is when the event was produced (UTC).
	Timestamp time.Time

	// Output holds text output content. Set when Kind=EventKindOutput.
	Output *OutputEvent
	// ToolCall holds a tool invocation. Set when Kind=EventKindToolCall.
	ToolCall *ToolCallEvent
	// Diff holds a file diff. Set when Kind=EventKindDiff.
	Diff *DiffEvent
	// PermissionRequest holds a consent prompt. Set when Kind=EventKindPermissionRequest.
	PermissionRequest *PermissionRequestEvent
	// Err holds an error description. Set when Kind=EventKindError.
	Err *ErrorEvent
	// ToolResult holds a completed external tool result. Set when Kind=EventKindToolResult.
	ToolResult *ToolResultEvent
}

// ToolResultEvent carries the completion of a tool call emitted by an external agent.
type ToolResultEvent struct {
	// CallID is the correlation ID matching the pending ToolCallEvent.CallID.
	CallID string
	// ToolName is the name of the tool that produced the result.
	ToolName string
	// Output is the raw JSON-encoded result output.
	Output []byte
	// IsError is true when the tool itself reported an error.
	IsError bool
}

// OutputEvent carries a text output chunk from the external agent.
type OutputEvent struct {
	Text string
}

// ToolCallEvent carries a tool invocation emitted by the external agent.
type ToolCallEvent struct {
	// ToolName is the name of the tool being invoked.
	ToolName string
	// ToolInput is the raw JSON-encoded input arguments.
	ToolInput []byte
	// CallID is a per-call correlation identifier (may be empty for agents that
	// do not emit stable call IDs).
	CallID string
}

// DiffEvent carries a file diff produced by the external agent.
type DiffEvent struct {
	// Path is the file path that was modified.
	Path string
	// Diff is the unified-diff representation of the change.
	Diff string
}

// PermissionRequestEvent carries a permission prompt emitted by the external agent.
// It must be routed to the consent layer via Decide; deny-by-default when no
// consent handler is registered.
type PermissionRequestEvent struct {
	// RequestID is a stable identifier for this permission prompt. Used in Decide.
	RequestID string
	// ToolName is the tool whose invocation is being gated.
	ToolName string
	// Description is a human-readable explanation of what the agent wants to do.
	Description string
	// RawInput is the raw JSON-encoded tool input that triggered the prompt.
	RawInput []byte
}

// ErrorEvent carries an error description from the external agent or the driver.
type ErrorEvent struct {
	// Message is the error description.
	Message string
	// Fatal is true when the run cannot continue after this error.
	Fatal bool
}

// PermissionDecision is the verdict returned to the external agent for a
// PermissionRequestEvent.
type PermissionDecision struct {
	// RequestID must match the PermissionRequestEvent.RequestID being decided.
	RequestID string
	// Allow is true to permit the tool call; false to deny it.
	Allow bool
	// Reason is an optional human-readable explanation of the decision.
	Reason string
}

// RunOptions configures an external agent run.
type RunOptions struct {
	// RunID is the stable identifier for this run. When non-empty, the driver
	// SHOULD resume the run if a prior session with this ID exists (e.g. via
	// `claude --resume <session-id>`). When empty, a new run is started.
	RunID string
	// WorkDir is the working directory for the external agent process.
	// This SHOULD point to a git worktree or isolated temporary directory
	// (per FR-5.3 — worktree isolation).
	WorkDir string
	// Input is the initial prompt/task text delivered to the external agent.
	Input string
	// Env is the environment variable set for the external process. The driver
	// merges this with its own required variables and the platform env allowlist.
	Env []string
	// TimeoutSeconds is the maximum wall-clock duration for this run.
	// Zero means the driver's built-in default applies.
	TimeoutSeconds int
	// MaxTurns is the maximum number of agentic turns the external agent may
	// take. Zero means the driver's built-in default applies.
	MaxTurns int
}

// ConnectionTestResult holds the outcome of an ExternalAgentRunner.Test call.
type ConnectionTestResult struct {
	// OK is true when the binary is present, authenticated, and the JSON handshake
	// succeeded without running real work.
	OK bool
	// Message is a human-readable description of the result (success or failure reason).
	Message string
	// CLIVersion is the detected CLI version string, if the handshake succeeded.
	CLIVersion string
	// Reason classifies a failure (missing-binary / handshake-failed /
	// unauthenticated / unknown-cli). Empty (ReasonOK) when OK is true. Lets callers
	// branch on the failure category without parsing Message. Set by TestConnection.
	Reason FailureReason
}

// ExternalAgentRunner is the bidirectional, consent-routed, resumable interface
// for driving an external CLI agent (Claude Code, Codex, opencode) as a sub-agent
// worker inside Omnipus.
//
// Contract (Spec-4 FR-5.1):
//
//  1. Events flow OUT via the channel returned by Run (or returned via Resume).
//     The channel is closed when the run ends (EventKindEnd or EventKindError).
//  2. Permission requests (EventKindPermissionRequest) MUST be routed to the
//     consent layer; the caller uses Decide to route the verdict back.
//     Deny-by-default when no consent handler is registered.
//  3. Runs are resumable: if RunOptions.RunID is set and a prior session exists,
//     the driver SHOULD resume it.
//  4. Cancel terminates the external process and closes the Events channel.
//  5. Input sends additional text to a running interactive agent (best-effort;
//     not all CLIs support mid-run input injection).
//  6. Test validates the runner configuration without running real work.
//
// Implementors: the U2 CLI drivers (claude-code, codex, opencode). The FakeRunner
// in this package is for tests only.
type ExternalAgentRunner interface {
	// Run starts the external agent with the given options and returns a read-only
	// channel of RunEvents. The channel is closed when the run ends (either
	// EventKindEnd or EventKindError has been sent). The context governs the run's
	// lifetime; cancelling it is equivalent to calling Cancel.
	//
	// An EventKindPermissionRequest event MUST be answered via Decide before the
	// run can continue; the driver blocks the run until a decision arrives or a
	// deny-by-default timeout elapses.
	Run(ctx context.Context, opts RunOptions) (<-chan RunEvent, error)

	// Decide routes a PermissionDecision back to the external agent process in
	// response to a PermissionRequestEvent. It is a no-op if the RequestID does
	// not match any pending request.
	Decide(decision PermissionDecision)

	// Cancel terminates the external agent process immediately. It is idempotent.
	// After Cancel, the Events channel returned by Run is closed.
	Cancel()

	// Input sends additional user input text to the running agent (best-effort).
	// Implementations that do not support mid-run input injection return nil without
	// error (the input is silently discarded).
	Input(text string) error

	// Resume starts (or re-attaches to) an existing run by its stable RunID.
	// If no prior session with that ID exists, the behaviour is driver-defined
	// (most drivers start a fresh run).
	Resume(ctx context.Context, runID string) (<-chan RunEvent, error)

	// Test validates the runner configuration without running real work.
	// It checks: (1) the CLI binary is present on PATH; (2) it is authenticated;
	// (3) a minimal JSON handshake succeeds.
	Test(ctx context.Context) ConnectionTestResult
}
