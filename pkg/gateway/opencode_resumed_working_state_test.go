package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1895 / docs/agents.md, "What you see in an external helper's chat": a
// helper still executing the person's follow-up must not look finished while
// waiting for its first resumed token. Either announce the new active turn,
// or retain the working state by not sending the interrupted turn's done.
func TestOpenCodeFix1895_InterruptedHelperStaysWorkingBeforeResumedOutput(t *testing.T) {
	program := opencodeFix1895Program(t)
	resumeAddress, awaitResume := opencodeFix1895Gate(t)
	var messages *bus.MessageBus
	al, child, viewers, original := opencodeFix1895Launch(t, program, "live_followup_pending", func(cfg *config.Config, actualBus *bus.MessageBus) {
		messages = actualBus
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
		o.readUntil(t, viewer, child.SessionID, "original CLI never announced readiness", func() bool { return o.ready })
	}
	opencode1895ProbePaused(t, original)
	const humanRowID = "person-pending-resume-row-1895"
	require.NoError(t, al.ResolveSessionStore(child.SessionID).AppendTranscriptWithProvenance(child.SessionID,
		session.TranscriptEntry{ID: humanRowID, Role: "user", Content: opencode1895LiveFollowup, Timestamp: time.Now().UTC()}, "person-1895"))
	require.NotNil(t, messages)
	require.NoError(t, messages.PublishInbound(ctx, bus.InboundMessage{
		Channel: "webchat", ChatID: "viewer-1895-0", SessionID: child.SessionID,
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "person-1895",
		Content: opencode1895LiveFollowup, TranscriptEntryID: humanRowID,
		Metadata: map[string]string{"agent_id": opencodeFix1895Worker}, UserInitiated: true, OperatorPrompt: true,
	}))
	resumed := awaitResume()
	require.NoError(t, original.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, readErr := bufio.NewReader(original).ReadString('\n')
	require.ErrorIs(t, readErr, io.EOF, "instrument: the ready original process was interrupted, never naturally released")
	require.NoError(t, resumed.SetDeadline(time.Now().Add(5*time.Second)))
	_, err := fmt.Fprintln(resumed, "probe")
	require.NoError(t, err)
	reply, err := bufio.NewReader(resumed).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "live-process-paused-before-output\n", reply, "instrument: resumed stdout is still completely silent")
	require.NoError(t, resumed.SetDeadline(time.Time{}))
	var beforeOutput [2][][]byte
	for i, viewer := range viewers {
		observed[i].drain(t, viewer, child.SessionID)
		require.Empty(t, observed[i].tokens, "instrument: no resumed token may precede the paused-state snapshot")
		beforeOutput[i] = append([][]byte(nil), observed[i].raw...)
		for _, raw := range beforeOutput[i] {
			t.Logf("viewer=%d before resumed stdout: %s", i, raw)
		}
	}

	// Freeze the pre-token observation first. Obtain the real resumed producing
	// id afterward, so no internal turn-counter convention becomes the oracle.
	_, err = fmt.Fprintln(resumed, "answer")
	require.NoError(t, err)
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "released resumed answer never reached viewer", func() bool { return o.text == opencode1895LiveAnswer })
	}
	opencodeFix1895Finish(t, al, child, resumed)
	closed = true
	entries, err := al.ResolveSessionStore(child.SessionID).ReadTranscript(child.SessionID)
	require.NoError(t, err)
	var answers []session.TranscriptEntry
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == opencode1895LiveAnswer {
			answers = append(answers, entry)
		}
	}
	require.Len(t, answers, 1, "instrument: resumed process produced exactly one real saved answer")
	require.NotEmpty(t, answers[0].TurnID)
	for i, viewer := range viewers {
		observed[i].drain(t, viewer, child.SessionID)
		opencode1895AssertSavedResumedAnswer(t, &observed[i], entries)
		opencode1895AssertPendingWorking(t, beforeOutput[i], answers[0].TurnID, i)
	}
}

func opencode1895PendingPersonIntake(t *testing.T, al *agent.AgentLoop, closed *bool) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	joined := make(chan error, 1)
	t.Cleanup(func() {
		cancel()
		al.Stop()
		select {
		case err := <-joined:
			assert.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Error("ordinary person-message intake did not join")
		}
		if !*closed {
			al.Close()
		}
	})
	go func() { joined <- al.Run(ctx) }()
	return ctx
}

func opencode1895AssertPendingWorking(t *testing.T, frames [][]byte, resumedTurnID string, viewer int) {
	t.Helper()
	// Both viewers already received the original tool's readiness event while
	// its process was paused. That is the initial working state, not a guessed
	// idle snapshot. Only real live done/session_state frames change it here.
	working, sawDone := true, false
	lastStatus := "original working turn retained (no done)"
	for _, raw := range frames {
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &fields))
		var kind string
		require.NoError(t, json.Unmarshal(fields["type"], &kind))
		switch kind {
		case "done":
			var done gen.DoneFrame
			require.NoError(t, json.Unmarshal(raw, &done))
			working, sawDone = false, true
			lastStatus = string(raw)
		case "session_state":
			var state gen.SessionStateFrame
			require.NoError(t, json.Unmarshal(raw, &state))
			working = state.ActiveTurn != nil && (!sawDone || state.ActiveTurn.TurnId == resumedTurnID)
			lastStatus = string(raw)
		}
	}
	assert.True(t, working,
		"#1895: viewer %d sees the helper idle before its first resumed output; resumed turn=%q; last session-level frame=%s; announce that active turn after done, or do not send the interrupted turn's done",
		viewer, resumedTurnID, lastStatus)
}
