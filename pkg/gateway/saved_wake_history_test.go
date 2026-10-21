package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: the UAT fix dispatch. Both REST history paths and WS attach replay
// keep the event row and reply, never an extra raw wake-input user bubble.
func TestSavedWakeInput_HistoryAndReplayKeepOnlyVisibleRows(t *testing.T) {
	for _, kind := range []string{"finished", "verdict", "restart"} {
		t.Run(kind, func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			sid := createTestSession(t, api)
			store := api.agentLoop.GetSessionStore()
			now := time.Now().UTC()
			body := "A delegated session handed back (final): B1-HELPER-4412"
			switch kind {
			case "verdict":
				body = "Goal verdict: met. Conditions: all met. Reason: the file exists."
			case "restart":
				body = "Interrupted by a restart."
			}
			appendEntry := func(entry session.TranscriptEntry) {
				t.Helper()
				entry.AgentID, entry.Timestamp = "mia", now
				_, err := store.AppendTranscriptAddressed(sid, entry)
				require.NoError(t, err)
			}
			// Identical person-authored text stays visible: classification is
			// never inferred from the words in the wake input.
			appendEntry(session.TranscriptEntry{ID: "person", Role: "user", Content: body})
			row := session.TranscriptEntry{ID: "wake-row", Type: session.EntryTypeSystem, Role: "system", Content: body}
			if kind == "verdict" {
				row.SystemSubtype = session.SystemSubtypeGoalOutcome
				row.GoalOutcome = &generated.GoalOutcome{
					GoalId: "goal-1", GoalText: "Create the file", Ending: "met", RoundsUsed: 1, MaxRounds: 2, EndedAt: now,
				}
			} else {
				row.SystemSubtype = "helper_event"
				eventKind := generated.HelperEventKindFinished
				if kind == "restart" {
					eventKind = generated.HelperEventKindRestarted
				}
				row.HelperEvent = &generated.HelperEvent{
					HelperSessionId: "child", Kind: eventKind, Text: body, CreatedAt: now,
				}
			}
			appendEntry(row)
			_, err := store.SaveChatInput(sid, "saved-input-new-outcome", "mia", body)
			require.NoError(t, err)
			// Old history has no private type. Its server-minted input id must
			// still be recognized, without rewriting the stored transcript.
			appendEntry(session.TranscriptEntry{ID: "saved-input-old-outcome", Role: "user", Content: body})
			appendEntry(session.TranscriptEntry{ID: "reply", Role: "assistant", Content: "The helper's result reached me."})
			wantIDs := []string{"person", "wake-row", "reply"}

			w := httptest.NewRecorder()
			api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages", nil))
			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
			var messages []generated.Message
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &messages))
			ids := make([]string, 0, len(messages))
			for _, message := range messages {
				ids = append(ids, message.Id)
			}
			assert.Equal(t, wantIDs, ids, "REST messages must not expose saved wake inputs")
			var rawMessages []any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rawMessages))
			schema := loadMessageSchema(t)
			for _, message := range rawMessages {
				require.NoError(t, schema.Validate(message), "private archive kinds must never cross the public Message contract")
			}

			w = httptest.NewRecorder()
			api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid, nil))
			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
			var detail generated.SessionDetail
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
			ids = ids[:0]
			for _, message := range detail.Messages {
				ids = append(ids, message.Id)
			}
			assert.Equal(t, wantIDs, ids, "REST session detail must use the same visible history")

			// Use attach's production snapshot reader, not a manually filtered
			// transcript: this is the reload path that produced the UAT bubble.
			view, err := attachReadTranscript(store, sid)
			require.NoError(t, err)
			var frames []any
			n, err := streamReplay(context.Background(), sid, view.Entries, computeReplayStats(view.Entries), func(frame any) error {
				frames = append(frames, frame)
				return nil
			}, nil, nil, nil, nil)
			require.NoError(t, err)
			require.Equal(t, 3, n, "only the person's message, event row and reply replay")
			require.Len(t, frames, 4, "three content frames plus exactly one done")
			person, ok := frames[0].(generated.ReplayMessageFrame)
			require.True(t, ok)
			require.Equal(t, "user", person.Role)
			require.Equal(t, body, person.Content)
			require.NotNil(t, person.Id)
			require.Equal(t, "person", *person.Id)
			if kind == "verdict" {
				verdict, verdictFrameOK := frames[1].(generated.GoalOutcomeFrame)
				require.True(t, verdictFrameOK, "the proper goal outcome row must remain")
				require.Equal(t, "wake-row", verdict.MessageId)
				require.Equal(t, "goal-1", verdict.Outcome.GoalId)
			} else {
				event, helperFrameOK := frames[1].(generated.HelperEventFrame)
				require.True(t, helperFrameOK, "the proper helper row must remain")
				require.Equal(t, "wake-row", event.MessageId)
				require.Equal(t, body, event.HelperEvent.Text)
			}
			reply, ok := frames[2].(generated.ReplayMessageFrame)
			require.True(t, ok)
			require.Equal(t, "assistant", reply.Role)
			require.Equal(t, "The helper's result reached me.", reply.Content)
			require.NotNil(t, reply.Id)
			require.Equal(t, "reply", *reply.Id)
			done, ok := frames[3].(generated.DoneFrame)
			require.True(t, ok)
			require.Equal(t, "done", done.Type)
		})
	}
}
