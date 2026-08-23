// live_setviewport_retry_test.go — SetViewport's single deadline-only retry
// (2026-08-13 UAT: transient "could not resize the browser viewport" toast
// when the browser process was momentarily starved under encode+input load;
// a GetWindowForTarget that cannot answer inside viewportSetTimeout is a
// stall, not an invalid resize).

package browser

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestSetViewport_RetriesOnceOnDeadlineTimeout(t *testing.T) {
	var applyAttempts int
	var calls int
	runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		calls++
		if _, isApply := actions[0].(windowBoundsAction); isApply {
			applyAttempts++
			if applyAttempts == 1 {
				return fmt.Errorf("get window for target: %w", context.DeadlineExceeded)
			}
			return nil
		}
		// The device-scale override is now its own call (see
		// viewportScaleTimeout), so the sequence is bounds -> scale ->
		// read-back and this stub must not assume every non-bounds call is a
		// read-back.
		if lm, ok := actions[0].(layoutMetricsAction); ok {
			*lm.w, *lm.h = 615, 744
		}
		return nil
	}
	reg, _ := newViewportTestLiveView(runCDP)

	applied, err := reg.SetViewport("s1", 615, 744, 1)
	require.NoError(t, err, "SetViewport must succeed when the single retry succeeds")
	require.True(t, applied)
	require.Equal(t, 2, applyAttempts, "exactly one retry after the timeout, never a loop")
}

func TestSetViewport_NoRetryOnNonDeadlineError(t *testing.T) {
	realErr := fmt.Errorf("target closed")
	var applyAttempts int
	runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		if _, isApply := actions[0].(windowBoundsAction); isApply {
			applyAttempts++
			return realErr
		}
		t.Fatal("read-back must not run after a failed apply")
		return nil
	}
	reg, _ := newViewportTestLiveView(runCDP)

	_, err := reg.SetViewport("s1", 615, 744, 1)
	require.ErrorContains(t, err, "target closed", "a non-deadline failure must surface immediately")
	require.Equal(t, 1, applyAttempts, "non-deadline errors must NOT be retried")
}

func TestSetViewport_SecondDeadlineStillFailsLoudly(t *testing.T) {
	var applyAttempts int
	runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		if _, isApply := actions[0].(windowBoundsAction); isApply {
			applyAttempts++
			return context.DeadlineExceeded
		}
		t.Fatal("read-back must not run after a failed apply")
		return nil
	}
	reg, _ := newViewportTestLiveView(runCDP)

	_, err := reg.SetViewport("s1", 615, 744, 1)
	require.ErrorIs(t, err, context.DeadlineExceeded, "two consecutive timeouts must fail loudly")
	require.ErrorContains(t, err, "after retry")
	require.Equal(t, 2, applyAttempts, "exactly two attempts, never a retry loop")
}
