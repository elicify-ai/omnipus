// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 6a gate fix round (code W3): each cron-engine Stop may wait up to its 30 s
// lane drain. Run one after another, three engines could take 90 s against the
// 70 s shutdown budget. The scheduler stops run side by side, so all three
// are in progress at once, and the wait for them ends at the budget.
func TestStopTurnSchedulers_RunsStopsSideBySide(t *testing.T) {
	entered := make(chan int, 3)
	release := make(chan struct{})
	stop := func(i int) func() {
		return func() {
			entered <- i
			<-release
		}
	}
	done := make(chan bool, 1)
	go func() { done <- stopTurnSchedulers(context.Background(), stop(1), stop(2), stop(3)) }()
	seen := map[int]bool{}
	for len(seen) < 3 {
		select {
		case i := <-entered:
			seen[i] = true
		case <-time.After(10 * time.Second):
			// A sequential implementation blocks inside stop 1 forever.
			close(release)
			t.Fatalf("only %d of 3 scheduler stops started while the first was still waiting: stops run one after another", len(seen))
		}
	}
	select {
	case <-done:
		t.Fatal("returned before the stops finished")
	default:
	}
	close(release)
	require.True(t, <-done, "all stops finished inside the budget")
}

func TestStopTurnSchedulers_EndsAtTheBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	entered := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		done <- stopTurnSchedulers(ctx, func() {}, func() { close(entered); <-stuck })
	}()
	<-entered
	cancel() // the shutdown budget is spent
	select {
	case finished := <-done:
		require.False(t, finished, "a stop still running at the budget is reported, not waited for")
	case <-time.After(10 * time.Second):
		t.Fatal("the scheduler-stop wait did not end at the shutdown budget")
	}
}

func TestStopTurnSchedulers_SkipsNilStops(t *testing.T) {
	require.True(t, stopTurnSchedulers(context.Background(), nil, func() {}, nil))
}

// The active-turn wait uses what is left of the budget, minus the 5 s margin
// for the loop close, and never more than the documented 65 s.
func TestShutdownActiveTurnWait_FitsTheRemainingBudget(t *testing.T) {
	for _, tc := range []struct{ remaining, want time.Duration }{
		{70 * time.Second, 65 * time.Second},
		{90 * time.Second, 65 * time.Second},
		{40 * time.Second, 35 * time.Second},
		{5 * time.Second, 0},
		{3 * time.Second, 0},
		{-time.Second, 0},
	} {
		require.Equal(t, tc.want, shutdownActiveTurnWait(tc.remaining), "remaining=%s", tc.remaining)
	}
}
