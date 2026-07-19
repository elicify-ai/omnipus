package main

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// relay is the SFU-style forwarding state shared by the ingest handler (the
// headless-Chrome encoder connects here) and the viewer handler (external
// browsers connect here to watch). Exactly one active ingest connection is
// supported for this spike; multiple viewers may attach concurrently by
// sharing the same two TrackLocalStaticRTP objects (Pion supports binding
// one local track to many RTPSenders/PeerConnections -- the standard
// broadcast/SFU pattern, no transcoding involved).
type relay struct {
	api *webrtc.API

	// bridge is the WS link to run.js's CDP pipe on the encoder Chrome --
	// Q4 addition. Every viewer's "input" data channel dispatches through
	// this same shared bridge (single captured tab, single CDP session).
	bridge *inputBridge

	mu         sync.Mutex
	ingestPC   *webrtc.PeerConnection
	videoTrack *webrtc.TrackLocalStaticRTP
	audioTrack *webrtc.TrackLocalStaticRTP
	videoSSRC  webrtc.SSRC
	videoCodec string
	audioCodec string

	videoPktCount atomic.Int64
	audioPktCount atomic.Int64
	viewerCount   atomic.Int64
	pliBursting   atomic.Bool
}

// newRelay builds the shared Pion API: an explicit MediaEngine with the
// default codec set registered (VP8/VP9/H264 + Opus, among others) so that
// Chrome's tabCapture-derived offer negotiates cleanly, and Interceptors
// left at Pion's registry defaults (NACK/RTCP-reports) which is what lets
// getStats() on the viewer side report framesDecoded/audioLevel etc.
func newRelay() (*relay, error) {
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("register default codecs: %w", err)
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil {
		return nil, fmt.Errorf("register default interceptors: %w", err)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir))
	return &relay{api: api, bridge: newInputBridge()}, nil
}

// buildPeerConnection returns a PeerConnection configured with the same
// "DEFAULT" STUN config Q1 validated in-pod (public Google STUN, normal UDP
// ICE) -- this is the mode whose external traversal result is still
// pending from the operator per wv1-spike-results.md, so ingest and viewer
// connections both use it to keep that evidence comparable.
func (rl *relay) buildPeerConnection() (*webrtc.PeerConnection, error) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}
	pc, err := rl.api.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("NewPeerConnection: %w", err)
	}
	return pc, nil
}

// attachIngestTrack is called from the ingest PeerConnection's OnTrack
// callback. It creates a local pass-through track (raw RTP copy, no
// transcode), stores it for viewers to attach to, and pumps RTP packets
// from the remote track to the local one until the remote track ends.
func (rl *relay) attachIngestTrack(prefix string, remote *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
	codec := remote.Codec()
	local, err := webrtc.NewTrackLocalStaticRTP(codec.RTPCodecCapability, remote.Kind().String(), "wv1-relay")
	if err != nil {
		serverLog.Add("%s attachIngestTrack: NewTrackLocalStaticRTP(%s) failed: %v", prefix, remote.Kind(), err)
		return
	}

	rl.mu.Lock()
	switch remote.Kind() {
	case webrtc.RTPCodecTypeVideo:
		rl.videoTrack = local
		rl.videoSSRC = remote.SSRC()
		rl.videoCodec = codec.MimeType
	case webrtc.RTPCodecTypeAudio:
		rl.audioTrack = local
		rl.audioCodec = codec.MimeType
	}
	rl.mu.Unlock()

	serverLog.Add("%s ingest track arrived: kind=%s codec=%s clockRate=%d ssrc=%d payloadType=%d",
		prefix, remote.Kind(), codec.MimeType, codec.ClockRate, remote.SSRC(), remote.PayloadType())

	// Drain RTCP on the receiver so its buffer never blocks (standard Pion
	// pattern for a track we only ever read from); the packets themselves
	// aren't otherwise useful for this spike.
	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, err := receiver.Read(rtcpBuf); err != nil {
				return
			}
		}
	}()

	rtpBuf := make([]byte, 1500)
	var lastLog time.Time
	for {
		n, _, err := remote.Read(rtpBuf)
		if err != nil {
			if err == io.EOF {
				serverLog.Add("%s ingest track ended (EOF): kind=%s", prefix, remote.Kind())
			} else {
				serverLog.Add("%s ingest track read error, stopping forward: kind=%s err=%v", prefix, remote.Kind(), err)
			}
			return
		}
		if _, err := local.Write(rtpBuf[:n]); err != nil {
			// ErrClosedPipe just means no viewer is currently bound to this
			// local track yet -- not an error worth logging per-packet.
			if err != io.ErrClosedPipe {
				serverLog.Add("%s forward write failed: kind=%s err=%v", prefix, remote.Kind(), err)
			}
			continue
		}
		var count int64
		switch remote.Kind() {
		case webrtc.RTPCodecTypeVideo:
			count = rl.videoPktCount.Add(1)
		case webrtc.RTPCodecTypeAudio:
			count = rl.audioPktCount.Add(1)
		}
		if time.Since(lastLog) > 5*time.Second {
			serverLog.Add("%s RTP forward progress: kind=%s packets=%d", prefix, remote.Kind(), count)
			lastLog = time.Now()
		}
	}
}

// waitForTracks polls for both the video and audio local tracks to exist
// (i.e. the encoder has connected and Pion has seen both OnTrack events),
// up to timeout. Needed because a viewer may hit /viewer-offer before the
// headless-Chrome encoder has finished ingesting.
func (rl *relay) waitForTracks(timeout time.Duration) (video, audio *webrtc.TrackLocalStaticRTP, ok bool) {
	deadline := time.Now().Add(timeout)
	for {
		rl.mu.Lock()
		v, a := rl.videoTrack, rl.audioTrack
		rl.mu.Unlock()
		if v != nil && a != nil {
			return v, a, true
		}
		if time.Now().After(deadline) {
			return v, a, false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// sendPLI asks the encoder (ingest PeerConnection) for a fresh keyframe by
// writing a PictureLossIndication RTCP packet referencing the video track's
// media SSRC. Audio (Opus) never needs this.
func (rl *relay) sendPLI(prefix string) {
	rl.mu.Lock()
	pc := rl.ingestPC
	ssrc := rl.videoSSRC
	rl.mu.Unlock()
	if pc == nil || ssrc == 0 {
		return
	}
	if err := pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(ssrc)}}); err != nil {
		serverLog.Add("%s PLI send failed: %v", prefix, err)
		return
	}
	serverLog.Add("%s PLI sent to encoder (ssrc=%d)", prefix, ssrc)
}

// pliBurstForNewViewer sends an immediate PLI plus one every 3s for 15s so a
// late-joining viewer's decoder gets a keyframe promptly. Guarded so
// overlapping viewer joins don't stack unbounded goroutines -- a burst
// already in flight just covers the new joiner too since it's a shared,
// short window.
func (rl *relay) pliBurstForNewViewer(prefix string) {
	if !rl.pliBursting.CompareAndSwap(false, true) {
		// A burst is already running; it will cover this viewer.
		rl.sendPLI(prefix)
		return
	}
	go func() {
		defer rl.pliBursting.Store(false)
		rl.sendPLI(prefix)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			<-ticker.C
			rl.sendPLI(prefix)
		}
	}()
}
