package webrtc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/interceptor"
	pion "github.com/pion/webrtc/v4"
)

type ingestCleanupGateFactory struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (f ingestCleanupGateFactory) NewInterceptor(string) (interceptor.Interceptor, error) {
	return &ingestCleanupGate{entered: f.entered, release: f.release}, nil
}

type ingestCleanupGate struct {
	interceptor.NoOp
	entered chan<- struct{}
	release <-chan struct{}
}

func (g *ingestCleanupGate) Close() error {
	g.entered <- struct{}{}
	<-g.release
	return nil
}

func TestIngestPreparationBoundsRetiredNativeCandidates(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	binding, err := s.BeginIngestBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoder, offer := admissionOffer(t, s)
	answer, err := s.HandleIngestOfferForBinding(context.Background(), binding, 1, offer, 1, "original")
	if err != nil {
		t.Fatal(err)
	}
	if callErr := encoder.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answer}); callErr != nil {
		t.Fatal(callErr)
	}
	s.mu.Lock()
	original := s.ingestPC
	s.mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for original.ConnectionState() != pion.PeerConnectionStateConnected && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if original.ConnectionState() != pion.PeerConnectionStateConnected {
		t.Fatal("original ingest did not connect")
	}
	originalAPI := s.api
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	cleanupEntered := make(chan struct{}, 4)
	cleanupRelease := make(chan struct{})
	var cleanupOnce sync.Once
	releaseCleanup := func() { cleanupOnce.Do(func() { close(cleanupRelease) }) }
	defer releaseCleanup()
	var enteredCount atomic.Int32
	// Four is the independently selected resource budget, matching viewer
	// preparation; canceled callers must not turn that budget into infinity.
	for i := 0; i < 4; i++ {
		entered := make(chan struct{})
		var once sync.Once
		var settings pion.SettingEngine
		settings.SetInterfaceFilter(func(string) bool {
			once.Do(func() { enteredCount.Add(1); close(entered); <-release })
			return true
		})
		registry := &interceptor.Registry{}
		registry.Add(ingestCleanupGateFactory{entered: cleanupEntered, release: cleanupRelease})
		s.api = pion.NewAPI(pion.WithSettingEngine(settings), pion.WithInterceptorRegistry(registry))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func(id uint64) {
			_, callErr := s.HandleIngestOfferForBinding(ctx, binding, id, offer, 2, "replacement")
			done <- callErr
		}(uint64(i + 2))
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatal("native preparation never reached interface enumeration")
		}
		cancel()
		select {
		case resultErr := <-done:
			if !errors.Is(resultErr, context.Canceled) {
				t.Errorf("canceled native request error=%v", resultErr)
			}
		case <-time.After(time.Second):
			t.Fatal("canceled native request retained its caller")
		}
	}
	s.api = originalAPI
	answer, err = s.HandleIngestOfferForBinding(context.Background(), binding, 6, offer, 2, "replacement")
	if err == nil || !strings.Contains(err.Error(), "ingest candidate preparation busy") || answer != "" {
		t.Errorf("fifth native candidate accepted: answer bytes=%d error=%v", len(answer), err)
	}
	s.mu.Lock()
	current := s.ingestPC
	s.mu.Unlock()
	if current != original || original.ConnectionState() != pion.PeerConnectionStateConnected || enteredCount.Load() != 4 {
		t.Error("native saturation displaced original ingest or exceeded four blocked workers")
	}
	unblock()
	for i := 0; i < 4; i++ {
		select {
		case <-cleanupEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("retired native candidate did not reach connection cleanup")
		}
	}
	answer, err = s.HandleIngestOfferForBinding(context.Background(), binding, 7, offer, 2, "replacement")
	if err == nil || !strings.Contains(err.Error(), "ingest candidate preparation busy") || answer != "" {
		t.Errorf("candidate capacity released before native cleanup: answer bytes=%d error=%v", len(answer), err)
	}
	releaseCleanup()
	deadline = time.Now().Add(3 * time.Second)
	for id := uint64(8); ; id++ {
		answer, err = s.HandleIngestOfferForBinding(context.Background(), binding, id, offer, 2, "replacement")
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "ingest candidate preparation busy") || time.Now().After(deadline) {
			t.Fatalf("released preparation capacity not reusable: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	s.mu.Lock()
	current = s.ingestPC
	s.mu.Unlock()
	if answer == "" || current == nil || current == original {
		t.Fatal("released-slot replacement did not install its own candidate")
	}
}
