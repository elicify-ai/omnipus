package webrtc

// In-package unit tests for waitForTracks' audio-grace behavior (UAT
// 2026-07-18: the first viewer's offer raced the ingest audio track's
// OnTrack and was answered video-only forever — the viewer leg has no
// renegotiation, so answer-time track presence is final for that viewer).
// The full Go<->Go media paths are covered by session_test.go (external
// package); these hit the unexported wait loop directly so the ordering
// cases are deterministic and fast.

import (
	"testing"
	"time"

	pion "github.com/pion/webrtc/v4"
)

func newTestTrack(t *testing.T, kind string) *pion.TrackLocalStaticRTP {
	t.Helper()
	caps := pion.RTPCodecCapability{MimeType: pion.MimeTypeVP8, ClockRate: 90000}
	if kind == "audio" {
		caps = pion.RTPCodecCapability{MimeType: pion.MimeTypeOpus, ClockRate: 48000, Channels: 2}
	}
	tr, err := pion.NewTrackLocalStaticRTP(caps, kind, "omnipus-browser")
	if err != nil {
		t.Fatalf("NewTrackLocalStaticRTP(%s): %v", kind, err)
	}
	return tr
}

func setGrace(t *testing.T, d time.Duration) {
	t.Helper()
	old := audioGraceTimeout
	audioGraceTimeout = d
	t.Cleanup(func() { audioGraceTimeout = old })
}

func TestWaitForTracksBothPresentReturnsImmediately(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	s.mu.Lock()
	s.videoTrack = newTestTrack(t, "video")
	s.audioTrack = newTestTrack(t, "audio")
	s.mu.Unlock()

	start := time.Now()
	v, a, ok := s.waitForTracks(time.Second)
	if !ok || v == nil || a == nil {
		t.Fatalf("waitForTracks = (v=%v a=%v ok=%v), want both tracks", v != nil, a != nil, ok)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("waitForTracks took %s with both tracks already present, want immediate", elapsed)
	}
}

func TestWaitForTracksWaitsOutTheAudioRace(t *testing.T) {
	// The UAT regression: video bound, audio's OnTrack a beat behind. The
	// wait must NOT answer video-only the instant video exists — it must
	// pick up the audio track that lands within the grace window.
	setGrace(t, 2*time.Second)
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	s.mu.Lock()
	s.videoTrack = newTestTrack(t, "video")
	s.mu.Unlock()

	go func() {
		time.Sleep(150 * time.Millisecond)
		s.mu.Lock()
		s.audioTrack = newTestTrack(t, "audio")
		s.mu.Unlock()
	}()

	v, a, ok := s.waitForTracks(time.Second)
	if !ok || v == nil {
		t.Fatalf("waitForTracks = (v=%v ok=%v), want video present", v != nil, ok)
	}
	if a == nil {
		t.Fatal("waitForTracks returned video-only even though audio arrived within the grace window")
	}
}

func TestWaitForTracksVideoOnlyAfterGrace(t *testing.T) {
	// Genuinely audio-less ingest: still tolerated (W1-C), just delayed by
	// the (shortened) grace rather than blocked forever.
	setGrace(t, 100*time.Millisecond)
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	s.mu.Lock()
	s.videoTrack = newTestTrack(t, "video")
	s.mu.Unlock()

	start := time.Now()
	v, a, ok := s.waitForTracks(time.Second)
	if !ok || v == nil {
		t.Fatalf("waitForTracks = (v=%v ok=%v), want ok with video", v != nil, ok)
	}
	if a != nil {
		t.Fatal("waitForTracks returned an audio track that was never set")
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf(
			"waitForTracks returned after %s, want it to wait out the %s audio grace first",
			elapsed,
			100*time.Millisecond,
		)
	}
}

func TestWaitForTracksNoVideoTimesOut(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	_, _, ok := s.waitForTracks(200 * time.Millisecond)
	if ok {
		t.Fatal("waitForTracks = ok with no ingest video track, want timeout (ok=false)")
	}
}

// TestWaitForTracksSurvivesObservedProductionColdStartLatency pins the exact
// real-world timing captured live on uat-omnipus, 2026-07-28 (DEBUG-level
// relay logs, two independent capture-session cycles): with a warm
// encoder/Chrome, ICE connected and the audio track's first RTP packet
// arrived in under 1s, but the VIDEO track's first RTP packet consistently
// arrived ~5.0-5.2s later (VP8 software-encoder warm-up/first-keyframe
// latency) — landing almost exactly on the boundary of the THEN-5s
// waitForTracksTimeout and losing the race on both observed cycles.
//
// This test proves the mechanism, not just restates the incident: it drives
// the REAL waitForTracks against a simulated video-arrival delay matched to
// the observed latency (using a conservative 5.2s, slightly past the
// observed ~5.0-5.1s, so this test does not itself sit on a knife's edge)
// and asserts:
//  1. the OLD 5s timeout would have lost this exact race (ok=false) —
//     reproducing the production symptom under test, so a future reader can
//     see the failure this fix closes without needing the live logs; and
//  2. the CURRENT waitForTracksTimeout constant wins it comfortably
//     (ok=true, video track returned) — so a future regression that shrinks
//     waitForTracksTimeout back toward the observed latency (or below it)
//     fails this test loudly instead of silently reintroducing the outage.
func TestWaitForTracksSurvivesObservedProductionColdStartLatency(t *testing.T) {
	const observedVideoArrivalDelay = 5200 * time.Millisecond
	const oldProductionTimeout = 5 * time.Second

	newSessionWithDelayedVideo := func(t *testing.T) *Session {
		t.Helper()
		s := NewSession(Config{}, nil, nil)
		t.Cleanup(func() { _ = s.Close() })
		go func() {
			time.Sleep(observedVideoArrivalDelay)
			s.mu.Lock()
			s.videoTrack = newTestTrack(t, "video")
			s.mu.Unlock()
		}()
		return s
	}

	t.Run("old 5s production timeout lost this exact race", func(t *testing.T) {
		s := newSessionWithDelayedVideo(t)
		_, _, ok := s.waitForTracks(oldProductionTimeout)
		if ok {
			t.Fatal("waitForTracks = ok against the OLD 5s timeout with a 5.2s video-arrival delay, " +
				"want timeout (ok=false) — this is supposed to reproduce the pre-fix production failure")
		}
	})

	t.Run("current waitForTracksTimeout wins the same race with margin", func(t *testing.T) {
		if waitForTracksTimeout <= observedVideoArrivalDelay {
			t.Fatalf(
				"waitForTracksTimeout (%s) has regressed to at or below the observed production "+
					"video-arrival latency (%s) — this WILL reproduce the 2026-07-28 outage, "+
					"see this const's doc comment in ingest.go before shrinking it",
				waitForTracksTimeout, observedVideoArrivalDelay,
			)
		}
		s := newSessionWithDelayedVideo(t)
		v, _, ok := s.waitForTracks(waitForTracksTimeout)
		if !ok || v == nil {
			t.Fatalf("waitForTracks = (v=%v ok=%v) against the current %s timeout with a %s video-arrival "+
				"delay, want ok=true with a video track", v != nil, ok, waitForTracksTimeout, observedVideoArrivalDelay)
		}
	})
}
