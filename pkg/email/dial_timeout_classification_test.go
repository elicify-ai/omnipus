package email

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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
		if !errors.Is(got, context.Canceled) {
			t.Fatalf("classifyDialErr(done ctx, _) = %v, want bare ctx.Err() (context.Canceled)", got)
		}
	})
}

// TestSMTPStep_ClassifiesDeadlineFlavored pins the deadline-flavored
// normalization smtpStep applies to a failed SMTP step (MC-21). smtpStep is the
// single classification point for every post-dial SMTP step — STARTTLS, auth,
// MAIL FROM, RCPT TO, DATA, write body, body close, QUIT, and (via the two
// smtp.NewClient greeting-read branches in smtp_send.go) the greeting read —
// so pinning it here pins the classification for all of those sites at once.
// The raw connection's SetDeadline clock and the caller's context timer are
// independent clocks: a blocked step (e.g. a greeting read on a stall-after-
// connect peer) can fail deadline-flavored a hair before ctx.Err() has
// transitioned to non-nil, and the old ctx.Err()-only check let the raw i/o
// timeout escape in exactly that window.
func TestSMTPStep_ClassifiesDeadlineFlavored(t *testing.T) {
	liveCtx := context.Background() // never Done for the life of the test

	t.Run("net.Error timeout step error with live ctx is reclassified", func(t *testing.T) {
		got := smtpStep(liveCtx, timeoutNetError{}, "auth")
		if !errors.Is(got, context.DeadlineExceeded) {
			t.Fatalf("smtpStep(live ctx, net.Error Timeout) = %v, want errors.Is context.DeadlineExceeded", got)
		}
		if errors.Is(got, os.ErrDeadlineExceeded) {
			t.Fatalf("smtpStep must REPLACE the raw timeout, not accumulate it: %v", got)
		}
	})

	t.Run("os.ErrDeadlineExceeded-wrapped step error is reclassified", func(t *testing.T) {
		got := smtpStep(liveCtx, fmt.Errorf("read tcp 127.0.0.1:1: %w", os.ErrDeadlineExceeded), "SMTP client (greeting)")
		if !errors.Is(got, context.DeadlineExceeded) {
			t.Fatalf("smtpStep(live ctx, os.ErrDeadlineExceeded-wrapped) = %v, want errors.Is context.DeadlineExceeded", got)
		}
		if !strings.Contains(got.Error(), "SMTP client (greeting)") {
			t.Fatalf("smtpStep dropped the step label on reclassification: %v", got)
		}
	})

	t.Run("unrelated step error keeps its label wrap", func(t *testing.T) {
		got := smtpStep(liveCtx, fmt.Errorf(" RCPT TO refused: %w", syscall.ECONNREFUSED), "RCPT TO")
		if !errors.Is(got, syscall.ECONNREFUSED) {
			t.Fatalf("smtpStep must not hide a real SMTP failure: got %v, want errors.Is ECONNREFUSED", got)
		}
		if errors.Is(got, context.DeadlineExceeded) {
			t.Fatalf("smtpStep reclassified a refused RCPT TO as a deadline: %v", got)
		}
	})

	t.Run("done ctx returns ctx.Err as today", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got := smtpStep(ctx, timeoutNetError{}, "auth")
		if !errors.Is(got, context.Canceled) {
			t.Fatalf("smtpStep(done ctx, _) = %v, want bare ctx.Err() (context.Canceled)", got)
		}
	})
}
