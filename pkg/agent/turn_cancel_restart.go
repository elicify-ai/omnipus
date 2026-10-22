// turn_cancel_restart.go: the shutdown-side turn_canceled_restart marker write
// (#1178, amendment L1-AMENDMENTS-20261013.md §4.2). Extracted from
// loop.go::writeTurnCancelledRestartForActiveTurns so loop.go stays under its
// size budget and the truthful-report rule lives beside the recovery twin in
// session_recovery.go.
//
// package agent — the FR-048 marker: a graceful shutdown appends one synthetic
// system message to every session with an active turn so the next load sees a
// recorded stop instead of a resume attempt.

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// writeTurnCancelledRestartMarker writes one session's shutdown marker and
// reports the outcome truthfully (#1178, amendment §4.2): a failed write is
// logged with the session_key and the error, and is never reported as
// "written"; only a session whose marker actually landed gets the success
// log. The caller keeps going for the remaining sessions either way.
func (al *AgentLoop) writeTurnCancelledRestartMarker(ts *turnState, sessionKey, syntheticContent string) {
	if addErr := ts.agent.Sessions.AddMessage(ts.contextKey(), "system", syntheticContent); addErr != nil {
		logger.WarnCF("agent", "FR-048: failed to write turn_canceled_restart on graceful shutdown",
			map[string]any{"session_key": sessionKey, "error": addErr.Error()})
		return
	}
	if err := ts.agent.Sessions.Save(ts.contextKey()); err != nil {
		logger.WarnCF("agent", "FR-048: failed to persist turn_canceled_restart on shutdown",
			map[string]any{"session_key": sessionKey, "error": err.Error()})
		return
	}
	logger.InfoCF("agent", "FR-048: turn_canceled_restart written on graceful shutdown",
		map[string]any{"session_key": sessionKey})
}
