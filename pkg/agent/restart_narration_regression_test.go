package agent

// Oracles: UAT fix dispatch A/B and parent-wake PW-D4/D5/D6/D13/D16/D19.
// The runtime, boot recovery, tools and durable stores are real. Only the
// external model is held; assertions inspect the input it actually receives.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func narrationNextWake(t *testing.T, al *AgentLoop, parent string) bus.InboundMessage {
	t.Helper()
	select {
	case wake := <-al.bus.InboundChan():
		require.Equal(t, parent, wake.AsyncTranscriptSessionID)
		return wake
	case <-time.After(5 * time.Second):
		t.Fatal("the real delivery published no parent wake")
		return bus.InboundMessage{}
	}
}

func narrationDriveWake(t *testing.T, al *AgentLoop, p *pwHeldProvider, wake bus.InboundMessage) []string {
	t.Helper()
	done := make(chan error, 1)
	go func() { _, err := al.processSystemMessage(context.Background(), wake); done <- err }()
	t.Cleanup(func() {
		p.open()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("parent wake consumer did not join")
		}
	})
	var inputs []string
	for _, msg := range p.await(t) {
		if msg.Role == "user" && !msg.Volatile {
			inputs = append(inputs, msg.Content)
		}
	}
	return inputs
}

func TestRestartNarration_ParentInputMatchesAdmission(t *testing.T) {
	for _, kind := range []string{"native", "refused", "external", "task"} {
		t.Run(kind, func(t *testing.T) {
			h := newPWBootRecoveryHarness(t)
			parent := h.rootSession(t)
			child := h.newSession(t, session.SessionTypeDelegate, parent)
			rec := h.steeredRecord(child, parent, session.LifecycleRunning)
			prefix := "Helper " + child + ": Interrupted by a restart. "
			want := prefix + "Automatic continuation has started (current state: running). This is not a request to resume the helper."
			switch kind {
			case "refused":
				rec.AgentID = "missing-agent-narration"
				want = prefix + "Automatic continuation did not start; the helper remains stopped."
			case "external":
				rec.Is3P = true
				want = prefix + "The external helper was not resumed."
			case "task":
				rec.Origin = &session.Origin{Kind: session.OriginKindTask, TaskID: "restart-narration-task"}
				want = prefix + "This task run was not resumed automatically; check the task's status."
			}
			h.persist(t, rec)
			h.seedCompletedToolHistory(t, child)
			require.NoError(t, h.recovery().Run(context.Background()))
			if kind == "native" {
				h.provider.await(t) // the helper was genuinely dispatched, not just stamped running
			}
			wake := narrationNextWake(t, h.al, parent)
			require.Equal(t, want, wake.Content, "restart wake must name the helper and distinguish admission from interruption")
			pending, err := h.sessions.PendingChatInputs(context.Background(), parent)
			require.NoError(t, err)
			require.Len(t, pending, 1)
			require.Equal(t, want, pending[0].Content, "the saved model input must match the actual wake")
			if kind == "native" {
				require.Equal(t, []string{want}, narrationDriveWake(t, h.al, h.provider, wake))
			}
			entries, err := h.sessions.ReadTranscript(parent)
			require.NoError(t, err)
			var restartRows []string
			for _, entry := range entries {
				if entry.HelperEvent != nil && entry.HelperEvent.Kind == generated.HelperEventKindRestarted {
					restartRows = append(restartRows, entry.Content)
				}
			}
			row := "Interrupted by a restart."
			if kind == "external" {
				row += " The external helper was not resumed."
			}
			require.Equal(t, []string{row}, restartRows, "the historical row remains immutable")
		})
	}
}

// GetSessionStore explicitly permits nil after shared-store initialization
// fails. Reading the first saved input must surface that failure, not panic.
func TestHelperReportNarration_MissingStoreReturnsError(t *testing.T) {
	al := &AgentLoop{}
	_, found, err := al.savedHelperInput("parent", "helper:1:final")
	require.EqualError(t, err, "helper input: session store is not configured")
	require.False(t, found)
}

// Supplemental boundary: retained reports must not multiply the shipped
// 32-KiB per-report budget into a multi-megabyte model input. Omitted report
// bodies remain in the inbox and the input explicitly says where to read them.
func TestHelperReportNarration_ContextIsBoundedWithoutDroppingReports(t *testing.T) {
	al, lifecycle, inbox, deliverer := newPWDeliverTestLoop(t)
	const parent, child = "budget-parent", "budget-helper"
	pwSeedDeliverySessions(t, al, parent, child)
	tool, childCtx := u4NewMessageParentTool(lifecycle, deliverer, child)
	for _, text := range []string{strings.Repeat("A", 17000), strings.Repeat("B", 17000)} {
		result := tool.Execute(childCtx, map[string]any{"kind": "progress", "text": text})
		require.False(t, result.IsError, "%s", result.ForLLM)
	}
	_, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{ChildSessionID: child,
		Outcome: steer.OutcomeFinalAnswer, Message: bootHandback(t, child, parent, child+":1:final")})
	require.NoError(t, err)
	wake := narrationNextWake(t, al, parent)
	require.Less(t, len(wake.Content), 32768, "two accepted 17-KiB updates must not become a 34-KiB receipt section")
	require.Contains(t, wake.Content, "More stored reports are available through delegate inbox; omitted here to keep this input bounded.")
	messages, _, _, err := inbox.Drain(parent, child, "", 0)
	require.NoError(t, err)
	require.Len(t, messages, 3, "both progress reports and the final remain durable")
	progress, err := messages[1].AsSessionMessageProgress()
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("B", 17000), progress.Text)
}

func TestHelperReportNarration_QuietReceiptsReachParent(t *testing.T) {
	al, lifecycle, inbox, deliverer := newPWDeliverTestLoop(t)
	const parent, child = "receipt-parent", "receipt-helper"
	pwSeedDeliverySessions(t, al, parent, child)
	tool, childCtx := u4NewMessageParentTool(lifecycle, deliverer, child)
	for i := 1; i <= 3; i++ { // UAT B4: three accepted progress reports, zero progress wakes
		id := fmt.Sprintf("progress-%d", i)
		result := tool.Execute(childCtx, map[string]any{"kind": "progress", "message_id": id, "text": fmt.Sprintf("B4-PROGRESS-%d", i)})
		require.NotNil(t, result)
		require.False(t, result.IsError, "%s", result.ForLLM)
		var receipt generated.MessageParentResponse
		require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &receipt))
		require.True(t, receipt.Accepted)
		require.Equal(t, &id, receipt.MessageId)
	}
	require.Empty(t, al.bus.InboundChan(), "quiet reports must not wake the parent")
	dt, parentCtx := u4DelegateToolFor(lifecycle, inbox, parent)
	result := dt.Execute(parentCtx, map[string]any{"action": "inbox", "session_id": child})
	require.NotNil(t, result)
	require.False(t, result.IsError, "%s", result.ForLLM)
	var polled generated.DelegateInboxResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &polled))
	require.Len(t, polled.Messages, 3, "accepted progress really exists in the parent's tool-readable inbox")

	_, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: child, Outcome: steer.OutcomeFinalAnswer,
		Message: bootHandback(t, child, parent, child+":1:final"),
	})
	require.NoError(t, err)
	wake := narrationNextWake(t, al, parent)
	want := "A delegated session handed back (final): finished\n\n" +
		"Stored reports from helper " + child + " (these reports did not wake the parent):\n" +
		"progress progress-1: B4-PROGRESS-1\nprogress progress-2: B4-PROGRESS-2\nprogress progress-3: B4-PROGRESS-3"
	require.Equal(t, want, wake.Content)
	inst, ok := al.GetRegistry().GetAgent("parent-agent")
	require.True(t, ok)
	p, ok := inst.Provider.(*pwHeldProvider)
	require.True(t, ok)
	require.Equal(t, []string{want}, narrationDriveWake(t, al, p, wake))
	messages, _, _, err := inbox.Drain(parent, child, "", 0)
	require.NoError(t, err)
	require.Len(t, messages, 3, "including progress as context must not falsely acknowledge those inbox reports")
}
