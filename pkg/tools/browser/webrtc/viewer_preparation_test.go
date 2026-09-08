package webrtc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestViewerPreparationBoundsCanceledNativeWorkers(t *testing.T) {
	var pool viewerPreparationPool
	var release, cleanupRelease [4]chan struct{}
	var cancel [4]context.CancelFunc
	entered := make(chan int, 4)
	cleanupEntered := make(chan int, 4)
	var cleanupFinished atomic.Int64
	type result struct {
		id  int
		err error
	}
	done := make(chan result, 4)
	closeOnce := func(ch chan struct{}) {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
	defer func() {
		for i := 0; i < 4; i++ {
			if cancel[i] != nil {
				cancel[i]()
			}
			if release[i] != nil {
				closeOnce(release[i])
			}
			if cleanupRelease[i] != nil {
				closeOnce(cleanupRelease[i])
			}
		}
	}()
	for i := 0; i < 4; i++ {
		release[i], cleanupRelease[i] = make(chan struct{}), make(chan struct{})
		var ctx context.Context
		ctx, cancel[i] = context.WithCancel(context.Background())
		go func(i int, ctx context.Context) {
			err := pool.run(ctx, func() error { entered <- i; <-release[i]; return nil }, func() {
				cleanupEntered <- i
				<-cleanupRelease[i]
				cleanupFinished.Add(1)
			})
			done <- result{i, err}
		}(i, ctx)
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("four native preparations did not start")
		}
	}
	for _, stop := range cancel {
		stop()
	}
	returned := 0
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
collect:
	for returned < 4 {
		select {
		case out := <-done:
			returned++
			if !errors.Is(out.err, context.Canceled) {
				t.Errorf("canceled caller %d error=%v", out.id, out.err)
			}
		case <-deadline.C:
			t.Errorf("native work retained %d canceled callers", 4-returned)
			break collect
		}
	}
	assertBusy := func(stage string) {
		t.Helper()
		called := false
		err := pool.run(context.Background(), func() error { called = true; return nil }, nil)
		if !errors.Is(err, errViewerPreparationBusy) || called {
			t.Errorf("%s admitted excess native work: called=%v error=%v", stage, called, err)
		}
	}
	assertBusy("after four canceled callers")
	closeOnce(release[0])
	select {
	case id := <-cleanupEntered:
		if id != 0 {
			t.Errorf("wrong native candidate cleanup=%d want0", id)
		}
	case <-time.After(time.Second):
		t.Error("retired native candidate was not cleaned after its work exited")
	}
	assertBusy("while native worker cleanup is still blocked")
	closeOnce(cleanupRelease[0])
	until := time.Now().Add(time.Second)
	reused := false
	for time.Now().Before(until) {
		err := pool.run(context.Background(), func() error { reused = true; return nil }, nil)
		if err == nil {
			break
		}
		if !errors.Is(err, errViewerPreparationBusy) {
			t.Fatalf("slot reuse error=%v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if !reused {
		t.Error("completed native worker did not release its slot")
	}
	for i := 1; i < 4; i++ {
		closeOnce(release[i])
		closeOnce(cleanupRelease[i])
	}
	for returned < 4 {
		select {
		case out := <-done:
			returned++
			if !errors.Is(out.err, context.Canceled) {
				t.Errorf("drained canceled caller %d error=%v", out.id, out.err)
			}
		case <-time.After(time.Second):
			t.Fatal("released native callers did not drain")
		}
	}
	until = time.Now().Add(time.Second)
	for cleanupFinished.Load() != 4 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if got := cleanupFinished.Load(); got != 4 {
		t.Errorf("cleaned retired native candidates=%d want4", got)
	}
}

func TestViewerPreparationCanceledAdmissionDoesNotRun(t *testing.T) {
	var pool viewerPreparationPool
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := pool.run(ctx, func() error { called = true; return nil }, nil)
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("canceled admission ran native work=%v error=%v", called, err)
	}
}

func TestViewerPreparationLegacyCancellationKeepsPublishedHandle(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	s := NewSession(Config{}, nil, func(format string, _ ...any) {
		if strings.Contains(format, "viewer count now") {
			enterOnce.Do(func() { close(entered); <-release })
		}
	})
	t.Cleanup(func() { _ = s.Close() })
	setLiveTrack(t, s, "video")
	setLiveTrack(t, s, "audio")
	_, offer := viewerRequestOffer(t, s)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		handle any
		err    error
	}
	done := make(chan result, 1)
	go func() { _, h, e := s.HandleViewerOfferHandleContext(parent, "viewer", offer); done <- result{h, e} }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("legacy registration did not reach post-publication boundary")
	}
	s.viewersMu.Lock()
	expected := s.viewers["viewer"].handle
	s.viewersMu.Unlock()
	cancel()
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) || got.handle != expected {
			t.Errorf("published legacy attempt lost exact cleanup: handle=%v want=%v error=%v", got.handle, expected, got.err)
		}
	case <-time.After(time.Second):
		t.Error("published legacy cancellation did not return")
	}
	releaseOnce.Do(func() { close(release) })
	until := time.Now().Add(3 * time.Second)
	for {
		s.viewerPreparations.mu.Lock()
		active := s.viewerPreparations.active
		s.viewerPreparations.mu.Unlock()
		if active == 0 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("released legacy preparation did not drain")
		}
		time.Sleep(time.Millisecond)
	}
}
