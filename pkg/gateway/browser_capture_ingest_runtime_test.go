package gateway

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	"github.com/stretchr/testify/require"
)

// This capture-only integration has no Chrome capability gate or viewer seam:
// real WebSocket authentication, real relay admission and real audio/video RTP.
func TestCaptureIngestRealRelayHandshake(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, func(cfg *config.Config) { cfg.Gateway.ValidateInbound = true })
	relay := webrtc.NewSession(webrtc.Config{StunServer: ""}, nil, e2eSafeLogf(t.Name()))
	var starts int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, "capture-runtime", relay, fakeEncoderStarter(&starts, nil), nil)
	require.NoError(t, err)
	t.Cleanup(cs.Stop)
	_, err = cs.BeginFrameTransition("runtime-target", 756, 413, 1.25)
	require.NoError(t, err)
	registry := newCaptureRegistry()
	registry.set("capture-runtime", cs)
	handler := newCaptureIngestWSHandler(al, registry)
	server := httptest.NewServer(handler)
	t.Cleanup(func() { server.Close(); handler.Wait() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		encoder *e2eFakeEncoder
		err     error
	}
	completed := make(chan result)
	go func() {
		encoder, err := startE2EFakeEncoder("ws"+server.URL[len("http"):], cs.TokenHex())
		select {
		case completed <- result{encoder, err}:
		case <-ctx.Done():
			if encoder != nil {
				encoder.close()
			}
		}
	}()
	var encoder *e2eFakeEncoder
	select {
	case result := <-completed:
		require.NoError(t, result.err)
		encoder = result.encoder
	case <-time.After(e2eWait):
		t.Fatal("qualified capture handshake exceeded its bound")
	}
	t.Cleanup(encoder.close)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	encoder.startPumping(stop)
	require.Eventually(t, func() bool { stats := relay.Stats(); return stats.HasVideo && stats.HasAudio }, e2eWait, 10*time.Millisecond, "real relay must receive both offered tracks")
	epoch, token := cs.CurrentIngestBinding()
	require.NotZero(t, epoch)
	require.NotZero(t, token)
}
