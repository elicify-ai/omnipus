package webrtc

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ADR-081: expected order is authored explicitly, independent of queue internals.
func TestDedicatedInputQueueBarrierAndOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, unblock := make(chan struct{}), make(chan struct{})
	got := make(chan string, 8)
	q := newDedicatedInputQueue(ctx, 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
		if f.Kind == "text" {
			close(entered)
			select {
			case <-unblock:
			case <-ctx.Done():
				return
			}
		}
		got <- f.Kind
	}, func(string) { t.Error("unexpected failure") })
	defer q.close()
	frame := func(kind string, seq, barrier int) generated.BrowserInputFrame {
		e, c := 1, 0
		return generated.BrowserInputFrame{Type: "browser_input", Kind: kind, InputEpoch: &e, ControlEpoch: &c, ReliableSeq: &seq, GestureBarrier: &barrier}
	}
	q.submit(false, frame("text", 1, 0))
	<-entered
	down := frame("mouse_down", 2, 1)
	button := "left"
	down.Button = &button
	q.submit(false, down)
	hover := frame("mouse_move", 0, 0)
	hover.ReliableSeq = nil
	hs := 1
	hover.HoverSeq = &hs
	q.submit(true, hover)
	up := frame("mouse_up", 3, 2)
	up.Button = &button
	q.submit(false, up)
	close(unblock)
	for _, want := range []string{"text", "mouse_down", "mouse_up"} {
		select {
		case actual := <-got:
			if actual != want {
				t.Fatalf("dispatch=%s want=%s", actual, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("missing %s", want)
		}
	}
	fresh := frame("mouse_move", 0, 2)
	fresh.ReliableSeq = nil
	freshSeq := 2
	fresh.HoverSeq = &freshSeq
	q.submit(true, fresh)
	select {
	case actual := <-got:
		if actual != "mouse_move" {
			t.Fatalf("fresh hover=%s", actual)
		}
	case <-time.After(time.Second):
		t.Fatal("fresh hover missing")
	}
	select {
	case extra := <-got:
		t.Fatalf("extra event %s", extra)
	default:
	}
}

func TestDedicatedInputQueueRejectsHoverActionsAndSaturation(t *testing.T) {
	for _, kind := range []string{"key_down", "wheel", "text", "mouse_down"} {
		t.Run(kind, func(t *testing.T) {
			failed := make(chan string, 1)
			q := newDedicatedInputQueue(context.Background(), 1, 0, func(context.Context, generated.BrowserInputFrame) { t.Error("invalid hover dispatched") }, func(s string) { failed <- s })
			defer q.close()
			e, c, h, b := 1, 0, 1, 0
			q.submit(true, generated.BrowserInputFrame{Kind: kind, InputEpoch: &e, ControlEpoch: &c, HoverSeq: &h, GestureBarrier: &b})
			select {
			case s := <-failed:
				if s != "invalid hover payload" {
					t.Fatalf("reason %q", s)
				}
			case <-time.After(time.Second):
				t.Fatal("invalid hover did not fail peer")
			}
		})
	}
	t.Run("saturation cancels blocked sink", func(t *testing.T) {
		entered := make(chan struct{})
		failed := make(chan string, 1)
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, _ generated.BrowserInputFrame) { close(entered); <-ctx.Done() }, func(s string) { failed <- s })
		defer q.close()
		e, c, b := 1, 0, 0
		for i := 1; i <= inputQueueCapacity+2; i++ {
			seq := i
			q.submit(false, generated.BrowserInputFrame{Kind: "text", InputEpoch: &e, ControlEpoch: &c, ReliableSeq: &seq, GestureBarrier: &b})
			if i == 1 {
				<-entered
			}
		}
		select {
		case s := <-failed:
			if s != "reliable input queue full" {
				t.Fatalf("reason %q", s)
			}
		case <-time.After(time.Second):
			t.Fatal("overflow did not fail")
		}
		select {
		case <-q.done:
		case <-time.After(time.Second):
			t.Fatal("overflow did not cancel blocked dispatch")
		}
	})
}

func TestDedicatedInputQueueControlRetiresPendingAndInFlight(t *testing.T) {
	entered := make(chan struct{})
	got := make(chan int, 3)
	q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
		if *f.ControlEpoch == 0 && *f.ReliableSeq == 1 {
			close(entered)
			<-ctx.Done()
			return
		}
		got <- *f.ReliableSeq
	}, func(s string) { t.Errorf("failure: %s", s) })
	defer q.close()
	e, c, b := 1, 0, 0
	for i := 1; i <= 2; i++ {
		seq := i
		q.submit(false, generated.BrowserInputFrame{Kind: "text", InputEpoch: &e, ControlEpoch: &c, ReliableSeq: &seq, GestureBarrier: &b})
		if i == 1 {
			<-entered
		}
	}
	source, done, err := q.pause(1)
	if err != nil {
		t.Fatal(err)
	}
	if source.Err() != context.Canceled {
		t.Fatal("old source remains live")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old dispatch did not retire")
	}
	if err = q.resume(1); err != nil {
		t.Fatal(err)
	}
	seq := 3
	q.submit(false, generated.BrowserInputFrame{Kind: "text", InputEpoch: &e, ControlEpoch: &c, ReliableSeq: &seq, GestureBarrier: &b})
	newControl, newSeq := 1, 1
	q.submit(false, generated.BrowserInputFrame{Kind: "text", InputEpoch: &e, ControlEpoch: &newControl, ReliableSeq: &newSeq, GestureBarrier: &b})
	select {
	case actual := <-got:
		if actual != 1 {
			t.Fatalf("old pending input executed: %d", actual)
		}
	case <-time.After(time.Second):
		t.Fatal("new epoch did not execute")
	}
}

// A WebSocket control may overtake an old reliable packet on the other PC.
// Neither its sequence nor its gesture boundary may poison the next epoch.
func TestDedicatedInputQueueControlOvertakesReliablePacket(t *testing.T) {
	delivered := make(chan generated.BrowserInputFrame, 4)
	failures := make(chan string, 4)
	q := newDedicatedInputQueue(context.Background(), 1, 0, func(_ context.Context, f generated.BrowserInputFrame) { delivered <- f }, func(reason string) { failures <- reason })
	defer q.close()
	peer, old, first, zero := 1, 0, 1, 0
	q.submit(false, generated.BrowserInputFrame{Kind: "text", InputEpoch: &peer, ControlEpoch: &old, ReliableSeq: &first, GestureBarrier: &zero})
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("initial input missing")
	}
	_, done, err := q.pause(1)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old worker did not retire")
	}
	if err := q.resume(1); err != nil {
		t.Fatal(err)
	}
	lateSeq, boundary, next := 2, 1, 1
	// This was sent before control, but reaches the server after its ack.
	q.submit(false, generated.BrowserInputFrame{Kind: "mouse_down", InputEpoch: &peer, ControlEpoch: &old, ReliableSeq: &lateSeq, GestureBarrier: &boundary})
	q.submit(false, generated.BrowserInputFrame{Kind: "mouse_down", InputEpoch: &peer, ControlEpoch: &next, ReliableSeq: &first, GestureBarrier: &boundary})
	select {
	case f := <-delivered:
		if *f.ControlEpoch != 1 || *f.ReliableSeq != 1 || *f.GestureBarrier != 1 {
			t.Fatalf("wrong epoch delivered: %#v", f)
		}
	case reason := <-failures:
		t.Fatalf("valid new epoch failed: %s", reason)
	case <-time.After(time.Second):
		t.Fatal("new epoch input missing")
	}
}
