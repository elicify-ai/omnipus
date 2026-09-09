package browser

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
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

// Hide only deadline discovery so the real caller timer expires before the
// target-derived operation timer. Its Done and Err remain the standard context
// implementation. This isolates the cancellation relay that races the two
// nearly-equal timers in TestLiveInputTabOperationWaitUsesCallerBudget.
type inputCallerWithoutDeadline struct{ context.Context }

func (inputCallerWithoutDeadline) Deadline() (time.Time, bool) { return time.Time{}, false }

func TestLiveInputCallerCancellationIdentitySurvivesRelay(t *testing.T) {
	for _, gateKind := range []string{"tab", "input"} {
		for _, deadline := range []bool{false, true} {
			mode := "manual"
			if deadline {
				mode = "deadline"
			}
			t.Run(gateKind+"/"+mode, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					calls := 0
					lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls++; return nil })
					if gateKind == "tab" {
						release, err := lv.mgr.acquireLiveTabCommand(context.Background(), lv.sessionID)
						if err != nil {
							t.Fatal(err)
						}
						defer release()
					} else {
						lv.mu.Lock()
						gate := lv.inputStateLocked().gate
						lv.mu.Unlock()
						gate <- struct{}{}
						defer func() { <-gate }()
					}
					caller, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
					defer cancel()
					expected := context.DeadlineExceeded
					if !deadline {
						expected = context.Canceled
						go func() { time.Sleep(10 * time.Millisecond); cancel() }()
					}
					err := lv.dispatchInputContext(inputCallerWithoutDeadline{caller}, "viewer", LiveInput{Kind: "text", Text: "obsolete"})
					if !errors.Is(err, expected) || calls != 0 {
						t.Fatalf("caller %s lost at %s gate: calls=%d err=%v, want %v", mode, gateKind, calls, err, expected)
					}
					if deadline && errors.Is(err, context.Canceled) {
						t.Fatalf("deadline was additionally classified as manual cancellation: %v", err)
					}
				})
			})
		}
	}
}

func TestLiveInputCallerCancellationPreservesUnrelatedDispatchError(t *testing.T) {
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	fault := errors.New("controlled browser transport failure")
	calls := 0
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		calls++
		cancel()
		return fault
	})
	picture := installInputTestPicture(t, lv)
	err := lv.dispatchInputContext(caller, "viewer", inputWithTestPicture(picture, LiveInput{Kind: "text", Text: "one input"}))
	if !errors.Is(err, fault) || errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("caller cancellation masked unrelated dispatch failure: calls=%d err=%v", calls, err)
	}
}

func TestLiveInputRequiresCurrentCommittedCapture(t *testing.T) {
	for _, scenario := range []string{"missing identity", "wrong capture", "wrong generation", "transition pending", "capture not initialized", "stopped capture", "current committed"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls++; return nil })
			cs, _ := newRecoveryTestSession(t, &fakeRelay{})
			lv.mgr.captures = map[string]*CaptureSession{lv.sessionID: cs}
			lv.mgr.sessions[lv.sessionID] = &sessionEntry{tabs: []*tabEntry{{ctx: lv.tabCtx, targetID: "target-a"}}}
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
				lv.mgr.captures = map[string]*CaptureSession{lv.sessionID: uninitialized}
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
	lv.mgr.captures = map[string]*CaptureSession{lv.sessionID: cs}
	lv.mgr.sessions[lv.sessionID] = &sessionEntry{tabs: []*tabEntry{{ctx: lv.tabCtx, targetID: "target-a"}}}
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
	lv.mgr.captures = map[string]*CaptureSession{lv.sessionID: cs}
	lv.mgr.sessions[lv.sessionID] = &sessionEntry{tabs: []*tabEntry{{ctx: lv.tabCtx, targetID: "target-a"}}}
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
