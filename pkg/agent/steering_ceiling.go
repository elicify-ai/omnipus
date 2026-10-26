package agent

// noteInTurnSteering remembers new input taken from the live queue, not the
// initial batch of a post-turn continuation. That distinction bounds a cutoff
// to one fresh turn per accepted live batch rather than renewing its own budget
// forever when the model keeps returning tool calls.
func (ri *agentLoopRunTurnIteration) noteInTurnSteering(items []steeringQueueItem) {
	for _, item := range items {
		if !item.resumeOnly {
			ri.inTurnSteering = true
			origin := &ri.inTurnSteeringOrigin
			if origin.channelPerson == "" && item.origin.channelPerson != "" {
				origin.channelPerson, origin.connection = item.origin.channelPerson, item.origin.connection
			}
			if origin.peerAgentID == "" {
				origin.peerAgentID = item.origin.peerAgentID
			}
			origin.approval = mergeApprovalOrigin(origin.approval, item.origin.approvalFor(ri.rf.rt.al))
		}
	}
}

func hasSteeringContinuation(items []steeringQueueItem) bool {
	for _, item := range items {
		if item.resumeOnly {
			return true
		}
	}
	return false
}

// deferUnansweredSteering leaves the existing post-turn drain a one-shot wake
// for input already injected before the ceiling. No text, receipt or tool result
// is replayed: the next turn reads the same conversation with a fresh budget.
func (ri *agentLoopRunTurnIteration) deferUnansweredSteering() {
	ts := ri.rf.rt.ts
	sq := ri.rf.rt.al.steering
	if !ri.inTurnSteering || sq == nil || ts.hardAbortRequested() {
		return
	}
	sq.mu.Lock()
	defer sq.mu.Unlock()
	scope := normalizeSteeringScope(ts.sessionKey)
	if ts.stopRequested.Load() {
		return
	}
	if _, closed := sq.closedGenerations[scope]; closed {
		return
	}
	// This is control flow, not another waiting user message. Like a wake,
	// it must not lose accepted input to the ordinary queue's capacity bound.
	sq.queues[scope] = append([]steeringQueueItem{{resumeOnly: true, origin: ri.inTurnSteeringOrigin}}, sq.queues[scope]...)
}
