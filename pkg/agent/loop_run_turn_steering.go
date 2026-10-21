package agent

import "github.com/elicify-ai/omnipus/pkg/providers"

// dequeueSteeringForContinuation only takes instructions this turn can still
// inject into another provider round. At the hard ceiling the existing
// post-turn drain (sessionWorker.processTurn / drainSteeredTurn) owns them;
// leaving the original queue items intact also preserves wakes and receipts.
// Below that ceiling, return the original items too: failed answer preservation
// must be able to restore unprocessed input before returning the save error.
func (ri *agentLoopRunTurnIteration) dequeueSteeringForContinuation() ([]providers.Message, []string, []steeringQueueItem) {
	ts := ri.rf.rt.ts
	if ts.currentIteration() >= 2*ts.agent.MaxIterations {
		return nil, nil, nil
	}
	_, items, messages, receipts := ri.rf.rt.al.dequeueSteeringItemsForScopeWithFallback(ts.sessionKey)
	ri.rf.rt.al.noteConsumedOriginsForScope(ts.sessionKey, items)
	return messages, receipts, items
}
