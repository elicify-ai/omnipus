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

type attachContextResult struct {
	controlled bool
	err        error
}

func newAttachContextRegistry(t *testing.T) (*BrowserManager, *LiveViewRegistry) {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	r := newLiveViewRegistry(m)
	t.Cleanup(m.Shutdown)
	return m, r
}

func assertAttachContextMembership(t *testing.T, m *BrowserManager, r *LiveViewRegistry, viewer string, want bool, viewers int) {
	t.Helper()
	attached := false
	if lv, ok := r.lookup(testSessionID); ok {
		lv.mu.Lock()
		_, attached = lv.viewers[viewer]
		lv.mu.Unlock()
	}
	m.mu.Lock()
	count := 0
	if se := m.sessions[testSessionID]; se != nil {
		count = se.viewers
	}
	m.mu.Unlock()
	if attached != want || count != viewers {
		t.Fatalf("viewer %q attached=%t count=%d; want attached=%t count=%d", viewer, attached, count, want, viewers)
	}
}

func TestAttachContextPreCanceledDoesNotCreateOrRegister(t *testing.T) {
	m, r := newAttachContextRegistry(t)
	caller, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.AttachContext(caller, testSessionID, "viewer", nil, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("pre-canceled attach error=%v", err)
	}
	m.mu.Lock()
	count := len(m.sessions)
	m.mu.Unlock()
	if count != 0 {
		t.Errorf("pre-canceled attach created %d sessions", count)
	}
	assertAttachContextMembership(t, m, r, "viewer", false, 0)
}

func TestAttachContextCanceledAdmissionReturnsWithoutRegistration(t *testing.T) {
	m, r := newAttachContextRegistry(t)
	release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	caller, cancel := context.WithCancel(context.Background())
	done := make(chan attachContextResult, 1)
	go func() {
		controlled, err := r.AttachContext(caller, testSessionID, "viewer", nil, nil, nil)
		done <- attachContextResult{controlled, err}
	}()
	cancel()
	var got attachContextResult
	prompt := false
	select {
	case got = <-done:
		prompt = true
	case <-time.After(200 * time.Millisecond):
	}
	release()
	if !prompt {
		got = <-done
	}
	if !prompt || !errors.Is(got.err, context.Canceled) {
		t.Errorf("queued attach prompt=%t error=%v", prompt, got.err)
	}
	assertAttachContextMembership(t, m, r, "viewer", false, 0)
}

func TestAttachContextInitialCallbackCancellationCleansOnlyNewViewer(t *testing.T) {
	m, r := newAttachContextRegistry(t)
	if _, err := r.Attach(testSessionID, "sibling", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	caller, cancel := context.WithCancel(context.Background())
	callbacks := 0
	_, err := r.AttachContext(caller, testSessionID, "new", nil, nil, func(tabs []Tab, active int) {
		callbacks++
		if len(tabs) != 1 || active != 0 {
			t.Errorf("initial tabs=%d active=%d", len(tabs), active)
		}
		cancel()
	})
	if !errors.Is(err, context.Canceled) || callbacks != 1 {
		t.Errorf("canceled callback error=%v calls=%d", err, callbacks)
	}
	assertAttachContextMembership(t, m, r, "new", false, 1)
	assertAttachContextMembership(t, m, r, "sibling", true, 1)
}

func TestAttachContextAcceptedViewerSurvivesOperationCancellation(t *testing.T) {
	m, r := newAttachContextRegistry(t)
	caller, cancel := context.WithCancel(context.Background())
	_, err := r.AttachContext(caller, testSessionID, "viewer", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	assertAttachContextMembership(t, m, r, "viewer", true, 1)
	lv := r.view(testSessionID)
	lv.mu.Lock()
	tabCtx := lv.tabCtx
	lv.mu.Unlock()
	select {
	case <-tabCtx.Done():
		t.Fatalf("accepted target canceled: %v", tabCtx.Err())
	case <-time.After(30 * time.Millisecond):
	}
	r.Detach(testSessionID, "viewer")
	assertAttachContextMembership(t, m, r, "viewer", false, 0)
}

func TestAttachContextCancellationReachesPendingColdLaunch(t *testing.T) {
	m, r := newAttachContextRegistry(t)
	home := t.TempDir()
	cfg := BrowserConfig{Enabled: true, Headless: true, ExecPath: fakeChromeBinary(t), ProfileDir: filepath.Join(home, "profile"), PageTimeout: time.Second}
	coord := NewBrowserCoordinator(home, cfg)
	chromeMajorCache.Store(cfg.ExecPath, "152")
	t.Cleanup(func() { chromeMajorCache.Delete(cfg.ExecPath) })
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
	t.Cleanup(func() { once.Do(func() { close(release) }); coord.Shutdown(); coord.waitStartupDrain() })
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan attachContextResult, 1)
	go func() {
		controlled, err := r.AttachContext(caller, testSessionID, "viewer", nil, nil, nil)
		done <- attachContextResult{controlled, err}
	}()
	launchCtx := <-entered
	cancel()
	var got attachContextResult
	prompt := false
	select {
	case got = <-done:
		prompt = true
	case <-time.After(200 * time.Millisecond):
	}
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
	if !prompt || !propagated || !errors.Is(got.err, context.Canceled) {
		t.Errorf("cold attach prompt=%t launchCanceled=%t error=%v", prompt, propagated, got.err)
	}
	assertAttachContextMembership(t, m, r, "viewer", false, 0)
}

func TestAttachContextRegistrationSerializesTargetSwitch(t *testing.T) {
	m, r := newAttachContextRegistry(t)
	if _, err := m.Session(testSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenTab(testSessionID); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var callbacks atomic.Int32
	attached := make(chan error, 1)
	go func() {
		_, err := r.AttachContext(context.Background(), testSessionID, "viewer", nil, nil, func(tabs []Tab, active int) {
			if callbacks.Add(1) == 1 {
				if len(tabs) != 2 || active != 1 {
					t.Errorf("initial tabs=%d active=%d", len(tabs), active)
				}
				close(entered)
				<-release
			}
		})
		attached <- err
	}()
	<-entered
	switched := make(chan error, 1)
	go func() { _, err := m.SwitchTab(testSessionID, 0); switched <- err }()
	var switchErr error
	switchedEarly := false
	select {
	case switchErr = <-switched:
		switchedEarly = true
	case <-time.After(200 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if err := <-attached; err != nil {
		t.Errorf("attach=%v", err)
	}
	if !switchedEarly {
		switchErr = <-switched
	}
	if switchedEarly {
		t.Error("target switched during attachment registration's initial publication")
	}
	if switchErr != nil {
		t.Fatal(switchErr)
	}
	active, _, err := m.activeTargetSnapshot(testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	lv := r.view(testSessionID)
	lv.mu.Lock()
	bound := lv.tabCtx
	lv.mu.Unlock()
	if bound != active {
		t.Error("accepted viewer did not follow the subsequently selected target")
	}
}

func TestAttachContextAdmissionRetainsPageTimeout(t *testing.T) {
	m, r := newAttachContextRegistry(t)
	m.cfg.PageTimeout = 30 * time.Millisecond
	release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.AttachContext(context.Background(), testSessionID, "viewer", nil, nil, nil)
		done <- err
	}()
	var got error
	prompt := false
	select {
	case got = <-done:
		prompt = true
	case <-time.After(200 * time.Millisecond):
	}
	release()
	if !prompt {
		got = <-done
	}
	if !prompt || !errors.Is(got, context.DeadlineExceeded) {
		t.Errorf("admission ignored PageTimeout: prompt=%t error=%v", prompt, got)
	}
	assertAttachContextMembership(t, m, r, "viewer", false, 0)
}
