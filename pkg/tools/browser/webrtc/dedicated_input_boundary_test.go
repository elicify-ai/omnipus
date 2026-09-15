package webrtc

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// R2 in docs/plan/browser-input-connection/spec.md permits loss and sequence
// gaps for hover, but never stale positions or unbounded pending hover work.
func TestDedicatedInputHoverKeepsOnlyNewestEligiblePosition(t *testing.T) {
	for _, tc := range []struct {
		name    string
		packets [][3]int // sequence, gesture barrier, x
		want    int
	}{
		{"duplicate", [][3]int{{1, 2, 10}, {1, 2, 99}}, 10},
		{"older", [][3]int{{2, 2, 20}, {1, 2, 99}}, 20},
		{"lost packet", [][3]int{{1, 2, 10}, {3, 2, 30}}, 30},
		{"latest only", [][3]int{{1, 2, 10}, {2, 2, 20}, {3, 2, 30}}, 30},
		{"future barrier", [][3]int{{1, 2, 10}, {2, 3, 99}}, 10},
		{"past barrier", [][3]int{{1, 2, 10}, {2, 1, 99}}, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			delivered := make(chan generated.BrowserInputFrame, 8)
			q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
				if f.Kind == "text" {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return
					}
					return
				}
				if f.Kind == "mouse_move" {
					delivered <- f
					<-ctx.Done()
				}
			}, func(reason string) { t.Errorf("valid hover failed: %s", reason) })
			t.Cleanup(q.close)
			q.submit(false, boundaryInput("text", 1, 0))
			awaitInputSignal(t, entered, "blocked browser operation")
			// A completed button gesture advances the barrier twice.
			button := "left"
			for i, kind := range []string{"mouse_down", "mouse_up"} {
				f := boundaryInput(kind, i+2, i+1)
				f.Button = &button
				q.submit(false, f)
			}
			for _, packet := range tc.packets {
				f := boundaryInput("mouse_move", 0, packet[1])
				f.ReliableSeq = nil
				seq, x := packet[0], float64(packet[2])
				f.HoverSeq, f.X = &seq, &x
				q.submit(true, f)
			}
			close(release)
			select {
			case f := <-delivered:
				if f.Kind != "mouse_move" || f.X == nil || *f.X != float64(tc.want) {
					t.Fatalf("hover delivered %+v; want mouse_move at x=%d", f, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("newest eligible hover was not delivered")
			}
			// Browser execution is still gated, so pending work cannot race
			// this check or be hidden by cancellation clearing the queue.
			q.mu.Lock()
			pendingReliable, pendingHover := len(q.frames), q.hover != nil
			q.mu.Unlock()
			if pendingReliable != 0 || pendingHover {
				t.Fatalf("extra pending input: reliable=%d hover=%v", pendingReliable, pendingHover)
			}
			_, done, err := q.pause(1)
			if err != nil {
				t.Fatal(err)
			}
			awaitInputSignal(t, done, "hover worker retirement")
			select {
			case extra := <-delivered:
				t.Fatalf("extra hover delivered: %+v", extra)
			default:
			}
		})
	}
}

// Safe-integer limits come from BrowserInputFrame's contract; zero is allowed
// for control/barrier and forbidden for peer/reliable/hover identities.
func TestDedicatedInputCounterBoundaries(t *testing.T) {
	const wireMax = 9007199254740991
	for _, min := range []int{0, 1} {
		for _, tc := range []struct {
			name  string
			value *int
			want  bool
		}{
			{"missing", nil, false},
			{"below minimum", boundaryInt(min - 1), false},
			{"minimum", boundaryInt(min), true},
			{"minimum plus one", boundaryInt(min + 1), true},
			{"maximum minus one", boundaryInt(wireMax - 1), true},
			{"maximum", boundaryInt(wireMax), true},
			{"overflow", boundaryInt(wireMax + 1), false},
		} {
			t.Run(fmt.Sprintf("min%d/%s", min, tc.name), func(t *testing.T) {
				if got := validInputCounter(tc.value, min); got != tc.want {
					t.Fatalf("counter valid=%v, want %v for minimum %d, value %v", got, tc.want, min, tc.value)
				}
			})
		}
	}
}

func TestDedicatedInputInvalidCountersCancelBeforeDispatch(t *testing.T) {
	for _, field := range []string{"peer", "control", "barrier", "reliable", "hover"} {
		for _, bad := range []*int{nil, boundaryInt(-1), boundaryInt(9007199254740992)} {
			t.Run(fmt.Sprintf("%s/%v", field, bad), func(t *testing.T) {
				failures := make(chan string, 2)
				q := newDedicatedInputQueue(context.Background(), 1, 0, func(context.Context, generated.BrowserInputFrame) {
					t.Error("invalid counter reached browser execution")
				}, func(reason string) { failures <- reason })
				t.Cleanup(q.close)
				f := boundaryInput("text", 1, 0)
				want := "invalid input identity"
				switch field {
				case "peer":
					f.InputEpoch = bad
				case "control":
					f.ControlEpoch = bad
				case "barrier":
					f.GestureBarrier = bad
				case "reliable":
					f.ReliableSeq = bad
					want = "invalid reliable sequence"
				case "hover":
					f.Kind = "mouse_move"
					f.ReliableSeq = nil
					f.HoverSeq = bad
					want = "invalid hover payload"
				}
				q.submit(field == "hover", f)
				select {
				case reason := <-failures:
					if reason != want {
						t.Fatalf("failure=%q, want %q", reason, want)
					}
				case <-time.After(time.Second):
					t.Fatalf("%s invalid counter did not fail", field)
				}
				awaitInputSignal(t, q.done, "invalid input worker cancellation")
			})
		}
	}
}

// R1/R3 require saturation to retire the source even when browser execution is
// blocked holding a button. Actual Chrome key/button release is covered by
// live_input_drain_test.go; this test proves the prerequisite cancellation.
func TestDedicatedInputSaturationCancelsHeldButtonSource(t *testing.T) {
	entered := make(chan struct{})
	var kinds []string
	failures := make(chan string, 2)
	q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
		kinds = append(kinds, f.Kind)
		close(entered)
		<-ctx.Done()
	}, func(reason string) { failures <- reason })
	t.Cleanup(q.close)
	down := boundaryInput("mouse_down", 1, 1)
	button := "left"
	down.Button = &button
	q.submit(false, down)
	awaitInputSignal(t, entered, "held button dispatch")
	for seq := 2; seq <= inputQueueCapacity+1; seq++ {
		q.submit(false, boundaryInput("mouse_move", seq, 1))
	}
	select {
	case reason := <-failures:
		t.Fatalf("queue failed before capacity: %s", reason)
	default:
	}
	up := boundaryInput("mouse_up", inputQueueCapacity+2, 2)
	up.Button = &button
	q.submit(false, up)
	select {
	case reason := <-failures:
		if reason != "reliable input queue full" {
			t.Fatalf("failure=%q, want queue full", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("full reliable queue did not fail")
	}
	awaitInputSignal(t, q.done, "held button source cancellation")
	if !reflect.DeepEqual(kinds, []string{"mouse_down"}) {
		t.Fatalf("executed kinds=%v, want only held mouse_down", kinds)
	}
	if q.ctx.Err() != context.Canceled {
		t.Fatalf("source error=%v, want context.Canceled", q.ctx.Err())
	}
}

func boundaryInt(value int) *int { return &value }

func boundaryInput(kind string, sequence, barrier int) generated.BrowserInputFrame {
	return generated.BrowserInputFrame{Type: "browser_input", Kind: kind, InputEpoch: boundaryInt(1), ControlEpoch: boundaryInt(0), ReliableSeq: boundaryInt(sequence), GestureBarrier: boundaryInt(barrier)}
}

func awaitInputSignal(t *testing.T, signal <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out awaiting %s", operation)
	}
}
