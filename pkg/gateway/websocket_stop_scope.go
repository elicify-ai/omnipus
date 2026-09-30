// websocket_stop_scope.go: both web Stop scopes end turns, never goals.

package gateway

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type scopedTurnStopper interface {
	StopTurns(ctx context.Context, sessionID string, by steer.Principal, subtree bool, stopTurn agent.GenerationCancelFunc) (steer.CancelReport, error)
}

func (h *WSHandler) requestScopedStop(wc *wsConn, sessionID string, stopAll bool) (steer.CancelReport, bool, agent.CancelOutcome, error) {
	ctx := context.Background()
	var rootOutcome agent.CancelOutcome
	var rootErr error
	var killed, failed int
	// Timers can fire while a large durable cascade is still being collected.
	// Publish an immutable report atomically; never race a timer with appends.
	var stageReport atomic.Pointer[steer.CancelReport]
	stopTurn := func(ctx context.Context, id string, generation int) (agent.GenerationCancelResult, error) {
		var report *atomic.Pointer[steer.CancelReport]
		if id == sessionID {
			report = &stageReport
		}
		outcome, err := h.requestTurnStop(ctx, wc, id, generation, report)
		killed += outcome.BackgroundSessionsKilled
		failed += outcome.BackgroundSessionsFailed
		if id == sessionID {
			rootOutcome, rootErr = outcome, err
		}
		return agent.GenerationCancelResult{
			Found: outcome.Fired || outcome.Armed, Cancelled: outcome.Fired,
			SkippedNewerGeneration: outcome.SkippedNewerGeneration,
		}, err
	}
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: wc.userID}
	report, durable := applySteeredCancel(h.agentLoop, sessionID, func(canceller steer.Canceller) (steer.CancelReport, error) {
		stopper, ok := canceller.(scopedTurnStopper)
		if !ok {
			return steer.CancelReport{}, fmt.Errorf("steer canceller does not support scoped Stop")
		}
		return stopper.StopTurns(ctx, sessionID, by, stopAll, stopTurn)
	})
	if !durable {
		rootOutcome, rootErr = h.requestTurnStop(ctx, wc, sessionID, 0, nil)
		return report, false, rootOutcome, rootErr
	}
	stageReport.Store(&report)
	rootOutcome.BackgroundSessionsKilled = killed
	rootOutcome.BackgroundSessionsFailed = failed
	return report, true, rootOutcome, rootErr
}

func (h *WSHandler) requestTurnStop(ctx context.Context, wc *wsConn, sessionID string, generation int, report *atomic.Pointer[steer.CancelReport]) (agent.CancelOutcome, error) {
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
	return h.agentLoop.RequestCancel(ctx, agent.CancelScope{
		SessionID: sessionID, TurnOnly: true, Generation: generation,
	}, agent.CancelCanceller{UserID: wc.userID, Channel: "web"}, hooks)
}
