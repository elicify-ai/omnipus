package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

func aggItem(n int) steeringQueueItem {
	return steeringQueueItem{message: providers.Message{Role: "user", Content: strings.Repeat("a", n)}}
}

// FR-010 / BDD-03.3: the waiting ordinary bytes of one scope may reach the cap
// exactly; one byte more is refused and nothing is shortened. A second scope is
// not affected, and a wake still passes at the cap.
func TestSteeringAggregateBody_BoundaryAtTheCap(t *testing.T) {
	sq := newSteeringQueue()
	sq.SetAggregateCap(100)
	if err := sq.pushItemScope("s1", aggItem(60)); err != nil {
		t.Fatal(err)
	}
	if err := sq.pushItemScope("s1", aggItem(40)); err != nil {
		t.Fatalf("exactly at the cap must be accepted: %v", err)
	}
	err := sq.pushItemScope("s1", aggItem(1))
	if !errors.Is(err, errSteeringAggregateFull) {
		t.Fatalf("one byte over must be refused with errSteeringAggregateFull, got %v", err)
	}
	if got := waitingBodyBytes(sq.queues["s1"]); got != 100 {
		t.Fatalf("refusal must not change the queue, waiting bytes = %d", got)
	}
	if err := sq.pushItemScope("s2", aggItem(100)); err != nil {
		t.Fatalf("another scope must be unaffected: %v", err)
	}
	wake := steeringQueueItem{wake: &steeringWake{messageID: "m1"}}
	if err := sq.pushItemScope("s1", wake); err != nil {
		t.Fatalf("a wake is exempt at the cap: %v", err)
	}
}

// The finishing-window buffer is bounded by the same cap.
func TestSteeringAggregateBody_FinishingWindowBounded(t *testing.T) {
	sq := newSteeringQueue()
	sq.SetAggregateCap(10)
	sq.terminalizing["s1"] = &steeringTerminalTransition{done: make(chan struct{})}
	if _, err := sq.pushItemScopeChecked("s1", aggItem(10), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := sq.pushItemScopeChecked("s1", aggItem(1), nil); !errors.Is(err, errSteeringAggregateFull) {
		t.Fatalf("finishing buffer must refuse past the cap, got %v", err)
	}
}

// BDD-03.4: the saved value reaches the queue through the boot/reload wiring,
// and the sender sees a bound-specific sentence.
func TestSteeringAggregateBody_ConfigReachesTheQueue(t *testing.T) {
	f := boundConnFixture(t)
	cfg := config.DefaultConfig()
	cfg.SessionMessaging.SteerAggregateBody = 50
	f.al.wireSessionMessagingForAgent(nil, cfg)
	if err := f.al.steering.pushItemScope("sx", aggItem(50)); err != nil {
		t.Fatal(err)
	}
	err := f.al.steering.pushItemScope("sx", aggItem(1))
	if !errors.Is(err, errSteeringAggregateFull) {
		t.Fatalf("configured cap not enforced: %v", err)
	}
	if got := curateSteerRefusal("sx", err).Error(); !strings.Contains(got, "exceed") {
		t.Fatalf("refusal sentence not bound-specific: %q", got)
	}
	cfg.SessionMessaging.SteerAggregateBody = 51 // hot reload widens it
	f.al.wireSessionMessagingForAgent(nil, cfg)
	if err := f.al.steering.pushItemScope("sx", aggItem(1)); err != nil {
		t.Fatalf("reloaded cap must take effect: %v", err)
	}
}
