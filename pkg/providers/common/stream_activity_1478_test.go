package common

// Issue #1478: the chat must be able to say "the model hasn't responded for
// N seconds" while a provider is silent. The streaming silence monitor is the
// only place that sees EVERY byte (content, reasoning, keep-alive), so it
// reports activity to a ctx-carried observer: once when it starts (with the
// silence limit, so the notice can say when the call will be ended) and on
// every re-arm.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIssue1478_StallWatchReportsActivityOnStartAndEveryArm(t *testing.T) {
	var mu sync.Mutex
	var limits []time.Duration
	ctx := WithStreamActivity(context.Background(), func(stallLimit time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		limits = append(limits, stallLimit)
	})
	const limit = 7 * time.Minute // the model's stream_stall_timeout
	w := WatchStreamStall(ctx, func() {}, limit)
	defer w.Stop()
	w.Arm()
	w.Arm()
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []time.Duration{limit, limit, limit}, limits,
		"#1478: one report when the stream starts and one per consumed event, each carrying the silence limit")
}

func TestIssue1478_NoObserverAndDisabledWatchAreSilent(t *testing.T) {
	// No observer attached: Arm must keep working exactly as before.
	w := WatchStreamStall(context.Background(), func() {}, time.Minute)
	w.Arm()
	w.Stop()
	require.False(t, w.Fired())

	// A disabled watch (no limit) reports nothing: there is no silence limit
	// to describe and no monitor running.
	called := false
	ctx := WithStreamActivity(context.Background(), func(time.Duration) { called = true })
	off := WatchStreamStall(ctx, func() {}, 0)
	off.Arm()
	off.Stop()
	require.False(t, called)
}
