package webrtc

import (
	"context"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func pressureWheel(seq int, dx, dy float64) generated.BrowserInputFrame {
	epoch, control, barrier, generation, modifiers := 1, 0, 0, 7, 0
	x, y, capture := 120.0, 240.0, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	return generated.BrowserInputFrame{Type: "browser_input", Kind: "wheel", InputEpoch: &epoch, ControlEpoch: &control, GestureBarrier: &barrier, ReliableSeq: &seq, X: &x, Y: &y, DeltaX: &dx, DeltaY: &dy, CaptureId: &capture, CaptureGeneration: &generation, Modifiers: &modifiers}
}

func TestDedicatedInputQueueWheelPendingMerge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var dispatched []generated.BrowserInputFrame
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
			if *f.ReliableSeq == 1 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return
				}
			}
			dispatched = append(dispatched, f)
		}, func(reason string) { t.Errorf("unexpected failure: %s", reason) })
		defer q.close()
		q.submit(false, pressureWheel(1, 0, 10))
		<-entered
		q.submit(false, pressureWheel(2, 0, 20))
		q.submit(false, pressureWheel(3, 0, 30))
		close(release)
		synctest.Wait()
		if len(dispatched) != 2 {
			t.Fatalf("pending compatible wheels dispatched %d times; want active plus one merged pending dispatch", len(dispatched))
		}
		if *dispatched[0].DeltaY != 10 || *dispatched[1].DeltaY != 50 {
			t.Fatalf("wheel totals changed: %+v", dispatched)
		}
		q.submit(false, pressureWheel(4, 0, 40))
		synctest.Wait()
		if len(dispatched) != 3 || *dispatched[2].ReliableSeq != 4 {
			t.Fatal("merged sequence admission lost the next valid action")
		}
	})
}

func TestDedicatedInputQueueWheelSustainedPressure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failed := make(chan string, 1)
		var total float64
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
			select {
			case <-time.After(75 * time.Millisecond):
				total += *f.DeltaY
			case <-ctx.Done():
			}
		}, func(reason string) { failed <- reason })
		defer q.close()
		for seq := 1; seq <= 120; seq++ {
			q.submit(false, pressureWheel(seq, 0, 10))
			time.Sleep(30 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
		synctest.Wait()
		if len(failed) != 0 {
			t.Fatalf("ordinary compatible wheel pressure failed: %s", <-failed)
		}
		if total != 1200 {
			t.Fatalf("delivered wheel delta=%v want1200", total)
		}
	})
}

func TestDedicatedInputQueueWheelNonMergeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*generated.BrowserInputFrame)
	}{
		{"position", func(f *generated.BrowserInputFrame) { x := 121.0; f.X = &x }},
		{"modifiers", func(f *generated.BrowserInputFrame) { m := 2; f.Modifiers = &m }},
		{"capture", func(f *generated.BrowserInputFrame) {
			c := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			f.CaptureId = &c
		}},
		{"generation", func(f *generated.BrowserInputFrame) { g := 8; f.CaptureGeneration = &g }},
		{"capture_dimensions", func(f *generated.BrowserInputFrame) { w := 900.0; f.CaptureWidth = &w }},
		{"reverse_y", func(f *generated.BrowserInputFrame) { d := -10.0; f.DeltaY = &d }},
		{"reverse_x", func(f *generated.BrowserInputFrame) { d := -10.0; f.DeltaX = &d }},
		{"button", func(f *generated.BrowserInputFrame) { b := "left"; f.Button = &b }},
		{"missing_capture", func(f *generated.BrowserInputFrame) { f.CaptureId = nil }},
		{"nonfinite", func(f *generated.BrowserInputFrame) { d := math.Inf(1); f.DeltaY = &d }},
		{"overflow", func(f *generated.BrowserInputFrame) { d := math.MaxFloat64; f.DeltaY = &d }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				var got []generated.BrowserInputFrame
				q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
					if *f.ReliableSeq == 1 {
						close(entered)
						select {
						case <-release:
						case <-ctx.Done():
							return
						}
					}
					got = append(got, f)
				}, func(reason string) { t.Errorf("unexpected failure: %s", reason) })
				defer q.close()
				q.submit(false, pressureWheel(1, 0, 1))
				<-entered
				a, b := pressureWheel(2, 10, 10), pressureWheel(3, 10, 10)
				if tc.name == "overflow" {
					d := math.MaxFloat64
					a.DeltaY = &d
				}
				tc.change(&b)
				q.submit(false, a)
				q.submit(false, b)
				close(release)
				synctest.Wait()
				if len(got) != 3 || *got[1].ReliableSeq != 2 || *got[2].ReliableSeq != 3 {
					t.Fatal("incompatible wheels were merged or reordered")
				}
			})
		})
	}
}

func TestDedicatedInputQueueWheelDoesNotCrossActionsOrHeldInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var got []int
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
			if *f.ReliableSeq == 1 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return
				}
			}
			got = append(got, *f.ReliableSeq)
		}, func(reason string) { t.Errorf("unexpected failure: %s", reason) })
		defer q.close()
		q.submit(false, pressureWheel(1, 0, 10))
		<-entered
		q.submit(false, pressureWheel(2, 0, 10))
		middle := pressureWheel(3, 0, 10)
		middle.Kind = "text"
		q.submit(false, middle)
		q.submit(false, pressureWheel(4, 0, 10))
		down := pressureWheel(5, 0, 10)
		down.Kind = "mouse_down"
		b, barrier := "left", 1
		down.Button = &b
		down.GestureBarrier = &barrier
		q.submit(false, down)
		for seq := 6; seq <= 7; seq++ {
			wheel := pressureWheel(seq, 0, 10)
			wheel.GestureBarrier = &barrier
			q.submit(false, wheel)
		}
		up := pressureWheel(8, 0, 10)
		up.Kind = "mouse_up"
		next := 2
		up.Button = &b
		up.GestureBarrier = &next
		q.submit(false, up)
		close(release)
		synctest.Wait()
		if len(got) != 8 {
			t.Fatalf("dispatched sequence=%v want1through8", got)
		}
		for i, seq := range got {
			if seq != i+1 {
				t.Fatalf("out of order: %v", got)
			}
		}
	})
}

func TestDedicatedInputQueueWheelMergedOldestExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		failed := make(chan string, 1)
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, _ generated.BrowserInputFrame) { close(entered); <-ctx.Done() }, func(reason string) { failed <- reason })
		defer q.close()
		q.submit(false, pressureWheel(1, 0, 10))
		<-entered
		q.submit(false, pressureWheel(2, 0, 10))
		time.Sleep(900 * time.Millisecond)
		q.submit(false, pressureWheel(3, 0, 10))
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		select {
		case reason := <-failed:
			if reason != "reliable input queue expired" {
				t.Fatalf("failure=%s", reason)
			}
		default:
			t.Fatal("merging refreshed the oldest waiting deadline")
		}
		select {
		case <-q.done:
		default:
			t.Fatal("expired source remains active")
		}
	})
}

func TestDedicatedInputQueueWheelSequenceGapStillFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		failed := make(chan string, 1)
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, _ generated.BrowserInputFrame) { close(entered); <-ctx.Done() }, func(reason string) { failed <- reason })
		defer q.close()
		q.submit(false, pressureWheel(1, 0, 10))
		<-entered
		q.submit(false, pressureWheel(2, 0, 10))
		q.submit(false, pressureWheel(3, 0, 10))
		q.submit(false, pressureWheel(5, 0, 10))
		synctest.Wait()
		select {
		case reason := <-failed:
			if reason != "invalid reliable sequence" {
				t.Fatalf("failure=%s", reason)
			}
		default:
			t.Fatal("coalescing bypassed sequence admission")
		}
	})
}

func TestDedicatedInputQueueWheelMergedRetirement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		var got []int
		failed := make(chan string, 1)
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
			if *f.ControlEpoch == 0 {
				close(entered)
				<-ctx.Done()
				return
			}
			got = append(got, *f.ReliableSeq)
		}, func(reason string) { failed <- reason })
		defer q.close()
		q.submit(false, pressureWheel(1, 0, 10))
		<-entered
		q.submit(false, pressureWheel(2, 0, 10))
		q.submit(false, pressureWheel(3, 0, 10))
		_, done, err := q.pause(1)
		if err != nil {
			t.Fatal(err)
		}
		<-done
		if err := q.resume(1); err != nil {
			t.Fatal(err)
		}
		// Old identity is ignored even though it would otherwise be mergeable.
		q.submit(false, pressureWheel(4, 0, 10))
		fresh := pressureWheel(1, 0, 40)
		control := 1
		fresh.ControlEpoch = &control
		q.submit(false, fresh)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if len(got) != 1 || got[0] != 1 || len(failed) != 0 {
			t.Fatalf("retired merged work affected replacement: got%v failed%d", got, len(failed))
		}
	})
}

func TestDedicatedInputQueueWheelTimingRange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var timings []InputQueueTiming
		var frames []generated.BrowserInputFrame
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
			if *f.ReliableSeq == 1 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
		}, func(reason string) { t.Errorf("unexpected failure: %s", reason) })
		defer q.close()
		q.setTimingObserver(func(f generated.BrowserInputFrame, timing InputQueueTiming) {
			frames = append(frames, f)
			timings = append(timings, timing)
		})
		firstAt := time.Now()
		q.submit(false, pressureWheel(1, 0, 10))
		<-entered
		time.Sleep(30 * time.Millisecond)
		oldestAt := time.Now()
		q.submit(false, pressureWheel(2, 0, 20))
		time.Sleep(30 * time.Millisecond)
		q.submit(false, pressureWheel(3, 0, 30))
		close(release)
		synctest.Wait()
		if len(timings) != 2 {
			t.Fatalf("timing rows=%d want2", len(timings))
		}
		if timings[0] != (InputQueueTiming{EnqueuedAt: firstAt, FirstReliableSeq: 1, LastReliableSeq: 1, InputCount: 1}) {
			t.Fatalf("active timing=%+v", timings[0])
		}
		if timings[1] != (InputQueueTiming{EnqueuedAt: oldestAt, FirstReliableSeq: 2, LastReliableSeq: 3, InputCount: 2}) {
			t.Fatalf("merged timing=%+v", timings[1])
		}
		if *frames[1].ReliableSeq != 2 || *frames[1].DeltaY != 50 {
			t.Fatal("timing must describe the actual merged dispatch, keeping first sequence")
		}
	})
}

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

// The clock and blocked browser boundary are controlled; queue admission,
// ordering, cancellation and failure delivery remain the production paths.
func TestDedicatedInputQueueReliableAgeBoundary(t *testing.T) {
	for _, sample := range []struct {
		name    string
		age     time.Duration
		expires bool
	}{
		{"before deadline", time.Second - time.Nanosecond, false},
		{"at deadline", time.Second, true},
		{"after deadline", time.Second + time.Nanosecond, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				dispatched := make(chan int, 4)
				failed := make(chan string, 2)
				var active context.Context
				q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, frame generated.BrowserInputFrame) {
					if *frame.ReliableSeq == 1 {
						active = ctx
						close(entered)
						<-release
					}
					dispatched <- *frame.ReliableSeq
				}, func(reason string) { failed <- reason })
				defer q.close()
				frame := func(kind string, sequence, barrier int) generated.BrowserInputFrame {
					epoch, control, key := 1, 0, "ArrowLeft"
					return generated.BrowserInputFrame{Kind: kind, InputEpoch: &epoch, ControlEpoch: &control, ReliableSeq: &sequence, GestureBarrier: &barrier, Key: &key}
				}
				q.submit(false, frame("key_down", 1, 1))
				<-entered
				q.submit(false, frame("key_up", 2, 2))
				q.submit(false, frame("text", 3, 2))
				time.Sleep(sample.age)
				close(release)
				synctest.Wait()
				if sample.expires {
					select {
					case reason := <-failed:
						if reason != "reliable input queue expired" {
							t.Fatalf("failure=%q", reason)
						}
					default:
						t.Fatal("expired reliable backlog did not explicitly fail")
					}
					if active.Err() != context.Canceled {
						t.Fatal("expired peer source was not canceled for held-input cleanup")
					}
					if len(dispatched) != 1 {
						t.Fatalf("expired actions reached browser: %d dispatches", len(dispatched))
					}
					if len(failed) != 0 {
						t.Fatal("failure emitted more than once")
					}
				} else {
					for _, want := range []int{1, 2, 3} {
						select {
						case got := <-dispatched:
							if got != want {
								t.Fatalf("dispatch=%d want=%d", got, want)
							}
						default:
							t.Fatalf("missing ordered input %d", want)
						}
					}
					if active.Err() != nil || len(failed) != 0 {
						t.Fatal("fresh reliable input was canceled")
					}
				}
			})
		})
	}
}

func TestDedicatedInputQueueExpiryCancelsBlockedSourceWithoutMoreArrivals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		failed := make(chan string, 2)
		dispatched := make(chan int, 2)
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, frame generated.BrowserInputFrame) {
			dispatched <- *frame.ReliableSeq
			close(entered)
			<-ctx.Done()
		}, func(reason string) { failed <- reason })
		defer q.close()
		epoch, control, barrier, first, second := 1, 0, 0, 1, 2
		q.submit(false, generated.BrowserInputFrame{Kind: "text", InputEpoch: &epoch, ControlEpoch: &control, GestureBarrier: &barrier, ReliableSeq: &first})
		<-entered
		q.submit(false, generated.BrowserInputFrame{Kind: "text", InputEpoch: &epoch, ControlEpoch: &control, GestureBarrier: &barrier, ReliableSeq: &second})
		time.Sleep(time.Second)
		synctest.Wait()
		select {
		case reason := <-failed:
			if reason != "reliable input queue expired" {
				t.Fatalf("failure=%q", reason)
			}
		default:
			t.Fatal("waiting backlog did not fail at its age bound without new arrivals")
		}
		select {
		case <-q.done:
		default:
			t.Fatal("blocked source was not canceled and joined")
		}
		if len(dispatched) != 1 || len(failed) != 0 {
			t.Fatal("expired action dispatched or failure duplicated")
		}
	})
}

func TestDedicatedInputQueueRetirementStopsExpiry(t *testing.T) {
	for _, retirement := range []string{"pause", "close"} {
		t.Run(retirement, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered := make(chan struct{}, 2)
				failed := make(chan string, 2)
				q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, _ generated.BrowserInputFrame) {
					entered <- struct{}{}
					<-ctx.Done()
				}, func(reason string) { failed <- reason })
				defer q.close()
				epoch, control, barrier, first, second := 1, 0, 0, 1, 2
				q.submit(false, generated.BrowserInputFrame{Kind: "text", InputEpoch: &epoch, ControlEpoch: &control, GestureBarrier: &barrier, ReliableSeq: &first})
				<-entered
				q.submit(false, generated.BrowserInputFrame{Kind: "text", InputEpoch: &epoch, ControlEpoch: &control, GestureBarrier: &barrier, ReliableSeq: &second})
				if retirement == "pause" {
					_, done, err := q.pause(1)
					if err != nil {
						t.Fatal(err)
					}
					<-done
					if err := q.resume(1); err != nil {
						t.Fatal(err)
					}
				} else {
					q.close()
				}
				time.Sleep(2 * time.Second)
				synctest.Wait()
				if len(failed) != 0 {
					t.Fatal("retired backlog timer failed the closed or replacement source")
				}
				if len(entered) != 0 {
					t.Fatal("retired queued input dispatched")
				}
			})
		})
	}
}
