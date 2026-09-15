package browser

import (
	"context"
	"testing"
	"time"

	relay "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	"github.com/pion/rtp"
	pion "github.com/pion/webrtc/v4"
)

type captureConstructorMarker struct{}

func captureConstructorPeer(t *testing.T) *pion.PeerConnection {
	t.Helper()
	pc, err := pion.NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	return pc
}
func captureConstructorOffer(t *testing.T, pc *pion.PeerConnection) string {
	t.Helper()
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := pion.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture gathering did not complete")
	}
	return pc.LocalDescription().SDP
}
func captureConstructorAnswer(t *testing.T, pc *pion.PeerConnection, sdp string) {
	t.Helper()
	if err := pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: sdp}); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureContextConstructorRoutesRealInput(t *testing.T) {
	for _, constructor := range []string{"context", "legacy"} {
		t.Run(constructor, func(t *testing.T) {
			type input struct {
				ctx     context.Context
				id, raw string
			}
			received := make(chan input, 1)
			var cs *CaptureSession
			var err error
			if constructor == "context" {
				cs, err = NewCaptureSessionWithContextInput(nil, "agent", "panel", relay.Config{}, func(ctx context.Context, id string, raw []byte) { received <- input{ctx, id, string(raw)} }, nil)
			} else {
				cs, err = NewCaptureSession(nil, "agent", "panel", relay.Config{}, func(id string, raw []byte) { received <- input{nil, id, string(raw)} }, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cs.Stop)
			server, ok := cs.relay.(*relay.Session)
			if !ok {
				t.Fatalf("production relay type=%T", cs.relay)
			}
			encoder := captureConstructorPeer(t)
			video, err := pion.NewTrackLocalStaticRTP(pion.RTPCodecCapability{MimeType: pion.MimeTypeVP8, ClockRate: 90000}, "video", "capture")
			if err != nil {
				t.Fatal(err)
			}
			if _, addTrackErr := encoder.AddTrack(video); addTrackErr != nil {
				t.Fatal(addTrackErr)
			}
			answer, err := server.HandleIngestOffer(captureConstructorOffer(t, encoder))
			if err != nil {
				t.Fatal(err)
			}
			captureConstructorAnswer(t, encoder, answer)
			stop, stopped := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(stopped)
				tick := time.NewTicker(10 * time.Millisecond)
				defer tick.Stop()
				var seq uint16
				for {
					select {
					case <-stop:
						return
					case <-tick.C:
						seq++
						_ = video.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: uint32(seq) * 900}, Payload: []byte{0x10, 0x00}})
					}
				}
			}()
			defer func() { close(stop); <-stopped }()
			viewer := captureConstructorPeer(t)
			if _, transceiverErr := viewer.AddTransceiverFromKind(pion.RTPCodecTypeVideo, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}); transceiverErr != nil {
				t.Fatal(transceiverErr)
			}
			dc, err := viewer.CreateDataChannel("input", nil)
			if err != nil {
				t.Fatal(err)
			}
			opened := make(chan struct{})
			dc.OnOpen(func() { close(opened) })
			parent, cancel := context.WithCancel(context.WithValue(context.Background(), captureConstructorMarker{}, "original"))
			defer cancel()
			answer, _, err = server.HandleViewerOfferHandleContext(parent, "viewer", captureConstructorOffer(t, viewer))
			if err != nil {
				t.Fatal(err)
			}
			captureConstructorAnswer(t, viewer, answer)
			select {
			case <-opened:
			case <-time.After(5 * time.Second):
				t.Fatal("real input data channel did not open")
			}
			if err := dc.SendText(`{"kind":"key_down","key":"a"}`); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-received:
				if got.id != "viewer" || got.raw != `{"kind":"key_down","key":"a"}` {
					t.Fatalf("constructor callback id=%q raw=%q", got.id, got.raw)
				}
				if constructor == "context" && (got.ctx == nil || got.ctx.Value(captureConstructorMarker{}) != "original" || got.ctx.Err() != nil) {
					t.Fatalf("constructor lost original live source context: %v", got.ctx)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("production constructor did not deliver real input")
			}
		})
	}
}
