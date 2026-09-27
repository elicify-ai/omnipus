package email

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

// timeoutNetError is a platform-independent net.Error whose Timeout() is true
// and whose chain contains no os.ErrDeadlineExceeded, so the net.Error arm of
// classifyDialErr is exercised separately from the os.ErrDeadlineExceeded arm
// (a real dial to a routing blackhole can surface either flavor depending on
// where the timeout originates - dialer, poller, or network stack).
type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "dial tcp 192.0.2.1:1: simulated hard timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

// TestClassifyDialErr pins the dial-error normalization dialSMTPRaw relies on
// (MC-21). The dialer fixed dialTimeout and the caller context deadline are
// independent clocks: a dial can fail deadline-flavored - wrapping
// os.ErrDeadlineExceeded, or as a net.Error with Timeout() true (e.g. the
// network stack ETIMEDOUT) - at a moment when ctx.Err() has not yet
// transitioned to non-nil, and the old inline branch let the raw error escape
// in exactly that window. Real network behavior is inherently
// non-deterministic, so these cases construct the inputs directly and test the
// classifier in isolation.
func TestClassifyDialErr(t *testing.T) {
	liveCtx := context.Background() // never Done for the life of the test
	rawDeadlineErr := fmt.Errorf("dial tcp 192.0.2.1:1: %w", os.ErrDeadlineExceeded)

	t.Run("deadline-flavored raw error with live ctx is reclassified", func(t *testing.T) {
		got := classifyDialErr(liveCtx, rawDeadlineErr)
		if !errors.Is(got, context.DeadlineExceeded) {
			t.Fatalf("classifyDialErr(live ctx, os.ErrDeadlineExceeded-wrapped) = %v, want errors.Is context.DeadlineExceeded", got)
		}
		if errors.Is(got, os.ErrDeadlineExceeded) {
			t.Fatalf("classifyDialErr must REPLACE the raw error, not accumulate it: %v", got)
		}
	})

	t.Run("net.Error timeout with live ctx is reclassified", func(t *testing.T) {
		got := classifyDialErr(liveCtx, timeoutNetError{})
		if !errors.Is(got, context.DeadlineExceeded) {
			t.Fatalf("classifyDialErr(live ctx, net.Error Timeout) = %v, want errors.Is context.DeadlineExceeded", got)
		}
	})

	t.Run("unrelated error passes through unchanged", func(t *testing.T) {
		raw := fmt.Errorf("dial tcp 127.0.0.1:1: %w", syscall.ECONNREFUSED)
		got := classifyDialErr(liveCtx, raw)
		if !errors.Is(got, syscall.ECONNREFUSED) {
			t.Fatalf("classifyDialErr must not hide connection errors: got %v, want errors.Is ECONNREFUSED", got)
		}
		if errors.Is(got, context.DeadlineExceeded) {
			t.Fatalf("classifyDialErr reclassified a refused connection as a deadline: %v", got)
		}
	})

	t.Run("done ctx returns ctx.Err as today", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got := classifyDialErr(ctx, rawDeadlineErr)
		if got != context.Canceled {
			t.Fatalf("classifyDialErr(done ctx, _) = %v, want bare ctx.Err() (context.Canceled)", got)
		}
	})
}
