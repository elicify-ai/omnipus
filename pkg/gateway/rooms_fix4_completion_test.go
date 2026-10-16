package gateway

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// R3-N1 / NEW-SF-1B: preserving a room answer is not enough; its original
// live repair route must survive the later failure/cancellation of its turn.
func TestRoomsFix4_PostedAnswerRepairSurvivesOwningTurnFailure(t *testing.T) {
	for _, marker := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			t.Run(fmt.Sprintf("marker_%t/cancel_%t", marker, cancelled), func(t *testing.T) {
				h := wu15NewHarness(t)
				h.stopMain("mia")
				h.stopMain("jim") // eventual mention capture is real, but starts no paid turn
				j := &fix4RoomJournal{Journal: rooms.NewFileJournal(rooms.DefaultDir(h.home)), stream: rooms.StreamMirrors}
				hub := fix4InstallJournal(t, h, j)
				writer := &fix4MirrorWriter{UnifiedStore: h.al.GetSessionStore()}
				h.al.SetRoomReceiverWriter(writer)
				room := h.create("squad", "Launch", "mia", "jim")
				h.post(room.Id, "held", "@Mia task")
				d := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "held"), "mia")
				h.waitState(room.Id, d.RequestId, "held")
				text := "completed answer @Jim"
				p := &fix4AnswerThenFailProvider{requestID: d.RequestId, content: text, cancel: cancelled, afterAnswer: make(chan struct{})}
				inst, ok := h.al.GetRegistry().GetAgent("mia")
				require.True(t, ok)
				inst.Provider = p
				inst.StoreProviderPool(map[string]providers.LLMProvider{})
				if marker {
					j.fail.Store(true)
				} else {
					writer.fail.Store(true)
				}
				conn := h.connect()
				require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
				wu15ReadFrame(t, conn, "catch_up_complete")
				h.sendMain(conn, "mia", "REVIVE_AND_ANSWER")
				select {
				case <-p.afterAnswer:
				case <-time.After(30 * time.Second):
					t.Fatal("instrument: provider never reached its post-answer failure cut")
				}
				if cancelled {
					result, err := h.al.StopSession(context.Background(), agent.StopRequest{SessionID: h.main("mia"), By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "wu15-tester"}, Channel: "webchat"})
					require.NoError(t, err)
					require.True(t, result.Durable)
				}
				fix4WaitTurnSettled(t, h, "mia")
				if marker {
					require.Positive(t, j.hits.Load())
				} else {
					require.Positive(t, writer.hits.Load())
				}
				capture, found, err := h.al.RequestLedger().Get(h.main("mia"), d.RequestId)
				require.NoError(t, err)
				require.True(t, found)
				require.False(t, capture.Discarded, "a posted answer still needing its mirror must retain live repair capability")
				j.fail.Store(false)
				writer.fail.Store(false)
				_, err = h.al.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{ReplyTo: d.RequestId, Content: text, ResponderSessionID: h.main("mia"), Author: tools.SendOrigin{WorkspaceID: wu15Workspace, AgentID: "mia"}})
				require.NoError(t, err, "same-request/same-text repair must work without restarting the room service")
				hub.svc.WaitIdleForTest()
				snap := h.snapshot(room.Id)
				require.Len(t, snap.Entries, 2, "one original entry and exactly one room answer")
				var answers []gen.RoomEntry
				for _, entry := range snap.Entries {
					if entry.Author.Kind == "agent" {
						answers = append(answers, entry)
					}
				}
				require.Len(t, answers, 1)
				require.Equal(t, text, answers[0].Text)
				jim := wu15Delivery(t, answers[0], "jim")
				h.waitState(room.Id, jim.RequestId, "held")
				for _, tc := range []struct{ id, request string }{{"mia", d.RequestId}, {"jim", jim.RequestId}} {
					entries, readErr := h.al.GetSessionStore().ReadTranscript(h.main(tc.id))
					require.NoError(t, readErr)
					copies := 0
					for _, entry := range entries {
						if entry.RoomRef != nil && entry.RoomRef.RequestId == tc.request && ((tc.id == "mia" && entry.RoomRef.Role == "answer") || (tc.id == "jim" && entry.RoomRef.Role == "request")) {
							copies++
						}
					}
					require.Equal(t, 1, copies, "one main mirror or one forwarded capture")
				}
				require.Equal(t, int64(2), p.calls.Load(), "mirror repair must not re-run the failed model task")
			})
		}
	}
}

// NEW-SF-1A: completion-write/read faults and the independently writable
// receiver ledger must not turn a failed completion into destructive success.
func TestRoomsFix4_FailedCompletionNeverDiscardsRepair(t *testing.T) {
	for _, loadFault := range []bool{false, true} {
		t.Run(fmt.Sprintf("load_%t", loadFault), func(t *testing.T) {
			h := wu15NewHarness(t)
			h.stopMain("mia")
			j := &fix4RoomJournal{Journal: rooms.NewFileJournal(rooms.DefaultDir(h.home)), state: "failed"}
			hub := fix4InstallJournal(t, h, j)
			room := h.create("squad", "Launch", "mia", "jim")
			h.post(room.Id, "held", "@Mia FAIL_WITH_STORAGE")
			d := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "held"), "mia")
			h.waitState(room.Id, d.RequestId, "held")
			p := &wu15ScriptProvider{entered: make(chan struct{}), release: make(chan struct{}), fail: fmt.Errorf("provider failed")}
			h.provider("mia", p)
			conn := h.connect()
			require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
			wu15ReadFrame(t, conn, "catch_up_complete")
			h.sendMain(conn, "mia", "REVIVE")
			select {
			case <-p.entered:
			case <-time.After(30 * time.Second):
				t.Fatal("instrument: held input never reached provider")
			}
			if loadFault {
				j.readFault.Store(true)
				hub = fix4InstallJournal(t, h, j) // cold real service: completion now must read the persisted room
			} else {
				j.fail.Store(true)
			}
			close(p.release)
			fix4WaitTurnSettled(t, h, "mia")
			require.Positive(t, j.hits.Load(), "instrument: actual completion storage fault fired")
			capture, found, err := h.al.RequestLedger().Get(h.main("mia"), d.RequestId)
			require.NoError(t, err)
			require.True(t, found)
			require.False(t, capture.Discarded, "failed completion is not permission to discard its recovery state")
			status, body := h.http(http.MethodGet, "/api/v1/rooms/"+room.Id, nil)
			require.Equal(t, http.StatusInternalServerError, status, "unrecorded outcome is not healthy working: %s", body)
			j.fail.Store(false)
			j.readFault.Store(false)
			got := h.room(room.Id)
			require.Equal(t, room.Id, got.Id)
			snap := h.snapshot(room.Id)
			state := wu15Delivery(t, wu15FindEntry(t, snap, "held"), "mia")
			require.Equal(t, "failed", state.State)
			require.NotNil(t, state.Reason)
			require.Equal(t, "turn_failed", *state.Reason)
			h.post(room.Id, "held", "@Mia FAIL_WITH_STORAGE")
			hub.svc.WaitIdleForTest()
			require.Len(t, p.inputs(), 1, "terminal bookkeeping repair must never replay model work")
		})
	}
}
