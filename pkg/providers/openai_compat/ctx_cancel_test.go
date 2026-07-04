// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package openai_compat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// errReader returns err on the first Read — simulating resp.Body being closed
// by ChatStream's ctx.Done() watchdog goroutine mid-stream (which surfaces as
// "http2: response body closed").
type errReader struct{ err error }

func (r *errReader) Read(_ []byte) (int, error) { return 0, r.err }

// TestParseStreamResponse_CtxCancel_ReturnsContextError proves the hidden-bug
// fix: when the caller's context is canceled/timed out, our own watchdog closes
// the body and the scanner errors with a connection-drop string ("http2:
// response body closed"). parseStreamResponse MUST surface the context error, not
// misreport it as a "streaming read error" (a transient stream reset), so callers
// classify it as a cancel/timeout instead of blindly retrying an abandoned call.
func TestParseStreamResponse_CtxCancel_ReturnsContextError(t *testing.T) {
	// The exact error string our ctx.Done() body-close produces (proven
	// empirically against real OpenRouter: a mid-stream cancel yields this).
	bodyClosed := errors.New("http2: response body closed")

	t.Run("canceled context surfaces context.Canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := parseStreamResponse(ctx, &errReader{err: bodyClosed}, func(string) {})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want errors.Is(err, context.Canceled); got %v", err)
		}
		if strings.Contains(err.Error(), "streaming read error") {
			t.Fatalf("ctx-cancel must NOT be reported as a streaming read error; got %v", err)
		}
	})

	t.Run("deadline-exceeded context surfaces context.DeadlineExceeded", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		_, err := parseStreamResponse(ctx, &errReader{err: bodyClosed}, func(string) {})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want errors.Is(err, context.DeadlineExceeded); got %v", err)
		}
	})

	t.Run("genuine stream error (ctx alive) still reports streaming read error", func(t *testing.T) {
		// No cancellation: a real server-side drop must still be a retryable
		// "streaming read error" so the transient-retry path fires.
		_, err := parseStreamResponse(context.Background(), &errReader{err: bodyClosed}, func(string) {})
		if err == nil || !strings.Contains(err.Error(), "streaming read error") {
			t.Fatalf("live-ctx stream drop must report a streaming read error; got %v", err)
		}
	})
}
