// Package webrtc is a self-contained Pion SFU relay library for the
// live-browser WebRTC transport (see docs/internal/design/webrtc-build/wave-plan.md,
// row W1-C, and docs/internal/design/wv1-spike-results.md Q3/Q4). It owns exactly
// two kinds of PeerConnection:
//
//   - one "ingest" PC: the headless-Chrome tabCapture encoder page connects
//     here and offers one video + (optionally) one audio track.
//   - N "viewer" PCs: external browsers connect here, recvonly, and get the
//     ingest tracks relayed to them with zero transcoding (raw RTP forward,
//     the standard Pion SFU pattern).
//
// Input (mouse/keyboard) rides a data channel labeled "input" that viewers
// open on their own PeerConnection; every message is handed to the caller's
// InputSink UNPARSED -- this package has no dependency on pkg/api/generated,
// so the gateway is free to interpret the bytes using whatever wire type it
// chooses (BrowserInputFrame today).
//
// Both signaling legs are non-trickle: HandleIngestOffer/HandleViewerOffer
// block until ICE gathering completes (or a timeout elapses) and return a
// complete answer SDP in one shot, matching decision 6 in the wave plan.
//
// This file has no build tag: Config, InputSink, Stats and ErrUnavailable are
// the shared, always-compiled surface between the real implementation
// (session.go et al, //go:build !lite) and the lite stub (stub.go,
// //go:build lite) so callers can reference these types regardless of build.
package webrtc

import "errors"

// ErrUnavailable is returned by every Session method on a lite build (-tags
// lite), where this package's real Pion-backed implementation is compiled
// out to keep the binary small (matches the whatsmeow/lite gating pattern in
// pkg/channels/whatsapp_native). WebRTC live-view is simply unavailable on a
// lite build; callers fall back to the JPEG screencast path.
var ErrUnavailable = errors.New("browser webrtc: not available in this build (lite)")

// Config configures a Session's ICE behavior.
type Config struct {
	// StunServer is a STUN server URL, e.g. "stun:stun.l.google.com:19302".
	// Empty disables STUN entirely: ICE gathers host candidates only, per
	// wave-plan decision 7 (Tools.Browser.WebRTCStunServer, "" = host-only).
	StunServer string
}

// InputSink receives raw input data-channel payloads exactly as sent by a
// viewer's "input" data channel, tagged with the viewerID passed to
// HandleViewerOffer. The payload is opaque JSON bytes (a BrowserInputFrame on
// the wire) -- this package does not parse it, so it never needs
// pkg/api/generated. A nil InputSink is valid; incoming input messages are
// then silently dropped (acked as an error to the sender) rather than
// panicking.
type InputSink func(viewerID string, raw []byte)

// Stats is a point-in-time snapshot of a Session's relay state, as returned
// by Session.Stats() / stubbed by the lite build.
type Stats struct {
	// Viewers is the number of currently attached viewer PeerConnections.
	Viewers int
	// HasVideo/HasAudio report whether an ingest track of that kind has ever
	// been attached (i.e. the shared local track exists, independent of
	// whether the CURRENT ingest connection is still alive -- a track
	// survives ingest replacement, see HandleIngestOffer).
	HasVideo bool
	HasAudio bool
	// VideoCodec/AudioCodec are the negotiated codec MIME types (e.g.
	// "video/VP8", "audio/opus") of the most recent ingest track of that
	// kind, empty if none has arrived yet.
	VideoCodec string
	AudioCodec string
	// VideoPackets/AudioPackets are cumulative RTP packets forwarded from
	// ingest to the shared local track since the Session was created (counts
	// survive ingest replacement; they do not reset).
	VideoPackets int64
	AudioPackets int64
}
