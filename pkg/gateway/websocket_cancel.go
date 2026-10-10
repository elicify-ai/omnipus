// websocket_cancel.go: Cancel and interrupt handling over the socket.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
// Stop surfaces. gateway_boot.go::wireSteerDeps calls this when it supplies the
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
		if c, ok := value.(steer.Canceller); ok {
			return c
		}
		// gatewaySteerCancellers is private to this file; every writer
		// (setGatewaySteerCanceller's Store, and the LoadOrStore below)
		// stores a steer.Canceller — unreachable in practice. Fall through
		// to mint a fresh canceller instead of trusting that blindly.
	}
	canceller := agent.NewSteerCanceller(al.GetSessionLifecycleStore())
	actual, _ := gatewaySteerCancellers.LoadOrStore(al, steer.Canceller(canceller))
	c, ok := actual.(steer.Canceller)
	if !ok {
		// Same guarantee as above — unreachable in practice.
		return canceller
	}
	return c
}

// sendCancelStageFrame publishes a cancel_stage frame for sessionID through
// the session hub (#823 BE-DESIGN.md §1.2): every tab bound to the session
// sees the Stop's progress, with a sequence number, not only the tab that
// pressed Stop. The requesting connection wc (nil for a connection-less
// cancel) additionally gets an unsequenced copy when it is not bound to
// sessionID itself (§1.4 alsoUnsequencedTo=conn) — e.g. a Stop issued for a
// delegated child session the requesting tab is not attached to.
// Best-effort: never blocks the cancel state machine.
func (h *WSHandler) sendCancelStageFrame(wc *wsConn, sessionID, stage string) {
	h.hubPublishFrame(sessionID, string(generated.WsFrameTypeCancelStage), generated.CancelStageFrame{
		Type:      string(generated.WsFrameTypeCancelStage),
		SessionId: sessionID,
		Stage:     stage,
	}, wc)
}

// sendCancelReportFrame is sendCancelStageFrame for ADR-091 I-6's detached
// stage, carrying the cascade's report. Like every cancel_stage it is
// published once through the session hub (#823: numbered, journaled, seen by
// every bound tab), with an unsequenced copy for an unbound requester.
func (h *WSHandler) sendCancelReportFrame(wc *wsConn, sessionID, stage string, report steer.CancelReport) {
	// `partial` means one thing only: the cascade could NOT reach part of the
	// subtree. WP-D FR-D-001 — "MUST report unreachable ones as partial — in
	// the report, on the Stop response frame, and as one line on the
	// originating channel" — and every other `partial` sentence in
	// adr-091-wp-d-cancel-cascade-spec.md (US-1 AS-4, the "Partial reported"
	// exact check, the BDD scenario, the integration-boundary table) names
	// the unreadable/unreachable branch and nothing else.
	//
	// SkippedNewerGeneration is deliberately NOT part of this predicate. US-1
	// AS-9's whole expected outcome is "the registry refuses it, B's
	// generation-2 turn keeps running, and the report lists B under
	// SkippedNewerGeneration" — no partial, no channel line. A revival that
	// landed after the Stop is the LATER instruction, and ADR-091 D8 settles
	// what that means: "the later instruction wins, which is what the
	// operator asked for". Cancelling it anyway is listed among the ADR's
	// prohibitions. Flagging a correct outcome as a partial failure is
	// over-signalling, and it costs nothing in honesty: the frame carries
	// `skipped_newer_generation` as its own array, so a client sees exactly
	// which sessions kept running whatever `partial` says.
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
	h.hubPublishFrame(sessionID, string(generated.WsFrameTypeCancelStage), frame, wc)
}

// cancelPartialSummary renders the one-line PARTIAL-STOP notice — the line
// ADR-091 Q21 sends to the channel the Stop came from, and the same wording
// the SPA sees alongside `partial: true`. Its subject is exactly what the
// cascade could not reach, so it is empty for a cascade that reached
// everything it was allowed to touch.
//
// WP-D pins both the gate and the wording: FR-D-001 ("MUST report unreachable
// ones as partial ... as one line on the originating channel"), the
// "Partial reported" machine-verifiable check ("exactly one line on the
// originating channel: \"stopped 3 of 5; 2 unreachable\"") and the US-1/AS-4
// BDD scenario ("Telegram receives one line: \"stopped 2 of 3; 1
// unreachable\"").
//
// SkippedNewerGeneration is not a partial failure and is not named here — see
// sendCancelReportFrame's comment for why. A caller that needs the broader
// "is anything under this node still running?" question answered — the REST
// delete guard, which must not delete session data out from under a live turn
// — wants cancelIncompleteSubtreeSummary below instead.
func cancelPartialSummary(report steer.CancelReport) string {
	if len(report.Unreachable) == 0 {
		return ""
	}
	// Count sessions, not list entries. The cascade lists a session as Reached
	// when its Stop is stamped and as Unreachable when firing the live cancel
	// then fails, so one session can sit in both lists (and a session can be
	// unreachable for more than one reason). It is unreachable, once.
	unreachable := make(map[string]struct{}, len(report.Unreachable))
	treeUnlisted := false
	for _, item := range report.Unreachable {
		if item.HelperTreeUnlisted {
			// Not a failure of item.ID's own Stop: reported on its own below.
			treeUnlisted = true
			continue
		}
		unreachable[item.ID] = struct{}{}
	}
	stopped := make(map[string]struct{}, len(report.Reached))
	for _, id := range report.Reached {
		if _, failed := unreachable[id]; !failed {
			stopped[id] = struct{}{}
		}
	}
	summary := fmt.Sprintf("stopped %d of %d", len(stopped), len(stopped)+len(unreachable))
	if len(unreachable) > 0 {
		summary += fmt.Sprintf("; %d unreachable", len(unreachable))
	}
	if treeUnlisted {
		summary += "; helper sessions could not be listed"
	}
	return summary
}

// cancelIncompleteSubtreeSummary answers a DIFFERENT question from
// cancelPartialSummary: not "was this Stop partial?" but "could any session
// under this node still be running?". Only the second question justifies
// refusing to delete session data, so a node the cascade correctly left alone
// because a newer generation had taken over counts here even though it does
// not count as partial. Empty when the cascade left nothing running.
func cancelIncompleteSubtreeSummary(report steer.CancelReport) string {
	summary := cancelPartialSummary(report)
	if len(report.SkippedNewerGeneration) == 0 {
		return summary
	}
	stillRunning := fmt.Sprintf("%d already advanced to a newer generation and are still running",
		len(report.SkippedNewerGeneration))
	if summary == "" {
		return "Stop cascade incomplete: " + stillRunning
	}
	return summary + "; " + stillRunning
}

// sendCancelPartialNotice publishes the one-line PARTIAL-STOP notice as a
// session error frame through the session hub (#823: every bound tab sees
// it, numbered and journaled), with an unsequenced copy for an unbound
// requester — the same delivery rule as the cancel_stage frame it
// accompanies.
func (h *WSHandler) sendCancelPartialNotice(wc *wsConn, sessionID string, report steer.CancelReport) {
	message := cancelPartialSummary(report)
	if message == "" {
		return
	}
	sid := sessionID
	h.hubPublishFrame(sessionID, string(generated.WsFrameTypeError), generated.ErrorFrame{
		Type:      string(generated.WsFrameTypeError),
		SessionId: &sid,
		Message:   message,
	}, wc)
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

// buildCancelHooks constructs the transport-specific agent.CancelHooks for a
// web-SPA-originated cancel. wc is the live connection to notify via
// cancel_stage frames — nil when there is no live connection to notify.
//
// A nil wc is a legitimate call shape whenever a cancel is triggered with no
// live connection to acknowledge to. sendCancelStageFrame still publishes to
// the session hub on a nil wc (every bound tab sees the stage), so the SAME hook set handleCancel builds for a real
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
				h.sendCancelReportFrame(wc, sid, stage, *report)
				return
			}
			h.sendCancelStageFrame(wc, sid, stage)
		},
		CancelPendingApprovals: func(sid, reason string) {
			if h.approvalRegV2 == nil {
				return
			}
			// ADR-057 FR-032/W10c: cancel over the DESCENDANT SET, not the
			// single chat/root id. Pending approvals match by EXACT equality on
			// the acting session id (FR-080), so a chat-level Stop that passed
			// only the root id would cancel nothing inside a live delegated
			// child (BDD-33); the durable lifecycle store's edges make the walk
			// authoritative whether or not the child is still running. The old single-id
			// cancelAllPendingForSession still compiles and remains correct
			// for a session with no descendants (its own doc comment says
			// so); this call site is the one FR-032 requires use the plural
			// form.
			var lifecycleStore *session.LifecycleStore
			if h.agentLoop != nil {
				lifecycleStore = h.agentLoop.GetSessionLifecycleStore()
			}
			// [FIX-5, Defect 2, 2026-08-03] Calls agent.CollectDescendantSessionIDs
			// directly so this call site can react to a partial-walk failure with the specific,
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
				// A cancel is a stop, not a failure — sub-agent control
				// plane ADR D4/MAJ-009: stopped stays coarse-active
				// (StatusInterrupted is for a restart-cut session only — the boot
				// sweep writes it; a cancel never does).
				status := session.StatusActive
				if err := store.SetMeta(sid, session.MetaPatch{Status: &status}); err != nil {
					slog.Warn("ws: could not mark session active",
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
		OnStopSettled: func(sid string, err error) {
			if err != nil && wc != nil {
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type: string(generated.WsFrameTypeError), SessionId: &sid,
					Message: fmt.Sprintf("Stop for session %s could not finish; required storage or notice publication failed. Retry after storage is repaired.", sid),
				})
			}
		},
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

// handleCancelFrame is the frame dispatcher's entry point for a `cancel`
// frame: parse, validate session_id, resolve the session/tree scope, and
// route to handleCancelWithScope. Kept out of dispatchFrame's own body —
// same reasoning as stringPtrOrEmpty and handleSessionModeUpdateFrame
// elsewhere in that switch — so dispatchFrame's
// grandfathered gocyclo budget (scripts/budgets/gocyclo.txt) doesn't grow;
// the session-vs-tree scope check (stopAll) was the one that pushed it over.
func (wh *wsHandlerReadLoop) handleCancelFrame(data []byte) wsHandlerReadLoopFlow {
	var f generated.CancelFrame
	if err := json.Unmarshal(data, &f); err != nil {
		slog.Warn("ws: malformed cancel frame", "error", err)
		return wsHandlerReadLoopContinue
	}
	if f.SessionId == "" {
		wh.wc.inboundDropped.Add(1)
		slog.Warn("ws: cancel frame missing required session_id — dropping",
			"chat_id", wh.chatID)
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "cancel requires session_id",
		})
		return wsHandlerReadLoopContinue
	}
	stopAll := f.Scope != nil && *f.Scope == "tree"
	wh.h.handleCancelWithScope(wh.wc, f.SessionId, stopAll)
	return wsHandlerReadLoopNext
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
	h.handleCancelWithScope(wc, sessionID, false)
}

func (h *WSHandler) handleCancelWithScope(wc *wsConn, sessionID string, stopAll bool) {
	if sessionID == "" {
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "cancel requires session_id",
		})
		return
	}

	report, cascaded, outcome, staged, err := h.requestScopedStop(wc, sessionID, stopAll)
	if cascaded {
		h.sendCancelPartialNotice(wc, sessionID, report)
		h.sendExternalCancelPartialNotice(context.Background(), sessionID, report)
	}

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
			h.sendCancelReportFrame(wc, sessionID, "detached", report)
		} else if outcome.BackgroundSessionsKilled > 0 || outcome.Armed {
			h.sendCancelStageFrame(wc, sessionID, "graceful")
		}
		return
	}
	if cascaded && !staged {
		// The Stop fired without a running turn of its own (a queued or
		// never-ran session settled at once), so no stop timeline will send
		// the requester a stage: acknowledge it with the cascade's report.
		h.sendCancelReportFrame(wc, sessionID, "detached", report)
	}
}
