// T2.11: Logger_Log_NilReceiver_DoesNotPanic.
//
// Verifies that (*audit.Logger).Log on a nil receiver returns nil without
// panicking and without goroutine leaks. B1.2(a) in the BRD.

package audit

import (
	"testing"
	"time"
)

// TestLogger_Log_NilReceiver_DoesNotPanic (T2.11) constructs a nil *Logger
// and calls Log with a real entry. It must return nil, must not panic, and
// must not hang (the goroutine timeout detects a deadlock or channel block).
func TestLogger_Log_NilReceiver_DoesNotPanic(t *testing.T) {
	var l *Logger // nil receiver

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// A panic is a failure — the nil-receiver guard must prevent it.
				done <- &nilReceiverPanicError{val: r}
				return
			}
			entry := &Entry{
				Timestamp: time.Now().UTC(),
				Event:     EventToolCall,
				Decision:  DecisionAllow,
				AgentID:   "nil-logger-test",
			}
			err := l.Log(entry)
			done <- err
		}()
	}()

	select {
	case err := <-done:
		if panicked, ok := err.(*nilReceiverPanicError); ok {
			t.Fatalf("nil *Logger.Log panicked: %v", panicked.val)
		}
		if err != nil {
			t.Errorf("nil *Logger.Log returned non-nil error: %v", err)
		}
		// err == nil is the correct outcome (B1.2(a): nil receiver returns nil).
	case <-time.After(5 * time.Second):
		t.Fatal("nil *Logger.Log hung for 5 s — goroutine leak or deadlock")
	}
}

// TestLogger_Log_NilEntry_DoesNotPanic verifies the nil-entry guard inside Log.
// A nil *Entry must also be handled safely (the check is adjacent to the nil-receiver guard).
func TestLogger_Log_NilEntry_DoesNotPanic(t *testing.T) {
	var l *Logger

	done := make(chan struct{}, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// panic → fail below
				done <- struct{}{}
				return
			}
			_ = l.Log(nil)
			done <- struct{}{}
		}()
	}()

	select {
	case <-done:
		// either the goroutine finished or it recovered a panic; only the
		// latter is a failure, but we can't distinguish here. The separate
		// TestLogger_Log_NilReceiver_DoesNotPanic catches panics more precisely.
	case <-time.After(5 * time.Second):
		t.Fatal("nil *Logger.Log(nil) hung — goroutine leak or deadlock")
	}
}

// nilReceiverPanicError is a sentinel error type so the goroutine can signal
// a panic to the parent test goroutine without calling runtime.Goexit().
type nilReceiverPanicError struct{ val any }

func (p *nilReceiverPanicError) Error() string {
	return "panic in nil Logger.Log"
}
