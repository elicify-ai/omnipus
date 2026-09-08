package browser

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

func TestSessionContextCancellationReachesColdLaunch(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	home := t.TempDir()
	cfg := BrowserConfig{Enabled: true, Headless: true, ExecPath: fakeChromeBinary(t), ProfileDir: filepath.Join(home, "profile"), PageTimeout: time.Second}
	coord := NewBrowserCoordinator(home, cfg)
	m.cfg = cfg
	m.started = false
	m.AttachSharedChrome(coord, testKey)
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	var once sync.Once
	coord.pipeLauncher = func(ctx context.Context, _ string, _ pipeLaunchConfig) (*pipeLaunchResult, error) {
		entered <- ctx
		<-release
		root, cancel := context.WithCancel(context.Background())
		return &pipeLaunchResult{rootCtx: root, cancel: cancel}, nil
	}
	t.Cleanup(func() { once.Do(func() { close(release) }); coord.Shutdown(); m.Shutdown() })
	caller, cancel := context.WithCancel(context.Background())
	done := make(chan startupResult, 1)
	go func() { root, err := m.SessionContext(caller, testSessionID); done <- startupResult{root, err} }()
	launchCtx := <-entered
	cancel()
	got, prompt := startupResultWithin(done)
	propagated := false
	select {
	case <-launchCtx.Done():
		propagated = true
	case <-time.After(200 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if !prompt {
		got = <-done
	}
	if !prompt || !errors.Is(got.err, context.Canceled) {
		t.Errorf("cold session cancellation prompt=%t err=%v", prompt, got.err)
	}
	if !propagated {
		t.Error("SessionContext lost caller cancellation before pipe launch")
	}
	m.mu.Lock()
	started, count := m.started, len(m.sessions)
	m.mu.Unlock()
	if started || count != 0 {
		t.Errorf("canceled cold request published manager state: started=%t sessions=%d", started, count)
	}
}

func TestSessionContextCanceledTabGateWaitDoesNotCreate(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	caller, cancel := context.WithCancel(context.Background())
	done := make(chan startupResult, 1)
	go func() { root, err := m.SessionContext(caller, testSessionID); done <- startupResult{root, err} }()
	cancel()
	got, prompt := startupResultWithin(done)
	release()
	if !prompt {
		got = <-done
	}
	if !prompt || !errors.Is(got.err, context.Canceled) {
		t.Errorf("queued caller cancellation prompt=%t err=%v", prompt, got.err)
	}
	m.mu.Lock()
	count := len(m.sessions)
	m.mu.Unlock()
	if count != 0 {
		t.Errorf("canceled caller queued outside startup created %d sessions", count)
	}
	m.Shutdown()
}

func TestSessionContextReturnedTargetOutlivesCaller(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	// Retain actual parent inheritance: the general tab fixture intentionally
	// ignores its parent and would hide a leaked request lifetime here.
	m.createTabFn = func(parent context.Context, _ target.ID) (*tabEntry, error) {
		child, cancel := chromedp.NewContext(parent)
		return &tabEntry{ctx: child, cancel: cancel, targetID: target.ID("persistent")}, nil
	}

	t.Cleanup(m.Shutdown)
	caller, cancel := context.WithCancel(context.Background())
	root, err := m.SessionContext(caller, testSessionID)
	if err != nil || root == nil {
		t.Fatalf("session failed: %v", err)
	}
	cancel()
	select {
	case <-root.Done():
		t.Fatalf("successful target inherited request cancellation: %v", root.Err())
	case <-time.After(50 * time.Millisecond):
	}
	again, err := m.SessionContext(context.Background(), testSessionID)
	if err != nil || again != root {
		t.Fatalf("successful target was not reusable: same=%t err=%v", again == root, err)
	}
}

func TestFirstAttachContextCancellationDoesNotWaitForLateWorker(t *testing.T) {
	caller, cancel := context.WithCancel(context.Background())
	entered, release, drained := make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runFirstAttachContext(caller, func() error { defer close(drained); close(entered); <-release; return nil }, time.Second)
	}()
	<-entered
	cancel()
	var err error
	prompt := false
	select {
	case err = <-done:
		prompt = true
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	<-drained
	if !prompt {
		err = <-done
	}
	if !prompt || !errors.Is(err, context.Canceled) {
		t.Errorf("first attach cancellation prompt=%t err=%v", prompt, err)
	}
}

func TestFirstAttachContextPreCanceledDoesNotDispatch(t *testing.T) {
	caller, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := runFirstAttachContext(caller, func() error { called = true; return nil }, time.Second)
	if called || !errors.Is(err, context.Canceled) {
		t.Errorf("pre-canceled attach called=%t err=%v", called, err)
	}
}

func TestSessionContextShutdownCancelsPendingStartup(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	home := t.TempDir()
	cfg := BrowserConfig{Enabled: true, Headless: true, ExecPath: fakeChromeBinary(t), ProfileDir: filepath.Join(home, "profile"), PageTimeout: time.Second}
	coord := NewBrowserCoordinator(home, cfg)
	m.cfg = cfg
	m.started = false
	m.AttachSharedChrome(coord, testKey)
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	var once sync.Once
	coord.pipeLauncher = func(ctx context.Context, _ string, _ pipeLaunchConfig) (*pipeLaunchResult, error) {
		entered <- ctx
		<-release
		root, cancel := context.WithCancel(context.Background())
		return &pipeLaunchResult{rootCtx: root, cancel: cancel}, nil
	}
	t.Cleanup(func() { once.Do(func() { close(release) }); coord.Shutdown(); coord.waitStartupDrain(); m.Shutdown() })
	done := make(chan startupResult, 1)
	go func() {
		root, err := m.SessionContext(context.Background(), testSessionID)
		done <- startupResult{root, err}
	}()
	launchCtx := <-entered
	m.Shutdown()
	got, prompt := startupResultWithin(done)
	canceled := false
	select {
	case <-launchCtx.Done():
		canceled = true
	case <-time.After(200 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if !prompt {
		got = <-done
	}
	if !prompt || !errors.Is(got.err, errBrowserSessionChanged) {
		t.Errorf("retired startup prompt=%t err=%v", prompt, got.err)
	}
	if !canceled {
		t.Error("manager shutdown did not cancel its pending startup request")
	}
	m.mu.Lock()
	started, count := m.started, len(m.sessions)
	m.mu.Unlock()
	if started || count != 0 {
		t.Errorf("late retired startup published manager state: started=%t sessions=%d", started, count)
	}
}
