package webrtc

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func releaseBudgetFrame(kind string, seq, barrier int) generated.BrowserInputFrame {
	epoch, control, generation, modifiers := 1, 0, 7, 0
	x, y, width, height := 120.0, 240.0, 800.0, 600.0
	capture, button, code, key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "left", "ArrowLeft", "ArrowLeft"
	f := generated.BrowserInputFrame{Kind: kind, InputEpoch: &epoch, ControlEpoch: &control, ReliableSeq: &seq, GestureBarrier: &barrier, CaptureId: &capture, CaptureGeneration: &generation, CaptureWidth: &width, CaptureHeight: &height, Modifiers: &modifiers}
	switch kind {
	case "mouse_down", "mouse_up", "mouse_move":
		f.X, f.Y, f.Button = &x, &y, &button
	case "key_down", "key_up":
		f.Code, f.Key = &code, &key
	}
	return f
}

func TestDedicatedReleaseBudgetPreservesExactPairAfterOneSecond(t *testing.T) {
	for _, press := range []string{"mouse_down", "key_down"} {
		t.Run(press, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				up := "mouse_up"
				if press == "key_down" {
					up = "key_up"
				}
				finish := make(chan struct{})
				var got []string
				var failures []string
				q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
					if f.Kind == press {
						select {
						case <-finish:
						case <-ctx.Done():
						}
					}
					if ctx.Err() == nil {
						got = append(got, f.Kind)
					}
				}, func(reason string) { failures = append(failures, reason) })
				defer q.close()
				q.setActiveDispatchBudget(2 * time.Second)
				source := q.ctx
				q.submit(false, releaseBudgetFrame(press, 1, 1))
				synctest.Wait()
				time.Sleep(100 * time.Millisecond)
				q.submit(false, releaseBudgetFrame(up, 2, 2))
				time.Sleep(1400 * time.Millisecond)
				synctest.Wait()
				if source.Err() != nil || len(failures) != 0 {
					t.Errorf("sole matching release expired before active press deadline: %v %v", source.Err(), failures)
				}
				close(finish)
				synctest.Wait()
				if !reflect.DeepEqual(got, []string{press, up}) {
					t.Errorf("pair not delivered once in order: %v", got)
				}
				if q.ctx != source || source.Err() != nil {
					t.Error("valid pair retired its held-input source")
				}
			})
		})
	}
}

func TestDedicatedReleaseBudgetRetainsStrictMixedAndMismatchExpiry(t *testing.T) {
	for _, variant := range []string{"following text", "drag move", "repeat before up", "other held key", "wrong button", "wrong key", "capture", "generation", "geometry", "modifiers"} {
		t.Run(variant, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				press, up := "mouse_down", "mouse_up"
				if variant == "repeat before up" || variant == "wrong key" {
					press, up = "key_down", "key_up"
				}
				var failures []string
				q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
					if variant == "other held key" && *f.ReliableSeq == 1 {
						return
					}
					<-ctx.Done()
				}, func(reason string) { failures = append(failures, reason) })
				defer q.close()
				q.setActiveDispatchBudget(2 * time.Second)
				seq, barrier := 1, 1
				if variant == "other held key" {
					q.submit(false, releaseBudgetFrame("key_down", seq, barrier))
					synctest.Wait()
					seq++
					barrier++
				}
				q.submit(false, releaseBudgetFrame(press, seq, barrier))
				synctest.Wait()
				time.Sleep(100 * time.Millisecond)
				seq++
				barrier++
				if variant == "drag move" {
					q.submit(false, releaseBudgetFrame("mouse_move", seq, barrier-1))
					seq++
				}
				if variant == "repeat before up" {
					q.submit(false, releaseBudgetFrame(press, seq, barrier))
					seq++
					barrier++
				}
				release := releaseBudgetFrame(up, seq, barrier)
				switch variant {
				case "wrong button":
					*release.Button = "right"
				case "wrong key":
					*release.Code, *release.Key = "ArrowRight", "ArrowRight"
				case "capture":
					*release.CaptureId = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
				case "generation":
					*release.CaptureGeneration = 8
				case "geometry":
					*release.CaptureWidth = 900
				case "modifiers":
					*release.Modifiers = 1
				}
				q.submit(false, release)
				if variant == "following text" {
					q.submit(false, releaseBudgetFrame("text", seq+1, barrier))
				}
				time.Sleep(time.Second - time.Nanosecond)
				synctest.Wait()
				if q.ctx.Err() != nil {
					t.Error("mixed/mismatched work expired before original one-second queue boundary")
				}
				time.Sleep(time.Nanosecond)
				synctest.Wait()
				if !errors.Is(q.ctx.Err(), context.Canceled) || !reflect.DeepEqual(failures, []string{"reliable input queue expired"}) {
					t.Errorf("original strict expiry lost: %v %v", q.ctx.Err(), failures)
				}
			})
		})
	}
}

func TestDedicatedReleaseBudgetRejectsInvalidSequenceAndBarrier(t *testing.T) {
	for _, field := range []string{"sequence", "barrier"} {
		t.Run(field, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var failures []string
				q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, _ generated.BrowserInputFrame) { <-ctx.Done() }, func(reason string) { failures = append(failures, reason) })
				defer q.close()
				q.setActiveDispatchBudget(2 * time.Second)
				q.submit(false, releaseBudgetFrame("mouse_down", 1, 1))
				synctest.Wait()
				release := releaseBudgetFrame("mouse_up", 2, 2)
				want := "invalid reliable sequence"
				if field == "sequence" {
					*release.ReliableSeq = 3
				} else {
					*release.GestureBarrier = 1
					want = "invalid gesture barrier"
				}
				q.submit(false, release)
				synctest.Wait()
				if !reflect.DeepEqual(failures, []string{want}) || q.ctx.Err() == nil {
					t.Errorf("invalid release obtained budget exception: %v %v", failures, q.ctx.Err())
				}
			})
		})
	}
}

func TestDedicatedReleaseBudgetLateArrivalCannotResetPressDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		finish := make(chan struct{})
		var failures []string
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(context.Context, generated.BrowserInputFrame) { <-finish }, func(reason string) { failures = append(failures, reason) })
		defer q.close()
		q.setActiveDispatchBudget(2 * time.Second)
		q.submit(false, releaseBudgetFrame("mouse_down", 1, 1))
		synctest.Wait()
		time.Sleep(1200 * time.Millisecond)
		q.submit(false, releaseBudgetFrame("mouse_up", 2, 2))
		time.Sleep(800*time.Millisecond - time.Nanosecond)
		synctest.Wait()
		if q.ctx.Err() != nil {
			t.Error("press expired before original two-second boundary")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if q.ctx.Err() == nil || !reflect.DeepEqual(failures, []string{"reliable input queue expired"}) {
			t.Errorf("late release extended noncooperating press: %v %v", q.ctx.Err(), failures)
		}
		close(finish)
		synctest.Wait()
	})
}

func TestDedicatedReleaseBudgetBoundsNoncooperatingRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		finishPress, finishRelease := make(chan struct{}), make(chan struct{})
		var releaseCtx context.Context
		var failures []string
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
			if f.Kind == "mouse_down" {
				<-finishPress
			} else {
				releaseCtx = ctx
				<-finishRelease
			}
		}, func(reason string) { failures = append(failures, reason) })
		defer q.close()
		q.setActiveDispatchBudget(2 * time.Second)
		q.submit(false, releaseBudgetFrame("mouse_down", 1, 1))
		synctest.Wait()
		q.submit(false, releaseBudgetFrame("mouse_up", 2, 2))
		time.Sleep(1500 * time.Millisecond)
		close(finishPress)
		synctest.Wait()
		if releaseCtx == nil {
			t.Error("matching release did not reach its own execution budget")
		} else {
			time.Sleep(2*time.Second - time.Nanosecond)
			synctest.Wait()
			if releaseCtx.Err() != nil {
				t.Error("release execution budget was shortened")
			}
			time.Sleep(time.Nanosecond)
			synctest.Wait()
			if releaseCtx.Err() == nil {
				t.Error("noncooperating release exceeded its own two-second execution budget")
			}
		}
		close(finishRelease)
		synctest.Wait()
		if !reflect.DeepEqual(failures, []string{"reliable input queue expired"}) {
			t.Errorf("unexpected terminal failures: %v", failures)
		}
	})
}

func TestDedicatedReleaseBudgetRetirementClearsContinuation(t *testing.T) {
	for _, mode := range []string{"pause", "close"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var got []string
				q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) { got = append(got, f.Kind); <-ctx.Done() }, func(reason string) { t.Errorf("unexpected failure: %s", reason) })
				defer q.close()
				q.setActiveDispatchBudget(2 * time.Second)
				source, done := q.ctx, q.done
				q.submit(false, releaseBudgetFrame("key_down", 1, 1))
				synctest.Wait()
				q.submit(false, releaseBudgetFrame("key_up", 2, 2))
				if mode == "close" {
					q.close()
				} else {
					retired, wait, err := q.pause(1)
					if err != nil || retired != source || wait != done {
						t.Errorf("retirement identity changed: %v", err)
					}
				}
				synctest.Wait()
				if source.Err() == nil || !reflect.DeepEqual(got, []string{"key_down"}) {
					t.Errorf("retired continuation executed: %v %v", source.Err(), got)
				}
				select {
				case <-done:
				default:
					t.Error("old dispatch did not join")
				}
				if mode == "pause" {
					if err := q.resume(1); err != nil {
						t.Error(err)
					}
					if q.ctx == source || q.ctx.Err() != nil {
						t.Error("replacement source is not fresh")
					}
				}
			})
		})
	}
}

func TestDedicatedReleaseBudgetPhysicalModifierIdentity(t *testing.T) {
	for _, tc := range []struct {
		code          string
		before, after int
		want          bool
	}{
		{"ShiftLeft", 8, 0, true}, {"AltRight", 1, 0, true},
		{"ControlLeft", 2, 0, true}, {"MetaRight", 4, 0, true},
		{"ShiftLeft", 10, 0, false}, {"KeyA", 0, 8, false},
	} {
		t.Run(tc.code, func(t *testing.T) {
			press, release := releaseBudgetFrame("key_down", 1, 1), releaseBudgetFrame("key_up", 2, 2)
			*press.Code, *release.Code = tc.code, tc.code
			*press.Modifiers, *release.Modifiers = tc.before, tc.after
			// Logical key labels can change; the physical key is authoritative.
			*press.Key, *release.Key = "pressed-label", "released-label"
			if got := matchingPressRelease(press, release); got != tc.want {
				t.Errorf("matching physical release=%v want%v", got, tc.want)
			}
		})
	}
}

func TestDedicatedReleaseBudgetPrintablePressTextPreserved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		finish := make(chan struct{})
		var got []generated.BrowserInputFrame
		q := newDedicatedInputQueue(context.Background(), 1, 0, func(ctx context.Context, f generated.BrowserInputFrame) {
			if f.Kind == "key_down" {
				select {
				case <-finish:
				case <-ctx.Done():
				}
			}
			if ctx.Err() == nil {
				got = append(got, f)
			}
		}, func(reason string) { t.Errorf("unexpected failure: %s", reason) })
		defer q.close()
		q.setActiveDispatchBudget(2 * time.Second)
		press, release := releaseBudgetFrame("key_down", 1, 1), releaseBudgetFrame("key_up", 2, 2)
		*press.Code, *release.Code = "KeyA", "KeyA"
		*press.Key, *release.Key = "a", "a"
		text := "a"
		press.Text = &text
		q.submit(false, press)
		synctest.Wait()
		q.submit(false, release)
		time.Sleep(1500 * time.Millisecond)
		close(finish)
		synctest.Wait()
		if len(got) != 2 {
			t.Fatalf("printable pair dispatched %d times; want press and release", len(got))
		}
		if got[0].Kind != "key_down" || got[0].Text == nil || *got[0].Text != "a" || got[1].Kind != "key_up" || got[1].Text != nil {
			t.Fatalf("printable press or text-free release was rewritten: %+v", got)
		}
	})
}

func TestDedicatedReleaseBudgetRejectsTextOnRelease(t *testing.T) {
	press, release := releaseBudgetFrame("key_down", 1, 1), releaseBudgetFrame("key_up", 2, 2)
	*press.Code, *release.Code = "KeyA", "KeyA"
	text := "a"
	press.Text, release.Text = &text, &text
	if matchingPressRelease(press, release) {
		t.Error("release carrying unexpected text obtained continuation allowance")
	}
}
