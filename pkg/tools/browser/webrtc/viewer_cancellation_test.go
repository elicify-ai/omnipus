package webrtc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	pion "github.com/pion/webrtc/v4"
)

func TestViewerAttachmentCancellationInterruptsTrackWait(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	s := NewSession(Config{}, nil, func(format string, _ ...any) {
		if strings.Contains(format, "offer received") {
			once.Do(func() { close(entered) })
		}
	})
	t.Cleanup(func() { _ = s.Close() })
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := s.HandleViewerOfferHandleContext(parent, "viewer", "offer-waiting-for-source")
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("viewer never entered track-wait path")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("canceled track wait error=%v", err)
		}
	case <-time.After(time.Second):
		t.Error("attachment cancellation left viewer waiting for source tracks")
		// Release the legacy wait deterministically after recording the failure;
		// this does not shorten its production timeout or strand a test worker.
		setLiveTrack(t, s, "video")
		setLiveTrack(t, s, "audio")
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("legacy wait did not drain after fixture tracks arrived")
		}
	}
}

func TestViewerAttachmentCancellationInterruptsCandidateGathering(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	_, offer := admissionOffer(t, s)
	setLiveTrack(t, s, "video")
	setLiveTrack(t, s, "audio")
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var settings pion.SettingEngine
	settings.SetInterfaceFilter(func(string) bool { enteredOnce.Do(func() { close(entered); <-release }); return true })
	s.apiViewer = pion.NewAPI(pion.WithSettingEngine(settings))
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		handle any
		err    error
	}
	done := make(chan result, 1)
	go func() {
		_, handle, err := s.HandleViewerOfferHandleContext(parent, "viewer", offer)
		done <- result{handle, err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("viewer candidate preparation never reached the network boundary")
	}
	cancel()
	var out result
	select {
	case out = <-done:
	case <-time.After(time.Second):
		t.Error("attachment cancellation left viewer blocked in candidate preparation")
		releaseOnce.Do(func() { close(release) })
		select {
		case out = <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("released candidate preparation did not drain")
		}
	}
	if !errors.Is(out.err, context.Canceled) {
		t.Errorf("canceled candidate error=%v", out.err)
	}
	// The external enumeration callback is deliberately not forcibly canceled.
	// Release it after measuring caller cancellation, then verify exact cleanup.
	releaseOnce.Do(func() { close(release) })
	s.CloseViewerIfCurrent(out.handle)
	s.viewersMu.Lock()
	count := len(s.viewers)
	s.viewersMu.Unlock()
	if count != 0 {
		t.Errorf("canceled candidate left %d registered viewers", count)
	}
}
