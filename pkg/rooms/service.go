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

// WorkspaceInfo is what the rooms need to know about one workspace.
type WorkspaceInfo struct {
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
	// Media are the workspace references of this request's own files, in
	// order (FR-019): they are written into the member's durable request so
	// admission, restart-time rewake and held delivery hand the model the
	// same files. Attachments are the same files as display parts for the
	// member's main chat (FR-023).
	Media       []string
	Attachments []generated.MediaPart
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
	// Attachments are the answer's files, shown in the main chat (FR-023).
	Attachments []generated.MediaPart
}

// AuditEvent is one audited room change (FR-172).
type AuditEvent struct {
	RoomID, WorkspaceID, Action string
	AgentIDs                    []string
	Principal                   string
}

// Directory is what the room service reads about workspaces and agents.
type Directory interface {
	// LookupWorkspace loads a workspace; found=false when it does not exist. A
	// read failure is an error, never "not found".
	LookupWorkspace(workspaceID string) (ws WorkspaceInfo, found bool, err error)
	// AgentName returns an agent's display name (any agent, member or not).
	AgentName(agentID string) (string, bool)
	// SessionInWorkspace reports whether sessionID is a main chat of a
	// conversation agent of workspaceID (origin_session_id validation).
	SessionInWorkspace(sessionID, workspaceID string) bool
	// Limits returns the per-sender-and-target admissions per minute and the
	// message body limit in bytes (FR-176: the session messaging limits).
	Limits() (ratePerMinute, bodyBytes int)
}

// Receivers is the member side: the agent loop's room admission, the
// member's request ledger and its main chat.
type Receivers interface {
	// Admit admits one delivery into the member's main session. Refusals
	// wrap ErrAdmitRefused / ErrAdmitUnavailable / ErrAdmitCancelled.
	Admit(ctx context.Context, a Admission) (AdmitResult, error)
	// Facts reads the member's request ledger for a delivery id.
	Facts(receiver addressing.Pair, requestID string) (ReceiverFacts, error)
	// Discard discards an admitted, not yet consumed request.
	Discard(sessionID, requestID string) error
	// Mirror writes an answer once into the responder's main chat.
	Mirror(m Mirror) error
	// CancelApproval withdraws the member's pending pre-admission card for a
	// delivery, if any (pause, removal, deletion).
	CancelApproval(requestID string)
}

// Effects are the room service's outward signals.
type Effects interface {
	// Publish sends a frame to the connections attached to the room.
	Publish(roomID string, frame any)
	// Audit records a room change.
	Audit(e AuditEvent)
}

// Deps are the gateway-owned facts and side effects the room service uses.
// Media, Publisher and Summarizer are optional (#1449): without them files
// and the invitation summary are refused or unavailable, never faked.
type Deps struct {
	Directory
	Receivers
	Effects
	Media      Media
	Publisher  Publisher
	Summarizer Summarizer
}

// DepsFrom builds Deps from one value that provides all three required parts
// and, when it implements them, the optional ones.
func DepsFrom(all interface {
	Directory
	Receivers
	Effects
}) Deps {
	d := Deps{Directory: all, Receivers: all, Effects: all}
	if m, ok := all.(Media); ok {
		d.Media = m
	}
	if p, ok := all.(Publisher); ok {
		d.Publisher = p
	}
	if sm, ok := all.(Summarizer); ok {
		d.Summarizer = sm
	}
	return d
}

// Service is the rooms of one Omnipus home.
type Service struct {
	journal Journal
	deps    Deps
	now     func() time.Time
	ctx     context.Context
	cancel  context.CancelFunc
	// inflight counts the background admissions; admitting maps a delivery
	// id in flight to its *inFlight (closed when settled and when finished).
	inflight  sync.WaitGroup
	admitting sync.Map
	// Failed allocation reads retain the incomplete mark until storage can
	// save it. Reads never serve a complete total while that repair is pending.
	incompleteCosts    sync.Map // room id -> true
	pendingSettlements sync.Map // settlementKey -> settlementRecord; completion bookkeeping only

	// workspaceMu excludes on-read system reconciliation while a user save
	// commits and applies its room memberships with that user's principal.
	workspaceMu sync.RWMutex
	mu          sync.Mutex
	rooms       map[string]*room
	loadedAll   bool
	// sessions maps a member main session to the room requests it is
	// working on (for waiting_on_user) and the cards pending there.
	// cardMu guards working and pending; it is a leaf lock (never held
	// while a room lock is taken).
	cardMu  sync.Mutex
	working map[string]map[string]string // session id -> request id -> room id
	pending map[string]map[string]bool   // session id -> pending card ids
	// reqCards maps a card that carries a group reference to the one request
	// it belongs to (#1449 FR-053: such a card flips only that request).
	reqCards map[string]cardTarget
	rate     rateLimiter
	// stagingDir is the private staging root of answer files (TD-3).
	stagingDir string
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
	// resumed: the unsent mentions found on load were handed to admission.
	resumed bool
	// cancels holds the approval-wait cancel of each delivery in flight.
	cancels map[string]context.CancelFunc
	// commands remembers the client ids of commands already applied in this
	// process, so a retried /pause does not pause twice.
	commands map[string]bool
	// intents are the answer intents (#1449 FR-078), latest line per id.
	intents     map[string]*intentRecord
	intentOrder []string
	// intentsResumed: the accepted intents found on load were handed to
	// completion once in this process.
	intentsResumed bool
	// invites are the invitation-summary decisions per member (FR-080).
	invites map[string]*inviteRecord
}

func newRoom() *room {
	return &room{
		byClientID: map[string]int{}, byEntryID: map[string]int{}, byEntry: map[string][]string{},
		deliveries: map[string]*deliveryRecord{}, mirrored: map[string]bool{}, turns: map[string]bool{},
		cancels: map[string]context.CancelFunc{}, commands: map[string]bool{},
		intents: map[string]*intentRecord{}, invites: map[string]*inviteRecord{},
	}
}

// New creates the service. journal and deps must be non-nil.
func New(journal Journal, deps Deps) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		journal: journal, deps: deps, now: func() time.Time { return time.Now().UTC() },
		ctx: ctx, cancel: cancel,
		rooms: map[string]*room{}, working: map[string]map[string]string{}, pending: map[string]map[string]bool{},
		reqCards: map[string]cardTarget{},
		rate:     rateLimiter{hits: map[string][]time.Time{}},
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
	if r.rec.Deleted {
		// A deletion saved before a crash or a failed cleanup: finish it.
		r.deleted = true
		s.finishDeletion(r)
		return nil, notFound("no such group chat")
	}
	if err := s.repairReleasedConsumes(r); err != nil {
		return nil, err
	}
	if err := s.repairIncompleteCost(r); err != nil {
		return nil, err
	}
	if err := s.repairSettlements(r); err != nil {
		return nil, err
	}
	if err := s.recover(r); err != nil {
		// Never serve states that could not be made true (a "working" member
		// whose turn died with the process): the room is unreadable until
		// its recovery can be saved.
		return nil, storageError("the group chat could not be recovered after a restart", err)
	}
	return r, nil
}

func (s *Service) readJournals(roomID string, r *room) error {
	if err := readStream(s.journal, roomID, StreamEntries, r.addEntry); err != nil {
		return err
	}
	if err := readStream(s.journal, roomID, StreamDeliveries, func(d deliveryRecord) { r.putDelivery(&d) }); err != nil {
		return err
	}
	if err := readStream(s.journal, roomID, StreamMirrors, func(m mirrorRecord) { r.mirrored[m.AnswerID] = true }); err != nil {
		return err
	}
	if err := readStream(s.journal, roomID, StreamIntents, func(in intentRecord) { r.addIntent(&in) }); err != nil {
		return err
	}
	if err := readStream(s.journal, roomID, StreamInvites, func(v inviteRecord) { r.putInvite(&v) }); err != nil {
		return err
	}
	var settlementErr error
	if err := readStream(s.journal, roomID, StreamSettlements, func(intent settlementRecord) {
		if err := validateSettlement(intent); err != nil {
			settlementErr = err
			return
		}
		s.pendingSettlements.Store(settlementKey{roomID, intent.RequestID}, intent)
	}); err != nil {
		return err
	}
	if settlementErr != nil {
		return settlementErr
	}
	// The turn journal is the allocated cost's source of truth (§6.14): each
	// counted turn's share and unpriced flag are summed here, so a share is
	// never lost when the room record could not be saved after its turn line.
	var cost int64
	unpriced := false
	err := readStream(s.journal, roomID, StreamTurns, func(t turnRecord) {
		if r.turns[t.TurnID] {
			return // a repeated line counts once
		}
		r.turns[t.TurnID] = true
		cost += t.MicroUSD
		unpriced = unpriced || t.Unpriced
	})
	if err != nil {
		return err
	}
	if err := readStream(s.journal, roomID, StreamCostGaps, func(t turnRecord) { unpriced = unpriced || t.Unpriced }); err != nil {
		return err
	}
	// The record's own mark stays: it also says a turn line could not be
	// written (AddTurnCost), which no turn line can say.
	r.rec.CostMicroUSD, r.rec.CostUnpriced = cost, r.rec.CostUnpriced || unpriced
	return nil
}

// readStream decodes every line of one journal stream, in order, into fn.
// An undecodable line fails the whole read (never skipped).
func readStream[T any](j Journal, roomID, stream string, fn func(T)) error {
	lines, err := j.Read(roomID, stream)
	if err != nil {
		return err
	}
	for i, line := range lines {
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			return fmt.Errorf("%s line %d: %w", stream, i+1, err)
		}
		fn(v)
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
// answer mirrors a crash cut off (FR-216). A recovered state that cannot be
// saved is an error: the room is not served with a state that is not true.
func (s *Service) recover(r *room) error {
	answered := map[string]bool{}
	for _, e := range r.entries {
		if e.AnswersRequest != "" {
			answered[e.AnswersRequest] = true
		}
	}
	for _, id := range r.order {
		d := r.deliveries[id]
		if terminal(d.State) {
			continue
		}
		next, reason := s.recoveredState(r, d, answered[d.RequestID])
		if next == StateInterrupted && d.SessionID != "" {
			// Interrupted work is never replayed (§6.11): the member's request
			// is discarded so no later turn picks it up again. A failed
			// discard is safe once "interrupted" is saved: the dequeue fence
			// refuses a settled request.
			if err := s.deps.Discard(d.SessionID, d.RequestID); err != nil {
				slog.Error("rooms: an interrupted request could not be discarded; the dequeue fence refuses it",
					"room_id", r.rec.ID, "request_id", d.RequestID, "error", err)
			}
		}
		if next == d.State && reason == d.Reason {
			continue
		}
		if err := s.setState(r, d, next, reason); err != nil {
			return fmt.Errorf("request %s could not be moved to %s: %w", d.RequestID, next, err)
		}
	}
	s.repairRetention(r)
	s.recoverInvitation(r)
	for _, e := range r.entries {
		if e.AnswersRequest != "" && !r.mirrored[e.Entry.Id] {
			if err := s.mirrorAnswer(r, e); err != nil {
				slog.Error("rooms: an answer mirror could not be repaired on load; the same answer sent again repairs it",
					"room_id", r.rec.ID, "answer_id", e.Entry.Id, "error", err)
			}
		}
	}
	return nil
}

// recoveredState decides a delivery's state after a restart from the room's
// own durable facts first (an answer in the room means it was answered),
// then from the member's ledger (the receiver facts).
func (s *Service) recoveredState(r *room, d *deliveryRecord, hasAnswer bool) (string, string) {
	if hasAnswer {
		// The answer reached the room before the crash cut its state write.
		return StateAnswered, ""
	}
	if d.Consumed {
		// The room recorded that a turn had taken it; the restart cut that
		// turn off.
		return StateInterrupted, ReasonRestart
	}
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
func (s *Service) publishMeta(r *room, ws WorkspaceInfo) error {
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
		InvitationContextStatus: invitationStatusPtr(r),
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
func (s *Service) roomView(r *room, ws WorkspaceInfo) generated.Room {
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
	status := generated.RoomInvitationStatus(invitationStatus(r))
	out.InvitationContextStatus = &status
	// Attention is any outstanding decision, not only each member's latest
	// request: a newer settled request never hides an older pending card.
	for _, id := range r.order {
		if d := r.deliveries[id]; d.State == StateAwaitingApproval || d.State == StateWaitingOnUser {
			out.NeedsAttention = true
			break
		}
	}
	return out
}

// readOnlyReason: a room with fewer than two current members is read-only
// until another agent joins (FR-160, FR-204).
func readOnlyReason(r *room, _ WorkspaceInfo) string {
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
	out := make([]string, len(in))
	copy(out, in)
	return out
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
