package gateway

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestDedicatedControlSupersessionKeepsPeerUsable(t *testing.T) {
	d := &browserDedicatedInput{epoch: 1, control: 1}
	var queue browserCommandQueue
	var workers sync.WaitGroup
	entered, finished := make(chan struct{}), make(chan struct{})
	failures := make(chan string, 1)
	fail := func(reason string) { failures <- reason }
	defer func() { queue.close(); workers.Wait() }()
	if !queue.submit(&workers, browserCommand{navigation: true, run: func(ctx context.Context) {
		close(entered)
		<-ctx.Done()
		d.failControlUnlessSuperseded(ctx, context.Background(), 1, 1, "older navigation canceled", fail)
	}}) {
		t.Fatal("first navigation rejected")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first navigation did not start")
	}
	// Production admission reserves the newer epoch before submitting its job.
	d.mu.Lock()
	d.control = 2
	d.mu.Unlock()
	if !queue.submit(&workers, browserCommand{navigation: true, run: func(context.Context) { close(finished) }}) {
		t.Fatal("replacement navigation rejected")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("replacement navigation did not execute")
	}
	select {
	case reason := <-failures:
		t.Fatalf("superseded navigation poisoned input: %s", reason)
	default:
	}
}

func TestDedicatedControlSupersessionDoesNotHideFailures(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	for _, tc := range []struct {
		name                  string
		operation, attachment context.Context
		epoch, latest         int
	}{
		{"latest cancellation", canceled, context.Background(), 1, 1},
		{"older deadline", expired, context.Background(), 1, 2},
		{"older real refusal", context.Background(), context.Background(), 1, 2},
		{"retired attachment", canceled, canceled, 1, 2},
		{"different peer", canceled, context.Background(), 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &browserDedicatedInput{epoch: tc.epoch, control: tc.latest}
			got := ""
			d.failControlUnlessSuperseded(tc.operation, tc.attachment, 1, 1, "must remain visible", func(reason string) { got = reason })
			if got != "must remain visible" {
				t.Fatal("failure was silently ignored")
			}
		})
	}
}
