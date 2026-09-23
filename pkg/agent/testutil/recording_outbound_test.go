package testutil

import (
	"fmt"
	"strings"
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

// TestRecordingOutbound_BoundaryScopeCatchesTheWrongAudience is the unit
// proof that BoundaryScope has teeth. The same recorded history — the
// boundary WAS invoked for the child, but its audience decision came out
// "user" instead of "steering_session", i.e. containment broken — passes the
// unscoped assertion and must fail the scoped one. Without this pairing the
// scoped form could itself be vacuous.
func TestRecordingOutbound_BoundaryScopeCatchesTheWrongAudience(t *testing.T) {
	fakeT := &recordingTestT{TB: t}
	recorder := RecordingOutbound(fakeT)
	recorder.Observe(steer.BoundaryFinalReply, "steered-child", steer.AudienceUser)

	// The unscoped form is satisfied by a completely broken gate.
	recorder.AssertBoundaryInvoked(steer.BoundaryFinalReply)

	message := requireAssertionFailure(t, func() {
		recorder.AssertBoundaryInvoked(steer.BoundaryFinalReply,
			ForSession("steered-child", steer.AudienceSteeringSession))
	})
	for _, want := range []string{"steered-child", "steering_session", "user"} {
		if !strings.Contains(message, want) {
			t.Fatalf("scoped failure message %q does not name %q", message, want)
		}
	}
}

// TestRecordingOutbound_BoundaryScopeCatchesTheWrongSession proves the other
// half: a boundary invoked for a DIFFERENT session than the one under test
// satisfies the unscoped assertion and must fail the scoped one, so an
// assertion can never be carried by some other session's boundary traffic.
func TestRecordingOutbound_BoundaryScopeCatchesTheWrongSession(t *testing.T) {
	fakeT := &recordingTestT{TB: t}
	recorder := RecordingOutbound(fakeT)
	recorder.Observe(steer.BoundaryMedia, "some-other-session", steer.AudienceSteeringSession)

	recorder.AssertBoundaryInvoked(steer.BoundaryMedia)

	message := requireAssertionFailure(t, func() {
		recorder.AssertBoundaryInvoked(steer.BoundaryMedia,
			ForSession("the-child-under-test", steer.AudienceSteeringSession))
	})
	if !strings.Contains(message, "the-child-under-test") {
		t.Fatalf("scoped failure message %q does not name the session it was scoped to", message)
	}
}

// TestRecordingOutbound_BoundaryScopePassesOnTheRealShape is the positive
// control: the correct history (invoked for this child, resolving
// steering_session) must pass, or the two failures above would prove only
// that the assertion always fails.
func TestRecordingOutbound_BoundaryScopePassesOnTheRealShape(t *testing.T) {
	fakeT := &recordingTestT{TB: t}
	recorder := RecordingOutbound(fakeT)
	recorder.Observe(steer.BoundaryFinalReply, "steered-child", steer.AudienceSteeringSession)
	recorder.Observe(steer.BoundaryFinalReply, "ordinary-root", steer.AudienceUser)

	recorder.AssertBoundaryInvoked(steer.BoundaryFinalReply,
		ForSession("steered-child", steer.AudienceSteeringSession),
		ForSession("ordinary-root", steer.AudienceUser))
	if fakeT.fatal != "" {
		t.Fatalf("the correct boundary history failed the scoped assertion: %s", fakeT.fatal)
	}
}
