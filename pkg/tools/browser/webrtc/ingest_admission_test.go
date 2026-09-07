package webrtc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pion "github.com/pion/webrtc/v4"
)

// admissionOffer creates a real host-candidate offer; only process-edge
// logging/network callbacks are gated by individual tests.
func admissionOffer(t *testing.T, s *Session) (*pion.PeerConnection, string) {
	t.Helper()
	pc, err := s.buildPeerConnection(s.api, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	track, err := pion.NewTrackLocalStaticRTP(pion.RTPCodecCapability{MimeType: pion.MimeTypeVP8, ClockRate: 90000}, "video", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.AddTrack(track); err != nil {
		t.Fatal(err)
	}
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
	case <-time.After(3 * time.Second):
		t.Fatal("fixture candidate gathering timed out")
	}
	return pc, pc.LocalDescription().SDP
}

func TestIngestAdmissionLateCompletionCannotReplaceNewerOffer(t *testing.T) {
	for _, change := range []string{"binding", "offer"} {
		t.Run(change, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var armed atomic.Bool
			s := NewSession(Config{}, nil, func(format string, _ ...any) {
				if strings.Contains(format, "server gathering complete") && armed.CompareAndSwap(true, false) {
					close(entered)
					<-release
				}
			})
			t.Cleanup(func() { _ = s.Close() })
			parent := context.Background()
			binding, err := s.BeginIngestBinding(parent)
			if err != nil {
				t.Fatal(err)
			}
			basePeer, baseSDP := admissionOffer(t, s)
			baseAnswer, err := s.HandleIngestOfferForBinding(parent, binding, 1, baseSDP, 6, "tab")
			if err != nil {
				t.Fatal(err)
			}
			if err := basePeer.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: baseAnswer}); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			baseline := s.ingestPC
			s.mu.Unlock()
			deadline := time.Now().Add(3 * time.Second)
			for baseline.ConnectionState() != pion.PeerConnectionStateConnected && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if baseline.ConnectionState() != pion.PeerConnectionStateConnected {
				t.Fatal("baseline peer did not become healthy before replacement")
			}

			binding, err = s.BeginIngestBinding(parent)
			if err != nil {
				t.Fatal(err)
			}
			_, oldSDP := admissionOffer(t, s)
			_, newSDP := admissionOffer(t, s)
			oldDone := make(chan error, 1)
			armed.Store(true)
			go func() {
				_, err := s.HandleIngestOfferForBinding(parent, binding, 1, oldSDP, 7, "same-target")
				oldDone <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("old offer never reached its completion barrier")
			}
			s.mu.Lock()
			whilePending := s.ingestPC
			s.mu.Unlock()
			if whilePending != baseline || baseline.ConnectionState() != pion.PeerConnectionStateConnected {
				t.Fatal("pending replacement tore down the previously installed ingest")
			}
			nextBinding, nextOffer := binding, uint64(2)
			if change == "binding" {
				nextBinding, err = s.BeginIngestBinding(parent)
				nextOffer = 1
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.HandleIngestOfferForBinding(parent, nextBinding, nextOffer, newSDP, 7, "same-target"); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			newest := s.ingestPC
			s.mu.Unlock()
			if newest == nil || newest == baseline {
				t.Fatal("new offer did not install a replacement peer")
			}
			releaseOnce.Do(func() { close(release) })
			select {
			case err := <-oldDone:
				if !errors.Is(err, ErrStaleIngestOffer) {
					t.Errorf("late old offer error=%v, want ErrStaleIngestOffer", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("superseded offer did not finish")
			}
			s.mu.Lock()
			got := s.ingestPC
			s.mu.Unlock()
			if got != newest {
				t.Fatal("late old offer overwrote the newer installed peer for the same capture generation")
			}
		})
	}
}

func TestIngestAdmissionRejectsCanceledContexts(t *testing.T) {
	for _, which := range []string{"binding", "offer"} {
		t.Run(which, func(t *testing.T) {
			s := NewSession(Config{}, nil, nil)
			t.Cleanup(func() { _ = s.Close() })
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			binding, err := s.BeginIngestBinding(parent)
			if err != nil {
				t.Fatal(err)
			}
			_, sdp := admissionOffer(t, s)
			offer, cancelOffer := context.WithCancel(context.Background())
			defer cancelOffer()
			if which == "binding" {
				cancelParent()
			} else {
				cancelOffer()
			}
			_, err = s.HandleIngestOfferForBinding(offer, binding, 1, sdp, 7, "tab")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled %s admission error=%v, want context.Canceled", which, err)
			}
			s.mu.Lock()
			installed := s.ingestPC
			s.mu.Unlock()
			if installed != nil {
				t.Fatal("canceled admission installed a peer")
			}
		})
	}
}

func TestIngestAdmissionBindingTokenCannotWrap(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	s.ingestBindingToken = ^uint64(0)
	token, err := s.BeginIngestBinding(context.Background())
	if token != 0 || err == nil || err.Error() != "webrtc: ingest binding token exhausted" {
		t.Fatalf("exhausted binding=(%d,%v), want zero and explicit exhaustion", token, err)
	}
	if s.ingestBindingToken != ^uint64(0) {
		t.Fatal("exhausted binding token wrapped")
	}
}

func TestIngestAdmissionRejectsInvalidAndReplayedOfferIDs(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	oldBinding, err := s.BeginIngestBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.BeginIngestBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, sdp := admissionOffer(t, s)
	if _, err := s.HandleIngestOfferForBinding(context.Background(), binding, 2, sdp, 7, "tab"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	installed := s.ingestPC
	s.mu.Unlock()
	cases := []struct {
		name        string
		binding, id uint64
		invalid     bool
	}{
		{"zero", binding, 0, true}, {"above_safe_integer", binding, 9007199254740992, true}, {"maximum_uint64", binding, ^uint64(0), true},
		{"older", binding, 1, false}, {"replayed", binding, 2, false}, {"missing_binding", 0, 3, false}, {"retired_binding", oldBinding, 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.HandleIngestOfferForBinding(context.Background(), tc.binding, tc.id, sdp, 7, "tab")
			if tc.invalid {
				if err == nil || err.Error() != "webrtc: ingest offer_id must be a positive safe integer" {
					t.Errorf("invalid offer ID error=%v, want explicit safe-integer rejection", err)
				}
			} else if !errors.Is(err, ErrStaleIngestOffer) {
				t.Errorf("replayed/retired offer error=%v, want ErrStaleIngestOffer", err)
			}
			s.mu.Lock()
			current := s.ingestPC
			s.mu.Unlock()
			if current != installed {
				t.Fatal("rejected offer changed installed peer")
			}
		})
	}
}

func TestIngestAdmissionAcceptsSafeOfferIDBoundaries(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	_, sdp := admissionOffer(t, s)
	for _, id := range []uint64{1, 2, 9007199254740990, 9007199254740991} {
		binding, err := s.BeginIngestBinding(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		answer, err := s.HandleIngestOfferForBinding(context.Background(), binding, id, sdp, 7, "tab")
		if err != nil || !strings.HasPrefix(answer, "v=0") {
			t.Fatalf("valid offer ID %d answer=%q err=%v", id, answer, err)
		}
	}
}

func TestIngestAdmissionCancellationInterruptsCandidateGathering(t *testing.T) {
	for _, which := range []string{"binding_cancel", "offer_cancel", "new_binding"} {
		t.Run(which, func(t *testing.T) {
			s := NewSession(Config{}, nil, nil)
			t.Cleanup(func() { _ = s.Close() })
			_, sdp := admissionOffer(t, s)
			entered, release := make(chan struct{}), make(chan struct{})
			var enteredOnce, releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var settings pion.SettingEngine
			settings.SetInterfaceFilter(func(string) bool { enteredOnce.Do(func() { close(entered); <-release }); return true })
			s.api = pion.NewAPI(pion.WithSettingEngine(settings))
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			binding, err := s.BeginIngestBinding(parent)
			if err != nil {
				t.Fatal(err)
			}
			offer, cancelOffer := context.WithCancel(context.Background())
			defer cancelOffer()
			done := make(chan error, 1)
			go func() { _, err := s.HandleIngestOfferForBinding(offer, binding, 1, sdp, 7, "tab"); done <- err }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("candidate enumeration never reached the network boundary")
			}
			wanted := error(context.Canceled)
			switch which {
			case "binding_cancel":
				cancelParent()
			case "offer_cancel":
				cancelOffer()
			case "new_binding":
				wanted = ErrStaleIngestOffer
				rebound := make(chan error, 1)
				go func() { _, err := s.BeginIngestBinding(context.Background()); rebound <- err }()
				select {
				case err := <-rebound:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("binding advance blocked on old candidate cleanup")
				}
			}
			select {
			case err := <-done:
				if !errors.Is(err, wanted) {
					t.Fatalf("canceled gathering error=%v, want %v", err, wanted)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled offer remained blocked in candidate gathering")
			}
			s.mu.Lock()
			installed := s.ingestPC
			s.mu.Unlock()
			if installed != nil {
				t.Fatal("canceled gathering installed a peer")
			}
		})
	}
}

func TestIngestAdmissionBoundSessionRejectsLegacyBypass(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	_, sdp := admissionOffer(t, s)
	if _, err := s.HandleIngestOfferForGeneration(sdp, 7, "tab"); err != nil {
		t.Fatalf("never-bound legacy offer rejected: %v", err)
	}
	s.mu.Lock()
	baseline := s.ingestPC
	s.mu.Unlock()
	if _, err := s.BeginIngestBinding(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"legacy", "generation"} {
		t.Run(method, func(t *testing.T) {
			var err error
			if method == "legacy" {
				_, err = s.HandleIngestOffer(sdp)
			} else {
				_, err = s.HandleIngestOfferForGeneration(sdp, 7, "tab")
			}
			if !errors.Is(err, ErrStaleIngestOffer) {
				t.Errorf("unbound %s offer error=%v, want ErrStaleIngestOffer", method, err)
			}
			s.mu.Lock()
			current := s.ingestPC
			s.mu.Unlock()
			if current != baseline {
				t.Fatal("legacy bypass replaced an authenticated session's installed ingest")
			}
		})
	}
}

func TestIngestAdmissionRejectsMissingAndExpiredContexts(t *testing.T) {
	t.Run("nil_binding", func(t *testing.T) {
		s := NewSession(Config{}, nil, nil)
		t.Cleanup(func() { _ = s.Close() })
		token, err := s.BeginIngestBinding(nil)
		if token != 0 || err == nil || err.Error() != "webrtc: ingest binding: nil context" {
			t.Fatalf("nil binding=(%d,%v), want explicit rejection", token, err)
		}
	})
	t.Run("canceled_binding", func(t *testing.T) {
		s := NewSession(Config{}, nil, nil)
		t.Cleanup(func() { _ = s.Close() })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		token, err := s.BeginIngestBinding(ctx)
		if token != 0 || !errors.Is(err, context.Canceled) {
			t.Fatalf("expired binding=(%d,%v), want cancellation", token, err)
		}
	})
	for _, which := range []string{"nil_offer", "expired_offer"} {
		t.Run(which, func(t *testing.T) {
			s := NewSession(Config{}, nil, nil)
			t.Cleanup(func() { _ = s.Close() })
			binding, err := s.BeginIngestBinding(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, sdp := admissionOffer(t, s)
			var ctx context.Context
			if which == "expired_offer" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
			}
			_, err = s.HandleIngestOfferForBinding(ctx, binding, 1, sdp, 7, "tab")
			if which == "nil_offer" {
				if err == nil || err.Error() != "webrtc: ingest offer: nil negotiation context" {
					t.Fatalf("nil offer error=%v, want explicit rejection", err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expired offer error=%v, want deadline exceeded", err)
			}
		})
	}
}

func TestIngestAdmissionLegacyOfferCannotCrossFirstBinding(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	s := NewSession(Config{}, nil, func(format string, _ ...any) {
		if strings.Contains(format, "server gathering complete") && armed.CompareAndSwap(true, false) {
			close(entered)
			<-release
		}
	})
	t.Cleanup(func() { _ = s.Close() })
	_, oldSDP := admissionOffer(t, s)
	_, newSDP := admissionOffer(t, s)
	done := make(chan error, 1)
	armed.Store(true)
	go func() { _, err := s.HandleIngestOffer(oldSDP); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("legacy offer never reached completion barrier")
	}
	binding, err := s.BeginIngestBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleIngestOfferForBinding(context.Background(), binding, 1, newSDP, 7, "tab"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	newest := s.ingestPC
	s.mu.Unlock()
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-done:
		if !errors.Is(err, ErrStaleIngestOffer) {
			t.Errorf("pre-binding legacy completion error=%v, want ErrStaleIngestOffer", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("old legacy offer never finished")
	}
	s.mu.Lock()
	current := s.ingestPC
	s.mu.Unlock()
	if current != newest {
		t.Fatal("pre-binding legacy negotiation bypassed authenticated admission")
	}
}
