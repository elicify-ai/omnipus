// License: MIT
// Copyright (c) 2026 Omnipus contributors

package runnertest_test

import (
	"errors"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/agent/runner/runnertest"
)

func TestInjectEvent_AfterCancelDropsTheEventAndReturnsAnError(t *testing.T) {
	fr := runnertest.NewFakeRunner()
	if err := fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd}); err != nil {
		t.Fatalf("instrument check: InjectEvent before Cancel = %v, want nil", err)
	}
	fr.Cancel()
	err := fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd}) // must not panic
	if !errors.Is(err, runnertest.ErrFakeRunnerCancelled) {
		t.Fatalf("InjectEvent after Cancel = %v, want ErrFakeRunnerCancelled", err)
	}
}

// Cancel must release an InjectEvent that is blocked on a full buffer rather
// than deadlock or panic.
func TestInjectEvent_BlockedOnAFullBufferIsReleasedByCancel(t *testing.T) {
	fr := runnertest.NewFakeRunner()
	for i := 0; i < 64; i++ { // fill the buffer
		if err := fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput}); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	got := make(chan error, 1)
	go func() { got <- fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd}) }()
	time.Sleep(50 * time.Millisecond) // let the sender block on the full buffer
	fr.Cancel()
	select {
	case err := <-got:
		if !errors.Is(err, runnertest.ErrFakeRunnerCancelled) {
			t.Fatalf("blocked InjectEvent = %v, want ErrFakeRunnerCancelled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Cancel did not release the blocked InjectEvent")
	}
}
