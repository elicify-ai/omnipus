package agent

// #1664 Round 5: adopt/equivalently strengthen CHECK's held-out P1u, P2,
// P3, P4u, P5u, P6/P6c, P8, P9a/P9b and P10. Oracles are verify-report B3,
// B5, B7, founder Q1/Q2/Q4, the documented steering/lazy-wake contract, and
// team-lead's explicit decision to fix R3-7/R3-8 in this landing. The latter
// two tests must be RED before their production fixes; no observed output
// determines an expected value. This author pack is not independent CHECK.

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func TestIssue1664_R5_UserStopDuringCallAnnouncesNoRetry(t *testing.T) {
	issue1478ShortSilence(t)
	var h *wu22Harness
	var claimed, interrupted atomic.Bool
	provider := &issue1478Provider{script: func(_ context.Context, call int) (*providers.LLMResponse, error) {
		if call == 1 {
			claimed.Store(h.ts.claimCancel(true))
			interrupted.Store(h.ts.requestGracefulInterrupt(""))
		}
		return nil, context.DeadlineExceeded
	}}
	var rr *agentLoopRunTurnResponse
	h, rr = issue1664R5Response(t, provider, true)
	rr.callLLMWithRetries()
	require.True(t, claimed.Load(), "SETUP: the user Stop claimed the turn")
	require.True(t, interrupted.Load(), "SETUP: the graceful interrupt was accepted")
	require.Error(t, rr.rq.ri.rf.err, "the stopped round must not return success")
	require.Equal(t, 1, issue1664CallCount(provider), "no second provider call after a Stop")
	require.Empty(t, issue1664RetryEvents(h), "R3-1: no retry frame after a Stop")
	require.Zero(t, issue1664R5EventCount(h, EventKindTurnTimeout), "no turn-timeout event after a Stop")
}

func TestIssue1664_R5_TimeoutRetryNamesModelAndProvider(t *testing.T) {
	issue1478ShortSilence(t)
	h, _, _ := issue1664Call(t, issue1664R5TimeoutProvider())
	retries := issue1664RetryEvents(h)
	require.Len(t, retries, 2, "SETUP: three silent calls announce two retries")
	for i, retry := range retries {
		require.Equal(t, "m", retry.Model, "R3-2: retry %d names the model that is the sentence subject", i+1)
		require.Equal(t, "p", retry.Provider, "R3-2: retry %d names its distinct provider", i+1)
	}
}

func TestIssue1664_R5_HardAbortWithChainEndsAborted(t *testing.T) {
	issue1478ShortSilence(t)
	var h *wu22Harness
	provider := &issue1478Provider{script: func(_ context.Context, call int) (*providers.LLMResponse, error) {
		if call == 1 {
			h.ts.requestHardAbort()
		}
		return nil, context.DeadlineExceeded
	}}
	var rr *agentLoopRunTurnResponse
	h, rr = issue1664R5Response(t, provider, true)
	rr.callLLMWithRetries()
	require.True(t, h.ts.hardAbortRequested(), "SETUP: hard abort arrived mid-call")
	require.Error(t, rr.rq.ri.rf.err)
	t.Run("one provider call", func(t *testing.T) {
		require.Equal(t, 1, issue1664CallCount(provider))
	})
	t.Run("no retry frame", func(t *testing.T) {
		require.Empty(t, issue1664RetryEvents(h))
	})
	t.Run("chain normalizes cancellation", func(t *testing.T) {
		require.ErrorIs(t, rr.rq.ri.rf.err, context.Canceled)
	})
	t.Run("response stage ends aborted", func(t *testing.T) {
		require.Equal(t, TurnEndStatusAborted, rr.rq.ri.turnStatus, "R3-3: a hard-aborted turn cannot end completed")
	})
}

func TestIssue1664_R5_HardAbortRawTimeoutAnnouncesNoRetry(t *testing.T) {
	issue1478ShortSilence(t)
	var h *wu22Harness
	provider := &issue1478Provider{script: func(_ context.Context, call int) (*providers.LLMResponse, error) {
		if call == 1 {
			h.ts.requestHardAbort()
		}
		return nil, context.DeadlineExceeded
	}}
	var rr *agentLoopRunTurnResponse
	h, rr = issue1664R5Response(t, provider, false)
	rr.callLLMWithRetries()
	require.True(t, h.ts.hardAbortRequested(), "SETUP: hard abort arrived mid-call without a chain")
	require.Error(t, rr.rq.ri.rf.err)
	t.Run("one provider call", func(t *testing.T) {
		require.Equal(t, 1, issue1664CallCount(provider), "R3-3: no second provider call after the hard abort")
	})
	t.Run("no retry frame", func(t *testing.T) {
		require.Empty(t, issue1664RetryEvents(h), "no retry frame after the hard abort")
	})
	t.Run("no turn-timeout event", func(t *testing.T) {
		require.Zero(t, issue1664R5EventCount(h, EventKindTurnTimeout), "no timeout recovery after the hard abort")
	})
}

func TestIssue1664_R5_WaitingNoticesCountAllAttempts(t *testing.T) {
	after := issue1478ShortSilence(t)
	provider := &issue1478Provider{script: func(_ context.Context, call int) (*providers.LLMResponse, error) {
		time.Sleep(3 * after) // the shortened silence seam, not a guard-length wait
		if call < 3 {
			return nil, context.DeadlineExceeded
		}
		return &providers.LLMResponse{Content: "third time"}, nil
	}}
	h, _, err := issue1664Call(t, provider)
	require.NoError(t, err)
	var attempts []int
	for _, status := range issue1478Statuses(h) {
		if status.State != ProviderStatusWaiting {
			continue
		}
		require.Equal(t, 3, status.MaxAttempts, "every waiting notice carries the founder-approved cap 3")
		if len(attempts) == 0 || attempts[len(attempts)-1] != status.Attempt {
			attempts = append(attempts, status.Attempt)
		}
	}
	require.Equal(t, []int{1, 2, 3}, attempts, "R3-4: every silent attempt has its own 1-based waiting count")
}

func TestIssue1664_R5_SteeringOnlyKeepsWrapUpCall(t *testing.T) {
	issue1478ShortSilence(t)
	var h *wu22Harness
	var interrupted atomic.Bool
	provider := &issue1478Provider{script: func(_ context.Context, call int) (*providers.LLMResponse, error) {
		if call == 1 {
			interrupted.Store(h.ts.requestGracefulInterrupt("steer"))
			return nil, context.DeadlineExceeded
		}
		return &providers.LLMResponse{Content: "wrap-up answer"}, nil
	}}
	var rr *agentLoopRunTurnResponse
	h, rr = issue1664R5Response(t, provider, true)
	rr.callLLMWithRetries()
	require.True(t, interrupted.Load(), "SETUP: steering-only interruption was accepted")
	require.False(t, h.ts.cancelFired.Load(), "SETUP: no user/admin cancel was claimed")
	require.NoError(t, rr.rq.ri.rf.err, "R3-5: steering-only interruption keeps its one wrap-up call")
	require.Equal(t, 2, issue1664CallCount(provider), "the wrap-up provider call still happens")
}

func TestIssue1664_R5_UserStopDuringStreamingResetBackoff(t *testing.T) {
	// Equivalent to P4u without the 300 ms wall-clock race. synctest.Wait
	// exposes a pending real retry timer without advancing the fake clock.
	synctest.Test(t, func(t *testing.T) {
		issue1478ShortSilence(t)
		p := &issue1478Provider{script: func(context.Context, int) (*providers.LLMResponse, error) { return nil, errIssue1664R5Reset }}
		q := &issue1478Provider{script: func(context.Context, int) (*providers.LLMResponse, error) { return nil, errIssue1664R5Reset }}
		h, rr := issue1664R5ResetResponse(t, p, q)
		turnCtx, cancel := context.WithCancel(context.Background())
		defer cancel() // also releases the worker if a setup assertion fails
		rr.rq.ri.rf.rt.turnCtx = turnCtx
		done := make(chan error, 1)
		go func() {
			rr.callLLMWithRetries()
			done <- rr.rq.ri.rf.err
		}()
		synctest.Wait()
		retries := issue1664RetryEvents(h)
		require.Len(t, retries, 1, "SETUP: the streaming-reset retry was announced and its backoff is pending")
		require.Equal(t, "streaming_reset", retries[0].Reason)
		require.Equal(t, 500*time.Millisecond, retries[0].Backoff, "SETUP: the unchanged first reset backoff")
		callsAtStop := issue1664CallCount(p) + issue1664CallCount(q)
		require.Positive(t, callsAtStop, "SETUP: real candidate calls reached the reset branch")
		require.True(t, h.ts.claimCancel(true), "SETUP: user Stop claimed the turn during the wait")
		require.True(t, h.ts.requestGracefulInterrupt(""))
		synctest.Wait()
		var err error
		select {
		case err = <-done:
		default:
			t.Error("R3-6: Stop must finish the streaming-reset backoff without waiting for its retry timer")
			err = <-done // join the worker even on a mutant; fake time may advance now
		}
		require.ErrorIs(t, err, context.Canceled, "Stop ends the reset round canceled")
		require.Equal(t, callsAtStop, issue1664CallCount(p)+issue1664CallCount(q), "no provider call after Stop")
		require.Len(t, issue1664RetryEvents(h), 1, "only the retry announced before Stop")
	})
}

func TestIssue1664_R5_UserStopDuringChainAnnouncesNoResetRetry(t *testing.T) {
	issue1478ShortSilence(t)
	var h *wu22Harness
	var claimed, interrupted atomic.Bool
	p := &issue1478Provider{script: func(_ context.Context, call int) (*providers.LLMResponse, error) {
		if call == 1 {
			claimed.Store(h.ts.claimCancel(true))
			interrupted.Store(h.ts.requestGracefulInterrupt(""))
		}
		return nil, errIssue1664R5Reset
	}}
	q := &issue1478Provider{script: func(context.Context, int) (*providers.LLMResponse, error) { return nil, errIssue1664R5Reset }}
	var rr *agentLoopRunTurnResponse
	h, rr = issue1664R5ResetResponse(t, p, q)
	rr.callLLMWithRetries()
	require.True(t, claimed.Load(), "SETUP: user Stop claimed the turn")
	require.True(t, interrupted.Load(), "SETUP: the graceful interruption was accepted")
	require.Error(t, rr.rq.ri.rf.err)
	require.Empty(t, issue1664RetryEvents(h), "R3-6: no retry event of any kind follows a user Stop during the chain")
}

func TestIssue1664_R5_StreamingResetWithoutStopAnnouncesRetry(t *testing.T) {
	issue1478ShortSilence(t)
	p := &issue1478Provider{script: func(context.Context, int) (*providers.LLMResponse, error) { return nil, errIssue1664R5Reset }}
	q := &issue1478Provider{script: func(context.Context, int) (*providers.LLMResponse, error) { return nil, errIssue1664R5Reset }}
	h, rr := issue1664R5ResetResponse(t, p, q)
	rr.callLLMWithRetries()
	require.Error(t, rr.rq.ri.rf.err, "SETUP: every candidate fails")
	retries := issue1664RetryEvents(h)
	require.NotEmpty(t, retries, "INSTRUMENT: without Stop, the reset branch must announce a retry")
	require.Equal(t, "streaming_reset", retries[0].Reason)
}

func TestIssue1664_R5_LazyCancelWakeReflectsClaimedStop(t *testing.T) {
	// A05 is defence in depth: the full Stop cascade eagerly creates the
	// channel. Pin the documented lazy-initialization contract separately,
	// with the entry flags as fixture data, without claiming a live Stop path.
	for _, tc := range []struct {
		name              string
		graceful, claimed bool
		wantClosed        bool
	}{
		{name: "no interruption", wantClosed: false},
		{name: "steering only", graceful: true, wantClosed: false},
		{name: "claim before graceful cascade", claimed: true, wantClosed: false},
		{name: "claimed graceful stop before lazy creation", graceful: true, claimed: true, wantClosed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := &turnState{gracefulInterrupt: tc.graceful}
			if tc.claimed {
				require.True(t, ts.claimCancel(false), "SETUP: claim the cancellation before lazy initialization")
			}
			wake := ts.cancelWake()
			closed := false
			select {
			case <-wake:
				closed = true
			default:
			}
			require.Equal(t, tc.wantClosed, closed, "A05: lazy cancel wake reflects the claimed graceful Stop on first access")
			require.Equal(t, wake, ts.cancelWake(), "lazy access returns the same channel")
		})
	}
}
