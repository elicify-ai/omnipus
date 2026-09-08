package browser

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/stretchr/testify/require"
)

func TestPassivePopupAdoptionRequiresAnOwnedOpener(t *testing.T) {
	synctest.Test(t, testPassivePopupAdoptionRequiresAnOwnedOpener)
}

func testPassivePopupAdoptionRequiresAnOwnedOpener(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	t.Cleanup(m.Shutdown)
	_, err := m.Session("a")
	require.NoError(t, err)
	_, err = m.Session("b")
	require.NoError(t, err)
	m.mu.Lock()
	opener := m.sessions["a"].active().targetID
	m.mu.Unlock()
	published := make(chan string, 4)
	m.SetTabsChangedFunc(func(id string, _ []Tab, _ int) { published <- id })
	event := &target.EventTargetCreated{TargetInfo: &target.Info{TargetID: "popup", OpenerID: opener, Type: "page"}}
	m.handleTargetEvent("a", event)
	select {
	case id := <-published:
		require.Equal(t, "a", id)
	case <-time.After(time.Second):
		t.Fatal("owned popup was not adopted")
	}
	m.handleTargetEvent("b", event)
	synctest.Wait()
	select {
	case id := <-published:
		t.Fatalf("foreign opener caused adoption into %s", id)
	default:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	require.Equal(t, 2, len(m.sessions["a"].tabs))
	require.Equal(t, 1, len(m.sessions["b"].tabs))
}

// FR-016 applies to legacy callers too: shutdown retires their pending start.
func TestOpenTabLocalStartupShutdownRetiresLaunch(t *testing.T) {
	m, entered, release, disposed := blockedLocalStartupManager(t)
	done := make(chan error, 1)
	go func() { _, err := m.OpenTab(testSessionID); done <- err }()
	launch := <-entered
	m.Shutdown()
	canceled := false
	select {
	case <-launch.Done():
		canceled = true
	case <-time.After(200 * time.Millisecond):
	}
	release()
	err := <-done
	require.True(t, canceled, "shutdown must cancel a legacy tab-open startup")
	require.True(t, errors.Is(err, errBrowserSessionChanged), "retired startup error: %v", err)
	assertLocalStartupDrained(t, m, disposed)
	require.False(t, m.Started(), "startup must not resurrect the stopped manager")
	require.Equal(t, 0, m.TotalOpenTabs())
}
