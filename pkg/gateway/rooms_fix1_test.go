package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// WU-15 fix round 1, gateway level. Real boot, HTTP, agent loop and stores
// (the WU-15 RED harness); only the paid model is scripted. Expected values
// come from the spec (FR-162, FR-172, FR-176, FR-216, §6.11) and the
// 2026-10-15 rulings, not from the implementation.

// wu15NewAuditRows returns the room.changed audit rows appended since before.
func wu15NewAuditRows(t *testing.T, before, after [][]byte) []audit.Entry {
	t.Helper()
	old := map[string]int{}
	for _, line := range before {
		old[string(line)]++
	}
	var out []audit.Entry
	for _, line := range after {
		if old[string(line)] > 0 {
			old[string(line)]--
			continue
		}
		var entry audit.Entry
		require.NoError(t, json.Unmarshal(line, &entry))
		if entry.Event == audit.EventRoomChanged {
			out = append(out, entry)
		}
	}
	return out
}

// requireRoomAudit (TA-08) finds exactly one new room.changed row for the
// room with the expected action, user, workspace and exact agent set.
func requireRoomAudit(t *testing.T, rows []audit.Entry, roomID, action, user string, agents ...string) {
	t.Helper()
	var matches []audit.Entry
	for _, row := range rows {
		if row.Details["room_id"] == roomID && row.Details["action"] == action {
			matches = append(matches, row)
		}
	}
	require.Len(t, matches, 1, "FR-172: one %s row for room %s in %+v", action, roomID, rows)
	row := matches[0]
	require.Equal(t, user, row.User, "FR-172: %s is attributed to %q", action, user)
	require.Equal(t, wu15Workspace, row.Details["workspace_id"])
	require.False(t, row.Timestamp.IsZero())
	var got []string
	if ids, ok := row.Details["agent_ids"].([]any); ok {
		for _, id := range ids {
			s, isString := id.(string)
			require.True(t, isString, "FR-172: agent ids are strings")
			got = append(got, s)
		}
	}
	require.ElementsMatch(t, agents, got, "FR-172: %s names exactly the affected agents", action)
}

// S5 + TA-08 (FR-172): a workspace team change made by a signed-in user is
// audited, for every affected room, under that user and the exact action.
func TestRoomsFix1_WorkspaceDrivenMembershipIsAttributed(t *testing.T) {
	h := wu15NewHarness(t)
	team := wu15TeamRoom(t, h.listRooms(wu15Workspace))
	squad := h.create("squad", "Launch", "mia", "jim", "researcher2")
	before := wu15ReadAuditLines(t, h.home)
	h.team("mia", "jim", "gp")
	rows := wu15NewAuditRows(t, before, wu15ReadAuditLines(t, h.home))
	requireRoomAudit(t, rows, squad.Id, "members_left_workspace", "wu15-tester", "researcher2")
	requireRoomAudit(t, rows, team.Id, "members_left_workspace", "wu15-tester", "researcher2")

	// Direct changes keep their exact actions (the TA-08 predicate).
	before = wu15ReadAuditLines(t, h.home)
	status, body := h.http(http.MethodPatch, "/api/v1/rooms/"+squad.Id+"/members", gen.RoomMembersUpdateRequest{Action: gen.RoomMembersUpdateRequestActionMute, AgentIds: []string{"jim"}})
	require.Equal(t, http.StatusOK, status, "%s", body)
	requireRoomAudit(t, wu15NewAuditRows(t, before, wu15ReadAuditLines(t, h.home)), squad.Id, "members_mute", "wu15-tester", "jim")
	before = wu15ReadAuditLines(t, h.home)
	status, body = h.http(http.MethodDelete, "/api/v1/rooms/"+squad.Id, nil)
	require.Equal(t, http.StatusNoContent, status, "%s", body)
	requireRoomAudit(t, wu15NewAuditRows(t, before, wu15ReadAuditLines(t, h.home)), squad.Id, "deleted", "wu15-tester", "mia", "jim")
}

// S3 + TA-03 (FR-162, K24): the membership PATCH refuses an over-long id
// array and malformed bodies in both inbound-validation modes, changes
// nothing when it refuses, and still accepts valid changes in each mode.
func TestRoomsFix1_MembershipPatchBoundsInBothValidationModes(t *testing.T) {
	for _, validation := range []bool{false, true} {
		name := "validation_off"
		if validation {
			name = "validation_on"
		}
		t.Run(name, func(t *testing.T) {
			h := wu15NewHarness(t)
			room := h.create("squad", "Launch", "mia", "jim", "researcher2")
			h.cfg.Gateway.ValidateInbound = validation
			path := "/api/v1/rooms/" + room.Id + "/members"
			status, body := h.http(http.MethodPatch, path, gen.RoomMembersUpdateRequest{Action: gen.RoomMembersUpdateRequestActionMute, AgentIds: []string{"jim"}})
			require.Equal(t, http.StatusOK, status, "control in this mode: %s", body)
			status, body = h.http(http.MethodPatch, path, gen.RoomMembersUpdateRequest{Action: gen.RoomMembersUpdateRequestActionUnmute, AgentIds: []string{"jim"}})
			require.Equal(t, http.StatusOK, status, "control in this mode: %s", body)
			baseline := h.room(room.Id)
			auditBefore := wu15ReadAuditLines(t, h.home)
			many := make([]string, 257)
			for i := range many {
				many[i] = "jim"
			}
			for _, tc := range []struct {
				name string
				body []byte
			}{
				{"257 ids", fix1JSON(t, gen.RoomMembersUpdateRequest{Action: gen.RoomMembersUpdateRequestActionMute, AgentIds: many})},
				{"missing action", []byte(`{"agent_ids":["jim"]}`)},
				{"unknown action", []byte(`{"action":"promote","agent_ids":["jim"]}`)},
				{"empty ids", []byte(`{"action":"mute","agent_ids":[]}`)},
				{"null ids", []byte(`{"action":"mute","agent_ids":null}`)},
				{"wrong type", []byte(`{"action":"mute","agent_ids":"jim"}`)},
			} {
				status, data := h.rawHTTP(http.MethodPatch, path, tc.body)
				require.True(t, status == http.StatusBadRequest || status == http.StatusUnprocessableEntity, "%s: status %d %s", tc.name, status, data)
				var refusal gen.ErrorResponse
				require.NoError(t, json.Unmarshal(data, &refusal), "%s: contract error body", tc.name)
				require.NotEmpty(t, refusal.Error)
			}
			after := h.room(room.Id)
			require.Equal(t, baseline.Members, after.Members, "a refused PATCH changed membership")
			require.Equal(t, baseline.Muted, after.Muted, "a refused PATCH changed mutes")
			require.Empty(t, wu15NewAuditRows(t, auditBefore, wu15ReadAuditLines(t, h.home)), "a refused PATCH was audited as a change")
		})
	}
}

func fix1JSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

// S2 (FR-176): an answer over the effective message limit, through the real
// answer_request reply path, is refused with a clear error and changes no
// room entry, main-chat mirror or member; an at-limit answer is posted once.
func TestRoomsFix1_AnswerOverTheMessageLimitIsRefused(t *testing.T) {
	h := wu15NewHarness(t)
	h.cfg.SessionMessaging.SteerBody = 2000
	room := h.create("squad", "Launch", "mia", "jim", "researcher2")
	h.post(room.Id, "request", "@Jim answer")
	delivery := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "request"), "jim")
	before := h.snapshot(room.Id)
	reply := func(text string) error {
		_, err := h.al.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{
			ReplyTo: delivery.RequestId, Content: text, ResponderSessionID: h.main("jim"),
			Author: tools.SendOrigin{WorkspaceID: wu15Workspace, AgentID: "jim"},
		})
		return err
	}
	over := "@Mia " + strings.Repeat("é", 998) // 5 + 1996 = 2001 bytes
	err := reply(over)
	require.Error(t, err, "S2: an over-limit answer was accepted")
	require.Contains(t, err.Error(), "2000 bytes", "S2: the refusal names the limit")
	after := h.snapshot(room.Id)
	require.Equal(t, len(before.Entries), len(after.Entries), "S2: a refused answer was posted")
	for _, msg := range h.messages("jim") {
		if msg.Content != nil {
			require.NotContains(t, *msg.Content, "éééé", "S2: a refused answer was mirrored into the main chat")
		}
	}
	for _, state := range after.MemberStates {
		require.NotEqual(t, "mia", state.AgentId, "S2: a refused answer passed its mention on")
	}
	at := "@Mia " + strings.Repeat("a", 1995)
	require.NoError(t, reply(at), "control: an at-limit answer")
	require.Len(t, h.snapshot(room.Id).Entries, len(before.Entries)+1, "control: posted once")
}

// Ruling A (§6.11, FR-252): a request that waited for its stopped member is
// handed to the turn that revives the member; when that turn ends without
// answering, the one reminder runs and the request then fails - the member is
// never left "working" - and a restart keeps that outcome.
func TestRoomsFix1_HeldRequestSettlesWhenTheRevivingTurnDoesNotAnswer(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.stopMain("mia")
	h.post(room.Id, "held", "@Mia HELD_TASK")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "held"), "mia")
	h.waitState(room.Id, request.RequestId, "held")
	p := &wu15ScriptProvider{answer: "plain reply, no answer_request"}
	h.provider("mia", p)
	conn := h.connect()
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, conn, "catch_up_complete")
	h.startLoop()
	h.sendMain(conn, "mia", "WEB_REVIVE")
	h.waitState(room.Id, request.RequestId, "failed")
	wu15AssertProviderSaw(t, p, "HELD_TASK")
	snap := h.snapshot(room.Id)
	for _, state := range snap.MemberStates {
		if state.RequestId == request.RequestId {
			require.NotNil(t, state.Reason)
			require.Equal(t, "no_answer", *state.Reason, "§6.11: unanswered after the one reminder")
		}
	}
	h = h.restart()
	h.waitState(room.Id, request.RequestId, "failed")
}

// Ruling A: a reviving turn that fails fails the waiting request it was
// handed (it is not left "working").
func TestRoomsFix1_HeldRequestFailsWithItsFailedRevivingTurn(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.stopMain("mia")
	h.post(room.Id, "held", "@Mia HELD_TASK")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "held"), "mia")
	h.waitState(room.Id, request.RequestId, "held")
	p := &wu15ScriptProvider{fail: errors.New("provider unavailable")}
	h.provider("mia", p)
	conn := h.connect()
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, conn, "catch_up_complete")
	h.startLoop()
	h.sendMain(conn, "mia", "WEB_REVIVE")
	snap := h.waitState(room.Id, request.RequestId, "failed")
	for _, state := range snap.MemberStates {
		if state.RequestId == request.RequestId {
			require.NotNil(t, state.Reason)
			require.Equal(t, "turn_failed", *state.Reason, "ruling A: settled by its failed turn, not left to the reminder")
		}
	}
}

// TA-04 (FR-216, §6.11): after a restart, interrupted work is never given to
// the member again; a fresh request, processed after it, is the positive
// control that the loop really ran.
func TestRoomsFix1_RestartNeverReplaysInterruptedWork(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	p := &wu15ScriptProvider{answer: "not committed", entered: make(chan struct{}), release: make(chan struct{})}
	h.provider("mia", p)
	h.startLoop()
	h.post(room.Id, "interrupted", "@Mia unfinished request")
	old := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "interrupted"), "mia")
	h.waitState(room.Id, old.RequestId, "working")
	select {
	case <-p.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("instrument: the interrupted input never reached the provider")
	}
	restore := wu15DurableCrashImage(t, h.home)
	h.shutdown()
	restore()
	h = wu15Boot(t, h.home, h.cfg)
	h.waitState(room.Id, old.RequestId, "interrupted")
	restarted := &wu15ScriptProvider{answer: "fresh answer", autoReply: true}
	h.provider("mia", restarted)
	h.startLoop()
	h.post(room.Id, "fresh", "@Mia fresh request")
	fresh := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "fresh"), "mia")
	// Whether the restart left Mia's main stopped or not, the person writing
	// to her runs her again (a stopped main holds the fresh request until
	// then): the positive control never depends on that restart timing.
	conn := h.connect()
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, conn, "catch_up_complete")
	h.sendMain(conn, "mia", "WAKE_AFTER_RESTART")
	h.waitState(room.Id, fresh.RequestId, "answered")
	// Retained/quoted model history is legitimate, regardless of how often
	// its text appears. A real re-delivery must cross the working fence again.
	// Pin the actual request's durable consume count instead of scanning history.
	data, readErr := os.ReadFile(filepath.Join(rooms.DefaultDir(h.home), room.Id, "deliveries.jsonl"))
	require.NoError(t, readErr)
	working := 0
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var record map[string]any
		require.NoError(t, json.Unmarshal(line, &record))
		if record["request_id"] == old.RequestId && record["state"] == "working" {
			working++
		}
	}
	require.Equal(t, 1, working, "§6.11: exactly the original pre-crash consume, never a restart re-delivery")
	require.NotEmpty(t, restarted.inputs(), "instrument: the restarted member really ran")
	capture, found, err := h.al.RequestLedger().Get(h.main("mia"), old.RequestId)
	require.NoError(t, err)
	require.True(t, found && capture.Discarded, "§6.11: interrupted work is discarded")
	// member_states keeps only Mia's latest terminal state (ruling Q2); the
	// interrupted request's own state is on its entry.
	still := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "interrupted"), "mia")
	require.Equal(t, "interrupted", still.State, "§6.11: the interrupted request stays interrupted")
}

// SF#8: a room response body that cannot be written is logged at a level the
// default configuration keeps (the status is already sent).
func TestRoomsFix1_BrokenRoomResponseBodyIsLoggedAsError(t *testing.T) {
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)
	w := &failingBodyWriter{ResponseRecorder: httptest.NewRecorder()}
	writeRoomJSON(w, http.StatusOK, []gen.Room{{Id: "r1"}})
	require.Contains(t, logged.String(), "level=ERROR", "SF#8: a failed room body write left no error at the default level")
	require.True(t, slices.Contains([]int{http.StatusOK}, w.Code))
}

type failingBodyWriter struct{ *httptest.ResponseRecorder }

func (*failingBodyWriter) Write([]byte) (int, error) { return 0, errors.New("connection reset") }
