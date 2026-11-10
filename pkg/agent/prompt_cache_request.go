package agent

// cacheSessionID is the conversation being executed, not the routing root or
// the agent shared by several conversations. Non-store test/CLI turns use their
// own session key when no transcript session id was supplied.
func (ts *turnState) cacheSessionID() string {
	if ts.transcriptSessionID != "" {
		return ts.transcriptSessionID
	}
	return ts.sessionKey
}
