package rooms

// retiredRoomContextEntry prevents a later request from quoting this member's
// interrupted demand or its derived answer as new active context. The room
// transcript stays intact; another member's independent work is not retired.
func retiredRoomContextEntry(r *room, agentID string, entry storedEntry) bool {
	for _, id := range r.byEntry[entry.Entry.Id] {
		if d := r.deliveries[id]; d != nil && d.AgentID == agentID && d.State == StateInterrupted {
			return true
		}
	}
	if d := r.deliveries[entry.AnswersRequest]; d != nil && d.AgentID == agentID && d.State == StateInterrupted {
		return true
	}
	return false
}
