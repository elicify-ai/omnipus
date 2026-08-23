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
	"net"
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

	// MediaConn is a SHARED, already-bound UDP socket that every viewer leg
	// multiplexes over (ADR-062 tier 1). nil keeps the pre-ADR-062
	// behaviour: an ephemeral port per session, which only ever works when
	// the viewer can reach the gateway directly (same host, or an open LAN).
	//
	// SHARED, and owned by the caller, deliberately. A Session exists PER
	// AGENT (capture_session.go), so a per-Session bind of the same fixed
	// port means the FIRST agent wins and every later agent silently falls
	// back to an ephemeral port -- i.e. multi-agent hosted installs would
	// have video for one agent and an unexplained failure for the rest.
	// Pion's UDP mux demultiplexes many ICE agents on one socket by ufrag,
	// so one gateway-owned socket serves them all. The caller closes it.
	//
	// WHY a fixed port is the whole ballgame for a hosted install (measured
	// 2026-08-15, Fly UAT): NO hosted provider delivers inbound UDP to an
	// UNDECLARED ephemeral port. Providers route what you declare. An
	// ephemeral port can never be declared, so ICE could never complete on a
	// hosted deployment however healthy the network was -- and Fly's network
	// was measured healthy (raw datagrams and STUN-formatted replies both
	// traversed, sizes to 1200B). Omnipus is client-to-SERVER, not
	// peer-to-peer: the gateway always has a fixed public address, so it
	// should LISTEN somewhere reachable and SAY WHERE, never hole-punch.
	MediaConn net.PacketConn

	// MediaTCP is a SHARED, already-bound TCP listener for ICE-TCP
	// (ADR-062 tier 2). nil keeps UDP-only. Same sharing rule as MediaConn:
	// one gateway-owned listener, muxed by ufrag, because a Session is per-agent.
	// Used when the viewer's network drops raw UDP (VPN system extensions).
	MediaTCP net.Listener

	// PublicIPs are addresses advertised to VIEWERS as media candidates
	// (ADR-062 tier 1). Applied to the viewer leg ONLY -- see
	// Session.apiViewer. On a hosted box
	// the socket binds a private address (Fly: 172.19.x.x) that no viewer can
	// route to; without this the gateway advertises only unreachable private
	// candidates plus a server-reflexive one whose ephemeral port nothing
	// forwards.
	//
	// Sourced from gateway.public_url, which any operator behind a domain has
	// already set -- deliberately NOT a new configuration key (ADR-062: no
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
