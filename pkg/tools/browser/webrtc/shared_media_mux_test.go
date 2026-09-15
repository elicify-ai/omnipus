package webrtc_test

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	pion "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"

	relay "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// The gateway transport outlives individual captures. ADR-069's shared port
// requires one demultiplexer, not merely one socket: retired/concurrent
// captures must never compete to consume another capture's ICE packets.
// Real sockets and Pion peers stay in the test boundary. The wrappers only
// count pending reads/accepts; they do not select or manufacture packets.
type observedPacketConn struct {
	net.PacketConn
	entered chan struct{}
}

func (c *observedPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	c.entered <- struct{}{}
	return c.PacketConn.ReadFrom(b)
}

type observedListener struct {
	net.Listener
	entered chan struct{}
}

func (l *observedListener) Accept() (net.Conn, error) {
	l.entered <- struct{}{}
	return l.Listener.Accept()
}

func TestSession_SharedMediaHasOneReaderAcrossCaptures(t *testing.T) {
	for _, transport := range []string{"udp", "tcp"} {
		t.Run(transport, func(t *testing.T) {
			entered := make(chan struct{}, 8)
			cfg := relay.Config{}
			if transport == "udp" {
				conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
				require.NoError(t, err)
				t.Cleanup(func() { _ = conn.Close() })
				cfg.MediaUDPMux = newTestUDPMux(t, &observedPacketConn{PacketConn: conn, entered: entered})
			} else {
				ln, err := net.Listen("tcp4", "127.0.0.1:0")
				require.NoError(t, err)
				t.Cleanup(func() { _ = ln.Close() })
				cfg.MediaTCPMux = newTestTCPMux(t, &observedListener{Listener: ln, entered: entered})
			}
			first := relay.NewSession(cfg, nil, nil)
			require.Eventually(t, func() bool { return len(entered) == 1 }, time.Second, time.Millisecond, "owner reader must start")
			<-entered
			require.NoError(t, first.Close())
			second := relay.NewSession(cfg, nil, nil)
			t.Cleanup(func() { _ = second.Close() })
			select {
			case <-entered:
				t.Fatal("replacement capture started a competing reader on the gateway transport")
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

func TestSession_SharedMediaSurvivesSequentialAndConcurrentCaptures(t *testing.T) {
	for _, transport := range []string{"udp", "tcp"} {
		t.Run(transport, func(t *testing.T) {
			cfg := relay.Config{PublicIPs: []string{"127.0.0.1"}}
			network := pion.NetworkTypeUDP4
			if transport == "udp" {
				conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
				require.NoError(t, err)
				t.Cleanup(func() { _ = conn.Close() })
				cfg.MediaUDPMux = newTestUDPMux(t, conn)
			} else {
				network = pion.NetworkTypeTCP4
				ln, err := net.Listen("tcp4", "127.0.0.1:0")
				require.NoError(t, err)
				t.Cleanup(func() { _ = ln.Close() })
				cfg.MediaTCPMux = newTestTCPMux(t, ln)
			}
			connect := func() (*relay.Session, *atomic.Int64) {
				s := newIngestedSession(t, cfg)
				se := pion.SettingEngine{}
				se.SetIncludeLoopbackCandidate(true)
				se.SetNetworkTypes([]pion.NetworkType{network})
				pc, err := pion.NewAPI(pion.WithSettingEngine(se)).NewPeerConnection(pion.Configuration{})
				require.NoError(t, err)
				t.Cleanup(func() { _ = pc.Close() })
				_, err = pc.AddTransceiverFromKind(pion.RTPCodecTypeVideo, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly})
				require.NoError(t, err)
				packets := &atomic.Int64{}
				pc.OnTrack(func(track *pion.TrackRemote, _ *pion.RTPReceiver) {
					go func() {
						for {
							if _, _, readErr := track.ReadRTP(); readErr != nil {
								return
							}
							packets.Add(1)
						}
					}()
				})
				answer, err := s.HandleViewerOffer("viewer", nonTrickleOffer(t, pc))
				require.NoError(t, err)
				setAnswer(t, pc, answer)
				require.Eventually(t, func() bool { return packets.Load() >= 3 }, 5*time.Second, 10*time.Millisecond, "fresh capture must deliver video over %s", transport)
				return s, packets
			}
			first, _ := connect()
			require.NoError(t, first.Close())
			second, secondPackets := connect()
			third, thirdPackets := connect()
			before := secondPackets.Load()
			require.Eventually(t, func() bool { return secondPackets.Load() > before }, time.Second, 10*time.Millisecond, "second capture remains live beside third")
			require.NoError(t, second.Close())
			before = thirdPackets.Load()
			require.Eventually(t, func() bool { return thirdPackets.Load() > before }, time.Second, 10*time.Millisecond, "closing another capture preserves current video")
			fourth, _ := connect()
			require.NoError(t, third.Close())
			require.NoError(t, fourth.Close())
		})
	}
}

func newTestUDPMux(t *testing.T, conn net.PacketConn) ice.UDPMux {
	t.Helper()
	mux := pion.NewICEUDPMux(nil, conn)
	t.Cleanup(func() { require.NoError(t, mux.Close()) })
	return mux
}
func newTestTCPMux(t *testing.T, ln net.Listener) ice.TCPMux {
	t.Helper()
	mux := pion.NewICETCPMux(nil, ln, 8)
	t.Cleanup(func() { require.NoError(t, mux.Close()) })
	return mux
}
