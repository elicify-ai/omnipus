package browser

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/stretchr/testify/require"
)

func TestPassivePopupRetryRetainsOriginalOwner(t *testing.T) {
	for _, change := range []string{"unchanged", "replace_session", "replace_before_cleanup", "close_opener", "session_context_ends"} {
		t.Run(change, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := newTestManagerWithFakeTabs(t)
				m.memoryPressureFn = func(int) (bool, bool) { return false, true }
				t.Cleanup(m.Shutdown)
				_, err := m.Session(testSessionID)
				require.NoError(t, err)
				m.mu.Lock()
				opener := m.sessions[testSessionID].active().targetID
				m.mu.Unlock()
				if change == "close_opener" {
					_, err = m.OpenTab(testSessionID)
					require.NoError(t, err)
				}
				base := m.createTabFn
				var attempts atomic.Int32
				m.createTabFn = func(ctx context.Context, id target.ID) (*tabEntry, error) {
					if id == "retained-popup" && attempts.Add(1) == 1 {
						return nil, context.DeadlineExceeded
					}
					return base(ctx, id)
				}
				m.adoptRetryBackoff = []time.Duration{time.Hour}
				m.handleTargetEvent(testSessionID, &target.EventTargetCreated{TargetInfo: &target.Info{
					TargetID: "retained-popup", OpenerID: opener, Type: "page",
				}})
				synctest.Wait() // Native failure has completed; retry is durably waiting.
				require.Equal(t, int32(1), attempts.Load())
				releaseCleanup := func() {}
				switch change {
				case "replace_before_cleanup":
					// Native cleanup may lag bookkeeping retirement. Keep its context
					// alive to prove exact session identity independently of cancellation.
					allow := make(chan struct{})
					var once sync.Once
					releaseCleanup = func() { once.Do(func() { close(allow) }) }
					defer releaseCleanup()
					m.mu.Lock()
					owner := m.sessions[testSessionID]
					cancel := owner.browserCancel
					owner.browserCancel = func() { <-allow; cancel() }
					m.mu.Unlock()
					m.CloseSession(testSessionID)
					require.NoError(t, owner.browserCtx.Err(), "native cleanup remains held")
					_, err = m.Session(testSessionID)
					require.NoError(t, err)
				case "replace_session":
					m.CloseSession(testSessionID)
					_, err = m.Session(testSessionID)
					require.NoError(t, err)
				case "session_context_ends":
					m.mu.Lock()
					cancel := m.sessions[testSessionID].browserCancel
					m.mu.Unlock()
					cancel()
				case "close_opener":
					_, _, err = m.CloseTab(testSessionID, 0)
					require.NoError(t, err)
				}
				time.Sleep(time.Hour)
				synctest.Wait()
				releaseCleanup()
				synctest.Wait()
				m.mu.Lock()
				se := m.sessions[testSessionID]
				count, popup := len(se.tabs), se.indexOfTarget("retained-popup")
				m.mu.Unlock()
				if change == "unchanged" || change == "close_opener" {
					require.Equal(t, int32(2), attempts.Load(), "one failed attach followed by one successful retry")
					require.Equal(t, 2, count)
					require.Equal(t, 1, popup)
				} else {
					require.Equal(t, int32(1), attempts.Load(), "retired owner cannot authorize another native attach")
					require.Equal(t, 1, count, "new or surviving set retains only its own tab")
					require.Equal(t, -1, popup, "old popup must not transfer to another owner")
				}
			})
		})
	}
}

func TestPassivePopupLateAttachDisposesRetiredCandidate(t *testing.T) {
	for _, retirement := range []string{"session", "native_opener", "session_context"} {
		t.Run(retirement, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := newTestManagerWithFakeTabs(t)
				m.memoryPressureFn = func(int) (bool, bool) { return false, true }
				t.Cleanup(m.Shutdown)
				_, err := m.Session(testSessionID)
				require.NoError(t, err)
				m.mu.Lock()
				owner := m.sessions[testSessionID]
				openerTab := owner.active()
				opener := openerTab.targetID
				// Model native closure once; fake chromedp teardown is not reentrant.
				openerTab.cancel = sync.OnceFunc(openerTab.cancel)
				m.mu.Unlock()
				entered, release := make(chan struct{}), make(chan struct{})
				base := m.createTabFn
				var attempts, disposed atomic.Int32
				m.createTabFn = func(ctx context.Context, id target.ID) (*tabEntry, error) {
					tab, err := base(ctx, id)
					if err == nil && id == "late-popup" {
						attempts.Add(1)
						cancel := tab.cancel
						tab.cancel = func() { disposed.Add(1); cancel() }
						close(entered)
						<-release
					}
					return tab, err
				}
				m.adoptRetryBackoff = []time.Duration{time.Hour}
				m.handleTargetEvent(testSessionID, &target.EventTargetCreated{TargetInfo: &target.Info{
					TargetID: "late-popup", OpenerID: opener, Type: "page",
				}})
				<-entered
				switch retirement {
				case "session":
					m.CloseSession(testSessionID)
				case "session_context":
					owner.browserCancel()
				default:
					// Chrome can close an opener while another target is attaching. Its
					// context ends before manager bookkeeping removes the old tab.
					openerTab.cancel()
				}
				close(release)
				synctest.Wait()
				require.Equal(t, int32(1), attempts.Load())
				switch retirement {
				case "session":
					require.Equal(t, int32(1), disposed.Load(), "late native target must be discarded exactly once")
					require.False(t, m.sessionExists(testSessionID), "late popup must not recreate closed session")
				case "session_context":
					require.Equal(t, int32(1), disposed.Load(), "candidate cannot survive the original browser lifetime")
					m.mu.Lock()
					popup := m.sessions[testSessionID].indexOfTarget("late-popup")
					m.mu.Unlock()
					require.Equal(t, -1, popup)
				default:
					m.mu.Lock()
					popup := m.sessions[testSessionID].indexOfTarget("late-popup")
					m.mu.Unlock()
					require.Equal(t, 1, popup, "historically proven ownership survives opener closure")
					require.Equal(t, int32(0), disposed.Load(), "a live owned popup must survive its opener")
				}
			})
		})
	}
}

func TestPassivePopupRetryWaitEndsWithOriginalSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newTestManagerWithFakeTabs(t)
		m.memoryPressureFn = func(int) (bool, bool) { return false, true }
		t.Cleanup(m.Shutdown)
		_, err := m.Session(testSessionID)
		require.NoError(t, err)
		var attempts atomic.Int32
		m.createTabFn = func(context.Context, target.ID) (*tabEntry, error) {
			attempts.Add(1)
			return nil, context.DeadlineExceeded
		}
		m.adoptRetryBackoff = []time.Duration{time.Hour}
		done := make(chan struct{})
		go func() { m.adoptTargetWithRetry(testSessionID, "retry-wait"); close(done) }()
		synctest.Wait()
		require.Equal(t, int32(1), attempts.Load())
		m.CloseSession(testSessionID)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("retired popup retry must finish without waiting out its backoff")
		}
		require.Equal(t, int32(1), attempts.Load())
	})
}
