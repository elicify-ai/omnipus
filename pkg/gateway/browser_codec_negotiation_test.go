// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// browser_codec_negotiation_test.go — coverage gap fill for Wave 2 component M
// (live-browser video streaming, ADR-044;
// docs/internal/specs/live-browser-video-streaming-spec.md). Adds direct unit
// coverage for the DS-1 codec-negotiation dataset (Test 9,
// negotiateVideoCodec/negotiateAudio, browser_stream.go) and Test 19
// (TestAudioChunk_SuppressedWhenNotNegotiated, FR-011/FR-023) that
// browser_stream_test.go and browser_metrics_test.go exercise only
// incidentally as a side effect of other scenarios. Hermetic: every external
// dependency is faked exactly like browser_stream_test.go (same package, same
// fakes/helpers — no real Chrome, no CDP, no network).

package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// ---- DS-1: codec negotiation, single encode (Test 9) ----

// TestNegotiateVideoCodec covers the DS-1 rows that resolve entirely within
// negotiateVideoCodec (no active stream yet): the default producible-codec
// priority order is H.264-main first ("avc1.4D4028"), VP8 next
// (defaultProducibleVideoCodecs, browser_stream.go).
func TestNegotiateVideoCodec(t *testing.T) {
	cases := []struct {
		name       string
		viewerCaps []string
		want       string
	}{
		// DS-1 row 1: [h264-main, vp8] -> h264-main (priority order picks the
		// first producible codec the viewer's caps intersect).
		{
			"viewer offers both -> h264-main wins priority",
			[]string{"avc1.4D401E", "vp8"},
			"avc1.4D4028",
		},
		// DS-1 row 2: [vp8] -> vp8 (h264-main isn't offered, falls through to
		// the next producible codec).
		{"viewer offers vp8 only -> vp8", []string{"vp8"}, "vp8"},
		// DS-1 row 4: [] -> unavailable-state (empty string signals "no
		// intersecting codec" to ensureStreamLocked, which sends the generic
		// unavailable state).
		{"viewer offers nothing -> unavailable (empty string)", []string{}, ""},
		// Not a DS-1 row per se, but the same "no intersection" case as a nil
		// slice (as opposed to an explicit empty slice) must behave
		// identically — a nil viewerCaps is what a genuinely absent
		// video_caps field on the wire deserializes to.
		{"nil viewer caps -> unavailable (empty string)", nil, ""},
		// A codec family neither H.264 nor VP8 (e.g. AV1) never intersects
		// the v1 producible set (VP9/AV1 are "available but not
		// v1-negotiated" per the spec's Encoder codecs note) -> unavailable.
		{
			"viewer offers only av1 -> unavailable (no v1 producible match)",
			[]string{"av01.0.04M.08"},
			"",
		},
	}

	o := newOrchestrator(BrowserVideoDeps{})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := o.negotiateVideoCodec(tc.viewerCaps)
			if got != tc.want {
				t.Fatalf("negotiateVideoCodec(%v) = %q, want %q", tc.viewerCaps, got, tc.want)
			}
		})
	}
}

// TestNegotiateVideoCodec_H264Baseline covers DS-1 row 5
// (`[h264-baseline]` (encoder-unsupported) -> unavailable-state, US-5/AC-1)
// and FR-006 ("negotiate H.264 main-first, VP8 next, NOT H.264 baseline
// avc1.42E01E, unsupported").
//
// codecFamily (browser_stream.go) discriminates the H.264 family by
// profile_idc (avc1.PPCCLL — "42" = Baseline -> "h264-42", "4d" = Main ->
// "h264-4d"), so a viewer offering ONLY H.264 Baseline ("avc1.42E01E") does
// NOT match the h264-MAIN producible codec ("avc1.4D4028") and correctly
// negotiates the unavailable state. Level (LL) differences within a profile
// still match — that is asserted by TestNegotiateVideoCodec's happy-path rows.
func TestNegotiateVideoCodec_H264Baseline(t *testing.T) {
	o := newOrchestrator(BrowserVideoDeps{})
	got := o.negotiateVideoCodec([]string{"avc1.42E01E"})
	if got != "" {
		t.Fatalf(
			"negotiateVideoCodec([avc1.42E01E]) = %q, want \"\" (unavailable) per FR-006/DS-1 row 5",
			got,
		)
	}
}

// TestNegotiateAudio covers negotiateAudio's two-sided gate (FR-011/FR-023):
// audio is negotiated only when BOTH the host has audio available
// (component K's classifier) AND the viewer advertises Opus support.
func TestNegotiateAudio(t *testing.T) {
	cases := []struct {
		name            string
		audioAvailable  bool
		viewerAudioCaps []string
		want            bool
	}{
		{"host has audio, viewer offers opus -> negotiated", true, []string{"opus"}, true},
		{
			"host has audio, viewer offers a non-opus codec -> not negotiated",
			true,
			[]string{"pcm"},
			false,
		},
		{"host has audio, viewer offers nothing -> not negotiated", true, nil, false},
		{
			"host has no audio, viewer offers opus -> not negotiated (host-gated, US-11/AC-2)",
			false,
			[]string{"opus"},
			false,
		},
		{"host has no audio, viewer offers nothing -> not negotiated", false, nil, false},
	}

	o := newOrchestrator(BrowserVideoDeps{})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capab := browser.VideoCapability{Capable: true, AudioAvailable: tc.audioAvailable}
			got := o.negotiateAudio(capab, tc.viewerAudioCaps)
			if got != tc.want {
				t.Fatalf(
					"negotiateAudio(AudioAvailable=%v, %v) = %v, want %v",
					tc.audioAvailable,
					tc.viewerAudioCaps,
					got,
					tc.want,
				)
			}
		})
	}
}

// TestStream_SingleEncodeMismatch_NoSecondEncoderSpawned covers DS-1 row 3
// (active encode h264-main, viewer video_caps [vp8] -> unavailable-state,
// US-6/AC-2) at the orchestrator level: v1 is single-encode-per-source
// (FR-006/"Out of scope: a second concurrent encoder for disjoint-codec
// viewers"), so a second viewer whose caps don't intersect the ALREADY-ACTIVE
// stream's codec must be moved to the unavailable state and must NOT trigger
// a second encoder launch.
func TestStream_SingleEncodeMismatch_NoSecondEncoderSpawned(t *testing.T) {
	ing := newFakeIngest()
	launch := &fakeLauncher{}
	capb := &fakeCaptureStarter{}
	srv := &fakeEncoderServer{}
	o := newTestOrchestrator(capable(), ing, launch.launch, capb.start, srv)

	// First viewer negotiates h264 (default producible priority) and brings
	// up the stream's one-and-only encoder for this agent tab.
	wc1 := newTestVideoConn()
	h1, err := o.AttachViewer(AttachParams{
		WC: wc1, AgentID: "agent1", SessionID: "sess1", ViewerID: "v1",
		AgentCtx:  context.Background(),
		VideoCaps: []string{"avc1.4D401E"},
	})
	if err != nil || h1 == nil {
		t.Fatalf("expected a live h264 stream; err=%v handle=%v", err, h1)
	}
	defer h1.Detach()
	if got := launch.count(); got != 1 {
		t.Fatalf("expected exactly 1 encoder launched for the first viewer, got %d", got)
	}
	activeCodec := launch.cfgAt(0).VideoCodec
	if !strings.HasPrefix(codecFamily(activeCodec), "h264") {
		t.Fatalf("expected the active encoder to be an h264 profile, got %q", activeCodec)
	}

	// A second viewer on the SAME agent tab supports ONLY vp8. The active
	// encoder is already producing h264 and v1 spawns no second concurrent
	// encoder for a disjoint codec (FR-006/US-6 AC-2): this viewer must get
	// the unavailable state, and the encoder-launch count must stay at 1.
	wc2 := newTestVideoConn()
	h2, err := o.AttachViewer(AttachParams{
		WC: wc2, AgentID: "agent1", SessionID: "sess2", ViewerID: "v2",
		AgentCtx:  context.Background(),
		VideoCaps: []string{"vp8"},
	})
	if err != nil {
		t.Fatalf("AttachViewer error: %v", err)
	}
	if h2 != nil {
		t.Fatal("expected a nil handle — a vp8-only viewer must not join an active h264 stream")
	}
	if got := launch.count(); got != 1 {
		t.Fatalf(
			"no second encoder may be spawned for the mismatched viewer (FR-006); launch count = %d, want 1",
			got,
		)
	}
	if got := ing.mintCount(); got != 1 {
		t.Fatalf(
			"no second ingest token may be minted for the mismatched viewer; mint count = %d, want 1",
			got,
		)
	}

	f := findStatusError(t, wc2)
	if f.Message == nil || *f.Message != browserVideoUnavailableMessage {
		t.Fatalf("expected the generic unavailable message, got %v", f.Message)
	}

	// The first viewer's stream is unaffected by the second viewer's
	// rejected attach.
	if o.lookupAgentStream("agent1") == nil {
		t.Fatal("the active h264 stream must survive an unrelated codec-mismatched attach")
	}
}

// ---- Test 19: TestAudioChunk_SuppressedWhenNotNegotiated (FR-011/FR-023) ----

// TestAudioChunk_SuppressedWhenNotNegotiated proves audio suppression is
// enforced at the ONE real production gate for it: when audio isn't
// negotiated (negotiateAudio returned false), the encoder page is never
// granted HasAudio:true (EncoderLaunchCfg), so it never captures/produces an
// audio chunk in the first place — there is no separate per-chunk-kind filter
// at the relay/viewer-delivery layer (stream_relay.go's Ingest fans out
// whatever kind of chunk it's given to every attached viewer without
// discrimination), so the suppression has to happen upstream, at negotiation
// time. This also proves the FR-011 "audio absence never blocks video" half:
// video streams and is delivered to the viewer exactly as normal even though
// this stream never negotiated audio.
func TestAudioChunk_SuppressedWhenNotNegotiated(t *testing.T) {
	ing := newFakeIngest()
	launch := &fakeLauncher{}
	capb := &fakeCaptureStarter{}
	srv := &fakeEncoderServer{}
	// Host has NO audio available (e.g. PulseAudio sidecar absent/failed —
	// US-11/AC-2) even though the viewer itself advertises Opus support:
	// negotiateAudio is host-gated first, so this must still resolve to no
	// audio, never a viewer-side decision alone.
	noAudio := func(string) browser.VideoCapability {
		return browser.VideoCapability{Capable: true, AudioAvailable: false}
	}
	o := newTestOrchestrator(noAudio, ing, launch.launch, capb.start, srv)

	wc := newTestVideoConn()
	h, err := o.AttachViewer(AttachParams{
		WC: wc, AgentID: "agent1", SessionID: "sess1", ViewerID: "v1",
		AgentCtx:  context.Background(),
		VideoCaps: []string{"avc1.4D4028"},
		AudioCaps: []string{"opus"},
	})
	if err != nil || h == nil {
		t.Fatalf("expected a live video stream even with no audio; err=%v handle=%v", err, h)
	}
	defer h.Detach()

	// browser_stream_init must announce HasAudio=false — the SPA's contract
	// signal that this stream carries no audio.
	initFrame := drainStreamInit(t, wc)
	if initFrame.HasAudio {
		t.Fatal("expected HasAudio=false in stream_init when the host has no audio available")
	}

	// The real suppression mechanism: the encoder was launched WITHOUT an
	// audio grant, so it never captures/produces an audio chunk to suppress
	// in the first place (FR-023: audio capture must not depend on video
	// capture, but it DOES depend on this grant).
	if got := launch.cfgAt(0).HasAudio; got {
		t.Fatal("encoder must not be granted audio capture when audio wasn't negotiated")
	}

	// Video streams normally regardless (FR-011: audio absence never blocks
	// video) — a video chunk still reaches the viewer.
	streamID := ing.mintCalls[0]
	adapter := &videoRelayAdapter{orch: o}
	adapter.Ingest(
		streamID,
		EncodedChunk{
			Seq:     0,
			TS:      0,
			Key:     true,
			Codec:   launch.cfgAt(0).VideoCodec,
			Kind:    "video",
			Payload: []byte{9, 9},
		},
	)

	chunk := nextBinaryChunk(t, wc, time.Second)
	if chunk[13] != 0 { // wire kind byte: 0 == video (relayChunkEnvelopeHeaderBytes layout)
		t.Fatalf("expected a video chunk (kind byte=0), got kind byte %d", chunk[13])
	}
}
