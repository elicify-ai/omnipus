package browser

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type startupResult struct {
	root context.Context
	err  error
}
type startupHarness struct {
	call        func(context.Context, string) (context.Context, error)
	stop        func()
	live        func() bool
	entered     chan context.Context
	release     chan struct{}
	canceled    chan struct{}
	launches    atomic.Int32
	requests    sync.WaitGroup
	releaseOnce sync.Once
}

func newStartupHarness(t *testing.T, pooled, lateSuccess bool) *startupHarness {
	t.Helper()
	h := &startupHarness{entered: make(chan context.Context, 16), release: make(chan struct{}), canceled: make(chan struct{}, 16)}
	home := t.TempDir()
	cfg := BrowserConfig{Enabled: true, Headless: true, ExecPath: fakeChromeBinary(t), ProfileDir: filepath.Join(home, "profile"), PageTimeout: time.Second}
	launcher := func(ctx context.Context, _ string, _ pipeLaunchConfig) (*pipeLaunchResult, error) {
		h.launches.Add(1)
		h.entered <- ctx
		if lateSuccess {
			<-h.release
		} else {
			select {
			case <-h.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		root, cancel := context.WithCancel(context.Background())
		var once sync.Once
		return &pipeLaunchResult{rootCtx: root, cancel: func() { cancel(); once.Do(func() { h.canceled <- struct{}{} }) }}, nil
	}
	if pooled {
		pool := NewBrowserPool(home, cfg)
		pool.availableMemory = func() (uint64, bool) { return 64 << 30, true }
		pool.newCoordinator = func(home string, cfg BrowserConfig, key BrowsingKey) *BrowserCoordinator {
			c := newKeyedCoordinator(home, cfg, key)
			c.pipeLauncher = launcher
			return c
		}
		h.call = func(ctx context.Context, _ string) (context.Context, error) {
			_, root, err := pool.Register(ctx, browserTestKey("startup"), &BrowserManager{})
			return root, err
		}
		h.stop = pool.Shutdown
		h.live = func() bool { pool.mu.Lock(); defer pool.mu.Unlock(); return len(pool.instances) != 0 }
	} else {
		c := NewBrowserCoordinator(home, cfg)
		c.pipeLauncher = launcher
		h.call = func(ctx context.Context, id string) (context.Context, error) {
			return c.Register(ctx, id, &BrowserManager{})
		}
		h.stop = c.Shutdown
		h.live = func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.launched || len(c.managers) != 0 }
	}
	t.Cleanup(func() {
		h.releaseOnce.Do(func() { close(h.release) })
		drained := make(chan struct{})
		go func() { h.requests.Wait(); close(drained) }()
		select {
		case <-drained:
		case <-time.After(2 * time.Second):
			t.Error("startup request workers did not drain")
		}
		h.stop()
	})
	return h
}
func (h *startupHarness) request(ctx context.Context, id string) <-chan startupResult {
	result := make(chan startupResult, 1)
	h.requests.Add(1)
	go func() { defer h.requests.Done(); root, err := h.call(ctx, id); result <- startupResult{root, err} }()
	return result
}
func (h *startupHarness) finish() { h.releaseOnce.Do(func() { close(h.release) }) }
func startupResultWithin(ch <-chan startupResult) (startupResult, bool) {
	select {
	case result := <-ch:
		return result, true
	case <-time.After(200 * time.Millisecond):
		return startupResult{}, false
	}
}
func forStartupLayers(t *testing.T, run func(*testing.T, bool)) {
	t.Helper()
	for _, pooled := range []bool{false, true} {
		name := "coordinator"
		if pooled {
			name = "pool"
		}
		t.Run(name, func(t *testing.T) { run(t, pooled) })
	}
}

func TestStartupCanceledFollowerReturnsBeforeLaunch(t *testing.T) {
	forStartupLayers(t, func(t *testing.T, pooled bool) {
		h := newStartupHarness(t, pooled, false)
		leader := h.request(context.Background(), "leader")
		<-h.entered
		caller, cancel := context.WithCancel(context.Background())
		follower := h.request(caller, "follower")
		cancel()
		got, prompt := startupResultWithin(follower)
		h.finish()
		first := <-leader
		if !prompt {
			got = <-follower
		}
		if !prompt || !errors.Is(got.err, context.Canceled) {
			t.Errorf("canceled follower prompt=%t err=%v", prompt, got.err)
		}
		if first.err != nil || first.root == nil {
			t.Errorf("live leader lost startup: %v", first.err)
		}
		if n := h.launches.Load(); n != 1 {
			t.Errorf("launches=%d, want one", n)
		}
	})
}

func TestStartupJoinedFollowerSurvivesLeaderCancellation(t *testing.T) {
	forStartupLayers(t, func(t *testing.T, pooled bool) {
		h := newStartupHarness(t, pooled, false)
		caller, cancel := context.WithCancel(context.Background())
		defer cancel()
		leader := h.request(caller, "leader")
		<-h.entered
		joined := &startupJoinObservedContext{Context: context.Background(), joined: make(chan struct{})}
		follower := h.request(joined, "follower")
		select {
		case <-joined.joined:
		case <-time.After(time.Second):
			h.finish()
			t.Fatal("follower did not join startup")
		}
		cancel()
		old, prompt := startupResultWithin(leader)
		h.finish()
		next := <-follower
		if !prompt {
			old = <-leader
		}
		if !prompt || !errors.Is(old.err, context.Canceled) {
			t.Errorf("old request prompt=%t err=%v", prompt, old.err)
		}
		if next.err != nil || next.root == nil || next.root.Err() != nil {
			t.Errorf("live follower lost shared startup: root=%v err=%v", next.root, next.err)
		}
		if n := h.launches.Load(); n != 1 {
			t.Errorf("joined follower required %d launches, want one", n)
		}
	})
}

func TestStartupCanceledSoleWaiterDisposesLateSuccess(t *testing.T) {
	forStartupLayers(t, func(t *testing.T, pooled bool) {
		h := newStartupHarness(t, pooled, true)
		caller, cancel := context.WithCancel(context.Background())
		result := h.request(caller, "sole")
		launchCtx := <-h.entered
		cancel()
		got, prompt := startupResultWithin(result)
		ctxCanceled := false
		select {
		case <-launchCtx.Done():
			ctxCanceled = true
		case <-time.After(200 * time.Millisecond):
		}
		h.finish()
		if !prompt {
			got = <-result
		}
		disposed := false
		select {
		case <-h.canceled:
			disposed = true
		case <-time.After(200 * time.Millisecond):
		}
		if !prompt || !errors.Is(got.err, context.Canceled) {
			t.Errorf("sole canceled request prompt=%t err=%v", prompt, got.err)
		}
		if !ctxCanceled {
			t.Error("last waiter did not cancel the launch lifetime")
		}
		if !disposed {
			t.Error("late successful Chrome was not disposed")
		}
		if h.live() {
			t.Error("canceled sole request published a browser or registration")
		}
	})
}

func TestStartupPreCanceledRequestCannotLaunchOrRegister(t *testing.T) {
	forStartupLayers(t, func(t *testing.T, pooled bool) {
		for _, warm := range []bool{false, true} {
			name := "cold"
			if warm {
				name = "warm"
			}
			t.Run(name, func(t *testing.T) {
				h := newStartupHarness(t, pooled, true)
				h.finish()
				wantLaunches := int32(0)
				if warm {
					first := <-h.request(context.Background(), "first")
					if first.err != nil {
						t.Fatal(first.err)
					}
					wantLaunches = 1
				}
				caller, cancel := context.WithCancel(context.Background())
				cancel()
				got := <-h.request(caller, "canceled")
				if !errors.Is(got.err, context.Canceled) || got.root != nil {
					t.Errorf("pre-canceled request returned root=%v err=%v", got.root, got.err)
				}
				if h.launches.Load() != wantLaunches {
					t.Errorf("pre-canceled request launched Chrome: got=%d want=%d", h.launches.Load(), wantLaunches)
				}
			})
		}
	})
}

func TestStartupShutdownDisposesLateSuccess(t *testing.T) {
	forStartupLayers(t, func(t *testing.T, pooled bool) {
		h := newStartupHarness(t, pooled, true)
		result := h.request(context.Background(), "late")
		<-h.entered
		h.stop()
		h.finish()
		got := <-result
		if got.err == nil || got.root != nil {
			t.Errorf("shutdown accepted late startup: root=%v err=%v", got.root, got.err)
		}
		disposed := false
		select {
		case <-h.canceled:
			disposed = true
		case <-time.After(200 * time.Millisecond):
		}
		if !disposed || h.live() {
			t.Errorf("shutdown left startup alive: disposed=%t live=%t", disposed, h.live())
		}
	})
}

// AfterFunc registration observes actual cohort membership; no scheduler delay
// is used as evidence that the follower has joined.
type startupJoinObservedContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (c *startupJoinObservedContext) Done() <-chan struct{} { return startupNeverDone }

var startupNeverDone = make(chan struct{})

func (c *startupJoinObservedContext) Value(any) any { return nil }
func (c *startupJoinObservedContext) AfterFunc(fn func()) func() bool {
	c.once.Do(func() { close(c.joined) })
	return func() bool { return true }
}
