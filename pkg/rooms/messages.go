// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// idSpace is the namespace of the deterministic ids (FR-216: entry,
// delivery, answer, mirror and mention-delivery ids are derived, so each is
// written once even when a step is repeated after a crash).
var idSpace = uuid.MustParse("6f1d2c4a-58c1-4d0e-9a51-7a2d3b9e0c11")

func deriveID(parts ...string) string {
	return uuid.NewSHA1(idSpace, []byte(strings.Join(parts, "\x00"))).String()
}

// PostMessage records the user's message (or runs a room command) and fans
// it out to the addressed members (K26, FR-164, FR-200, FR-205, FR-216).
// It returns once the message is recorded; admissions run in the background
// and show as member states. A repeated client_message_id is a Retry: the
// message is not recorded again and only undelivered current members are
// admitted (FR-216, FR-252).
func (s *Service) PostMessage(roomID string, req generated.RoomMessageRequest, principal string) error {
	if err := validateMessage(req, s.bodyLimit()); err != nil {
		return err
	}
	r, ws, err := s.openRoom(roomID)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(req.Text)
	if strings.HasPrefix(trimmed, "/") {
		return s.runCommand(r, ws, trimmed, req, principal)
	}
	r.mu.Lock()
	if idx, retry := r.byClientID[req.ClientMessageId]; retry {
		pending := s.retryLocked(r, r.entries[idx].Entry.Id)
		r.mu.Unlock()
		s.admitAll(r, ws, pending)
		return nil
	}
	pending, err := s.recordUserMessage(r, ws, req)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	s.admitAll(r, ws, pending)
	return nil
}

func (s *Service) bodyLimit() int {
	_, body := s.deps.Limits()
	if body <= 0 {
		return defaultMessageMaxBytes
	}
	return body
}

func validateMessage(req generated.RoomMessageRequest, bodyLimit int) error {
	cid := req.ClientMessageId
	if strings.TrimSpace(cid) == "" || len(cid) > 128 || !validID(cid) {
		return badRequest("client_message_id must be 1-128 characters without slashes")
	}
	if !utf8.ValidString(req.Text) || strings.ContainsRune(req.Text, 0) {
		return badRequest("text must be valid text")
	}
	if strings.TrimSpace(req.Text) == "" {
		return unprocessable("the message is empty")
	}
	if len(req.Text) > bodyLimit {
		return unprocessable("the message is %d bytes; the limit is %d bytes", len(req.Text), bodyLimit)
	}
	if len(req.Mentions) > 256 {
		return badRequest("too many mentions")
	}
	return nil
}

// recordUserMessage records the entry and one delivery per addressee
// (nothing is delivered while the room is paused, FR-170). It returns the
// deliveries to admit. The caller holds r.mu.
func (s *Service) recordUserMessage(r *room, ws WorkspaceInfo, req generated.RoomMessageRequest) ([]string, error) {
	if reason := readOnlyReason(r, ws); reason != "" {
		return nil, unprocessable("%s", reason)
	}
	addressees, err := s.userAddressees(r, ws, req)
	if err != nil {
		return nil, err
	}
	entryID := deriveID("entry", r.rec.ID, req.ClientMessageId)
	cid := req.ClientMessageId
	se := storedEntry{Entry: generated.RoomEntry{
		Id: entryID, RoomId: r.rec.ID, ClientMessageId: &cid, Text: req.Text,
		Author: generated.RoomEntryAuthor{Kind: "user"}, Addressees: nonNil(addressees),
		Timestamp: s.now().Format(time.RFC3339Nano),
	}}
	var ds []*deliveryRecord
	if !r.rec.Paused {
		ds = s.newDeliveries(r, entryID, addressees, "")
	}
	if err := s.appendEntry(r, se, ds); err != nil {
		return nil, err
	}
	return deliveryIDs(ds), nil
}

// userAddressees applies FR-164 and §6.13. Picker mentions are
// authoritative: when the person picked members (or @all) in the picker, the
// picked ids are the addressees and the text is not parsed again - a picked
// duplicate name or a renamed member is never refused or retargeted by its
// label (each must still be a current member; `all` = every unmuted member).
// Without picks, typed @Full Name resolves by exact unique match (ambiguity
// refused); no @-name addresses every unmuted member.
func (s *Service) userAddressees(r *room, ws WorkspaceInfo, req generated.RoomMessageRequest) ([]string, error) {
	if len(req.Mentions) > 0 {
		return s.pickedAddressees(r, req.Mentions)
	}
	typed := resolveTyped(req.Text, memberAgents(r, ws))
	if typed.ambiguous != "" {
		return nil, unprocessable(textAmbiguousMention, typed.ambiguous)
	}
	ids := slices.Clone(typed.ids)
	if len(ids) == 0 && !typed.all && typed.unresolved {
		// "@Jim go" where no member is called exactly Jim: the name stays
		// text and the message addresses nobody (§6.13, BDD-17.31).
		return []string{}, nil
	}
	if typed.all || len(ids) == 0 {
		for _, id := range unmuted(r) {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

// pickedAddressees resolves the picker's ids (FR-164, §6.13).
func (s *Service) pickedAddressees(r *room, picked []string) ([]string, error) {
	var ids []string
	all := false
	for _, m := range picked {
		if m == mentionAll {
			all = true
			continue
		}
		if !slices.Contains(r.rec.Members, m) {
			return nil, unprocessable("%s is not a member of this group", s.agentLabel(m))
		}
		if !slices.Contains(ids, m) {
			ids = append(ids, m)
		}
	}
	if all {
		for _, id := range unmuted(r) {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return nonNil(ids), nil
}

// agentAddressees applies FR-165 to an agent's answer: only the members it
// @-names (typed text, ambiguous names address nobody); `@all` addresses
// every other unmuted member; never itself.
func agentAddressees(r *room, ws WorkspaceInfo, text, authorID string) []string {
	typed := resolveTyped(text, memberAgents(r, ws))
	var ids []string
	for _, id := range typed.ids {
		if id != authorID {
			ids = append(ids, id)
		}
	}
	if typed.all {
		for _, id := range unmuted(r) {
			if id != authorID && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func memberAgents(r *room, ws WorkspaceInfo) []Agent {
	out := make([]Agent, 0, len(r.rec.Members))
	for _, id := range r.rec.Members {
		out = append(out, Agent{ID: id, Name: agentName(ws, id)})
	}
	return out
}

func unmuted(r *room) []string {
	var out []string
	for _, id := range r.rec.Members {
		if !slices.Contains(r.rec.Muted, id) {
			out = append(out, id)
		}
	}
	return out
}

// newDeliveries makes the recorded deliveries of one entry; their ids are
// derived from the entry and the member (FR-216). An already recorded
// delivery (a repeat after a crash) is reused as it is.
func (s *Service) newDeliveries(r *room, entryID string, addressees []string, senderAgentID string) []*deliveryRecord {
	out := make([]*deliveryRecord, 0, len(addressees))
	for _, id := range addressees {
		reqID := deriveID("delivery", entryID, id)
		if d, ok := r.deliveries[reqID]; ok {
			out = append(out, d)
			continue
		}
		out = append(out, &deliveryRecord{
			RequestID: reqID, EntryID: entryID, AgentID: id, Generation: r.rec.Generation,
			State: StateRecorded, SenderAgentID: senderAgentID,
		})
	}
	return out
}

func deliveryIDs(ds []*deliveryRecord) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.RequestID)
	}
	return out
}

// appendEntry records the deliveries (before any admission, FR-216), then
// the entry, then publishes the entry. The caller holds r.mu.
func (s *Service) appendEntry(r *room, se storedEntry, ds []*deliveryRecord) error {
	for _, d := range ds {
		if _, known := r.deliveries[d.RequestID]; known {
			continue
		}
		d.Seq, d.At = r.nextSeq(), s.now()
		if err := s.journal.Append(r.rec.ID, StreamDeliveries, d); err != nil {
			r.seq--
			return storageError("the message could not be saved", err)
		}
		r.putDelivery(d)
	}
	se.Entry.Seq = r.nextSeq()
	se.Entry.Deliveries = []generated.RoomDeliveryState{}
	if err := s.journal.Append(r.rec.ID, StreamEntries, &se); err != nil {
		r.seq--
		return storageError("the message could not be saved", err)
	}
	r.addEntry(se)
	s.deps.Publish(r.rec.ID, generated.RoomEntryFrame{
		Type: string(generated.WsFrameTypeRoomEntry), RoomId: r.rec.ID, Seq: se.Entry.Seq, Entry: entryWire(r, se),
	})
	return nil
}

// entryWire is the K26 projection with the entry's current delivery states.
// The caller holds r.mu.
func entryWire(r *room, se storedEntry) generated.RoomEntry {
	out := se.Entry
	out.Addressees = nonNil(out.Addressees)
	out.Deliveries = []generated.RoomDeliveryState{}
	for _, id := range r.byEntry[se.Entry.Id] {
		out.Deliveries = append(out.Deliveries, deliveryWire(r.deliveries[id]))
	}
	return out
}

// retryLocked reconciles a repeated message (FR-216, FR-252). It acts only
// on the retried message and what depends on it: its own deliveries and the
// mention deliveries of the answers they produced (transitively). Members
// removed since are shown "cancelled — removed"; deliveries of the current
// generation that never started a turn (recorded, unknown, unavailable, or
// admitted but not yet consumed) are admitted again - after their receiver
// facts are read, so nothing runs twice. Other messages of the room are never
// touched. The caller holds r.mu.
func (s *Service) retryLocked(r *room, entryID string) []string {
	var pending []string
	for _, id := range s.dependentDeliveries(r, entryID) {
		d := r.deliveries[id]
		switch {
		case d.Consumed || d.State == StateAnswered:
			continue
		case !slices.Contains(r.rec.Members, d.AgentID) && !terminal(d.State):
			if err := s.cancelDelivery(r, d, ReasonRemoved); err != nil {
				logRoomError("a retry could not mark a removed member's request cancelled", r.rec.ID, err)
			}
		case d.Generation != r.rec.Generation:
			continue
		case admittable(d.State):
			pending = append(pending, id)
		}
	}
	return pending
}

// dependentDeliveries lists, in first-seen order, the deliveries of entryID
// and of every answer that (transitively) answers one of them. The caller
// holds r.mu.
func (s *Service) dependentDeliveries(r *room, entryID string) []string {
	answersTo := map[string][]string{} // request id -> answer entry ids
	for _, e := range r.entries {
		if e.AnswersRequest != "" {
			answersTo[e.AnswersRequest] = append(answersTo[e.AnswersRequest], e.Entry.Id)
		}
	}
	seen := map[string]bool{}
	var out []string
	queue := []string{entryID}
	for len(queue) > 0 {
		entry := queue[0]
		queue = queue[1:]
		for _, id := range r.byEntry[entry] {
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
			queue = append(queue, answersTo[id]...)
		}
	}
	return out
}

// runCommand applies a room command (FR-205, FR-206). Only the user's text
// reaches here; an agent's text is never a command (FR-168).
func (s *Service) runCommand(r *room, ws WorkspaceInfo, text string, req generated.RoomMessageRequest, principal string) error {
	fields := strings.Fields(text)
	cmd := strings.ToLower(fields[0])
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.commands[req.ClientMessageId] {
		return nil // a retried command was already applied
	}
	var err error
	switch cmd {
	case "/pause":
		err = s.pause(r, ws)
	case "/resume":
		err = s.resume(r, ws)
	case "/mute", "/unmute":
		err = s.muteCommand(r, ws, cmd == "/mute", text, req.Mentions, principal)
	case "/members", "/help":
		// Accepted and a no-op on the server (architect ruling, 2026-10-14):
		// nothing is recorded or published; the SPA renders the list as a
		// local notice from the room data it already holds.
	default:
		return unprocessable("%s", s.steerRefusal(r, ws, text, req.Mentions))
	}
	if err == nil {
		r.commands[req.ClientMessageId] = true
	}
	return err
}

// pause bumps the durable generation and cancels every not-yet-consumed
// request of older generations under the room lock (FR-170, FR-217). Work a
// member already took finishes; its answer is marked "after pause".
func (s *Service) pause(r *room, ws WorkspaceInfo) error {
	if !r.rec.Paused {
		before := r.rec.clone()
		r.rec.Paused = true
		r.rec.Generation++
		if err := s.publishMeta(r, ws); err != nil {
			r.rec = before
			return err
		}
	}
	// Also on a repeated /pause: the requests a failed label write left
	// showing their old state are marked cancelled now.
	var failed error
	for _, id := range r.order {
		if d := r.deliveries[id]; d.Generation < r.rec.Generation && cancellable(d) {
			if err := s.cancelDelivery(r, d, ReasonPaused); err != nil && failed == nil {
				failed = err
			}
		}
	}
	if failed != nil {
		return storageError("The group is paused and its pending requests will not run, but some of them could not be marked cancelled. Send /pause again to retry", failed)
	}
	return nil
}

// resume starts sending new messages again; messages written while paused
// stay undelivered (§0.10).
func (s *Service) resume(r *room, ws WorkspaceInfo) error {
	if !r.rec.Paused {
		return nil
	}
	before := r.rec.clone()
	r.rec.Paused = false
	if err := s.publishMeta(r, ws); err != nil {
		r.rec = before
		return err
	}
	return nil
}

func (s *Service) muteCommand(r *room, ws WorkspaceInfo, mute bool, text string, picked []string, principal string) error {
	targets := slices.DeleteFunc(slices.Clone(picked), func(id string) bool { return id == mentionAll })
	if len(targets) == 0 {
		typed := resolveTyped(text, memberAgents(r, ws))
		if typed.ambiguous != "" {
			return unprocessable(textAmbiguousMention, typed.ambiguous)
		}
		targets = typed.ids
	}
	if len(targets) == 0 {
		return unprocessable("name a member: /mute @Agent or /unmute @Agent")
	}
	before := r.rec.clone()
	changed, err := s.setMuted(r, targets, mute)
	if err != nil || len(changed) == 0 {
		r.rec = before
		return err
	}
	if err := s.publishMeta(r, ws); err != nil {
		r.rec = before
		return err
	}
	action := "members_unmute"
	if mute {
		action = "members_mute"
	}
	s.deps.Audit(AuditEvent{RoomID: r.rec.ID, WorkspaceID: ws.ID, Action: action, AgentIDs: changed, Principal: principal})
	return nil
}

// steerRefusal is FR-205's refusal: "Rooms don't steer agents — use <Agent>'s
// main chat", naming the @-named members (else every member), followed by the
// link target of each main chat (its main session id) so the client can link
// to them.
func (s *Service) steerRefusal(r *room, ws WorkspaceInfo, text string, picked []string) string {
	ids := slices.DeleteFunc(slices.Clone(picked), func(id string) bool { return !slices.Contains(r.rec.Members, id) })
	if len(ids) == 0 {
		ids = resolveTyped(text, memberAgents(r, ws)).ids
	}
	if len(ids) == 0 {
		ids = slices.Clone(r.rec.Members)
	}
	names := make([]string, 0, len(ids))
	links := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, agentName(ws, id)+"'s")
		if sid, err := session.MainSessionID(ws.ID, id); err == nil {
			links = append(links, sid)
		}
	}
	who := "each member's"
	switch len(names) {
	case 0:
	case 1:
		who = names[0]
	default:
		who = fmt.Sprintf("%s or %s", strings.Join(names[:len(names)-1], ", "), names[len(names)-1])
	}
	msg := fmt.Sprintf(textRoomsDontSteer, who)
	if len(links) > 0 {
		msg += ": " + strings.Join(links, ", ")
	}
	return msg
}
