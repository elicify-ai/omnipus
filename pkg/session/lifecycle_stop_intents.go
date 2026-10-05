package session

// UnfinishedStopIntent is an accepted stop control whose effect never took
// hold: its ledger line is still queued, it has no landed history, and the
// session's record does not carry its fence. D4's crash state ("a stamp or
// persist refusal after a durable intent") leaves exactly this; the agent's
// finisher completes or supersedes it.
//
// not-wire-format: internal runtime targeting only.
type UnfinishedStopIntent struct {
	Selection StopSelection
	Cause     StopCause
	Actor     string
}

// UnfinishedStopIntents returns sessionID's unfinished stop intents in ledger
// order. A missing ledger is empty; an unreadable ledger is a visible error.
func (s *LifecycleStore) UnfinishedStopIntents(sessionID string) ([]UnfinishedStopIntent, error) {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return nil, err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()
	lines, err := s.readControlLedgerLocked(sessionID)
	if err != nil || len(lines) == 0 {
		return nil, err
	}
	rec, found, err := s.tail(sessionID)
	if err != nil {
		return nil, err
	}
	type state struct {
		first  controlLedgerLine
		last   string
		landed bool
	}
	order := make([]string, 0, len(lines))
	byID := make(map[string]*state)
	for _, line := range lines {
		if line.Verb != controlVerbStop || line.StopEffect == nil || line.ControlID == "" {
			continue
		}
		st, ok := byID[line.ControlID]
		if !ok {
			st = &state{first: line}
			byID[line.ControlID] = st
			order = append(order, line.ControlID)
		}
		st.last = line.State
		if line.LandedStop != nil {
			st.landed = true
		}
	}
	var out []UnfinishedStopIntent
	for _, id := range order {
		st := byID[id]
		if st.landed || st.last != controlStateQueued {
			continue
		}
		if found && rec.StopEffect != nil && rec.StopEffect.ControlID == id {
			continue // its fence is on the record: the owner settles it
		}
		out = append(out, UnfinishedStopIntent{
			Selection: StopSelection{SessionID: sessionID, Effect: *st.first.StopEffect},
			Cause:     st.first.Cause,
			Actor:     st.first.Actor,
		})
	}
	return out, nil
}
