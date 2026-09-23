package testutil

import (
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

func TestRecordingOutbound_AssertReceivedFailsWithoutDelivery(t *testing.T) {
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
