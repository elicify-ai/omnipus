package browser

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

func TestLiveInputTabOperationWaitUsesCallerBudget(t *testing.T) {
	calls := 0
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls++; return nil })
	release, err := lv.mgr.acquireLiveTabCommand(context.Background(), lv.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = lv.dispatchInputContext(ctx, "viewer", LiveInput{Kind: "text", Text: "obsolete"})
	if !errors.Is(err, context.DeadlineExceeded) || calls != 0 {
		t.Fatalf("input bypassed in-flight tab operation: calls=%d err=%v", calls, err)
	}
}

func TestLiveInputRequiresCurrentCommittedCapture(t *testing.T) {
	for _, scenario := range []string{"missing identity", "wrong capture", "wrong generation", "transition pending", "capture not initialized", "stopped capture", "current committed"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls++; return nil })
			cs, _ := newRecoveryTestSession(t, &fakeRelay{})
			lv.mgr.capture = cs
			frame, err := cs.BeginFrameTransition("target-a", 800, 600, 1)
			if err != nil {
				t.Fatal(err)
			}
			if !cs.CommitFrameBoundary(frame.Generation, "target-a", 123) {
				t.Fatal("fixture boundary rejected")
			}
			in := LiveInput{Kind: "text", Text: "typed", CaptureID: frame.CaptureID, CaptureGeneration: frame.Generation}
			switch scenario {
			case "missing identity":
				in.CaptureID = ""
				in.CaptureGeneration = 0
			case "wrong capture":
				in.CaptureID = "another-capture"
			case "wrong generation":
				in.CaptureGeneration++
			case "transition pending":
				frame, err = cs.BeginFrameTransition("target-a", 900, 600, 1)
				if err != nil {
					t.Fatal(err)
				}
				in.CaptureGeneration = frame.Generation
			case "capture not initialized":
				uninitialized, _ := newRecoveryTestSession(t, &fakeRelay{})
				lv.mgr.capture = uninitialized
			case "stopped capture":
				cs.Stop()
			}
			err = lv.dispatchInput("viewer", in)
			if scenario == "current committed" {
				if err != nil || calls != 1 {
					t.Fatalf("current picture rejected: calls=%d err=%v", calls, err)
				}
			} else if err == nil || !IsBenignLiveInputError(err) || calls != 0 {
				t.Fatalf("unproven picture reached browser: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestLiveInputCaptureClaimIsCheckedAfterQueueWait(t *testing.T) {
	var calls atomic.Int32
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls.Add(1); return nil })
	cs, _ := newRecoveryTestSession(t, &fakeRelay{})
	lv.mgr.capture = cs
	frame, err := cs.BeginFrameTransition("target-a", 800, 600, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !cs.CommitFrameBoundary(frame.Generation, "target-a", 123) {
		t.Fatal("fixture boundary rejected")
	}
	lv.mu.Lock()
	gate := lv.inputStateLocked().gate
	lv.mu.Unlock()
	gate <- struct{}{}
	result := make(chan error, 1)
	go func() {
		result <- lv.dispatchInput("viewer", LiveInput{Kind: "text", Text: "obsolete", CaptureID: frame.CaptureID, CaptureGeneration: frame.Generation})
	}()
	waitFor(t, "input to enter queue", time.Second, func() bool { lv.mu.Lock(); defer lv.mu.Unlock(); return len(lv.inputState.requests) > 0 })
	if _, err := cs.BeginFrameTransition("target-b", 800, 600, 1); err != nil {
		<-gate
		t.Fatal(err)
	}
	<-gate
	select {
	case err := <-result:
		if err == nil || !IsBenignLiveInputError(err) || calls.Load() != 0 {
			t.Fatalf("queued stale claim accepted: calls=%d err=%v", calls.Load(), err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued input never completed")
	}
}

func TestLiveInputOwnedReleaseSurvivesCaptureTransition(t *testing.T) {
	var sequence []input.KeyType
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		for _, action := range actions {
			if key, ok := action.(*input.DispatchKeyEventParams); ok {
				sequence = append(sequence, key.Type)
			}
		}
		return nil
	})
	cs, _ := newRecoveryTestSession(t, &fakeRelay{})
	lv.mgr.capture = cs
	frame, err := cs.BeginFrameTransition("target-a", 800, 600, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !cs.CommitFrameBoundary(frame.Generation, "target-a", 123) {
		t.Fatal("fixture boundary rejected")
	}
	in := LiveInput{Kind: "key_down", Key: "Shift", Code: "ShiftLeft", KeyCode: 16, CaptureID: frame.CaptureID, CaptureGeneration: frame.Generation}
	if err := lv.dispatchInput("viewer", in); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.BeginFrameTransition("target-a", 900, 600, 1); err != nil {
		t.Fatal(err)
	}
	in.Kind = "key_up"
	if err := lv.dispatchInput("viewer", in); err != nil {
		t.Fatal(err)
	}
	if len(sequence) != 2 || sequence[0] != input.KeyRawDown || sequence[1] != input.KeyUp {
		t.Fatalf("transition stranded owned key: %v", sequence)
	}
}
