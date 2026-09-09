// replay_ask_user_test.go — askuserquestion-tool-spec v3 §0.6 cold-reload
// reconstruction: a session whose AskUserQuestion set resolved replays the
// COLLAPSED record (an ask_user_question frame rebuilt from the terminal
// PendingAskJSON record), and the §0.2 resume message never renders as a raw
// JSON user bubble (the card frame IS its render — presentation rule).
//
// Against the pre-fix behavior these tests fail: replay emitted the resume
// message as a raw replay_message and no ask_user_question frame at all.

package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// terminalAskFixture builds a terminal (answered) PendingSet like the one
// Registry.persistTerminal writes on submission.
func terminalAskFixture() *askuser.PendingSet {
	return &askuser.PendingSet{
		CardID:              "ask_replay_1",
		TranscriptSessionID: "session_test",
		AgentID:             "mia",
		Channel:             "webchat",
		ChatID:              "chat-1",
		CreatedAt:           time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
		Status:              askuser.StatusAnswered,
		Questions: []askuser.Question{{
			Header:   "Scope",
			Question: "Which scope?",
			Options:  []askuser.Option{{Label: "Backend"}, {Label: "Full stack"}},
		}},
		Answers: []askuser.Answer{{
			Header:       "Scope",
			QuestionText: "Which scope?",
			Selected:     []string{"Backend"},
		}},
	}
}

// runReplayWithAsk mirrors runReplay but passes a terminal AskUserQuestion
// record into streamReplay.
func runReplayWithAsk(t *testing.T, entries []session.TranscriptEntry, terminalAsk *askuser.PendingSet) []replayFrameDecoder {
	t.Helper()
	sink := &sliceSink{}
	rs := computeReplayStats(entries)
	_, err := streamReplay(context.Background(), "session_test", entries, rs, sink.emit, nil, nil, nil, terminalAsk)
	require.NoError(t, err)
	return sink.all()
}

// askReplayEntries builds the canonical resolved-card transcript: the user
// asks, the agent parks on AskUserQuestion (park-time "pending" tool stub,
// C-R2-1), the §0.2 resume message starts the resume turn, and the agent
// continues.
func askReplayEntries(t *testing.T, terminal *askuser.PendingSet) []session.TranscriptEntry {
	t.Helper()
	resumeText, err := askuser.ResumeMessage(terminal)
	require.NoError(t, err)
	return []session.TranscriptEntry{
		userEntry("please deploy the feature"),
		assistantEntry("Let me confirm the scope first.", "mia",
			toolCall("tc-ask-1", "AskUserQuestion", "success", 5,
				map[string]any{"questions": []any{}},
				map[string]any{"status": "pending", "card_id": terminal.CardID, "question_count": float64(1)})),
		userEntry(resumeText),
		assistantEntry("Proceeding with Backend.", "mia"),
	}
}

func TestReplay_TerminalAsk_CollapsedCardReplacesRawResumeBubble(t *testing.T) {
	terminal := terminalAskFixture()
	frames := runReplayWithAsk(t, askReplayEntries(t, terminal), terminal)

	askIdx, resultIdx, proceedIdx := -1, -1, -1
	askCount := 0
	for i, f := range frames {
		switch f.Type {
		case "ask_user_question":
			askCount++
			askIdx = i
			require.NotNil(t, f.Card, "ask_user_question frame must carry the card")
			assert.Equal(t, terminal.CardID, f.Card["card_id"])
			assert.Equal(t, "answered", f.Card["status"], "collapsed record renders the terminal status")
			answers, ok := f.Card["answers"].([]any)
			require.True(t, ok, "terminal card must carry the answers (§0.6: record is the render source)")
			require.Len(t, answers, 1)
		case "tool_call_result":
			if f.CallID == "tc-ask-1" {
				resultIdx = i
			}
		case "replay_message":
			assert.NotContains(t, f.Content, "Answers to your questions",
				"the §0.2 resume message must never replay as a raw JSON bubble")
			if f.Content == "Proceeding with Backend." {
				proceedIdx = i
			}
		}
	}
	assert.Equal(t, 1, askCount, "exactly one collapsed-card frame")
	require.GreaterOrEqual(t, resultIdx, 0, "park-time tool stub still replays")
	require.GreaterOrEqual(t, proceedIdx, 0)
	// The collapsed record lands where the resume happened: after the parked
	// tool pair, before the resume turn's assistant reply.
	assert.Greater(t, askIdx, resultIdx)
	assert.Less(t, askIdx, proceedIdx)
}

// A set cancelled via session Stop (CancelOnSessionStop) dispatches NO resume
// turn — there is no resume message in the transcript to anchor on. The
// terminal record must still reach the client: it is appended at the end of
// the stream, before the done frame.
func TestReplay_TerminalAsk_NoResumeMessage_CardAppendedAtEnd(t *testing.T) {
	terminal := terminalAskFixture()
	terminal.Status = askuser.StatusCancelled
	terminal.Answers = nil
	entries := []session.TranscriptEntry{
		userEntry("please deploy the feature"),
		assistantEntry("Let me confirm the scope first.", "mia",
			toolCall("tc-ask-1", "AskUserQuestion", "success", 5,
				map[string]any{"questions": []any{}},
				map[string]any{"status": "pending", "card_id": terminal.CardID, "question_count": float64(1)})),
	}
	frames := runReplayWithAsk(t, entries, terminal)

	askIdx := -1
	for i, f := range frames {
		if f.Type == "ask_user_question" {
			require.Equal(t, -1, askIdx, "exactly one collapsed-card frame")
			askIdx = i
			assert.Equal(t, "cancelled", f.Card["status"])
			assert.Nil(t, f.Card["answers"], "a cancelled record carries no answers")
		}
	}
	require.GreaterOrEqual(t, askIdx, 0, "terminal record with no resume anchor must still emit its card")
	require.Equal(t, "done", frames[len(frames)-1].Type)
	assert.Equal(t, len(frames)-2, askIdx, "appended at the end of the stream, before done")
}

// A resume message whose terminal record is gone (PendingAskJSON holds only
// the LATEST set — an older set's record is overwritten by the next
// CreatePending) is STILL suppressed: raw JSON must never render (§0.2), and
// the park-time tool stub remains as the historical trace.
func TestReplay_ResumeMessageWithoutRecord_SuppressedNeverRawJSON(t *testing.T) {
	terminal := terminalAskFixture()
	frames := runReplayWithAsk(t, askReplayEntries(t, terminal), nil)

	for _, f := range frames {
		assert.NotEqual(t, "ask_user_question", f.Type, "no record -> nothing to reconstruct")
		if f.Type == "replay_message" {
			assert.NotContains(t, f.Content, "Answers to your questions",
				"resume message must be suppressed even without a matching record")
		}
	}
}

// A still-PENDING record never replays: the live registry owns pending cards
// (session_state's pending_asks snapshot). streamReplay drops it defensively
// even if a caller passes one.
func TestReplay_PendingAskRecord_NotReplayed(t *testing.T) {
	pending := terminalAskFixture()
	pending.Status = askuser.StatusPending
	pending.Answers = nil
	entries := []session.TranscriptEntry{
		assistantEntry("Let me confirm the scope first.", "mia",
			toolCall("tc-ask-1", "AskUserQuestion", "success", 5,
				map[string]any{"questions": []any{}},
				map[string]any{"status": "pending", "card_id": pending.CardID, "question_count": float64(1)})),
	}
	for _, f := range runReplayWithAsk(t, entries, pending) {
		assert.NotEqual(t, "ask_user_question", f.Type)
	}
}

// loadTerminalAskRecord reads the record handleAttachSession hands to
// streamReplay: terminal records load, pending/corrupt/absent return nil.
func TestLoadTerminalAskRecord(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	sid := meta.ID

	setMetaAsk := func(v string) {
		require.NoError(t, store.SetMeta(sid, session.MetaPatch{PendingAskJSON: &v}))
	}

	assert.Nil(t, loadTerminalAskRecord(nil, sid), "nil store")
	assert.Nil(t, loadTerminalAskRecord(store, sid), "no record yet")

	setMetaAsk(`{not json`)
	assert.Nil(t, loadTerminalAskRecord(store, sid), "corrupt record is skipped, not fatal")

	setMetaAsk(`{"card_id":"ask_p","transcript_session_id":"` + sid + `","status":"pending"}`)
	assert.Nil(t, loadTerminalAskRecord(store, sid), "pending record belongs to the live registry")

	setMetaAsk(`{"card_id":"ask_t","transcript_session_id":"` + sid + `","status":"answered","answers":[{"header":"Scope","question":"Which scope?","selected":["Backend"],"auto_default":false}]}`)
	got := loadTerminalAskRecord(store, sid)
	require.NotNil(t, got)
	assert.Equal(t, "ask_t", got.CardID)
	assert.Equal(t, askuser.StatusAnswered, got.Status)
	require.Len(t, got.Answers, 1)
	assert.True(t, strings.HasPrefix(got.Answers[0].QuestionText, "Which scope"))
}
