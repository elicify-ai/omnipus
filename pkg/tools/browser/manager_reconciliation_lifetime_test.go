package browser

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/chromedp/cdproto/target"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconcileSnapshotRetainsOriginalSession(t *testing.T) {
	for _, ending := range []string{"browser_context", "removed_before_cleanup", "opener_only", "retire_during_attach"} {
		t.Run(ending, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := newTestManagerWithFakeTabs(t)
				m.memoryPressureFn = func(int) (bool, bool) { return false, true }
				t.Cleanup(m.Shutdown)
				_, err := m.Session(testSessionID)
				require.NoError(t, err)
				m.mu.Lock()
				owner := m.sessions[testSessionID]
				opener := owner.active()
				opener.cancel = sync.OnceFunc(opener.cancel)
				m.mu.Unlock()
				base := m.createTabFn
				var attaches atomic.Int32
				m.createTabFn = func(ctx context.Context, id target.ID) (*tabEntry, error) {
					if id == "listed-popup" {
						attaches.Add(1)
						if ending == "retire_during_attach" {
							m.CloseSession(testSessionID)
						}
					}
					return base(ctx, id)
				}
				allowCleanup := make(chan struct{})
				var releaseOnce sync.Once
				releaseCleanup := func() { releaseOnce.Do(func() { close(allowCleanup) }) }
				defer releaseCleanup()
				m.listTargets = func(context.Context) ([]*target.Info, error) {
					switch ending {
					case "browser_context":
						owner.browserCancel()
					case "removed_before_cleanup":
						cancel := owner.browserCancel
						owner.browserCancel = func() { <-allowCleanup; cancel() }
						m.CloseSession(testSessionID)
						require.NoError(t, owner.browserCtx.Err(), "cleanup still held after bookkeeping retirement")
					case "opener_only":
						opener.cancel()
					}
					return []*target.Info{{TargetID: "listed-popup", OpenerID: opener.targetID, Type: "page"}}, nil
				}
				out, err := m.ReconcileTabs(testSessionID)
				releaseCleanup()
				synctest.Wait()
				if ending == "opener_only" {
					require.NoError(t, err)
					require.Equal(t, int32(1), attaches.Load())
					require.True(t, out.Adopted)
					require.NotNil(t, out.NewActive)
					require.Equal(t, 1, out.NewActive.Index)
					require.False(t, out.Unadopted)
				} else {
					assert.ErrorIs(t, err, errBrowserSessionChanged)
					wantAttaches := int32(0)
					if ending == "retire_during_attach" {
						wantAttaches = 1 // Native attach started before retirement.
					}
					assert.Equal(t, wantAttaches, attaches.Load(), "retired list cannot authorize native attachment")
					assert.Equal(t, ReconcileOutcome{}, out)
				}
			})
		})
	}
}

func TestReconcileSnapshotCannotContinueAfterSessionEndsBetweenAdoptions(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(fmt.Sprintf("targets_%d", count), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := newTestManagerWithFakeTabs(t)
				m.memoryPressureFn = func(int) (bool, bool) { return false, true }
				t.Cleanup(m.Shutdown)
				_, err := m.Session(testSessionID)
				require.NoError(t, err)
				m.mu.Lock()
				owner := m.sessions[testSessionID]
				opener := owner.active().targetID
				m.mu.Unlock()
				base := m.createTabFn
				var attaches atomic.Int32
				m.createTabFn = func(ctx context.Context, id target.ID) (*tabEntry, error) {
					attaches.Add(1)
					return base(ctx, id)
				}
				m.listTargets = func(context.Context) ([]*target.Info, error) {
					infos := []*target.Info{
						{TargetID: "first-popup", OpenerID: opener, Type: "page"},
						{TargetID: "second-popup", OpenerID: opener, Type: "page"},
					}
					return infos[:count], nil
				}
				m.SetTabsChangedFunc(func(string, []Tab, int) { owner.browserCancel() })
				out, err := m.ReconcileTabs(testSessionID)
				assert.ErrorIs(t, err, errBrowserSessionChanged)
				assert.Equal(t, int32(1), attaches.Load(), "later items retain the same original browser lifetime")
				assert.Equal(t, ReconcileOutcome{}, out, "partial success from a retired session is not current state")
				m.mu.Lock()
				second := owner.indexOfTarget("second-popup")
				m.mu.Unlock()
				require.Equal(t, -1, second)
			})
		})
	}
}
