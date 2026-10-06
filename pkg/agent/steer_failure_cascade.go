// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// stopDescendantsOfFailedSession is ADR-20260928 D6's "Failure of a parent
// is different from its self-only Stop" (#1053): when a steered session lands
// a genuine failed outcome, each of its reachable active helpers is stopped
// through the one Stop (Stop all per direct child, cause cascade), without
// waiting for them (#947's no-hang). It runs before the failed commit, so the
// summary it returns can name every helper and its stop result in the fatal
// hand-back; an incomplete stop is named explicitly, never claimed done.
// Helpers that are already stopped or terminal are left as they are.
func (al *AgentLoop) stopDescendantsOfFailedSession(ctx context.Context, rec *session.LifecycleRecord) string {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil || rec == nil {
		return ""
	}
	children, err := lifecycle.List(session.LifecycleFilter{SteeringSessionID: rec.SessionID, NonTerminalOnly: true})
	if err != nil {
		return fmt.Sprintf(" Its helpers could not be listed, so none were stopped: %v.", err)
	}
	var stopped, stopping, incomplete []string
	// A7: "stopped" only for a helper whose Stop has LANDED; a reached
	// helper whose running work is still shutting down is "stopping".
	classify := func(id string) {
		if cur, loadErr := lifecycle.Load(id); loadErr == nil && cur.State == session.LifecycleStopped {
			stopped = append(stopped, id)
			return
		}
		stopping = append(stopping, id)
	}
	for _, child := range children {
		if child.State == session.LifecycleStopped {
			continue
		}
		res, stopErr := al.StopSession(ctx, StopRequest{
			SessionID: child.SessionID,
			By:        steer.Principal{Kind: steer.PrincipalKindAgent, ID: rec.SessionID},
			Channel:   "agent",
			Tree:      true,
			Cause:     session.StopCauseCascade,
		})
		switch {
		case stopErr != nil:
			incomplete = append(incomplete, fmt.Sprintf("%s (%v)", child.SessionID, stopErr))
			continue
		case res.RootErr != nil:
			incomplete = append(incomplete, fmt.Sprintf("%s (%v)", child.SessionID, res.RootErr))
		}
		for _, item := range res.Report.Unreachable {
			// An intent accepted durably but never fenced is retried at once
			// and again at boot (D6's durable retry item, D4's finisher).
			if retryErr := al.finishUnfinishedStopIntents(ctx, item.ID); retryErr == nil {
				if cur, loadErr := lifecycle.Load(item.ID); loadErr == nil && cur.Stopped() {
					classify(item.ID)
					continue
				}
			}
			incomplete = append(incomplete, fmt.Sprintf("%s (%s)", item.ID, item.Reason))
		}
		for _, id := range res.Report.Reached {
			if res.RootErr != nil && id == child.SessionID {
				continue
			}
			classify(id)
		}
	}
	var b strings.Builder
	if len(stopped) > 0 {
		fmt.Fprintf(&b, " Its helpers were stopped: %s.", strings.Join(stopped, ", "))
	}
	if len(stopping) > 0 {
		fmt.Fprintf(&b, " Stop was requested for these helpers; they are still shutting down: %s.", strings.Join(stopping, ", "))
	}
	if len(incomplete) > 0 {
		fmt.Fprintf(&b, " These helpers could not be stopped and may still be running: %s.", strings.Join(incomplete, "; "))
	}
	return b.String()
}
