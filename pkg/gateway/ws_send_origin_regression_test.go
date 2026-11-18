package gateway

// ws_send_origin_regression_test.go — SV-16 (S-9; #1428 step 2 option C,
// qa-lead): the NAMED regressions of spec §6.1 S-9 must behave exactly as
// today once the send-origin pre-check lands — `ask_user_answer`,
// `session_mode_update`, `attach_session`, `attach_room`, `omnipus run`
// (its gateway surface IS the CLI-token connection), CLI token, room REST
// send, workspace-only upload and the skills REST surface. BR-22's
// browser-side room-composer assertions live in the frontend pack
// (`src/components/rooms/room-binding.regression.test.tsx`, RED step 0) and
// are not duplicated here.
//
// RED/GUARD note: every row below is GREEN BY DESIGN — these paths work
// today and the spec requires them to keep working. Their failability proof
// is CHECK's mutation run (a mutant that routes any of these frames through
// the new refusal — or refuses origin-less non-browser sends — must kill
// this file). The plan records the file as GUARD.
//
// ORACLE: each row asserts today's own reply on the real surface (an ack, a
// REST 200/201, the room path's own error when no hub is wired) and that no
// `send_refused` appears — a `send_refused` on any of these surfaces is the
// regression the spec forbids.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// soNoSendRefused is the shared regression oracle: today's reply, never a
// send_refused.
func soNoSendRefused(t *testing.T, frames []soFrame, phase string) {
	t.Helper()
	for _, f := range frames {
		require.NotEqual(t, "send_refused", f.Type,
			"%s: the send-origin rule must never refuse this surface (got %s)", phase, f.Raw)
	}
}

func soBoolPtr(b bool) *bool { return &b }

func TestSendOriginRegressions(t *testing.T) {
	s := newSendOriginSrv(t, &restMockProvider{})

	t.Run("ask_user_answer processed", func(t *testing.T) {
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		sid := s.mintChat(t, conn, soRowKey("sv16-ask", 1), "sv16 ask setup")
		soSend(t, conn, generated.AskUserAnswerFrame{
			Type: "ask_user_answer", SessionId: sid, CardId: "sv16-no-such-card", Cancel: boolPtr(true),
		})
		frames := soWindow(t, conn, soRefusalWindow)
		soNoSendRefused(t, frames, "ask_user_answer")
		require.NotEmpty(t, frames, "ask_user_answer must be processed by today's path (window: %v)", soTypes(frames))
	})

	t.Run("session_mode_update acknowledged", func(t *testing.T) {
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		sid := s.mintChat(t, conn, soRowKey("sv16-mode", 1), "sv16 mode setup")
		soSend(t, conn, generated.SessionModeUpdateFrame{Type: "session_mode_update", SessionId: sid, AutoApprove: false})
		frames := soWindow(t, conn, soRefusalWindow, "session_mode_updated")
		soNoSendRefused(t, frames, "session_mode_update")
		require.Contains(t, soTypes(frames), "session_mode_updated",
			"session_mode_update must keep its acknowledgement; window: %v", soTypes(frames))
	})

	t.Run("attach_session catch-up completes", func(t *testing.T) {
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		sid := s.mintChat(t, conn, soRowKey("sv16-attach", 1), "sv16 attach setup")
		attacher := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, attacher)
		soSend(t, attacher, generated.AttachSessionFrame{Type: "attach_session", SessionId: sid})
		frames := soWindow(t, attacher, 15*time.Second, "catch_up_complete")
		soNoSendRefused(t, frames, "attach_session")
		require.Contains(t, soTypes(frames), "catch_up_complete",
			"attach_session must complete today's catch-up; window: %v", soTypes(frames))
	})

	t.Run("attach_room reaches the room path", func(t *testing.T) {
		conn := s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		// No rooms hub is wired on this harness; the room path's own reply
		// is its "group chats are not available" error (ws_rooms.go) — the
		// frame must reach THAT path, never the send-origin refusal.
		soSend(t, conn, generated.AttachRoomFrame{Type: "attach_room", RoomId: "sv16-room", AfterSeq: 0})
		frames := soWindow(t, conn, soRefusalWindow)
		soNoSendRefused(t, frames, "attach_room")
		errs := soErrors(t, frames)
		require.NotEmpty(t, errs, "attach_room must reach the room path (window: %v)", soTypes(frames))
		require.Equal(t, "group chats are not available on this gateway", errs[0].Message,
			"attach_room must be answered by the room path's own refusal")
	})

	t.Run("cli token send and omnipus run surface", func(t *testing.T) {
		// `omnipus run` drives the gateway over exactly this surface: a
		// programmatic connection authenticating with the CLI token, sending
		// origin-less frames (P2 legacy).
		conn := s.dialCLIConn(t)
		drainAdmissionOneShot(t, conn)
		sid := s.mintChat(t, conn, soRowKey("sv16-cli", 1), "sv16 cli mint")
		soSend(t, conn, soMessage(&sid, nil, "so-sv16-cli-1", "cli token regression send"))
		frames := soWindow(t, conn, 4*time.Second, "message_status")
		soNoSendRefused(t, frames, "cli token send")
		require.Contains(t, soTypes(frames), "message_status",
			"the CLI-token path must keep accepting origin-less sends; window: %v", soTypes(frames))
	})

	t.Run("room REST send unchanged", func(t *testing.T) {
		rh := newRoomsHarness(t)
		w := rh.do(http.MethodPost, "/api/v1/workspaces/sales/rooms",
			`{"kind":"adhoc","agent_ids":["mia","jim"]}`, rh.api.HandleWorkspaces)
		require.True(t, w.Code == http.StatusOK || w.Code == http.StatusCreated,
			"room create: %d %s", w.Code, w.Body.String())
		var room generated.Room
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &room), "room create body: %s", w.Body.String())
		require.NotEmpty(t, room.Id, "room create must return the room id")

		w = rh.do(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages",
			`{"client_message_id":"sv16-r1","text":"room send regression","mentions":[]}`, rh.api.HandleRooms)
		// The room send is async: today's success codes are 200/202.
		require.True(t, w.Code == http.StatusOK || w.Code == http.StatusAccepted,
			"room send: %d %s", w.Code, w.Body.String())
	})

	t.Run("workspace-only upload unchanged", func(t *testing.T) {
		rh := newRoomsHarness(t)
		ref := soUploadMediaRefAPI(t, rh.api.agentLoop, rh.api.homePath, "sv16-ws-upload")
		require.True(t, strings.HasPrefix(ref, "media://workspace/sv16-ws-upload/"),
			"workspace-only upload must keep its workspace-scoped ref, got %q", ref)
	})

	t.Run("skills REST surface unchanged", func(t *testing.T) {
		rh := newRoomsHarness(t)
		w := rh.do(http.MethodGet, "/api/v1/skills", "", rh.api.HandleSkills)
		require.Equal(t, http.StatusOK, w.Code, "skills list: %s", w.Body.String())
		var list []map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list), "skills list must stay a JSON array: %s", w.Body.String())
	})
}
