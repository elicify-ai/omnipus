//go:build lite

package webrtc

// Available is false in a lite build — see session.go's doc comment (the
// non-lite twin of this var) for why a caller needs this cheap, no-I/O
// signal.
var Available = false

// Session is the lite-build stand-in for the real Pion-backed Session
// (session.go et al, //go:build !lite). Lite builds (-tags lite) deliberately
// exclude Pion to keep the binary small, matching the whatsmeow/lite gating
// pattern in pkg/channels/whatsapp_native -- every method here returns
// ErrUnavailable so a caller written against the real API compiles
// unchanged and gets a clear, typed signal to fall back to the JPEG
// screencast path (wave-plan decision 5: "JPEG is the lite behavior").
type Session struct{}

// NewSession returns a stub Session. cfg/sink/logf are accepted (and
// ignored) purely so call sites don't need a build-tag branch -- every
// method on the result returns ErrUnavailable.
func NewSession(_ Config, _ InputSink, _ func(string, ...any)) *Session {
	return &Session{}
}

// HandleIngestOffer always returns ErrUnavailable in a lite build.
func (s *Session) HandleIngestOffer(_ string) (string, error) {
	return "", ErrUnavailable
}

// HandleViewerOffer always returns ErrUnavailable in a lite build.
func (s *Session) HandleViewerOffer(_ string, _ string) (string, error) {
	return "", ErrUnavailable
}

// SetOnBitrateTarget is a no-op in a lite build: there is no viewer leg to
// receive RTCP from.
func (s *Session) SetOnBitrateTarget(_ func(int)) {}

// CloseViewer is a no-op: there is never an attached viewer to close in a
// lite build (HandleViewerOffer can't have succeeded).
func (s *Session) CloseViewer(_ string) {}

// SignalRecapture is a no-op: there is never an active ingest connection to
// prime with a keyframe request in a lite build.
func (s *Session) SignalRecapture() {}

// SendToViewer always returns ErrUnavailable in a lite build.
func (s *Session) SendToViewer(_ string, _ []byte) error {
	return ErrUnavailable
}

// Stats returns the zero value: no viewers, no tracks, no packets.
func (s *Session) Stats() Stats {
	return Stats{}
}

// Close returns nil (idempotent no-op). Unlike the offer/send methods,
// Close is a normal part of every caller's shutdown sequence regardless of
// whether WebRTC was ever actually usable, so it deliberately does NOT
// return ErrUnavailable -- there is nothing to fail to close.
func (s *Session) Close() error {
	return nil
}
