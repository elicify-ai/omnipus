package webrtc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pion/interceptor"
	pion "github.com/pion/webrtc/v4"
)

type installedCleanupGateFactory struct {
	ingestCleanupGateFactory
	closed *sync.WaitGroup
}

func (f installedCleanupGateFactory) NewInterceptor(id string) (interceptor.Interceptor, error) {
	inner, err := f.ingestCleanupGateFactory.NewInterceptor(id)
	if err != nil {
		return nil, err
	}
	f.closed.Add(1)
	return &installedCleanupGate{Interceptor: inner, closed: f.closed}, nil
}

type installedCleanupGate struct {
	interceptor.Interceptor
	closed *sync.WaitGroup
}

func (g *installedCleanupGate) Close() error {
	defer g.closed.Done()
	return g.Interceptor.Close()
}

func TestIngestReplacementRetainsCapacityUntilInstalledCleanupFinishes(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	var closed sync.WaitGroup
	t.Cleanup(func() { _ = s.Close(); closed.Wait() })
	binding, err := s.BeginIngestBinding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, offer := admissionOffer(t, s)
	originalAPI := s.api
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	registry := &interceptor.Registry{}
	registry.Add(installedCleanupGateFactory{
		ingestCleanupGateFactory: ingestCleanupGateFactory{entered: entered, release: release},
		closed:                   &closed,
	})
	s.api = pion.NewAPI(pion.WithInterceptorRegistry(registry))
	request := func(id uint64) (string, error) {
		t.Helper()
		type result struct {
			answer string
			err    error
		}
		done := make(chan result, 1)
		go func() {
			answer, offerErr := s.HandleIngestOfferForBinding(context.Background(), binding, id, offer, 1, "original")
			done <- result{answer, offerErr}
		}()
		select {
		case result := <-done:
			return result.answer, result.err
		case <-time.After(3 * time.Second):
			unblock()
			<-done
			t.Fatal("replacement answer waited for previously installed connection cleanup")
			return "", nil
		}
	}
	if answer, offerErr := request(1); offerErr != nil || answer == "" {
		t.Fatalf("initial installed connection: answer bytes=%d error=%v", len(answer), offerErr)
	}
	// The resource contract permits four unfinished cleanup operations; the
	// current installed connection itself must consume no preparation slot.
	for id := uint64(2); id <= 5; id++ {
		if answer, offerErr := request(id); offerErr != nil || answer == "" {
			t.Fatalf("replacement %d: answer bytes=%d error=%v", id, len(answer), offerErr)
		}
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("previously installed connection did not reach cleanup")
		}
	}
	s.api = originalAPI
	s.mu.Lock()
	current := s.ingestPC
	s.mu.Unlock()
	answer, err := request(6)
	if !errors.Is(err, errIngestPreparationBusy) || answer != "" {
		t.Errorf("fifth blocked retirement admitted: answer bytes=%d error=%v", len(answer), err)
	}
	s.mu.Lock()
	unchanged := s.ingestPC == current
	s.mu.Unlock()
	if !unchanged {
		t.Error("saturated retirement capacity displaced the installed connection")
	}
	unblock()
	deadline := time.Now().Add(3 * time.Second)
	for id := uint64(7); ; id++ {
		answer, err = request(id)
		if err == nil {
			break
		}
		if !errors.Is(err, errIngestPreparationBusy) || time.Now().After(deadline) {
			t.Fatalf("completed retirement capacity was not reusable: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if answer == "" {
		t.Fatal("released capacity returned an empty replacement answer")
	}
}
