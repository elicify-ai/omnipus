package rooms

// repairReleasedConsumes replays durable undo intent before serving or
// consuming a request. The caller holds the room lock (or is loading it).
// Ownership is forgotten only after both the queued state and intent cleanup
// are durable. A persistent failure is returned, never a healthy working label.
func (s *Service) repairReleasedConsumes(r *room) error {
	for requestID := range r.rec.PendingReleases {
		d := r.deliveries[requestID]
		if d != nil && !terminal(d.State) && d.Consumed {
			if err := s.setState(r, d, StateAdmitted, ""); err != nil {
				return storageError("The request did not reach a turn and could not yet be returned to the queue", err)
			}
		}
		delete(r.rec.PendingReleases, requestID)
		if err := s.persistRecord(r); err != nil {
			r.rec.PendingReleases[requestID] = true
			return err
		}
		if d != nil {
			s.forgetWorking(d.SessionID, requestID)
		}
	}
	return nil
}

// markCostIncomplete does not need a current workspace or membership refresh:
// the turn already incurred the charge. A room that supplied an input is
// normally already loaded; its journal remains usable on a directory-read
// failure. The durable mark survives recovery and is published on the next read.
func (s *Service) markCostIncomplete(roomID string) {
	s.recordCostGap(roomID)
	r, err := s.loadRoom(roomID)
	if err != nil {
		logRoomError("the incomplete-cost mark could not be recorded: the room is unreadable", roomID, err)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rec.CostUnpriced = true
	if err := s.publishMeta(r, WorkspaceInfo{ID: r.rec.WorkspaceID}); err != nil {
		logRoomError("the room's incomplete-cost mark could not be saved", roomID, err)
		return
	}
	s.incompleteCosts.Delete(roomID)
}

func (s *Service) repairIncompleteCost(r *room) error {
	if _, pending := s.incompleteCosts.Load(r.rec.ID); !pending {
		return nil
	}
	r.rec.CostUnpriced = true
	if err := s.persistRecord(r); err != nil {
		return err
	}
	s.incompleteCosts.Delete(r.rec.ID)
	return nil
}

// A gap marker can be appended without reading room.json. It is separate from
// turn shares: even a failed turn-stream append can leave the total durably
// incomplete, including a restart before any successful metadata read.
func (s *Service) recordCostGap(roomID string) {
	s.incompleteCosts.Store(roomID, true)
	if err := s.journal.Append(roomID, StreamCostGaps, &turnRecord{Unpriced: true, At: s.now()}); err != nil {
		logRoomError("the incomplete-cost journal mark could not be saved; it remains pending", roomID, err)
	}
}
