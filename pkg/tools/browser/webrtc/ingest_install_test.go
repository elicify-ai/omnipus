package webrtc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	pion "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

func blockedIngestWriter(t *testing.T, audio bool) (*Session, *pion.PeerConnection, func()) {
	t.Helper()
	s := NewSession(Config{}, nil, nil)
	old, err := s.buildPeerConnection(s.api, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = old.Close() })
	s.ingestPC = old
	s.videoFeedID = 11
	s.videoGeneration = 1
	s.videoTargetID = "old"
	forward := &s.videoForward
	if audio {
		forward = &s.audioForward
		s.audioFeedID = 11
	}
	forward.begin(11, 90000)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }); <-done }
	go func() {
		defer close(done)
		_, _ = forward.write(11, &rtp.Packet{Header: rtp.Header{SequenceNumber: 1, Timestamp: 100}}, time.Now(), func(*rtp.Packet) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	t.Cleanup(func() { unblock(); _ = s.Close() })
	return s, old, unblock
}

func replacementPeer(t *testing.T, s *Session) *pion.PeerConnection {
	t.Helper()
	pc, err := s.buildPeerConnection(s.api, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })
	return pc
}

func TestIngestInstallCanceledWriterWaitPreservesInstalledFeed(t *testing.T) {
	for _, audio := range []bool{false, true} {
		name := "video"
		if audio {
			name = "audio"
		}
		t.Run(name, func(t *testing.T) {
			s, old, unblock := blockedIngestWriter(t, audio)
			candidate := replacementPeer(t, s)
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			binding, err := s.BeginIngestBinding(context.Background())
			require.NoError(t, err)
			s.mu.Lock()
			s.ingestOfferID = 1
			s.mu.Unlock()
			admission := &ingestAdmission{ctx: caller, negotiation: caller, bindingToken: binding, offerID: 1}
			done := make(chan error, 1)
			go func() {
				_, err := s.installIngestCandidate(caller, candidate, admission, 7, "installed-tab")
				done <- err
			}()
			// The write is already inside its external callback. Give installation
			// an opportunity to reach that occupied ownership boundary before cancel.
			time.Sleep(20 * time.Millisecond)
			cancel()
			var result error
			prompt := false
			select {
			case result = <-done:
				prompt = true
			case <-time.After(200 * time.Millisecond):
			}
			unblock()
			if !prompt {
				result = <-done
			}
			require.True(t, prompt, "cancellation must not wait for the external RTP write")
			require.ErrorIs(t, result, context.Canceled)
			s.mu.Lock()
			installed, video, audioFeed := s.ingestPC, s.videoFeedID, s.audioFeedID
			s.mu.Unlock()
			require.Same(t, old, installed, "canceled candidate cannot replace the healthy peer")
			require.Equal(t, int64(11), video)
			forward := &s.videoForward
			if audio {
				require.Equal(t, int64(11), audioFeed)
				forward = &s.audioForward
			}
			accepted, err := forward.write(11, &rtp.Packet{Header: rtp.Header{SequenceNumber: 2, Timestamp: 200}}, time.Now(), func(*rtp.Packet) error { return nil })
			require.NoError(t, err)
			require.True(t, accepted, "canceled admission must not retire old writer ownership")
		})
	}
}

func TestIngestControlDoesNotHoldSessionWhileWriterBlocked(t *testing.T) {
	for _, operation := range []string{"install", "clear", "end", "close", "boundary"} {
		t.Run(operation, func(t *testing.T) {
			s, old, unblock := blockedIngestWriter(t, false)
			candidate := replacementPeer(t, s)
			done := make(chan struct{})
			go func() {
				defer close(done)
				switch operation {
				case "install":
					_, _ = s.installIngestCandidate(context.Background(), candidate, nil, 7, "installed-tab")
				case "clear":
					s.clearIngestIfCurrent("test", old, "failed")
				case "end":
					s.endFeed("test", pion.RTPCodecTypeVideo, 11)
				case "close":
					_ = s.Close()
				case "boundary":
					_, _, _, _ = s.CurrentVideoBoundary()
				}
			}()
			time.Sleep(20 * time.Millisecond)
			control := make(chan struct{})
			go func() { _ = s.Stats(); s.SetOnIngestLive(nil); close(control) }()
			responsive := false
			select {
			case <-control:
				responsive = true
			case <-time.After(200 * time.Millisecond):
			}
			unblock()
			<-done
			<-control
			require.True(t, responsive, "stats/control must complete before the stalled RTP writer is released")
		})
	}
}

func TestIngestInstallWaitsForOldWriteBeforeCommitting(t *testing.T) {
	s, old, unblock := blockedIngestWriter(t, false)
	candidate := replacementPeer(t, s)
	done := make(chan error, 1)
	go func() {
		previous, err := s.installIngestCandidate(context.Background(), candidate, nil, 7, "installed-tab")
		if err == nil && previous != old {
			err = errors.New("wrong previous peer")
		}
		done <- err
	}()
	early := false
	select {
	case <-done:
		early = true
	case <-time.After(40 * time.Millisecond):
	}
	unblock()
	if !early {
		require.NoError(t, <-done)
	}
	require.False(t, early, "installation cannot authorize new media while an old write remains in flight")
	s.mu.Lock()
	installed, feed := s.ingestPC, s.videoFeedID
	s.mu.Unlock()
	require.Same(t, candidate, installed)
	require.Zero(t, feed)
	accepted, err := s.videoForward.write(11, &rtp.Packet{}, time.Now(), func(*rtp.Packet) error { return nil })
	require.NoError(t, err)
	require.False(t, accepted, "retired feed must never write after installation commits")
}

func TestIngestCloseCancelsBindingBeforeWaitingForWriter(t *testing.T) {
	s, _, unblock := blockedIngestWriter(t, false)
	_, err := s.BeginIngestBinding(context.Background())
	require.NoError(t, err)
	s.mu.Lock()
	binding := s.ingestBindingCtx
	s.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	canceled := false
	select {
	case <-binding.Done():
		canceled = true
	case <-time.After(200 * time.Millisecond):
	}
	unblock()
	require.NoError(t, <-done)
	require.True(t, canceled, "session close must cancel pending signaling before waiting for an external writer")
}

func TestIngestInstalledLifetimeEndsBeforeRetirementWait(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		name := "clear"
		if closeSession {
			name = "close"
		}
		t.Run(name, func(t *testing.T) {
			s, old, unblock := blockedIngestWriter(t, false)
			lifetime, err := s.ingestTrackLifetime(old)
			require.NoError(t, err)
			done := make(chan struct{})
			go func() {
				defer close(done)
				if closeSession {
					_ = s.Close()
				} else {
					s.clearIngestIfCurrent("test", old, "failed")
				}
			}()
			ended := false
			select {
			case <-lifetime.Done():
				ended = true
			case <-time.After(200 * time.Millisecond):
			}
			unblock()
			<-done
			require.True(t, ended, "retired PC lifetime must cancel before waiting for its writer")
		})
	}
}

func TestIngestReplacementCancelsOnlyPreviousInstalledLifetime(t *testing.T) {
	s, old, unblock := blockedIngestWriter(t, false)
	unblock()
	previous, err := s.ingestTrackLifetime(old)
	require.NoError(t, err)
	candidate := replacementPeer(t, s)
	_, err = s.installIngestCandidate(context.Background(), candidate, nil, 7, "installed-tab")
	require.NoError(t, err)
	current, err := s.ingestTrackLifetime(candidate)
	require.NoError(t, err)
	require.ErrorIs(t, previous.Err(), context.Canceled, "successful replacement retires the old OnTrack admission lifetime")
	require.NoError(t, current.Err(), "installed lifetime must outlive successful negotiation")
	require.False(t, s.clearIngestIfCurrent("old", old, "closed"))
	require.NoError(t, current.Err(), "late old callback must preserve replacement lifetime")
}

func TestIngestTrackAdmissionCancelsBeforeBlockedWriterDrains(t *testing.T) {
	s, old, unblock := blockedIngestWriter(t, false)
	_, err := s.ingestTrackLifetime(old)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, release, err := s.acquireIngestTrackInstallation(old, pion.RTPCodecTypeVideo)
		if release != nil {
			release()
		}
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cleared := make(chan struct{})
	go func() { s.clearIngestIfCurrent("old", old, "closed"); close(cleared) }()
	prompt := false
	var result error
	select {
	case result = <-done:
		prompt = true
	case <-time.After(200 * time.Millisecond):
	}
	unblock()
	if !prompt {
		result = <-done
	}
	<-cleared
	require.True(t, prompt, "obsolete OnTrack waiter must stop before the external writer drains")
	require.Error(t, result)
}

func TestIngestInstallRevalidatesReservationAfterWriterWait(t *testing.T) {
	s, old, unblock := blockedIngestWriter(t, false)
	candidate := replacementPeer(t, s)
	binding, err := s.BeginIngestBinding(context.Background())
	require.NoError(t, err)
	s.mu.Lock()
	s.ingestOfferID = 1
	s.mu.Unlock()
	admission := &ingestAdmission{ctx: context.Background(), negotiation: context.Background(), bindingToken: binding, offerID: 1}
	done := make(chan error, 1)
	go func() {
		_, err := s.installIngestCandidate(admission.ctx, candidate, admission, 7, "installed-tab")
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	// A regression that nests Session.mu around writer admission must fail
	// an assertion, not hang the suite before its deferred cleanup can run.
	fallback := time.AfterFunc(time.Second, unblock)
	defer fallback.Stop()
	// A newer reservation is published under Session.mu before its old-offer
	// cancellation callback runs outside that lock. Revalidation must cover it.
	s.mu.Lock()
	s.ingestOfferID = 2
	s.mu.Unlock()
	unblock()
	require.ErrorIs(t, <-done, ErrStaleIngestOffer)
	s.mu.Lock()
	installed, feed := s.ingestPC, s.videoFeedID
	s.mu.Unlock()
	require.Same(t, old, installed)
	require.Equal(t, int64(11), feed)
}

func TestIngestLateFeedCleanupCannotRetireSuccessor(t *testing.T) {
	var forward mediaForwarder
	forward.begin(11, 90000)
	forward.begin(12, 90000)
	forward.retireIfFeed(11)
	accepted, err := forward.write(12, &rtp.Packet{Header: rtp.Header{SequenceNumber: 3, Timestamp: 200}}, time.Now(), func(*rtp.Packet) error { return nil })
	require.NoError(t, err)
	require.True(t, accepted, "cleanup of an old feed must preserve its successor")
	forward.retireIfFeed(12)
	accepted, err = forward.write(12, &rtp.Packet{}, time.Now(), func(*rtp.Packet) error { return nil })
	require.NoError(t, err)
	require.False(t, accepted, "the matching current feed must still retire")
}

func TestIngestTrackAdmissionRechecksBeforeDelayedCancellation(t *testing.T) {
	s, old, unblock := blockedIngestWriter(t, false)
	_, err := s.ingestTrackLifetime(old)
	require.NoError(t, err)
	candidate := replacementPeer(t, s)
	done := make(chan error, 1)
	go func() {
		_, release, err := s.acquireIngestTrackInstallation(old, pion.RTPCodecTypeVideo)
		if release != nil {
			release()
		}
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	// A regression that nests Session.mu around writer admission must fail
	// an assertion, not hang the suite before its deferred cleanup can run.
	fallback := time.AfterFunc(time.Second, unblock)
	defer fallback.Stop()
	// Model the committed replacement before its out-of-lock old-lifetime
	// cancellation is delivered. Identity, not cancellation timing, is decisive.
	s.mu.Lock()
	oldCancel := s.ingestMediaCancel
	s.ingestPC = candidate
	s.ingestMediaCtx, s.ingestMediaCancel = context.WithCancel(context.Background())
	s.mu.Unlock()
	defer oldCancel()
	unblock()
	require.ErrorIs(t, <-done, ErrStaleIngestOffer)
}

func TestIngestBoundaryRevalidatesAfterWriterWait(t *testing.T) {
	s, _, unblock := blockedIngestWriter(t, false)
	done := make(chan bool, 1)
	go func() { _, _, _, ok := s.CurrentVideoBoundary(); done <- ok }()
	time.Sleep(20 * time.Millisecond)
	// A regression that nests Session.mu around writer admission must fail
	// an assertion, not hang the suite before its deferred cleanup can run.
	fallback := time.AfterFunc(time.Second, unblock)
	defer fallback.Stop()
	// Connection clearing can publish retirement before the writer drains.
	s.mu.Lock()
	s.videoFeedID = 0
	s.mu.Unlock()
	unblock()
	require.False(t, <-done, "a retired feed's completed write cannot authorize a current boundary")
}
