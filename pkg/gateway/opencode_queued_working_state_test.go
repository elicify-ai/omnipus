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
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1899 B and #1895 / docs/agents.md: queued startup input does not interrupt,
// but the still-working follow-up must not look finished before its first output.
func TestOpenCodeFix1895_QueuedStartupFollowupStaysWorkingBeforeFirstOutput(t *testing.T) {
	resumeAddress, awaitResume := opencodeFix1895Gate(t)
	var messages *bus.MessageBus
	al, child, viewers, original := opencodeFix1895Launch(t, opencodeFix1895Program(t), "queued_followup_pending", func(cfg *config.Config, actual *bus.MessageBus) {
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
		o.readUntil(t, viewer, child.SessionID, "starting CLI readiness never reached viewer", func() bool { return o.ready })
	}
	events := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(events.ID) })
	ctx, cancel := context.WithCancel(t.Context())
	intakeJoined := make(chan error, 1)
	go func() { intakeJoined <- al.Run(ctx) }()
	stopIntake := func() {
		cancel()
		al.Stop()
		select {
		case runErr := <-intakeJoined:
			require.NoError(t, runErr)
		case <-time.After(10 * time.Second):
			t.Fatal("ordinary person-message intake did not join")
		}
	}
	intakeStopped := false
	t.Cleanup(func() {
		if !intakeStopped {
			stopIntake()
		}
	})
	opencode1895PublishPerson(t, ctx, al, messages, child.SessionID, "person-queued-startup-1895")
	opencode1895AwaitQueuedPerson(t, events.C, child.SessionID)
	// Intake processes this helper route synchronously. Joining that intake
	// after its enqueue acknowledgement proves the unready/no-interrupt check
	// returned BEFORE the fixture is allowed to announce an id. This does not
	// stop the separately admitted helper; its live process probe proves that.
	stopIntake()
	intakeStopped = true
	require.NoError(t, original.SetDeadline(time.Now().Add(5*time.Second)))
	_, err := fmt.Fprintln(original, "probe")
	require.NoError(t, err)
	reply, err := bufio.NewReader(original).ReadString('\n')
	require.NoError(t, err, "#1899 B: accepted queued input must leave the unready process alive")
	require.Equal(t, "still-starting-without-native-id\n", reply)
	require.NoError(t, original.SetDeadline(time.Time{}))
	_, err = fmt.Fprintln(original, "finish")
	require.NoError(t, err) // Natural completion, not cancel/interrupt, now announces the native id.
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
		require.Empty(t, observed[i].tokens, "no original answer or resumed output may precede the snapshot")
		beforeOutput[i] = append([][]byte(nil), observed[i].raw...)
		for _, raw := range beforeOutput[i] {
			t.Logf("queued viewer=%d before follow-up stdout: %s", i, raw)
		}
	}
	_, err = fmt.Fprintln(resumed, "answer")
	require.NoError(t, err)
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "released queued answer never reached viewer", func() bool { return o.text == opencode1895LiveAnswer })
	}
	opencodeFix1895Finish(t, al, child, resumed)
	closed = true
	entries, err := al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	require.NoError(t, err)
	var answer session.TranscriptEntry
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == opencode1895LiveAnswer {
			answer = entry
		}
	}
	require.NotEmpty(t, answer.TurnID)
	for i, viewer := range viewers {
		observed[i].drain(t, viewer, child.SessionID)
		opencode1895AssertSavedResumedAnswer(t, &observed[i], entries)
		opencode1895AssertPendingWorking(t, beforeOutput[i], answer.TurnID, i)
	}
	assert.Equal(t, observed[0].raw, observed[1].raw)
}

func opencode1895AwaitQueuedPerson(t *testing.T, events <-chan agent.Event, sessionID string) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Kind != agent.EventKindInterruptReceived || event.Meta.SessionKey != sessionID {
				continue
			}
			payload, ok := event.Payload.(agent.InterruptReceivedPayload)
			require.True(t, ok)
			require.Equal(t, agent.InterruptKindSteering, payload.Kind)
			require.Equal(t, "user", payload.Role)
			require.Equal(t, 1, payload.QueueDepth, "instrument: exactly one person's instruction is accepted while unready")
			return
		case <-timer.C:
			t.Fatal("person's queued startup instruction was never acknowledged by the real intake")
		}
	}
}
