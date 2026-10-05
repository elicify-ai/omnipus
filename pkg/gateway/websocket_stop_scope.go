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
// The returned bool reports whether a durable lifecycle cascade ran.
func (h *WSHandler) requestScopedStop(wc *wsConn, sessionID string, stopAll bool) (steer.CancelReport, bool, agent.CancelOutcome, error) {
	// Timers can fire while a large durable cascade is still being collected.
	// Publish an immutable report atomically; never race a timer with appends.
	var stageReport atomic.Pointer[steer.CancelReport]
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
			var report *atomic.Pointer[steer.CancelReport]
			if id == sessionID {
				report = &stageReport
			}
			return h.stopHooks(wc, report)
		},
	})
	if err != nil {
		return steer.CancelReport{}, false, agent.CancelOutcome{}, err
	}
	if !res.Durable {
		return res.Report, false, res.Root, res.RootErr
	}
	stageReport.Store(&res.Report)
	root := res.Root
	root.BackgroundSessionsKilled = res.BackgroundKilled
	root.BackgroundSessionsFailed = res.BackgroundFailed
	return res.Report, true, root, res.RootErr
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
