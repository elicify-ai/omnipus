package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1895 / docs/agents.md, "its answer appears as the tool writes it": a
// PERSON interrupts a CLI which already has its conversation id. This is not
// #1899's starting-process queue. Nothing releases the resumed process until
// both real hub subscribers have received its answer, without a replay/reload.
func TestOpenCodeFix1895_HumanInterruptResumedAnswerStreamsBeforeDoneToBothViewers(t *testing.T) {
	program := opencodeFix1895Program(t)
	resumeAddress, awaitResume := opencodeFix1895Gate(t)
	var messages *bus.MessageBus
	al, child, viewers, original := opencodeFix1895Launch(t, program, "live_followup", func(cfg *config.Config, actualBus *bus.MessageBus) {
		messages = actualBus
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == opencodeFix1895Worker {
				cfg.Agents.List[i].Subagents.Executor.CLIArgs += " --fixture-resume-address=" + resumeAddress
			}
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	intakeDone := make(chan error, 1)
	closed := false
	// This cleanup must precede the launcher's h.Wait cleanup even on a fatal
	// assertion: cancel/join ordinary intake and the held real helper process.
	t.Cleanup(func() {
		cancel()
		al.Stop()
		select {
		case runErr := <-intakeDone:
			assert.NoError(t, runErr, "ordinary inbound intake must shut down cleanly")
		case <-time.After(10 * time.Second):
			t.Error("ordinary inbound intake did not join")
		}
		if !closed {
			al.Close()
		}
	})
	go func() { intakeDone <- al.Run(ctx) }()
	var observed [2]opencode1895LiveObservation
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "original CLI readiness event never reached viewer", func() bool { return o.ready })
	}
	// The real parser processed the native id before this later tool event.
	// The process is still live; no natural completion can cause the follow-up.
	opencode1895ProbePaused(t, original)
	const humanRowID = "person-live-followup-row-1895"
	require.NoError(t, al.ResolveSessionStore(child.SessionID).AppendTranscriptWithProvenance(child.SessionID,
		session.TranscriptEntry{ID: humanRowID, Role: "user", Content: opencode1895LiveFollowup, Timestamp: time.Now().UTC()}, "person-1895"))
	require.NotNil(t, messages, "instrument: use the launcher's actual bus, not a direct steering API")
	require.NoError(t, messages.PublishInbound(ctx, bus.InboundMessage{
		Channel: "webchat", ChatID: "viewer-1895-0", SessionID: child.SessionID,
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "person-1895",
		Content: opencode1895LiveFollowup, TranscriptEntryID: humanRowID,
		Metadata: map[string]string{"agent_id": opencodeFix1895Worker}, UserInitiated: true, OperatorPrompt: true,
	}))
	resumed := awaitResume()
	require.NoError(t, original.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, readErr := bufio.NewReader(original).ReadString('\n')
	require.ErrorIs(t, readErr, io.EOF, "the person's input must interrupt the original paused process, not wait for its natural exit")
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "resumed answer missing while the producing process is still paused", func() bool { return o.text == opencode1895LiveAnswer })
		o.drain(t, viewer, child.SessionID)
		require.NotEmpty(t, o.tokens, "live answer must be a token frame, never only saved history")
		require.NotNil(t, o.tokens[0].TurnId, "resumed output must identify its producing turn")
		require.NotEmpty(t, *o.tokens[0].TurnId)
		assert.Empty(t, o.doneForTurn(*o.tokens[0].TurnId), "a resumed done must not precede its live answer / process completion")
	}
	opencode1895ProbePaused(t, resumed) // The answer arrived before we permit process exit.
	opencodeFix1895Finish(t, al, child, resumed)
	closed = true
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleCompleted, rec.State)
	entries, err := al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	require.NoError(t, err)
	for i, viewer := range viewers {
		observed[i].drain(t, viewer, child.SessionID)
		opencode1895AssertSavedResumedAnswer(t, &observed[i], entries)
		assert.Len(t, observed[i].done, 1, "TEST-ANALYSIS #3: the entire interrupt/resume stream has only the resumed final done, never an old held completion")
	}
	assert.Equal(t, observed[0].raw, observed[1].raw, "both subscribers receive identical gap-free session frames")
	t.Log("#1895: both subscribers received resumed tokens while the CLI was paused; saved row/turn ids, token-before-done and session sequences checked")
}

func opencode1895ProbePaused(t *testing.T, process net.Conn) {
	t.Helper()
	require.NoError(t, process.SetDeadline(time.Now().Add(5*time.Second)), "deadlock failsafe for the process barrier")
	_, err := fmt.Fprintln(process, "probe")
	require.NoError(t, err)
	reply, err := bufio.NewReader(process).ReadString('\n')
	require.NoError(t, err, "the process must still be running behind its exit barrier")
	require.Equal(t, "live-process-still-paused\n", reply)
	require.NoError(t, process.SetDeadline(time.Time{}))
}

func opencode1895AssertSavedResumedAnswer(t *testing.T, observed *opencode1895LiveObservation, entries []session.TranscriptEntry) {
	t.Helper()
	var saved []session.TranscriptEntry
	humanRows := 0
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == opencode1895LiveAnswer {
			saved = append(saved, entry)
		}
		if entry.Role == "user" && entry.Content == opencode1895LiveFollowup {
			humanRows++
		}
	}
	require.Len(t, saved, 1, "the resumed answer is saved exactly once, not duplicated by finalization")
	assert.Equal(t, 1, humanRows, "ordinary inbound intake must not duplicate the already-saved person's message")
	require.NotEmpty(t, saved[0].ID)
	require.NotEmpty(t, saved[0].TurnID)
	assert.Equal(t, opencode1895LiveAnswer, observed.text, "the complete resumed answer reached the open chat without reload")
	require.NotEmpty(t, observed.tokens)
	for _, token := range observed.tokens {
		assert.Equal(t, &saved[0].ID, token.MessageId, "every resumed token reuses its real saved assistant row id")
		assert.Equal(t, &saved[0].TurnID, token.TurnId, "every resumed token identifies the actual producing turn")
	}
	done := observed.doneForTurn(saved[0].TurnID)
	if assert.Len(t, done, 1, "the resumed producing turn sends exactly one ordinary done") {
		assert.Equal(t, &saved[0].ID, done[0].MessageId)
		for _, token := range observed.tokens {
			assert.Less(t, *token.Seq, *done[0].Seq, "resumed tokens must precede the resumed done signal")
		}
		if done[0].Stats != nil && done[0].Stats.TurnFailed != nil {
			assert.False(t, *done[0].Stats.TurnFailed, "a successfully resumed helper is not failed")
		}
	}
	assert.Empty(t, observed.errors, "normal human interruption and successful resume must not become a chat failure")
}
