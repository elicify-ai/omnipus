// media_sustained_growth_test.go closes two coverage gaps in
// TestSessionGoToGoFullFlow (session_test.go), which only asserts each of
// video/audio packet counts "> 5" ONCE and Stats().{HasAudio,AudioCodec,...}
// non-empty ONCE:
//
//  1. it never asserts the negotiated audio codec is SPECIFICALLY opus (only
//     that the string is non-empty — a codec-negotiation regression that
//     silently fell back to some other codec string would still pass);
//  2. it never asserts packet counts keep growing across a SECOND,
//     independent sampling window — a single snapshot cannot distinguish
//     "media streams continuously" from "one burst arrived, then the
//     encoder stalled/died" (the server-side analog of "the picture is
//     actually moving" / "the audio is actually live", not a one-shot
//     delivery).
//
// Package webrtc_test (external), reusing session_test.go's fixtures
// (newFakeEncoder, newFakeViewer, nonTrickleOffer, setAnswer, waitCond,
// safeLogf, testWait) rather than re-deriving them.
//
// Traces to: QA wave task "TDD WAVE — live-browser WebRTC video feature"
// items 4 ("Audio track is genuinely carried, not just counted") and 5
// ("Video frame advancement").

package webrtc_test

import (
	"testing"
	"time"

	pion "github.com/pion/webrtc/v4"

	relay "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// mediaGrowthSampleWindow is the pause between sampling windows. The
// pumpSamples fixture (session_test.go) writes at ~30fps — one sample per
// ~33ms — so 400ms is roughly a dozen samples' worth, generous enough that
// "no growth" is an unambiguous signal on a loaded CI box, not a rounding
// artifact of sampling right at a frame boundary.
const mediaGrowthSampleWindow = 400 * time.Millisecond

// TestSessionVideoPacketGrowth_SustainedAcrossTwoIndependentWindows proves
// video packet counts keep growing across TWO separate windows, not just
// once (TestSessionGoToGoFullFlow only ever samples a single "> 5" snapshot).
func TestSessionVideoPacketGrowth_SustainedAcrossTwoIndependentWindows(t *testing.T) {
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

	viewer := newFakeViewer(t, true)
	offerV := nonTrickleOffer(t, viewer.pc)
	answerV, err := sess.HandleViewerOffer("video-growth-viewer", offerV)
	if err != nil {
		t.Fatalf("HandleViewerOffer: %v", err)
	}
	setAnswer(t, viewer.pc, answerV)

	waitCond(t, testWait, "viewer to receive initial video RTP", func() bool { return viewer.videoPkts.Load() > 0 })

	w0 := viewer.videoPkts.Load()
	time.Sleep(mediaGrowthSampleWindow)
	w1 := viewer.videoPkts.Load()
	time.Sleep(mediaGrowthSampleWindow)
	w2 := viewer.videoPkts.Load()

	if w1 <= w0 {
		t.Fatalf(
			"video packets did not grow in sampling window 1: %d -> %d (single burst, not continuous streaming)",
			w0,
			w1,
		)
	}
	if w2 <= w1 {
		t.Fatalf(
			"video packets did not grow in sampling window 2: %d -> %d -> %d (one burst then silence, not sustained streaming)",
			w0,
			w1,
			w2,
		)
	}
	t.Logf("OBSERVED video packet growth across 2 independent windows: %d -> %d -> %d", w0, w1, w2)
}

// TestSessionAudioTrack_GenuinelyCarried_OpusCodecAndSustainedGrowth proves
// (a) Stats().HasAudio is true, (b) the negotiated codec is SPECIFICALLY
// opus (audio/opus), not merely non-empty, and (c) audio packet counts keep
// growing across two independent sampling windows, not just once.
func TestSessionAudioTrack_GenuinelyCarried_OpusCodecAndSustainedGrowth(t *testing.T) {
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

	viewer := newFakeViewer(t, true)
	offerV := nonTrickleOffer(t, viewer.pc)
	answerV, err := sess.HandleViewerOffer("audio-growth-viewer", offerV)
	if err != nil {
		t.Fatalf("HandleViewerOffer: %v", err)
	}
	setAnswer(t, viewer.pc, answerV)

	waitCond(t, testWait, "viewer to receive initial audio RTP", func() bool { return viewer.audioPkts.Load() > 0 })

	stats := sess.Stats()
	if !stats.HasAudio {
		t.Fatal("Stats().HasAudio = false, want true once an audio track has attached")
	}
	if stats.AudioCodec != pion.MimeTypeOpus {
		t.Errorf(
			"Stats().AudioCodec = %q, want %q (the SPECIFIC negotiated codec, not merely a non-empty string)",
			stats.AudioCodec, pion.MimeTypeOpus,
		)
	}

	a0 := viewer.audioPkts.Load()
	time.Sleep(mediaGrowthSampleWindow)
	a1 := viewer.audioPkts.Load()
	time.Sleep(mediaGrowthSampleWindow)
	a2 := viewer.audioPkts.Load()

	if a1 <= a0 {
		t.Fatalf(
			"audio packets did not grow in sampling window 1: %d -> %d (single burst, not continuous streaming)",
			a0,
			a1,
		)
	}
	if a2 <= a1 {
		t.Fatalf(
			"audio packets did not grow in sampling window 2: %d -> %d -> %d (one burst then silence, not sustained streaming)",
			a0,
			a1,
			a2,
		)
	}
	t.Logf(
		"OBSERVED audio packet growth across 2 independent windows: %d -> %d -> %d (codec=%s)",
		a0,
		a1,
		a2,
		stats.AudioCodec,
	)
}
