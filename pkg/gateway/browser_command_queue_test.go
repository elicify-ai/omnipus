package gateway

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// FR-001/011: coalescing may discard obsolete motion, never discrete actions.
func TestBrowserCommandQueuePreservesDiscreteOrder(t *testing.T) {
	var q browserCommandQueue
	var wg sync.WaitGroup
	entered, release := make(chan struct{}), make(chan struct{})
	require.True(t, q.submit(&wg, browserCommand{run: func(context.Context) { close(entered); <-release }}))
	<-entered
	var got []string
	add := func(s string, move bool) {
		require.True(t, q.submit(&wg, browserCommand{move: move, run: func(context.Context) { got = append(got, s) }}))
	}
	add("old-move", true)
	add("latest-move", true)
	add("down", false)
	add("drag", true)
	add("up", false)
	add("text", false)
	close(release)
	wg.Wait()
	require.Equal(t, []string{"latest-move", "down", "drag", "up", "text"}, got)
}

func TestBrowserCommandQueueBoundsAndCancellation(t *testing.T) {
	var q browserCommandQueue
	var wg sync.WaitGroup
	entered := make(chan struct{})
	require.True(t, q.submit(&wg, browserCommand{run: func(ctx context.Context) { close(entered); <-ctx.Done() }}))
	<-entered
	for range browserCommandCapacity {
		require.True(t, q.submit(&wg, browserCommand{run: func(context.Context) { t.Error("closed queue executed stale input") }}))
	}
	require.False(t, q.submit(&wg, browserCommand{run: func(context.Context) { t.Error("overflow input executed") }}))
	q.close()
	wg.Wait()
	require.False(t, q.submit(&wg, browserCommand{}))
}

func TestBrowserCommandQueueNewNavigationCancelsActiveNavigation(t *testing.T) {
	var q browserCommandQueue
	var wg sync.WaitGroup
	entered := make(chan struct{})
	var oldErr error
	require.True(t, q.submit(&wg, browserCommand{navigation: true, run: func(ctx context.Context) { close(entered); <-ctx.Done(); oldErr = ctx.Err() }}))
	<-entered
	var next bool
	require.True(t, q.submit(&wg, browserCommand{navigation: true, run: func(context.Context) { next = true }}))
	wg.Wait()
	require.Equal(t, context.Canceled, oldErr)
	require.True(t, next)
}
