package browser

import (
	"context"
	"fmt"
	"sync"
)

// startupCohort owns one launch while at least one original request remains
// live. Its short mutex may be acquired under a pool/coordinator mutex, never
// the reverse. Browser lifetime is independent of this temporary context.
type startupCohort struct {
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	waiters   map[uint64]context.Context
	next      uint64
	joined    bool
	abandoned bool
	completed bool
	err       error
}

func newStartupCohort() *startupCohort {
	ctx, cancel := context.WithCancel(context.Background())
	return &startupCohort{ctx: ctx, cancel: cancel, done: make(chan struct{}), waiters: make(map[uint64]context.Context)}
}

func (f *startupCohort) hasLiveLocked() bool {
	for _, ctx := range f.waiters {
		if ctx.Err() == nil {
			return true
		}
	}
	return false
}

func (f *startupCohort) join(ctx context.Context) (func(), bool) {
	f.mu.Lock()
	if f.completed || f.abandoned || f.ctx.Err() != nil || ctx.Err() != nil {
		f.mu.Unlock()
		return nil, false
	}
	if f.joined && !f.hasLiveLocked() {
		f.abandoned = true
		f.mu.Unlock()
		f.cancel()
		return nil, false
	}
	f.next++
	id := f.next
	f.waiters[id] = ctx //nolint:fatcontext // Retains an original context for ownership or observation; it does not derive a context from an earlier iteration.
	f.joined = true
	f.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { f.remove(id) })
	return func() { stop(); f.remove(id) }, true
}

func (f *startupCohort) remove(id uint64) {
	f.mu.Lock()
	delete(f.waiters, id)
	abandon := !f.completed && !f.hasLiveLocked()
	if abandon {
		f.abandoned = true
	}
	f.mu.Unlock()
	if abandon {
		f.cancel()
	}
}

// live reads the original caller contexts at the publication boundary. It
// must not rely on AfterFunc having already delivered their cancellation.
func (f *startupCohort) live() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.completed && !f.abandoned && f.ctx.Err() == nil && f.hasLiveLocked()
}

func (f *startupCohort) finish(err error) {
	f.mu.Lock()
	if f.completed {
		f.mu.Unlock()
		return
	}
	f.err = err
	f.completed = true
	close(f.done)
	f.mu.Unlock()
	f.cancel()
}

func (f *startupCohort) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return f.err
	}
}

func invokeStartup(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("browser: startup failed unexpectedly: %v", recovered)
		}
	}()
	return fn()
}
