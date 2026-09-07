package webrtc_test

import (
	"context"
	"testing"
	"time"

	relay "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	pion "github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

func TestVideoReceiptTracksOneFinitePacketThroughRealIngestReplacement(t *testing.T) {
	sess := relay.NewSession(relay.Config{}, nil, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })
	binding, err := sess.BeginIngestBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var previous relay.VideoReceipt
	for index, tc := range []struct {
		generation uint64
		target     string
		newBinding bool
	}{
		{7, "tab-A", false}, {8, "tab-B", false}, {8, "tab-B", true},
	} {
		if tc.newBinding {
			binding, err = sess.BeginIngestBinding(context.Background())
			if err != nil {
				t.Fatal(err)
			}
		}
		enc := newFakeEncoder(t, false)
		answer, err := sess.HandleIngestOfferForBinding(context.Background(), binding, uint64(index+1), nonTrickleOffer(t, enc.pc), tc.generation, tc.target)
		if err != nil {
			t.Fatal(err)
		}
		setAnswer(t, enc.pc, answer)
		if got := sess.Stats(); got.HasVideo || got.VideoGeneration != 0 || got.VideoTargetID != "" || got.VideoReceipt != previous || got.IngestBindingToken != binding {
			t.Fatalf("installed but unreceived source%d stats=%+v want old receipt%+v and no live video identity", index, got, previous)
		}
		waitCond(t, 5*time.Second, "encoder connected", func() bool { return enc.pc.ConnectionState() == pion.PeerConnectionStateConnected })
		// One sample fits one RTP packet. No ongoing pump: the first watchdog
		// sample must retain proof even when the page becomes static afterward.
		if err := enc.video.WriteSample(media.Sample{Data: []byte{0}, Duration: time.Second / 30}); err != nil {
			t.Fatal(err)
		}
		expected := relay.VideoReceipt{BindingToken: binding, Generation: tc.generation, TargetID: tc.target, Serial: uint64(index + 1)}
		waitCond(t, 3*time.Second, "one finite video receipt", func() bool { return sess.Stats().VideoReceipt == expected })
		got := sess.Stats()
		if !got.HasVideo || got.VideoGeneration != tc.generation || got.VideoTargetID != tc.target || got.VideoReceivedPackets != int64(index+1) {
			t.Fatalf("live source%d stats=%+v want generation%d target%s and%d packets", index, got, tc.generation, tc.target, index+1)
		}
		previous = expected
	}
}
