//go:build lite

package webrtc

import "testing"

// TestStubSessionReturnsErrUnavailable exercises the lite-build API surface:
// every method that has real work to do on a real build must return
// ErrUnavailable here, and the no-op methods (CloseViewer, SignalRecapture,
// Close) must not panic.
func TestStubSessionReturnsErrUnavailable(t *testing.T) {
	sess := NewSession(Config{StunServer: "stun:stun.l.google.com:19302"}, func(string, []byte) {}, t.Logf)
	if sess == nil {
		t.Fatal("NewSession returned nil")
	}

	if _, err := sess.HandleIngestOffer("v=0..."); err != ErrUnavailable {
		t.Errorf("HandleIngestOffer error = %v, want ErrUnavailable", err)
	}
	if _, err := sess.HandleViewerOffer("viewer-1", "v=0..."); err != ErrUnavailable {
		t.Errorf("HandleViewerOffer error = %v, want ErrUnavailable", err)
	}
	if err := sess.SendToViewer("viewer-1", []byte("x")); err != ErrUnavailable {
		t.Errorf("SendToViewer error = %v, want ErrUnavailable", err)
	}

	// No-ops: must not panic.
	sess.CloseViewer("viewer-1")
	sess.SignalRecapture()

	if got := sess.Stats(); got != (Stats{}) {
		t.Errorf("Stats() = %+v, want zero value", got)
	}

	if err := sess.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}
