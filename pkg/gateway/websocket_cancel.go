// websocket_cancel.go: Cancel and interrupt handling over the socket.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

var gatewaySteerCancellers sync.Map // key: *agent.AgentLoop, value: steer.Canceller

// setGatewaySteerCanceller binds the composition-root Canceller to gateway
// Stop surfaces. WP-A's wiring section calls this when it supplies the
// generation-aware live-turn adapter; the lazy fallback keeps focused gateway
// tests and partially landed branches functional without inventing a second
// wire contract.
func setGatewaySteerCanceller(al *agent.AgentLoop, canceller steer.Canceller) {
	if al != nil && canceller != nil {
		gatewaySteerCancellers.Store(al, canceller)
	}
}

func gatewaySteerCanceller(al *agent.AgentLoop) steer.Canceller {
	if al == nil {
		return nil
	}
	if value, ok := gatewaySteerCancellers.Load(al); ok {
		return value.(steer.Canceller)
	}
	canceller := agent.NewSteerCanceller(al.GetSessionLifecycleStore())
	actual, _ := gatewaySteerCancellers.LoadOrStore(al, steer.Canceller(canceller))
	return actual.(steer.Canceller)
}

// sendCancelStageFrame marshals a generated.CancelStageFrame and delivers it via wc.sendCh.
// Mirrors sendConnGenFrame's non-critical send path (immediate try, then 10ms/50ms
// backoffs) but is non-critical so it does not use sendConnGenFrame's critical-frame
// timeout path. Best-effort: marshal/send errors are logged at debug level and do not
// block the cancel state machine.
func sendCancelStageFrame(wc *wsConn, sessionID, stage string) {
	if wc == nil {
		return
	}
	data, err := json.Marshal(generated.CancelStageFrame{
		Type:      string(generated.WsFrameTypeCancelStage),
		SessionId: sessionID,
		Stage:     stage,
	})
	if err != nil {
		slog.Debug("ws: marshal cancel_stage frame failed", "stage", stage, "error", err)
		return
	}
	// Route through sendRawFrameBytes to respect replay-divert logic and the
	// replayMu serialization that prevents the TOCTOU race (code-reviewer Finding #2).
	sendRawFrameBytes(wc, string(generated.WsFrameTypeCancelStage), data)
	// sendRawFrameBytes logs at Warn on drop; suppress the duplicate debug log that
	// existed in the old inline implementation.
}

func sendCancelReportFrame(wc *wsConn, sessionID, stage string, report steer.CancelReport) {
	if wc == nil {
		return
	}
	partial := len(report.Unreachable) > 0
	frame := generated.CancelStageFrame{
		Type:                   string(generated.WsFrameTypeCancelStage),
		SessionId:              sessionID,
		Stage:                  stage,
		Reached:                append([]string(nil), report.Reached...),
		SkippedNewerGeneration: append([]string(nil), report.SkippedNewerGeneration...),
		SkippedTerminal:        append([]string(nil), report.SkippedTerminal...),
		Partial:                &partial,
	}
	for _, unreachable := range report.Unreachable {
		frame.Unreachable = append(frame.Unreachable, struct {
			Id     string `json:"id"`
			Reason string `json:"reason"`
		}{Id: unreachable.ID, Reason: unreachable.Reason})
	}
	data, err := json.Marshal(frame)
	if err != nil {
		slog.Debug("ws: marshal cancel report frame failed", "stage", stage, "error", err)
		return
	}
	sendRawFrameBytes(wc, string(generated.WsFrameTypeCancelStage), data)
}

func cancelPartialSummary(report steer.CancelReport) string {
	if len(report.Unreachable) == 0 {
		return ""
	}
	return fmt.Sprintf("stopped %d of %d; %d unreachable",
		len(report.Reached), len(report.Reached)+len(report.Unreachable), len(report.Unreachable))
}

func sendCancelPartialNotice(wc *wsConn, sessionID string, report steer.CancelReport) {
	message := cancelPartialSummary(report)
	if wc == nil || message == "" {
		return
	}
	sid := sessionID
	sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
		Type:      string(generated.WsFrameTypeError),
		SessionId: &sid,
		Message:   message,
	})
}

func (h *WSHandler) sendExternalCancelPartialNotice(ctx context.Context, sessionID string, report steer.CancelReport) {
	message := cancelPartialSummary(report)
	if h == nil || h.agentLoop == nil || h.msgBus == nil || message == "" {
		return
	}
	lifecycle := h.agentLoop.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil || rec.SteeredBy == nil {
		return
	}
	target := rec.SteeredBy.ReportingTarget
	if target.Channel == "" || target.ChatID == "" || target.Channel == "web" || target.Channel == "webchat" {
		return
	}
	if err := h.msgBus.PublishOutbound(ctx, bus.OutboundMessage{
		Channel: target.Channel,
		ChatID:  target.ChatID,
		Content: message,
	}); err != nil {
		slog.Warn("ws: publish partial Stop notice to originating channel failed",
			"session_id", sessionID, "channel", target.Channel, "chat_id", target.ChatID, "error", err)
	}
}

// cancelSteeredSubtree applies ADR-091 Stop only when a durable lifecycle
// record exists. Ordinary chats with no steering record keep using the legacy
// live-turn cancel path and must not be falsely reported as partial.
func cancelSteeredSubtree(ctx context.Context, al *agent.AgentLoop, sessionID string, by steer.Principal) (steer.CancelReport, bool) {
	var report steer.CancelReport
	if al == nil {
		return report, false
	}
	store := al.GetSessionLifecycleStore()
	if store == nil {
		return report, false
	}
	if _, err := store.Load(sessionID); err != nil {
		if errors.Is(err, session.ErrLifecycleNotFound) {
			if _, statErr := os.Stat(filepath.Join(store.Dir(), sessionID+".jsonl")); errors.Is(statErr, os.ErrNotExist) {
				return report, false
			}
		}
		report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: sessionID, Reason: err.Error()})
		return report, true
	}
	canceller := gatewaySteerCanceller(al)
	if canceller == nil {
		report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: sessionID, Reason: "steer canceller is not configured"})
		return report, true
	}
	result, err := canceller.CancelSubtree(ctx, sessionID, by)
	if err != nil {
		result.Unreachable = append(result.Unreachable, steer.UnreachableSession{ID: sessionID, Reason: err.Error()})
	}
	return result, true
}

// u11CollectDescendantSessionIDs walks the durable lifecycle store's
// SteeringSessionID edges (pkg/session/lifecycle.go, FR-019/FR-020;
// LifecycleStore.List(LifecycleFilter{SteeringSessionID: id}) returns X's
// DIRECT children only, index-backed per BDD-19) to collect EVERY descendant
// of rootID, however many delegation levels deep. Returns only descendants —
// rootID itself is never included; the caller prepends it.
//
// ADR-057 FR-032/W10c: cancelAllPendingForSessions matches a pending
// approval by EXACT equality on the registry entry's own acting session id
// (FR-080 — a delegated child's entry carries the CHILD's own id, never the
// chat's), so a chat-level Stop that passed only the chat/root id would
// cancel nothing inside any live child (BDD-33). The DURABLE lifecycle store
// is reachable read-only through h.agentLoop's already-exported
// GetSessionLifecycleStore() (pkg/agent/session_messaging_wire.go), and
// every delegation — live or not — has a LifecycleRecord (User Story 4), so
// this walk is authoritative independent of what is still running.
//
// [FIX-5, Defect 4, 2026-08-03] HOISTED: this used to be a byte-identical
// duplicate of pkg/agent/cancel.go's collectDescendantSessionIDs, kept
// separate only because a parallel-implementation ownership rule (then:
// "neither of which this unit may edit") forbade this unit from touching
// pkg/agent. That rule has expired — this is now a thin signature-compat
// shim over the ONE hoisted implementation, agent.CollectDescendantSessionIDs
// (pkg/agent/cancel.go), which also gained a real error return (Defect 2:
// a lifecycleStore.List failure partway through the walk used to be
// swallowed as "this node has no children" — silently truncating the
// returned set with no signal). This shim is kept, rather than deleted and
// inlined at every call site, because pkg/gateway/rest.go's deleteSession
// handler and pkg/gateway/websocket_adr057_test.go's U11 unit tests call it
// directly by this exact name/signature and are outside this fix's file
// ownership — changing its signature would require edits there too. A
// caller that CAN use the richer (typed-error) signature directly — see
// buildCancelHooks's CancelPendingApprovals closure below — calls
// agent.CollectDescendantSessionIDs itself instead of this shim, so it can
// react to a partial-walk failure with a more specific diagnostic than the
// generic one logged here.
//
// Guards against a corrupted or cyclic SteeringSessionID chain with a visited
// set rather than trusting the configured delegation-depth cap to bound
// recursion — this walk must terminate
// even over on-disk state that predates or violates that cap. A nil store
// (no delegation lifecycle store wired — most webchat-only installs never
// mint one) yields an empty slice, so the caller degrades to exactly
// cancelAllPendingForSession's documented single-id behavior. A walk error
// is logged at WARN (never returned — see the shim rationale above) so this
// call's INCOMPLETE-cascade case is never silent, even though it cannot be
// propagated through this signature.
func u11CollectDescendantSessionIDs(ls *session.LifecycleStore, rootID string) []string {
	descendants, err := agent.CollectDescendantSessionIDs(ls, rootID)
	if err != nil {
		slog.Warn("ws: descendant walk: could not list children — the walk is INCOMPLETE; any descendant beyond the failure point is unreachable to this caller",
			"session_id", rootID, "error", err)
	}
	return descendants
}

// buildCancelHooks constructs the transport-specific agent.CancelHooks for a
// web-SPA-originated cancel. wc is the live connection to notify via
// cancel_stage frames — nil when there is no live connection to notify.
//
// A nil wc is a legitimate call shape whenever a cancel is triggered with no
// live connection to acknowledge to. sendCancelStageFrame already no-ops
// safely on a nil wc, so the SAME hook set handleCancel builds for a real
// Stop-click also works, unmodified, for any connection-less caller — there
// is only one place in this file that knows how to build a web-cancel's side
// effects.
func (h *WSHandler) buildCancelHooks(wc *wsConn) agent.CancelHooks {
	return h.buildCancelHooksWithReport(wc, nil)
}

func (h *WSHandler) buildCancelHooksWithReport(wc *wsConn, report *steer.CancelReport) agent.CancelHooks {
	return agent.CancelHooks{
		SendStageFrame: func(sid, stage string) {
			if stage == "detached" && report != nil {
				sendCancelReportFrame(wc, sid, stage, *report)
				return
			}
			sendCancelStageFrame(wc, sid, stage)
		},
		CancelPendingApprovals: func(sid, reason string) {
			if h.approvalRegV2 == nil {
				return
			}
			// ADR-057 FR-032/W10c: cancel over the DESCENDANT SET, not the
			// single chat/root id — see u11CollectDescendantSessionIDs' doc
			// comment for why a single id would silently cancel nothing
			// inside a live delegated child (BDD-33). The old single-id
			// cancelAllPendingForSession still compiles and remains correct
			// for a session with no descendants (its own doc comment says
			// so); this call site is the one FR-032 requires use the plural
			// form.
			var lifecycleStore *session.LifecycleStore
			if h.agentLoop != nil {
				lifecycleStore = h.agentLoop.GetSessionLifecycleStore()
			}
			// [FIX-5, Defect 2, 2026-08-03] Calls agent.CollectDescendantSessionIDs
			// directly (not the u11CollectDescendantSessionIDs shim above) so this
			// call site can react to a partial-walk failure with the specific,
			// user-visible consequence it has here: a lifecycleStore.List error
			// partway through the walk used to be swallowed as "no more
			// children" and the dropped descendant's pending RequestApproval was
			// left standing — its blocked select only unblocks on ITS OWN
			// multi-minute approval timeout, all while this Stop click's UI
			// already reported the cancel as complete.
			descendants, walkErr := agent.CollectDescendantSessionIDs(lifecycleStore, sid)
			if walkErr != nil {
				slog.Warn("ws: buildCancelHooks: descendant walk failed partway through — the approval-cancel cascade below is INCOMPLETE; a dropped descendant's pending approval will hang until its own timeout instead of being auto-denied by this Stop",
					"session_id", sid, "error", walkErr)
			}
			sessionIDs := append([]string{sid}, descendants...)
			h.approvalRegV2.cancelAllPendingForSessions(sessionIDs, reason)
		},
		KillBackgroundSessions: func(sid string) (killed, failed int) {
			// Cascade the cancel to any detached background bash/exec
			// sessions this chat session started (FR-B10/FR-B11, User Story 5).
			// The returned counts flow back through RequestCancel into the
			// turn_canceled audit event's background_sessions_killed/
			// background_sessions_failed fields, and into CancelOutcome so
			// handleCancel below can notify the client even when there was no
			// active turn to cancel.
			return tools.GetSharedSessionManager().KillAllForSession(sid)
		},
		SetSessionInterrupted: func(sid string) {
			store := h.resolveSessionStore(sid)
			if store != nil {
				status := session.StatusInterrupted
				if err := store.SetMeta(sid, session.MetaPatch{Status: &status}); err != nil {
					slog.Warn("ws: could not mark session interrupted",
						"session_id", sid, "error", err)
				}
			}
		},
		// OnLatchExpired closes the last gap in the chain: handleCancel below
		// already tells the user "acknowledged" (cancel_stage "graceful") the
		// instant CancelOutcome.Armed is true, but a latch that ages out
		// (cancelPreArmTTL, pkg/agent/cancel_prearm.go) with no turn ever
		// registering to consume it means that acknowledgement was never made
		// good on — the turn runs to completion (or the background-only
		// cancel this reap represents never lands at all) uncanceled. Without
		// this, the user is told "stop requested" and then never told it
		// didn't happen — the exact silent-success defect this whole chain
		// exists to close, just moved one step later.
		//
		// Deliberately NOT a second cancel_stage frame: {graceful, hard,
		// detached} (contracts/components/schemas/CancelStageFrame.yaml) is a
		// closed, SPA-validated enum and none of its three members mean
		// "requested but did not land" — graceful/hard/detached all describe
		// a cancel that IS proceeding. Reusing "graceful" here (as the
		// no-active-turn case just above does, deliberately, for the
		// still-pending case) would claim progress that never happened —
		// over-signaling in exactly the place this fix must not. ErrorFrame
		// is the right existing wire type instead: it is documented as
		// "session-scoped... SPA displays the message as a toast or inline
		// error... does NOT terminate the WebSocket connection" — precisely
		// "something the user asked for did not happen", with no enum to
		// extend and no contract change required.
		OnLatchExpired: func(scope agent.CancelScope, canceller agent.CancelCanceller) {
			if wc == nil {
				// No live connection to notify — buildCancelHooks(nil) is used
				// whenever a cancel is triggered with nobody watching this
				// session. notifyLatchExpired (cancel_prearm.go) already
				// logged the base "latch expired" Warn unconditionally; add
				// site-specific context here so an operator sees not just
				// THAT it expired but that this was a no-connection case with
				// no user to have told anyway.
				slog.Warn("ws: cancel latch expired with no live connection to notify — the cancel it stood in for never took effect",
					"session_id", scope.SessionID,
					"channel", scope.Channel,
					"chat_id", scope.ChatID,
					"canceller_user", canceller.UserID,
					"canceller_channel", canceller.Channel,
				)
				return
			}
			var sidPtr *string
			if scope.SessionID != "" {
				sidCopy := scope.SessionID
				sidPtr = &sidCopy
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				SessionId: sidPtr,
				Message:   "Cancel request did not take effect: no matching operation was found before the request timed out. If something is still running, try Stop again.",
			})
		},
	}
}

// handleCancel delegates to agentLoop.RequestCancel — the canonical cancel
// state machine that provides uniform audit, transcript, abuse-detection, and
// 2-stage timer behavior across all four cancel entry points (web SPA,
// Tier A /cancel command, Tier B text-parsing, CLI). FR-10, FR-11, FR-12,
// FR-13a, FR-15, FR-17, FR-18-21, FR-25a, FR-35, FR-36.
//
// This function is intentionally thin: it builds scope/canceller/hooks and
// delegates. All state-machine logic lives in pkg/agent.RequestCancel.
func (h *WSHandler) handleCancel(wc *wsConn, sessionID string) {
	if sessionID == "" {
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "cancel requires session_id",
		})
		return
	}

	report, cascaded := cancelSteeredSubtree(context.Background(), h.agentLoop, sessionID, steer.Principal{
		Kind: steer.PrincipalKindHuman,
		ID:   wc.userID,
	})
	if cascaded {
		sendCancelPartialNotice(wc, sessionID, report)
		h.sendExternalCancelPartialNotice(context.Background(), sessionID, report)
	}

	scope := agent.CancelScope{SessionID: sessionID}
	canceller := agent.CancelCanceller{
		UserID:  wc.userID,
		Channel: "web",
	}
	var reportForHooks *steer.CancelReport
	if cascaded {
		reportForHooks = &report
	}
	hooks := h.buildCancelHooksWithReport(wc, reportForHooks)

	outcome, err := h.agentLoop.RequestCancel(context.Background(), scope, canceller, hooks)
	if err != nil {
		slog.Warn("ws: handleCancel: RequestCancel error",
			"session_id", sessionID, "error", err)
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "cancel failed: " + err.Error(),
		})
		return
	}
	if !outcome.Fired {
		slog.Debug("ws: cancel — no active turn or already canceled",
			"session_id", sessionID, "armed", outcome.Armed)
		// Observability fix: a cancel with no active turn is the COMMON case
		// for a `bash run_in_background=true` job whose own turn already
		// ended (see cancel.go's RequestCancel doc comment) — the background
		// kill cascade above still ran and may have actually killed real
		// work, but without this the user clicking Stop got ZERO feedback
		// (no frame, no log) despite that. Reuse the existing "graceful"
		// stage value rather than introducing a new CancelStageFrame.stage
		// enum member: that field is a closed, SPA-validated enum
		// ({graceful, hard, detached} — contracts/components/schemas/
		// CancelStageFrame.yaml) and adding a value would require a
		// contract + frontend change beyond this fix's scope; "graceful"'s
		// documented meaning ("cancel request acknowledged; agent is
		// completing the current tool call and will stop at the next safe
		// checkpoint") fits a background-only kill well enough to give the
		// Stop button SOME visible response instead of silence.
		//
		// outcome.Armed closes a second, HIGH-severity gap in the same
		// family (review finding on commit 99d4e729, CancelOutcome.Armed):
		// its doc comment mandates "Callers surfacing Fired to a user MUST
		// also check Armed before reporting a cancel as a no-op" — a contract
		// every RequestCancel caller that surfaces Fired to a user or operator
		// must honour, not one this edit closes everywhere in one pass. This
		// fixes THIS caller (the web-SPA Stop button, handleCancel): a Stop
		// click arriving before its turn has registered
		// (pkg/agent/cancel_prearm.go) now correctly latches and WILL cancel
		// that turn the instant it registers (within cancelPreArmTTL), and
		// the click itself produces a frame here instead of the pre-fix zero
		// frames. Other RequestCancel callers still need their own fix for
		// the same contract — notably pkg/commands' RequestCancelForSession
		// (flattens to a bare bool, dropping Armed, so cmd_cancel.go reports
		// "Nothing to cancel" for an armed latch; owned by a separate agent
		// via a widened adapter signature) and plan_engine.go's cancelSessions
		// fan-out (buckets Armed as notFired; owned separately) — do not
		// treat either as covered by this file. Reuse "graceful" for this
		// case too, for the same reason as the background-kill case above:
		// on the wire it means "acknowledged, pending", never "canceled" —
		// nothing has actually stopped yet, so claiming more than that would
		// just be the same under-signaling bug inverted into over-signaling
		// (the trap this fix must not fall into).
		if outcome.BackgroundSessionsKilled > 0 {
			slog.Info("ws: cancel killed background session(s) with no active turn",
				"session_id", sessionID,
				"background_sessions_killed", outcome.BackgroundSessionsKilled,
				"background_sessions_failed", outcome.BackgroundSessionsFailed,
				"armed", outcome.Armed,
			)
		}
		if outcome.Armed {
			slog.Info("ws: cancel armed a pre-registration latch — no turn registered yet for this session; the next turn to register will be canceled the instant it does, unless the latch's TTL expires first",
				"session_id", sessionID,
			)
		}
		if cascaded {
			sendCancelReportFrame(wc, sessionID, "detached", report)
		} else if outcome.BackgroundSessionsKilled > 0 || outcome.Armed {
			sendCancelStageFrame(wc, sessionID, "graceful")
		}
	}
}
