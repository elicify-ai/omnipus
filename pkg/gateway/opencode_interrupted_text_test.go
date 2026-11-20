package gateway

import (
	"bufio"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1895 item 3: interrupting an answer that has already streamed must preserve
// that answer, not replace it with the continuation or reuse its message/turn.
// The real parser/store companion checks row finalization without original done.
func TestOpenCodeFix1895_InterruptedTextAndResumedAnswerKeepSeparateSavedRows(t *testing.T) {
	resumeAddress, awaitResume := opencodeFix1895Gate(t)
	var messages *bus.MessageBus
	al, child, viewers, original := opencodeFix1895Launch(t, opencodeFix1895Program(t), "live_followup_text", func(cfg *config.Config, actual *bus.MessageBus) {
		messages = actual
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == opencodeFix1895Worker {
				cfg.Agents.List[i].Subagents.Executor.CLIArgs += " --fixture-resume-address=" + resumeAddress
			}
		}
	})
	closed := false
	ctx := opencode1895PendingPersonIntake(t, al, &closed)
	var observed [2]opencode1895LiveObservation
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "original text/readiness never reached viewer", func() bool { return o.ready })
		require.Equal(t, opencode1895FirstAnswer, o.text, "instrument: the original answer is visible BEFORE the person interrupts")
		require.NotEmpty(t, o.tokens)
		require.NotNil(t, o.tokens[0].MessageId)
		require.NotNil(t, o.tokens[0].TurnId)
	}
	opencode1895ProbePaused(t, original)
	opencode1895PublishPerson(t, ctx, al, messages, child.SessionID, "person-after-original-text-1895")
	resumed := awaitResume()
	require.NoError(t, original.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err := bufio.NewReader(original).ReadString('\n')
	require.ErrorIs(t, err, io.EOF, "the original text-producing process was interrupted, never naturally released")
	require.NoError(t, resumed.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = fmt.Fprintln(resumed, "probe")
	require.NoError(t, err)
	reply, err := bufio.NewReader(resumed).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "live-process-paused-before-output\n", reply)
	require.NoError(t, resumed.SetDeadline(time.Time{}))
	for i, viewer := range viewers {
		observed[i].drain(t, viewer, child.SessionID)
		assert.Equal(t, opencode1895FirstAnswer, observed[i].text, "earlier text survives the interruption before any resumed output")
	}
	_, err = fmt.Fprintln(resumed, "answer")
	require.NoError(t, err)
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "resumed answer did not arrive live", func() bool { return o.text == opencode1895FirstAnswer+opencode1895LiveAnswer })
	}
	opencode1895ProbePaused(t, resumed)
	opencodeFix1895Finish(t, al, child, resumed)
	closed = true
	assert.Nil(t, al.GetActiveTurnBySession(child.SessionID), "final completion leaves no active producing turn")
	entries, err := al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	require.NoError(t, err)
	for i, viewer := range viewers {
		observed[i].drain(t, viewer, child.SessionID)
		opencode1895AssertSeparateAnswers(t, &observed[i], entries)
		for _, raw := range observed[i].raw {
			t.Logf("text-before-interrupt viewer=%d: %s", i, raw)
		}
	}
	assert.Equal(t, observed[0].raw, observed[1].raw, "both viewers see identical, gap-free original/continuation frames")
}

func opencode1895AssertSeparateAnswers(t *testing.T, observed *opencode1895LiveObservation, entries []session.TranscriptEntry) {
	t.Helper()
	var answers []session.TranscriptEntry
	var personContents []string
	for _, entry := range entries {
		if entry.Role == "assistant" {
			answers = append(answers, entry)
		}
		if entry.Role == "user" && entry.Content == opencode1895LiveFollowup {
			personContents = append(personContents, entry.Content)
		}
	}
	require.Len(t, answers, 2, "original and resumed text must be two saved assistant rows, never overwritten or duplicated")
	assert.Equal(t, []string{opencode1895FirstAnswer, opencode1895LiveAnswer}, []string{answers[0].Content, answers[1].Content})
	assert.Equal(t, []string{opencode1895LiveFollowup}, personContents, "the person's instruction is saved exactly once")
	for _, answer := range answers {
		require.NotEmpty(t, answer.ID)
		require.NotEmpty(t, answer.TurnID)
	}
	assert.NotEqual(t, answers[0].ID, answers[1].ID, "continuation gets its own assistant bubble")
	assert.NotEqual(t, answers[0].TurnID, answers[1].TurnID, "continuation gets its own producing turn")
	contents := make(map[string]string)
	var rowOrder []string
	for _, token := range observed.tokens {
		require.NotNil(t, token.MessageId)
		require.NotNil(t, token.TurnId)
		if len(rowOrder) == 0 || rowOrder[len(rowOrder)-1] != *token.MessageId {
			rowOrder = append(rowOrder, *token.MessageId)
		}
		contents[*token.MessageId] += token.Content
		if *token.MessageId == answers[0].ID {
			assert.Equal(t, answers[0].TurnID, *token.TurnId)
		} else {
			assert.Equal(t, answers[1].ID, *token.MessageId, "no third or replacement assistant identity may appear")
			assert.Equal(t, answers[1].TurnID, *token.TurnId)
		}
	}
	assert.Equal(t, []string{answers[0].ID, answers[1].ID}, rowOrder, "all original tokens precede the separate resumed answer")
	assert.Equal(t, map[string]string{answers[0].ID: opencode1895FirstAnswer, answers[1].ID: opencode1895LiveAnswer}, contents)
	done := observed.doneForTurn(answers[1].TurnID)
	if assert.Len(t, done, 1, "the resumed answer completes exactly once") {
		assert.Equal(t, &answers[1].ID, done[0].MessageId)
		for _, token := range observed.tokens {
			assert.Less(t, *token.Seq, *done[0].Seq, "final done follows both answers")
		}
	}
	assert.Empty(t, observed.errors, "ordinary interruption must not become a chat error")
	t.Logf("original text turn has %d done frame(s); real parser/store companion must finalize it even with none", len(observed.doneForTurn(answers[0].TurnID)))
}
