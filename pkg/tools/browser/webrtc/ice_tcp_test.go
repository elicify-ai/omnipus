package webrtc_test

import (
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// candidateLines returns only the a=candidate: lines of an SDP. Asserting on
// the raw SDP is a trap: every Pion answer carries a=rtcp-mux / a=rtcp-rsize /
// a=rtcp-fb:, and "rtcp" CONTAINS "tcp", so a substring check for "tcp" passes
// even with the ICE-TCP mux removed. That exact vacuous assertion shipped here
// on 2026-08-17 and gave tier 2 zero real coverage.
func candidateLines(sdp string) []string {
	var out []string
	for _, l := range strings.Split(sdp, "\r\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "a=candidate:") {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

func newIngestedSession(t *testing.T, cfg webrtc.Config) *webrtc.Session {
	t.Helper()
	s := webrtc.NewSession(cfg, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	enc := newFakeEncoder(t, false)
	enc.startPumping(t)
	answer, err := s.HandleIngestOffer(nonTrickleOffer(t, enc.pc))
	require.NoError(t, err)
	require.NotEmpty(t, answer)
	setAnswer(t, enc.pc, answer)
	return s
}

func TestSession_ViewerAnswerAdvertisesTCPCandidate(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	s := newIngestedSession(t, webrtc.Config{MediaTCP: ln, PublicIPs: []string{"203.0.113.8"}})
	viewer := newFakeViewer(t, false)
	answer, err := s.HandleViewerOffer("v-tcp", nonTrickleOffer(t, viewer.pc))
	require.NoError(t, err)

	cands := candidateLines(answer)
	require.NotEmpty(t, cands, "answer must carry ICE candidates")
	var tcpPassive bool
	for _, c := range cands {
		if strings.Contains(c, " tcp ") && strings.Contains(c, "tcptype passive") {
			tcpPassive = true
		}
	}
	require.True(t, tcpPassive,
		"ADR-069 tier 2: a configured ICE-TCP listener must appear as a passive tcp candidate; got %v", cands)
}

// TestSession_TCPOnlyWithStun_StillNegotiates is the regression guard for the
// gate mismatch found in review on 2026-08-17: ICE-Lite was enabled whenever a
// media socket existed, but STUN was only skipped when a UDP socket or a public
// IP existed. A TCP-only install therefore built a lite agent WITH an ICE URL,
// which pion rejects (ErrUselessUrlsProvided) -- CreateOffer failed outright,
// so the viewer got no offer at all rather than a nameable ICE failure.
func TestSession_TCPOnlyWithStun_StillNegotiates(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	s := newIngestedSession(t, webrtc.Config{
		MediaTCP:   ln,
		StunServer: "stun:stun.l.google.com:19302",
	})
	viewer := newFakeViewer(t, false)
	answer, err := s.HandleViewerOffer("v-tcp-stun", nonTrickleOffer(t, viewer.pc))
	require.NoError(t, err, "a TCP-only session with STUN configured must still produce an answer")
	require.NotEmpty(t, answer)
}

// TestSession_HostedViewerIsLiteAndStunFree pins the two settings that must
// agree: with a declared public address the viewer leg is ICE-Lite and gets no
// STUN URL, so it advertises only the address we told the viewer to use.
func TestSession_HostedViewerIsLiteAndStunFree(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	s := newIngestedSession(t, webrtc.Config{
		MediaConn:  conn,
		PublicIPs:  []string{"203.0.113.9"},
		StunServer: "stun:stun.l.google.com:19302",
	})
	viewer := newFakeViewer(t, false)
	answer, err := s.HandleViewerOffer("v-hosted", nonTrickleOffer(t, viewer.pc))
	require.NoError(t, err)
	require.Contains(t, answer, "a=ice-lite", "a hosted viewer leg must announce ICE-Lite")
	for _, c := range candidateLines(answer) {
		require.NotContains(t, c, "typ srflx",
			"ICE-Lite hosted leg must not advertise server-reflexive candidates: %s", c)
	}
}

// TestSession_SelfHostedNoPublicIP_KeepsSrflx guards the other direction: an
// operator who pins a media port for a firewall forward but declares NO public
// address still needs server-reflexive candidates (their NAT mapping is the
// only way in), so that install must NOT be lite and must keep its STUN URL.
func TestSession_SelfHostedNoPublicIP_KeepsSrflx(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	s := newIngestedSession(t, webrtc.Config{
		MediaConn:  conn,
		StunServer: "stun:stun.l.google.com:19302",
	})
	viewer := newFakeViewer(t, false)
	answer, err := s.HandleViewerOffer("v-selfhost", nonTrickleOffer(t, viewer.pc))
	require.NoError(t, err)
	require.NotContains(t, answer, "a=ice-lite",
		"a fixed media port without a declared public address must NOT turn the leg into a lite agent")
}
