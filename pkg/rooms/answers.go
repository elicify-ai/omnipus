// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ErrRoomUnavailable is the refusal an answer gets when its room or its
// membership is gone (§6.11 "room unavailable").
var ErrRoomUnavailable = errors.New(textRoomUnavailable)

// Answer is a member's answer_request call on a room request.
type Answer struct {
	RoomID, RequestID  string
	Responder          addressing.Pair
	ResponderSessionID string
	Content            string
}

// AnswerReceipt is what the answering agent is told.
type AnswerReceipt struct {
	AnswerID string
	RoomName string
}

// DeliverAnswer is the room reply branch (FR-166, FR-215): the answer is
// appended to the room authored by the responder, mirrored once into the
// responder's main chat, and its resolved @-mentions are fanned out (none
// for an "after pause" answer, FR-170). No agent owner is woken (a room has
// none). Ids are derived from the request and the answer occurrence.
func (s *Service) DeliverAnswer(_ context.Context, a Answer) (AnswerReceipt, error) {
	if limit := s.bodyLimit(); len(a.Content) > limit {
		// FR-176: the same effective limit as a person's room message. Nothing
		// is posted, mirrored or passed on.
		return AnswerReceipt{}, fmt.Errorf("%w: your answer is %d bytes; a group message can be at most %d bytes. "+
			"Shorten it and call answer_request again", ErrAnswerTooLarge, len(a.Content), limit)
	}
	r, ws, err := s.openRoom(a.RoomID)
	if err != nil {
		if StatusOf(err) == 404 {
			return AnswerReceipt{}, ErrRoomUnavailable
		}
		return AnswerReceipt{}, err
	}
	r.mu.Lock()
	se, mentions, err := s.recordAnswer(r, ws, a)
	name := r.rec.Name
	r.mu.Unlock()
	if err != nil {
		return AnswerReceipt{}, err
	}
	receipt := AnswerReceipt{AnswerID: se.Entry.Id, RoomName: name}
	// WU-15 ruling 06:00Z: the room answer is committed; a failed mirror is
	// returned, never swallowed. Mentions wait for the mirror (FR-216 order:
	// room append, mirror, mention admission). The same answer sent again
	// repairs the mirror exactly once and then fans the mentions out.
	if err := s.mirrorAnswer(r, se); err != nil {
		return receipt, fmt.Errorf("%w: %w", ErrMirrorFailed, err)
	}
	s.admitAll(r, ws, mentions)
	return receipt, nil
}

// ErrAnswerTooLarge: an answer over the room's message size limit (FR-176).
var ErrAnswerTooLarge = errors.New("the answer was not posted: it is too long")

// ErrMirrorFailed: the answer is in the room, but its copy in the member's
// main chat could not be written. Sending the same answer again repairs it.
var ErrMirrorFailed = errors.New("your answer was posted in the group, but it could not be saved in your main chat; " +
	"call answer_request again with the same text to save it there")

// recordAnswer appends the answer and its mention deliveries. The caller
// holds r.mu.
func (s *Service) recordAnswer(r *room, ws WorkspaceInfo, a Answer) (storedEntry, []string, error) {
	d := r.deliveries[a.RequestID]
	if r.deleted || d == nil || d.AgentID != a.Responder.AgentID || a.Responder.WorkspaceID != r.rec.WorkspaceID {
		return storedEntry{}, nil, ErrRoomUnavailable
	}
	if !slices.Contains(r.rec.Members, d.AgentID) {
		s.updateState(r, d, StateUnavailable, ReasonRoomUnavailable)
		return storedEntry{}, nil, errors.New(textMemberLeftRoom)
	}
	if d.State == StateCancelled {
		return storedEntry{}, nil, ErrRoomUnavailable
	}
	if a.ResponderSessionID != "" {
		d.SessionID = a.ResponderSessionID
	}
	if prior, ok := unmirroredAnswer(r, a); ok {
		// A repeat of an answer whose main-chat mirror failed: nothing new is
		// appended; its mentions recorded with it are admitted after the
		// mirror is repaired.
		return prior, r.byEntry[prior.Entry.Id], nil
	}
	afterPause := d.Generation < r.rec.Generation
	occurrence := 1
	for _, e := range r.entries {
		if e.AnswersRequest == a.RequestID {
			occurrence++
		}
	}
	answerID := deriveID("answer", a.RequestID, strconv.Itoa(occurrence))
	var addressees []string
	if !afterPause && !r.rec.Paused {
		addressees = agentAddressees(r, ws, a.Content, d.AgentID)
	}
	author := d.AgentID
	se := storedEntry{
		Entry: generated.RoomEntry{
			Id: answerID, RoomId: r.rec.ID, Text: a.Content, Addressees: nonNil(addressees), AfterPause: afterPause,
			Author:    generated.RoomEntryAuthor{Kind: "agent", AgentId: &author},
			Timestamp: s.now().Format(time.RFC3339Nano),
		},
		AnswersRequest: a.RequestID, SenderAgentID: d.AgentID,
	}
	ds := s.newDeliveries(r, answerID, addressees, d.AgentID)
	if err := s.appendEntry(r, se, ds); err != nil {
		return storedEntry{}, nil, err
	}
	if !terminal(d.State) {
		s.updateState(r, d, StateAnswered, "")
	}
	s.forgetWorking(d.SessionID, d.RequestID)
	return se, deliveryIDs(ds), nil
}

// unmirroredAnswer finds the latest answer to the same request with the
// same text whose main-chat mirror is still missing. The caller holds r.mu.
func unmirroredAnswer(r *room, a Answer) (storedEntry, bool) {
	for i := len(r.entries) - 1; i >= 0; i-- {
		e := r.entries[i]
		if e.AnswersRequest != a.RequestID {
			continue
		}
		if !r.mirrored[e.Entry.Id] && e.Entry.Text == a.Content {
			return e, true
		}
		return storedEntry{}, false // the latest answer is mirrored or differs
	}
	return storedEntry{}, false
}

// mirrorAnswer writes the answer once into the responder's main chat (room_ref
// role "answer") and records that it did. A failure is returned: the answer
// stays in the room and no mirror exists until a repeat or the room's next
// load repairs it (its id is derived, so it is written at most once).
func (s *Service) mirrorAnswer(r *room, se storedEntry) error {
	r.mu.Lock()
	if r.mirrored[se.Entry.Id] {
		r.mu.Unlock()
		return nil
	}
	d := r.deliveries[se.AnswersRequest]
	var m Mirror
	if d != nil {
		m = Mirror{
			MainSessionID: d.SessionID, MirrorID: deriveID("mirror", se.Entry.Id),
			Author:  addressing.Pair{WorkspaceID: r.rec.WorkspaceID, AgentID: d.AgentID},
			Ref:     generated.RoomRef{RoomId: r.rec.ID, RoomName: r.rec.Name, RequestId: d.RequestID, Role: "answer"},
			Content: se.Entry.Text,
		}
	}
	r.mu.Unlock()
	if d == nil || m.MainSessionID == "" {
		return fmt.Errorf("the member's main chat for answer %s is not known", se.Entry.Id)
	}
	if err := s.deps.Mirror(m); err != nil {
		logRoomError("an answer could not be mirrored into the member's main chat", r.rec.ID, err)
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := s.journal.Append(r.rec.ID, StreamMirrors, &mirrorRecord{AnswerID: se.Entry.Id, At: s.now()}); err != nil {
		// The main-chat copy is written, but the room could not record that
		// it was: the answer is not counted as mirrored, so the same answer
		// sent again (or the next load) records it once - the copy itself
		// is written at most once (its id is derived) - and only then are its
		// mentions passed on.
		logRoomError("an answer mirror could not be recorded", r.rec.ID, err)
		return err
	}
	r.mirrored[se.Entry.Id] = true
	return nil
}

// BeginConsume is the run/not-run form of DecideConsume: true only when the
// request may run now.
func (s *Service) BeginConsume(roomID, requestID, sessionID string) bool {
	return s.DecideConsume(roomID, requestID, sessionID) == ConsumeRun
}

// DecideConsume is the FR-217 dequeue fence: under the room lock, the request
// may run only while it is current (same generation, member still there,
// room not deleted). It then shows "working". A room that cannot be read, or
// a "working" state that cannot be saved, never lets the request run: it is
// deferred (kept in the member's queue, offered again on the member's next
// turn or a Retry), never run unchecked and never discarded on a passing
// fault. A request a turn already took is deferred too: a second wake for it
// is a duplicate, not a second run.
func (s *Service) DecideConsume(roomID, requestID, sessionID string) ConsumeDecision {
	r, _, err := s.openRoom(roomID)
	if err != nil {
		if StatusOf(err) == 404 {
			return ConsumeCancel
		}
		slog.Error("rooms: dequeue fence could not read the room; the request is not run and stays queued",
			"room_id", roomID, "request_id", requestID, "error", err)
		return ConsumeDefer
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.deliveries[requestID]
	if d == nil || d.State == StateCancelled {
		return ConsumeCancel
	}
	if terminal(d.State) {
		// A duplicate wake may arrive after completion. Settled work never
		// runs again, but an unfinished answer mirror still needs its capture.
		if answerNeedsMirror(r, requestID) {
			return ConsumeDefer
		}
		return ConsumeCancel
	}
	if d.Consumed {
		return ConsumeDefer
	}
	if !s.deliveryCurrent(r, d) {
		s.cancelStale(r, d)
		return ConsumeCancel
	}
	prevSession := d.SessionID
	d.SessionID = sessionID
	if err := s.setState(r, d, StateWorking, ""); err != nil {
		d.SessionID = prevSession
		slog.Error("rooms: dequeue fence could not save the working state; the request is not run and stays queued",
			"room_id", roomID, "request_id", requestID, "error", err)
		return ConsumeDefer
	}
	s.cardMu.Lock()
	if s.working[sessionID] == nil {
		s.working[sessionID] = map[string]string{}
	}
	s.working[sessionID][requestID] = roomID
	waiting := len(s.pending[sessionID]) > 0
	s.cardMu.Unlock()
	if waiting {
		s.updateState(r, d, StateWaitingOnUser, "")
	}
	return ConsumeRun
}

// ReleaseConsume undoes a consume whose turn never received the request (it
// could not be handed to the turn): the request is queued again, not
// running, so its next delivery runs it.
func (s *Service) ReleaseConsume(roomID, requestID string) error {
	r, err := s.loadRoom(roomID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.deliveries[requestID]
	if d == nil || r.deleted || terminal(d.State) || !d.Consumed {
		return nil
	}
	if r.rec.PendingReleases == nil {
		r.rec.PendingReleases = map[string]bool{}
	}
	r.rec.PendingReleases[requestID] = true
	// Save intent independently of the delivery append. If the latter fails,
	// live reads and restart both repair the unstarted request before serving it.
	intentErr := s.persistRecord(r)
	repairErr := s.repairReleasedConsumes(r)
	if repairErr != nil {
		return errors.Join(intentErr, repairErr)
	}
	return nil
}

// ConsumeHeld puts a consumed request back to held: it met a stopped member.
func (s *Service) ConsumeHeld(roomID, requestID string) {
	s.withDelivery(roomID, requestID, func(r *room, d *deliveryRecord) {
		if !terminal(d.State) && !r.rec.PendingReleases[requestID] && !s.settlementPending(r, requestID) {
			s.forgetWorking(d.SessionID, requestID)
			s.updateState(r, d, StateHeld, "")
		}
	})
}

// ReminderTurnEnded fails the room requests of sessionID still unanswered
// after their one reminder turn.
func (s *Service) ReminderTurnEnded(sessionID string) error {
	s.cardMu.Lock()
	reqs := make(map[string]string, len(s.working[sessionID]))
	for req, roomID := range s.working[sessionID] {
		reqs[req] = roomID
	}
	s.cardMu.Unlock()
	var result error
	for req, roomID := range reqs {
		r, err := s.loadRoom(roomID)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		r.mu.Lock()
		if r.rec.PendingReleases[req] || s.settlementPending(r, req) {
			r.mu.Unlock()
			continue
		}
		d := r.deliveries[req]
		if d == nil || terminal(d.State) || !d.Consumed {
			r.mu.Unlock()
			continue
		}
		intent := settlementRecord{RequestID: req, State: StateFailed, Reason: ReasonNoAnswer}
		_, err = s.applySettlement(r, intent)
		if err != nil {
			err = errors.Join(err, s.rememberSettlement(roomID, intent))
		}
		r.mu.Unlock()
		result = errors.Join(result, err)
	}
	return result
}

// TurnCost is one consumed room request's turn (§6.14).
type TurnCost struct {
	RoomID, RequestID, TurnID string
	CostUSD                   float64
	Priced                    bool
	Inputs, RoomInputs        int
}

// AddTurnCost allocates a turn's cost to the room: turn cost × (this room's
// inputs ÷ all inputs the turn consumed), in micro-dollars, each turn once.
// The turn line is the source of truth (it is summed again on every load), so
// the share survives a failed room-record save. A turn line that cannot be
// written leaves the header marked incomplete ("plus unpriced turns") instead
// of silently low.
func (s *Service) AddTurnCost(c TurnCost) {
	if c.TurnID == "" || c.Inputs <= 0 {
		return
	}
	r, ws, err := s.openRoom(c.RoomID)
	if err != nil {
		if StatusOf(err) != 404 { // a deleted room has no cost to show
			slog.Error("rooms: a turn's cost could not be allocated: the room could not be read; the room's cost omits it",
				"room_id", c.RoomID, "turn_id", c.TurnID, "error", err)
		}
		if StatusOf(err) != 404 {
			s.markCostIncomplete(c.RoomID)
		}
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turns[c.TurnID] {
		return
	}
	share := allocate(c)
	if err := s.journal.Append(r.rec.ID, StreamTurns, &turnRecord{TurnID: c.TurnID, MicroUSD: share, Unpriced: !c.Priced, At: s.now()}); err != nil {
		logRoomError("a turn's cost could not be recorded; the room's cost is marked incomplete", r.rec.ID, err)
		s.recordCostGap(r.rec.ID)
		r.rec.CostUnpriced = true
		if pErr := s.publishMeta(r, ws); pErr != nil {
			logRoomError("the room's incomplete-cost mark could not be saved", r.rec.ID, pErr)
		}
		return
	}
	r.turns[c.TurnID] = true
	r.rec.CostMicroUSD += share
	if !c.Priced {
		r.rec.CostUnpriced = true
	}
	if err := s.publishMeta(r, ws); err != nil {
		// The turn line is saved and is what the next load sums: the share
		// is kept in memory too, and the next room change publishes it.
		logRoomError("the room's cost could not be published; it is kept and shown on the next change", r.rec.ID, err)
	}
}

// allocate is the §6.14 share rounded to the nearest whole micro-dollar,
// exact halves up (WU-15 ruling 06:00Z; math.Round on a non-negative share).
func allocate(c TurnCost) int64 {
	if !c.Priced || c.Inputs <= 0 || c.RoomInputs <= 0 {
		return 0
	}
	return int64(math.Round(c.CostUSD * 1e6 * float64(c.RoomInputs) / float64(c.Inputs)))
}

// CardPending / CardResolved track the member's own decisions (approval and
// question cards, by card id) in its main chat: while one is pending, the
// room requests it is working on show "waiting for you in <Agent>'s main
// chat" (FR-169). Both are idempotent per card id.
func (s *Service) CardPending(mainSessionID, cardID string) {
	s.cardChanged(mainSessionID, cardID, true)
}

// CardResolved is the end of one pending card.
func (s *Service) CardResolved(mainSessionID, cardID string) {
	s.cardChanged(mainSessionID, cardID, false)
}

func (s *Service) cardChanged(sessionID, cardID string, pending bool) {
	if sessionID == "" || cardID == "" {
		return
	}
	s.cardMu.Lock()
	if pending {
		if s.pending[sessionID] == nil {
			s.pending[sessionID] = map[string]bool{}
		}
		s.pending[sessionID][cardID] = true
	} else if cards := s.pending[sessionID]; cards != nil {
		delete(cards, cardID)
		if len(cards) == 0 {
			delete(s.pending, sessionID)
		}
	}
	waiting := len(s.pending[sessionID]) > 0
	reqs := make(map[string]string, len(s.working[sessionID]))
	for req, roomID := range s.working[sessionID] {
		reqs[req] = roomID
	}
	s.cardMu.Unlock()
	for req, roomID := range reqs {
		s.withDelivery(roomID, req, func(r *room, d *deliveryRecord) {
			if r.rec.PendingReleases[req] || s.settlementPending(r, req) {
				return
			}
			switch {
			case waiting && d.State == StateWorking:
				s.updateState(r, d, StateWaitingOnUser, "")
			case !waiting && d.State == StateWaitingOnUser:
				s.updateState(r, d, StateWorking, "")
			}
		})
	}
}

func (s *Service) forgetWorking(sessionID, requestID string) {
	s.cardMu.Lock()
	defer s.cardMu.Unlock()
	if m := s.working[sessionID]; m != nil {
		delete(m, requestID)
		if len(m) == 0 {
			delete(s.working, sessionID)
		}
	}
}

// withDelivery runs fn on a loaded room's delivery under the room lock.
func (s *Service) withDelivery(roomID, requestID string, fn func(r *room, d *deliveryRecord)) {
	r, err := s.loadRoom(roomID)
	if err != nil {
		if StatusOf(err) != 404 {
			slog.Error("rooms: a request's state change could not be applied: the room could not be read",
				"room_id", roomID, "request_id", requestID, "error", err)
		}
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if d := r.deliveries[requestID]; d != nil && !r.deleted {
		fn(r, d)
	}
}
