package agent

// Round 5 explicit product decisions R3-7 and R3-8: these two RED tests
// extend the no-false-retry promise to administrative wrap-up and to Stop
// landing between the retry checks and the retry announcement. Adapted from
// CHECK's P6/P6c and P8, with the real response stage and synchronous event tap.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIssue1664_R5_CancelledBeforeWrapUpAnnouncesNoRetry(t *testing.T) {
	issue1478ShortSilence(t)
	provider := issue1664R5TimeoutProvider()
	h, rr := issue1664R5Response(t, provider, true)
	require.True(t, h.ts.claimCancel(false), "SETUP: administrative cancel claimed the turn")
	require.True(t, h.ts.requestGracefulInterrupt(""), "SETUP: graceful interruption accepted")
	rr.callLLMWithRetries()
	require.Error(t, rr.rq.ri.rf.err)
	require.Equal(t, 1, issue1664CallCount(provider), "no second call after the claimed cancellation")
	require.Empty(t, issue1664RetryEvents(h), "P6c control: no retry before the wrap-up request is built")
}

func TestIssue1664_R5_RED_CancelledBuiltWrapUpAnnouncesNoRetry(t *testing.T) {
	issue1478ShortSilence(t)
	provider := issue1664R5TimeoutProvider()
	h, rr := issue1664R5Response(t, provider, true)
	require.True(t, h.ts.claimCancel(false), "SETUP: administrative cancel claimed the turn")
	require.True(t, h.ts.requestGracefulInterrupt(""), "SETUP: graceful interruption accepted")
	h.ts.markGracefulTerminalUsed() // the real wrap-up request-build transition
	rr.callLLMWithRetries()
	require.Error(t, rr.rq.ri.rf.err, "the canceled wrap-up round must not return success")
	require.Equal(t, 1, issue1664CallCount(provider), "no second provider call after administrative cancel")
	require.Empty(t, issue1664RetryEvents(h), "R3-7: a timed-out built wrap-up must not announce a retry that will not happen")
}

func TestIssue1664_R5_RED_StopBetweenRetryCheckAndFrameAnnouncesNoRetry(t *testing.T) {
	issue1478ShortSilence(t)
	provider := issue1664R5TimeoutProvider()
	h, rr := issue1664R5Response(t, provider, true)
	// Timeout is emitted after the retry checks and before the retry frame.
	// Delivering Stop here deterministically probes that exact interval.
	fired := issue1664R5StopAt(h, EventKindTurnTimeout)
	rr.callLLMWithRetries()
	require.EqualValues(t, 1, fired.Load(), "SETUP: Stop delivered inside the timeout event, between check and frame")
	require.Error(t, rr.rq.ri.rf.err, "the stopped round must not return success")
	t.Run("no later provider call", func(t *testing.T) {
		require.Equal(t, 1, issue1664CallCount(provider), "no second provider call after Stop")
	})
	t.Run("no retry frame after Stop", func(t *testing.T) {
		require.Empty(t, issue1664RetryEvents(h), "R3-8: no retry frame is announced after Stop lands between check and frame")
	})
	t.Run("ends canceled", func(t *testing.T) {
		require.ErrorIs(t, rr.rq.ri.rf.err, context.Canceled)
	})
}
