package gateway

import (
	"bufio"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1899 B / #1895 and docs/agents.md: a starting helper may already have text.
// Queued person input must not interrupt it or create an idle gap when it ends
// naturally. The parser/store companion verifies the first new-id row closes
// the earlier bubble even though that earlier turn never publishes done.
func TestOpenCodeFix1895_QueuedStartupTextKeepsWorkingAndSeparateAnswers(t *testing.T) {
	resumeAddress, awaitResume := opencodeFix1895Gate(t)
	var messages *bus.MessageBus
	al, child, viewers, original := opencodeFix1895Launch(t, opencodeFix1895Program(t), "queued_followup_text_pending", func(cfg *config.Config, actual *bus.MessageBus) {
		messages = actual
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == opencodeFix1895Worker {
				cfg.Agents.List[i].Subagents.Executor.CLIArgs += " --fixture-resume-address=" + resumeAddress
			}
		}
	})
	closed := false
	t.Cleanup(func() {
		if !closed {
			al.Close()
		}
	})
	var observed [2]opencode1895LiveObservation
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "starting helper text/readiness did not reach viewer", func() bool { return o.ready })
		require.Equal(t, opencode1895FirstAnswer, o.text, "instrument: original text is visible while the process still has no native id")
		require.NotEmpty(t, o.tokens)
		require.NotNil(t, o.tokens[0].MessageId)
		require.NotNil(t, o.tokens[0].TurnId)
	}
	events := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(events.ID) })
	ctx, stopIntake := opencode1895QueuedTextIntake(t, al)
	opencode1895PublishPerson(t, ctx, al, messages, child.SessionID, "person-queued-after-text-1895")
	opencode1895AwaitQueuedPerson(t, events.C, child.SessionID)
	stopIntake() // Join the real input's no-interrupt decision before releasing any native id.
	require.NoError(t, original.SetDeadline(time.Now().Add(5*time.Second)))
	_, err := fmt.Fprintln(original, "probe")
	require.NoError(t, err)
	reply, err := bufio.NewReader(original).ReadString('\n')
	require.NoError(t, err, "accepted queued input must leave the starting text-producing process alive")
	require.Equal(t, "still-starting-without-native-id\n", reply)
	require.NoError(t, original.SetDeadline(time.Time{}))
	_, err = fmt.Fprintln(original, "finish")
	require.NoError(t, err) // Natural successful completion now announces the native id.
	resumed := awaitResume()
	require.NoError(t, resumed.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = fmt.Fprintln(resumed, "probe")
	require.NoError(t, err)
	reply, err = bufio.NewReader(resumed).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "live-process-paused-before-output\n", reply)
	require.NoError(t, resumed.SetDeadline(time.Time{}))
	var beforeOutput [2][][]byte
	for i, viewer := range viewers {
		observed[i].drain(t, viewer, child.SessionID)
		require.Equal(t, opencode1895FirstAnswer, observed[i].text, "no resumed stdout may precede this snapshot")
		beforeOutput[i] = append([][]byte(nil), observed[i].raw...)
		for _, raw := range beforeOutput[i] {
			t.Logf("starting-text viewer=%d before follow-up stdout: %s", i, raw)
		}
	}
	_, err = fmt.Fprintln(resumed, "answer")
	require.NoError(t, err)
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "queued continuation text never reached viewer", func() bool { return o.text == opencode1895FirstAnswer+opencode1895LiveAnswer })
		last := o.tokens[len(o.tokens)-1]
		require.NotNil(t, last.MessageId)
		require.NotNil(t, last.TurnId)
		assert.NotEqual(t, *o.tokens[0].MessageId, *last.MessageId, "first follow-up token opens a distinct answer row")
		assert.NotEqual(t, *o.tokens[0].TurnId, *last.TurnId)
		opencode1895AssertPendingWorking(t, beforeOutput[i], *last.TurnId, i)
		assert.Empty(t, o.doneForTurn(*o.tokens[0].TurnId), "original text turn must not publish done while its queued continuation follows")
	}
	opencode1895ProbePaused(t, resumed)
	opencodeFix1895Finish(t, al, child, resumed)
	closed = true
	assert.Nil(t, al.GetActiveTurnBySession(child.SessionID), "final continuation completion leaves no active turn")
	entries, err := al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	require.NoError(t, err)
	for i, viewer := range viewers {
		observed[i].drain(t, viewer, child.SessionID)
		opencode1895AssertSeparateAnswers(t, &observed[i], entries)
		assert.Empty(t, observed[i].doneForTurn(*observed[i].tokens[0].TurnId), "the original turn never needs its own done to preserve/finalize its text")
		assert.Len(t, observed[i].done, 1, "only the resumed final done is published")
		for _, raw := range observed[i].raw {
			t.Logf("starting-text viewer=%d final frame: %s", i, raw)
		}
	}
	assert.Equal(t, observed[0].raw, observed[1].raw, "both viewers receive the same ordered original and resumed answers")
}

func opencode1895QueuedTextIntake(t *testing.T, al *agent.AgentLoop) (context.Context, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	joined := make(chan error, 1)
	go func() { joined <- al.Run(ctx) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		al.Stop()
		select {
		case err := <-joined:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("ordinary person intake did not join before releasing the starting process")
		}
	}
	t.Cleanup(stop)
	return ctx, stop
}
