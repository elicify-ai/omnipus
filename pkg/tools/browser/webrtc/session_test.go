//go:build !lite

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
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
		go func() {
			buf := make([]byte, 1500)
			for {
				if _, _, err := receiver.Read(buf); err != nil {
					return
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
