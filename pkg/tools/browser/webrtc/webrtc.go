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
// the shared, always-compiled surface of the package. It used to be the
// boundary between the real implementation and a lite stub (stub.go), but
// ADR-067 retired the lite variant and deleted that stub, so session.go et al
// now compile unconditionally.
package webrtc

import (
	"errors"

	"github.com/pion/ice/v4"
)

// ErrUnavailable is returned by every Session method on a lite build (-tags
// lite), where this package's real Pion-backed implementation is compiled
// out to keep the binary small (matches the whatsmeow/lite gating pattern in
// pkg/channels/whatsapp_native). WebRTC live-view is simply unavailable on a
// lite build; callers fall back to the JPEG screencast path.
var ErrUnavailable = errors.New("browser webrtc: not available in this build (lite)")

// ErrNoIngestVideoTrack is the sentinel HandleViewerOffer wraps (via %w) when
// waitForTracks times out with no video track ever having arrived. Exported
// so pkg/gateway/browser_webrtc.go can classify this SPECIFIC failure mode
// (errors.Is) separately from every other HandleViewerOffer error (bad SDP,
// closed session, PC/track-negotiation failures) for logging/audit
// observability — distinguishing "the capture pipeline just hadn't produced
// a frame yet" from a generic runtime error, per the 2026-07-28 incident
// (see waitForTracksTimeout's doc comment): a viewer-offer failure of THIS
// specific shape is not a capability gate (disabled/not_capable/lite_build)
// and not necessarily a real defect either — it can be a legitimate,
// transient cold-start race — but it deserves its own name in logs/audit
// rather than being indistinguishable from every other "error".
//
// This lives HERE rather than alongside waitForTracks in ingest.go because
// pkg/gateway references it UNCONDITIONALLY — an errors.Is classification
// that must compile in every build. Historically ingest.go was //go:build
// !lite and defining the sentinel there broke the lite/mipsle link check
// ("undefined: webrtc.ErrNoIngestVideoTrack"); ADR-067 retired the lite
// variant, so that specific trap is gone, but the rule it taught still
// holds for the architecture-gated files. Keep every exported symbol the
// gateway names in a file every build compiles,
// the same way ErrUnavailable above already is.
var ErrNoIngestVideoTrack = errors.New("no ingest video track")

// Config configures a Session's ICE behavior.
type Config struct {
	// StunServer is a STUN server URL, e.g. "stun:stun.l.google.com:19302".
	// Empty disables STUN entirely: ICE gathers host candidates only, per
	// wave-plan decision 7 (Tools.Browser.WebRTCStunServer, "" = host-only).
	StunServer string

	// MediaUDPMux and MediaTCPMux are borrowed gateway-owned demultiplexers.
	// Every capture on a fixed media port must share the SAME mux: separate
	// muxes over one socket/listener compete for packets and drop other
	// captures' ICE handshakes. The gateway closes them after its sessions.
	// nil retains ephemeral UDP / no passive TCP respectively. These settings
	// apply only to the viewer leg; loopback ingest has independent sockets.
	MediaUDPMux ice.UDPMux
	MediaTCPMux ice.TCPMux

	// PublicIPs are addresses advertised to VIEWERS as media candidates
	// (ADR-069 tier 1). Applied to the viewer leg ONLY -- see
	// Session.apiViewer. On a hosted box
	// the socket binds a private address (Fly: 172.19.x.x) that no viewer can
	// route to; without this the gateway advertises only unreachable private
	// candidates plus a server-reflexive one whose ephemeral port nothing
	// forwards.
	//
	// Sourced from gateway.public_url, which any operator behind a domain has
	// already set -- deliberately NOT a new configuration key (ADR-069: no
	// additional configuration for the user).
	PublicIPs []string
}

// InputSink receives raw input data-channel payloads exactly as sent by a
// viewer's "input" data channel, tagged with the viewerID passed to
// HandleViewerOffer. The payload is opaque JSON bytes (a BrowserInputFrame on
// the wire) -- this package does not parse it, so it never needs
// pkg/api/generated. A nil InputSink is valid; incoming input messages are
// then silently dropped (acked as an error to the sender) rather than
// panicking.
type InputSink func(viewerID string, raw []byte)

// VideoReceipt identifies the source of the latest accepted video RTP packet.
// Serial advances across feed replacement; zero means no packet was accepted.
type VideoReceipt struct {
	BindingToken uint64
	Generation   uint64
	TargetID     string
	Serial       uint64
}

// Stats is a point-in-time snapshot of a Session's relay state, as returned
// by Session.Stats() / stubbed by the lite build.
type Stats struct {
	// Viewers is the number of currently attached viewer PeerConnections.
	Viewers int
	// HasVideo/HasAudio report a currently live ingest feed of that kind.
	// Shared local tracks survive replacement, but an unfed track is not live.
	HasVideo bool
	HasAudio bool
	// VideoGeneration/VideoTargetID describe only the current live video feed.
	// They are zero/empty while an installed peer has no video track yet.
	VideoGeneration uint64
	VideoTargetID   string
	// VideoReceipt retains the latest actual packet's identity across feed
	// changes until a new packet is accepted, independently of viewer writes.
	VideoReceipt VideoReceipt
	// VideoCodec/AudioCodec are the negotiated codec MIME types (e.g.
	// "video/VP8", "audio/opus") of the most recent ingest track of that
	// kind, empty if none has arrived yet.
	VideoCodec string
	AudioCodec string
	// VideoPackets/AudioPackets count successful shared-track WriteRTP calls
	// since Session creation. An aggregate failure may follow successful
	// delivery to some viewers; zero bound viewers can also return success.
	// These cumulative counters are not proof of receiver presentation.
	VideoPackets int64
	AudioPackets int64
	// Received counters count parsed packets accepted from the current ingest
	// owner before writing to viewers; failures count aggregate writer errors.
	VideoReceivedPackets int64
	AudioReceivedPackets int64
	VideoForwardFailures int64
	AudioForwardFailures int64
	// IngestBindingToken belongs to the installed peer, not a pending offer.
	IngestBindingToken uint64
	// Input overflow counters exclude lossless dequeue-time coalescing.
	InputShedPositional    int64
	InputDroppedPositional int64
	InputDroppedDiscrete   int64
}
