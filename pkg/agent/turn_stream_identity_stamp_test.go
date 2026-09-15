package agent

// producerAgentIDMockStreamer is a bus.Streamer that also records
// SetProducerAgentID, SetTurnID, and SetParentSpawnCallID calls, mirroring
// *gateway.wsStreamer's methods of the same names. It lets
// stampStreamerProducerAgentID's, stampStreamerTurnID's, and
// stampStreamerParentSpawnCallID's type-assertions be exercised without
// importing the gateway package.
type producerAgentIDMockStreamer struct {
	mockStreamer
	setProducerAgentIDCalls   []string
	setTurnIDCalls            []string
	setParentSpawnCallIDCalls []string
}

func (m *producerAgentIDMockStreamer) SetProducerAgentID(agentID string) {
	m.setProducerAgentIDCalls = append(m.setProducerAgentIDCalls, agentID)
}

// SetTurnID mirrors *gateway.wsStreamer.SetTurnID's own no-op-on-empty
// guard, so tests exercising stampStreamerTurnID's caller-side behavior
// (which — unlike stampStreamerProducerAgentID — has no nil-pointer reason
// to skip the call itself, and so unconditionally forwards to the streamer,
// relying on the streamer's own empty check) observe production-equivalent
// results.
func (m *producerAgentIDMockStreamer) SetTurnID(turnID string) {
	if turnID == "" {
		return
	}
	m.setTurnIDCalls = append(m.setTurnIDCalls, turnID)
}

// SetParentSpawnCallID mirrors *gateway.wsStreamer.SetParentSpawnCallID's own
// semantics exactly: UNLIKE SetTurnID, it does NOT no-op on empty — an empty
// parentSpawnCallID is the valid, common "root turn, not a delegation child"
// value, so every call (including empty-string ones) is recorded.
func (m *producerAgentIDMockStreamer) SetParentSpawnCallID(parentSpawnCallID string) {
	m.setParentSpawnCallIDCalls = append(m.setParentSpawnCallIDCalls, parentSpawnCallID)
}
