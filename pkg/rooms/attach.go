// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"sort"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Attach answers an attach_room frame (K45) with the room's snapshot after
// the client's cursor. deliver runs under the room lock: it sends the
// snapshot and subscribes the connection before any later frame can be
// published (every frame is published under the same lock), so the client
// sees the snapshot first and misses nothing after it.
func (s *Service) Attach(roomID string, afterSeq int64, deliver func(generated.RoomSnapshotFrame)) error {
	r, ws, err := s.openRoom(roomID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deleted {
		return notFound("no such group chat")
	}
	deliver(s.snapshot(r, ws, afterSeq))
	return nil
}

// snapshot builds the RoomSnapshotFrame (architect ruling 2026-10-14): the
// NEWEST entries after the cursor, at most maxSnapshotEntries, in seq order,
// with has_older set when older ones after the cursor were left out;
// `complete` is always true (reserved). The caller holds r.mu.
func (s *Service) snapshot(r *room, ws WorkspaceInfo, afterSeq int64) generated.RoomSnapshotFrame {
	view := s.roomView(r, ws)
	out := generated.RoomSnapshotFrame{
		Type: string(generated.WsFrameTypeRoomSnapshot), RoomId: r.rec.ID, Seq: r.seq,
		Entries: []generated.RoomEntry{}, Members: view.Members, Muted: view.Muted,
		MemberStates: memberStates(r, maxSnapshotEntries), Paused: view.Paused, Generation: view.Generation,
		ReadOnlyReason:        view.ReadOnlyReason,
		AllocatedCostMicroUsd: view.AllocatedCostMicroUsd, AllocatedCostUnpriced: view.AllocatedCostUnpriced,
		Complete: true,
	}
	// r.entries is in seq order: find the first entry after the cursor.
	first := sort.Search(len(r.entries), func(i int) bool { return r.entries[i].Entry.Seq > afterSeq })
	if len(r.entries)-first > maxSnapshotEntries {
		first = len(r.entries) - maxSnapshotEntries
		hasOlder := true
		out.HasOlder = &hasOlder
	}
	for _, se := range r.entries[first:] {
		out.Entries = append(out.Entries, entryWire(r, se))
	}
	return out
}

// memberStates is every non-terminal delivery state plus, per member, its
// latest TERMINAL state (architect ruling 2026-10-14), in first-seen order.
// Over limit, the OLDEST terminal states are dropped; a live (non-terminal)
// state never is.
func memberStates(r *room, limit int) []generated.RoomDeliveryState {
	latestTerminal := map[string]*deliveryRecord{}
	liveCount := 0
	for _, id := range r.order {
		d := r.deliveries[id]
		if terminal(d.State) {
			latestTerminal[d.AgentID] = d
		} else {
			liveCount++
		}
	}
	dropTerminal := liveCount + len(latestTerminal) - limit
	out := make([]generated.RoomDeliveryState, 0, liveCount+len(latestTerminal))
	for _, id := range r.order {
		d := r.deliveries[id]
		switch {
		case !terminal(d.State):
		case latestTerminal[d.AgentID] != d:
			continue
		case dropTerminal > 0:
			dropTerminal--
			continue
		}
		out = append(out, deliveryWire(d))
	}
	return out
}
