package browser

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/stretchr/testify/require"
)

func delayNextCreatedTarget(m *BrowserManager) (<-chan struct{}, func(), *atomic.Int32) {
	entered, release := make(chan struct{}), make(chan struct{})
	base := m.createTabFn
	var calls atomic.Int32
	canceled := &atomic.Int32{}
	m.createTabFn = func(ctx context.Context, id target.ID) (*tabEntry, error) {
		tab, err := base(ctx, id)
		if err != nil {
			return nil, err
		}
		if calls.Add(1) == 1 {
			cancel := tab.cancel
			tab.cancel = func() { canceled.Add(1); cancel() }
			close(entered)
			<-release
		}
		return tab, nil
	}
	return entered, func() { close(release) }, canceled
}

func TestLifecycleFirstCreationCannotResurrectRemovedSession(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		name := "close"
		if shutdown {
			name = "shutdown"
		}
		t.Run(name, func(t *testing.T) {
			m := newTestManagerWithFakeTabs(t)
			t.Cleanup(m.Shutdown)
			entered, release, canceled := delayNextCreatedTarget(m)
			done := make(chan error, 1)
			go func() { done <- m.createFirstTab(testSessionID) }()
			<-entered
			if shutdown {
				m.Shutdown()
			} else {
				m.CloseSession(testSessionID)
			}
			release()
			err := <-done
			require.ErrorIs(t, err, errBrowserSessionChanged)
			m.mu.Lock()
			count := len(m.sessions)
			m.mu.Unlock()
			require.Equal(t, 0, count, "removed session must stay absent after late creation")
			require.Equal(t, int32(1), canceled.Load(), "discarded creation must cancel its own target exactly once")
		})
	}
}

func TestLifecycleLateAppendOrAdoptionCannotRecreateClosedSession(t *testing.T) {
	for _, adopt := range []bool{false, true} {
		name := "append"
		if adopt {
			name = "adopt"
		}
		t.Run(name, func(t *testing.T) {
			m := newTestManagerWithFakeTabs(t)
			t.Cleanup(m.Shutdown)
			_, err := m.Session(testSessionID)
			require.NoError(t, err)
			entered, release, canceled := delayNextCreatedTarget(m)
			done := make(chan error, 1)
			go func() {
				if adopt {
					_, adoptErr := m.adoptTarget(testSessionID, "late-popup")
					done <- adoptErr
				} else {
					_, openErr := m.OpenTab(testSessionID)
					done <- openErr
				}
			}()
			<-entered
			m.CloseSession(testSessionID)
			release()
			err = <-done
			require.ErrorIs(t, err, errBrowserSessionChanged)
			m.mu.Lock()
			count := len(m.sessions)
			m.mu.Unlock()
			require.Equal(t, 0, count, "late target must not recreate the closed tab set")
			require.Equal(t, int32(1), canceled.Load(), "late target must be canceled exactly once")
		})
	}
}

func prepareIdleActiveTarget(t *testing.T) *BrowserManager {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	now := time.Date(2026, 9, 7, 1, 0, 0, 0, time.UTC)
	m.nowFn = func() time.Time { return now }
	m.cfg.IdleTTL = time.Minute
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	m.mu.Lock()
	se := m.sessions[testSessionID]
	se.tabs[0].title = "survivor"
	se.tabs[0].url = "about:blank"
	se.tabs[0].lastActivity = now
	se.tabs[1].title = "idle"
	se.tabs[1].url = "about:blank"
	se.tabs[1].lastActivity = now.Add(-2 * time.Minute)
	m.mu.Unlock()
	return m
}

func TestLifecycleReaperSkipsBusyTargetWithoutBlocking(t *testing.T) {
	m := prepareIdleActiveTarget(t)
	release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
	require.NoError(t, err)
	done := make(chan []string, 1)
	go func() { done <- m.ReapIdleSessions() }()
	var removed []string
	select {
	case removed = <-done:
	case <-time.After(250 * time.Millisecond):
		release()
		<-done
		t.Fatal("reaper waited behind active target operation")
	}
	release()
	require.Empty(t, removed)
	tabs, active, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Equal(t, []Tab{{Index: 0, Title: "survivor", URL: "about:blank", Active: false}, {Index: 1, Title: "idle", URL: "about:blank", Active: true}}, tabs)
	require.Equal(t, 1, active)
}

func TestLifecycleReaperPublishesSurvivorUnderAdmission(t *testing.T) {
	m := prepareIdleActiveTarget(t)
	var published []Tab
	var publishedActive = -1
	var admissionErr error
	publications := 0
	m.SetTabsChangedFunc(func(id string, tabs []Tab, active int) {
		publications++
		if publications > 1 {
			return
		}
		published = tabs
		publishedActive = active
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		_, admissionErr = m.SwitchTabContext(ctx, id, 0)
	})
	require.Empty(t, m.ReapIdleSessions())
	require.Equal(t, []Tab{{Index: 0, Title: "survivor", URL: "about:blank", Active: true}}, published)
	require.Equal(t, 0, publishedActive)
	require.Equal(t, 1, publications)
	require.ErrorIs(t, admissionErr, context.DeadlineExceeded, "reaper must retain admission through observer publication")
}

func TestLifecycleNewCreationCanReplaceCanceledPendingOwner(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	base := m.createTabFn
	oldEntered, releaseOld, newEntered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var oldCanceled atomic.Int32
	m.createTabFn = func(ctx context.Context, id target.ID) (*tabEntry, error) {
		tab, err := base(ctx, id)
		if err != nil {
			return nil, err
		}
		if calls.Add(1) == 1 {
			tab.targetID = "retired-target"
			cancel := tab.cancel
			tab.cancel = func() { oldCanceled.Add(1); cancel() }
			close(oldEntered)
			<-releaseOld
		} else {
			tab.targetID = "replacement-target"
			close(newEntered)
		}
		return tab, nil
	}
	oldDone, newDone := make(chan error, 1), make(chan error, 1)
	go func() { oldDone <- m.createFirstTab(testSessionID) }()
	<-oldEntered
	m.CloseSession(testSessionID)
	go func() { newDone <- m.createFirstTab(testSessionID) }()
	replacementStarted := false
	select {
	case <-newEntered:
		replacementStarted = true
	case <-time.After(250 * time.Millisecond):
	}
	close(releaseOld)
	oldErr, newErr := <-oldDone, <-newDone
	require.True(t, replacementStarted, "new creation must not wait on a retired pending owner")
	require.ErrorIs(t, oldErr, errBrowserSessionChanged)
	require.NoError(t, newErr)
	ctx, id, err := m.activeTargetSnapshot(testSessionID)
	require.NoError(t, err)
	require.Equal(t, target.ID("replacement-target"), id)
	require.NoError(t, ctx.Err(), "late old completion must not cancel the replacement")
	require.Equal(t, int32(1), oldCanceled.Load())
}

func TestLifecycleRetiresCommandBeforeItRegistersCreation(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
	require.NoError(t, err)
	m.CloseSession(testSessionID)
	err = m.createFirstTab(testSessionID)
	release()
	require.ErrorIs(t, err, errBrowserSessionChanged)
	m.mu.Lock()
	count := len(m.sessions)
	m.mu.Unlock()
	require.Equal(t, 0, count, "a command admitted before close must not begin creating afterward")
}

func TestLifecycleQueuedOldCommandIsRejectedAndFreshAdmissionRecovers(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		next, nextErr := m.acquireLiveTabCommand(context.Background(), testSessionID)
		if next != nil {
			next()
		}
		done <- nextErr
	}()
	// Establish that the second command joined this exact admission lifetime,
	// rather than racing a fresh request after close has already completed.
	require.Eventually(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.tabCommands[testSessionID].users == 2 }, time.Second, time.Millisecond)
	m.CloseSession(testSessionID)
	release()
	require.ErrorIs(t, <-done, errBrowserSessionChanged)
	_, err = m.Session(testSessionID)
	require.NoError(t, err, "a new request after retired users drain must be able to create normally")
}
