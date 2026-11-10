package gateway

// sessionTokensTotal is an optional usage observation, independent of history
// readiness. No session/usage authority means unknown, not a synthetic zero.
func (h *WSHandler) sessionTokensTotal(sessionID string) *int {
	if h == nil || h.agentLoop == nil || sessionID == "" {
		return nil
	}
	return h.resolveSessionStore(sessionID).ReadSessionTokensTotal(sessionID)
}
