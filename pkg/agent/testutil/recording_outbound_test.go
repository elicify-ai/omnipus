package testutil

import (
	"context"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

type assertionPanic struct{ message string }

type recordingTestT struct {
	testing.TB
	fatal string
}

func (t *recordingTestT) Helper() {}

func (t *recordingTestT) Fatalf(format string, args ...any) {
	t.fatal = fmt.Sprintf(format, args...)
	panic(assertionPanic{message: t.fatal})
}

func requireAssertionFailure(t *testing.T, fn func()) string {
	t.Helper()
	var message string
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				failure, ok := recovered.(assertionPanic)
				if !ok {
					panic(recovered)
				}
				message = failure.message
			}
		}()
		fn()
	}()
	if message == "" {
		t.Fatal("assertion unexpectedly passed")
	}
	return message
}

func TestRecordingOutbound_CapturesAllSinks(t *testing.T) {
	recorder := RecordingOutbound(t)
	for _, boundary := range steer.Boundaries {
		recorder.Observe(boundary, "child-1", steer.AudienceNone)
		switch boundary {
		case steer.BoundaryMedia:
			recorder.PublishOutboundMedia("child-1", "root-chat", string(boundary))
		case steer.BoundaryWebchatStreaming:
			recorder.SendWebSocket("child-1", "root-chat", string(boundary))
		case steer.BoundaryExternalChannelStreaming:
			recorder.WriteExternalStream("child-1", "root-chat", string(boundary))
		case steer.BoundaryAgentRequestedMessage:
			recorder.SendMessage("child-1", "root-chat", string(boundary))
		case steer.BoundaryTaskResultNotification:
			recorder.NotifyTaskResult("child-1", "root-chat", string(boundary))
		case steer.BoundaryQuestionCard:
			recorder.BroadcastQuestionCard("child-1", "root-chat", string(boundary))
		default:
			recorder.PublishOutbound(boundary, "child-1", "root-chat", string(boundary))
		}
	}

	for _, boundary := range steer.Boundaries {
		recorder.AssertBoundaryInvoked(boundary)
		recorder.AssertReceived("child-1", string(boundary))
	}
	if got := len(recorder.Deliveries()); got != len(steer.Boundaries) {
		t.Fatalf("len(Deliveries) = %d, want %d", got, len(steer.Boundaries))
	}
}

func TestRecordingOutbound_ControlFailsWhenChildIdle(t *testing.T) {
	provider := NewScenario().WithText("verdict without a tool call")
	response, err := provider.Chat(context.Background(), nil, nil, "", nil)
	if err != nil {
		t.Fatalf("scripted provider verdict: %v", err)
	}
	if got := len(response.ToolCalls); got != 0 {
		t.Fatalf("scripted provider made %d tool calls, want none for the vacuity guard", got)
	}

	fakeT := &recordingTestT{TB: t}
	recorder := RecordingOutbound(fakeT)
	message := requireAssertionFailure(t, func() {
		recorder.AssertReceived("idle-child", "tool_result")
	})
	if message == "" {
		t.Fatal("AssertReceived failure did not explain the missing control")
	}
}

func TestRecordingOutbound_BoundaryInvokedFailsWhenSkipped(t *testing.T) {
	fakeT := &recordingTestT{TB: t}
	recorder := RecordingOutbound(fakeT)
	recorder.Observe(steer.BoundaryFinalReply, "child-1", steer.AudienceNone)

	message := requireAssertionFailure(t, func() {
		recorder.AssertBoundaryInvoked(steer.BoundaryMedia)
	})
	if message == "" {
		t.Fatal("AssertBoundaryInvoked failure did not explain the skipped boundary")
	}
}
