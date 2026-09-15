package browser

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFirstAttachContextCancellationDoesNotWaitForLateWorker(t *testing.T) {
	caller, cancel := context.WithCancel(context.Background())
	entered, release, drained := make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runFirstAttachContext(caller, func() error { defer close(drained); close(entered); <-release; return nil }, time.Second)
	}()
	<-entered
	cancel()
	var err error
	prompt := false
	select {
	case err = <-done:
		prompt = true
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	<-drained
	if !prompt {
		err = <-done
	}
	if !prompt || !errors.Is(err, context.Canceled) {
		t.Errorf("first attach cancellation prompt=%t err=%v", prompt, err)
	}
}

func TestFirstAttachContextPreCanceledDoesNotDispatch(t *testing.T) {
	caller, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := runFirstAttachContext(caller, func() error { called = true; return nil }, time.Second)
	if called || !errors.Is(err, context.Canceled) {
		t.Errorf("pre-canceled attach called=%t err=%v", called, err)
	}
}
