package webrtc_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	pion "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	relay "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// This encoder writes only the explicit clock anchors below. Its empty
// interceptor registry cannot add a second, uncontrolled sender-report source.
func newClockEncoder(t *testing.T) (*fakeEncoder, *atomic.Int32) {
	t.Helper()
	engine := &pion.MediaEngine{}
	require.NoError(t, engine.RegisterDefaultCodecs())
	settings := pion.SettingEngine{}
	settings.SetIncludeLoopbackCandidate(true)
	api := pion.NewAPI(pion.WithMediaEngine(engine), pion.WithInterceptorRegistry(&interceptor.Registry{}), pion.WithSettingEngine(settings))
	pc, err := api.NewPeerConnection(pion.Configuration{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })
	enc := &fakeEncoder{pc: pc}
	feedback := &atomic.Int32{}
	for _, kind := range []pion.RTPCodecType{pion.RTPCodecTypeVideo, pion.RTPCodecTypeAudio} {
		mime := pion.MimeTypeVP8
		if kind == pion.RTPCodecTypeAudio {
			mime = pion.MimeTypeOpus
		}
		track, err := pion.NewTrackLocalStaticSample(pion.RTPCodecCapability{MimeType: mime}, kind.String(), "clock-source")
		require.NoError(t, err)
		sender, err := pc.AddTrack(track)
		require.NoError(t, err)
		if kind == pion.RTPCodecTypeVideo {
			enc.video = track
		} else {
			enc.audio = track
		}
		go func() {
			for {
				packets, _, err := sender.ReadRTCP()
				if err != nil {
					return
				}
				for _, packet := range packets {
					if rr, ok := packet.(*rtcp.ReceiverReport); ok && len(rr.Reports) > 0 {
						feedback.Add(1)
					}
				}
			}
		}()
	}
	return enc, feedback
}

// Record exact received video sequence numbers so feedback preservation is
// verified by a real retransmission, not merely by an SDP declaration.
type clockViewer struct {
	*fakeViewer
	packetsMu      sync.Mutex
	videoSequences map[uint16]int
	latestVideo    uint16
}

func newClockViewer(t *testing.T) *clockViewer {
	t.Helper()
	pc := newFakePeer(t)
	for _, kind := range []pion.RTPCodecType{pion.RTPCodecTypeVideo, pion.RTPCodecTypeAudio} {
		_, err := pc.AddTransceiverFromKind(kind, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly})
		require.NoError(t, err)
	}
	v := &clockViewer{fakeViewer: &fakeViewer{pc: pc}, videoSequences: make(map[uint16]int)}
	pc.OnTrack(func(track *pion.TrackRemote, receiver *pion.RTPReceiver) {
		if track.Kind() == pion.RTPCodecTypeVideo {
			v.videoSSRC.Store(uint32(track.SSRC()))
		} else {
			v.audioSSRC.Store(uint32(track.SSRC()))
		}
		go func() {
			for {
				packets, _, err := receiver.ReadRTCP()
				if err != nil {
					return
				}
				for _, packet := range packets {
					if sr, ok := packet.(*rtcp.SenderReport); ok {
						v.srMu.Lock()
						v.srs = append(v.srs, *sr)
						v.srMu.Unlock()
					}
				}
			}
		}()
		go func() {
			for {
				packet, _, err := track.ReadRTP()
				if err != nil {
					return
				}
				if track.Kind() == pion.RTPCodecTypeVideo {
					v.packetsMu.Lock()
					v.videoSequences[packet.SequenceNumber]++
					v.latestVideo = packet.SequenceNumber
					v.packetsMu.Unlock()
					v.videoPkts.Add(1)
				} else {
					v.audioPkts.Add(1)
				}
			}
		}()
	})
	return v
}

func TestSessionViewerUsesOnlyEncoderSenderClock(t *testing.T) {
	for _, compound := range []bool{false, true} {
		name := "separate_reports"
		if compound {
			name = "compound_with_foreign_report"
		}
		t.Run(name, func(t *testing.T) { testViewerEncoderClock(t, compound) })
	}
}

func testViewerEncoderClock(t *testing.T, compound bool) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })
	enc, receiverReports := newClockEncoder(t)
	enc.startPumping(t)
	answer, err := sess.HandleIngestOffer(nonTrickleOffer(t, enc.pc))
	require.NoError(t, err)
	setAnswer(t, enc.pc, answer)
	viewer := newClockViewer(t)
	answer, err = sess.HandleViewerOffer("clock-viewer", nonTrickleOffer(t, viewer.pc))
	require.NoError(t, err)
	setAnswer(t, viewer.pc, answer)
	waitCond(t, testWait, "both media kinds at viewer", func() bool {
		return viewer.videoPkts.Load() > 5 && viewer.audioPkts.Load() > 5
	})

	viewer.packetsMu.Lock()
	sequence := viewer.latestVideo
	receivedBefore := viewer.videoSequences[sequence]
	viewer.packetsMu.Unlock()
	require.NoError(t, viewer.pc.WriteRTCP([]rtcp.Packet{&rtcp.TransportLayerNack{MediaSSRC: viewer.videoSSRC.Load(), Nacks: []rtcp.NackPair{{PacketID: sequence}}}}))
	waitCond(t, testWait, "viewer NACK to retransmit its exact cached RTP packet", func() bool {
		viewer.packetsMu.Lock()
		defer viewer.packetsMu.Unlock()
		return viewer.videoSequences[sequence] > receivedBefore
	})

	// Independent RTP offsets share the same two NTP anchors. Every observed
	// report must originate here, not from gateway packet-arrival timing.
	const anchor = uint64(0x1234567800000000)
	var reports [2][]rtcp.Packet
	want := make(map[uint32]map[uint64]rtcp.SenderReport)
	for _, sender := range enc.pc.GetSenders() {
		kind := sender.Track().Kind()
		params := sender.GetParameters()
		require.Len(t, params.Encodings, 1)
		outSSRC, baseRTP, rate := viewer.videoSSRC.Load(), uint32(900000), uint32(90000)
		if kind == pion.RTPCodecTypeAudio {
			outSSRC, baseRTP, rate = viewer.audioSSRC.Load(), 240000, 48000
		}
		require.NotZero(t, outSSRC)
		want[outSSRC] = make(map[uint64]rtcp.SenderReport)
		for phase := range reports {
			sr := rtcp.SenderReport{SSRC: uint32(params.Encodings[0].SSRC), NTPTime: anchor + (uint64(phase) << 32),
				RTPTime: baseRTP + uint32(phase)*rate, PacketCount: uint32(100 + phase), OctetCount: uint32(50000 + phase)}
			reports[phase] = append(reports[phase], &sr)
			expected := sr
			expected.SSRC = outSSRC
			want[outSSRC][sr.NTPTime] = expected
		}
	}
	require.Len(t, want, 2, "both independently clocked media kinds are negotiated")
	// More than two production one-second sender intervals. Repeating authored
	// reports tolerates loopback packet loss without allowing a foreign clock.
	for i := 0; i < 24; i++ {
		if compound {
			packets := append([]rtcp.Packet{}, reports[i/12]...)
			packets = append(packets, &rtcp.SenderReport{SSRC: 0, NTPTime: 17, RTPTime: 29})
			require.NoError(t, enc.pc.WriteRTCP(packets))
		} else {
			for _, report := range reports[i/12] {
				require.NoError(t, enc.pc.WriteRTCP([]rtcp.Packet{report}))
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	seen := make(map[uint32]map[uint64]bool)
	for _, got := range viewer.senderReports() {
		expected, ok := want[got.SSRC][got.NTPTime]
		if !ok {
			t.Errorf("viewer received competing clock for SSRC %d: NTP=%d RTP=%d; only authored encoder anchors are allowed", got.SSRC, got.NTPTime, got.RTPTime)
			continue
		}
		assert.Equal(t, expected, got, "source clock and counters survive outgoing SSRC translation")
		if seen[got.SSRC] == nil {
			seen[got.SSRC] = make(map[uint64]bool)
		}
		seen[got.SSRC][got.NTPTime] = true
	}
	for ssrc := range want {
		require.Len(t, seen[ssrc], 2, "both encoder anchors must reach each media kind")
	}
	require.Positive(t, receiverReports.Load(), "ingest receiver feedback must remain enabled")
}
