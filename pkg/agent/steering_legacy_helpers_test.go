package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Single-turn test conveniences. The production methods of these names were
// process-wide "act on whichever turn is first" entry points that DEL-22/24
// deleted (they are gone from every non-test file, and
// TestSessionCoreU3_CompetingIntakeDeclarationsAreGone keeps them gone). Tests
// that drive exactly one live turn still want the short form, so it lives here,
// in a _test file, and cannot reach production.

func (al *AgentLoop) getAnyActiveTurnState() *turnState {
	var firstTS *turnState
	al.activeTurnStates.Range(func(key, value any) bool {
		ts, ok := value.(*turnState)
		if !ok {
			logger.ErrorCF("agent", "activeTurnStates: invariant violated - unexpected value type, skipping entry",
				map[string]any{"key": key, "got_type": fmt.Sprintf("%T", value)})
			return true
		}
		firstTS = ts
		return false
	})
	return firstTS
}

// Steer enqueues msg for the (single) active turn's scope.
func (al *AgentLoop) Steer(msg providers.Message) error {
	scope, agentID := "", ""
	if ts := al.getAnyActiveTurnState(); ts != nil {
		scope, agentID = ts.sessionKey, ts.agentID
	}
	_, _, err := al.enqueueSteeringMessage(scope, agentID, msg, "")
	return err
}

// GetActiveTurn returns a snapshot of the (single) active turn, or nil.
func (al *AgentLoop) GetActiveTurn() *ActiveTurnInfo {
	ts := al.getAnyActiveTurnState()
	if ts == nil {
		return nil
	}
	info := ts.snapshot()
	return &info
}

// InterruptGraceful asks the (single) active turn to wind down gracefully.
func (al *AgentLoop) InterruptGraceful(hint string) error {
	ts := al.getAnyActiveTurnState()
	if ts == nil {
		return fmt.Errorf("no active turn")
	}
	if !ts.requestGracefulInterrupt(hint) {
		return fmt.Errorf("turn %s cannot accept graceful interrupt", ts.turnID)
	}
	al.emitEvent(
		EventKindInterruptReceived,
		ts.eventMeta("InterruptGraceful", "turn.interrupt.received"),
		InterruptReceivedPayload{Kind: InterruptKindGraceful, HintLen: len(hint)},
	)
	return nil
}

// InterruptHard aborts the (single) active turn.
func (al *AgentLoop) InterruptHard() error {
	ts := al.getAnyActiveTurnState()
	if ts == nil {
		return fmt.Errorf("no active turn")
	}
	if !ts.requestHardAbort() {
		return fmt.Errorf("turn %s is already aborting", ts.turnID)
	}
	al.emitEvent(
		EventKindInterruptReceived,
		ts.eventMeta("InterruptHard", "turn.interrupt.received"),
		InterruptReceivedPayload{Kind: InterruptKindHard},
	)
	return nil
}
