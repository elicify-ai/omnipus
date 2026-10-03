package commands

import (
	"context"
	"errors"
)

// cmd_stop.go — the D9 chat commands /stop and /stop-redirect
// (ADR-20260928 sub-agent control plane, D9; amends the cross-channel cancel
// spec's decision 12). Both are THEIR OWN commands, not aliases of /cancel
// (/cancel keeps FR-5's no-aliases rule); there is no /steer alias (founder
// O4); /goal stop remains the /goal clear alias (cmd_goal.go).
//
// D9 input table:
//   /stop                    (root OR helper) — stop ONLY this session's
//                            current turn; helpers keep working; no cascade.
//   /stop-redirect <instr>   (helper chat)     — D2 redirect on that helper
//                            only; its subtree keeps working.
//   /stop-redirect <instr>   (root chat)       — refuse with guidance to
//                            target a helper; the root's own /stop works.
//
// Transport: both are DeliveryClient + AvailableWhileStreaming (architect
// seam ruling §3.1) — on the web the SPA intercepts the typed command and
// sends the dedicated generated RedirectFrame (/stop via cancelIfStreaming),
// never as chat text, so both execute mid-stream. This handler is the server
// seam for the CLI surface and the channel interception path.

// redirectUsageReply is the usage reply for a bare /stop-redirect (D9: an
// empty or whitespace-only instruction replies with usage and changes
// nothing). It names the command so the reply is self-explanatory.
const redirectUsageReply = "/stop-redirect <instruction> — stop this helper's current turn, then continue it with <instruction> as the newest instruction (its helpers keep working)."

// redirectRootRefusalReply is the root-chat refusal (D9 row 3): guidance to
// target a helper; nothing is issued.
const redirectRootRefusalReply = "/stop-redirect works only in a helper session's chat — this is the root conversation. Open the helper session and run /stop-redirect there."

// stopCommand is D9 /stop: conversation-scoped (#955), self-only — the same
// session-scoped server action as one Stop-button press (scope session, no
// cascade, never the /cancel tree path). Works in a root or a helper chat.
func stopCommand() Definition {
	return Definition{
		Name:                    "stop",
		Description:             "Stop this session's current turn (its helpers keep working)",
		Usage:                   "/stop",
		Surfaces:                []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:                DeliveryClient,
		AvailableWhileStreaming: true,
		// No Aliases per D9 — /stop is its OWN command, not an alias of
		// /cancel, and /cancel keeps FR-5's no-aliases rule.
		Handler: func(ctx context.Context, req Request, rt *Runtime) error {
			if rt == nil {
				return req.Reply(unavailableMsg)
			}
			var sessionID string
			if rt.SessionID != nil {
				sessionID = rt.SessionID()
			}
			if rt.agentLoop == nil {
				// No agent loop wired — honest no-op, same shape as
				// CancelActiveTurn's nil-loop contract.
				return req.Reply("Nothing to stop")
			}
			fired, armed, err := rt.agentLoop.StopSessionTurn(ctx, sessionID, req.SenderID, req.Channel)
			switch {
			case err != nil:
				// Real failure (e.g. fsync error, lock contention) — surface
				// it; a silent failure would read as success.
				return req.Reply("Stop request failed: " + err.Error())
			case fired:
				return req.Reply("⏸ Stopping this session's current turn — its helpers keep working.")
			case armed:
				// Distinct truthful outcome: a pre-registration latch now
				// stands in — nothing stopped yet, but the stop WILL fire the
				// instant a turn registers (mirrors /cancel's armed reply).
				return req.Reply("⏸ Stop acknowledged — nothing is running yet, but it will stop the instant it starts.")
			default:
				// Informational — nothing was running; not a failure.
				return req.Reply("Nothing to stop")
			}
		},
	}
}

// stopRedirectCommand is D9 /stop-redirect <instruction>: in a helper's chat
// it issues the D2 redirect on that helper only (its subtree keeps working);
// in the root's chat it refuses with helper-targeting guidance and issues
// nothing; a bare or whitespace-only instruction (Unicode-aware) replies
// with usage and changes nothing.
func stopRedirectCommand() Definition {
	return Definition{
		Name:                    "stop-redirect",
		Description:             "Stop this helper's current turn, then continue it with a new instruction",
		Usage:                   "/stop-redirect <instruction>",
		Surfaces:                []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:                DeliveryClient,
		AvailableWhileStreaming: true,
		// No Aliases per D9 — its OWN command; the /stop- prefix encodes the
		// founder's model ("redirect stops first, then gives the new
		// instruction"), it does not alias /stop.
		Handler: func(ctx context.Context, req Request, rt *Runtime) error {
			if rt == nil {
				return req.Reply(unavailableMsg)
			}

			// Root-vs-helper gate, fail-closed (seam ruling §3.2): nil or
			// false ⇒ root ⇒ refusal, nothing issued. The gate runs before
			// anything else so a root chat never produces side effects.
			isHelper := false
			if rt.IsHelperSession != nil {
				isHelper = rt.IsHelperSession()
			}
			if !isHelper {
				return req.Reply(redirectRootRefusalReply)
			}

			// Instruction extraction via the package's own free-text
			// remainder helper (strings.Fields → unicode.IsSpace-aware, so
			// NBSP/em-space-only input yields "" exactly like bare input).
			instruction := CommandArgs(req.Text)
			if instruction == "" {
				// Usage reply, change nothing (D9: the command shape is
				// "/stop-redirect <instruction>").
				return req.Reply(redirectUsageReply)
			}

			if rt.agentLoop == nil {
				return req.Reply("Redirect failed: no agent loop is wired for this session")
			}
			var sessionID string
			if rt.SessionID != nil {
				sessionID = rt.SessionID()
			}
			err := rt.agentLoop.RedirectSessionTurn(ctx, sessionID, instruction, req.SenderID, req.Channel)
			switch {
			case err == nil:
				return req.Reply("⏪ Redirecting — stopping this helper's current turn, then continuing with your instruction (its helpers keep working).")
			case errors.Is(err, ErrNotHelperSession):
				// Stale identity: the loop re-checked server-side and the
				// target is not a helper — same guidance as the root
				// refusal, nothing issued.
				return req.Reply(redirectRootRefusalReply)
			case errors.Is(err, ErrNothingToRedirect):
				// done/failed target (D2 stop table): truthful non-error
				// guidance — the exact wording D2 pins.
				return req.Reply("already finished — use RESUME")
			default:
				// Real (unknown) failure — surface it; a silent failure
				// would read as success.
				return req.Reply("Redirect failed: " + err.Error())
			}
		},
	}
}
