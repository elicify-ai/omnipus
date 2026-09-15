package browser

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

type panelCaptureBlockingClose struct {
	*fakeRelay
	entered chan struct{}
	release chan struct{}
}

func (r *panelCaptureBlockingClose) Close() error {
	close(r.entered)
	<-r.release
	return r.fakeRelay.Close()
}

func TestPanelCaptureLifecycleRejectsAdmissionDuringOverlappingTeardown(t *testing.T) {
	for _, operation := range []string{"shutdown", "connection invalidation"} {
		t.Run(operation, func(t *testing.T) {
			m := &BrowserManager{}
			relay := &panelCaptureBlockingClose{fakeRelay: &fakeRelay{}, entered: make(chan struct{}), release: make(chan struct{})}
			cs, err := NewCaptureSessionWithDeps(m, "old", relay, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			cs.SetOnStopped(func() { m.ClearCaptureSession(cs) })
			if _, installErr := m.EnsureCaptureSessionForPanel("old", func() (*CaptureSession, error) { return cs, nil }); installErr != nil {
				t.Fatal(installErr)
			}
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				if operation == "shutdown" {
					m.Shutdown()
				} else {
					m.invalidateConnection()
				}
			}()
			released := false
			defer func() {
				if !released {
					close(relay.release)
				}
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Error("teardown failed to finish after relay release")
				}
			}()
			select {
			case <-relay.entered:
			case <-time.After(time.Second):
				t.Fatal("teardown never reached relay Close")
			}
			for _, phase := range []string{"during first teardown", "after overlapping teardown"} {
				if phase == "after overlapping teardown" {
					overlapped := make(chan struct{})
					go func() {
						defer close(overlapped)
						if operation == "shutdown" {
							m.invalidateConnection()
						} else {
							m.Shutdown()
						}
					}()
					select {
					case <-overlapped:
					case <-time.After(time.Second):
						t.Fatal("overlapping teardown waited on an already stopped capture")
					}
				}
				type admissionResult struct {
					capture       *CaptureSession
					err           error
					factoryCalled bool
				}
				admission := make(chan admissionResult, 1)
				go func() {
					factoryCalled := false
					got, replacementErr := m.EnsureCaptureSessionForPanel("new", func() (*CaptureSession, error) {
						factoryCalled = true
						return NewCaptureSessionWithDeps(m, "new", &fakeRelay{}, nil, nil)
					})
					admission <- admissionResult{got, replacementErr, factoryCalled}
				}()
				select {
				case got := <-admission:
					if got.err == nil || got.capture != nil || got.factoryCalled {
						t.Errorf("%s admitted capture %s: got=%p err=%v factory=%t", operation, phase, got.capture, got.err, got.factoryCalled)
					}
				case <-time.After(time.Second):
					t.Fatal("capture admission blocked during teardown")
				}
			}
			close(relay.release)
			released = true
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("teardown did not finish after relay release")
			}
			fresh := &CaptureSession{}
			got, err := m.EnsureCaptureSessionForPanel("fresh", func() (*CaptureSession, error) { return fresh, nil })
			if err != nil || got != fresh {
				t.Fatalf("completed teardown prevented fresh capture: got=%p err=%v", got, err)
			}
		})
	}
}

func TestPanelCaptureKeepsIndependentRoutes(t *testing.T) {
	m := &BrowserManager{}
	a, b := &CaptureSession{}, &CaptureSession{}
	for panel, want := range map[string]*CaptureSession{"panel-a": a, "panel-b": b} {
		got, err := m.EnsureCaptureSessionForPanel(panel, func() (*CaptureSession, error) { return want, nil })
		if err != nil || got != want {
			t.Fatalf("panel %s borrowed another capture: got=%p want=%p err=%v", panel, got, want, err)
		}
	}
	if m.CaptureSessionForPanel("panel-a") != a || m.CaptureSessionForPanel("panel-b") != b {
		t.Fatal("panel lookup did not retain each capture")
	}
	if got := m.CaptureSessionForPanel("unknown"); got != nil {
		t.Fatalf("unknown panel borrowed capture %p", got)
	}
	got, err := m.EnsureCaptureSessionForPanel("panel-a", func() (*CaptureSession, error) {
		t.Fatal("same-panel reuse unexpectedly invoked factory")
		return nil, fmt.Errorf("same-panel reuse unexpectedly invoked factory")
	})
	if err != nil || got != a {
		t.Fatalf("same panel did not reuse its capture: got=%p err=%v", got, err)
	}
}

func TestPanelCaptureLifecycleStopsEveryPanel(t *testing.T) {
	for _, operation := range []string{"shutdown", "connection invalidation"} {
		t.Run(operation, func(t *testing.T) {
			m := &BrowserManager{}
			captures := make(map[string]*CaptureSession)
			for _, panel := range []string{"a", "b"} {
				var cs *CaptureSession
				var err error
				cs, err = NewCaptureSessionWithDeps(m, panel, &fakeRelay{}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				cs.SetOnStopped(func() { m.ClearCaptureSession(cs) })
				t.Cleanup(cs.Stop)
				if got, err := m.EnsureCaptureSessionForPanel(panel, func() (*CaptureSession, error) { return cs, nil }); err != nil || got != cs {
					t.Fatal("fixture failed to install panel")
				}
				captures[panel] = cs
			}
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				if operation == "shutdown" {
					m.Shutdown()
				} else {
					m.invalidateConnection()
				}
			}()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("manager teardown blocked its capture cleanup callback")
			}
			for panel, cs := range captures {
				select {
				case <-cs.Done():
				default:
					t.Errorf("%s left panel %s capture alive", operation, panel)
				}
				if m.CaptureSessionForPanel(panel) != nil {
					t.Errorf("%s retained panel %s capture", operation, panel)
				}
			}
		})
	}
}

func TestPanelCaptureLifecycleObserverCoversExistingAndFuturePanels(t *testing.T) {
	m := &BrowserManager{}
	panels := map[string]*CaptureSession{"a": {}, "b": {}}
	for panel, cs := range panels {
		if _, err := m.EnsureCaptureSessionForPanel(panel, func() (*CaptureSession, error) { return cs, nil }); err != nil {
			t.Fatal(err)
		}
	}
	observed := make(map[string]int)
	m.SetVideoHealthObserver(func(ev VideoHealthEvent) { observed[ev.AgentID]++ })
	future := &CaptureSession{}
	if _, err := m.EnsureCaptureSessionForPanel("future", func() (*CaptureSession, error) { return future, nil }); err != nil {
		t.Fatal(err)
	}
	panels["future"] = future
	for panel, cs := range panels {
		cs.mu.Lock()
		observer := cs.onVideoHealth
		cs.mu.Unlock()
		if observer == nil {
			t.Errorf("panel %s did not receive health observer", panel)
			continue
		}
		observer(VideoHealthEvent{AgentID: panel})
	}
	for panel := range panels {
		if observed[panel] != 1 {
			t.Errorf("panel %s observer deliveries=%d, want one", panel, observed[panel])
		}
	}
	m.SetVideoHealthObserver(nil)
	for panel, cs := range panels {
		cs.mu.Lock()
		registered := cs.onVideoHealth != nil
		cs.mu.Unlock()
		if registered {
			t.Errorf("panel %s retained unregistered observer", panel)
		}
	}
}

func TestPanelCaptureFactoryFailureDoesNotBorrowOtherPanel(t *testing.T) {
	m := &BrowserManager{}
	a, b := &CaptureSession{}, &CaptureSession{}
	if got, err := m.EnsureCaptureSessionForPanel("a", func() (*CaptureSession, error) { return a, nil }); err != nil || got != a {
		t.Fatal("fixture failed to install first panel")
	}
	wantErr := errors.New("capture creation failed")
	got, err := m.EnsureCaptureSessionForPanel("b", func() (*CaptureSession, error) { return nil, wantErr })
	if !errors.Is(err, wantErr) || got != nil || m.CaptureSessionForPanel("b") != nil {
		t.Fatalf("failed panel borrowed or cached another capture: got=%p err=%v", got, err)
	}
	got, err = m.EnsureCaptureSessionForPanel("b", func() (*CaptureSession, error) { return b, nil })
	if err != nil || got != b || m.CaptureSessionForPanel("a") != a {
		t.Fatalf("panel retry disturbed capture ownership: got=%p err=%v", got, err)
	}
}

func TestPanelCaptureCleanupDoesNotClearReplacementOrOtherPanel(t *testing.T) {
	m := &BrowserManager{}
	old, other, replacement := &CaptureSession{}, &CaptureSession{}, &CaptureSession{}
	for panel, cs := range map[string]*CaptureSession{"a": old, "b": other} {
		if _, err := m.EnsureCaptureSessionForPanel(panel, func() (*CaptureSession, error) { return cs, nil }); err != nil {
			t.Fatal(err)
		}
	}
	m.ClearCaptureSession(old)
	if m.CaptureSessionForPanel("a") != nil || m.CaptureSessionForPanel("b") != other {
		t.Fatal("cleanup removed another panel or retained the stopped capture")
	}
	if _, err := m.EnsureCaptureSessionForPanel("a", func() (*CaptureSession, error) { return replacement, nil }); err != nil {
		t.Fatal(err)
	}
	m.ClearCaptureSession(old)
	if m.CaptureSessionForPanel("a") != replacement || m.CaptureSessionForPanel("b") != other {
		t.Fatal("stale cleanup removed replacement or another panel")
	}
}
