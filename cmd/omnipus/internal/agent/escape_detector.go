package agent

import (
	"time"
)

// escapeDetector is a byte-level state machine that detects double-Escape (0x1B 0x1B)
// within a 500ms window while ignoring CSI/SS3 escape sequences (arrow keys, F-keys).
//
// Use:
//
//	d := newEscapeDetector()
//	cancel, passThrough := d.feed(b)
//
// When feed returns cancel==true, the caller should fire the cancellation.
// passThrough contains bytes that should be passed to the terminal as-is (e.g.
// the bytes of an arrow-key sequence after we determine it is not a cancel).
//
// Timing is wall-clock driven. Two consecutive Escape bytes arriving within
// window (500ms) trigger cancel. A single 0x1B followed by 0x5B ('[') or 0x4F
// ('O') within csiTimeout (50ms) is treated as a CSI/SS3 sequence and passed
// through. A CSI introducer arriving after csiTimeout is NOT classified as a
// CSI sequence — the buffered ESC is silently discarded and the new byte starts
// fresh processing (FR-31a).
//
// This type is NOT goroutine-safe; the caller must serialize calls.
type escapeDetector struct {
	// waitingForSecond is true after we have seen a first 0x1B and are
	// waiting to see whether the next byte is another 0x1B (cancel), a CSI/SS3
	// introducer (ignore + pass through), or something else (clear).
	waitingForSecond bool

	// firstEscAt is the wall-clock time we received the first 0x1B.
	firstEscAt time.Time

	// window is the maximum gap between two consecutive Escape bytes
	// that still counts as "double Escape". Default 500ms.
	window time.Duration

	// csiTimeout is the grace period after 0x1B within which a 0x5B or 0x4F
	// byte is treated as a CSI/SS3 sequence start (not a cancel). Default 50ms.
	csiTimeout time.Duration
}

func newEscapeDetector() *escapeDetector {
	return &escapeDetector{
		window:     500 * time.Millisecond,
		csiTimeout: 50 * time.Millisecond,
	}
}

// feed processes one byte from stdin.
// Returns cancel=true when a double-Escape cancel should be fired.
// Returns passThrough with any bytes that should be treated as regular terminal
// input (e.g. the 0x1B + 0x5B bytes of an arrow key sequence, so readline can
// handle them if needed).
func (d *escapeDetector) feed(b byte) (cancel bool, passThrough []byte) {
	const (
		byteESC = 0x1B
		byteCSI = 0x5B // '[' — CSI introducer after ESC
		byteSS3 = 0x4F // 'O' — SS3 introducer after ESC (F1-F4)
	)

	now := time.Now()

	// If we were waiting for a second Escape but the 500ms window has expired,
	// clear the pending state first.
	if d.waitingForSecond && now.Sub(d.firstEscAt) > d.window {
		d.waitingForSecond = false
	}

	switch {
	case !d.waitingForSecond && b == byteESC:
		// First Escape: enter the waiting-for-second state.
		d.waitingForSecond = true
		d.firstEscAt = now
		return false, nil

	case d.waitingForSecond && b == byteESC:
		// Second Escape within the window → cancel.
		d.waitingForSecond = false
		return true, nil

	case d.waitingForSecond && (b == byteCSI || b == byteSS3):
		// CSI/SS3 introducer after the first Escape.
		// FR-31a: only treat it as a CSI/SS3 sequence if it arrived within
		// csiTimeout (50ms). If more than 50ms has elapsed, the ESC was a
		// standalone escape — discard it silently and process the new byte
		// as a regular byte (not a cancel trigger).
		d.waitingForSecond = false
		if now.Sub(d.firstEscAt) <= d.csiTimeout {
			// Arrow key or F-key sequence within the 50ms CSI window:
			// pass both the ESC and this byte through so readline can process them.
			return false, []byte{byteESC, b}
		}
		// CSI byte arrived too late — the ESC was stale. Discard the ESC and
		// treat '[' or 'O' as a normal byte.
		return false, []byte{b}

	case d.waitingForSecond:
		// Some other byte after a single Escape: ignore the Escape (no-op)
		// and pass this new byte through normally.
		d.waitingForSecond = false
		return false, []byte{b}

	default:
		// Normal byte with no pending Escape state.
		return false, []byte{b}
	}
}
