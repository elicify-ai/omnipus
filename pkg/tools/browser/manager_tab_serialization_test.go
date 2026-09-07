package browser

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/stretchr/testify/require"
)

func TestLegacyTabCommandsWaitForSharedGate(t *testing.T) {
	cases := []struct {
		name string
		call func(*BrowserManager) error
	}{
		{"session", func(m *BrowserManager) error { _, err := m.Session(testSessionID); return err }},
		{"switch", func(m *BrowserManager) error { _, err := m.SwitchTab(testSessionID, 0); return err }},
		{"open", func(m *BrowserManager) error { _, err := m.OpenTab(testSessionID); return err }},
		{"close", func(m *BrowserManager) error { _, _, err := m.CloseTab(testSessionID, 0); return err }},
		{"adopt", func(m *BrowserManager) error { _, err := m.adoptTarget(testSessionID, "popup-target"); return err }},
		{"reconcile", func(m *BrowserManager) error { _, err := m.ReconcileTabs(testSessionID); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManagerWithFakeTabs(t)
			t.Cleanup(m.Shutdown)
			_, err := m.Session(testSessionID)
			require.NoError(t, err)
			_, err = m.OpenTab(testSessionID)
			require.NoError(t, err)
			m.listTargets = func(context.Context) ([]*target.Info, error) { return nil, nil }
			release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() { done <- tc.call(m) }()
			var early bool
			select {
			case <-done:
				early = true
			case <-time.After(50 * time.Millisecond):
			}
			release()
			if !early {
				select {
				case err = <-done:
					require.NoError(t, err)
				case <-time.After(time.Second):
					t.Fatal("legacy operation did not resume after gate release")
				}
			}
			require.False(t, early, "legacy operation completed while another target command owned the gate")
		})
	}
}

func TestTabMetadataWaitsForGateAndPublishesNewestSnapshot(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	m.mu.Lock()
	id := m.sessions[testSessionID].active().targetID
	m.mu.Unlock()
	published := make(chan []Tab, 100)
	m.SetTabsChangedFunc(func(_ string, tabs []Tab, _ int) { published <- tabs })
	release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
	require.NoError(t, err)
	for _, title := range []string{"old", "middle", "newest"} {
		m.handleTargetEvent(testSessionID, &target.EventTargetInfoChanged{TargetInfo: &target.Info{TargetID: id, Type: "page", Title: title, URL: "https://example.test/"}})
	}
	var early bool
	select {
	case <-published:
		early = true
	case <-time.After(50 * time.Millisecond):
	}
	release()
	require.False(t, early, "target listener published while target mutation gate was occupied")
	select {
	case tabs := <-published:
		require.Len(t, tabs, 1)
		require.Equal(t, "newest", tabs[0].Title)
	case <-time.After(time.Second):
		t.Fatal("latest metadata was never published")
	}
	select {
	case tabs := <-published:
		t.Fatalf("coalesced metadata replayed another snapshot: %+v", tabs)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTabNotificationNeverCreatesMissingOrDeadTarget(t *testing.T) {
	for _, dead := range []bool{false, true} {
		name := "missing"
		if dead {
			name = "dead"
		}
		t.Run(name, func(t *testing.T) {
			m := newTestManagerWithFakeTabs(t)
			t.Cleanup(m.Shutdown)
			if dead {
				_, err := m.Session(testSessionID)
				require.NoError(t, err)
				m.mu.Lock()
				cancel := m.sessions[testSessionID].active().cancel
				m.mu.Unlock()
				cancel()
			}
			var creates atomic.Int32
			m.createTabFn = func(context.Context, target.ID) (*tabEntry, error) {
				creates.Add(1)
				return nil, errors.New("unexpected recovery from notification")
			}
			lv := &LiveView{mgr: m, sessionID: testSessionID}
			lv.onTabsChanged(nil, 0)
			require.Equal(t, int32(0), creates.Load(), "read-only notification must not launch or recreate a target")
		})
	}
}

func TestLegacyTabCommandHoldsGateThroughNotification(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	entered, unblock := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	m.SetTabsChangedFunc(func(_ string, _ []Tab, _ int) {
		if calls.Add(1) == 1 {
			close(entered)
			<-unblock
		}
	})
	legacyDone := make(chan error, 1)
	go func() { _, err := m.SwitchTab(testSessionID, 0); legacyDone <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, switchErr := m.SwitchTabContext(ctx, testSessionID, 1)
	_, active, listErr := m.ListTabs(testSessionID)
	close(unblock)
	require.NoError(t, <-legacyDone)
	require.NoError(t, listErr)
	require.ErrorIs(t, switchErr, context.DeadlineExceeded, "new UI mutation must expire behind an unfinished legacy observer")
	require.Equal(t, 0, active, "waiting canceled command must not move the active target")
	require.Equal(t, int32(1), calls.Load(), "canceled waiting command must not publish a second transition")
}

func TestActiveTargetSnapshotPreservesIdentityAndRejectsDeadTarget(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	wantCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	m.mu.Lock()
	wantID := m.sessions[testSessionID].active().targetID
	cancel := m.sessions[testSessionID].active().cancel
	m.mu.Unlock()
	gotCtx, gotID, err := m.activeTargetSnapshot(testSessionID)
	require.NoError(t, err)
	require.Same(t, wantCtx, gotCtx)
	require.Equal(t, wantID, gotID)
	cancel()
	gotCtx, gotID, err = m.activeTargetSnapshot(testSessionID)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, gotCtx)
	require.Equal(t, target.ID(""), gotID)
}

func TestActiveTargetSnapshotMissingDoesNotCreate(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	ctx, id, err := m.activeTargetSnapshot("missing")
	require.EqualError(t, err, "browser: no active target for session \"missing\"")
	require.Nil(t, ctx)
	require.Equal(t, target.ID(""), id)
	m.mu.Lock()
	count := len(m.sessions)
	m.mu.Unlock()
	require.Equal(t, 0, count)
}

func TestLegacyTabCommandAdmissionUsesConfiguredTimeout(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	m.cfg.PageTimeout = 25 * time.Millisecond
	release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := m.SwitchTab(testSessionID, 0); done <- err }()
	var result error
	select {
	case result = <-done:
	case <-time.After(250 * time.Millisecond):
		release()
		<-done
		t.Fatal("legacy gate admission ignored configured timeout")
	}
	release()
	require.ErrorIs(t, result, context.DeadlineExceeded)
}
