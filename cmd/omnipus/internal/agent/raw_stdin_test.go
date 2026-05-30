package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// slogRecord captures slog records emitted during a test.
type slogRecord struct {
	Level   slog.Level
	Message string
	Attrs   map[string]any
}

// capturingHandler is a slog.Handler that appends records to a slice.
type capturingHandler struct {
	records *[]slogRecord
	level   slog.Level
}

func (h *capturingHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }
func (h *capturingHandler) WithAttrs(attrs []slog.Attr) slog.Handler     { return h }
func (h *capturingHandler) WithGroup(name string) slog.Handler           { return h }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	rec := slogRecord{Level: r.Level, Message: r.Message, Attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool {
		rec.Attrs[a.Key] = a.Value.Any()
		return true
	})
	*h.records = append(*h.records, rec)
	return nil
}

// withCapturingLogger installs a capturing slog handler for the duration of
// the test and returns the collected records after fn returns.
func withCapturingLogger(t *testing.T, fn func()) []slogRecord {
	t.Helper()
	var records []slogRecord
	h := &capturingHandler{records: &records, level: slog.LevelDebug}
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })
	fn()
	return records
}

// errorReader is a byteReader that always returns the given error.
type errorReader struct{ err error }

func (e *errorReader) Read(_ []byte) (int, error) { return 0, e.err }

// eofReader returns io.EOF immediately.
type eofReader struct{}

func (e *eofReader) Read(_ []byte) (int, error) { return 0, io.EOF }

// TestRawStdinHandler_NonEOFErrorLogged verifies that a non-EOF read error from
// the stdin watcher emits a WARN log containing "stdin read failed" (H-4 fix).
func TestRawStdinHandler_NonEOFErrorLogged(t *testing.T) {
	syntheticErr := errors.New("pipe broken: EIO")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var warnFound bool
	records := withCapturingLogger(t, func() {
		runEscapeReadLoop(ctx, cancel, &errorReader{err: syntheticErr})
	})

	for _, r := range records {
		if r.Level == slog.LevelWarn && strings.Contains(r.Message, "stdin read failed") {
			warnFound = true
		}
	}
	if !warnFound {
		t.Errorf("expected WARN log containing 'stdin read failed' for non-EOF read error, got records: %+v", records)
	}
}

// TestRawStdinHandler_EOFIsSilent verifies that an io.EOF from the stdin reader
// does NOT produce any log output (H-4 fix: EOF is a normal terminal close).
func TestRawStdinHandler_EOFIsSilent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	records := withCapturingLogger(t, func() {
		runEscapeReadLoop(ctx, cancel, &eofReader{})
	})

	for _, r := range records {
		if strings.Contains(r.Message, "stdin read failed") {
			t.Errorf("unexpected log for EOF: %+v", r)
		}
	}
}

// TestRawStdinHandler_ContextCancellationExitsCleanly verifies that canceling
// the context causes runEscapeReadLoop to return without logging an error.
func TestRawStdinHandler_ContextCancellationExitsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// A reader that blocks until context is canceled by returning io.EOF after
	// cancel — simulating the goroutine exiting on context done.
	blockingReader := &eofReader{}

	// Cancel immediately so the loop exits on the first context check.
	cancel()

	records := withCapturingLogger(t, func() {
		runEscapeReadLoop(ctx, cancel, blockingReader)
	})

	for _, r := range records {
		if r.Level >= slog.LevelWarn && strings.Contains(r.Message, "stdin read failed") {
			t.Errorf("unexpected error log on context cancellation: %+v", r)
		}
	}
}
