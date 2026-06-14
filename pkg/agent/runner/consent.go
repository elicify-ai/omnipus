// consent.go — consent routing for external agent permission requests.
//
// Maps a PermissionRequestEvent from an external CLI driver to an
// agent.ToolApprovalRequest and routes it through the ToolApprover hook
// (the same consent layer used for Omnipus-native tool approvals).
//
// ┌─────────────────────────────────────────────────────────────────────────┐
// │ POST-HOC CONSENT LIMITATION — READ BEFORE WIRING external-cli            │
// │                                                                          │
// │ External-CLI consent is fundamentally POST-HOC and BEST-EFFORT. The      │
// │ three supported CLIs (claude -p, codex exec, opencode run) run in        │
// │ non-interactive streaming mode with NO bidirectional permission fence    │
// │ that Omnipus can answer mid-call. By the time a tool_use / tool_call /   │
// │ tool.start event reaches Omnipus and the consent layer renders a prompt, │
// │ the CLI has ALREADY started (or finished) that tool call. A DENY         │
// │ therefore CANNOT veto the individual call — it can only CANCEL the whole │
// │ run by killing the process (drivers call Cancel() on !Allow). An ALLOW   │
// │ is a no-op; the run simply continues.                                    │
// │                                                                          │
// │ The REAL security boundary for external-cli is the CLI's own sandbox     │
// │ plus the isolated git worktree the run executes in (RunOptions.WorkDir   │
// │ — FR-5.3), NOT this consent layer. external-cli IS wired into production  │
// │ sub-agent dispatch in v0.1.0 (ResolveDispatch → DispatchKindExternalCLI; │
// │ pkg/agent/subturn.go runs the driver in a worktree under the CLI's own   │
// │ sandbox). This consent path is routed BEST-EFFORT post-hoc: a DENY kills │
// │ the run, an ALLOW lets it continue. It is observability + a kill switch, │
// │ NOT a pre-emptive call-level gate. The operator decision is that the     │
// │ CLI's own sandbox + the worktree are the authoritative confinement.      │
// └─────────────────────────────────────────────────────────────────────────┘
//
// Spec-4 FR-5.1 contract:
//   - Permission requests from external runners MUST be routed to the consent layer.
//   - Deny-by-default when no consent handler is registered.
//   - Every default-deny is audit-logged (m-5 / FR-5.1 requirement).
//
// The ConsentHandler interface defined here is the injection point. Wire it by
// providing an implementation backed by agent.ToolApprover at boot time.
//
// Mapping (per m-7 resolution, spec-review round-3):
//
//	PermissionRequestEvent.ToolName  → ToolApprovalRequest.Tool
//	PermissionRequestEvent.RawInput  → ToolApprovalRequest.Arguments (via JSON decode)
//	PermissionRequestEvent.RequestID → used as ToolCallID
//	PermissionRequestEvent.Description → surfaced in the approval frame Message
//
// The RunID and optional session metadata are carried in the Meta.SessionID field
// so the SPA can scope the approval frame to the right session.
package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// ConsentHandler routes a permission request from an external runner to the
// Omnipus consent layer and returns the decision.
//
// Implementations wrap agent.ToolApprover.ApproveTool (pkg/agent/hooks.go:123-125)
// so the same WS approval flow that drives native tool consent also handles
// external-runner permission prompts.
//
// When the handler is nil or returns an error, consent.go falls back to
// DenyByDefault (FR-5.1 / deny-by-default gate).
type ConsentHandler interface {
	// RequestConsent routes a PermissionRequestEvent to the approval layer and
	// blocks until a decision is received or the context is cancelled.
	// Returns (allow, reason).
	//
	// The context carries the run lifetime; if cancelled, return (false, "context cancelled").
	RequestConsent(ctx context.Context, req ConsentRequest) (allow bool, reason string)
}

// ConsentRequest is the payload passed to a ConsentHandler.
// It carries the information needed to render an approval prompt and route
// the decision back to the external agent.
type ConsentRequest struct {
	// RequestID is a stable identifier for this permission prompt.
	// Matches PermissionRequestEvent.RequestID.
	RequestID string

	// ToolName is the tool whose invocation is being gated.
	// Maps to ToolApprovalRequest.Tool.
	ToolName string

	// Description is a human-readable explanation of what the agent wants to do.
	Description string

	// RawInput is the raw JSON-encoded tool input. Decoded to Arguments below.
	RawInput []byte

	// Arguments is the decoded tool input. Populated from RawInput if available.
	// Maps to ToolApprovalRequest.Arguments.
	Arguments map[string]any

	// RunID identifies the external runner run that issued this request.
	RunID string

	// SessionID is the Omnipus session that owns this run, for WS frame scoping.
	SessionID string
}

// RouteConsent routes a PermissionRequestEvent through the ConsentHandler.
// When handler is nil, it denies immediately and logs the default-deny (FR-5.1, m-5).
// Returns a PermissionDecision suitable for passing back to ExternalAgentRunner.Decide.
func RouteConsent(
	ctx context.Context,
	ev PermissionRequestEvent,
	runID string,
	sessionID string,
	handler ConsentHandler,
) PermissionDecision {
	// Decode raw input to arguments map (best-effort).
	var args map[string]any
	if len(ev.RawInput) > 0 {
		if err := json.Unmarshal(ev.RawInput, &args); err != nil {
			slog.Debug("runner/consent: could not decode raw input as object",
				"request_id", ev.RequestID, "tool", ev.ToolName, "err", err)
		}
	}

	req := ConsentRequest{
		RequestID:   ev.RequestID,
		ToolName:    ev.ToolName,
		Description: ev.Description,
		RawInput:    ev.RawInput,
		Arguments:   args,
		RunID:       runID,
		SessionID:   sessionID,
	}

	if handler == nil {
		return auditedDenyByDefault(ev.RequestID, ev.ToolName, runID, "no consent handler registered")
	}

	allow, reason := handler.RequestConsent(ctx, req)
	if !allow {
		slog.Info("runner/consent: permission denied",
			"request_id", ev.RequestID,
			"tool", ev.ToolName,
			"run_id", runID,
			"reason", reason)
		return PermissionDecision{
			RequestID: ev.RequestID,
			Allow:     false,
			Reason:    reason,
		}
	}
	slog.Info("runner/consent: permission granted",
		"request_id", ev.RequestID,
		"tool", ev.ToolName,
		"run_id", runID)
	return PermissionDecision{
		RequestID: ev.RequestID,
		Allow:     true,
	}
}

// auditedDenyByDefault constructs a denial and logs it so the deny is always
// observable (FR-5.1 / m-5: every default-deny must be audit-logged).
func auditedDenyByDefault(requestID, toolName, runID, reason string) PermissionDecision {
	slog.Warn("runner/consent: default-deny (no consent handler)",
		"request_id", requestID,
		"tool", toolName,
		"run_id", runID,
		"reason", reason,
		"ts", time.Now().UTC().Format(time.RFC3339),
	)
	return DenyByDefault(requestID)
}

// ConsentDispatcher is a helper that dispatches permission request events from
// a runner's event channel to the consent layer and sends decisions back.
// It runs in its own goroutine and exits when ctx is cancelled or the event
// channel is closed.
//
// Usage:
//
//	go ConsentDispatcher(ctx, evCh, runner, runID, sessionID, handler)
func ConsentDispatcher(
	ctx context.Context,
	evCh <-chan RunEvent,
	runner ExternalAgentRunner,
	runID string,
	sessionID string,
	handler ConsentHandler,
	out chan<- RunEvent,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-evCh:
			if !ok {
				return
			}
			if ev.Kind == EventKindPermissionRequest && ev.PermissionRequest != nil {
				// Route to consent layer and send decision back.
				decision := RouteConsent(ctx, *ev.PermissionRequest, runID, sessionID, handler)
				runner.Decide(decision)
				// Also forward the permission-request event to the caller's output
				// channel so the SPA can render a pending-approval indicator.
				forwardEvent(ctx, ev, out)
			} else {
				forwardEvent(ctx, ev, out)
			}
		}
	}
}

// forwardEvent sends ev to out, dropping it if ctx is cancelled.
func forwardEvent(ctx context.Context, ev RunEvent, out chan<- RunEvent) {
	select {
	case out <- ev:
	case <-ctx.Done():
	}
}
