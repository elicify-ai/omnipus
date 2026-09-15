package webrtc

import (
	"context"
	"errors"
	"sync"
)

const viewerPreparationLimit = 4

var errViewerPreparationBusy = errors.New("webrtc: viewer candidate preparation busy")

// A native interface lookup cannot be forcibly interrupted. A canceled caller
// leaves its slot occupied until the real worker and retired cleanup finish.
// The fixed cap allows replacements without accumulating unlimited workers.
type viewerPreparationPool struct {
	mu     sync.Mutex
	active int
}

// run transfers a completed result to the caller, or runs retired if the caller
// canceled before accepting it. Native errors in accepted results remain the
// caller's responsibility. Neither work nor cleanup runs under the pool lock.
func (p *viewerPreparationPool) run(ctx context.Context, work func() error, retired func()) error {
	p.mu.Lock()
	if err := context.Cause(ctx); err != nil {
		p.mu.Unlock()
		return err
	}
	if p.active >= viewerPreparationLimit {
		p.mu.Unlock()
		return errViewerPreparationBusy
	}
	p.active++
	p.mu.Unlock()

	result := make(chan error)
	accepted := make(chan bool, 1)
	go func() {
		defer func() {
			p.mu.Lock()
			p.active--
			p.mu.Unlock()
		}()
		cleanup := func() {
			if retired != nil {
				retired()
			}
		}
		if ctx.Err() != nil {
			cleanup()
			return
		}
		err := work()
		select {
		case result <- err:
			if !<-accepted {
				cleanup()
			}
		case <-ctx.Done():
			cleanup()
		}
	}()

	select {
	case err := <-result:
		canceled := context.Cause(ctx)
		accepted <- canceled == nil
		if canceled != nil {
			return canceled
		}
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
