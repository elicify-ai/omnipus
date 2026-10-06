// websocket_stop_scope.go: both web Stop scopes end turns, never goals.

package gateway

import (
	"context"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// requestScopedStop is the web Stop surface's entry to the one session Stop
// (agent.StopSession): this session (stopAll false) or its whole helper tree.
// The returned bool reports whether a durable lifecycle cascade ran; staged
// reports whether the requested session's own stop timeline already sent
// the requester a cancel_stage frame.
func (h *WSHandler) requestScopedStop(wc *wsConn, sessionID string, stopAll bool) (report steer.CancelReport, cascaded bool, outcome agent.CancelOutcome, staged bool, err error) {
	// Timers can fire while a large durable cascade is still being collected.
	// Publish an immutable report atomically; never race a timer with appends.
	var stageReport atomic.Pointer[steer.CancelReport]
	var stageSent atomic.Bool
	var stopper agent.StopTurnsCanceller
	if c, ok := gatewaySteerCanceller(h.agentLoop).(agent.StopTurnsCanceller); ok {
		stopper = c
	}
	res, err := h.agentLoop.StopSession(context.Background(), agent.StopRequest{
		SessionID: sessionID,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: wc.userID},
		Channel:   "web",
		Tree:      stopAll,
		Canceller: stopper,
		HooksFor: func(id string) agent.CancelHooks {
			if id != sessionID {
				return h.webStopHooks(wc, nil, stopAll)
			}
			hooks := h.webStopHooks(wc, &stageReport, stopAll)
			send := hooks.SendStageFrame
			hooks.SendStageFrame = func(id, stage string) {
				stageSent.Store(true)
				send(id, stage)
			}
			return hooks
		},
	})
	if err != nil {
		return steer.CancelReport{}, false, agent.CancelOutcome{}, false, err
	}
	if !res.Durable {
		return res.Report, false, res.Root, stageSent.Load(), res.RootErr
	}
	stageReport.Store(&res.Report)
	root := res.Root
	root.BackgroundSessionsKilled = res.BackgroundKilled
	root.BackgroundSessionsFailed = res.BackgroundFailed
	return res.Report, true, root, stageSent.Load(), res.RootErr
}

// stopHooks are the web transport hooks for one reached session.
func (h *WSHandler) stopHooks(wc *wsConn, report *atomic.Pointer[steer.CancelReport]) agent.CancelHooks {
	hooks := h.buildCancelHooks(wc)
	// Each reached session receives its own RequestCancel. Do not let a
	// transport hook silently expand a this-turn Stop back into a subtree.
	hooks.CancelPendingApprovals = func(id, reason string) {
		if h.approvalRegV2 != nil {
			h.approvalRegV2.cancelAllPendingForSession(id, reason)
		}
	}
	if report != nil {
		hooks.SendStageFrame = func(id, stage string) {
			if stage == "detached" {
				if snapshot := report.Load(); snapshot != nil {
					h.sendCancelReportFrame(wc, id, stage, *snapshot)
					return
				}
			}
			h.sendCancelStageFrame(wc, id, stage)
		}
	}
	return hooks
}

// webStopHooks are stopHooks for one web Stop scope. Founder decision Q13:
// a plain Stop leaves the session's background shell processes running;
// only Stop all kills them.
func (h *WSHandler) webStopHooks(wc *wsConn, report *atomic.Pointer[steer.CancelReport], stopAll bool) agent.CancelHooks {
	hooks := h.stopHooks(wc, report)
	if !stopAll {
		hooks.KillBackgroundSessions = nil
	}
	return hooks
}
