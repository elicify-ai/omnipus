// Package webrtc_test exercises Session end-to-end with two IN-PROCESS Pion
// PeerConnections standing in for the real browser endpoints -- one as the
// headless-Chrome tabCapture "encoder" (offers synthetic video+audio),
// one as an external-browser "viewer" (recvonly + an "input" data channel).
// No Chrome, no network beyond loopback/host candidates: CI-safe.
package webrtc_test

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtcp"
	pion "github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	relay "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

const testWait = 20 * time.Second

// safeLogf returns a log function safe to pass as Session's logf even though
// Session's internal goroutines (ICE/DTLS teardown callbacks in particular)
// are asynchronous and can fire after Close() returns -- including after the
// TEST FUNCTION ITSELF has already completed. t.Logf panics if called after
// its test has finished; this writes straight to stderr instead, which has
// no such lifecycle constraint. (Verbosity is then only visible piping -v
// output directly rather than go test's per-test buffering, an acceptable
// trade for not flaking the suite.)
func safeLogf(t *testing.T) func(string, ...any) {
	prefix := t.Name()
	return func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "["+prefix+"] "+format+"\n", args...)
	}
}

// --- fixtures -----------------------------------------------------------

// newFakePeer returns a bare Pion PeerConnection (default codecs +
// interceptors, host-only/loopback-included ICE via the top-level
// pion.NewPeerConnection constructor) standing in for either the encoder or
// a viewer browser.
func newFakePeer(t *testing.T) *pion.PeerConnection {
	t.Helper()
	pc, err := pion.NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatalf("NewPeerConnection: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	return pc
}

// nonTrickleOffer creates an offer, sets it locally, and waits for ICE
// gathering to complete before returning the full offer SDP -- mirroring the
// non-trickle contract HandleIngestOffer/HandleViewerOffer expect.
func nonTrickleOffer(t *testing.T, pc *pion.PeerConnection) string {
	t.Helper()
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	gatherComplete := pion.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("SetLocalDescription(offer): %v", err)
	}
	select {
	case <-gatherComplete:
	case <-time.After(testWait):
		t.Fatal("offerer ICE gathering did not complete in time")
	}
	local := pc.LocalDescription()
	if local == nil {
		t.Fatal("nil LocalDescription after gathering")
	}
	return local.SDP
}

func setAnswer(t *testing.T, pc *pion.PeerConnection, answerSDP string) {
	t.Helper()
	err := pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answerSDP})
	if err != nil {
		t.Fatalf("SetRemoteDescription(answer): %v", err)
	}
}

// waitCond polls cond until it returns true or timeout elapses.
func waitCond(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// dummyPayload stands in for an encoded VP8/Opus frame. Packet FLOW is what
// this test suite verifies (per the W1-C task: "packet flow matters,
// decodability doesn't"), not real decodability, so any non-empty byte
// string that a Payloader can chunk works.
var dummyPayload = []byte("wv1c-fake-encoded-media-frame-payload-0123456789")

// pumpSamples writes dummyPayload to track at ~30fps until stop is closed.
// Errors are ignored -- a persistent failure surfaces as the caller's
// waitCond on received-packet-count timing out, which is a clear test
// failure on its own.
func pumpSamples(track *pion.TrackLocalStaticSample, stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(33 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = track.WriteSample(media.Sample{Data: dummyPayload, Duration: 33 * time.Millisecond})
			}
		}
	}()
}

// fakeEncoder is a Pion PeerConnection playing the role of the headless-
// Chrome tabCapture encoder page: it offers one video (VP8) track and,
// optionally, one audio (Opus) track.
type fakeEncoder struct {
	pc    *pion.PeerConnection
	video *pion.TrackLocalStaticSample
	audio *pion.TrackLocalStaticSample // nil if withAudio is false
}

func newFakeEncoder(t *testing.T, withAudio bool) *fakeEncoder {
	t.Helper()
	pc := newFakePeer(t)

	video, err := pion.NewTrackLocalStaticSample(
		pion.RTPCodecCapability{MimeType: pion.MimeTypeVP8}, "video", "wv1c-test",
	)
	if err != nil {
		t.Fatalf("NewTrackLocalStaticSample(video): %v", err)
	}
	if _, err := pc.AddTrack(video); err != nil {
		t.Fatalf("AddTrack(video): %v", err)
	}

	fe := &fakeEncoder{pc: pc, video: video}

	if withAudio {
		audio, err := pion.NewTrackLocalStaticSample(
			pion.RTPCodecCapability{MimeType: pion.MimeTypeOpus}, "audio", "wv1c-test",
		)
		if err != nil {
			t.Fatalf("NewTrackLocalStaticSample(audio): %v", err)
		}
		if _, err := pc.AddTrack(audio); err != nil {
			t.Fatalf("AddTrack(audio): %v", err)
		}
		fe.audio = audio
	}

	return fe
}

// videoSender returns the RTPSender this encoder's PeerConnection negotiated
// for its video track -- used to observe RTCP arriving FROM the Session
// (i.e. PLI forwarded by fix-wave finding 1's forwardPLIThrottled) the same
// way the real headless-Chrome encoder page would.
func (fe *fakeEncoder) videoSender(t *testing.T) *pion.RTPSender {
	t.Helper()
	for _, sender := range fe.pc.GetSenders() {
		if tr := sender.Track(); tr != nil && tr.Kind() == pion.RTPCodecTypeVideo {
			return sender
		}
	}
	t.Fatal("fakeEncoder: no video RTPSender found")
	return nil
}

// startPumping starts sample pumps for every track this encoder has,
// stopping them when the test ends.
func (fe *fakeEncoder) startPumping(t *testing.T) {
	t.Helper()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	pumpSamples(fe.video, stop)
	if fe.audio != nil {
		pumpSamples(fe.audio, stop)
	}
}

// fakeViewer is a Pion PeerConnection playing the role of an external
// browser viewer: recvonly video (+ optional audio) and an "input" data
// channel it opens itself (mirroring the real browser's role as DC
// initiator, per wave-plan decision 6 / the Q4 spike).
type fakeViewer struct {
	pc        *pion.PeerConnection
	videoPkts atomic.Int64
	audioPkts atomic.Int64
	inputDC   *pion.DataChannel
	dcOpen    chan struct{}
	acksMu    sync.Mutex
	acks      [][]byte

	// videoSSRC/audioSSRC record the SSRC this viewer observed on each
	// TrackRemote's OnTrack firing -- i.e. the SSRC the Session negotiated
	// for THIS viewer's own binding of the shared local track (fix-wave
	// finding 2's forwardSenderReport rewrites a forwarded Sender Report's
	// SSRC to exactly this value per viewer).
	videoSSRC atomic.Uint32
	audioSSRC atomic.Uint32

	// srMu/srs records every RTCP Sender Report this viewer's RTPReceiver
	// observes (fix-wave finding 2's Go<->Go proof).
	srMu sync.Mutex
	srs  []rtcp.SenderReport
}

func newFakeViewer(t *testing.T, wantAudio bool) *fakeViewer {
	t.Helper()
	pc := newFakePeer(t)

	recvonly := pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}
	if _, err := pc.AddTransceiverFromKind(pion.RTPCodecTypeVideo, recvonly); err != nil {
		t.Fatalf("AddTransceiverFromKind(video): %v", err)
	}
	if wantAudio {
		if _, err := pc.AddTransceiverFromKind(pion.RTPCodecTypeAudio, recvonly); err != nil {
			t.Fatalf("AddTransceiverFromKind(audio): %v", err)
		}
	}

	fv := &fakeViewer{pc: pc, dcOpen: make(chan struct{})}

	pc.OnTrack(func(track *pion.TrackRemote, receiver *pion.RTPReceiver) {
		switch track.Kind() {
		case pion.RTPCodecTypeVideo:
			fv.videoSSRC.Store(uint32(track.SSRC()))
		case pion.RTPCodecTypeAudio:
			fv.audioSSRC.Store(uint32(track.SSRC()))
		}
		go func() {
			// Sender Reports for a track this viewer receives arrive here
			// (receiver.Read), not on any RTPSender -- fix-wave finding 2's
			// Go<->Go proof parses every packet for one instead of just
			// discarding, in addition to draining so the buffer never
			// blocks (standard Pion requirement).
			buf := make([]byte, 1500)
			for {
				n, _, err := receiver.Read(buf)
				if err != nil {
					return
				}
				pkts, unmarshalErr := rtcp.Unmarshal(buf[:n])
				if unmarshalErr != nil {
					continue
				}
				for _, pkt := range pkts {
					if sr, ok := pkt.(*rtcp.SenderReport); ok {
						fv.srMu.Lock()
						fv.srs = append(fv.srs, *sr)
						fv.srMu.Unlock()
					}
				}
			}
		}()
		go func() {
			buf := make([]byte, 1500)
			for {
				_, _, err := track.Read(buf)
				if err != nil {
					return
				}
				switch track.Kind() {
				case pion.RTPCodecTypeVideo:
					fv.videoPkts.Add(1)
				case pion.RTPCodecTypeAudio:
					fv.audioPkts.Add(1)
				}
			}
		}()
	})

	dc, err := pc.CreateDataChannel("input", nil)
	if err != nil {
		t.Fatalf("CreateDataChannel(input): %v", err)
	}
	fv.inputDC = dc
	dc.OnOpen(func() { close(fv.dcOpen) })
	dc.OnMessage(func(msg pion.DataChannelMessage) {
		fv.acksMu.Lock()
		fv.acks = append(fv.acks, append([]byte(nil), msg.Data...))
		fv.acksMu.Unlock()
	})

	return fv
}

func (fv *fakeViewer) ackCount() int {
	fv.acksMu.Lock()
	defer fv.acksMu.Unlock()
	return len(fv.acks)
}

func (fv *fakeViewer) lastAck() []byte {
	fv.acksMu.Lock()
	defer fv.acksMu.Unlock()
	if len(fv.acks) == 0 {
		return nil
	}
	return fv.acks[len(fv.acks)-1]
}

// senderReports returns a snapshot of every Sender Report this viewer has
// observed so far.
func (fv *fakeViewer) senderReports() []rtcp.SenderReport {
	fv.srMu.Lock()
	defer fv.srMu.Unlock()
	return append([]rtcp.SenderReport(nil), fv.srs...)
}

// receivedInput records one InputSink callback invocation.
type receivedInput struct {
	viewerID string
	raw      []byte
}

// --- tests ----------------------------------------------------------------

// TestSessionGoToGoFullFlow is the mandatory Go<->Go PeerConnection test:
// pion plays both the fake encoder and the fake viewer, in-process, with no
// Chrome involved. It exercises the full contract end to end: media flowing
// on both tracks, input data-channel messages reaching the InputSink,
// SendToViewer round-tripping an ack, and -- the key correctness property
// carried over from the wave-plan's "ingest replacement" requirement -- a
// second ingest connection replacing the first without dropping the
// already-attached viewer.
func TestSessionGoToGoFullFlow(t *testing.T) {
	var (
		receivedMu sync.Mutex
		received   []receivedInput
	)
	sink := func(viewerID string, raw []byte) {
		receivedMu.Lock()
		received = append(received, receivedInput{viewerID: viewerID, raw: append([]byte(nil), raw...)})
		receivedMu.Unlock()
	}

	sess := relay.NewSession(relay.Config{}, sink, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	// --- ingest #1: fake encoder offers video+audio ---
	enc1 := newFakeEncoder(t, true)
	enc1.startPumping(t)

	offer1 := nonTrickleOffer(t, enc1.pc)
	answer1, err := sess.HandleIngestOffer(offer1)
	if err != nil {
		t.Fatalf("HandleIngestOffer #1: %v", err)
	}
	if answer1 == "" {
		t.Fatal("HandleIngestOffer #1: empty answer SDP")
	}
	setAnswer(t, enc1.pc, answer1)

	// --- viewer: recvonly video+audio, opens the "input" data channel ---
	const viewerID = "viewer-1"
	viewer := newFakeViewer(t, true)

	offerV := nonTrickleOffer(t, viewer.pc)
	answerV, err := sess.HandleViewerOffer(viewerID, offerV)
	if err != nil {
		t.Fatalf("HandleViewerOffer: %v", err)
	}
	if answerV == "" {
		t.Fatal("HandleViewerOffer: empty answer SDP")
	}
	setAnswer(t, viewer.pc, answerV)

	// Media flows on both tracks.
	waitCond(t, testWait, "viewer to receive video RTP", func() bool { return viewer.videoPkts.Load() > 5 })
	waitCond(t, testWait, "viewer to receive audio RTP", func() bool { return viewer.audioPkts.Load() > 5 })

	// Input DC opens and a message reaches the InputSink.
	select {
	case <-viewer.dcOpen:
	case <-time.After(testWait):
		t.Fatal("input data channel did not open")
	}

	inputMsg := `{"id":1,"type":"mousemove","x":10,"y":20}`
	if sendErr := viewer.inputDC.SendText(inputMsg); sendErr != nil {
		t.Fatalf("viewer input DC SendText: %v", sendErr)
	}

	waitCond(t, testWait, "InputSink to receive the mousemove event", func() bool {
		receivedMu.Lock()
		defer receivedMu.Unlock()
		return len(received) == 1
	})
	receivedMu.Lock()
	got := received[0]
	receivedMu.Unlock()
	if got.viewerID != viewerID {
		t.Errorf("InputSink viewerID = %q, want %q", got.viewerID, viewerID)
	}
	var evt struct {
		ID   int64   `json:"id"`
		Type string  `json:"type"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
	}
	if unmarshalErr := json.Unmarshal(got.raw, &evt); unmarshalErr != nil {
		t.Fatalf("InputSink payload did not round-trip as the original JSON: %v (raw=%q)", unmarshalErr, got.raw)
	}
	if evt.Type != "mousemove" || evt.X != 10 || evt.Y != 20 {
		t.Errorf("InputSink payload = %+v, want type=mousemove x=10 y=20", evt)
	}

	// SendToViewer round-trips an ack over the same data channel, as TEXT.
	ackPayload := []byte(`{"id":1,"ok":true}`)
	if sendErr := sess.SendToViewer(viewerID, ackPayload); sendErr != nil {
		t.Fatalf("SendToViewer: %v", sendErr)
	}
	waitCond(t, testWait, "viewer to receive the ack", func() bool { return viewer.ackCount() == 1 })
	if got := string(viewer.lastAck()); got != string(ackPayload) {
		t.Errorf("viewer received ack %q, want %q", got, ackPayload)
	}

	// --- ingest replacement: a second encoder connects; the SAME viewer
	// must keep receiving packets without any viewer-side action, proving
	// the shared TrackLocalStaticRTP survives ingest replacement. ---
	videoBefore := viewer.videoPkts.Load()
	audioBefore := viewer.audioPkts.Load()

	enc2 := newFakeEncoder(t, true)
	enc2.startPumping(t)

	offer2 := nonTrickleOffer(t, enc2.pc)
	answer2, err := sess.HandleIngestOffer(offer2)
	if err != nil {
		t.Fatalf("HandleIngestOffer #2 (replacement): %v", err)
	}
	setAnswer(t, enc2.pc, answer2)

	waitCond(t, testWait, "viewer to keep receiving video after ingest replacement", func() bool {
		return viewer.videoPkts.Load() > videoBefore+5
	})
	waitCond(t, testWait, "viewer to keep receiving audio after ingest replacement", func() bool {
		return viewer.audioPkts.Load() > audioBefore+5
	})

	// --- Stats reflects reality. ---
	stats := sess.Stats()
	if stats.Viewers != 1 {
		t.Errorf("Stats().Viewers = %d, want 1", stats.Viewers)
	}
	if !stats.HasVideo || !stats.HasAudio {
		t.Errorf("Stats() = %+v, want HasVideo=true HasAudio=true", stats)
	}
	if stats.VideoCodec == "" || stats.AudioCodec == "" {
		t.Errorf("Stats() codecs empty: %+v", stats)
	}
	if stats.VideoPackets == 0 || stats.AudioPackets == 0 {
		t.Errorf("Stats() packet counts empty: %+v", stats)
	}

	// --- CloseViewer removes the viewer. ---
	sess.CloseViewer(viewerID)
	waitCond(t, testWait, "viewer count to drop to 0 after CloseViewer", func() bool {
		return sess.Stats().Viewers == 0
	})

	// SendToViewer for the now-closed viewer must fail cleanly.
	if err := sess.SendToViewer(viewerID, []byte("x")); err == nil {
		t.Error("SendToViewer after CloseViewer: want error, got nil")
	}
}

// TestSessionViewerLegReplacement_SameViewerID_OldPCClosedNewPCFlows is the
// QA regression-wave item 8 guard: viewer.go's HandleViewerOffer explicitly
// documents (and implements) "if viewerID collides with an already-attached
// viewer (e.g. a reconnect that races the old connection's teardown), the
// old PeerConnection is closed and replaced" — the SAME pattern already
// proven for INGEST replacement by TestSessionGoToGoFullFlow, but no test
// exercised the VIEWER-leg replacement path (a second browser_webrtc_offer
// for the SAME viewerID — e.g. a viewer's page reconnecting after a network
// blip) until now. Proves: (a) the OLD viewer PeerConnection transitions to
// Closed, (b) the NEW viewer PeerConnection keeps receiving RTP after the
// swap, and (c) sess.Stats().Viewers stays at 1 throughout — never 2, even
// transiently at the moment of replacement.
func TestSessionViewerLegReplacement_SameViewerID_OldPCClosedNewPCFlows(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	enc := newFakeEncoder(t, true)
	enc.startPumping(t)
	offerE, err := sess.HandleIngestOffer(nonTrickleOffer(t, enc.pc))
	if err != nil {
		t.Fatalf("HandleIngestOffer: %v", err)
	}
	setAnswer(t, enc.pc, offerE)

	const viewerID = "viewer-reconnect"

	// --- first viewer leg ---
	viewer1 := newFakeViewer(t, true)
	oldClosed := make(chan struct{})
	var oldClosedOnce sync.Once
	viewer1.pc.OnConnectionStateChange(func(st pion.PeerConnectionState) {
		if st == pion.PeerConnectionStateClosed {
			oldClosedOnce.Do(func() { close(oldClosed) })
		}
	})

	answer1, err := sess.HandleViewerOffer(viewerID, nonTrickleOffer(t, viewer1.pc))
	if err != nil {
		t.Fatalf("HandleViewerOffer (first leg): %v", err)
	}
	setAnswer(t, viewer1.pc, answer1)

	waitCond(t, testWait, "first leg to receive video RTP", func() bool { return viewer1.videoPkts.Load() > 5 })
	if got := sess.Stats().Viewers; got != 1 {
		t.Fatalf("Stats().Viewers after first leg = %d, want 1", got)
	}

	// --- second viewer leg: SAME viewerID, a brand-new PeerConnection (the
	// "reconnect that races the old connection's teardown" case). ---
	viewer2 := newFakeViewer(t, true)

	answer2, err := sess.HandleViewerOffer(viewerID, nonTrickleOffer(t, viewer2.pc))
	if err != nil {
		t.Fatalf("HandleViewerOffer (second leg, replacement): %v", err)
	}
	setAnswer(t, viewer2.pc, answer2)

	// (a) the OLD PeerConnection must actually be closed by the replacement,
	// not merely forgotten about (which would leak it).
	select {
	case <-oldClosed:
	case <-time.After(testWait):
		t.Fatal("old viewer PeerConnection was never closed after being replaced by a same-viewerID reconnect")
	}

	// (b) the NEW PeerConnection carries packets — the replacement actually
	// wired up media, not just torn down the old leg.
	videoBefore := viewer2.videoPkts.Load()
	waitCond(t, testWait, "new leg to receive video RTP after replacement", func() bool {
		return viewer2.videoPkts.Load() > videoBefore+5
	})
	waitCond(
		t,
		testWait,
		"new leg to receive audio RTP after replacement",
		func() bool { return viewer2.audioPkts.Load() > 5 },
	)

	// (c) exactly one viewer registered for viewerID throughout — never 2.
	if got := sess.Stats().Viewers; got != 1 {
		t.Fatalf("Stats().Viewers after replacement = %d, want 1 (never 2, even transiently)", got)
	}

	// SendToViewer now reaches the NEW leg (proves the registry entry
	// actually points at viewer2's PC, not a stale reference to viewer1's).
	ackPayload := []byte(`{"id":1,"ok":true}`)
	if sendErr := sess.SendToViewer(viewerID, ackPayload); sendErr != nil {
		t.Fatalf("SendToViewer after replacement: %v", sendErr)
	}
	waitCond(t, testWait, "new leg to receive the ack", func() bool { return viewer2.ackCount() == 1 })
	if got := viewer1.ackCount(); got != 0 {
		t.Fatalf("old (replaced) leg received %d acks, want 0 — SendToViewer must target the CURRENT connection", got)
	}
}

// TestSessionVideoOnlyTolerated exercises the "one video + one audio track
// expected but tolerate video-only" requirement: an encoder that never sends
// audio must still let a viewer attach and receive video.
func TestSessionVideoOnlyTolerated(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	enc := newFakeEncoder(t, false) // video only
	enc.startPumping(t)

	offer := nonTrickleOffer(t, enc.pc)
	answer, err := sess.HandleIngestOffer(offer)
	if err != nil {
		t.Fatalf("HandleIngestOffer: %v", err)
	}
	setAnswer(t, enc.pc, answer)

	viewer := newFakeViewer(t, true) // viewer still asks for both kinds
	offerV := nonTrickleOffer(t, viewer.pc)
	answerV, err := sess.HandleViewerOffer("viewer-video-only", offerV)
	if err != nil {
		t.Fatalf("HandleViewerOffer: %v", err)
	}
	setAnswer(t, viewer.pc, answerV)

	waitCond(t, testWait, "viewer to receive video RTP", func() bool { return viewer.videoPkts.Load() > 5 })

	// Give any (incorrect) audio packets a moment to show up, then assert
	// none did -- there is no audio track for this encoder.
	time.Sleep(300 * time.Millisecond)
	if n := viewer.audioPkts.Load(); n != 0 {
		t.Errorf("viewer received %d audio packets with a video-only ingest, want 0", n)
	}

	stats := sess.Stats()
	if !stats.HasVideo {
		t.Error("Stats().HasVideo = false, want true")
	}
	if stats.HasAudio {
		t.Error("Stats().HasAudio = true, want false (video-only ingest)")
	}
}

// TestSessionViewerOfferWithoutIngestTimesOut asserts HandleViewerOffer
// rejects a viewer offer when no ingest connection has ever attached,
// instead of hanging forever.
func TestSessionViewerOfferWithoutIngestTimesOut(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	viewer := newFakeViewer(t, true)
	offerV := nonTrickleOffer(t, viewer.pc)

	start := time.Now()
	_, err := sess.HandleViewerOffer("no-ingest-viewer", offerV)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("HandleViewerOffer with no ingest: want error, got nil")
	}
	if elapsed > testWait {
		t.Errorf("HandleViewerOffer took %s to fail, want a bounded wait", elapsed)
	}
}

// TestSessionSendToViewerUnknownID asserts SendToViewer fails cleanly for a
// viewerID that was never attached.
func TestSessionSendToViewerUnknownID(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	if err := sess.SendToViewer("nobody-here", []byte("x")); err == nil {
		t.Error("SendToViewer for an unknown viewerID: want error, got nil")
	}
}

// TestSessionCloseRejectsFurtherOffers asserts a closed Session cleanly
// rejects both ingest and viewer offers rather than panicking or hanging.
func TestSessionCloseRejectsFurtherOffers(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close must be idempotent.
	if err := sess.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	enc := newFakeEncoder(t, true)
	offer := nonTrickleOffer(t, enc.pc)
	if _, err := sess.HandleIngestOffer(offer); err == nil {
		t.Error("HandleIngestOffer on a closed Session: want error, got nil")
	}

	viewer := newFakeViewer(t, true)
	offerV := nonTrickleOffer(t, viewer.pc)
	if _, err := sess.HandleViewerOffer("v", offerV); err == nil {
		t.Error("HandleViewerOffer on a closed Session: want error, got nil")
	}
}

// TestSessionSignalRecaptureNoPanic asserts SignalRecapture is safe to call
// both before any ingest connects and after one does (it's a fire-and-forget
// hook -- see the doc comment on Session.SignalRecapture for why this
// package doesn't need to do anything more than that).
func TestSessionSignalRecaptureNoPanic(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	sess.SignalRecapture() // no ingest yet -- must not panic or block

	enc := newFakeEncoder(t, true)
	enc.startPumping(t)
	offer := nonTrickleOffer(t, enc.pc)
	answer, err := sess.HandleIngestOffer(offer)
	if err != nil {
		t.Fatalf("HandleIngestOffer: %v", err)
	}
	setAnswer(t, enc.pc, answer)

	waitCond(t, testWait, "ingest video track to attach", func() bool { return sess.Stats().HasVideo })
	sess.SignalRecapture() // ingest present now -- also must not panic or block
}

// TestSessionConfigStunEmptyHostOnly asserts a Session built with an empty
// Config.StunServer (host-only ICE) still completes a full offer/answer/
// media round trip -- this is the mode wave-plan decision 7 designates for
// Tools.Browser.WebRTCStunServer == "".
func TestSessionConfigStunEmptyHostOnly(t *testing.T) {
	sess := relay.NewSession(relay.Config{StunServer: ""}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	enc := newFakeEncoder(t, true)
	enc.startPumping(t)
	offer := nonTrickleOffer(t, enc.pc)
	answer, err := sess.HandleIngestOffer(offer)
	if err != nil {
		t.Fatalf("HandleIngestOffer: %v", err)
	}
	if answer == "" {
		t.Fatal("empty answer SDP")
	}
	setAnswer(t, enc.pc, answer)

	waitCond(t, testWait, "ingest to connect with host-only ICE", func() bool { return sess.Stats().HasVideo })
}

// TestSessionMultipleViewersShareTracks asserts two independent viewers can
// attach to the same ingest and both receive media -- the core SFU property
// (one shared TrackLocalStaticRTP fanned out to N viewer RTPSenders, zero
// transcoding).
func TestSessionMultipleViewersShareTracks(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	enc := newFakeEncoder(t, true)
	enc.startPumping(t)
	offer := nonTrickleOffer(t, enc.pc)
	answer, err := sess.HandleIngestOffer(offer)
	if err != nil {
		t.Fatalf("HandleIngestOffer: %v", err)
	}
	setAnswer(t, enc.pc, answer)

	viewers := make([]*fakeViewer, 2)
	for i := range viewers {
		v := newFakeViewer(t, true)
		offerV := nonTrickleOffer(t, v.pc)
		answerV, err := sess.HandleViewerOffer(fmt.Sprintf("viewer-%d", i), offerV)
		if err != nil {
			t.Fatalf("HandleViewerOffer(viewer-%d): %v", i, err)
		}
		setAnswer(t, v.pc, answerV)
		viewers[i] = v
	}

	for i, v := range viewers {
		vv := v
		idx := i
		waitCond(t, testWait, fmt.Sprintf("viewer-%d to receive video RTP", idx), func() bool {
			return vv.videoPkts.Load() > 5
		})
	}

	if stats := sess.Stats(); stats.Viewers != 2 {
		t.Errorf("Stats().Viewers = %d, want 2", stats.Viewers)
	}
}

// --- fix-wave: media smoothness (UAT freeze/sync/CPU/ordering findings) ---

// TestSessionViewerPLIForwardedToIngestThrottled is the Go<->Go proof for
// fix-wave finding 1 (CRIT, the reported freeze: "video froze while audio
// kept playing"): a viewer's own RTCP PictureLossIndication now reaches the
// ingest connection (before this fix it dead-ended in drainRTCP's plain
// discard), AND forwarding is throttled across the whole session so a burst
// of loss reports can't force the encoder into a constant-keyframe spiral.
func TestSessionViewerPLIForwardedToIngestThrottled(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	enc := newFakeEncoder(t, true)
	enc.startPumping(t)
	offer := nonTrickleOffer(t, enc.pc)
	answer, err := sess.HandleIngestOffer(offer)
	if err != nil {
		t.Fatalf("HandleIngestOffer: %v", err)
	}
	setAnswer(t, enc.pc, answer)

	var pliCount atomic.Int64
	videoSender := enc.videoSender(t)
	go func() {
		buf := make([]byte, 1500)
		for {
			n, _, readErr := videoSender.Read(buf)
			if readErr != nil {
				return
			}
			pkts, unmarshalErr := rtcp.Unmarshal(buf[:n])
			if unmarshalErr != nil {
				continue
			}
			for _, pkt := range pkts {
				if _, ok := pkt.(*rtcp.PictureLossIndication); ok {
					pliCount.Add(1)
				}
			}
		}
	}()

	const viewerID = "pli-viewer"
	viewer := newFakeViewer(t, true)
	offerV := nonTrickleOffer(t, viewer.pc)
	answerV, err := sess.HandleViewerOffer(viewerID, offerV)
	if err != nil {
		t.Fatalf("HandleViewerOffer: %v", err)
	}
	setAnswer(t, viewer.pc, answerV)

	// The new-viewer join burst (pliBurstForNewViewer) sends its own
	// immediate PLI, independent of anything this test writes -- wait for
	// it (and for media to actually be flowing) so the baseline below is
	// stable before this test's own writes begin.
	waitCond(t, testWait, "viewer to receive video RTP", func() bool { return viewer.videoPkts.Load() > 5 })
	waitCond(t, testWait, "join-burst PLI to arrive at the encoder", func() bool { return pliCount.Load() >= 1 })
	waitCond(t, testWait, "viewer to observe its own video SSRC", func() bool { return viewer.videoSSRC.Load() != 0 })

	baseline := pliCount.Load()

	writeViewerPLI := func() {
		if writeErr := viewer.pc.WriteRTCP([]rtcp.Packet{
			&rtcp.PictureLossIndication{MediaSSRC: viewer.videoSSRC.Load()},
		}); writeErr != nil {
			t.Fatalf("viewer WriteRTCP(PLI): %v", writeErr)
		}
	}

	// One explicit viewer-side PLI. forwardPLIThrottled's own
	// lastPLIForwardAt has never been touched by anything but a
	// viewer-triggered forward (the join burst calls sendPLI directly,
	// bypassing the throttle), so this first call always goes through.
	writeViewerPLI()

	waitCond(t, 3*time.Second, "the explicit viewer PLI to be forwarded to the encoder", func() bool {
		return pliCount.Load() > baseline
	})
	afterFirst := pliCount.Load()
	if afterFirst != baseline+1 {
		t.Fatalf(
			"pliCount after one viewer PLI = %d, want exactly baseline+1=%d (baseline=%d)",
			afterFirst,
			baseline+1,
			baseline,
		)
	}

	// A rapid burst of further viewer PLIs, all well within
	// pliForwardMinInterval (750ms production default) and well before the
	// join burst's next scheduled tick (3s after viewer attach) -- none of
	// these must be forwarded.
	for i := 0; i < 10; i++ {
		writeViewerPLI()
	}
	time.Sleep(200 * time.Millisecond)

	afterBurst := pliCount.Load()
	if afterBurst != afterFirst {
		t.Fatalf(
			"pliCount after a rapid 10-write burst = %d, want unchanged at %d (the throttle should have suppressed all 10)",
			afterBurst,
			afterFirst,
		)
	}
}

// TestSessionSenderReportForwardedWithRewrittenSSRC is the Go<->Go proof for
// fix-wave finding 2 (the reported "audio slightly delayed vs video"): an
// RTCP Sender Report arriving on the ingest connection is forwarded to the
// viewer with its SSRC rewritten to that viewer's own negotiated outgoing
// SSRC for the track (required for the browser to accept/correlate it) --
// every other field (NTP/RTP time, packet/octet counts) passes through
// unmodified.
func TestSessionSenderReportForwardedWithRewrittenSSRC(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	enc := newFakeEncoder(t, true)
	enc.startPumping(t)
	offer := nonTrickleOffer(t, enc.pc)
	answer, err := sess.HandleIngestOffer(offer)
	if err != nil {
		t.Fatalf("HandleIngestOffer: %v", err)
	}
	setAnswer(t, enc.pc, answer)

	const viewerID = "sr-viewer"
	viewer := newFakeViewer(t, true)
	offerV := nonTrickleOffer(t, viewer.pc)
	answerV, err := sess.HandleViewerOffer(viewerID, offerV)
	if err != nil {
		t.Fatalf("HandleViewerOffer: %v", err)
	}
	setAnswer(t, viewer.pc, answerV)

	waitCond(t, testWait, "viewer to receive video RTP", func() bool { return viewer.videoPkts.Load() > 5 })
	waitCond(t, testWait, "viewer to observe its own video SSRC", func() bool { return viewer.videoSSRC.Load() != 0 })

	// The SSRC the ingest side actually negotiated for the encoder's video
	// track -- the same value remote.SSRC() reports in attachIngestTrack's
	// OnTrack, and what a real WriteRTCP(SenderReport) from Chrome would
	// carry as its own SSRC.
	encVideoSender := enc.videoSender(t)
	encParams := encVideoSender.GetParameters()
	if len(encParams.Encodings) == 0 {
		t.Fatal("encoder video sender has no negotiated encodings")
	}
	encoderVideoSSRC := uint32(encParams.Encodings[0].SSRC)

	sr := &rtcp.SenderReport{
		SSRC:        encoderVideoSSRC,
		NTPTime:     1234567890,
		RTPTime:     90000,
		PacketCount: 100,
		OctetCount:  50000,
	}
	if writeErr := enc.pc.WriteRTCP([]rtcp.Packet{sr}); writeErr != nil {
		t.Fatalf("encoder WriteRTCP(SenderReport): %v", writeErr)
	}

	waitCond(t, testWait, "the viewer to observe a forwarded Sender Report", func() bool {
		return len(viewer.senderReports()) > 0
	})

	got := viewer.senderReports()[0]
	wantSSRC := viewer.videoSSRC.Load()
	if got.SSRC != wantSSRC {
		t.Errorf(
			"forwarded Sender Report SSRC = %d, want the viewer's own negotiated video SSRC %d (not the encoder's %d)",
			got.SSRC,
			wantSSRC,
			encoderVideoSSRC,
		)
	}
	if got.NTPTime != sr.NTPTime || got.RTPTime != sr.RTPTime || got.PacketCount != sr.PacketCount ||
		got.OctetCount != sr.OctetCount {
		t.Errorf(
			"forwarded Sender Report fields = %+v, want the source fields untouched (only SSRC rewritten): source=%+v",
			got,
			sr,
		)
	}
}

// TestSessionIngestReplacementFailureLeavesPreviousConnectionIntact is the
// fix-wave finding 2b regression test (7-reviewer finding): a malformed/
// failing recapture offer must NOT tear down a previously-healthy ingest
// connection -- see HandleIngestOffer's doc comment for the ordering fix
// (build + fully negotiate the new connection BEFORE swapping it in and
// closing the old one).
func TestSessionIngestReplacementFailureLeavesPreviousConnectionIntact(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	enc1 := newFakeEncoder(t, true)
	enc1.startPumping(t)
	offer1 := nonTrickleOffer(t, enc1.pc)
	answer1, err := sess.HandleIngestOffer(offer1)
	if err != nil {
		t.Fatalf("HandleIngestOffer #1: %v", err)
	}
	setAnswer(t, enc1.pc, answer1)

	const viewerID = "replace-fail-viewer"
	viewer := newFakeViewer(t, true)
	offerV := nonTrickleOffer(t, viewer.pc)
	answerV, err := sess.HandleViewerOffer(viewerID, offerV)
	if err != nil {
		t.Fatalf("HandleViewerOffer: %v", err)
	}
	setAnswer(t, viewer.pc, answerV)

	waitCond(
		t,
		testWait,
		"viewer to receive video RTP from the first encoder",
		func() bool { return viewer.videoPkts.Load() > 5 },
	)
	videoBefore := viewer.videoPkts.Load()

	// A malformed "recapture" offer -- SetRemoteDescription must fail
	// parsing this. Before the ordering fix, HandleIngestOffer would
	// ALREADY have swapped this failed attempt in as s.ingestPC and closed
	// enc1's healthy connection by this point in the call.
	if _, offerErr := sess.HandleIngestOffer("this is not a valid SDP offer at all"); offerErr == nil {
		t.Fatal("HandleIngestOffer with a malformed SDP: want an error, got nil")
	}

	// The FIRST (still healthy) encoder's media must keep flowing to the
	// already-attached viewer, proving its connection was never torn down
	// by the failed replacement attempt.
	waitCond(
		t,
		testWait,
		"viewer to keep receiving video from the ORIGINAL encoder after a failed replacement attempt",
		func() bool {
			return viewer.videoPkts.Load() > videoBefore+5
		},
	)

	if stats := sess.Stats(); stats.VideoCodec == "" {
		t.Error(
			"Stats().VideoCodec empty after a failed ingest replacement -- the original ingest connection's track info should be untouched",
		)
	}

	// A LEGITIMATE second ingest must still be able to replace the first
	// one afterwards -- the failed attempt must not have wedged anything.
	enc2 := newFakeEncoder(t, true)
	enc2.startPumping(t)
	offer2 := nonTrickleOffer(t, enc2.pc)
	answer2, err := sess.HandleIngestOffer(offer2)
	if err != nil {
		t.Fatalf("HandleIngestOffer #2 (legitimate replacement after the failed attempt): %v", err)
	}
	setAnswer(t, enc2.pc, answer2)

	videoBeforeReplacement := viewer.videoPkts.Load()
	waitCond(t, testWait, "viewer to keep receiving video after a LEGITIMATE ingest replacement", func() bool {
		return viewer.videoPkts.Load() > videoBeforeReplacement+5
	})
}

// TestSessionInputDataChannelPreservesOrderUnderSlowSink is the Go<->Go
// proof for fix-wave finding 2c (inputdc.go): the per-viewer serialized
// input queue delivers data-channel messages to the InputSink in the SAME
// order the browser sent them, even when the sink's processing time varies
// per call. This is exactly the failure mode the previous "go
// s.sink(viewerID, raw)" dispatch (one fresh goroutine per message) was
// exposed to: nothing serialized WHICH goroutine's sink call actually ran
// first once more than one was in flight, so a slow sink call for an
// EARLIER message could complete after a fast sink call for a LATER one --
// e.g. delivering a keydown after its own keyup to the gateway's CDP
// dispatch path.
func TestSessionInputDataChannelPreservesOrderUnderSlowSink(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []int
	)
	sink := func(viewerID string, raw []byte) {
		var evt struct {
			Seq int `json:"seq"`
		}
		if unmarshalErr := json.Unmarshal(raw, &evt); unmarshalErr != nil {
			return
		}
		// Deliberately variable per-call latency, mirroring the gateway's
		// real CDP round trip -- an unserialized dispatcher racing these
		// goroutines against each other would very likely deliver results
		// out of order.
		time.Sleep(time.Duration((evt.Seq*37)%11) * time.Millisecond)
		mu.Lock()
		seen = append(seen, evt.Seq)
		mu.Unlock()
	}

	sess := relay.NewSession(relay.Config{}, sink, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	enc := newFakeEncoder(t, true)
	enc.startPumping(t)
	offer := nonTrickleOffer(t, enc.pc)
	answer, err := sess.HandleIngestOffer(offer)
	if err != nil {
		t.Fatalf("HandleIngestOffer: %v", err)
	}
	setAnswer(t, enc.pc, answer)

	const viewerID = "order-viewer"
	viewer := newFakeViewer(t, true)
	offerV := nonTrickleOffer(t, viewer.pc)
	answerV, err := sess.HandleViewerOffer(viewerID, offerV)
	if err != nil {
		t.Fatalf("HandleViewerOffer: %v", err)
	}
	setAnswer(t, viewer.pc, answerV)

	select {
	case <-viewer.dcOpen:
	case <-time.After(testWait):
		t.Fatal("input data channel did not open")
	}

	const n = 30
	for i := 0; i < n; i++ {
		msg := fmt.Sprintf(`{"seq":%d}`, i)
		if sendErr := viewer.inputDC.SendText(msg); sendErr != nil {
			t.Fatalf("SendText(seq=%d): %v", i, sendErr)
		}
	}

	waitCond(t, testWait, "sink to receive all n messages", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == n
	})

	mu.Lock()
	defer mu.Unlock()
	for i, s := range seen {
		if s != i {
			t.Fatalf(
				"sink received out-of-order sequence at index %d: got seq=%d, want seq=%d (full sequence: %v)",
				i,
				s,
				i,
				seen,
			)
		}
	}
}

// TestSessionCloseViewerIfCurrent_SupersededHandleIsNoop_NewerConnectionSurvives
// is the Go<->Go regression guard for the reviewer-traced supersede race: a
// superseded/failed offer's own cleanup must not be able to tear down a
// NEWER, already-committed offer's live viewer connection for the SAME
// viewerID. Mirrors TestSessionViewerLegReplacement_SameViewerID_
// OldPCClosedNewPCFlows's replacement scenario, but proves the SPECIFIC
// supersede-safety property CloseViewerIfCurrent adds on top of that: the
// OLDER handle's cleanup call is a safe no-op once superseded, rather than
// blindly closing whatever is currently registered for viewerID (which plain
// CloseViewer(viewerID) does, and which pkg/gateway/browser_webrtc.go's
// handleWebRTCOffer used to call directly for exactly this cleanup).
func TestSessionCloseViewerIfCurrent_SupersededHandleIsNoop_NewerConnectionSurvives(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	enc := newFakeEncoder(t, true)
	enc.startPumping(t)
	offerE, err := sess.HandleIngestOffer(nonTrickleOffer(t, enc.pc))
	if err != nil {
		t.Fatalf("HandleIngestOffer: %v", err)
	}
	setAnswer(t, enc.pc, offerE)

	const viewerID = "viewer-race"

	// --- older (losing) offer ---
	viewerA := newFakeViewer(t, true)
	answerA, handleA, err := sess.HandleViewerOfferHandle(viewerID, nonTrickleOffer(t, viewerA.pc))
	if err != nil {
		t.Fatalf("HandleViewerOfferHandle (A): %v", err)
	}
	if handleA == nil {
		t.Fatal("HandleViewerOfferHandle (A) returned a nil handle on success")
	}
	setAnswer(t, viewerA.pc, answerA)
	waitCond(t, testWait, "viewer A to receive video RTP", func() bool { return viewerA.videoPkts.Load() > 5 })

	// --- newer (winning) offer, SAME viewerID -- replaces A in the registry,
	// closing A's PC (the ordinary same-viewerID reconnect/replace path,
	// already covered by TestSessionViewerLegReplacement_SameViewerID_
	// OldPCClosedNewPCFlows). ---
	viewerB := newFakeViewer(t, true)
	answerB, handleB, err := sess.HandleViewerOfferHandle(viewerID, nonTrickleOffer(t, viewerB.pc))
	if err != nil {
		t.Fatalf("HandleViewerOfferHandle (B): %v", err)
	}
	if handleB == nil {
		t.Fatal("HandleViewerOfferHandle (B) returned a nil handle on success")
	}
	setAnswer(t, viewerB.pc, answerB)
	waitCond(t, testWait, "viewer B to receive video RTP", func() bool { return viewerB.videoPkts.Load() > 5 })

	if got := sess.Stats().Viewers; got != 1 {
		t.Fatalf("Stats().Viewers after B replaces A = %d, want 1", got)
	}

	// A's own cleanup runs AFTER B has already won -- simulating a slow or
	// ultimately-failed offer A's teardown racing in late. Without the
	// identity check this would close B's live PeerConnection: plain
	// CloseViewer(viewerID) closes WHATEVER is currently registered.
	sess.CloseViewerIfCurrent(handleA)

	// B must be completely unaffected: still registered, still receiving
	// media, still reachable via SendToViewer.
	if got := sess.Stats().Viewers; got != 1 {
		t.Fatalf("Stats().Viewers after A's superseded cleanup = %d, want still 1 (B must survive)", got)
	}
	videoBefore := viewerB.videoPkts.Load()
	waitCond(t, testWait, "viewer B to KEEP receiving video after A's superseded cleanup", func() bool {
		return viewerB.videoPkts.Load() > videoBefore+5
	})
	if sendErr := sess.SendToViewer(viewerID, []byte("x")); sendErr != nil {
		t.Fatalf("SendToViewer(B) after A's superseded cleanup: %v -- B's registration was disturbed", sendErr)
	}

	// A SECOND call with the same (already-superseded) handleA must also be
	// a safe no-op (idempotent) -- and so must a nil handle.
	sess.CloseViewerIfCurrent(handleA)
	sess.CloseViewerIfCurrent(nil)
	if got := sess.Stats().Viewers; got != 1 {
		t.Fatalf("Stats().Viewers after redundant/nil CloseViewerIfCurrent calls = %d, want still 1", got)
	}

	// Now B's OWN (current) handle DOES tear it down -- proving
	// CloseViewerIfCurrent still works for the connection it's actually
	// meant for, not just unconditionally no-oping.
	sess.CloseViewerIfCurrent(handleB)
	waitCond(t, testWait, "viewer count to drop to 0 once B's OWN handle closes it", func() bool {
		return sess.Stats().Viewers == 0
	})
}

// TestSessionViewerICEDisconnect_ClientVanishesWithoutSignaling_ServerEvictsAndClosesPC
// is the fix-wave CRIT regression guard (two independent reviewers, conf
// 85-90): before this fix, viewer.go's OnConnectionStateChange handler
// routed Closed/Failed/Disconnected straight to removeViewer, which only
// ever deleted the map entry -- the server-side PeerConnection itself, its
// two drainViewerRTCP goroutines, its runInputQueue goroutine (inputdc.go),
// and its UDP sockets were never closed, leaking on every viewer that simply
// vanished (tab closed, network dropped) without a clean CloseViewer /
// browser_detach round trip.
//
// This test never calls sess.CloseViewer -- it closes ONLY the CLIENT-side
// PeerConnection, exactly as a crashed/killed browser tab would, and proves
// the SERVER independently notices (via its own ICE/peer-connection-state
// machine, not any explicit signaling) and cleans up: Stats().Viewers drops
// to 0, SendToViewer starts failing, and -- the actual leak proof -- the
// process's live goroutine count returns to its pre-viewer baseline
// (drainViewerRTCP x2 + runInputQueue x1 must all have actually exited, not
// just had their registry entry disappear).
//
// Timing: Pion's ICE agent defaults to a 5s disconnected-detection window
// (pion/ice's defaultDisconnectedTimeout) before the PeerConnectionState
// callback even fires Disconnected; this package's own disconnectGracePeriod
// (viewer.go, 10s) then runs from there. disconnectDetectWait gives that
// combined ~15s a generous margin without shrinking either production
// timeout (an unexported test seam would only be reachable from THIS
// package's own in-package tests, not this external Go<->Go one).
func TestSessionViewerICEDisconnect_ClientVanishesWithoutSignaling_ServerEvictsAndClosesPC(t *testing.T) {
	var (
		receivedMu sync.Mutex
		received   int
	)
	sink := func(string, []byte) {
		receivedMu.Lock()
		received++
		receivedMu.Unlock()
	}

	// Baseline the two per-viewer goroutines BEFORE this test creates any of
	// its own, and assert at the end that we return TO that baseline rather
	// than to zero.
	//
	// Why this matters: the check at the end of this test uses
	// runtime.Stack(buf, true) — a PROCESS-WIDE dump. Asserting those
	// functions appear NOWHERE makes this test fail whenever ANY other test
	// in the package still has a viewer goroutine winding down, including
	// ones that already finished — Pion's Close() is documented (in this
	// test's own comments below) not to tear down every internal goroutine
	// synchronously. That is a package-global assertion masquerading as a
	// per-test one.
	//
	// It is also precisely why this test PASSED when run alone with
	// -run '^TestSessionViewerICEDisconnect...$' and FAILED on CI, where the
	// whole package runs: identical code, different neighbors. Blaming this
	// test for another test's residue sends the next person hunting a leak
	// that isn't where the failure points. Baseline-relative preserves the
	// real regression this guards — THIS viewer's goroutines must exit —
	// while staying immune to unrelated residue.
	baselineViewerGoroutines := countViewerGoroutines()

	sess := relay.NewSession(relay.Config{}, sink, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	enc := newFakeEncoder(t, true)
	enc.startPumping(t)
	offerE, err := sess.HandleIngestOffer(nonTrickleOffer(t, enc.pc))
	if err != nil {
		t.Fatalf("HandleIngestOffer: %v", err)
	}
	setAnswer(t, enc.pc, offerE)

	const viewerID = "viewer-client-vanishes"
	viewer := newFakeViewer(t, true)

	answerV, err := sess.HandleViewerOffer(viewerID, nonTrickleOffer(t, viewer.pc))
	if err != nil {
		t.Fatalf("HandleViewerOffer: %v", err)
	}
	setAnswer(t, viewer.pc, answerV)

	waitCond(t, testWait, "viewer to receive video RTP", func() bool { return viewer.videoPkts.Load() > 5 })
	select {
	case <-viewer.dcOpen:
	case <-time.After(testWait):
		t.Fatal("input data channel did not open")
	}
	// Prove the input queue worker is actually alive before the connection
	// dies, so its later exit (asserted via the goroutine-count check below)
	// is a real transition, not a vacuous no-op.
	if sendErr := viewer.inputDC.SendText(`{"type":"noop"}`); sendErr != nil {
		t.Fatalf("viewer input DC SendText: %v", sendErr)
	}
	waitCond(t, testWait, "InputSink to receive the pre-disconnect probe message", func() bool {
		receivedMu.Lock()
		defer receivedMu.Unlock()
		return received == 1
	})
	if got := sess.Stats().Viewers; got != 1 {
		t.Fatalf("Stats().Viewers before disconnect = %d, want 1", got)
	}

	// The client vanishes WITHOUT calling sess.CloseViewer -- exactly what a
	// crashed tab or dropped network looks like from the server's
	// perspective. The server must notice entirely on its own.
	if closeErr := viewer.pc.Close(); closeErr != nil {
		t.Fatalf("viewer.pc.Close(): %v", closeErr)
	}

	// 60s (4x the nominal ~15s combined ICE-disconnected-detection +
	// disconnectGracePeriod window documented above) — bumped from the
	// original 45s (only 3x margin) after it flaked on a GitHub-hosted
	// runner at 45.36s: shared/throttled CI CPU and network jitter can slow
	// Pion's ICE state-machine transitions well past what this pod's own
	// margin covers. Still a hard bound (no production timeout changes),
	// just enough slack to stop timeout luck from deciding the outcome on a
	// loaded runner.
	const disconnectDetectWait = 60 * time.Second
	waitCond(
		t,
		disconnectDetectWait,
		"server to evict the viewer after the client vanished without signaling",
		func() bool {
			return sess.Stats().Viewers == 0
		},
	)

	if sendErr := sess.SendToViewer(viewerID, []byte("x")); sendErr == nil {
		t.Error(
			"SendToViewer after the client-initiated disconnect: want error, got nil (registry entry should be gone)",
		)
	}

	// The leak proof: both of this viewer's server-side goroutines --
	// drainViewerRTCP (x2, video+audio) and runInputQueue (inputdc.go) --
	// must have actually exited, not just the registry entry vanished,
	// which only happens if the server's PeerConnection was really
	// Close()d, not merely forgotten about.
	//
	// This checks for the ABSENCE of those two functions' frames in a full
	// stack dump, rather than a raw total-process-goroutine-count delta
	// against a pre-viewer baseline (the original fix-wave version of this
	// assertion). The count-based version flaked hard in practice -- a 90s
	// wait and even a 240s soak both left the total 10-17 goroutines above
	// baseline, PERMANENTLY (not converging with more time). Isolating the
	// leftovers via a full stack dump traced them to pion's OWN internal
	// ICE-agent/DTLS/mDNS teardown goroutines for the vanished client's
	// PeerConnection (github.com/pion/ice, github.com/pion/dtls,
	// github.com/pion/mdns) -- not to drainViewerRTCP/runInputQueue, which
	// were already confirmed absent well before the raw count ever
	// stabilized. That's a pion library characteristic (its Close() does
	// not synchronously tear down every internal goroutine) outside this
	// package's control, not a regression of the CRIT fix this test guards;
	// asserting on the two SPECIFIC functions this test actually cares
	// about is both more precise and immune to that noise.
	waitCond(
		t,
		disconnectDetectWait,
		"drainViewerRTCP/runInputQueue goroutines to actually exit (not just the registry entry to vanish)",
		func() bool {
			return countViewerGoroutines() <= baselineViewerGoroutines
		},
	)
}

// countViewerGoroutines counts live goroutines currently inside
// drainViewerRTCP or runInputQueue — the two per-viewer goroutines a viewer
// eviction must reap.
//
// The dump is process-wide (runtime.Stack's all=true), so the ABSOLUTE count
// includes every other test's viewers too; callers must therefore compare it
// against a baseline they captured themselves rather than against zero. See
// the caller's own comment for why that distinction is what makes this
// assertion meaningful instead of merely noisy.
//
// The buffer grows until the dump fits: a truncated dump would UNDERCOUNT,
// silently turning a real leak into a pass — the one failure mode this helper
// must not have. Starting at 1MB covers the common case in a single call.
func countViewerGoroutines() int {
	size := 1 << 20
	var dump string
	for {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < size {
			dump = string(buf[:n])
			break
		}
		size *= 2
	}
	return strings.Count(dump, ".drainViewerRTCP(") + strings.Count(dump, ".runInputQueue(")
}

// TestSessionIngestDeath_RelayStopsClaimingItHasVideo is the end-to-end half
// of issue #674, over a real Go<->Go wire flow rather than the unexported
// state machine (that half is ingest_liveness_test.go).
//
// It pins two facts that together are the fix:
//
//  1. SetOnIngestLive fires when a video feed actually starts. Without a
//     positive success signal, the owner's bounded automatic recovery can only
//     ever exhaust its attempt budget — it would keep counting failures
//     against a stream that had already come back.
//  2. Once the encoder is gone, the relay stops reporting that it has video.
//     Before the fix the shared local track was assigned once and never
//     retired, so Stats().HasVideo — and, far worse, waitForTracks — kept
//     saying yes forever, and every later viewer offer was answered instantly
//     against a track nothing was writing to.
func TestSessionIngestDeath_RelayStopsClaimingItHasVideo(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	var liveCalls atomic.Int32
	sess.SetOnIngestLive(func() { liveCalls.Add(1) })

	enc := newFakeEncoder(t, true)
	enc.startPumping(t)
	answer, err := sess.HandleIngestOffer(nonTrickleOffer(t, enc.pc))
	if err != nil {
		t.Fatalf("HandleIngestOffer: %v", err)
	}
	setAnswer(t, enc.pc, answer)

	waitCond(t, testWait, "ingest video track to attach", func() bool { return sess.Stats().HasVideo })
	waitCond(t, testWait, "the ingest-live hook to fire", func() bool { return liveCalls.Load() > 0 })

	// The encoder vanishes — the exact thing that leaves the shared local
	// tracks in place with nothing feeding them.
	if err := enc.pc.Close(); err != nil {
		t.Fatalf("closing the fake encoder: %v", err)
	}

	waitCond(t, testWait, "the relay to stop claiming it has video", func() bool {
		return !sess.Stats().HasVideo
	})
}
