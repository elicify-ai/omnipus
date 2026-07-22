//go:build lite

package webrtc

// lite_available_test.go verifies the lite-build half of the Available
// contract (session.go's non-lite twin has no dedicated test of its own —
// TestNewSession and friends in session_test.go, //go:build !lite, already
// implicitly exercise Available==true by existing at all under that build
// tag). Run with: go test -tags goolm,stdjson,lite -run '^TestAvailable$' ./pkg/tools/browser/webrtc/

import "testing"

func TestAvailable(t *testing.T) {
	if Available {
		t.Fatal(
			"Available must be false in a lite build (Pion compiled out) — the gateway's WebRTC gate ladder (wave-plan W2-A) depends on this to report reason=lite_build without ever attempting a real offer",
		)
	}
}

func TestSession_MethodsReturnErrUnavailable(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	if _, err := s.HandleIngestOffer("sdp"); err != ErrUnavailable {
		t.Fatalf("HandleIngestOffer err = %v, want ErrUnavailable", err)
	}
	if _, err := s.HandleViewerOffer("viewer", "sdp"); err != ErrUnavailable {
		t.Fatalf("HandleViewerOffer err = %v, want ErrUnavailable", err)
	}
	if err := s.SendToViewer("viewer", []byte("x")); err != ErrUnavailable {
		t.Fatalf("SendToViewer err = %v, want ErrUnavailable", err)
	}
	// CloseViewer/SignalRecapture must not panic (no-ops per stub.go).
	s.CloseViewer("viewer")
	s.SignalRecapture()
	if stats := s.Stats(); stats.Viewers != 0 || stats.HasVideo || stats.HasAudio {
		t.Fatalf("Stats() = %+v, want the zero value", stats)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil (documented idempotent no-op)", err)
	}
}
