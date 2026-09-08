package browser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoolRegistrationPublicationRejectsRetirement(t *testing.T) {
	for _, phase := range []string{"during_teardown", "after_teardown", "caller_canceled"} {
		t.Run(phase, func(t *testing.T) {
			f := newPoolFixture(t)
			old := f.mustAcquire(t, "publication")
			m := newTestManagerWithFakeTabs(t)
			m.memoryPressureFn = func(int) (bool, bool) { return false, true }
			m.started = false
			m.AttachPool(f.pool, old.key)
			t.Cleanup(m.Shutdown)

			registered, resume := make(chan struct{}), make(chan struct{})
			f.pool.afterRegisterHook = func() { close(registered); <-resume }
			type result struct {
				ctx context.Context
				err error
			}
			done := make(chan result, 1)
			caller, cancelCaller := context.WithCancel(context.Background())
			defer cancelCaller()
			go func() {
				ctx, err := m.SessionContext(caller, testSessionID)
				done <- result{ctx, err}
			}()
			<-registered

			tearingDown, finishTeardown := make(chan struct{}), make(chan struct{})
			old.coord.mu.Lock()
			cancel := old.coord.rootCancel
			oldRoot := old.coord.rootCtx
			old.coord.rootCancel = func() {
				close(tearingDown)
				if phase != "after_teardown" {
					<-finishTeardown
				}
				cancel()
			}
			old.coord.mu.Unlock()
			closed := make(chan struct{})
			go func() { f.pool.Close(old.key); close(closed) }()
			<-tearingDown
			if phase == "after_teardown" {
				<-closed
			}
			if phase == "caller_canceled" {
				cancelCaller()
			}
			close(resume)
			got := <-done
			started := m.Started()
			m.mu.Lock()
			publishedRoot := m.allocCtx
			m.mu.Unlock()
			close(finishTeardown)
			<-closed

			wantErr := ErrBrowserRestarting
			if phase == "caller_canceled" {
				wantErr = context.Canceled
			}
			require.ErrorIs(t, got.err, wantErr, "retirement must reject unpublished registration while preserving caller cancellation")
			assert.Nil(t, got.ctx)
			assert.False(t, started, "late completion must not mark the retiring browser usable")
			assert.Nil(t, publishedRoot, "late completion must not install the old allocator")
			require.ErrorIs(t, oldRoot.Err(), context.Canceled)

			f.pool.afterRegisterHook = nil
			retry, err := m.SessionContext(context.Background(), testSessionID)
			require.NoError(t, err)
			require.NotNil(t, retry)
			assert.NoError(t, retry.Err())
			assert.True(t, m.Started())
			assert.NotSame(t, old.coord, m.Coordinator(), "retry must join a fresh browser")
			f.pool.mu.Lock()
			fresh := f.pool.instances[old.key.String()]
			f.pool.mu.Unlock()
			require.NotNil(t, fresh)
			assert.NotSame(t, old, fresh)
			assert.Same(t, fresh.coord, m.Coordinator())
			assert.Len(t, *f.launched, 2, "one original launch and one successful retry")
		})
	}
}

func TestPoolRegistrationPublicationSucceedsWithoutRetirement(t *testing.T) {
	f := newPoolFixture(t)
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	m.started = false
	m.AttachPool(f.pool, browserTestKey("uninterrupted"))
	t.Cleanup(m.Shutdown)
	ctx, err := m.SessionContext(context.Background(), testSessionID)
	require.NoError(t, err)
	require.NotNil(t, ctx)
	assert.NoError(t, ctx.Err())
	assert.True(t, m.Started())
	assert.Len(t, *f.launched, 1)
}
