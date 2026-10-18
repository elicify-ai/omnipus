// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }

// workspace loads a workspace or returns 404 / storage errors.
func (s *Service) workspace(workspaceID string) (WorkspaceInfo, error) {
	ws, found, err := s.deps.LookupWorkspace(workspaceID)
	if err != nil {
		return WorkspaceInfo{}, storageError("the workspace could not be read", err)
	}
	if !found {
		return WorkspaceInfo{}, notFound("no such workspace")
	}
	return ws, nil
}

func agentIDs(ws WorkspaceInfo) []string {
	out := make([]string, 0, len(ws.Agents))
	for _, a := range ws.Agents {
		out = append(out, a.ID)
	}
	return out
}

func agentName(ws WorkspaceInfo, id string) string {
	for _, a := range ws.Agents {
		if a.ID == id {
			return a.Name
		}
	}
	return id
}

// List returns the workspace's rooms (team room first, FR-174). A storage or
// read failure is an error (500), never an empty list (#1314).
func (s *Service) List(workspaceID string) ([]generated.Room, error) {
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	ws, err := s.workspace(workspaceID)
	if err != nil {
		return nil, err
	}
	rs, err := s.workspaceRooms(ws)
	if err != nil {
		return nil, err
	}
	out := make([]generated.Room, 0, len(rs))
	for _, r := range rs {
		r.mu.Lock()
		out = append(out, s.roomView(r, ws))
		r.mu.Unlock()
	}
	return out, nil
}

// Get returns one room.
func (s *Service) Get(roomID string) (generated.Room, error) {
	r, ws, err := s.openRoom(roomID)
	if err != nil {
		return generated.Room{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return s.roomView(r, ws), nil
}

// openRoom loads a room and its workspace and brings its membership up to
// date with the workspace (FR-160, FR-162).
func (s *Service) openRoom(roomID string) (*room, WorkspaceInfo, error) {
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	r, err := s.loadRoom(roomID)
	if err != nil {
		return nil, WorkspaceInfo{}, err
	}
	ws, err := s.workspace(r.rec.WorkspaceID)
	if err != nil {
		if StatusOf(err) == 404 {
			return nil, WorkspaceInfo{}, notFound("no such group chat")
		}
		return nil, WorkspaceInfo{}, err
	}
	r.mu.Lock()
	if r.deleted {
		r.mu.Unlock()
		return nil, WorkspaceInfo{}, notFound("no such group chat")
	}
	if err := s.repairIncompleteCost(r); err != nil {
		r.mu.Unlock()
		return nil, WorkspaceInfo{}, err
	}
	if err := s.repairReleasedConsumes(r); err != nil {
		r.mu.Unlock()
		return nil, WorkspaceInfo{}, err
	}
	if err := s.repairSettlements(r); err != nil {
		r.mu.Unlock()
		return nil, WorkspaceInfo{}, err
	}
	if err := s.reconcile(r, ws, PrincipalSystem); err != nil {
		r.mu.Unlock()
		return nil, WorkspaceInfo{}, err
	}
	if err := s.sweepStale(r); err != nil {
		r.mu.Unlock()
		return nil, WorkspaceInfo{}, err
	}
	resume := s.unsentMentionsLocked(r)
	r.mu.Unlock()
	if len(resume) > 0 {
		s.inflight.Add(1)
		go func() {
			defer s.inflight.Done()
			s.admitAll(r, ws, resume)
		}()
	}
	return r, ws, nil
}

// unsentMentionsLocked returns, once per loaded room, the mention deliveries
// of mirrored answers that a crash left unsent (recorded after the answer was
// copied to the main chat, before they were admitted): no person will retry
// an agent's answer, so the room sends them itself (FR-216). The caller holds
// r.mu.
func (s *Service) unsentMentionsLocked(r *room) []string {
	if r.resumed {
		return nil
	}
	r.resumed = true
	var out []string
	for _, e := range r.entries {
		if e.AnswersRequest == "" || !r.mirrored[e.Entry.Id] {
			continue
		}
		for _, id := range r.byEntry[e.Entry.Id] {
			if d := r.deliveries[id]; d.State == StateRecorded && d.Generation == r.rec.Generation {
				out = append(out, id)
			}
		}
	}
	return out
}

// sweepStale repairs the labels of requests a pause or a member removal
// already fenced off but could not mark cancelled when it ran (a failed
// write is reported to the person who paused or removed; the dequeue fence
// refuses those requests either way). The caller holds r.mu.
func (s *Service) sweepStale(r *room) error {
	for _, id := range r.order {
		d := r.deliveries[id]
		member := slices.Contains(r.rec.Members, d.AgentID)
		if !cancellable(d) || (member && d.Generation >= r.rec.Generation) {
			continue
		}
		reason := ReasonPaused
		if !member {
			reason = ReasonRemoved
		}
		if err := s.cancelDelivery(r, d, reason); err != nil {
			return storageError("The change was saved, but pending requests could not be marked cancelled", err)
		}
	}
	return nil
}

// workspaceRooms loads, reconciles and orders the workspace's rooms, making
// sure its team room exists once two conversation agents do (FR-160).
func (s *Service) workspaceRooms(ws WorkspaceInfo) ([]*room, error) {
	return s.workspaceRoomsAs(ws, PrincipalSystem)
}

// workspaceRoomsAs is workspaceRooms with the actor its membership changes
// are audited under (the signed-in user of a workspace change, FR-172).
func (s *Service) workspaceRoomsAs(ws WorkspaceInfo, principal string) ([]*room, error) {
	if err := s.loadAll(); err != nil {
		return nil, err
	}
	if err := s.ensureTeamRoom(ws, principal); err != nil {
		return nil, err
	}
	s.mu.Lock()
	var rs []*room
	for _, r := range s.rooms {
		if !r.deleted && r.rec.WorkspaceID == ws.ID {
			rs = append(rs, r)
		}
	}
	s.mu.Unlock()
	// #1314: the list is answered from storage that can be read NOW. A room
	// whose record cannot be read fails the list (500); it is never left out.
	for _, r := range rs {
		var probe roomRecord
		if err := s.journal.ReadRecord(r.rec.ID, &probe); err != nil {
			return nil, storageError("the group chats could not be read", err)
		}
	}
	for _, r := range rs {
		r.mu.Lock()
		err := s.repairIncompleteCost(r)
		if err == nil {
			err = s.repairReleasedConsumes(r)
		}
		if err == nil {
			err = s.repairSettlements(r)
		}
		if err == nil {
			err = s.reconcile(r, ws, principal)
		}
		if err == nil {
			err = s.sweepStale(r)
		}
		r.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	sortRooms(rs)
	return rs, nil
}

// ApplyWorkspaceUpdate commits a workspace save and applies its room roster
// while room reads cannot reconcile it first as system (FR-172). A failure
// after commit is a partial change: the saved workspace is not rolled back.
func (s *Service) ApplyWorkspaceUpdate(workspaceID, principal string, commit func() error) error {
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	if err := commit(); err != nil {
		return err
	}
	return s.syncWorkspace(workspaceID, principal)
}

// SyncWorkspace reconciles an already-stored workspace, preserving an explicit
// actor (boot/on-read callers use PrincipalSystem). Failures reach the caller.
func (s *Service) SyncWorkspace(workspaceID, principal string) error {
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	return s.syncWorkspace(workspaceID, principal)
}

func (s *Service) syncWorkspace(workspaceID, principal string) error {
	ws, err := s.workspace(workspaceID)
	if err != nil {
		return err
	}
	_, err = s.workspaceRoomsAs(ws, principal)
	return err
}

// ensureTeamRoom creates the workspace's one team room the first time it has
// two conversation agents. Below two an existing team room stays (hidden,
// read-only, history kept).
func (s *Service) ensureTeamRoom(ws WorkspaceInfo, principal string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rooms {
		if !r.deleted && r.rec.WorkspaceID == ws.ID && r.rec.Kind == KindTeam {
			return nil
		}
	}
	if len(ws.Agents) < 2 {
		return nil
	}
	rec := roomRecord{
		ID: uuid.NewString(), WorkspaceID: ws.ID, Kind: KindTeam, Name: teamRoomName,
		Members: agentIDs(ws), Muted: []string{}, CreatedAt: s.now(),
	}
	r, err := s.createRecord(rec)
	if err != nil {
		return err
	}
	s.rooms[rec.ID] = r
	s.deps.Audit(AuditEvent{RoomID: rec.ID, WorkspaceID: ws.ID, Action: "team_room_created", AgentIDs: rec.Members, Principal: principal})
	return nil
}

// createRecord writes a new room's directory and record.
func (s *Service) createRecord(rec roomRecord) (*room, error) {
	if err := s.journal.Create(rec.ID); err != nil {
		return nil, storageError("the group chat could not be created", err)
	}
	r := newRoom()
	r.rec = rec
	r.resumed = true // a new room has nothing a crash left unsent
	if err := s.persistRecord(r); err != nil {
		if rmErr := s.journal.Remove(rec.ID); rmErr != nil {
			slog.Error("rooms: a room whose record failed could not be removed", "room_id", rec.ID, "error", rmErr)
		}
		return nil, err
	}
	return r, nil
}

// reconcile applies the workspace's current membership to a room: the team
// room's members ARE the workspace's conversation agents; a squad or ad-hoc
// room loses members that left the workspace. Departures cancel their
// not-yet-consumed requests ("cancelled — removed") and are audited. The
// caller holds r.mu.
func (s *Service) reconcile(r *room, ws WorkspaceInfo, principal string) error {
	current := agentIDs(ws)
	var next []string
	if r.rec.Kind == KindTeam {
		next = current
	} else {
		for _, id := range r.rec.Members {
			if slices.Contains(current, id) {
				next = append(next, id)
			}
		}
	}
	if slices.Equal(next, r.rec.Members) {
		return nil
	}
	removed, added := diff(r.rec.Members, next)
	before := r.rec.clone()
	r.rec.Members = nonNil(next)
	// Mutes are kept for members who left: a hidden team room keeps its mute
	// preferences and gets them back when the agent rejoins (FR-160).
	if err := s.publishMeta(r, ws); err != nil {
		r.rec = before
		return err
	}
	if len(removed) > 0 {
		s.deps.Audit(AuditEvent{RoomID: r.rec.ID, WorkspaceID: ws.ID, Action: "members_left_workspace", AgentIDs: removed, Principal: principal})
	}
	if len(added) > 0 {
		s.deps.Audit(AuditEvent{RoomID: r.rec.ID, WorkspaceID: ws.ID, Action: "members_joined_workspace", AgentIDs: added, Principal: principal})
	}
	// The membership change is saved: the departed members' pending requests
	// are now cancelled (a failed label write is repaired by the next sweep;
	// the dequeue fence refuses them either way).
	if err := s.cancelMembers(r, removed, ReasonRemoved); err != nil {
		return storageError("The members are removed, but pending requests could not be marked cancelled", err)
	}
	return nil
}

// diff returns the ids in before but not after, and in after but not before.
func diff(before, after []string) (removed, added []string) {
	for _, id := range before {
		if !slices.Contains(after, id) {
			removed = append(removed, id)
		}
	}
	for _, id := range after {
		if !slices.Contains(before, id) {
			added = append(added, id)
		}
	}
	return removed, added
}

// keep returns the ids of in that are also in allowed.
func keep(in, allowed []string) []string {
	out := []string{}
	for _, id := range in {
		if slices.Contains(allowed, id) {
			out = append(out, id)
		}
	}
	return out
}

// cancelMembers cancels every not-yet-consumed request of the given members.
// It returns the first failure to record a cancellation. The caller holds
// r.mu.
func (s *Service) cancelMembers(r *room, agentIDs []string, reason string) error {
	var first error
	for _, id := range r.order {
		if d := r.deliveries[id]; slices.Contains(agentIDs, d.AgentID) && cancellable(d) {
			if err := s.cancelDelivery(r, d, reason); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

// cancelDelivery cancels one not-yet-consumed request: its approval wait is
// ended, an admitted request is discarded from the member's ledger (so it
// can never be dequeued or answered), and the room shows it cancelled. The
// caller holds r.mu.
func (s *Service) cancelDelivery(r *room, d *deliveryRecord, reason string) error {
	if cancel, ok := r.cancels[d.RequestID]; ok {
		cancel()
		delete(r.cancels, d.RequestID)
	}
	if d.State == StateAwaitingApproval {
		// The member's card is withdrawn now, not when the waiting admission
		// next runs: a late click finds nothing to approve (FR-251).
		s.deps.CancelApproval(d.RequestID)
	}
	if d.SessionID != "" {
		if err := s.deps.Discard(d.SessionID, d.RequestID); err != nil {
			// The dequeue fence still refuses it (BeginRoomConsume).
			slog.Error("rooms: a cancelled request could not be discarded from the member's ledger; the dequeue fence still refuses it",
				"room_id", r.rec.ID, "request_id", d.RequestID, "error", err)
		}
	}
	// A failed label write is returned to the caller: the request is fenced
	// off (the dequeue fence re-checks pause, membership and deletion) but
	// the room would still show its old state.
	return s.setState(r, d, StateCancelled, reason)
}

// validateNewMembers checks agent ids against the workspace: known
// conversation agents of this workspace only (FR-162).
func (s *Service) validateNewMembers(ws WorkspaceInfo, ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, badRequest("agent id is empty")
		}
		if !slices.Contains(agentIDs(ws), id) {
			return nil, unprocessable("%s is not in %s", s.agentLabel(id), workspaceLabel(ws))
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

func workspaceLabel(ws WorkspaceInfo) string {
	if strings.TrimSpace(ws.Name) != "" {
		return ws.Name
	}
	return ws.ID
}

// agentLabel names an agent for a refusal: its display name when known.
func (s *Service) agentLabel(id string) string {
	if name, ok := s.deps.AgentName(id); ok && strings.TrimSpace(name) != "" {
		return name
	}
	return id
}
