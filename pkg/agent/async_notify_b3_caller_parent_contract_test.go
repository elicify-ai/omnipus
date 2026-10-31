package agent

// F01 is a coverage addition, not a production repair. Correct production may
// already pass. The different CHECK instance must apply the exact historical
// M12 child-parent change and restore it; this author never mutates production.
import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

const b3CallerParentFiller = "b3-caller-parent-filler"
const b3CallerParentTarget = "b3-caller-parent-target"

// All channels/contexts/producer goroutines are created inside the SAME native
// synctest bubble. Wait establishes quiescence WITHOUT advancing its clock.
// The unchanged real bus and helper remain the unit, not an interface double.
type b3CallerParentFixture struct {
	bus      *bus.MessageBus
	notifier *asyncNotifierImpl
	message  bus.InboundMessage
	capacity int
}
type b3CallerParentRun struct {
	returned chan struct{}
	attempts int
	err      error
}

func b3NewCallerParentFixture(t *testing.T) *b3CallerParentFixture {
	t.Helper()
	mb := bus.NewMessageBus()
	capacity := cap(mb.InboundChan())
	if capacity <= 0 {
		t.Fatal("INVALID fixture: real concrete bus has no buffered capacity")
	}
	for i := 0; i < capacity; i++ {
		if err := mb.PublishInbound(context.Background(), bus.InboundMessage{
			Channel: "system", ChatID: b3CallerParentFiller,
		}); err != nil {
			t.Fatalf("INVALID fixture: real bus fill failed: %v", err)
		}
	}
	if len(mb.InboundChan()) != capacity {
		t.Fatal("INVALID fixture: real bus was not filled")
	}
	return &b3CallerParentFixture{
		bus:      mb,
		notifier: newAsyncNotifier(&AgentLoop{bus: mb}),
		message:  bus.InboundMessage{Channel: "system", ChatID: b3CallerParentTarget, Content: "synthetic caller-parent unit payload"},
		capacity: capacity,
	}
}

func (f *b3CallerParentFixture) start(ctx context.Context) *b3CallerParentRun {
	run := &b3CallerParentRun{returned: make(chan struct{})}
	go func() {
		defer close(run.returned)
		run.attempts, run.err = f.notifier.publishAsyncNotify(ctx,
			AsyncNotifyEvent{Channel: "webchat", ChatID: b3CallerParentTarget, SourceKind: "b3_caller_parent"},
			"b3-caller-parent-notice", f.message)
	}()
	return run
}

func (f *b3CallerParentFixture) requireBlocked(t *testing.T, ctx context.Context, run *b3CallerParentRun) {
	t.Helper()
	synctest.Wait()
	if ctx.Err() != nil {
		t.Fatal("INVALID fixture: caller ended before the actual blocked-attempt premise")
	}
	if len(f.bus.InboundChan()) != f.capacity {
		t.Fatal("INVALID fixture: bus capacity changed before caller termination")
	}
	select {
	case <-run.returned:
		t.Fatal("INVALID fixture: actual publish helper returned before cancellation or capacity release")
	default:
	}
	// The live caller/open full bus exclude error and acceptance; the first
	// attempt has no backoff. The result channel cannot itself block. After
	// Wait the sole producer is therefore in the concrete bus's blocked select.
}

func (f *b3CallerParentFixture) requireStopped(t *testing.T, run *b3CallerParentRun, callerEnded bool) bool {
	t.Helper()
	synctest.Wait()
	select {
	case <-run.returned:
		return true
	default:
		// This is THE behavioral oracle: a discrete producer completion after
		// native caller termination and quiescence, not elapsed wall time or a
		// watchdog. A detached M12 child is still durably blocked here.
		if callerEnded {
			t.Error("F01: actual publish attempt outlived its caller; producer remains blocked after caller termination and native quiescence")
		} else {
			t.Error("INVALID positive instrument: live-caller actual publish did not finish after one real capacity slot was released")
		}
		// Fail FIRST, then release only the bus-close limb to join a broken
		// producer. No capacity is offered during cancellation, so this does
		// not assert on, or reinterpret, accepted-send/cancel races.
		f.bus.Close()
		synctest.Wait()
		<-run.returned
		return false
	}
}

func (f *b3CallerParentFixture) requireOnlyFillers(t *testing.T) {
	t.Helper()
	if len(f.bus.InboundChan()) != f.capacity {
		t.Fatal("caller-terminated attempt changed the full bus")
	}
	for i := 0; i < f.capacity; i++ {
		msg := <-f.bus.InboundChan()
		if msg.ChatID != b3CallerParentFiller {
			t.Fatal("caller-terminated attempt enqueued an unexpected message")
		}
	}
	if len(f.bus.InboundChan()) != 0 {
		t.Fatal("unexpected extra inbound after exact filler count")
	}
}

func TestAsyncNotifierB3CallerParentLifetimeContract(t *testing.T) {
	t.Run("LiveCallerPublishesAfterCapacity", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := b3NewCallerParentFixture(t)
			defer f.bus.Close()
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			run := f.start(caller)
			f.requireBlocked(t, caller, run)
			filler := <-f.bus.InboundChan()
			if filler.ChatID != b3CallerParentFiller {
				t.Fatal("INVALID fixture: released slot was not an original filler")
			}
			if !f.requireStopped(t, run, false) {
				return
			}
			if caller.Err() != nil {
				t.Fatal("INVALID fixture: positive caller unexpectedly ended")
			}
			if run.attempts != 1 || run.err != nil {
				t.Fatalf("live capacity handoff: attempts=%d err=%v; want one accepted attempt", run.attempts, run.err)
			}
			if len(f.bus.InboundChan()) != f.capacity {
				t.Fatal("positive handoff did not occupy exactly the released slot")
			}
			targets, fillers := 0, 0
			for i := 0; i < f.capacity; i++ {
				msg := <-f.bus.InboundChan()
				switch msg.ChatID {
				case b3CallerParentFiller:
					fillers++
				case b3CallerParentTarget:
					targets++
					if msg.Channel != f.message.Channel || msg.Content != f.message.Content {
						t.Error("accepted target differs from the prepared channel/content")
					}
				default:
					t.Error("unexpected message at the actual concrete inbound boundary")
				}
			}
			if targets != 1 || fillers != f.capacity-1 {
				t.Fatalf("actual inbound counts target=%d filler=%d; want one target and all remaining original fillers", targets, fillers)
			}
			if len(f.bus.InboundChan()) != 0 {
				t.Fatal("positive handoff left an unexpected extra inbound")
			}
		})
	})
	t.Run("CanceledCallerStopsBlockedAttempt", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := b3NewCallerParentFixture(t)
			defer f.bus.Close()
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			run := f.start(caller)
			f.requireBlocked(t, caller, run)
			cancel()
			if caller.Err() != context.Canceled {
				t.Fatal("INVALID fixture: actual caller did not cancel")
			}
			if !f.requireStopped(t, run, true) {
				return
			}
			if run.attempts != 1 {
				t.Fatalf("caller cancellation attempts=%d; want exactly one actual attempt", run.attempts)
			}
			if !errors.Is(run.err, context.Canceled) {
				t.Fatalf("caller cancellation lost from actual joined error: %v", run.err)
			}
			f.requireOnlyFillers(t)
		})
	})
	t.Run("DeadlineCallerStopsBlockedAttempt", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := b3NewCallerParentFixture(t)
			defer f.bus.Close()
			// Independent fixture caller deadline: TWO seconds, strictly less
			// than the contract's unchanged FIVE-second production child.
			caller, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			run := f.start(caller)
			f.requireBlocked(t, caller, run)
			// Advance the real native deadline within the standard test bubble;
			// this is not an elapsed-time assertion or a scheduling tolerance.
			time.Sleep(2 * time.Second)
			synctest.Wait()
			if caller.Err() != context.DeadlineExceeded {
				t.Fatal("INVALID fixture: actual caller deadline did not expire")
			}
			if !f.requireStopped(t, run, true) {
				return
			}
			if run.attempts != 1 {
				t.Fatalf("caller deadline attempts=%d; want exactly one actual attempt", run.attempts)
			}
			if !errors.Is(run.err, context.DeadlineExceeded) {
				t.Fatalf("caller deadline lost from actual joined error: %v", run.err)
			}
			f.requireOnlyFillers(t)
		})
	})
}
