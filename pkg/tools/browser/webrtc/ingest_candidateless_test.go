package webrtc

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The SDP fragments below are verbatim excerpts of what real Chromium 152
// offered during the 2026-09-05 reproduction runs (the harness in
// pkg/tools/browser/webrtc_ingest_repro_test.go), not invented shapes.
const (
	// chromeUDPHostCandidate: an ordinary container host candidate. Note the
	// address is a real IP, NOT an mDNS "<uuid>.local" name — measured on both
	// macOS and Linux, on every one of ~35 successful connections.
	chromeUDPHostCandidate = "a=candidate:2320954543 1 udp 2122260223 172.17.0.2 41645 typ host generation 0 network-id 1"
	// chromeSrflxCandidate: the server-reflexive candidate a reachable STUN
	// server produces. Never once selected on the loopback ingest leg.
	chromeSrflxCandidate = "a=candidate:2622209565 1 udp 1686052607 202.8.29.26 58898 typ srflx raddr 172.17.0.2 rport 41645 generation 0 network-id 1"
	// chromeTCPActiveCandidate: Chrome sends one of these on EVERY offer, and
	// pion/ice discards every one of them ("Ignoring remote candidate with
	// tcpType active", agent.go). An offer carrying only this is unusable.
	chromeTCPActiveCandidate = "a=candidate:4103673399 1 tcp 1518280447 172.17.0.2 9 typ host tcptype active generation 0 network-id 1"
)

func sdpWith(candidates ...string) string {
	lines := []string{
		"v=0",
		"o=- 4611731400430051336 2 IN IP4 127.0.0.1",
		"s=-",
		"t=0 0",
		"m=video 9 UDP/TLS/RTP/SAVPF 102",
		"c=IN IP4 0.0.0.0",
		"a=ice-ufrag:abcd",
		"a=ice-pwd:0123456789abcdef0123",
	}
	lines = append(lines, candidates...)
	return strings.Join(lines, "\r\n") + "\r\n"
}

// TestUsableRemoteCandidateCount_IgnoresTCPActive pins the one judgement this
// counter makes: a TCP candidate whose tcptype is `active` does not count.
//
// The oracle is pion/ice's own behaviour, not this implementation — agent.go's
// addRemoteCandidate returns early on TCPTypeActive with "Ignoring remote
// candidate with tcpType active", because an active candidate only ever dials
// and never listens, so it can never be half of a pair we check. Chrome sends
// exactly one on every offer, so a counter that merely counted a=candidate
// lines would report an offer as usable on the strength of the single
// candidate pion is guaranteed to throw away — which is the whole failure this
// check exists to catch.
func TestUsableRemoteCandidateCount_IgnoresTCPActive(t *testing.T) {
	tests := []struct {
		name string
		sdp  string
		want int
	}{
		{"no candidates at all", sdpWith(), 0},
		{"only a tcp-active candidate", sdpWith(chromeTCPActiveCandidate), 0},
		{"one udp host candidate", sdpWith(chromeUDPHostCandidate), 1},
		{"host plus the discarded tcp-active", sdpWith(chromeUDPHostCandidate, chromeTCPActiveCandidate), 1},
		{"srflx counts too", sdpWith(chromeSrflxCandidate), 1},
		{
			"a real Chrome offer: host + srflx + tcp-active",
			sdpWith(chromeUDPHostCandidate, chromeSrflxCandidate, chromeTCPActiveCandidate),
			2,
		},
		{"empty sdp", "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, usableRemoteCandidateCount(tc.sdp))
		})
	}
}

// TestHandleIngestOffer_RejectsCandidatelessOffer is the regression guard for
// the failure reproduced on 2026-09-05 in a Linux container with no
// non-loopback interface: Chrome does not gather loopback host candidates, so
// with no other interface and no reachable STUN its offer carried zero
// candidates — 6082 bytes of valid SDP describing a connection that could not
// exist.
//
// Before this guard the gateway ANSWERED that offer, installed it as the live
// ingest connection, and surfaced the problem only as "ICE connection state ->
// failed" exactly 30 seconds later (pion's disconnectedTimeout 5s +
// failedTimeout 25s) — thirty seconds of a black panel for a fact that was
// fully determined the moment the offer arrived.
//
// The assertion is on the SENTINEL, not on message text: a caller
// distinguishing this from an ordinary negotiation failure needs errors.Is to
// work, and matching on prose would pass just as happily against a rewritten
// message that no longer wrapped the sentinel at all.
func TestHandleIngestOffer_RejectsCandidatelessOffer(t *testing.T) {
	sess := NewSession(Config{}, nil, func(string, ...any) {})
	t.Cleanup(func() { _ = sess.Close() })

	_, err := sess.HandleIngestOffer(sdpWith())
	require.Error(t, err, "an offer with no candidates must be refused, not answered")
	require.ErrorIs(t, err, ErrOfferHasNoUsableCandidates)

	_, err = sess.HandleIngestOffer(sdpWith(chromeTCPActiveCandidate))
	require.Error(t, err, "an offer carrying only a tcp-active candidate is equally unusable to pion")
	require.ErrorIs(t, err, ErrOfferHasNoUsableCandidates)
}

// TestHandleIngestOffer_CandidatelessRejectionLeavesPreviousIngestAlone
// protects the property that made the pre-existing "install only after full
// negotiation success" ordering worth having (fix-wave finding 2b): a bad
// offer must never cost a healthy ingest connection.
//
// The new early rejection returns BEFORE buildPeerConnection, which is the
// cheapest possible place — but "cheap" is not the point being asserted. The
// point is that the early return path cannot touch s.ingestPC, so an encoder
// that briefly loses its network and sends one candidate-less offer does not
// tear down the connection currently feeding every attached viewer.
func TestHandleIngestOffer_CandidatelessRejectionLeavesPreviousIngestAlone(t *testing.T) {
	sess := NewSession(Config{}, nil, func(string, ...any) {})
	t.Cleanup(func() { _ = sess.Close() })

	// Stand in for an already-installed, healthy ingest connection. A real
	// PeerConnection is used rather than nil so the identity check being
	// protected is a real one.
	pc, err := sess.buildPeerConnection(sess.api, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })
	sess.mu.Lock()
	sess.ingestPC = pc
	sess.mu.Unlock()

	_, err = sess.HandleIngestOffer(sdpWith())
	require.ErrorIs(t, err, ErrOfferHasNoUsableCandidates)

	sess.mu.Lock()
	still := sess.ingestPC
	sess.mu.Unlock()
	require.Same(t, pc, still,
		"a candidate-less offer must not evict the ingest connection that is currently feeding viewers")
}

// TestBuildPeerConnection_StunIsViewerLegOnly pins the split measured on
// 2026-09-05: the loopback ingest leg gets no STUN server, the viewer leg does.
//
// Why it matters, in numbers rather than principle. With STUN configured on the
// ingest leg and the server unroutable, pion's gathering took 5008ms — exactly
// its defaultSTUNGatherTimeout — and because this leg is non-trickle the ANSWER
// is withheld for the whole of it. End to end in a Linux container, time to
// first video frame went from ~1s to ~17.5s. The candidate it was waiting for
// was never once used: every successful connection across both platforms chose
// a host/host pair.
func TestBuildPeerConnection_StunIsViewerLegOnly(t *testing.T) {
	sess := NewSession(Config{StunServer: "stun:stun.l.google.com:19302"}, nil, func(string, ...any) {})
	t.Cleanup(func() { _ = sess.Close() })

	ingestPC, err := sess.buildPeerConnection(sess.api, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ingestPC.Close() })
	require.Empty(t, ingestPC.GetConfiguration().ICEServers,
		"the loopback ingest leg must be configured with no ICE servers — both peers are on this host")

	viewerPC, err := sess.buildPeerConnection(sess.apiViewer, true)
	require.NoError(t, err)
	t.Cleanup(func() { _ = viewerPC.Close() })
	require.Len(t, viewerPC.GetConfiguration().ICEServers, 1,
		"the viewer leg is the one with a real network between its peers and must keep its STUN server")
}

// TestErrOfferHasNoUsableCandidates_NamesTheCause guards the message itself,
// which is unusual and deliberate. This error is not read by code alone: it is
// delivered to the encoder page as an ErrorFrame and lands in
// window.__omnipusState.lastError, which is what an operator sees when live
// video will not start. "negotiation failed" would send them back to guessing,
// which is the exact position the original incident left everyone in.
func TestErrOfferHasNoUsableCandidates_NamesTheCause(t *testing.T) {
	msg := ErrOfferHasNoUsableCandidates.Error()
	require.Contains(t, msg, "no usable ICE candidate")
	require.Contains(t, msg, "non-loopback network interface",
		"the message must name the environment condition an operator can actually act on")
	require.True(t, errors.Is(ErrOfferHasNoUsableCandidates, ErrOfferHasNoUsableCandidates))
}
