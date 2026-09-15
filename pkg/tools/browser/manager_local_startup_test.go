package browser

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSessionContextLocalStartupCancellationReturnsBeforeLaunch(t *testing.T) {
	m, entered, release, disposed := blockedLocalStartupManager(t)
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan startupResult, 1)
	go func() { root, err := m.SessionContext(caller, testSessionID); done <- startupResult{root, err} }()
	launch := <-entered
	cancel()
	got, prompt := startupResultWithin(done)
	select {
	case <-launch.Done():
	case <-time.After(200 * time.Millisecond):
		t.Error("local launch did not receive caller cancellation")
	}
	release()
	if !prompt {
		got = <-done
	}
	if !prompt || !errors.Is(got.err, context.Canceled) {
		t.Errorf("local startup cancellation prompt=%t err=%v", prompt, got.err)
	}
	assertLocalStartupDrained(t, m, disposed)
	m.mu.Lock()
	started, tabs := m.started, len(m.sessions)
	m.mu.Unlock()
	if started || tabs != 0 {
		t.Errorf("canceled local launch published started=%t sessions=%d", started, tabs)
	}
}

func TestSessionContextLocalStartupShutdownDoesNotWaitForLaunch(t *testing.T) {
	testLocalStartupShutdown(t, false)
}

func TestSessionLocalStartupShutdownDoesNotWaitForLaunch(t *testing.T) {
	testLocalStartupShutdown(t, true)
}

func testLocalStartupShutdown(t *testing.T, legacy bool) {
	t.Helper()
	m, entered, release, disposed := blockedLocalStartupManager(t)
	done := make(chan startupResult, 1)
	go func() {
		var root context.Context
		var err error
		if legacy {
			root, err = m.Session(testSessionID)
		} else {
			root, err = m.SessionContext(context.Background(), testSessionID)
		}
		done <- startupResult{root, err}
	}()
	launch := <-entered
	stopped := make(chan struct{})
	go func() { m.Shutdown(); close(stopped) }()
	prompt := false
	select {
	case <-stopped:
		prompt = true
	case <-time.After(200 * time.Millisecond):
	}
	canceled := false
	select {
	case <-launch.Done():
		canceled = true
	case <-time.After(200 * time.Millisecond):
	}
	release()
	<-stopped
	got := <-done
	if !prompt || !canceled {
		t.Errorf("shutdown blocked by local launch: prompt=%t canceled=%t", prompt, canceled)
	}
	if !errors.Is(got.err, errBrowserSessionChanged) {
		t.Errorf("retired local request err=%v", got.err)
	}
	assertLocalStartupDrained(t, m, disposed)
	m.mu.Lock()
	started, tabs := m.started, len(m.sessions)
	m.mu.Unlock()
	if started || tabs != 0 {
		t.Errorf("retired local startup published started=%t sessions=%d", started, tabs)
	}
}

// Only the process boundary is blocked; manager admission and lifecycle remain real.
func blockedLocalStartupManager(t *testing.T) (*BrowserManager, <-chan context.Context, func(), <-chan struct{}) {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
	m.started = false
	m.cfg = BrowserConfig{Enabled: true, Headless: true, ExecPath: fakeChromeBinary(t), ProfileDir: filepath.Join(t.TempDir(), "profile"), PageTimeout: time.Second}
	entered, unblock := make(chan context.Context, 1), make(chan struct{})
	disposed := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(unblock) }) }
	m.pipeLauncherFn = func(ctx context.Context, _ string, _ pipeLaunchConfig) (*pipeLaunchResult, error) {
		entered <- ctx
		<-unblock
		root, cancel := context.WithCancel(context.Background())
		var once sync.Once
		return &pipeLaunchResult{rootCtx: root, cancel: func() { cancel(); once.Do(func() { close(disposed) }) }}, nil
	}
	t.Cleanup(func() { release(); m.Shutdown() })
	return m, entered, release, disposed
}

func assertLocalStartupDrained(t *testing.T, m *BrowserManager, disposed <-chan struct{}) {
	t.Helper()
	m.mu.Lock()
	flight := m.localStartup
	m.mu.Unlock()
	if flight != nil {
		select {
		case <-flight.done:
		case <-time.After(time.Second):
			t.Error("local startup worker did not drain")
		}
	}
	select {
	case <-disposed:
	case <-time.After(time.Second):
		t.Error("rejected local startup leaked its returned browser")
	}
}
