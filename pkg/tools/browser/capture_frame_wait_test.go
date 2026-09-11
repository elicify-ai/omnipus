package browser

import (
	"context"
	"errors"
	"testing"
	"time"
)

type confirmedFrameResult struct {
	frame CaptureFrameState
	err   error
}

func TestWaitConfirmedFrameInitialFollowsPendingTransitions(t *testing.T) {
	cs, _ := newRecoveryTestSession(t, &fakeRelay{})
	events := make(chan CaptureFrameState, 4)
	cs.SetOnFrameState(func(f CaptureFrameState) { events <- f })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan confirmedFrameResult, 1)
	go func() { f, e := cs.WaitConfirmedFrame(ctx, "", 0); done <- confirmedFrameResult{f, e} }()
	for _, target := range []string{"pending-a", "pending-b"} {
		if _, err := cs.BeginFrameTransition(target, 0, 0, 1); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-done:
			t.Fatalf("unconfirmed initial frame returned: %+v", got)
		case <-time.After(20 * time.Millisecond):
		}
	}
	measured, err := cs.BeginFrameTransition("pending-b", 641, 479, 1.25)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.frame != measured || got.frame.Ready {
			t.Fatalf("initial measured frame=%+v want=%+v", got, measured)
		}
	case <-time.After(time.Second):
		t.Fatal("measured geometry did not wake initial waiter")
	}
	if len(events) != 3 {
		t.Fatalf("existing observer lost transitions: %d", len(events))
	}
}

func TestWaitConfirmedFrameExactMeasuredDoesNotRequireDecodedReady(t *testing.T) {
	cs, _ := newRecoveryTestSession(t, &fakeRelay{})
	expected, err := cs.BeginFrameTransition("page", 800, 600, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := cs.WaitConfirmedFrame(ctx, expected.CaptureID, expected.Generation)
	if err != nil || got != expected || got.Ready {
		t.Fatalf("measured non-presented frame=%+v err=%v", got, err)
	}
}

func TestWaitConfirmedFrameExactPendingCannotRetag(t *testing.T) {
	cs, _ := newRecoveryTestSession(t, &fakeRelay{})
	pending, err := cs.BeginFrameTransition("page", 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan confirmedFrameResult, 1)
	go func() {
		f, e := cs.WaitConfirmedFrame(ctx, pending.CaptureID, pending.Generation)
		done <- confirmedFrameResult{f, e}
	}()
	select {
	case got := <-done:
		t.Fatalf("pending exact frame returned: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}
	if _, err := cs.BeginFrameTransition("page", 800, 600, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if !errors.Is(got.err, ErrStaleCaptureFrame) || got.frame != (CaptureFrameState{}) {
			t.Fatalf("superseded claim retagged: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("superseded exact waiter did not finish")
	}
}

func TestWaitConfirmedFrameRejectsInvalidAndStaleClaims(t *testing.T) {
	cs, _ := newRecoveryTestSession(t, &fakeRelay{})
	frame, err := cs.BeginFrameTransition("page", 800, 600, 1)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		ctx  context.Context
		id   string
		gen  uint64
		want error
	}{
		{"nil context", nil, "", 0, nil},
		{"ID without generation", context.Background(), frame.CaptureID, 0, nil},
		{"generation without ID", context.Background(), "", 1, nil},
		{"different capture", context.Background(), "other", 1, ErrStaleCaptureFrame},
		{"different generation", context.Background(), frame.CaptureID, 2, ErrStaleCaptureFrame},
		{"already canceled", canceled, frame.CaptureID, 1, context.Canceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cs.WaitConfirmedFrame(tc.ctx, tc.id, tc.gen)
			if err == nil || got != (CaptureFrameState{}) || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("invalid claim frame=%+v err=%v want=%v", got, err, tc.want)
			}
		})
	}
}

func TestWaitConfirmedFrameCancellationAndStop(t *testing.T) {
	for _, stop := range []bool{false, true} {
		name := "caller cancellation"
		if stop {
			name = "capture stop"
		}
		t.Run(name, func(t *testing.T) {
			cs, _ := newRecoveryTestSession(t, &fakeRelay{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan confirmedFrameResult, 1)
			go func() { f, e := cs.WaitConfirmedFrame(ctx, "", 0); done <- confirmedFrameResult{f, e} }()
			select {
			case got := <-done:
				t.Fatalf("unmeasured wait returned: %+v", got)
			case <-time.After(20 * time.Millisecond):
			}
			if stop {
				cs.Stop()
			} else {
				cancel()
			}
			select {
			case got := <-done:
				if got.err == nil || got.frame != (CaptureFrameState{}) || !stop && !errors.Is(got.err, context.Canceled) {
					t.Fatalf("ended wait=%+v", got)
				}
			case <-time.After(time.Second):
				t.Fatal("ended lifetime did not stop geometry wait")
			}
		})
	}
}

func TestWaitConfirmedFrameStoppedMeasuredCaptureCannotAuthorize(t *testing.T) {
	cs, _ := newRecoveryTestSession(t, &fakeRelay{})
	frame, err := cs.BeginFrameTransition("page", 800, 600, 1)
	if err != nil {
		t.Fatal(err)
	}
	cs.Stop()
	got, err := cs.WaitConfirmedFrame(context.Background(), frame.CaptureID, frame.Generation)
	if err == nil || got != (CaptureFrameState{}) {
		t.Fatalf("stopped measured capture authorized: %+v err=%v", got, err)
	}
}
