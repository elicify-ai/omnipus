package browser

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCaptureStartWaiterCancellationPreservesOwner(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var calls atomic.Int32
	var target context.Context
	owner, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	cs := newTestCaptureSession(t, &fakeRelay{}, func(ctx context.Context, _ *BrowserManager, _, _, _, _ string) (context.Context, context.CancelFunc, error) {
		calls.Add(1)
		return runEncoderStartup(ctx, context.Background(), func(parent context.Context) (*tabEntry, error) {
			var cancel context.CancelFunc
			target, cancel = context.WithCancel(parent)
			return &tabEntry{ctx: target, cancel: cancel}, nil
		}, func(runCtx context.Context) error {
			close(entered)
			select {
			case <-release:
			case <-runCtx.Done():
			}
			return runCtx.Err()
		})
	})
	t.Cleanup(cs.Stop)
	ownerDone := make(chan error, 1)
	go func() { _, err := cs.Start(owner, "test-ingest"); ownerDone <- err }()
	<-entered
	waiter, cancelWaiter := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() { _, err := cs.Start(waiter, "test-ingest"); waiterDone <- err }()
	cancelWaiter()
	var waiterErr error
	prompt := false
	select {
	case waiterErr = <-waiterDone:
		prompt = true
	case <-time.After(200 * time.Millisecond):
	}
	if owner.Err() != nil || calls.Load() != 1 {
		t.Error("waiter cancellation changed owner lifetime or started another encoder")
	}
	unblock()
	if err := <-ownerDone; err != nil {
		t.Fatalf("original startup failed: %v", err)
	}
	if !prompt {
		waiterErr = <-waiterDone
	}
	if !prompt || !errors.Is(waiterErr, context.Canceled) {
		t.Errorf("canceled waiter prompt=%t error=%v; want prompt context.Canceled", prompt, waiterErr)
	}
	cancelOwner()
	if target.Err() != nil || calls.Load() != 1 {
		t.Error("successful persistent target did not survive original caller completion")
	}
	started, err := cs.Start(waiter, "test-ingest")
	if started || !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Errorf("already-canceled cached start=(%t,%v), calls=%d", started, err, calls.Load())
	}
}

func TestCaptureStopCancelsWholeEncoderStartup(t *testing.T) {
	for _, stage := range []string{"create", "navigate"} {
		t.Run(stage, func(t *testing.T) {
			caller, cancelCaller := context.WithCancel(context.Background())
			defer cancelCaller()
			entered, nativeDone := make(chan struct{}), make(chan struct{})
			var target context.Context
			cs := newTestCaptureSession(t, &fakeRelay{}, func(ctx context.Context, _ *BrowserManager, _, _, _, _ string) (context.Context, context.CancelFunc, error) {
				defer close(nativeDone)
				return runEncoderStartup(ctx, context.Background(), func(parent context.Context) (*tabEntry, error) {
					var cancel context.CancelFunc
					target, cancel = context.WithCancel(parent)
					if stage == "create" {
						close(entered)
						<-parent.Done()
					}
					return &tabEntry{ctx: target, cancel: cancel}, nil
				}, func(ctx context.Context) error {
					close(entered)
					<-ctx.Done()
					return ctx.Err()
				})
			})
			t.Cleanup(cs.Stop)
			done := make(chan error, 1)
			go func() { _, err := cs.Start(caller, "test-ingest"); done <- err }()
			<-entered
			cs.Stop()
			prompt := false
			select {
			case <-nativeDone:
				prompt = true
			case <-time.After(200 * time.Millisecond):
			}
			if caller.Err() != nil {
				t.Error("capture Stop canceled the independent caller")
			}
			cancelCaller() // Drain the unfixed implementation after recording its failure.
			err := <-done
			if !prompt || !errors.Is(err, context.Canceled) || target.Err() != context.Canceled {
				t.Errorf("Stop during %s: native exit=%t error=%v target=%v", stage, prompt, err, target.Err())
			}
		})
	}
}
