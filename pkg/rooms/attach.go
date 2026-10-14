// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
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

// snapshot builds the RoomSnapshotFrame. `complete` is false when more
// entries follow the cursor than one snapshot carries. The caller holds r.mu.
func (s *Service) snapshot(r *room, ws Workspace, afterSeq int64) generated.RoomSnapshotFrame {
	view := s.roomView(r, ws)
	out := generated.RoomSnapshotFrame{
		Type: string(generated.WsFrameTypeRoomSnapshot), RoomId: r.rec.ID, Seq: r.seq,
		Entries: []generated.RoomEntry{}, Members: view.Members, Muted: view.Muted,
		MemberStates: []generated.RoomDeliveryState{}, Paused: view.Paused, Generation: view.Generation,
		ReadOnlyReason:        view.ReadOnlyReason,
		AllocatedCostMicroUsd: view.AllocatedCostMicroUsd, AllocatedCostUnpriced: view.AllocatedCostUnpriced,
		Complete: true,
	}
	if afterSeq < 0 {
		afterSeq = 0
	}
	for _, se := range r.entries {
		if se.Entry.Seq <= afterSeq {
			continue
		}
		if len(out.Entries) >= maxSnapshotEntries {
			out.Complete = false
			break
		}
		out.Entries = append(out.Entries, entryWire(r, se))
	}
	latest := latestByMember(r)
	for _, id := range r.order {
		d := r.deliveries[id]
		if !terminal(d.State) || latest[d.AgentID] == d {
			if len(out.MemberStates) >= maxSnapshotEntries {
				out.Complete = false
				break
			}
			out.MemberStates = append(out.MemberStates, deliveryWire(d))
		}
	}
	return out
}
