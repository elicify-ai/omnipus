package browser

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestLiveTabCommandCanceledSwitchDoesNotMoveModel(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = m.SwitchTabContext(ctx, testSessionID, 0)
	require.ErrorIs(t, err, context.Canceled)
	_, active, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Equal(t, 1, active, "canceled request must not change active target")
}

func TestLiveTabCommandCancellationStopsFocus(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	m.tabFocusFn = func(ctx context.Context, _ ...chromedp.Action) error {
		select {
		case <-entered:
		default:
			close(entered)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := m.SwitchTabContext(ctx, testSessionID, 0); result <- err }()
	<-entered
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
		close(release)
	case <-time.After(500 * time.Millisecond):
		close(release)
		<-result
		t.Fatal("caller cancellation did not stop foreground operation")
	}
}

func TestLiveTabCommandCanceledLastCloseKeepsOldTarget(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	old, err := m.Session(testSessionID)
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	m.createTabFn = func(ctx context.Context, _ target.ID) (*tabEntry, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return nil, errors.New("test release")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, _, closeErr := m.CloseTabContext(ctx, testSessionID, 0); result <- closeErr }()
	<-entered
	oldErr := old.Err()
	cancel()
	select {
	case resultErr := <-result:
		require.ErrorIs(t, resultErr, context.Canceled)
		close(release)
	case <-time.After(500 * time.Millisecond):
		close(release)
		<-result
		t.Error("caller cancellation did not stop replacement creation")
	}
	require.NoError(t, oldErr, "last tab must survive until replacement commits")
	require.NoError(t, old.Err(), "canceled replacement must leave original target alive")
	tabs, active, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	require.Equal(t, 0, active)
}

func TestLiveTabCommandNeverRecreatesMissingAttachment(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	creates := 0
	m.createTabFn = func(context.Context, target.ID) (*tabEntry, error) {
		creates++
		return nil, errors.New("unexpected browser creation")
	}
	_, err := m.OpenTabContext(context.Background(), "missing-attachment")
	require.Error(t, err)
	require.Zero(t, creates, "stale attachment must not bootstrap another session")
}

func TestLiveTabCommandSuccessfulNewTargetOutlivesCaller(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	var created context.Context
	m.createTabFn = func(parent context.Context, _ target.ID) (*tabEntry, error) {
		ctx, cancel := chromedp.NewContext(parent)
		created = ctx //nolint:fatcontext // Retains an original context for ownership or observation; it does not derive a context from an earlier iteration.
		return &tabEntry{ctx: ctx, cancel: cancel, targetID: "successful-context-target"}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = m.OpenTabContext(ctx, testSessionID)
	require.NoError(t, err)
	cancel()
	select {
	case <-created.Done():
		t.Fatal("command completion left new target tied to caller cancellation")
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, created.Err(), "command completion must detach caller lifetime from new target")
	m.mu.Lock()
	active := m.sessions[testSessionID].tabs[m.sessions[testSessionID].activeIdx].ctx
	m.mu.Unlock()
	require.True(t, active == created, "successful command must commit the target that was created")
}

func TestLiveTabCommandSerializesConcurrentSwitches(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	m.tabFocusFn = func(ctx context.Context, _ ...chromedp.Action) error {
		once.Do(func() { close(entered) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}
	first := make(chan error, 1)
	go func() { _, firstErr := m.SwitchTabContext(context.Background(), testSessionID, 0); first <- firstErr }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	second := make(chan error, 1)
	go func() { _, secondErr := m.SwitchTabContext(ctx, testSessionID, 1); second <- secondErr }()
	received := false
	select {
	case resultErr := <-second:
		received = true
		require.ErrorIs(t, resultErr, context.DeadlineExceeded)
	case <-time.After(500 * time.Millisecond):
		t.Error("queued tab command did not honor its deadline")
	}
	_, active, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	close(release)
	require.NoError(t, <-first)
	if !received {
		<-second
	}
	require.Equal(t, 0, active, "second tab mutation must wait until first callback sequence completes")
}

func TestLiveTabCommandFocusFailureIsReportedAfterCommittedSwitch(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	t.Cleanup(m.Shutdown)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	failure := errors.New("Chrome rejected foreground request")
	m.tabFocusFn = func(context.Context, ...chromedp.Action) error { return failure }
	_, err = m.SwitchTabContext(context.Background(), testSessionID, 0)
	require.ErrorIs(t, err, failure, "foreground failure must not be reported as command success")
	_, active, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Equal(t, 0, active, "committed model changes must not be replayed or silently rolled back")
}
