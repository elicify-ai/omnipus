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
func (s *Service) workspace(workspaceID string) (Workspace, error) {
	ws, found, err := s.deps.Workspace(workspaceID)
	if err != nil {
		return Workspace{}, storageError("the workspace could not be read", err)
	}
	if !found {
		return Workspace{}, notFound("no such workspace")
	}
	return ws, nil
}

func agentIDs(ws Workspace) []string {
	out := make([]string, 0, len(ws.Agents))
	for _, a := range ws.Agents {
		out = append(out, a.ID)
	}
	return out
}

func agentName(ws Workspace, id string) string {
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
func (s *Service) openRoom(roomID string) (*room, Workspace, error) {
	r, err := s.loadRoom(roomID)
	if err != nil {
		return nil, Workspace{}, err
	}
	ws, err := s.workspace(r.rec.WorkspaceID)
	if err != nil {
		if StatusOf(err) == 404 {
			return nil, Workspace{}, notFound("no such group chat")
		}
		return nil, Workspace{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deleted {
		return nil, Workspace{}, notFound("no such group chat")
	}
	if err := s.reconcile(r, ws); err != nil {
		return nil, Workspace{}, err
	}
	return r, ws, nil
}

// workspaceRooms loads, reconciles and orders the workspace's rooms, making
// sure its team room exists once two conversation agents do (FR-160).
func (s *Service) workspaceRooms(ws Workspace) ([]*room, error) {
	if err := s.loadAll(); err != nil {
		return nil, err
	}
	if err := s.ensureTeamRoom(ws); err != nil {
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
	for _, r := range rs {
		r.mu.Lock()
		err := s.reconcile(r, ws)
		r.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	sortRooms(rs)
	return rs, nil
}

// SyncWorkspace brings the workspace's rooms up to date with its membership
// now (called after a workspace's team changes). Errors are logged: the next
// read reconciles again.
func (s *Service) SyncWorkspace(workspaceID string) {
	ws, err := s.workspace(workspaceID)
	if err != nil {
		if StatusOf(err) != 404 {
			slog.Error("rooms: workspace sync failed; the next read retries", "workspace_id", workspaceID, "error", err)
		}
		return
	}
	if _, err := s.workspaceRooms(ws); err != nil {
		slog.Error("rooms: workspace sync failed; the next read retries", "workspace_id", workspaceID, "error", err)
	}
}

// ensureTeamRoom creates the workspace's one team room the first time it has
// two conversation agents. Below two an existing team room stays (hidden,
// read-only, history kept).
func (s *Service) ensureTeamRoom(ws Workspace) error {
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
	s.deps.Audit(AuditEvent{RoomID: rec.ID, WorkspaceID: ws.ID, Action: "team_room_created", AgentIDs: rec.Members})
	return nil
}

// createRecord writes a new room's directory and record.
func (s *Service) createRecord(rec roomRecord) (*room, error) {
	if err := s.journal.Create(rec.ID); err != nil {
		return nil, storageError("the group chat could not be created", err)
	}
	r := newRoom()
	r.rec = rec
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
func (s *Service) reconcile(r *room, ws Workspace) error {
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
	r.rec.Muted = keep(r.rec.Muted, next)
	if err := s.publishMeta(r, ws); err != nil {
		r.rec = before
		return err
	}
	for _, id := range removed {
		s.cancelMember(r, id, ReasonRemoved)
	}
	if len(removed) > 0 {
		s.deps.Audit(AuditEvent{RoomID: r.rec.ID, WorkspaceID: ws.ID, Action: "members_left_workspace", AgentIDs: removed})
	}
	if len(added) > 0 {
		s.deps.Audit(AuditEvent{RoomID: r.rec.ID, WorkspaceID: ws.ID, Action: "members_joined_workspace", AgentIDs: added})
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

// cancelMember cancels every not-yet-consumed request of one member. The
// caller holds r.mu.
func (s *Service) cancelMember(r *room, agentID, reason string) {
	for _, id := range r.order {
		if d := r.deliveries[id]; d.AgentID == agentID && cancellable(d) {
			s.cancelDelivery(r, d, reason)
		}
	}
}

// cancelDelivery cancels one not-yet-consumed request: its approval wait is
// ended, an admitted request is discarded from the member's ledger (so it
// can never be dequeued or answered), and the room shows it cancelled. The
// caller holds r.mu.
func (s *Service) cancelDelivery(r *room, d *deliveryRecord, reason string) {
	if cancel, ok := r.cancels[d.RequestID]; ok {
		cancel()
		delete(r.cancels, d.RequestID)
	}
	if d.SessionID != "" {
		if err := s.deps.Discard(d.SessionID, d.RequestID); err != nil {
			// The dequeue fence still refuses it (BeginRoomConsume).
			slog.Error("rooms: a cancelled request could not be discarded from the member's ledger; the dequeue fence still refuses it",
				"room_id", r.rec.ID, "request_id", d.RequestID, "error", err)
		}
	}
	if err := s.setState(r, d, StateCancelled, reason); err != nil {
		slog.Error("rooms: a cancellation could not be recorded; the dequeue fence still refuses the request",
			"room_id", r.rec.ID, "request_id", d.RequestID, "error", err)
	}
}

// validateNewMembers checks agent ids against the workspace: known
// conversation agents of this workspace only (FR-162).
func (s *Service) validateNewMembers(ws Workspace, ids []string) ([]string, error) {
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

func workspaceLabel(ws Workspace) string {
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
