package agent

import "github.com/elicify-ai/omnipus/pkg/providers"

// dequeueSteeringForContinuation only takes instructions this turn can still
// inject into another provider round. At the hard ceiling the existing
// post-turn drain (sessionWorker.processTurn / drainSteeredTurn) owns them;
// leaving the original queue items intact also preserves wakes and receipts.
func (ri *agentLoopRunTurnIteration) dequeueSteeringForContinuation() ([]providers.Message, []string) {
	ts := ri.rf.rt.ts
	if ts.currentIteration() >= 2*ts.agent.MaxIterations {
		return nil, nil
	}
	return ri.rf.rt.al.dequeueSteeringMessagesForScope(ts.sessionKey)
}
