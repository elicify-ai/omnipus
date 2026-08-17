//go:build !lite

package webrtc_test

import (
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

func TestSession_ViewerAnswerAdvertisesTCPCandidate(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	s := webrtc.NewSession(webrtc.Config{MediaTCP: ln, PublicIPs: []string{"203.0.113.8"}}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	enc := newFakeEncoder(t, false)
	enc.startPumping(t)
	ingestAnswer, err := s.HandleIngestOffer(nonTrickleOffer(t, enc.pc))
	require.NoError(t, err)
	require.NotEmpty(t, ingestAnswer)
	setAnswer(t, enc.pc, ingestAnswer)

	viewer := newFakeViewer(t, false)
	answer, err := s.HandleViewerOffer("v-tcp", nonTrickleOffer(t, viewer.pc))
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(answer), "tcp",
		"ADR-062 tier 2: a configured ICE-TCP listener must appear as a tcp candidate in the viewer answer")
}
