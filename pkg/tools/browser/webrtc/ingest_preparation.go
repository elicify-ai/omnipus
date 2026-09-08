package webrtc

import (
	"context"
	"errors"
	"sync"
)

const ingestPreparationLimit = 4

var errIngestPreparationBusy = errors.New("webrtc: ingest candidate preparation busy")

// Native SDP preparation cannot be interrupted. Admission remains occupied
// until negotiation and any private candidate cleanup finish. Installed media
// has its own lifetime and is never owned by this resource budget.
type ingestPreparationPool struct {
	mu     sync.Mutex
	active int
}

func (p *ingestPreparationPool) run(ctx context.Context, work func() (string, error)) (string, error) {
	p.mu.Lock()
	if err := context.Cause(ctx); err != nil {
		p.mu.Unlock()
		return "", err
	}
	if p.active >= ingestPreparationLimit {
		p.mu.Unlock()
		return "", errIngestPreparationBusy
	}
	p.active++
	p.mu.Unlock()
	type result struct {
		answer string
		err    error
	}
	completed := make(chan result, 1)
	go func() {
		defer func() {
			p.mu.Lock()
			p.active--
			p.mu.Unlock()
		}()
		if err := context.Cause(ctx); err != nil {
			completed <- result{err: err}
			return
		}
		answer, err := work()
		completed <- result{answer, err}
	}()
	select {
	case result := <-completed:
		if err := context.Cause(ctx); err != nil {
			return "", err
		}
		return result.answer, result.err
	case <-ctx.Done():
		return "", context.Cause(ctx)
	}
}
