package webrtc

// In-package tests for ingest track LIVENESS (issue #674).
//
// The bug these pin: Session.videoTrack was assigned in exactly one place and
// never cleared — Close() cleared only ingestPC — while waitForTracks returned
// ok the instant videoTrack != nil, with no check that anything still fed it.
// So after the first successful ingest, EVERY later viewer offer was answered
// instantly against a dead track. That is why a black live-browser panel never
// recovered however many times the user closed and reopened it: the relay kept
// saying "video is ready" about a track nothing had written to for minutes.
//
// These hit the unexported state machine directly (a real pion
// PeerConnection driven through handleIngestStateChange, the SAME entry point
// pion's OnConnectionStateChange callback uses in production) so the death
// path is exercised without a 15s waitForTracksTimeout or a full Go<->Go wire
// flow. The end-to-end counterpart lives in session_test.go
// (TestSessionIngestDeath_RelayStopsClaimingItHasVideo).

import (
	"testing"
	"time"

	pion "github.com/pion/webrtc/v4"
)

// newLiveIngestSession returns a Session with a real (never negotiated)
// PeerConnection installed as the current ingest connection and both shared
// local tracks marked live, i.e. the exact state the relay is in while a
// healthy capture is streaming.
func newLiveIngestSession(t *testing.T) (*Session, *pion.PeerConnection) {
	t.Helper()
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	pc, err := s.buildPeerConnection(s.api, false)
	if err != nil {
		t.Fatalf("buildPeerConnection: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })

	s.mu.Lock()
	s.ingestPC = pc
	s.mu.Unlock()
	setLiveTrack(t, s, "video")
	setLiveTrack(t, s, "audio")
	return s, pc
}

// TestWaitForTracks_RefusesAViewerOnceTheIngestConnectionDied is the direct
// regression for #674: a viewer offer arriving AFTER the ingest connection
// died must be refused (so the SPA gets ErrNoIngestVideoTrack and says so),
// not answered against the leftover local tracks.
func TestWaitForTracks_RefusesAViewerOnceTheIngestConnectionDied(t *testing.T) {
	s, pc := newLiveIngestSession(t)

	// Baseline: while the feed is live, a viewer offer IS answerable. Without
	// this the test could pass for the wrong reason (e.g. if waitForTracks
	// started refusing everything).
	if v, a, ok := s.waitForTracks(500 * time.Millisecond); !ok || v == nil || a == nil {
		t.Fatalf("waitForTracks with a live ingest = (v=%v a=%v ok=%v), want both tracks — "+
			"the test's own premise is broken", v != nil, a != nil, ok)
	}

	// The ingest connection dies, reported exactly as pion reports it.
	s.handleIngestStateChange("[test]", pc, pion.PeerConnectionStateFailed)

	v, a, ok := s.waitForTracks(300 * time.Millisecond)
	if ok {
		t.Fatalf("waitForTracks = (v=%v a=%v ok=%v) after the ingest connection failed, want ok=false. "+
			"The shared local tracks outlive every ingest connection by design, so their mere "+
			"existence must never be read as 'video is flowing' — answering a viewer here produces "+
			"a permanently black panel that no amount of reopening fixes (#674)",
			v != nil, a != nil, ok)
	}
}

// TestWaitForTracks_AnswersAgainOnceAFreshFeedArrives is the other half of the
// same contract: invalidating liveness must not be a one-way latch that
// permanently bricks the relay. A new ingest attachment re-arms it.
func TestWaitForTracks_AnswersAgainOnceAFreshFeedArrives(t *testing.T) {
	s, pc := newLiveIngestSession(t)
	s.handleIngestStateChange("[test]", pc, pion.PeerConnectionStateFailed)

	if _, _, ok := s.waitForTracks(200 * time.Millisecond); ok {
		t.Fatal("waitForTracks = ok immediately after the ingest died, want ok=false")
	}

	// A fresh capture lands: attachIngestTrack re-marks the SAME shared local
	// tracks (never new ones — see videoFeedID's doc comment) as fed.
	setLiveTrack(t, s, "video")
	setLiveTrack(t, s, "audio")

	if v, a, ok := s.waitForTracks(500 * time.Millisecond); !ok || v == nil || a == nil {
		t.Fatalf("waitForTracks = (v=%v a=%v ok=%v) after a fresh ingest attached, want both tracks — "+
			"liveness must be re-armable or a single blip disables video for the process's life",
			v != nil, a != nil, ok)
	}
}

// TestWaitForTracks_SurvivesTheCloseOfASupersededIngestConnection guards the
// replacement window. A normal recapture closes the OLD connection AFTER the
// new one is installed; that close must not retire the NEW connection's feed.
func TestWaitForTracks_SurvivesTheCloseOfASupersededIngestConnection(t *testing.T) {
	s, old := newLiveIngestSession(t)

	// A newer offer installs its own connection and attaches its own feed,
	// exactly as HandleIngestOffer -> attachIngestTrack does.
	fresh, err := s.buildPeerConnection(s.api, false)
	if err != nil {
		t.Fatalf("buildPeerConnection: %v", err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	s.mu.Lock()
	s.ingestPC = fresh
	s.mu.Unlock()
	setLiveTrack(t, s, "video")
	setLiveTrack(t, s, "audio")

	// Now the superseded connection reports its (expected, orderly) death.
	s.handleIngestStateChange("[test-old]", old, pion.PeerConnectionStateClosed)

	if v, _, ok := s.waitForTracks(500 * time.Millisecond); !ok || v == nil {
		t.Fatalf("waitForTracks = (v=%v ok=%v) after a SUPERSEDED ingest connection closed, "+
			"want the live replacement's tracks — a stale connection's teardown must never "+
			"retire its successor's feed", v != nil, ok)
	}
}

// TestEndFeed_OnlyRetiresItsOwnAttachment pins the identity check in endFeed.
// A forwarding goroutine whose blocking Read finally unblocks long after its
// connection was replaced must not clear the successor's liveness on its way
// out — the failure mode would be a viewer refused against a perfectly healthy
// stream, which is just as wrong as answering against a dead one.
func TestEndFeed_OnlyRetiresItsOwnAttachment(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	stale := s.feedSeq.Add(1)
	setLiveTrack(t, s, "video") // mints a NEWER feed token for the same kind

	s.mu.Lock()
	current := s.videoFeedID
	s.mu.Unlock()

	s.endFeed("[test]", pion.RTPCodecTypeVideo, stale)

	s.mu.Lock()
	after := s.videoFeedID
	s.mu.Unlock()
	if after != current {
		t.Fatalf("videoFeedID = %d after a STALE feed ended, want the current feed %d left alone",
			after, current)
	}
}

// TestStats_ReportsLivenessNotMereTrackExistence pins the same honesty rule on
// the stats surface the gateway turns into the panel's has_audio and an
// operator reads in a dump.
func TestStats_ReportsLivenessNotMereTrackExistence(t *testing.T) {
	s, pc := newLiveIngestSession(t)

	if st := s.Stats(); !st.HasVideo || !st.HasAudio {
		t.Fatalf("Stats() = %+v with a live ingest, want HasVideo=true HasAudio=true", st)
	}

	s.handleIngestStateChange("[test]", pc, pion.PeerConnectionStateFailed)

	if st := s.Stats(); st.HasVideo || st.HasAudio {
		t.Fatalf("Stats() = %+v after the ingest connection failed, want HasVideo=false HasAudio=false — "+
			"the shared local tracks still exist but nothing writes to them", st)
	}
}
