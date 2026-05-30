package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"golang.org/x/term"
)

// rawStdinWatcher watches stdin in raw mode during agent inference. It polls
// stdin byte-by-byte, feeds each byte to an escapeDetector, and calls cancelFn
// when a double-Escape sequence is detected.
//
// The watcher holds stdin in raw mode for the duration of the watch. Callers
// must:
//  1. Call startRawStdinWatcher to begin watching. It returns (stopFn, ok).
//     If ok==false, the terminal is not a TTY; no watcher is started and
//     stopFn is a no-op.
//  2. Call stopFn (or cancel the returned context) when inference is done.
//     stopFn blocks briefly until the goroutine exits cleanly and restores the
//     terminal to its previous mode.
//
// Coordination with ergochat/readline:
//
//	readline owns stdin ONLY while rl.Readline() is executing. Our watcher owns
//	stdin ONLY while ProcessDirect is executing (between readline.Readline()
//	returning and the next readline.Readline() call). The caller is responsible
//	for enforcing this sequencing.
func startRawStdinWatcher(ctx context.Context, cancelFn context.CancelFunc) (stop func(), ok bool) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		slog.Warn("stdin is not a TTY; double-Escape cancel is unavailable for this session")
		return func() {}, false
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		slog.Warn("failed to set stdin to raw mode; double-Escape cancel is unavailable", "error", err)
		return func() {}, false
	}

	done := make(chan struct{})

	go func() {
		defer close(done)
		defer func() {
			if restoreErr := term.Restore(fd, oldState); restoreErr != nil {
				// Failing to restore means the terminal is stuck in raw mode.
				// Log at ERROR level and print a user-facing message so the
				// user knows to run `reset` or `stty sane` if their shell breaks.
				slog.Error("rawStdinWatcher: failed to restore terminal mode; shell may be unusable until reset",
					"error", restoreErr)
				fmt.Fprintf(os.Stderr, "\n[omnipus] Warning: terminal mode could not be restored (error: %v).\n"+
					"If your shell is unusable, run: reset\n", restoreErr)
			}
		}()

		runEscapeReadLoop(ctx, cancelFn, os.Stdin)
	}()

	stopFn := func() {
		// Signal the goroutine to stop; it will restore terminal state.
		cancelFn()
		<-done
	}
	return stopFn, true
}

// byteReader is the minimal io interface needed by the escape-detection loop.
// Satisfied by *os.File and any io.Reader implementation — used to make the
// loop unit-testable without a real TTY.
type byteReader interface {
	Read(p []byte) (n int, err error)
}

// runEscapeReadLoop reads bytes from r one at a time, feeding them to an
// escapeDetector. It calls cancelFn on double-Escape and returns when ctx
// is done or the reader returns an error (EOF is silent; other errors log WARN).
//
// Extracted from the startRawStdinWatcher goroutine so tests can inject a
// synthetic reader without needing a real TTY (H-4 fix).
func runEscapeReadLoop(ctx context.Context, cancelFn context.CancelFunc, r byteReader) {
	det := newEscapeDetector()
	buf := make([]byte, 1)

	for {
		// Check whether the caller has signaled us to stop.
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, readErr := r.Read(buf)
		if readErr != nil || n == 0 {
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				// Unexpected read failure (not a normal EOF / terminal close).
				// Log at WARN so the operator can see the cause; cancel
				// detection is now disabled for this session.
				slog.Warn("rawStdinWatcher: stdin read failed; cancel escape detection disabled for this session",
					"error", readErr)
			}
			// Silent return for EOF (normal terminal detach) or zero-byte read.
			return
		}

		// Re-check context after potentially blocking read.
		select {
		case <-ctx.Done():
			return
		default:
		}

		cancel, _ := det.feed(buf[0])
		if cancel {
			slog.Info("double-Escape detected; canceling current inference turn")
			cancelFn()
			// Keep the goroutine alive until ctx.Done() so we don't
			// leave the terminal in raw mode prematurely. The caller
			// will close ctx after ProcessDirect returns.
			<-ctx.Done()
			return
		}
	}
}
