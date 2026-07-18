//go:build !lite

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
		t.Errorf("waitForTracks returned after %s, want it to wait out the %s audio grace first", elapsed, 100*time.Millisecond)
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
