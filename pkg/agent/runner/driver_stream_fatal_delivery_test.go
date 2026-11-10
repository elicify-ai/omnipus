package runner

import (
	"context"
	"strings"
	"testing"
	"time"
)

// turnCapFatalParseLine mimics every driver's turn-cap branch
// (parseAssistantEvent / codex turn.completed / opencode message.start): it
// calls the run's cancel BEFORE returning the fatal "turn cap exceeded" event,
// so by the time streamParser tries to deliver the event, ctx is already done.
func turnCapFatalParseLine(cancel context.CancelFunc) func(raw []byte) (RunEvent, bool) {
	return func(raw []byte) (RunEvent, bool) {
		cancel()
		return RunEvent{
			Kind: EventKindError,
			Err:  &ErrorEvent{Message: "turn cap exceeded: 3 turns (max 2)", Fatal: true},
		}, true
	}
}

// TestStreamParser_FatalEventDeliveredAfterCancel_BufferedRoom pins #904 F1:
// a fatal event whose producer cancelled the run context must still reach the
// consumer when the out channel has room. Before the fix, streamParser's
// `select { case out <- ev: case <-ctx.Done(): }` saw both cases ready and
// Go picked one at random, so the fatal event was dropped about half the
// time. 200 iterations make the old ordering fail with probability
// 1 - 2^-200 — deterministic for all practical purposes.
func TestStreamParser_FatalEventDeliveredAfterCancel_BufferedRoom(t *testing.T) {
	const iterations = 200
	for i := 0; i < iterations; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		out := make(chan RunEvent, 4)
		emitted := streamParser(ctx, strings.NewReader("{\"type\":\"assistant\"}\n"), "run-f1",
			turnCapFatalParseLine(cancel), out)
		cancel()
		close(out)

		var got []RunEvent
		for ev := range out {
			got = append(got, ev)
		}
		if len(got) != 1 {
			t.Fatalf("iteration %d: got %d events, want exactly 1 fatal turn-cap event (dropped on cancel)", i, len(got))
		}
		ev := got[0]
		if ev.Kind != EventKindError || ev.Err == nil || !ev.Err.Fatal {
			t.Fatalf("iteration %d: got event %+v, want a fatal EventKindError", i, ev)
		}
		if ev.Err.Message != "turn cap exceeded: 3 turns (max 2)" {
			t.Fatalf("iteration %d: message = %q, want the turn-cap message", i, ev.Err.Message)
		}
		if ev.RunID != "run-f1" {
			t.Fatalf("iteration %d: RunID = %q, want run-f1", i, ev.RunID)
		}
		if !emitted {
			t.Fatalf("iteration %d: emittedFatal = false, want true after delivering a fatal event", i)
		}
	}
}

// TestStreamParser_FatalEventDeliveredAfterCancel_ConsumerLate pins the
// full-buffer half of #904 F1: with no buffer room at the moment of cancel
// (unbuffered channel, consumer not yet receiving), the fatal event must
// still be handed over to a consumer that starts receiving shortly after.
// Before the fix only ctx.Done() was ready, so the event was dropped every
// time.
func TestStreamParser_FatalEventDeliveredAfterCancel_ConsumerLate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan RunEvent) // unbuffered: no room until the consumer receives

	type result struct{ emitted bool }
	done := make(chan result, 1)
	go func() {
		emitted := streamParser(ctx, strings.NewReader("{\"type\":\"assistant\"}\n"), "run-f1-late",
			turnCapFatalParseLine(cancel), out)
		done <- result{emitted: emitted}
	}()

	// Start receiving only after the producer has cancelled the context.
	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)

	select {
	case ev := <-out:
		if ev.Kind != EventKindError || ev.Err == nil || !ev.Err.Fatal {
			t.Fatalf("got event %+v, want a fatal EventKindError", ev)
		}
		if ev.Err.Message != "turn cap exceeded: 3 turns (max 2)" {
			t.Fatalf("message = %q, want the turn-cap message", ev.Err.Message)
		}
	case res := <-done:
		t.Fatalf("streamParser returned (emittedFatal=%v) without delivering the fatal turn-cap event", res.emitted)
	case <-time.After(fatalDeliveryGrace + time.Second):
		t.Fatal("timed out waiting for the fatal turn-cap event")
	}

	select {
	case res := <-done:
		if !res.emitted {
			t.Fatal("emittedFatal = false, want true after delivering a fatal event")
		}
	case <-time.After(fatalDeliveryGrace + time.Second):
		t.Fatal("streamParser did not return after delivering the fatal event")
	}
}
