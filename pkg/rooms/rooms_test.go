package rooms

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Tests for the room service (omnipus-system-agent US-17, §6.11, §6.13,
// §6.14, FR-158–FR-217 room rows). Expected values come from the spec's
// BDD-17.x tables and FR text, not from the implementation.

type fakeDeps struct {
	mu        sync.Mutex
	ws        WorkspaceInfo
	wsErr     error
	names     map[string]string
	admits    []Admission
	admitErr  map[string]error // agent id -> admission error
	held      map[string]bool  // agent id -> admitted held
	facts     map[string]ReceiverFacts
	factsErr  map[string]error
	discarded []string
	mirrors   []Mirror
	mirrorErr error
	frames    []any
	audits    []AuditEvent
	rate      int
	body      int
	// extraSessions are main-session ids this fixture accepts as chats of
	// the workspace, besides the legacy "main-mia" id. Nil keeps the old behaviour.
	extraSessions map[string]bool
	// rewakes counts admissions of a delivery the member already had (the
	// real AdmitRoomRequest writes nothing then and only wakes the member).
	rewakes []string
	// gate, when set for an agent, blocks that agent's admission until it is
	// closed; entered receives the delivery id as the admission starts.
	gate    map[string]chan struct{}
	entered chan string
}

func newFakeDeps() *fakeDeps {
	return &fakeDeps{
		ws:       WorkspaceInfo{ID: "sales", Name: "Sales", Agents: []Agent{{"mia", "Mia"}, {"jim", "Jim"}, {"r2", "Researcher2"}}},
		names:    map[string]string{"mia": "Mia", "jim": "Jim", "r2": "Researcher2", "ray": "Ray"},
		admitErr: map[string]error{}, held: map[string]bool{}, facts: map[string]ReceiverFacts{}, factsErr: map[string]error{}, rate: 60,
		body: 65536, gate: map[string]chan struct{}{},
	}
}

func (f *fakeDeps) LookupWorkspace(id string) (WorkspaceInfo, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wsErr != nil {
		return WorkspaceInfo{}, false, f.wsErr
	}
	if id != f.ws.ID {
		return WorkspaceInfo{}, false, nil
	}
	ws := f.ws
	ws.Agents = slices.Clone(f.ws.Agents)
	return ws, true, nil
}

func (f *fakeDeps) Admit(_ context.Context, a Admission) (AdmitResult, error) {
	f.mu.Lock()
	gate, entered := f.gate[a.Receiver.AgentID], f.entered
	f.mu.Unlock()
	if entered != nil {
		entered <- a.DeliveryID
	}
	if gate != nil {
		<-gate
	}
	if a.BeforeCommit != nil {
		if err := a.BeforeCommit(); err != nil {
			return AdmitResult{}, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.admitErr[a.Receiver.AgentID]; err != nil {
		return AdmitResult{}, err
	}
	if prior := f.facts[a.DeliveryID]; prior.Found && !prior.Discarded {
		// Like AdmitRoomRequest: a capture the member already has is
		// acknowledged, never written or counted again.
		f.rewakes = append(f.rewakes, a.DeliveryID)
		return AdmitResult{ReceiverSessionID: prior.SessionID, Held: prior.Held, AlreadyAdmitted: true}, nil
	}
	f.admits = append(f.admits, a)
	f.facts[a.DeliveryID] = ReceiverFacts{Found: true, SessionID: "main-" + a.Receiver.AgentID, Held: f.held[a.Receiver.AgentID]}
	return AdmitResult{ReceiverSessionID: "main-" + a.Receiver.AgentID, Held: f.held[a.Receiver.AgentID]}, nil
}

func (f *fakeDeps) Facts(_ addressing.Pair, requestID string) (ReceiverFacts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.factsErr[requestID]; err != nil {
		return ReceiverFacts{}, err
	}
	return f.facts[requestID], nil
}

func (f *fakeDeps) Discard(_, requestID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discarded = append(f.discarded, requestID)
	return nil
}

func (f *fakeDeps) Mirror(m Mirror) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mirrorErr != nil {
		return f.mirrorErr
	}
	f.mirrors = append(f.mirrors, m)
	return nil
}

func (f *fakeDeps) Publish(_ string, frame any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = append(f.frames, frame)
}

func (f *fakeDeps) Audit(e AuditEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, e)
}

func (f *fakeDeps) SessionInWorkspace(sessionID, _ string) bool {
	if sessionID == "main-mia" {
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.extraSessions[sessionID]
}
func (f *fakeDeps) Limits() (int, int)                 { return f.rate, f.body }
func (f *fakeDeps) AgentName(id string) (string, bool) { n, ok := f.names[id]; return n, ok }
func (f *fakeDeps) CancelApproval(string)              {}

func (f *fakeDeps) admittedTo() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.admits))
	for _, a := range f.admits {
		out = append(out, a.Receiver.AgentID)
	}
	slices.Sort(out)
	return out
}

type fixture struct {
	t    *testing.T
	dir  string
	deps *fakeDeps
	svc  *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	deps := newFakeDeps()
	return &fixture{t: t, dir: dir, deps: deps, svc: New(NewFileJournal(dir), DepsFrom(deps))}
}

func (f *fixture) squad(members ...string) generated.Room {
	f.t.Helper()
	name := "Launch"
	room, err := f.svc.Create("sales", generated.RoomCreateRequest{Kind: "squad", Name: &name, AgentIds: members}, "admin")
	if err != nil {
		f.t.Fatalf("create squad: %v", err)
	}
	return room
}

// post sends a user message and admits its deliveries synchronously.
func (f *fixture) post(roomID, cid, text string, mentions ...string) error {
	f.t.Helper()
	if mentions == nil {
		mentions = []string{}
	}
	err := f.svc.PostMessage(roomID, generated.RoomMessageRequest{ClientMessageId: cid, Text: text, Mentions: mentions}, "admin")
	if err == nil {
		f.drain(roomID)
	}
	return err
}

// drain waits until the background admissions of the room finished.
func (f *fixture) drain(string) {
	f.t.Helper()
	f.svc.WaitIdleForTest()
}

func (f *fixture) states(roomID string) map[string]string {
	r, _ := f.svc.loadRoom(roomID)
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for _, d := range latestByMember(r) {
		out[d.AgentID] = d.State + "/" + d.Reason
	}
	return out
}

func (f *fixture) deliveryOf(roomID, agentID string) *deliveryRecord {
	r, _ := f.svc.loadRoom(roomID)
	r.mu.Lock()
	defer r.mu.Unlock()
	d := latestByMember(r)[agentID]
	if d == nil {
		f.t.Fatalf("no delivery for %s", agentID)
	}
	cp := *d
	return &cp
}

// BDD-17.31 / §6.13 mention resolution.
func TestResolveTyped_ExactFullNameLongestMatchAmbiguityAndAll(t *testing.T) {
	members := []Agent{{"m1", "Mia"}, {"m2", "Mia"}, {"js", "Jim Smith"}, {"a", "all"}, {"j", "Jim"}}
	cases := []struct {
		text      string
		wantIDs   []string
		wantAll   bool
		ambiguous string
	}{
		{"@Mia hello", nil, false, "Mia"},
		{"@Jim Smith go", []string{"js"}, false, ""},
		{"@jim go", []string{"j"}, false, ""},
		{"@Jimmy go", nil, false, ""},
		{"@all regroup", nil, true, ""},
		{"mail me@jim.example", nil, false, ""},
	}
	for _, c := range cases {
		got := resolveTyped(c.text, members)
		if !slices.Equal(got.ids, c.wantIDs) || got.all != c.wantAll || got.ambiguous != c.ambiguous {
			t.Errorf("%q: got ids=%v all=%v ambiguous=%q, want ids=%v all=%v ambiguous=%q",
				c.text, got.ids, got.all, got.ambiguous, c.wantIDs, c.wantAll, c.ambiguous)
		}
	}
}

// BDD-17.3 / FR-164: no @-name addresses every unmuted member; @-names only
// those; the picker id is authoritative.
func TestPostMessage_AddressingFollowsMentions(t *testing.T) {
	cases := []struct {
		text     string
		mentions []string
		want     []string
	}{
		{"status?", nil, []string{"jim", "mia", "r2"}},
		{"@Jim plan it", nil, []string{"jim"}},
		{"@Mia @Jim compare", nil, []string{"jim", "mia"}},
		{"@all go", nil, []string{"jim", "mia", "r2"}},
		{"@Jimmy plan it", []string{"jim"}, []string{"jim"}},
		{"@Jimmy plan it", nil, nil}, // §6.13: an unresolved @-name addresses nobody
	}
	for _, c := range cases {
		f := newFixture(t)
		room := f.squad("mia", "jim", "r2")
		if err := f.post(room.Id, "c1", c.text, c.mentions...); err != nil {
			t.Fatalf("%q: %v", c.text, err)
		}
		if got := f.deps.admittedTo(); !slices.Equal(got, c.want) {
			t.Errorf("%q: admitted to %v, want %v", c.text, got, c.want)
		}
	}
}

// BDD-17.18 / FR-158: a muted member is not addressed by the default or
// @all but answers when @-named.
func TestPostMessage_MutedMemberOnlyWhenNamed(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim", "r2")
	if err := f.post(room.Id, "m", "/mute @Researcher2"); err != nil {
		t.Fatalf("/mute: %v", err)
	}
	if err := f.post(room.Id, "c1", "status?"); err != nil {
		t.Fatal(err)
	}
	if got := f.deps.admittedTo(); !slices.Equal(got, []string{"jim", "mia"}) {
		t.Fatalf("default message reached %v, want jim and mia only", got)
	}
	if err := f.post(room.Id, "c2", "@Researcher2 anything?"); err != nil {
		t.Fatal(err)
	}
	if got := f.deps.admittedTo(); !slices.Equal(got, []string{"jim", "mia", "r2"}) {
		t.Fatalf("after @Researcher2: admitted to %v, want r2 added", got)
	}
}

// BDD-17.31: ambiguous typed name refused with the spec text.
func TestPostMessage_AmbiguousTypedNameRefused(t *testing.T) {
	f := newFixture(t)
	f.deps.ws.Agents = append(f.deps.ws.Agents, Agent{"mia2", "Mia"})
	room := f.squad("mia", "mia2", "jim")
	err := f.post(room.Id, "c1", "@Mia hello")
	if StatusOf(err) != http.StatusUnprocessableEntity || err.Error() != "Several members are called Mia — pick from the list" {
		t.Fatalf("err = %v (status %d), want the 422 ambiguity refusal", err, StatusOf(err))
	}
	if len(f.deps.admittedTo()) != 0 {
		t.Fatal("an ambiguous message was delivered")
	}
}

// BDD-17.20 / BDD-17.9 / FR-204 / FR-162.
func TestCreateAndMembers_TwoAgentRuleAndWorkspaceLock(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Create("sales", generated.RoomCreateRequest{Kind: "adhoc", AgentIds: []string{"jim"}}, "admin")
	if StatusOf(err) != 422 || err.Error() != "a room needs at least two agents" {
		t.Fatalf("one-agent room: %v", err)
	}
	_, err = f.svc.Create("sales", generated.RoomCreateRequest{Kind: "adhoc", AgentIds: []string{"jim", "ray"}}, "admin")
	if StatusOf(err) != 422 || err.Error() != "Ray is not in Sales" {
		t.Fatalf("cross-workspace member: %v", err)
	}
	room := f.squad("mia", "jim")
	_, err = f.svc.UpdateMembers(room.Id, generated.RoomMembersUpdateRequest{Action: "remove", AgentIds: []string{"jim"}}, "admin")
	if StatusOf(err) != 422 || err.Error() != "a room needs at least two agents — delete the room instead" {
		t.Fatalf("remove below two: %v", err)
	}
	got, err := f.svc.UpdateMembers(room.Id, generated.RoomMembersUpdateRequest{Action: "add", AgentIds: []string{"r2"}}, "admin")
	if err != nil || !slices.Equal(got.Members, []string{"mia", "jim", "r2"}) {
		t.Fatalf("add r2: %+v %v", got.Members, err)
	}
	if n := len(f.deps.audits); n < 2 || f.deps.audits[n-1].Action != "members_add" {
		t.Fatalf("membership change not audited: %+v", f.deps.audits)
	}
}

// FR-160 / BDD-17.20 row 3: the team room exists with two agents, hidden and
// read-only below two with its history kept, back with that history.
func TestTeamRoom_FollowsWorkspaceMembership(t *testing.T) {
	f := newFixture(t)
	list, err := f.svc.List("sales")
	if err != nil || len(list) != 1 || list[0].Kind != "team" || len(list[0].Members) != 3 || list[0].Hidden {
		t.Fatalf("team room = %+v, %v", list, err)
	}
	team := list[0].Id
	if err := f.post(team, "c1", "hello team"); err != nil {
		t.Fatal(err)
	}
	f.deps.ws.Agents = f.deps.ws.Agents[:1] // only Mia left
	list, _ = f.svc.List("sales")
	if !list[0].Hidden || list[0].ReadOnlyReason == nil || len(list[0].Members) != 1 {
		t.Fatalf("below two: %+v", list[0])
	}
	if err := f.post(team, "c2", "anyone?"); StatusOf(err) != 422 {
		t.Fatalf("a hidden team room accepted a message: %v", err)
	}
	f.deps.ws.Agents = append(f.deps.ws.Agents, Agent{"jim", "Jim"})
	list, _ = f.svc.List("sales")
	r, _ := f.svc.loadRoom(team)
	if list[0].Id != team || list[0].Hidden || len(r.entries) != 1 {
		t.Fatalf("team room must reappear with its history: %+v entries=%d", list[0], len(r.entries))
	}
}

// #1314: a storage failure is a 500, never an empty list.
func TestList_StorageFailureIsAnErrorNotAnEmptyList(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	if err := os.WriteFile(filepath.Join(f.dir, room.Id, "room.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := New(NewFileJournal(f.dir), DepsFrom(f.deps))
	list, err := fresh.List("sales")
	if err == nil || StatusOf(err) != http.StatusInternalServerError || list != nil {
		t.Fatalf("list over an unreadable room = %v, %v; want a 500 error", list, err)
	}
	f.deps.wsErr = errors.New("disk read error")
	if _, err := f.svc.List("sales"); StatusOf(err) != http.StatusInternalServerError {
		t.Fatalf("unreadable workspace: %v, want 500", err)
	}
}

// BDD-17.6 / BDD-17.28 / FR-217: /pause cancels not-yet-consumed requests
// (discarding admitted ones); the dequeue fence then refuses them; running
// work's answer is "after pause" and forwards no mentions; resume affects
// new messages only.
func TestPause_FenceCancelsPendingAndMarksAfterPause(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim", "r2")
	if err := f.post(room.Id, "c1", "status?"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	if !f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim") {
		t.Fatal("a current request must pass the fence")
	}
	if err := f.post(room.Id, "p", "/pause"); err != nil {
		t.Fatalf("/pause: %v", err)
	}
	st := f.states(room.Id)
	if st["mia"] != "cancelled/paused" || st["r2"] != "cancelled/paused" || st["jim"] != "working/" {
		t.Fatalf("after /pause: %v; want mia and r2 cancelled, jim still working", st)
	}
	mia := f.deliveryOf(room.Id, "mia")
	if !slices.Contains(f.deps.discarded, mia.RequestID) {
		t.Fatal("an admitted, cancelled request was not discarded from the member's ledger")
	}
	if f.svc.BeginConsume(room.Id, mia.RequestID, "main-mia") {
		t.Fatal("the dequeue fence let a cancelled request run")
	}
	if err := f.post(room.Id, "p2", "/resume"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.DeliverAnswer(context.Background(), Answer{
		RoomID: room.Id, RequestID: jim.RequestID, Responder: addressing.Pair{WorkspaceID: "sales", AgentID: "jim"},
		ResponderSessionID: "main-jim", Content: "done — @Mia can you draft?",
	}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	r, _ := f.svc.loadRoom(room.Id)
	last := r.entries[len(r.entries)-1].Entry
	if !last.AfterPause || len(last.Addressees) != 0 {
		t.Fatalf("answer to pre-pause work: after_pause=%v addressees=%v; want true and none", last.AfterPause, last.Addressees)
	}
	if got := f.deps.admittedTo(); countOf(got, "mia") != 1 {
		t.Fatalf("an after-pause answer forwarded its mention: admissions %v", got)
	}
}

// BDD-17.4 / BDD-17.19 / FR-165: an agent answer reaches only the members it
// names; @all reaches the others, never itself; no @ reaches nobody. The
// answer is mirrored once into the responder's main chat.
func TestAnswer_MentionFanOutAndMirror(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim", "r2")
	if err := f.post(room.Id, "c1", "@Jim plan it"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim")
	answer := func(text string) {
		t.Helper()
		if _, err := f.svc.DeliverAnswer(context.Background(), Answer{
			RoomID: room.Id, RequestID: jim.RequestID, Responder: addressing.Pair{WorkspaceID: "sales", AgentID: "jim"},
			ResponderSessionID: "main-jim", Content: text,
		}); err != nil {
			t.Fatalf("answer %q: %v", text, err)
		}
		f.drain(room.Id)
	}
	answer("done")
	if got := f.deps.admittedTo(); !slices.Equal(got, []string{"jim"}) {
		t.Fatalf("an answer without @ reached %v", got)
	}
	answer("@all regroup")
	if got := f.deps.admittedTo(); !slices.Equal(got, []string{"jim", "mia", "r2"}) {
		t.Fatalf("@all from Jim reached %v, want mia and r2 (not jim again)", got)
	}
	if len(f.deps.mirrors) != 2 || f.deps.mirrors[0].Ref.Role != "answer" || f.deps.mirrors[0].MainSessionID != "main-jim" ||
		f.deps.mirrors[0].MirrorID == f.deps.mirrors[1].MirrorID {
		t.Fatalf("mirrors = %+v, want one per answer into Jim's main with role answer", f.deps.mirrors)
	}
	if st := f.states(room.Id)["jim"]; st == "" {
		t.Fatal("no state for jim")
	}
}

// BDD-17.27 / BDD-17.33 / FR-216 / FR-252: a retried message admits only the
// members without receiver facts; a member that left shows cancelled —
// removed; nothing runs twice.
func TestRetry_ReconcilesReceiverFactsAndRemovedMembers(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim", "r2")
	f.deps.admitErr["jim"] = errors.New("crash before Jim's admission")
	if err := f.post(room.Id, "c1", "status?"); err != nil {
		t.Fatal(err)
	}
	if got := f.deps.admittedTo(); !slices.Equal(got, []string{"mia", "r2"}) {
		t.Fatalf("first pass admitted %v", got)
	}
	delete(f.deps.admitErr, "jim")
	f.deps.ws.Agents = []Agent{{"mia", "Mia"}, {"jim", "Jim"}} // r2 left the workspace
	if err := f.post(room.Id, "c1", "status?"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := f.deps.admittedTo(); !slices.Equal(got, []string{"jim", "mia", "r2"}) {
		t.Fatalf("after retry admitted %v; want jim once more and mia not again", got)
	}
	r, _ := f.svc.loadRoom(room.Id)
	if len(r.entries) != 1 {
		t.Fatalf("a retry recorded the message again: %d entries", len(r.entries))
	}
}

// §6.11 restart: a request a turn had taken shows interrupted (and is
// discarded, never replayed); a waiting one whose receiver facts cannot be
// read shows unknown, never working.
func TestRestart_RestoresProvableStatesOnly(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "c1", "status?"); err != nil {
		t.Fatal(err)
	}
	mia := f.deliveryOf(room.Id, "mia")
	jim := f.deliveryOf(room.Id, "jim")
	f.svc.BeginConsume(room.Id, mia.RequestID, "main-mia")
	f.deps.facts[mia.RequestID] = ReceiverFacts{Found: true, Delivered: true, SessionID: "main-mia"}
	f.deps.factsErr[jim.RequestID] = errors.New("ledger unreadable")
	fresh := New(NewFileJournal(f.dir), DepsFrom(f.deps))
	fx := &fixture{t: t, dir: f.dir, deps: f.deps, svc: fresh}
	st := fx.states(room.Id)
	if st["mia"] != "interrupted/restart" || st["jim"] != "unknown/" {
		t.Fatalf("after restart: %v; want mia interrupted, jim unknown", st)
	}
	if !slices.Contains(f.deps.discarded, mia.RequestID) {
		t.Fatal("interrupted work must be discarded so it is never replayed")
	}
}

// §6.14 / BDD-17.30: allocated cost = turn cost × room inputs ÷ all inputs,
// in micro-dollars, each turn counted once.
func TestAddTurnCost_AllocatesShareOncePerTurn(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	c := TurnCost{RoomID: room.Id, TurnID: "turn-1", CostUSD: 0.03, Priced: true, Inputs: 3, RoomInputs: 1}
	f.svc.AddTurnCost(c)
	f.svc.AddTurnCost(c)
	got, _ := f.svc.Get(room.Id)
	if got.AllocatedCostMicroUsd != 10000 || got.AllocatedCostUnpriced {
		t.Fatalf("allocated = %d unpriced=%v, want 10000 (one cent) counted once", got.AllocatedCostMicroUsd, got.AllocatedCostUnpriced)
	}
	f.svc.AddTurnCost(TurnCost{RoomID: room.Id, TurnID: "turn-2", Inputs: 1, RoomInputs: 1})
	if got, _ = f.svc.Get(room.Id); !got.AllocatedCostUnpriced || got.AllocatedCostMicroUsd != 10000 {
		t.Fatalf("an unpriced turn must add nothing and set the flag: %+v", got)
	}
}

// FR-205: anything but the six room commands is refused, naming the
// members' (or the @-named agent's) main chats.
func TestCommands_SteeringRefused(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	miaMain, _ := session.MainSessionID("sales", "mia")
	jimMain, _ := session.MainSessionID("sales", "jim")
	err := f.post(room.Id, "x1", "/model gpt-x")
	if StatusOf(err) != 422 || err.Error() != "Rooms don't steer agents - use Mia's or Jim's main chat: "+miaMain+", "+jimMain {
		t.Fatalf("/model: %v", err)
	}
	err = f.post(room.Id, "x2", "/goal @Jim ship it")
	if StatusOf(err) != 422 || err.Error() != "Rooms don't steer agents - use Jim's main chat: "+jimMain {
		t.Fatalf("/goal @Jim: %v", err)
	}
	if len(f.deps.admittedTo()) != 0 {
		t.Fatal("a refused command reached a member")
	}
}

// K45: the snapshot carries entries after the cursor and the current states.
func TestAttach_SnapshotAfterCursor(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "c1", "one"); err != nil {
		t.Fatal(err)
	}
	if err := f.post(room.Id, "c2", "two"); err != nil {
		t.Fatal(err)
	}
	var first generated.RoomSnapshotFrame
	if err := f.svc.Attach(room.Id, 0, func(s generated.RoomSnapshotFrame) { first = s }); err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 2 || !first.Complete || len(first.MemberStates) != 4 || first.Seq < first.Entries[1].Seq {
		t.Fatalf("snapshot = %d entries, complete=%v, %d states, seq=%d", len(first.Entries), first.Complete, len(first.MemberStates), first.Seq)
	}
	var next generated.RoomSnapshotFrame
	_ = f.svc.Attach(room.Id, first.Entries[0].Seq, func(s generated.RoomSnapshotFrame) { next = s })
	if len(next.Entries) != 1 || next.Entries[0].Text != "two" {
		t.Fatalf("after the first entry's cursor: %+v", next.Entries)
	}
}

func countOf(in []string, v string) int {
	n := 0
	for _, x := range in {
		if x == v {
			n++
		}
	}
	return n
}

// bigRoom is an in-memory room with n entries (seq 1..n).
func bigRoom(n int) *room {
	r := newRoom()
	r.rec = roomRecord{ID: "big", WorkspaceID: "sales", Kind: KindAdhoc, Name: "Big", Members: []string{"mia", "jim"}, Muted: []string{}}
	for i := 1; i <= n; i++ {
		r.addEntry(storedEntry{Entry: generated.RoomEntry{
			Id: fmt.Sprintf("e%d", i), RoomId: "big", Seq: int64(i), Text: fmt.Sprintf("m%d", i),
			Author: generated.RoomEntryAuthor{Kind: "user"}, Addressees: []string{}, Timestamp: "2026-10-14T00:00:00Z",
		}})
	}
	return r
}

// Architect ruling (Q3): a snapshot serves the NEWEST <= 10,000 entries after
// the cursor, in seq order, complete always true, has_older when entries were
// left out.
func TestSnapshot_NewestEntriesWithHasOlder(t *testing.T) {
	f := newFixture(t)
	r := bigRoom(10050)
	ws, _, _ := f.deps.LookupWorkspace("sales")
	snap := f.svc.snapshot(r, ws, 0)
	if len(snap.Entries) != 10000 || snap.Entries[0].Seq != 51 || snap.Entries[9999].Seq != 10050 {
		t.Fatalf("got %d entries, first seq %d, last seq %d; want 10000, 51..10050",
			len(snap.Entries), snap.Entries[0].Seq, snap.Entries[len(snap.Entries)-1].Seq)
	}
	if !snap.Complete || snap.HasOlder == nil || !*snap.HasOlder {
		t.Fatalf("complete=%v has_older=%v, want both true", snap.Complete, snap.HasOlder)
	}
	near := f.svc.snapshot(r, ws, 10040)
	if len(near.Entries) != 10 || near.Entries[0].Seq != 10041 || (near.HasOlder != nil && *near.HasOlder) || !near.Complete {
		t.Fatalf("near-head cursor: %d entries, has_older=%v", len(near.Entries), near.HasOlder)
	}
}

// Over the cap, member_states drops the oldest terminal states, never a live
// one.
func TestSnapshot_MemberStatesNeverDropLiveStates(t *testing.T) {
	r := bigRoom(1)
	// The live states are the OLDEST, so an "oldest first, any kind" cut
	// would hit them first.
	for i := 0; i < 20; i++ {
		r.putDelivery(&deliveryRecord{RequestID: fmt.Sprintf("l%02d", i), EntryID: "e1", AgentID: fmt.Sprintf("live%02d", i), State: StateAdmitted})
	}
	for i := 0; i < 10040; i++ {
		r.putDelivery(&deliveryRecord{RequestID: fmt.Sprintf("t%05d", i), EntryID: "e1", AgentID: fmt.Sprintf("a%05d", i), State: StateAnswered})
	}
	states := memberStates(r, maxSnapshotEntries)
	live, newest, oldest := 0, false, false
	for _, d := range states {
		switch {
		case d.State == StateAdmitted:
			live++
		case d.RequestId == "t10039":
			newest = true
		case d.RequestId == "t00000":
			oldest = true
		}
	}
	if len(states) != maxSnapshotEntries || live != 20 || !newest || oldest {
		t.Fatalf("%d states, %d live, newest terminal kept=%v, oldest kept=%v; want 10000, all 20 live, newest kept, oldest dropped",
			len(states), live, newest, oldest)
	}
}

// WU-15 ruling 06:00Z: a failed mirror keeps the committed room answer,
// returns the error, writes no mirror and holds the mentions; the same answer
// sent again repairs the mirror exactly once and then fans the mentions out.
func TestAnswer_FailedMirrorIsReturnedAndRepairedOnce(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim", "r2")
	if err := f.post(room.Id, "c1", "@Jim plan it"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	f.svc.BeginConsume(room.Id, jim.RequestID, "main-jim")
	answer := Answer{RoomID: room.Id, RequestID: jim.RequestID, Responder: addressing.Pair{WorkspaceID: "sales", AgentID: "jim"},
		ResponderSessionID: "main-jim", Content: "done @Mia"}
	f.deps.mirrorErr = errors.New("disk full")
	if _, err := f.svc.DeliverAnswer(context.Background(), answer); !errors.Is(err, ErrMirrorFailed) {
		t.Fatalf("err = %v, want ErrMirrorFailed (never swallowed)", err)
	}
	r, _ := f.svc.loadRoom(room.Id)
	if n := countAnswers(r, jim.RequestID); n != 1 || len(f.deps.mirrors) != 0 {
		t.Fatalf("after a failed mirror: %d answers, %d mirrors; want the answer kept and no mirror", n, len(f.deps.mirrors))
	}
	if got := f.deps.admittedTo(); slices.Contains(got, "mia") {
		t.Fatal("mentions must wait for the mirror")
	}
	f.deps.mirrorErr = nil
	if _, err := f.svc.DeliverAnswer(context.Background(), answer); err != nil {
		t.Fatalf("repeat: %v", err)
	}
	f.drain(room.Id)
	if n := countAnswers(r, jim.RequestID); n != 1 || len(f.deps.mirrors) != 1 {
		t.Fatalf("after the repeat: %d answers, %d mirrors; want exactly one of each", n, len(f.deps.mirrors))
	}
	if got := f.deps.admittedTo(); countOf(got, "mia") != 1 {
		t.Fatalf("the repaired answer's mention must reach Mia once: %v", got)
	}
	if _, err := f.svc.DeliverAnswer(context.Background(), answer); err != nil || countAnswers(r, jim.RequestID) != 2 {
		t.Fatalf("a later answer with a mirrored predecessor is a new answer: err=%v answers=%d", err, countAnswers(r, jim.RequestID))
	}
}

func countAnswers(r *room, requestID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.entries {
		if e.AnswersRequest == requestID {
			n++
		}
	}
	return n
}

// WU-15 ruling 06:00Z: shares round to the nearest micro-dollar, halves up.
func TestAllocate_RoundsHalvesUp(t *testing.T) {
	cases := []struct {
		cost      float64
		in, rooms int
		want      int64
	}{
		{0.0000015, 1, 1, 2}, // 1.5 µ$ -> 2
		{0.000001, 2, 1, 1},  // 0.5 µ$ -> 1
		{0.0000004, 1, 1, 0}, // 0.4 µ$ -> 0
		{0.03, 3, 1, 10000},  // BDD-17.30
	}
	for _, c := range cases {
		if got := allocate(TurnCost{CostUSD: c.cost, Priced: true, Inputs: c.in, RoomInputs: c.rooms}); got != c.want {
			t.Errorf("allocate(%v, %d/%d) = %d, want %d", c.cost, c.rooms, c.in, got, c.want)
		}
	}
}
