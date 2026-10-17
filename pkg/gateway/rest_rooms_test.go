package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Reachability of group-chat rooms through the real gateway routes and the
// real agent loop (omnipus-system-agent §13 rooms row; K23, K24, K26, K33):
// the workspace room list, squad creation, a room message admitted into a
// member's MAIN session with room_ref, the member's answer returned into the
// room and mirrored once into its main chat. #1314: an unreadable room is a
// 500 with the error body, never 200 with an empty list.

type roomsHarness struct {
	t   *testing.T
	h   *WSHandler
	api *restAPI
	hub *roomsHub
}

func newRoomsHarness(t *testing.T) *roomsHarness {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	// Never the real ~/.omnipus, never the package directory.
	testHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", testHome)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(testHome, "workspace"),
				DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom},
				{ID: "jim", Name: "Jim", Type: config.AgentTypeCustom},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	home := config.OmnipusHomeDir()
	if err := writeWorkspaceFile(home, storedWorkspace{ID: "sales", Name: "Sales", CoreTeam: []string{"mia", "jim"}}); err != nil {
		t.Fatalf("write workspace: %v", err)
	}
	for _, id := range []string{"mia", "jim"} {
		inst, ok := al.GetRegistry().GetAgent(id)
		if !ok {
			t.Fatalf("agent %s not registered", id)
		}
		inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyAllow}})
	}
	h := newWSHandler(msgBus, al, "http://localhost:3000")
	h.home = home
	al.SetAddressDeps(gatewayAddressDeps{h: h})
	hub := newRoomsHub(h, home)
	h.rooms = hub
	api := &restAPI{agentLoop: al, homePath: home}
	api.rooms.Store(hub)
	return &roomsHarness{t: t, h: h, api: api, hub: hub}
}

func (rh *roomsHarness) do(method, path, body string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	rh.t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func TestRoomsREST_MessageReachesMemberMainAndAnswerReturns(t *testing.T) {
	rh := newRoomsHarness(t)
	w := rh.do(http.MethodGet, "/api/v1/workspaces/sales/rooms", "", rh.api.HandleWorkspaces)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var list []generated.Room
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].Kind != "team" || len(list[0].Members) != 2 {
		t.Fatalf("team room list = %s (%v)", w.Body.String(), err)
	}
	w = rh.do(http.MethodPost, "/api/v1/workspaces/sales/rooms", `{"kind":"squad","name":"Launch","agent_ids":["mia","jim"]}`, rh.api.HandleWorkspaces)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var squad generated.Room
	_ = json.Unmarshal(w.Body.Bytes(), &squad)
	w = rh.do(http.MethodPost, "/api/v1/rooms/"+squad.Id+"/messages", `{"client_message_id":"c1","text":"@Mia draft it","mentions":[]}`, rh.api.HandleRooms)
	if w.Code != http.StatusAccepted {
		t.Fatalf("post: %d %s", w.Code, w.Body.String())
	}
	rh.hub.svc.WaitIdleForTest()
	mainID, _ := session.MainSessionID("sales", "mia")
	al := rh.api.agentLoop
	entries, err := al.GetSessionStore().ReadTranscript(mainID)
	if err != nil || len(entries) != 1 || entries[0].RoomRef == nil || entries[0].RoomRef.Role != "request" ||
		entries[0].RoomRef.RoomId != squad.Id || !strings.HasPrefix(entries[0].Content, "@Mia draft it") {
		t.Fatalf("Mia's main after the room message: %+v (err %v)", entries, err)
	}
	requestID := entries[0].ID
	jimMain, _ := session.MainSessionID("sales", "jim")
	if jimEntries, _ := al.GetSessionStore().ReadTranscript(jimMain); len(jimEntries) != 0 {
		t.Fatalf("Jim was not @-named but received %d entries", len(jimEntries))
	}
	receipt, err := al.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{
		ReplyTo: requestID, Content: "Draft attached.",
		Author: tools.SendOrigin{WorkspaceID: "sales", AgentID: "mia"}, ResponderSessionID: mainID,
	})
	if err != nil || receipt.ReplyID == "" {
		t.Fatalf("answer: %+v %v", receipt, err)
	}
	var snap generated.RoomSnapshotFrame
	if err := rh.hub.svc.Attach(squad.Id, 0, func(s generated.RoomSnapshotFrame) { snap = s }); err != nil {
		t.Fatal(err)
	}
	if len(snap.Entries) != 2 || snap.Entries[1].Author.Kind != "agent" || *snap.Entries[1].Author.AgentId != "mia" ||
		snap.Entries[1].Text != "Draft attached." {
		t.Fatalf("room transcript = %+v", snap.Entries)
	}
	entries, _ = al.GetSessionStore().ReadTranscript(mainID)
	if len(entries) != 2 || entries[1].RoomRef == nil || entries[1].RoomRef.Role != "answer" || entries[1].Content != "Draft attached." {
		t.Fatalf("Mia's main must hold one answer mirror: %+v", entries)
	}
}

func TestRoomsREST_RefusalStatuses(t *testing.T) {
	rh := newRoomsHarness(t)
	cases := []struct {
		name, method, path, body string
		handler                  http.HandlerFunc
		want                     int
		text                     string
	}{
		{"one agent", http.MethodPost, "/api/v1/workspaces/sales/rooms", `{"kind":"adhoc","agent_ids":["mia"]}`, rh.api.HandleWorkspaces, 422, "a room needs at least two agents"},
		{"unknown field", http.MethodPost, "/api/v1/workspaces/sales/rooms", `{"kind":"adhoc","agent_ids":["mia","jim"],"x":1}`, rh.api.HandleWorkspaces, 400, ""},
		{"missing agent_ids", http.MethodPost, "/api/v1/workspaces/sales/rooms", `{"kind":"adhoc"}`, rh.api.HandleWorkspaces, 400, ""},
		{"unknown workspace", http.MethodGet, "/api/v1/workspaces/nope/rooms", "", rh.api.HandleWorkspaces, 404, ""},
		{"unknown room", http.MethodGet, "/api/v1/rooms/nope", "", rh.api.HandleRooms, 404, ""},
		{"missing mentions", http.MethodPost, "/api/v1/rooms/nope/messages", `{"client_message_id":"c","text":"hi"}`, rh.api.HandleRooms, 400, ""},
	}
	for _, c := range cases {
		w := rh.do(c.method, c.path, c.body, c.handler)
		if w.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, w.Code, c.want, w.Body.String())
			continue
		}
		var e generated.ErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error == "" {
			t.Errorf("%s: body %q is not an ErrorResponse", c.name, w.Body.String())
		}
		if c.text != "" && e.Error != c.text {
			t.Errorf("%s: error %q, want %q", c.name, e.Error, c.text)
		}
	}
	if list, _ := rh.hub.svc.List("sales"); len(list) != 1 {
		t.Fatalf("a refusal persisted a room: %d rooms", len(list))
	}
}

// #1314: an unreadable room makes the list a 500 with the error body.
func TestRoomsREST_ListStorageFailureIs500(t *testing.T) {
	rh := newRoomsHarness(t)
	w := rh.do(http.MethodPost, "/api/v1/workspaces/sales/rooms", `{"kind":"adhoc","agent_ids":["mia","jim"]}`, rh.api.HandleWorkspaces)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var room generated.Room
	_ = json.Unmarshal(w.Body.Bytes(), &room)
	path := filepath.Join(rooms.DefaultDir(rh.api.homePath), room.Id, "room.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A fresh service (a restart) reads the broken record.
	fresh := newRoomsHub(rh.h, rh.api.homePath)
	rh.api.rooms.Store(fresh)
	w = rh.do(http.MethodGet, "/api/v1/workspaces/sales/rooms", "", rh.api.HandleWorkspaces)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("list over an unreadable room: %d %s, want 500", w.Code, w.Body.String())
	}
	var e generated.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error == "" || strings.Contains(w.Body.String(), "[") {
		t.Fatalf("500 body %q must be an ErrorResponse, not a list", w.Body.String())
	}
}

// faultJournal fails the named journal operations (the rooms.Journal
// post-back seam), to prove every room route answers a storage fault with a
// 500 ErrorResponse.
type faultJournal struct {
	rooms.Journal
	fail map[string]bool
}

func (j *faultJournal) Create(id string) error {
	if j.fail["create"] {
		return os.ErrPermission
	}
	return j.Journal.Create(id)
}

func (j *faultJournal) WriteRecord(id string, rec any) error {
	if j.fail["write"] {
		return os.ErrPermission
	}
	return j.Journal.WriteRecord(id, rec)
}

func (j *faultJournal) Append(id, stream string, rec any) error {
	if j.fail["append"] {
		return os.ErrPermission
	}
	return j.Journal.Append(id, stream, rec)
}

func (j *faultJournal) Remove(id string) error {
	if j.fail["remove"] {
		return os.ErrPermission
	}
	return j.Journal.Remove(id)
}

// useJournal replaces the harness's room service with one over journal.
func (rh *roomsHarness) useJournal(j rooms.Journal) {
	hub := &roomsHub{subs: map[string]map[*wsConn]struct{}{}}
	hub.svc = rooms.New(j, rooms.DepsFrom(&gatewayRoomDeps{h: rh.h, home: rh.api.homePath, hub: hub}))
	rh.h.agentLoop.SetRoomRuntime(agentRoomRuntime{svc: hub.svc})
	rh.h.rooms = hub
	rh.hub = hub
	rh.api.rooms.Store(hub)
}

func TestRoomsREST_StorageFaultIs500OnEveryRoute(t *testing.T) {
	rh := newRoomsHarness(t)
	fj := &faultJournal{Journal: rooms.NewFileJournal(rooms.DefaultDir(rh.api.homePath)), fail: map[string]bool{}}
	rh.useJournal(fj)
	w := rh.do(http.MethodPost, "/api/v1/workspaces/sales/rooms", `{"kind":"adhoc","agent_ids":["mia","jim"]}`, rh.api.HandleWorkspaces)
	if w.Code != http.StatusCreated {
		t.Fatalf("control create: %d %s", w.Code, w.Body.String())
	}
	var room generated.Room
	_ = json.Unmarshal(w.Body.Bytes(), &room)
	base := "/api/v1/rooms/" + room.Id
	cases := []struct {
		name, fault, method, path, body string
		handler                         http.HandlerFunc
	}{
		{"createRoom", "create", http.MethodPost, "/api/v1/workspaces/sales/rooms", `{"kind":"adhoc","agent_ids":["mia","jim"]}`, rh.api.HandleWorkspaces},
		{"updateRoomMembers", "write", http.MethodPatch, base + "/members", `{"action":"mute","agent_ids":["jim"]}`, rh.api.HandleRooms},
		{"postRoomMessage", "append", http.MethodPost, base + "/messages", `{"client_message_id":"c1","text":"status?","mentions":[]}`, rh.api.HandleRooms},
		// The deletion is saved first (its intent on the record, F04): a
		// failure to save it is the 500. A failure to remove the files after
		// that is not - see the end of this test.
		{"deleteRoom", "write", http.MethodDelete, base, "", rh.api.HandleRooms},
	}
	for _, c := range cases {
		fj.fail = map[string]bool{c.fault: true}
		res := rh.do(c.method, c.path, c.body, c.handler)
		var e generated.ErrorResponse
		if res.Code != http.StatusInternalServerError || json.Unmarshal(res.Body.Bytes(), &e) != nil || e.Error == "" {
			t.Errorf("%s under a %s fault: %d %q, want 500 with an ErrorResponse", c.name, c.fault, res.Code, res.Body.String())
		}
	}
	fj.fail = map[string]bool{}
	got, err := rh.hub.svc.Get(room.Id)
	if err != nil || len(got.Muted) != 0 {
		t.Fatalf("a failed members write changed the room: %+v %v", got, err)
	}
	// A deletion whose files cannot be removed is still a committed deletion
	// (204, the group is gone); the next load finishes the removal.
	gone := rh.do(http.MethodPost, "/api/v1/workspaces/sales/rooms", `{"kind":"adhoc","agent_ids":["mia","jim"]}`, rh.api.HandleWorkspaces)
	var doomed generated.Room
	_ = json.Unmarshal(gone.Body.Bytes(), &doomed)
	fj.fail = map[string]bool{"remove": true}
	if res := rh.do(http.MethodDelete, "/api/v1/rooms/"+doomed.Id, "", rh.api.HandleRooms); res.Code != http.StatusNoContent {
		t.Fatalf("delete with a failing file removal: %d %q, want 204 (the deletion is saved)", res.Code, res.Body.String())
	}
	fj.fail = map[string]bool{}
	if _, err := rh.hub.svc.Get(doomed.Id); rooms.StatusOf(err) != http.StatusNotFound {
		t.Fatalf("a committed deletion still serves the group: %v", err)
	}
	rh.useJournal(rooms.NewFileJournal(rooms.DefaultDir(rh.api.homePath)))
	if _, err := rh.hub.svc.Get(doomed.Id); rooms.StatusOf(err) != http.StatusNotFound {
		t.Fatalf("after a restart the deleted group is served: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(rooms.DefaultDir(rh.api.homePath), doomed.Id)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the next load did not finish removing the deleted group's files: %v", statErr)
	}
	rh.useJournal(fj)
	// getRoom: an unreadable record after a restart.
	if err := os.WriteFile(filepath.Join(rooms.DefaultDir(rh.api.homePath), room.Id, "room.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	rh.useJournal(rooms.NewFileJournal(rooms.DefaultDir(rh.api.homePath)))
	w = rh.do(http.MethodGet, base, "", rh.api.HandleRooms)
	var e generated.ErrorResponse
	if w.Code != http.StatusInternalServerError || json.Unmarshal(w.Body.Bytes(), &e) != nil || e.Error == "" {
		t.Fatalf("getRoom over an unreadable room: %d %q, want 500", w.Code, w.Body.String())
	}
}

// /members and /help are accepted (202) and change nothing on the server:
// no entry, no state, no stream position (architect ruling on Q1).
func TestRoomsREST_MembersAndHelpAreServerNoOps(t *testing.T) {
	rh := newRoomsHarness(t)
	w := rh.do(http.MethodPost, "/api/v1/workspaces/sales/rooms", `{"kind":"adhoc","agent_ids":["mia","jim"]}`, rh.api.HandleWorkspaces)
	var room generated.Room
	_ = json.Unmarshal(w.Body.Bytes(), &room)
	snapshot := func() generated.RoomSnapshotFrame {
		var s generated.RoomSnapshotFrame
		if err := rh.hub.svc.Attach(room.Id, 0, func(f generated.RoomSnapshotFrame) { s = f }); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()
	for i, cmd := range []string{"/members", "/help"} {
		body := `{"client_message_id":"cmd-` + string(rune('a'+i)) + `","text":"` + cmd + `","mentions":[]}`
		w := rh.do(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", body, rh.api.HandleRooms)
		if w.Code != http.StatusAccepted {
			t.Fatalf("%s: %d %s, want 202", cmd, w.Code, w.Body.String())
		}
	}
	after := snapshot()
	if after.Seq != before.Seq || len(after.Entries) != len(before.Entries) {
		t.Fatalf("seq %d -> %d, entries %d -> %d; /members and /help must change nothing",
			before.Seq, after.Seq, len(before.Entries), len(after.Entries))
	}
	// Positive control: an ordinary message does move the stream.
	rh.do(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", `{"client_message_id":"m1","text":"status?","mentions":[]}`, rh.api.HandleRooms)
	rh.hub.svc.WaitIdleForTest()
	if s := snapshot(); s.Seq == after.Seq || len(s.Entries) != len(after.Entries)+1 {
		t.Fatalf("control message did not move the stream: seq %d entries %d", s.Seq, len(s.Entries))
	}
}
