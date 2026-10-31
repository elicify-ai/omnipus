// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Create creates a squad or an ad-hoc room (K24, FR-161, FR-204, FR-207):
// at least two distinct conversation agents of the workspace; a squad needs a
// name; the team room is never created here. principal is the
// authenticated web user (audited).
func (s *Service) Create(workspaceID string, req generated.RoomCreateRequest, principal string) (generated.Room, error) {
	ws, err := s.workspace(workspaceID)
	if err != nil {
		return generated.Room{}, err
	}
	kind := string(req.Kind)
	if len(req.AgentIds) > maxMembers {
		return generated.Room{}, unprocessable("a room has at most %d agents", maxMembers)
	}
	if kind != KindSquad && kind != KindAdhoc {
		return generated.Room{}, unprocessable("only a squad or an ad-hoc room can be created; the team room is the workspace's own")
	}
	members, err := s.validateNewMembers(ws, req.AgentIds)
	if err != nil {
		return generated.Room{}, err
	}
	if len(members) < 2 {
		return generated.Room{}, unprocessable(textTwoAgents)
	}
	name, err := roomName(kind, req.Name, members, ws)
	if err != nil {
		return generated.Room{}, err
	}
	rec := roomRecord{
		ID: uuid.NewString(), WorkspaceID: ws.ID, Kind: kind, Name: name,
		Members: members, Muted: []string{}, CreatedAt: s.now(),
	}
	if req.OriginSessionId != nil {
		origin := strings.TrimSpace(*req.OriginSessionId)
		if origin == "" || !s.deps.SessionInWorkspace(origin, ws.ID) {
			return generated.Room{}, unprocessable("the chat this group is started from is not a main chat of %s", workspaceLabel(ws))
		}
		rec.OriginSessionID = origin
		rec.Invitation = s.newInvitation(kind, origin)
	}
	if err = s.loadAll(); err != nil {
		return generated.Room{}, err
	}
	r, err := s.createRecord(rec)
	if err != nil {
		return generated.Room{}, err
	}
	s.mu.Lock()
	s.rooms[rec.ID] = r
	s.mu.Unlock()
	s.deps.Audit(AuditEvent{RoomID: rec.ID, WorkspaceID: ws.ID, Action: "created", AgentIDs: members, Principal: principal})
	if rec.Invitation != nil && rec.Invitation.Status == InvitationGenerating {
		// FR-039: outside every room lock; nothing waits for it (D20).
		s.inflight.Add(1)
		go func() {
			defer s.inflight.Done()
			s.runInvitation(rec.ID)
		}()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return s.roomView(r, ws), nil
}

// roomName validates a squad name or composes an ad-hoc room's name from its
// members (the Room schema requires a name).
func roomName(kind string, name *string, members []string, ws WorkspaceInfo) (string, error) {
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" || !utf8.ValidString(n) || nameUnits(n) > maxRoomNameUnits || strings.ContainsRune(n, 0) {
			return "", unprocessable(textNameLength)
		}
		return n, nil
	}
	if kind == KindSquad {
		return "", unprocessable("a squad needs a name")
	}
	names := make([]string, 0, len(members))
	for _, id := range members {
		names = append(names, agentName(ws, id))
	}
	return truncateName(strings.Join(names, ", ")), nil
}

// nameUnits counts a room name the way every wire carrier's limit is checked
// in the browser: the generated validators compare JavaScript String.length,
// UTF-16 code units, so a character outside the Basic Multilingual Plane
// (U+10000 and up) counts twice. A name within 80 code points but over 80
// units made the browser reject whole approval frames, reload snapshots and
// room lists (gate round 3, security O1; founder decision 2026-10-20). Units
// are never fewer than code points, so this one count also satisfies the
// contract's maxLength.
func nameUnits(n string) int {
	units := 0
	for _, r := range n {
		units += utf16.RuneLen(r)
	}
	return units
}

// truncateName shortens a composed name to at most maxRoomNameUnits UTF-16
// units, keeping whole characters and ending with "…" (one unit).
func truncateName(n string) string {
	if nameUnits(n) <= maxRoomNameUnits {
		return n
	}
	var b strings.Builder
	units := 0
	for _, r := range n {
		w := utf16.RuneLen(r)
		if units+w > maxRoomNameUnits-1 {
			break
		}
		b.WriteRune(r)
		units += w
	}
	return b.String() + "…"
}

// Delete deletes a squad or an ad-hoc room: its pending requests are
// cancelled; work already running finishes in the members' main chats and
// its answer is reported "room unavailable" (§6.11). The team room is not
// deletable (422). The deletion is saved first (the record's deletion
// intent); only then is pending work cancelled and the directory removed, so
// a failed deletion never leaves a live room with cancelled work, and an
// interrupted one is finished on the next load.
func (s *Service) Delete(roomID, principal string) error {
	r, ws, err := s.openRoom(roomID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rec.Kind == KindTeam {
		return unprocessable("the team room cannot be deleted")
	}
	r.rec.Deleted = true
	if err := s.persistRecord(r); err != nil {
		r.rec.Deleted = false
		return storageError("the group chat could not be deleted", err)
	}
	r.deleted = true
	s.mu.Lock()
	delete(s.rooms, r.rec.ID)
	s.mu.Unlock()
	s.deps.Audit(AuditEvent{RoomID: r.rec.ID, WorkspaceID: ws.ID, Action: "deleted", AgentIDs: r.rec.Members, Principal: principal})
	s.finishDeletion(r)
	return nil
}

// finishDeletion ends a deleted room's pending requests (their approval
// cards are withdrawn and their captures discarded, so none of them can run)
// and removes its directory. A failure is logged: the deletion itself is
// saved, the room is never served again, and the next load finishes it. The
// caller holds r.mu.
func (s *Service) finishDeletion(r *room) {
	for _, id := range r.order {
		d := r.deliveries[id]
		if !cancellable(d) {
			continue
		}
		if cancel, ok := r.cancels[d.RequestID]; ok {
			cancel()
			delete(r.cancels, d.RequestID)
		}
		if d.State == StateAwaitingApproval {
			s.deps.CancelApproval(d.RequestID)
		}
		if d.SessionID != "" {
			if err := s.deps.Discard(d.SessionID, d.RequestID); err != nil {
				logRoomError("a deleted group's request could not be discarded from the member's ledger; the dequeue fence refuses it", r.rec.ID, err)
			}
		}
	}
	if err := s.journal.Remove(r.rec.ID); err != nil {
		logRoomError("a deleted group's files could not be removed; the next load retries", r.rec.ID, err)
	}
	// The group's journals go; its files stay (D19). Only the private
	// staging of answers that never posted is removed.
	if s.stagingDir != "" {
		removeAll(filepath.Join(s.stagingDir, r.rec.ID))
	}
}

// UpdateMembers adds, removes, mutes or unmutes members (K24). The team room
// accepts only mute and unmute; removing below two agents is refused
// (FR-204); a room never grows past 256 agents and a request names at most
// 256 (both whatever the inbound validation setting); every change is
// audited (FR-172). A removal is saved before the removed members' pending
// requests are cancelled.
func (s *Service) UpdateMembers(roomID string, req generated.RoomMembersUpdateRequest, principal string) (generated.Room, error) {
	if len(req.AgentIds) > maxMembers {
		return generated.Room{}, unprocessable("a membership change names at most %d agents", maxMembers)
	}
	r, ws, err := s.openRoom(roomID)
	if err != nil {
		return generated.Room{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	action := string(req.Action)
	if r.rec.Kind == KindTeam && (action == "add" || action == "remove") {
		return generated.Room{}, unprocessable(textTeamMembership)
	}
	before := r.rec.clone()
	changed, err := s.applyMemberAction(r, ws, action, req.AgentIds)
	if err != nil {
		r.rec = before
		return generated.Room{}, err
	}
	if len(changed) == 0 {
		return s.roomView(r, ws), nil
	}
	if err := s.publishMeta(r, ws); err != nil {
		r.rec = before
		return generated.Room{}, err
	}
	s.deps.Audit(AuditEvent{RoomID: r.rec.ID, WorkspaceID: ws.ID, Action: "members_" + action, AgentIDs: changed, Principal: principal})
	if action == "remove" {
		if err := s.cancelMembers(r, changed, ReasonRemoved); err != nil {
			return generated.Room{}, storageError("The members are removed and their pending requests will not run, but some of those requests could not be marked cancelled", err)
		}
	}
	return s.roomView(r, ws), nil
}

// applyMemberAction changes the record in memory and returns the agent ids
// it changed. The caller holds r.mu and persists.
func (s *Service) applyMemberAction(r *room, ws WorkspaceInfo, action string, ids []string) ([]string, error) {
	switch action {
	case "add":
		add, err := s.validateNewMembers(ws, ids)
		if err != nil {
			return nil, err
		}
		var changed []string
		for _, id := range add {
			if !slices.Contains(r.rec.Members, id) {
				r.rec.Members = append(r.rec.Members, id)
				changed = append(changed, id)
			}
		}
		if len(r.rec.Members) > maxMembers {
			return nil, unprocessable("a room has at most %d agents", maxMembers)
		}
		return changed, nil
	case "remove":
		return s.removeMembers(r, ids)
	case "mute", "unmute":
		return s.setMuted(r, ids, action == "mute")
	default:
		return nil, badRequest("unknown action %q", action)
	}
}

func (s *Service) removeMembers(r *room, ids []string) ([]string, error) {
	var gone []string
	for _, id := range ids {
		if !slices.Contains(r.rec.Members, id) {
			return nil, unprocessable("%s is not a member of this group", s.agentLabel(id))
		}
		if !slices.Contains(gone, id) {
			gone = append(gone, id)
		}
	}
	if len(r.rec.Members)-len(gone) < 2 {
		return nil, unprocessable(textTwoAgentsDelete)
	}
	r.rec.Members = slices.DeleteFunc(r.rec.Members, func(id string) bool { return slices.Contains(gone, id) })
	r.rec.Muted = keep(r.rec.Muted, r.rec.Members)
	return gone, nil
}

func (s *Service) setMuted(r *room, ids []string, mute bool) ([]string, error) {
	var changed []string
	for _, id := range ids {
		if !slices.Contains(r.rec.Members, id) {
			return nil, unprocessable("%s is not a member of this group", s.agentLabel(id))
		}
	}
	for _, id := range ids {
		muted := slices.Contains(r.rec.Muted, id)
		switch {
		case mute && !muted:
			r.rec.Muted = append(r.rec.Muted, id)
			changed = append(changed, id)
		case !mute && muted:
			r.rec.Muted = slices.DeleteFunc(r.rec.Muted, func(m string) bool { return m == id })
			changed = append(changed, id)
		}
	}
	return changed, nil
}

// logRoomError logs a background failure the room shows on its next change.
func logRoomError(msg string, roomID string, err error) {
	slog.Error("rooms: "+msg, "room_id", roomID, "error", err)
}
