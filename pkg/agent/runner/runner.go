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
	"errors"
	"fmt"
	"time"
)

// EventKind is a typed string discriminator for RunEvent.
type EventKind string

const (
	// EventKindStart signals that a run has begun. It is the first event
	// emitted on the Run channel and carries the detected CLI version
	// (FR-5.6 / N3) so the run log and SPA can pin the stream schema.
	EventKindStart EventKind = "start"
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

	// Start holds run-start metadata (CLI + pinned version). Set when Kind=EventKindStart.
	Start *StartEvent
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

// StartEvent carries run-start metadata emitted as the first event of every
// external-CLI run (FR-5.6 / N3). It pins the detected CLI version so the run
// log and SPA can record which stream schema the run used, and whether that
// version is recognized by the driver (graceful-degradation signal).
type StartEvent struct {
	// CLI is the external CLI binary name (e.g. "claude", "codex", "opencode").
	CLI string
	// Version is the detected CLI version string (parsed from `--version`).
	// Empty when the version could not be detected — the driver still proceeds
	// with graceful degradation (FR-5.6).
	Version string
	// VersionKnown is true when the detected version matches a prefix the
	// driver has been validated against. False means the driver will proceed
	// but the JSON stream schema may drift; the SPA SHOULD surface a warning.
	VersionKnown bool
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
	// WorkDir is the working directory for the external agent process. Per
	// ADR-032 this is the delegate agent's own workspace directory (not an
	// isolated git-worktree/temp-dir copy) — see
	// docs/internal/architecture/ADR-032-external-agent-workspace-execution.md.
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
	// take — the agent's resolved tool-iteration limit (1-1000), passed by the
	// dispatch site (#904 D4). It is REQUIRED: there is no built-in default
	// (FR-004, no hidden default), so Run refuses a value <= 0 with
	// ErrMaxTurnsRequired instead of silently substituting a cap.
	MaxTurns int

	// CLIPath is the filesystem path to the CLI binary to exec (MAJ-5 /
	// ExecutorConfig.cli_path). When empty, the driver's default binary name is
	// used and resolved via $PATH. An absolute path pins a specific install.
	CLIPath string
	// CLIArgs are pre-parsed extra arguments appended to the driver's own argv
	// (MAJ-5 / ExecutorConfig.cli_args). These are passed to execve literally —
	// the dispatch site tokenises the free-form cli_args string (no shell).
	CLIArgs []string
	// EnvOverrides are additional environment variables merged into Env for the
	// spawned process (MAJ-5 / ExecutorConfig.env_overrides). Omnipus-internal
	// keys (OMNIPUS_*) are never overridable — the driver's env builder rejects
	// override attempts on OMNIPUS_* keys (the base values are preserved) and
	// logs one WARN per offending key.
	EnvOverrides map[string]string

	// Model is the delegate agent's own configured model (ADR-032 fix C). Each
	// driver's buildArgs auto-sets the CLI's model flag from this value when
	// non-empty; an empty value (or, for opencode, a value not shaped like
	// "provider/model") is simply omitted so the CLI falls back to its own
	// configured default rather than being passed a garbage flag value.
	Model string

	// resumeNativeID carries the CLI's own captured native-conversation id into
	// a resumed invocation (FR-043). It is deliberately UNEXPORTED: only a
	// driver's own Resume sets it, from the id it captured during a prior Run's
	// stream — never a caller, and never the fresh-dispatch RunID (which is an
	// Omnipus identifier the CLI has never seen). Empty means a fresh run.
	// This is an in-process field only; no wire type is involved.
	resumeNativeID string
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
//  3. Runs are resumable via native-conversation resume: after a Run whose CLI
//     stream announced its own session/thread id, Resume continues THAT native
//     conversation. When no such id was captured, Resume refuses visibly rather
//     than silently starting a fresh conversation (FR-043, BDD-05.6).
//  4. Cancel terminates the external process and closes the Events channel.
//  5. Input delivers text to a running agent only where that is possible; a
//     driver that cannot deliver (no live conversation, or no mid-run injection
//     channel) returns a visible error, never a silent success (FR-043, DEL-20).
//  6. Test validates the runner configuration without running real work.
//
// Implementors: the U2 CLI drivers (claude-code, codex, opencode). The FakeRunner
// in this package is for tests only.
type ExternalAgentRunner interface {
	// Run starts the external agent with the given options and returns a read-only
	// channel of RunEvents. The channel is closed when the run ends (either
	// EventKindEnd or EventKindError has been sent). The context governs the run's
	// lifetime; canceling it is equivalent to calling Cancel.
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

	// Input attempts to deliver additional user input text to a running agent.
	// A driver that cannot deliver the text — because there is no live
	// conversation to steer, or because the CLI exposes no mid-run injection
	// channel (true of all three external CLIs: claude -p, codex exec and
	// opencode run) — MUST return a visible error. It MUST NOT silently discard
	// the text and return nil (FR-043, BDD-05.6, DEL-20).
	Input(text string) error

	// Resume continues the CLI's own native conversation captured from a prior
	// Run, identified by the session/thread id the CLI announced on that run's
	// stream. When no such id was captured the driver refuses with a visible error
	// — it MUST NOT start a fresh conversation and report success (FR-043,
	// BDD-05.6). runID is the Omnipus dispatch label for the resumed run, never the
	// CLI's native id.
	//
	// The optional instruction is the new text S to deliver to the resumed
	// native conversation (FR-043: "the new instruction S reaches that same
	// conversation"). It reaches the CLI the same way a fresh Run's prompt does
	// — over the child's stdin, which claude -p / codex exec / opencode run all
	// consume without a positional prompt — so a delivery resume does NOT
	// replay the original prompt. It is variadic (rather than a plain string)
	// so a BARE continuation (`Resume(ctx, runID)`) stays a one-argument call:
	// existing callers and tests did not have to change. A driver that cannot
	// deliver the instruction (no live native conversation to resume) refuses
	// visibly rather than silently dropping it (BDD-05.6, DEL-20).
	Resume(ctx context.Context, runID string, instruction ...string) (<-chan RunEvent, error)

	// Test validates the runner configuration without running real work.
	// It checks: (1) the CLI binary is present on PATH; (2) it is authenticated;
	// (3) a minimal JSON handshake succeeds.
	Test(ctx context.Context) ConnectionTestResult
}

// ErrMaxTurnsRequired is returned by a driver's Run when RunOptions.MaxTurns is
// not a positive turn cap. The dispatch site always passes the agent's resolved
// tool-iteration limit (#904 D4), so a missing cap is a programming error; the
// run is refused rather than given a hidden default (#904 FR-004).
var ErrMaxTurnsRequired = errors.New(
	"RunOptions.MaxTurns must be a positive turn cap (the agent's resolved tool-iteration limit); there is no built-in default")

// validateMaxTurns rejects a non-positive turn cap for the named driver.
func validateMaxTurns(driver string, maxTurns int) error {
	if maxTurns <= 0 {
		return fmt.Errorf("%s driver: %w (got %d)", driver, ErrMaxTurnsRequired, maxTurns)
	}
	return nil
}

// resumeInstruction resolves the optional instruction carried by a variadic
// Resume call (see ExternalAgentRunner.Resume). Zero args, or an explicitly
// empty first arg, is a bare continuation — no new instruction is delivered.
func resumeInstruction(instruction []string) string {
	if len(instruction) == 0 {
		return ""
	}
	return instruction[0]
}
