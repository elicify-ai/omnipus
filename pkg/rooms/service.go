// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Workspace is what the rooms need to know about one workspace.
type Workspace struct {
	ID   string
	Name string
	// Agents are the workspace's conversation agents in team order: members
	// that are chat targets, never workers, system agents or Omnipus.
	Agents []Agent
}

// Admission is one (room message, member) delivery handed to the agent loop.
type Admission struct {
	Receiver    addressing.Pair
	DeliveryID  string
	RoomID      string
	RoomName    string
	Sender      addressing.Sender
	SenderLabel string
	DisplayText string
	ModelText   string
	// OnAwaitingApproval and BeforeCommit are the room's hooks into the
	// admission (see pkg/agent.RoomRequestAdmission).
	OnAwaitingApproval func()
	BeforeCommit       func() error
}

// AdmitResult is a successful admission.
type AdmitResult struct {
	ReceiverSessionID string
	Held              bool
	AlreadyAdmitted   bool
}

// ReceiverFacts is what the member's request ledger says about a delivery
// id (FR-216: the receiver's facts, read before anything is re-admitted).
type ReceiverFacts struct {
	Found, Delivered, Discarded, Held bool
	SessionID                         string
}

// Mirror is one answer's mirror into the responder's main chat.
type Mirror struct {
	MainSessionID string
	MirrorID      string
	Author        addressing.Pair
	Ref           generated.RoomRef
	Content       string
}

// AuditEvent is one audited room change (FR-172).
type AuditEvent struct {
	RoomID, WorkspaceID, Action string
	AgentIDs                    []string
	Principal                   string
}

// Deps are the gateway-owned facts and side effects the room service uses.
type Deps interface {
	// Workspace loads a workspace; found=false when it does not exist. A
	// read failure is an error, never "not found".
	Workspace(workspaceID string) (ws Workspace, found bool, err error)
	// Admit admits one delivery into the member's main session. Refusals
	// wrap ErrAdmitRefused / ErrAdmitUnavailable / ErrAdmitCancelled.
	Admit(ctx context.Context, a Admission) (AdmitResult, error)
	// Facts reads the member's request ledger for a delivery id.
	Facts(receiver addressing.Pair, requestID string) (ReceiverFacts, error)
	// Discard discards an admitted, not yet consumed request.
	Discard(sessionID, requestID string) error
	// Mirror writes an answer once into the responder's main chat.
	Mirror(m Mirror) error
	// Publish sends a frame to the connections attached to the room.
	Publish(roomID string, frame any)
	// Audit records a room change.
	Audit(e AuditEvent)
	// SessionInWorkspace reports whether sessionID is a main chat of a
	// conversation agent of workspaceID (origin_session_id validation).
	SessionInWorkspace(sessionID, workspaceID string) bool
	// Limits returns the per-sender-and-target admissions per minute and the
	// message body limit in bytes (FR-176: the session messaging limits).
	Limits() (ratePerMinute, bodyBytes int)
	// AgentName returns an agent's display name (any agent, member or not).
	AgentName(agentID string) (string, bool)
}

// Service is the rooms of one Omnipus home.
type Service struct {
	journal Journal
	deps    Deps
	now     func() time.Time
	ctx     context.Context
	cancel  context.CancelFunc

	mu        sync.Mutex
	rooms     map[string]*room
	loadedAll bool
	// sessions maps a member main session to the room requests it is
	// working on (for waiting_on_user) and the cards pending there.
	// cardMu guards working and pending; it is a leaf lock (never held
	// while a room lock is taken).
	cardMu  sync.Mutex
	working map[string]map[string]string // session id -> request id -> room id
	pending map[string]map[string]bool   // session id -> pending card ids
	rate    rateLimiter
}

// room is one loaded room; mu is the room lock (FR-217).
type room struct {
	mu         sync.Mutex
	rec        roomRecord
	entries    []storedEntry
	byClientID map[string]int // client_message_id -> entries index
	byEntryID  map[string]int
	byEntry    map[string][]string // entry id -> its delivery ids
	deliveries map[string]*deliveryRecord
	order      []string // delivery ids in first-seen order
	mirrored   map[string]bool
	turns      map[string]bool
	seq        int64
	deleted    bool
	// cancels holds the approval-wait cancel of each delivery in flight.
	cancels map[string]context.CancelFunc
	// commands remembers the client ids of commands already applied in this
	// process, so a retried /pause does not pause twice.
	commands map[string]bool
}

func newRoom() *room {
	return &room{
		byClientID: map[string]int{}, byEntryID: map[string]int{}, byEntry: map[string][]string{},
		deliveries: map[string]*deliveryRecord{}, mirrored: map[string]bool{}, turns: map[string]bool{},
		cancels: map[string]context.CancelFunc{}, commands: map[string]bool{},
	}
}

// New creates the service. journal and deps must be non-nil.
func New(journal Journal, deps Deps) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		journal: journal, deps: deps, now: func() time.Time { return time.Now().UTC() },
		ctx: ctx, cancel: cancel,
		rooms: map[string]*room{}, working: map[string]map[string]string{}, pending: map[string]map[string]bool{},
		rate: rateLimiter{hits: map[string][]time.Time{}},
	}
}

// Close cancels every in-flight fan-out (pending approvals among them).
func (s *Service) Close() { s.cancel() }

// SetClockForTest replaces the clock (tests only).
func (s *Service) SetClockForTest(now func() time.Time) { s.now = now }

// loadRoom returns a loaded room or a 404 / storage error.
func (s *Service) loadRoom(roomID string) (*room, error) {
	if !validID(roomID) {
		return nil, notFound("no such group chat")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.rooms[roomID]; ok {
		if r.deleted {
			return nil, notFound("no such group chat")
		}
		return r, nil
	}
	r, err := s.readRoom(roomID)
	if err != nil {
		return nil, err
	}
	s.rooms[roomID] = r
	return r, nil
}

// loadAll loads every stored room once.
func (s *Service) loadAll() error {
	s.mu.Lock()
	if s.loadedAll {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	ids, err := s.journal.List()
	if err != nil {
		return storageError("the group chats could not be listed", err)
	}
	for _, id := range ids {
		if _, err := s.loadRoom(id); err != nil && StatusOf(err) != 404 {
			return err
		}
	}
	s.mu.Lock()
	s.loadedAll = true
	s.mu.Unlock()
	return nil
}

// readRoom reads a room's record and journals and restores its delivery
// states after a restart (§6.11 "Restart").
func (s *Service) readRoom(roomID string) (*room, error) {
	r := newRoom()
	if err := s.journal.ReadRecord(roomID, &r.rec); err != nil {
		if isNotExist(err) {
			return nil, notFound("no such group chat")
		}
		return nil, storageError("the group chat could not be read", err)
	}
	r.seq = r.rec.Seq
	if err := s.readJournals(roomID, r); err != nil {
		return nil, storageError("the group chat could not be read", err)
	}
	s.recover(r)
	return r, nil
}

func (s *Service) readJournals(roomID string, r *room) error {
	lines, err := s.journal.Read(roomID, StreamEntries)
	if err != nil {
		return err
	}
	for _, line := range lines {
		var e storedEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("entry: %w", err)
		}
		r.addEntry(e)
	}
	if lines, err = s.journal.Read(roomID, StreamDeliveries); err != nil {
		return err
	}
	for _, line := range lines {
		var d deliveryRecord
		if err := json.Unmarshal(line, &d); err != nil {
			return fmt.Errorf("delivery: %w", err)
		}
		r.putDelivery(&d)
	}
	if lines, err = s.journal.Read(roomID, StreamMirrors); err != nil {
		return err
	}
	for _, line := range lines {
		var m mirrorRecord
		if err := json.Unmarshal(line, &m); err != nil {
			return fmt.Errorf("mirror: %w", err)
		}
		r.mirrored[m.AnswerID] = true
	}
	if lines, err = s.journal.Read(roomID, StreamTurns); err != nil {
		return err
	}
	for _, line := range lines {
		var t turnRecord
		if err := json.Unmarshal(line, &t); err != nil {
			return fmt.Errorf("turn: %w", err)
		}
		r.turns[t.TurnID] = true
	}
	return nil
}

func (r *room) addEntry(e storedEntry) {
	r.byEntryID[e.Entry.Id] = len(r.entries)
	if e.Entry.ClientMessageId != nil {
		r.byClientID[*e.Entry.ClientMessageId] = len(r.entries)
	}
	r.entries = append(r.entries, e)
	if e.Entry.Seq > r.seq {
		r.seq = e.Entry.Seq
	}
}

func (r *room) putDelivery(d *deliveryRecord) {
	if _, ok := r.deliveries[d.RequestID]; !ok {
		r.order = append(r.order, d.RequestID)
		r.byEntry[d.EntryID] = append(r.byEntry[d.EntryID], d.RequestID)
	}
	r.deliveries[d.RequestID] = d
	if d.Seq > r.seq {
		r.seq = d.Seq
	}
}

// recover restores the states the previous process left in flight: nothing
// that cannot be proven is shown as working (§6.11). It also completes the
// answer mirrors a crash cut off (FR-216).
func (s *Service) recover(r *room) {
	for _, id := range r.order {
		d := r.deliveries[id]
		if terminal(d.State) {
			continue
		}
		next, reason := s.recoveredState(r, d)
		if next == d.State && reason == d.Reason {
			continue
		}
		if err := s.setState(r, d, next, reason); err != nil {
			slog.Error("rooms: restart state could not be recorded; the room shows it after the next change",
				"room_id", r.rec.ID, "request_id", d.RequestID, "error", err)
		}
	}
	for _, e := range r.entries {
		if e.AnswersRequest != "" && !r.mirrored[e.Entry.Id] {
			s.mirrorAnswer(r, e)
		}
	}
}

// recoveredState decides a delivery's state after a restart from the
// member's ledger (the receiver facts).
func (s *Service) recoveredState(r *room, d *deliveryRecord) (string, string) {
	facts, err := s.deps.Facts(addressing.Pair{WorkspaceID: r.rec.WorkspaceID, AgentID: d.AgentID}, d.RequestID)
	if err != nil {
		return StateUnknown, ""
	}
	switch {
	case !facts.Found && d.State == StateRecorded:
		return StateRecorded, d.Reason // a Retry admits it
	case !facts.Found:
		// An approval wait or an admission the process lost: not provable.
		return StateUnknown, ""
	case facts.Discarded:
		return StateCancelled, d.Reason
	case facts.Delivered:
		// A turn had taken it and the restart cut that turn off.
		return StateInterrupted, ReasonRestart
	case facts.Held:
		return StateHeld, ""
	default:
		return StateAdmitted, ""
	}
}

// nextSeq advances the room stream position.
func (r *room) nextSeq() int64 {
	r.seq++
	return r.seq
}

// persistRecord writes the room record at the current stream position.
func (s *Service) persistRecord(r *room) error {
	r.rec.Seq = r.seq
	if err := s.journal.WriteRecord(r.rec.ID, r.rec); err != nil {
		return storageError("the group chat could not be saved", err)
	}
	return nil
}

// setState records a delivery's new state and publishes it. The caller
// holds r.mu.
func (s *Service) setState(r *room, d *deliveryRecord, state, reason string) error {
	next := *d
	next.State, next.Reason = state, reason
	next.Seq = r.nextSeq()
	next.At = s.now()
	switch state {
	case StateWorking:
		next.Consumed = true
	case StateHeld, StateAdmitted:
		next.Consumed = false
	}
	if err := s.journal.Append(r.rec.ID, StreamDeliveries, &next); err != nil {
		r.seq--
		return storageError("the request state could not be saved", err)
	}
	*d = next
	s.deps.Publish(r.rec.ID, generated.RoomMemberStateFrame{
		Type: string(generated.WsFrameTypeRoomMemberState), RoomId: r.rec.ID, Seq: next.Seq, Delivery: deliveryWire(d),
	})
	return nil
}

// publishMeta persists the record and publishes a room_meta frame. The
// caller holds r.mu.
func (s *Service) publishMeta(r *room, ws Workspace) error {
	r.nextSeq()
	if err := s.persistRecord(r); err != nil {
		r.seq--
		return err
	}
	view := s.roomView(r, ws)
	s.deps.Publish(r.rec.ID, generated.RoomMetaFrame{
		Type: string(generated.WsFrameTypeRoomMeta), RoomId: r.rec.ID, Seq: r.seq,
		Members: view.Members, Muted: view.Muted, Paused: view.Paused, Generation: view.Generation,
		ReadOnlyReason:        view.ReadOnlyReason,
		AllocatedCostMicroUsd: view.AllocatedCostMicroUsd, AllocatedCostUnpriced: view.AllocatedCostUnpriced,
	})
	return nil
}

func deliveryWire(d *deliveryRecord) generated.RoomDeliveryState {
	out := generated.RoomDeliveryState{AgentId: d.AgentID, RequestId: d.RequestID, State: d.State}
	if d.Reason != "" {
		reason := d.Reason
		out.Reason = &reason
	}
	return out
}

// roomView is the K23 Room projection. The caller holds r.mu.
func (s *Service) roomView(r *room, ws Workspace) generated.Room {
	out := generated.Room{
		Id: r.rec.ID, WorkspaceId: r.rec.WorkspaceID, Kind: generated.RoomKind(r.rec.Kind), Name: r.rec.Name,
		Members: nonNil(r.rec.Members), Muted: nonNil(r.rec.Muted), Paused: r.rec.Paused, Generation: r.rec.Generation,
		AllocatedCostMicroUsd: r.rec.CostMicroUSD, AllocatedCostUnpriced: r.rec.CostUnpriced,
	}
	if r.rec.OriginSessionID != "" {
		origin := r.rec.OriginSessionID
		out.OriginSessionId = &origin
	}
	if reason := readOnlyReason(r, ws); reason != "" {
		out.ReadOnlyReason = &reason
	}
	out.Hidden = r.rec.Kind == KindTeam && len(r.rec.Members) < 2
	for _, d := range latestByMember(r) {
		if d.State == StateAwaitingApproval || d.State == StateWaitingOnUser {
			out.NeedsAttention = true
		}
	}
	return out
}

// readOnlyReason: a room with fewer than two current members is read-only
// until another agent joins (FR-160, FR-204).
func readOnlyReason(r *room, _ Workspace) string {
	if len(r.rec.Members) >= 2 {
		return ""
	}
	if r.rec.Kind == KindTeam {
		return textTeamHidden
	}
	return textReadOnlyTwoAgents
}

// latestByMember returns each member's most recent delivery (§6.11 "the
// member's displayed state is the most recent one").
func latestByMember(r *room) map[string]*deliveryRecord {
	out := map[string]*deliveryRecord{}
	for _, id := range r.order {
		d := r.deliveries[id]
		out[d.AgentID] = d
	}
	return out
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return append([]string(nil), in...)
}

// sortRooms orders a workspace's rooms: team room first, then squads, then
// ad-hoc rooms, each by creation time (FR-174).
func sortRooms(rs []*room) {
	rank := map[string]int{KindTeam: 0, KindSquad: 1, KindAdhoc: 2}
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i].rec, rs[j].rec
		if rank[a.Kind] != rank[b.Kind] {
			return rank[a.Kind] < rank[b.Kind]
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
}
