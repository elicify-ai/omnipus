package webrtc

import (
	"context"
	"sync"
)

// mediaWriterLock preserves exclusive packet/ownership ordering while allowing
// a canceled installation to stop waiting without spawning a lock waiter.
// Its zero value is ready to use. Never acquire it while holding Session.mu.
type mediaWriterLock struct {
	once  sync.Once
	token chan struct{}
}

func (m *mediaWriterLock) LockContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.once.Do(func() { m.token = make(chan struct{}, 1) })
	select {
	case m.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-m.token
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *mediaWriterLock) Lock() { _ = m.LockContext(context.Background()) }

func (m *mediaWriterLock) Unlock() {
	select {
	case <-m.token:
	default:
		panic("webrtc: unlock of unowned media writer")
	}
}
