package gateway

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	relay "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

type observedOwnerUDPMux struct {
	ice.UDPMux
	closes      atomic.Int32
	beforeClose func()
}

func (m *observedOwnerUDPMux) Close() error {
	m.closes.Add(1)
	m.beforeClose()
	return m.UDPMux.Close()
}

type observedOwnerTCPMux struct {
	ice.TCPMux
	closes atomic.Int32
}

func (m *observedOwnerTCPMux) Close() error {
	m.closes.Add(1)
	return m.TCPMux.Close()
}

func TestSharedMediaMuxGatewayOwnership(t *testing.T) {
	// Reserve then release local ports, matching the existing media-port tests.
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	require.NoError(t, err)
	udpPort := udp.LocalAddr().(*net.UDPAddr).Port
	require.NoError(t, udp.Close())
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	tcpPort := tcp.Addr().(*net.TCPAddr).Port
	require.NoError(t, tcp.Close())
	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCMediaUDPPort = udpPort
	cfg.Tools.Browser.WebRTCMediaUDPBindAddress = "127.0.0.1"
	cfg.Tools.Browser.WebRTCMediaTCPPort = tcpPort
	h := &BrowserWSHandler{captures: newCaptureRegistry()}
	t.Cleanup(h.closeMediaTransport)
	var wg sync.WaitGroup
	udps := make([]ice.UDPMux, 8)
	tcps := make([]ice.TCPMux, 8)
	for i := range udps {
		wg.Go(func() { udps[i] = h.sharedMediaUDPMux(cfg); tcps[i] = h.sharedMediaTCPMux(cfg) })
	}
	wg.Wait()
	require.NotNil(t, udps[0])
	require.NotNil(t, tcps[0])
	for i := range udps {
		require.Same(t, udps[0], udps[i], "concurrent captures borrow the one UDP router")
		require.Same(t, tcps[0], tcps[i], "concurrent captures borrow the one TCP router")
	}
	cs, err := browser.NewCaptureSession(nil, "agent", "panel", relay.Config{MediaUDPMux: udps[0], MediaTCPMux: tcps[0]}, nil, nil)
	require.NoError(t, err)
	h.captures.set("workspace", cs)
	udpOwner := &observedOwnerUDPMux{UDPMux: udps[0], beforeClose: func() {
		select {
		case <-cs.Done():
		default:
			t.Error("gateway closed transport before stopping its capture")
		}
	}}
	tcpOwner := &observedOwnerTCPMux{TCPMux: tcps[0]}
	h.mediaUDPMux, h.mediaTCPMux = udpOwner, tcpOwner
	services := &services{browserWS: h}
	stopAndCleanupServices(services, time.Second, true)
	require.Equal(t, int32(0), udpOwner.closes.Load(), "reload preserves active transport")
	require.Equal(t, int32(0), tcpOwner.closes.Load(), "reload preserves active transport")
	select {
	case <-cs.Done():
		t.Fatal("reload stopped capture")
	default:
	}
	stopAndCleanupServices(services, time.Second, false)
	h.closeMediaTransport()
	require.Equal(t, int32(1), udpOwner.closes.Load(), "owner closes UDP once")
	require.Equal(t, int32(1), tcpOwner.closes.Load(), "owner closes TCP once")
	_, err = h.mediaConn.WriteTo([]byte("closed"), h.mediaConn.LocalAddr())
	require.ErrorIs(t, err, net.ErrClosed)
	_, err = h.mediaTCP.Accept()
	require.ErrorIs(t, err, net.ErrClosed)
	require.Nil(t, h.sharedMediaUDPMux(cfg), "shutdown cannot create fresh UDP router")
	require.Nil(t, h.sharedMediaTCPMux(cfg), "shutdown cannot create fresh TCP router")
	_, err = h.ensureCaptureSession(nil, "late", "panel", cfg)
	require.EqualError(t, err, "browser media transport is closed")
	require.False(t, errors.Is(err, net.ErrClosed), "late creation is rejected at lifecycle admission")
}
