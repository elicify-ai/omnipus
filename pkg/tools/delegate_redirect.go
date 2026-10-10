// delegate_redirect.go: action="redirect" — replace a helper's current turn
// with a new instruction (ADR-20260928 D2, retained by ADR-20261004 locked
// decision 3). Single-session by definition: a redirect never cascades to
// descendants, and it never withdraws a person-question, because that pause
// no longer exists (ADR-20261004 removed it).
package tools

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// steerRedirecter is satisfied by the production delegate steering sink
// through its embedded *agent.AgentLoop beyond DelegateSteeringSink's own
// EnqueueSteeringMessage method. Declared here, not added to
// DelegateSteeringSink itself, for the same reason steerReviver stays
// narrow: a narrower test fake standing in for the steering sink is not
// forced to also implement redirection.
type steerRedirecter interface {
	RedirectSteeredSession(ctx context.Context, sessionID string, by steer.Principal, instruction string) error
	// ReviveStoppedSessionAsRedirect is ReviveStoppedSession with the
	// instruction stored as a redirect entry (the "redirect-" id the SPA reads
	// to drop the interrupted marker after reload).
	ReviveStoppedSessionAsRedirect(ctx context.Context, sessionID string, by steer.Principal, instruction string) (bool, error)
}

// executeRedirect replaces the target's current turn with the new
// instruction.
//
// State disposition (ADR-20260928 D2 as amended by ADR-20261004):
//   - a 3P child is not_steerable (named result pointing at stop_all /
//     resume) — its external CLI has no steerable live turn to replace;
//   - done/failed: "already finished — use resume", non-error (source D2's
//     preserved redirect row; Correction C1's next-round row governs
//     MESSAGES, not this control);
//   - stopped: resumed on the same conversation and generation with the
//     instruction as the newest input — the stop half is skipped;
//   - working: the live turn is stopped (single session, never a cascade)
//     and the replacement turn runs with the instruction (the agent side
//     owns the stop-then-deliver sequence).
func (t *DelegateTool) executeRedirect(ctx context.Context, args map[string]any) *ToolResult {
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

	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: redirect: %v", lerr))
	}
	by, verr := t.verifyCallerPrincipal(ctx, rec)
	if verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: redirect: %v", verr))
	}
	if cerr := t.checkSteerCaps(sessionID, text); cerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: redirect: %v", cerr)).WithError(cerr)
	}

	// Named not_steerable, like executeSteer's own 3P posture (D2: "external
	// command-line children return a named not_steerable result pointing to
	// Stop all / RESUME").
	if rec.Is3P {
		return ErrorResult(fmt.Sprintf(
			"delegate: redirect: not_steerable: external command-line session %s runs on an external CLI "+
				"(claude-code/codex/opencode) with no steerable live turn to replace; use "+
				"action=\"stop_all\" to stop it, or action=\"resume\" (which continues the same CLI conversation only while it is live; "+
				"otherwise start a new delegation)",
			sessionID,
		))
	}

	// done/failed: nothing to replace — non-error, nothing started (source
	// D2's preserved row; the same non-error reasoning as executeStopAll's
	// "already terminal": an error here drives agents into retry loops).
	if rec.Terminal() {
		return NewToolResult(fmt.Sprintf(
			"Session %s has already finished (%s) — nothing was replaced. Use action=\"resume\" to start its next round.",
			sessionID, rec.State,
		))
	}

	// stopped: skip the stop half; the redirect is the resume instruction
	// (same conversation, same generation — Correction C1's stopped row).
	if rec.Stopped() {
		reviver, ok := t.steering.(steerRedirecter)
		if !ok {
			return ErrorResult(fmt.Sprintf("delegate: redirect: session %s is stopped and cannot be resumed: no reviver configured", sessionID))
		}
		revived, rerr := reviver.ReviveStoppedSessionAsRedirect(ctx, sessionID, by, text)
		if rerr != nil {
			return ErrorResult(fmt.Sprintf("delegate: redirect: resume stopped session %s: %v", sessionID, rerr)).WithError(rerr)
		}
		if !revived {
			return ErrorResult(fmt.Sprintf("delegate: redirect: session %s could not be resumed", sessionID))
		}
		return NewToolResult(fmt.Sprintf(
			"Session %s was stopped; the redirect instruction revived it on the same conversation and generation.",
			sessionID,
		))
	}

	// working (or queued with a live admission): stop the current turn and
	// deliver the replacement through the agent side.
	redirecter, ok := t.steering.(steerRedirecter)
	if !ok {
		return ErrorResult(fmt.Sprintf("delegate: redirect: session %s cannot be redirected: no redirect capability configured", sessionID))
	}
	if rerr := redirecter.RedirectSteeredSession(ctx, sessionID, by, text); rerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: redirect: %v", rerr)).WithError(rerr)
	}
	return NewToolResult(fmt.Sprintf(
		"Redirect accepted for session %s: its current turn is being stopped and replaced with the new instruction.",
		sessionID,
	))
}
